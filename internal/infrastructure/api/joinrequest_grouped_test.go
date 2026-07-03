package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/joinrequest"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// jrFromSubmitter builds a PENDING join request with an explicit submitter (hot actor).
func jrFromSubmitter(launchID uuid.UUID, operatorAddr, submitterAddr string) *joinrequest.JoinRequest {
	jr := testJoinRequest(launchID, operatorAddr)
	jr.SubmitterAddress = mustAddr(submitterAddr)
	return jr
}

// groupJSON decodes a /join/grouped element.
type groupJSON struct {
	SubmitterAddress    string `json:"submitter_address"`
	Label               string `json:"label"`
	RequestCount        int    `json:"request_count"`
	TotalSelfDelegation string `json:"total_self_delegation"`
	Requests            []struct {
		SubmitterAddress string `json:"submitter_address"`
		OperatorAddress  string `json:"operator_address"`
	} `json:"requests"`
}

func TestHandleJoinGrouped_CommitteeSuccess(t *testing.T) {
	h := newHarness(t)
	l := soloCommitteeLaunch() // committee = testAddr1
	l.Allowlist = launch.NewAllowlistFromMembers([]launch.Member{{Address: mustAddr(testAddr2), Label: "acme"}})
	h.launches.data[l.ID] = l
	for _, jr := range []*joinrequest.JoinRequest{
		jrFromSubmitter(l.ID, testAddr1, testAddr2),
		jrFromSubmitter(l.ID, testAddr3, testAddr2),
		jrFromSubmitter(l.ID, testAddr2, testAddr3),
	} {
		h.joinReqs.data[jr.ID] = jr
	}
	tok := h.seedSession(testAddr1)

	w := h.do("GET", "/launch/"+l.ID.String()+"/join/grouped", nil,
		map[string]string{"Authorization": "Bearer " + tok})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var groups []groupJSON
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &groups))
	require.Len(t, groups, 2)

	byAddr := make(map[string]groupJSON, len(groups))
	for _, g := range groups {
		byAddr[g.SubmitterAddress] = g
	}
	acme, ok := byAddr[testAddr2]
	require.True(t, ok)
	assert.Equal(t, "acme", acme.Label)
	assert.Equal(t, 2, acme.RequestCount)
	require.Len(t, acme.Requests, 2)
	assert.Equal(t, testAddr2, acme.Requests[0].SubmitterAddress, "flat DTO exposes submitter_address (FU-2)")

	beta, ok := byAddr[testAddr3]
	require.True(t, ok)
	assert.Empty(t, beta.Label, "a submitter with no members-list entry has an empty label")
	assert.Equal(t, 1, beta.RequestCount)
}

func TestHandleJoinGrouped_NonCommittee(t *testing.T) {
	h := newHarness(t)
	l := soloCommitteeLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr2) // not on the committee
	w := h.do("GET", "/launch/"+l.ID.String()+"/join/grouped", nil,
		map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusForbidden)
}

func TestHandleJoinGrouped_Unauthenticated(t *testing.T) {
	h := newHarness(t)
	l := soloCommitteeLaunch()
	h.launches.data[l.ID] = l
	w := h.do("GET", "/launch/"+l.ID.String()+"/join/grouped", nil, nil)
	assertStatusCode(t, w, http.StatusUnauthorized)
}

// TestHandleJoinGrouped_StaticSegmentWins guards the route ordering: "grouped" must hit the
// grouped handler, not be captured as a {req_id} by GET /launch/{id}/join/{req_id}.
func TestHandleJoinGrouped_StaticSegmentWins(t *testing.T) {
	h := newHarness(t)
	l := soloCommitteeLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	w := h.do("GET", "/launch/"+l.ID.String()+"/join/grouped", nil,
		map[string]string{"Authorization": "Bearer " + tok})
	// A committee member with no requests gets an empty array (200), not a 400/404 from the
	// {req_id} route trying to parse "grouped" as a UUID.
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "[]", strings.TrimSpace(w.Body.String()))
}
