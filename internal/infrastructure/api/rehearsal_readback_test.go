package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleRehearsalResultsList_CommitteeSuccess(t *testing.T) {
	h := newHarness(t)
	l, priv := seedRehearsalLaunch(t, h)

	// Record a result through the bridge (ops plane): claim → sign → post.
	attemptID, hash := fetchAttempt(t, h, l.ID.String())
	body := signAPIResultFact(t, l.ID.String(), hash, attemptID, priv)
	require.Equal(t, http.StatusOK, h.do("POST", resultsPath(l.ID.String()), body, opsJSONHeader()).Code)

	// Committee member reads it back on the governance plane.
	tok := h.seedSession(testAddr1)
	w := h.do("GET", "/launch/"+l.ID.String()+"/rehearsal", nil,
		map[string]string{"Authorization": "Bearer " + tok})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got []struct {
		AttemptID string `json:"attempt_id"`
		Outcome   string `json:"outcome"`
		Stale     bool   `json:"stale"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got, 1)
	assert.Equal(t, attemptID, got[0].AttemptID)
	assert.Equal(t, "PASS", got[0].Outcome)
	assert.False(t, got[0].Stale)
}

func TestHandleRehearsalResultsList_NonCommitteeForbidden(t *testing.T) {
	h := newHarness(t)
	l, _ := seedRehearsalLaunch(t, h)
	l.Committee.Members = l.Committee.Members[:1] // sole member: testAddr1 (lead)
	tok := h.seedSession(testAddr2)               // authenticated, but not on the committee
	w := h.do("GET", "/launch/"+l.ID.String()+"/rehearsal", nil,
		map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusForbidden)
}
