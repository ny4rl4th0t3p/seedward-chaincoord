package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---- GET /audit/pubkey (handleAuditPubKey) ----------------------------------

func TestHandleAuditPubKey_NoKeyConfigured(t *testing.T) {
	// The harness wires a nil audit public key, so the endpoint must report 503.
	h := newHarness(t)
	w := h.do("GET", "/audit/pubkey", nil, nil)
	assertStatusCode(t, w, http.StatusServiceUnavailable)
	assertContentTypeJSON(t, w)
}

// ---- GET /auth/session (handleAuthSessionInfo) ------------------------------

func TestHandleAuthSessionInfo_Success(t *testing.T) {
	h := newHarness(t)
	tok := h.seedSession(testAddr1)

	w := h.do("GET", "/auth/session", nil, map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
	assert.Contains(t, w.Body.String(), testAddr1, "session info must echo the operator address")
}

func TestHandleAuthSessionInfo_MissingToken(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/auth/session", nil, nil)
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestHandleAuthSessionInfo_InvalidToken(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/auth/session", nil, map[string]string{"Authorization": "Bearer bogus-token"})
	assertStatusCode(t, w, http.StatusUnauthorized)
}

// ---- DELETE /auth/sessions/all (handleAuthRevokeAll) ------------------------

func TestHandleAuthRevokeAll_Success(t *testing.T) {
	h := newHarness(t)
	tok := h.seedSession(testAddr1)

	w := h.do("DELETE", "/auth/sessions/all", nil, map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusNoContent)

	// Every session for the operator must be gone.
	_, ok := h.sessions.data[tok]
	assert.False(t, ok, "all sessions for the operator should be revoked")
}

func TestHandleAuthRevokeAll_Unauthenticated(t *testing.T) {
	h := newHarness(t)
	w := h.do("DELETE", "/auth/sessions/all", nil, nil)
	assertStatusCode(t, w, http.StatusUnauthorized)
}

// ---- GET /launch/{id}/gentxs (handleGentxsGet) ------------------------------

func TestHandleGentxsGet_Success(t *testing.T) {
	h := newHarness(t)
	l := testLaunch() // committee includes testAddr1
	h.launches.data[l.ID] = l
	jr := testApprovedJoinRequest(l.ID, testAddr2)
	h.joinReqs.data[jr.ID] = jr
	tok := h.seedSession(testAddr1)

	w := h.doAuthJSON("GET", "/launch/"+l.ID.String()+"/gentxs", nil, tok)
	assertStatusCode(t, w, http.StatusOK)
	assertContentTypeJSON(t, w)
	assert.Contains(t, w.Body.String(), testAddr2, "approved validator's gentx must be listed")
}

func TestHandleGentxsGet_Forbidden(t *testing.T) {
	h := newHarness(t)
	l := soloCommitteeLaunch() // committee is testAddr1 only
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr2) // not a committee member

	w := h.doAuthJSON("GET", "/launch/"+l.ID.String()+"/gentxs", nil, tok)
	assertStatusCode(t, w, http.StatusForbidden)
}

func TestHandleGentxsGet_InvalidID(t *testing.T) {
	h := newHarness(t)
	tok := h.seedSession(testAddr1)

	w := h.doAuthJSON("GET", "/launch/not-a-uuid/gentxs", nil, tok)
	assertStatusCode(t, w, http.StatusBadRequest)
}
