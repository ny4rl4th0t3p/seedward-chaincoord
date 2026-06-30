package services

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	require.NoError(t, jr.Approve(uuid.New()), "approvedJoinRequest")
	return jr
}

// --- Confirm ---

func TestReadinessService_Confirm_NonceConflict(t *testing.T) {
	l := genesisReadyLaunch()
	nonces := newFakeNonceStore()
	nonces.consumeErr = ports.ErrConflict
	svc := newReadinessSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeReadinessRepo(), nonces, &fakeVerifier{})

	_, err := svc.Confirm(context.Background(), l.ID, validConfirmInput(l))
	require.ErrorIs(t, err, ports.ErrConflict, "a rejected nonce must surface as a conflict")
}

func TestReadinessService_Confirm_BadTimestamp(t *testing.T) {
	l := genesisReadyLaunch()
	svc := newReadinessSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	input := validConfirmInput(l)
	input.Timestamp = expiredTS()
	_, err := svc.Confirm(context.Background(), l.ID, input)
	require.ErrorIs(t, err, ports.ErrUnauthorized, "expired timestamp is an auth failure")
}

func TestReadinessService_Confirm_SigFails(t *testing.T) {
	l := genesisReadyLaunch()
	verifier := &fakeVerifier{err: ports.ErrUnauthorized}
	svc := newReadinessSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeReadinessRepo(), newFakeNonceStore(), verifier)

	_, err := svc.Confirm(context.Background(), l.ID, validConfirmInput(l))
	require.ErrorIs(t, err, ports.ErrUnauthorized, "invalid signature must map to 401")
}

func TestReadinessService_Confirm_LaunchNotGenesisReady(t *testing.T) {
	l := testLaunch() // DRAFT
	svc := newReadinessSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := svc.Confirm(context.Background(), l.ID, validConfirmInput(l))
	require.ErrorIs(t, err, ports.ErrLaunchNotGenesisReady)
	assert.ErrorIs(t, err, ports.ErrConflict, "should map to 409")
}

func TestReadinessService_Confirm_NoApprovedJoinRequest(t *testing.T) {
	l := genesisReadyLaunch()
	// No join requests stored → FindByOperator returns ErrNotFound → ErrForbidden.
	svc := newReadinessSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := svc.Confirm(context.Background(), l.ID, validConfirmInput(l))
	require.ErrorIs(t, err, ports.ErrForbidden)
}

func TestReadinessService_Confirm_JoinRequestNotApproved(t *testing.T) {
	l := genesisReadyLaunch()
	jr := makeJoinRequest(t, l.ID, testAddr1) // PENDING, not APPROVED
	jrRepo := newFakeJoinRequestRepo(jr)
	svc := newReadinessSvc(newFakeLaunchRepo(l), jrRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := svc.Confirm(context.Background(), l.ID, validConfirmInput(l))
	require.ErrorIs(t, err, ports.ErrJoinRequestNotApproved)
	assert.ErrorIs(t, err, ports.ErrForbidden, "should map to 403")
}

func TestReadinessService_Confirm_GenesisHashMismatch(t *testing.T) {
	l := genesisReadyLaunch()
	jr := approvedJoinRequest(t, l.ID, testAddr1)
	jrRepo := newFakeJoinRequestRepo(jr)
	svc := newReadinessSvc(newFakeLaunchRepo(l), jrRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	input := validConfirmInput(l)
	input.GenesisHashConfirmed = "wrong-hash"
	_, err := svc.Confirm(context.Background(), l.ID, input)
	require.ErrorIs(t, err, ports.ErrBadRequest)
}

func TestReadinessService_Confirm_BinaryHashMismatch(t *testing.T) {
	l := genesisReadyLaunch()
	jr := approvedJoinRequest(t, l.ID, testAddr1)
	jrRepo := newFakeJoinRequestRepo(jr)
	svc := newReadinessSvc(newFakeLaunchRepo(l), jrRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	input := validConfirmInput(l)
	input.BinaryHashConfirmed = "wrong-binary-hash"
	_, err := svc.Confirm(context.Background(), l.ID, input)
	require.ErrorIs(t, err, ports.ErrBadRequest)
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
	require.ErrorIs(t, err, ports.ErrReadinessAlreadyConfirmed)
	assert.ErrorIs(t, err, ports.ErrConflict, "should map to 409")
}

// A prior confirmation that was invalidated (e.g. by a genesis-time update) must
// not block a fresh confirmation — exercises the !IsValid() branch.
func TestReadinessService_Confirm_ReconfirmsAfterInvalidation(t *testing.T) {
	l := genesisReadyLaunch()
	jr := approvedJoinRequest(t, l.ID, testAddr1)
	jrRepo := newFakeJoinRequestRepo(jr)

	stale := &launch.ReadinessConfirmation{
		ID:              uuid.New(),
		LaunchID:        l.ID,
		JoinRequestID:   jr.ID,
		OperatorAddress: mustAddr(testAddr1),
		ConfirmedAt:     time.Now().UTC(),
	}
	stale.Invalidate(time.Now().UTC())
	rcRepo := newFakeReadinessRepo(stale)
	svc := newReadinessSvc(newFakeLaunchRepo(l), jrRepo, rcRepo, newFakeNonceStore(), &fakeVerifier{})

	rc, err := svc.Confirm(context.Background(), l.ID, validConfirmInput(l))
	require.NoError(t, err, "an invalidated prior confirmation should not block re-confirmation")
	require.NotEqual(t, stale.ID, rc.ID, "expected a fresh confirmation")
}

// A non-NotFound error from the existing-confirmation lookup aborts the confirm.
func TestReadinessService_Confirm_ExistingCheckError(t *testing.T) {
	l := genesisReadyLaunch()
	jr := approvedJoinRequest(t, l.ID, testAddr1)
	jrRepo := newFakeJoinRequestRepo(jr)
	rcRepo := newFakeReadinessRepo()
	rcRepo.findByOpErr = ports.ErrConflict // any non-NotFound error
	svc := newReadinessSvc(newFakeLaunchRepo(l), jrRepo, rcRepo, newFakeNonceStore(), &fakeVerifier{})

	_, err := svc.Confirm(context.Background(), l.ID, validConfirmInput(l))
	require.Error(t, err, "a lookup error on the existing confirmation must abort")
}

func TestReadinessService_Confirm_Success(t *testing.T) {
	l := genesisReadyLaunch()
	jr := approvedJoinRequest(t, l.ID, testAddr1)
	jrRepo := newFakeJoinRequestRepo(jr)
	rcRepo := newFakeReadinessRepo()
	svc := newReadinessSvc(newFakeLaunchRepo(l), jrRepo, rcRepo, newFakeNonceStore(), &fakeVerifier{})

	rc, err := svc.Confirm(context.Background(), l.ID, validConfirmInput(l))
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, rc.ID, "expected non-nil readiness confirmation ID")
	_, ok := rcRepo.data[rc.ID]
	require.True(t, ok, "readiness confirmation not persisted")
}

// --- GetDashboard ---

func TestReadinessService_GetDashboard_Empty(t *testing.T) {
	l := testLaunch()
	svc := newReadinessSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	dash, err := svc.GetDashboard(context.Background(), l.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, dash.TotalApproved)
	assert.Equal(t, "AT_RISK", dash.ThresholdStatus, "empty dashboard is AT_RISK")
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
	require.NoError(t, err)
	assert.Equal(t, "CONFIRMED", dash.ThresholdStatus, "all validators ready")
	assert.Equal(t, 2, dash.ConfirmedReady)
}

func TestReadinessService_GetDashboard_AtRisk(t *testing.T) {
	l := testLaunch()
	jr1 := approvedJoinRequest(t, l.ID, testAddr1)
	jr2 := approvedJoinRequest(t, l.ID, testAddr2)
	jrRepo := newFakeJoinRequestRepo(jr1, jr2)
	// No confirmations — 0% confirmed.
	svc := newReadinessSvc(newFakeLaunchRepo(l), jrRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	dash, err := svc.GetDashboard(context.Background(), l.ID)
	require.NoError(t, err)
	assert.Equal(t, "AT_RISK", dash.ThresholdStatus)
}

// --- GetPeers ---

func TestReadinessService_GetPeers_Success(t *testing.T) {
	l := testLaunch()
	jr1 := approvedJoinRequest(t, l.ID, testAddr1)
	jr2 := approvedJoinRequest(t, l.ID, testAddr2)
	jrRepo := newFakeJoinRequestRepo(jr1, jr2)
	svc := newReadinessSvc(newFakeLaunchRepo(l), jrRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	peers, err := svc.GetPeers(context.Background(), l.ID)
	require.NoError(t, err)
	assert.Len(t, peers, 2)
}
