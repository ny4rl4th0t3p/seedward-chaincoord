package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// LaunchRepository implements ports.LaunchRepository for SQLite.
type LaunchRepository struct {
	db *sql.DB
}

func NewLaunchRepository(db *sql.DB) *LaunchRepository {
	return &LaunchRepository{db: db}
}

// Save inserts or replaces a Launch and its committee, members list (allowlist), and allocation files.
// Optimistic locking: the UPDATE increments version and checks the expected value;
// if no row is affected the caller's snapshot is stale → ErrConflict.
func (r *LaunchRepository) Save(ctx context.Context, l *launch.Launch) error {
	q := conn(ctx, r.db)

	// Attempt UPDATE first (optimistic lock).
	res, err := q.ExecContext(ctx, `
		UPDATE launches SET
			chain_id=?, chain_name=?, bech32_prefix=?, binary_name=?, binary_version=?, binary_sha256=?,
			repo_url=?, repo_commit=?, genesis_time=?, denom=?, min_self_delegation=?,
			max_commission_rate=?, max_commission_change_rate=?,
			gentx_deadline=?, min_validator_count=?,
			launch_type=?, status=?,
			initial_genesis_sha256=?, final_genesis_sha256=?,
			monitor_rpc_url=?,
			total_supply=?, rehearsal_service_pubkey=?, rehearsal_endpoint=?,
			final_genesis_input_set_hash=?,
			updated_at=?, version=version+1
		WHERE id=? AND version=?`,
		l.Record.ChainID, l.Record.ChainName, l.Record.Bech32Prefix, l.Record.BinaryName,
		l.Record.BinaryVersion, l.Record.BinarySHA256,
		l.Record.RepoURL, l.Record.RepoCommit,
		nullTimeToStr(l.Record.GenesisTime),
		l.Record.Denom, l.Record.MinSelfDelegation,
		l.Record.MaxCommissionRate.String(), l.Record.MaxCommissionChangeRate.String(),
		timeToStr(l.Record.GentxDeadline),
		l.Record.MinValidatorCount,
		string(l.LaunchType), string(l.Status),
		l.InitialGenesisSHA256, l.FinalGenesisSHA256,
		l.MonitorRPCURL,
		l.Record.TotalSupply, l.RehearsalServicePubKey, l.RehearsalEndpoint,
		l.FinalGenesisInputSetHash,
		timeToStr(l.UpdatedAt),
		uuidToStr(l.ID), l.Version,
	)
	if err != nil {
		return fmt.Errorf("launch save update: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("launch save rows affected: %w", err)
	}

	if n == 0 {
		// No row matched — either new record or stale version.
		// Try INSERT; if it fails with a unique constraint, it was a stale update.
		if err := r.insert(ctx, l); err != nil {
			return ports.ErrConflict
		}
	}

	// Re-sync committee, allowlist, and allocation files (idempotent replace).
	if err := r.saveCommittee(ctx, l); err != nil {
		return err
	}
	if err := r.saveAllowlist(ctx, l); err != nil {
		return err
	}
	return r.saveAllocationFiles(ctx, l)
}

func (r *LaunchRepository) insert(ctx context.Context, l *launch.Launch) error {
	q := conn(ctx, r.db)
	_, err := q.ExecContext(ctx, `
		INSERT INTO launches (
			id, chain_id, chain_name, bech32_prefix, binary_name, binary_version, binary_sha256,
			repo_url, repo_commit, genesis_time, denom, min_self_delegation,
			max_commission_rate, max_commission_change_rate,
			gentx_deadline, min_validator_count,
			launch_type, status,
			initial_genesis_sha256, final_genesis_sha256,
			monitor_rpc_url,
			total_supply, rehearsal_service_pubkey, rehearsal_endpoint,
			final_genesis_input_set_hash,
			created_at, updated_at, version
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0)`,
		uuidToStr(l.ID),
		l.Record.ChainID, l.Record.ChainName, l.Record.Bech32Prefix, l.Record.BinaryName,
		l.Record.BinaryVersion, l.Record.BinarySHA256,
		l.Record.RepoURL, l.Record.RepoCommit,
		nullTimeToStr(l.Record.GenesisTime),
		l.Record.Denom, l.Record.MinSelfDelegation,
		l.Record.MaxCommissionRate.String(), l.Record.MaxCommissionChangeRate.String(),
		timeToStr(l.Record.GentxDeadline),
		l.Record.MinValidatorCount,
		string(l.LaunchType), string(l.Status),
		l.InitialGenesisSHA256, l.FinalGenesisSHA256,
		l.MonitorRPCURL,
		l.Record.TotalSupply, l.RehearsalServicePubKey, l.RehearsalEndpoint,
		l.FinalGenesisInputSetHash,
		timeToStr(l.CreatedAt), timeToStr(l.UpdatedAt),
	)
	return err
}

// underPrefix renders an account under the given bech32 prefix for launch-scoped
// storage, so all of a launch's stored addresses read under its own prefix. Falls
// back to the address's display form if the prefix is empty or encoding fails.
func underPrefix(a launch.AccountID, prefix string) string {
	if prefix != "" {
		if s, err := a.Bech32(prefix); err == nil {
			return s
		}
	}
	return a.String()
}

func (r *LaunchRepository) saveCommittee(ctx context.Context, l *launch.Launch) error {
	q := conn(ctx, r.db)
	c := l.Committee

	_, err := q.ExecContext(ctx, `
		INSERT OR REPLACE INTO committees (id, launch_id, threshold_m, total_n, lead_address, creation_signature, created_at)
		VALUES (?,?,?,?,?,?,?)`,
		uuidToStr(c.ID), uuidToStr(l.ID),
		c.ThresholdM, c.TotalN,
		underPrefix(c.LeadAddress, l.Record.Bech32Prefix), c.CreationSignature.String(),
		timeToStr(c.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("save committee: %w", err)
	}

	// Replace all members atomically.
	if _, err := q.ExecContext(ctx, `DELETE FROM committee_members WHERE committee_id=?`, uuidToStr(c.ID)); err != nil {
		return fmt.Errorf("delete committee members: %w", err)
	}
	for i, m := range c.Members {
		if _, err := q.ExecContext(ctx,
			`INSERT INTO committee_members (committee_id, position, address, account, moniker, pubkey_b64) VALUES (?,?,?,?,?,?)`,
			uuidToStr(c.ID), i, underPrefix(m.Address, l.Record.Bech32Prefix), m.Address.Hex(), m.Moniker, m.PubKeyB64,
		); err != nil {
			return fmt.Errorf("insert committee member %d: %w", i, err)
		}
	}
	return nil
}

func (r *LaunchRepository) saveAllowlist(ctx context.Context, l *launch.Launch) error {
	q := conn(ctx, r.db)
	if _, err := q.ExecContext(ctx, `DELETE FROM allowlist WHERE launch_id=?`, uuidToStr(l.ID)); err != nil {
		return fmt.Errorf("delete allowlist: %w", err)
	}
	for _, mem := range l.Allowlist.Members() {
		addedAt := ""
		if !mem.AddedAt.IsZero() {
			addedAt = timeToStr(mem.AddedAt)
		}
		if _, err := q.ExecContext(ctx,
			`INSERT INTO allowlist (launch_id, address, account, label, added_by, added_at) VALUES (?,?,?,?,?,?)`,
			uuidToStr(l.ID), underPrefix(mem.Address, l.Record.Bech32Prefix), mem.Address.Hex(), mem.Label, mem.AddedBy, addedAt,
		); err != nil {
			return fmt.Errorf("insert allowlist: %w", err)
		}
	}
	return nil
}

func (r *LaunchRepository) FindByID(ctx context.Context, id uuid.UUID) (*launch.Launch, error) {
	q := conn(ctx, r.db)
	row := q.QueryRowContext(ctx, `SELECT * FROM launches WHERE id=?`, uuidToStr(id))
	l, err := r.scanLaunch(row)
	if err != nil {
		return nil, err
	}
	return r.hydrate(ctx, l)
}

func (r *LaunchRepository) FindByChainID(ctx context.Context, chainID string) (*launch.Launch, error) {
	q := conn(ctx, r.db)
	row := q.QueryRowContext(ctx, `SELECT * FROM launches WHERE chain_id=?`, chainID)
	l, err := r.scanLaunch(row)
	if err != nil {
		return nil, err
	}
	return r.hydrate(ctx, l)
}

// membershipFilterFrom is the FROM/JOIN/WHERE clause shared by the visibility-gated
// launch list and count queries: a caller sees a launch only if they are on its
// allowlist or its committee. Both bound params are the caller's operator address. Keeping
// this in one place keeps the paginated results and the total count in agreement.
const membershipFilterFrom = `
	FROM launches l
	LEFT JOIN allowlist al ON al.launch_id = l.id AND al.account = ?
	LEFT JOIN committees c ON c.launch_id = l.id
	LEFT JOIN committee_members cm ON cm.committee_id = c.id AND cm.account = ?
	WHERE al.account IS NOT NULL OR cm.account IS NOT NULL`

func (r *LaunchRepository) FindAll(ctx context.Context, operatorAddr string, page, perPage int) ([]*launch.Launch, int, error) {
	q := conn(ctx, r.db)
	offset := (page - 1) * perPage

	// Launches are private: a caller sees a launch only if they are a committee member or on its
	// members allowlist — matched on the HRP-independent account key (like coordinator_allowlist),
	// NOT the per-launch display address, so a caller sees their launches under any wallet prefix.
	// An unauthenticated or unparseable caller sees nothing (mirrors the in-memory IsVisibleTo).
	acct, ok := accountHex(operatorAddr)
	if !ok {
		return []*launch.Launch{}, 0, nil
	}
	rows, err := q.QueryContext(ctx,
		`SELECT DISTINCT l.* `+membershipFilterFrom+`
		ORDER BY l.created_at DESC LIMIT ? OFFSET ?`,
		acct, acct, perPage, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("launch find all: %w", err)
	}
	defer rows.Close()

	// Drain all rows before closing the cursor — hydrate makes additional queries
	// and with MaxOpenConns(1) keeping the cursor open would deadlock.
	var scanned []*launch.Launch
	for rows.Next() {
		l, err := r.scanLaunchRow(rows)
		if err != nil {
			return nil, 0, err
		}
		scanned = append(scanned, l)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("launch find all: %w", err)
	}
	rows.Close()

	var launches []*launch.Launch
	for _, l := range scanned {
		l, err = r.hydrate(ctx, l)
		if err != nil {
			return nil, 0, err
		}
		launches = append(launches, l)
	}

	// Total count (same membership filter; acct is a valid account key here).
	var total int
	err = q.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT l.id) `+membershipFilterFrom,
		acct, acct).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("launch count: %w", err)
	}
	return launches, total, nil
}

// FindByStatus returns all launches in the given status, regardless of caller membership/visibility.
func (r *LaunchRepository) FindByStatus(ctx context.Context, status launch.Status) ([]*launch.Launch, error) {
	q := conn(ctx, r.db)
	rows, err := q.QueryContext(ctx,
		`SELECT * FROM launches WHERE status=? ORDER BY created_at ASC`,
		string(status))
	if err != nil {
		return nil, fmt.Errorf("launch find by status: %w", err)
	}
	defer rows.Close()

	var scanned []*launch.Launch
	for rows.Next() {
		l, err := r.scanLaunchRow(rows)
		if err != nil {
			return nil, err
		}
		scanned = append(scanned, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("launch find by status: %w", err)
	}
	rows.Close()

	var launches []*launch.Launch
	for _, l := range scanned {
		l, err = r.hydrate(ctx, l)
		if err != nil {
			return nil, err
		}
		launches = append(launches, l)
	}
	return launches, nil
}

// hydrate loads committee, members, allowlist, voting power, and allocation files into l.
func (r *LaunchRepository) hydrate(ctx context.Context, l *launch.Launch) (*launch.Launch, error) {
	if err := r.loadCommittee(ctx, l); err != nil {
		return nil, err
	}
	if err := r.loadAllowlist(ctx, l); err != nil {
		return nil, err
	}
	if err := r.loadVotingPower(ctx, l); err != nil {
		return nil, err
	}
	if err := r.loadAllocationFiles(ctx, l); err != nil {
		return nil, err
	}
	return l, nil
}

func (r *LaunchRepository) saveAllocationFiles(ctx context.Context, l *launch.Launch) error {
	q := conn(ctx, r.db)
	if _, err := q.ExecContext(ctx,
		`DELETE FROM launch_allocation_files WHERE launch_id=?`, uuidToStr(l.ID)); err != nil {
		return fmt.Errorf("delete allocation files: %w", err)
	}
	for _, f := range l.AllocationFiles {
		var approvedBy any // nil → SQL NULL
		if f.ApprovedByProposal != nil {
			approvedBy = uuidToStr(*f.ApprovedByProposal)
		}
		if _, err := q.ExecContext(ctx,
			`INSERT INTO launch_allocation_files (launch_id, alloc_type, sha256, status, approved_by_proposal, uploaded_at) VALUES (?,?,?,?,?,?)`,
			uuidToStr(l.ID), string(f.Type), f.SHA256, string(f.Status), approvedBy, timeToStr(f.UploadedAt),
		); err != nil {
			return fmt.Errorf("insert allocation file %s: %w", f.Type, err)
		}
	}
	return nil
}

func (r *LaunchRepository) loadAllocationFiles(ctx context.Context, l *launch.Launch) error {
	q := conn(ctx, r.db)
	rows, err := q.QueryContext(ctx,
		`SELECT alloc_type, sha256, status, approved_by_proposal, uploaded_at FROM launch_allocation_files WHERE launch_id=? ORDER BY alloc_type`,
		uuidToStr(l.ID))
	if err != nil {
		return fmt.Errorf("load allocation files: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			allocType, sha256Hex, status, uploadedAt string
			approvedBy                               *string
		)
		if err := rows.Scan(&allocType, &sha256Hex, &status, &approvedBy, &uploadedAt); err != nil {
			return fmt.Errorf("scan allocation file: %w", err)
		}
		ua, err := strToTime(uploadedAt)
		if err != nil {
			return fmt.Errorf("scan allocation uploaded_at: %w", err)
		}
		f := launch.AllocationFile{
			Type:       launch.AllocationType(allocType),
			SHA256:     sha256Hex,
			Status:     launch.AllocationFileStatus(status),
			UploadedAt: ua,
		}
		if approvedBy != nil {
			id, err := strToUUID(*approvedBy)
			if err != nil {
				return fmt.Errorf("scan allocation approved_by_proposal: %w", err)
			}
			f.ApprovedByProposal = &id
		}
		l.AllocationFiles = append(l.AllocationFiles, f)
	}
	return rows.Err()
}

func (r *LaunchRepository) loadCommittee(ctx context.Context, l *launch.Launch) error {
	q := conn(ctx, r.db)
	row := q.QueryRowContext(ctx,
		`SELECT id, threshold_m, total_n, lead_address, creation_signature, created_at FROM committees WHERE launch_id=?`,
		uuidToStr(l.ID))

	var (
		idStr, leadAddr, sig, createdAt string
	)
	err := row.Scan(&idStr, &l.Committee.ThresholdM, &l.Committee.TotalN, &leadAddr, &sig, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // committee not yet set (DRAFT with no committee)
	}
	if err != nil {
		return fmt.Errorf("load committee: %w", err)
	}

	l.Committee.ID, err = strToUUID(idStr)
	if err != nil {
		return err
	}
	l.Committee.LeadAddress, err = launch.NewAccountID(leadAddr)
	if err != nil {
		return fmt.Errorf("load committee lead: %w", err)
	}
	l.Committee.CreationSignature, err = launch.NewSignature(sig)
	if err != nil {
		return fmt.Errorf("load committee sig: %w", err)
	}
	l.Committee.CreatedAt, err = strToTime(createdAt)
	if err != nil {
		return err
	}

	// Load members ordered by position.
	rows, err := q.QueryContext(ctx,
		`SELECT address, moniker, pubkey_b64 FROM committee_members WHERE committee_id=? ORDER BY position`,
		idStr)
	if err != nil {
		return fmt.Errorf("load committee members: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var addrStr, moniker, pubKey string
		if err := rows.Scan(&addrStr, &moniker, &pubKey); err != nil {
			return fmt.Errorf("scan committee member: %w", err)
		}
		addr, err := launch.NewAccountID(addrStr)
		if err != nil {
			return fmt.Errorf("load member address: %w", err)
		}
		l.Committee.Members = append(l.Committee.Members, launch.CommitteeMember{
			Address:   addr,
			Moniker:   moniker,
			PubKeyB64: pubKey,
		})
	}
	return rows.Err()
}

func (r *LaunchRepository) loadAllowlist(ctx context.Context, l *launch.Launch) error {
	q := conn(ctx, r.db)
	rows, err := q.QueryContext(ctx,
		`SELECT address, label, added_by, added_at FROM allowlist WHERE launch_id=?`, uuidToStr(l.ID))
	if err != nil {
		return fmt.Errorf("load allowlist: %w", err)
	}
	defer rows.Close()

	var members []launch.Member
	for rows.Next() {
		var addrStr, label, addedBy, addedAtStr string
		if err := rows.Scan(&addrStr, &label, &addedBy, &addedAtStr); err != nil {
			return fmt.Errorf("scan allowlist: %w", err)
		}
		addr, err := launch.NewAccountID(addrStr)
		if err != nil {
			return fmt.Errorf("load allowlist address: %w", err)
		}
		var addedAt time.Time
		if addedAtStr != "" {
			if addedAt, err = strToTime(addedAtStr); err != nil {
				return fmt.Errorf("load allowlist added_at: %w", err)
			}
		}
		members = append(members, launch.Member{Address: addr, Label: label, AddedBy: addedBy, AddedAt: addedAt})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	l.Allowlist = launch.NewAllowlistFromMembers(members)
	return nil
}

func (r *LaunchRepository) loadVotingPower(ctx context.Context, l *launch.Launch) error {
	q := conn(ctx, r.db)
	rows, err := q.QueryContext(ctx,
		`SELECT operator_address, self_delegation_amount FROM join_requests WHERE launch_id=? AND status='APPROVED'`,
		uuidToStr(l.ID))
	if err != nil {
		return fmt.Errorf("load voting power: %w", err)
	}
	defer rows.Close()

	powers := make(map[string]int64)
	for rows.Next() {
		var addr string
		var amount int64
		if err := rows.Scan(&addr, &amount); err != nil {
			return fmt.Errorf("scan voting power: %w", err)
		}
		// Normalize the stored operator bech32 to the canonical account hex so the key
		// matches the in-memory map (AccountID.Hex()); a persisted address that ever
		// carried a different HRP still resolves to one entry.
		id, err := launch.NewAccountID(addr)
		if err != nil {
			return fmt.Errorf("voting power: invalid operator address %q: %w", addr, err)
		}
		powers[id.Hex()] = amount
	}
	if err := rows.Err(); err != nil {
		return err
	}
	l.InitVotingPower(powers)
	return nil
}

// scanLaunch scans a single *sql.Row (QueryRow result).
func (*LaunchRepository) scanLaunch(row *sql.Row) (*launch.Launch, error) {
	return scanLaunchCols(row.Scan)
}

// scanLaunchRow scans from an open *sql.Rows cursor.
func (*LaunchRepository) scanLaunchRow(rows *sql.Rows) (*launch.Launch, error) {
	return scanLaunchCols(rows.Scan)
}

func scanLaunchCols(scan func(dest ...any) error) (*launch.Launch, error) {
	var (
		idStr, chainID, chainName, binaryName, binaryVersion, binarySHA256 string
		repoURL, repoCommit                                                string
		genesisTime                                                        *string
		denom, minSelfDelegation                                           string
		maxCommRate, maxCommChangeRate                                     string
		gentxDeadline                                                      string
		minValCount                                                        int
		launchType, status                                                 string
		initialGenesisSHA256, finalGenesisSHA256                           string
		monitorRPCURL                                                      string
		createdAt, updatedAt                                               string
		version                                                            int
		bech32Prefix                                                       string // added by migration 0002
		totalSupply, rehearsalServicePubKey, rehearsalEndpoint             string // added by migration 0011
		finalGenesisInputSetHash                                           string // added by migration 0015
	)
	err := scan(
		&idStr, &chainID, &chainName, &binaryName, &binaryVersion, &binarySHA256,
		&repoURL, &repoCommit, &genesisTime, &denom, &minSelfDelegation,
		&maxCommRate, &maxCommChangeRate,
		&gentxDeadline, &minValCount,
		&launchType, &status,
		&initialGenesisSHA256, &finalGenesisSHA256,
		&monitorRPCURL,
		&createdAt, &updatedAt, &version,
		&bech32Prefix,
		&totalSupply, &rehearsalServicePubKey, &rehearsalEndpoint,
		&finalGenesisInputSetHash,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan launch: %w", err)
	}

	id, err := strToUUID(idStr)
	if err != nil {
		return nil, err
	}
	gt, err := nullStrToTime(genesisTime)
	if err != nil {
		return nil, err
	}
	gentxDL, err := strToTime(gentxDeadline)
	if err != nil {
		return nil, err
	}
	ca, err := strToTime(createdAt)
	if err != nil {
		return nil, err
	}
	ua, err := strToTime(updatedAt)
	if err != nil {
		return nil, err
	}
	maxComm, err := launch.NewCommissionRate(maxCommRate)
	if err != nil {
		return nil, fmt.Errorf("scan max_commission_rate: %w", err)
	}
	maxCommChange, err := launch.NewCommissionRate(maxCommChangeRate)
	if err != nil {
		return nil, fmt.Errorf("scan max_commission_change_rate: %w", err)
	}

	l := &launch.Launch{
		ID: id,
		Record: launch.ChainRecord{
			ChainID:                 chainID,
			ChainName:               chainName,
			Bech32Prefix:            bech32Prefix,
			BinaryName:              binaryName,
			BinaryVersion:           binaryVersion,
			BinarySHA256:            binarySHA256,
			RepoURL:                 repoURL,
			RepoCommit:              repoCommit,
			GenesisTime:             gt,
			Denom:                   denom,
			MinSelfDelegation:       minSelfDelegation,
			TotalSupply:             totalSupply,
			MaxCommissionRate:       maxComm,
			MaxCommissionChangeRate: maxCommChange,
			GentxDeadline:           gentxDL,
			MinValidatorCount:       minValCount,
		},
		LaunchType:               launch.LaunchType(launchType),
		Status:                   launch.Status(status),
		InitialGenesisSHA256:     initialGenesisSHA256,
		FinalGenesisSHA256:       finalGenesisSHA256,
		MonitorRPCURL:            monitorRPCURL,
		RehearsalServicePubKey:   rehearsalServicePubKey,
		RehearsalEndpoint:        rehearsalEndpoint,
		FinalGenesisInputSetHash: finalGenesisInputSetHash,
		CreatedAt:                ca,
		UpdatedAt:                ua,
		Version:                  version,
	}
	return l, nil
}
