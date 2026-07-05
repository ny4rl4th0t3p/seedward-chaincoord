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
	svc, _ := claimSvc(t, l)

	first, err := svc.ClaimRehearsalRun(context.Background(), l.ID, "runner-1")
	require.NoError(t, err)
	second, err := svc.ClaimRehearsalRun(context.Background(), l.ID, "runner-1")
	require.NoError(t, err)
	assert.Equal(t, first.AttemptID, second.AttemptID, "same runner refreshes its own lease")
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
