package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/joinrequest"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/proposal"
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
	if err == nil {
		t.Fatal("expected error for nonce conflict")
	}
}

func TestProposalService_Raise_BadTimestamp(t *testing.T) {
	l := testLaunch()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	input := validRaiseInput(l)
	input.Timestamp = expiredTS()
	_, err := svc.Raise(context.Background(), l.ID, input)
	if err == nil {
		t.Fatal("expected error for expired timestamp")
	}
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
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
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
	if !errors.Is(err, ports.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestProposalService_Raise_SigFails(t *testing.T) {
	l := testLaunch()
	verifier := &fakeVerifier{err: ports.ErrUnauthorized}
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), verifier)

	_, err := svc.Raise(context.Background(), l.ID, validRaiseInput(l))
	if err == nil {
		t.Fatal("expected error when signature verification fails")
	}
}

func TestProposalService_Raise_StaysPending(t *testing.T) {
	// 2-of-3 committee: one signature (the proposer's) is not enough to execute.
	l := testLaunch() // 2-of-3
	propRepo := newFakeProposalRepo()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), propRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	p, err := svc.Raise(context.Background(), l.ID, validRaiseInput(l))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status != proposal.StatusPendingSignatures {
		t.Errorf("want PENDING_SIGNATURES, got %s", p.Status)
	}
	if _, ok := propRepo.data[p.ID]; !ok {
		t.Fatal("proposal not persisted")
	}
}

func TestProposalService_Raise_1of1ExecutesImmediately(t *testing.T) {
	// 1-of-1 committee: the proposer's single signature executes the proposal.
	l := test1of1Launch()
	l.Status = launch.StatusWindowOpen // CloseWindow requires WINDOW_OPEN

	propRepo := newFakeProposalRepo()
	// Add one approved validator so CloseWindow passes the MinValidatorCount=1 check.
	jr := makeJoinRequest(t, l.ID, testAddr2)
	if err := jr.Approve(uuid.New()); err != nil {
		t.Fatalf("approve: %v", err)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Status != proposal.StatusExecuted {
		t.Errorf("want EXECUTED for 1-of-1 committee, got %s", p.Status)
	}
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
	if err == nil {
		t.Fatal("expected error for nonce conflict")
	}
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
	if !errors.Is(err, ports.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
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
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want ErrNotFound when proposal belongs to different launch, got %v", err)
	}
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
	if err != nil {
		t.Fatalf("Raise: %v", err)
	}
	if p.Status != proposal.StatusPendingSignatures {
		t.Fatalf("want PENDING after raise, got %s", p.Status)
	}

	// Sign as testAddr2.
	p2, err := svc.Sign(context.Background(), l.ID, p.ID, SignInput{
		CoordinatorAddr: testAddr2,
		Decision:        proposal.DecisionSign,
		Nonce:           uuid.New().String(),
		Timestamp:       nowTS(),
		Signature:       testSig,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if p2.Status != proposal.StatusPendingSignatures {
		t.Errorf("want still PENDING after second of three, got %s", p2.Status)
	}
	if p2.SignCount() != 2 {
		t.Errorf("want 2 signatures, got %d", p2.SignCount())
	}
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

	if err := svc.ExpireStale(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stored := propRepo.data[p.ID]
	if stored.Status != proposal.StatusExpired {
		t.Errorf("want EXPIRED, got %s", stored.Status)
	}
}

func TestProposalService_ExpireStale_SkipsFresh(t *testing.T) {
	l := testLaunch()
	propRepo := newFakeProposalRepo()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), propRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	payload, _ := json.Marshal(proposal.CloseApplicationWindowPayload{})
	p, _ := proposal.New(uuid.New(), l.ID, proposal.ActionCloseApplicationWindow, payload,
		mustAddr(testAddr1), mustSig(), 48*time.Hour, time.Now())
	propRepo.data[p.ID] = p

	if err := svc.ExpireStale(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if propRepo.data[p.ID].Status != proposal.StatusPendingSignatures {
		t.Errorf("fresh proposal should not be expired")
	}
}

// --- GetByID ---

func TestProposalService_GetByID_WrongLaunch(t *testing.T) {
	p := testProposal(uuid.New())
	svc := newProposalSvc(newFakeLaunchRepo(), newFakeJoinRequestRepo(), newFakeProposalRepo(p), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := svc.GetByID(context.Background(), uuid.New(), p.ID) // wrong launchID
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestProposalService_GetByID_Success(t *testing.T) {
	l := testLaunch()
	p := testProposal(l.ID)
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(p), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	got, err := svc.GetByID(context.Background(), l.ID, p.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != p.ID {
		t.Errorf("ID mismatch")
	}
}

// --- ListForLaunch ---

func TestProposalService_ListForLaunch_ReturnsList(t *testing.T) {
	l := testLaunch()
	p1 := testProposal(l.ID)
	p2 := testProposal(l.ID)
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(p1, p2), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	got, total, err := svc.ListForLaunch(context.Background(), l.ID, 1, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 2 || len(got) != 2 {
		t.Errorf("expected 2 proposals, got total=%d len=%d", total, len(got))
	}
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
	if err != nil {
		t.Fatalf("Raise: %v", err)
	}
	if p.Status != proposal.StatusExecuted {
		t.Fatalf("want EXECUTED, got %s", p.Status)
	}
	if jrRepo.data[jr.ID].Status != joinrequest.StatusApproved {
		t.Error("join request should be APPROVED")
	}
}

func TestProposalService_applyApproveValidator_JoinRequestNotFound(t *testing.T) {
	l := test1of1Launch()
	l.Status = launch.StatusWindowOpen
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionApproveValidator, proposal.ApproveValidatorPayload{
		JoinRequestID:   uuid.New(),
		OperatorAddress: testAddr2,
	})
	if err == nil {
		t.Fatal("expected error when join request not found")
	}
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
	if err != nil {
		t.Fatalf("Raise: %v", err)
	}
	if p.Status != proposal.StatusExecuted {
		t.Fatalf("want EXECUTED, got %s", p.Status)
	}
	if jrRepo.data[jr.ID].Status != joinrequest.StatusRejected {
		t.Errorf("want REJECTED, got %s", jrRepo.data[jr.ID].Status)
	}
}

func TestProposalService_applyRemoveValidator_Success(t *testing.T) {
	l := test1of1Launch()
	l.Status = launch.StatusWindowOpen
	jr := makeJoinRequest(t, l.ID, testAddr2)
	if err := jr.Approve(uuid.New()); err != nil {
		t.Fatal(err)
	}
	jrRepo := newFakeJoinRequestRepo(jr)
	svc := newProposalSvc(newFakeLaunchRepo(l), jrRepo, newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	p, err := raiseWith(t, svc, l.ID, proposal.ActionRemoveApprovedValidator, proposal.RemoveApprovedValidatorPayload{
		JoinRequestID:   jr.ID,
		OperatorAddress: testAddr2,
		Reason:          "removed for test",
	})
	if err != nil {
		t.Fatalf("Raise: %v", err)
	}
	if p.Status != proposal.StatusExecuted {
		t.Fatalf("want EXECUTED, got %s", p.Status)
	}
	if jrRepo.data[jr.ID].Status != joinrequest.StatusRejected {
		t.Errorf("want REJECTED after revoke, got %s", jrRepo.data[jr.ID].Status)
	}
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
	if err == nil {
		t.Fatal("expected error: REMOVE_APPROVED_VALIDATOR not allowed at GENESIS_READY")
	}
}

func TestProposalService_applyPublishGenesis_Success(t *testing.T) {
	l := test1of1Launch()
	l.Status = launch.StatusWindowClosed
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	p, err := raiseWith(t, svc, l.ID, proposal.ActionPublishGenesis, proposal.PublishGenesisPayload{
		GenesisHash: "deadbeef1234567890abcdef",
	})
	if err != nil {
		t.Fatalf("Raise: %v", err)
	}
	if p.Status != proposal.StatusExecuted {
		t.Fatalf("want EXECUTED, got %s", p.Status)
	}
	if l.Status != launch.StatusGenesisReady {
		t.Errorf("want GENESIS_READY, got %s", l.Status)
	}
}

func TestProposalService_applyPublishChainRecord_Success(t *testing.T) {
	l := test1of1Launch()
	l.InitialGenesisSHA256 = "deadbeef01"
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	p, err := raiseWith(t, svc, l.ID, proposal.ActionPublishChainRecord, proposal.PublishChainRecordPayload{
		InitialGenesisHash: "deadbeef01",
	})
	if err != nil {
		t.Fatalf("Raise: %v", err)
	}
	if p.Status != proposal.StatusExecuted {
		t.Fatalf("want EXECUTED, got %s", p.Status)
	}
	got, _ := newFakeLaunchRepo(l).FindByID(context.Background(), l.ID)
	_ = got
	if l.Status != launch.StatusPublished {
		t.Errorf("want PUBLISHED after publish-chain-record, got %s", l.Status)
	}
}

func TestProposalService_applyPublishChainRecord_HashMismatch(t *testing.T) {
	l := test1of1Launch()
	l.InitialGenesisSHA256 = "deadbeef01"
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionPublishChainRecord, proposal.PublishChainRecordPayload{
		InitialGenesisHash: "wronghash",
	})
	if err == nil {
		t.Fatal("expected error: attested hash does not match uploaded hash")
	}
}

func TestProposalService_applyPublishChainRecord_NoGenesisUploaded(t *testing.T) {
	l := test1of1Launch() // InitialGenesisSHA256 is empty
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionPublishChainRecord, proposal.PublishChainRecordPayload{
		InitialGenesisHash: "somehash",
	})
	if err == nil {
		t.Fatal("expected error: genesis not uploaded")
	}
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
	if err != nil {
		t.Fatalf("Raise: %v", err)
	}
	if p.Status != proposal.StatusExecuted {
		t.Fatalf("want EXECUTED, got %s", p.Status)
	}
	if l.Record.GenesisTime == nil || !l.Record.GenesisTime.Equal(newTime) {
		t.Errorf("genesis time not updated: got %v, want %v", l.Record.GenesisTime, newTime)
	}
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
	if err != nil {
		t.Fatalf("Raise: %v", err)
	}
	if readinessRepo.data[rc.ID].IsValid() {
		t.Error("readiness confirmation should have been invalidated")
	}
}

func TestProposalService_applyUpdateGenesisTime_AfterLaunched(t *testing.T) {
	// UPDATE_GENESIS_TIME is blocked once the chain has LAUNCHED.
	l := test1of1Launch()
	l.Status = launch.StatusLaunched
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionUpdateGenesisTime, proposal.UpdateGenesisTimePayload{
		NewGenesisTime: time.Now().Add(48 * time.Hour).UTC(),
	})
	if err == nil {
		t.Fatal("expected error: UPDATE_GENESIS_TIME not allowed at LAUNCHED")
	}
}

func TestProposalService_ApplyAddGenesisAccount_Success(t *testing.T) {
	l := test1of1Launch()
	lRepo := newFakeLaunchRepo(l)
	svc := newProposalSvc(lRepo, newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionAddGenesisAccount, proposal.AddGenesisAccountPayload{
		Address: testAddr2,
		Amount:  "1000utest",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	if len(stored.GenesisAccounts) != 1 || stored.GenesisAccounts[0].Address != testAddr2 {
		t.Errorf("genesis account not persisted: %+v", stored.GenesisAccounts)
	}
}

func TestProposalService_ApplyAddGenesisAccount_Duplicate(t *testing.T) {
	l := test1of1Launch()
	l.GenesisAccounts = []launch.GenesisAccount{{Address: testAddr2, Amount: "500utest"}}
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionAddGenesisAccount, proposal.AddGenesisAccountPayload{
		Address: testAddr2,
		Amount:  "1000utest",
	})
	if err == nil {
		t.Fatal("expected error for duplicate genesis account")
	}
}

func TestProposalService_ApplyRemoveGenesisAccount_Success(t *testing.T) {
	l := test1of1Launch()
	l.GenesisAccounts = []launch.GenesisAccount{{Address: testAddr2, Amount: "1000utest"}}
	lRepo := newFakeLaunchRepo(l)
	svc := newProposalSvc(lRepo, newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionRemoveGenesisAccount, proposal.RemoveGenesisAccountPayload{
		Address: testAddr2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	if len(stored.GenesisAccounts) != 0 {
		t.Errorf("genesis account not removed: %+v", stored.GenesisAccounts)
	}
}

func TestProposalService_ApplyRemoveGenesisAccount_NotFound(t *testing.T) {
	l := test1of1Launch()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionRemoveGenesisAccount, proposal.RemoveGenesisAccountPayload{
		Address: testAddr2,
	})
	if err == nil {
		t.Fatal("expected error removing non-existent genesis account")
	}
}

func TestProposalService_ApplyModifyGenesisAccount_Success(t *testing.T) {
	l := test1of1Launch()
	l.GenesisAccounts = []launch.GenesisAccount{{Address: testAddr2, Amount: "1000utest"}}
	lRepo := newFakeLaunchRepo(l)
	svc := newProposalSvc(lRepo, newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	newAmt := "9999utest"
	_, err := raiseWith(t, svc, l.ID, proposal.ActionModifyGenesisAccount, proposal.ModifyGenesisAccountPayload{
		Address: testAddr2,
		Amount:  newAmt,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	if len(stored.GenesisAccounts) != 1 || stored.GenesisAccounts[0].Amount != newAmt {
		t.Errorf("genesis account not updated: %+v", stored.GenesisAccounts)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	if stored.Committee.Members[0].Address.String() != testAddr2 {
		t.Errorf("committee member not replaced: got %s", stored.Committee.Members[0].Address)
	}
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
	if err == nil {
		t.Fatal("expected error: old address not in committee")
	}
}

func TestProposalService_applyReviseGenesis_Success(t *testing.T) {
	l := test1of1Launch()
	l.Status = launch.StatusGenesisReady
	l.FinalGenesisSHA256 = "deadbeef"
	lRepo := newFakeLaunchRepo(l)
	svc := newProposalSvc(lRepo, newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	p, err := raiseWith(t, svc, l.ID, proposal.ActionReviseGenesis, proposal.ReviseGenesisPayload{})
	if err != nil {
		t.Fatalf("Raise: %v", err)
	}
	if p.Status != proposal.StatusExecuted {
		t.Fatalf("want EXECUTED, got %s", p.Status)
	}
	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	if stored.Status != launch.StatusWindowClosed {
		t.Errorf("want WINDOW_CLOSED, got %s", stored.Status)
	}
	if stored.FinalGenesisSHA256 != "" {
		t.Errorf("want FinalGenesisSHA256 cleared, got %q", stored.FinalGenesisSHA256)
	}
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
	if err != nil {
		t.Fatalf("Raise: %v", err)
	}
	if readinessRepo.data[rc.ID].IsValid() {
		t.Error("readiness confirmation should have been invalidated")
	}
}

func TestProposalService_applyReviseGenesis_WrongStatus(t *testing.T) {
	// ReopenForRevision requires GENESIS_READY; WINDOW_CLOSED must be rejected.
	l := test1of1Launch()
	l.Status = launch.StatusWindowClosed
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionReviseGenesis, proposal.ReviseGenesisPayload{})
	if err == nil {
		t.Fatal("expected error when launch is not in GENESIS_READY")
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	if stored.Committee.LeadAddress.String() != testAddr2 {
		t.Errorf("lead address not updated: got %s", stored.Committee.LeadAddress)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	if stored.Committee.TotalN != 2 {
		t.Errorf("TotalN: want 2, got %d", stored.Committee.TotalN)
	}
	if stored.Committee.ThresholdM != 1 {
		t.Errorf("ThresholdM: want 1, got %d", stored.Committee.ThresholdM)
	}
	found := false
	for _, m := range stored.Committee.Members {
		if m.Address.String() == testAddr2 {
			found = true
		}
	}
	if !found {
		t.Error("new member not found in stored committee")
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	if stored.Committee.ThresholdM != 1 {
		t.Errorf("ThresholdM: want 1, got %d", stored.Committee.ThresholdM)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The pending CloseWindow proposal must be EXPIRED; the expand proposal itself is EXECUTED.
	stored := pRepo.data[pending.ID]
	if stored.Status != proposal.StatusExpired {
		t.Errorf("pre-existing pending proposal: want EXPIRED, got %s", stored.Status)
	}
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
	if err == nil {
		t.Fatal("expected error: duplicate member address")
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	if stored.Committee.TotalN != 2 {
		t.Errorf("TotalN: want 2, got %d", stored.Committee.TotalN)
	}
	if stored.Committee.ThresholdM != 1 {
		t.Errorf("ThresholdM: want 1, got %d", stored.Committee.ThresholdM)
	}
	for _, m := range stored.Committee.Members {
		if m.Address.String() == testAddr3 {
			t.Error("removed member still present in committee")
		}
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
			if got != tc.want {
				t.Errorf("ResolveThreshold(%d, %d, %v): want %d, got %d", tc.currentM, tc.newN, tc.override, tc.want, got)
			}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	if stored.Committee.ThresholdM != 1 {
		t.Errorf("ThresholdM: want 1, got %d", stored.Committee.ThresholdM)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stored := pRepo.data[pending.ID]
	if stored.Status != proposal.StatusExpired {
		t.Errorf("pre-existing pending proposal: want EXPIRED, got %s", stored.Status)
	}
}

func TestProposalService_ApplyShrinkCommittee_MemberNotFound(t *testing.T) {
	l := test1of3Launch()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	// testAddr1 is the proposer (committee member); testAddr2/testAddr3 are also members;
	// use an address not in the committee.
	_, err := raiseWith(t, svc, l.ID, proposal.ActionShrinkCommittee, proposal.ShrinkCommitteePayload{
		RemoveAddress: testAddr1, // removing the proposer/lead is allowed by domain, but use a non-existent one
	})
	// testAddr1 IS in the committee, so this succeeds. Instead test with an unlisted address via
	// a direct address that's not in testCommittee(1,3).
	_ = err

	// Use cosmos1sxpg8py9s6rc3zv23wxgmr50jzge9yu5r5slya which is not in the 1-of-3 committee.
	const unknownAddr = "cosmos1sxpg8py9s6rc3zv23wxgmr50jzge9yu5r5slya"
	l2 := test1of3Launch()
	svc2 := newProposalSvc(newFakeLaunchRepo(l2), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})
	_, err = raiseWith(t, svc2, l2.ID, proposal.ActionShrinkCommittee, proposal.ShrinkCommitteePayload{
		RemoveAddress: unknownAddr,
	})
	if err == nil {
		t.Fatal("expected error: remove_address not in committee")
	}
}
