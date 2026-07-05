package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func claimPath(launchID string) string { return "/bridge/launches/" + launchID + "/rehearsal-claim" }

func TestHandleRehearsalClaim_Success(t *testing.T) {
	h := newHarness(t)
	l, _ := seedRehearsalLaunch(t, h)

	w := h.do("POST", claimPath(l.ID.String()), []byte(`{"runner_id":"runner-1"}`), opsJSONHeader())
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var in struct {
		AttemptID    string `json:"attempt_id"`
		InputSetHash string `json:"input_set_hash"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &in))
	assert.NotEmpty(t, in.AttemptID)
	assert.NotEmpty(t, in.InputSetHash)
}

func TestHandleRehearsalClaim_Busy409(t *testing.T) {
	h := newHarness(t)
	l, _ := seedRehearsalLaunch(t, h)

	first := h.do("POST", claimPath(l.ID.String()), []byte(`{"runner_id":"runner-1"}`), opsJSONHeader())
	require.Equal(t, http.StatusOK, first.Code)

	w := h.do("POST", claimPath(l.ID.String()), []byte(`{"runner_id":"runner-2"}`), opsJSONHeader())
	require.Equal(t, http.StatusConflict, w.Code)

	var conflict struct {
		ClaimedBy      string `json:"claimed_by"`
		LeaseExpiresAt string `json:"lease_expires_at"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &conflict))
	assert.Equal(t, "runner-1", conflict.ClaimedBy)
	assert.NotEmpty(t, conflict.LeaseExpiresAt)
}

func TestHandleRehearsalClaim_NoOpsToken(t *testing.T) {
	h := newHarness(t)
	l, _ := seedRehearsalLaunch(t, h)
	w := h.do("POST", claimPath(l.ID.String()), []byte(`{"runner_id":"r"}`),
		map[string]string{"Content-Type": "application/json"})
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestHandleRehearsalClaim_MissingRunnerID(t *testing.T) {
	h := newHarness(t)
	l, _ := seedRehearsalLaunch(t, h)
	w := h.do("POST", claimPath(l.ID.String()), []byte(`{}`), opsJSONHeader())
	assertStatusCode(t, w, http.StatusBadRequest)
}
