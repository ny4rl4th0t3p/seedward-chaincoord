package sqlite

import (
	"database/sql"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		check func(t *testing.T, db *sql.DB)
	}{
		{
			name: "applies migrations on open",
			check: func(t *testing.T, db *sql.DB) {
				var count int
				require.NoError(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&count), "schema_migrations query")
				assert.NotZero(t, count, "expected at least one applied migration")
			},
		},
		{
			name: "sets WAL or memory journal mode",
			check: func(t *testing.T, db *sql.DB) {
				// WAL mode is not available for :memory: databases — SQLite silently uses
				// "memory" journal mode instead. Both are acceptable here.
				var mode string
				require.NoError(t, db.QueryRowContext(t.Context(), `PRAGMA journal_mode`).Scan(&mode), "journal_mode")
				assert.Contains(t, []string{"wal", "memory"}, mode, "unexpected journal mode")
			},
		},
		{
			name: "enables foreign key enforcement",
			check: func(t *testing.T, db *sql.DB) {
				var fk int
				require.NoError(t, db.QueryRowContext(t.Context(), `PRAGMA foreign_keys`).Scan(&fk), "foreign_keys")
				assert.Equal(t, 1, fk, "expected foreign keys to be ON")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.check(t, openTestDB(t))
		})
	}
}

func TestRunMigrations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		check func(t *testing.T, db *sql.DB)
	}{
		{
			name: "running twice does not create duplicate records",
			check: func(t *testing.T, db *sql.DB) {
				require.NoError(t, runMigrations(db), "second runMigrations call")
				files, err := fs.Glob(migrationsFS, "migrations/*.sql")
				require.NoError(t, err, "glob migrations")
				var count int
				require.NoError(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&count), "count")
				assert.Equal(t, len(files), count, "expected one migration record per file after two runs")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.check(t, openTestDB(t))
		})
	}
}
