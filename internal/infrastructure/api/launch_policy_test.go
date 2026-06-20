package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/config"
)

// minimalLaunchBody is a POST /launch payload that passes all parsing.
// The service layer will reject it (missing real data), but the policy
// check fires before service calls, so for policy tests we only need
// to get past JSON parsing and into the handler body.
const minimalLaunchBody = `{
	"record": {
		"chain_id": "test-1",
		"binary_name": "gaiad",
		"denom": "uatom",
		"min_self_delegation": "1",
		"max_commission_rate": "0.10",
		"max_commission_change_rate": "0.01",
		"gentx_deadline": "2099-01-01T00:00:00Z",
		"application_window_open": "2099-01-01T00:00:00Z"
	},
	"launch_type": "MAINNET",
	"visibility": "PUBLIC",
	"committee": {
		"members": [{"address":"cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu","moniker":"m","pubkey_b64":"AAAA"}],
		"threshold_m": 1,
		"total_n": 1,
		"lead_address": "cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu",
		"creation_signature": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="
	}
}`

func TestHandleLaunchCreate_OpenPolicy(t *testing.T) {
	t.Run("any authenticated caller may create a launch", func(t *testing.T) {
		h := newHarnessWithPolicy(t, config.LaunchPolicyOpen)
		h.seedSession(testAddr1)
		// Service will reject due to missing data, but we get past the policy
		// gate and into service layer (not a 403).
		w := h.doAuthJSON(http.MethodPost, "/launch", jsonBody(minimalLaunchBody), "tok-"+testAddr1)
		if w.Code == http.StatusForbidden {
			t.Errorf("open policy: got 403, want any non-403 status")
		}
	})

	t.Run("caller not on allowlist still passes open policy", func(t *testing.T) {
		h := newHarnessWithPolicy(t, config.LaunchPolicyOpen)
		h.seedSession(testAddr2) // testAddr2 is NOT on allowlist
		w := h.doAuthJSON(http.MethodPost, "/launch", jsonBody(minimalLaunchBody), "tok-"+testAddr2)
		if w.Code == http.StatusForbidden {
			t.Errorf("open policy: non-allowlisted caller got 403")
		}
	})
}

func TestHandleLaunchCreate_RestrictedPolicy(t *testing.T) {
	t.Run("caller not on allowlist → 403", func(t *testing.T) {
		h := newHarnessWithPolicy(t, config.LaunchPolicyRestricted)
		h.seedSession(testAddr1)
		w := h.doAuthJSON(http.MethodPost, "/launch", jsonBody(minimalLaunchBody), "tok-"+testAddr1)
		assertStatus(t, w, http.StatusForbidden)
	})

	t.Run("caller on allowlist passes policy gate", func(t *testing.T) {
		h := newHarnessWithPolicy(t, config.LaunchPolicyRestricted, testAddr1)
		h.seedSession(testAddr1)
		// Pre-seed testAddr1 onto the coordinator allowlist.
		_ = h.allowlist.Add(context.Background(), testAddr1, "admin")
		w := h.doAuthJSON(http.MethodPost, "/launch", jsonBody(minimalLaunchBody), "tok-"+testAddr1)
		// Policy gate passes; service may return an error, but not 403.
		if w.Code == http.StatusForbidden {
			t.Errorf("restricted policy: allowlisted caller got 403")
		}
	})

	t.Run("unauthenticated request → 401 (auth check before policy)", func(t *testing.T) {
		h := newHarnessWithPolicy(t, config.LaunchPolicyRestricted)
		w := h.doJSON(http.MethodPost, "/launch", jsonBody(minimalLaunchBody))
		assertStatus(t, w, http.StatusUnauthorized)
	})
}
