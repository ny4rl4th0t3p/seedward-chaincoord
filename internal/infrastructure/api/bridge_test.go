package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

func bridgePath(launchID string) string { return "/bridge/launches/" + launchID + "/rehearsal-input" }

func TestHandleRehearsalInput_Success(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	jr := testApprovedJoinRequest(l.ID, testAddr2)
	h.joinReqs.data[jr.ID] = jr

	w := h.do("GET", bridgePath(l.ID.String()), nil,
		map[string]string{"Authorization": "Bearer " + testOpsToken})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var body struct {
		SchemaVersion int    `json:"schema_version"`
		LaunchID      string `json:"launch_id"`
		Status        string `json:"status"`
		InputSetHash  string `json:"input_set_hash"`
		Chain         struct {
			ChainID     string `json:"chain_id"`
			TotalSupply string `json:"total_supply"`
			Binary      struct {
				SHA256 string `json:"sha256"`
			} `json:"binary"`
		} `json:"chain"`
		Gentxs []struct {
			OperatorAddress string `json:"operator_address"`
		} `json:"gentxs"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 1, body.SchemaVersion)
	assert.Equal(t, l.ID.String(), body.LaunchID)
	assert.Equal(t, string(l.Status), body.Status)
	assert.Len(t, body.InputSetHash, 64)
	assert.Equal(t, l.Record.ChainID, body.Chain.ChainID)
	assert.Equal(t, l.Record.BinarySHA256, body.Chain.Binary.SHA256)
	require.Len(t, body.Gentxs, 1)
	assert.Equal(t, testAddr2, body.Gentxs[0].OperatorAddress)
}

func TestHandleRehearsalInput_NoOpsToken(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.do("GET", bridgePath(l.ID.String()), nil, nil)
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestHandleRehearsalInput_WrongOpsToken(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.do("GET", bridgePath(l.ID.String()), nil,
		map[string]string{"Authorization": "Bearer wrong-token"})
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestHandleRehearsalInput_LaunchNotFound(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", bridgePath(uuid.New().String()), nil,
		map[string]string{"Authorization": "Bearer " + testOpsToken})
	assertStatusCode(t, w, http.StatusNotFound)
}

// rehearsal-input emits a per-file stream URL for an approved allocation (host OR attestor);
// coordd no longer inlines bytes, so airdrop-scale files never buffer.
func TestHandleRehearsalInput_AllocationURL(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	propID := uuid.New()
	l.AllocationFiles = []launch.AllocationFile{
		{Type: launch.AllocationAccounts, SHA256: "accountshash", Status: launch.AllocationApproved, ApprovedByProposal: &propID},
	}
	h.launches.data[l.ID] = l

	w := h.do("GET", bridgePath(l.ID.String()), nil,
		map[string]string{"Authorization": "Bearer " + testOpsToken})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var body struct {
		Allocations map[string]struct {
			SHA256 string `json:"sha256"`
			URL    string `json:"url"`
		} `json:"allocations"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	acc, ok := body.Allocations["accounts"]
	require.True(t, ok)
	assert.Equal(t, "accountshash", acc.SHA256)
	// The emitted URL is server-authored wire data: the full public path including the /api/v1
	// mount (the daemon resolves it against its coordd base URL), unlike allocPath, which is
	// harness-relative (h.do prefixes the mount).
	assert.Equal(t, "/api/v1"+allocPath(l.ID.String(), "accounts"), acc.URL)
}

func allocPath(launchID, allocType string) string {
	return "/bridge/launches/" + launchID + "/allocations/" + allocType
}

func TestHandleBridgeAllocationGet_HostStream(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	csv := "address,amount\ncosmos1abc,1000\n"
	require.NoError(t, h.allocation.Save(context.Background(), l.ID.String(), "accounts", []byte(csv)))

	w := h.do("GET", allocPath(l.ID.String(), "accounts"), nil,
		map[string]string{"Authorization": "Bearer " + testOpsToken})
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/octet-stream", w.Header().Get("Content-Type"))
	assert.Equal(t, csv, w.Body.String())
}

func TestHandleBridgeAllocationGet_AttestorRedirect(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	require.NoError(t, h.allocation.SaveRef(context.Background(), l.ID.String(), "claims",
		"https://example.com/claims.csv", "claimshash"))

	w := h.do("GET", allocPath(l.ID.String(), "claims"), nil,
		map[string]string{"Authorization": "Bearer " + testOpsToken})
	assertStatusCode(t, w, http.StatusFound)
	assert.Equal(t, "https://example.com/claims.csv", w.Header().Get("Location"))
}

func TestHandleBridgeAllocationGet_NoOpsToken(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.do("GET", allocPath(l.ID.String(), "accounts"), nil, nil)
	assertStatusCode(t, w, http.StatusUnauthorized)
}

func TestHandleBridgeAllocationGet_NotFound(t *testing.T) {
	h := newHarness(t)
	l := testLaunch()
	h.launches.data[l.ID] = l
	w := h.do("GET", allocPath(l.ID.String(), "accounts"), nil,
		map[string]string{"Authorization": "Bearer " + testOpsToken})
	assertStatusCode(t, w, http.StatusNotFound)
}
