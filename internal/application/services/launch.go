package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/joinrequest"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/netutil"
)

// LaunchService handles use cases related to the Launch aggregate lifecycle.
type LaunchService struct {
	launches     ports.LaunchRepository
	joinRequests ports.JoinRequestRepository
	readiness    ports.ReadinessRepository
	genesis      ports.GenesisStore
	events       ports.EventPublisher
	audit        ports.AuditLogWriter
	urlValidator func(string) error
}

func NewLaunchService(
	launches ports.LaunchRepository,
	joinRequests ports.JoinRequestRepository,
	readiness ports.ReadinessRepository,
	genesis ports.GenesisStore,
	events ports.EventPublisher,
	audit ports.AuditLogWriter,
) *LaunchService {
	return &LaunchService{
		launches:     launches,
		joinRequests: joinRequests,
		readiness:    readiness,
		genesis:      genesis,
		events:       events,
		audit:        audit,
		urlValidator: netutil.ValidateRPCURL,
	}
}

// WithURLValidator returns a copy of the service using fn instead of the
// default SSRF-checking URL validator. Use in environments where the RPC URL
// host is trusted (e.g. smoke-test Docker networks).
func (s *LaunchService) WithURLValidator(fn func(string) error) *LaunchService {
	cp := *s
	cp.urlValidator = fn
	return &cp
}

// CreateLaunchInput carries the parameters for a new chain launch record.
type CreateLaunchInput struct {
	Record     launch.ChainRecord
	LaunchType launch.LaunchType
	Visibility launch.Visibility
	Allowlist  []launch.OperatorAddress
	Committee  launch.Committee
}

// CreateLaunch creates a new Launch in DRAFT status.
func (s *LaunchService) CreateLaunch(ctx context.Context, input CreateLaunchInput) (*launch.Launch, error) {
	al := launch.NewAllowlist(input.Allowlist)
	l, err := launch.New(uuid.New(), input.Record, input.LaunchType, input.Visibility, input.Committee)
	if err != nil {
		return nil, fmt.Errorf("create launch: %w", err)
	}
	l.Allowlist = al

	if err := s.launches.Save(ctx, l); err != nil {
		return nil, fmt.Errorf("create launch: save: %w", err)
	}
	_ = s.writeAudit(ctx, l.ID.String(), domain.LaunchCreated{
		LaunchID:    l.ID,
		ChainID:     l.Record.ChainID,
		LaunchType:  string(l.LaunchType),
		Visibility:  string(l.Visibility),
		LeadAddress: l.Committee.LeadAddress.String(),
	})
	return l, nil
}

// UploadInitialGenesis stores the initial (pre-gentx) genesis file, computes its
// SHA256, and transitions the launch to PUBLISHED once committee quorum is reached.
// For now this method stores the genesis and records the hash; the PUBLISH transition
// is triggered by the ProposalService when the PUBLISH_GENESIS proposal executes.
func (s *LaunchService) UploadInitialGenesis(
	ctx context.Context,
	launchID uuid.UUID,
	genesisData []byte,
	callerAddr string,
) (sha256Hash string, err error) {
	l, err := s.launches.FindByID(ctx, launchID)
	if err != nil {
		return "", fmt.Errorf("upload genesis: %w", err)
	}
	callerOp, err := launch.NewOperatorAddress(callerAddr)
	if err != nil || !l.Committee.HasMember(callerOp) {
		return "", fmt.Errorf("upload genesis: caller is not a committee member: %w", ports.ErrForbidden)
	}
	if l.Status != launch.StatusDraft {
		return "", fmt.Errorf("upload genesis: launch must be in DRAFT status, current: %s", l.Status)
	}

	if err := validateGenesisStructure(genesisData, l.Record.ChainID); err != nil {
		return "", fmt.Errorf("upload genesis: validation: %w", err)
	}

	hash := sha256Hex(genesisData)

	if err := s.genesis.SaveInitial(ctx, launchID.String(), genesisData); err != nil {
		return "", fmt.Errorf("upload genesis: store: %w", err)
	}

	l.InitialGenesisSHA256 = hash
	if err := s.launches.Save(ctx, l); err != nil {
		return "", fmt.Errorf("upload genesis: save launch: %w", err)
	}
	_ = s.writeAudit(ctx, launchID.String(), domain.InitialGenesisUploaded{LaunchID: launchID, GenesisHash: hash})
	return hash, nil
}

// UploadFinalGenesis stores the coordinator-assembled final genesis file and
// validates its structure. The PUBLISH_GENESIS proposal is raised separately by
// the coordinator; this endpoint just accepts and validates the file.
func (s *LaunchService) UploadFinalGenesis(
	ctx context.Context,
	launchID uuid.UUID,
	genesisData []byte,
	callerAddr string,
) (sha256Hash string, err error) {
	l, err := s.launches.FindByID(ctx, launchID)
	if err != nil {
		return "", fmt.Errorf("upload final genesis: %w", err)
	}
	callerOp, err := launch.NewOperatorAddress(callerAddr)
	if err != nil || !l.Committee.HasMember(callerOp) {
		return "", fmt.Errorf("upload final genesis: caller is not a committee member: %w", ports.ErrForbidden)
	}
	if l.Status != launch.StatusWindowClosed {
		return "", fmt.Errorf("upload final genesis: launch must be in WINDOW_CLOSED status, current: %s", l.Status)
	}

	if err := validateGenesisStructure(genesisData, l.Record.ChainID); err != nil {
		return "", fmt.Errorf("upload final genesis: validation: %w", err)
	}

	approved, err := s.joinRequests.FindApprovedByLaunch(ctx, launchID)
	if err != nil {
		return "", fmt.Errorf("upload final genesis: fetch approved validators: %w", err)
	}
	genesisTime, err := validateFinalGenesis(genesisData, approved)
	if err != nil {
		return "", fmt.Errorf("upload final genesis: structural check: %w", err)
	}

	// Sync the authoritative genesis time from the file into the launch record so
	// the dashboard always reflects the real chain start time, regardless of what
	// was (or wasn't) set via PATCH /launch/{id}.
	l.Record.GenesisTime = &genesisTime

	hash := sha256Hex(genesisData)

	if err := s.genesis.SaveFinal(ctx, launchID.String(), genesisData); err != nil {
		return "", fmt.Errorf("upload final genesis: store: %w", err)
	}

	l.FinalGenesisSHA256 = hash
	if err := s.launches.Save(ctx, l); err != nil {
		return "", fmt.Errorf("upload final genesis: save launch: %w", err)
	}
	_ = s.writeAudit(ctx, launchID.String(), domain.FinalGenesisUploaded{LaunchID: launchID, GenesisHash: hash})
	return hash, nil
}

// UploadInitialGenesisRef registers an external URL as the source of the initial
// genesis file (Option A / attestor mode). The caller provides the URL and the
// expected SHA-256 hex digest; no bytes are stored on this server.
//
// Structural validation (chain_id, JSON format) is skipped because the file
// bytes are not available; the hash is the integrity guarantee.
func (s *LaunchService) UploadInitialGenesisRef(ctx context.Context, launchID uuid.UUID, genesisURL, sha256Hash, callerAddr string) error {
	if err := s.uploadGenesisRef(ctx, "upload initial genesis ref", launch.StatusDraft, launchID, genesisURL, sha256Hash, callerAddr,
		s.genesis.SaveInitialRef,
		func(l *launch.Launch, hash string) { l.InitialGenesisSHA256 = hash },
	); err != nil {
		return err
	}
	_ = s.writeAudit(ctx, launchID.String(), domain.InitialGenesisUploaded{LaunchID: launchID, GenesisHash: sha256Hash})
	return nil
}

// UploadFinalGenesisRef registers an external URL as the source of the final
// genesis file (Option A / attestor mode). The caller provides the URL, the
// expected SHA-256 hex digest, and the attested genesis time; no bytes are
// stored on this server.
//
// genesisTime must be in the future — the same constraint applied in host mode.
// The time is synced into the launch record so the dashboard countdown is accurate.
func (s *LaunchService) UploadFinalGenesisRef(
	ctx context.Context, launchID uuid.UUID, genesisURL, sha256Hash string, genesisTime time.Time, callerAddr string,
) error {
	if genesisTime.IsZero() {
		return fmt.Errorf("upload final genesis ref: genesis_time is required: %w", ports.ErrBadRequest)
	}
	if !genesisTime.After(time.Now().UTC()) {
		return fmt.Errorf("upload final genesis ref: genesis_time %s is not in the future: %w",
			genesisTime.Format(time.RFC3339), ports.ErrBadRequest)
	}
	if err := s.uploadGenesisRef(ctx, "upload final genesis ref", launch.StatusWindowClosed, launchID, genesisURL, sha256Hash, callerAddr,
		s.genesis.SaveFinalRef,
		func(l *launch.Launch, hash string) {
			l.FinalGenesisSHA256 = hash
			l.Record.GenesisTime = &genesisTime
		},
	); err != nil {
		return err
	}
	_ = s.writeAudit(ctx, launchID.String(), domain.FinalGenesisUploaded{LaunchID: launchID, GenesisHash: sha256Hash})
	return nil
}

func (s *LaunchService) uploadGenesisRef(
	ctx context.Context,
	op string,
	requiredStatus launch.Status,
	launchID uuid.UUID,
	genesisURL, sha256Hash, callerAddr string,
	saveFn func(ctx context.Context, id, url, hash string) error,
	setHashFn func(l *launch.Launch, hash string),
) error {
	l, err := s.launches.FindByID(ctx, launchID)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	callerOp, err := launch.NewOperatorAddress(callerAddr)
	if err != nil || !l.Committee.HasMember(callerOp) {
		return fmt.Errorf("%s: caller is not a committee member: %w", op, ports.ErrForbidden)
	}
	if l.Status != requiredStatus {
		return fmt.Errorf("%s: launch must be in %s status, current: %s", op, requiredStatus, l.Status)
	}
	if err := s.urlValidator(genesisURL); err != nil {
		return fmt.Errorf("%s: invalid url: %w: %w", op, err, ports.ErrBadRequest)
	}
	if !isValidSHA256Hex(sha256Hash) {
		return fmt.Errorf("%s: sha256 must be a 64-character lowercase hex string: %w", op, ports.ErrBadRequest)
	}
	if err := saveFn(ctx, launchID.String(), genesisURL, sha256Hash); err != nil {
		return fmt.Errorf("%s: store: %w", op, err)
	}
	setHashFn(l, sha256Hash)
	if err := s.launches.Save(ctx, l); err != nil {
		return fmt.Errorf("%s: save launch: %w", op, err)
	}
	return nil
}

// sha256HexLen is the number of hex characters in a SHA-256 digest.
const sha256HexLen = 64

// isValidSHA256Hex reports whether s is a 64-character lowercase hexadecimal string
// (the canonical encoding of a SHA-256 digest).
func isValidSHA256Hex(s string) bool {
	if len(s) != sha256HexLen {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// validateFinalGenesis performs structural checks on the coordinator-assembled genesis
// file beyond what validateGenesisStructure already covers. Specifically:
//  1. genesis_time is set and is in the future.
//  2. Every approved validator's consensus pubkey appears exactly once in gen_txs.
//  3. No unapproved gentxs are present (len(gen_txs) == len(approved)).
//
// Returns the validated genesis_time so the caller can sync it into the launch record.
func validateFinalGenesis(data []byte, approved []*joinrequest.JoinRequest) (time.Time, error) {
	var g struct {
		GenesisTime time.Time `json:"genesis_time"`
		AppState    struct {
			Genutil struct {
				GenTxs []json.RawMessage `json:"gen_txs"`
			} `json:"genutil"`
		} `json:"app_state"`
	}
	if err := json.Unmarshal(data, &g); err != nil {
		// Already validated as valid JSON; unexpected.
		return time.Time{}, fmt.Errorf("genesis file is not valid JSON: %w", err)
	}

	if g.GenesisTime.IsZero() {
		return time.Time{}, fmt.Errorf("genesis_time is not set")
	}
	if !g.GenesisTime.After(time.Now().UTC()) {
		return time.Time{}, fmt.Errorf("genesis_time %s is not in the future", g.GenesisTime.Format(time.RFC3339))
	}

	if len(g.AppState.Genutil.GenTxs) != len(approved) {
		return time.Time{}, fmt.Errorf("genesis has %d gen_txs but %d validators are approved",
			len(g.AppState.Genutil.GenTxs), len(approved))
	}

	// Build a set of consensus pubkeys from gen_txs.
	genTxPubKeys := make(map[string]struct{}, len(g.AppState.Genutil.GenTxs))
	for i, rawTx := range g.AppState.Genutil.GenTxs {
		key, err := extractGenTxPubKey(rawTx)
		if err != nil {
			return time.Time{}, fmt.Errorf("gen_txs[%d]: %w", i, err)
		}
		if _, dup := genTxPubKeys[key]; dup {
			return time.Time{}, fmt.Errorf("gen_txs[%d]: duplicate consensus pubkey %q", i, key)
		}
		genTxPubKeys[key] = struct{}{}
	}

	// Verify every approved validator appears in gen_txs.
	for _, jr := range approved {
		if _, ok := genTxPubKeys[jr.ConsensusPubKey]; !ok {
			return time.Time{}, fmt.Errorf("approved validator %s (pubkey %q) is missing from gen_txs",
				jr.OperatorAddress, jr.ConsensusPubKey)
		}
	}

	return g.GenesisTime, nil
}

// extractGenTxPubKey parses the consensus pubkey from a single gentx JSON object.
// Expected structure: body.messages[0].pubkey.key
func extractGenTxPubKey(genTx json.RawMessage) (string, error) {
	var tx struct {
		Body struct {
			Messages []struct {
				PubKey struct {
					Key string `json:"key"`
				} `json:"pubkey"`
			} `json:"messages"`
		} `json:"body"`
	}
	if err := json.Unmarshal(genTx, &tx); err != nil {
		return "", fmt.Errorf("parse gentx: %w", err)
	}
	if len(tx.Body.Messages) == 0 {
		return "", fmt.Errorf("gentx has no messages")
	}
	key := tx.Body.Messages[0].PubKey.Key
	if key == "" {
		return "", fmt.Errorf("gentx message has no pubkey.key field")
	}
	return key, nil
}

// PatchLaunchInput carries the mutable fields for a PATCH /launch/:id call.
// Only non-nil fields are applied.
// MonitorRPCURL may be set on launches in any status; all other fields are
// restricted to DRAFT launches only.
type PatchLaunchInput struct {
	ChainName         *string
	BinaryVersion     *string
	BinarySHA256      *string
	RepoURL           *string
	RepoCommit        *string
	GenesisTime       *time.Time
	MinValidatorCount *int
	Visibility        *launch.Visibility
	Allowlist         []launch.OperatorAddress // nil = no change; empty slice = clear
	MonitorRPCURL     *string                  // settable in any status
}

// PatchLaunch applies a partial update to mutable fields of a launch.
// MonitorRPCURL may be updated at any status. All other fields require DRAFT status.
// The caller must be a committee member.
func (s *LaunchService) PatchLaunch(
	ctx context.Context, launchID uuid.UUID, input PatchLaunchInput, callerAddr string,
) (*launch.Launch, error) {
	l, err := s.launches.FindByID(ctx, launchID)
	if err != nil {
		return nil, err
	}
	callerOp, err := launch.NewOperatorAddress(callerAddr)
	if err != nil || !l.Committee.HasMember(callerOp) {
		return nil, fmt.Errorf("patch launch: caller is not a committee member: %w", ports.ErrForbidden)
	}

	// MonitorRPCURL is status-independent — set it before the DRAFT gate.
	if input.MonitorRPCURL != nil {
		if err := s.urlValidator(*input.MonitorRPCURL); err != nil {
			return nil, fmt.Errorf("patch launch: monitor_rpc_url: %w: %w", err, ports.ErrBadRequest)
		}
		l.MonitorRPCURL = *input.MonitorRPCURL
	}

	if hasDraftFields(input) {
		if l.Status != launch.StatusDraft {
			return nil, fmt.Errorf("patch launch: only DRAFT launches may have chain fields updated: %w", ports.ErrForbidden)
		}
		applyDraftFields(l, input)
	}

	if err := s.launches.Save(ctx, l); err != nil {
		return nil, fmt.Errorf("patch launch: save: %w", err)
	}
	return l, nil
}

// hasDraftFields reports whether the input contains any fields that require DRAFT status.
func hasDraftFields(input PatchLaunchInput) bool {
	return input.ChainName != nil || input.BinaryVersion != nil ||
		input.BinarySHA256 != nil || input.RepoURL != nil || input.RepoCommit != nil ||
		input.GenesisTime != nil || input.MinValidatorCount != nil ||
		input.Visibility != nil || input.Allowlist != nil
}

// applyDraftFields writes all draft-only optional fields from input onto l.
// Callers must verify l.Status == StatusDraft before calling.
func applyDraftFields(l *launch.Launch, input PatchLaunchInput) {
	if input.ChainName != nil {
		l.Record.ChainName = *input.ChainName
	}
	if input.BinaryVersion != nil {
		l.Record.BinaryVersion = *input.BinaryVersion
	}
	if input.BinarySHA256 != nil {
		l.Record.BinarySHA256 = *input.BinarySHA256
	}
	if input.RepoURL != nil {
		l.Record.RepoURL = *input.RepoURL
	}
	if input.RepoCommit != nil {
		l.Record.RepoCommit = *input.RepoCommit
	}
	if input.GenesisTime != nil {
		l.Record.GenesisTime = input.GenesisTime
	}
	if input.MinValidatorCount != nil {
		l.Record.MinValidatorCount = *input.MinValidatorCount
	}
	if input.Visibility != nil {
		l.Visibility = *input.Visibility
	}
	if input.Allowlist != nil {
		l.Allowlist = launch.NewAllowlist(input.Allowlist)
	}
}

// SetCommittee replaces the committee on a DRAFT launch.
// Only the current lead coordinator may call this.
func (s *LaunchService) SetCommittee(ctx context.Context, launchID uuid.UUID, committee launch.Committee, callerAddr string) error {
	l, err := s.launches.FindByID(ctx, launchID)
	if err != nil {
		return err
	}
	if l.Status != launch.StatusDraft {
		return fmt.Errorf("set committee: launch must be in DRAFT status, current: %s: %w", l.Status, ports.ErrForbidden)
	}
	if l.Committee.LeadAddress.String() != callerAddr {
		return fmt.Errorf("set committee: only the lead coordinator may replace the committee: %w", ports.ErrForbidden)
	}
	if committee.ThresholdM < 1 || committee.ThresholdM > committee.TotalN {
		return fmt.Errorf("set committee: threshold %d out of range [1, %d]", committee.ThresholdM, committee.TotalN)
	}
	if len(committee.Members) != committee.TotalN {
		return fmt.Errorf("set committee: %d members provided but total_n is %d", len(committee.Members), committee.TotalN)
	}
	l.Committee = committee
	if err := s.launches.Save(ctx, l); err != nil {
		return fmt.Errorf("set committee: save: %w", err)
	}
	return nil
}

// IsCoordinator reports whether callerAddr is a committee member of the given launch.
func (s *LaunchService) IsCoordinator(ctx context.Context, launchID uuid.UUID, callerAddr string) (bool, error) {
	l, err := s.launches.FindByID(ctx, launchID)
	if err != nil {
		return false, err
	}
	addr, err := launch.NewOperatorAddress(callerAddr)
	if err != nil {
		return false, nil
	}
	return l.Committee.HasMember(addr), nil
}

// GetCommittee returns the committee for a launch, gated by visibility.
func (s *LaunchService) GetCommittee(ctx context.Context, launchID uuid.UUID, callerAddr string) (launch.Committee, error) {
	l, err := s.GetLaunch(ctx, launchID, callerAddr)
	if err != nil {
		return launch.Committee{}, err
	}
	return l.Committee, nil
}

// ChainHintOutput is the minimal public metadata returned by GetChainHint.
// It is intentionally small: enough for a wallet to register the chain,
// but reveals nothing about who is participating.
type ChainHintOutput struct {
	ChainID      string
	ChainName    string
	Bech32Prefix string
	Denom        string
}

// GetChainHint returns the chain metadata needed to register the network with a
// wallet extension. It bypasses visibility — even ALLOWLIST launches expose
// this data so validators can derive their address before being added to the list.
// Returns ErrNotFound for unknown IDs.
func (s *LaunchService) GetChainHint(ctx context.Context, id uuid.UUID) (ChainHintOutput, error) {
	l, err := s.launches.FindByID(ctx, id)
	if err != nil {
		return ChainHintOutput{}, err
	}
	return ChainHintOutput{
		ChainID:      l.Record.ChainID,
		ChainName:    l.Record.ChainName,
		Bech32Prefix: l.Record.Bech32Prefix,
		Denom:        l.Record.Denom,
	}, nil
}

// GetLaunch returns a single launch by ID, gated by visibility.
func (s *LaunchService) GetLaunch(ctx context.Context, id uuid.UUID, callerAddr string) (*launch.Launch, error) {
	l, err := s.launches.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// ALLOWLIST chains are invisible to callers not on the list — return ErrNotFound,
	// not ErrForbidden, so as not to reveal the chain's existence.
	if !l.IsVisibleTo(callerAddr) {
		return nil, ports.ErrNotFound
	}
	return l, nil
}

// ListLaunches returns launches visible to the caller with pagination.
func (s *LaunchService) ListLaunches(ctx context.Context, callerAddr string, page, perPage int) ([]*launch.Launch, int, error) {
	return s.launches.FindAll(ctx, callerAddr, page, perPage)
}

// OpenWindow transitions a launch to WINDOW_OPEN.
// Accepts PUBLISHED status directly. If the launch is still in DRAFT and the initial
// genesis hash has already been uploaded, it auto-publishes first (single-coordinator
// shortcut — equivalent to raising and immediately executing a PUBLISH_CHAIN_RECORD
// proposal). Any other status returns ErrBadRequest.
// Only a committee member may call this.
func (s *LaunchService) OpenWindow(ctx context.Context, launchID uuid.UUID, callerAddr string) error {
	l, err := s.launches.FindByID(ctx, launchID)
	if err != nil {
		return err
	}
	callerOp, err := launch.NewOperatorAddress(callerAddr)
	if err != nil || !l.Committee.HasMember(callerOp) {
		return fmt.Errorf("open window: caller is not a committee member: %w", ports.ErrForbidden)
	}

	if l.Status == launch.StatusDraft {
		if l.InitialGenesisSHA256 == "" {
			return fmt.Errorf("open window: initial genesis must be uploaded before opening the application window: %w", ports.ErrBadRequest)
		}
		if err := l.Publish(l.InitialGenesisSHA256); err != nil {
			return fmt.Errorf("open window: auto-publish: %w: %w", err, ports.ErrBadRequest)
		}
	}

	if err := l.OpenWindow(); err != nil {
		return fmt.Errorf("%w: %w", err, ports.ErrBadRequest)
	}
	if err := s.launches.Save(ctx, l); err != nil {
		return err
	}
	_ = s.writeAudit(ctx, l.ID.String(), domain.WindowOpened{LaunchID: l.ID})
	return nil
}

// GetDashboard returns the readiness dashboard state for a launch.
// The readiness counts are assembled by the ReadinessService; this method returns
// the launch-level fields.
func (s *LaunchService) GetDashboard(ctx context.Context, launchID uuid.UUID, callerAddr string) (*LaunchDashboard, error) {
	l, err := s.GetLaunch(ctx, launchID, callerAddr)
	if err != nil {
		return nil, err
	}

	var countdown *time.Duration
	if l.Record.GenesisTime != nil {
		d := time.Until(*l.Record.GenesisTime)
		countdown = &d
	}

	return &LaunchDashboard{
		LaunchID:             l.ID,
		ChainID:              l.Record.ChainID,
		Status:               l.Status,
		GenesisTime:          l.Record.GenesisTime,
		Countdown:            countdown,
		FinalGenesisSHA256:   l.FinalGenesisSHA256,
		InitialGenesisSHA256: l.InitialGenesisSHA256,
	}, nil
}

// LaunchDashboard is the read model for the launch dashboard endpoint.
type LaunchDashboard struct {
	LaunchID             uuid.UUID
	ChainID              string
	Status               launch.Status
	GenesisTime          *time.Time
	Countdown            *time.Duration
	FinalGenesisSHA256   string
	InitialGenesisSHA256 string
	// ValidatorReadiness is populated by ReadinessService and merged in the HTTP handler.
}

// validateGenesisStructure checks that genesis data is valid JSON with the correct chain_id.
// This is structural only — no binary is invoked.
func validateGenesisStructure(data []byte, expectedChainID string) error {
	var g struct {
		ChainID string `json:"chain_id"`
	}
	if err := json.Unmarshal(data, &g); err != nil {
		return fmt.Errorf("genesis file is not valid JSON: %w", err)
	}
	if g.ChainID == "" {
		return fmt.Errorf("genesis file missing chain_id field")
	}
	if g.ChainID != expectedChainID {
		return fmt.Errorf("genesis chain_id %q does not match expected %q", g.ChainID, expectedChainID)
	}
	return nil
}

// CancelLaunch transitions a launch to CANCELED. Only the committee lead may call
// this. Readiness confirmations are invalidated when canceling from GENESIS_READY.
func (s *LaunchService) CancelLaunch(ctx context.Context, launchID uuid.UUID, callerAddr string) error {
	l, err := s.launches.FindByID(ctx, launchID)
	if err != nil {
		return err
	}
	if l.Committee.LeadAddress.String() != callerAddr {
		return fmt.Errorf("cancel launch: only the committee lead may cancel: %w", ports.ErrForbidden)
	}
	prevStatus := l.Status
	if err := l.Cancel(); err != nil {
		return fmt.Errorf("%w: %w", err, ports.ErrConflict)
	}
	if prevStatus == launch.StatusGenesisReady {
		if err := s.readiness.InvalidateByLaunch(ctx, l.ID); err != nil {
			return fmt.Errorf("cancel launch: invalidate readiness: %w", err)
		}
	}
	if err := s.launches.Save(ctx, l); err != nil {
		return fmt.Errorf("cancel launch: save: %w", err)
	}
	ev := domain.LaunchCancelled{LaunchID: l.ID}
	s.events.Publish(ev)
	_ = s.writeAudit(ctx, l.ID.String(), ev)
	return nil
}

func (s *LaunchService) writeAudit(ctx context.Context, launchID string, ev domain.DomainEvent) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return s.audit.Append(ctx, ports.AuditEvent{
		LaunchID:   launchID,
		EventName:  ev.EventName(),
		OccurredAt: ev.OccurredAt(),
		Payload:    payload,
	})
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
