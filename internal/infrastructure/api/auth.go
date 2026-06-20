package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/services"
)

// challengeRequest is the body for POST /auth/challenge.
type challengeRequest struct {
	OperatorAddress string `json:"operator_address" example:"cosmos1abcdef..."`
}

// challengeResponse is the response to an auth challenge request.
type challengeResponse struct {
	Challenge string `json:"challenge"`
}

// tokenResponse carries an issued session token.
type tokenResponse struct {
	Token string `json:"token"`
}

// sessionInfoJSON describes the current session.
type sessionInfoJSON struct {
	OperatorAddress string `json:"operator_address"`
	ExpiresAt       string `json:"expires_at"`
	IsCoordinator   bool   `json:"is_coordinator"`
}

// POST /auth/challenge
// Body: { "operator_address": "cosmos1..." }
// Response: { "challenge": "..." }
//
// @Summary      Request an auth challenge
// @Description  Issues a short-lived challenge for the given operator address. Rate-limited to 10 req/IP/min and 5 req/operator/5min.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      challengeRequest  true  "Operator address"
// @Success      200   {object}  challengeResponse
// @Failure      400   {object}  errorEnvelope
// @Failure      429   {object}  errorEnvelope
// @Router       /auth/challenge [post]
func (s *Server) handleAuthChallenge(w http.ResponseWriter, r *http.Request) {
	var body challengeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
		return
	}
	if body.OperatorAddress == "" {
		writeError(w, http.StatusBadRequest, "missing_field", "operator_address is required")
		return
	}

	challenge, err := s.auth.IssueChallenge(r.Context(), body.OperatorAddress)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, challengeResponse{Challenge: challenge})
}

// POST /auth/verify
// Body: services.VerifyChallengeInput (operator_address, challenge, nonce, timestamp, signature)
// Response: { "token": "..." }
//
// @Summary      Verify a signed challenge
// @Description  Validates the signed challenge response and issues a session token.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      services.VerifyChallengeInput  true  "Signed challenge payload"
// @Success      200   {object}  tokenResponse
// @Failure      400   {object}  errorEnvelope
// @Failure      401   {object}  errorEnvelope
// @Router       /auth/verify [post]
func (s *Server) handleAuthVerify(w http.ResponseWriter, r *http.Request) {
	var input services.VerifyChallengeInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
		return
	}

	token, err := s.auth.VerifyChallenge(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "auth_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, tokenResponse{Token: token})
}

// DELETE /auth/session
// Requires: Authorization: Bearer <token>
// Response: 204 No Content
//
// @Summary      Revoke the current session
// @Tags         auth
// @Security     BearerAuth
// @Success      204
// @Failure      401  {object}  errorEnvelope
// @Router       /auth/session [delete]
func (s *Server) handleAuthRevoke(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "missing_token", "Authorization header required")
		return
	}

	if err := s.auth.RevokeSession(r.Context(), token); err != nil {
		writeServiceError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GET /auth/session
// Requires: Authorization: Bearer <token>
// Response: { "operator_address": "...", "expires_at": "...", "is_coordinator": true/false }
//
// @Summary      Get current session info
// @Description  Returns the operator address, expiry, and coordinator status of the supplied session token.
// @Tags         auth
// @Security     BearerAuth
// @Produce      json
// @Success      200  {object}  sessionInfoJSON
// @Failure      401  {object}  errorEnvelope
// @Router       /auth/session [get]
func (s *Server) handleAuthSessionInfo(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "missing_token", "Authorization header required")
		return
	}

	info, err := s.auth.GetSessionInfo(token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_token", "token is invalid or expired")
		return
	}

	isCoordinator, _ := s.coordinatorAllowlist.Contains(r.Context(), info.OperatorAddress)

	writeJSON(w, http.StatusOK, sessionInfoJSON{
		OperatorAddress: info.OperatorAddress,
		ExpiresAt:       info.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z"),
		IsCoordinator:   isCoordinator,
	})
}

// DELETE /auth/sessions/all
// Requires: Authorization: Bearer <token>
// Revokes all tokens for the authenticated operator.
// Response: 204 No Content
//
// @Summary      Revoke all sessions for the current operator
// @Tags         auth
// @Security     BearerAuth
// @Success      204
// @Failure      401  {object}  errorEnvelope
// @Router       /auth/sessions/all [delete]
func (s *Server) handleAuthRevokeAll(w http.ResponseWriter, r *http.Request) {
	operatorAddr := operatorFromContext(r.Context())

	if err := s.auth.RevokeAllSessions(r.Context(), operatorAddr); err != nil {
		writeServiceError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// DELETE /admin/sessions/{address}
// Requires: admin authentication
// Revokes all tokens for the given operator address.
// Response: 204 No Content
//
// @Summary      Revoke all sessions for an operator (admin)
// @Tags         admin
// @Security     BearerAuth
// @Param        address  path  string  true  "Operator address"
// @Success      204
// @Failure      400  {object}  errorEnvelope
// @Failure      401  {object}  errorEnvelope
// @Failure      403  {object}  errorEnvelope
// @Router       /admin/sessions/{address} [delete]
func (s *Server) handleAdminRevokeAllSessions(w http.ResponseWriter, r *http.Request) {
	address := chi.URLParam(r, "address")
	if address == "" {
		writeError(w, http.StatusBadRequest, "missing_param", "address is required")
		return
	}

	if err := s.auth.RevokeAllSessions(r.Context(), address); err != nil {
		writeServiceError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
