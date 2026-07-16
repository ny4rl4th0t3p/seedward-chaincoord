package services

import (
	"context"
	"encoding/json"
	"errors"
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
	allocHashA = "3333333333333333333333333333333333333333333333333333333333333333"
	allocHashB = "4444444444444444444444444444444444444444444444444444444444444444"
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
		ActionType: proposal.ActionCloseApplicationWindow,
		Payload:    payload,
		MemberAddr: testAddr1,
		Nonce:      uuid.New().String(),
		Timestamp:  nowTS(),
		Signature:  testSig,
		PubKeyB64:  testSig,
	}
}

// --- Raise ---

func TestProposalService_Raise_NonceConflict(t *testing.T) {
	l := testLaunch()
	nonces := newFakeNonceStore()
	nonces.consumeErr = ports.ErrConflict
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), nonces, &fakeVerifier{})

	_, err := svc.Raise(context.Background(), l.ID, validRaiseInput(l))
	require.ErrorIs(t, err, ports.ErrConflict, "a rejected nonce must surface as a conflict")
}

func TestProposalService_Raise_BadTimestamp(t *testing.T) {
	l := testLaunch()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	input := validRaiseInput(l)
	input.Timestamp = expiredTS()
	_, err := svc.Raise(context.Background(), l.ID, input)
	require.ErrorIs(t, err, ports.ErrUnauthorized, "an expired timestamp is an auth failure")
}

func TestProposalService_Raise_LaunchNotFound(t *testing.T) {
	svc := newProposalSvc(newFakeLaunchRepo(), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	payload, _ := json.Marshal(proposal.CloseApplicationWindowPayload{})
	_, err := svc.Raise(context.Background(), uuid.New(), RaiseInput{
		ActionType: proposal.ActionCloseApplicationWindow,
		Payload:    payload,
		MemberAddr: testAddr1,
		Nonce:      uuid.New().String(),
		Timestamp:  nowTS(),
		Signature:  testSig,
		PubKeyB64:  testSig,
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
		ActionType: proposal.ActionCloseApplicationWindow,
		Payload:    payload,
		MemberAddr: testAddr2, // not in committee
		Nonce:      uuid.New().String(),
		Timestamp:  nowTS(),
		Signature:  testSig,
		PubKeyB64:  testSig,
	})
	require.ErrorIs(t, err, ports.ErrForbidden)
}

func TestProposalService_Raise_SigFails(t *testing.T) {
	l := testLaunch()
	// A BARE verifier error (as the real crypto verifiers return) must still map to 401 —
	// the service is responsible for attaching the sentinel.
	verifier := &fakeVerifier{err: errors.New("signature verification failed")}
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), verifier)

	_, err := svc.Raise(context.Background(), l.ID, validRaiseInput(l))
	require.ErrorIs(t, err, ports.ErrUnauthorized, "a failed signature must map to 401")
}

func TestProposalService_Raise_EmptyPubkey(t *testing.T) {
	l := testLaunch()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	input := validRaiseInput(l)
	input.PubKeyB64 = "" // the ADR-036 envelope must carry the signer's pubkey
	_, err := svc.Raise(context.Background(), l.ID, input)
	require.ErrorIs(t, err, ports.ErrBadRequest, "a missing pubkey_b64 is a bad request")
}

// TestProposalService_Raise_ForwardsRequestPubkey proves the verified pubkey is the one from the
// request envelope, not a stored/committee value — that is what lets a member who never pre-registered
// a key sign, and prevents verifying against a different (stored) key than the one that actually signed.
func TestProposalService_Raise_ForwardsRequestPubkey(t *testing.T) {
	l := testLaunch()
	verifier := &fakeVerifier{}
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), verifier)

	input := validRaiseInput(l)
	input.PubKeyB64 = "request-envelope-pubkey"
	_, err := svc.Raise(context.Background(), l.ID, input)
	require.NoError(t, err)
	require.Equal(t, "request-envelope-pubkey", verifier.gotPubKeyB64, "verifier must receive the request pubkey, not a stored one")
}

func TestProposalService_Raise_BadMemberAddress(t *testing.T) {
	l := testLaunch()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	input := validRaiseInput(l)
	input.MemberAddr = "not-a-bech32-address"
	_, err := svc.Raise(context.Background(), l.ID, input)
	require.ErrorIs(t, err, ports.ErrBadRequest, "an unparseable committee member address is a 400")
}

func TestProposalService_Raise_InvalidAction(t *testing.T) {
	l := testLaunch()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	input := validRaiseInput(l)
	input.ActionType = "BOGUS_ACTION" // rejected by proposal.New's payload/action validation
	_, err := svc.Raise(context.Background(), l.ID, input)
	require.ErrorIs(t, err, ports.ErrBadRequest, "an invalid action is a 400")
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
		ActionType: proposal.ActionCloseApplicationWindow,
		Payload:    payload,
		MemberAddr: testAddr1,
		Nonce:      uuid.New().String(),
		Timestamp:  nowTS(),
		Signature:  testSig,
		PubKeyB64:  testSig,
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
		MemberAddr: testAddr1,
		Decision:   proposal.DecisionSign,
		Nonce:      uuid.New().String(),
		Timestamp:  nowTS(),
		Signature:  testSig,
		PubKeyB64:  testSig,
	})
	require.ErrorIs(t, err, ports.ErrConflict, "a rejected nonce must surface as a conflict")
}

func TestProposalService_Sign_SigFails(t *testing.T) {
	l := testLaunch()
	p := testProposal(l.ID)
	// Bare verifier error (as the real verifiers return) must still map to 401.
	verifier := &fakeVerifier{err: errors.New("signature verification failed")}
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(p), newFakeReadinessRepo(), newFakeNonceStore(), verifier)

	_, err := svc.Sign(context.Background(), l.ID, p.ID, SignInput{
		MemberAddr: testAddr1,
		Decision:   proposal.DecisionSign,
		Nonce:      uuid.New().String(),
		Timestamp:  nowTS(),
		Signature:  testSig,
		PubKeyB64:  testSig,
	})
	require.ErrorIs(t, err, ports.ErrUnauthorized, "a failed signature must map to 401")
}

func TestProposalService_Sign_EmptyPubkey(t *testing.T) {
	l := testLaunch()
	p := testProposal(l.ID)
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(p), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := svc.Sign(context.Background(), l.ID, p.ID, SignInput{
		MemberAddr: testAddr1,
		Decision:   proposal.DecisionSign,
		Nonce:      uuid.New().String(),
		Timestamp:  nowTS(),
		Signature:  testSig,
		// PubKeyB64 omitted → empty
	})
	require.ErrorIs(t, err, ports.ErrBadRequest, "a missing pubkey_b64 is a bad request")
}

func TestProposalService_Sign_NotCommitteeMember(t *testing.T) {
	l := testLaunch()
	l.Committee = testCommittee(1, 1) // only testAddr1
	p := testProposal(l.ID)
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(p), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := svc.Sign(context.Background(), l.ID, p.ID, SignInput{
		MemberAddr: testAddr2, // not in committee
		Decision:   proposal.DecisionSign,
		Nonce:      uuid.New().String(),
		Timestamp:  nowTS(),
		Signature:  testSig,
		PubKeyB64:  testSig,
	})
	require.ErrorIs(t, err, ports.ErrForbidden)
}

func TestProposalService_Sign_WrongLaunch(t *testing.T) {
	l := testLaunch()
	otherLaunchID := uuid.New()
	p := testProposal(otherLaunchID) // proposal belongs to a different launch
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(p), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := svc.Sign(context.Background(), l.ID, p.ID, SignInput{
		MemberAddr: testAddr1,
		Decision:   proposal.DecisionSign,
		Nonce:      uuid.New().String(),
		Timestamp:  nowTS(),
		Signature:  testSig,
		PubKeyB64:  testSig,
	})
	require.ErrorIs(t, err, ports.ErrNotFound)
}

// mustNewLaunch builds a launch with the standard test record, failing the test with a diagnostic
// (rather than nil-panicking in a later line) if the fixture is invalid.
func mustNewLaunch(t *testing.T, committee launch.Committee) *launch.Launch {
	t.Helper()
	l, err := launch.New(uuid.New(), testChainRecord(), launch.LaunchTypeTestnet, committee)
	require.NoError(t, err, "mustNewLaunch fixture")
	return l
}

// mustNewProposal builds a proposal, failing the test with a diagnostic if the fixture is invalid.
func mustNewProposal(t *testing.T, launchID uuid.UUID, payload []byte, ttl time.Duration, createdAt time.Time) *proposal.Proposal {
	t.Helper()
	p, err := proposal.New(uuid.New(), launchID, proposal.ActionCloseApplicationWindow, payload, mustAddr(testAddr1), mustSig(), ttl, createdAt)
	require.NoError(t, err, "mustNewProposal fixture")
	return p
}

func TestProposalService_Sign_AlreadySigned(t *testing.T) {
	// 3-of-3 committee: Raise adds testAddr1's signature (stays PENDING). Signing again
	// as testAddr1 must be a state conflict (409), not a 500.
	l := mustNewLaunch(t, testCommittee(3, 3))
	propRepo := newFakeProposalRepo()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), propRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	p, err := svc.Raise(context.Background(), l.ID, validRaiseInput(l))
	require.NoError(t, err)
	require.Equal(t, proposal.StatusPendingSignatures, p.Status)

	_, err = svc.Sign(context.Background(), l.ID, p.ID, SignInput{
		MemberAddr: testAddr1, // already signed as proposer
		Decision:   proposal.DecisionSign,
		Nonce:      uuid.New().String(),
		Timestamp:  nowTS(),
		Signature:  testSig,
		PubKeyB64:  testSig,
	})
	require.ErrorIs(t, err, ports.ErrConflict, "a double-sign must map to 409")
	assert.ErrorIs(t, err, proposal.ErrMemberAlreadySigned, "and preserves the domain sentinel")
}

func TestProposalService_Sign_TTLExpired(t *testing.T) {
	// A proposal whose TTL has elapsed but is still PENDING (the expiry job has not yet run):
	// signing it must surface a state conflict (409), not a 500.
	l := testLaunch() // 2-of-3
	p := mustNewProposal(t, l.ID, []byte(`{}`),
		1*time.Millisecond, time.Now().Add(-1*time.Hour))
	propRepo := newFakeProposalRepo(p)
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), propRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := svc.Sign(context.Background(), l.ID, p.ID, SignInput{
		MemberAddr: testAddr2, // a member who has not yet signed
		Decision:   proposal.DecisionSign,
		Nonce:      uuid.New().String(),
		Timestamp:  nowTS(),
		Signature:  testSig,
		PubKeyB64:  testSig,
	})
	require.ErrorIs(t, err, ports.ErrConflict, "signing a TTL-elapsed proposal must map to 409")
	assert.ErrorIs(t, err, proposal.ErrProposalTTLExpired, "and preserves the domain sentinel")
}

func TestProposalService_Sign_AddsSignature(t *testing.T) {
	// 3-of-3 committee; Raise already added 1 signature (testAddr1 as proposer).
	// Sign as testAddr2 → still PENDING.
	l := mustNewLaunch(t, testCommittee(3, 3))
	propRepo := newFakeProposalRepo()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), propRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	// Raise as testAddr1.
	payload, _ := json.Marshal(proposal.CloseApplicationWindowPayload{})
	p, err := svc.Raise(context.Background(), l.ID, RaiseInput{
		ActionType: proposal.ActionCloseApplicationWindow,
		Payload:    payload,
		MemberAddr: testAddr1,
		Nonce:      uuid.New().String(),
		Timestamp:  nowTS(),
		Signature:  testSig,
		PubKeyB64:  testSig,
	})
	require.NoError(t, err)
	require.Equal(t, proposal.StatusPendingSignatures, p.Status, "want PENDING after raise")

	// Sign as testAddr2.
	p2, err := svc.Sign(context.Background(), l.ID, p.ID, SignInput{
		MemberAddr: testAddr2,
		Decision:   proposal.DecisionSign,
		Nonce:      uuid.New().String(),
		Timestamp:  nowTS(),
		Signature:  testSig,
		PubKeyB64:  testSig,
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
	p := mustNewProposal(t, l.ID, payload,
		1*time.Millisecond, time.Now().Add(-1*time.Hour))
	propRepo.data[p.ID] = p

	require.NoError(t, svc.ExpireStale(context.Background()))
	assert.Equal(t, proposal.StatusExpired, propRepo.data[p.ID].Status)
}

func TestProposalService_ExpireStale_SkipsFresh(t *testing.T) {
	l := testLaunch()
	propRepo := newFakeProposalRepo()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), propRepo, newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	payload, _ := json.Marshal(proposal.CloseApplicationWindowPayload{})
	p := mustNewProposal(t, l.ID, payload,
		48*time.Hour, time.Now())
	propRepo.data[p.ID] = p

	require.NoError(t, svc.ExpireStale(context.Background()))
	assert.Equal(t, proposal.StatusPendingSignatures, propRepo.data[p.ID].Status, "fresh proposal should not be expired")
}

func TestProposalService_ExpireStale_Audited(t *testing.T) {
	l := testLaunch()
	audit := &fakeAuditLogWriter{}
	propRepo := newFakeProposalRepo()
	svc := NewProposalService(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), propRepo,
		newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{},
		&fakeEventPublisher{}, audit, &fakeTransactor{})

	// A stale proposal (TTL already elapsed, still PENDING).
	p := mustNewProposal(t, l.ID, []byte(`{}`),
		1*time.Millisecond, time.Now().Add(-1*time.Hour))
	propRepo.data[p.ID] = p

	require.NoError(t, svc.ExpireStale(context.Background()))

	require.Len(t, audit.events, 1, "expiring a stale proposal must be audited")
	assert.Equal(t, "ProposalExpired", audit.events[0].EventName)
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
		ActionType: action,
		Payload:    raw,
		MemberAddr: testAddr1,
		Nonce:      uuid.New().String(),
		Timestamp:  nowTS(),
		Signature:  testSig,
		PubKeyB64:  testSig,
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
	require.ErrorIs(t, err, ports.ErrNotFound, "a missing join request is a 404")
}

func TestProposalService_applyApproveValidator_AlreadyApproved(t *testing.T) {
	// Approving a join request that is already APPROVED hits the aggregate's status
	// guard; it must surface as a 409, not a 500.
	l := test1of1Launch()
	l.Status = launch.StatusWindowOpen
	jr := makeJoinRequest(t, l.ID, testAddr2)
	require.NoError(t, jr.Approve(uuid.New())) // pre-approve
	jrRepo := newFakeJoinRequestRepo(jr)
	svc := newProposalSvc(newFakeLaunchRepo(l), jrRepo, newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionApproveValidator, proposal.ApproveValidatorPayload{
		JoinRequestID:   jr.ID,
		OperatorAddress: testAddr2,
	})
	require.ErrorIs(t, err, ports.ErrConflict, "re-approving an APPROVED request is a 409")
	assert.ErrorIs(t, err, joinrequest.ErrInvalidJoinRequestStatus, "and preserves the domain sentinel")
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
	require.ErrorIs(t, err, ports.ErrBadRequest, "REMOVE_APPROVED_VALIDATOR not allowed at GENESIS_READY")
}

func TestProposalService_applyRemoveValidator_NotApproved(t *testing.T) {
	// Revoking a join request that is still PENDING (never approved) hits the aggregate's
	// status guard; it must surface as a 409, not a 500.
	l := test1of1Launch()
	l.Status = launch.StatusWindowOpen
	jr := makeJoinRequest(t, l.ID, testAddr2) // PENDING
	jrRepo := newFakeJoinRequestRepo(jr)
	svc := newProposalSvc(newFakeLaunchRepo(l), jrRepo, newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionRemoveApprovedValidator, proposal.RemoveApprovedValidatorPayload{
		JoinRequestID:   jr.ID,
		OperatorAddress: testAddr2,
		Reason:          "not approved yet",
	})
	require.ErrorIs(t, err, ports.ErrConflict, "revoking a PENDING request is a 409")
	assert.ErrorIs(t, err, joinrequest.ErrInvalidJoinRequestStatus, "and preserves the domain sentinel")
}

func TestProposalService_applyRejectValidator_JoinRequestNotFound(t *testing.T) {
	l := test1of1Launch()
	l.Status = launch.StatusWindowOpen
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionRejectValidator, proposal.RejectValidatorPayload{
		JoinRequestID:   uuid.New(),
		OperatorAddress: testAddr2,
		Reason:          "no such request",
	})
	require.ErrorIs(t, err, ports.ErrNotFound, "a missing join request is a 404")
}

func TestProposalService_applyCloseWindow_InsufficientValidators(t *testing.T) {
	// 1-of-1 launch with MinValidatorCount=1 and no approved validators: CloseWindow
	// hits the domain precondition and must surface as a 409, not a 500.
	l := test1of1Launch()
	l.Status = launch.StatusWindowOpen
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionCloseApplicationWindow, proposal.CloseApplicationWindowPayload{})
	require.ErrorIs(t, err, ports.ErrConflict, "closing with too few validators is a state conflict")
	assert.ErrorIs(t, err, launch.ErrInsufficientValidators, "and preserves the domain sentinel")
}

func TestProposalService_applyPublishGenesis_WrongStatus(t *testing.T) {
	// From DRAFT no final genesis has been uploaded, so the finalization guard rejects the raise as a
	// 409 (stale/absent) before the domain status guard is reached. (The domain status transition
	// itself is covered in the launch package tests.)
	l := test1of1Launch() // DRAFT
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionPublishGenesis, proposal.PublishGenesisPayload{
		GenesisHash: "1111111111111111111111111111111111111111111111111111111111111111",
	})
	require.ErrorIs(t, err, ports.ErrConflict, "publishing genesis without an uploaded final genesis is a state conflict")
	assert.ErrorIs(t, err, launch.ErrGenesisStale, "and surfaces the stale/absent-genesis sentinel")
}

func TestProposalService_applyProposal_SaveLaunchFails(t *testing.T) {
	// A persistence failure in saveLaunchAndProposal must propagate (not be swallowed).
	l := test1of1Launch()
	l.Status = launch.StatusWindowClosed
	lRepo := newFakeLaunchRepo(l)
	lRepo.saveErr = errors.New("db down")
	svc := newProposalSvc(lRepo, newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})
	hash := bindFinalGenesis(t, svc, l)

	_, err := raiseWith(t, svc, l.ID, proposal.ActionPublishGenesis, proposal.PublishGenesisPayload{
		GenesisHash: hash,
	})
	require.Error(t, err, "a launch save failure must surface")
	assert.ErrorContains(t, err, "save launch")
}

func TestProposalService_applyPublishGenesis_Success(t *testing.T) {
	l := test1of1Launch()
	l.Status = launch.StatusWindowClosed
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})
	hash := bindFinalGenesis(t, svc, l)

	p, err := raiseWith(t, svc, l.ID, proposal.ActionPublishGenesis, proposal.PublishGenesisPayload{
		GenesisHash: hash,
	})
	require.NoError(t, err)
	require.Equal(t, proposal.StatusExecuted, p.Status)
	assert.Equal(t, launch.StatusGenesisReady, l.Status)
}

// bindFinalGenesis simulates a final-genesis upload: it sets the launch's final hash and binds the
// current input_set_hash (as UploadFinalGenesis does), so the finalization guards see a fresh,
// consistent genesis. Returns the genesis hash to put in the PUBLISH_GENESIS payload.
func bindFinalGenesis(t *testing.T, svc *ProposalService, l *launch.Launch) string {
	t.Helper()
	const hash = "1111111111111111111111111111111111111111111111111111111111111111"
	l.FinalGenesisSHA256 = hash
	ish, err := svc.hasher.Current(context.Background(), l)
	require.NoError(t, err)
	l.FinalGenesisInputSetHash = ish
	return hash
}

// pendingProposal builds a PENDING_SIGNATURES proposal for a launch (proposal.New starts pending),
// used to seed the freeze checks. The payload must be valid for the action (proposal.New validates it).
func pendingProposal(t *testing.T, launchID uuid.UUID, action proposal.ActionType, payload any) *proposal.Proposal {
	t.Helper()
	raw, _ := json.Marshal(payload)
	p, err := proposal.New(uuid.New(), launchID, action, raw, mustAddr(testAddr1), mustSig(), defaultProposalTTL, time.Now())
	require.NoError(t, err)
	require.Equal(t, proposal.StatusPendingSignatures, p.Status)
	return p
}

// --- Part A: genesis ↔ approved-set consistency guards ---

func TestProposalService_PublishGenesis_StaleAtRaise(t *testing.T) {
	// Bind a final genesis for the current set, then approve another validator (the set drifts) →
	// raising PUBLISH_GENESIS is rejected as stale. This is the core hole the guard closes.
	l := test1of1Launch()
	l.Status = launch.StatusWindowClosed
	jrRepo := newFakeJoinRequestRepo()
	svc := newProposalSvc(newFakeLaunchRepo(l), jrRepo, newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})
	hash := bindFinalGenesis(t, svc, l) // bound to the current (empty) approved set

	jr := makeJoinRequest(t, l.ID, testAddr2)
	require.NoError(t, jr.Approve(uuid.New()))
	jrRepo.data[jr.ID] = jr // set now differs from what the genesis was assembled from

	_, err := raiseWith(t, svc, l.ID, proposal.ActionPublishGenesis, proposal.PublishGenesisPayload{GenesisHash: hash})
	require.ErrorIs(t, err, ports.ErrConflict)
	assert.ErrorIs(t, err, launch.ErrGenesisStale, "approved set changed since upload → stale")
}

func TestProposalService_PublishGenesis_HashMismatch(t *testing.T) {
	// Raising PUBLISH_GENESIS with a hash other than the uploaded final genesis is rejected.
	l := test1of1Launch()
	l.Status = launch.StatusWindowClosed
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})
	_ = bindFinalGenesis(t, svc, l)

	_, err := raiseWith(t, svc, l.ID, proposal.ActionPublishGenesis, proposal.PublishGenesisPayload{
		GenesisHash: "2222222222222222222222222222222222222222222222222222222222222222",
	})
	require.ErrorIs(t, err, ports.ErrConflict)
	assert.ErrorIs(t, err, launch.ErrGenesisHashMismatch, "proposal hash must match the uploaded final genesis")
}

func TestProposalService_PublishGenesis_FrozenByPendingMutation(t *testing.T) {
	// A pending APPROVE_VALIDATOR proposal freezes genesis publication.
	l := test1of1Launch()
	l.Status = launch.StatusWindowClosed
	pending := pendingProposal(t, l.ID, proposal.ActionApproveValidator, proposal.ApproveValidatorPayload{
		JoinRequestID:   uuid.New(),
		OperatorAddress: testAddr2,
	})
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(pending), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})
	hash := bindFinalGenesis(t, svc, l)

	_, err := raiseWith(t, svc, l.ID, proposal.ActionPublishGenesis, proposal.PublishGenesisPayload{GenesisHash: hash})
	require.ErrorIs(t, err, ports.ErrConflict)
	assert.ErrorIs(t, err, launch.ErrGenesisPublishInProgress, "cannot publish while a set mutation is pending")
}

func TestProposalService_ApproveValidator_FrozenByPendingPublish(t *testing.T) {
	// Bidirectional: a pending PUBLISH_GENESIS proposal freezes validator-set changes.
	l := test1of1Launch()
	l.Status = launch.StatusWindowClosed
	jr := makeJoinRequest(t, l.ID, testAddr2)
	pending := pendingProposal(t, l.ID, proposal.ActionPublishGenesis, proposal.PublishGenesisPayload{
		GenesisHash: "1111111111111111111111111111111111111111111111111111111111111111",
	})
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(jr), newFakeProposalRepo(pending), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionApproveValidator, proposal.ApproveValidatorPayload{
		JoinRequestID:   jr.ID,
		OperatorAddress: testAddr2,
	})
	require.ErrorIs(t, err, ports.ErrConflict)
	assert.ErrorIs(t, err, launch.ErrGenesisPublishInProgress, "cannot change the set while a genesis publication is pending")
}

// --- Part B: opt-in rehearsal gate (off is exercised by applyPublishGenesis_Success) ---

func TestProposalService_RehearsalGate_Required_NoPass_Rejected(t *testing.T) {
	l := test1of1Launch()
	l.Status = launch.StatusWindowClosed
	l.RehearsalServicePubKey = "pk" // a rehearsal service IS configured for this launch
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{}).
		WithRehearsalGate("required", newFakeRehearsalResultRepo())
	hash := bindFinalGenesis(t, svc, l)

	_, err := raiseWith(t, svc, l.ID, proposal.ActionPublishGenesis, proposal.PublishGenesisPayload{GenesisHash: hash})
	require.ErrorIs(t, err, ports.ErrConflict)
	assert.ErrorIs(t, err, launch.ErrRehearsalGateUnsatisfied, "required + no passing rehearsal → blocked")
}

func TestProposalService_RehearsalGate_Required_NoService_Rejected(t *testing.T) {
	l := test1of1Launch()
	l.Status = launch.StatusWindowClosed
	// RehearsalServicePubKey empty → required gate but no rehearsal service wired for this launch.
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{}).
		WithRehearsalGate("required", newFakeRehearsalResultRepo())
	hash := bindFinalGenesis(t, svc, l)

	_, err := raiseWith(t, svc, l.ID, proposal.ActionPublishGenesis, proposal.PublishGenesisPayload{GenesisHash: hash})
	require.ErrorIs(t, err, ports.ErrConflict)
	assert.ErrorIs(t, err, launch.ErrRehearsalGateNoService)
}

func TestProposalService_RehearsalGate_Required_CurrentPass_Allowed(t *testing.T) {
	l := test1of1Launch()
	l.Status = launch.StatusWindowClosed
	l.RehearsalServicePubKey = "pk"
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})
	hash := bindFinalGenesis(t, svc, l) // also sets l.FinalGenesisInputSetHash

	// A current PASS for the launch's present input set.
	results := newFakeRehearsalResultRepo()
	require.NoError(t, results.Save(context.Background(), &launch.RehearsalResult{
		LaunchID:     l.ID,
		Outcome:      launch.OutcomePass,
		InputSetHash: l.FinalGenesisInputSetHash,
		Signature:    "sig-current-pass",
	}))
	gated := svc.WithRehearsalGate("required", results)

	p, err := raiseWith(t, gated, l.ID, proposal.ActionPublishGenesis, proposal.PublishGenesisPayload{GenesisHash: hash})
	require.NoError(t, err)
	require.Equal(t, proposal.StatusExecuted, p.Status, "required + current PASS → finalizes")
}

func TestProposalService_applyPublishChainRecord_Success(t *testing.T) {
	l := test1of1Launch()
	l.InitialGenesisSHA256 = "1111111111111111111111111111111111111111111111111111111111111111"
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	p, err := raiseWith(t, svc, l.ID, proposal.ActionPublishChainRecord, proposal.PublishChainRecordPayload{
		InitialGenesisHash: "1111111111111111111111111111111111111111111111111111111111111111",
	})
	require.NoError(t, err)
	require.Equal(t, proposal.StatusExecuted, p.Status)
	assert.Equal(t, launch.StatusPublished, l.Status, "want PUBLISHED after publish-chain-record")
}

func TestProposalService_applyPublishChainRecord_HashMismatch(t *testing.T) {
	l := test1of1Launch()
	l.InitialGenesisSHA256 = "1111111111111111111111111111111111111111111111111111111111111111"
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionPublishChainRecord, proposal.PublishChainRecordPayload{
		InitialGenesisHash: "wronghash",
	})
	require.ErrorIs(t, err, ports.ErrBadRequest, "a mismatched attested hash is a 400")
}

func TestProposalService_applyPublishChainRecord_NoGenesisUploaded(t *testing.T) {
	l := test1of1Launch() // InitialGenesisSHA256 is empty
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionPublishChainRecord, proposal.PublishChainRecordPayload{
		InitialGenesisHash: "1111111111111111111111111111111111111111111111111111111111111111",
	})
	require.ErrorIs(t, err, ports.ErrConflict, "publishing before genesis upload is a state conflict")
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

func TestProposalService_UpdateGenesisTime_Audited(t *testing.T) {
	l := test1of1Launch() // 1-of-1 → raise executes immediately
	l.Status = launch.StatusGenesisReady
	audit := &fakeAuditLogWriter{}
	svc := newAuditingProposalSvc(l, audit)

	prevTime := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	newTime := time.Date(2030, 2, 1, 0, 0, 0, 0, time.UTC)
	_, err := raiseWith(t, svc, l.ID, proposal.ActionUpdateGenesisTime, proposal.UpdateGenesisTimePayload{
		NewGenesisTime:  newTime,
		PrevGenesisTime: prevTime,
	})
	require.NoError(t, err)

	// Both times must propagate from the payload into the audit event — PrevGenesisTime especially,
	// which production copies through unaltered and no other test asserts.
	pl := auditPayload(t, audit.events, "GenesisTimeUpdated")
	assert.Equal(t, newTime.Format(time.RFC3339), pl["NewGenesisTime"], "event records the new genesis time")
	assert.Equal(t, prevTime.Format(time.RFC3339), pl["PrevGenesisTime"], "event propagates the previous genesis time")
}

func TestProposalService_applyUpdateGenesisTime_AfterLaunched(t *testing.T) {
	// UPDATE_GENESIS_TIME is blocked once the chain has LAUNCHED.
	l := test1of1Launch()
	l.Status = launch.StatusLaunched
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionUpdateGenesisTime, proposal.UpdateGenesisTimePayload{
		NewGenesisTime: time.Now().Add(48 * time.Hour).UTC(),
	})
	require.ErrorIs(t, err, ports.ErrBadRequest, "UPDATE_GENESIS_TIME not allowed at LAUNCHED")
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
	require.ErrorIs(t, err, ports.ErrConflict, "a stale allocation hash is a 409")
	assert.ErrorIs(t, err, launch.ErrAllocationStaleHash, "and preserves the domain sentinel")
}

func TestProposalService_applyApproveAllocationFile_NotFound(t *testing.T) {
	// No allocation file uploaded for the type → approval fails.
	l := test1of1Launch()
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionApproveAllocationFile, proposal.ApproveAllocationFilePayload{
		Type: string(launch.AllocationAccounts),
		Hash: allocHashA,
	})
	require.ErrorIs(t, err, ports.ErrNotFound, "approving a type with no uploaded file is a 404")
	assert.ErrorIs(t, err, launch.ErrAllocationNotFound, "and preserves the domain sentinel")
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
		MemberAddr: testAddr2,
		Decision:   proposal.DecisionVeto,
		Nonce:      uuid.New().String(),
		Timestamp:  nowTS(),
		Signature:  testSig,
		PubKeyB64:  testSig,
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
		MemberAddr: testAddr2,
		Decision:   proposal.DecisionVeto,
		Nonce:      uuid.New().String(),
		Timestamp:  nowTS(),
		Signature:  testSig,
		PubKeyB64:  testSig,
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
	})
	require.ErrorIs(t, err, ports.ErrBadRequest, "an unknown old_address is a 400")
	assert.ErrorIs(t, err, launch.ErrCommitteeMemberNotFound, "and preserves the domain sentinel")
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
	require.ErrorIs(t, err, ports.ErrConflict, "reopening from the wrong status is a state conflict")
	assert.ErrorIs(t, err, launch.ErrInvalidStatusTransition, "and preserves the domain sentinel")
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
	})
	require.NoError(t, err)
	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	assert.Equal(t, testAddr2, stored.Committee.LeadAddress.String(), "lead address not updated")
}

// ---- Committee-resize audit events ------------------------------------------

// auditPayload returns the unmarshalled payload of the first audit event with the given name.
func auditPayload(t *testing.T, evs []ports.AuditEvent, name string) map[string]any {
	t.Helper()
	for _, e := range evs {
		if e.EventName == name {
			var m map[string]any
			require.NoError(t, json.Unmarshal(e.Payload, &m))
			return m
		}
	}
	t.Fatalf("no audit event %q found", name)
	return nil
}

func newAuditingProposalSvc(l *launch.Launch, audit *fakeAuditLogWriter) *ProposalService {
	return NewProposalService(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(),
		newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{},
		&fakeEventPublisher{}, audit, &fakeTransactor{})
}

func TestProposalService_ExpandCommittee_Audited(t *testing.T) {
	l := test1of1Launch() // 1-of-1 → raise executes immediately
	audit := &fakeAuditLogWriter{}
	svc := newAuditingProposalSvc(l, audit)

	_, err := raiseWith(t, svc, l.ID, proposal.ActionExpandCommittee, proposal.ExpandCommitteePayload{
		NewMember: proposal.CommitteeMemberSpec{Address: testAddr2, Moniker: "coord-2"},
	})
	require.NoError(t, err)

	pl := auditPayload(t, audit.events, "CommitteeExpanded")
	assert.Equal(t, testAddr2, pl["AddedAddress"])
	assert.Len(t, pl["OldMembers"], 1, "old committee snapshot")
	assert.Len(t, pl["NewMembers"], 2, "new committee snapshot")
}

func TestProposalService_ShrinkCommittee_Audited(t *testing.T) {
	l := test1of3Launch() // members testAddr1/2/3, M=1
	audit := &fakeAuditLogWriter{}
	svc := newAuditingProposalSvc(l, audit)

	_, err := raiseWith(t, svc, l.ID, proposal.ActionShrinkCommittee, proposal.ShrinkCommitteePayload{
		RemoveAddress: testAddr3,
	})
	require.NoError(t, err)

	pl := auditPayload(t, audit.events, "CommitteeShrunk")
	assert.Equal(t, testAddr3, pl["RemovedAddress"])
	assert.Len(t, pl["OldMembers"], 3)
	assert.Len(t, pl["NewMembers"], 2)
}

func TestProposalService_ReplaceCommitteeMember_Audited(t *testing.T) {
	l := test1of3Launch()
	audit := &fakeAuditLogWriter{}
	svc := newAuditingProposalSvc(l, audit)

	const newAddr = "cosmos1v93xxer9venks6t2ddkx6mn0wpchyum5nn4cca" // valid, not already a member
	_, err := raiseWith(t, svc, l.ID, proposal.ActionReplaceCommitteeMember, proposal.ReplaceCommitteeMemberPayload{
		OldAddress: testAddr3,
		NewAddress: newAddr,
		NewMoniker: "coord-new",
	})
	require.NoError(t, err)

	pl := auditPayload(t, audit.events, "CommitteeMemberReplaced")
	assert.Equal(t, testAddr3, pl["OldAddress"])
	assert.Equal(t, newAddr, pl["NewAddress"])
	assert.Len(t, pl["NewMembers"], 3, "replacement keeps the committee size")
}

// --- CancelLaunch (proposal path) ---

// A committee member who is NOT the lead can still cancel an early-stage launch — through the M-of-N
// proposal path. The lead's direct endpoint is only a shortcut, so the committee stays in control even
// in DRAFT/PUBLISHED if the lead is absent or adversarial.
func TestProposalService_CancelLaunch_NonLeadEarlyStage_Executes(t *testing.T) {
	l := test1of3Launch() // members testAddr1/2/3, lead testAddr1, M=1 → a single raise executes
	require.Equal(t, launch.StatusDraft, l.Status)
	lRepo := newFakeLaunchRepo(l)
	audit := &fakeAuditLogWriter{}
	svc := NewProposalService(lRepo, newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(),
		newFakeNonceStore(), &fakeVerifier{}, &fakeEventPublisher{}, audit, &fakeTransactor{})

	// testAddr2 is a committee member but NOT the lead.
	raw, _ := json.Marshal(proposal.CancelLaunchPayload{})
	p, err := svc.Raise(context.Background(), l.ID, RaiseInput{
		ActionType: proposal.ActionCancelLaunch,
		Payload:    raw,
		MemberAddr: testAddr2,
		Nonce:      uuid.New().String(),
		Timestamp:  nowTS(),
		Signature:  testSig,
		PubKeyB64:  testSig,
	})
	require.NoError(t, err, "a non-lead committee member may raise CANCEL_LAUNCH in an early stage")
	require.Equal(t, proposal.StatusExecuted, p.Status)

	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	assert.Equal(t, launch.StatusCancelled, stored.Status)
	assert.True(t, hasAuditEvent(audit.events, "LaunchCancelled"), "the cancel must be audited")
}

// The high-stakes path: canceling a GENESIS_READY launch requires M-of-N and, on execution,
// invalidates the readiness confirmations validators already submitted.
func TestProposalService_CancelLaunch_FromGenesisReady_MultiSig_InvalidatesReadiness(t *testing.T) {
	l := mustNewLaunch(t, testCommittee(2, 3)) // 2-of-3
	l.Status = launch.StatusGenesisReady
	rc := &launch.ReadinessConfirmation{
		ID:              uuid.New(),
		LaunchID:        l.ID,
		OperatorAddress: mustAddr(testAddr3),
		ConfirmedAt:     time.Now().UTC(),
	}
	lRepo := newFakeLaunchRepo(l)
	readinessRepo := newFakeReadinessRepo(rc)
	svc := newProposalSvc(lRepo, newFakeJoinRequestRepo(), newFakeProposalRepo(), readinessRepo, newFakeNonceStore(), &fakeVerifier{})

	// The lead raises (1 signature) → still short of the 2-of-3 threshold.
	p, err := raiseWith(t, svc, l.ID, proposal.ActionCancelLaunch, proposal.CancelLaunchPayload{})
	require.NoError(t, err)
	require.Equal(t, proposal.StatusPendingSignatures, p.Status, "one signature is below threshold")
	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	assert.Equal(t, launch.StatusGenesisReady, stored.Status, "not canceled until quorum")
	assert.True(t, readinessRepo.data[rc.ID].IsValid(), "readiness intact until the cancel executes")

	// A second member signs → quorum → executes.
	p2, err := svc.Sign(context.Background(), l.ID, p.ID, SignInput{
		MemberAddr: testAddr2,
		Decision:   proposal.DecisionSign,
		Nonce:      uuid.New().String(),
		Timestamp:  nowTS(),
		Signature:  testSig,
		PubKeyB64:  testSig,
	})
	require.NoError(t, err)
	require.Equal(t, proposal.StatusExecuted, p2.Status)

	stored, _ = lRepo.FindByID(context.Background(), l.ID)
	assert.Equal(t, launch.StatusCancelled, stored.Status)
	assert.False(t, readinessRepo.data[rc.ID].IsValid(), "canceling from GENESIS_READY invalidates readiness")
}

func TestProposalService_CancelLaunch_TerminalRejected(t *testing.T) {
	l := test1of3Launch()
	l.Status = launch.StatusCancelled
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionCancelLaunch, proposal.CancelLaunchPayload{})
	require.ErrorIs(t, err, ports.ErrConflict, "cannot raise CANCEL_LAUNCH on a terminal launch")
}

func TestProposalService_CancelLaunch_NonMemberRejected(t *testing.T) {
	l := test1of1Launch() // only testAddr1 is a committee member
	svc := newProposalSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	raw, _ := json.Marshal(proposal.CancelLaunchPayload{})
	_, err := svc.Raise(context.Background(), l.ID, RaiseInput{
		ActionType: proposal.ActionCancelLaunch,
		Payload:    raw,
		MemberAddr: testAddr2, // not a committee member
		Nonce:      uuid.New().String(),
		Timestamp:  nowTS(),
		Signature:  testSig,
		PubKeyB64:  testSig,
	})
	require.ErrorIs(t, err, ports.ErrForbidden, "a non-member cannot raise a cancel proposal")
}

func TestProposalService_DispatchEvents_PublishesAndAudits(t *testing.T) {
	// dispatchEvents is the single sink for proposal events: every event it pops must be BOTH
	// published (SSE) and written to the audit log. The dispatch loop treats all events uniformly,
	// so proving it for one executing proposal generalizes to every proposal event.
	l := test1of1Launch()
	audit := &fakeAuditLogWriter{}
	pub := &fakeEventPublisher{}
	svc := NewProposalService(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(),
		newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{}, pub, audit, &fakeTransactor{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionExpandCommittee, proposal.ExpandCommitteePayload{
		NewMember: proposal.CommitteeMemberSpec{Address: testAddr2, Moniker: "coord-2"},
	})
	require.NoError(t, err)

	assert.True(t, hasAuditEvent(audit.events, "CommitteeExpanded"), "event must be audited")
	published := false
	for _, ev := range pub.events {
		if ev.EventName() == "CommitteeExpanded" {
			published = true
		}
	}
	assert.True(t, published, "the same event must also be published (dispatch publishes AND audits)")
}

// ---- Two-phase proposal-execution audit ------------------------------------

// failAtAuditWriter fails the Nth Append (1-based); failAt=0 never fails. Records the rest.
type failAtAuditWriter struct {
	events []ports.AuditEvent
	failAt int
	n      int
}

func (w *failAtAuditWriter) Append(_ context.Context, ev ports.AuditEvent) error {
	w.n++
	if w.failAt != 0 && w.n == w.failAt {
		return errors.New("audit down")
	}
	w.events = append(w.events, ev)
	return nil
}

// failingTransactor makes InTransaction fail (execution rolls back) without running the body.
type failingTransactor struct{}

func (failingTransactor) InTransaction(_ context.Context, _ func(context.Context) error) error {
	return errors.New("tx failed")
}

func newTwoPhaseSvc(l *launch.Launch, audit ports.AuditLogWriter, tx ports.Transactor) *ProposalService {
	return NewProposalService(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeProposalRepo(),
		newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{}, &fakeEventPublisher{}, audit, tx)
}

func expandPayload() (proposal.ActionType, proposal.ExpandCommitteePayload) {
	return proposal.ActionExpandCommittee, proposal.ExpandCommitteePayload{
		NewMember: proposal.CommitteeMemberSpec{Address: testAddr2, Moniker: "coord-2"},
	}
}

func TestProposalService_TwoPhaseAudit_IntentThenCompletion(t *testing.T) {
	audit := &failAtAuditWriter{}
	l := test1of1Launch()
	svc := newTwoPhaseSvc(l, audit, &fakeTransactor{})

	action, pl := expandPayload()
	_, err := raiseWith(t, svc, l.ID, action, pl)
	require.NoError(t, err)

	require.Len(t, audit.events, 2, "intent + completion")
	assert.Equal(t, "ProposalExecuting", audit.events[0].EventName, "intent recorded first")
	assert.Equal(t, "CommitteeExpanded", audit.events[1].EventName, "completion recorded after")
}

func TestProposalService_TwoPhaseAudit_AbortsWhenIntentFails(t *testing.T) {
	audit := &failAtAuditWriter{failAt: 1} // the intent write fails
	l := test1of1Launch()
	lRepo := newFakeLaunchRepo(l)
	svc := NewProposalService(lRepo, newFakeJoinRequestRepo(), newFakeProposalRepo(),
		newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{}, &fakeEventPublisher{}, audit, &fakeTransactor{})

	action, pl := expandPayload()
	_, err := raiseWith(t, svc, l.ID, action, pl)
	require.Error(t, err, "an intent-audit failure must abort the proposal")

	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	assert.Equal(t, 1, stored.Committee.TotalN, "the proposal must not have executed")
	assert.Empty(t, audit.events, "nothing recorded — the intent write itself failed")
}

func TestProposalService_TwoPhaseAudit_AbortedEntryOnRollback(t *testing.T) {
	audit := &failAtAuditWriter{}
	l := test1of1Launch()
	svc := newTwoPhaseSvc(l, audit, failingTransactor{})

	action, pl := expandPayload()
	_, err := raiseWith(t, svc, l.ID, action, pl)
	require.Error(t, err, "execution rolled back")

	require.Len(t, audit.events, 2, "intent + aborted")
	assert.Equal(t, "ProposalExecuting", audit.events[0].EventName)
	assert.Equal(t, "ProposalExecutionAborted", audit.events[1].EventName)
}

func TestProposalService_TwoPhaseAudit_FatalOnCompletionFailure(t *testing.T) {
	audit := &failAtAuditWriter{failAt: 2} // intent ok, completion fails
	l := test1of1Launch()
	svc := newTwoPhaseSvc(l, audit, &fakeTransactor{})
	exited := 0
	svc.exit = func(int) { exited++ }

	action, pl := expandPayload()
	_, err := raiseWith(t, svc, l.ID, action, pl)
	require.NoError(t, err, "execution committed; the fatal is a dispatch side effect")
	assert.Equal(t, 1, exited, "a completion-audit failure must trigger the fatal exit")
}

// ---- ExpandCommittee --------------------------------------------------------

func TestProposalService_ApplyExpandCommittee_DefaultThreshold(t *testing.T) {
	// 1-of-1 committee; expand with nil threshold → effective M stays 1 → 1-of-2.
	l := test1of1Launch()
	lRepo := newFakeLaunchRepo(l)
	svc := newProposalSvc(lRepo, newFakeJoinRequestRepo(), newFakeProposalRepo(), newFakeReadinessRepo(), newFakeNonceStore(), &fakeVerifier{})

	_, err := raiseWith(t, svc, l.ID, proposal.ActionExpandCommittee, proposal.ExpandCommitteePayload{
		NewMember: proposal.CommitteeMemberSpec{
			Address: testAddr2,
			Moniker: "coord-2",
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
			Address: testAddr2,
			Moniker: "coord-2",
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
			Address: testAddr2,
			Moniker: "coord-2",
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
			Address: testAddr1,
			Moniker: "dup",
		},
	})
	require.ErrorIs(t, err, ports.ErrConflict, "a duplicate committee member is a 409")
	assert.ErrorIs(t, err, launch.ErrCommitteeMemberExists, "and preserves the domain sentinel")
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
	require.ErrorIs(t, err, ports.ErrBadRequest, "an unknown remove_address is a 400")
	assert.ErrorIs(t, err, launch.ErrCommitteeMemberNotFound, "and preserves the domain sentinel")
}
