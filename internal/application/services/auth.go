// Package services contain the application-layer use cases.
// Each service orchestrates domain objects and ports — it contains no business rules
// and no I/O implementations.
package services

import (
	"context"
	"fmt"
	"time"

	"github.com/ny4rl4th0t3p/seedward-libs/canonicaljson"
	"github.com/rs/zerolog"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// AuthService handles the challenge-response authentication flow.
type AuthService struct {
	challenges ports.ChallengeStore
	sessions   ports.SessionStore
	nonces     ports.NonceStore
	verifier   ports.SignatureVerifier
	audit      ports.AuditLogWriter
	logger     zerolog.Logger
}

func NewAuthService(
	challenges ports.ChallengeStore,
	sessions ports.SessionStore,
	nonces ports.NonceStore,
	verifier ports.SignatureVerifier,
	audit ports.AuditLogWriter,
) *AuthService {
	return &AuthService{
		challenges: challenges,
		sessions:   sessions,
		nonces:     nonces,
		verifier:   verifier,
		audit:      audit,
		logger:     zerolog.Nop(),
	}
}

// WithLogger sets the logger used to report audit-write failures (defaults to no-op).
func (s *AuthService) WithLogger(l zerolog.Logger) *AuthService {
	s.logger = l
	return s
}

// writeAudit records an audit event under the given scope, logging (not failing) on error — the
// post-commit log-and-continue path, consistent with the other services.
func (s *AuthService) writeAudit(ctx context.Context, scope string, ev domain.DomainEvent) {
	recordAudit(ctx, s.audit, s.logger, scope, ev)
}

// accountKey derives the HRP-independent account key from a presented bech32
// address, rejecting non-account (valoper/valcons) forms. Auth state — challenge
// and nonce — is keyed on this, so the same account under any prefix is one
// identity (cosmos1<h> ≡ network1<h>).
func accountKey(addr string) (string, error) {
	id, err := launch.NewAccountID(addr)
	if err != nil {
		return "", fmt.Errorf("%w: %w", err, ports.ErrBadRequest)
	}
	return id.Hex(), nil
}

// IssueChallenge generates a short-lived challenge string for the given account.
func (s *AuthService) IssueChallenge(ctx context.Context, operatorAddr string) (string, error) {
	acct, err := accountKey(operatorAddr)
	if err != nil {
		return "", err
	}
	return s.challenges.Issue(ctx, acct)
}

// VerifyChallengeInput is the payload the validator signs to authenticate.
//
// # Signing contract (cross-language)
//
// The bytes that are signed are produced by canonicaljson.MarshalForSigning applied to this
// struct. That function strips "signature" and "pubkey_b64" (but KEEPS "nonce", so it is
// bound to the signature for replay protection), then sorts the remaining keys
// lexicographically. The resulting canonical JSON is always:
//
//	{"challenge":"<value>","nonce":"<value>","operator_address":"<value>","timestamp":"<value>"}
//
// The TypeScript web client MUST produce byte-identical output before calling
// signArbitrary. Field order must be: challenge → nonce → operator_address → timestamp.
// No whitespace. Timestamp must be RFC 3339 UTC with second precision (e.g. "2026-01-01T00:00:00Z").
// See the contract test in auth_contract_test.go for a known-good example.
type VerifyChallengeInput struct {
	OperatorAddress string `json:"operator_address"`
	// PubKeyB64 is the caller's secp256k1 compressed public key (33 bytes, base64-encoded).
	// Required so the server can verify the signature — bech32 addresses are hashes
	// and the public key cannot be recovered from them.
	// Stripped before signing — not included in the canonical bytes.
	PubKeyB64 string `json:"pubkey_b64"`
	Challenge string `json:"challenge"`
	// Nonce is included in the signed bytes (replay protection) and consumed once
	// per (operator, nonce) by the nonce store.
	Nonce     string `json:"nonce"`
	Timestamp string `json:"timestamp"`
	// Signature is stripped before signing — not included in the canonical bytes.
	Signature string `json:"signature"`
}

// VerifyChallenge validates a signed challenge response and issues a session token.
func (s *AuthService) VerifyChallenge(ctx context.Context, input VerifyChallengeInput) (token string, err error) {
	// Derive the HRP-independent account key (also rejects valoper/valcons — only
	// account-form addresses may authenticate). Challenge + nonce are keyed on it,
	// so a nonce consumed under one prefix cannot be replayed under another prefix
	// of the same account.
	acct, err := accountKey(input.OperatorAddress)
	if err != nil {
		return "", err
	}

	// Replay protection: consume the nonce (per-account) before anything else.
	if err := s.nonces.Consume(ctx, acct, input.Nonce); err != nil {
		return "", fmt.Errorf("auth: nonce rejected: %w", err)
	}
	if err := validateTimestamp(input.Timestamp); err != nil {
		return "", fmt.Errorf("auth: %w", err)
	}

	// Retrieve and consume the challenge (one-time use, per-account).
	expected, err := s.challenges.Consume(ctx, acct)
	if err != nil {
		return "", fmt.Errorf("auth: challenge not found or expired: %w", err)
	}
	if input.Challenge != expected {
		return "", fmt.Errorf("auth: %w", ports.ErrChallengeMismatch)
	}

	// Verify the signature over canonical JSON of the payload (minus signature field).
	message, err := canonicaljson.MarshalForSigning(input)
	if err != nil {
		return "", fmt.Errorf("auth: failed to produce signing bytes: %w", err)
	}
	sigBytes, err := decodeBase64Sig(input.Signature)
	if err != nil {
		return "", fmt.Errorf("auth: invalid signature encoding: %w", err)
	}
	if err := s.verifier.Verify(input.OperatorAddress, input.PubKeyB64, message, sigBytes); err != nil {
		// Invalid signature is an auth failure (401); the verifier returns a bare
		// error, so attach the sentinel lest it map to 500. Both wrapped (the
		// verifier error carries no competing sentinel, so the status stays 401).
		return "", fmt.Errorf("auth: signature verification failed: %w: %w", err, ports.ErrUnauthorized)
	}

	return s.sessions.Issue(ctx, input.OperatorAddress)
}

// RevokeSession invalidates a session token.
func (s *AuthService) RevokeSession(ctx context.Context, token string) error {
	return s.sessions.Revoke(ctx, token)
}

// RevokeAllSessions invalidates all tokens currently held by the given operator.
// Used by the operator themselves (DELETE /auth/sessions/all) and by admins
// (DELETE /admin/sessions/{address}).
// RevokeAllSessions revokes every session for operatorAddr and records a SessionsRevoked audit
// event. revokedBy is the actor (the account itself for self-service, or an admin).
func (s *AuthService) RevokeAllSessions(ctx context.Context, operatorAddr, revokedBy string) error {
	if err := s.sessions.RevokeAllForOperator(ctx, operatorAddr); err != nil {
		return err
	}
	s.writeAudit(ctx, ports.GlobalAuditScope, domain.SessionsRevoked{
		Account:   auditAccount(operatorAddr),
		RevokedBy: auditAccount(revokedBy),
	}.WithTime(time.Now().UTC()))
	return nil
}

// SessionInfo holds metadata about the caller's current session token.
type SessionInfo struct {
	OperatorAddress string
	ExpiresAt       time.Time
}

// GetSessionInfo returns metadata about the supplied token without consuming it.
func (s *AuthService) GetSessionInfo(ctx context.Context, token string) (SessionInfo, error) {
	// Fence-checked validity (signature + expiry + revocation), consistent with the auth middleware.
	// ParseClaims alone would report a server-side-revoked-but-cryptographically-valid token as valid,
	// so a status endpoint must Validate too — otherwise it lies about a revoked session.
	if _, err := s.sessions.Validate(ctx, token); err != nil {
		return SessionInfo{}, err
	}
	addr, exp, err := s.sessions.ParseClaims(token)
	if err != nil {
		return SessionInfo{}, err
	}
	return SessionInfo{OperatorAddress: addr, ExpiresAt: exp}, nil
}

// ValidateSession checks a session token and returns the operator address.
func (s *AuthService) ValidateSession(ctx context.Context, token string) (string, error) {
	return s.sessions.Validate(ctx, token)
}
