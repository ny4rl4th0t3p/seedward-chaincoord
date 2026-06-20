package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/joinrequest"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

func newReadinessSvc(
	launchRepo *fakeLaunchRepo,
	jrRepo *fakeJoinRequestRepo,
	readinessRepo *fakeReadinessRepo,
	nonces *fakeNonceStore,
	verifier *fakeVerifier,
) *ReadinessService {
	return NewReadinessService(launchRepo, jrRepo, readinessRepo, nonces, verifier)
}

// genesisReadyLaunch returns a launch in GENESIS_READY status with known hashes.
func genesisReadyLaunch() *launch.Launch {
	l := testLaunch()
	l.Status = launch.StatusGenesisReady
	l.FinalGenesisSHA256 = "finalhash"
	l.Record.BinarySHA256 = "abc123"
	return l
}

func validConfirmInput(l *launch.Launch) ConfirmInput {
	return ConfirmInput{
		OperatorAddress:      testAddr1,
		GenesisHashConfirmed: l.FinalGenesisSHA256,
		BinaryHashConfirmed:  l.Record.BinarySHA256,
		Nonce:                uuid.New().String(),
		Timestamp:            nowTS(),
		Signature:            testSig,
	}
}

// approvedJoinRequest returns a join request in APPROVED status for the given launch and addr.
func approvedJoinRequest(t *testing.T, launchID uuid.UUID, addr string) *joinrequest.JoinRequest {
	t.Helper()
	jr := makeJoinRequest(t, launchID, addr)
	if err := jr.Approve(uuid.New()); err != nil {
		t.Fatalf("approvedJoinRequest: %v", err)
	}
	return jr
}

// --- Confirm ---

func TestReadinessService_Confirm_NonceConflict(t *testing.T) {
	l := genesisReadyLaunch()
	nonces := newFakeNonceStore()
	nonces.consumeErr = ports.ErrConflict
	svc := newReadinessSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeReadinessRepo(), nonces, &fakeVerifier{})

	_, err := svc.Confirm(context.Background(), l.ID, validConfirmInput(l))
	if err == nil {
		t.Fatal("expected error for nonce conflict")
	}
}

func TestReadinessService_Confirm_BadTimestamp(t *testing.T) {
	l := genesisReadyLaunch()
	svc := newReadinessSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	input := validConfirmInput(l)
	input.Timestamp = expiredTS()
	_, err := svc.Confirm(context.Background(), l.ID, input)
	if err == nil {
		t.Fatal("expected error for expired timestamp")
	}
}

func TestReadinessService_Confirm_SigFails(t *testing.T) {
	l := genesisReadyLaunch()
	verifier := &fakeVerifier{err: ports.ErrUnauthorized}
	svc := newReadinessSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeReadinessRepo(), newFakeNonceStore(), verifier)

	_, err := svc.Confirm(context.Background(), l.ID, validConfirmInput(l))
	if err == nil {
		t.Fatal("expected error for invalid signature")
	}
}

func TestReadinessService_Confirm_LaunchNotGenesisReady(t *testing.T) {
	l := testLaunch() // DRAFT
	svc := newReadinessSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := svc.Confirm(context.Background(), l.ID, validConfirmInput(l))
	if err == nil {
		t.Fatal("expected error: launch not in GENESIS_READY")
	}
}

func TestReadinessService_Confirm_NoApprovedJoinRequest(t *testing.T) {
	l := genesisReadyLaunch()
	// No join requests stored → FindByOperator returns ErrNotFound → ErrForbidden.
	svc := newReadinessSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := svc.Confirm(context.Background(), l.ID, validConfirmInput(l))
	if !errors.Is(err, ports.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestReadinessService_Confirm_JoinRequestNotApproved(t *testing.T) {
	l := genesisReadyLaunch()
	jr := makeJoinRequest(t, l.ID, testAddr1) // PENDING, not APPROVED
	jrRepo := newFakeJoinRequestRepo(jr)
	svc := newReadinessSvc(newFakeLaunchRepo(l), jrRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := svc.Confirm(context.Background(), l.ID, validConfirmInput(l))
	if err == nil {
		t.Fatal("expected error for non-approved join request")
	}
}

func TestReadinessService_Confirm_GenesisHashMismatch(t *testing.T) {
	l := genesisReadyLaunch()
	jr := approvedJoinRequest(t, l.ID, testAddr1)
	jrRepo := newFakeJoinRequestRepo(jr)
	svc := newReadinessSvc(newFakeLaunchRepo(l), jrRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	input := validConfirmInput(l)
	input.GenesisHashConfirmed = "wrong-hash"
	_, err := svc.Confirm(context.Background(), l.ID, input)
	if err == nil {
		t.Fatal("expected error for genesis hash mismatch")
	}
}

func TestReadinessService_Confirm_BinaryHashMismatch(t *testing.T) {
	l := genesisReadyLaunch()
	jr := approvedJoinRequest(t, l.ID, testAddr1)
	jrRepo := newFakeJoinRequestRepo(jr)
	svc := newReadinessSvc(newFakeLaunchRepo(l), jrRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	input := validConfirmInput(l)
	input.BinaryHashConfirmed = "wrong-binary-hash"
	_, err := svc.Confirm(context.Background(), l.ID, input)
	if err == nil {
		t.Fatal("expected error for binary hash mismatch")
	}
}

func TestReadinessService_Confirm_DuplicateValid(t *testing.T) {
	l := genesisReadyLaunch()
	jr := approvedJoinRequest(t, l.ID, testAddr1)
	jrRepo := newFakeJoinRequestRepo(jr)

	// Pre-store a valid (non-invalidated) confirmation.
	existing := &launch.ReadinessConfirmation{
		ID:              uuid.New(),
		LaunchID:        l.ID,
		JoinRequestID:   jr.ID,
		OperatorAddress: mustAddr(testAddr1),
		ConfirmedAt:     time.Now().UTC(),
	}
	rcRepo := newFakeReadinessRepo(existing)
	svc := newReadinessSvc(newFakeLaunchRepo(l), jrRepo, rcRepo, newFakeNonceStore(), &fakeVerifier{})

	_, err := svc.Confirm(context.Background(), l.ID, validConfirmInput(l))
	if err == nil {
		t.Fatal("expected error: duplicate valid confirmation")
	}
}

func TestReadinessService_Confirm_Success(t *testing.T) {
	l := genesisReadyLaunch()
	jr := approvedJoinRequest(t, l.ID, testAddr1)
	jrRepo := newFakeJoinRequestRepo(jr)
	rcRepo := newFakeReadinessRepo()
	svc := newReadinessSvc(newFakeLaunchRepo(l), jrRepo, rcRepo, newFakeNonceStore(), &fakeVerifier{})

	rc, err := svc.Confirm(context.Background(), l.ID, validConfirmInput(l))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rc.ID == uuid.Nil {
		t.Fatal("expected non-nil readiness confirmation ID")
	}
	if _, ok := rcRepo.data[rc.ID]; !ok {
		t.Fatal("readiness confirmation not persisted")
	}
}

// --- GetDashboard ---

func TestReadinessService_GetDashboard_Empty(t *testing.T) {
	l := testLaunch()
	svc := newReadinessSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	dash, err := svc.GetDashboard(context.Background(), l.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dash.TotalApproved != 0 {
		t.Errorf("want 0 total, got %d", dash.TotalApproved)
	}
	if dash.ThresholdStatus != "AT_RISK" {
		t.Errorf("want AT_RISK for empty dashboard, got %s", dash.ThresholdStatus)
	}
}

func TestReadinessService_GetDashboard_Confirmed(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusGenesisReady
	l.FinalGenesisSHA256 = "hash"

	// Two approved validators, each with 50% stake, both confirmed.
	jr1 := approvedJoinRequest(t, l.ID, testAddr1)
	jr2 := approvedJoinRequest(t, l.ID, testAddr2)
	jrRepo := newFakeJoinRequestRepo(jr1, jr2)

	rc1 := &launch.ReadinessConfirmation{ID: uuid.New(), LaunchID: l.ID, OperatorAddress: mustAddr(testAddr1), ConfirmedAt: time.Now()}
	rc2 := &launch.ReadinessConfirmation{ID: uuid.New(), LaunchID: l.ID, OperatorAddress: mustAddr(testAddr2), ConfirmedAt: time.Now()}
	rcRepo := newFakeReadinessRepo(rc1, rc2)

	svc := newReadinessSvc(newFakeLaunchRepo(l), jrRepo, rcRepo, newFakeNonceStore(), &fakeVerifier{})
	dash, err := svc.GetDashboard(context.Background(), l.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dash.ThresholdStatus != "CONFIRMED" {
		t.Errorf("want CONFIRMED when all validators ready, got %s", dash.ThresholdStatus)
	}
	if dash.ConfirmedReady != 2 {
		t.Errorf("want 2 confirmed, got %d", dash.ConfirmedReady)
	}
}

func TestReadinessService_GetDashboard_AtRisk(t *testing.T) {
	l := testLaunch()
	jr1 := approvedJoinRequest(t, l.ID, testAddr1)
	jr2 := approvedJoinRequest(t, l.ID, testAddr2)
	jrRepo := newFakeJoinRequestRepo(jr1, jr2)
	// No confirmations — 0% confirmed.
	svc := newReadinessSvc(newFakeLaunchRepo(l), jrRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	dash, err := svc.GetDashboard(context.Background(), l.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dash.ThresholdStatus != "AT_RISK" {
		t.Errorf("want AT_RISK, got %s", dash.ThresholdStatus)
	}
}

// --- GetPeers ---

func TestReadinessService_GetPeers_Success(t *testing.T) {
	l := testLaunch()
	jr1 := approvedJoinRequest(t, l.ID, testAddr1)
	jr2 := approvedJoinRequest(t, l.ID, testAddr2)
	jrRepo := newFakeJoinRequestRepo(jr1, jr2)
	svc := newReadinessSvc(newFakeLaunchRepo(l), jrRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	peers, err := svc.GetPeers(context.Background(), l.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(peers) != 2 {
		t.Errorf("want 2 peers, got %d", len(peers))
	}
}
