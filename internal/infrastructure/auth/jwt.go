// Package auth provides JWT-based session management for coordd.
package auth

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

const jwtTTL = time.Hour

// JWTSessionStore issues and validates EdDSA (Ed25519) JWTs, using the
// operator_revocations table for bulk revocation. The private key is a 32-byte
// Ed25519 seed, base64-encoded (the value of Config.JWTPrivKeyB64).
type JWTSessionStore struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
	db   *sql.DB
}

// NewJWTSessionStore constructs a JWTSessionStore from a base64-encoded 32-byte
// Ed25519 seed and a database handle (for revocation checks).
func NewJWTSessionStore(privKeyB64 string, db *sql.DB) (*JWTSessionStore, error) {
	seed, err := base64.StdEncoding.DecodeString(privKeyB64)
	if err != nil {
		return nil, fmt.Errorf("jwt store: decode private key: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("jwt store: private key must be %d bytes, got %d", ed25519.SeedSize, len(seed))
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return &JWTSessionStore{
		priv: priv,
		pub:  priv.Public().(ed25519.PublicKey),
		db:   db,
	}, nil
}

// Issue creates a signed JWT for the given operator address.
func (s *JWTSessionStore) Issue(_ context.Context, operatorAddr string) (string, error) {
	now := time.Now().UTC()
	claims := jwt.RegisteredClaims{
		Subject:   operatorAddr,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(jwtTTL)),
		ID:        uuid.New().String(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := tok.SignedString(s.priv)
	if err != nil {
		return "", fmt.Errorf("jwt store: sign token: %w", err)
	}
	return signed, nil
}

// accountFenceKey maps a bech32 account address (a session subject, or a caller-supplied address)
// to the HRP-independent account key used for revocation fences,
// so a revoke-all covers every prefix the account authenticated under. The input was
// minted as / validated as an account address, so decoding succeeds; on the off
// chance it doesn't, fall back to the raw string rather than failing.
func accountFenceKey(addr string) string {
	if id, err := launch.NewAccountID(addr); err == nil {
		return id.Hex()
	}
	return addr
}

// Validate verifies the token signature and expiry, then checks the
// operator_revocations table to reject tokens issued before a bulk-revocation fence.
// The fence is keyed on the HRP-independent account, so a revoke-all covers every prefix.
func (s *JWTSessionStore) Validate(ctx context.Context, raw string) (string, error) {
	operatorAddr, issuedAt, _, err := s.parseClaims(raw)
	if err != nil {
		return "", ports.ErrUnauthorized
	}

	// One DB read: check the account's revocation fence.
	var revokeBeforeStr string
	queryErr := s.db.QueryRowContext(ctx,
		`SELECT revoke_before FROM operator_revocations WHERE operator_address = ?`,
		accountFenceKey(operatorAddr),
	).Scan(&revokeBeforeStr)
	if queryErr != nil && !errors.Is(queryErr, sql.ErrNoRows) {
		return "", fmt.Errorf("jwt store: revocation check: %w", queryErr)
	}
	if queryErr == nil {
		revokeBefore, err := time.Parse(time.RFC3339, revokeBeforeStr)
		if err != nil {
			return "", fmt.Errorf("jwt store: parse revoke_before: %w", err)
		}
		// Reject any token whose issuedAt is at or before the fence.
		// Both timestamps are second-precision, so "equal" means the token
		// was issued in the same second as the revocation call — treat it as
		// revoked rather than risk accepting a pre-revocation token.
		if !issuedAt.After(revokeBefore) {
			return "", ports.ErrUnauthorized
		}
	}

	return operatorAddr, nil
}

// Revoke is a no-op for JWT sessions: individual token revocation is not supported
// without a denylist. Callers should use RevokeAllForOperator instead, or simply
// allow the short TTL to expire the token naturally.
func (*JWTSessionStore) Revoke(_ context.Context, _ string) error {
	return nil
}

// RevokeAllForOperator upserts a revocation fence in operator_revocations so that
// all tokens issued before now for this account are rejected on the next Validate.
// The fence is keyed on the HRP-independent account, so it covers sessions the
// account holds under any prefix.
func (s *JWTSessionStore) RevokeAllForOperator(ctx context.Context, operatorAddr string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO operator_revocations (operator_address, revoke_before)
		 VALUES (?, ?)
		 ON CONFLICT(operator_address) DO UPDATE SET revoke_before = excluded.revoke_before`,
		accountFenceKey(operatorAddr), now,
	)
	if err != nil {
		return fmt.Errorf("jwt store: upsert revocation fence: %w", err)
	}
	return nil
}

// ParseClaims extracts the operator address and expiry from a token without a DB
// lookup, so it does NOT detect server-side (fence) revocation. Callers that must
// reject revoked tokens (e.g. the session-info endpoint) Validate first and use
// ParseClaims only to read the expiry.
func (s *JWTSessionStore) ParseClaims(raw string) (string, time.Time, error) {
	operatorAddr, _, expiresAt, err := s.parseClaims(raw)
	if err != nil {
		return "", time.Time{}, ports.ErrUnauthorized
	}
	return operatorAddr, expiresAt, nil
}

// parseClaims verifies the signature/expiry and returns (sub, iat, exp, err).
func (s *JWTSessionStore) parseClaims(raw string) (operatorAddr string, issuedAt, expiresAt time.Time, err error) {
	tok, err := jwt.ParseWithClaims(raw, &jwt.RegisteredClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.pub, nil
	})
	if err != nil || !tok.Valid {
		return "", time.Time{}, time.Time{}, fmt.Errorf("jwt: invalid token")
	}
	c, ok := tok.Claims.(*jwt.RegisteredClaims)
	if !ok || c.Subject == "" {
		return "", time.Time{}, time.Time{}, fmt.Errorf("jwt: missing subject claim")
	}
	var iat, exp time.Time
	if c.IssuedAt != nil {
		iat = c.IssuedAt.Time
	}
	if c.ExpiresAt != nil {
		exp = c.ExpiresAt.Time
	}
	return c.Subject, iat, exp, nil
}
