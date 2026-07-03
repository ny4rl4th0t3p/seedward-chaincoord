package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// launchWithMember returns a DRAFT solo-committee launch (testAddr1) with testAddr2
// already on the members list, for remove/list tests.
func launchWithMember(t *testing.T) *launch.Launch {
	t.Helper()
	l := soloCommitteeLaunch()
	l.Allowlist = launch.NewAllowlistFromMembers([]launch.Member{{Address: mustAddr(testAddr2), Label: "acme"}})
	return l
}

func TestHandleMemberAdd_CommitteeSuccess(t *testing.T) {
	h := newHarness(t)
	l := soloCommitteeLaunch() // DRAFT; committee = testAddr1
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)

	w := h.doAuthJSON("POST", "/launch/"+l.ID.String()+"/members",
		[]byte(`{"address":"`+testAddr2+`","label":"acme-fleet"}`), tok)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var got struct {
		Address string `json:"address"`
		Label   string `json:"label"`
		AddedBy string `json:"added_by"`
		AddedAt string `json:"added_at"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, testAddr2, got.Address)
	assert.Equal(t, "acme-fleet", got.Label)
	assert.Equal(t, testAddr1, got.AddedBy, "provenance names the adding committee member")
	assert.NotEmpty(t, got.AddedAt)
}

func TestHandleMemberAdd_Unauthenticated(t *testing.T) {
	h := newHarness(t)
	l := soloCommitteeLaunch()
	h.launches.data[l.ID] = l
	// doJSON sends no Authorization header.
	w := h.doJSON("POST", "/launch/"+l.ID.String()+"/members", []byte(`{"address":"`+testAddr2+`"}`))
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestHandleMemberAdd_NonCommittee(t *testing.T) {
	h := newHarness(t)
	l := soloCommitteeLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr2) // not on the committee
	w := h.doAuthJSON("POST", "/launch/"+l.ID.String()+"/members",
		[]byte(`{"address":"`+testAddr3+`","label":"x"}`), tok)
	assertStatusCode(t, w, http.StatusForbidden)
}

func TestHandleMemberAdd_InvalidAddress(t *testing.T) {
	h := newHarness(t)
	l := soloCommitteeLaunch()
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	w := h.doAuthJSON("POST", "/launch/"+l.ID.String()+"/members",
		[]byte(`{"address":"not-a-bech32","label":"x"}`), tok)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestHandleMemberAdd_FrozenStatus(t *testing.T) {
	h := newHarness(t)
	l := soloCommitteeLaunch()
	l.Status = launch.StatusWindowClosed // members list frozen (E1)
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	w := h.doAuthJSON("POST", "/launch/"+l.ID.String()+"/members",
		[]byte(`{"address":"`+testAddr2+`","label":"x"}`), tok)
	assertStatusCode(t, w, http.StatusConflict)
}

func TestHandleMemberRemove_CommitteeSuccess(t *testing.T) {
	h := newHarness(t)
	l := launchWithMember(t)
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	w := h.do("DELETE", "/launch/"+l.ID.String()+"/members/"+testAddr2, nil,
		map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusNoContent)
}

func TestHandleMemberRemove_Absent(t *testing.T) {
	h := newHarness(t)
	l := soloCommitteeLaunch() // empty members list
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	w := h.do("DELETE", "/launch/"+l.ID.String()+"/members/"+testAddr2, nil,
		map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusNotFound)
}

func TestHandleMemberRemove_NonCommittee(t *testing.T) {
	h := newHarness(t)
	l := launchWithMember(t)
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr3) // not committee
	w := h.do("DELETE", "/launch/"+l.ID.String()+"/members/"+testAddr2, nil,
		map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusForbidden)
}

func TestHandleMemberList_CommitteeSuccess(t *testing.T) {
	h := newHarness(t)
	l := launchWithMember(t)
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr1)
	w := h.do("GET", "/launch/"+l.ID.String()+"/members", nil,
		map[string]string{"Authorization": "Bearer " + tok})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var got []struct {
		Address string `json:"address"`
		Label   string `json:"label"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got, 1)
	assert.Equal(t, testAddr2, got[0].Address)
	assert.Equal(t, "acme", got[0].Label)
}

func TestHandleMemberList_NonCommitteeForbidden(t *testing.T) {
	h := newHarness(t)
	l := launchWithMember(t)
	h.launches.data[l.ID] = l
	tok := h.seedSession(testAddr2) // a member, but not committee → still forbidden
	w := h.do("GET", "/launch/"+l.ID.String()+"/members", nil,
		map[string]string{"Authorization": "Bearer " + tok})
	assertStatusCode(t, w, http.StatusForbidden)
}

func TestHandleMemberList_Unauthenticated(t *testing.T) {
	h := newHarness(t)
	l := launchWithMember(t)
	h.launches.data[l.ID] = l
	w := h.do("GET", "/launch/"+l.ID.String()+"/members", nil, nil)
	assertStatusCode(t, w, http.StatusUnauthorized)
}
