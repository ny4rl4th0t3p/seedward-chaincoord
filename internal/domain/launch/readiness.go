package launch

import (
	"time"

	"github.com/google/uuid"
)

// ReadinessConfirmation records a validator's self-attestation that their node
// is prepared with the correct binary and genesis file.
// It belongs to the Launch aggregate — it has no independent lifecycle.
type ReadinessConfirmation struct {
	ID              uuid.UUID
	LaunchID        uuid.UUID
	JoinRequestID   uuid.UUID
	OperatorAddress AccountID

	// The SHA256 hash the validator claims their local genesis file matches.
	GenesisHashConfirmed string
	// The SHA256 hash the validator claims their local binary matches.
	BinaryHashConfirmed string

	ConfirmedAt       time.Time
	OperatorSignature Signature

	// InvalidatedAt is set when a genesis time update occurs after this confirmation.
	// Validators must re-confirm against the new genesis hash.
	InvalidatedAt *time.Time
}

// IsValid reports whether this confirmation is still valid (not invalidated).
func (r ReadinessConfirmation) IsValid() bool {
	return r.InvalidatedAt == nil
}

// Invalidate marks this confirmation as stale due to a genesis update.
func (r *ReadinessConfirmation) Invalidate(at time.Time) {
	r.InvalidatedAt = &at
}
