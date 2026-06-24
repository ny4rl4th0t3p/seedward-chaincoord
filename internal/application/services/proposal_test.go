package services

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/joinrequest"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/proposal"
)

// allocation file content hashes for the APPROVE_ALLOCATION_FILE tests.
const (
	allocHashA = "a1b5c3d4a5"
	allocHashB = "f6a5d2c3b2"
)

func newProposalSvc(
	launchRepo *fakeLaunchRepo,
	jrRepo *fakeJoinRequestRepo,
	propRepo *fakeProposalRepo,
	readinessRepo *fakeReadinessRepo,
	nonces *fakeNonceStore,
	verifier *fakeVerifier,
) *ProposalService {
	return NewProposalService(
		launchRepo, jrRepo, propRepo, readinessRepo,
		nonces, verifier,
		&fakeEventPublisher{}, &fakeAuditLogWriter{}, &fakeTransactor{},
	)
}

// hasAuditEvent reports whether an audit event with the given name was written.
func hasAuditEvent(evs []ports.AuditEvent, name string) bool {
	for _, e := range evs {
		if e.EventName == name {
			return true
		}
	}
	return false
}

func validRaiseInput(_ *launch.Launch) RaiseInput {
	payload, _ := json.Marshal(proposal.CloseApplicationWindowPayload{})
	return RaiseInput{
		ActionType:      proposal.ActionCloseApplicationWindow,
		Payload:         payload,
		CoordinatorAddr: testAddr1,
		Nonce:           uuid.New().String(),
		Timestamp:       nowTS(),
		Signature:       testSig,
	}
}

// --- Raise ---

func TestProposalService_Raise_NonceConflict(t *testing.T) {
	l := testLaunch()
	nonces := newFakeNonceStore()
	nonces.consumeErr = ports.ErrConflict
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), nonces, &fakeVerifier{})

	_, err := svc.Raise(context.Background(), l.ID, validRaiseInput(l))
	require.Error(t, err, "expected error for nonce conflict")
}

func TestProposalService_Raise_BadTimestamp(t *testing.T) {
	l := testLaunch()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	input := validRaiseInput(l)
	input.Timestamp = expiredTS()
	_, err := svc.Raise(context.Background(), l.ID, input)
	require.Error(t, err, "expected error for expired timestamp")
}

func TestProposalService_Raise_LaunchNotFound(t *testing.T) {
	svc := newProposalSvc(newFakeLaunchRepo(), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	payload, _ := json.Marshal(proposal.CloseApplicationWindowPayload{})
	_, err := svc.Raise(context.Background(), uuid.New(), RaiseInput{
		ActionType:      proposal.ActionCloseApplicationWindow,
		Payload:         payload,
		CoordinatorAddr: testAddr1,
		Nonce:           uuid.New().String(),
		Timestamp:       nowTS(),
		Signature:       testSig,
	})
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestProposalService_Raise_NotCommitteeMember(t *testing.T) {
	// Use a 1-of-1 committee with only testAddr1; testAddr2 is not a member.
	l := testLaunch()
	l.Committee = testCommittee(1, 1)
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	payload, _ := json.Marshal(proposal.CloseApplicationWindowPayload{})
	_, err := svc.Raise(context.Background(), l.ID, RaiseInput{
		ActionType:      proposal.ActionCloseApplicationWindow,
		Payload:         payload,
		CoordinatorAddr: testAddr2, // not in committee
		Nonce:           uuid.New().String(),
		Timestamp:       nowTS(),
		Signature:       testSig,
	})
	require.ErrorIs(t, err, ports.ErrForbidden)
}

func TestProposalService_Raise_SigFails(t *testing.T) {
	l := testLaunch()
	verifier := &fakeVerifier{err: ports.ErrUnauthorized}
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), verifier)

	_, err := svc.Raise(context.Background(), l.ID, validRaiseInput(l))
	require.Error(t, err, "expected error when signature verification fails")
}

func TestProposalService_Raise_StaysPending(t *testing.T) {
	// 2-of-3 committee: one signature (the proposer's) is not enough to execute.
	l := testLaunch() // 2-of-3
	propRepo := newFakeProposalRepo()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), propRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	p, err := svc.Raise(context.Background(), l.ID, validRaiseInput(l))
	require.NoError(t, err)
	assert.Equal(t, proposal.StatusPendingSignatures, p.Status)
	require.Contains(t, propRepo.data, p.ID, "proposal not persisted")
}

func TestProposalService_Raise_1of1ExecutesImmediately(t *testing.T) {
	// 1-of-1 committee: the proposer's single signature executes the proposal.
	l := test1of1Launch()
	l.Status = launch.StatusWindowOpen // CloseWindow requires WINDOW_OPEN

	propRepo := newFakeProposalRepo()
	// Add one approved validator so CloseWindow passes the MinValidatorCount=1 check.
	jr := makeJoinRequest(t, l.ID, testAddr2)
	require.NoError(t, jr.Approve(uuid.New()))
	jrRepo := newFakeJoinRequestRepo(jr)

	svc := newProposalSvc(newFakeLaunchRepo(l), jrRepo, propRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	payload, _ := json.Marshal(proposal.CloseApplicationWindowPayload{})
	p, err := svc.Raise(context.Background(), l.ID, RaiseInput{
		ActionType:      proposal.ActionCloseApplicationWindow,
		Payload:         payload,
		CoordinatorAddr: testAddr1,
		Nonce:           uuid.New().String(),
		Timestamp:       nowTS(),
		Signature:       testSig,
	})
	require.NoError(t, err)
	assert.Equal(t, proposal.StatusExecuted, p.Status, "want EXECUTED for 1-of-1 committee")
}

// --- Sign ---

func TestProposalService_Sign_NonceConflict(t *testing.T) {
	l := testLaunch()
	p := testProposal(l.ID)
	nonces := newFakeNonceStore()
	nonces.consumeErr = ports.ErrConflict
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(p), newFakeReadinessRepo(), nonces, &fakeVerifier{})

	_, err := svc.Sign(context.Background(), l.ID, p.ID, SignInput{
		CoordinatorAddr: testAddr1,
		Decision:        proposal.DecisionSign,
		Nonce:           uuid.New().String(),
		Timestamp:       nowTS(),
		Signature:       testSig,
	})
	require.Error(t, err, "expected error for nonce conflict")
}

func TestProposalService_Sign_NotCommitteeMember(t *testing.T) {
	l := testLaunch()
	l.Committee = testCommittee(1, 1) // only testAddr1
	p := testProposal(l.ID)
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(p), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := svc.Sign(context.Background(), l.ID, p.ID, SignInput{
		CoordinatorAddr: testAddr2, // not in committee
		Decision:        proposal.DecisionSign,
		Nonce:           uuid.New().String(),
		Timestamp:       nowTS(),
		Signature:       testSig,
	})
	require.ErrorIs(t, err, ports.ErrForbidden)
}

func TestProposalService_Sign_WrongLaunch(t *testing.T) {
	l := testLaunch()
	otherLaunchID := uuid.New()
	p := testProposal(otherLaunchID) // proposal belongs to a different launch
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(p), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := svc.Sign(context.Background(), l.ID, p.ID, SignInput{
		CoordinatorAddr: testAddr1,
		Decision:        proposal.DecisionSign,
		Nonce:           uuid.New().String(),
		Timestamp:       nowTS(),
		Signature:       testSig,
	})
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestProposalService_Sign_AddsSignature(t *testing.T) {
	// 3-of-3 committee; Raise already added 1 signature (testAddr1 as proposer).
	// Sign as testAddr2 → still PENDING.
	l, _ := launch.New(uuid.New(), testChainRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee(3, 3))
	propRepo := newFakeProposalRepo()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), propRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	// Raise as testAddr1.
	payload, _ := json.Marshal(proposal.CloseApplicationWindowPayload{})
	p, err := svc.Raise(context.Background(), l.ID, RaiseInput{
		ActionType:      proposal.ActionCloseApplicationWindow,
		Payload:         payload,
		CoordinatorAddr: testAddr1,
		Nonce:           uuid.New().String(),
		Timestamp:       nowTS(),
		Signature:       testSig,
	})
	require.NoError(t, err)
	require.Equal(t, proposal.StatusPendingSignatures, p.Status, "want PENDING after raise")

	// Sign as testAddr2.
	p2, err := svc.Sign(context.Background(), l.ID, p.ID, SignInput{
		CoordinatorAddr: testAddr2,
		Decision:        proposal.DecisionSign,
		Nonce:           uuid.New().String(),
		Timestamp:       nowTS(),
		Signature:       testSig,
	})
	require.NoError(t, err)
	assert.Equal(t, proposal.StatusPendingSignatures, p2.Status, "want still PENDING after second of three")
	assert.Equal(t, 2, p2.SignCount())
}

// --- ExpireStale ---

func TestProposalService_ExpireStale_ExpiresOld(t *testing.T) {
	l := testLaunch()
	propRepo := newFakeProposalRepo()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), propRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	// Create a proposal with a TTL that has already elapsed.
	payload, _ := json.Marshal(proposal.CloseApplicationWindowPayload{})
	p, _ := proposal.New(uuid.New(), l.ID, proposal.ActionCloseApplicationWindow, payload,
		mustAddr(testAddr1), mustSig(), 1*time.Millisecond, time.Now().Add(-1*time.Hour))
	propRepo.data[p.ID] = p

	require.NoError(t, svc.ExpireStale(context.Background()))
	assert.Equal(t, proposal.StatusExpired, propRepo.data[p.ID].Status)
}

func TestProposalService_ExpireStale_SkipsFresh(t *testing.T) {
	l := testLaunch()
	propRepo := newFakeProposalRepo()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), propRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	payload, _ := json.Marshal(proposal.CloseApplicationWindowPayload{})
	p, _ := proposal.New(uuid.New(), l.ID, proposal.ActionCloseApplicationWindow, payload,
		mustAddr(testAddr1), mustSig(), 48*time.Hour, time.Now())
	propRepo.data[p.ID] = p

	require.NoError(t, svc.ExpireStale(context.Background()))
	assert.Equal(t, proposal.StatusPendingSignatures, propRepo.data[p.ID].Status, "fresh proposal should not be expired")
}

// --- GetByID ---

func TestProposalService_GetByID_WrongLaunch(t *testing.T) {
	p := testProposal(uuid.New())
	svc := newProposalSvc(newFakeLaunchRepo(), newFakeJoinRequestRepo(), newFakeProposalRepo(p), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := svc.GetByID(context.Background(), uuid.New(), p.ID) // wrong launchID
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestProposalService_GetByID_Success(t *testing.T) {
	l := testLaunch()
	p := testProposal(l.ID)
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(p), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	got, err := svc.GetByID(context.Background(), l.ID, p.ID)
	require.NoError(t, err)
	assert.Equal(t, p.ID, got.ID)
}

// --- ListForLaunch ---

func TestProposalService_ListForLaunch_ReturnsList(t *testing.T) {
	l := testLaunch()
	p1 := testProposal(l.ID)
	p2 := testProposal(l.ID)
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(p1, p2), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	got, total, err := svc.ListForLaunch(context.Background(), l.ID, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Len(t, got, 2)
}

// --- apply-path tests ---
// These go through the full Raise path on a 1-of-1 committee so the proposal
// auto-executes, exercising every apply* function via the public API.

func raiseWith(t *testing.T, svc *ProposalService, launchID uuid.UUID, action proposal.ActionType, payload any) (*proposal.Proposal, error) {
	t.Helper()
	raw, _ := json.Marshal(payload)
	return svc.Raise(context.Background(), launchID, RaiseInput{
		ActionType:      action,
		Payload:         raw,
		CoordinatorAddr: testAddr1,
		Nonce:           uuid.New().String(),
		Timestamp:       nowTS(),
		Signature:       testSig,
	})
}

func TestProposalService_applyApproveValidator_Success(t *testing.T) {
	l := test1of1Launch()
	l.Status = launch.StatusWindowOpen
	jr := makeJoinRequest(t, l.ID, testAddr2)
	jrRepo := newFakeJoinRequestRepo(jr)
	svc := newProposalSvc(newFakeLaunchRepo(l), jrRepo, newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	p, err := raiseWith(t, svc, l.ID, proposal.ActionApproveValidator, proposal.ApproveValidatorPayload{
		JoinRequestID:   jr.ID,
		OperatorAddress: testAddr2,
	})
	require.NoError(t, err)
	require.Equal(t, proposal.StatusExecuted, p.Status)
	assert.Equal(t, joinrequest.StatusApproved, jrRepo.data[jr.ID].Status, "join request should be APPROVED")
}

func TestProposalService_applyApproveValidator_JoinRequestNotFound(t *testing.T) {
	l := test1of1Launch()
	l.Status = launch.StatusWindowOpen
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionApproveValidator, proposal.ApproveValidatorPayload{
		JoinRequestID:   uuid.New(),
		OperatorAddress: testAddr2,
	})
	require.Error(t, err, "expected error when join request not found")
}

func TestProposalService_applyRejectValidator_Success(t *testing.T) {
	l := test1of1Launch()
	l.Status = launch.StatusWindowOpen
	jr := makeJoinRequest(t, l.ID, testAddr2)
	jrRepo := newFakeJoinRequestRepo(jr)
	svc := newProposalSvc(newFakeLaunchRepo(l), jrRepo, newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	p, err := raiseWith(t, svc, l.ID, proposal.ActionRejectValidator, proposal.RejectValidatorPayload{
		JoinRequestID:   jr.ID,
		OperatorAddress: testAddr2,
		Reason:          "test rejection",
	})
	require.NoError(t, err)
	require.Equal(t, proposal.StatusExecuted, p.Status)
	assert.Equal(t, joinrequest.StatusRejected, jrRepo.data[jr.ID].Status)
}

func TestProposalService_applyRemoveValidator_Success(t *testing.T) {
	l := test1of1Launch()
	l.Status = launch.StatusWindowOpen
	jr := makeJoinRequest(t, l.ID, testAddr2)
	require.NoError(t, jr.Approve(uuid.New()))
	jrRepo := newFakeJoinRequestRepo(jr)
	svc := newProposalSvc(newFakeLaunchRepo(l), jrRepo, newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	p, err := raiseWith(t, svc, l.ID, proposal.ActionRemoveApprovedValidator, proposal.RemoveApprovedValidatorPayload{
		JoinRequestID:   jr.ID,
		OperatorAddress: testAddr2,
		Reason:          "removed for test",
	})
	require.NoError(t, err)
	require.Equal(t, proposal.StatusExecuted, p.Status)
	assert.Equal(t, joinrequest.StatusRejected, jrRepo.data[jr.ID].Status, "want REJECTED after revoke")
}

func TestProposalService_applyRemoveValidator_WrongStatus(t *testing.T) {
	// REMOVE_APPROVED_VALIDATOR is only allowed in WINDOW_OPEN or WINDOW_CLOSED.
	l := test1of1Launch()
	l.Status = launch.StatusGenesisReady
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionRemoveApprovedValidator, proposal.RemoveApprovedValidatorPayload{
		JoinRequestID:   uuid.New(),
		OperatorAddress: testAddr2,
		Reason:          "should be blocked",
	})
	require.Error(t, err, "REMOVE_APPROVED_VALIDATOR not allowed at GENESIS_READY")
}

func TestProposalService_applyPublishGenesis_Success(t *testing.T) {
	l := test1of1Launch()
	l.Status = launch.StatusWindowClosed
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	p, err := raiseWith(t, svc, l.ID, proposal.ActionPublishGenesis, proposal.PublishGenesisPayload{
		GenesisHash: "deadbeef1234567890abcdef",
	})
	require.NoError(t, err)
	require.Equal(t, proposal.StatusExecuted, p.Status)
	assert.Equal(t, launch.StatusGenesisReady, l.Status)
}

func TestProposalService_applyPublishChainRecord_Success(t *testing.T) {
	l := test1of1Launch()
	l.InitialGenesisSHA256 = "deadbeef01"
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	p, err := raiseWith(t, svc, l.ID, proposal.ActionPublishChainRecord, proposal.PublishChainRecordPayload{
		InitialGenesisHash: "deadbeef01",
	})
	require.NoError(t, err)
	require.Equal(t, proposal.StatusExecuted, p.Status)
	assert.Equal(t, launch.StatusPublished, l.Status, "want PUBLISHED after publish-chain-record")
}

func TestProposalService_applyPublishChainRecord_HashMismatch(t *testing.T) {
	l := test1of1Launch()
	l.InitialGenesisSHA256 = "deadbeef01"
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionPublishChainRecord, proposal.PublishChainRecordPayload{
		InitialGenesisHash: "wronghash",
	})
	require.Error(t, err, "attested hash does not match uploaded hash")
}

func TestProposalService_applyPublishChainRecord_NoGenesisUploaded(t *testing.T) {
	l := test1of1Launch() // InitialGenesisSHA256 is empty
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionPublishChainRecord, proposal.PublishChainRecordPayload{
		InitialGenesisHash: "somehash",
	})
	require.Error(t, err, "genesis not uploaded")
}

func TestProposalService_applyUpdateGenesisTime_Success(t *testing.T) {
	l := test1of1Launch()
	l.Status = launch.StatusGenesisReady
	readinessRepo := newFakeReadinessRepo()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), readinessRepo, newFakeNonceStore(), &fakeVerifier{})

	newTime := time.Now().Add(48 * time.Hour).UTC()
	p, err := raiseWith(t, svc, l.ID, proposal.ActionUpdateGenesisTime, proposal.UpdateGenesisTimePayload{
		NewGenesisTime: newTime,
	})
	require.NoError(t, err)
	require.Equal(t, proposal.StatusExecuted, p.Status)
	require.NotNil(t, l.Record.GenesisTime)
	assert.True(t, l.Record.GenesisTime.Equal(newTime), "genesis time not updated: got %v, want %v", l.Record.GenesisTime, newTime)
}

func TestProposalService_applyUpdateGenesisTime_InvalidatesReadiness(t *testing.T) {
	l := test1of1Launch()
	l.Status = launch.StatusGenesisReady
	rc := &launch.ReadinessConfirmation{
		ID:              uuid.New(),
		LaunchID:        l.ID,
		OperatorAddress: mustAddr(testAddr2),
		ConfirmedAt:     time.Now().UTC(),
	}
	readinessRepo := newFakeReadinessRepo(rc)
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), readinessRepo, newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionUpdateGenesisTime, proposal.UpdateGenesisTimePayload{
		NewGenesisTime: time.Now().Add(48 * time.Hour).UTC(),
	})
	require.NoError(t, err)
	assert.False(t, readinessRepo.data[rc.ID].IsValid(), "readiness confirmation should have been invalidated")
}

func TestProposalService_applyUpdateGenesisTime_AfterLaunched(t *testing.T) {
	// UPDATE_GENESIS_TIME is blocked once the chain has LAUNCHED.
	l := test1of1Launch()
	l.Status = launch.StatusLaunched
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionUpdateGenesisTime, proposal.UpdateGenesisTimePayload{
		NewGenesisTime: time.Now().Add(48 * time.Hour).UTC(),
	})
	require.Error(t, err, "UPDATE_GENESIS_TIME not allowed at LAUNCHED")
}

// ---- ApproveAllocationFile -------------------------------------------------

func TestProposalService_applyApproveAllocationFile_Success(t *testing.T) {
	l := test1of1Launch()
	require.NoError(t, l.UploadAllocationFile(launch.AllocationClaims, allocHashA))
	lRepo := newFakeLaunchRepo(l)
	audit := &fakeAuditLogWriter{}
	svc := NewProposalService(lRepo, newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(),
		newFakeNonceStore(), &fakeVerifier{}, &fakeEventPublisher{}, audit, &fakeTransactor{})

	p, err := raiseWith(t, svc, l.ID, proposal.ActionApproveAllocationFile, proposal.ApproveAllocationFilePayload{
		Type: string(launch.AllocationClaims),
		Hash: allocHashA,
	})
	require.NoError(t, err)
	require.Equal(t, proposal.StatusExecuted, p.Status)

	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	f, ok := stored.AllocationFileOf(launch.AllocationClaims)
	require.True(t, ok)
	assert.Equal(t, launch.AllocationApproved, f.Status)
	require.NotNil(t, f.ApprovedByProposal)
	assert.Equal(t, p.ID, *f.ApprovedByProposal)
	assert.True(t, hasAuditEvent(audit.events, "AllocationFileApproved"), "approval not audited")
}

func TestProposalService_applyApproveAllocationFile_StaleHash(t *testing.T) {
	// File uploaded with allocHashA, but the proposal approves allocHashB → stale, rejected.
	l := test1of1Launch()
	require.NoError(t, l.UploadAllocationFile(launch.AllocationClaims, allocHashA))
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionApproveAllocationFile, proposal.ApproveAllocationFilePayload{
		Type: string(launch.AllocationClaims),
		Hash: allocHashB,
	})
	require.Error(t, err, "expected stale-hash error approving a hash that no longer matches")
}

func TestProposalService_applyApproveAllocationFile_NotFound(t *testing.T) {
	// No allocation file uploaded for the type → approval fails.
	l := test1of1Launch()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionApproveAllocationFile, proposal.ApproveAllocationFilePayload{
		Type: string(launch.AllocationAccounts),
		Hash: allocHashA,
	})
	require.Error(t, err, "expected error approving a type with no uploaded file")
}

func TestProposalService_applyAllocationVeto_RejectsFile(t *testing.T) {
	// 2-of-3 committee so the proposal stays pending until a veto arrives.
	l := testLaunch()
	require.NoError(t, l.UploadAllocationFile(launch.AllocationClaims, allocHashA))
	lRepo := newFakeLaunchRepo(l)
	audit := &fakeAuditLogWriter{}
	svc := NewProposalService(lRepo, newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(),
		newFakeNonceStore(), &fakeVerifier{}, &fakeEventPublisher{}, audit, &fakeTransactor{})

	p, err := raiseWith(t, svc, l.ID, proposal.ActionApproveAllocationFile, proposal.ApproveAllocationFilePayload{
		Type: string(launch.AllocationClaims),
		Hash: allocHashA,
	})
	require.NoError(t, err)
	require.Equal(t, proposal.StatusPendingSignatures, p.Status)

	p2, err := svc.Sign(context.Background(), l.ID, p.ID, SignInput{
		CoordinatorAddr: testAddr2,
		Decision:        proposal.DecisionVeto,
		Nonce:           uuid.New().String(),
		Timestamp:       nowTS(),
		Signature:       testSig,
	})
	require.NoError(t, err)
	require.Equal(t, proposal.StatusVetoed, p2.Status)

	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	f, _ := stored.AllocationFileOf(launch.AllocationClaims)
	assert.Equal(t, launch.AllocationRejected, f.Status, "veto should mark the file REJECTED")
	assert.True(t, hasAuditEvent(audit.events, "AllocationFileRejected"), "rejection not audited")
}

func TestProposalService_applyAllocationVeto_StaleNoop(t *testing.T) {
	// A veto landing after the file was re-uploaded must not touch the new file.
	l := testLaunch()
	require.NoError(t, l.UploadAllocationFile(launch.AllocationClaims, allocHashA))
	lRepo := newFakeLaunchRepo(l)
	audit := &fakeAuditLogWriter{}
	svc := NewProposalService(lRepo, newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(),
		newFakeNonceStore(), &fakeVerifier{}, &fakeEventPublisher{}, audit, &fakeTransactor{})

	p, err := raiseWith(t, svc, l.ID, proposal.ActionApproveAllocationFile, proposal.ApproveAllocationFilePayload{
		Type: string(launch.AllocationClaims),
		Hash: allocHashA,
	})
	require.NoError(t, err)

	// Re-upload with a different hash; the pending approve/veto proposal is now stale.
	require.NoError(t, l.UploadAllocationFile(launch.AllocationClaims, allocHashB))

	_, err = svc.Sign(context.Background(), l.ID, p.ID, SignInput{
		CoordinatorAddr: testAddr2,
		Decision:        proposal.DecisionVeto,
		Nonce:           uuid.New().String(),
		Timestamp:       nowTS(),
		Signature:       testSig,
	})
	require.NoError(t, err)

	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	f, _ := stored.AllocationFileOf(launch.AllocationClaims)
	assert.Equal(t, launch.AllocationPending, f.Status, "stale veto should leave the re-uploaded file PENDING")
	assert.Equal(t, allocHashB, f.SHA256)
	assert.False(t, hasAuditEvent(audit.events, "AllocationFileRejected"), "stale veto should not emit a rejection")
}

func TestProposalService_ApplyReplaceCommitteeMember_Success(t *testing.T) {
	l := test1of1Launch()
	lRepo := newFakeLaunchRepo(l)
	svc := newProposalSvc(lRepo, newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	originalAddr := l.Committee.Members[0].Address.String()

	_, err := raiseWith(t, svc, l.ID, proposal.ActionReplaceCommitteeMember, proposal.ReplaceCommitteeMemberPayload{
		OldAddress: originalAddr,
		NewAddress: testAddr2,
		NewMoniker: "new-member",
		NewPubKey:  "AAEC",
	})
	require.NoError(t, err)
	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	assert.Equal(t, testAddr2, stored.Committee.Members[0].Address.String(), "committee member not replaced")
}

func TestProposalService_ApplyReplaceCommitteeMember_OldNotFound(t *testing.T) {
	l := test1of1Launch()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionReplaceCommitteeMember, proposal.ReplaceCommitteeMemberPayload{
		OldAddress: testAddr3,
		NewAddress: testAddr2,
		NewMoniker: "new",
		NewPubKey:  "AAEC",
	})
	require.Error(t, err, "old address not in committee")
}

func TestProposalService_applyReviseGenesis_Success(t *testing.T) {
	l := test1of1Launch()
	l.Status = launch.StatusGenesisReady
	l.FinalGenesisSHA256 = "deadbeef"
	lRepo := newFakeLaunchRepo(l)
	svc := newProposalSvc(lRepo, newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	p, err := raiseWith(t, svc, l.ID, proposal.ActionReviseGenesis, proposal.ReviseGenesisPayload{})
	require.NoError(t, err)
	require.Equal(t, proposal.StatusExecuted, p.Status)
	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	assert.Equal(t, launch.StatusWindowClosed, stored.Status)
	assert.Empty(t, stored.FinalGenesisSHA256, "want FinalGenesisSHA256 cleared")
}

func TestProposalService_applyReviseGenesis_InvalidatesReadiness(t *testing.T) {
	l := test1of1Launch()
	l.Status = launch.StatusGenesisReady
	l.FinalGenesisSHA256 = "deadbeef"
	rc := &launch.ReadinessConfirmation{
		ID:              uuid.New(),
		LaunchID:        l.ID,
		OperatorAddress: mustAddr(testAddr2),
		ConfirmedAt:     time.Now().UTC(),
	}
	readinessRepo := newFakeReadinessRepo(rc)
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), readinessRepo, newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionReviseGenesis, proposal.ReviseGenesisPayload{})
	require.NoError(t, err)
	assert.False(t, readinessRepo.data[rc.ID].IsValid(), "readiness confirmation should have been invalidated")
}

func TestProposalService_applyReviseGenesis_WrongStatus(t *testing.T) {
	// ReopenForRevision requires GENESIS_READY; WINDOW_CLOSED must be rejected.
	l := test1of1Launch()
	l.Status = launch.StatusWindowClosed
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionReviseGenesis, proposal.ReviseGenesisPayload{})
	require.Error(t, err, "expected error when launch is not in GENESIS_READY")
}

func TestProposalService_ApplyReplaceCommitteeMember_LeadReplaced(t *testing.T) {
	l := test1of1Launch()
	lRepo := newFakeLaunchRepo(l)
	svc := newProposalSvc(lRepo, newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	leadAddr := l.Committee.LeadAddress.String()
	_, err := raiseWith(t, svc, l.ID, proposal.ActionReplaceCommitteeMember, proposal.ReplaceCommitteeMemberPayload{
		OldAddress: leadAddr,
		NewAddress: testAddr2,
		NewMoniker: "new-lead",
		NewPubKey:  "AAEC",
	})
	require.NoError(t, err)
	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	assert.Equal(t, testAddr2, stored.Committee.LeadAddress.String(), "lead address not updated")
}

// ---- ExpandCommittee --------------------------------------------------------

func TestProposalService_ApplyExpandCommittee_DefaultThreshold(t *testing.T) {
	// 1-of-1 committee; expand with nil threshold → effective M stays 1 → 1-of-2.
	l := test1of1Launch()
	lRepo := newFakeLaunchRepo(l)
	svc := newProposalSvc(lRepo, newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionExpandCommittee, proposal.ExpandCommitteePayload{
		NewMember: proposal.CommitteeMemberSpec{
			Address:   testAddr2,
			Moniker:   "coord-2",
			PubKeyB64: "BBBB",
		},
	})
	require.NoError(t, err)
	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	assert.Equal(t, 2, stored.Committee.TotalN)
	assert.Equal(t, 1, stored.Committee.ThresholdM)
	found := false
	for _, m := range stored.Committee.Members {
		if m.Address.String() == testAddr2 {
			found = true
		}
	}
	assert.True(t, found, "new member not found in stored committee")
}

func TestProposalService_ApplyExpandCommittee_ExplicitThreshold(t *testing.T) {
	// 1-of-1 → expand with explicit threshold 1 → 1-of-2.
	l := test1of1Launch()
	lRepo := newFakeLaunchRepo(l)
	m := 1
	svc := newProposalSvc(lRepo, newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionExpandCommittee, proposal.ExpandCommitteePayload{
		NewMember: proposal.CommitteeMemberSpec{
			Address:   testAddr2,
			Moniker:   "coord-2",
			PubKeyB64: "BBBB",
		},
		NewThresholdM: &m,
	})
	require.NoError(t, err)
	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	assert.Equal(t, 1, stored.Committee.ThresholdM)
}

func TestProposalService_ApplyExpandCommittee_ExpiresPendingProposals(t *testing.T) {
	l := test1of1Launch()
	pending := testProposal(l.ID)
	pRepo := newFakeProposalRepo(pending)
	lRepo := newFakeLaunchRepo(l)
	svc := newProposalSvc(lRepo, newFakeJoinRequestRepo(), pRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionExpandCommittee, proposal.ExpandCommitteePayload{
		NewMember: proposal.CommitteeMemberSpec{
			Address:   testAddr2,
			Moniker:   "coord-2",
			PubKeyB64: "BBBB",
		},
	})
	require.NoError(t, err)
	// The pending CloseWindow proposal must be EXPIRED; the expand proposal itself is EXECUTED.
	assert.Equal(t, proposal.StatusExpired, pRepo.data[pending.ID].Status, "pre-existing pending proposal should be EXPIRED")
}

func TestProposalService_ApplyExpandCommittee_DuplicateMember(t *testing.T) {
	l := test1of1Launch()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	// testAddr1 is already a member.
	_, err := raiseWith(t, svc, l.ID, proposal.ActionExpandCommittee, proposal.ExpandCommitteePayload{
		NewMember: proposal.CommitteeMemberSpec{
			Address:   testAddr1,
			Moniker:   "dup",
			PubKeyB64: "AAAA",
		},
	})
	require.Error(t, err, "duplicate member address")
}

// ---- ShrinkCommittee --------------------------------------------------------

func TestProposalService_ApplyShrinkCommittee_DefaultThreshold(t *testing.T) {
	// 1-of-3 committee; shrink with nil threshold: currentM=1, newN=2 → clamp(1,[1,1])=1 → 1-of-2.
	l := test1of3Launch()
	lRepo := newFakeLaunchRepo(l)
	svc := newProposalSvc(lRepo, newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionShrinkCommittee, proposal.ShrinkCommitteePayload{
		RemoveAddress: testAddr3,
	})
	require.NoError(t, err)
	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	assert.Equal(t, 2, stored.Committee.TotalN)
	assert.Equal(t, 1, stored.Committee.ThresholdM)
	for _, m := range stored.Committee.Members {
		assert.NotEqual(t, testAddr3, m.Address.String(), "removed member still present in committee")
	}
}

// ---- ResolveThreshold -------------------------------------------------------

func TestResolveThreshold(t *testing.T) {
	ptr := func(n int) *int { return &n }
	cases := []struct {
		name     string
		currentM int
		newN     int
		override *int
		want     int
	}{
		// Override takes precedence.
		{"explicit override", 2, 4, ptr(3), 3},
		{"explicit override min", 2, 4, ptr(1), 1},
		// No override: keep currentM when it fits.
		{"keep current M, expand", 2, 4, nil, 2},
		{"keep current M, shrink fits", 1, 3, nil, 1},
		// No override: clamp when M >= newN (e.g. 2-of-3 shrinking to 2).
		{"clamp on shrink", 2, 2, nil, 1},
		// No override: floor at 1.
		{"clamp floor", 1, 1, nil, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveThreshold(tc.currentM, tc.newN, tc.override)
			assert.Equal(t, tc.want, got, "ResolveThreshold(%d, %d, %v)", tc.currentM, tc.newN, tc.override)
		})
	}
}

func TestProposalService_ApplyShrinkCommittee_ExplicitThreshold(t *testing.T) {
	l := test1of3Launch()
	lRepo := newFakeLaunchRepo(l)
	m := 1
	svc := newProposalSvc(lRepo, newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionShrinkCommittee, proposal.ShrinkCommitteePayload{
		RemoveAddress: testAddr3,
		NewThresholdM: &m,
	})
	require.NoError(t, err)
	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	assert.Equal(t, 1, stored.Committee.ThresholdM)
}

func TestProposalService_ApplyShrinkCommittee_ExpiresPendingProposals(t *testing.T) {
	l := test1of3Launch()
	pending := testProposal(l.ID)
	pRepo := newFakeProposalRepo(pending)
	lRepo := newFakeLaunchRepo(l)
	svc := newProposalSvc(lRepo, newFakeJoinRequestRepo(), pRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionShrinkCommittee, proposal.ShrinkCommitteePayload{
		RemoveAddress: testAddr3,
	})
	require.NoError(t, err)
	assert.Equal(t, proposal.StatusExpired, pRepo.data[pending.ID].Status, "pre-existing pending proposal should be EXPIRED")
}

func TestProposalService_ApplyShrinkCommittee_MemberNotFound(t *testing.T) {
	// Removing an address that is not in the 1-of-3 committee must fail.
	const unknownAddr = "cosmos1sxpg8py9s6rc3zv23wxgmr50jzge9yu5r5slya"
	l := test1of3Launch()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionShrinkCommittee, proposal.ShrinkCommitteePayload{
		RemoveAddress: unknownAddr,
	})
	require.Error(t, err, "remove_address not in committee")
}
