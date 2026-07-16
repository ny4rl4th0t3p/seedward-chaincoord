package api_test

// Comprehensive HTTP handler tests.
// Each test exercises a single endpoint, focusing on HTTP-layer concerns:
// authentication, Content-Type enforcement, path-parameter parsing, and
// service-error → status-code mapping.
// Business logic is already covered by the service-layer tests.

import (
	"encoding/json"
	"net/http"
	"strings"
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

// ---- shared helpers ---------------------------------------------------------

const (
	testPeerAddress = "1234567890abcdef1234567890abcdef12345678@127.0.0.1:26656"
	testRPCURL      = "http://localhost:26657"
)

func mustPeerAddr(s string) launch.PeerAddress {
	p, err := launch.NewPeerAddress(s)
	if err != nil {
		panic(err)
	}
	return p
}

// acctKey is the HRP-independent account key the auth service keys challenge state
// on — challenge fixtures must seed under this, not the bech32 address.
func acctKey(addr string) string {
	return launch.MustNewAccountID(addr).Hex()
}

func mustRPCEndpoint(s string) launch.RPCEndpoint {
	e, err := launch.NewRPCEndpoint(s)
	if err != nil {
		panic(err)
	}
	return e
}

// windowOpenLaunch returns the testLaunch with status forced to WINDOW_OPEN.
func windowOpenLaunch() *launch.Launch {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen
	return l
}

// soloCommitteeLaunch returns a DRAFT launch with a 1-of-1 committee (testAddr1 only).
// Used to verify that testAddr2 is NOT a committee member.
func soloCommitteeLaunch() *launch.Launch {
	l := testLaunch()
	l.Committee = launch.Committee{
		ID: uuid.New(),
		Members: []launch.CommitteeMember{
			{Address: mustAddr(testAddr1), Moniker: "coord-1", PubKeyB64: "AAAA"},
		},
		ThresholdM:  1,
		TotalN:      1,
		LeadAddress: mustAddr(testAddr1),
		CreatedAt:   time.Now().UTC(),
	}
	return l
}

// genesisReadyLaunch returns a launch in GENESIS_READY status with hashes set.
func genesisReadyLaunch() *launch.Launch {
	l := testLaunch()
	l.Status = launch.StatusGenesisReady
	l.FinalGenesisSHA256 = "final-genesis-hash"
	// BinarySHA256 is testLaunch's chain-record value (a valid 64-hex SHA-256).
	return l
}

// testJoinRequest builds a minimal PENDING join request for seeding.
func testJoinRequest(launchID uuid.UUID, operatorAddr string) *joinrequest.JoinRequest {
	return &joinrequest.JoinRequest{
		ID:                uuid.New(),
		LaunchID:          launchID,
		OperatorAddress:   mustAddr(operatorAddr),
		ConsensusPubKey:   "AAAA",
		GentxJSON:         json.RawMessage(`{"chain_id":"testchain-1"}`),
		PeerAddress:       mustPeerAddr(testPeerAddress),
		RPCEndpoint:       mustRPCEndpoint(testRPCURL),
		SubmittedAt:       time.Now().UTC(),
		OperatorSignature: mustSig(),
		Status:            joinrequest.StatusPending,
	}
}

// testApprovedJoinRequest returns a JoinRequest in APPROVED status.
func testApprovedJoinRequest(launchID uuid.UUID, operatorAddr string) *joinrequest.JoinRequest {
	jr := testJoinRequest(launchID, operatorAddr)
	jr.Status = joinrequest.StatusApproved
	propID := uuid.New()
	jr.ApprovedByProposal = &propID
	return jr
}

// testProposalObj builds a minimal PENDING_SIGNATURES proposal for seeding.
func testProposalObj(launchID uuid.UUID) *proposal.Proposal {
	return &proposal.Proposal{
		ID:         uuid.New(),
		LaunchID:   launchID,
		ActionType: proposal.ActionCloseApplicationWindow,
		Payload:    []byte(`{}`),
		ProposedBy: mustAddr(testAddr1),
		ProposedAt: time.Now().UTC(),
		TTLExpires: time.Now().Add(48 * time.Hour).UTC(),
		Status:     proposal.StatusPendingSignatures,
		Signatures: []proposal.SignatureEntry{},
	}
}

// validLaunchBody returns a minimal valid POST /launch body.
func validLaunchBody() []byte {
	return []byte(`{
		"record":{
			"chain_id":"newchain-1","chain_name":"New Chain","bech32_prefix":"cosmos",
			"binary_name":"newchaind","binary_version":"v1.0.0","binary_sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			"denom":"unew","min_self_delegation":"1000000",
			"max_commission_rate":"0.20","max_commission_change_rate":"0.01",
			"gentx_deadline":"2026-12-01T00:00:00Z",
			"min_validator_count":1
		},
		"launch_type":"TESTNET",
		"committee":{
			"members":[{"address":"` + testAddr1 + `","moniker":"c1","pub_key_b64":"AAAA"}],
			"threshold_m":1,"total_n":1,
			"lead_address":"` + testAddr1 + `",
			"creation_signature":"` + testSig + `"
		}
	}`)
}

// validCommitteeBody returns a minimal valid POST /launch/{id}/committee body.
func validCommitteeBody() []byte {
	return []byte(`{
		"members":[{"address":"` + testAddr1 + `","moniker":"c1","pub_key_b64":"AAAA"}],
		"threshold_m":1,"total_n":1,
		"lead_address":"` + testAddr1 + `",
		"creation_signature":"` + testSig + `"
	}`)
}

// ---- GET /healthz -----------------------------------------------------------

func TestHandleHealthz(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/healthz", nil, nil)
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
}

// ---- middleware -------------------------------------------------------------

func TestMiddleware_RequireAuth_NoToken(t *testing.T) {
	h := newHarness(t)
	// POST /launch requires auth; send without Authorization header.
	w := h.doJSON("POST", "/launch", validLaunchBody())
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestMiddleware_RequireAuth_InvalidToken(t *testing.T) {
	h := newHarness(t)
	w := h.doAuthJSON("POST", "/launch", validLaunchBody(), "bad-token")
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestMiddleware_OptionalAuth_NoToken_Passes(t *testing.T) {
	h := newHarness(t)
	// GET /launches uses optionalAuth — unauthenticated calls are allowed.
	w := h.do("GET", "/launches", nil, nil)
	assertStatusCode(t, w, http.StatusOK)
}

func TestMiddleware_RequireJSONBody_WrongContentType(t *testing.T) {
	h := newHarness(t)
	tok := h.seedSession(testAddr1)
	w := h.do("POST", "/launch", validLaunchBody(), map[string]string{
		"Content-Type":  "text/plain",
		"Authorization": "Bearer " + tok,
	})
	assertStatusCode(t, w, http.StatusUnsupportedMediaType)
}

// ---- POST /auth/challenge ---------------------------------------------------

func TestHandleAuthChallenge_Success(t *testing.T) {
	h := newHarness(t)
	w := h.doJSON("POST", "/auth/challenge", jsonBody(`{"operator_address":"`+testAddr1+`"}`))
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
}

func TestHandleAuthChallenge_MissingOperatorAddress(t *testing.T) {
	h := newHarness(t)
	w := h.doJSON("POST", "/auth/challenge", jsonBody(`{"operator_address":""}`))
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleAuthChallenge_BadJSON(t *testing.T) {
	h := newHarness(t)
	w := h.doJSON("POST", "/auth/challenge", jsonBody(`not json`))
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleAuthChallenge_RateLimited(t *testing.T) {
	h := newHarness(t)
	// Instruct the fake challenge store to return ErrTooManyRequests.
	h.challenges.issueErr = ports.ErrTooManyRequests
	w := h.doJSON("POST", "/auth/challenge", jsonBody(`{"operator_address":"`+testAddr1+`"}`))
	assertStatusCode(t, w, http.StatusTooManyRequests)
}

func TestHandleAuthChallenge_RateLimitDisabled(t *testing.T) {
	h := newHarnessRateLimitDisabled(t)
	body := jsonBody(`{"operator_address":"` + testAddr1 + `"}`)
	// Send more requests than the default per-minute limit (challengeRatePerMin=10).
	// All must succeed — the HTTP middleware rate limiter must be bypassed.
	for i := range 15 {
		w := h.doJSON("POST", "/auth/challenge", body)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d: got 429; rate limiter not disabled", i+1)
		}
		assertStatusCode(t, w, http.StatusOK)
	}
}

func TestValidatorWriteEndpoints_RateLimitDisabled(t *testing.T) {
	h := newHarnessRateLimitDisabled(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	path := "/launch/" + l.ID.String() + "/join"
	body := jsonBody(`{
		"operator_address":"` + testAddr1 + `",
		"moniker":"val",
		"peer_address":"` + testPeerAddress + `",
		"consensus_pub_key":"AAAA",
		"nonce":"n1",
		"timestamp":"` + nowTS() + `",
		"signature":"` + testSig + `"
	}`)
	// Send more requests than the default per-minute limit (validatorRatePerMin=60).
	// All must succeed — the HTTP middleware rate limiter must be bypassed.
	for i := range 65 {
		h.joinReqs.data = make(map[uuid.UUID]*joinrequest.JoinRequest) // reset so store doesn't conflict
		w := h.doAuthJSON("POST", path, body, tok)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d: got 429; validator rate limiter not disabled", i+1)
		}
	}
}

// ---- POST /auth/verify ------------------------------------------------------

func TestHandleAuthVerify_Success(t *testing.T) {
	h := newHarness(t)
	// Pre-seed the challenge so Consume finds it (keyed on the account).
	h.challenges.data[acctKey(testAddr1)] = "my-challenge"
	body := []byte(`{
		"operator_address":"` + testAddr1 + `",
		"challenge":"my-challenge",
		"nonce":"nonce-av1",
		"timestamp":"` + nowTS() + `",
		"signature":"` + testSig + `"
	}`)
	w := h.doJSON("POST", "/auth/verify", body)
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
}

func TestHandleAuthVerify_BadJSON(t *testing.T) {
	h := newHarness(t)
	w := h.doJSON("POST", "/auth/verify", jsonBody(`not json`))
	assertStatusCode(t, w, http.StatusBadRequest)
}

// ---- DELETE /auth/session ---------------------------------------------------

func TestHandleAuthRevoke_Success(t *testing.T) {
	h := newHarness(t)
	tok := h.seedSession(testAddr1)
	w := h.do("DELETE", "/auth/session", nil, map[string]string{
		"Authorization": "Bearer " + tok,
	})
	assertStatusCode(t, w, http.StatusNoContent)
}

func TestHandleAuthRevoke_MissingToken(t *testing.T) {
	h := newHarness(t)
	w := h.do("DELETE", "/auth/session", nil, nil)
	assertStatusCode(t, w, http.StatusUnauthorized)
}

// ---- POST /launch -----------------------------------------------------------

func TestHandleLaunchCreate_NoAuth(t *testing.T) {
	h := newHarness(t)
	w := h.doJSON("POST", "/launch", validLaunchBody())
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestHandleLaunchCreate_WrongContentType(t *testing.T) {
	h := newHarness(t)
	tok := h.seedSession(testAddr1)
	w := h.do("POST", "/launch", validLaunchBody(), map[string]string{
		"Content-Type":  "application/x-www-form-urlencoded",
		"Authorization": "Bearer " + tok,
	})
	assertStatusCode(t, w, http.StatusUnsupportedMediaType)
}

func TestHandleLaunchCreate_BadJSON(t *testing.T) {
	h := newHarness(t)
	tok := h.seedSession(testAddr1)
	w := h.doAuthJSON("POST", "/launch", jsonBody(`not json`), tok)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleLaunchCreate_BadCommissionRate(t *testing.T) {
	h := newHarness(t)
	tok := h.seedSession(testAddr1)
	body := []byte(`{
		"record":{"chain_id":"x","max_commission_rate":"bad","max_commission_change_rate":"0.01"},
		"launch_type":"TESTNET",
		"committee":{"members":[],"threshold_m":1,"total_n":1,
			"lead_address":"` + testAddr1 + `","creation_signature":"` + testSig + `"}
	}`)
	w := h.doAuthJSON("POST", "/launch", body, tok)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleLaunchCreate_InvalidRecord_BadRequest(t *testing.T) {
	// A body that decodes to a well-shaped record but fails domain validation (empty chain_id)
	// must render a 400 "bad_request" envelope — not a 500 "internal_error". Regression guard for
	// the CreateLaunch mapping that previously surfaced New's validation error with no status.
	h := newHarness(t)
	tok := h.seedSession(testAddr1)
	body := []byte(`{
		"record":{
			"chain_id":"","chain_name":"New Chain","bech32_prefix":"cosmos",
			"binary_name":"newchaind","binary_version":"v1.0.0","binary_sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			"denom":"unew","min_self_delegation":"1000000",
			"max_commission_rate":"0.20","max_commission_change_rate":"0.01",
			"gentx_deadline":"2026-12-01T00:00:00Z",
			"min_validator_count":1
		},
		"launch_type":"TESTNET",
		"committee":{
			"members":[{"address":"` + testAddr1 + `","moniker":"c1","pub_key_b64":"AAAA"}],
			"threshold_m":1,"total_n":1,
			"lead_address":"` + testAddr1 + `",
			"creation_signature":"` + testSig + `"
		}
	}`)
	w := h.doAuthJSON("POST", "/launch", body, tok)
	assertStatusCode(t, w, http.StatusBadRequest)

	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	errObj, _ := resp["error"].(map[string]any)
	if code, _ := errObj["code"].(string); code != "bad_request" {
		t.Errorf("want error code bad_request, got %q", code)
	}
}

func TestHandleLaunchCreate_Success(t *testing.T) {
	h := newHarness(t)
	tok := h.seedSession(testAddr1)
	w := h.doAuthJSON("POST", "/launch", validLaunchBody(), tok)
	assertStatusCode(t, w, http.StatusCreated)
	assertContentTypeJSON(t, w)
}

// ---- GET /launches ----------------------------------------------------------

func TestHandleLaunchList_Empty(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/launches", nil, nil)
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
}

func TestHandleLaunchList_WithData(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.do("GET", "/launches", nil, nil)
	assertStatusCode(t, w, http.StatusOK)
}

// ---- GET /launch/{id} -------------------------------------------------------

func TestHandleLaunchGet_BadUUID(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/launch/not-a-uuid", nil, nil)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleLaunchGet_NotFound(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/launch/"+uuid.New().String(), nil, nil)
	assertStatusCode(t, w, http.StatusNotFound)
}

func TestHandleLaunchGet_Success(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.getAsMember("/launch/" + l.ID.String())
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
}

// ---- GET /launch/{id}/chain-hint --------------------------------------------

func TestHandleChainHint_BadUUID(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/launch/not-a-uuid/chain-hint", nil, nil)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleChainHint_NotFound(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/launch/"+uuid.New().String()+"/chain-hint", nil, nil)
	assertStatusCode(t, w, http.StatusNotFound)
}

func TestHandleChainHint_AnonymousHidden(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	// No token — a non-member (here, anonymous) must not learn the launch exists: 404.
	w := h.do("GET", "/launch/"+l.ID.String()+"/chain-hint", nil, nil)
	assertStatusCode(t, w, http.StatusNotFound)
}

func TestHandleChainHint_MemberVisible(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	// A committee member reads the hint after authenticating (any HRP) → 200.
	w := h.getAsMember("/launch/" + l.ID.String() + "/chain-hint")
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
}

func TestHandleChainHint_ResponseFields(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.getAsMember("/launch/" + l.ID.String() + "/chain-hint")
	assertStatusCode(t, w, http.StatusOK)
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, field := range []string{"chain_id", "chain_name", "bech32_prefix", "denom"} {
		if _, ok := body[field]; !ok {
			t.Errorf("response missing field %q", field)
		}
	}
}

// ---- PATCH /launch/{id} -----------------------------------------------------

func TestHandleLaunchPatch_NoAuth(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.doJSON("PATCH", "/launch/"+l.ID.String(), jsonBody(`{"chain_name":"Updated"}`))
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestHandleLaunchPatch_BadUUID(t *testing.T) {
	h := newHarness(t)
	tok := h.seedSession(testAddr1)
	w := h.doAuthJSON("PATCH", "/launch/not-a-uuid", jsonBody(`{"chain_name":"x"}`), tok)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleLaunchPatch_Success(t *testing.T) {
	h := newHarness(t)
	l := testLaunch() // DRAFT, testAddr1 is committee lead
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	w := h.doAuthJSON("PATCH", "/launch/"+l.ID.String(), jsonBody(`{"chain_name":"Renamed"}`), tok)
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
}

// ---- POST /launch/{id}/committee --------------------------------------------

func TestHandleCommitteeCreate_NoAuth(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.doJSON("POST", "/launch/"+l.ID.String()+"/committee", validCommitteeBody())
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestHandleCommitteeCreate_BadLeadAddress(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	body := []byte(`{"members":[],"threshold_m":1,"total_n":1,
		"lead_address":"not-valid","creation_signature":"` + testSig + `"}`)
	w := h.doAuthJSON("POST", "/launch/"+l.ID.String()+"/committee", body, tok)
	assertStatusCode(t, w, http.StatusBadRequest)
	assertErrorCode(t, w, "invalid_field") // only lead_address is bad → the field-validation path fired
}

func TestHandleCommitteeCreate_Success(t *testing.T) {
	h := newHarness(t)
	l := testLaunch() // DRAFT, testAddr1 is lead
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	w := h.doAuthJSON("POST", "/launch/"+l.ID.String()+"/committee", validCommitteeBody(), tok)
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
}

// ---- GET /committee/{launch_id} ---------------------------------------------

func TestHandleCommitteeGet_BadUUID(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/committee/not-a-uuid", nil, nil)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleCommitteeGet_NotFound(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/committee/"+uuid.New().String(), nil, nil)
	assertStatusCode(t, w, http.StatusNotFound)
}

func TestHandleCommitteeGet_Success(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.getAsMember("/committee/" + l.ID.String())
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
}

// ---- POST /launch/{id}/genesis ----------------------------------------------

func TestHandleGenesisUpload_NoAuth(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.do("POST", "/launch/"+l.ID.String()+"/genesis",
		[]byte(`{"chain_id":"testchain-1"}`), nil)
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestHandleGenesisUpload_BadUUID(t *testing.T) {
	h := newHarness(t)
	tok := h.seedSession(testAddr1)
	w := h.do("POST", "/launch/not-a-uuid/genesis",
		[]byte(`{"chain_id":"testchain-1"}`),
		map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleGenesisUpload_EmptyBody(t *testing.T) {
	// Host mode ON so the request reaches the empty-body check. With host mode off, an empty
	// no-content-type body is rejected earlier as host_mode_disabled — this test would then pass
	// for the wrong reason, never exercising the empty-body guard it names.
	h := newHarnessHostMode(t, 32<<20)
	l := testLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	w := h.do("POST", "/launch/"+l.ID.String()+"/genesis",
		[]byte{},
		map[string]string{"Authorization": "Bearer " + tok, "Content-Type": "application/octet-stream"})
	assertStatusCode(t, w, http.StatusBadRequest)
	assertErrorCode(t, w, "empty_body")
}

func TestHandleGenesisUpload_InitialSuccess(t *testing.T) {
	h := newHarness(t)
	l := testLaunch() // DRAFT status
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	body := `{"url":"https://example.com/genesis.json","sha256":"a3f9b72c1d4e8f05a6b2c3d4e5f67890a1b2c3d4e5f6789012345678901234ab"}`
	w := h.do("POST", "/launch/"+l.ID.String()+"/genesis?type=initial",
		[]byte(body),
		map[string]string{
			"Authorization": "Bearer " + tok,
			"Content-Type":  "application/json",
		})
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
}

// ---- GET /launch/{id}/genesis -----------------------------------------------

func TestHandleGenesisGet_BadUUID(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/launch/not-a-uuid/genesis", nil, nil)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleGenesisGet_NoGenesisUploaded(t *testing.T) {
	h := newHarness(t)
	l := testLaunch() // InitialGenesisSHA256 == ""
	h.launches.data[l.ID] = l
	w := h.do("GET", "/launch/"+l.ID.String()+"/genesis", nil, nil)
	assertStatusCode(t, w, http.StatusNotFound)
}

func TestHandleGenesisGet_InitialGenesis(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	l.InitialGenesisSHA256 = "abc123"
	h.launches.data[l.ID] = l
	h.genesis.initial[l.ID.String()] = []byte(`{"chain_id":"testchain-1"}`)
	w := h.getAsMember("/launch/" + l.ID.String() + "/genesis")
	assertStatusCode(t, w, http.StatusOK)
}

// ---- GET /launch/{id}/genesis/hash ------------------------------------------

func TestHandleGenesisHashGet_BadUUID(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/launch/not-a-uuid/genesis/hash", nil, nil)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleGenesisHashGet_Success(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.getAsMember("/launch/" + l.ID.String() + "/genesis/hash")
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
}

// ---- POST /launch/{id}/allocations/{type} -----------------------------------

// validAllocSHA256 is a syntactically valid (64-char lowercase hex) SHA-256 digest.
const validAllocSHA256 = "a3f9b72c1d4e8e05a6b2c3d4e5f5a890a1b2c3d4e5f6789012345678901234ab"

func TestHandleAllocationUpload_NoAuth(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.do("POST", "/launch/"+l.ID.String()+"/allocations/claims",
		[]byte(`{"url":"https://example.com/claims.csv","sha256":"`+validAllocSHA256+`"}`),
		map[string]string{"Content-Type": "application/json"})
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestHandleAllocationUpload_NonCommittee_Forbidden(t *testing.T) {
	h := newHarness(t)
	l := soloCommitteeLaunch() // 1-of-1: only testAddr1
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr2) // not a committee member
	w := h.do("POST", "/launch/"+l.ID.String()+"/allocations/claims",
		[]byte(`{"url":"https://example.com/claims.csv","sha256":"`+validAllocSHA256+`"}`),
		map[string]string{"Authorization": "Bearer " + tok, "Content-Type": "application/json"})
	assertStatusCode(t, w, http.StatusForbidden)
}

func TestHandleAllocationUpload_AttestorSuccess(t *testing.T) {
	h := newHarness(t)
	l := testLaunch() // DRAFT; testAddr1 is a committee member
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	w := h.do("POST", "/launch/"+l.ID.String()+"/allocations/claims",
		[]byte(`{"url":"https://example.com/claims.csv","sha256":"`+validAllocSHA256+`"}`),
		map[string]string{"Authorization": "Bearer " + tok, "Content-Type": "application/json"})
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
}

func TestHandleAllocationUpload_UnknownType(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	w := h.do("POST", "/launch/"+l.ID.String()+"/allocations/bogus",
		[]byte(`{"url":"https://example.com/x.csv","sha256":"`+validAllocSHA256+`"}`),
		map[string]string{"Authorization": "Bearer " + tok, "Content-Type": "application/json"})
	assertStatusCode(t, w, http.StatusBadRequest)
	assertErrorCode(t, w, "bad_request") // an unknown type reaches the service → ErrUnknownAllocationType → 400
}

func TestHandleAllocationUpload_MissingURL(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	w := h.do("POST", "/launch/"+l.ID.String()+"/allocations/claims",
		[]byte(`{"sha256":"`+validAllocSHA256+`"}`),
		map[string]string{"Authorization": "Bearer " + tok, "Content-Type": "application/json"})
	assertStatusCode(t, w, http.StatusBadRequest)
	assertErrorCode(t, w, "missing_url")
}

func TestHandleAllocationUpload_HostModeDisabled(t *testing.T) {
	h := newHarness(t) // host mode off by default
	l := testLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	// Non-JSON content-type → host-mode (bytes) path, which is disabled.
	w := h.do("POST", "/launch/"+l.ID.String()+"/allocations/claims",
		[]byte("address,amount\ncosmos1abc,1000\n"),
		map[string]string{"Authorization": "Bearer " + tok, "Content-Type": "text/csv"})
	assertStatusCode(t, w, http.StatusBadRequest)
}

// TestHandleAllocationUpload_HostModeCSV_Success is the regression guard for the
// fix that allocation file content is opaque (CSV/TSV), not JSON: a raw CSV upload
// in host mode must succeed.
func TestHandleAllocationUpload_HostModeCSV_Success(t *testing.T) {
	h := newHarnessHostMode(t, 32<<20)
	l := testLaunch() // DRAFT; testAddr1 is a committee member
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	w := h.do("POST", "/launch/"+l.ID.String()+"/allocations/claims",
		[]byte("address,amount\ncosmos1abc,1000\ncosmos1def,2000\n"),
		map[string]string{"Authorization": "Bearer " + tok, "Content-Type": "text/csv"})
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
}

// ---- GET /launch/{id}/allocations -------------------------------------------

func TestHandleAllocationList_BadUUID(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/launch/not-a-uuid/allocations", nil, nil)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleAllocationList_Success(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	require.NoError(t, l.UploadAllocationFile(launch.AllocationClaims, "1111111111111111111111111111111111111111111111111111111111111111"))
	h.launches.data[l.ID] = l
	w := h.getAsMember("/launch/" + l.ID.String() + "/allocations")
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
	body := w.Body.String()
	assert.Contains(t, body, "claims", "listing should include the claims file")
	assert.Contains(t, body, "PENDING", "listing should include the PENDING status")
}

// ---- GET /launch/{id}/allocations/{type} ------------------------------------

func TestHandleAllocationGet_Attestor_Redirect(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	h.allocation.refs[l.ID.String()+":claims"] = &ports.StoredFileRef{
		ExternalURL: "https://example.com/claims.csv",
		SHA256:      validAllocSHA256,
	}
	w := h.getAsMember("/launch/" + l.ID.String() + "/allocations/claims")
	assertStatusCode(t, w, http.StatusFound)
	assert.Equal(t, "https://example.com/claims.csv", w.Header().Get("Location"))
}

func TestHandleAllocationGet_HostMode_Stream(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	csv := "address,amount\ncosmos1abc,1000\n"
	h.allocation.bytes[l.ID.String()+":claims"] = []byte(csv)
	w := h.getAsMember("/launch/" + l.ID.String() + "/allocations/claims")
	assertStatusCode(t, w, http.StatusOK)
	assert.Equal(t, "application/octet-stream", w.Header().Get("Content-Type"))
	assert.Equal(t, csv, w.Body.String())
}

func TestHandleAllocationGet_NotFound(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l // launch exists, but no allocation file stored
	w := h.do("GET", "/launch/"+l.ID.String()+"/allocations/claims", nil, nil)
	assertStatusCode(t, w, http.StatusNotFound)
}

// ---- POST /launch/{id}/join -------------------------------------------------

func TestHandleJoinSubmit_NoAuth(t *testing.T) {
	h := newHarness(t)
	l := windowOpenLaunch()
	h.launches.data[l.ID] = l
	body := []byte(`{"operator_address":"` + testAddr2 + `"}`)
	w := h.doJSON("POST", "/launch/"+l.ID.String()+"/join", body)
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestHandleJoinSubmit_BadUUID(t *testing.T) {
	h := newHarness(t)
	tok := h.seedSession(testAddr2)
	w := h.doAuthJSON("POST", "/launch/not-a-uuid/join", jsonBody(`{}`), tok)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleJoinSubmit_BadJSON(t *testing.T) {
	h := newHarness(t)
	l := windowOpenLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr2)
	w := h.doAuthJSON("POST", "/launch/"+l.ID.String()+"/join", jsonBody(`not json`), tok)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleJoinSubmit_Success(t *testing.T) {
	h := newHarness(t)
	l := windowOpenLaunch() // WINDOW_OPEN
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr2)
	body := []byte(`{
		"chain_id":"testchain-1",
		"operator_address":"` + testAddr2 + `",
		"gentx":{"body":{"messages":[{"@type":"/cosmos.staking.v1beta1.MsgCreateValidator","description":{"moniker":"test-validator"},"pubkey":{"@type":"/cosmos.crypto.ed25519.PubKey","key":"f5DzEhtQbnmXE/WZQsX+I8RljPdEU0u0ncVGtniFyEM="},"value":{"denom":"utest","amount":"2000000"}}]},"auth_info":{},"signatures":[]},
		"peer_address":"` + testPeerAddress + `",
		"rpc_endpoint":"` + testRPCURL + `",
		"memo":"",
		"nonce":"nonce-js1",
		"timestamp":"` + nowTS() + `",
		"signature":"` + testSig + `"
	}`)
	w := h.doAuthJSON("POST", "/launch/"+l.ID.String()+"/join", body, tok)
	assertStatusCode(t, w, http.StatusCreated)
	assertContentTypeJSON(t, w)
}

// ---- GET /launch/{id}/join --------------------------------------------------

func TestHandleJoinList_NoAuth(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.do("GET", "/launch/"+l.ID.String()+"/join", nil, nil)
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestHandleJoinList_BadUUID(t *testing.T) {
	h := newHarness(t)
	tok := h.seedSession(testAddr1)
	w := h.do("GET", "/launch/not-a-uuid/join", nil,
		map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleJoinList_NotCommitteeMember(t *testing.T) {
	h := newHarness(t)
	// Solo committee: only testAddr1. testAddr2 is not a committee member.
	l := soloCommitteeLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr2)
	w := h.do("GET", "/launch/"+l.ID.String()+"/join", nil,
		map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusForbidden)
}

func TestHandleJoinList_Success(t *testing.T) {
	h := newHarness(t)
	l := testLaunch() // testAddr1 is a committee member
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	w := h.do("GET", "/launch/"+l.ID.String()+"/join", nil,
		map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
}

// ---- GET /launch/{id}/join/{req_id} -----------------------------------------

func TestHandleJoinGet_NoAuth(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.do("GET", "/launch/"+l.ID.String()+"/join/"+uuid.New().String(), nil, nil)
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestHandleJoinGet_BadLaunchUUID(t *testing.T) {
	h := newHarness(t)
	tok := h.seedSession(testAddr1)
	w := h.do("GET", "/launch/not-a-uuid/join/"+uuid.New().String(), nil,
		map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleJoinGet_BadReqUUID(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	w := h.do("GET", "/launch/"+l.ID.String()+"/join/not-a-uuid", nil,
		map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleJoinGet_NotFound(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	w := h.do("GET", "/launch/"+l.ID.String()+"/join/"+uuid.New().String(), nil,
		map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusNotFound)
}

func TestHandleJoinGet_Success(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	jr := testJoinRequest(l.ID, testAddr2)
	h.joinReqs.data[jr.ID] = jr
	tok := h.seedSession(testAddr1) // committee member sees any join request
	w := h.do("GET", "/launch/"+l.ID.String()+"/join/"+jr.ID.String(), nil,
		map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
}

// ---- POST /launch/{id}/proposal ---------------------------------------------

func TestHandleProposalRaise_NoAuth(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.doJSON("POST", "/launch/"+l.ID.String()+"/proposal", jsonBody(`{}`))
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestHandleProposalRaise_BadUUID(t *testing.T) {
	h := newHarness(t)
	tok := h.seedSession(testAddr1)
	w := h.doAuthJSON("POST", "/launch/not-a-uuid/proposal", jsonBody(`{}`), tok)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleProposalRaise_BadJSON(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	w := h.doAuthJSON("POST", "/launch/"+l.ID.String()+"/proposal", jsonBody(`not json`), tok)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleProposalRaise_AddrMismatch(t *testing.T) {
	// Handler checks member_address matches the authenticated session before
	// calling the service — a mismatch returns 403 immediately.
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	body := []byte(`{
		"action_type":"CLOSE_APPLICATION_WINDOW",
		"payload":{},
		"member_address":"` + testAddr2 + `",
		"nonce":"nonce-pr1","timestamp":"` + nowTS() + `","signature":"` + testSig + `"
	}`)
	w := h.doAuthJSON("POST", "/launch/"+l.ID.String()+"/proposal", body, tok)
	assertStatusCode(t, w, http.StatusForbidden)
}

func TestHandleProposalRaise_Success(t *testing.T) {
	h := newHarness(t)
	l := testLaunch() // 2-of-3 committee; testAddr1 is a member
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	body := []byte(`{
		"action_type":"CLOSE_APPLICATION_WINDOW",
		"payload":{},
		"member_address":"` + testAddr1 + `",
		"nonce":"nonce-pr2","timestamp":"` + nowTS() + `","signature":"` + testSig + `","pubkey_b64":"` + testSig + `"
	}`)
	w := h.doAuthJSON("POST", "/launch/"+l.ID.String()+"/proposal", body, tok)
	assertStatusCode(t, w, http.StatusCreated)
	assertContentTypeJSON(t, w)
}

// ---- GET /launch/{id}/proposals ---------------------------------------------

func TestHandleProposalList_NoAuth(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.do("GET", "/launch/"+l.ID.String()+"/proposals", nil, nil)
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestHandleProposalList_BadUUID(t *testing.T) {
	h := newHarness(t)
	tok := h.seedSession(testAddr1)
	w := h.do("GET", "/launch/not-a-uuid/proposals", nil,
		map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleProposalList_Success(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	w := h.do("GET", "/launch/"+l.ID.String()+"/proposals", nil,
		map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
}

// ---- GET /launch/{id}/proposal/{prop_id} ------------------------------------

func TestHandleProposalGet_NoAuth(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.do("GET", "/launch/"+l.ID.String()+"/proposal/"+uuid.New().String(), nil, nil)
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestHandleProposalGet_BadLaunchUUID(t *testing.T) {
	h := newHarness(t)
	tok := h.seedSession(testAddr1)
	w := h.do("GET", "/launch/not-a-uuid/proposal/"+uuid.New().String(), nil,
		map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleProposalGet_BadPropUUID(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	w := h.do("GET", "/launch/"+l.ID.String()+"/proposal/not-a-uuid", nil,
		map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleProposalGet_NotFound(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	w := h.do("GET", "/launch/"+l.ID.String()+"/proposal/"+uuid.New().String(), nil,
		map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusNotFound)
}

func TestHandleProposalGet_Success(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	p := testProposalObj(l.ID)
	h.proposals.data[p.ID] = p
	tok := h.seedSession(testAddr1)
	w := h.do("GET", "/launch/"+l.ID.String()+"/proposal/"+p.ID.String(), nil,
		map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
}

// ---- POST /launch/{id}/proposal/{prop_id}/sign ------------------------------

func TestHandleProposalSign_NoAuth(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	p := testProposalObj(l.ID)
	h.proposals.data[p.ID] = p
	body := jsonBody(`{"member_address":"` + testAddr1 + `","decision":"SIGN"}`)
	w := h.doJSON("POST", "/launch/"+l.ID.String()+"/proposal/"+p.ID.String()+"/sign", body)
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestHandleProposalSign_AddrMismatch(t *testing.T) {
	// member_address in body must match the authenticated session address.
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	p := testProposalObj(l.ID)
	h.proposals.data[p.ID] = p
	tok := h.seedSession(testAddr1)
	body := []byte(`{
		"member_address":"` + testAddr2 + `",
		"decision":"SIGN",
		"nonce":"nonce-ps0","timestamp":"` + nowTS() + `","signature":"` + testSig + `"
	}`)
	w := h.doAuthJSON("POST", "/launch/"+l.ID.String()+"/proposal/"+p.ID.String()+"/sign", body, tok)
	assertStatusCode(t, w, http.StatusForbidden)
}

func TestHandleProposalSign_Success(t *testing.T) {
	h := newHarness(t)
	l := testLaunch() // 2-of-3; testAddr1 is a committee member
	h.launches.data[l.ID] = l
	p := testProposalObj(l.ID) // 0 signatures, PENDING
	h.proposals.data[p.ID] = p
	tok := h.seedSession(testAddr1)
	body := []byte(`{
		"member_address":"` + testAddr1 + `",
		"decision":"SIGN",
		"nonce":"nonce-ps1","timestamp":"` + nowTS() + `","signature":"` + testSig + `","pubkey_b64":"` + testSig + `"
	}`)
	w := h.doAuthJSON("POST", "/launch/"+l.ID.String()+"/proposal/"+p.ID.String()+"/sign", body, tok)
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
}

// ---- POST /launch/{id}/ready ------------------------------------------------

func TestHandleReadinessConfirm_NoAuth(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.doJSON("POST", "/launch/"+l.ID.String()+"/ready", jsonBody(`{}`))
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestHandleReadinessConfirm_BadUUID(t *testing.T) {
	h := newHarness(t)
	tok := h.seedSession(testAddr2)
	w := h.doAuthJSON("POST", "/launch/not-a-uuid/ready", jsonBody(`{}`), tok)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleReadinessConfirm_BadJSON(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr2)
	w := h.doAuthJSON("POST", "/launch/"+l.ID.String()+"/ready", jsonBody(`not json`), tok)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleReadinessConfirm_Success(t *testing.T) {
	h := newHarness(t)
	// GENESIS_READY launch; hashes set.
	l := genesisReadyLaunch() // FinalGenesisSHA256="final-genesis-hash", BinarySHA256=valid 64-hex
	h.launches.data[l.ID] = l
	// Approved join request for testAddr2.
	jr := testApprovedJoinRequest(l.ID, testAddr2)
	h.joinReqs.data[jr.ID] = jr
	tok := h.seedSession(testAddr2)
	body := []byte(`{
		"operator_address":"` + testAddr2 + `",
		"genesis_hash_confirmed":"final-genesis-hash",
		"binary_hash_confirmed":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"nonce":"nonce-rc1","timestamp":"` + nowTS() + `","signature":"` + testSig + `"
	}`)
	w := h.doAuthJSON("POST", "/launch/"+l.ID.String()+"/ready", body, tok)
	assertStatusCode(t, w, http.StatusCreated)
	assertContentTypeJSON(t, w)
}

// ---- GET /launch/{id}/dashboard ---------------------------------------------

func TestHandleDashboard_BadUUID(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/launch/not-a-uuid/dashboard", nil, nil)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleDashboard_NotFound(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/launch/"+uuid.New().String()+"/dashboard", nil, nil)
	assertStatusCode(t, w, http.StatusNotFound)
}

func TestHandleDashboard_Success(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.getAsMember("/launch/" + l.ID.String() + "/dashboard")
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
}

// ---- GET /launch/{id}/peers -------------------------------------------------

func TestHandlePeers_BadUUID(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/launch/not-a-uuid/peers", nil, nil)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandlePeers_NotFound(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/launch/"+uuid.New().String()+"/peers", nil, nil)
	assertStatusCode(t, w, http.StatusNotFound)
}

func TestHandlePeers_Success(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.getAsMember("/launch/" + l.ID.String() + "/peers")
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
}

// ---- GET /launch/{id}/audit -------------------------------------------------

func TestHandleAudit_BadUUID(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/launch/not-a-uuid/audit", nil, nil)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleAudit_NotFound(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/launch/"+uuid.New().String()+"/audit", nil, nil)
	assertStatusCode(t, w, http.StatusNotFound)
}

func TestHandleAudit_Success(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.getAsMember("/launch/" + l.ID.String() + "/audit")
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
}

// ---- GET /launch/{id}/events ------------------------------------------------

func TestHandleEvents_BadUUID(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/launch/not-a-uuid/events", nil, nil)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleEvents_NotFound(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/launch/"+uuid.New().String()+"/events", nil, nil)
	assertStatusCode(t, w, http.StatusNotFound)
}

func TestHandleEvents_Success(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	// thinSSEBroker returns an immediately-closed channel, so the handler exits
	// after writing the SSE headers and flushing once.
	w := h.getAsMember("/launch/" + l.ID.String() + "/events")
	assertStatusCode(t, w, http.StatusOK)
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("want Content-Type text/event-stream, got %q", ct)
	}
}

// ---- POST /launch/{id}/cancel -----------------------------------------------

func TestHandleLaunchCancel_NoAuth(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.do("POST", "/launch/"+l.ID.String()+"/cancel", nil, nil)
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestHandleLaunchCancel_BadUUID(t *testing.T) {
	h := newHarness(t)
	tok := h.seedSession(testAddr1)
	w := h.doAuthJSON("POST", "/launch/not-a-uuid/cancel", nil, tok)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleLaunchCancel_NotFound(t *testing.T) {
	h := newHarness(t)
	tok := h.seedSession(testAddr1)
	w := h.doAuthJSON("POST", "/launch/"+uuid.New().String()+"/cancel", nil, tok)
	assertStatusCode(t, w, http.StatusNotFound)
}

func TestHandleLaunchCancel_NonLead_Forbidden(t *testing.T) {
	h := newHarness(t)
	l := testLaunch() // lead = testAddr1
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr2) // not the lead
	w := h.doAuthJSON("POST", "/launch/"+l.ID.String()+"/cancel", nil, tok)
	assertStatusCode(t, w, http.StatusForbidden)
}

func TestHandleLaunchCancel_AlreadyTerminal_Conflict(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	l.Status = launch.StatusCancelled
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1) // lead
	w := h.doAuthJSON("POST", "/launch/"+l.ID.String()+"/cancel", nil, tok)
	assertStatusCode(t, w, http.StatusConflict)
}

func TestHandleLaunchCancel_Success(t *testing.T) {
	h := newHarness(t)
	l := testLaunch() // DRAFT, lead = testAddr1
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	w := h.doAuthJSON("POST", "/launch/"+l.ID.String()+"/cancel", nil, tok)
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)

	var body map[string]json.RawMessage
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var status string
	if err := json.Unmarshal(body["status"], &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status != "CANCELED" {
		t.Errorf("want status CANCELED, got %q", status)
	}
}

// ---- Genesis upload (POST /launch/{id}/genesis) --------------------------------

func TestGenesisUpload_AttestorMode_Success(t *testing.T) {
	h := newHarness(t)
	l := testLaunch() // DRAFT
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)

	body := `{"url":"https://example.com/genesis.json","sha256":"a3f9b72c1d4e8f05a6b2c3d4e5f67890a1b2c3d4e5f6789012345678901234ab"}`
	w := h.do("POST", "/launch/"+l.ID.String()+"/genesis",
		[]byte(body),
		map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusOK)

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["sha256"] != "a3f9b72c1d4e8f05a6b2c3d4e5f67890a1b2c3d4e5f6789012345678901234ab" {
		t.Errorf("sha256 in response: got %q", resp["sha256"])
	}
}

func TestGenesisUpload_AttestorMode_Unauthenticated(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l

	body := `{"url":"https://example.com/genesis.json","sha256":"a3f9b72c1d4e8f05a6b2c3d4e5f67890a1b2c3d4e5f6789012345678901234ab"}`
	w := h.do("POST", "/launch/"+l.ID.String()+"/genesis",
		[]byte(body),
		map[string]string{"Content-Type": "application/json"})
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestGenesisUpload_AttestorMode_MissingURL(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)

	body := `{"sha256":"a3f9b72c1d4e8f05a6b2c3d4e5f67890a1b2c3d4e5f6789012345678901234ab"}`
	w := h.do("POST", "/launch/"+l.ID.String()+"/genesis",
		[]byte(body),
		map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestGenesisUpload_HostModeDisabled_ReturnsError(t *testing.T) {
	// Default harness has host mode OFF.
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)

	// Send raw bytes (no Content-Type or octet-stream) — should be rejected.
	w := h.do("POST", "/launch/"+l.ID.String()+"/genesis",
		[]byte(`{"chain_id":"testchain-1"}`),
		map[string]string{"Content-Type": "application/octet-stream", "Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusBadRequest)

	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	errObj, _ := resp["error"].(map[string]any)
	if code, _ := errObj["code"].(string); code != "host_mode_disabled" {
		t.Errorf("want error code host_mode_disabled, got %q", code)
	}
}

func TestGenesisUpload_HostMode_OversizedFile(t *testing.T) {
	// Enable host mode with a tiny cap (10 bytes).
	h := newHarnessHostMode(t, 10)
	l := testLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)

	// Send more than 10 bytes.
	big := strings.Repeat("x", 20)
	w := h.do("POST", "/launch/"+l.ID.String()+"/genesis",
		[]byte(big),
		map[string]string{"Content-Type": "application/octet-stream", "Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusRequestEntityTooLarge)
}

// ---- Genesis GET (GET /launch/{id}/genesis) ------------------------------------

func TestGenesisGet_AttestorMode_Redirects(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	l.InitialGenesisSHA256 = "a3f9b72c1d4e8f05a6b2c3d4e5f67890a1b2c3d4e5f6789012345678901234ab"
	h.launches.data[l.ID] = l
	// Inject an Option A ref.
	h.genesis.initialRef[l.ID.String()] = &ports.StoredFileRef{
		ExternalURL: "https://example.com/genesis.json",
		SHA256:      l.InitialGenesisSHA256,
	}

	w := h.getAsMember("/launch/" + l.ID.String() + "/genesis")
	assertStatusCode(t, w, http.StatusFound)
	if loc := w.Header().Get("Location"); loc != "https://example.com/genesis.json" {
		t.Errorf("Location header: got %q", loc)
	}
}

func TestGenesisGet_HostMode_StreamsFile(t *testing.T) {
	h := newHarnessHostMode(t, 32<<20)
	l := testLaunch()
	l.InitialGenesisSHA256 = "somehash"
	h.launches.data[l.ID] = l
	// Store raw bytes (Option C path in thin fake).
	h.genesis.initial[l.ID.String()] = []byte(`{"chain_id":"testchain-1"}`)

	w := h.getAsMember("/launch/" + l.ID.String() + "/genesis")
	assertStatusCode(t, w, http.StatusOK)
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: got %q", ct)
	}
	if body := w.Body.String(); body != `{"chain_id":"testchain-1"}` {
		t.Errorf("body: got %q", body)
	}
}

func TestGenesisGet_NoGenesis_Returns404(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	// InitialGenesisSHA256 is empty — no genesis uploaded.
	h.launches.data[l.ID] = l

	w := h.do("GET", "/launch/"+l.ID.String()+"/genesis", nil, nil)
	assertStatusCode(t, w, http.StatusNotFound)
}
