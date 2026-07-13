package services

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

func claimSvc(t *testing.T, l *launch.Launch) (*LaunchService, *fakeRehearsalAttemptRepo) {
	t.Helper()
	attempts := newFakeRehearsalAttemptRepo()
	svc := NewLaunchService(
		newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeReadinessRepo(),
		newFakeGenesisStore(), newFakeAllocationStore(), &fakeEventPublisher{}, &fakeAuditLogWriter{},
		attempts, newFakeRehearsalResultRepo(),
	)
	return svc, attempts
}

func TestClaimRehearsalRun_HappyPath(t *testing.T) {
	l := testLaunch()
	svc, attempts := claimSvc(t, l)

	in, err := svc.ClaimRehearsalRun(context.Background(), l.ID, "runner-1")
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, in.AttemptID)

	a, err := attempts.FindByID(context.Background(), in.AttemptID)
	require.NoError(t, err)
	assert.Equal(t, launch.AttemptRunning, a.Status)
	assert.Equal(t, "runner-1", a.RunnerID)
}

func TestClaimRehearsalRun_BusyDifferentRunner(t *testing.T) {
	l := testLaunch()
	svc, _ := claimSvc(t, l)

	_, err := svc.ClaimRehearsalRun(context.Background(), l.ID, "runner-1")
	require.NoError(t, err)

	_, err = svc.ClaimRehearsalRun(context.Background(), l.ID, "runner-2")
	require.ErrorIs(t, err, ports.ErrConflict)
	var leased *RehearsalLeasedError
	require.ErrorAs(t, err, &leased)
	assert.Equal(t, "runner-1", leased.RunnerID)
}

func TestClaimRehearsalRun_SameRunnerReclaims(t *testing.T) {
	l := testLaunch()
	svc, attempts := claimSvc(t, l)

	first, err := svc.ClaimRehearsalRun(context.Background(), l.ID, "runner-1")
	require.NoError(t, err)
	claimed, err := attempts.FindByID(context.Background(), first.AttemptID)
	require.NoError(t, err)
	require.NotNil(t, claimed.LeaseExpiresAt, "a claim sets the lease deadline")
	firstDeadline := *claimed.LeaseExpiresAt // snapshot the value before re-claim

	second, err := svc.ClaimRehearsalRun(context.Background(), l.ID, "runner-1")
	require.NoError(t, err)
	assert.Equal(t, first.AttemptID, second.AttemptID, "same runner may re-claim its own attempt (idempotent)")

	reclaimed, err := attempts.FindByID(context.Background(), second.AttemptID)
	require.NoError(t, err)
	require.NotNil(t, reclaimed.LeaseExpiresAt)
	assert.Equal(t, firstDeadline, *reclaimed.LeaseExpiresAt,
		"re-claim by the same runner must not extend the lease deadline")
}

func TestClaimRehearsalRun_Audited(t *testing.T) {
	l := testLaunch()
	audit := &fakeAuditLogWriter{}
	svc := NewLaunchService(
		newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeReadinessRepo(),
		newFakeGenesisStore(), newFakeAllocationStore(), &fakeEventPublisher{}, audit,
		newFakeRehearsalAttemptRepo(), newFakeRehearsalResultRepo(),
	)

	_, err := svc.ClaimRehearsalRun(context.Background(), l.ID, "runner-1")
	require.NoError(t, err)

	require.Len(t, audit.events, 1, "claiming a rehearsal run must be audited")
	assert.Equal(t, "RehearsalRunClaimed", audit.events[0].EventName)
}

func TestResetRehearsalAttempt_FreesLease(t *testing.T) {
	l := testLaunch()
	l.Committee = testCommittee(1, 1) // lead = testAddr1
	svc, _ := claimSvc(t, l)

	in, err := svc.ClaimRehearsalRun(context.Background(), l.ID, "runner-1")
	require.NoError(t, err)

	// Non-committee caller cannot reset.
	require.ErrorIs(t,
		svc.ResetRehearsalAttempt(context.Background(), l.ID, in.AttemptID, testAddr2),
		ports.ErrForbidden)

	// Committee lead resets → a different runner can then claim.
	require.NoError(t, svc.ResetRehearsalAttempt(context.Background(), l.ID, in.AttemptID, testAddr1))
	_, err = svc.ClaimRehearsalRun(context.Background(), l.ID, "runner-2")
	require.NoError(t, err)
}
