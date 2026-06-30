package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransactor_InTransaction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB, txr *Transactor)
	}{
		{
			name: "commits row on success",
			run: func(t *testing.T, db *sql.DB, txr *Transactor) {
				err := txr.InTransaction(context.Background(), func(ctx context.Context) error {
					_, err := conn(ctx, db).ExecContext(ctx,
						`INSERT INTO operator_revocations (operator_address, revoke_before) VALUES ('addr1','2099-01-01T00:00:00Z')`)
					return err
				})
				require.NoError(t, err, "InTransaction")
				var count int
				require.NoError(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM operator_revocations WHERE operator_address='addr1'`).Scan(&count), "Scan")
				assert.Equal(t, 1, count, "expected row to be committed")
			},
		},
		{
			name: "rolls back row on error",
			run: func(t *testing.T, db *sql.DB, txr *Transactor) {
				sentinel := errors.New("intentional failure")
				err := txr.InTransaction(context.Background(), func(ctx context.Context) error {
					_, xerr := conn(ctx, db).ExecContext(ctx,
						`INSERT INTO operator_revocations (operator_address, revoke_before) VALUES ('addr2','2099-01-01T00:00:00Z')`)
					require.NoError(t, xerr, "ExecContext")
					return sentinel
				})
				require.ErrorIs(t, err, sentinel, "expected sentinel error")
				var count int
				require.NoError(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM operator_revocations WHERE operator_address='addr2'`).Scan(&count), "Scan")
				assert.Zero(t, count, "expected row to be rolled back")
			},
		},
		{
			name: "nested call reuses outer transaction",
			run: func(t *testing.T, db *sql.DB, txr *Transactor) {
				// The inner call must reuse the outer tx — if it started its own,
				// the outer rollback would not undo the inner insert.
				outerErr := errors.New("outer failure")
				_ = txr.InTransaction(context.Background(), func(outerCtx context.Context) error {
					_, xerr := conn(outerCtx, db).ExecContext(outerCtx,
						`INSERT INTO operator_revocations (operator_address, revoke_before) VALUES ('addr3','2099-01-01T00:00:00Z')`)
					require.NoError(t, xerr, "outer ExecContext")
					_ = txr.InTransaction(outerCtx, func(innerCtx context.Context) error {
						_, ierr := conn(innerCtx, db).ExecContext(innerCtx,
							`INSERT INTO operator_revocations (operator_address, revoke_before) VALUES ('addr4','2099-01-01T00:00:00Z')`)
						require.NoError(t, ierr, "inner ExecContext")
						return nil
					})
					return outerErr
				})
				var count int
				require.NoError(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM operator_revocations WHERE operator_address IN ('addr3','addr4')`).Scan(&count), "Scan")
				assert.Zero(t, count, "expected both rows rolled back when outer tx fails")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			tc.run(t, db, NewTransactor(db))
		})
	}
}
