package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

func TestRehearsalAttemptRepo_GetOrCreateIdempotent(t *testing.T) {
	repo := NewRehearsalAttemptRepository(openTestDB(t))
	ctx := context.Background()
	launchID := uuid.New()

	a1, err := repo.GetOrCreate(ctx, launchID, "hashA", nowUTC())
	require.NoError(t, err)
	assert.Equal(t, launch.AttemptOpen, a1.Status)

	a2, err := repo.GetOrCreate(ctx, launchID, "hashA", nowUTC())
	require.NoError(t, err)
	assert.Equal(t, a1.ID, a2.ID, "same (launch, hash) → same attempt")

	a3, err := repo.GetOrCreate(ctx, launchID, "hashB", nowUTC())
	require.NoError(t, err)
	assert.NotEqual(t, a1.ID, a3.ID, "different hash → different attempt")
}

func TestRehearsalAttemptRepo_FindByID(t *testing.T) {
	repo := NewRehearsalAttemptRepository(openTestDB(t))
	ctx := context.Background()

	a, err := repo.GetOrCreate(ctx, uuid.New(), "h", nowUTC())
	require.NoError(t, err)

	got, err := repo.FindByID(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, "h", got.InputSetHash)

	_, err = repo.FindByID(ctx, uuid.New())
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestRehearsalAttemptRepo_SaveLease(t *testing.T) {
	repo := NewRehearsalAttemptRepository(openTestDB(t))
	ctx := context.Background()

	a, err := repo.GetOrCreate(ctx, uuid.New(), "h", nowUTC())
	require.NoError(t, err)
	now := nowUTC()
	require.NoError(t, a.Claim("runner-1", now, now.Add(time.Hour)))
	require.NoError(t, repo.Save(ctx, a))

	got, err := repo.FindByID(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, launch.AttemptRunning, got.Status)
	assert.Equal(t, "runner-1", got.RunnerID)
	require.NotNil(t, got.LeaseExpiresAt)

	// Reset clears the lease back to OPEN.
	got.Reset()
	require.NoError(t, repo.Save(ctx, got))
	after, err := repo.FindByID(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, launch.AttemptOpen, after.Status)
	assert.Nil(t, after.LeaseExpiresAt)
	assert.Empty(t, after.RunnerID)
}

func TestRehearsalResultRepo_SaveAndFind(t *testing.T) {
	repo := NewRehearsalResultRepository(openTestDB(t))
	ctx := context.Background()
	launchID := uuid.New()

	res := &launch.RehearsalResult{
		ID: uuid.New(), AttemptID: uuid.New(), LaunchID: launchID, InputSetHash: "h",
		Outcome: launch.OutcomePass, Summary: "ok",
		Steps:         []launch.RehearsalResultStep{{Name: "boot", Status: "PASS", Detail: "d"}},
		EngineVersion: "eng1", BinaryName: "gaiad", Validators: 2, BlocksAdvanced: 3,
		StartedAt: nowUTC(), FinishedAt: nowUTC(), ServicePubKey: "pk", Signature: "sig-1",
		Stale: true, RecordedAt: nowUTC(),
	}
	require.NoError(t, repo.Save(ctx, res))

	got, err := repo.FindByLaunch(ctx, launchID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "sig-1", got[0].Signature)
	assert.Equal(t, launch.OutcomePass, got[0].Outcome)
	assert.True(t, got[0].Stale)
	assert.Equal(t, 3, got[0].BlocksAdvanced)
	require.Len(t, got[0].Steps, 1)
	assert.Equal(t, "boot", got[0].Steps[0].Name)
}

func TestRehearsalResultRepo_IdempotentOnSignature(t *testing.T) {
	repo := NewRehearsalResultRepository(openTestDB(t))
	ctx := context.Background()
	launchID := uuid.New()
	mk := func() *launch.RehearsalResult {
		return &launch.RehearsalResult{
			ID: uuid.New(), AttemptID: uuid.New(), LaunchID: launchID, InputSetHash: "h",
			Outcome: launch.OutcomePass, Signature: "dup-sig",
			StartedAt: nowUTC(), FinishedAt: nowUTC(), RecordedAt: nowUTC(),
		}
	}
	require.NoError(t, repo.Save(ctx, mk()))
	require.NoError(t, repo.Save(ctx, mk()))

	got, err := repo.FindByLaunch(ctx, launchID)
	require.NoError(t, err)
	require.Len(t, got, 1, "UNIQUE(signature) dedupes re-saves")
}
