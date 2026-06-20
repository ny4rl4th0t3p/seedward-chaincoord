package ratelimit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ratelimit"
)

// --- fakes ---

type fakeChallengeStore struct {
	issued    []string
	challenge string
	err       error
}

func (f *fakeChallengeStore) Issue(_ context.Context, addr string) (string, error) {
	f.issued = append(f.issued, addr)
	return f.challenge, f.err
}

func (f *fakeChallengeStore) Consume(_ context.Context, _ string) (string, error) {
	return f.challenge, f.err
}

type fakeRateLimiter struct {
	err error
}

func (f *fakeRateLimiter) Allow(_ context.Context, _ string) error { return f.err }

// --- tests ---

func TestRateLimitedChallengeStore_Issue_AllowsWhenLimiterOK(t *testing.T) {
	inner := &fakeChallengeStore{challenge: "abc123"}
	store := ratelimit.NewRateLimitedChallengeStore(inner, &fakeRateLimiter{})

	got, err := store.Issue(context.Background(), "addr1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "abc123" {
		t.Fatalf("expected challenge %q, got %q", "abc123", got)
	}
	if len(inner.issued) != 1 || inner.issued[0] != "addr1" {
		t.Fatalf("expected inner store to be called once with addr1, got %v", inner.issued)
	}
}

func TestRateLimitedChallengeStore_Issue_BlocksWhenLimiterDenies(t *testing.T) {
	inner := &fakeChallengeStore{challenge: "abc123"}
	store := ratelimit.NewRateLimitedChallengeStore(inner, &fakeRateLimiter{err: ports.ErrTooManyRequests})

	_, err := store.Issue(context.Background(), "addr1")
	if !errors.Is(err, ports.ErrTooManyRequests) {
		t.Fatalf("expected ErrTooManyRequests, got %v", err)
	}
	if len(inner.issued) != 0 {
		t.Fatal("inner store must not be called when limiter denies")
	}
}

func TestRateLimitedChallengeStore_Consume_BypassesLimiter(t *testing.T) {
	inner := &fakeChallengeStore{challenge: "abc123"}
	// Limiter always denies — Consume must still succeed.
	store := ratelimit.NewRateLimitedChallengeStore(inner, &fakeRateLimiter{err: ports.ErrTooManyRequests})

	got, err := store.Consume(context.Background(), "addr1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "abc123" {
		t.Fatalf("expected challenge %q, got %q", "abc123", got)
	}
}
