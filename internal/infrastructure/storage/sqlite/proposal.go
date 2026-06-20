package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/proposal"
)

// ProposalRepository implements ports.ProposalRepository for SQLite.
type ProposalRepository struct {
	db *sql.DB
}

func NewProposalRepository(db *sql.DB) *ProposalRepository {
	return &ProposalRepository{db: db}
}

func (r *ProposalRepository) Save(ctx context.Context, p *proposal.Proposal) error {
	q := conn(ctx, r.db)

	_, err := q.ExecContext(ctx, `
		INSERT INTO proposals (id, launch_id, action_type, payload, proposed_by, proposed_at, ttl_expires, status, executed_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			status=excluded.status,
			executed_at=excluded.executed_at`,
		uuidToStr(p.ID), uuidToStr(p.LaunchID),
		string(p.ActionType), string(p.Payload),
		p.ProposedBy.String(),
		timeToStr(p.ProposedAt), timeToStr(p.TTLExpires),
		string(p.Status), nullTimeToStr(p.ExecutedAt),
	)
	if err != nil {
		return fmt.Errorf("proposal save: %w", err)
	}

	// Upsert each signature entry.
	for _, s := range p.Signatures {
		if _, err := q.ExecContext(ctx, `
			INSERT OR IGNORE INTO proposal_signatures (proposal_id, coordinator_address, decision, signed_at, signature)
			VALUES (?,?,?,?,?)`,
			uuidToStr(p.ID), s.CoordinatorAddress.String(),
			string(s.Decision), timeToStr(s.Timestamp), s.Signature.String(),
		); err != nil {
			return fmt.Errorf("proposal save signature: %w", err)
		}
	}
	return nil
}

func (r *ProposalRepository) FindByID(ctx context.Context, id uuid.UUID) (*proposal.Proposal, error) {
	q := conn(ctx, r.db)
	row := q.QueryRowContext(ctx, `SELECT * FROM proposals WHERE id=?`, uuidToStr(id))
	p, err := scanProposal(row.Scan)
	if err != nil {
		return nil, err
	}
	return r.loadSignatures(ctx, p)
}

func (r *ProposalRepository) FindByLaunch(ctx context.Context, launchID uuid.UUID, page, perPage int) ([]*proposal.Proposal, int, error) {
	q := conn(ctx, r.db)
	offset := (page - 1) * perPage

	rows, err := q.QueryContext(ctx,
		`SELECT * FROM proposals WHERE launch_id=? ORDER BY proposed_at DESC LIMIT ? OFFSET ?`,
		uuidToStr(launchID), perPage, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("proposal find by launch: %w", err)
	}
	defer rows.Close()

	proposals, err := r.scanProposalRows(ctx, rows)
	if err != nil {
		return nil, 0, err
	}

	var total int
	if err := q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM proposals WHERE launch_id=?`, uuidToStr(launchID)).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("proposal count: %w", err)
	}
	return proposals, total, nil
}

func (r *ProposalRepository) FindPending(ctx context.Context) ([]*proposal.Proposal, error) {
	q := conn(ctx, r.db)
	rows, err := q.QueryContext(ctx,
		`SELECT * FROM proposals WHERE status='PENDING_SIGNATURES' ORDER BY proposed_at`)
	if err != nil {
		return nil, fmt.Errorf("proposal find pending: %w", err)
	}
	defer rows.Close()
	return r.scanProposalRows(ctx, rows)
}

func (r *ProposalRepository) ExpireAllPending(ctx context.Context, launchID uuid.UUID) error {
	q := conn(ctx, r.db)
	_, err := q.ExecContext(ctx,
		`UPDATE proposals SET status='EXPIRED' WHERE launch_id=? AND status='PENDING_SIGNATURES'`,
		uuidToStr(launchID),
	)
	if err != nil {
		return fmt.Errorf("proposal expire all pending: %w", err)
	}
	return nil
}

func (r *ProposalRepository) loadSignatures(ctx context.Context, p *proposal.Proposal) (*proposal.Proposal, error) {
	q := conn(ctx, r.db)
	rows, err := q.QueryContext(ctx,
		`SELECT coordinator_address, decision, signed_at, signature FROM proposal_signatures WHERE proposal_id=? ORDER BY signed_at`,
		uuidToStr(p.ID))
	if err != nil {
		return nil, fmt.Errorf("load signatures: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var addrStr, decision, signedAt, sigStr string
		if err := rows.Scan(&addrStr, &decision, &signedAt, &sigStr); err != nil {
			return nil, fmt.Errorf("scan signature: %w", err)
		}
		addr, err := launch.NewOperatorAddress(addrStr)
		if err != nil {
			return nil, fmt.Errorf("signature address: %w", err)
		}
		t, err := strToTime(signedAt)
		if err != nil {
			return nil, err
		}
		sig, err := launch.NewSignature(sigStr)
		if err != nil {
			return nil, fmt.Errorf("signature value: %w", err)
		}
		p.Signatures = append(p.Signatures, proposal.SignatureEntry{
			CoordinatorAddress: addr,
			Decision:           proposal.Decision(decision),
			Timestamp:          t,
			Signature:          sig,
		})
	}
	return p, rows.Err()
}

func (r *ProposalRepository) scanProposalRows(ctx context.Context, rows *sql.Rows) ([]*proposal.Proposal, error) {
	// Drain the cursor before loading signatures — each loadSignatures call
	// needs a connection and with MaxOpenConns(1) keeping rows open deadlocks.
	var scanned []*proposal.Proposal
	for rows.Next() {
		p, err := scanProposal(rows.Scan)
		if err != nil {
			return nil, err
		}
		scanned = append(scanned, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	for _, p := range scanned {
		if _, err := r.loadSignatures(ctx, p); err != nil {
			return nil, err
		}
	}
	return scanned, nil
}

func scanProposal(scan func(dest ...any) error) (*proposal.Proposal, error) {
	var (
		idStr, launchIDStr     string
		actionType, payload    string
		proposedBy             string
		proposedAt, ttlExpires string
		status                 string
		executedAt             *string
	)
	err := scan(
		&idStr, &launchIDStr,
		&actionType, &payload, &proposedBy,
		&proposedAt, &ttlExpires, &status, &executedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan proposal: %w", err)
	}

	id, err := strToUUID(idStr)
	if err != nil {
		return nil, err
	}
	launchID, err := strToUUID(launchIDStr)
	if err != nil {
		return nil, err
	}
	pb, err := launch.NewOperatorAddress(proposedBy)
	if err != nil {
		return nil, fmt.Errorf("scan proposed_by: %w", err)
	}
	pa, err := strToTime(proposedAt)
	if err != nil {
		return nil, err
	}
	ttl, err := strToTime(ttlExpires)
	if err != nil {
		return nil, err
	}
	ea, err := nullStrToTime(executedAt)
	if err != nil {
		return nil, err
	}

	p := &proposal.Proposal{
		ID:         id,
		LaunchID:   launchID,
		ActionType: proposal.ActionType(actionType),
		Payload:    []byte(payload),
		ProposedBy: pb,
		ProposedAt: pa,
		TTLExpires: ttl,
		Status:     proposal.Status(status),
		ExecutedAt: ea,
	}
	return p, nil
}
