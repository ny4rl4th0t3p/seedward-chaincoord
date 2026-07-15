// Package launch contains the Launch aggregate and its owned entities.
package launch

import (
	"errors"
	"fmt"
	"math/big"
	"net/url"
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

// Sentinel errors for the launch state-machine and committee operations. Callers
// (the proposal/launch services and tests) match these with errors.Is to distinguish
// failure kinds and map them to an HTTP status — the authoritative mapping lives in the
// services' mapLaunchDomainErr (state conflicts → 409, bad input → 400, not-found → 404).
var (
	ErrInvalidStatusTransition  = errors.New("operation not allowed from the current launch status")
	ErrGenesisHashRequired      = errors.New("genesis hash must be set")
	ErrInsufficientValidators   = errors.New("not enough approved validators to close the window")
	ErrDominantVotingPower      = errors.New("a single operator holds an unsafe share of voting power")
	ErrCommitteeMemberNotFound  = errors.New("committee member not found")
	ErrCommitteeMemberExists    = errors.New("address is already a committee member")
	ErrInvalidCommitteeChange   = errors.New("invalid committee change")
	ErrWindowNotOpen            = errors.New("application window is not open")
	ErrMembersNotEditable       = errors.New("members list is not editable in the current launch status")
	ErrNotAMember               = errors.New("address is not a member of this launch")
	ErrLeadNotFirstMember       = errors.New("committee lead must be the committee's first member")
	ErrGenesisStale             = errors.New("the final genesis no longer matches the current approved validator set")
	ErrGenesisHashMismatch      = errors.New("proposal genesis hash does not match the uploaded final genesis")
	ErrGenesisPublishInProgress = errors.New("a genesis publication is in progress")
	ErrRehearsalGateUnsatisfied = errors.New("rehearsal gate not satisfied: a current passing rehearsal is required")
	ErrRehearsalGateNoService   = errors.New("rehearsal gate is required but no rehearsal service is configured for this launch")

	// Chain-record field validation (New / ChainRecord.Validate) — invalid input.
	ErrChainIDRequired            = errors.New("chain_id is required")
	ErrBech32PrefixRequired       = errors.New("bech32_prefix is required")
	ErrBinaryNameRequired         = errors.New("binary_name is required")
	ErrDenomRequired              = errors.New("denom is required")
	ErrMinValidatorCountTooLow    = errors.New("min_validator_count must be at least 1")
	ErrGentxDeadlineRequired      = errors.New("gentx_deadline is required")
	ErrCommissionChangeExceedsMax = errors.New("max_commission_change_rate must not exceed max_commission_rate")
	ErrInvalidRepoURL             = errors.New("repo_url must be a valid http(s) URL")
	ErrInvalidBinarySHA256        = errors.New("binary_sha256 must be a 64-character lowercase hex SHA-256 digest")
	ErrInvalidMinSelfDelegation   = errors.New("min_self_delegation must be a non-negative integer in base denom")
	ErrInvalidTotalSupply         = errors.New("total_supply must be a non-negative integer in base denom")

	// Committee construction validation (New).
	ErrCommitteeThresholdRange = errors.New("committee threshold out of range [1, TotalN]")
	ErrCommitteeSizeMismatch   = errors.New("committee member count does not match TotalN")
)

// CommitteeMember is an individual coordinator in the M-of-N committee.
type CommitteeMember struct {
	Address   AccountID
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
	LeadAddress AccountID
	// CreationSignature is the lead coordinator's secp256k1 signature over the canonical
	// JSON of this committee record. It is stored for the audit log — it proves the
	// declared committee config was intentional. Verification is the responsibility of
	// the CommitteeService in the application layer when the committee is created.
	CreationSignature Signature
	CreatedAt         time.Time
}

// HasMember reports whether the given address is a committee member.
func (c Committee) HasMember(addr AccountID) bool {
	for _, m := range c.Members {
		if m.Address.Equal(addr) {
			return true
		}
	}
	return false
}

// ChainRecord holds the immutable and mutable parameters declared by the coordinator.
type ChainRecord struct {
	ChainID           string
	ChainName         string
	Bech32Prefix      string
	BinaryName        string
	BinaryVersion     string
	BinarySHA256      string
	RepoURL           string
	RepoCommit        string
	GenesisTime       *time.Time // nullable until set
	Denom             string
	MinSelfDelegation string // bigint string in base denom
	// TotalSupply is the genesis supply anchor in base denom (bigint string). Optional at
	// creation; required by the rehearsal bridge's genesis.Build. Empty when
	// the launch does not use the bridge.
	TotalSupply             string
	MaxCommissionRate       CommissionRate
	MaxCommissionChangeRate CommissionRate
	GentxDeadline           time.Time
	MinValidatorCount       int
}

// Launch is the aggregate root for an entire chain launch lifecycle.
type Launch struct {
	ID         uuid.UUID
	Record     ChainRecord
	LaunchType LaunchType
	Allowlist  Allowlist
	Status     Status
	Committee  Committee

	InitialGenesisSHA256 string
	FinalGenesisSHA256   string // empty until GENESIS_READY

	// FinalGenesisInputSetHash is the input_set_hash (approved gentxs + allocations + chain params)
	// that the uploaded final genesis was assembled from — bound at upload, re-checked at
	// PUBLISH_GENESIS so a genesis that no longer matches the current approved set cannot be
	// finalized. Empty until a final genesis is uploaded.
	FinalGenesisInputSetHash string

	// AllocationFiles holds the curated allocation files (≤1 per type). Each is
	// uploaded by a committee member and approved independently by an
	// APPROVE_ALLOCATION_FILE committee proposal. Ordered by type.
	AllocationFiles []AllocationFile

	// MonitorRPCURL is the CometBFT RPC endpoint polled by the block monitoring job.
	// Set by the coordinator via PATCH /launch/:id; empty disables monitoring.
	MonitorRPCURL string

	// RehearsalServicePubKey is the base64 Ed25519 public key coordd trusts for this launch's
	// rehearsal result facts. RehearsalEndpoint is the advertised URL of
	// the rehearsal service for this launch. Both are operational config, set by the coordinator
	// via PATCH /launch/:id at any status (like MonitorRPCURL); empty when the bridge is unused.
	RehearsalServicePubKey string
	RehearsalEndpoint      string

	CreatedAt time.Time
	UpdatedAt time.Time

	// Version is the optimistic-locking counter managed exclusively by the repository.
	// It must not be modified by domain or application code.
	Version int

	// approvedVotingPower tracks the sum of self-delegations of approved validators
	// in base denom (as int64 for calculation purposes). Maintained by the application layer.
	// Keyed by AccountID.Hex() (canonical account, HRP-independent) — NOT the display bech32 —
	// so the same account under different prefixes is a single entry.
	approvedVotingPower map[string]int64 // account hex → self_delegation
}

// New creates a new Launch in DRAFT status. The committee lead must be its first member
// (Members[0] == LeadAddress) — leadership is position 0; New rejects otherwise with
// ErrLeadNotFirstMember (compared by account, not display string). This does NOT require the
// launch's creator to be on the committee: creation is gated separately (the coordinator allowlist),
// so the committee — lead included — may be an entirely external set of parties (full delegation).
func New(id uuid.UUID, record ChainRecord, lt LaunchType, committee Committee) (*Launch, error) {
	if err := validateChainRecord(record); err != nil {
		return nil, fmt.Errorf("launch: invalid chain record: %w", err)
	}
	if committee.ThresholdM < 1 || committee.ThresholdM > committee.TotalN {
		return nil, fmt.Errorf("launch: committee threshold %d out of range [1, %d]: %w",
			committee.ThresholdM, committee.TotalN, ErrCommitteeThresholdRange)
	}
	if len(committee.Members) != committee.TotalN {
		return nil, fmt.Errorf("launch: committee has %d members but TotalN is %d: %w",
			len(committee.Members), committee.TotalN, ErrCommitteeSizeMismatch)
	}
	// The lead is the committee's first member (Members[0]) — an invariant RemoveMember relies on
	// when it reassigns the lead to Members[0]. Compare by account (Equal), not display string.
	if !committee.Members[0].Address.Equal(committee.LeadAddress) {
		return nil, fmt.Errorf("launch: committee lead must be member #0 (got %s, lead %s): %w",
			committee.Members[0].Address.Hex(), committee.LeadAddress.Hex(), ErrLeadNotFirstMember)
	}
	now := time.Now().UTC()
	return &Launch{
		ID:                  id,
		Record:              record,
		LaunchType:          lt,
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
		return fmt.Errorf("launch: can only publish from DRAFT, current status: %s: %w", l.Status, ErrInvalidStatusTransition)
	}
	if !isSHA256HexLower(initialGenesisSHA256) {
		return fmt.Errorf("launch: initial genesis hash must be a 64-character lowercase hex string: %w", ErrGenesisHashRequired)
	}
	l.InitialGenesisSHA256 = initialGenesisSHA256
	l.Status = StatusPublished
	l.touch()
	return nil
}

// OpenWindow transitions a PUBLISHED launch to WINDOW_OPEN.
func (l *Launch) OpenWindow() error {
	if l.Status != StatusPublished {
		return fmt.Errorf("launch: can only open window from PUBLISHED, current status: %s: %w", l.Status, ErrInvalidStatusTransition)
	}
	l.Status = StatusWindowOpen
	l.touch()
	return nil
}

// CloseWindow transitions a WINDOW_OPEN launch to WINDOW_CLOSED.
// Enforces min_validator_count precondition.
func (l *Launch) CloseWindow(approvedCount int) error {
	if l.Status != StatusWindowOpen {
		return fmt.Errorf("launch: can only close window from WINDOW_OPEN, current status: %s: %w", l.Status, ErrInvalidStatusTransition)
	}
	if approvedCount < l.Record.MinValidatorCount {
		return fmt.Errorf("launch: need at least %d approved validators to close the window, have %d: %w",
			l.Record.MinValidatorCount, approvedCount, ErrInsufficientValidators)
	}
	// Enforce no single entity holds ≥33% voting power (precondition check only;
	// the running warning is checked on each approval — this is a final gate).
	if dominant, pct := l.dominantVotingPowerPct(); pct >= bftSafetyThreshold {
		return fmt.Errorf("launch: operator %s holds %.1f%% of committed voting power (≥1/3 not allowed at window close): %w",
			dominant, pct, ErrDominantVotingPower)
	}
	l.Status = StatusWindowClosed
	l.touch()
	return nil
}

// PublishGenesis transitions a WINDOW_CLOSED launch to GENESIS_READY.
func (l *Launch) PublishGenesis(finalGenesisSHA256 string) error {
	if l.Status != StatusWindowClosed {
		return fmt.Errorf("launch: can only publish genesis from WINDOW_CLOSED, current status: %s: %w", l.Status, ErrInvalidStatusTransition)
	}
	if !isSHA256HexLower(finalGenesisSHA256) {
		return fmt.Errorf("launch: final genesis hash must be a 64-character lowercase hex string: %w", ErrGenesisHashRequired)
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
		return fmt.Errorf("launch: already canceled: %w", ErrInvalidStatusTransition)
	}
	if l.Status == StatusLaunched {
		return fmt.Errorf("launch: cannot cancel a launched chain: %w", ErrInvalidStatusTransition)
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
		return fmt.Errorf("launch: can only reopen for revision from GENESIS_READY, current status: %s: %w", l.Status, ErrInvalidStatusTransition)
	}
	l.FinalGenesisSHA256 = ""
	l.Status = StatusWindowClosed
	l.touch()
	return nil
}

// MarkLaunched transitions a GENESIS_READY launch to LAUNCHED.
func (l *Launch) MarkLaunched() error {
	if l.Status != StatusGenesisReady {
		return fmt.Errorf("launch: can only mark launched from GENESIS_READY, current status: %s: %w", l.Status, ErrInvalidStatusTransition)
	}
	l.Status = StatusLaunched
	l.touch()
	return nil
}

// EnsureOpenForApplications reports whether the launch's application window is open.
// It does NOT gate membership: who may submit is enforced by the service on the hot
// SUBMITTER address (membership = committee ∪ members, via IsVisibleTo), and validators
// are vetted by committee approval anchored on the operator address — there is no
// validator allowlist (v1 membership model).
func (l *Launch) EnsureOpenForApplications() error {
	if l.Status != StatusWindowOpen {
		return fmt.Errorf("application window is not open (status: %s): %w", l.Status, ErrWindowNotOpen)
	}
	return nil
}

// IsVisibleTo reports whether the launch is visible to the given operator address.
// An empty address represents an unauthenticated caller.
func (l *Launch) IsVisibleTo(addr string) bool {
	// Every launch is discovery-private: visible only to its committee and its
	// validator allowlist. There is no public kind — an empty/unparseable caller
	// sees nothing.
	if addr == "" {
		return false
	}
	validated, err := NewAccountID(addr)
	if err != nil {
		return false
	}
	return l.IsVisibleToAddr(validated)
}

// IsVisibleToAddr is IsVisibleTo for an already-validated operator address. Hot paths
// that have parsed the caller (e.g. join submit) call this to avoid a second bech32 decode.
func (l *Launch) IsVisibleToAddr(addr AccountID) bool {
	return l.Committee.HasMember(addr) || l.Allowlist.Contains(addr)
}

// membersEditable reports whether the member list may be modified in the current status.
// Membership governs who can see + submit, which is only meaningful before the application
// window closes: DRAFT, PUBLISHED, WINDOW_OPEN. After that the set is frozen.
func (l *Launch) membersEditable() bool {
	switch l.Status {
	case StatusDraft, StatusPublished, StatusWindowOpen:
		return true
	case StatusWindowClosed, StatusGenesisReady, StatusLaunched, StatusCancelled:
		return false
	}
	return false
}

// AddMember adds (or relabels) a member on the launch's members list. Idempotent on
// address — re-adding overwrites the label and provenance. Allowed only while the
// member list is editable; returns ErrMembersNotEditable otherwise. Authorization
// (committee-only) is enforced by the application layer, not here.
func (l *Launch) AddMember(m Member) error {
	if !l.membersEditable() {
		return fmt.Errorf("cannot add member in status %s: %w", l.Status, ErrMembersNotEditable)
	}
	l.Allowlist = l.Allowlist.AddMember(m)
	return nil
}

// RemoveMember removes a member from the members list. Returns ErrNotAMember if the
// address is not currently on the list (a committee member not separately on the list is
// not "a member" for this purpose). Allowed only while the members list is editable.
func (l *Launch) RemoveMember(addr AccountID) error {
	if !l.membersEditable() {
		return fmt.Errorf("cannot remove member in status %s: %w", l.Status, ErrMembersNotEditable)
	}
	if !l.Allowlist.Contains(addr) {
		return fmt.Errorf("address %s: %w", addr.String(), ErrNotAMember)
	}
	l.Allowlist = l.Allowlist.Remove(addr)
	return nil
}

// ReplaceCommitteeMember swaps the committee member at oldAddr with newMember.
// Returns an error if oldAddr is not found. No status guard — committee rotation
// can occur at any lifecycle stage via proposal.
func (l *Launch) ReplaceCommitteeMember(oldAddr AccountID, newMember CommitteeMember) error {
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
	return fmt.Errorf("launch: committee member %s not found: %w", oldAddr, ErrCommitteeMemberNotFound)
}

// ExpandCommittee appends newMember to the committee and sets the new threshold.
// Returns an error if newMember's address is already a member, if newThresholdM is
// not in [1, newN-1] (liveness guard: M must be strictly less than N so the committee
// can still act when one member is absent).
func (l *Launch) ExpandCommittee(newMember CommitteeMember, newThresholdM int) error {
	for _, m := range l.Committee.Members {
		if m.Address.Equal(newMember.Address) {
			return fmt.Errorf("launch: committee member %s is already in the committee: %w", newMember.Address, ErrCommitteeMemberExists)
		}
	}
	newN := len(l.Committee.Members) + 1
	if newThresholdM < 1 {
		return fmt.Errorf("launch: threshold must be at least 1: %w", ErrInvalidCommitteeChange)
	}
	if newThresholdM >= newN {
		return fmt.Errorf("launch: threshold %d must be less than new committee size %d (liveness guard: M < N required): %w",
			newThresholdM, newN, ErrInvalidCommitteeChange)
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
func (l *Launch) ShrinkCommittee(removeAddr AccountID, newThresholdM int) error {
	idx := -1
	for i, m := range l.Committee.Members {
		if m.Address.Equal(removeAddr) {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("launch: committee member %s not found: %w", removeAddr, ErrCommitteeMemberNotFound)
	}
	newN := len(l.Committee.Members) - 1
	if newN < 1 {
		return fmt.Errorf("launch: cannot shrink a committee below 1 member: %w", ErrInvalidCommitteeChange)
	}
	if newThresholdM < 1 {
		return fmt.Errorf("launch: threshold must be at least 1: %w", ErrInvalidCommitteeChange)
	}
	if newThresholdM >= newN {
		return fmt.Errorf("launch: threshold %d must be less than new committee size %d (liveness guard: M < N required): %w",
			newThresholdM, newN, ErrInvalidCommitteeChange)
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
	if !isSHA256HexLower(sha256) {
		return fmt.Errorf("launch: %q allocation hash must be a 64-character lowercase hex string: %w", t, ErrAllocationEmptyHash)
	}
	now := time.Now().UTC()
	for i := range l.AllocationFiles {
		if l.AllocationFiles[i].Type != t {
			continue
		}
		l.AllocationFiles[i].SHA256 = sha256
		l.AllocationFiles[i].Status = AllocationPending
		l.AllocationFiles[i].ApprovedByProposal = nil
		l.AllocationFiles[i].UploadedAt = now
		l.touch()
		return nil
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
		if l.AllocationFiles[i].Type != t {
			continue
		}
		if l.AllocationFiles[i].SHA256 != hash {
			return fmt.Errorf("launch: %q: %w", t, ErrAllocationStaleHash)
		}
		pid := proposalID
		l.AllocationFiles[i].Status = AllocationApproved
		l.AllocationFiles[i].ApprovedByProposal = &pid
		l.touch()
		return nil
	}
	return fmt.Errorf("launch: %q: %w", t, ErrAllocationNotFound)
}

// RejectAllocationFile marks the file of type t REJECTED if a file of that type exists
// with the given hash. It reports whether it transitioned a file to REJECTED: false when
// no such file exists or it has since been re-uploaded with a different hash (a stale veto,
// left PENDING). It is the side effect of a vetoed APPROVE_ALLOCATION_FILE proposal, where
// neither "no file" nor "stale" is an error — the veto itself still stands.
func (l *Launch) RejectAllocationFile(t AllocationType, hash string) (rejected bool) {
	for i := range l.AllocationFiles {
		if l.AllocationFiles[i].Type != t {
			continue
		}
		if l.AllocationFiles[i].SHA256 != hash {
			return false // superseded by a re-upload; leave the new file PENDING
		}
		l.AllocationFiles[i].Status = AllocationRejected
		l.AllocationFiles[i].ApprovedByProposal = nil
		l.touch()
		return true
	}
	return false
}

// RecordValidatorApproval records the voting power contribution of an approved validator.
// Returns a warning string if any single entity now holds ≥33% voting power.
func (l *Launch) RecordValidatorApproval(operatorAddr AccountID, selfDelegation int64) string {
	l.approvedVotingPower[operatorAddr.Hex()] = selfDelegation
	dominant, pct := l.dominantVotingPowerPct()
	if pct >= bftSafetyThreshold {
		return fmt.Sprintf("WARNING: operator %s now holds %.1f%% of committed voting power", l.displayAccount(dominant), pct)
	}
	return ""
}

// RemoveValidatorApproval removes a validator's voting power contribution.
func (l *Launch) RemoveValidatorApproval(operatorAddr AccountID) {
	delete(l.approvedVotingPower, operatorAddr.Hex())
}

// ApprovedVotingPowerOf returns the self-delegation of an approved validator (0 if not found).
func (l *Launch) ApprovedVotingPowerOf(operatorAddr AccountID) int64 {
	return l.approvedVotingPower[operatorAddr.Hex()]
}

// InitVotingPower hydrates the in-memory voting power map from persisted data.
// Keys MUST be AccountID.Hex() (canonical account) to match record/lookup; repositories
// normalize the stored address before calling. Called exclusively by repository
// implementations — not for domain or application use.
func (l *Launch) InitVotingPower(powers map[string]int64) {
	l.approvedVotingPower = powers
}

func (l *Launch) touch() {
	l.UpdatedAt = time.Now().UTC()
}

// dominantVotingPowerPct returns the account hex and percentage (0–100) of the operator
// with the highest share of total committed voting power. The returned key is an
// AccountID.Hex(); callers render it for display via displayAccount.
// Uses big.Int arithmetic to handle chains with large token supplies without overflow.
func (l *Launch) dominantVotingPowerPct() (dominantAccountHex string, pct float64) {
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

// displayAccount renders an account-hex map key as bech32 under the launch's prefix for
// human-readable warnings, falling back to the raw hex if rendering fails.
func (l *Launch) displayAccount(accountHex string) string {
	s, err := AccountID{acct: accountHex}.Bech32(l.Record.Bech32Prefix)
	if err != nil {
		return accountHex
	}
	return s
}

func validateChainRecord(r ChainRecord) error {
	if r.ChainID == "" {
		return ErrChainIDRequired
	}
	if r.Bech32Prefix == "" {
		return ErrBech32PrefixRequired
	}
	if r.BinaryName == "" {
		return ErrBinaryNameRequired
	}
	if r.Denom == "" {
		return ErrDenomRequired
	}
	if r.MinValidatorCount < 1 {
		return ErrMinValidatorCountTooLow
	}
	if r.GentxDeadline.IsZero() {
		return ErrGentxDeadlineRequired
	}
	if !r.MaxCommissionChangeRate.LessThanOrEqual(r.MaxCommissionRate) {
		return ErrCommissionChangeExceedsMax
	}
	if r.RepoURL != "" && !IsValidHTTPURL(r.RepoURL) {
		return ErrInvalidRepoURL
	}
	if r.BinarySHA256 != "" && !isSHA256HexLower(r.BinarySHA256) {
		return ErrInvalidBinarySHA256
	}
	if r.MinSelfDelegation != "" {
		if n, ok := new(big.Int).SetString(r.MinSelfDelegation, 10); !ok || n.Sign() < 0 {
			return ErrInvalidMinSelfDelegation
		}
	}
	if r.TotalSupply != "" {
		if n, ok := new(big.Int).SetString(r.TotalSupply, 10); !ok || n.Sign() < 0 {
			return ErrInvalidTotalSupply
		}
	}
	return nil
}

// Validate reports whether the chain record satisfies all field invariants. New runs it at
// creation; the application layer re-runs it after patching draft fields so a launch's record
// stays a valid invariant.
func (r ChainRecord) Validate() error { return validateChainRecord(r) }

// IsValidHTTPURL reports whether s parses as an absolute http(s) URL with a host. It is a
// format-only check (no DNS/SSRF resolution) — used for advertised URLs coordd never dials
// (repo_url, the rehearsal endpoint), unlike the SSRF-checking validator for URLs coordd polls.
func IsValidHTTPURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// isSHA256HexLower reports whether s is a 64-character lowercase hexadecimal string.
func isSHA256HexLower(s string) bool {
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
