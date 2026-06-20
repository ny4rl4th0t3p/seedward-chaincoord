package api

import (
	"encoding/base64"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

// auditResponse wraps a launch's audit log entries.
type auditResponse struct {
	Entries []ports.AuditEvent `json:"entries"`
}

// auditPubKeyResponse carries the server's Ed25519 audit public key (base64).
type auditPubKeyResponse struct {
	PublicKey string `json:"public_key"`
}

// GET /launch/{id}/audit
// Returns the audit log entries for a launch.  Post-launch, observer access.
//
// @Summary      Get audit log
// @Description  Returns all audit log entries for a launch. Visibility-gated (same rules as GET /launch/{id}).
// @Tags         audit
// @Produce      json
// @Param        id   path      string  true  "Launch UUID"
// @Success      200  {object}  auditResponse
// @Failure      400  {object}  errorEnvelope
// @Failure      404  {object}  errorEnvelope
// @Router       /launch/{id}/audit [get]
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}

	// Visibility check.
	callerAddr := operatorFromContext(r.Context())
	if _, err := s.launches.GetLaunch(r.Context(), launchID, callerAddr); err != nil {
		writeServiceError(w, r, err)
		return
	}

	entries, err := s.auditLog.ReadForLaunch(r.Context(), launchID.String())
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	if entries == nil {
		entries = []ports.AuditEvent{}
	}

	writeJSON(w, http.StatusOK, auditResponse{Entries: entries})
}

// GET /audit/pubkey
// Returns the server's Ed25519 audit public key so external verifiers can
// validate audit log entry signatures without access to server config.
//
// @Summary      Get audit public key
// @Description  Returns the server's Ed25519 public key for offline audit log signature verification.
// @Tags         audit
// @Produce      json
// @Success      200  {object}  auditPubKeyResponse
// @Failure      503  {object}  errorEnvelope
// @Router       /audit/pubkey [get]
func (s *Server) handleAuditPubKey(w http.ResponseWriter, _ *http.Request) {
	if s.auditPubKey == nil {
		writeError(w, http.StatusServiceUnavailable, "no_audit_key", "server has no audit signing key configured")
		return
	}
	writeJSON(w, http.StatusOK, auditPubKeyResponse{
		PublicKey: base64.StdEncoding.EncodeToString(s.auditPubKey),
	})
}
