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

// newBridgeSvc wires a LaunchService with the given launch repo, join-request repo, and
// allocation store — the deps BuildRehearsalInput reads from.
func newBridgeSvc(lRepo *fakeLaunchRepo, jrRepo *fakeJoinRequestRepo, alloc *fakeAllocationStore) *LaunchService {
	return NewLaunchService(lRepo, jrRepo, newFakeReadinessRepo(), newFakeGenesisStore(), alloc, &fakeEventPublisher{}, &fakeAuditLogWriter{})
}

func TestBuildRehearsalInput_HappyPath(t *testing.T) {
	l := testLaunch()
	l.Record.TotalSupply = "1000000000"
	propID := uuid.New()
	l.AllocationFiles = []launch.AllocationFile{
		{Type: launch.AllocationAccounts, SHA256: "accountshash", Status: launch.AllocationApproved, ApprovedByProposal: &propID},
	}

	jr1 := makeJoinRequestSplit(t, l.ID, testAddr2, testAddr1)
	require.NoError(t, jr1.Approve(uuid.New()))
	jr2 := makeJoinRequestSplit(t, l.ID, testAddr3, testAddr1)
	require.NoError(t, jr2.Approve(uuid.New()))

	svc := newBridgeSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(jr1, jr2), newFakeAllocationStore())

	in, err := svc.BuildRehearsalInput(context.Background(), l.ID)
	require.NoError(t, err)

	assert.Equal(t, l.ID, in.LaunchID)
	assert.Equal(t, l.Status, in.Status)
	assert.Equal(t, "1000000000", in.Chain.TotalSupply)
	assert.Equal(t, l.Record.BinarySHA256, in.Chain.BinarySHA256)

	require.Len(t, in.Gentxs, 2)
	assert.Less(t, in.Gentxs[0].OperatorAddress, in.Gentxs[1].OperatorAddress, "gentxs sorted by operator address")

	// Allocation is metadata only — the daemon streams bytes from a per-file URL (built in the API layer).
	require.Len(t, in.Allocations, 1)
	assert.Equal(t, "accounts", in.Allocations[0].Type)
	assert.Equal(t, "accountshash", in.Allocations[0].SHA256)
	assert.Equal(t, propID.String(), in.Allocations[0].ApprovedByProposal)

	assert.Len(t, in.InputSetHash, 64, "sha256 hex")
}

func TestBuildRehearsalInput_ApprovedGentxsOnly(t *testing.T) {
	l := testLaunch()
	approved := makeJoinRequestSplit(t, l.ID, testAddr2, testAddr1)
	require.NoError(t, approved.Approve(uuid.New()))
	pending := makeJoinRequestSplit(t, l.ID, testAddr3, testAddr1) // stays PENDING

	svc := newBridgeSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(approved, pending), newFakeAllocationStore())

	in, err := svc.BuildRehearsalInput(context.Background(), l.ID)
	require.NoError(t, err)
	require.Len(t, in.Gentxs, 1, "only APPROVED join requests appear")
	assert.Equal(t, testAddr2, in.Gentxs[0].OperatorAddress)
}

func TestBuildRehearsalInput_LaunchNotFound(t *testing.T) {
	svc := newBridgeSvc(newFakeLaunchRepo(), newFakeJoinRequestRepo(), newFakeAllocationStore())
	_, err := svc.BuildRehearsalInput(context.Background(), uuid.New())
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestBuildRehearsalInput_HashDeterministicAndStatusIndependent(t *testing.T) {
	l := testLaunch()
	jr := makeJoinRequestSplit(t, l.ID, testAddr2, testAddr1)
	require.NoError(t, jr.Approve(uuid.New()))
	jrRepo := newFakeJoinRequestRepo(jr)
	svc := newBridgeSvc(newFakeLaunchRepo(l), jrRepo, newFakeAllocationStore())

	first, err := svc.BuildRehearsalInput(context.Background(), l.ID)
	require.NoError(t, err)

	// Same inputs → same hash (generated_at differs but is excluded).
	again, err := svc.BuildRehearsalInput(context.Background(), l.ID)
	require.NoError(t, err)
	assert.Equal(t, first.InputSetHash, again.InputSetHash, "hash is deterministic")

	// Status change → SAME hash (status is excluded, D7).
	l.Status = launch.StatusWindowClosed
	afterStatus, err := svc.BuildRehearsalInput(context.Background(), l.ID)
	require.NoError(t, err)
	assert.Equal(t, first.InputSetHash, afterStatus.InputSetHash, "status is not part of input_set_hash")

	// Adding an approved gentx changes the input set → different hash.
	jr2 := makeJoinRequestSplit(t, l.ID, testAddr3, testAddr1)
	require.NoError(t, jr2.Approve(uuid.New()))
	jrRepo.data[jr2.ID] = jr2
	afterGentx, err := svc.BuildRehearsalInput(context.Background(), l.ID)
	require.NoError(t, err)
	assert.NotEqual(t, first.InputSetHash, afterGentx.InputSetHash, "a new gentx changes the hash")
}
