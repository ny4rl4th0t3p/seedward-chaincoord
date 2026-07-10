package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// backfillAccountForms canonizes address columns to their durable forms after schema
// migrations: global coordinator addresses to the HRP-independent account hex, and
// launch-scoped member/committee addresses to their launch's own bech32 prefix. It is
// idempotent — rows already in canonical form are left unchanged — so it is safe to
// run on every Open().
func backfillAccountForms(ctx context.Context, db *sql.DB) error {
	if err := backfillCoordinatorAccounts(ctx, db); err != nil {
		return err
	}
	return backfillLaunchScopedAddresses(ctx, db)
}

type coordRow struct{ address, addedBy, addedAt string }

// loadCoordinatorRows reads every coordinator row up front (closing the cursor) so the
// caller can rewrite rows without holding the single writer connection open.
func loadCoordinatorRows(ctx context.Context, db *sql.DB) ([]coordRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT address, added_by, added_at FROM coordinator_allowlist`)
	if err != nil {
		return nil, fmt.Errorf("backfill coordinators: list: %w", err)
	}
	defer rows.Close()
	var out []coordRow
	for rows.Next() {
		var r coordRow
		if err := rows.Scan(&r.address, &r.addedBy, &r.addedAt); err != nil {
			return nil, fmt.Errorf("backfill coordinators: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// backfillCoordinatorAccounts rewrites any bech32 coordinator rows to the account hex,
// deduping the rare case where two prefixes of one account both had a row.
func backfillCoordinatorAccounts(ctx context.Context, db *sql.DB) error {
	all, err := loadCoordinatorRows(ctx, db)
	if err != nil {
		return err
	}
	for _, r := range all {
		id, err := launch.NewAccountID(r.address)
		if err != nil {
			continue // already account hex (or junk) — leave as-is
		}
		acct := id.Hex()
		if acct == r.address {
			continue // already canonical
		}
		if _, err := db.ExecContext(ctx,
			`DELETE FROM coordinator_allowlist WHERE address = ?`, r.address); err != nil {
			return fmt.Errorf("backfill coordinators: delete %q: %w", r.address, err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO coordinator_allowlist (address, added_by, added_at) VALUES (?,?,?)
			 ON CONFLICT(address) DO NOTHING`,
			acct, r.addedBy, r.addedAt); err != nil {
			return fmt.Errorf("backfill coordinators: rewrite %q: %w", r.address, err)
		}
	}
	return nil
}

// loadLaunchIDs reads every launch id up front (closing the cursor) so the caller can
// load and re-save each launch without holding the single writer connection open.
func loadLaunchIDs(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM launches`)
	if err != nil {
		return nil, fmt.Errorf("backfill launches: list: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("backfill launches: scan: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// backfillLaunchScopedAddresses rewrites each launch's member + committee addresses
// under that launch's bech32 prefix by loading and re-saving them: the load normalizes
// to accounts, the save (now prefix-aware) writes them canonically. The in-memory
// allowlist dedupes, so two prefixes of one account collapse to one row.
func backfillLaunchScopedAddresses(ctx context.Context, db *sql.DB) error {
	repo := NewLaunchRepository(db)
	ids, err := loadLaunchIDs(ctx, db)
	if err != nil {
		return err
	}
	for _, idStr := range ids {
		id, err := strToUUID(idStr)
		if err != nil {
			return fmt.Errorf("backfill launches: parse id %q: %w", idStr, err)
		}
		l, err := repo.FindByID(ctx, id)
		if err != nil {
			return fmt.Errorf("backfill launches: load %q: %w", idStr, err)
		}
		if err := repo.saveAllowlist(ctx, l); err != nil {
			return fmt.Errorf("backfill launches: save allowlist %q: %w", idStr, err)
		}
		// Only re-save the committee when one exists (a DRAFT may have none).
		if l.Committee.TotalN > 0 {
			if err := repo.saveCommittee(ctx, l); err != nil {
				return fmt.Errorf("backfill launches: save committee %q: %w", idStr, err)
			}
		}
	}
	return nil
}
