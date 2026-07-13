package api_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHealthz_OKByDefault(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/healthz", nil, nil)
	assertStatus(t, w, http.StatusOK)
}

func TestHealthz_OKWhenDependenciesUp(t *testing.T) {
	h := newHarness(t)
	h.server.WithHealthCheck(func(context.Context) error { return nil })
	w := h.do("GET", "/healthz", nil, nil)
	assertStatus(t, w, http.StatusOK)
}

func TestHealthz_503WhenDependencyDown(t *testing.T) {
	h := newHarness(t)
	h.server.WithHealthCheck(func(context.Context) error { return errors.New("db down") })
	w := h.do("GET", "/healthz", nil, nil)
	assertStatus(t, w, http.StatusServiceUnavailable)
}

func TestSecurityHeaders_PresentNoTLS(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/healthz", nil, nil)
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	assert.Empty(t, w.Header().Get("Strict-Transport-Security"), "no HSTS when TLS is off")
}

func TestSecurityHeaders_HSTSWhenTLS(t *testing.T) {
	h := newHarness(t)
	h.server.WithTLS(true)
	w := h.do("GET", "/healthz", nil, nil)
	assert.NotEmpty(t, w.Header().Get("Strict-Transport-Security"), "HSTS present when coordd terminates TLS")
}

func TestMetrics_Exposed(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/metrics", nil, nil)
	assertStatus(t, w, http.StatusOK)
	assert.Contains(t, w.Body.String(), "go_goroutines", "default Go runtime metrics are exposed")
}
