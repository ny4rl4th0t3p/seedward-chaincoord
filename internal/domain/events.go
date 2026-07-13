// Package domain defines the shared domain event types emitted by aggregates.
// Application-layer handlers consume these events; the domain never calls handlers directly.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// DomainEvent is the interface implemented by all domain events.
type DomainEvent interface {
	EventName() string
	OccurredAt() time.Time
	GetLaunchID() uuid.UUID
}

// base carries the common fields for all events.
type base struct {
	occurredAt time.Time
}

func (b base) OccurredAt() time.Time { return b.occurredAt }

// withTime returns a copy of the base with the given timestamp.
// Used by aggregates that set the time explicitly (e.g. from ExecutedAt).
func (base) withTime(t time.Time) base { return base{occurredAt: t} }

// ValidatorApproved is emitted when a APPROVE_VALIDATOR proposal reaches quorum.
type ValidatorApproved struct {
	base
	LaunchID        uuid.UUID
	JoinRequestID   uuid.UUID
	OperatorAddress string
}

func (ValidatorApproved) EventName() string        { return "ValidatorApproved" }
func (e ValidatorApproved) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e ValidatorApproved) WithTime(t time.Time) ValidatorApproved {
	e.base = e.withTime(t)
	return e
}

// ValidatorRejected is emitted when a REJECT_VALIDATOR proposal reaches quorum.
type ValidatorRejected struct {
	base
	LaunchID      uuid.UUID
	JoinRequestID uuid.UUID
	Reason        string
}

func (ValidatorRejected) EventName() string        { return "ValidatorRejected" }
func (e ValidatorRejected) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e ValidatorRejected) WithTime(t time.Time) ValidatorRejected {
	e.base = e.withTime(t)
	return e
}

// ValidatorRemoved is emitted when a REMOVE_APPROVED_VALIDATOR proposal reaches quorum.
type ValidatorRemoved struct {
	base
	LaunchID      uuid.UUID
	JoinRequestID uuid.UUID
	Reason        string
}

func (ValidatorRemoved) EventName() string        { return "ValidatorRemoved" }
func (e ValidatorRemoved) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e ValidatorRemoved) WithTime(t time.Time) ValidatorRemoved {
	e.base = e.withTime(t)
	return e
}

// GenesisPublished is emitted when a PUBLISH_GENESIS proposal reaches quorum.
type GenesisPublished struct {
	base
	LaunchID    uuid.UUID
	GenesisHash string
}

func (GenesisPublished) EventName() string        { return "GenesisPublished" }
func (e GenesisPublished) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e GenesisPublished) WithTime(t time.Time) GenesisPublished {
	e.base = e.withTime(t)
	return e
}

// RehearsalGateNotSatisfied is recorded (advisory gate mode) when a PUBLISH_GENESIS raise proceeds
// despite the rehearsal gate not being satisfied. In required mode this is a rejection, not an event.
type RehearsalGateNotSatisfied struct {
	base
	LaunchID uuid.UUID
	Reason   string
}

func (RehearsalGateNotSatisfied) EventName() string        { return "RehearsalGateNotSatisfied" }
func (e RehearsalGateNotSatisfied) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e RehearsalGateNotSatisfied) WithTime(t time.Time) RehearsalGateNotSatisfied {
	e.base = e.withTime(t)
	return e
}

// GenesisTimeUpdated is emitted when an UPDATE_GENESIS_TIME proposal reaches quorum.
type GenesisTimeUpdated struct {
	base
	LaunchID        uuid.UUID
	NewGenesisTime  time.Time
	PrevGenesisTime time.Time
}

func (GenesisTimeUpdated) EventName() string        { return "GenesisTimeUpdated" }
func (e GenesisTimeUpdated) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e GenesisTimeUpdated) WithTime(t time.Time) GenesisTimeUpdated {
	e.base = e.withTime(t)
	return e
}

// WindowClosed is emitted when a CLOSE_APPLICATION_WINDOW proposal reaches quorum.
type WindowClosed struct {
	base
	LaunchID uuid.UUID
}

func (WindowClosed) EventName() string        { return "WindowClosed" }
func (e WindowClosed) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e WindowClosed) WithTime(t time.Time) WindowClosed {
	e.base = e.withTime(t)
	return e
}

// ChainRecordPublished is emitted when a PUBLISH_CHAIN_RECORD proposal reaches quorum.
type ChainRecordPublished struct {
	base
	LaunchID           uuid.UUID
	InitialGenesisHash string
}

func (ChainRecordPublished) EventName() string        { return "ChainRecordPublished" }
func (e ChainRecordPublished) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e ChainRecordPublished) WithTime(t time.Time) ChainRecordPublished {
	e.base = e.withTime(t)
	return e
}

// LaunchCancelled is emitted when a committee lead cancels a launch.
type LaunchCancelled struct {
	base
	LaunchID uuid.UUID
}

func (LaunchCancelled) EventName() string        { return "LaunchCancelled" }
func (e LaunchCancelled) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e LaunchCancelled) WithTime(t time.Time) LaunchCancelled {
	e.base = e.withTime(t)
	return e
}

// GenesisRevisionApproved is emitted when a REVISE_GENESIS proposal reaches quorum,
// reopening the launch for a corrected genesis upload.
type GenesisRevisionApproved struct {
	base
	LaunchID uuid.UUID
}

func (GenesisRevisionApproved) EventName() string        { return "GenesisRevisionApproved" }
func (e GenesisRevisionApproved) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e GenesisRevisionApproved) WithTime(t time.Time) GenesisRevisionApproved {
	e.base = e.withTime(t)
	return e
}

// LaunchCreated is emitted when a new launch is created in DRAFT status.
type LaunchCreated struct {
	base
	LaunchID    uuid.UUID
	ChainID     string
	LaunchType  string
	LeadAddress string
}

func (LaunchCreated) EventName() string        { return "LaunchCreated" }
func (e LaunchCreated) GetLaunchID() uuid.UUID { return e.LaunchID }

// WindowOpened is emitted when the application window is opened on a PUBLISHED launch.
type WindowOpened struct {
	base
	LaunchID uuid.UUID
}

func (WindowOpened) EventName() string        { return "WindowOpened" }
func (e WindowOpened) GetLaunchID() uuid.UUID { return e.LaunchID }

// InitialGenesisUploaded is emitted when the initial (pre-gentx) genesis file is stored.
type InitialGenesisUploaded struct {
	base
	LaunchID    uuid.UUID
	GenesisHash string
}

func (InitialGenesisUploaded) EventName() string        { return "InitialGenesisUploaded" }
func (e InitialGenesisUploaded) GetLaunchID() uuid.UUID { return e.LaunchID }

// FinalGenesisUploaded is emitted when the coordinator-assembled final genesis file is stored.
type FinalGenesisUploaded struct {
	base
	LaunchID    uuid.UUID
	GenesisHash string
}

func (FinalGenesisUploaded) EventName() string        { return "FinalGenesisUploaded" }
func (e FinalGenesisUploaded) GetLaunchID() uuid.UUID { return e.LaunchID }

// AllocationFileUploaded is emitted when a curated allocation file is uploaded
// (or re-uploaded), landing in PENDING status awaiting committee approval.
type AllocationFileUploaded struct {
	base
	LaunchID       uuid.UUID
	AllocationType string
	SHA256         string
}

func (AllocationFileUploaded) EventName() string        { return "AllocationFileUploaded" }
func (e AllocationFileUploaded) GetLaunchID() uuid.UUID { return e.LaunchID }

// AllocationFileApproved is emitted when an APPROVE_ALLOCATION_FILE proposal reaches quorum.
type AllocationFileApproved struct {
	base
	LaunchID       uuid.UUID
	AllocationType string
	SHA256         string
}

func (AllocationFileApproved) EventName() string        { return "AllocationFileApproved" }
func (e AllocationFileApproved) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e AllocationFileApproved) WithTime(t time.Time) AllocationFileApproved {
	e.base = e.withTime(t)
	return e
}

// AllocationFileRejected is emitted when an APPROVE_ALLOCATION_FILE proposal is vetoed,
// marking that file REJECTED (re-uploading a corrected file resets it to PENDING).
type AllocationFileRejected struct {
	base
	LaunchID       uuid.UUID
	AllocationType string
	SHA256         string
}

func (AllocationFileRejected) EventName() string        { return "AllocationFileRejected" }
func (e AllocationFileRejected) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e AllocationFileRejected) WithTime(t time.Time) AllocationFileRejected {
	e.base = e.withTime(t)
	return e
}

// LaunchDetected is emitted by the block monitoring goroutine when block 1 is seen.
type LaunchDetected struct {
	base
	LaunchID    uuid.UUID
	BlockHeight int64
	SourceRPC   string
}

func (LaunchDetected) EventName() string        { return "LaunchDetected" }
func (e LaunchDetected) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e LaunchDetected) WithTime(t time.Time) LaunchDetected {
	e.base = e.withTime(t)
	return e
}

// RehearsalResultRecorded is emitted when coordd stores a signature-verified rehearsal result fact
// (bridge write-back). Stale marks that the attempt's input set is no longer the launch's current one.
type RehearsalResultRecorded struct {
	base
	LaunchID     uuid.UUID
	AttemptID    uuid.UUID
	InputSetHash string
	Outcome      string
	Stale        bool
}

func (RehearsalResultRecorded) EventName() string        { return "RehearsalResultRecorded" }
func (e RehearsalResultRecorded) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e RehearsalResultRecorded) WithTime(t time.Time) RehearsalResultRecorded {
	e.base = e.withTime(t)
	return e
}

// RehearsalAttemptReset is emitted when a coordinator force-releases a stuck rehearsal run lease.
type RehearsalAttemptReset struct {
	base
	LaunchID  uuid.UUID
	AttemptID uuid.UUID
	ResetBy   string
}

func (RehearsalAttemptReset) EventName() string        { return "RehearsalAttemptReset" }
func (e RehearsalAttemptReset) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e RehearsalAttemptReset) WithTime(t time.Time) RehearsalAttemptReset {
	e.base = e.withTime(t)
	return e
}

// RehearsalServiceKeyChanged is emitted when a PATCH changes a launch's trusted rehearsal
// service public key. It is recorded in the tamper-evident audit log because this key is the
// trust anchor for rehearsal result facts: swapping it (any status, a single committee member,
// no proposal) would otherwise let a forged PASS satisfy the required gate with no auditable
// trace. Carries the old and new keys for forensics.
type RehearsalServiceKeyChanged struct {
	base
	LaunchID  uuid.UUID
	OldPubKey string
	NewPubKey string
	ChangedBy string
}

func (RehearsalServiceKeyChanged) EventName() string        { return "RehearsalServiceKeyChanged" }
func (e RehearsalServiceKeyChanged) GetLaunchID() uuid.UUID { return e.LaunchID }

// CommitteeMemberReplaced is emitted when a REPLACE_COMMITTEE_MEMBER proposal executes, swapping
// one committee member for another. Carries the committee membership and threshold before and
// after so this governance change is fully reconstructable from the tamper-evident audit log.
type CommitteeMemberReplaced struct {
	base
	LaunchID      uuid.UUID
	OldAddress    string
	NewAddress    string
	OldMembers    []string
	NewMembers    []string
	OldThresholdM int
	NewThresholdM int
}

func (CommitteeMemberReplaced) EventName() string        { return "CommitteeMemberReplaced" }
func (e CommitteeMemberReplaced) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e CommitteeMemberReplaced) WithTime(t time.Time) CommitteeMemberReplaced {
	e.base = e.withTime(t)
	return e
}

// CommitteeExpanded is emitted when an EXPAND_COMMITTEE proposal executes, adding a member and
// (possibly) changing the M-of-N threshold. Carries membership + threshold before and after.
type CommitteeExpanded struct {
	base
	LaunchID      uuid.UUID
	AddedAddress  string
	OldMembers    []string
	NewMembers    []string
	OldThresholdM int
	NewThresholdM int
}

func (CommitteeExpanded) EventName() string        { return "CommitteeExpanded" }
func (e CommitteeExpanded) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e CommitteeExpanded) WithTime(t time.Time) CommitteeExpanded {
	e.base = e.withTime(t)
	return e
}

// CommitteeShrunk is emitted when a SHRINK_COMMITTEE proposal executes, removing a member and
// (possibly) changing the M-of-N threshold. Carries membership + threshold before and after.
type CommitteeShrunk struct {
	base
	LaunchID       uuid.UUID
	RemovedAddress string
	OldMembers     []string
	NewMembers     []string
	OldThresholdM  int
	NewThresholdM  int
}

func (CommitteeShrunk) EventName() string        { return "CommitteeShrunk" }
func (e CommitteeShrunk) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e CommitteeShrunk) WithTime(t time.Time) CommitteeShrunk {
	e.base = e.withTime(t)
	return e
}

// --- Global (non-launch) events: recorded under ports.GlobalAuditScope. GetLaunchID is uuid.Nil.

// CoordinatorAdded is emitted when an address is added to the global coordinator allowlist.
type CoordinatorAdded struct {
	base
	Address string
	AddedBy string
}

func (CoordinatorAdded) EventName() string      { return "CoordinatorAdded" }
func (CoordinatorAdded) GetLaunchID() uuid.UUID { return uuid.Nil }
func (e CoordinatorAdded) WithTime(t time.Time) CoordinatorAdded {
	e.base = e.withTime(t)
	return e
}

// CoordinatorRemoved is emitted when an address is removed from the global coordinator allowlist.
type CoordinatorRemoved struct {
	base
	Address   string
	RemovedBy string
}

func (CoordinatorRemoved) EventName() string      { return "CoordinatorRemoved" }
func (CoordinatorRemoved) GetLaunchID() uuid.UUID { return uuid.Nil }
func (e CoordinatorRemoved) WithTime(t time.Time) CoordinatorRemoved {
	e.base = e.withTime(t)
	return e
}

// SessionsRevoked is emitted when all sessions for an account are revoked — by the account itself
// (self-service) or by an admin. RevokedBy is the actor; Account is the target.
type SessionsRevoked struct {
	base
	Account   string
	RevokedBy string
}

func (SessionsRevoked) EventName() string      { return "SessionsRevoked" }
func (SessionsRevoked) GetLaunchID() uuid.UUID { return uuid.Nil }
func (e SessionsRevoked) WithTime(t time.Time) SessionsRevoked {
	e.base = e.withTime(t)
	return e
}

// --- Two-phase proposal-execution audit. The intent (ProposalExecuting) is written BEFORE the
// state mutation commits; the completion (the per-action event) AFTER. If the intent write fails
// the proposal is aborted (no unaudited governance); if execution rolls back after the intent,
// ProposalExecutionAborted records it so the trail self-explains.

// ProposalExecuting is the write-ahead intent recorded before a quorum-reached proposal's state
// mutation is committed. Intent present with no completion event = the action was in flight.
type ProposalExecuting struct {
	base
	LaunchID   uuid.UUID
	ProposalID uuid.UUID
	ActionType string
}

func (ProposalExecuting) EventName() string        { return "ProposalExecuting" }
func (e ProposalExecuting) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e ProposalExecuting) WithTime(t time.Time) ProposalExecuting {
	e.base = e.withTime(t)
	return e
}

// ProposalExecutionAborted is recorded when a proposal's execution fails or rolls back AFTER its
// intent was written (intent + aborted = the action did not happen).
type ProposalExecutionAborted struct {
	base
	LaunchID   uuid.UUID
	ProposalID uuid.UUID
	ActionType string
	Reason     string
}

func (ProposalExecutionAborted) EventName() string        { return "ProposalExecutionAborted" }
func (e ProposalExecutionAborted) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e ProposalExecutionAborted) WithTime(t time.Time) ProposalExecutionAborted {
	e.base = e.withTime(t)
	return e
}

// JoinRequestSubmitted is emitted when a validator's join request passes validation and is stored.
type JoinRequestSubmitted struct {
	base
	LaunchID         uuid.UUID
	JoinRequestID    uuid.UUID
	OperatorAddress  string
	SubmitterAddress string
}

func (JoinRequestSubmitted) EventName() string        { return "JoinRequestSubmitted" }
func (e JoinRequestSubmitted) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e JoinRequestSubmitted) WithTime(t time.Time) JoinRequestSubmitted {
	e.base = e.withTime(t)
	return e
}

// ReadinessConfirmed is emitted when an approved validator confirms readiness for a GENESIS_READY launch.
type ReadinessConfirmed struct {
	base
	LaunchID        uuid.UUID
	OperatorAddress string
}

func (ReadinessConfirmed) EventName() string        { return "ReadinessConfirmed" }
func (e ReadinessConfirmed) GetLaunchID() uuid.UUID { return e.LaunchID }
func (e ReadinessConfirmed) WithTime(t time.Time) ReadinessConfirmed {
	e.base = e.withTime(t)
	return e
}
