// Package launch contains the Launch aggregate and its owned entities.
package launch

import (
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
)

// Status is the launch lifecycle state.
type Status string

const (
	StatusDraft        Status = "DRAFT"
	StatusPublished    Status = "PUBLISHED"
	StatusWindowOpen   Status = "WINDOW_OPEN"
	StatusWindowClosed Status = "WINDOW_CLOSED"
	StatusGenesisReady Status = "GENESIS_READY"
	StatusLaunched     Status = "LAUNCHED"
	StatusCancelled    Status = "CANCELED"
)

// LaunchType classifies the launch for validation rule selection.
type LaunchType string

const (
	LaunchTypeTestnet             LaunchType = "TESTNET"
	LaunchTypeIncentivizedTestnet LaunchType = "INCENTIVIZED_TESTNET"
	LaunchTypeMainnet             LaunchType = "MAINNET"
	LaunchTypePermissioned        LaunchType = "PERMISSIONED"
)

// bftSafetyThreshold is the exact 1/3 BFT safety threshold (33.333...%).
// A single entity holding ≥ this share of voting power can halt the chain.
const (
	bftSafetyThreshold = 100.0 / 3.0
	pctScaleFactor     = 100.0 // multiplier to express a ratio as a percentage
)

// Visibility controls who can discover the launch.
type Visibility string

const (
	VisibilityPublic    Visibility = "PUBLIC"
	VisibilityAllowlist Visibility = "ALLOWLIST"
)

// CommitteeMember is an individual coordinator in the M-of-N committee.
type CommitteeMember struct {
	Address   OperatorAddress
	Moniker   string
	PubKeyB64 string // base64-encoded secp256k1 compressed public key (33 bytes)
}

// Committee is the M-of-N coordinator group that governs a launch.
// It is owned by the Launch aggregate and does not have an independent lifecycle.
type Committee struct {
	ID          uuid.UUID
	Members     []CommitteeMember
	ThresholdM  int
	TotalN      int
	LeadAddress OperatorAddress
	// CreationSignature is the lead coordinator's secp256k1 signature over the canonical
	// JSON of this committee record. It is stored for the audit log — it proves the
	// declared committee config was intentional. Verification is the responsibility of
	// the CommitteeService in the application layer when the committee is created.
	CreationSignature Signature
	CreatedAt         time.Time
}

// HasMember reports whether the given address is a committee member.
func (c Committee) HasMember(addr OperatorAddress) bool {
	for _, m := range c.Members {
		if m.Address.Equal(addr) {
			return true
		}
	}
	return false
}

// ChainRecord holds the immutable and mutable parameters declared by the coordinator.
type ChainRecord struct {
	ChainID                 string
	ChainName               string
	Bech32Prefix            string
	BinaryName              string
	BinaryVersion           string
	BinarySHA256            string
	RepoURL                 string
	RepoCommit              string
	GenesisTime             *time.Time // nullable until set
	Denom                   string
	MinSelfDelegation       string // bigint string in base denom
	MaxCommissionRate       CommissionRate
	MaxCommissionChangeRate CommissionRate
	GentxDeadline           time.Time
	ApplicationWindowOpen   time.Time
	MinValidatorCount       int
}

// Launch is the aggregate root for an entire chain launch lifecycle.
type Launch struct {
	ID         uuid.UUID
	Record     ChainRecord
	LaunchType LaunchType
	Visibility Visibility
	Allowlist  Allowlist
	Status     Status
	Committee  Committee

	InitialGenesisSHA256 string
	FinalGenesisSHA256   string // empty until GENESIS_READY

	// AllocationFiles holds the curated allocation files (≤1 per type). Each is
	// uploaded by a committee member and approved independently by an
	// APPROVE_ALLOCATION_FILE committee proposal. Ordered by type.
	AllocationFiles []AllocationFile

	// MonitorRPCURL is the CometBFT RPC endpoint polled by the block monitoring job.
	// Set by the coordinator via PATCH /launch/:id; empty disables monitoring.
	MonitorRPCURL string

	CreatedAt time.Time
	UpdatedAt time.Time

	// Version is the optimistic-locking counter managed exclusively by the repository.
	// It must not be modified by domain or application code.
	Version int

	// approvedVotingPower tracks the sum of self-delegations of approved validators
	// in base denom (as int64 for calculation purposes). Maintained by the application layer.
	approvedVotingPower map[string]int64 // operator_address → self_delegation

	events []any // accumulated domain events
}

// New creates a new Launch in DRAFT status.
func New(id uuid.UUID, record ChainRecord, lt LaunchType, vis Visibility, committee Committee) (*Launch, error) {
	if err := validateChainRecord(record); err != nil {
		return nil, fmt.Errorf("launch: invalid chain record: %w", err)
	}
	if committee.ThresholdM < 1 || committee.ThresholdM > committee.TotalN {
		return nil, fmt.Errorf("launch: committee threshold %d out of range [1, %d]", committee.ThresholdM, committee.TotalN)
	}
	if len(committee.Members) != committee.TotalN {
		return nil, fmt.Errorf("launch: committee has %d members but TotalN is %d", len(committee.Members), committee.TotalN)
	}
	now := time.Now().UTC()
	return &Launch{
		ID:                  id,
		Record:              record,
		LaunchType:          lt,
		Visibility:          vis,
		Status:              StatusDraft,
		Committee:           committee,
		CreatedAt:           now,
		UpdatedAt:           now,
		approvedVotingPower: make(map[string]int64),
	}, nil
}

// Publish transitions a DRAFT launch to PUBLISHED. Requires the initial genesis
// SHA256 to have been set.
func (l *Launch) Publish(initialGenesisSHA256 string) error {
	if l.Status != StatusDraft {
		return fmt.Errorf("launch: can only publish from DRAFT, current status: %s", l.Status)
	}
	if initialGenesisSHA256 == "" {
		return fmt.Errorf("launch: initial genesis hash must be set before publishing")
	}
	l.InitialGenesisSHA256 = initialGenesisSHA256
	l.Status = StatusPublished
	l.touch()
	return nil
}

// OpenWindow transitions a PUBLISHED launch to WINDOW_OPEN.
func (l *Launch) OpenWindow() error {
	if l.Status != StatusPublished {
		return fmt.Errorf("launch: can only open window from PUBLISHED, current status: %s", l.Status)
	}
	l.Status = StatusWindowOpen
	l.touch()
	return nil
}

// CloseWindow transitions a WINDOW_OPEN launch to WINDOW_CLOSED.
// Enforces min_validator_count precondition.
func (l *Launch) CloseWindow(approvedCount int) error {
	if l.Status != StatusWindowOpen {
		return fmt.Errorf("launch: can only close window from WINDOW_OPEN, current status: %s", l.Status)
	}
	if approvedCount < l.Record.MinValidatorCount {
		return fmt.Errorf("launch: need at least %d approved validators to close the window, have %d",
			l.Record.MinValidatorCount, approvedCount)
	}
	// Enforce no single entity holds ≥33% voting power (precondition check only;
	// the running warning is checked on each approval — this is a final gate).
	if dominant, pct := l.dominantVotingPowerPct(); pct >= bftSafetyThreshold {
		return fmt.Errorf("launch: operator %s holds %.1f%% of committed voting power (≥1/3 not allowed at window close)",
			dominant, pct)
	}
	l.Status = StatusWindowClosed
	l.touch()
	return nil
}

// PublishGenesis transitions a WINDOW_CLOSED launch to GENESIS_READY.
func (l *Launch) PublishGenesis(finalGenesisSHA256 string) error {
	if l.Status != StatusWindowClosed {
		return fmt.Errorf("launch: can only publish genesis from WINDOW_CLOSED, current status: %s", l.Status)
	}
	if finalGenesisSHA256 == "" {
		return fmt.Errorf("launch: final genesis hash must not be empty")
	}
	l.FinalGenesisSHA256 = finalGenesisSHA256
	l.Status = StatusGenesisReady
	l.touch()
	return nil
}

// Cancel transitions a launch to CANCELED from any non-terminal status.
// Returns an error if the launch is already CANCELED or LAUNCHED.
func (l *Launch) Cancel() error {
	if l.Status == StatusCancelled {
		return fmt.Errorf("launch: already canceled")
	}
	if l.Status == StatusLaunched {
		return fmt.Errorf("launch: cannot cancel a launched chain")
	}
	l.Status = StatusCancelled
	l.touch()
	return nil
}

// ReopenForRevision transitions a GENESIS_READY launch back to WINDOW_CLOSED and clears
// FinalGenesisSHA256 so the coordinator can re-upload a corrected genesis file.
// Returns an error if the current status is not GENESIS_READY.
func (l *Launch) ReopenForRevision() error {
	if l.Status != StatusGenesisReady {
		return fmt.Errorf("launch: can only reopen for revision from GENESIS_READY, current status: %s", l.Status)
	}
	l.FinalGenesisSHA256 = ""
	l.Status = StatusWindowClosed
	l.touch()
	return nil
}

// MarkLaunched transitions a GENESIS_READY launch to LAUNCHED.
func (l *Launch) MarkLaunched() error {
	if l.Status != StatusGenesisReady {
		return fmt.Errorf("launch: can only mark launched from GENESIS_READY, current status: %s", l.Status)
	}
	l.Status = StatusLaunched
	l.touch()
	return nil
}

// CanValidatorApply reports whether the given operator address may submit a join request.
func (l *Launch) CanValidatorApply(addr OperatorAddress) error {
	if l.Status != StatusWindowOpen {
		return fmt.Errorf("application window is not open (status: %s)", l.Status)
	}
	if l.Visibility == VisibilityAllowlist && !l.Allowlist.Contains(addr) {
		return fmt.Errorf("operator address not on allowlist")
	}
	return nil
}

// IsVisibleTo reports whether the launch is visible to the given operator address.
// An empty address represents an unauthenticated caller.
func (l *Launch) IsVisibleTo(addr string) bool {
	if l.Visibility == VisibilityPublic {
		return true
	}
	if addr == "" {
		return false
	}
	validated, err := NewOperatorAddress(addr)
	if err != nil {
		return false
	}
	return l.Allowlist.Contains(validated)
}

// ReplaceCommitteeMember swaps the committee member at oldAddr with newMember.
// Returns an error if oldAddr is not found. No status guard — committee rotation
// can occur at any lifecycle stage via proposal.
func (l *Launch) ReplaceCommitteeMember(oldAddr OperatorAddress, newMember CommitteeMember) error {
	for i, m := range l.Committee.Members {
		if m.Address.Equal(oldAddr) {
			l.Committee.Members[i] = newMember
			if l.Committee.LeadAddress.Equal(oldAddr) {
				l.Committee.LeadAddress = newMember.Address
			}
			l.touch()
			return nil
		}
	}
	return fmt.Errorf("launch: committee member %s not found", oldAddr)
}

// ExpandCommittee appends newMember to the committee and sets the new threshold.
// Returns an error if newMember's address is already a member, if newThresholdM is
// not in [1, newN-1] (liveness guard: M must be strictly less than N so the committee
// can still act when one member is absent).
func (l *Launch) ExpandCommittee(newMember CommitteeMember, newThresholdM int) error {
	for _, m := range l.Committee.Members {
		if m.Address.Equal(newMember.Address) {
			return fmt.Errorf("launch: committee member %s is already in the committee", newMember.Address)
		}
	}
	newN := len(l.Committee.Members) + 1
	if newThresholdM < 1 {
		return fmt.Errorf("launch: threshold must be at least 1")
	}
	if newThresholdM >= newN {
		return fmt.Errorf("launch: threshold %d must be less than new committee size %d (liveness guard: M < N required)", newThresholdM, newN)
	}
	l.Committee.Members = append(l.Committee.Members, newMember)
	l.Committee.TotalN = newN
	l.Committee.ThresholdM = newThresholdM
	l.touch()
	return nil
}

// ShrinkCommittee removes the member at removeAddr from the committee and sets the new
// threshold. Returns an error if removeAddr is not found, if the resulting committee
// would have fewer than 1 member, or if newThresholdM is not in [1, newN-1] (liveness
// guard). If the removed member was the committee lead, the lead is transferred to the
// first remaining member.
func (l *Launch) ShrinkCommittee(removeAddr OperatorAddress, newThresholdM int) error {
	idx := -1
	for i, m := range l.Committee.Members {
		if m.Address.Equal(removeAddr) {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("launch: committee member %s not found", removeAddr)
	}
	newN := len(l.Committee.Members) - 1
	if newN < 1 {
		return fmt.Errorf("launch: cannot shrink a committee below 1 member")
	}
	if newThresholdM < 1 {
		return fmt.Errorf("launch: threshold must be at least 1")
	}
	if newThresholdM >= newN {
		return fmt.Errorf("launch: threshold %d must be less than new committee size %d (liveness guard: M < N required)", newThresholdM, newN)
	}
	l.Committee.Members = append(l.Committee.Members[:idx], l.Committee.Members[idx+1:]...)
	l.Committee.TotalN = newN
	l.Committee.ThresholdM = newThresholdM
	if l.Committee.LeadAddress.Equal(removeAddr) {
		l.Committee.LeadAddress = l.Committee.Members[0].Address
	}
	l.touch()
	return nil
}

// allocationLocked reports whether the allocation-file set is frozen: once the
// genesis is published (GENESIS_READY) or the launch is terminal, file changes
// can no longer affect the published genesis, so upload/approve are rejected.
func (l *Launch) allocationLocked() bool {
	return l.Status == StatusGenesisReady || l.Status == StatusLaunched || l.Status == StatusCancelled
}

// AllocationFileOf returns the allocation file of the given type, if present.
func (l *Launch) AllocationFileOf(t AllocationType) (AllocationFile, bool) {
	for _, f := range l.AllocationFiles {
		if f.Type == t {
			return f, true
		}
	}
	return AllocationFile{}, false
}

// UploadAllocationFile records (or replaces) the allocation file of type t with the
// given content hash, landing it in PENDING status. A re-upload with a different hash
// invalidates any prior approval (status resets to PENDING, ApprovedByProposal cleared).
// Returns an error for an unknown type or once the allocation set is locked.
func (l *Launch) UploadAllocationFile(t AllocationType, sha256 string) error {
	if l.allocationLocked() {
		return fmt.Errorf("launch: %s status: %w", l.Status, ErrAllocationLocked)
	}
	if !ValidAllocationType(t) {
		return fmt.Errorf("launch: %q: %w", t, ErrUnknownAllocationType)
	}
	if sha256 == "" {
		return fmt.Errorf("launch: %q: %w", t, ErrAllocationEmptyHash)
	}
	now := time.Now().UTC()
	for i := range l.AllocationFiles {
		if l.AllocationFiles[i].Type == t {
			l.AllocationFiles[i].SHA256 = sha256
			l.AllocationFiles[i].Status = AllocationPending
			l.AllocationFiles[i].ApprovedByProposal = nil
			l.AllocationFiles[i].UploadedAt = now
			l.touch()
			return nil
		}
	}
	l.AllocationFiles = append(l.AllocationFiles, AllocationFile{
		Type:       t,
		SHA256:     sha256,
		Status:     AllocationPending,
		UploadedAt: now,
	})
	l.touch()
	return nil
}

// ApproveAllocationFile marks the file of type t APPROVED, binding the approval to the
// given proposal. The hash must match the file's current hash (a stale approval against
// a hash that has since been re-uploaded is rejected). Returns an error if no file of
// that type exists, the hash is stale, or the allocation set is locked.
func (l *Launch) ApproveAllocationFile(t AllocationType, hash string, proposalID uuid.UUID) error {
	if l.allocationLocked() {
		return fmt.Errorf("launch: %s status: %w", l.Status, ErrAllocationLocked)
	}
	for i := range l.AllocationFiles {
		if l.AllocationFiles[i].Type == t {
			if l.AllocationFiles[i].SHA256 != hash {
				return fmt.Errorf("launch: %q: %w", t, ErrAllocationStaleHash)
			}
			pid := proposalID
			l.AllocationFiles[i].Status = AllocationApproved
			l.AllocationFiles[i].ApprovedByProposal = &pid
			l.touch()
			return nil
		}
	}
	return fmt.Errorf("launch: %q: %w", t, ErrAllocationNotFound)
}

// RejectAllocationFile marks the file of type t REJECTED. The hash must match the file's
// current hash; if it does not, the file has since been re-uploaded and the (stale) veto
// is ignored as a no-op. Returns an error only if no file of that type exists.
func (l *Launch) RejectAllocationFile(t AllocationType, hash string) error {
	for i := range l.AllocationFiles {
		if l.AllocationFiles[i].Type == t {
			if l.AllocationFiles[i].SHA256 != hash {
				return nil // superseded by a re-upload; leave the new file PENDING
			}
			l.AllocationFiles[i].Status = AllocationRejected
			l.AllocationFiles[i].ApprovedByProposal = nil
			l.touch()
			return nil
		}
	}
	return fmt.Errorf("launch: %q: %w", t, ErrAllocationNotFound)
}

// RecordValidatorApproval records the voting power contribution of an approved validator.
// Returns a warning string if any single entity now holds ≥33% voting power.
func (l *Launch) RecordValidatorApproval(operatorAddr OperatorAddress, selfDelegation int64) string {
	l.approvedVotingPower[operatorAddr.String()] = selfDelegation
	dominant, pct := l.dominantVotingPowerPct()
	if pct >= bftSafetyThreshold {
		return fmt.Sprintf("WARNING: operator %s now holds %.1f%% of committed voting power", dominant, pct)
	}
	return ""
}

// RemoveValidatorApproval removes a validator's voting power contribution.
func (l *Launch) RemoveValidatorApproval(operatorAddr OperatorAddress) {
	delete(l.approvedVotingPower, operatorAddr.String())
}

// ApprovedVotingPowerOf returns the self-delegation of an approved validator (0 if not found).
func (l *Launch) ApprovedVotingPowerOf(operatorAddr OperatorAddress) int64 {
	return l.approvedVotingPower[operatorAddr.String()]
}

// InitVotingPower hydrates the in-memory voting power map from persisted data.
// Called exclusively by repository implementations — not for domain or application use.
func (l *Launch) InitVotingPower(powers map[string]int64) {
	l.approvedVotingPower = powers
}

// PopEvents returns and clears the accumulated domain events.
func (l *Launch) PopEvents() []any {
	ev := l.events
	l.events = nil
	return ev
}

func (l *Launch) touch() {
	l.UpdatedAt = time.Now().UTC()
}

// dominantVotingPowerPct returns the operator address and percentage (0–100) of the
// operator with the highest share of total committed voting power.
// Uses big.Int arithmetic to handle chains with large token supplies without overflow.
func (l *Launch) dominantVotingPowerPct() (dominantAddr string, pct float64) {
	total := new(big.Int)
	powers := make(map[string]*big.Int)

	for addr, power := range l.approvedVotingPower {
		p := new(big.Int).SetInt64(power)
		total.Add(total, p)
		if existing, ok := powers[addr]; ok {
			existing.Add(existing, p)
		} else {
			powers[addr] = new(big.Int).Set(p)
		}
	}
	if total.Sign() == 0 {
		return "", 0
	}

	var dominant string
	dominantPower := new(big.Int)
	for addr, power := range powers {
		if power.Cmp(dominantPower) > 0 {
			dominant = addr
			dominantPower.Set(power)
		}
	}

	// pct = dominantPower * 100 / total, as float64
	bigintPct := new(big.Float).Quo(
		new(big.Float).Mul(new(big.Float).SetInt(dominantPower), big.NewFloat(pctScaleFactor)),
		new(big.Float).SetInt(total),
	)
	result, _ := bigintPct.Float64()
	return dominant, result
}

func validateChainRecord(r ChainRecord) error {
	if r.ChainID == "" {
		return fmt.Errorf("chain_id is required")
	}
	if r.Bech32Prefix == "" {
		return fmt.Errorf("bech32_prefix is required")
	}
	if r.BinaryName == "" {
		return fmt.Errorf("binary_name is required")
	}
	if r.Denom == "" {
		return fmt.Errorf("denom is required")
	}
	if r.MinValidatorCount < 1 {
		return fmt.Errorf("min_validator_count must be at least 1")
	}
	if r.GentxDeadline.IsZero() {
		return fmt.Errorf("gentx_deadline is required")
	}
	return nil
}
