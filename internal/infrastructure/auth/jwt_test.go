package auth_test

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/auth"
)

// testKey is a valid base64-encoded 32-byte Ed25519 seed.
const testKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func openMemDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err, "open db")
	t.Cleanup(func() { db.Close() })
	_, err = db.ExecContext(t.Context(), `CREATE TABLE operator_revocations (
		operator_address TEXT NOT NULL PRIMARY KEY,
		revoke_before    TEXT NOT NULL
	)`)
	require.NoError(t, err, "create table")
	return db
}

func newStore(t *testing.T) *auth.JWTSessionStore {
	t.Helper()
	db := openMemDB(t)
	store, err := auth.NewJWTSessionStore(testKey, db)
	require.NoError(t, err, "NewJWTSessionStore")
	return store
}

func TestNewJWTSessionStore_InvalidKey(t *testing.T) {
	db := openMemDB(t)
	_, err := auth.NewJWTSessionStore("not-valid-base64!!!", db)
	require.Error(t, err, "expected error for invalid base64 key")
	assert.ErrorContains(t, err, "decode private key", "must fail on the base64-decode branch, not the length check")
}

func TestNewJWTSessionStore_WrongLength(t *testing.T) {
	db := openMemDB(t)
	// "AAAA" is valid base64 (3 decoded bytes) but not the 32-byte Ed25519 seed size.
	_, err := auth.NewJWTSessionStore("AAAA", db)
	require.Error(t, err, "expected error for wrong key length")
	assert.ErrorContains(t, err, "private key must be", "must fail on the length check")
}

func TestJWTSessionStore_IssueAndValidate(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	token, err := store.Issue(ctx, "cosmos1abc")
	require.NoError(t, err, "Issue")
	require.NotEmpty(t, token, "Issue returned empty token")

	addr, err := store.Validate(ctx, token)
	require.NoError(t, err, "Validate")
	assert.Equal(t, "cosmos1abc", addr)
}

func TestJWTSessionStore_Validate_InvalidToken(t *testing.T) {
	store := newStore(t)
	_, err := store.Validate(context.Background(), "not-a-jwt")
	require.ErrorIs(t, err, ports.ErrUnauthorized)
}

func TestJWTSessionStore_Validate_ExpiredToken(t *testing.T) {
	store := newStore(t)
	// Issue has no clock injection, so craft a token signed with the store's key but already
	// expired to prove Validate rejects it on the expiry branch.
	seed, err := base64.StdEncoding.DecodeString(testKey)
	require.NoError(t, err)
	priv := ed25519.NewKeyFromSeed(seed)

	past := time.Now().Add(-2 * time.Hour)
	signed, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.RegisteredClaims{
		Subject:   "cosmos1abc",
		IssuedAt:  jwt.NewNumericDate(past),
		ExpiresAt: jwt.NewNumericDate(past.Add(time.Hour)), // expired ~1h ago
		ID:        uuid.New().String(),
	}).SignedString(priv)
	require.NoError(t, err)

	_, err = store.Validate(context.Background(), signed)
	require.ErrorIs(t, err, ports.ErrUnauthorized, "an expired token must be rejected")
}

func TestJWTSessionStore_ParseClaims(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	token, _ := store.Issue(ctx, "cosmos1xyz")
	addr, exp, err := store.ParseClaims(token)
	require.NoError(t, err, "ParseClaims")
	assert.Equal(t, "cosmos1xyz", addr)
	assert.True(t, exp.After(time.Now()), "expiry %v is in the past", exp)
}

func TestJWTSessionStore_ParseClaims_InvalidToken(t *testing.T) {
	store := newStore(t)
	_, _, err := store.ParseClaims("garbage")
	require.ErrorIs(t, err, ports.ErrUnauthorized)
}

func TestJWTSessionStore_RevokeAllForOperator(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	token, _ := store.Issue(ctx, "cosmos1revoke")

	// Sleep 1 second so revoke_before is strictly after issuedAt.
	time.Sleep(time.Second)

	require.NoError(t, store.RevokeAllForOperator(ctx, "cosmos1revoke"), "RevokeAllForOperator")

	// Token issued strictly before the fence must be rejected.
	_, err := store.Validate(ctx, token)
	require.ErrorIs(t, err, ports.ErrUnauthorized, "expected ErrUnauthorized after revocation")
}

func TestJWTSessionStore_RevokeAllForOperator_NewTokenAllowed(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	require.NoError(t, store.RevokeAllForOperator(ctx, "cosmos1reissue"), "RevokeAllForOperator")

	// Wait one second so the new token's issuedAt is strictly after the fence.
	// JWT NumericDate is second-precision, so the token must be issued in a
	// different second than the fence to pass the revocation check.
	time.Sleep(time.Second)

	token, _ := store.Issue(ctx, "cosmos1reissue")
	addr, err := store.Validate(ctx, token)
	require.NoError(t, err, "expected new token to be valid after revocation")
	assert.Equal(t, "cosmos1reissue", addr)
}

func TestJWTSessionStore_Revoke_IsNoop(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	token, _ := store.Issue(ctx, "cosmos1noop")
	require.NoError(t, store.Revoke(ctx, token), "Revoke")
	// Token is still valid because Revoke is a no-op for JWTs.
	_, err := store.Validate(ctx, token)
	require.NoError(t, err, "expected token to still be valid after no-op Revoke")
}
