// Package ports defines the interfaces (ports) that the application layer depends on.
// All I/O crosses these boundaries. The domain and application layers have zero
// knowledge of which adapter implements them.
package ports

import (
	"context"

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

	// FindAll returns all launches visible to the given operator address.
	// An empty operatorAddr represents an unauthenticated caller — only PUBLIC launches
	// are returned. Authenticated callers also receive ALLOWLIST launches they appear on.
	FindAll(ctx context.Context, operatorAddr string, page, perPage int) ([]*launch.Launch, int, error)

	// FindByChainID returns the Launch with the given chain_id, or ErrNotFound.
	// chain_id is unique per server instance.
	FindByChainID(ctx context.Context, chainID string) (*launch.Launch, error)

	// FindByStatus returns all launches in the given status, across all visibilities.
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
	// PENDING request or reject against a locked APPROVED one (D4). At most one active request
	// can exist per validator (enforced by a partial unique index).
	FindActiveByValidator(ctx context.Context, launchID uuid.UUID, validatorAddr string) (*joinrequest.JoinRequest, error)

	// FindApprovedByLaunch returns all APPROVED join requests for genesis assembly.
	FindApprovedByLaunch(ctx context.Context, launchID uuid.UUID) ([]*joinrequest.JoinRequest, error)

	// CountBySubmitter returns the number of join requests a submitter (the request signer) has
	// made for a launch — the per-submitter rate-limit cap.
	CountBySubmitter(ctx context.Context, launchID uuid.UUID, submitterAddr string) (int, error)

	// CountByConsensusPubKey returns the number of join requests with the given
	// consensus pubkey for a launch (spec §2.4: no duplicate consensus pubkey).
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
