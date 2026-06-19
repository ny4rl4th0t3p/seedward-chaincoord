package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/chaincoord/internal/application/services"
	"github.com/ny4rl4th0t3p/chaincoord/internal/domain/joinrequest"
)

// maxGentxDownloadCount is the upper bound on approved join requests fetched
// in a single gentx bundle download.
const maxGentxDownloadCount = 10000

// joinRequestJSON is the wire representation of a JoinRequest.
type joinRequestJSON struct {
	ID                 string          `json:"id"`
	LaunchID           string          `json:"launch_id"`
	OperatorAddress    string          `json:"operator_address"`
	ConsensusPubKey    string          `json:"consensus_pubkey"`
	GentxJSON          json.RawMessage `json:"gentx" swaggertype:"object"`
	PeerAddress        string          `json:"peer_address"`
	RPCEndpoint        string          `json:"rpc_endpoint"`
	Memo               string          `json:"memo"`
	SubmittedAt        time.Time       `json:"submitted_at"`
	Status             string          `json:"status"`
	RejectionReason    string          `json:"rejection_reason,omitempty"`
	ApprovedByProposal *string         `json:"approved_by_proposal,omitempty"`
}

func joinRequestToJSON(jr *joinrequest.JoinRequest) joinRequestJSON {
	out := joinRequestJSON{
		ID:              jr.ID.String(),
		LaunchID:        jr.LaunchID.String(),
		OperatorAddress: jr.OperatorAddress.String(),
		ConsensusPubKey: jr.ConsensusPubKey,
		GentxJSON:       jr.GentxJSON,
		PeerAddress:     jr.PeerAddress.String(),
		RPCEndpoint:     jr.RPCEndpoint.String(),
		Memo:            jr.Memo,
		SubmittedAt:     jr.SubmittedAt,
		Status:          string(jr.Status),
		RejectionReason: jr.RejectionReason,
	}
	if jr.ApprovedByProposal != nil {
		s := jr.ApprovedByProposal.String()
		out.ApprovedByProposal = &s
	}
	return out
}

// gentxEntry is one approved validator's gentx in the bundle download.
type gentxEntry struct {
	JoinRequestID   string          `json:"join_request_id"`
	OperatorAddress string          `json:"operator_address"`
	ConsensusPubKey string          `json:"consensus_pubkey"`
	Gentx           json.RawMessage `json:"gentx" swaggertype:"object"`
}

// gentxsResponse wraps the approved gentx bundle (GET /launch/{id}/gentxs).
type gentxsResponse struct {
	Gentxs []gentxEntry `json:"gentxs"`
}

// POST /launch/{id}/join
// Validator submits a join request.  Full payload including signature.
//
// @Summary      Submit a join request
// @Description  Validator submits a signed join request. Rate-limited to 60 req/IP/min.
// @Tags         join-requests
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id    path      string                true  "Launch UUID"
// @Param        body  body      services.SubmitInput  true  "Join request payload"
// @Success      201   {object}  joinRequestJSON
// @Failure      400   {object}  errorEnvelope
// @Failure      401   {object}  errorEnvelope
// @Failure      409   {object}  errorEnvelope
// @Router       /launch/{id}/join [post]
func (s *Server) handleJoinSubmit(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}

	var input services.SubmitInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
		return
	}

	jr, err := s.joinReqs.Submit(r.Context(), launchID, input)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	writeJSON(w, http.StatusCreated, joinRequestToJSON(jr))
}

// GET /launch/{id}/join
// Coordinator: lists all join requests for a launch.
// Optional ?status= filter.
//
// @Summary      List join requests
// @Description  Coordinator-only. Returns all join requests for a launch.
// @Tags         join-requests
// @Security     BearerAuth
// @Produce      json
// @Param        id        path      string  true   "Launch UUID"
// @Param        status    query     string  false  "Filter by status"  Enums(pending,approved,rejected)
// @Param        page      query     int     false  "Page number"       minimum(1)
// @Param        per_page  query     int     false  "Items per page"    minimum(1) maximum(100)
// @Success      200       {object}  pageEnvelope[[]joinRequestJSON]
// @Failure      400       {object}  errorEnvelope
// @Failure      401       {object}  errorEnvelope
// @Failure      403       {object}  errorEnvelope
// @Router       /launch/{id}/join [get]
func (s *Server) handleJoinList(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}

	callerAddr := operatorFromContext(r.Context())
	isCoord, err := s.launches.IsCoordinator(r.Context(), launchID, callerAddr)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	if !isCoord {
		writeError(w, http.StatusForbidden, "forbidden", "only committee members may list join requests")
		return
	}

	pg, ok := parsePagination(w, r)
	if !ok {
		return
	}

	var statusFilter *joinrequest.Status
	if s := r.URL.Query().Get("status"); s != "" {
		st := joinrequest.Status(s)
		statusFilter = &st
	}

	items, total, err := s.joinReqs.ListForLaunch(r.Context(), launchID, statusFilter, pg.Page, pg.PerPage)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	out := make([]joinRequestJSON, len(items))
	for i, jr := range items {
		out[i] = joinRequestToJSON(jr)
	}
	writeJSON(w, http.StatusOK, pageEnvelope[[]joinRequestJSON]{
		Items:   out,
		Total:   total,
		Page:    pg.Page,
		PerPage: pg.PerPage,
	})
}

// GET /launch/{id}/gentxs
// Returns the gentx JSON for every approved join request.
// Coordinator-only; used to assemble the final genesis (spec §4.2, §9).
//
// @Summary      Download approved gentxs
// @Description  Returns the gentx JSON for all approved join requests. Coordinator only.
// @Tags         join-requests
// @Security     BearerAuth
// @Produce      json
// @Param        id   path      string  true  "Launch UUID"
// @Success      200  {object}  gentxsResponse
// @Failure      400  {object}  errorEnvelope
// @Failure      401  {object}  errorEnvelope
// @Failure      403  {object}  errorEnvelope
// @Router       /launch/{id}/gentxs [get]
func (s *Server) handleGentxsGet(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}

	callerAddr := operatorFromContext(r.Context())
	isCoord, err := s.launches.IsCoordinator(r.Context(), launchID, callerAddr)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	if !isCoord {
		writeError(w, http.StatusForbidden, "forbidden", "only committee members may download gentxs")
		return
	}

	approved := joinrequest.StatusApproved
	items, _, err := s.joinReqs.ListForLaunch(r.Context(), launchID, &approved, 1, maxGentxDownloadCount)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	out := make([]gentxEntry, len(items))
	for i, jr := range items {
		out[i] = gentxEntry{
			JoinRequestID:   jr.ID.String(),
			OperatorAddress: jr.OperatorAddress.String(),
			ConsensusPubKey: jr.ConsensusPubKey,
			Gentx:           jr.GentxJSON,
		}
	}
	writeJSON(w, http.StatusOK, gentxsResponse{Gentxs: out})
}

// GET /launch/{id}/join/{req_id}
// Coordinator or the owning validator may fetch a single join request.
//
// @Summary      Get a join request
// @Description  Coordinator or the owning validator may fetch a single join request.
// @Tags         join-requests
// @Security     BearerAuth
// @Produce      json
// @Param        id      path      string  true  "Launch UUID"
// @Param        req_id  path      string  true  "Join request UUID"
// @Success      200     {object}  joinRequestJSON
// @Failure      400     {object}  errorEnvelope
// @Failure      401     {object}  errorEnvelope
// @Failure      403     {object}  errorEnvelope
// @Failure      404     {object}  errorEnvelope
// @Router       /launch/{id}/join/{req_id} [get]
func (s *Server) handleJoinGet(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}
	reqID, err := uuid.Parse(chi.URLParam(r, "req_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "req_id must be a valid UUID")
		return
	}

	callerAddr := operatorFromContext(r.Context())
	isCoord, err := s.launches.IsCoordinator(r.Context(), launchID, callerAddr)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	jr, err := s.joinReqs.GetByID(r.Context(), reqID, callerAddr, isCoord)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, joinRequestToJSON(jr))
}
