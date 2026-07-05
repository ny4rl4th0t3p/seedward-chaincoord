package launch

import (
	"time"

	"github.com/google/uuid"
)

// RehearsalAttemptStatus is the lifecycle of a rehearsal attempt (the run lease). Lease
// enforcement (claim-before-run) lands in B3.5; in B3 an attempt is minted OPEN and stays there.
type RehearsalAttemptStatus string

const (
	AttemptOpen    RehearsalAttemptStatus = "OPEN"
	AttemptRunning RehearsalAttemptStatus = "RUNNING"
	AttemptDone    RehearsalAttemptStatus = "DONE"
)

// RehearsalAttempt is coordd's record that it served a specific approved input set for a launch.
// It is the anti-fabrication anchor: coordd only records a result whose input_set_hash it minted an
// attempt for. Identity is (LaunchID, InputSetHash) — the same input set maps to one attempt
// (get-or-create). The lease fields are inert in B3 and enforced in B3.5 (claim-before-run).
type RehearsalAttempt struct {
	ID           uuid.UUID
	LaunchID     uuid.UUID
	InputSetHash string
	IssuedAt     time.Time

	Status         RehearsalAttemptStatus
	ClaimedAt      *time.Time
	LeaseExpiresAt *time.Time
	RunnerID       string
}

// RehearsalOutcome is a result verdict (§4). SKIPPED is informational (the rehearsal service's
// status filter declined to run) — tracked so a console operator sees "skipped: status X excluded",
// not a misleading FAIL.
type RehearsalOutcome string

const (
	OutcomePass    RehearsalOutcome = "PASS"
	OutcomeFail    RehearsalOutcome = "FAIL"
	OutcomeError   RehearsalOutcome = "ERROR"
	OutcomeSkipped RehearsalOutcome = "SKIPPED"
)

// IsValidRehearsalOutcome reports whether s is a recognized outcome.
func IsValidRehearsalOutcome(s RehearsalOutcome) bool {
	switch s {
	case OutcomePass, OutcomeFail, OutcomeError, OutcomeSkipped:
		return true
	default:
		return false
	}
}

// RehearsalResultStep is one step verdict in a stored result fact.
type RehearsalResultStep struct {
	Name   string
	Status string
	Detail string
}

// RehearsalResult is a stored, signature-verified result fact (§4), bound to the attempt it ran
// against. Stale is true when the attempt's input set is no longer the launch's current one — the
// result is genuine (coordd minted the attempt) but the approved inputs have since drifted.
type RehearsalResult struct {
	ID           uuid.UUID
	AttemptID    uuid.UUID
	LaunchID     uuid.UUID
	InputSetHash string

	Outcome    RehearsalOutcome
	FailedStep string
	Summary    string
	Steps      []RehearsalResultStep

	EngineVersion  string
	BinaryName     string
	BinaryVersion  string
	BinarySHA256   string
	Validators     int
	BlocksAdvanced int

	StartedAt  time.Time
	FinishedAt time.Time

	ServicePubKey string
	Signature     string

	Stale      bool
	RecordedAt time.Time
}
