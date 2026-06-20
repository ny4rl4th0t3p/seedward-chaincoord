package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

// nonceTTL is how long a consumed nonce is remembered. It MUST be >= the signed-payload
// acceptance window (services.signedPayloadMaxAge, 5m): a nonce has to be
// remembered for at least as long as a request bearing it can still pass the
// signed-timestamp freshness check. 10m leaves a comfortable margin.
const nonceTTL = 10 * time.Minute

// NonceStore implements ports.NonceStore for SQLite.
// A nonce is accepted once within its TTL window; subsequent uses return ErrConflict.
type NonceStore struct {
	db *sql.DB
}

func NewNonceStore(db *sql.DB) *NonceStore {
	return &NonceStore{db: db}
}

func (s *NonceStore) Consume(ctx context.Context, operatorAddr, nonce string) error {
	expiresAt := nowUTC().Add(nonceTTL)

	// INSERT fails if the (operator_address, nonce) pair already exists → replay.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO nonces (operator_address, nonce, expires_at) VALUES (?,?,?)`,
		operatorAddr, nonce, timeToStr(expiresAt))
	if err != nil {
		// SQLite UNIQUE constraint violation — nonce was already used.
		return fmt.Errorf("nonce replay detected: %w", ports.ErrConflict)
	}

	// Opportunistic cleanup of expired nonces (best-effort, non-fatal).
	_, _ = s.db.ExecContext(ctx,
		`DELETE FROM nonces WHERE expires_at < ?`, timeToStr(nowUTC()))

	return nil
}
