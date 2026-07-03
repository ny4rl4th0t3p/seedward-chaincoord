package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/rs/zerolog/hlog"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

type contextKey int

const operatorAddrKey contextKey = iota

// requireAuth validates the Bearer session token and injects the operator
// address into the request context.  Returns 401 if the token is missing,
// malformed, or invalid.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing_token", "Authorization header required")
			return
		}
		operatorAddr, err := s.sessions.Validate(r.Context(), token)
		if err != nil {
			if errors.Is(err, ports.ErrUnauthorized) {
				writeError(w, http.StatusUnauthorized, "invalid_token", "session token is invalid or expired")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal_error", "could not validate session")
			return
		}
		ctx := context.WithValue(r.Context(), operatorAddrKey, operatorAddr)
		next(w, r.WithContext(ctx))
	}
}

// operatorFromContext returns the authenticated operator address stored by
// requireAuth.  Panics if called outside an authenticated handler — callers
// must always be behind requireAuth.
func operatorFromContext(ctx context.Context) string {
	v, _ := ctx.Value(operatorAddrKey).(string)
	return v
}

// optionalAuth attempts to resolve the caller's operator address from a Bearer
// token if one is present.  Unlike requireAuth it never rejects the request —
// unauthenticated callers simply get an empty operator address in context.
// This is used on public endpoints so the service layer can apply allowlist
// visibility filtering for callers who happen to be authenticated.
func (s *Server) optionalAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token := bearerToken(r); token != "" {
			if operatorAddr, err := s.sessions.Validate(r.Context(), token); err == nil {
				ctx := context.WithValue(r.Context(), operatorAddrKey, operatorAddr)
				r = r.WithContext(ctx)
			}
		}
		next(w, r)
	}
}

// requireAdmin validates the Bearer session token and additionally checks that
// the authenticated operator address appears in the server's admin list.
// Returns 401 if not authenticated, 403 if authenticated but not an admin.
// On success the operator address is injected into context identically to requireAuth.
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing_token", "Authorization header required")
			return
		}
		operatorAddr, err := s.sessions.Validate(r.Context(), token)
		if err != nil {
			if errors.Is(err, ports.ErrUnauthorized) {
				writeError(w, http.StatusUnauthorized, "invalid_token", "session token is invalid or expired")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal_error", "could not validate session")
			return
		}
		if _, ok := s.adminAddresses[operatorAddr]; !ok {
			writeError(w, http.StatusForbidden, "forbidden", "admin access required")
			return
		}
		ctx := context.WithValue(r.Context(), operatorAddrKey, operatorAddr)
		next(w, r.WithContext(ctx))
	}
}

// requireOps gates the ops-plane bridge endpoints (/bridge/*) on the shared rehearsal ops
// token (bridge contract D6/D4). It compares the Bearer token against the configured
// rehearsal_ops_token in constant time. Fail-closed: if no token is configured the bridge is
// disabled and every request is rejected. Unlike requireAuth it injects no operator identity —
// the ops plane is a headless shared service credential, never a wallet (DEC-14).
func (s *Server) requireOps(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.rehearsalOpsToken == "" {
			writeError(w, http.StatusUnauthorized, "ops_credential_required", "rehearsal bridge is not enabled")
			return
		}
		token := bearerToken(r)
		if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(s.rehearsalOpsToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "invalid_ops_credential", "a valid ops credential is required")
			return
		}
		next(w, r)
	}
}

// requireJSONBody is a middleware that enforces two invariants on POST/PATCH
// requests whose body is JSON:
//  1. Content-Type must be application/json (returns 415 otherwise).
//  2. The body is capped at maxBytes to prevent unbounded reads (returns 413
//     if the limit is exceeded when the handler reads the body).
//
// Do not apply this to the genesis upload endpoint, which accepts raw bytes
// with its own size cap.
func requireJSONBody(maxBytes int64, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ct := r.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "application/json") {
			writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type",
				"Content-Type must be application/json")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next(w, r)
	}
}

// bearerToken extracts the token from "Authorization: Bearer <token>".
// Returns empty string if the header is absent or malformed.
func bearerToken(r *http.Request) string {
	hdr := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(hdr, "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(token)
}

// recoveryMiddleware catches panics in downstream handlers, logs the panic
// value and stack trace via the request-scoped zerolog logger, and returns
// a 500 Internal Server Error to the client.
func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				hlog.FromRequest(r).Error().
					Str("panic", fmt.Sprintf("%v", v)).
					Str("stack", string(debug.Stack())).
					Msg("panic recovered")
				writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
