package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/services"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/proposal"
)

// proposalJSON is the wire representation of a Proposal.
type proposalJSON struct {
	ID         string               `json:"id"`
	LaunchID   string               `json:"launch_id"`
	ActionType string               `json:"action_type"`
	Payload    json.RawMessage      `json:"payload" swaggertype:"object"`
	ProposedBy string               `json:"proposed_by"`
	ProposedAt time.Time            `json:"proposed_at"`
	TTLExpires time.Time            `json:"ttl_expires"`
	Status     string               `json:"status"`
	ExecutedAt *time.Time           `json:"executed_at,omitempty"`
	Signatures []signatureEntryJSON `json:"signatures"`
}

type signatureEntryJSON struct {
	MemberAddress string    `json:"member_address"`
	Decision      string    `json:"decision"`
	Timestamp     time.Time `json:"timestamp"`
}

func proposalToJSON(p *proposal.Proposal) proposalJSON {
	sigs := make([]signatureEntryJSON, len(p.Signatures))
	for i, s := range p.Signatures {
		sigs[i] = signatureEntryJSON{
			MemberAddress: s.MemberAddress.String(),
			Decision:      string(s.Decision),
			Timestamp:     s.Timestamp,
		}
	}
	return proposalJSON{
		ID:         p.ID.String(),
		LaunchID:   p.LaunchID.String(),
		ActionType: string(p.ActionType),
		Payload:    p.Payload,
		ProposedBy: p.ProposedBy.String(),
		ProposedAt: p.ProposedAt,
		TTLExpires: p.TTLExpires,
		Status:     string(p.Status),
		ExecutedAt: p.ExecutedAt,
		Signatures: sigs,
	}
}

// POST /launch/{id}/proposal
// Committee member raises a new action proposal.
//
// @Summary      Raise a proposal
// @Description  Committee member raises a new action proposal. Rate-limited to 60 req/IP/min.
// @Description  409 if a conflicting proposal is already pending for the launch: an identical one
// @Description  (same action + payload), or a contradictory validator decision (approve/reject/remove
// @Description  targeting the same join request).
// @Tags         proposals
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id    path      string              true  "Launch UUID"
// @Param        body  body      services.RaiseInput  true  "Proposal payload"
// @Success      201   {object}  proposalJSON
// @Failure      400   {object}  errorEnvelope
// @Failure      401   {object}  errorEnvelope
// @Failure      403   {object}  errorEnvelope
// @Failure      409   {object}  errorEnvelope
// @Router       /launch/{id}/proposal [post]
func (s *Server) handleProposalRaise(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}

	var input services.RaiseInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
		return
	}

	// Ensure the caller is only acting as themselves.
	if input.MemberAddr != operatorFromContext(r.Context()) {
		writeError(w, http.StatusForbidden, "forbidden", "member_address must match the authenticated session")
		return
	}

	p, err := s.proposals.Raise(r.Context(), launchID, input)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	writeJSON(w, http.StatusCreated, proposalToJSON(p))
}

// GET /launch/{id}/proposals
// Returns all proposals for a launch, paginated.
//
// @Summary      List proposals
// @Tags         proposals
// @Security     BearerAuth
// @Produce      json
// @Param        id        path      string  true   "Launch UUID"
// @Param        page      query     int     false  "Page number"     minimum(1)
// @Param        per_page  query     int     false  "Items per page"  minimum(1) maximum(100)
// @Success      200       {object}  pageEnvelope[[]proposalJSON]
// @Failure      400       {object}  errorEnvelope
// @Failure      401       {object}  errorEnvelope
// @Router       /launch/{id}/proposals [get]
func (s *Server) handleProposalList(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}

	pg, ok := parsePagination(w, r)
	if !ok {
		return
	}

	items, total, err := s.proposals.ListForLaunch(r.Context(), launchID, pg.Page, pg.PerPage)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	out := make([]proposalJSON, len(items))
	for i, p := range items {
		out[i] = proposalToJSON(p)
	}
	writeJSON(w, http.StatusOK, pageEnvelope[[]proposalJSON]{
		Items:   out,
		Total:   total,
		Page:    pg.Page,
		PerPage: pg.PerPage,
	})
}

// GET /launch/{id}/proposal/{prop_id}
// Returns a single proposal.
//
// @Summary      Get a proposal
// @Tags         proposals
// @Security     BearerAuth
// @Produce      json
// @Param        id       path      string  true  "Launch UUID"
// @Param        prop_id  path      string  true  "Proposal UUID"
// @Success      200      {object}  proposalJSON
// @Failure      400      {object}  errorEnvelope
// @Failure      401      {object}  errorEnvelope
// @Failure      404      {object}  errorEnvelope
// @Router       /launch/{id}/proposal/{prop_id} [get]
func (s *Server) handleProposalGet(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}
	propID, err := uuid.Parse(chi.URLParam(r, "prop_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "prop_id must be a valid UUID")
		return
	}

	p, err := s.proposals.GetByID(r.Context(), launchID, propID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, proposalToJSON(p))
}

// POST /launch/{id}/proposal/{prop_id}/sign
// Committee member signs or vetoes a pending proposal.
//
// @Summary      Sign or veto a proposal
// @Description  Committee member adds their SIGN or VETO decision to a pending proposal. Rate-limited to 60 req/IP/min.
// @Tags         proposals
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id       path      string             true  "Launch UUID"
// @Param        prop_id  path      string             true  "Proposal UUID"
// @Param        body     body      services.SignInput  true  "Signing payload"
// @Success      200      {object}  proposalJSON
// @Failure      400      {object}  errorEnvelope
// @Failure      401      {object}  errorEnvelope
// @Failure      403      {object}  errorEnvelope
// @Failure      404      {object}  errorEnvelope
// @Router       /launch/{id}/proposal/{prop_id}/sign [post]
func (s *Server) handleProposalSign(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}
	propID, err := uuid.Parse(chi.URLParam(r, "prop_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "prop_id must be a valid UUID")
		return
	}

	var input services.SignInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
		return
	}

	if input.MemberAddr != operatorFromContext(r.Context()) {
		writeError(w, http.StatusForbidden, "forbidden", "member_address must match the authenticated session")
		return
	}

	p, err := s.proposals.Sign(r.Context(), launchID, propID, input)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, proposalToJSON(p))
}
