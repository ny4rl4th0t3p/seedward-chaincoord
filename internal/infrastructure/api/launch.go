package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/services"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/config"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// chainRecordJSON is the wire representation of a ChainRecord.
type chainRecordJSON struct {
	ChainID                 string     `json:"chain_id"`
	ChainName               string     `json:"chain_name"`
	Bech32Prefix            string     `json:"bech32_prefix"`
	BinaryName              string     `json:"binary_name"`
	BinaryVersion           string     `json:"binary_version"`
	BinarySHA256            string     `json:"binary_sha256"`
	RepoURL                 string     `json:"repo_url"`
	RepoCommit              string     `json:"repo_commit"`
	GenesisTime             *time.Time `json:"genesis_time,omitempty"`
	Denom                   string     `json:"denom"`
	MinSelfDelegation       string     `json:"min_self_delegation"`
	TotalSupply             string     `json:"total_supply,omitempty"`
	MaxCommissionRate       string     `json:"max_commission_rate"`
	MaxCommissionChangeRate string     `json:"max_commission_change_rate"`
	GentxDeadline           time.Time  `json:"gentx_deadline"`
	MinValidatorCount       int        `json:"min_validator_count"`
}

// launchJSON is the wire representation of a Launch aggregate.
type launchJSON struct {
	ID                   string          `json:"id"`
	Record               chainRecordJSON `json:"record"`
	LaunchType           string          `json:"launch_type"`
	Status               string          `json:"status"`
	InitialGenesisSHA256 string          `json:"initial_genesis_sha256,omitempty"`
	FinalGenesisSHA256   string          `json:"final_genesis_sha256,omitempty"`
	MonitorRPCURL        string          `json:"monitor_rpc_url,omitempty"`
	// RehearsalServicePubKey/RehearsalEndpoint are the bridge fields (not secret).
	RehearsalServicePubKey string    `json:"rehearsal_service_pubkey,omitempty"`
	RehearsalEndpoint      string    `json:"rehearsal_endpoint,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

func launchToJSON(l *launch.Launch) launchJSON {
	r := l.Record
	return launchJSON{
		ID: l.ID.String(),
		Record: chainRecordJSON{
			ChainID:                 r.ChainID,
			ChainName:               r.ChainName,
			Bech32Prefix:            r.Bech32Prefix,
			BinaryName:              r.BinaryName,
			BinaryVersion:           r.BinaryVersion,
			BinarySHA256:            r.BinarySHA256,
			RepoURL:                 r.RepoURL,
			RepoCommit:              r.RepoCommit,
			GenesisTime:             r.GenesisTime,
			Denom:                   r.Denom,
			MinSelfDelegation:       r.MinSelfDelegation,
			TotalSupply:             r.TotalSupply,
			MaxCommissionRate:       r.MaxCommissionRate.String(),
			MaxCommissionChangeRate: r.MaxCommissionChangeRate.String(),
			GentxDeadline:           r.GentxDeadline,
			MinValidatorCount:       r.MinValidatorCount,
		},
		LaunchType:             string(l.LaunchType),
		Status:                 string(l.Status),
		InitialGenesisSHA256:   l.InitialGenesisSHA256,
		FinalGenesisSHA256:     l.FinalGenesisSHA256,
		MonitorRPCURL:          l.MonitorRPCURL,
		RehearsalServicePubKey: l.RehearsalServicePubKey,
		RehearsalEndpoint:      l.RehearsalEndpoint,
		CreatedAt:              l.CreatedAt,
		UpdatedAt:              l.UpdatedAt,
	}
}

// createLaunchRequest is the body for POST /launch.
type createLaunchRequest struct {
	Record     chainRecordJSON      `json:"record"`
	LaunchType string               `json:"launch_type" example:"MAINNET"`
	Allowlist  []string             `json:"allowlist"`
	Committee  committeeRequestJSON `json:"committee"`
}

// POST /launch
// Body: full launch + committee definition.
// Response: 201 launch JSON.
//
// @Summary      Create a launch
// @Description  Creates a new chain launch. When launch_policy is "restricted", the caller must be on the coordinator allowlist.
// @Tags         launches
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body  body      createLaunchRequest  true  "Launch definition"
// @Success      201   {object}  launchJSON
// @Failure      400   {object}  errorEnvelope
// @Failure      401   {object}  errorEnvelope
// @Failure      403   {object}  errorEnvelope
// @Router       /launch [post]
func (s *Server) handleLaunchCreate(w http.ResponseWriter, r *http.Request) {
	if s.launchPolicy == config.LaunchPolicyRestricted {
		callerAddr := operatorFromContext(r.Context())
		allowed, err := s.coordinators.Contains(r.Context(), callerAddr)
		if err != nil {
			writeServiceError(w, r, err)
			return
		}
		if !allowed {
			writeError(w, http.StatusForbidden, "forbidden", "launch creation requires coordinator allowlist membership")
			return
		}
	}

	var body createLaunchRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
		return
	}

	record, err := parseChainRecord(body.Record)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_field", err.Error())
		return
	}

	committee, err := parseCommittee(body.Committee)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_field", err.Error())
		return
	}

	allowlist, err := parseOperatorAddresses(body.Allowlist)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_field", "allowlist: "+err.Error())
		return
	}

	input := services.CreateLaunchInput{
		Record:     record,
		LaunchType: launch.LaunchType(body.LaunchType),
		Allowlist:  allowlist,
		Committee:  committee,
	}

	l, err := s.launches.CreateLaunch(r.Context(), input)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	writeJSON(w, http.StatusCreated, launchToJSON(l))
}

// GET /launches
// Optional auth — visibility filtered.
// Response: paginated list of launches.
//
// @Summary      List launches
// @Description  Returns a paginated list of launches. Visibility is filtered based on auth status.
// @Tags         launches
// @Produce      json
// @Param        page      query     int  false  "Page number"     minimum(1)
// @Param        per_page  query     int  false  "Items per page"  minimum(1) maximum(100)
// @Success      200       {object}  pageEnvelope[[]launchJSON]
// @Failure      400       {object}  errorEnvelope
// @Router       /launches [get]
func (s *Server) handleLaunchList(w http.ResponseWriter, r *http.Request) {
	pg, ok := parsePagination(w, r)
	if !ok {
		return
	}

	callerAddr := operatorFromContext(r.Context())
	items, total, err := s.launches.ListLaunches(r.Context(), callerAddr, pg.Page, pg.PerPage)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	out := make([]launchJSON, len(items))
	for i, l := range items {
		out[i] = launchToJSON(l)
	}
	writeJSON(w, http.StatusOK, pageEnvelope[[]launchJSON]{
		Items:   out,
		Total:   total,
		Page:    pg.Page,
		PerPage: pg.PerPage,
	})
}

// GET /launch/{id}
// Optional auth — visibility-gated (committee ∪ members; a non-member gets 404).
//
// @Summary      Get a launch
// @Tags         launches
// @Produce      json
// @Param        id   path      string  true  "Launch UUID"
// @Success      200  {object}  launchJSON
// @Failure      400  {object}  errorEnvelope
// @Failure      404  {object}  errorEnvelope
// @Router       /launch/{id} [get]
func (s *Server) handleLaunchGet(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}

	callerAddr := operatorFromContext(r.Context())
	l, err := s.launches.GetLaunch(r.Context(), id, callerAddr)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, launchToJSON(l))
}

// patchLaunchRequest is the body for PATCH /launch/{id}. Every field is optional; only the
// fields present in the request are updated. Most fields require DRAFT status; monitor_rpc_url
// and the rehearsal bridge fields are settable at any status. The handler decodes the raw body
// to distinguish an absent field from a zero value — this type exists to document the contract
// for the generated spec.
type patchLaunchRequest struct {
	ChainName         *string    `json:"chain_name,omitempty"`
	BinaryVersion     *string    `json:"binary_version,omitempty"`
	BinarySHA256      *string    `json:"binary_sha256,omitempty"`
	RepoURL           *string    `json:"repo_url,omitempty"`
	RepoCommit        *string    `json:"repo_commit,omitempty"`
	MonitorRPCURL     *string    `json:"monitor_rpc_url,omitempty"`
	GenesisTime       *time.Time `json:"genesis_time,omitempty"`
	MinValidatorCount *int       `json:"min_validator_count,omitempty"`
	TotalSupply       *string    `json:"total_supply,omitempty"`
	Allowlist         []string   `json:"allowlist,omitempty"`
	// Bridge fields — operational, settable at any status.
	RehearsalServicePubKey *string `json:"rehearsal_service_pubkey,omitempty"`
	RehearsalEndpoint      *string `json:"rehearsal_endpoint,omitempty"`
}

// PATCH /launch/{id}
// Coordinator only — updates mutable fields on a DRAFT launch.
//
// @Summary      Update a launch
// @Description  Partially updates mutable fields on a launch (coordinator only). Most fields require
// @Description  DRAFT status; monitor_rpc_url and the rehearsal bridge fields are settable at any status.
// @Tags         launches
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id    path      string               true  "Launch UUID"
// @Param        body  body      patchLaunchRequest   false "Partial update (all fields optional)"
// @Success      200   {object}  launchJSON
// @Failure      400   {object}  errorEnvelope
// @Failure      401   {object}  errorEnvelope
// @Failure      403   {object}  errorEnvelope
// @Failure      404   {object}  errorEnvelope
// @Router       /launch/{id} [patch]
func (s *Server) handleLaunchPatch(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}

	// Use a raw map so we can distinguish absent fields from zero values.
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
		return
	}

	input, err := parsePatchInput(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_field", err.Error())
		return
	}

	callerAddr := operatorFromContext(r.Context())
	l, err := s.launches.PatchLaunch(r.Context(), id, input, callerAddr)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, launchToJSON(l))
}

// parseStringField returns a pointer to the string value at key, nil if the key
// is absent, or an error if the value cannot be unmarshalled as a string.
func parseStringField(raw map[string]json.RawMessage, key string) (*string, error) {
	v, ok := raw[key]
	if !ok {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return nil, fmt.Errorf("%s must be a string", key)
	}
	return &s, nil
}

func parsePatchInput(raw map[string]json.RawMessage) (services.PatchLaunchInput, error) {
	var (
		input services.PatchLaunchInput
		err   error
	)

	if input.ChainName, err = parseStringField(raw, "chain_name"); err != nil {
		return input, err
	}
	if input.BinaryVersion, err = parseStringField(raw, "binary_version"); err != nil {
		return input, err
	}
	if input.BinarySHA256, err = parseStringField(raw, "binary_sha256"); err != nil {
		return input, err
	}
	if input.RepoURL, err = parseStringField(raw, "repo_url"); err != nil {
		return input, err
	}
	if input.RepoCommit, err = parseStringField(raw, "repo_commit"); err != nil {
		return input, err
	}
	if input.MonitorRPCURL, err = parseStringField(raw, "monitor_rpc_url"); err != nil {
		return input, err
	}
	if input.TotalSupply, err = parseStringField(raw, "total_supply"); err != nil {
		return input, err
	}
	if input.RehearsalServicePubKey, err = parseStringField(raw, "rehearsal_service_pubkey"); err != nil {
		return input, err
	}
	if input.RehearsalEndpoint, err = parseStringField(raw, "rehearsal_endpoint"); err != nil {
		return input, err
	}

	if v, ok := raw["genesis_time"]; ok {
		var t time.Time
		if err := json.Unmarshal(v, &t); err != nil {
			return input, fmt.Errorf("genesis_time must be an ISO 8601 timestamp")
		}
		input.GenesisTime = &t
	}
	if v, ok := raw["min_validator_count"]; ok {
		var n int
		if err := json.Unmarshal(v, &n); err != nil {
			return input, fmt.Errorf("min_validator_count must be an integer")
		}
		input.MinValidatorCount = &n
	}
	if v, ok := raw["allowlist"]; ok {
		var addrs []string
		if err := json.Unmarshal(v, &addrs); err != nil {
			return input, fmt.Errorf("allowlist must be an array of strings")
		}
		parsed, err := parseOperatorAddresses(addrs)
		if err != nil {
			return input, fmt.Errorf("allowlist: %w", err)
		}
		input.Allowlist = parsed
	}

	return input, nil
}

// @Summary      Open the application window
// @Description  Transitions a launch from DRAFT to OPEN. Coordinator only.
// @Tags         launches
// @Security     BearerAuth
// @Produce      json
// @Param        id   path      string  true  "Launch UUID"
// @Success      200  {object}  launchJSON
// @Failure      400  {object}  errorEnvelope
// @Failure      401  {object}  errorEnvelope
// @Failure      403  {object}  errorEnvelope
// @Failure      404  {object}  errorEnvelope
// @Router       /launch/{id}/open-window [post]
func (s *Server) handleOpenWindow(w http.ResponseWriter, r *http.Request) {
	s.handleLaunchAction(w, r, s.launches.OpenWindow)
}

// POST /launch/{id}/cancel
// Committee lead only — transitions the launch to CANCELED from any non-terminal status.
//
// @Summary      Cancel a launch
// @Description  Transitions a launch to CANCELED. Only the committee lead may call this.
// @Description  No quorum required — cancellation is a single-actor emergency action.
// @Tags         launches
// @Security     BearerAuth
// @Produce      json
// @Param        id   path      string  true  "Launch UUID"
// @Success      200  {object}  launchJSON
// @Failure      401  {object}  errorEnvelope
// @Failure      403  {object}  errorEnvelope
// @Failure      404  {object}  errorEnvelope
// @Failure      409  {object}  errorEnvelope  "Launch is already in a terminal status"
// @Router       /launch/{id}/cancel [post]
func (s *Server) handleLaunchCancel(w http.ResponseWriter, r *http.Request) {
	s.handleLaunchAction(w, r, s.launches.CancelLaunch)
}

func (s *Server) handleLaunchAction(
	w http.ResponseWriter,
	r *http.Request,
	action func(ctx context.Context, id uuid.UUID, callerAddr string) error,
) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}
	callerAddr := operatorFromContext(r.Context())
	if err := action(r.Context(), id, callerAddr); err != nil {
		writeServiceError(w, r, err)
		return
	}
	l, err := s.launches.GetLaunch(r.Context(), id, callerAddr)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, launchToJSON(l))
}

// --- shared parsing helpers -----------------------------------------------

// committeeRequestJSON mirrors committeeMemberJSON but is used for the
// POST /launch and POST /launch/{id}/committee request bodies.
//
// Membership contract (enforced by launch.New and SetCommittee — both reject with 400 /
// lead-not-first-member otherwise):
//
//   - lead_address MUST equal members[0]. The lead is, by definition, the committee's first
//     member; leadership is position 0 of the list.
//   - The authenticated CREATOR need not be a committee member or the lead. Launch creation is
//     gated only by the coordinator allowlist (under launch_policy=restricted); the committee —
//     lead included — may be an entirely different set of parties. This is deliberate: a
//     coordinator may create a launch and delegate its governance wholesale to an external
//     committee whose lead is members[0].
//   - creation_signature is the lead's (members[0]'s) secp256k1 signature over the canonical
//     committee record, so a delegated committee is attested by its own lead, not the creator.
//
// UI note: set members[0] to whoever will lead — the connected wallet for a self-run launch, or
// the delegate for a handed-off one — BEFORE collecting the lead's signature.
type committeeRequestJSON struct {
	Members           []committeeMemberJSON `json:"members"`
	ThresholdM        int                   `json:"threshold_m"`
	TotalN            int                   `json:"total_n"`
	LeadAddress       string                `json:"lead_address"`
	CreationSignature string                `json:"creation_signature"`
}

func parseCommittee(body committeeRequestJSON) (launch.Committee, error) {
	leadAddr, err := launch.NewAccountID(body.LeadAddress)
	if err != nil {
		return launch.Committee{}, fmt.Errorf("committee.lead_address: %w", err)
	}
	sig, err := launch.NewSignature(body.CreationSignature)
	if err != nil {
		return launch.Committee{}, fmt.Errorf("committee.creation_signature: %w", err)
	}
	members := make([]launch.CommitteeMember, len(body.Members))
	for i, m := range body.Members {
		addr, err := launch.NewAccountID(m.Address)
		if err != nil {
			return launch.Committee{}, fmt.Errorf("committee.members[%d].address: %w", i, err)
		}
		members[i] = launch.CommitteeMember{
			Address:   addr,
			Moniker:   m.Moniker,
			PubKeyB64: m.PubKeyB64,
		}
	}
	return launch.Committee{
		ID:                uuid.New(),
		Members:           members,
		ThresholdM:        body.ThresholdM,
		TotalN:            body.TotalN,
		LeadAddress:       leadAddr,
		CreationSignature: sig,
		CreatedAt:         timeNow(),
	}, nil
}

func parseChainRecord(r chainRecordJSON) (launch.ChainRecord, error) {
	maxComm, err := launch.NewCommissionRate(r.MaxCommissionRate)
	if err != nil {
		return launch.ChainRecord{}, fmt.Errorf("max_commission_rate: %w", err)
	}
	maxCommChange, err := launch.NewCommissionRate(r.MaxCommissionChangeRate)
	if err != nil {
		return launch.ChainRecord{}, fmt.Errorf("max_commission_change_rate: %w", err)
	}
	return launch.ChainRecord{
		ChainID:                 r.ChainID,
		ChainName:               r.ChainName,
		Bech32Prefix:            r.Bech32Prefix,
		BinaryName:              r.BinaryName,
		BinaryVersion:           r.BinaryVersion,
		BinarySHA256:            r.BinarySHA256,
		RepoURL:                 r.RepoURL,
		RepoCommit:              r.RepoCommit,
		GenesisTime:             r.GenesisTime,
		Denom:                   r.Denom,
		MinSelfDelegation:       r.MinSelfDelegation,
		TotalSupply:             r.TotalSupply,
		MaxCommissionRate:       maxComm,
		MaxCommissionChangeRate: maxCommChange,
		GentxDeadline:           r.GentxDeadline,
		MinValidatorCount:       r.MinValidatorCount,
	}, nil
}

// chainHintJSON is the response body for GET /launch/{id}/chain-hint.
type chainHintJSON struct {
	ChainID      string `json:"chain_id"`
	ChainName    string `json:"chain_name"`
	Bech32Prefix string `json:"bech32_prefix"`
	Denom        string `json:"denom"`
}

// GET /launch/{id}/chain-hint
// Member-gated: returns the minimal chain metadata a member needs to register the
// network with a wallet (notably the bech32 prefix, to build a gentx). Non-members
// get 404 — the launch's existence is not revealed. A validator authenticates with
// any existing address first, then reads this to learn the launch's prefix.
//
// @Summary      Chain hint
// @Description  Returns chain_id, chain_name, bech32_prefix, and denom. Visible only to a launch member
// @Description  (committee ∪ allowlist); non-members get 404.
// @Tags         launches
// @Security     BearerAuth
// @Produce      json
// @Param        id   path      string  true  "Launch UUID"
// @Success      200  {object}  chainHintJSON
// @Failure      400  {object}  errorEnvelope
// @Failure      404  {object}  errorEnvelope
// @Router       /launch/{id}/chain-hint [get]
func (s *Server) handleChainHint(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "launch id must be a valid UUID")
		return
	}
	callerAddr := operatorFromContext(r.Context())
	hint, err := s.launches.GetChainHint(r.Context(), id, callerAddr)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, chainHintJSON{
		ChainID:      hint.ChainID,
		ChainName:    hint.ChainName,
		Bech32Prefix: hint.Bech32Prefix,
		Denom:        hint.Denom,
	})
}

func parseOperatorAddresses(addrs []string) ([]launch.AccountID, error) {
	out := make([]launch.AccountID, len(addrs))
	for i, a := range addrs {
		addr, err := launch.NewAccountID(a)
		if err != nil {
			return nil, fmt.Errorf("[%d]: %w", i, err)
		}
		out[i] = addr
	}
	return out, nil
}
