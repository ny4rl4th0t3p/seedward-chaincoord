// Package sqlite implements the storage ports using SQLite via modernc.org/sqlite.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // register sqlite driver
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open opens an SQLite database at the given DSN and runs all pending migrations.
// Use ":memory:" for in-memory databases (tests).
// WAL mode is enabled for better concurrent read performance.
func Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	// SQLite is single-writer; a connection pool > 1 causes SQLITE_BUSY under
	// concurrent writes. One writer + WAL mode gives the best throughput here.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: pragmas: %w", err)
	}

	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: migrate: %w", err)
	}
	if err := backfillAccountForms(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: backfill: %w", err)
	}
	return db, nil
}

func runMigrations(db *sql.DB) error {
	ctx := context.Background()

	// Ensure the tracking table exists before querying it.
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			applied_at TEXT    NOT NULL
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	files, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(files)

	for _, file := range files {
		version, err := versionFromFilename(file)
		if err != nil {
			return err
		}

		var exists int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, version).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %d: %w", version, err)
		}
		if exists > 0 {
			continue
		}

		content, err := migrationsFS.ReadFile(file)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", file, err)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("migration %d: begin: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, string(content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: exec: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`,
			version, time.Now().UTC().Format(time.RFC3339),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: record: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %d: commit: %w", version, err)
		}
	}
	return nil
}

// versionFromFilename parses the leading integer from "migrations/0001_foo.sql".
func versionFromFilename(path string) (int, error) {
	base := path[strings.LastIndex(path, "/")+1:]
	var version int
	if _, err := fmt.Sscanf(base, "%d", &version); err != nil {
		return 0, fmt.Errorf("invalid migration filename %q: must start with an integer", path)
	}
	return version, nil
}

// helpers shared across all repository types.

// txKey is the context key used to carry an active *sql.Tx.
type txKey struct{}

// queryable is the common interface satisfied by both *sql.DB and *sql.Tx.
type queryable interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// conn returns the active transaction from ctx if one exists, otherwise the pool.
func conn(ctx context.Context, db *sql.DB) queryable {
	if tx, ok := ctx.Value(txKey{}).(*sql.Tx); ok && tx != nil {
		return tx
	}
	return db
}
