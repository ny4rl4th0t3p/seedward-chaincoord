//go:build integration

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

// openFileDB opens a real on-disk SQLite database in a temp directory.
func openFileDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("openFileDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestIntegration_Open(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		check func(t *testing.T, db *sql.DB)
	}{
		{
			name: "sets WAL journal mode on file database",
			check: func(t *testing.T, db *sql.DB) {
				var mode string
				if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
					t.Fatalf("journal_mode: %v", err)
				}
				if mode != "wal" {
					t.Errorf("expected WAL journal mode on file DB, got %q", mode)
				}
			},
		},
	}

	for _, tc := range tests {

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.check(t, openFileDB(t))
		})
	}
}

func TestIntegration_ForeignKeyEnforced(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		stmt string
		args []any
	}{
		{
			name: "join_request with missing launch is rejected",
			stmt: `INSERT INTO join_requests (
				id, launch_id, operator_address, consensus_pubkey, gentx_json,
				peer_address, rpc_endpoint, memo, submitted_at, operator_signature,
				status, rejection_reason, self_delegation_amount
			) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			args: []any{
				uuid.New().String(), uuid.New().String(),
				"addr", "pubkey", `{}`, "peer", "http://rpc", "",
				"2026-01-01T00:00:00Z", "sig", "PENDING", "", 0,
			},
		},
		{
			name: "proposal_signature with missing proposal is rejected",
			stmt: `INSERT INTO proposal_signatures (proposal_id, coordinator_address, decision, signed_at, signature)
				VALUES (?,?,?,?,?)`,
			args: []any{
				uuid.New().String(), "addr", "SIGN", "2026-01-01T00:00:00Z", "sig",
			},
		},
	}

	for _, tc := range tests {

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := openFileDB(t).ExecContext(context.Background(), tc.stmt, tc.args...)
			if err == nil {
				t.Error("expected FK violation, got nil")
			}
		})
	}
}

func TestIntegration_OptimisticLock_Conflict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB)
	}{
		{
			name: "stale update returns ErrConflict",
			run: func(t *testing.T, db *sql.DB) {
				lRepo := NewLaunchRepository(db)
				ctx := context.Background()

				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatalf("initial save: %v", err)
				}

				// Simulate two concurrent readers loading the same version.
				snapshot1, err := lRepo.FindByID(ctx, l.ID)
				if err != nil {
					t.Fatal(err)
				}
				snapshot2, err := lRepo.FindByID(ctx, l.ID)
				if err != nil {
					t.Fatal(err)
				}

				snapshot1.Record.ChainName = "updated-by-1"
				if err := lRepo.Save(ctx, snapshot1); err != nil {
					t.Fatalf("first update: %v", err)
				}

				// Second writer must fail — its version is now stale.
				snapshot2.Record.ChainName = "updated-by-2"
				if err := lRepo.Save(ctx, snapshot2); !errors.Is(err, ports.ErrConflict) {
					t.Errorf("expected ErrConflict on stale update, got %v", err)
				}
			},
		},
	}

	for _, tc := range tests {

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, openFileDB(t))
		})
	}
}

func TestIntegration_NonceStore_ConcurrentReplay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB)
	}{
		{
			name: "exactly one goroutine succeeds, the rest get ErrConflict",
			run: func(t *testing.T, db *sql.DB) {
				store := NewNonceStore(db)
				ctx := context.Background()

				const goroutines = 20
				results := make([]error, goroutines)
				var wg sync.WaitGroup

				for i := range goroutines {
					wg.Add(1)
					go func(idx int) {
						defer wg.Done()
						results[idx] = store.Consume(ctx, addr1, "concurrent-nonce")
					}(i)
				}
				wg.Wait()

				successes, conflicts := 0, 0
				for _, err := range results {
					switch {
					case err == nil:
						successes++
					case errors.Is(err, ports.ErrConflict):
						conflicts++
					default:
						t.Errorf("unexpected error: %v", err)
					}
				}
				if successes != 1 {
					t.Errorf("expected exactly 1 success, got %d", successes)
				}
				if conflicts != goroutines-1 {
					t.Errorf("expected %d conflicts, got %d", goroutines-1, conflicts)
				}
			},
		},
	}

	for _, tc := range tests {

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, openFileDB(t))
		})
	}
}
