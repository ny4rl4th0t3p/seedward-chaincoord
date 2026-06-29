package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

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
// The gentx matches the real-world v0.50+ format (SIGN_MODE_DIRECT, pubkey embedded).
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

	input := validSubmitInput(l)
	_, err := svc.Submit(context.Background(), l.ID, input)
	if err == nil {
		t.Fatal("expected error for conflicting nonce")
	}
}

func TestJoinRequestService_Submit_BadTimestamp(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen
	svc := newJoinReqSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo())

	input := validSubmitInput(l)
	input.Timestamp = expiredTS()
	_, err := svc.Submit(context.Background(), l.ID, input)
	if err == nil {
		t.Fatal("expected error for expired timestamp")
	}
}

func TestJoinRequestService_Submit_SigFails(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen
	verifier := &fakeVerifier{err: ports.ErrUnauthorized}
	svc := NewJoinRequestService(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeNonceStore(), verifier, &fakeGentxValidator{})

	_, err := svc.Submit(context.Background(), l.ID, validSubmitInput(l))
	if err == nil {
		t.Fatal("expected error when signature verification fails")
	}
}

func TestJoinRequestService_Submit_LaunchNotFound(t *testing.T) {
	svc := newJoinReqSvc(newFakeLaunchRepo(), newFakeJoinRequestRepo())
	_, err := svc.Submit(context.Background(), uuid.New(), SubmitInput{
		OperatorAddress: testAddr1,
		Timestamp:       nowTS(),
		Nonce:           uuid.New().String(),
		Signature:       testSig,
	})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestJoinRequestService_Submit_WindowNotOpen(t *testing.T) {
	l := testLaunch() // DRAFT — not open for applications
	svc := newJoinReqSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo())

	_, err := svc.Submit(context.Background(), l.ID, validSubmitInput(l))
	if err == nil {
		t.Fatal("expected error: window not open")
	}
}

func TestJoinRequestService_Submit_RateLimitExceeded(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen
	jrRepo := newFakeJoinRequestRepo()
	jrRepo.setCount(l.ID, testAddr1, maxJoinRequestsPerSubmitter) // already at limit
	svc := newJoinReqSvc(newFakeLaunchRepo(l), jrRepo)

	_, err := svc.Submit(context.Background(), l.ID, validSubmitInput(l))
	if err == nil {
		t.Fatal("expected error: rate limit exceeded")
	}
}

func TestJoinRequestService_Submit_Success(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen
	jrRepo := newFakeJoinRequestRepo()
	svc := newJoinReqSvc(newFakeLaunchRepo(l), jrRepo)

	jr, err := svc.Submit(context.Background(), l.ID, validSubmitInput(l))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if jr.ID == uuid.Nil {
		t.Fatal("expected non-nil join request ID")
	}
	if _, ok := jrRepo.data[jr.ID]; !ok {
		t.Fatal("join request not persisted")
	}
	// Identity split: OperatorAddress is the validator from the gentx (fake: testAddr2);
	// SubmitterAddress is the request signer (validSubmitInput signs as testAddr1). They differ.
	if jr.OperatorAddress.String() != testAddr2 {
		t.Errorf("OperatorAddress = %q, want the gentx validator %q", jr.OperatorAddress, testAddr2)
	}
	if jr.SubmitterAddress.String() != testAddr1 {
		t.Errorf("SubmitterAddress = %q, want the signer %q", jr.SubmitterAddress, testAddr1)
	}
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
	if !errors.As(err, &gErr) {
		t.Fatalf("want *ports.GentxInvalidError, got %v", err)
	}
	if !errors.Is(err, ports.ErrBadRequest) {
		t.Error("GentxInvalidError should unwrap to ErrBadRequest")
	}
}

func TestJoinRequestService_Submit_BuildsParamsFromLaunch(t *testing.T) {
	// Mainnet carries the self-delegation floor from the record.
	lm := testLaunch()
	lm.Status = launch.StatusWindowOpen
	lm.LaunchType = launch.LaunchTypeMainnet
	vm := &fakeGentxValidator{}
	svcM := NewJoinRequestService(newFakeLaunchRepo(lm), newFakeJoinRequestRepo(), newFakeNonceStore(), &fakeVerifier{}, vm)
	if _, err := svcM.Submit(context.Background(), lm.ID, validSubmitInput(lm)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vm.gotParams.ChainID != lm.Record.ChainID ||
		vm.gotParams.BondDenom != lm.Record.Denom ||
		vm.gotParams.Bech32Prefix != lm.Record.Bech32Prefix {
		t.Errorf("params not mapped from record: %+v", vm.gotParams)
	}
	if vm.gotParams.MinSelfDelegation != lm.Record.MinSelfDelegation {
		t.Errorf("mainnet must carry the self-delegation floor, got %q want %q",
			vm.gotParams.MinSelfDelegation, lm.Record.MinSelfDelegation)
	}

	// Testnet does not carry the floor.
	lt := testLaunch()
	lt.Status = launch.StatusWindowOpen
	lt.LaunchType = launch.LaunchTypeTestnet
	vt := &fakeGentxValidator{}
	svcT := NewJoinRequestService(newFakeLaunchRepo(lt), newFakeJoinRequestRepo(), newFakeNonceStore(), &fakeVerifier{}, vt)
	if _, err := svcT.Submit(context.Background(), lt.ID, validSubmitInput(lt)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vt.gotParams.MinSelfDelegation != "" {
		t.Errorf("testnet must not carry a self-delegation floor, got %q", vt.gotParams.MinSelfDelegation)
	}
}

func TestJoinRequestService_Submit_DeadlinePassed(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen
	l.Record.GentxDeadline = time.Now().Add(-1 * time.Hour)
	svc := newJoinReqSvc(newFakeLaunchRepo(l), newFakeJoinRequestRepo())

	_, err := svc.Submit(context.Background(), l.ID, validSubmitInput(l))
	if err == nil {
		t.Fatal("expected error: gentx submission deadline passed")
	}
	if !errors.Is(err, ports.ErrBadRequest) {
		t.Errorf("deadline-passed should be a bad request, got %v", err)
	}
}

// --- GetByID ---

func TestJoinRequestService_GetByID_ForbiddenForOtherValidator(t *testing.T) {
	l := testLaunch()
	jrRepo := newFakeJoinRequestRepo()
	jr := makeJoinRequest(t, l.ID, testAddr1)
	jrRepo.data[jr.ID] = jr
	svc := newJoinReqSvc(newFakeLaunchRepo(l), jrRepo)

	// Caller is testAddr2 (not the owner), not a coordinator.
	_, err := svc.GetByID(context.Background(), jr.ID, testAddr2, false)
	if !errors.Is(err, ports.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestJoinRequestService_GetByID_AllowedForOwner(t *testing.T) {
	l := testLaunch()
	jrRepo := newFakeJoinRequestRepo()
	jr := makeJoinRequest(t, l.ID, testAddr1)
	jrRepo.data[jr.ID] = jr
	svc := newJoinReqSvc(newFakeLaunchRepo(l), jrRepo)

	got, err := svc.GetByID(context.Background(), jr.ID, testAddr1, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != jr.ID {
		t.Errorf("ID mismatch")
	}
}

func TestJoinRequestService_GetByID_AllowedForCoordinator(t *testing.T) {
	l := testLaunch()
	jrRepo := newFakeJoinRequestRepo()
	jr := makeJoinRequest(t, l.ID, testAddr1)
	jrRepo.data[jr.ID] = jr
	svc := newJoinReqSvc(newFakeLaunchRepo(l), jrRepo)

	// Coordinator (isCoordinator=true) can see anyone's request.
	got, err := svc.GetByID(context.Background(), jr.ID, testAddr2, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != jr.ID {
		t.Errorf("ID mismatch")
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Errorf("want 2 items, got %d (total=%d)", len(items), total)
	}
}

// --- helpers ---

// makeJoinRequest builds a minimal join request for the given operator address.
func makeJoinRequest(t *testing.T, launchID uuid.UUID, addr string) *joinrequest.JoinRequest {
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

	jr := joinrequest.New(
		uuid.New(), launchID,
		mustAddr(addr), // operator (validator)
		mustAddr(addr), // submitter
		gentx,
		peer, rpc, "test-memo",
		mustSig(),
		osmosisPubKey,
		time.Now().UTC(),
	)
	return jr
}
