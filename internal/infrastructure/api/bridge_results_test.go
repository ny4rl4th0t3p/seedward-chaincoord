package api_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-libs/canonicaljson"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/services"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

func resultsPath(launchID string) string {
	return "/bridge/launches/" + launchID + "/rehearsal-results"
}

func opsHeader() map[string]string {
	return map[string]string{"Authorization": "Bearer " + testOpsToken}
}

func opsJSONHeader() map[string]string {
	return map[string]string{"Authorization": "Bearer " + testOpsToken, "Content-Type": "application/json"}
}

// seedRehearsalLaunch stores a launch that trusts a fresh service key and returns the key.
func seedRehearsalLaunch(t *testing.T, h *harness) (*launch.Launch, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	l := testLaunch()
	l.RehearsalServicePubKey = base64.StdEncoding.EncodeToString(pub)
	h.launches.data[l.ID] = l
	return l, priv
}

// fetchAttempt runs GET rehearsal-input (which mints the attempt) and returns (attempt_id, hash).
func fetchAttempt(t *testing.T, h *harness, launchID string) (attemptID, hash string) {
	t.Helper()
	w := h.do("GET", "/bridge/launches/"+launchID+"/rehearsal-input", nil, opsHeader())
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var in struct {
		AttemptID    string `json:"attempt_id"`
		InputSetHash string `json:"input_set_hash"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &in))
	return in.AttemptID, in.InputSetHash
}

func signAPIResultFact(t *testing.T, launchID, hash, attemptID string, priv ed25519.PrivateKey) []byte {
	t.Helper()
	att := attemptID
	fact := services.RehearsalResultFact{
		SchemaVersion: 1,
		LaunchID:      launchID,
		InputSetHash:  hash,
		AttemptID:     &att,
		Outcome:       "PASS",
		Summary:       "ok",
		Steps:         []services.RehearsalResultFactStep{{Name: "boot", Status: "PASS"}},
		Rehearsal:     services.RehearsalResultFactMeta{EngineVersion: "eng1", BinaryName: "gaiad", Validators: 2},
		StartedAt:     "2026-01-01T00:00:00Z",
		FinishedAt:    "2026-01-01T00:00:05Z",
	}
	fact.ServicePubkey = base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))
	msg, err := canonicaljson.MarshalForSigning(&fact)
	require.NoError(t, err)
	fact.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, msg))
	body, err := json.Marshal(fact)
	require.NoError(t, err)
	return body
}

func TestHandleRehearsalResults_Success(t *testing.T) {
	h := newHarness(t)
	l, priv := seedRehearsalLaunch(t, h)
	attemptID, hash := fetchAttempt(t, h, l.ID.String())

	body := signAPIResultFact(t, l.ID.String(), hash, attemptID, priv)
	w := h.do("POST", resultsPath(l.ID.String()), body, opsJSONHeader())
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var ack struct {
		AttemptID string `json:"attempt_id"`
		Outcome   string `json:"outcome"`
		Stale     bool   `json:"stale"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ack))
	assert.Equal(t, attemptID, ack.AttemptID)
	assert.Equal(t, "PASS", ack.Outcome)
	assert.False(t, ack.Stale)
}

func TestHandleRehearsalResults_NoOpsToken(t *testing.T) {
	h := newHarness(t)
	l, priv := seedRehearsalLaunch(t, h)
	attemptID, hash := fetchAttempt(t, h, l.ID.String())
	body := signAPIResultFact(t, l.ID.String(), hash, attemptID, priv)

	w := h.do("POST", resultsPath(l.ID.String()), body, nil)
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestHandleRehearsalResults_FabricatedAttempt(t *testing.T) {
	h := newHarness(t)
	l, priv := seedRehearsalLaunch(t, h)
	_, hash := fetchAttempt(t, h, l.ID.String())

	body := signAPIResultFact(t, l.ID.String(), hash, uuid.New().String(), priv)
	w := h.do("POST", resultsPath(l.ID.String()), body, opsJSONHeader())
	assertStatusCode(t, w, http.StatusBadRequest)
}

// resultFactGolden is the canonical result-fact wire payload (bridge-contract.md §4) — the coordd
// (consumer) side of the drift guard. seedward-rehearsal has a mirror producer test that MARSHALS
// to this shape. Keep both copies + §4 in sync.
const resultFactGolden = `{
  "schema_version": 1,
  "launch_id": "11111111-1111-1111-1111-111111111111",
  "input_set_hash": "deadbeefdeadbeef",
  "attempt_id": "33333333-3333-3333-3333-333333333333",
  "outcome": "PASS",
  "failed_step": null,
  "summary": "all good",
  "steps": [
    {"name": "build", "status": "PASS", "detail": ""},
    {"name": "assert:supply", "status": "PASS", "detail": "7000000"}
  ],
  "rehearsal": {
    "engine_version": "eng1",
    "binary_name": "gaiad",
    "binary_version": "v27.2.0",
    "binary_sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
    "validators": 2,
    "blocks_advanced": 3
  },
  "started_at": "2026-01-01T00:00:00Z",
  "finished_at": "2026-01-01T00:00:05Z",
  "service_pubkey": "cHVia2V5",
  "signature": "c2ln"
}`

func TestResultFact_DecodesWireGolden(t *testing.T) {
	var fact services.RehearsalResultFact
	require.NoError(t, json.Unmarshal([]byte(resultFactGolden), &fact))

	assert.Equal(t, 1, fact.SchemaVersion)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", fact.LaunchID)
	assert.Equal(t, "deadbeefdeadbeef", fact.InputSetHash)
	require.NotNil(t, fact.AttemptID)
	assert.Equal(t, "33333333-3333-3333-3333-333333333333", *fact.AttemptID)
	assert.Equal(t, "PASS", fact.Outcome)
	assert.Nil(t, fact.FailedStep)
	require.Len(t, fact.Steps, 2)
	assert.Equal(t, "build", fact.Steps[0].Name)
	assert.Equal(t, "gaiad", fact.Rehearsal.BinaryName)
	assert.Equal(t, 3, fact.Rehearsal.BlocksAdvanced)
	assert.Equal(t, "c2ln", fact.Signature)
}
