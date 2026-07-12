package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/services"
)

// ── Response wire types ─────────────────────────────────────────────────────

// readinessConfirmJSON is the response to a readiness confirmation.
type readinessConfirmJSON struct {
	ID          string `json:"id"`
	LaunchID    string `json:"launch_id"`
	ConfirmedAt string `json:"confirmed_at"`
}

// validatorReadinessJSON is one validator's readiness row in the dashboard.
type validatorReadinessJSON struct {
	JoinRequestID        string     `json:"join_request_id"`
	OperatorAddress      string     `json:"operator_address"`
	Moniker              string     `json:"moniker"`
	VotingPowerPct       float64    `json:"voting_power_pct"`
	IsReady              bool       `json:"is_ready"`
	LastConfirmedAt      *time.Time `json:"last_confirmed_at,omitempty"`
	GenesisHashConfirmed string     `json:"genesis_hash_confirmed,omitempty"`
	BinaryHashConfirmed  string     `json:"binary_hash_confirmed,omitempty"`
}

// dashboardJSON is the combined launch + readiness dashboard.
type dashboardJSON struct {
	LaunchID             string                   `json:"launch_id"`
	ChainID              string                   `json:"chain_id"`
	Status               string                   `json:"status"`
	GenesisTime          *time.Time               `json:"genesis_time"`
	FinalGenesisSHA256   string                   `json:"final_genesis_sha256"`
	TotalApproved        int                      `json:"total_approved"`
	ConfirmedReady       int                      `json:"confirmed_ready"`
	VotingPowerConfirmed float64                  `json:"voting_power_confirmed"`
	ThresholdStatus      string                   `json:"threshold_status"`
	Validators           []validatorReadinessJSON `json:"validators"`
}

// peerJSON is one approved validator's peer entry.
type peerJSON struct {
	OperatorAddress string `json:"operator_address"`
	PeerAddress     string `json:"peer_address"`
}

// peersResponse wraps the approved-validator peer list (JSON format).
type peersResponse struct {
	Peers []peerJSON `json:"peers"`
}

// POST /launch/{id}/ready
// Validator submits a readiness confirmation.
//
// @Summary      Confirm readiness
// @Description  Validator submits a signed readiness confirmation. Rate-limited to 60 req/IP/min.
// @Tags         readiness
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id    path      string                 true  "Launch UUID"
// @Param        body  body      services.ConfirmInput  true  "Readiness confirmation"
// @Success      201   {object}  readinessConfirmJSON
// @Failure      400   {object}  errorEnvelope
// @Failure      401   {object}  errorEnvelope
// @Router       /launch/{id}/ready [post]
func (s *Server) handleReadinessConfirm(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}

	var input services.ConfirmInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
		return
	}

	rc, err := s.readiness.Confirm(r.Context(), launchID, input)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	writeJSON(w, http.StatusCreated, readinessConfirmJSON{
		ID:          rc.ID.String(),
		LaunchID:    rc.LaunchID.String(),
		ConfirmedAt: rc.ConfirmedAt.Format(time.RFC3339),
	})
}

// GET /launch/{id}/dashboard
// Returns the combined launch + readiness dashboard.
//
// @Summary      Get launch dashboard
// @Description  Returns combined launch and readiness status for all approved validators.
// @Tags         readiness
// @Produce      json
// @Param        id   path      string  true  "Launch UUID"
// @Success      200  {object}  dashboardJSON
// @Failure      400  {object}  errorEnvelope
// @Failure      404  {object}  errorEnvelope
// @Router       /launch/{id}/dashboard [get]
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}

	callerAddr := operatorFromContext(r.Context())

	launchDash, err := s.launches.GetDashboard(r.Context(), launchID, callerAddr)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	readinessDash, err := s.readiness.GetDashboard(r.Context(), launchID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	perVal := make([]validatorReadinessJSON, len(readinessDash.PerValidator))
	for i, v := range readinessDash.PerValidator {
		perVal[i] = validatorReadinessJSON{
			JoinRequestID:        v.JoinRequestID.String(),
			OperatorAddress:      v.OperatorAddress,
			Moniker:              v.Moniker,
			VotingPowerPct:       v.VotingPowerPct,
			IsReady:              v.IsReady,
			LastConfirmedAt:      v.LastConfirmedAt,
			GenesisHashConfirmed: v.GenesisHashConfirmed,
			BinaryHashConfirmed:  v.BinaryHashConfirmed,
		}
	}

	writeJSON(w, http.StatusOK, dashboardJSON{
		LaunchID:             launchDash.LaunchID.String(),
		ChainID:              launchDash.ChainID,
		Status:               string(launchDash.Status),
		GenesisTime:          launchDash.GenesisTime,
		FinalGenesisSHA256:   launchDash.FinalGenesisSHA256,
		TotalApproved:        readinessDash.TotalApproved,
		ConfirmedReady:       readinessDash.ConfirmedReady,
		VotingPowerConfirmed: readinessDash.VotingPowerConfirmed,
		ThresholdStatus:      readinessDash.ThresholdStatus,
		Validators:           perVal,
	})
}

// GET /launch/{id}/peers
// Returns peer addresses of all approved validators.
//
// @Summary      Get approved validator peers
// @Description  Returns peer addresses of all approved validators. Use ?format=text for persistent_peers format (comma-separated).
// @Tags         readiness
// @Produce      json
// @Param        id      path      string  true   "Launch UUID"
// @Param        format  query     string  false  "Output format"  Enums(json,text)
// @Success      200     {object}  peersResponse  "JSON format; with ?format=text returns comma-separated text/plain"
// @Failure      400     {object}  errorEnvelope
// @Failure      404     {object}  errorEnvelope
// @Router       /launch/{id}/peers [get]
func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}

	callerAddr := operatorFromContext(r.Context())
	// Visibility check — load the launch through the service.
	if _, err := s.launches.GetLaunch(r.Context(), launchID, callerAddr); err != nil {
		writeServiceError(w, r, err)
		return
	}

	peers, err := s.readiness.GetPeers(r.Context(), launchID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	// ?format=text returns a comma-separated plain-text list for use in
	// persistent_peers. Default is JSON.
	if r.URL.Query().Get("format") == "text" {
		addrs := make([]string, len(peers))
		for i, p := range peers {
			addrs[i] = p.PeerAddress
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(strings.Join(addrs, ",")))
		if err != nil {
			writeServiceError(w, r, err)
			return
		}
		return
	}

	out := make([]peerJSON, len(peers))
	for i, p := range peers {
		out[i] = peerJSON{OperatorAddress: p.OperatorAddress, PeerAddress: p.PeerAddress}
	}
	writeJSON(w, http.StatusOK, peersResponse{Peers: out})
}
