package launch

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRehearsalAttempt_ClaimAndLease(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := &RehearsalAttempt{ID: uuid.New(), Status: AttemptOpen}

	require.NoError(t, a.Claim("runner-A", now, now.Add(time.Hour)))
	assert.Equal(t, AttemptRunning, a.Status)
	assert.True(t, a.IsLeased(now))

	// A different runner while the lease is live → refused.
	require.ErrorIs(t, a.Claim("runner-B", now.Add(time.Minute), now.Add(time.Hour)), ErrAttemptLeased)

	// The same runner re-claiming is an idempotent no-op — it must NOT extend the deadline, else a
	// chatty/looping runner would hold the lease forever and defeat the TTL self-heal.
	require.NoError(t, a.Claim("runner-A", now.Add(time.Minute), now.Add(5*time.Hour)))
	require.NotNil(t, a.LeaseExpiresAt)
	assert.Equal(t, now.Add(time.Hour), *a.LeaseExpiresAt, "re-claim must keep the original lease deadline")

	// Once that original lease expires, a different runner may claim.
	assert.False(t, a.IsLeased(now.Add(90*time.Minute)))
	require.NoError(t, a.Claim("runner-B", now.Add(90*time.Minute), now.Add(150*time.Minute)))
	assert.Equal(t, "runner-B", a.RunnerID)
}

func TestRehearsalAttempt_ReleaseAndReset(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := &RehearsalAttempt{ID: uuid.New(), Status: AttemptOpen}
	require.NoError(t, a.Claim("runner-A", now, now.Add(time.Hour)))

	a.Release()
	assert.Equal(t, AttemptDone, a.Status)
	assert.False(t, a.IsLeased(now))
	// A finished attempt is freely re-claimable (a fresh run of the same input set).
	require.NoError(t, a.Claim("runner-A", now, now.Add(time.Hour)))

	a.Reset()
	assert.Equal(t, AttemptOpen, a.Status)
	assert.Nil(t, a.ClaimedAt)
	assert.Nil(t, a.LeaseExpiresAt)
	assert.Empty(t, a.RunnerID)
}

func TestEvaluateRehearsalReady(t *testing.T) {
	const h = "abc123"
	ready := func(latest *RehearsalResult, cur string) bool {
		ok, _ := EvaluateRehearsalReady(latest, cur)
		return ok
	}
	assert.True(t, ready(&RehearsalResult{Outcome: OutcomePass, InputSetHash: h}, h), "current PASS is ready")
	assert.False(t, ready(nil, h), "no result → not ready")
	assert.False(t, ready(&RehearsalResult{Outcome: OutcomeFail, InputSetHash: h}, h), "FAIL → not ready")
	assert.False(t, ready(&RehearsalResult{Outcome: OutcomeSkipped, InputSetHash: h}, h), "SKIPPED → not ready")

	ok, reason := EvaluateRehearsalReady(&RehearsalResult{Outcome: OutcomePass, InputSetHash: "old"}, h)
	assert.False(t, ok, "stale PASS → not ready")
	assert.Contains(t, reason, "stale")
}

func TestParseRehearsalGateMode(t *testing.T) {
	for in, want := range map[string]RehearsalGateMode{
		"":         RehearsalGateOff,
		"off":      RehearsalGateOff,
		"advisory": RehearsalGateAdvisory,
		"required": RehearsalGateRequired,
	} {
		got, err := ParseRehearsalGateMode(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	_, err := ParseRehearsalGateMode("bogus")
	assert.Error(t, err, "unrecognized mode is an error")
}
