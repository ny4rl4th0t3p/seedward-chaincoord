package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

const attemptCols = `id, launch_id, input_set_hash, issued_at, status, claimed_at, lease_expires_at, runner_id`

// RehearsalAttemptRepository implements ports.RehearsalAttemptRepository for SQLite.
type RehearsalAttemptRepository struct {
	db *sql.DB
}

func NewRehearsalAttemptRepository(db *sql.DB) *RehearsalAttemptRepository {
	return &RehearsalAttemptRepository{db: db}
}

func (r *RehearsalAttemptRepository) GetOrCreate(
	ctx context.Context, launchID uuid.UUID, inputSetHash string, issuedAt time.Time,
) (*launch.RehearsalAttempt, error) {
	q := conn(ctx, r.db)
	// Mint a fresh OPEN attempt; the unique (launch_id, input_set_hash) makes this idempotent.
	_, err := q.ExecContext(ctx, `
		INSERT INTO rehearsal_attempts (id, launch_id, input_set_hash, issued_at, status, runner_id)
		VALUES (?,?,?,?, 'OPEN', '')
		ON CONFLICT(launch_id, input_set_hash) DO NOTHING`,
		uuidToStr(uuid.New()), uuidToStr(launchID), inputSetHash, timeToStr(issuedAt))
	if err != nil {
		return nil, fmt.Errorf("rehearsal attempt get-or-create: %w", err)
	}
	row := q.QueryRowContext(ctx,
		`SELECT `+attemptCols+` FROM rehearsal_attempts WHERE launch_id=? AND input_set_hash=?`,
		uuidToStr(launchID), inputSetHash)
	return scanAttempt(row.Scan)
}

func (r *RehearsalAttemptRepository) FindByID(ctx context.Context, id uuid.UUID) (*launch.RehearsalAttempt, error) {
	q := conn(ctx, r.db)
	row := q.QueryRowContext(ctx, `SELECT `+attemptCols+` FROM rehearsal_attempts WHERE id=?`, uuidToStr(id))
	a, err := scanAttempt(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ports.ErrNotFound
		}
		return nil, err
	}
	return a, nil
}

func (r *RehearsalAttemptRepository) Save(ctx context.Context, a *launch.RehearsalAttempt) error {
	q := conn(ctx, r.db)
	_, err := q.ExecContext(ctx, `
		UPDATE rehearsal_attempts
		SET status=?, claimed_at=?, lease_expires_at=?, runner_id=?
		WHERE id=?`,
		string(a.Status), nullTimeToStr(a.ClaimedAt), nullTimeToStr(a.LeaseExpiresAt), a.RunnerID,
		uuidToStr(a.ID))
	if err != nil {
		return fmt.Errorf("rehearsal attempt save: %w", err)
	}
	return nil
}

func scanAttempt(scan func(dest ...any) error) (*launch.RehearsalAttempt, error) {
	var (
		idStr, launchIDStr, hash, issuedAt, status, runnerID string
		claimedAt, leaseExpiresAt                            *string
	)
	if err := scan(&idStr, &launchIDStr, &hash, &issuedAt, &status, &claimedAt, &leaseExpiresAt, &runnerID); err != nil {
		return nil, err
	}
	id, err := strToUUID(idStr)
	if err != nil {
		return nil, err
	}
	launchID, err := strToUUID(launchIDStr)
	if err != nil {
		return nil, err
	}
	issued, err := strToTime(issuedAt)
	if err != nil {
		return nil, err
	}
	claimed, err := nullStrToTime(claimedAt)
	if err != nil {
		return nil, err
	}
	lease, err := nullStrToTime(leaseExpiresAt)
	if err != nil {
		return nil, err
	}
	return &launch.RehearsalAttempt{
		ID:             id,
		LaunchID:       launchID,
		InputSetHash:   hash,
		IssuedAt:       issued,
		Status:         launch.RehearsalAttemptStatus(status),
		ClaimedAt:      claimed,
		LeaseExpiresAt: lease,
		RunnerID:       runnerID,
	}, nil
}

const resultCols = `id, attempt_id, launch_id, input_set_hash, outcome, failed_step, summary, steps,
	engine_version, binary_name, binary_version, binary_sha256, validators, blocks_advanced,
	started_at, finished_at, service_pubkey, signature, stale, recorded_at`

// RehearsalResultRepository implements ports.RehearsalResultRepository for SQLite.
type RehearsalResultRepository struct {
	db *sql.DB
}

func NewRehearsalResultRepository(db *sql.DB) *RehearsalResultRepository {
	return &RehearsalResultRepository{db: db}
}

func (r *RehearsalResultRepository) Save(ctx context.Context, res *launch.RehearsalResult) error {
	stepsJSON, err := json.Marshal(res.Steps)
	if err != nil {
		return fmt.Errorf("rehearsal result marshal steps: %w", err)
	}
	q := conn(ctx, r.db)
	_, err = q.ExecContext(ctx, `
		INSERT INTO rehearsal_results (
			id, attempt_id, launch_id, input_set_hash, outcome, failed_step, summary, steps,
			engine_version, binary_name, binary_version, binary_sha256, validators, blocks_advanced,
			started_at, finished_at, service_pubkey, signature, stale, recorded_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(signature) DO NOTHING`,
		uuidToStr(res.ID), uuidToStr(res.AttemptID), uuidToStr(res.LaunchID), res.InputSetHash,
		string(res.Outcome), res.FailedStep, res.Summary, string(stepsJSON),
		res.EngineVersion, res.BinaryName, res.BinaryVersion, res.BinarySHA256, res.Validators, res.BlocksAdvanced,
		timeToStr(res.StartedAt), timeToStr(res.FinishedAt), res.ServicePubKey, res.Signature,
		boolToInt(res.Stale), timeToStr(res.RecordedAt))
	if err != nil {
		return fmt.Errorf("rehearsal result save: %w", err)
	}
	return nil
}

func (r *RehearsalResultRepository) FindByLaunch(ctx context.Context, launchID uuid.UUID) ([]*launch.RehearsalResult, error) {
	q := conn(ctx, r.db)
	rows, err := q.QueryContext(ctx,
		`SELECT `+resultCols+` FROM rehearsal_results WHERE launch_id=? ORDER BY recorded_at DESC`,
		uuidToStr(launchID))
	if err != nil {
		return nil, fmt.Errorf("rehearsal results find by launch: %w", err)
	}
	defer rows.Close()

	var out []*launch.RehearsalResult
	for rows.Next() {
		res, err := scanResult(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, rows.Err()
}

func scanResult(scan func(dest ...any) error) (*launch.RehearsalResult, error) {
	var idStr, attemptIDStr, launchIDStr, hash string
	var outcome, failedStep, summary, stepsJSON string
	var engineVersion, binaryName, binaryVersion, binarySHA string
	var validators, blocksAdvanced, staleInt int
	var startedAt, finishedAt, servicePubkey, signature, recordedAt string
	if err := scan(
		&idStr, &attemptIDStr, &launchIDStr, &hash, &outcome, &failedStep, &summary, &stepsJSON,
		&engineVersion, &binaryName, &binaryVersion, &binarySHA, &validators, &blocksAdvanced,
		&startedAt, &finishedAt, &servicePubkey, &signature, &staleInt, &recordedAt,
	); err != nil {
		return nil, err
	}
	id, err := strToUUID(idStr)
	if err != nil {
		return nil, err
	}
	attemptID, err := strToUUID(attemptIDStr)
	if err != nil {
		return nil, err
	}
	launchID, err := strToUUID(launchIDStr)
	if err != nil {
		return nil, err
	}
	var steps []launch.RehearsalResultStep
	if err := json.Unmarshal([]byte(stepsJSON), &steps); err != nil {
		return nil, fmt.Errorf("rehearsal result unmarshal steps: %w", err)
	}
	started, err := strToTime(startedAt)
	if err != nil {
		return nil, err
	}
	finished, err := strToTime(finishedAt)
	if err != nil {
		return nil, err
	}
	recorded, err := strToTime(recordedAt)
	if err != nil {
		return nil, err
	}
	return &launch.RehearsalResult{
		ID:             id,
		AttemptID:      attemptID,
		LaunchID:       launchID,
		InputSetHash:   hash,
		Outcome:        launch.RehearsalOutcome(outcome),
		FailedStep:     failedStep,
		Summary:        summary,
		Steps:          steps,
		EngineVersion:  engineVersion,
		BinaryName:     binaryName,
		BinaryVersion:  binaryVersion,
		BinarySHA256:   binarySHA,
		Validators:     validators,
		BlocksAdvanced: blocksAdvanced,
		StartedAt:      started,
		FinishedAt:     finished,
		ServicePubKey:  servicePubkey,
		Signature:      signature,
		Stale:          staleInt != 0,
		RecordedAt:     recorded,
	}, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
