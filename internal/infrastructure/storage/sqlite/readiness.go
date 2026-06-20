package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// ReadinessRepository implements ports.ReadinessRepository for SQLite.
type ReadinessRepository struct {
	db *sql.DB
}

func NewReadinessRepository(db *sql.DB) *ReadinessRepository {
	return &ReadinessRepository{db: db}
}

func (r *ReadinessRepository) Save(ctx context.Context, rc *launch.ReadinessConfirmation) error {
	q := conn(ctx, r.db)
	_, err := q.ExecContext(ctx, `
		INSERT INTO readiness_confirmations (
			id, launch_id, join_request_id, operator_address,
			genesis_hash_confirmed, binary_hash_confirmed,
			confirmed_at, operator_signature, invalidated_at
		) VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET invalidated_at=excluded.invalidated_at`,
		uuidToStr(rc.ID), uuidToStr(rc.LaunchID), uuidToStr(rc.JoinRequestID),
		rc.OperatorAddress.String(),
		rc.GenesisHashConfirmed, rc.BinaryHashConfirmed,
		timeToStr(rc.ConfirmedAt), rc.OperatorSignature.String(),
		nullTimeToStr(rc.InvalidatedAt),
	)
	if err != nil {
		return fmt.Errorf("readiness save: %w", err)
	}
	return nil
}

func (r *ReadinessRepository) FindByLaunch(ctx context.Context, launchID uuid.UUID) ([]*launch.ReadinessConfirmation, error) {
	q := conn(ctx, r.db)
	rows, err := q.QueryContext(ctx,
		`SELECT * FROM readiness_confirmations WHERE launch_id=? ORDER BY confirmed_at`,
		uuidToStr(launchID))
	if err != nil {
		return nil, fmt.Errorf("readiness find by launch: %w", err)
	}
	defer rows.Close()
	return scanReadinessRows(rows)
}

func (r *ReadinessRepository) FindByOperator(
	ctx context.Context, launchID uuid.UUID, operatorAddr string,
) (*launch.ReadinessConfirmation, error) {
	q := conn(ctx, r.db)
	row := q.QueryRowContext(ctx,
		`SELECT * FROM readiness_confirmations WHERE launch_id=? AND operator_address=? ORDER BY confirmed_at DESC LIMIT 1`,
		uuidToStr(launchID), operatorAddr)
	return scanReadiness(row.Scan)
}

func (r *ReadinessRepository) InvalidateByLaunch(ctx context.Context, launchID uuid.UUID) error {
	q := conn(ctx, r.db)
	now := timeToStr(nowUTC())
	_, err := q.ExecContext(ctx,
		`UPDATE readiness_confirmations SET invalidated_at=? WHERE launch_id=? AND invalidated_at IS NULL`,
		now, uuidToStr(launchID))
	if err != nil {
		return fmt.Errorf("readiness invalidate: %w", err)
	}
	return nil
}

func scanReadiness(scan func(dest ...any) error) (*launch.ReadinessConfirmation, error) {
	var (
		idStr, launchIDStr, jrIDStr string
		operatorAddr                string
		genesisHash, binaryHash     string
		confirmedAt, operatorSig    string
		invalidatedAt               *string
	)
	err := scan(
		&idStr, &launchIDStr, &jrIDStr, &operatorAddr,
		&genesisHash, &binaryHash,
		&confirmedAt, &operatorSig, &invalidatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan readiness: %w", err)
	}

	id, err := strToUUID(idStr)
	if err != nil {
		return nil, err
	}
	launchID, err := strToUUID(launchIDStr)
	if err != nil {
		return nil, err
	}
	jrID, err := strToUUID(jrIDStr)
	if err != nil {
		return nil, err
	}
	oa, err := launch.NewOperatorAddress(operatorAddr)
	if err != nil {
		return nil, fmt.Errorf("readiness operator address: %w", err)
	}
	ca, err := strToTime(confirmedAt)
	if err != nil {
		return nil, err
	}
	sig, err := launch.NewSignature(operatorSig)
	if err != nil {
		return nil, fmt.Errorf("readiness signature: %w", err)
	}
	ia, err := nullStrToTime(invalidatedAt)
	if err != nil {
		return nil, err
	}

	return &launch.ReadinessConfirmation{
		ID:                   id,
		LaunchID:             launchID,
		JoinRequestID:        jrID,
		OperatorAddress:      oa,
		GenesisHashConfirmed: genesisHash,
		BinaryHashConfirmed:  binaryHash,
		ConfirmedAt:          ca,
		OperatorSignature:    sig,
		InvalidatedAt:        ia,
	}, nil
}

func scanReadinessRows(rows *sql.Rows) ([]*launch.ReadinessConfirmation, error) {
	var out []*launch.ReadinessConfirmation
	for rows.Next() {
		rc, err := scanReadiness(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, rc)
	}
	return out, rows.Err()
}
