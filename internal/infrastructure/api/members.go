package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// memberJSON is the wire representation of a launch members-list entry.
type memberJSON struct {
	Address string `json:"address"`
	Label   string `json:"label"`
	AddedBy string `json:"added_by,omitempty"`
	AddedAt string `json:"added_at,omitempty"`
}

// addMemberRequest is the body for POST /launch/{id}/members.
type addMemberRequest struct {
	Address string `json:"address"`
	Label   string `json:"label"`
}

func memberToJSON(m launch.Member) memberJSON {
	j := memberJSON{Address: m.Address.String(), Label: m.Label, AddedBy: m.AddedBy}
	if !m.AddedAt.IsZero() {
		j.AddedAt = m.AddedAt.UTC().Format(time.RFC3339)
	}
	return j
}

// POST /launch/{id}/members
// Add a hot actor address to the launch's members list.
//
// @Summary      Add a launch member
// @Description  Adds a hot actor address (with a label) to the launch's members list — granting see + submit access.
// @Description  Committee members only. Allowed only in DRAFT/PUBLISHED/WINDOW_OPEN. Idempotent on address.
// @Tags         members
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id    path      string            true  "Launch UUID"
// @Param        body  body      addMemberRequest  true  "Address and label to add"
// @Success      201   {object}  memberJSON
// @Failure      400   {object}  errorEnvelope
// @Failure      401   {object}  errorEnvelope
// @Failure      403   {object}  errorEnvelope
// @Failure      404   {object}  errorEnvelope
// @Failure      409   {object}  errorEnvelope
// @Router       /launch/{id}/members [post]
func (s *Server) handleMemberAdd(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}

	var body addMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
		return
	}
	if body.Address == "" {
		writeError(w, http.StatusBadRequest, "invalid_param", "address is required")
		return
	}

	callerAddr := operatorFromContext(r.Context())
	m, err := s.launches.AddMember(r.Context(), launchID, body.Address, body.Label, callerAddr)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, memberToJSON(*m))
}

// DELETE /launch/{id}/members/{address}
// Remove an address from the launch's members list.
//
// @Summary      Remove a launch member
// @Description  Removes a hot actor address from the launch's members list — revoking see + submit access.
// @Description  Committee members only. Allowed only in DRAFT/PUBLISHED/WINDOW_OPEN.
// @Tags         members
// @Security     BearerAuth
// @Produce      json
// @Param        id       path  string  true  "Launch UUID"
// @Param        address  path  string  true  "Operator address to remove"
// @Success      204      "No Content"
// @Failure      400      {object}  errorEnvelope
// @Failure      401      {object}  errorEnvelope
// @Failure      403      {object}  errorEnvelope
// @Failure      404      {object}  errorEnvelope
// @Failure      409      {object}  errorEnvelope
// @Router       /launch/{id}/members/{address} [delete]
func (s *Server) handleMemberRemove(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}
	address := chi.URLParam(r, "address")
	if address == "" {
		writeError(w, http.StatusBadRequest, "invalid_param", "address path parameter is required")
		return
	}

	callerAddr := operatorFromContext(r.Context())
	if err := s.launches.RemoveMember(r.Context(), launchID, address, callerAddr); err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /launch/{id}/members
// List the launch's members.
//
// @Summary      List launch members
// @Description  Returns the launch's members list (address + label + provenance), sorted by address. Committee members only.
// @Tags         members
// @Security     BearerAuth
// @Produce      json
// @Param        id  path  string  true  "Launch UUID"
// @Success      200  {array}   memberJSON
// @Failure      400  {object}  errorEnvelope
// @Failure      401  {object}  errorEnvelope
// @Failure      403  {object}  errorEnvelope
// @Failure      404  {object}  errorEnvelope
// @Router       /launch/{id}/members [get]
func (s *Server) handleMemberList(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}

	callerAddr := operatorFromContext(r.Context())
	members, err := s.launches.ListMembers(r.Context(), launchID, callerAddr)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	out := make([]memberJSON, len(members))
	for i, m := range members {
		out[i] = memberToJSON(m)
	}
	writeJSON(w, http.StatusOK, out)
}
