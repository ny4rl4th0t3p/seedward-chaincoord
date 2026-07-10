package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/joinrequest"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// JoinRequestRepository implements ports.JoinRequestRepository for SQLite.
type JoinRequestRepository struct {
	db *sql.DB
}

func NewJoinRequestRepository(db *sql.DB) *JoinRequestRepository {
	return &JoinRequestRepository{db: db}
}

// launchPrefix returns the launch's bech32 prefix, used to canonicalize this launch's
// stored submitter address under it (so join_requests read under one prefix and the
// per-submitter cap counts by account). Returns "" if unknown, in which case the
// address keeps its own display form.
func (r *JoinRequestRepository) launchPrefix(ctx context.Context, launchID uuid.UUID) string {
	var prefix string
	_ = conn(ctx, r.db).QueryRowContext(ctx,
		`SELECT bech32_prefix FROM launches WHERE id=?`, uuidToStr(launchID)).Scan(&prefix)
	return prefix
}

func (r *JoinRequestRepository) Save(ctx context.Context, jr *joinrequest.JoinRequest) error {
	q := conn(ctx, r.db)
	prefix := r.launchPrefix(ctx, jr.LaunchID)
	var approvedBy *string
	if jr.ApprovedByProposal != nil {
		s := jr.ApprovedByProposal.String()
		approvedBy = &s
	}

	_, err := q.ExecContext(ctx, `
		INSERT INTO join_requests (
			id, launch_id, operator_address, consensus_pubkey, gentx_json,
			peer_address, rpc_endpoint, memo, submitted_at, operator_signature,
			status, rejection_reason, approved_by_proposal, self_delegation_amount,
			submitter_address
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			status=excluded.status,
			rejection_reason=excluded.rejection_reason,
			approved_by_proposal=excluded.approved_by_proposal`,
		uuidToStr(jr.ID), uuidToStr(jr.LaunchID),
		jr.OperatorAddress.String(), jr.ConsensusPubKey,
		string(jr.GentxJSON),
		jr.PeerAddress.String(), jr.RPCEndpoint.String(),
		jr.Memo,
		timeToStr(jr.SubmittedAt),
		jr.OperatorSignature.String(),
		string(jr.Status), jr.RejectionReason,
		approvedBy,
		jr.SelfDelegationAmount(),
		underPrefix(jr.SubmitterAddress, prefix),
	)
	if err != nil {
		return fmt.Errorf("join request save: %w", err)
	}
	return nil
}

func (r *JoinRequestRepository) FindByID(ctx context.Context, id uuid.UUID) (*joinrequest.JoinRequest, error) {
	q := conn(ctx, r.db)
	row := q.QueryRowContext(ctx, `SELECT * FROM join_requests WHERE id=?`, uuidToStr(id))
	jr, err := scanJoinRequest(row.Scan)
	if err != nil {
		return nil, err
	}
	return jr, nil
}

func (r *JoinRequestRepository) FindByLaunch(
	ctx context.Context,
	launchID uuid.UUID,
	status *joinrequest.Status,
	page, perPage int,
) ([]*joinrequest.JoinRequest, int, error) {
	q := conn(ctx, r.db)
	offset := (page - 1) * perPage

	var (
		rows  *sql.Rows
		total int
		err   error
	)
	if status == nil {
		rows, err = q.QueryContext(ctx,
			`SELECT * FROM join_requests WHERE launch_id=? ORDER BY submitted_at DESC LIMIT ? OFFSET ?`,
			uuidToStr(launchID), perPage, offset)
	} else {
		rows, err = q.QueryContext(ctx,
			`SELECT * FROM join_requests WHERE launch_id=? AND status=? ORDER BY submitted_at DESC LIMIT ? OFFSET ?`,
			uuidToStr(launchID), string(*status), perPage, offset)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("join request find by launch: %w", err)
	}
	defer rows.Close()

	jrs, err := scanJoinRequestRows(rows)
	if err != nil {
		return nil, 0, err
	}

	if status == nil {
		err = q.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM join_requests WHERE launch_id=?`, uuidToStr(launchID)).Scan(&total)
	} else {
		err = q.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM join_requests WHERE launch_id=? AND status=?`,
			uuidToStr(launchID), string(*status)).Scan(&total)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("join request count: %w", err)
	}
	return jrs, total, nil
}

func (r *JoinRequestRepository) FindByOperator(
	ctx context.Context, launchID uuid.UUID, operatorAddr string,
) (*joinrequest.JoinRequest, error) {
	q := conn(ctx, r.db)
	row := q.QueryRowContext(ctx,
		`SELECT * FROM join_requests WHERE launch_id=? AND operator_address=? ORDER BY submitted_at DESC LIMIT 1`,
		uuidToStr(launchID), operatorAddr)
	return scanJoinRequest(row.Scan)
}

func (r *JoinRequestRepository) FindApprovedByLaunch(ctx context.Context, launchID uuid.UUID) ([]*joinrequest.JoinRequest, error) {
	q := conn(ctx, r.db)
	rows, err := q.QueryContext(ctx,
		`SELECT * FROM join_requests WHERE launch_id=? AND status='APPROVED' ORDER BY submitted_at`,
		uuidToStr(launchID))
	if err != nil {
		return nil, fmt.Errorf("find approved: %w", err)
	}
	defer rows.Close()
	return scanJoinRequestRows(rows)
}

// AllByLaunch returns every join request for a launch (all statuses), ordered by submitted_at.
func (r *JoinRequestRepository) AllByLaunch(ctx context.Context, launchID uuid.UUID) ([]*joinrequest.JoinRequest, error) {
	q := conn(ctx, r.db)
	rows, err := q.QueryContext(ctx,
		`SELECT * FROM join_requests WHERE launch_id=? ORDER BY submitted_at`,
		uuidToStr(launchID))
	if err != nil {
		return nil, fmt.Errorf("join request all by launch: %w", err)
	}
	defer rows.Close()
	return scanJoinRequestRows(rows)
}

// CountBySubmitter counts a launch's join requests by the request signer's
// HRP-independent account (the per-submitter anti-spam cap), so switching bech32
// prefix cannot reset the count. Submitters are stored canonicalized under the launch
// prefix, so the query address is normalized to that same form and matched by index.
func (r *JoinRequestRepository) CountBySubmitter(ctx context.Context, launchID uuid.UUID, submitterAddr string) (int, error) {
	id, err := launch.NewAccountID(submitterAddr)
	if err != nil {
		return 0, fmt.Errorf("count by submitter: %w", err)
	}
	stored := underPrefix(id, r.launchPrefix(ctx, launchID))
	var n int
	err = conn(ctx, r.db).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM join_requests WHERE launch_id=? AND submitter_address=?`,
		uuidToStr(launchID), stored).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count by submitter: %w", err)
	}
	return n, nil
}

// CountByConsensusPubKey counts a launch's ACTIVE (PENDING/APPROVED) join requests with the
// given consensus pubkey. Terminal (REJECTED/EXPIRED) rows are excluded (D4): the consensus key
// guards only the genesis-relevant set, so a rejected validator can re-submit the same key, and a
// superseded request frees its key. Mirrors the partial idx_jr_consensus_pubkey unique index.
func (r *JoinRequestRepository) CountByConsensusPubKey(ctx context.Context, launchID uuid.UUID, consensusPubKey string) (int, error) {
	q := conn(ctx, r.db)
	var n int
	err := q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM join_requests
		 WHERE launch_id=? AND consensus_pubkey=? AND status IN ('PENDING', 'APPROVED')`,
		uuidToStr(launchID), consensusPubKey).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count by consensus pubkey: %w", err)
	}
	return n, nil
}

// FindActiveByValidator returns the single ACTIVE (PENDING or APPROVED) join request for a
// validator in a launch, or ports.ErrNotFound if none. operator_address holds the validator
// identity (migration 0005). At most one active row can exist per validator (the partial
// idx_jr_active_validator unique index), so this is the supersede/lock decision point (D4).
func (r *JoinRequestRepository) FindActiveByValidator(
	ctx context.Context, launchID uuid.UUID, validatorAddr string,
) (*joinrequest.JoinRequest, error) {
	q := conn(ctx, r.db)
	row := q.QueryRowContext(ctx,
		`SELECT * FROM join_requests
		 WHERE launch_id=? AND operator_address=? AND status IN ('PENDING', 'APPROVED')
		 ORDER BY submitted_at DESC LIMIT 1`,
		uuidToStr(launchID), validatorAddr)
	return scanJoinRequest(row.Scan)
}

func scanJoinRequest(scan func(dest ...any) error) (*joinrequest.JoinRequest, error) {
	var (
		idStr, launchIDStr                       string
		operatorAddr, consensusPubKey, gentxJSON string
		peerAddr, rpcEndpoint, memo              string
		submittedAt, operatorSig                 string
		status, rejectionReason                  string
		approvedByProposal                       *string
		selfDelegation                           int64
		submitterAddr                            string
	)
	err := scan(
		&idStr, &launchIDStr,
		&operatorAddr, &consensusPubKey, &gentxJSON,
		&peerAddr, &rpcEndpoint, &memo,
		&submittedAt, &operatorSig,
		&status, &rejectionReason, &approvedByProposal, &selfDelegation,
		&submitterAddr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan join request: %w", err)
	}

	id, err := strToUUID(idStr)
	if err != nil {
		return nil, err
	}
	launchID, err := strToUUID(launchIDStr)
	if err != nil {
		return nil, err
	}
	oa, err := launch.NewAccountID(operatorAddr)
	if err != nil {
		return nil, fmt.Errorf("scan operator address: %w", err)
	}
	pa, err := launch.NewPeerAddress(peerAddr)
	if err != nil {
		return nil, fmt.Errorf("scan peer address: %w", err)
	}
	var rpc launch.RPCEndpoint
	if rpcEndpoint != "" {
		if rpc, err = launch.NewRPCEndpoint(rpcEndpoint); err != nil {
			return nil, fmt.Errorf("scan rpc endpoint: %w", err)
		}
	}
	sa, err := strToTime(submittedAt)
	if err != nil {
		return nil, err
	}
	sig, err := launch.NewSignature(operatorSig)
	if err != nil {
		return nil, fmt.Errorf("scan operator sig: %w", err)
	}
	approvedBy, err := nullStrToUUID(approvedByProposal)
	if err != nil {
		return nil, err
	}
	var submitter launch.AccountID
	if submitterAddr != "" { // empty on pre-0005 / unmigrated rows (POC reset)
		if submitter, err = launch.NewAccountID(submitterAddr); err != nil {
			return nil, fmt.Errorf("scan submitter address: %w", err)
		}
	}

	jr := &joinrequest.JoinRequest{
		ID:                 id,
		LaunchID:           launchID,
		OperatorAddress:    oa,
		SubmitterAddress:   submitter,
		ConsensusPubKey:    consensusPubKey,
		GentxJSON:          []byte(gentxJSON),
		PeerAddress:        pa,
		RPCEndpoint:        rpc,
		Memo:               memo,
		SubmittedAt:        sa,
		OperatorSignature:  sig,
		Status:             joinrequest.Status(status),
		RejectionReason:    rejectionReason,
		ApprovedByProposal: approvedBy,
	}
	return jr, nil
}

func scanJoinRequestRows(rows *sql.Rows) ([]*joinrequest.JoinRequest, error) {
	var out []*joinrequest.JoinRequest
	for rows.Next() {
		jr, err := scanJoinRequest(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, jr)
	}
	return out, rows.Err()
}
