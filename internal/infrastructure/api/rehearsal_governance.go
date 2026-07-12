package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// rehearsalResultStepJSON is one step verdict in a rehearsal result.
type rehearsalResultStepJSON struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// rehearsalResultJSON is the committee-facing view of a stored rehearsal result.
type rehearsalResultJSON struct {
	AttemptID      string                    `json:"attempt_id"`
	InputSetHash   string                    `json:"input_set_hash"`
	Outcome        string                    `json:"outcome"`
	FailedStep     string                    `json:"failed_step,omitempty"`
	Summary        string                    `json:"summary"`
	Steps          []rehearsalResultStepJSON `json:"steps"`
	EngineVersion  string                    `json:"engine_version"`
	BinaryName     string                    `json:"binary_name"`
	BinaryVersion  string                    `json:"binary_version"`
	BinarySHA256   string                    `json:"binary_sha256"`
	Validators     int                       `json:"validators"`
	BlocksAdvanced int                       `json:"blocks_advanced"`
	StartedAt      string                    `json:"started_at"`
	FinishedAt     string                    `json:"finished_at"`
	Stale          bool                      `json:"stale"`
	RecordedAt     string                    `json:"recorded_at"`
}

func rehearsalResultToJSON(res *launch.RehearsalResult) rehearsalResultJSON {
	steps := make([]rehearsalResultStepJSON, len(res.Steps))
	for i, st := range res.Steps {
		steps[i] = rehearsalResultStepJSON{Name: st.Name, Status: st.Status, Detail: st.Detail}
	}
	return rehearsalResultJSON{
		AttemptID:      res.AttemptID.String(),
		InputSetHash:   res.InputSetHash,
		Outcome:        string(res.Outcome),
		FailedStep:     res.FailedStep,
		Summary:        res.Summary,
		Steps:          steps,
		EngineVersion:  res.EngineVersion,
		BinaryName:     res.BinaryName,
		BinaryVersion:  res.BinaryVersion,
		BinarySHA256:   res.BinarySHA256,
		Validators:     res.Validators,
		BlocksAdvanced: res.BlocksAdvanced,
		StartedAt:      res.StartedAt.UTC().Format(time.RFC3339),
		FinishedAt:     res.FinishedAt.UTC().Format(time.RFC3339),
		Stale:          res.Stale,
		RecordedAt:     res.RecordedAt.UTC().Format(time.RFC3339),
	}
}

// GET /launch/{id}/rehearsal
// Committee read-back of a launch's rehearsal results. Governance plane (committee-gated).
//
// @Summary      Rehearsal results (committee read-back)
// @Description  Returns the launch's recorded rehearsal results — outcome, which step failed, the
// @Description  input-set hash the run consumed, and a `stale` flag (the approved inputs changed
// @Description  since the run) — newest first. Committee members only.
// @Tags         rehearsal
// @Produce      json
// @Param        id  path  string  true  "Launch UUID"
// @Success      200  {array}   rehearsalResultJSON
// @Failure      400  {object}  errorEnvelope
// @Failure      401  {object}  errorEnvelope
// @Failure      403  {object}  errorEnvelope
// @Failure      404  {object}  errorEnvelope
// @Router       /launch/{id}/rehearsal [get]
func (s *Server) handleRehearsalResultsList(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}
	results, err := s.launches.ListRehearsalResults(r.Context(), launchID, operatorFromContext(r.Context()))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	out := make([]rehearsalResultJSON, len(results))
	for i, res := range results {
		out[i] = rehearsalResultToJSON(res)
	}
	writeJSON(w, http.StatusOK, out)
}

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
// @Failure      401  {object}  errorEnvelope
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
