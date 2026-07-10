package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// backfillAccountForms canonizes address columns to their durable forms after schema
// migrations: global coordinator addresses to the HRP-independent account hex, and
// launch-scoped member/committee/submitter addresses to their launch's own bech32
// prefix. It is idempotent — rows already in canonical form are left unchanged — so it
// is safe to run on every Open().
func backfillAccountForms(ctx context.Context, db *sql.DB) error {
	if err := backfillCoordinatorAccounts(ctx, db); err != nil {
		return err
	}
	if err := backfillLaunchScopedAddresses(ctx, db); err != nil {
		return err
	}
	return backfillJoinRequestSubmitters(ctx, db)
}

// collectRows runs a query and collects every row up front (closing the cursor via
// defer), so the caller can rewrite rows without holding the single writer connection
// open across the read.
func collectRows[T any](ctx context.Context, db *sql.DB, query string, scan func(*sql.Rows) (T, error)) ([]T, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type coordRow struct{ address, addedBy, addedAt string }

// backfillCoordinatorAccounts rewrites any bech32 coordinator rows to the account hex,
// deduping the rare case where two prefixes of one account both had a row.
func backfillCoordinatorAccounts(ctx context.Context, db *sql.DB) error {
	all, err := collectRows(ctx, db, `SELECT address, added_by, added_at FROM coordinator_allowlist`,
		func(rows *sql.Rows) (coordRow, error) {
			var r coordRow
			err := rows.Scan(&r.address, &r.addedBy, &r.addedAt)
			return r, err
		})
	if err != nil {
		return fmt.Errorf("backfill coordinators: %w", err)
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

// backfillLaunchScopedAddresses rewrites each launch's member + committee addresses
// under that launch's bech32 prefix by loading and re-saving them: the load normalizes
// to accounts, the save (now prefix-aware) writes them canonically. The in-memory
// allowlist dedupes, so two prefixes of one account collapse to one row.
func backfillLaunchScopedAddresses(ctx context.Context, db *sql.DB) error {
	repo := NewLaunchRepository(db)
	ids, err := collectRows(ctx, db, `SELECT id FROM launches`,
		func(rows *sql.Rows) (string, error) {
			var id string
			err := rows.Scan(&id)
			return id, err
		})
	if err != nil {
		return fmt.Errorf("backfill launches: %w", err)
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

type jrSubmitterRow struct{ id, submitter, prefix string }

// backfillJoinRequestSubmitters rewrites each join request's submitter address under
// its launch's bech32 prefix (matching the operator address in the same row), so the
// per-submitter cap counts by account. The submitter column is not unique, so two
// re-encoded submissions of one account coexist.
func backfillJoinRequestSubmitters(ctx context.Context, db *sql.DB) error {
	all, err := collectRows(ctx, db,
		`SELECT jr.id, jr.submitter_address, l.bech32_prefix
		   FROM join_requests jr JOIN launches l ON jr.launch_id = l.id`,
		func(rows *sql.Rows) (jrSubmitterRow, error) {
			var r jrSubmitterRow
			err := rows.Scan(&r.id, &r.submitter, &r.prefix)
			return r, err
		})
	if err != nil {
		return fmt.Errorf("backfill submitters: %w", err)
	}
	for _, r := range all {
		id, err := launch.NewAccountID(r.submitter)
		if err != nil {
			continue // empty (unmigrated) or junk — leave as-is
		}
		canonical := underPrefix(id, r.prefix)
		if canonical == r.submitter {
			continue // already under the launch prefix
		}
		if _, err := db.ExecContext(ctx,
			`UPDATE join_requests SET submitter_address = ? WHERE id = ?`,
			canonical, r.id); err != nil {
			return fmt.Errorf("backfill submitters: update %q: %w", r.id, err)
		}
	}
	return nil
}
