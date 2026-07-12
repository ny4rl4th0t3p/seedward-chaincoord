package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-libs/canonicaljson"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/joinrequest"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/proposal"
)

const (
	defaultProposalTTL = 48 * time.Hour
	pendingJRLimit     = 1000 // max pending join requests to expire in a single close-window call
)

// ProposalService orchestrates the lifecycle of coordinator proposals.
// It is the central dispatcher: when a proposal executes, this service applies
// the side effects to the affected aggregates and dispatches domain events.
type ProposalService struct {
	launches      ports.LaunchRepository
	joinRequests  ports.JoinRequestRepository
	proposals     ports.ProposalRepository
	readiness     ports.ReadinessRepository
	nonces        ports.NonceStore
	verifier      ports.SignatureVerifier
	events        ports.EventPublisher
	audit         ports.AuditLogWriter
	tx            ports.Transactor
	hasher        *InputSetHasher
	rehearsalGate launch.RehearsalGateMode        // Part B: off (default) | advisory | required
	results       ports.RehearsalResultRepository // latest rehearsal fact for the gate; nil when off
}

// WithRehearsalGate returns a copy of the service configured with the opt-in rehearsal gate (Part B).
// mode is one of off|advisory|required (already validated at config load; an unrecognized value falls
// back to off). results supplies the latest rehearsal fact for advisory/required evaluation.
func (s *ProposalService) WithRehearsalGate(mode string, results ports.RehearsalResultRepository) *ProposalService {
	cp := *s
	cp.rehearsalGate, _ = launch.ParseRehearsalGateMode(mode)
	cp.results = results
	return &cp
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
		hasher:       NewInputSetHasher(joinRequests),
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

	coordAddr, err := launch.NewAccountID(input.CoordinatorAddr)
	if err != nil {
		return nil, fmt.Errorf("raise proposal: coordinator address: %w: %w", err, ports.ErrBadRequest)
	}
	if !l.Committee.HasMember(coordAddr) {
		return nil, fmt.Errorf("raise proposal: %s is not a committee member: %w", input.CoordinatorAddr, ports.ErrForbidden)
	}

	// Find the committee member's pubkey for verification.
	pubKeyB64 := committeeMemberPubKey(l.Committee, coordAddr)
	if err := s.verifier.Verify(input.CoordinatorAddr, pubKeyB64, message, sigBytes); err != nil {
		// Invalid signature is an auth failure (401); the verifier returns a bare error.
		return nil, fmt.Errorf("raise proposal: signature invalid: %w: %w", err, ports.ErrUnauthorized)
	}

	// Genesis-finalization guards (Part A): keep the published genesis consistent with the approved
	// set — reject a raise that would create or lock in an inconsistency.
	if err := s.guardFinalizationRaise(ctx, l, input.ActionType); err != nil {
		return nil, err
	}

	sig, err := launch.NewSignature(input.Signature)
	if err != nil {
		return nil, fmt.Errorf("raise proposal: signature value: %w: %w", err, ports.ErrBadRequest)
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
		return nil, fmt.Errorf("raise proposal: %w: %w", err, ports.ErrBadRequest)
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

	coordAddr, err := launch.NewAccountID(input.CoordinatorAddr)
	if err != nil {
		return nil, fmt.Errorf("sign proposal: coordinator address: %w: %w", err, ports.ErrBadRequest)
	}
	if !l.Committee.HasMember(coordAddr) {
		return nil, fmt.Errorf("sign proposal: %w", ports.ErrForbidden)
	}

	pubKeyB64 := committeeMemberPubKey(l.Committee, coordAddr)
	if err := s.verifier.Verify(input.CoordinatorAddr, pubKeyB64, message, sigBytes); err != nil {
		// Invalid signature is an auth failure (401); the verifier returns a bare error.
		return nil, fmt.Errorf("sign proposal: signature invalid: %w: %w", err, ports.ErrUnauthorized)
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
		return nil, fmt.Errorf("sign proposal: signature value: %w: %w", err, ports.ErrBadRequest)
	}

	if err := p.Sign(coordAddr, input.Decision, sig, l.Committee.ThresholdM, time.Now()); err != nil {
		return nil, mapProposalDomainErr("sign proposal", err)
	}

	if p.Status == proposal.StatusExecuted {
		if err := s.applyAndSave(ctx, l, p); err != nil {
			return nil, err
		}
		return p, nil
	}

	// A veto of an APPROVE_ALLOCATION_FILE proposal rejects the bound file (REJECTED
	// + AllocationFileRejected event). Every other vetoed action has no side effect.
	if p.Status == proposal.StatusVetoed && p.ActionType == proposal.ActionApproveAllocationFile {
		if err := s.applyAllocationVeto(ctx, l, p); err != nil {
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

	case proposal.ActionApproveAllocationFile:
		return s.applyApproveAllocationFile(ctx, l, p)

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

// mapJoinRequestDomainErr maps the joinrequest aggregate's lifecycle sentinel to the
// matching client-facing sentinel so an executed proposal that hits an invalid
// join-request transition (e.g. approving an already-approved request) renders a 409
// rather than a 500. Used at the proposal apply boundary (jr.Approve/Reject/Revoke).
func mapJoinRequestDomainErr(op string, err error) error {
	if errors.Is(err, joinrequest.ErrInvalidJoinRequestStatus) {
		return fmt.Errorf("%s: %w: %w", op, err, ports.ErrConflict)
	}
	return fmt.Errorf("%s: %w", op, err)
}

// mapProposalDomainErr maps the proposal aggregate's signing-guard sentinels to the
// matching client-facing sentinel. A not-pending, TTL-expired, or already-signed
// proposal is a state conflict (409). Used at the Sign boundary (p.Sign).
func mapProposalDomainErr(op string, err error) error {
	switch {
	case errors.Is(err, proposal.ErrProposalNotPending),
		errors.Is(err, proposal.ErrProposalTTLExpired),
		errors.Is(err, proposal.ErrCoordinatorAlreadySigned):
		return fmt.Errorf("%s: %w: %w", op, err, ports.ErrConflict)
	default:
		return fmt.Errorf("%s: %w", op, err)
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
		return mapJoinRequestDomainErr("apply approve validator", err)
	}

	// Record voting power and check 33% warning.
	// The address was validated when the proposal was raised; this error is unexpected.
	operatorAddr, err := launch.NewAccountID(pl.OperatorAddress)
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
		return mapJoinRequestDomainErr("apply reject validator", err)
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
		return mapJoinRequestDomainErr("apply remove validator", err)
	}

	operatorAddr, err := launch.NewAccountID(pl.OperatorAddress)
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
		return fmt.Errorf("apply publish chain record: initial genesis has not been uploaded: %w", ports.ErrConflict)
	}
	if pl.InitialGenesisHash != l.InitialGenesisSHA256 {
		return fmt.Errorf("apply publish chain record: attested genesis hash %q does not match uploaded hash %q: %w",
			pl.InitialGenesisHash, l.InitialGenesisSHA256, ports.ErrBadRequest)
	}
	if err := l.Publish(pl.InitialGenesisHash); err != nil {
		return mapLaunchDomainErr("apply publish chain record", err)
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
		return mapLaunchDomainErr("apply close window", err)
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
	// Consistency (Part A): publish exactly the uploaded genesis, and only if it still matches the
	// current approved set (which can drift between raise and quorum, or via a raise-time race —
	// this execute-time re-check is the hard floor that closes that window).
	if pl.GenesisHash != l.FinalGenesisSHA256 {
		return fmt.Errorf("apply publish genesis: proposal hash %q does not match the uploaded final genesis: %w: %w",
			pl.GenesisHash, launch.ErrGenesisHashMismatch, ports.ErrConflict)
	}
	if err := s.checkGenesisFresh(ctx, l); err != nil {
		return fmt.Errorf("apply publish genesis: %w", err)
	}
	// Part B: required-gate re-check at execute (safety net against a raise-time race). Advisory is
	// already recorded at raise, so only required re-blocks here.
	if s.rehearsalGate == launch.RehearsalGateRequired {
		if err := s.gateRehearsal(ctx, l); err != nil {
			return fmt.Errorf("apply publish genesis: %w", err)
		}
	}
	if err := l.PublishGenesis(pl.GenesisHash); err != nil {
		return mapLaunchDomainErr("apply publish genesis", err)
	}
	return s.saveLaunchAndProposal(ctx, l, p)
}

// guardFinalizationRaise (Part A) rejects a raise that would break genesis↔approved-set consistency.
// Runs after auth so a doomed proposal never enters the signing flow:
//   - PUBLISH_GENESIS: the uploaded genesis must still match the current set (freshness), and no
//     set-mutating proposal may be pending (freeze).
//   - APPROVE_VALIDATOR / REMOVE_APPROVED_VALIDATOR: no genesis publication may be pending (freeze).
//
// The freeze is bidirectional, so the two kinds can never be pending at once. applyPublishGenesis's
// execute-time re-check remains the hard floor against a concurrent-raise race.
func (s *ProposalService) guardFinalizationRaise(ctx context.Context, l *launch.Launch, action proposal.ActionType) error {
	// if (not switch) so we only reason about the three actions that touch consistency — the rest are
	// unaffected (and it keeps the exhaustive linter out of an intentionally partial enum match).
	if action == proposal.ActionPublishGenesis {
		if err := s.checkGenesisFresh(ctx, l); err != nil {
			return fmt.Errorf("raise proposal: cannot publish genesis: %w", err)
		}
		pending, err := s.hasPending(ctx, l.ID, proposal.ActionApproveValidator, proposal.ActionRemoveApprovedValidator)
		if err != nil {
			return fmt.Errorf("raise proposal: %w", err)
		}
		if pending {
			return fmt.Errorf(
				"raise proposal: cannot publish genesis while a validator approve/remove proposal is pending — resolve it first: %w: %w",
				launch.ErrGenesisPublishInProgress, ports.ErrConflict)
		}
		// Part B: opt-in rehearsal gate (off by default → no-op).
		if err := s.gateRehearsal(ctx, l); err != nil {
			return fmt.Errorf("raise proposal: %w", err)
		}
		return nil
	}
	if action == proposal.ActionApproveValidator || action == proposal.ActionRemoveApprovedValidator {
		pending, err := s.hasPending(ctx, l.ID, proposal.ActionPublishGenesis)
		if err != nil {
			return fmt.Errorf("raise proposal: %w", err)
		}
		if pending {
			return fmt.Errorf("raise proposal: cannot change the validator set while a genesis publication is pending — veto it first: %w: %w",
				launch.ErrGenesisPublishInProgress, ports.ErrConflict)
		}
	}
	return nil
}

// checkGenesisFresh verifies the uploaded final genesis still matches the launch's current approved
// input set. Shared by the raise guard and the execute-time re-check.
func (s *ProposalService) checkGenesisFresh(ctx context.Context, l *launch.Launch) error {
	if l.FinalGenesisInputSetHash == "" {
		return fmt.Errorf("no final genesis has been uploaded: %w: %w", launch.ErrGenesisStale, ports.ErrConflict)
	}
	current, err := s.hasher.Current(ctx, l)
	if err != nil {
		return fmt.Errorf("input-set hash: %w", err)
	}
	if current != l.FinalGenesisInputSetHash {
		return fmt.Errorf("the approved set changed since the final genesis was assembled — re-upload the final genesis: %w: %w",
			launch.ErrGenesisStale, ports.ErrConflict)
	}
	return nil
}

// hasPending reports whether the launch has a PENDING_SIGNATURES proposal of the given action
// types. Pending proposals are few (they execute or expire), so scanning FindPending is inexpensive and —
// unlike a paginated per-launch scan — cannot miss one.
func (s *ProposalService) hasPending(ctx context.Context, launchID uuid.UUID, actions ...proposal.ActionType) (bool, error) {
	pending, err := s.proposals.FindPending(ctx)
	if err != nil {
		return false, fmt.Errorf("check pending proposals: %w", err)
	}
	for _, p := range pending {
		if p.LaunchID != launchID {
			continue
		}
		for _, a := range actions {
			if p.ActionType == a {
				return true, nil
			}
		}
	}
	return false, nil
}

// gateRehearsal (Part B) enforces the opt-in rehearsal gate for a PUBLISH_GENESIS. off → no-op;
// required → error unless the latest rehearsal is a current PASS (and a rehearsal service is
// configured for this launch); advisory → records an audit event but never blocks.
func (s *ProposalService) gateRehearsal(ctx context.Context, l *launch.Launch) error {
	if s.rehearsalGate != launch.RehearsalGateAdvisory && s.rehearsalGate != launch.RehearsalGateRequired {
		return nil // off / unset — standalone coordd is untouched
	}
	if s.rehearsalGate == launch.RehearsalGateRequired && l.RehearsalServicePubKey == "" {
		return fmt.Errorf("%w: %w", launch.ErrRehearsalGateNoService, ports.ErrConflict)
	}
	latest, err := s.latestRehearsal(ctx, l.ID)
	if err != nil {
		return fmt.Errorf("rehearsal gate: %w", err)
	}
	current, err := s.hasher.Current(ctx, l)
	if err != nil {
		return fmt.Errorf("rehearsal gate: input-set hash: %w", err)
	}
	ok, reason := launch.EvaluateRehearsalReady(latest, current)
	if ok {
		return nil
	}
	if s.rehearsalGate == launch.RehearsalGateRequired {
		return fmt.Errorf("%s: %w: %w", reason, launch.ErrRehearsalGateUnsatisfied, ports.ErrConflict)
	}
	s.auditRehearsalGate(ctx, l.ID, reason) // advisory: record, never block
	return nil
}

// latestRehearsal returns the newest stored rehearsal fact for a launch, or nil if none / gate off.
func (s *ProposalService) latestRehearsal(ctx context.Context, launchID uuid.UUID) (*launch.RehearsalResult, error) {
	if s.results == nil {
		return nil, nil
	}
	results, err := s.results.FindByLaunch(ctx, launchID)
	if err != nil {
		return nil, fmt.Errorf("fetch rehearsal results: %w", err)
	}
	if len(results) == 0 {
		return nil, nil
	}
	return results[0], nil
}

// auditRehearsalGate records an advisory gate miss (never blocks).
func (s *ProposalService) auditRehearsalGate(ctx context.Context, launchID uuid.UUID, reason string) {
	if s.audit == nil {
		return
	}
	ev := domain.RehearsalGateNotSatisfied{LaunchID: launchID, Reason: reason}.WithTime(time.Now())
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_ = s.audit.Append(ctx, ports.AuditEvent{
		LaunchID:   launchID.String(),
		EventName:  ev.EventName(),
		OccurredAt: ev.OccurredAt(),
		Payload:    payload,
	})
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

func (s *ProposalService) applyApproveAllocationFile(ctx context.Context, l *launch.Launch, p *proposal.Proposal) error {
	var pl proposal.ApproveAllocationFilePayload
	if err := json.Unmarshal(p.Payload, &pl); err != nil {
		return fmt.Errorf("apply approve allocation file: payload: %w", err)
	}
	// Binds approval to the file's current hash; a stale hash (file re-uploaded since the
	// proposal was raised) or a missing file fails here, rolling back the transaction.
	if err := l.ApproveAllocationFile(launch.AllocationType(pl.Type), pl.Hash, p.ID); err != nil {
		return mapAllocationDomainErr("apply approve allocation file", err)
	}
	// The AllocationFileApproved domain event is emitted by the proposal aggregate on
	// execution and dispatched (publish + audit) by applyAndSave after commit.
	return s.saveLaunchAndProposal(ctx, l, p)
}

// applyAllocationVeto handles a vetoed APPROVE_ALLOCATION_FILE proposal: it marks the
// bound file REJECTED (if it still matches the proposed hash) and records the
// AllocationFileRejected event. A stale veto (file re-uploaded since the proposal was
// raised, or never uploaded) leaves the file untouched and only persists the vetoed
// proposal — the veto itself always stands. A veto is not an execution, so the domain's
// emitExecutionEvents does not fire; the app records the AllocationFileRejected event on the
// proposal and dispatches it through the standard dispatchEvents path.
func (s *ProposalService) applyAllocationVeto(ctx context.Context, l *launch.Launch, p *proposal.Proposal) error {
	var pl proposal.ApproveAllocationFilePayload
	if err := json.Unmarshal(p.Payload, &pl); err != nil {
		return fmt.Errorf("apply allocation veto: payload: %w", err)
	}

	if !l.RejectAllocationFile(launch.AllocationType(pl.Type), pl.Hash) {
		// Nothing to reject (stale or absent); just persist the vetoed proposal.
		if err := s.proposals.Save(ctx, p); err != nil {
			return fmt.Errorf("apply allocation veto: save proposal: %w", err)
		}
		return nil
	}

	if err := s.tx.InTransaction(ctx, func(ctx context.Context) error {
		return s.saveLaunchAndProposal(ctx, l, p)
	}); err != nil {
		return err
	}

	p.RecordEvent(domain.AllocationFileRejected{
		LaunchID:       l.ID,
		AllocationType: pl.Type,
		SHA256:         pl.Hash,
	}.WithTime(time.Now().UTC()))
	if err := s.dispatchEvents(ctx, p); err != nil {
		return fmt.Errorf("apply allocation veto: %w", err)
	}
	return nil
}

func (s *ProposalService) applyReplaceCommitteeMember(ctx context.Context, l *launch.Launch, p *proposal.Proposal) error {
	var pl proposal.ReplaceCommitteeMemberPayload
	if err := json.Unmarshal(p.Payload, &pl); err != nil {
		return fmt.Errorf("apply replace committee member: payload: %w", err)
	}
	oldAddr, err := launch.NewAccountID(pl.OldAddress)
	if err != nil {
		return fmt.Errorf("apply replace committee member: invalid old_address: %w", err)
	}
	newAddr, err := launch.NewAccountID(pl.NewAddress)
	if err != nil {
		return fmt.Errorf("apply replace committee member: invalid new_address: %w", err)
	}
	newMember := launch.CommitteeMember{
		Address:   newAddr,
		Moniker:   pl.NewMoniker,
		PubKeyB64: pl.NewPubKey,
	}
	oldMembers := committeeMemberAddrs(l.Committee)
	oldM := l.Committee.ThresholdM
	if err := l.ReplaceCommitteeMember(oldAddr, newMember); err != nil {
		return mapLaunchDomainErr("apply replace committee member", err)
	}
	p.RecordEvent(domain.CommitteeMemberReplaced{
		LaunchID:      l.ID,
		OldAddress:    oldAddr.String(),
		NewAddress:    newAddr.String(),
		OldMembers:    oldMembers,
		NewMembers:    committeeMemberAddrs(l.Committee),
		OldThresholdM: oldM,
		NewThresholdM: l.Committee.ThresholdM,
	}.WithTime(time.Now().UTC()))
	return s.saveLaunchAndProposal(ctx, l, p)
}

func (s *ProposalService) applyReviseGenesis(ctx context.Context, l *launch.Launch, p *proposal.Proposal) error {
	if err := l.ReopenForRevision(); err != nil {
		return mapLaunchDomainErr("apply revise genesis", err)
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
	newAddr, err := launch.NewAccountID(pl.NewMember.Address)
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
	oldMembers := committeeMemberAddrs(l.Committee)
	oldM := l.Committee.ThresholdM
	if err := l.ExpandCommittee(newMember, effectiveM); err != nil {
		return mapLaunchDomainErr("apply expand committee", err)
	}
	p.RecordEvent(domain.CommitteeExpanded{
		LaunchID:      l.ID,
		AddedAddress:  newAddr.String(),
		OldMembers:    oldMembers,
		NewMembers:    committeeMemberAddrs(l.Committee),
		OldThresholdM: oldM,
		NewThresholdM: l.Committee.ThresholdM,
	}.WithTime(time.Now().UTC()))
	return s.saveLaunchAndProposal(ctx, l, p)
}

func (s *ProposalService) applyShrinkCommittee(ctx context.Context, l *launch.Launch, p *proposal.Proposal) error {
	var pl proposal.ShrinkCommitteePayload
	if err := json.Unmarshal(p.Payload, &pl); err != nil {
		return fmt.Errorf("apply shrink committee: payload: %w", err)
	}
	removeAddr, err := launch.NewAccountID(pl.RemoveAddress)
	if err != nil {
		return fmt.Errorf("apply shrink committee: invalid remove_address: %w", err)
	}
	effectiveM := ResolveThreshold(l.Committee.ThresholdM, l.Committee.TotalN-1, pl.NewThresholdM)
	if err := s.expirePendingProposals(ctx, l.ID); err != nil {
		return fmt.Errorf("apply shrink committee: expire pending proposals: %w", err)
	}
	oldMembers := committeeMemberAddrs(l.Committee)
	oldM := l.Committee.ThresholdM
	if err := l.ShrinkCommittee(removeAddr, effectiveM); err != nil {
		return mapLaunchDomainErr("apply shrink committee", err)
	}
	p.RecordEvent(domain.CommitteeShrunk{
		LaunchID:       l.ID,
		RemovedAddress: removeAddr.String(),
		OldMembers:     oldMembers,
		NewMembers:     committeeMemberAddrs(l.Committee),
		OldThresholdM:  oldM,
		NewThresholdM:  l.Committee.ThresholdM,
	}.WithTime(time.Now().UTC()))
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

// committeeMemberAddrs returns the committee members' account addresses (display form) in
// membership order — used to snapshot committee state in governance audit events.
func committeeMemberAddrs(c launch.Committee) []string {
	addrs := make([]string, len(c.Members))
	for i, m := range c.Members {
		addrs[i] = m.Address.String()
	}
	return addrs
}

func committeeMemberPubKey(c launch.Committee, addr launch.AccountID) string {
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
