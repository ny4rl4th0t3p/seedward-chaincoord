package launch

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Sentinel errors for allocation-file operations. Callers (tests and the API layer)
// match these with errors.Is to distinguish failure kinds — e.g. the handler maps
// ErrAllocationStaleHash/ErrAllocationLocked to 409, ErrAllocationNotFound to 404,
// and ErrUnknownAllocationType/ErrAllocationEmptyHash to 400.
var (
	ErrUnknownAllocationType = errors.New("unknown allocation type")
	ErrAllocationEmptyHash   = errors.New("allocation file hash must not be empty")
	ErrAllocationLocked      = errors.New("allocation files are frozen in the current status")
	ErrAllocationNotFound    = errors.New("no allocation file of that type")
	ErrAllocationStaleHash   = errors.New("allocation file hash mismatch")
)

// AllocationType identifies a curated allocation file that the committee governs
// independently. There is at most one file of each type per launch. The set is
// fixed; a committee member produces chain-shaped JSON for each (the same data that
// would otherwise be hand-edited into genesis), and the committee approves whole
// files rather than individual entries.
type AllocationType string

const (
	AllocationAccounts AllocationType = "accounts"
	AllocationClaims   AllocationType = "claims"
	AllocationGrants   AllocationType = "grants"
	AllocationAuthz    AllocationType = "authz"
	AllocationFeegrant AllocationType = "feegrant"
)

// ValidAllocationType reports whether t is one of the fixed allocation types.
func ValidAllocationType(t AllocationType) bool {
	switch t {
	case AllocationAccounts, AllocationClaims, AllocationGrants, AllocationAuthz, AllocationFeegrant:
		return true
	default:
		return false
	}
}

// AllocationFileStatus is the governance state of a single allocation file.
//
//	PENDING  → uploaded, awaiting committee approval (also the state after a re-upload).
//	APPROVED → an APPROVE_ALLOCATION_FILE proposal reached quorum on this exact hash.
//	REJECTED → an APPROVE_ALLOCATION_FILE proposal on this exact hash was vetoed.
type AllocationFileStatus string

const (
	AllocationPending  AllocationFileStatus = "PENDING"
	AllocationApproved AllocationFileStatus = "APPROVED"
	AllocationRejected AllocationFileStatus = "REJECTED"
)

// AllocationFile is the governance metadata for one curated allocation file. The
// bytes (or attestor ref) live in the AllocationStore; this entity tracks only the
// hash and approval state. Approval binds to SHA256, so any re-upload (new hash)
// invalidates a prior approval and resets the file to PENDING.
type AllocationFile struct {
	Type               AllocationType
	SHA256             string
	Status             AllocationFileStatus
	ApprovedByProposal *uuid.UUID // set if Status == APPROVED
	UploadedAt         time.Time
}
