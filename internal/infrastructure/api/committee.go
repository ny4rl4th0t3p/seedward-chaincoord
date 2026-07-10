package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// committeeMemberJSON is the wire representation of a single committee member.
type committeeMemberJSON struct {
	Address   string `json:"address"`
	Moniker   string `json:"moniker"`
	PubKeyB64 string `json:"pub_key_b64"`
}

// committeeJSON is the wire representation of a Committee.
type committeeJSON struct {
	ID                string                `json:"id"`
	Members           []committeeMemberJSON `json:"members"`
	ThresholdM        int                   `json:"threshold_m"`
	TotalN            int                   `json:"total_n"`
	LeadAddress       string                `json:"lead_address"`
	CreationSignature string                `json:"creation_signature"`
	CreatedAt         time.Time             `json:"created_at"`
}

func committeeToJSON(c launch.Committee) committeeJSON {
	members := make([]committeeMemberJSON, len(c.Members))
	for i, m := range c.Members {
		members[i] = committeeMemberJSON{
			Address:   m.Address.String(),
			Moniker:   m.Moniker,
			PubKeyB64: m.PubKeyB64,
		}
	}
	return committeeJSON{
		ID:                c.ID.String(),
		Members:           members,
		ThresholdM:        c.ThresholdM,
		TotalN:            c.TotalN,
		LeadAddress:       c.LeadAddress.String(),
		CreationSignature: c.CreationSignature.String(),
		CreatedAt:         c.CreatedAt,
	}
}

// POST /launch/{id}/committee
// Replaces the committee on a DRAFT launch.  Only the lead coordinator may call this.
// Body: { members, threshold_m, total_n, lead_address, creation_signature }
// Response: 200 committee JSON
//
// @Summary      Set committee
// @Description  Replaces the committee on a DRAFT launch. Lead coordinator only.
// @Tags         committee
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id    path      string               true  "Launch UUID"
// @Param        body  body      committeeRequestJSON  true  "Committee definition"
// @Success      200   {object}  committeeJSON
// @Failure      400   {object}  errorEnvelope
// @Failure      401   {object}  errorEnvelope
// @Failure      403   {object}  errorEnvelope
// @Failure      404   {object}  errorEnvelope
// @Router       /launch/{id}/committee [post]
func (s *Server) handleCommitteeCreate(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}

	var body struct {
		Members           []committeeMemberJSON `json:"members"`
		ThresholdM        int                   `json:"threshold_m"`
		TotalN            int                   `json:"total_n"`
		LeadAddress       string                `json:"lead_address"`
		CreationSignature string                `json:"creation_signature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
		return
	}

	// Build domain value objects.
	leadAddr, err := launch.NewAccountID(body.LeadAddress)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_field", "lead_address: "+err.Error())
		return
	}
	sig, err := launch.NewSignature(body.CreationSignature)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_field", "creation_signature: "+err.Error())
		return
	}
	members := make([]launch.CommitteeMember, len(body.Members))
	for i, m := range body.Members {
		addr, err := launch.NewAccountID(m.Address)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_field", fmt.Sprintf("members[%d]: address: %s", i, err.Error()))
			return
		}
		members[i] = launch.CommitteeMember{
			Address:   addr,
			Moniker:   m.Moniker,
			PubKeyB64: m.PubKeyB64,
		}
	}

	committee := launch.Committee{
		ID:                uuid.New(),
		Members:           members,
		ThresholdM:        body.ThresholdM,
		TotalN:            body.TotalN,
		LeadAddress:       leadAddr,
		CreationSignature: sig,
		CreatedAt:         timeNow(),
	}

	callerAddr := operatorFromContext(r.Context())
	if err := s.launches.SetCommittee(r.Context(), launchID, committee, callerAddr); err != nil {
		writeServiceError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, committeeToJSON(committee))
}

// GET /committee/{launch_id}
// Returns the committee for a launch.
//
// @Summary      Get committee
// @Tags         committee
// @Produce      json
// @Param        launch_id  path      string  true  "Launch UUID"
// @Success      200        {object}  committeeJSON
// @Failure      400        {object}  errorEnvelope
// @Failure      404        {object}  errorEnvelope
// @Router       /committee/{launch_id} [get]
func (s *Server) handleCommitteeGet(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "launch_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch_id must be a valid UUID")
		return
	}

	callerAddr := operatorFromContext(r.Context())
	committee, err := s.launches.GetCommittee(r.Context(), launchID, callerAddr)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, committeeToJSON(committee))
}
