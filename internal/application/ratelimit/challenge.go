package ratelimit

import (
	"context"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

// RateLimitedChallengeStore wraps a ports.ChallengeStore and enforces a
// ports.ChallengeRateLimiter before delegating Issue calls. Consume is
// passed through without rate limiting.
type RateLimitedChallengeStore struct {
	inner   ports.ChallengeStore
	limiter ports.ChallengeRateLimiter
}

func NewRateLimitedChallengeStore(inner ports.ChallengeStore, limiter ports.ChallengeRateLimiter) *RateLimitedChallengeStore {
	return &RateLimitedChallengeStore{inner: inner, limiter: limiter}
}

func (s *RateLimitedChallengeStore) Issue(ctx context.Context, operatorAddr string) (string, error) {
	if err := s.limiter.Allow(ctx, operatorAddr); err != nil {
		return "", err
	}
	return s.inner.Issue(ctx, operatorAddr)
}

func (s *RateLimitedChallengeStore) Consume(ctx context.Context, operatorAddr string) (string, error) {
	return s.inner.Consume(ctx, operatorAddr)
}
