package api_test

import (
	"net/http"
	"testing"
)

// TestAPITopology pins the public mount (ADR-0027): the whole REST surface lives under /api/v1,
// while the ops endpoints (/healthz, /metrics) stay at the root, outside the versioned API.
// A regression here silently breaks every client's base URL, so it is asserted explicitly —
// the rest of the suite exercises the mount implicitly through the harness prefix.
func TestAPITopology(t *testing.T) {
	h := newHarness(t)

	t.Run("api is mounted under /api/v1", func(t *testing.T) {
		w := h.doRaw("GET", "/api/v1/launches", nil, nil)
		assertStatus(t, w, http.StatusOK)
	})

	t.Run("root-level api paths do not exist", func(t *testing.T) {
		w := h.doRaw("GET", "/launches", nil, nil)
		assertStatus(t, w, http.StatusNotFound)
	})

	t.Run("ops endpoints stay at the root", func(t *testing.T) {
		assertStatus(t, h.doRaw("GET", "/healthz", nil, nil), http.StatusOK)
		w := h.doRaw("GET", "/api/v1/healthz", nil, nil)
		assertStatus(t, w, http.StatusNotFound)
	})
}
