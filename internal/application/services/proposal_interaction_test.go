package services

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/proposal"
)

// These tests deliberately stage MULTI-PROPOSAL SEQUENCES — the interaction/ordering seam that
// per-action unit tests (each proposal exercised in isolation) can't see, and precisely where the
// genesis↔approved-set drift bug lived. They complement the per-action tests in proposal_test.go.

func windowClosed2of2(t *testing.T) *launch.Launch {
	t.Helper()
	l, err := launch.New(uuid.New(), testChainRecord(), launch.LaunchTypeTestnet, testCommittee(2, 2))
	require.NoError(t, err)
	l.Status = launch.StatusWindowClosed
	return l
}

// signAs signs a proposal as the 2-of-2 committee's second member (testAddr2) — the quorum-completer.
func signAs(t *testing.T, svc *ProposalService, launchID, propID uuid.UUID, d proposal.Decision) (*proposal.Proposal, error) {
	t.Helper()
	return svc.Sign(context.Background(), launchID, propID, SignInput{
		CoordinatorAddr: testAddr2,
		Decision:        d,
		Nonce:           uuid.New().String(),
		Timestamp:       nowTS(),
		Signature:       testSig,
	})
}

// The bug shape, at the sign→execute seam: a PUBLISH_GENESIS pends in a 2-of-2 committee while the
// approved set changes underneath it (a concurrent approve that raced past the raise-time freeze). The
// execute-time re-check must refuse to finalize a genesis that no longer matches the set.
func TestInteraction_PublishGenesis_SetDriftsDuringSigning_BlockedAtExecute(t *testing.T) {
	l := windowClosed2of2(t)
	jrRepo := newFakeJoinRequestRepo()
	svc := newProposalSvc(newFakeLaunchRepo(l), jrRepo, newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})
	hash := bindFinalGenesis(t, svc, l) // bound to the current (empty) approved set

	p, err := raiseWith(t, svc, l.ID, proposal.ActionPublishGenesis, proposal.PublishGenesisPayload{GenesisHash: hash})
	require.NoError(t, err)
	require.Equal(t, proposal.StatusPendingSignatures, p.Status)

	// The approved set drifts while the proposal is pending.
	jr := makeJoinRequest(t, l.ID, testAddr2)
	require.NoError(t, jr.Approve(uuid.New()))
	jrRepo.data[jr.ID] = jr

	_, err = signAs(t, svc, l.ID, p.ID, proposal.DecisionSign)
	require.ErrorIs(t, err, ports.ErrConflict)
	require.ErrorIs(t, err, launch.ErrGenesisStale)
	assert.Equal(t, launch.StatusWindowClosed, l.Status, "must NOT advance to GENESIS_READY on a stale genesis")
}

// The multi-sig sign→execute path runs the same consistency guards and finalizes when the set is
// unchanged — proving the guards live on the execute path, not just the 1-of-1 auto-execute.
func TestInteraction_PublishGenesis_MultiSig_ExecutesWhenConsistent(t *testing.T) {
	l := windowClosed2of2(t)
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})
	hash := bindFinalGenesis(t, svc, l)

	p, err := raiseWith(t, svc, l.ID, proposal.ActionPublishGenesis, proposal.PublishGenesisPayload{GenesisHash: hash})
	require.NoError(t, err)
	require.Equal(t, proposal.StatusPendingSignatures, p.Status)

	p2, err := signAs(t, svc, l.ID, p.ID, proposal.DecisionSign)
	require.NoError(t, err)
	require.Equal(t, proposal.StatusExecuted, p2.Status)
	assert.Equal(t, launch.StatusGenesisReady, l.Status)
}

// The freeze is tied to a PENDING publish and lifts once it is resolved: while a PUBLISH_GENESIS is
// pending, set changes are refused; vetoing it re-opens them.
func TestInteraction_Freeze_LiftsOnVeto(t *testing.T) {
	l := windowClosed2of2(t)
	jr := makeJoinRequest(t, l.ID, testAddr3)
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(jr), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})
	hash := bindFinalGenesis(t, svc, l)

	pub, err := raiseWith(t, svc, l.ID, proposal.ActionPublishGenesis, proposal.PublishGenesisPayload{GenesisHash: hash})
	require.NoError(t, err)
	require.Equal(t, proposal.StatusPendingSignatures, pub.Status)

	// Frozen: cannot approve while the publish is pending.
	_, err = raiseWith(t, svc, l.ID, proposal.ActionApproveValidator, proposal.ApproveValidatorPayload{
		JoinRequestID: jr.ID, OperatorAddress: testAddr3,
	})
	require.ErrorIs(t, err, launch.ErrGenesisPublishInProgress)

	// Veto the publish → no longer pending.
	pub2, err := signAs(t, svc, l.ID, pub.ID, proposal.DecisionVeto)
	require.NoError(t, err)
	require.Equal(t, proposal.StatusVetoed, pub2.Status)

	// Freeze lifted: the approve raise is accepted again (pending in the 2-of-2).
	_, err = raiseWith(t, svc, l.ID, proposal.ActionApproveValidator, proposal.ApproveValidatorPayload{
		JoinRequestID: jr.ID, OperatorAddress: testAddr3,
	})
	require.NoError(t, err, "approve is allowed once the publish is vetoed")
}

// The required gate re-checks at execute, so a rehearsal that flips to FAIL while a PUBLISH_GENESIS is
// pending (the approved set unchanged) blocks finalization — something the set-consistency check alone
// cannot catch, since the input_set_hash is identical. Only the gate sees the changed verdict.
func TestInteraction_RehearsalGate_Required_FailDuringSigning_BlockedAtExecute(t *testing.T) {
	l := windowClosed2of2(t)
	l.RehearsalServicePubKey = "pk"
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})
	hash := bindFinalGenesis(t, svc, l)

	results := newFakeRehearsalResultRepo()
	require.NoError(t, results.Save(context.Background(), &launch.RehearsalResult{
		LaunchID: l.ID, Outcome: launch.OutcomePass, InputSetHash: l.FinalGenesisInputSetHash, Signature: "pass",
	}))
	gated := svc.WithRehearsalGate("required", results)

	// Raise with a current PASS — the gate is satisfied, so the proposal pends (1 of 2).
	p, err := raiseWith(t, gated, l.ID, proposal.ActionPublishGenesis, proposal.PublishGenesisPayload{GenesisHash: hash})
	require.NoError(t, err)
	require.Equal(t, proposal.StatusPendingSignatures, p.Status)

	// A FAIL rehearsal for the SAME input set arrives while the publish is pending (consistency stays OK).
	require.NoError(t, results.Save(context.Background(), &launch.RehearsalResult{
		LaunchID: l.ID, Outcome: launch.OutcomeFail, InputSetHash: l.FinalGenesisInputSetHash, Signature: "fail",
	}))

	// The quorum-completing signature re-checks the gate → latest is now FAIL → blocked.
	_, err = signAs(t, gated, l.ID, p.ID, proposal.DecisionSign)
	require.ErrorIs(t, err, ports.ErrConflict)
	require.ErrorIs(t, err, launch.ErrRehearsalGateUnsatisfied)
	assert.Equal(t, launch.StatusWindowClosed, l.Status, "a FAIL rehearsal during signing must block finalization")
}
