package auth_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/auth"
)

// testKey is a valid base64-encoded 32-byte Ed25519 seed.
const testKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func openMemDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.ExecContext(t.Context(), `CREATE TABLE operator_revocations (
		operator_address TEXT NOT NULL PRIMARY KEY,
		revoke_before    TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

func newStore(t *testing.T) *auth.JWTSessionStore {
	t.Helper()
	db := openMemDB(t)
	store, err := auth.NewJWTSessionStore(testKey, db)
	if err != nil {
		t.Fatalf("NewJWTSessionStore: %v", err)
	}
	return store
}

func TestNewJWTSessionStore_InvalidKey(t *testing.T) {
	db := openMemDB(t)
	_, err := auth.NewJWTSessionStore("not-valid-base64!!!", db)
	if err == nil {
		t.Fatal("expected error for invalid base64 key")
	}
}

func TestJWTSessionStore_IssueAndValidate(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	token, err := store.Issue(ctx, "cosmos1abc")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if token == "" {
		t.Fatal("Issue returned empty token")
	}

	addr, err := store.Validate(ctx, token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if addr != "cosmos1abc" {
		t.Errorf("Validate returned %q, want %q", addr, "cosmos1abc")
	}
}

func TestJWTSessionStore_Validate_InvalidToken(t *testing.T) {
	store := newStore(t)
	_, err := store.Validate(context.Background(), "not-a-jwt")
	if !errors.Is(err, ports.ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestJWTSessionStore_ParseClaims(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	token, _ := store.Issue(ctx, "cosmos1xyz")
	addr, exp, err := store.ParseClaims(token)
	if err != nil {
		t.Fatalf("ParseClaims: %v", err)
	}
	if addr != "cosmos1xyz" {
		t.Errorf("addr: got %q, want %q", addr, "cosmos1xyz")
	}
	if exp.Before(time.Now()) {
		t.Errorf("expiry %v is in the past", exp)
	}
}

func TestJWTSessionStore_ParseClaims_InvalidToken(t *testing.T) {
	store := newStore(t)
	_, _, err := store.ParseClaims("garbage")
	if !errors.Is(err, ports.ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestJWTSessionStore_RevokeAllForOperator(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	token, _ := store.Issue(ctx, "cosmos1revoke")

	// Sleep 1 second so revoke_before is strictly after issuedAt.
	time.Sleep(time.Second)

	if err := store.RevokeAllForOperator(ctx, "cosmos1revoke"); err != nil {
		t.Fatalf("RevokeAllForOperator: %v", err)
	}

	// Token issued strictly before the fence must be rejected.
	_, err := store.Validate(ctx, token)
	if !errors.Is(err, ports.ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized after revocation, got %v", err)
	}
}

func TestJWTSessionStore_RevokeAllForOperator_NewTokenAllowed(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	if err := store.RevokeAllForOperator(ctx, "cosmos1reissue"); err != nil {
		t.Fatalf("RevokeAllForOperator: %v", err)
	}

	// Wait one second so the new token's issuedAt is strictly after the fence.
	// JWT NumericDate is second-precision, so the token must be issued in a
	// different second than the fence to pass the revocation check.
	time.Sleep(time.Second)

	token, _ := store.Issue(ctx, "cosmos1reissue")
	addr, err := store.Validate(ctx, token)
	if err != nil {
		t.Errorf("expected new token to be valid after revocation, got %v", err)
	}
	if addr != "cosmos1reissue" {
		t.Errorf("addr: got %q, want %q", addr, "cosmos1reissue")
	}
}

func TestJWTSessionStore_Revoke_IsNoop(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	token, _ := store.Issue(ctx, "cosmos1noop")
	if err := store.Revoke(ctx, token); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	// Token is still valid because Revoke is a no-op for JWTs.
	_, err := store.Validate(ctx, token)
	if err != nil {
		t.Errorf("expected token to still be valid after no-op Revoke, got %v", err)
	}
}
