package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

// coordinatorAllowlistEntryJSON is the wire representation of a coordinator allowlist entry.
type coordinatorAllowlistEntryJSON struct {
	Address string `json:"address"`
	AddedBy string `json:"added_by"`
	AddedAt string `json:"added_at"`
}

// addCoordinatorRequest is the request body for POST /admin/coordinators.
type addCoordinatorRequest struct {
	Address string `json:"address"`
}

// POST /admin/coordinators
// Add an address to the coordinator allowlist.
//
// @Summary      Add coordinator to allowlist
// @Description  Adds an address to the coordinator allowlist. Admin only. Idempotent.
// @Tags         admin
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body  body      addCoordinatorRequest          true  "Address to add"
// @Success      201   {object}  coordinatorAllowlistEntryJSON
// @Failure      400   {object}  errorEnvelope
// @Failure      401   {object}  errorEnvelope
// @Failure      403   {object}  errorEnvelope
// @Router       /admin/coordinators [post]
func (s *Server) handleCoordinatorAdd(w http.ResponseWriter, r *http.Request) {
	var body addCoordinatorRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
		return
	}
	if body.Address == "" {
		writeError(w, http.StatusBadRequest, "invalid_param", "address is required")
		return
	}

	addedBy := operatorFromContext(r.Context())
	if err := s.coordinatorAllowlist.Add(r.Context(), body.Address, addedBy); err != nil {
		writeServiceError(w, r, err)
		return
	}

	// Return the canonical account form (what was stored), consistent with List —
	// coordinators are global, so there is no launch prefix to render under.
	writeJSON(w, http.StatusCreated, coordinatorAllowlistEntryJSON{
		Address: accountLookupKey(body.Address),
		AddedBy: addedBy,
	})
}

// DELETE /admin/coordinators/{address}
// Remove an address from the coordinator allowlist.
//
// @Summary      Remove coordinator from allowlist
// @Description  Removes an address from the coordinator allowlist. Admin only.
// @Tags         admin
// @Security     BearerAuth
// @Produce      json
// @Param        address  path  string  true  "Operator address to remove"
// @Success      204      "No Content"
// @Failure      401      {object}  errorEnvelope
// @Failure      403      {object}  errorEnvelope
// @Failure      404      {object}  errorEnvelope
// @Router       /admin/coordinators/{address} [delete]
func (s *Server) handleCoordinatorRemove(w http.ResponseWriter, r *http.Request) {
	address := chi.URLParam(r, "address")
	if address == "" {
		writeError(w, http.StatusBadRequest, "invalid_param", "address path parameter is required")
		return
	}

	if err := s.coordinatorAllowlist.Remove(r.Context(), address); err != nil {
		writeServiceError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GET /admin/coordinators
// List all addresses on the coordinator allowlist.
//
// @Summary      List coordinator allowlist
// @Description  Returns all addresses on the coordinator allowlist. Admin only. Paginated.
// @Tags         admin
// @Security     BearerAuth
// @Produce      json
// @Param        page      query     int  false  "Page number"    minimum(1)
// @Param        per_page  query     int  false  "Items per page" minimum(1) maximum(100)
// @Success      200       {object}  pageEnvelope[[]coordinatorAllowlistEntryJSON]
// @Failure      401       {object}  errorEnvelope
// @Failure      403       {object}  errorEnvelope
// @Router       /admin/coordinators [get]
func (s *Server) handleCoordinatorList(w http.ResponseWriter, r *http.Request) {
	pg, ok := parsePagination(w, r)
	if !ok {
		return
	}

	entries, total, err := s.coordinatorAllowlist.List(r.Context(), pg.Page, pg.PerPage)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	out := make([]coordinatorAllowlistEntryJSON, len(entries))
	for i, e := range entries {
		out[i] = coordinatorEntryToJSON(e)
	}
	writeJSON(w, http.StatusOK, pageEnvelope[[]coordinatorAllowlistEntryJSON]{
		Items:   out,
		Total:   total,
		Page:    pg.Page,
		PerPage: pg.PerPage,
	})
}

func coordinatorEntryToJSON(e *ports.CoordinatorAllowlistEntry) coordinatorAllowlistEntryJSON {
	return coordinatorAllowlistEntryJSON{
		Address: e.Address,
		AddedBy: e.AddedBy,
		AddedAt: e.AddedAt,
	}
}
