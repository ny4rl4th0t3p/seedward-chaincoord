// Package proposal contains the Proposal aggregate, which collects M-of-N
// coordinator signatures and executes actions when quorum is reached.
package proposal

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// Sentinel errors for proposal construction and signing. Callers (the proposal service,
// tests) match these with errors.Is. The service maps ErrProposalPayloadRequired → 400 and
// the signing-guard errors → 409.
var (
	ErrProposalPayloadRequired  = errors.New("proposal payload must not be nil")
	ErrProposalNotPending       = errors.New("proposal is not pending signatures")
	ErrProposalTTLExpired       = errors.New("proposal TTL has expired")
	ErrCoordinatorAlreadySigned = errors.New("coordinator has already signed this proposal")
)

// Status is the proposal lifecycle state.
type Status string

const (
	StatusPendingSignatures Status = "PENDING_SIGNATURES"
	StatusExecuted          Status = "EXECUTED"
	StatusVetoed            Status = "VETOED"
	StatusExpired           Status = "EXPIRED"
)

// ActionType identifies what the proposal does when executed.
type ActionType string

const (
	ActionApproveValidator        ActionType = "APPROVE_VALIDATOR"
	ActionRejectValidator         ActionType = "REJECT_VALIDATOR"
	ActionRemoveApprovedValidator ActionType = "REMOVE_APPROVED_VALIDATOR"
	ActionApproveAllocationFile   ActionType = "APPROVE_ALLOCATION_FILE"
	ActionPublishChainRecord      ActionType = "PUBLISH_CHAIN_RECORD"
	ActionCloseApplicationWindow  ActionType = "CLOSE_APPLICATION_WINDOW"
	ActionPublishGenesis          ActionType = "PUBLISH_GENESIS"
	ActionUpdateGenesisTime       ActionType = "UPDATE_GENESIS_TIME"
	ActionReplaceCommitteeMember  ActionType = "REPLACE_COMMITTEE_MEMBER"
	ActionReviseGenesis           ActionType = "REVISE_GENESIS"
	ActionExpandCommittee         ActionType = "EXPAND_COMMITTEE"
	ActionShrinkCommittee         ActionType = "SHRINK_COMMITTEE"
)

// Decision is a coordinator's vote on a proposal.
type Decision string

const (
	DecisionSign Decision = "SIGN"
	DecisionVeto Decision = "VETO"
)

// SignatureEntry records one coordinator's signed decision.
type SignatureEntry struct {
	CoordinatorAddress launch.OperatorAddress
	Decision           Decision
	Timestamp          time.Time
	Signature          launch.Signature // sig over canonical JSON of the signing payload
}

// Proposal is the aggregate root for a multi-sig coordinator action.
type Proposal struct {
	ID         uuid.UUID
	LaunchID   uuid.UUID
	ActionType ActionType
	Payload    []byte // canonical JSON of action-specific data
	ProposedBy launch.OperatorAddress
	ProposedAt time.Time
	TTLExpires time.Time
	Status     Status
	ExecutedAt *time.Time
	Signatures []SignatureEntry

	events []domain.DomainEvent
}

// New creates a new Proposal in PENDING_SIGNATURES status.
// The proposer's signature is added immediately (they implicitly sign by proposing).
func New(
	id uuid.UUID,
	launchID uuid.UUID,
	actionType ActionType,
	payload []byte,
	proposedBy launch.OperatorAddress,
	proposerSig launch.Signature,
	ttl time.Duration,
	now time.Time,
) (*Proposal, error) {
	if payload == nil {
		return nil, fmt.Errorf("proposal: %w", ErrProposalPayloadRequired)
	}
	if err := ValidatePayload(actionType, payload); err != nil {
		return nil, fmt.Errorf("proposal: invalid payload: %w", err)
	}
	p := &Proposal{
		ID:         id,
		LaunchID:   launchID,
		ActionType: actionType,
		Payload:    payload,
		ProposedBy: proposedBy,
		ProposedAt: now,
		TTLExpires: now.Add(ttl),
		Status:     StatusPendingSignatures,
		Signatures: []SignatureEntry{
			{
				CoordinatorAddress: proposedBy,
				Decision:           DecisionSign,
				Timestamp:          now,
				Signature:          proposerSig,
			},
		},
	}
	return p, nil
}

// Sign adds a coordinator's SIGN or VETO decision.
// - A VETO immediately moves the proposal to VETOED.
// - A SIGN is accumulated; once M signatures are collected the proposal executes.
func (p *Proposal) Sign(
	coordinatorAddr launch.OperatorAddress,
	decision Decision,
	sig launch.Signature,
	thresholdM int,
	now time.Time,
) error {
	if p.Status != StatusPendingSignatures {
		return fmt.Errorf("proposal: cannot sign a proposal in status %s: %w", p.Status, ErrProposalNotPending)
	}
	if p.TTLExpires.Before(now) {
		return fmt.Errorf("proposal: TTL has expired: %w", ErrProposalTTLExpired)
	}
	for _, s := range p.Signatures {
		if s.CoordinatorAddress.Equal(coordinatorAddr) {
			return fmt.Errorf("proposal: coordinator %s has already signed: %w", coordinatorAddr, ErrCoordinatorAlreadySigned)
		}
	}

	p.Signatures = append(p.Signatures, SignatureEntry{
		CoordinatorAddress: coordinatorAddr,
		Decision:           decision,
		Timestamp:          now,
		Signature:          sig,
	})

	if decision == DecisionVeto {
		p.Status = StatusVetoed
		return nil
	}

	// Count SIGN decisions
	signCount := 0
	for _, s := range p.Signatures {
		if s.Decision == DecisionSign {
			signCount++
		}
	}
	if signCount >= thresholdM {
		executedAt := now
		p.Status = StatusExecuted
		p.ExecutedAt = &executedAt
		p.emitExecutionEvents()
	}

	return nil
}

// CheckQuorum runs the threshold check without adding a new signature.
// Called by the service after New to handle cases where the proposer's own
// signature already meets the threshold (e.g. a 1-of-N committee).
func (p *Proposal) CheckQuorum(thresholdM int, now time.Time) {
	if p.Status != StatusPendingSignatures {
		return
	}
	signCount := 0
	for _, s := range p.Signatures {
		if s.Decision == DecisionSign {
			signCount++
		}
	}
	if signCount >= thresholdM {
		executedAt := now
		p.Status = StatusExecuted
		p.ExecutedAt = &executedAt
		p.emitExecutionEvents()
	}
}

// ExpireIfStale transitions the proposal to EXPIRED if the TTL has elapsed.
func (p *Proposal) ExpireIfStale(now time.Time) bool {
	if p.Status == StatusPendingSignatures && p.TTLExpires.Before(now) {
		p.Status = StatusExpired
		return true
	}
	return false
}

// SignCount returns the number of SIGN decisions collected so far.
func (p *Proposal) SignCount() int {
	n := 0
	for _, s := range p.Signatures {
		if s.Decision == DecisionSign {
			n++
		}
	}
	return n
}

// PopEvents returns and clears the accumulated domain events.
func (p *Proposal) PopEvents() []domain.DomainEvent {
	ev := p.events
	p.events = nil
	return ev
}

// emitExecutionEvents emits the appropriate domain event based on action type.
func (p *Proposal) emitExecutionEvents() {
	now := *p.ExecutedAt

	// Parse action-specific fields from payload as needed.
	// The payload is canonical JSON; specific fields extracted by helpers below.
	switch p.ActionType {
	case ActionApproveValidator:
		jrID, opAddr := extractValidatorFields(p.Payload)
		p.events = append(p.events, domain.ValidatorApproved{
			LaunchID:        p.LaunchID,
			JoinRequestID:   jrID,
			OperatorAddress: opAddr,
		}.WithTime(now))

	case ActionRejectValidator:
		jrID, reason := extractValidatorRejectFields(p.Payload)
		p.events = append(p.events, domain.ValidatorRejected{
			LaunchID:      p.LaunchID,
			JoinRequestID: jrID,
			Reason:        reason,
		}.WithTime(now))

	case ActionRemoveApprovedValidator:
		jrID, reason := extractValidatorRejectFields(p.Payload)
		p.events = append(p.events, domain.ValidatorRemoved{
			LaunchID:      p.LaunchID,
			JoinRequestID: jrID,
			Reason:        reason,
		}.WithTime(now))

	case ActionPublishChainRecord:
		hash := extractInitialGenesisHash(p.Payload)
		p.events = append(p.events, domain.ChainRecordPublished{
			LaunchID:           p.LaunchID,
			InitialGenesisHash: hash,
		}.WithTime(now))

	case ActionCloseApplicationWindow:
		p.events = append(p.events, domain.WindowClosed{
			LaunchID: p.LaunchID,
		}.WithTime(now))

	case ActionPublishGenesis:
		hash := extractGenesisHash(p.Payload)
		p.events = append(p.events, domain.GenesisPublished{
			LaunchID:    p.LaunchID,
			GenesisHash: hash,
		}.WithTime(now))

	case ActionUpdateGenesisTime:
		newTime, prevTime := extractGenesisTimes(p.Payload)
		p.events = append(p.events, domain.GenesisTimeUpdated{
			LaunchID:        p.LaunchID,
			NewGenesisTime:  newTime,
			PrevGenesisTime: prevTime,
		}.WithTime(now))

	case ActionReviseGenesis:
		p.events = append(p.events, domain.GenesisRevisionApproved{
			LaunchID: p.LaunchID,
		}.WithTime(now))

	case ActionApproveAllocationFile:
		allocType, hash := extractAllocationFields(p.Payload)
		p.events = append(p.events, domain.AllocationFileApproved{
			LaunchID:       p.LaunchID,
			AllocationType: allocType,
			SHA256:         hash,
		}.WithTime(now))

	case ActionReplaceCommitteeMember, ActionExpandCommittee, ActionShrinkCommittee:
		// These actions mutate launch state directly via applyAndSave; no domain event needed.
	}
}
