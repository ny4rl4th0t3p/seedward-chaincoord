package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

const (
	challengeRateWindow  = 5 * time.Minute
	challengeRateMaxReqs = 5
)

// ChallengeRateLimiterStore implements ports.ChallengeRateLimiter for SQLite.
type ChallengeRateLimiterStore struct {
	db *sql.DB
}

func NewChallengeRateLimiterStore(db *sql.DB) *ChallengeRateLimiterStore {
	return &ChallengeRateLimiterStore{db: db}
}

// Allow checks whether operatorAddr is within the rate limit and records the attempt.
// Returns ErrTooManyRequests if the per-operator limit has been exceeded.
func (s *ChallengeRateLimiterStore) Allow(ctx context.Context, operatorAddr string) error {
	now := nowUTC()
	windowStart := now.Add(-challengeRateWindow)

	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM challenge_rate_limits
		 WHERE operator_address = ? AND requested_at >= ?`,
		operatorAddr, timeToStr(windowStart)).Scan(&count)
	if err != nil {
		return fmt.Errorf("challenge rate limit check: %w", err)
	}
	if count >= challengeRateMaxReqs {
		return fmt.Errorf("challenge rate limit exceeded: %w", ports.ErrTooManyRequests)
	}

	_, _ = s.db.ExecContext(ctx,
		`INSERT INTO challenge_rate_limits (operator_address, requested_at) VALUES (?, ?)`,
		operatorAddr, timeToStr(now))

	// Opportunistic cleanup of entries outside the rate window.
	cutoff := now.Add(-challengeRateWindow)
	_, _ = s.db.ExecContext(ctx,
		`DELETE FROM challenge_rate_limits WHERE requested_at < ?`, timeToStr(cutoff))

	return nil
}
