package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// RehearsalLeasedError is returned by ClaimRehearsalRun when a different runner already holds an
// unexpired lease on the run. It maps to 409 and carries the current holder for the response body.
type RehearsalLeasedError struct {
	RunnerID       string
	ClaimedAt      time.Time
	LeaseExpiresAt time.Time
}

func (e *RehearsalLeasedError) Error() string {
	return fmt.Sprintf("rehearsal run already claimed by %q until %s",
		e.RunnerID, e.LeaseExpiresAt.Format(time.RFC3339))
}

func (*RehearsalLeasedError) Unwrap() error { return ports.ErrConflict }

// PreviewRehearsalInput assembles the rehearsal input for a launch WITHOUT minting an attempt or
// acquiring a lease — a read-only "what would be rehearsed" view (GET rehearsal-input). Its
// AttemptID is empty; a runner must ClaimRehearsalRun to obtain one. Returns ErrNotFound if the
// launch does not exist.
func (s *LaunchService) PreviewRehearsalInput(ctx context.Context, launchID uuid.UUID) (*RehearsalInput, error) {
	l, err := s.launches.FindByID(ctx, launchID)
	if err != nil {
		return nil, err
	}
	return s.hasher.Assemble(ctx, l)
}

// ClaimRehearsalRun is the run entry point (POST rehearsal-claim): it assembles the input, mints
// (get-or-create) the attempt for its input_set_hash, and acquires the run lease for runnerID —
// returning the input + attempt_id. If a different runner already holds an unexpired lease on the
// same input set, it returns *RehearsalLeasedError (409). The lease auto-expires after the TTL, so a
// crashed runner self-heals; a committee member can also ResetRehearsalAttempt for an immediate override.
func (s *LaunchService) ClaimRehearsalRun(ctx context.Context, launchID uuid.UUID, runnerID string) (*RehearsalInput, error) {
	const op = "claim rehearsal run"
	l, err := s.launches.FindByID(ctx, launchID)
	if err != nil {
		return nil, err
	}
	in, err := s.hasher.Assemble(ctx, l)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	attempt, err := s.attempts.GetOrCreate(ctx, l.ID, in.InputSetHash, now)
	if err != nil {
		return nil, fmt.Errorf("%s: attempt: %w", op, err)
	}
	if err := attempt.Claim(runnerID, now, now.Add(s.rehearsalLeaseTTL)); err != nil {
		if errors.Is(err, launch.ErrAttemptLeased) {
			return nil, &RehearsalLeasedError{
				RunnerID:       attempt.RunnerID,
				ClaimedAt:      derefTime(attempt.ClaimedAt),
				LeaseExpiresAt: derefTime(attempt.LeaseExpiresAt),
			}
		}
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	if err := s.attempts.Save(ctx, attempt); err != nil {
		return nil, fmt.Errorf("%s: save: %w", op, err)
	}
	in.AttemptID = attempt.ID
	s.emit(ctx, l.ID.String(), domain.RehearsalRunClaimed{
		LaunchID:  l.ID,
		AttemptID: attempt.ID,
		RunnerID:  runnerID,
	})
	return in, nil
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
