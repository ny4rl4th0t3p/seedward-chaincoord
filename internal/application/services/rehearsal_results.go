package services

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-libs/canonicaljson"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

var (
	// ErrRehearsalSignatureInvalid the fact's Ed25519 signature does not verify against the
	// launch's trusted rehearsal service pubkey. (401)
	ErrRehearsalSignatureInvalid = fmt.Errorf("rehearsal result signature does not verify: %w", ports.ErrUnauthorized)
	// ErrRehearsalNoTrustedKey the launch has no (valid) trusted rehearsal service pubkey, so no
	// result can be accepted for it. (409)
	ErrRehearsalNoTrustedKey = fmt.Errorf("launch has no valid trusted rehearsal service key: %w", ports.ErrConflict)
)

// RehearsalResultFact is the inbound, signed result fact. Its JSON tags MUST match
// seedward-rehearsal's bridge.ResultFact field-for-field: the Ed25519 signature is over
// canonicaljson.MarshalForSigning of this exact shape (signature stripped), so any drift breaks
// verification. Pinned by the result-fact drift golden on both sides.
type RehearsalResultFact struct {
	SchemaVersion int     `json:"schema_version"`
	LaunchID      string  `json:"launch_id"`
	InputSetHash  string  `json:"input_set_hash"`
	AttemptID     *string `json:"attempt_id"`

	Outcome    string                    `json:"outcome"`
	FailedStep *string                   `json:"failed_step"`
	Summary    string                    `json:"summary"`
	Steps      []RehearsalResultFactStep `json:"steps"`

	Rehearsal RehearsalResultFactMeta `json:"rehearsal"`

	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`

	ServicePubkey string `json:"service_pubkey"`
	Signature     string `json:"signature"`
}

// RehearsalResultFactStep is one step verdict in the fact.
type RehearsalResultFactStep struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// RehearsalResultFactMeta is the fact's "rehearsal" block (what actually ran).
type RehearsalResultFactMeta struct {
	EngineVersion  string `json:"engine_version"`
	BinaryName     string `json:"binary_name"`
	BinaryVersion  string `json:"binary_version"`
	BinarySHA256   string `json:"binary_sha256"`
	Validators     int    `json:"validators"`
	BlocksAdvanced int    `json:"blocks_advanced"`
}

// RecordRehearsalResult verifies and stores a signed rehearsal result fact (bridge write-back).
//   - the fact's signature must verify against the launch's trusted service pubkey;
//   - the fact must reference an attempt coordd itself minted for this launch, whose input set
//     matches the fact's — the anti-fabrication lock (coordd never records a hash it did not serve);
//   - stale = the attempt's input set is no longer the launch's current one (stored, flagged).
//
// The binding vouches the input set was genuine; it does NOT vouch the PASS/FAIL verdict, which is
// trusted by the signature. Idempotent on the fact signature.
func (s *LaunchService) RecordRehearsalResult(
	ctx context.Context, launchID uuid.UUID, fact RehearsalResultFact,
) (*launch.RehearsalResult, error) {
	const op = "record rehearsal result"
	l, err := s.launches.FindByID(ctx, launchID)
	if err != nil {
		return nil, err
	}

	if err := s.verifyFactSignature(l, fact); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	// Idempotency: the same authentic signed fact is a no-op re-POST.
	if existing, err := s.findResultBySignature(ctx, launchID, fact.Signature); err == nil && existing != nil {
		return existing, nil
	}

	if !launch.IsValidRehearsalOutcome(launch.RehearsalOutcome(fact.Outcome)) {
		return nil, fmt.Errorf("%s: unknown outcome %q: %w", op, fact.Outcome, ports.ErrBadRequest)
	}

	attempt, err := s.resolveAttempt(ctx, launchID, fact)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	current, err := s.hasher.Current(ctx, l)
	if err != nil {
		return nil, fmt.Errorf("%s: current hash: %w", op, err)
	}
	stale := attempt.InputSetHash != current

	res := factToResult(launchID, attempt.ID, fact, stale, time.Now().UTC())
	if err := s.results.Save(ctx, res); err != nil {
		return nil, fmt.Errorf("%s: save: %w", op, err)
	}

	// Recording the result finishes the run — release the claim lease.
	attempt.Release()
	if err := s.attempts.Save(ctx, attempt); err != nil {
		return nil, fmt.Errorf("%s: release attempt: %w", op, err)
	}

	ev := domain.RehearsalResultRecorded{
		LaunchID:     launchID,
		AttemptID:    attempt.ID,
		InputSetHash: fact.InputSetHash,
		Outcome:      fact.Outcome,
		Stale:        stale,
	}.WithTime(res.RecordedAt)
	s.events.Publish(ev)
	_ = s.writeAudit(ctx, launchID.String(), ev)

	return res, nil
}

// verifyFactSignature checks the fact's Ed25519 signature against the launch's trusted service
// pubkey (NOT the self-declared service_pubkey on the fact). Mirrors the daemon's signing exactly:
// canonicaljson strips "signature", the remaining canonical bytes are Ed25519-verified.
func (*LaunchService) verifyFactSignature(l *launch.Launch, fact RehearsalResultFact) error {
	if l.RehearsalServicePubKey == "" {
		return ErrRehearsalNoTrustedKey
	}
	pub, err := base64.StdEncoding.DecodeString(l.RehearsalServicePubKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return ErrRehearsalNoTrustedKey
	}
	sig, err := base64.StdEncoding.DecodeString(fact.Signature)
	if err != nil {
		return ErrRehearsalSignatureInvalid
	}
	msg, err := canonicaljson.MarshalForSigning(&fact)
	if err != nil {
		return fmt.Errorf("canonicalize fact: %w", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), msg, sig) {
		return ErrRehearsalSignatureInvalid
	}
	return nil
}

// resolveAttempt enforces the anti-fabrication lock: the fact must reference an attempt coordd
// minted for this launch, whose input set matches the fact's input_set_hash.
func (s *LaunchService) resolveAttempt(
	ctx context.Context, launchID uuid.UUID, fact RehearsalResultFact,
) (*launch.RehearsalAttempt, error) {
	if fact.AttemptID == nil || *fact.AttemptID == "" {
		return nil, fmt.Errorf("attempt_id is required: %w", ports.ErrBadRequest)
	}
	attemptID, err := uuid.Parse(*fact.AttemptID)
	if err != nil {
		return nil, fmt.Errorf("attempt_id is not a uuid: %w", ports.ErrBadRequest)
	}
	attempt, err := s.attempts.FindByID(ctx, attemptID)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return nil, fmt.Errorf("unknown attempt %s — coordd never served this input set: %w", attemptID, ports.ErrBadRequest)
		}
		return nil, fmt.Errorf("load attempt: %w", err)
	}
	if attempt.LaunchID != launchID {
		return nil, fmt.Errorf("attempt %s belongs to a different launch: %w", attemptID, ports.ErrBadRequest)
	}
	if attempt.InputSetHash != fact.InputSetHash {
		return nil, fmt.Errorf("input_set_hash does not match the referenced attempt: %w", ports.ErrBadRequest)
	}
	return attempt, nil
}

func (s *LaunchService) findResultBySignature(
	ctx context.Context, launchID uuid.UUID, signature string,
) (*launch.RehearsalResult, error) {
	existing, err := s.results.FindByLaunch(ctx, launchID)
	if err != nil {
		return nil, err
	}
	for _, r := range existing {
		if r.Signature == signature {
			return r, nil
		}
	}
	return nil, nil
}

func factToResult(
	launchID, attemptID uuid.UUID, fact RehearsalResultFact, stale bool, recordedAt time.Time,
) *launch.RehearsalResult {
	steps := make([]launch.RehearsalResultStep, len(fact.Steps))
	for i, st := range fact.Steps {
		steps[i] = launch.RehearsalResultStep{Name: st.Name, Status: st.Status, Detail: st.Detail}
	}
	failedStep := ""
	if fact.FailedStep != nil {
		failedStep = *fact.FailedStep
	}
	return &launch.RehearsalResult{
		ID:             uuid.New(),
		AttemptID:      attemptID,
		LaunchID:       launchID,
		InputSetHash:   fact.InputSetHash,
		Outcome:        launch.RehearsalOutcome(fact.Outcome),
		FailedStep:     failedStep,
		Summary:        fact.Summary,
		Steps:          steps,
		EngineVersion:  fact.Rehearsal.EngineVersion,
		BinaryName:     fact.Rehearsal.BinaryName,
		BinaryVersion:  fact.Rehearsal.BinaryVersion,
		BinarySHA256:   fact.Rehearsal.BinarySHA256,
		Validators:     fact.Rehearsal.Validators,
		BlocksAdvanced: fact.Rehearsal.BlocksAdvanced,
		StartedAt:      parseRFC3339OrZero(fact.StartedAt),
		FinishedAt:     parseRFC3339OrZero(fact.FinishedAt),
		ServicePubKey:  fact.ServicePubkey,
		Signature:      fact.Signature,
		Stale:          stale,
		RecordedAt:     recordedAt,
	}
}

// ListRehearsalResults returns a launch's recorded rehearsal results, newest first — the committee
// read-back (governance plane). Committee members only: 404 if the launch does not exist, 403 if the
// caller is authenticated but not a committee member.
func (s *LaunchService) ListRehearsalResults(
	ctx context.Context,
	launchID uuid.UUID,
	callerAddr string,
) ([]*launch.RehearsalResult, error) {
	const op = "list rehearsal results"
	if _, err := s.requireCommittee(ctx, launchID, callerAddr, op); err != nil {
		return nil, err
	}
	return s.results.FindByLaunch(ctx, launchID)
}

// ResetRehearsalAttempt force-releases a stuck run lease back to OPEN — a coordinator override for
// when a runner crashed mid-run (before the lease TTL expires). Committee-gated (governance plane).
func (s *LaunchService) ResetRehearsalAttempt(ctx context.Context, launchID, attemptID uuid.UUID, callerAddr string) error {
	const op = "reset rehearsal attempt"
	l, err := s.requireCommittee(ctx, launchID, callerAddr, op)
	if err != nil {
		return err
	}
	attempt, err := s.attempts.FindByID(ctx, attemptID)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if attempt.LaunchID != l.ID {
		return fmt.Errorf("%s: attempt belongs to a different launch: %w", op, ports.ErrNotFound)
	}
	attempt.Reset()
	if err := s.attempts.Save(ctx, attempt); err != nil {
		return fmt.Errorf("%s: save: %w", op, err)
	}
	ev := domain.RehearsalAttemptReset{LaunchID: l.ID, AttemptID: attemptID, ResetBy: callerAddr}.WithTime(time.Now().UTC())
	s.events.Publish(ev)
	_ = s.writeAudit(ctx, l.ID.String(), ev)
	return nil
}

func parseRFC3339OrZero(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
