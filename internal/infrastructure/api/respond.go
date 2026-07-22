package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/rs/zerolog/hlog"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

// timeNow is the clock used by handlers. Override in tests.
var timeNow = time.Now

// errorEnvelope is the standard error response shape for all API errors.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code       string                `json:"code"`
	Message    string                `json:"message"`
	RequestID  string                `json:"request_id,omitempty"`
	Invariants []invariantResultJSON `json:"invariants,omitempty"`
}

// invariantResultJSON is one gentx invariant result in an error response.
type invariantResultJSON struct {
	Invariant string `json:"invariant"`
	OK        bool   `json:"ok"`
	Reason    string `json:"reason,omitempty"`
}

// writeJSON encodes v as JSON and writes it with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a standard error envelope.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: errorBody{Code: code, Message: message}})
}

const (
	defaultPage    = 1
	defaultPerPage = 20
	maxPerPage     = 100
)

// pagination holds parsed page/per_page query parameters.
type pagination struct {
	Page    int
	PerPage int
}

// parsePagination reads ?page=&per_page= from the request.
// Missing values fall back to defaults; per_page is capped at maxPerPage.
// Returns a 400 response and false if either value is present but not a positive
// integer (non-numeric or < 1).
func parsePagination(w http.ResponseWriter, r *http.Request) (pagination, bool) {
	p := pagination{Page: defaultPage, PerPage: defaultPerPage}

	if s := r.URL.Query().Get("page"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v < 1 {
			writeError(w, http.StatusBadRequest, "invalid_param", "page must be a positive integer")
			return p, false
		}
		p.Page = v
	}

	if s := r.URL.Query().Get("per_page"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v < 1 {
			writeError(w, http.StatusBadRequest, "invalid_param", "per_page must be a positive integer")
			return p, false
		}
		if v > maxPerPage {
			v = maxPerPage
		}
		p.PerPage = v
	}

	return p, true
}

// pageEnvelope wraps a list result with pagination metadata.
type pageEnvelope[T any] struct {
	Items   T   `json:"items"`
	Total   int `json:"total"`
	Page    int `json:"page"`
	PerPage int `json:"per_page"`
}

// writeServiceError maps application-layer sentinel errors to HTTP responses.
// For 5xx responses the request ID (injected by hlog.RequestIDHandler) is
// included in the error body so clients can correlate errors with server logs.
func writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	// A gentx-invalid failure carries per-invariant detail. Check it before the
	// ErrBadRequest case below, which it also unwraps to.
	var gentxErr *ports.GentxInvalidError
	if errors.As(err, &gentxErr) {
		inv := make([]invariantResultJSON, 0, len(gentxErr.Results))
		for _, res := range gentxErr.Results {
			inv = append(inv, invariantResultJSON{Invariant: res.Invariant, OK: res.OK, Reason: res.Reason})
		}
		writeJSON(w, http.StatusBadRequest, errorEnvelope{Error: errorBody{
			Code:       "gentx_invalid",
			Message:    gentxErr.Error(),
			Invariants: inv,
		}})
		return
	}

	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, ports.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, ports.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
	case errors.Is(err, ports.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
	case errors.Is(err, ports.ErrBadRequest):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, ports.ErrTooManyRequests):
		writeError(w, http.StatusTooManyRequests, "too_many_requests", err.Error())
	default:
		id, _ := hlog.IDFromRequest(r)
		hlog.FromRequest(r).Error().Err(err).Str("req_id", id.String()).Msg("internal server error")
		writeJSON(w, http.StatusInternalServerError, errorEnvelope{
			Error: errorBody{
				Code:      "internal_error",
				Message:   "an unexpected error occurred",
				RequestID: id.String(),
			},
		})
	}
}
