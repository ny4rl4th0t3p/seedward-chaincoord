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
	Visibility  string
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
