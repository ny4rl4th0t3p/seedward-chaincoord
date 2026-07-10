package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/services"
)

// bridgeAllocationURL is the coordd path where the daemon streams an approved allocation file.
func bridgeAllocationURL(launchID, allocType string) string {
	return "/bridge/launches/" + launchID + "/allocations/" + allocType
}

// rehearsalInputSchemaVersion is the wire schema version of the rehearsal-input payload.
const rehearsalInputSchemaVersion = 1

// The wire DTOs below MUST match seedward-rehearsal/internal/bridge (input.go) field-for-field
// — that is the normative bridge contract. Drift is caught by the wire-golden
// tests on both sides (TestRehearsalInputJSON_WireGolden here; TestRehearsalInput_DecodesWireGolden
// in the daemon). status is carried for the service's status-filter; it is not in input_set_hash.

type rehearsalInputJSON struct {
	SchemaVersion int                           `json:"schema_version"`
	LaunchID      string                        `json:"launch_id"`
	AttemptID     string                        `json:"attempt_id"`
	GeneratedAt   string                        `json:"generated_at"`
	Status        string                        `json:"status"`
	Chain         rehearsalChainJSON            `json:"chain"`
	Gentxs        []rehearsalGentxJSON          `json:"gentxs"`
	Allocations   map[string]rehearsalAllocJSON `json:"allocations"`
	InputSetHash  string                        `json:"input_set_hash"`
}

type rehearsalChainJSON struct {
	ChainID                 string              `json:"chain_id"`
	Bech32Prefix            string              `json:"bech32_prefix"`
	Denom                   string              `json:"denom"`
	TotalSupply             string              `json:"total_supply"`
	MinSelfDelegation       string              `json:"min_self_delegation"`
	MaxCommissionRate       string              `json:"max_commission_rate"`
	MaxCommissionChangeRate string              `json:"max_commission_change_rate"`
	MinValidatorCount       int                 `json:"min_validator_count"`
	GenesisTime             *string             `json:"genesis_time"`
	Binary                  rehearsalBinaryJSON `json:"binary"`
}

type rehearsalBinaryJSON struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	SHA256     string `json:"sha256"`
	RepoURL    string `json:"repo_url"`
	RepoCommit string `json:"repo_commit"`
}

type rehearsalGentxJSON struct {
	OperatorAddress string          `json:"operator_address"`
	ConsensusPubkey string          `json:"consensus_pubkey"`
	Moniker         string          `json:"moniker"`
	SelfDelegation  string          `json:"self_delegation"`
	Gentx           json.RawMessage `json:"gentx" swaggertype:"object"`
}

type rehearsalAllocJSON struct {
	SHA256             string `json:"sha256"`
	ApprovedByProposal string `json:"approved_by_proposal"`
	// URL is where the daemon streams the file — coordd's ops-gated per-file stream endpoint
	// (host bytes) or, for attestor-mode files, a 302 to the external URL. Relative to coordd's
	// base URL. The bytes are NOT inlined, so airdrop-scale files never buffer in memory.
	URL string `json:"url"`
}

// GET /bridge/launches/{id}/rehearsal-input
// Serve the complete approved rehearsal build input. Ops-plane (requireOps).
//
// @Summary      Rehearsal input (bridge)
// @Description  Returns the launch's approved rehearsal build input (chain + approved gentxs + approved
// @Description  allocation files, host or attestor mode) plus input_set_hash. Ops-credential only. The
// @Description  daemon streams each allocation by reference (host bytes, or a 302 to the attestor URL).
// @Tags         bridge
// @Produce      json
// @Param        id  path      string  true  "Launch UUID"
// @Success      200  {object}  rehearsalInputJSON
// @Failure      400  {object}  errorEnvelope
// @Failure      401  {object}  errorEnvelope
// @Failure      404  {object}  errorEnvelope
// @Router       /bridge/launches/{id}/rehearsal-input [get]
func (s *Server) handleRehearsalInput(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}
	in, err := s.launches.PreviewRehearsalInput(r.Context(), launchID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rehearsalInputToJSON(in))
}

// rehearsalClaimRequest is the POST rehearsal-claim body.
type rehearsalClaimRequest struct {
	RunnerID string `json:"runner_id"`
}

// rehearsalLeaseConflictJSON is the 409 body when another runner holds the run lease.
type rehearsalLeaseConflictJSON struct {
	ClaimedBy      string `json:"claimed_by"`
	ClaimedAt      string `json:"claimed_at"`
	LeaseExpiresAt string `json:"lease_expires_at"`
}

// POST /bridge/launches/{id}/rehearsal-claim
// Claim the run lease for a launch's current input set and return the build input. Ops-plane.
//
// @Summary      Claim a rehearsal run (bridge)
// @Description  Acquires the single-writer run lease for the launch's current input set and returns the
// @Description  full build input (chain + gentxs + allocation URLs + attempt_id). 409 if another runner
// @Description  already holds an unexpired lease. Ops-credential only.
// @Tags         bridge
// @Accept       json
// @Produce      json
// @Param        id       path  string                  true  "Launch UUID"
// @Param        request  body  rehearsalClaimRequest   true  "Runner identity"
// @Success      200  {object}  rehearsalInputJSON
// @Failure      400  {object}  errorEnvelope
// @Failure      401  {object}  errorEnvelope
// @Failure      404  {object}  errorEnvelope
// @Failure      409  {object}  rehearsalLeaseConflictJSON
// @Router       /bridge/launches/{id}/rehearsal-claim [post]
func (s *Server) handleRehearsalClaim(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}
	var req rehearsalClaimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "claim request must be valid JSON")
		return
	}
	if req.RunnerID == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", "runner_id is required")
		return
	}
	in, err := s.launches.ClaimRehearsalRun(r.Context(), launchID, req.RunnerID)
	if err != nil {
		var leased *services.RehearsalLeasedError
		if errors.As(err, &leased) {
			writeJSON(w, http.StatusConflict, rehearsalLeaseConflictJSON{
				ClaimedBy:      leased.RunnerID,
				ClaimedAt:      leased.ClaimedAt.UTC().Format(time.RFC3339),
				LeaseExpiresAt: leased.LeaseExpiresAt.UTC().Format(time.RFC3339),
			})
			return
		}
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rehearsalInputToJSON(in))
}

// GET /bridge/launches/{id}/allocations/{type}
// Stream an approved allocation file for the rehearsal daemon. Ops-plane (requireOps).
//
// @Summary      Rehearsal allocation file (bridge)
// @Description  Streams a launch's allocation file (host mode → raw bytes; attestor mode → 302 to the
// @Description  external URL). Ops-credential only. Streamed, not buffered — safe for airdrop-scale files.
// @Tags         bridge
// @Produce      application/octet-stream
// @Param        id    path  string  true  "Launch UUID"
// @Param        type  path  string  true  "Allocation type (accounts/claims/grants/authz/feegrant)"
// @Success      200   {string}  string  "Raw file bytes (host mode)"
// @Success      302   {string}  string  "Redirect to external URL (attestor mode)"
// @Failure      400   {object}  errorEnvelope
// @Failure      401   {object}  errorEnvelope
// @Failure      404   {object}  errorEnvelope
// @Router       /bridge/launches/{id}/allocations/{type} [get]
func (s *Server) handleBridgeAllocationGet(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}
	// Ops-gated by requireOps (route); no visibility gate. GetRef 404s if nothing is stored.
	s.serveAllocationRef(w, r, id.String(), chi.URLParam(r, "type"))
}

// rehearsalResultAckJSON is coordd's acknowledgement of a stored result fact.
type rehearsalResultAckJSON struct {
	LaunchID   string `json:"launch_id"`
	AttemptID  string `json:"attempt_id"`
	Outcome    string `json:"outcome"`
	Stale      bool   `json:"stale"`
	RecordedAt string `json:"recorded_at"`
}

// POST /bridge/launches/{id}/rehearsal-results
// Store a signed rehearsal result fact. Ops-plane (requireOps).
//
// @Summary      Rehearsal result write-back (bridge)
// @Description  Verifies the fact's Ed25519 signature against the launch's trusted service pubkey and
// @Description  that it references an attempt coordd minted (anti-fabrication), then stores it —
// @Description  flagged stale if the approved input set has since changed. Ops-credential only.
// @Tags         bridge
// @Accept       json
// @Produce      json
// @Param        id      path  string                    true  "Launch UUID"
// @Param        fact    body  services.RehearsalResultFact  true  "Signed result fact"
// @Success      200  {object}  rehearsalResultAckJSON
// @Failure      400  {object}  errorEnvelope
// @Failure      401  {object}  errorEnvelope
// @Failure      404  {object}  errorEnvelope
// @Failure      409  {object}  errorEnvelope
// @Router       /bridge/launches/{id}/rehearsal-results [post]
func (s *Server) handleRehearsalResults(w http.ResponseWriter, r *http.Request) {
	launchID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}
	var fact services.RehearsalResultFact
	if err := json.NewDecoder(r.Body).Decode(&fact); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "result fact must be valid JSON")
		return
	}
	res, err := s.launches.RecordRehearsalResult(r.Context(), launchID, fact)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rehearsalResultAckJSON{
		LaunchID:   res.LaunchID.String(),
		AttemptID:  res.AttemptID.String(),
		Outcome:    string(res.Outcome),
		Stale:      res.Stale,
		RecordedAt: res.RecordedAt.UTC().Format(time.RFC3339),
	})
}

func rehearsalInputToJSON(in *services.RehearsalInput) rehearsalInputJSON {
	gentxs := make([]rehearsalGentxJSON, len(in.Gentxs))
	for i, g := range in.Gentxs {
		gentxs[i] = rehearsalGentxJSON{
			OperatorAddress: g.OperatorAddress,
			ConsensusPubkey: g.ConsensusPubKey,
			Moniker:         g.Moniker,
			SelfDelegation:  g.SelfDelegation,
			Gentx:           g.GentxJSON,
		}
	}
	allocs := make(map[string]rehearsalAllocJSON, len(in.Allocations))
	for _, a := range in.Allocations {
		allocs[a.Type] = rehearsalAllocJSON{
			SHA256:             a.SHA256,
			ApprovedByProposal: a.ApprovedByProposal,
			URL:                bridgeAllocationURL(in.LaunchID.String(), a.Type),
		}
	}
	var genesisTime *string
	if in.Chain.GenesisTime != nil {
		gt := in.Chain.GenesisTime.UTC().Format(time.RFC3339)
		genesisTime = &gt
	}
	return rehearsalInputJSON{
		SchemaVersion: rehearsalInputSchemaVersion,
		LaunchID:      in.LaunchID.String(),
		AttemptID:     in.AttemptID.String(),
		GeneratedAt:   in.GeneratedAt.UTC().Format(time.RFC3339),
		Status:        string(in.Status),
		Chain: rehearsalChainJSON{
			ChainID:                 in.Chain.ChainID,
			Bech32Prefix:            in.Chain.Bech32Prefix,
			Denom:                   in.Chain.Denom,
			TotalSupply:             in.Chain.TotalSupply,
			MinSelfDelegation:       in.Chain.MinSelfDelegation,
			MaxCommissionRate:       in.Chain.MaxCommissionRate,
			MaxCommissionChangeRate: in.Chain.MaxCommissionChangeRate,
			MinValidatorCount:       in.Chain.MinValidatorCount,
			GenesisTime:             genesisTime,
			Binary: rehearsalBinaryJSON{
				Name:       in.Chain.BinaryName,
				Version:    in.Chain.BinaryVersion,
				SHA256:     in.Chain.BinarySHA256,
				RepoURL:    in.Chain.RepoURL,
				RepoCommit: in.Chain.RepoCommit,
			},
		},
		Gentxs:       gentxs,
		Allocations:  allocs,
		InputSetHash: in.InputSetHash,
	}
}
