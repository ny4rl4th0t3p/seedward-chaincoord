package services

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-libs/gentxvalidate"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/joinrequest"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

func newJoinReqSvc(launchRepo *fakeLaunchRepo, jrRepo *fakeJoinRequestRepo) *JoinRequestService {
	return NewJoinRequestService(launchRepo, jrRepo, newFakeNonceStore(), &fakeVerifier{}, &fakeGentxValidator{})
}

// fakeGentxValidator is a stub ports.GentxValidator. By default it reports a
// fully passing gentx carrying the osmosis consensus pubkey and a validator
// (operator) address; set failResults to simulate invariant failures. It records
// the last params it received.
type fakeGentxValidator struct {
	failResults   []gentxvalidate.Result
	validatorAddr string // ValidatorAddress on a passing gentx; defaults to testAddr2
	gotParams     gentxvalidate.Params
}

func (f *fakeGentxValidator) Validate(_ []byte, p gentxvalidate.Params) ports.GentxValidationOutcome {
	f.gotParams = p
	if f.failResults != nil {
		return ports.GentxValidationOutcome{Results: f.failResults}
	}
	validator := f.validatorAddr
	if validator == "" {
		validator = testAddr2 // valid operator address, distinct from the default submitter testAddr1
	}
	return ports.GentxValidationOutcome{
		Results:            []gentxvalidate.Result{{Invariant: gentxvalidate.InvWellFormed, OK: true}},
		ConsensusPubKeyB64: osmosisPubKey,
		ValidatorAddress:   validator,
	}
}

// osmosisPubKey is the real Ed25519 consensus key from the Bi23Labs Osmosis gentx (32 bytes).
const osmosisPubKey = "f5DzEhtQbnmXE/WZQsX+I8RljPdEU0u0ncVGtniFyEM="

// validSubmitInput returns a SubmitInput that passes all validation for the given launch.
// The gentx is a minimal body-only fixture (consensus pubkey embedded in MsgCreateValidator);
// auth_info/signatures are omitted because fakeGentxValidator stubs signer extraction.
func validSubmitInput(l *launch.Launch) SubmitInput {
	gentx, _ := json.Marshal(map[string]any{
		"body": map[string]any{
			"messages": []any{
				map[string]any{
					"@type":       "/cosmos.staking.v1beta1.MsgCreateValidator",
					"description": map[string]any{"moniker": "test-validator"},
					"pubkey": map[string]any{
						"@type": "/cosmos.crypto.ed25519.PubKey",
						"key":   osmosisPubKey,
					},
					"value": map[string]any{"denom": l.Record.Denom, "amount": "2000000"},
				},
			},
		},
		"auth_info":  map[string]any{},
		"signatures": []any{},
	})
	return SubmitInput{
		ChainID:         l.Record.ChainID,
		OperatorAddress: testAddr1,
		GentxJSON:       gentx,
		PeerAddress:     "abcdef1234567890abcdef1234567890abcdef12@192.168.1.1:26656",
		RPCEndpoint:     "https://192.168.1.1:26657",
		Memo:            "test",
		Timestamp:       nowTS(),
		Nonce:           uuid.New().String(),
		Signature:       testSig,
	}
}

// --- Submit ---

func TestJoinRequestService_Submit_NonceConflict(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen
	nonces := newFakeNonceStore()
	nonces.consumeErr = ports.ErrConflict
	svc := NewJoinRequestService(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), nonces, &fakeVerifier{}, &fakeGentxValidator{})

	_, err := svc.Submit(context.Background(), l.ID, validSubmitInput(l))
	require.ErrorIs(t, err, ports.ErrConflict, "a rejected nonce must surface as a conflict")
}

func TestJoinRequestService_Submit_BadTimestamp(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen
	svc := newJoinReqSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo())

	input := validSubmitInput(l)
	input.Timestamp = expiredTS()
	_, err := svc.Submit(context.Background(), l.ID, input)
	require.ErrorIs(t, err, ports.ErrUnauthorized, "an expired timestamp is an auth failure")
}

func TestJoinRequestService_Submit_SigFails(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen
	verifier := &fakeVerifier{err: ports.ErrUnauthorized}
	svc := NewJoinRequestService(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeNonceStore(), verifier, &fakeGentxValidator{})

	_, err := svc.Submit(context.Background(), l.ID, validSubmitInput(l))
	require.ErrorIs(t, err, ports.ErrUnauthorized, "a failed signature must map to 401")
}

func TestJoinRequestService_Submit_LaunchNotFound(t *testing.T) {
	svc := newJoinReqSvc(newFakeLaunchRepo(), newFakeJoinRequestRepo())
	_, err := svc.Submit(context.Background(), uuid.New(), SubmitInput{
		OperatorAddress: testAddr1,
		Timestamp:       nowTS(),
		Nonce:           uuid.New().String(),
		Signature:       testSig,
	})
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestJoinRequestService_Submit_InvalidSubmitterAddress(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen
	svc := newJoinReqSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo())

	input := validSubmitInput(l)
	input.OperatorAddress = "not-a-bech32-address"
	_, err := svc.Submit(context.Background(), l.ID, input)
	require.ErrorIs(t, err, ports.ErrBadRequest, "an unparseable submitter address is a 400, not a 500")
}

func TestJoinRequestService_Submit_InvalidValidatorAddressFromGentx(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen
	v := &fakeGentxValidator{validatorAddr: "not-a-bech32-address"}
	svc := NewJoinRequestService(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeNonceStore(), &fakeVerifier{}, v)

	_, err := svc.Submit(context.Background(), l.ID, validSubmitInput(l))
	require.ErrorIs(t, err, ports.ErrBadRequest, "an unparseable validator address from the gentx is a 400, not a 500")
}

func TestJoinRequestService_Submit_WindowNotOpen(t *testing.T) {
	l := testLaunch() // DRAFT — not open for applications
	svc := newJoinReqSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo())

	_, err := svc.Submit(context.Background(), l.ID, validSubmitInput(l))
	require.ErrorIs(t, err, ports.ErrConflict, "a closed window is a launch-state conflict (409), not authz (403)")
}

func TestJoinRequestService_Submit_InvalidConnectionFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*SubmitInput)
	}{
		{"bad peer address", func(in *SubmitInput) { in.PeerAddress = "not-a-peer" }},
		{"bad rpc endpoint", func(in *SubmitInput) { in.RPCEndpoint = "://bad-url" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := testLaunch()
			l.Status = launch.StatusWindowOpen
			svc := newJoinReqSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo())
			in := validSubmitInput(l)
			tc.mutate(&in)

			_, err := svc.Submit(context.Background(), l.ID, in)
			require.ErrorIs(t, err, ports.ErrBadRequest)
		})
	}
}

func TestJoinRequestService_Submit_RateLimitExceeded(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen
	jrRepo := newFakeJoinRequestRepo()
	jrRepo.setCount(l.ID, testAddr1, maxJoinRequestsPerSubmitter) // already at limit
	svc := newJoinReqSvc(newFakeLaunchRepo(l), jrRepo)

	_, err := svc.Submit(context.Background(), l.ID, validSubmitInput(l))
	// Must unwrap to ErrTooManyRequests so the API maps it to 429, not 500.
	require.ErrorIs(t, err, ports.ErrTooManyRequests)
	assert.ErrorIs(t, err, ports.ErrSubmissionCapReached, "want the specific cap sentinel")
}

func TestJoinRequestService_Submit_Success(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen
	jrRepo := newFakeJoinRequestRepo()
	svc := newJoinReqSvc(newFakeLaunchRepo(l), jrRepo)

	jr, err := svc.Submit(context.Background(), l.ID, validSubmitInput(l))
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, jr.ID, "expected non-nil join request ID")
	_, ok := jrRepo.data[jr.ID]
	require.True(t, ok, "join request not persisted")

	// Identity split: OperatorAddress is the validator from the gentx (fake: testAddr2);
	// SubmitterAddress is the request signer (validSubmitInput signs as testAddr1). They differ.
	assert.Equal(t, testAddr2, jr.OperatorAddress.String(), "OperatorAddress should be the gentx validator")
	assert.Equal(t, testAddr1, jr.SubmitterAddress.String(), "SubmitterAddress should be the signer")
}

// v1 membership: the gate is on the hot SUBMITTER address (committee ∪ members), not the gentx
// validator — validators are vetted by committee approval, not allowlisted. A committee submitter
// passes (Submit_Success); an allowlisted non-committee member also passes.
func TestJoinRequestService_Submit_AllowlistedMemberAllowed(t *testing.T) {
	l := test1of1Launch() // committee = testAddr1 only
	l.Status = launch.StatusWindowOpen
	l.Allowlist = launch.NewAllowlist([]launch.AccountID{mustAddr(testAddr2)}) // member, not committee
	svc := newJoinReqSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo())

	in := validSubmitInput(l)
	in.OperatorAddress = testAddr2 // submit as the allowlisted member
	_, err := svc.Submit(context.Background(), l.ID, in)
	require.NoError(t, err, "an allowlisted member may submit")
}

// A non-member (neither committee nor on the members list) is rejected before any gentx work —
// a leaked launch URL grants nothing.
func TestJoinRequestService_Submit_NonMemberNotFound(t *testing.T) {
	l := test1of1Launch() // committee = testAddr1 only; empty allowlist
	l.Status = launch.StatusWindowOpen
	svc := newJoinReqSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo())

	in := validSubmitInput(l)
	in.OperatorAddress = testAddr2 // not committee, not a member
	_, err := svc.Submit(context.Background(), l.ID, in)
	// Not-found, not forbidden: a 403 would distinguish a real private launch from a
	// nonexistent one and leak its existence (mirrors GetLaunch).
	require.ErrorIs(t, err, ports.ErrNotFound, "a non-member must not learn the launch exists")
}

func TestJoinRequestService_Submit_GentxInvalid(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen
	validator := &fakeGentxValidator{failResults: []gentxvalidate.Result{
		{Invariant: gentxvalidate.InvBondDenom, OK: false, Reason: "denom mismatch"},
	}}
	svc := NewJoinRequestService(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeNonceStore(), &fakeVerifier{}, validator)

	_, err := svc.Submit(context.Background(), l.ID, validSubmitInput(l))
	var gErr *ports.GentxInvalidError
	require.ErrorAs(t, err, &gErr)
	assert.ErrorIs(t, err, ports.ErrBadRequest, "GentxInvalidError should unwrap to ErrBadRequest")
}

func TestJoinRequestService_Submit_BuildsParamsFromLaunch(t *testing.T) {
	// Mainnet carries the self-delegation floor from the record.
	lm := testLaunch()
	lm.Status = launch.StatusWindowOpen
	lm.LaunchType = launch.LaunchTypeMainnet
	vm := &fakeGentxValidator{}
	svcM := NewJoinRequestService(newFakeLaunchRepo(lm), newFakeJoinRequestRepo(), newFakeNonceStore(), &fakeVerifier{}, vm)
	_, err := svcM.Submit(context.Background(), lm.ID, validSubmitInput(lm))
	require.NoError(t, err)
	assert.Equal(t, lm.Record.ChainID, vm.gotParams.ChainID)
	assert.Equal(t, lm.Record.Denom, vm.gotParams.BondDenom)
	assert.Equal(t, lm.Record.Bech32Prefix, vm.gotParams.Bech32Prefix)
	assert.Equal(t, lm.Record.MinSelfDelegation, vm.gotParams.MinSelfDelegation,
		"mainnet must carry the self-delegation floor")

	// Testnet does not carry the floor.
	lt := testLaunch()
	lt.Status = launch.StatusWindowOpen
	lt.LaunchType = launch.LaunchTypeTestnet
	vt := &fakeGentxValidator{}
	svcT := NewJoinRequestService(newFakeLaunchRepo(lt), newFakeJoinRequestRepo(), newFakeNonceStore(), &fakeVerifier{}, vt)
	_, err = svcT.Submit(context.Background(), lt.ID, validSubmitInput(lt))
	require.NoError(t, err)
	assert.Empty(t, vt.gotParams.MinSelfDelegation, "testnet must not carry a self-delegation floor")
}

func TestJoinRequestService_Submit_DeadlinePassed(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen
	l.Record.GentxDeadline = time.Now().Add(-1 * time.Hour)
	svc := newJoinReqSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo())

	_, err := svc.Submit(context.Background(), l.ID, validSubmitInput(l))
	require.Error(t, err, "expected error: gentx submission deadline passed")
	assert.ErrorIs(t, err, ports.ErrBadRequest, "deadline-passed should be a bad request")
}

// --- dedup: validator identity + status ---

// A re-submission for a validator with a PENDING request supersedes it: the old
// request is expired and the new one replaces it (the new gentx is validator-signed,
// so its content is self-authorized). The shared consensus key does not block this,
// because the superseded request is terminal and excluded from the active-key check.
func TestJoinRequestService_Submit_SupersedesPendingFromSameValidator(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen
	jrRepo := newFakeJoinRequestRepo()
	svc := newJoinReqSvc(newFakeLaunchRepo(l), jrRepo)

	first, err := svc.Submit(context.Background(), l.ID, validSubmitInput(l))
	require.NoError(t, err, "first submit")
	second, err := svc.Submit(context.Background(), l.ID, validSubmitInput(l))
	require.NoError(t, err, "second submit should supersede, not fail")

	assert.NotEqual(t, first.ID, second.ID, "expected a new request")
	assert.Equal(t, joinrequest.StatusPending, second.Status)
	assert.Equal(t, joinrequest.StatusExpired, jrRepo.data[first.ID].Status, "prior request should be superseded")

	active, err := jrRepo.FindActiveByValidator(context.Background(), l.ID, testAddr2)
	require.NoError(t, err)
	assert.Equal(t, second.ID, active.ID, "the new request should be the active one")
}

// If superseding the prior PENDING request fails to persist, Submit surfaces the error.
func TestJoinRequestService_Submit_SupersedeSaveError(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen
	pending := makeJoinRequestSplit(t, l.ID, testAddr2, testAddr1) // active PENDING for the validator
	jrRepo := newFakeJoinRequestRepo(pending)
	jrRepo.saveErr = ports.ErrConflict // the supersede Save() fails
	svc := newJoinReqSvc(newFakeLaunchRepo(l), jrRepo)

	_, err := svc.Submit(context.Background(), l.ID, validSubmitInput(l))
	require.Error(t, err, "a failed supersede save must abort the submission")
}

// A validator with an APPROVED request is locked: a new submission is rejected with
// the specific ErrValidatorAlreadyApproved (which still maps to 409).
func TestJoinRequestService_Submit_RejectsWhenValidatorHasApproved(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen
	approved := makeJoinRequestSplit(t, l.ID, testAddr2, testAddr1) // validator = fake's testAddr2
	require.NoError(t, approved.Approve(uuid.New()))
	svc := newJoinReqSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(approved))

	_, err := svc.Submit(context.Background(), l.ID, validSubmitInput(l))
	require.ErrorIs(t, err, ports.ErrValidatorAlreadyApproved, "locked validator")
	assert.ErrorIs(t, err, ports.ErrConflict, "should still map to 409")
}

// A prior REJECTED/EXPIRED request for the validator never blocks a fresh submission,
// and does not hold the consensus key (terminal rows are excluded from dedup).
func TestJoinRequestService_Submit_TerminalRequestDoesNotBlock(t *testing.T) {
	cases := []struct {
		name       string
		transition func(*joinrequest.JoinRequest) error
	}{
		{"rejected", func(jr *joinrequest.JoinRequest) error { return jr.Reject("fix your commission") }},
		{"expired", func(jr *joinrequest.JoinRequest) error { return jr.Expire() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := testLaunch()
			l.Status = launch.StatusWindowOpen
			prior := makeJoinRequestSplit(t, l.ID, testAddr2, testAddr1)
			require.NoError(t, tc.transition(prior))
			svc := newJoinReqSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(prior))

			fresh, err := svc.Submit(context.Background(), l.ID, validSubmitInput(l))
			require.NoError(t, err, "%s prior request should not block", tc.name)
			assert.Equal(t, joinrequest.StatusPending, fresh.Status)
		})
	}
}

// A different validator already holding the gentx's consensus key blocks the
// submission with the distinct ErrConsensusKeyAlreadyUsed (still a 409). This is
// not the supersede path: the active holder is a *different* validator identity.
func TestJoinRequestService_Submit_RejectsDuplicateConsensusKey(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen
	// Seed an active request for a DIFFERENT validator (testAddr3) carrying the same
	// consensus key the fake validator will echo (osmosisPubKey).
	other := makeJoinRequestSplit(t, l.ID, testAddr3, testAddr1)
	svc := newJoinReqSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo(other))

	// Incoming submission is validator testAddr2 (distinct), same consensus key.
	_, err := svc.Submit(context.Background(), l.ID, validSubmitInput(l))
	require.ErrorIs(t, err, ports.ErrConsensusKeyAlreadyUsed, "duplicate consensus key")
	assert.ErrorIs(t, err, ports.ErrConflict, "should still map to 409")
}

// --- GetByID ---

func TestJoinRequestService_GetByID_ForbiddenForOtherValidator(t *testing.T) {
	l := testLaunch()
	jrRepo := newFakeJoinRequestRepo()
	jr := makeJoinRequest(t, l.ID, testAddr1)
	jrRepo.data[jr.ID] = jr
	svc := newJoinReqSvc(newFakeLaunchRepo(l), jrRepo)

	// Caller is testAddr2 (not the owner), not a committee member.
	_, err := svc.GetByID(context.Background(), jr.ID, testAddr2, false)
	require.ErrorIs(t, err, ports.ErrForbidden)
}

func TestJoinRequestService_GetByID_AllowedForOwner(t *testing.T) {
	l := testLaunch()
	jrRepo := newFakeJoinRequestRepo()
	jr := makeJoinRequest(t, l.ID, testAddr1)
	jrRepo.data[jr.ID] = jr
	svc := newJoinReqSvc(newFakeLaunchRepo(l), jrRepo)

	got, err := svc.GetByID(context.Background(), jr.ID, testAddr1, false)
	require.NoError(t, err)
	assert.Equal(t, jr.ID, got.ID)
}

func TestJoinRequestService_GetByID_AllowedForCommitteeMember(t *testing.T) {
	l := testLaunch()
	jrRepo := newFakeJoinRequestRepo()
	jr := makeJoinRequest(t, l.ID, testAddr1)
	jrRepo.data[jr.ID] = jr
	svc := newJoinReqSvc(newFakeLaunchRepo(l), jrRepo)

	// Committee member (isCommitteeMember=true) can see anyone's request.
	got, err := svc.GetByID(context.Background(), jr.ID, testAddr2, true)
	require.NoError(t, err)
	assert.Equal(t, jr.ID, got.ID)
}

func TestJoinRequestService_GetByID_AllowedForSubmitter(t *testing.T) {
	l := testLaunch()
	jrRepo := newFakeJoinRequestRepo()
	// Validator (operator) is testAddr2; the request was submitted/signed by testAddr1.
	jr := makeJoinRequestSplit(t, l.ID, testAddr2, testAddr1)
	jrRepo.data[jr.ID] = jr
	svc := newJoinReqSvc(newFakeLaunchRepo(l), jrRepo)

	// The submitter (testAddr1), though not the validator, is a party to the request.
	got, err := svc.GetByID(context.Background(), jr.ID, testAddr1, false)
	require.NoError(t, err, "submitter should be allowed")
	assert.Equal(t, jr.ID, got.ID)

	// An address that is neither validator nor submitter is still forbidden.
	_, err = svc.GetByID(context.Background(), jr.ID, testAddr3, false)
	require.ErrorIs(t, err, ports.ErrForbidden, "unrelated address")
}

// --- ListForLaunch ---

func TestJoinRequestService_ListForLaunch_ReturnsAll(t *testing.T) {
	l := testLaunch()
	jrRepo := newFakeJoinRequestRepo()
	jr1 := makeJoinRequest(t, l.ID, testAddr1)
	jr2 := makeJoinRequest(t, l.ID, testAddr2)
	jrRepo.data[jr1.ID] = jr1
	jrRepo.data[jr2.ID] = jr2
	svc := newJoinReqSvc(newFakeLaunchRepo(l), jrRepo)

	items, total, err := svc.ListForLaunch(context.Background(), l.ID, nil, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Len(t, items, 2)
}

// --- helpers ---

// makeJoinRequest builds a minimal join request for the given operator address.
func makeJoinRequest(t *testing.T, launchID uuid.UUID, addr string) *joinrequest.JoinRequest {
	t.Helper()
	return makeJoinRequestSplit(t, launchID, addr, addr)
}

// makeJoinRequestSplit builds a join request whose validator (operator) and
// submitter identities may differ, exercising the submitter-vs-validator split.
func makeJoinRequestSplit(t *testing.T, launchID uuid.UUID, validatorAddr, submitterAddr string) *joinrequest.JoinRequest {
	t.Helper()
	rec := testChainRecord()
	gentx, _ := json.Marshal(map[string]any{
		"body": map[string]any{
			"messages": []any{
				map[string]any{
					"@type":       "/cosmos.staking.v1beta1.MsgCreateValidator",
					"description": map[string]any{"moniker": "test-validator"},
					"pubkey": map[string]any{
						"@type": "/cosmos.crypto.ed25519.PubKey",
						"key":   osmosisPubKey,
					},
					"value": map[string]any{"denom": rec.Denom, "amount": "2000000"},
				},
			},
		},
		"auth_info":  map[string]any{},
		"signatures": []any{},
	})
	peer, _ := launch.NewPeerAddress("abcdef1234567890abcdef1234567890abcdef12@192.168.1.1:26656")
	rpc, _ := launch.NewRPCEndpoint("https://192.168.1.1:26657")

	return joinrequest.New(
		uuid.New(), launchID,
		mustAddr(validatorAddr), // operator (validator)
		mustAddr(submitterAddr), // submitter
		gentx,
		peer, rpc, "test-memo",
		mustSig(),
		osmosisPubKey,
		time.Now().UTC(),
	)
}

func TestJoinRequestService_GetByID_CrossHRPParty(t *testing.T) {
	// operator (validator) = testAddr1, submitter = testAddr2.
	jr := makeJoinRequestSplit(t, uuid.New(), testAddr1, testAddr2)
	svc := newJoinReqSvc(newFakeLaunchRepo(), newFakeJoinRequestRepo(jr))
	ctx := context.Background()

	// The operator authenticating under a DIFFERENT HRP than stored is still the party → 200.
	osmoOperator, err := mustAddr(testAddr1).Bech32("osmo")
	require.NoError(t, err)
	got, err := svc.GetByID(ctx, jr.ID, osmoOperator, false)
	require.NoError(t, err, "operator under a different HRP must be authorized")
	assert.Equal(t, jr.ID, got.ID)

	// The submitter under a different HRP is likewise authorized.
	osmoSubmitter, err := mustAddr(testAddr2).Bech32("osmo")
	require.NoError(t, err)
	_, err = svc.GetByID(ctx, jr.ID, osmoSubmitter, false)
	require.NoError(t, err, "submitter under a different HRP must be authorized")

	// A true non-party (not operator, not submitter, not committee) → 403.
	_, err = svc.GetByID(ctx, jr.ID, testAddr3, false)
	require.ErrorIs(t, err, ports.ErrForbidden)
}

// ---- grouped-by-submitter read-model ----

func TestJoinRequestService_ListGroupedBySubmitter(t *testing.T) {
	l := test1of1Launch() // committee = testAddr1
	l.Allowlist = launch.NewAllowlistFromMembers([]launch.Member{
		{Address: mustAddr(testAddr2), Label: "acme"},
		{Address: mustAddr(testAddr3), Label: "beta"},
	})
	jrRepo := newFakeJoinRequestRepo(
		makeJoinRequestSplit(t, l.ID, testAddr1, testAddr2), // submitter acme, validator testAddr1
		makeJoinRequestSplit(t, l.ID, testAddr3, testAddr2), // submitter acme, validator testAddr3
		makeJoinRequestSplit(t, l.ID, testAddr2, testAddr3), // submitter beta, validator testAddr2
	)
	svc := newJoinReqSvc(newFakeLaunchRepo(l), jrRepo)

	groups, err := svc.ListGroupedBySubmitter(context.Background(), l.ID, testAddr1)
	require.NoError(t, err)
	require.Len(t, groups, 2)

	// Deterministic: groups sorted by submitter address.
	assert.Less(t, groups[0].SubmitterAddress.String(), groups[1].SubmitterAddress.String())

	byAddr := make(map[string]SubmitterGroup, len(groups))
	for _, g := range groups {
		byAddr[g.SubmitterAddress.String()] = g
	}
	acme := byAddr[testAddr2]
	assert.Equal(t, "acme", acme.Label, "group carries the submitter's members-list label")
	assert.Len(t, acme.Requests, 2, "acme submitted two validators")
	beta := byAddr[testAddr3]
	assert.Equal(t, "beta", beta.Label)
	assert.Len(t, beta.Requests, 1)
}

func TestJoinRequestService_ListGroupedBySubmitter_NotCommittee(t *testing.T) {
	l := test1of1Launch() // committee = testAddr1 only
	svc := newJoinReqSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo())
	_, err := svc.ListGroupedBySubmitter(context.Background(), l.ID, testAddr2) // not committee
	require.ErrorIs(t, err, ports.ErrForbidden)
}

func TestJoinRequestService_ListGroupedBySubmitter_LaunchNotFound(t *testing.T) {
	svc := newJoinReqSvc(newFakeLaunchRepo(), newFakeJoinRequestRepo())
	_, err := svc.ListGroupedBySubmitter(context.Background(), uuid.New(), testAddr1)
	require.ErrorIs(t, err, ports.ErrNotFound)
}
