package ports

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ny4rl4th0t3p/seedward-libs/gentxvalidate"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain"
)

// Transactor runs fn inside a single database transaction.
// If fn returns an error the transaction is rolled back; otherwise it is committed.
// Repositories that receive a context carrying an active transaction must use it
// rather than acquiring a new connection from the pool — this ensures all writes
// in a single applyProposal call are atomic.
type Transactor interface {
	InTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}

// AuditLogWriter appends a signed entry to the immutable audit log.
// Implementations must be append-only — existing entries must never be modified.
type AuditLogWriter interface {
	Append(ctx context.Context, event AuditEvent) error
}

// AuditEvent is a single entry in the audit log.
type AuditEvent struct {
	// LaunchID scopes the event to a specific launch for per-launch filtering.
	LaunchID string `json:"launch_id"`
	// EventName matches domain.DomainEvent.EventName()
	EventName  string    `json:"event_name"`
	OccurredAt time.Time `json:"occurred_at"`
	// Payload is the canonical JSON of the domain event, stored as raw JSON
	// so audit entries are human-readable without base64 decoding.
	Payload json.RawMessage `json:"payload" swaggertype:"object"`
	// Signature is a base64 Ed25519 signature over the canonical JSON of this entry
	// (excluding the signature field itself), signed by the server's audit key.
	Signature string `json:"signature"`
	// PrevHash is the SHA-256 hex digest of the previous entry's raw JSON bytes
	// (the full marshaled line, signature included). Empty for the first entry.
	// Included in the signature of the current entry so it cannot be silently altered.
	PrevHash string `json:"prev_hash,omitempty"`
}

// GlobalAuditScope is the sentinel LaunchID for audit events not scoped to a launch — admin-plane
// actions (coordinator allowlist changes, session revocations). Such events ride the same
// tamper-evident hash chain; ReadForLaunch(GlobalAuditScope) returns them.
const GlobalAuditScope = "global"

// AuditLogReader reads entries from the immutable audit log.
type AuditLogReader interface {
	// ReadForLaunch returns all audit events for the given launch ID, in append order.
	ReadForLaunch(ctx context.Context, launchID string) ([]AuditEvent, error)
}

// AuditChainStore persists the SHA-256 chain tip (hash of the last written line)
// so the audit log hash chain survives server restarts. This closes the gap where
// log lines deleted between restarts would otherwise go undetected.
type AuditChainStore interface {
	LoadPrevHash(ctx context.Context) (string, error)
	SavePrevHash(ctx context.Context, hash string) error
}

// EventPublisher dispatches domain events to SSE subscribers and any other
// in-process listeners. It is non-blocking — slow subscribers are dropped.
type EventPublisher interface {
	Publish(event domain.DomainEvent)
}

// SessionStore issues and validates short-lived JWT session tokens for authenticated
// operators (validators, committee members, and coordinators).
type SessionStore interface {
	// Issue creates a new session token for the given operator address.
	Issue(ctx context.Context, operatorAddr string) (token string, err error)

	// Validate checks a token and returns the operator address it was issued to.
	// Returns ErrUnauthorized if the token is invalid or expired.
	Validate(ctx context.Context, token string) (operatorAddr string, err error)

	// Revoke invalidates a session token immediately.
	Revoke(ctx context.Context, token string) error

	// RevokeAllForOperator sets a revocation fence so all tokens issued before
	// now for the given operator address are rejected on next Validate call.
	RevokeAllForOperator(ctx context.Context, operatorAddr string) error

	// ParseClaims extracts the operator address and expiry from a token without
	// performing a database revocation check. Used by session-info endpoints.
	ParseClaims(token string) (operatorAddr string, expiresAt time.Time, err error)
}

// ChallengeStore manages short-lived authentication challenges.
type ChallengeStore interface {
	// Issue creates and stores a challenge for the given operator address.
	// The challenge is valid for a short TTL (default 5 minutes).
	Issue(ctx context.Context, operatorAddr string) (challenge string, err error)

	// Consume retrieves and deletes a challenge for the given operator address.
	// Returns ErrNotFound if no challenge exists or has expired.
	Consume(ctx context.Context, operatorAddr string) (challenge string, err error)
}

// ChallengeRateLimiter enforces per-operator request limits for challenge issuance.
// Implementations are responsible for both checking and recording each attempt.
type ChallengeRateLimiter interface {
	// Allow checks whether operatorAddr is within the rate limit and records the
	// attempt. Returns ErrTooManyRequests if the limit has been exceeded.
	Allow(ctx context.Context, operatorAddr string) error
}

// NonceStore tracks used nonces for replay protection.
// Nonces are stored with a TTL and rejected if seen again within that window.
type NonceStore interface {
	// Consume records a nonce as used. Returns ErrConflict if the nonce was already seen.
	Consume(ctx context.Context, operatorAddr, nonce string) error
}

// GentxValidationOutcome is the result of running the gentx invariant set over a
// single gentx. ConsensusPubKeyB64 and ValidatorAddress are populated only when every
// result passed.
type GentxValidationOutcome struct {
	Results            []gentxvalidate.Result
	ConsensusPubKeyB64 string
	// ValidatorAddress is the validator's self-delegation account address (account form,
	// derived from the verified gentx signer) — the genesis-relevant validator identity.
	ValidatorAddress string
}

// GentxValidator runs the shared gentxvalidate server invariant set over a gentx.
// Implemented by an infrastructure adapter wrapping the library, so the
// pure domain stays free of the validation/SDK weight, and there is one
// authoritative implementation shared by coordd, the CLI, and the WASM validator.
type GentxValidator interface {
	Validate(gentxJSON []byte, p gentxvalidate.Params) GentxValidationOutcome
}

// GentxInvalidError reports that a gentx failed one or more invariants. It is a
// bad request; the HTTP layer renders the per-invariant results so the submitter
// sees exactly which invariant failed and why.
type GentxInvalidError struct {
	Results []gentxvalidate.Result
}

func (e *GentxInvalidError) Error() string {
	var failed []string
	for _, r := range e.Results {
		if !r.OK {
			failed = append(failed, r.Invariant)
		}
	}
	return "gentx validation failed: " + strings.Join(failed, ", ")
}

// Unwrap lets callers treat a gentx-invalid failure as a bad request via
// errors.Is(err, ErrBadRequest) while errors.As recovers the per-invariant detail.
func (*GentxInvalidError) Unwrap() error { return ErrBadRequest }

// SignatureVerifier verifies signatures against public keys.
type SignatureVerifier interface {
	// Verify checks that sig is a valid signature over message by the key
	// associated with operatorAddr. The pubKeyB64 hint may be used if the verifier
	// needs it; pass empty string to let the verifier resolve it from the address.
	Verify(operatorAddr, pubKeyB64 string, message, sig []byte) error
}

// StoredFileRef describes how a stored file (a genesis or allocation file) is
// served. It is returned by both GenesisStore and AllocationStore.
// Exactly one of ExternalURL or LocalPath will be non-empty.
type StoredFileRef struct {
	// ExternalURL is set in attestor mode (Option A): the file lives at an
	// external URL and the server redirects clients there.
	ExternalURL string
	// SHA256 is the hex-encoded SHA-256 hash of the file contents.
	SHA256 string
	// LocalPath is set in host mode (Option C): the file is stored on the
	// local filesystem at this path.
	LocalPath string
}

// GenesisStore manages genesis file storage (initial and final).
// Two storage modes are supported:
//
//   - Option A (attestor): a committee member publishes the genesis file to their
//     own infrastructure and registers the URL + hash here. Clients are
//     redirected to the external URL. No file bytes are stored on this server.
//   - Option C (host): a committee member uploads the raw file; this server stores
//     it on disk and serves it directly. Must be explicitly enabled via config.
type GenesisStore interface {
	// Option C — store raw genesis bytes (host mode).
	SaveInitial(ctx context.Context, launchID string, data []byte) error
	SaveFinal(ctx context.Context, launchID string, data []byte) error

	// Option A — store an external URL reference (attestor mode).
	SaveInitialRef(ctx context.Context, launchID, url, sha256 string) error
	SaveFinalRef(ctx context.Context, launchID, url, sha256 string) error

	// GetInitialRef returns how to serve the initial genesis file.
	// Returns ErrNotFound if neither a ref nor a file has been stored.
	GetInitialRef(ctx context.Context, launchID string) (*StoredFileRef, error)

	// GetFinalRef returns how to serve the final genesis file.
	// Returns ErrNotFound if neither a ref nor a file has been stored.
	GetFinalRef(ctx context.Context, launchID string) (*StoredFileRef, error)
}

// AllocationStore manages curated allocation-file storage (one file per allocation
// type per launch). It mirrors GenesisStore's dual-mode design and reuses StoredFileRef
// to describe how a stored file is served:
//
//   - host mode: the committee uploads raw bytes; this server stores them on disk
//     and serves them directly.
//   - attestor mode: the committee registers an external URL + sha256; clients are
//     redirected to the URL and no bytes are stored here.
//
// allocType is one of the fixed launch.AllocationType values.
type AllocationStore interface {
	// Save stores raw allocation-file bytes (host mode).
	Save(ctx context.Context, launchID, allocType string, data []byte) error

	// SaveRef records an external URL reference (attestor mode).
	SaveRef(ctx context.Context, launchID, allocType, url, sha256 string) error

	// GetRef returns how to serve the file of the given type, or ErrNotFound.
	GetRef(ctx context.Context, launchID, allocType string) (*StoredFileRef, error)
}
