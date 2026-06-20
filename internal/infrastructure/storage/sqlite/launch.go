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

// LaunchRepository implements ports.LaunchRepository for SQLite.
type LaunchRepository struct {
	db *sql.DB
}

func NewLaunchRepository(db *sql.DB) *LaunchRepository {
	return &LaunchRepository{db: db}
}

// Save inserts or replaces a Launch and its committee, members, and allowlist.
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
			gentx_deadline=?, application_window_open=?, min_validator_count=?,
			launch_type=?, visibility=?, status=?,
			initial_genesis_sha256=?, final_genesis_sha256=?,
			monitor_rpc_url=?,
			updated_at=?, version=version+1
		WHERE id=? AND version=?`,
		l.Record.ChainID, l.Record.ChainName, l.Record.Bech32Prefix, l.Record.BinaryName,
		l.Record.BinaryVersion, l.Record.BinarySHA256,
		l.Record.RepoURL, l.Record.RepoCommit,
		nullTimeToStr(l.Record.GenesisTime),
		l.Record.Denom, l.Record.MinSelfDelegation,
		l.Record.MaxCommissionRate.String(), l.Record.MaxCommissionChangeRate.String(),
		timeToStr(l.Record.GentxDeadline), timeToStr(l.Record.ApplicationWindowOpen),
		l.Record.MinValidatorCount,
		string(l.LaunchType), string(l.Visibility), string(l.Status),
		l.InitialGenesisSHA256, l.FinalGenesisSHA256,
		l.MonitorRPCURL,
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

	// Re-sync committee, allowlist, and genesis accounts (idempotent replace).
	if err := r.saveCommittee(ctx, l); err != nil {
		return err
	}
	if err := r.saveAllowlist(ctx, l); err != nil {
		return err
	}
	return r.saveGenesisAccounts(ctx, l)
}

func (r *LaunchRepository) insert(ctx context.Context, l *launch.Launch) error {
	q := conn(ctx, r.db)
	_, err := q.ExecContext(ctx, `
		INSERT INTO launches (
			id, chain_id, chain_name, bech32_prefix, binary_name, binary_version, binary_sha256,
			repo_url, repo_commit, genesis_time, denom, min_self_delegation,
			max_commission_rate, max_commission_change_rate,
			gentx_deadline, application_window_open, min_validator_count,
			launch_type, visibility, status,
			initial_genesis_sha256, final_genesis_sha256,
			monitor_rpc_url,
			created_at, updated_at, version
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0)`,
		uuidToStr(l.ID),
		l.Record.ChainID, l.Record.ChainName, l.Record.Bech32Prefix, l.Record.BinaryName,
		l.Record.BinaryVersion, l.Record.BinarySHA256,
		l.Record.RepoURL, l.Record.RepoCommit,
		nullTimeToStr(l.Record.GenesisTime),
		l.Record.Denom, l.Record.MinSelfDelegation,
		l.Record.MaxCommissionRate.String(), l.Record.MaxCommissionChangeRate.String(),
		timeToStr(l.Record.GentxDeadline), timeToStr(l.Record.ApplicationWindowOpen),
		l.Record.MinValidatorCount,
		string(l.LaunchType), string(l.Visibility), string(l.Status),
		l.InitialGenesisSHA256, l.FinalGenesisSHA256,
		l.MonitorRPCURL,
		timeToStr(l.CreatedAt), timeToStr(l.UpdatedAt),
	)
	return err
}

func (r *LaunchRepository) saveCommittee(ctx context.Context, l *launch.Launch) error {
	q := conn(ctx, r.db)
	c := l.Committee

	_, err := q.ExecContext(ctx, `
		INSERT OR REPLACE INTO committees (id, launch_id, threshold_m, total_n, lead_address, creation_signature, created_at)
		VALUES (?,?,?,?,?,?,?)`,
		uuidToStr(c.ID), uuidToStr(l.ID),
		c.ThresholdM, c.TotalN,
		c.LeadAddress.String(), c.CreationSignature.String(),
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
			`INSERT INTO committee_members (committee_id, position, address, moniker, pubkey_b64) VALUES (?,?,?,?,?)`,
			uuidToStr(c.ID), i, m.Address.String(), m.Moniker, m.PubKeyB64,
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
	for _, addr := range l.Allowlist.Addresses() {
		if _, err := q.ExecContext(ctx,
			`INSERT INTO allowlist (launch_id, address) VALUES (?,?)`,
			uuidToStr(l.ID), addr.String(),
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

func (r *LaunchRepository) FindAll(ctx context.Context, operatorAddr string, page, perPage int) ([]*launch.Launch, int, error) {
	q := conn(ctx, r.db)
	offset := (page - 1) * perPage

	// PUBLIC launches are visible to all. ALLOWLIST launches are visible only to
	// addresses on the list.
	var (
		rows *sql.Rows
		err  error
	)
	if operatorAddr == "" {
		rows, err = q.QueryContext(ctx,
			`SELECT * FROM launches WHERE visibility='PUBLIC' ORDER BY created_at DESC LIMIT ? OFFSET ?`,
			perPage, offset)
	} else {
		rows, err = q.QueryContext(ctx, `
			SELECT DISTINCT l.* FROM launches l
			LEFT JOIN allowlist al ON al.launch_id = l.id AND al.address = ?
			WHERE l.visibility='PUBLIC' OR al.address IS NOT NULL
			ORDER BY l.created_at DESC LIMIT ? OFFSET ?`,
			operatorAddr, perPage, offset)
	}
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

	// Total count (same filter).
	var total int
	if operatorAddr == "" {
		err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM launches WHERE visibility='PUBLIC'`).Scan(&total)
	} else {
		err = q.QueryRowContext(ctx, `
			SELECT COUNT(DISTINCT l.id) FROM launches l
			LEFT JOIN allowlist al ON al.launch_id = l.id AND al.address = ?
			WHERE l.visibility='PUBLIC' OR al.address IS NOT NULL`,
			operatorAddr).Scan(&total)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("launch count: %w", err)
	}
	return launches, total, nil
}

// FindByStatus returns all launches in the given status, across all visibilities.
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

// hydrate loads committee, members, allowlist, voting power, and genesis accounts into l.
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
	if err := r.loadGenesisAccounts(ctx, l); err != nil {
		return nil, err
	}
	return l, nil
}

func (r *LaunchRepository) saveGenesisAccounts(ctx context.Context, l *launch.Launch) error {
	q := conn(ctx, r.db)
	if _, err := q.ExecContext(ctx,
		`DELETE FROM launch_genesis_accounts WHERE launch_id=?`, uuidToStr(l.ID)); err != nil {
		return fmt.Errorf("delete genesis accounts: %w", err)
	}
	for _, a := range l.GenesisAccounts {
		if _, err := q.ExecContext(ctx,
			`INSERT INTO launch_genesis_accounts (launch_id, address, amount, vesting_schedule) VALUES (?,?,?,?)`,
			uuidToStr(l.ID), a.Address, a.Amount, a.VestingSchedule,
		); err != nil {
			return fmt.Errorf("insert genesis account %s: %w", a.Address, err)
		}
	}
	return nil
}

func (r *LaunchRepository) loadGenesisAccounts(ctx context.Context, l *launch.Launch) error {
	q := conn(ctx, r.db)
	rows, err := q.QueryContext(ctx,
		`SELECT address, amount, vesting_schedule FROM launch_genesis_accounts WHERE launch_id=? ORDER BY address`,
		uuidToStr(l.ID))
	if err != nil {
		return fmt.Errorf("load genesis accounts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var a launch.GenesisAccount
		if err := rows.Scan(&a.Address, &a.Amount, &a.VestingSchedule); err != nil {
			return fmt.Errorf("scan genesis account: %w", err)
		}
		l.GenesisAccounts = append(l.GenesisAccounts, a)
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
	l.Committee.LeadAddress, err = launch.NewOperatorAddress(leadAddr)
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
		addr, err := launch.NewOperatorAddress(addrStr)
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
	rows, err := q.QueryContext(ctx, `SELECT address FROM allowlist WHERE launch_id=?`, uuidToStr(l.ID))
	if err != nil {
		return fmt.Errorf("load allowlist: %w", err)
	}
	defer rows.Close()

	var addrs []launch.OperatorAddress
	for rows.Next() {
		var addrStr string
		if err := rows.Scan(&addrStr); err != nil {
			return fmt.Errorf("scan allowlist: %w", err)
		}
		addr, err := launch.NewOperatorAddress(addrStr)
		if err != nil {
			return fmt.Errorf("load allowlist address: %w", err)
		}
		addrs = append(addrs, addr)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	l.Allowlist = launch.NewAllowlist(addrs)
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
		powers[addr] = amount
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
		gentxDeadline, appWindowOpen                                       string
		minValCount                                                        int
		launchType, visibility, status                                     string
		initialGenesisSHA256, finalGenesisSHA256                           string
		monitorRPCURL                                                      string
		createdAt, updatedAt                                               string
		version                                                            int
		bech32Prefix                                                       string // added by migration 0002; scanned last
	)
	err := scan(
		&idStr, &chainID, &chainName, &binaryName, &binaryVersion, &binarySHA256,
		&repoURL, &repoCommit, &genesisTime, &denom, &minSelfDelegation,
		&maxCommRate, &maxCommChangeRate,
		&gentxDeadline, &appWindowOpen, &minValCount,
		&launchType, &visibility, &status,
		&initialGenesisSHA256, &finalGenesisSHA256,
		&monitorRPCURL,
		&createdAt, &updatedAt, &version,
		&bech32Prefix,
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
	appWO, err := strToTime(appWindowOpen)
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
			MaxCommissionRate:       maxComm,
			MaxCommissionChangeRate: maxCommChange,
			GentxDeadline:           gentxDL,
			ApplicationWindowOpen:   appWO,
			MinValidatorCount:       minValCount,
		},
		LaunchType:           launch.LaunchType(launchType),
		Visibility:           launch.Visibility(visibility),
		Status:               launch.Status(status),
		InitialGenesisSHA256: initialGenesisSHA256,
		FinalGenesisSHA256:   finalGenesisSHA256,
		MonitorRPCURL:        monitorRPCURL,
		CreatedAt:            ca,
		UpdatedAt:            ua,
		Version:              version,
	}
	return l, nil
}
