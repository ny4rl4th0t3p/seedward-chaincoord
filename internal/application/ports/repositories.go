// Package ports defines the interfaces (ports) that the application layer depends on.
// All I/O crosses these boundaries. The domain and application layers have zero
// knowledge of which adapter implements them.
package ports

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/joinrequest"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/proposal"
)

// LaunchRepository persists and retrieves Launch aggregates.
type LaunchRepository interface {
	// Save inserts or updates a Launch. Implementations must use optimistic locking
	// on the version field to prevent lost updates under concurrent writes.
	Save(ctx context.Context, l *launch.Launch) error

	// FindByID returns the Launch with the given ID, or ErrNotFound.
	FindByID(ctx context.Context, id uuid.UUID) (*launch.Launch, error)

	// FindAll returns the launches visible to the given operator address: those where the
	// caller is a committee member or on the launch's members list. An empty operatorAddr
	// (unauthenticated) sees nothing — every launch is private; there is no public kind.
	FindAll(ctx context.Context, operatorAddr string, page, perPage int) ([]*launch.Launch, int, error)

	// FindByChainID returns the Launch with the given chain_id, or ErrNotFound.
	// chain_id is unique per server instance.
	FindByChainID(ctx context.Context, chainID string) (*launch.Launch, error)

	// FindByStatus returns all launches in the given status, regardless of caller visibility.
	// Used by background jobs that need to act on launches regardless of who owns them.
	FindByStatus(ctx context.Context, status launch.Status) ([]*launch.Launch, error)
}

// JoinRequestRepository persists and retrieves JoinRequest aggregates.
type JoinRequestRepository interface {
	Save(ctx context.Context, jr *joinrequest.JoinRequest) error
	FindByID(ctx context.Context, id uuid.UUID) (*joinrequest.JoinRequest, error)

	// FindByLaunch returns all join requests for a launch, optionally filtered by status.
	// Pass nil status to return all.
	FindByLaunch(
		ctx context.Context,
		launchID uuid.UUID,
		status *joinrequest.Status,
		page, perPage int,
	) ([]*joinrequest.JoinRequest, int, error)

	// FindByOperator returns the most recent join request from a given operator
	// for a given launch, or ErrNotFound.
	FindByOperator(ctx context.Context, launchID uuid.UUID, operatorAddr string) (*joinrequest.JoinRequest, error)

	// FindActiveByValidator returns the single ACTIVE (PENDING or APPROVED) join request for a
	// validator in a launch, or ErrNotFound. Used by Submit to decide whether to supersede a
	// PENDING request or reject against a locked APPROVED one. At most one active request
	// can exist per validator (enforced by a partial unique index).
	FindActiveByValidator(ctx context.Context, launchID uuid.UUID, validatorAddr string) (*joinrequest.JoinRequest, error)

	// FindApprovedByLaunch returns all APPROVED join requests for genesis assembly.
	FindApprovedByLaunch(ctx context.Context, launchID uuid.UUID) ([]*joinrequest.JoinRequest, error)

	// AllByLaunch returns every join request for a launch, all statuses, ordered by
	// submitted_at. Unpaginated — used by the submitter-grouped approval read-model,
	// which needs the full set to group and aggregate per actor.
	AllByLaunch(ctx context.Context, launchID uuid.UUID) ([]*joinrequest.JoinRequest, error)

	// CountBySubmitter returns the number of join requests a submitter (the request signer) has
	// made for a launch — the per-submitter rate-limit cap.
	CountBySubmitter(ctx context.Context, launchID uuid.UUID, submitterAddr string) (int, error)

	// CountByConsensusPubKey returns the number of join requests with the given
	// consensus pubkey for a launch (enforces no duplicate consensus pubkey).
	CountByConsensusPubKey(ctx context.Context, launchID uuid.UUID, consensusPubKey string) (int, error)
}

// ProposalRepository persists and retrieves Proposal aggregates.
type ProposalRepository interface {
	Save(ctx context.Context, p *proposal.Proposal) error
	FindByID(ctx context.Context, id uuid.UUID) (*proposal.Proposal, error)

	// FindByLaunch returns all proposals for a launch, with pagination.
	FindByLaunch(ctx context.Context, launchID uuid.UUID, page, perPage int) ([]*proposal.Proposal, int, error)

	// FindPending returns all proposals in PENDING_SIGNATURES status across all launches.
	// Used by the TTL expiry background job.
	FindPending(ctx context.Context) ([]*proposal.Proposal, error)

	// ExpireAllPending transitions every PENDING_SIGNATURES proposal for the given
	// launch to EXPIRED. Called when a committee resize executes, since in-flight
	// proposals carry a threshold that no longer matches the new committee.
	ExpireAllPending(ctx context.Context, launchID uuid.UUID) error
}

// CoordinatorAllowlistEntry is the record type returned by CoordinatorAllowlistRepository.
type CoordinatorAllowlistEntry struct {
	Address string
	AddedBy string
	AddedAt string // RFC3339
}

// CoordinatorAllowlistRepository controls which addresses may create launches
// when launch_policy is "restricted".
type CoordinatorAllowlistRepository interface {
	Add(ctx context.Context, address, addedBy string) error
	Remove(ctx context.Context, address string) error
	Contains(ctx context.Context, address string) (bool, error)
	List(ctx context.Context, page, perPage int) ([]*CoordinatorAllowlistEntry, int, error)
}

// ReadinessRepository persists and retrieves ReadinessConfirmations.
type ReadinessRepository interface {
	Save(ctx context.Context, rc *launch.ReadinessConfirmation) error
	FindByLaunch(ctx context.Context, launchID uuid.UUID) ([]*launch.ReadinessConfirmation, error)
	FindByOperator(ctx context.Context, launchID uuid.UUID, operatorAddr string) (*launch.ReadinessConfirmation, error)

	// InvalidateByLaunch marks all confirmations for a launch as invalidated.
	// Called when genesis time is updated.
	InvalidateByLaunch(ctx context.Context, launchID uuid.UUID) error
}

// RehearsalAttemptRepository persists rehearsal attempts — coordd's record that it served a given
// approved input set for a launch. Attempts are the anti-fabrication anchor for result write-back.
type RehearsalAttemptRepository interface {
	// GetOrCreate returns the attempt for (launchID, inputSetHash), minting a fresh OPEN one at
	// issuedAt if none exists. Identity is (launch, hash), so repeated calls are idempotent.
	GetOrCreate(ctx context.Context, launchID uuid.UUID, inputSetHash string, issuedAt time.Time) (*launch.RehearsalAttempt, error)
	// FindByID returns the attempt, or ErrNotFound.
	FindByID(ctx context.Context, id uuid.UUID) (*launch.RehearsalAttempt, error)
	// Save persists lease-state changes (claim / release / reset) to an existing attempt.
	Save(ctx context.Context, a *launch.RehearsalAttempt) error
}

// RehearsalResultRepository persists signature-verified rehearsal result facts.
type RehearsalResultRepository interface {
	// Save stores a result. It is idempotent on the fact signature — re-submitting the same signed
	// fact must not create a duplicate.
	Save(ctx context.Context, res *launch.RehearsalResult) error
	// FindByLaunch returns a launch's results, newest first (committee read-back).
	FindByLaunch(ctx context.Context, launchID uuid.UUID) ([]*launch.RehearsalResult, error)
}
