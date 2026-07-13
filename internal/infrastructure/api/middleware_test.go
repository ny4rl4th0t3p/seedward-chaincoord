package api

// Internal package tests for unexported middleware (requireAdmin and requireOps).
// Uses package api directly so unexported fields and methods are accessible.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// stubSessions is a minimal ports.SessionStore used only by middleware tests.
type stubSessions struct {
	tokens map[string]string // token → address
}

func (s *stubSessions) Issue(_ context.Context, addr string) (string, error) {
	tok := "tok-" + addr
	s.tokens[tok] = addr
	return tok, nil
}
func (s *stubSessions) Validate(_ context.Context, token string) (string, error) {
	if addr, ok := s.tokens[token]; ok {
		return addr, nil
	}
	return "", ports.ErrUnauthorized
}
func (s *stubSessions) Revoke(_ context.Context, token string) error {
	delete(s.tokens, token)
	return nil
}

func (s *stubSessions) RevokeAllForOperator(_ context.Context, addr string) error {
	for tok, a := range s.tokens {
		if a == addr {
			delete(s.tokens, tok)
		}
	}
	return nil
}

func (s *stubSessions) ParseClaims(token string) (string, time.Time, error) {
	if addr, ok := s.tokens[token]; ok {
		return addr, time.Now().Add(time.Hour), nil
	}
	return "", time.Time{}, ports.ErrUnauthorized
}

// adminServer builds a minimal Server with only the fields requireAdmin needs.
func adminServer(admins []string, tokens map[string]string) *Server {
	am := make(map[string]struct{}, len(admins))
	for _, a := range admins {
		am[accountLookupKey(a)] = struct{}{} // normalize like production NewServer (account-keyed)
	}
	return &Server{
		adminAddresses: am,
		sessions:       &stubSessions{tokens: tokens},
	}
}

func TestRequireAdmin(t *testing.T) {
	const adminAddr = "cosmos1admin000000000000000000000000000000000"
	const userAddr = "cosmos1user0000000000000000000000000000000000"

	tokens := map[string]string{
		"admin-token": adminAddr,
		"user-token":  userAddr,
	}
	srv := adminServer([]string{adminAddr}, tokens)

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{
			name:       "no token → 401",
			authHeader: "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid token → 401",
			authHeader: "Bearer bogus",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "valid token but not admin → 403",
			authHeader: "Bearer user-token",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "valid admin token → 200",
			authHeader: "Bearer admin-token",
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			if tc.authHeader != "" {
				r.Header.Set("Authorization", tc.authHeader)
			}
			w := httptest.NewRecorder()
			srv.requireAdmin(ok)(w, r)
			assert.Equal(t, tc.wantStatus, w.Code, "body: %s", w.Body.String())
		})
	}
}

func TestRequireAdmin_HRPIndependent(t *testing.T) {
	// An admin configured under one HRP must still be recognized when the session authenticates
	// under a DIFFERENT HRP for the SAME account — admin membership is account-keyed, not display.
	const adminCosmos = "cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu" // valid bech32 account
	adminOsmo, err := launch.MustNewAccountID(adminCosmos).Bech32("osmo")
	require.NoError(t, err)

	srv := adminServer([]string{adminCosmos}, map[string]string{"osmo-admin-token": adminOsmo})

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	r.Header.Set("Authorization", "Bearer osmo-admin-token")
	w := httptest.NewRecorder()
	srv.requireAdmin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))(w, r)
	assert.Equal(t, http.StatusOK, w.Code, "admin under a different HRP must be recognized; body: %s", w.Body.String())
}

func TestRequireOps(t *testing.T) {
	const opsToken = "s3cr3t-ops-token"

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name       string
		configured string // the server's rehearsal_ops_token
		authHeader string
		wantStatus int
	}{
		{"valid ops token → 200", opsToken, "Bearer " + opsToken, http.StatusOK},
		{"no token → 401", opsToken, "", http.StatusUnauthorized},
		{"wrong token → 401", opsToken, "Bearer nope", http.StatusUnauthorized},
		{"not configured, fail-closed → 401", "", "Bearer " + opsToken, http.StatusUnauthorized},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := &Server{rehearsalOpsToken: tc.configured}
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			if tc.authHeader != "" {
				r.Header.Set("Authorization", tc.authHeader)
			}
			w := httptest.NewRecorder()
			srv.requireOps(ok)(w, r)
			assert.Equal(t, tc.wantStatus, w.Code, "body: %s", w.Body.String())
		})
	}
}
