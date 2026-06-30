package api

// Internal test for writeServiceError (unexported): verifies the sentinel→HTTP
// mapping, that wrapped specific sentinels resolve via the errors.Is chain, that
// a gentx-invalid error renders its per-invariant detail, and that an unmapped
// error becomes an opaque 500 without leaking the underlying message.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-libs/gentxvalidate"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

func TestWriteServiceError_SentinelMapping(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantBody string // expected error.code in the envelope
	}{
		// --- base sentinels ---
		{"not found", ports.ErrNotFound, http.StatusNotFound, "not_found"},
		{"conflict", ports.ErrConflict, http.StatusConflict, "conflict"},
		{"unauthorized", ports.ErrUnauthorized, http.StatusUnauthorized, "unauthorized"},
		{"forbidden", ports.ErrForbidden, http.StatusForbidden, "forbidden"},
		{"bad request", ports.ErrBadRequest, http.StatusBadRequest, "bad_request"},
		{"too many requests", ports.ErrTooManyRequests, http.StatusTooManyRequests, "too_many_requests"},

		// --- specific sentinels resolve to their base via the errors.Is chain ---
		{"submission cap → 429", ports.ErrSubmissionCapReached, http.StatusTooManyRequests, "too_many_requests"},
		{"challenge mismatch → 401", ports.ErrChallengeMismatch, http.StatusUnauthorized, "unauthorized"},
		{"jr not approved → 403", ports.ErrJoinRequestNotApproved, http.StatusForbidden, "forbidden"},
		{"validator already approved → 409", ports.ErrValidatorAlreadyApproved, http.StatusConflict, "conflict"},

		// --- an extra wrapping layer must still resolve down the chain ---
		{
			"double-wrapped specific sentinel → 409",
			fmt.Errorf("apply approve validator: %w", ports.ErrValidatorAlreadyApproved),
			http.StatusConflict, "conflict",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)

			writeServiceError(w, r, tc.err)

			require.Equal(t, tc.wantCode, w.Code, "status; body: %s", w.Body.String())
			assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

			var env errorEnvelope
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
			assert.Equal(t, tc.wantBody, env.Error.Code)
			assert.Equal(t, tc.err.Error(), env.Error.Message, "4xx responses surface the underlying message")
		})
	}
}

func TestWriteServiceError_UnmappedIs500AndOpaque(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)

	// A plain error that wraps no sentinel must hit the default branch.
	writeServiceError(w, r, errors.New("some raw internal detail: db connection refused"))

	require.Equal(t, http.StatusInternalServerError, w.Code)

	var env errorEnvelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.Equal(t, "internal_error", env.Error.Code)
	assert.Equal(t, "an unexpected error occurred", env.Error.Message, "the 500 message must be opaque")
	assert.NotContains(t, w.Body.String(), "db connection refused", "the raw error must not leak to the client")
}

func TestWriteServiceError_GentxInvalidRendersInvariants(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)

	gerr := &ports.GentxInvalidError{
		Results: []gentxvalidate.Result{
			{Invariant: "well_formed", OK: true},
			{Invariant: "self_delegation", OK: false, Reason: "below minimum"},
		},
	}
	// GentxInvalidError unwraps to ErrBadRequest, but the gentx branch must win and
	// render the per-invariant detail rather than a plain bad_request.
	writeServiceError(w, r, gerr)

	require.Equal(t, http.StatusBadRequest, w.Code)

	var env errorEnvelope
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.Equal(t, "gentx_invalid", env.Error.Code)
	require.Len(t, env.Error.Invariants, 2)
	assert.Equal(t, "self_delegation", env.Error.Invariants[1].Invariant)
	assert.False(t, env.Error.Invariants[1].OK)
	assert.Equal(t, "below minimum", env.Error.Invariants[1].Reason)
}
