package services

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/chaincoord/internal/domain"
	"github.com/ny4rl4th0t3p/chaincoord/internal/domain/joinrequest"
	"github.com/ny4rl4th0t3p/chaincoord/internal/domain/launch"
	"github.com/ny4rl4th0t3p/chaincoord/internal/domain/proposal"
	"github.com/ny4rl4th0t3p/chaincoord/pkg/canonicaljson"
)

const (
	defaultProposalTTL = 48 * time.Hour
	pendingJRLimit     = 1000 // max pending join requests to expire in a single close-window call
)

// ProposalService orchestrates the lifecycle of coordinator proposals.
// It is the central dispatcher: when a proposal executes, this service applies
// the side effects to the affected aggregates and dispatches domain events.
type ProposalService struct {
	launches     ports.LaunchRepository
	joinRequests ports.JoinRequestRepository
	proposals    ports.ProposalRepository
	readiness    ports.ReadinessRepository
	nonces       ports.NonceStore
	verifier     ports.SignatureVerifier
	events       ports.EventPublisher
	audit        ports.AuditLogWriter
	tx           ports.Transactor
}

func NewProposalService(
	launches ports.LaunchRepository,
	joinRequests ports.JoinRequestRepository,
	proposals ports.ProposalRepository,
	readiness ports.ReadinessRepository,
	nonces ports.NonceStore,
	verifier ports.SignatureVerifier,
	events ports.EventPublisher,
	audit ports.AuditLogWriter,
	tx ports.Transactor,
) *ProposalService {
	return &ProposalService{
		launches:     launches,
		joinRequests: joinRequests,
		proposals:    proposals,
		readiness:    readiness,
		nonces:       nonces,
		verifier:     verifier,
		events:       events,
		audit:        audit,
		tx:           tx,
	}
}

// RaiseInput is the payload for creating a new proposal.
type RaiseInput struct {
	ActionType      proposal.ActionType `json:"action_type"`
	Payload         json.RawMessage     `json:"payload" swaggertype:"object"`
	CoordinatorAddr string              `json:"coordinator_address"`
	Nonce           string              `json:"nonce"`
	Timestamp       string              `json:"timestamp"`
	Signature       string              `json:"signature"`
}

// Raise creates a new proposal. The proposer's signature is attached immediately.
func (s *ProposalService) Raise(ctx context.Context, launchID uuid.UUID, input RaiseInput) (*proposal.Proposal, error) {
	if err := s.nonces.Consume(ctx, input.CoordinatorAddr, input.Nonce); err != nil {
		return nil, fmt.Errorf("raise proposal: nonce rejected: %w", err)
	}
	if err := validateTimestamp(input.Timestamp); err != nil {
		return nil, fmt.Errorf("raise proposal: %w", err)
	}

	message, err := canonicaljson.MarshalForSigning(input)
	if err != nil {
		return nil, fmt.Errorf("raise proposal: signing bytes: %w", err)
	}
	sigBytes, err := decodeBase64Sig(input.Signature)
	if err != nil {
		return nil, fmt.Errorf("raise proposal: signature encoding: %w", err)
	}

	l, err := s.launches.FindByID(ctx, launchID)
	if err != nil {
		return nil, fmt.Errorf("raise proposal: launch: %w", err)
	}

	coordAddr, err := launch.NewOperatorAddress(input.CoordinatorAddr)
	if err != nil {
		return nil, fmt.Errorf("raise proposal: coordinator address: %w", err)
	}
	if !l.Committee.HasMember(coordAddr) {
		return nil, fmt.Errorf("raise proposal: %s is not a committee member: %w", input.CoordinatorAddr, ports.ErrForbidden)
	}

	// Find the committee member's pubkey for verification.
	pubKeyB64 := committeeMemberPubKey(l.Committee, coordAddr)
	if err := s.verifier.Verify(input.CoordinatorAddr, pubKeyB64, message, sigBytes); err != nil {
		return nil, fmt.Errorf("raise proposal: signature invalid: %w", err)
	}

	sig, err := launch.NewSignature(input.Signature)
	if err != nil {
		return nil, fmt.Errorf("raise proposal: signature value: %w", err)
	}

	now := time.Now()
	p, err := proposal.New(
		uuid.New(),
		launchID,
		input.ActionType,
		input.Payload,
		coordAddr,
		sig,
		defaultProposalTTL,
		now,
	)
	if err != nil {
		return nil, fmt.Errorf("raise proposal: %w", err)
	}

	// If the proposer's single signature already meets the committee threshold
	// (e.g. a 1-of-N committee), execute the proposal immediately.
	p.CheckQuorum(l.Committee.ThresholdM, now)
	if p.Status == proposal.StatusExecuted {
		if err := s.applyAndSave(ctx, l, p); err != nil {
			return nil, err
		}
		return p, nil
	}

	if err := s.proposals.Save(ctx, p); err != nil {
		return nil, fmt.Errorf("raise proposal: save: %w", err)
	}
	return p, nil
}

// SignInput is the payload for a coordinator's SIGN or VETO decision.
type SignInput struct {
	CoordinatorAddr string            `json:"coordinator_address"`
	Decision        proposal.Decision `json:"decision"`
	Nonce           string            `json:"nonce"`
	Timestamp       string            `json:"timestamp"`
	Signature       string            `json:"signature"`
}

// Sign adds a coordinator's decision to a pending proposal.
func (s *ProposalService) Sign(ctx context.Context, launchID, proposalID uuid.UUID, input SignInput) (*proposal.Proposal, error) {
	if err := s.nonces.Consume(ctx, input.CoordinatorAddr, input.Nonce); err != nil {
		return nil, fmt.Errorf("sign proposal: nonce rejected: %w", err)
	}
	if err := validateTimestamp(input.Timestamp); err != nil {
		return nil, fmt.Errorf("sign proposal: %w", err)
	}

	message, err := canonicaljson.MarshalForSigning(input)
	if err != nil {
		return nil, fmt.Errorf("sign proposal: signing bytes: %w", err)
	}
	sigBytes, err := decodeBase64Sig(input.Signature)
	if err != nil {
		return nil, fmt.Errorf("sign proposal: signature encoding: %w", err)
	}

	l, err := s.launches.FindByID(ctx, launchID)
	if err != nil {
		return nil, fmt.Errorf("sign proposal: launch: %w", err)
	}

	coordAddr, err := launch.NewOperatorAddress(input.CoordinatorAddr)
	if err != nil {
		return nil, fmt.Errorf("sign proposal: coordinator address: %w", err)
	}
	if !l.Committee.HasMember(coordAddr) {
		return nil, fmt.Errorf("sign proposal: %w", ports.ErrForbidden)
	}

	pubKeyB64 := committeeMemberPubKey(l.Committee, coordAddr)
	if err := s.verifier.Verify(input.CoordinatorAddr, pubKeyB64, message, sigBytes); err != nil {
		return nil, fmt.Errorf("sign proposal: signature invalid: %w", err)
	}

	p, err := s.proposals.FindByID(ctx, proposalID)
	if err != nil {
		return nil, fmt.Errorf("sign proposal: proposal: %w", err)
	}
	if p.LaunchID != launchID {
		return nil, ports.ErrNotFound
	}
	if p.Status != proposal.StatusPendingSignatures {
		return nil, fmt.Errorf("sign proposal: proposal is already %s: %w", p.Status, ports.ErrConflict)
	}

	sig, err := launch.NewSignature(input.Signature)
	if err != nil {
		return nil, fmt.Errorf("sign proposal: signature value: %w", err)
	}

	if err := p.Sign(coordAddr, input.Decision, sig, l.Committee.ThresholdM, time.Now()); err != nil {
		return nil, fmt.Errorf("sign proposal: %w", err)
	}

	if p.Status == proposal.StatusExecuted {
		if err := s.applyAndSave(ctx, l, p); err != nil {
			return nil, err
		}
		return p, nil
	}

	if err := s.proposals.Save(ctx, p); err != nil {
		return nil, fmt.Errorf("sign proposal: save: %w", err)
	}
	return p, nil
}

// ExpireStale transitions all TTL-elapsed proposals to EXPIRED.
// Called by the background job on a regular interval.
func (s *ProposalService) ExpireStale(ctx context.Context) error {
	pending, err := s.proposals.FindPending(ctx)
	if err != nil {
		return fmt.Errorf("expire stale: find pending: %w", err)
	}
	now := time.Now()
	for _, p := range pending {
		if p.ExpireIfStale(now) {
			if err := s.proposals.Save(ctx, p); err != nil {
				return fmt.Errorf("expire stale: save proposal %s: %w", p.ID, err)
			}
		}
	}
	return nil
}

// ListForLaunch returns all proposals for a launch with pagination.
func (s *ProposalService) ListForLaunch(ctx context.Context, launchID uuid.UUID, page, perPage int) ([]*proposal.Proposal, int, error) {
	return s.proposals.FindByLaunch(ctx, launchID, page, perPage)
}

// GetByID returns a single proposal.
func (s *ProposalService) GetByID(ctx context.Context, launchID, proposalID uuid.UUID) (*proposal.Proposal, error) {
	p, err := s.proposals.FindByID(ctx, proposalID)
	if err != nil {
		return nil, err
	}
	if p.LaunchID != launchID {
		return nil, ports.ErrNotFound
	}
	return p, nil
}

// applyProposal applies the side effects of an executed proposal to the affected
// aggregates and persists all changed aggregates. It is always called inside a
// database transaction (via applyAndSave). Event dispatch happens after commit.
func (s *ProposalService) applyProposal(ctx context.Context, l *launch.Launch, p *proposal.Proposal) error {
	switch p.ActionType {
	case proposal.ActionApproveValidator:
		return s.applyApproveValidator(ctx, l, p)

	case proposal.ActionRejectValidator:
		return s.applyRejectValidator(ctx, p)

	case proposal.ActionRemoveApprovedValidator:
		return s.applyRemoveValidator(ctx, l, p)

	case proposal.ActionPublishChainRecord:
		return s.applyPublishChainRecord(ctx, l, p)

	case proposal.ActionCloseApplicationWindow:
		return s.applyCloseWindow(ctx, l, p)

	case proposal.ActionPublishGenesis:
		return s.applyPublishGenesis(ctx, l, p)

	case proposal.ActionUpdateGenesisTime:
		return s.applyUpdateGenesisTime(ctx, l, p)

	case proposal.ActionAddGenesisAccount:
		return s.applyAddGenesisAccount(ctx, l, p)

	case proposal.ActionRemoveGenesisAccount:
		return s.applyRemoveGenesisAccount(ctx, l, p)

	case proposal.ActionModifyGenesisAccount:
		return s.applyModifyGenesisAccount(ctx, l, p)

	case proposal.ActionReplaceCommitteeMember:
		return s.applyReplaceCommitteeMember(ctx, l, p)

	case proposal.ActionReviseGenesis:
		return s.applyReviseGenesis(ctx, l, p)

	case proposal.ActionExpandCommittee:
		return s.applyExpandCommittee(ctx, l, p)

	case proposal.ActionShrinkCommittee:
		return s.applyShrinkCommittee(ctx, l, p)

	default:
		return fmt.Errorf("apply proposal: unknown action type %q", p.ActionType)
	}
}

func (s *ProposalService) applyApproveValidator(ctx context.Context, l *launch.Launch, p *proposal.Proposal) error {
	var pl proposal.ApproveValidatorPayload
	if err := json.Unmarshal(p.Payload, &pl); err != nil {
		return fmt.Errorf("apply approve validator: payload: %w", err)
	}

	jr, err := s.joinRequests.FindByID(ctx, pl.JoinRequestID)
	if err != nil {
		return fmt.Errorf("apply approve validator: join request: %w", err)
	}
	if err := jr.Approve(p.ID); err != nil {
		return fmt.Errorf("apply approve validator: %w", err)
	}

	// Record voting power and check 33% warning.
	// The address was validated when the proposal was raised; this error is unexpected.
	operatorAddr, err := launch.NewOperatorAddress(pl.OperatorAddress)
	if err != nil {
		return fmt.Errorf("apply approve validator: invalid operator address in payload: %w", err)
	}
	// RecordValidatorApproval returns a non-empty warning string when a single entity
	// reaches ≥33% of committed voting power. The warning is stored on the proposal
	// payload so it surfaces in the API response and the audit log.
	// It does not block the approval — the coordinator committee decides whether to proceed.
	_ = l.RecordValidatorApproval(operatorAddr, jr.SelfDelegationAmount())

	if err := s.joinRequests.Save(ctx, jr); err != nil {
		return fmt.Errorf("apply approve validator: save join request: %w", err)
	}
	return s.saveLaunchAndProposal(ctx, l, p)
}

func (s *ProposalService) applyRejectValidator(ctx context.Context, p *proposal.Proposal) error {
	var pl proposal.RejectValidatorPayload
	if err := json.Unmarshal(p.Payload, &pl); err != nil {
		return fmt.Errorf("apply reject validator: payload: %w", err)
	}

	jr, err := s.joinRequests.FindByID(ctx, pl.JoinRequestID)
	if err != nil {
		return fmt.Errorf("apply reject validator: join request: %w", err)
	}
	if err := jr.Reject(pl.Reason); err != nil {
		return fmt.Errorf("apply reject validator: %w", err)
	}
	if err := s.joinRequests.Save(ctx, jr); err != nil {
		return fmt.Errorf("apply reject validator: save join request: %w", err)
	}
	if err := s.proposals.Save(ctx, p); err != nil {
		return fmt.Errorf("apply reject validator: save proposal: %w", err)
	}
	return nil
}

func (s *ProposalService) applyRemoveValidator(ctx context.Context, l *launch.Launch, p *proposal.Proposal) error {
	if l.Status != launch.StatusWindowOpen && l.Status != launch.StatusWindowClosed {
		return fmt.Errorf("apply remove validator: only allowed in WINDOW_OPEN or WINDOW_CLOSED, current: %s: %w", l.Status, ports.ErrBadRequest)
	}
	var pl proposal.RemoveApprovedValidatorPayload
	if err := json.Unmarshal(p.Payload, &pl); err != nil {
		return fmt.Errorf("apply remove validator: payload: %w", err)
	}

	jr, err := s.joinRequests.FindByID(ctx, pl.JoinRequestID)
	if err != nil {
		return fmt.Errorf("apply remove validator: join request: %w", err)
	}
	if err := jr.Revoke(pl.Reason); err != nil {
		return fmt.Errorf("apply remove validator: %w", err)
	}

	operatorAddr, err := launch.NewOperatorAddress(pl.OperatorAddress)
	if err != nil {
		return fmt.Errorf("apply remove validator: invalid operator address in payload: %w", err)
	}
	l.RemoveValidatorApproval(operatorAddr)

	if err := s.joinRequests.Save(ctx, jr); err != nil {
		return fmt.Errorf("apply remove validator: save join request: %w", err)
	}
	return s.saveLaunchAndProposal(ctx, l, p)
}

func (s *ProposalService) applyPublishChainRecord(ctx context.Context, l *launch.Launch, p *proposal.Proposal) error {
	var pl proposal.PublishChainRecordPayload
	if err := json.Unmarshal(p.Payload, &pl); err != nil {
		return fmt.Errorf("apply publish chain record: payload: %w", err)
	}
	if l.InitialGenesisSHA256 == "" {
		return fmt.Errorf("apply publish chain record: initial genesis has not been uploaded")
	}
	if pl.InitialGenesisHash != l.InitialGenesisSHA256 {
		return fmt.Errorf("apply publish chain record: attested genesis hash %q does not match uploaded hash %q",
			pl.InitialGenesisHash, l.InitialGenesisSHA256)
	}
	if err := l.Publish(pl.InitialGenesisHash); err != nil {
		return fmt.Errorf("apply publish chain record: %w", err)
	}
	return s.saveLaunchAndProposal(ctx, l, p)
}

func (s *ProposalService) applyCloseWindow(ctx context.Context, l *launch.Launch, p *proposal.Proposal) error {
	// Count approved validators for the precondition check.
	approved, err := s.joinRequests.FindApprovedByLaunch(ctx, l.ID)
	if err != nil {
		return fmt.Errorf("apply close window: count approved: %w", err)
	}
	if err := l.CloseWindow(len(approved)); err != nil {
		return fmt.Errorf("apply close window: %w", err)
	}

	// Expire all remaining PENDING join requests.
	pending, _, err := s.joinRequests.FindByLaunch(ctx, l.ID, pendingStatus(), 1, pendingJRLimit)
	if err != nil {
		return fmt.Errorf("apply close window: find pending: %w", err)
	}
	for _, jr := range pending {
		_ = jr.Expire()
		if err := s.joinRequests.Save(ctx, jr); err != nil {
			return fmt.Errorf("apply close window: expire join request %s: %w", jr.ID, err)
		}
	}

	return s.saveLaunchAndProposal(ctx, l, p)
}

func (s *ProposalService) applyPublishGenesis(ctx context.Context, l *launch.Launch, p *proposal.Proposal) error {
	var pl proposal.PublishGenesisPayload
	if err := json.Unmarshal(p.Payload, &pl); err != nil {
		return fmt.Errorf("apply publish genesis: payload: %w", err)
	}
	if err := l.PublishGenesis(pl.GenesisHash); err != nil {
		return fmt.Errorf("apply publish genesis: %w", err)
	}
	return s.saveLaunchAndProposal(ctx, l, p)
}

func (s *ProposalService) applyUpdateGenesisTime(ctx context.Context, l *launch.Launch, p *proposal.Proposal) error {
	if l.Status == launch.StatusLaunched || l.Status == launch.StatusCancelled {
		return fmt.Errorf("apply update genesis time: not allowed in %s status: %w", l.Status, ports.ErrBadRequest)
	}
	var pl proposal.UpdateGenesisTimePayload
	if err := json.Unmarshal(p.Payload, &pl); err != nil {
		return fmt.Errorf("apply update genesis time: payload: %w", err)
	}

	// Update the genesis time on the chain record.
	l.Record.GenesisTime = &pl.NewGenesisTime

	// Invalidate all existing readiness confirmations.
	if err := s.readiness.InvalidateByLaunch(ctx, l.ID); err != nil {
		return fmt.Errorf("apply update genesis time: invalidate readiness: %w", err)
	}

	return s.saveLaunchAndProposal(ctx, l, p)
}

func (s *ProposalService) applyAddGenesisAccount(ctx context.Context, l *launch.Launch, p *proposal.Proposal) error {
	var pl proposal.AddGenesisAccountPayload
	if err := json.Unmarshal(p.Payload, &pl); err != nil {
		return fmt.Errorf("apply add genesis account: payload: %w", err)
	}
	account := launch.GenesisAccount{
		Address: pl.Address,
		Amount:  pl.Amount,
	}
	if pl.VestingSchedule != nil {
		account.VestingSchedule = pl.VestingSchedule
	}
	if err := l.AddGenesisAccount(account); err != nil {
		return fmt.Errorf("apply add genesis account: %w", err)
	}
	return s.saveLaunchAndProposal(ctx, l, p)
}

func (s *ProposalService) applyRemoveGenesisAccount(ctx context.Context, l *launch.Launch, p *proposal.Proposal) error {
	var pl proposal.RemoveGenesisAccountPayload
	if err := json.Unmarshal(p.Payload, &pl); err != nil {
		return fmt.Errorf("apply remove genesis account: payload: %w", err)
	}
	if err := l.RemoveGenesisAccount(pl.Address); err != nil {
		return fmt.Errorf("apply remove genesis account: %w", err)
	}
	return s.saveLaunchAndProposal(ctx, l, p)
}

func (s *ProposalService) applyModifyGenesisAccount(ctx context.Context, l *launch.Launch, p *proposal.Proposal) error {
	var pl proposal.ModifyGenesisAccountPayload
	if err := json.Unmarshal(p.Payload, &pl); err != nil {
		return fmt.Errorf("apply modify genesis account: payload: %w", err)
	}
	if err := l.ModifyGenesisAccount(pl.Address, pl.Amount, pl.VestingSchedule); err != nil {
		return fmt.Errorf("apply modify genesis account: %w", err)
	}
	return s.saveLaunchAndProposal(ctx, l, p)
}

func (s *ProposalService) applyReplaceCommitteeMember(ctx context.Context, l *launch.Launch, p *proposal.Proposal) error {
	var pl proposal.ReplaceCommitteeMemberPayload
	if err := json.Unmarshal(p.Payload, &pl); err != nil {
		return fmt.Errorf("apply replace committee member: payload: %w", err)
	}
	oldAddr, err := launch.NewOperatorAddress(pl.OldAddress)
	if err != nil {
		return fmt.Errorf("apply replace committee member: invalid old_address: %w", err)
	}
	newAddr, err := launch.NewOperatorAddress(pl.NewAddress)
	if err != nil {
		return fmt.Errorf("apply replace committee member: invalid new_address: %w", err)
	}
	newMember := launch.CommitteeMember{
		Address:   newAddr,
		Moniker:   pl.NewMoniker,
		PubKeyB64: pl.NewPubKey,
	}
	if err := l.ReplaceCommitteeMember(oldAddr, newMember); err != nil {
		return fmt.Errorf("apply replace committee member: %w", err)
	}
	return s.saveLaunchAndProposal(ctx, l, p)
}

func (s *ProposalService) applyReviseGenesis(ctx context.Context, l *launch.Launch, p *proposal.Proposal) error {
	if err := l.ReopenForRevision(); err != nil {
		return fmt.Errorf("apply revise genesis: %w", err)
	}
	if err := s.readiness.InvalidateByLaunch(ctx, l.ID); err != nil {
		return fmt.Errorf("apply revise genesis: invalidate readiness: %w", err)
	}
	return s.saveLaunchAndProposal(ctx, l, p)
}

func (s *ProposalService) applyExpandCommittee(ctx context.Context, l *launch.Launch, p *proposal.Proposal) error {
	var pl proposal.ExpandCommitteePayload
	if err := json.Unmarshal(p.Payload, &pl); err != nil {
		return fmt.Errorf("apply expand committee: payload: %w", err)
	}
	newAddr, err := launch.NewOperatorAddress(pl.NewMember.Address)
	if err != nil {
		return fmt.Errorf("apply expand committee: invalid new_member.address: %w", err)
	}
	newMember := launch.CommitteeMember{
		Address:   newAddr,
		Moniker:   pl.NewMember.Moniker,
		PubKeyB64: pl.NewMember.PubKeyB64,
	}
	effectiveM := ResolveThreshold(l.Committee.ThresholdM, l.Committee.TotalN+1, pl.NewThresholdM)
	if err := s.expirePendingProposals(ctx, l.ID); err != nil {
		return fmt.Errorf("apply expand committee: expire pending proposals: %w", err)
	}
	if err := l.ExpandCommittee(newMember, effectiveM); err != nil {
		return fmt.Errorf("apply expand committee: %w", err)
	}
	return s.saveLaunchAndProposal(ctx, l, p)
}

func (s *ProposalService) applyShrinkCommittee(ctx context.Context, l *launch.Launch, p *proposal.Proposal) error {
	var pl proposal.ShrinkCommitteePayload
	if err := json.Unmarshal(p.Payload, &pl); err != nil {
		return fmt.Errorf("apply shrink committee: payload: %w", err)
	}
	removeAddr, err := launch.NewOperatorAddress(pl.RemoveAddress)
	if err != nil {
		return fmt.Errorf("apply shrink committee: invalid remove_address: %w", err)
	}
	effectiveM := ResolveThreshold(l.Committee.ThresholdM, l.Committee.TotalN-1, pl.NewThresholdM)
	if err := s.expirePendingProposals(ctx, l.ID); err != nil {
		return fmt.Errorf("apply shrink committee: expire pending proposals: %w", err)
	}
	if err := l.ShrinkCommittee(removeAddr, effectiveM); err != nil {
		return fmt.Errorf("apply shrink committee: %w", err)
	}
	return s.saveLaunchAndProposal(ctx, l, p)
}

// expirePendingProposals transitions all PENDING_SIGNATURES proposals for the
// launch to EXPIRED. Called before any committee resize so that in-flight proposals
// (which carry an old threshold) do not outlive the membership change.
func (s *ProposalService) expirePendingProposals(ctx context.Context, launchID uuid.UUID) error {
	return s.proposals.ExpireAllPending(ctx, launchID)
}

// ResolveThreshold returns the effective committee threshold for a resize operation.
// If override is non-nil it is used as-is (the domain validates M < newN).
// Otherwise currentM is clamped to [1, newN-1]: this naturally handles the case
// where the current M would equal or exceed the new N after a shrink.
func ResolveThreshold(currentM, newN int, override *int) int {
	if override != nil {
		return *override
	}
	m := currentM
	if m >= newN {
		m = newN - 1
	}
	if m < 1 {
		m = 1
	}
	return m
}

// applyAndSave wraps all aggregate writes for an executed proposal in a single
// transaction, then dispatches domain events after the transaction commits.
// Events are dispatched outside the transaction so SSE and audit writes do not
// hold a DB connection open.
func (s *ProposalService) applyAndSave(ctx context.Context, l *launch.Launch, p *proposal.Proposal) error {
	if err := s.tx.InTransaction(ctx, func(ctx context.Context) error {
		return s.applyProposal(ctx, l, p)
	}); err != nil {
		return err
	}
	return s.dispatchEvents(ctx, p)
}

// saveLaunchAndProposal persists both the launch and proposal within the current
// transaction scope. Event dispatch is handled by applyAndSave after commit.
func (s *ProposalService) saveLaunchAndProposal(ctx context.Context, l *launch.Launch, p *proposal.Proposal) error {
	if err := s.launches.Save(ctx, l); err != nil {
		return fmt.Errorf("save launch: %w", err)
	}
	if err := s.proposals.Save(ctx, p); err != nil {
		return fmt.Errorf("save proposal: %w", err)
	}
	return nil
}

// dispatchEvents dispatches domain events from an executed proposal to the event
// publisher and audit log, without saving the launch.
// Audit log failures are returned — callers should treat them as hard errors because
// an unlogged action cannot be reconstructed for forensics. The proposal table
// provides a secondary record, but the audit log is the primary tamper-evident trail.
func (s *ProposalService) dispatchEvents(ctx context.Context, p *proposal.Proposal) error {
	for _, ev := range p.PopEvents() {
		s.events.Publish(ev)
		if err := s.writeAudit(ctx, p, ev); err != nil {
			return fmt.Errorf("audit log write failed for event %q on proposal %s: %w", ev.EventName(), p.ID, err)
		}
	}
	return nil
}

func (s *ProposalService) writeAudit(ctx context.Context, p *proposal.Proposal, ev domain.DomainEvent) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return s.audit.Append(ctx, ports.AuditEvent{
		LaunchID:   p.LaunchID.String(),
		EventName:  ev.EventName(),
		OccurredAt: ev.OccurredAt(),
		Payload:    payload,
	})
}

func committeeMemberPubKey(c launch.Committee, addr launch.OperatorAddress) string {
	for _, m := range c.Members {
		if m.Address.Equal(addr) {
			return m.PubKeyB64
		}
	}
	return ""
}

func pendingStatus() *joinrequest.Status {
	s := joinrequest.StatusPending
	return &s
}
