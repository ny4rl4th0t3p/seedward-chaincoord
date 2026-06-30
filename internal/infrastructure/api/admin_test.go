package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// adminHarness returns a harness where testAddr1 is an admin with a valid session.
func adminHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarnessWithAdmins(t, testAddr1)
	h.seedSession(testAddr1)
	return h
}

func adminToken() string { return "tok-" + testAddr1 }
func userToken() string  { return "tok-" + testAddr2 }

// ---- POST /admin/coordinators -----------------------------------------------

func TestHandleCoordinatorAdd(t *testing.T) {
	t.Run("unauthenticated → 401", func(t *testing.T) {
		h := adminHarness(t)
		w := h.doJSON(http.MethodPost, "/admin/coordinators", jsonBody(`{"address":"cosmos1abc"}`))
		assertStatus(t, w, http.StatusUnauthorized)
	})

	t.Run("non-admin → 403", func(t *testing.T) {
		h := adminHarness(t)
		h.seedSession(testAddr2)
		w := h.doAuthJSON(http.MethodPost, "/admin/coordinators", jsonBody(`{"address":"cosmos1abc"}`), userToken())
		assertStatus(t, w, http.StatusForbidden)
	})

	t.Run("missing address → 400", func(t *testing.T) {
		h := adminHarness(t)
		w := h.doAuthJSON(http.MethodPost, "/admin/coordinators", jsonBody(`{}`), adminToken())
		assertStatus(t, w, http.StatusBadRequest)
	})

	t.Run("admin adds address → 201", func(t *testing.T) {
		h := adminHarness(t)
		w := h.doAuthJSON(http.MethodPost, "/admin/coordinators",
			jsonBody(fmt.Sprintf(`{"address":%q}`, testAddr2)), adminToken())
		assertStatus(t, w, http.StatusCreated)
		assertContentTypeJSON(t, w)

		var body map[string]string
		require.NoError(t, json.NewDecoder(w.Body).Decode(&body), "decode body")
		assert.Equal(t, testAddr2, body["address"])
		assert.Equal(t, testAddr1, body["added_by"])
	})

	t.Run("idempotent — second add → 201", func(t *testing.T) {
		h := adminHarness(t)
		body := jsonBody(fmt.Sprintf(`{"address":%q}`, testAddr2))
		h.doAuthJSON(http.MethodPost, "/admin/coordinators", body, adminToken())
		w := h.doAuthJSON(http.MethodPost, "/admin/coordinators", body, adminToken())
		assertStatus(t, w, http.StatusCreated)
	})
}

// ---- DELETE /admin/coordinators/{address} -----------------------------------

func TestHandleCoordinatorRemove(t *testing.T) {
	t.Run("unauthenticated → 401", func(t *testing.T) {
		h := adminHarness(t)
		w := h.do(http.MethodDelete, "/admin/coordinators/"+testAddr2, nil, nil)
		assertStatus(t, w, http.StatusUnauthorized)
	})

	t.Run("non-admin → 403", func(t *testing.T) {
		h := adminHarness(t)
		h.seedSession(testAddr2)
		w := h.do(http.MethodDelete, "/admin/coordinators/"+testAddr3, nil,
			map[string]string{"Authorization": "Bearer " + userToken()})
		assertStatus(t, w, http.StatusForbidden)
	})

	t.Run("missing address → 404", func(t *testing.T) {
		h := adminHarness(t)
		w := h.do(http.MethodDelete, "/admin/coordinators/"+testAddr2, nil,
			map[string]string{"Authorization": "Bearer " + adminToken()})
		assertStatus(t, w, http.StatusNotFound)
	})

	t.Run("existing address → 204", func(t *testing.T) {
		h := adminHarness(t)
		// Seed the allowlist.
		h.doAuthJSON(http.MethodPost, "/admin/coordinators",
			jsonBody(fmt.Sprintf(`{"address":%q}`, testAddr2)), adminToken())

		w := h.do(http.MethodDelete, "/admin/coordinators/"+testAddr2, nil,
			map[string]string{"Authorization": "Bearer " + adminToken()})
		assertStatus(t, w, http.StatusNoContent)
	})
}

// ---- GET /admin/coordinators ------------------------------------------------

func TestHandleCoordinatorList(t *testing.T) {
	t.Run("unauthenticated → 401", func(t *testing.T) {
		h := adminHarness(t)
		w := h.do(http.MethodGet, "/admin/coordinators", nil, nil)
		assertStatus(t, w, http.StatusUnauthorized)
	})

	t.Run("non-admin → 403", func(t *testing.T) {
		h := adminHarness(t)
		h.seedSession(testAddr2)
		w := h.do(http.MethodGet, "/admin/coordinators", nil,
			map[string]string{"Authorization": "Bearer " + userToken()})
		assertStatus(t, w, http.StatusForbidden)
	})

	t.Run("empty list → 200 with zero total", func(t *testing.T) {
		h := adminHarness(t)
		w := h.do(http.MethodGet, "/admin/coordinators", nil,
			map[string]string{"Authorization": "Bearer " + adminToken()})
		assertStatus(t, w, http.StatusOK)
		assertContentTypeJSON(t, w)

		var pg struct {
			Total int `json:"total"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&pg), "decode")
		assert.Equal(t, 0, pg.Total)
	})

	t.Run("lists added entries with correct total", func(t *testing.T) {
		h := adminHarness(t)
		for _, addr := range []string{testAddr2, testAddr3} {
			h.doAuthJSON(http.MethodPost, "/admin/coordinators",
				jsonBody(fmt.Sprintf(`{"address":%q}`, addr)), adminToken())
		}

		w := h.do(http.MethodGet, "/admin/coordinators", nil,
			map[string]string{"Authorization": "Bearer " + adminToken()})
		assertStatus(t, w, http.StatusOK)

		var pg struct {
			Total int `json:"total"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&pg), "decode")
		assert.Equal(t, 2, pg.Total)
	})
}
