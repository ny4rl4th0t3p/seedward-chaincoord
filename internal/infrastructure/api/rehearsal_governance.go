package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// POST /launch/{id}/rehearsal/{attempt_id}/reset
// Force-release a stuck rehearsal run lease. Committee-gated (governance plane, not /bridge).
//
// @Summary      Reset a rehearsal run lease
// @Description  Coordinator override that returns a stuck (crashed-runner) rehearsal attempt to OPEN so
// @Description  the run can be re-claimed before the lease TTL expires. Committee members only.
// @Tags         rehearsal
// @Param        id          path  string  true  "Launch UUID"
// @Param        attempt_id  path  string  true  "Attempt UUID"
// @Success      204  "Lease reset"
// @Failure      400  {object}  errorEnvelope
// @Failure      403  {object}  errorEnvelope
// @Failure      404  {object}  errorEnvelope
// @Router       /launch/{id}/rehearsal/{attempt_id}/reset [post]
func (s *Server) handleRehearsalReset(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}
	attemptID, err := uuid.Parse(chi.URLParam(r, "attempt_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "attempt id must be a valid UUID")
		return
	}
	if err := s.launches.ResetRehearsalAttempt(r.Context(), launchID, attemptID, operatorFromContext(r.Context())); err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
