package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

// CoordinatorAllowlistRepo implements ports.CoordinatorAllowlistRepository for SQLite.
type CoordinatorAllowlistRepo struct {
	db *sql.DB
}

func NewCoordinatorAllowlistRepo(db *sql.DB) *CoordinatorAllowlistRepo {
	return &CoordinatorAllowlistRepo{db: db}
}

func (r *CoordinatorAllowlistRepo) Add(ctx context.Context, address, addedBy string) error {
	_, err := conn(ctx, r.db).ExecContext(ctx,
		`INSERT INTO coordinator_allowlist (address, added_by, added_at) VALUES (?,?,?)
		 ON CONFLICT(address) DO NOTHING`,
		address, addedBy, timeToStr(nowUTC()),
	)
	if err != nil {
		return fmt.Errorf("coordinator_allowlist: add %q: %w", address, err)
	}
	return nil
}

func (r *CoordinatorAllowlistRepo) Remove(ctx context.Context, address string) error {
	res, err := conn(ctx, r.db).ExecContext(ctx,
		`DELETE FROM coordinator_allowlist WHERE address = ?`, address)
	if err != nil {
		return fmt.Errorf("coordinator_allowlist: remove %q: %w", address, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("coordinator_allowlist: remove %q: %w", address, ports.ErrNotFound)
	}
	return nil
}

func (r *CoordinatorAllowlistRepo) Contains(ctx context.Context, address string) (bool, error) {
	var exists int
	err := conn(ctx, r.db).QueryRowContext(ctx,
		`SELECT COUNT(1) FROM coordinator_allowlist WHERE address = ?`, address).Scan(&exists)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("coordinator_allowlist: contains %q: %w", address, err)
	}
	return exists > 0, nil
}

func (r *CoordinatorAllowlistRepo) List(ctx context.Context, page, perPage int) ([]*ports.CoordinatorAllowlistEntry, int, error) {
	var total int
	if err := conn(ctx, r.db).QueryRowContext(ctx,
		`SELECT COUNT(1) FROM coordinator_allowlist`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("coordinator_allowlist: count: %w", err)
	}

	offset := (page - 1) * perPage
	rows, err := conn(ctx, r.db).QueryContext(ctx,
		`SELECT address, added_by, added_at FROM coordinator_allowlist ORDER BY added_at ASC LIMIT ? OFFSET ?`,
		perPage, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("coordinator_allowlist: list: %w", err)
	}
	defer rows.Close()

	var entries []*ports.CoordinatorAllowlistEntry
	for rows.Next() {
		e := &ports.CoordinatorAllowlistEntry{}
		if err := rows.Scan(&e.Address, &e.AddedBy, &e.AddedAt); err != nil {
			return nil, 0, fmt.Errorf("coordinator_allowlist: scan: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("coordinator_allowlist: rows: %w", err)
	}
	return entries, total, nil
}
