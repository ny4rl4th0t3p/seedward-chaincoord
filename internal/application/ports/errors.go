package ports

import (
	"errors"
	"fmt"
)

// Sentinel errors returned by repository implementations.
// Application services check against these using errors.Is.
//
// The base sentinels map directly to an HTTP status in the API layer
// (see writeServiceError). The "specific" sentinels below each WRAP a base
// sentinel: they keep the same HTTP mapping (errors.Is finds the base in the
// chain) while letting callers and tests distinguish the precise condition
// with errors.Is — instead of substring-matching the message.
var (
	ErrNotFound        = errors.New("not found")
	ErrConflict        = errors.New("conflict") // optimistic lock violation or duplicate
	ErrUnauthorized    = errors.New("unauthorized")
	ErrForbidden       = errors.New("forbidden")
	ErrBadRequest      = errors.New("bad request") // client-supplied value failed validation
	ErrTooManyRequests = errors.New("too many requests")
)

// Specific conditions. Each WRAPS one of the base sentinels above, so it keeps
// that sentinel's HTTP mapping (errors.Is finds the base in the chain) while
// letting callers and tests distinguish the precise condition via errors.Is
// instead of substring-matching the message.
var (
	// ErrValidatorAlreadyApproved the validator already has an APPROVED join
	// request, which is locked — changing terms requires revoke → re-submit. (409)
	ErrValidatorAlreadyApproved = fmt.Errorf("validator already has an approved request (revoke it first): %w", ErrConflict)
	// ErrConsensusKeyAlreadyUsed another active request in the launch already
	// claims this consensus pubkey (no two active validators may share one). (409)
	ErrConsensusKeyAlreadyUsed = fmt.Errorf("consensus pubkey already submitted for this launch: %w", ErrConflict)
	// ErrSubmissionCapReached the per-submitter join-request cap is exhausted. (429)
	ErrSubmissionCapReached = fmt.Errorf("join-request submission cap reached for this submitter: %w", ErrTooManyRequests)

	// ErrChallengeMismatch the signed challenge does not match the issued one. (401)
	ErrChallengeMismatch = fmt.Errorf("challenge mismatch: %w", ErrUnauthorized)
	// ErrLaunchNotGenesisReady a readiness action requires the launch to be GENESIS_READY. (409)
	ErrLaunchNotGenesisReady = fmt.Errorf("launch is not in GENESIS_READY status: %w", ErrConflict)
	// ErrJoinRequestNotApproved the operator's join request exists but is not APPROVED. (403)
	ErrJoinRequestNotApproved = fmt.Errorf("join request is not approved: %w", ErrForbidden)
	// ErrReadinessAlreadyConfirmed a valid confirmation already exists for this operator+genesis. (409)
	ErrReadinessAlreadyConfirmed = fmt.Errorf("readiness already confirmed for this operator and genesis version: %w", ErrConflict)
)
