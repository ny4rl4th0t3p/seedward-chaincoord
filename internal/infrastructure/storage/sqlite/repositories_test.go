package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/joinrequest"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/proposal"
)

// ---- LaunchRepository ----

func TestLaunchRepository_Save(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, repo *LaunchRepository)
	}{
		{
			name: "persists new launch with all fields",
			run: func(t *testing.T, repo *LaunchRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				if err := repo.Save(ctx, l); err != nil {
					t.Fatalf("Save: %v", err)
				}
				got, err := repo.FindByID(ctx, l.ID)
				if err != nil {
					t.Fatalf("FindByID: %v", err)
				}
				if got.ID != l.ID {
					t.Errorf("ID mismatch: got %v, want %v", got.ID, l.ID)
				}
				if got.Record.ChainID != l.Record.ChainID {
					t.Errorf("ChainID mismatch: got %q, want %q", got.Record.ChainID, l.Record.ChainID)
				}
				if got.Status != l.Status {
					t.Errorf("Status mismatch: got %q, want %q", got.Status, l.Status)
				}
				if got.Committee.ThresholdM != l.Committee.ThresholdM {
					t.Errorf("Committee.ThresholdM mismatch: got %d, want %d", got.Committee.ThresholdM, l.Committee.ThresholdM)
				}
				if len(got.Committee.Members) != 3 {
					t.Errorf("expected 3 committee members, got %d", len(got.Committee.Members))
				}
			},
		},
		{
			name: "persists status update on subsequent save",
			run: func(t *testing.T, repo *LaunchRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				if err := repo.Save(ctx, l); err != nil {
					t.Fatalf("initial Save: %v", err)
				}
				if err := l.Publish("deadbeef"); err != nil {
					t.Fatalf("Publish: %v", err)
				}
				if err := repo.Save(ctx, l); err != nil {
					t.Fatalf("update Save: %v", err)
				}
				got, err := repo.FindByID(ctx, l.ID)
				if err != nil {
					t.Fatalf("FindByID: %v", err)
				}
				if got.Status != launch.StatusPublished {
					t.Errorf("expected PUBLISHED, got %q", got.Status)
				}
			},
		},
		{
			name: "round-trips allocation files (status + approved_by_proposal) and re-upload resets",
			run: func(t *testing.T, repo *LaunchRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				require.NoError(t, l.UploadAllocationFile(launch.AllocationAccounts, "hashaccounts"))
				require.NoError(t, l.UploadAllocationFile(launch.AllocationClaims, "hashclaims"))
				// Approve one file so the APPROVED status + approved_by_proposal round-trips.
				pid := uuid.New()
				require.NoError(t, l.ApproveAllocationFile(launch.AllocationClaims, "hashclaims", pid))
				require.NoError(t, repo.Save(ctx, l))

				got, err := repo.FindByID(ctx, l.ID)
				require.NoError(t, err)
				require.Len(t, got.AllocationFiles, 2)

				accounts, ok := got.AllocationFileOf(launch.AllocationAccounts)
				require.True(t, ok)
				assert.Equal(t, "hashaccounts", accounts.SHA256)
				assert.Equal(t, launch.AllocationPending, accounts.Status)
				assert.Nil(t, accounts.ApprovedByProposal)

				claims, ok := got.AllocationFileOf(launch.AllocationClaims)
				require.True(t, ok)
				assert.Equal(t, launch.AllocationApproved, claims.Status)
				require.NotNil(t, claims.ApprovedByProposal)
				assert.Equal(t, pid, *claims.ApprovedByProposal)

				// Re-upload claims with a new hash → replaces in place and resets to PENDING.
				require.NoError(t, got.UploadAllocationFile(launch.AllocationClaims, "newhash"))
				require.NoError(t, repo.Save(ctx, got))
				got2, err := repo.FindByID(ctx, l.ID)
				require.NoError(t, err)
				require.Len(t, got2.AllocationFiles, 2)
				claims2, ok := got2.AllocationFileOf(launch.AllocationClaims)
				require.True(t, ok)
				assert.Equal(t, "newhash", claims2.SHA256)
				assert.Equal(t, launch.AllocationPending, claims2.Status)
				assert.Nil(t, claims2.ApprovedByProposal)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, NewLaunchRepository(openTestDB(t)))
		})
	}
}

func TestLaunchRepository_FindByID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		seed    func(t *testing.T, repo *LaunchRepository) uuid.UUID
		wantErr error
	}{
		{
			name: "returns launch for existing ID",
			seed: func(t *testing.T, repo *LaunchRepository) uuid.UUID {
				l := testLaunch(t)
				if err := repo.Save(context.Background(), l); err != nil {
					t.Fatal(err)
				}
				return l.ID
			},
		},
		{
			name: "returns ErrNotFound for unknown ID",
			seed: func(*testing.T, *LaunchRepository) uuid.UUID {
				return uuid.New()
			},
			wantErr: ports.ErrNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := NewLaunchRepository(openTestDB(t))
			id := tc.seed(t, repo)
			_, err := repo.FindByID(context.Background(), id)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("FindByID() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestLaunchRepository_FindByChainID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, repo *LaunchRepository)
	}{
		{
			name: "returns launch for existing chain ID",
			run: func(t *testing.T, repo *LaunchRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				if err := repo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}
				got, err := repo.FindByChainID(ctx, l.Record.ChainID)
				if err != nil {
					t.Fatalf("FindByChainID: %v", err)
				}
				if got.ID != l.ID {
					t.Errorf("ID mismatch")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, NewLaunchRepository(openTestDB(t)))
		})
	}
}

func TestLaunchRepository_FindAll(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, repo *LaunchRepository)
	}{
		{
			name: "unauthenticated caller only sees public launches",
			run: func(t *testing.T, repo *LaunchRepository) {
				ctx := context.Background()

				pub := testLaunch(t)
				if err := repo.Save(ctx, pub); err != nil {
					t.Fatal(err)
				}

				restricted := testLaunch(t)
				restricted.Record.ChainID = "restricted-chain-1"
				restricted.Visibility = launch.VisibilityAllowlist
				if err := repo.Save(ctx, restricted); err != nil {
					t.Fatal(err)
				}

				launches, total, err := repo.FindAll(ctx, "", 1, 10)
				if err != nil {
					t.Fatalf("FindAll: %v", err)
				}
				if total != 1 || len(launches) != 1 {
					t.Errorf("expected 1 public launch, got total=%d len=%d", total, len(launches))
				}
				if launches[0].ID != pub.ID {
					t.Error("returned wrong launch")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, NewLaunchRepository(openTestDB(t)))
		})
	}
}

func TestLaunchRepository_FindByStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		setup  func(t *testing.T, repo *LaunchRepository) uuid.UUID
		status launch.Status
	}{
		{
			name: "returns draft launches",
			setup: func(t *testing.T, repo *LaunchRepository) uuid.UUID {
				l := testLaunch(t)
				if err := repo.Save(context.Background(), l); err != nil {
					t.Fatal(err)
				}
				return l.ID
			},
			status: launch.StatusDraft,
		},
		{
			name: "returns published launches",
			setup: func(t *testing.T, repo *LaunchRepository) uuid.UUID {
				ctx := context.Background()
				l := testLaunch(t)
				if err := repo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}
				if err := l.Publish("abc123"); err != nil {
					t.Fatal(err)
				}
				if err := repo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}
				return l.ID
			},
			status: launch.StatusPublished,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := NewLaunchRepository(openTestDB(t))
			id := tc.setup(t, repo)
			got, err := repo.FindByStatus(context.Background(), tc.status)
			if err != nil {
				t.Fatalf("FindByStatus(%q): %v", tc.status, err)
			}
			if len(got) != 1 || got[0].ID != id {
				t.Errorf("expected 1 launch with ID %v, got %d launches", id, len(got))
			}
		})
	}
}

func TestLaunchRepository_VotingPowerHydrated(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository)
	}{
		{
			name: "hydrates voting power from approved join requests",
			run: func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository) {
				ctx := context.Background()

				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}

				jr := testJoinRequest(t, l.ID)
				if err := jrRepo.Save(ctx, jr); err != nil {
					t.Fatal(err)
				}
				if err := jr.Approve(uuid.New()); err != nil {
					t.Fatal(err)
				}
				if err := jrRepo.Save(ctx, jr); err != nil {
					t.Fatal(err)
				}

				got, err := lRepo.FindByID(ctx, l.ID)
				if err != nil {
					t.Fatalf("FindByID: %v", err)
				}
				if got.ApprovedVotingPowerOf(mustAddr(addr1)) == 0 {
					t.Error("expected non-zero voting power after hydration")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			tc.run(t, NewLaunchRepository(db), NewJoinRequestRepository(db))
		})
	}
}

// ---- JoinRequestRepository ----

func TestJoinRequestRepository_Save(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository)
	}{
		{
			name: "persists new join request with pending status",
			run: func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}

				jr := testJoinRequest(t, l.ID)
				if err := jrRepo.Save(ctx, jr); err != nil {
					t.Fatalf("Save: %v", err)
				}
				got, err := jrRepo.FindByID(ctx, jr.ID)
				if err != nil {
					t.Fatalf("FindByID: %v", err)
				}
				if got.ID != jr.ID {
					t.Errorf("ID mismatch")
				}
				if got.Status != joinrequest.StatusPending {
					t.Errorf("expected PENDING, got %q", got.Status)
				}
			},
		},
		{
			name: "persists approved status and proposal reference",
			run: func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}
				jr := testJoinRequest(t, l.ID)
				if err := jrRepo.Save(ctx, jr); err != nil {
					t.Fatal(err)
				}

				propID := uuid.New()
				if err := jr.Approve(propID); err != nil {
					t.Fatal(err)
				}
				if err := jrRepo.Save(ctx, jr); err != nil {
					t.Fatalf("Save after Approve: %v", err)
				}
				got, _ := jrRepo.FindByID(ctx, jr.ID)
				if got.Status != joinrequest.StatusApproved {
					t.Errorf("expected APPROVED, got %q", got.Status)
				}
				if got.ApprovedByProposal == nil || *got.ApprovedByProposal != propID {
					t.Error("ApprovedByProposal not persisted correctly")
				}
			},
		},
		{
			name: "persists and hydrates the validator/submitter identity split",
			run: func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}
				jr := testJoinRequest(t, l.ID)
				jr.OperatorAddress = mustAddr(addr1)  // validator (operator)
				jr.SubmitterAddress = mustAddr(addr2) // distinct signer
				if err := jrRepo.Save(ctx, jr); err != nil {
					t.Fatalf("Save: %v", err)
				}
				got, err := jrRepo.FindByID(ctx, jr.ID)
				if err != nil {
					t.Fatalf("FindByID: %v", err)
				}
				if got.OperatorAddress.String() != addr1 {
					t.Errorf("OperatorAddress = %q, want %q", got.OperatorAddress, addr1)
				}
				if got.SubmitterAddress.String() != addr2 {
					t.Errorf("SubmitterAddress = %q, want %q", got.SubmitterAddress, addr2)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			tc.run(t, NewLaunchRepository(db), NewJoinRequestRepository(db))
		})
	}
}

func TestJoinRequestRepository_CountBySubmitter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository)
	}{
		{
			name: "counts join requests by submitter for a launch",
			run: func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}
				if err := jrRepo.Save(ctx, testJoinRequest(t, l.ID)); err != nil {
					t.Fatal(err)
				}

				n, err := jrRepo.CountBySubmitter(ctx, l.ID, addr1)
				if err != nil {
					t.Fatalf("CountBySubmitter: %v", err)
				}
				if n != 1 {
					t.Errorf("expected 1, got %d", n)
				}
			},
		},
		{
			// D2 anti-flood semantic: the cap must count ALL statuses, so that
			// rejected/expired submissions consume the budget and are never
			// refunded — a noisy submitter cannot reset the counter by getting
			// rejected. (Distinct from the active-only consensus-pubkey check.)
			name: "counts terminal-status submissions toward the cap",
			run: func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}

				pending := testJoinRequest(t, l.ID)
				rejected := testJoinRequest(t, l.ID)
				if err := rejected.Reject("bad commission"); err != nil {
					t.Fatal(err)
				}
				expired := testJoinRequest(t, l.ID)
				if err := expired.Expire(); err != nil {
					t.Fatal(err)
				}
				for _, jr := range []*joinrequest.JoinRequest{pending, rejected, expired} {
					if err := jrRepo.Save(ctx, jr); err != nil {
						t.Fatal(err)
					}
				}

				n, err := jrRepo.CountBySubmitter(ctx, l.ID, addr1)
				if err != nil {
					t.Fatalf("CountBySubmitter: %v", err)
				}
				if n != 3 {
					t.Errorf("expected all 3 statuses counted, got %d", n)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			tc.run(t, NewLaunchRepository(db), NewJoinRequestRepository(db))
		})
	}
}

func TestJoinRequestRepository_FindByLaunch(t *testing.T) {
	t.Parallel()

	approvedStatus := joinrequest.StatusApproved

	tests := []struct {
		name         string
		setup        func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository) uuid.UUID
		statusFilter *joinrequest.Status
		wantTotal    int
	}{
		{
			name: "returns all join requests without status filter",
			setup: func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository) uuid.UUID {
				ctx := context.Background()
				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}

				jr1 := testJoinRequest(t, l.ID)
				jr2 := testJoinRequest(t, l.ID)
				peer, _ := launch.NewPeerAddress("abcdef1234567890abcdef1234567890abcdef12@10.0.0.2:26656")
				rpc, _ := launch.NewRPCEndpoint("https://10.0.0.2:26657")
				jr2.PeerAddress = peer
				jr2.RPCEndpoint = rpc
				jr2.OperatorAddress = mustAddr(addr2)
				if err := jrRepo.Save(ctx, jr1); err != nil {
					t.Fatal(err)
				}
				if err := jrRepo.Save(ctx, jr2); err != nil {
					t.Fatal(err)
				}
				return l.ID
			},
			wantTotal: 2,
		},
		{
			name: "returns only approved join requests with status filter",
			setup: func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository) uuid.UUID {
				ctx := context.Background()
				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}
				jr := testJoinRequest(t, l.ID)
				if err := jrRepo.Save(ctx, jr); err != nil {
					t.Fatal(err)
				}
				if err := jr.Approve(uuid.New()); err != nil {
					t.Fatal(err)
				}
				if err := jrRepo.Save(ctx, jr); err != nil {
					t.Fatal(err)
				}
				return l.ID
			},
			statusFilter: &approvedStatus,
			wantTotal:    1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			lRepo := NewLaunchRepository(db)
			jrRepo := NewJoinRequestRepository(db)
			launchID := tc.setup(t, lRepo, jrRepo)
			got, total, err := jrRepo.FindByLaunch(context.Background(), launchID, tc.statusFilter, 1, 10)
			if err != nil {
				t.Fatalf("FindByLaunch: %v", err)
			}
			if total != tc.wantTotal || len(got) != tc.wantTotal {
				t.Errorf("expected total=%d len=%d, got total=%d len=%d",
					tc.wantTotal, tc.wantTotal, total, len(got))
			}
		})
	}
}

func TestJoinRequestRepository_FindByOperator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		seed    bool
		wantErr error
	}{
		{
			name:    "returns ErrNotFound when no join request exists",
			wantErr: ports.ErrNotFound,
		},
		{
			name: "returns join request for the operator",
			seed: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			lRepo := NewLaunchRepository(db)
			jrRepo := NewJoinRequestRepository(db)
			ctx := context.Background()

			l := testLaunch(t)
			if err := lRepo.Save(ctx, l); err != nil {
				t.Fatal(err)
			}
			if tc.seed {
				if err := jrRepo.Save(ctx, testJoinRequest(t, l.ID)); err != nil {
					t.Fatal(err)
				}
			}

			_, err := jrRepo.FindByOperator(ctx, l.ID, addr1)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("FindByOperator() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestJoinRequestRepository_FindApprovedByLaunch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository)
	}{
		{
			name: "returns approved join requests for launch",
			run: func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}
				jr := testJoinRequest(t, l.ID)
				if err := jrRepo.Save(ctx, jr); err != nil {
					t.Fatal(err)
				}
				if err := jr.Approve(uuid.New()); err != nil {
					t.Fatal(err)
				}
				if err := jrRepo.Save(ctx, jr); err != nil {
					t.Fatal(err)
				}

				approved, err := jrRepo.FindApprovedByLaunch(ctx, l.ID)
				if err != nil {
					t.Fatalf("FindApprovedByLaunch: %v", err)
				}
				if len(approved) != 1 || approved[0].ID != jr.ID {
					t.Errorf("expected 1 approved join request, got %d", len(approved))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			tc.run(t, NewLaunchRepository(db), NewJoinRequestRepository(db))
		})
	}
}

func TestJoinRequestRepository_FindActiveByValidator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository)
	}{
		{
			name: "returns the active request and ignores terminal ones",
			run: func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}
				// A terminal (rejected) and an active (pending) request for the same validator (addr1).
				rejected := testJoinRequest(t, l.ID)
				if err := rejected.Reject("bad commission"); err != nil {
					t.Fatal(err)
				}
				if err := jrRepo.Save(ctx, rejected); err != nil {
					t.Fatal(err)
				}
				pending := testJoinRequest(t, l.ID)
				if err := jrRepo.Save(ctx, pending); err != nil {
					t.Fatal(err)
				}

				got, err := jrRepo.FindActiveByValidator(ctx, l.ID, addr1)
				if err != nil {
					t.Fatalf("FindActiveByValidator: %v", err)
				}
				if got.ID != pending.ID {
					t.Errorf("got %s, want the active request %s", got.ID, pending.ID)
				}
			},
		},
		{
			name: "returns ErrNotFound when only terminal requests exist",
			run: func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}
				rejected := testJoinRequest(t, l.ID)
				if err := rejected.Reject("bad commission"); err != nil {
					t.Fatal(err)
				}
				if err := jrRepo.Save(ctx, rejected); err != nil {
					t.Fatal(err)
				}

				if _, err := jrRepo.FindActiveByValidator(ctx, l.ID, addr1); !errors.Is(err, ports.ErrNotFound) {
					t.Fatalf("want ErrNotFound, got %v", err)
				}
			},
		},
		{
			name: "partial unique index blocks a second active request for the same validator",
			run: func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}
				if err := jrRepo.Save(ctx, testJoinRequest(t, l.ID)); err != nil {
					t.Fatal(err)
				}
				// A second PENDING request for the same validator (addr1) must violate idx_jr_active_validator.
				if err := jrRepo.Save(ctx, testJoinRequest(t, l.ID)); err == nil {
					t.Fatal("expected a unique-index violation for a second active request, got nil")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			tc.run(t, NewLaunchRepository(db), NewJoinRequestRepository(db))
		})
	}
}

// ---- ProposalRepository ----

func TestProposalRepository_Save(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, lRepo *LaunchRepository, pRepo *ProposalRepository)
	}{
		{
			name: "persists new proposal with proposer signature",
			run: func(t *testing.T, lRepo *LaunchRepository, pRepo *ProposalRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}
				p := testProposal(t, l.ID)
				if err := pRepo.Save(ctx, p); err != nil {
					t.Fatalf("Save: %v", err)
				}
				got, err := pRepo.FindByID(ctx, p.ID)
				if err != nil {
					t.Fatalf("FindByID: %v", err)
				}
				if got.ID != p.ID {
					t.Error("ID mismatch")
				}
				if got.ActionType != proposal.ActionCloseApplicationWindow {
					t.Errorf("ActionType mismatch: %q", got.ActionType)
				}
				if len(got.Signatures) != 1 {
					t.Errorf("expected 1 signature, got %d", len(got.Signatures))
				}
			},
		},
		{
			name: "persists additional signatures on subsequent save",
			run: func(t *testing.T, lRepo *LaunchRepository, pRepo *ProposalRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}
				p := testProposal(t, l.ID)
				if err := pRepo.Save(ctx, p); err != nil {
					t.Fatal(err)
				}

				if err := p.Sign(mustAddr(addr2), proposal.DecisionSign, mustSig(), 2, time.Now()); err != nil {
					t.Fatal(err)
				}
				if err := pRepo.Save(ctx, p); err != nil {
					t.Fatalf("Save after Sign: %v", err)
				}
				got, _ := pRepo.FindByID(ctx, p.ID)
				if len(got.Signatures) != 2 {
					t.Errorf("expected 2 signatures, got %d", len(got.Signatures))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			tc.run(t, NewLaunchRepository(db), NewProposalRepository(db))
		})
	}
}

func TestProposalRepository_FindPending(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, lRepo *LaunchRepository, pRepo *ProposalRepository)
	}{
		{
			name: "returns all pending proposals",
			run: func(t *testing.T, lRepo *LaunchRepository, pRepo *ProposalRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}
				if err := pRepo.Save(ctx, testProposal(t, l.ID)); err != nil {
					t.Fatal(err)
				}
				if err := pRepo.Save(ctx, testProposal(t, l.ID)); err != nil {
					t.Fatal(err)
				}

				pending, err := pRepo.FindPending(ctx)
				if err != nil {
					t.Fatalf("FindPending: %v", err)
				}
				if len(pending) != 2 {
					t.Errorf("expected 2 pending proposals, got %d", len(pending))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			tc.run(t, NewLaunchRepository(db), NewProposalRepository(db))
		})
	}
}

func TestProposalRepository_FindByLaunch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, lRepo *LaunchRepository, pRepo *ProposalRepository)
	}{
		{
			name: "returns only proposals for the specified launch",
			run: func(t *testing.T, lRepo *LaunchRepository, pRepo *ProposalRepository) {
				ctx := context.Background()

				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}
				if err := pRepo.Save(ctx, testProposal(t, l.ID)); err != nil {
					t.Fatal(err)
				}
				if err := pRepo.Save(ctx, testProposal(t, l.ID)); err != nil {
					t.Fatal(err)
				}

				other := testLaunch(t)
				other.Record.ChainID = "other-2"
				if err := lRepo.Save(ctx, other); err != nil {
					t.Fatal(err)
				}
				if err := pRepo.Save(ctx, testProposal(t, other.ID)); err != nil {
					t.Fatal(err)
				}

				got, total, err := pRepo.FindByLaunch(ctx, l.ID, 1, 10)
				if err != nil {
					t.Fatalf("FindByLaunch: %v", err)
				}
				if total != 2 || len(got) != 2 {
					t.Errorf("expected 2 proposals for launch, got total=%d len=%d", total, len(got))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			tc.run(t, NewLaunchRepository(db), NewProposalRepository(db))
		})
	}
}

func TestProposalRepository_ExpireAllPending(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, lRepo *LaunchRepository, pRepo *ProposalRepository)
	}{
		{
			name: "expires all pending proposals for the launch",
			run: func(t *testing.T, lRepo *LaunchRepository, pRepo *ProposalRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}
				p1 := testProposal(t, l.ID)
				p2 := testProposal(t, l.ID)
				if err := pRepo.Save(ctx, p1); err != nil {
					t.Fatal(err)
				}
				if err := pRepo.Save(ctx, p2); err != nil {
					t.Fatal(err)
				}

				if err := pRepo.ExpireAllPending(ctx, l.ID); err != nil {
					t.Fatalf("ExpireAllPending: %v", err)
				}

				got1, err := pRepo.FindByID(ctx, p1.ID)
				if err != nil {
					t.Fatalf("FindByID p1: %v", err)
				}
				got2, err := pRepo.FindByID(ctx, p2.ID)
				if err != nil {
					t.Fatalf("FindByID p2: %v", err)
				}
				if got1.Status != proposal.StatusExpired {
					t.Errorf("p1: want EXPIRED, got %s", got1.Status)
				}
				if got2.Status != proposal.StatusExpired {
					t.Errorf("p2: want EXPIRED, got %s", got2.Status)
				}
			},
		},
		{
			name: "does not expire proposals for a different launch",
			run: func(t *testing.T, lRepo *LaunchRepository, pRepo *ProposalRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}
				other := testLaunch(t)
				other.Record.ChainID = "other-chain"
				if err := lRepo.Save(ctx, other); err != nil {
					t.Fatal(err)
				}

				pOther := testProposal(t, other.ID)
				if err := pRepo.Save(ctx, pOther); err != nil {
					t.Fatal(err)
				}

				if err := pRepo.ExpireAllPending(ctx, l.ID); err != nil {
					t.Fatalf("ExpireAllPending: %v", err)
				}

				got, err := pRepo.FindByID(ctx, pOther.ID)
				if err != nil {
					t.Fatalf("FindByID: %v", err)
				}
				if got.Status != proposal.StatusPendingSignatures {
					t.Errorf("other launch proposal: want PENDING_SIGNATURES, got %s", got.Status)
				}
			},
		},
		{
			name: "does not expire already-executed proposals",
			run: func(t *testing.T, lRepo *LaunchRepository, pRepo *ProposalRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}
				p := testProposal(t, l.ID)
				p.Status = proposal.StatusExecuted
				if err := pRepo.Save(ctx, p); err != nil {
					t.Fatal(err)
				}

				if err := pRepo.ExpireAllPending(ctx, l.ID); err != nil {
					t.Fatalf("ExpireAllPending: %v", err)
				}

				got, err := pRepo.FindByID(ctx, p.ID)
				if err != nil {
					t.Fatalf("FindByID: %v", err)
				}
				if got.Status != proposal.StatusExecuted {
					t.Errorf("executed proposal: want EXECUTED, got %s", got.Status)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			tc.run(t, NewLaunchRepository(db), NewProposalRepository(db))
		})
	}
}

// ---- ReadinessRepository ----

func TestReadinessRepository_Save(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository, rRepo *ReadinessRepository)
	}{
		{
			name: "persists readiness confirmation and marks it valid",
			run: func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository, rRepo *ReadinessRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}
				jr := testJoinRequest(t, l.ID)
				if err := jrRepo.Save(ctx, jr); err != nil {
					t.Fatal(err)
				}

				rc := &launch.ReadinessConfirmation{
					ID:                   uuid.New(),
					LaunchID:             l.ID,
					JoinRequestID:        jr.ID,
					OperatorAddress:      mustAddr(addr1),
					GenesisHashConfirmed: "deadbeef",
					BinaryHashConfirmed:  "cafebabe",
					ConfirmedAt:          time.Now().UTC(),
					OperatorSignature:    mustSig(),
				}
				if err := rRepo.Save(ctx, rc); err != nil {
					t.Fatalf("Save: %v", err)
				}
				got, err := rRepo.FindByOperator(ctx, l.ID, addr1)
				if err != nil {
					t.Fatalf("FindByOperator: %v", err)
				}
				if got.ID != rc.ID {
					t.Error("ID mismatch")
				}
				if !got.IsValid() {
					t.Error("confirmation should be valid")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			tc.run(t, NewLaunchRepository(db), NewJoinRequestRepository(db), NewReadinessRepository(db))
		})
	}
}

func TestReadinessRepository_InvalidateByLaunch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository, rRepo *ReadinessRepository)
	}{
		{
			name: "marks all confirmations for a launch as invalid",
			run: func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository, rRepo *ReadinessRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				if err := lRepo.Save(ctx, l); err != nil {
					t.Fatal(err)
				}
				jr := testJoinRequest(t, l.ID)
				if err := jrRepo.Save(ctx, jr); err != nil {
					t.Fatal(err)
				}

				rc := &launch.ReadinessConfirmation{
					ID:                   uuid.New(),
					LaunchID:             l.ID,
					JoinRequestID:        jr.ID,
					OperatorAddress:      mustAddr(addr1),
					GenesisHashConfirmed: "aaa",
					BinaryHashConfirmed:  "bbb",
					ConfirmedAt:          time.Now().UTC(),
					OperatorSignature:    mustSig(),
				}
				if err := rRepo.Save(ctx, rc); err != nil {
					t.Fatal(err)
				}

				if err := rRepo.InvalidateByLaunch(ctx, l.ID); err != nil {
					t.Fatalf("InvalidateByLaunch: %v", err)
				}
				got, _ := rRepo.FindByOperator(ctx, l.ID, addr1)
				if got.IsValid() {
					t.Error("confirmation should be invalidated")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			tc.run(t, NewLaunchRepository(db), NewJoinRequestRepository(db), NewReadinessRepository(db))
		})
	}
}

func TestReadinessRepository_FindByLaunch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		seed    bool
		wantLen int
	}{
		{
			name:    "returns empty slice when no confirmations exist",
			wantLen: 0,
		},
		{
			name:    "returns confirmations for the specified launch",
			seed:    true,
			wantLen: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			lRepo := NewLaunchRepository(db)
			jrRepo := NewJoinRequestRepository(db)
			rRepo := NewReadinessRepository(db)
			ctx := context.Background()

			l := testLaunch(t)
			if err := lRepo.Save(ctx, l); err != nil {
				t.Fatal(err)
			}

			if tc.seed {
				jr := testJoinRequest(t, l.ID)
				if err := jrRepo.Save(ctx, jr); err != nil {
					t.Fatal(err)
				}
				rc := &launch.ReadinessConfirmation{
					ID:                   uuid.New(),
					LaunchID:             l.ID,
					JoinRequestID:        jr.ID,
					OperatorAddress:      mustAddr(addr1),
					GenesisHashConfirmed: "deadbeef",
					BinaryHashConfirmed:  "cafebabe",
					ConfirmedAt:          time.Now().UTC(),
					OperatorSignature:    mustSig(),
				}
				if err := rRepo.Save(ctx, rc); err != nil {
					t.Fatal(err)
				}
			}

			got, err := rRepo.FindByLaunch(ctx, l.ID)
			if err != nil {
				t.Fatalf("FindByLaunch: %v", err)
			}
			if len(got) != tc.wantLen {
				t.Errorf("expected %d confirmations, got %d", tc.wantLen, len(got))
			}
		})
	}
}

// ---- ChallengeStore ----

func TestChallengeStore_Issue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, store *ChallengeStore)
	}{
		{
			name: "first issue returns a non-empty challenge",
			run: func(t *testing.T, store *ChallengeStore) {
				ch, err := store.Issue(context.Background(), addr1)
				if err != nil {
					t.Fatalf("Issue: %v", err)
				}
				if ch == "" {
					t.Error("expected non-empty challenge")
				}
			},
		},
		{
			name: "second issue before expiry returns the same challenge (idempotent)",
			run: func(t *testing.T, store *ChallengeStore) {
				ctx := context.Background()
				first, err := store.Issue(ctx, addr1)
				if err != nil {
					t.Fatalf("first Issue: %v", err)
				}
				second, err := store.Issue(ctx, addr1)
				if err != nil {
					t.Fatalf("second Issue: %v", err)
				}
				if first != second {
					t.Errorf("expected idempotent challenge %q, got %q", first, second)
				}
			},
		},
		{
			name: "issue for different addresses returns independent challenges",
			run: func(t *testing.T, store *ChallengeStore) {
				ctx := context.Background()
				ch1, _ := store.Issue(ctx, addr1)
				ch2, _ := store.Issue(ctx, addr2)
				if ch1 == ch2 {
					t.Error("expected distinct challenges for different addresses")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, NewChallengeStore(openTestDB(t)))
		})
	}
}

func TestChallengeStore_Consume(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setup   func(t *testing.T, store *ChallengeStore) string // returns expected challenge
		wantErr error
	}{
		{
			name: "returns the issued challenge",
			setup: func(t *testing.T, store *ChallengeStore) string {
				c, err := store.Issue(context.Background(), addr1)
				if err != nil {
					t.Fatal(err)
				}
				return c
			},
		},
		{
			name: "second consume returns ErrNotFound",
			setup: func(t *testing.T, store *ChallengeStore) string {
				ctx := context.Background()
				if _, err := store.Issue(ctx, addr1); err != nil {
					t.Fatal(err)
				}
				if _, err := store.Consume(ctx, addr1); err != nil {
					t.Fatal(err)
				}
				return ""
			},
			wantErr: ports.ErrNotFound,
		},
		{
			name: "no challenge issued returns ErrNotFound",
			setup: func(_ *testing.T, _ *ChallengeStore) string {
				return ""
			},
			wantErr: ports.ErrNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := NewChallengeStore(openTestDB(t))
			want := tc.setup(t, store)
			got, err := store.Consume(context.Background(), addr1)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Consume() error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

// ---- NonceStore ----

func TestNonceStore_Consume(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setup    func(t *testing.T, store *NonceStore)
		operator string
		nonce    string
		wantErr  error
	}{
		{
			name:     "first consume of a nonce succeeds",
			operator: addr1,
			nonce:    "nonce-abc",
		},
		{
			name: "replay of the same nonce returns ErrConflict",
			setup: func(t *testing.T, store *NonceStore) {
				if err := store.Consume(context.Background(), addr1, "nonce-abc"); err != nil {
					t.Fatal(err)
				}
			},
			operator: addr1,
			nonce:    "nonce-abc",
			wantErr:  ports.ErrConflict,
		},
		{
			name: "same nonce is allowed for different operators",
			setup: func(t *testing.T, store *NonceStore) {
				if err := store.Consume(context.Background(), addr1, "shared-nonce"); err != nil {
					t.Fatal(err)
				}
			},
			operator: addr2,
			nonce:    "shared-nonce",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := NewNonceStore(openTestDB(t))
			if tc.setup != nil {
				tc.setup(t, store)
			}
			err := store.Consume(context.Background(), tc.operator, tc.nonce)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Consume() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// ---- CoordinatorAllowlistRepo ----

func TestCoordinatorAllowlistRepo(t *testing.T) {
	t.Parallel()

	t.Run("Add and Contains", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		repo := NewCoordinatorAllowlistRepo(openTestDB(t))

		ok, err := repo.Contains(ctx, addr1)
		if err != nil || ok {
			t.Fatalf("Contains before Add: got (%v, %v), want (false, nil)", ok, err)
		}

		if err := repo.Add(ctx, addr1, addr2); err != nil {
			t.Fatalf("Add: %v", err)
		}

		ok, err = repo.Contains(ctx, addr1)
		if err != nil || !ok {
			t.Fatalf("Contains after Add: got (%v, %v), want (true, nil)", ok, err)
		}
	})

	t.Run("Add is idempotent", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		repo := NewCoordinatorAllowlistRepo(openTestDB(t))

		if err := repo.Add(ctx, addr1, addr2); err != nil {
			t.Fatalf("first Add: %v", err)
		}
		if err := repo.Add(ctx, addr1, addr3); err != nil {
			t.Fatalf("second Add (duplicate): %v", err)
		}
	})

	t.Run("Remove existing entry", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		repo := NewCoordinatorAllowlistRepo(openTestDB(t))

		if err := repo.Add(ctx, addr1, addr2); err != nil {
			t.Fatalf("Add: %v", err)
		}
		if err := repo.Remove(ctx, addr1); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		ok, err := repo.Contains(ctx, addr1)
		if err != nil || ok {
			t.Fatalf("Contains after Remove: got (%v, %v), want (false, nil)", ok, err)
		}
	})

	t.Run("Remove missing entry returns ErrNotFound", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		repo := NewCoordinatorAllowlistRepo(openTestDB(t))

		err := repo.Remove(ctx, addr1)
		if !errors.Is(err, ports.ErrNotFound) {
			t.Fatalf("Remove missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("List pagination", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		repo := NewCoordinatorAllowlistRepo(openTestDB(t))

		addrs := []string{addr1, addr2, addr3}
		for _, a := range addrs {
			if err := repo.Add(ctx, a, "admin"); err != nil {
				t.Fatalf("Add %s: %v", a, err)
			}
		}

		entries, total, err := repo.List(ctx, 1, 2)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 3 {
			t.Errorf("total: got %d, want 3", total)
		}
		if len(entries) != 2 {
			t.Errorf("page 1 entries: got %d, want 2", len(entries))
		}

		entries2, _, err := repo.List(ctx, 2, 2)
		if err != nil {
			t.Fatalf("List page 2: %v", err)
		}
		if len(entries2) != 1 {
			t.Errorf("page 2 entries: got %d, want 1", len(entries2))
		}
	})
}

// ---- ChallengeRateLimiterStore ----

func TestChallengeRateLimiterStore_Allow(t *testing.T) {
	t.Parallel()

	t.Run("allows requests up to the limit", func(t *testing.T) {
		t.Parallel()
		store := NewChallengeRateLimiterStore(openTestDB(t))
		ctx := context.Background()
		for i := range challengeRateMaxReqs {
			if err := store.Allow(ctx, addr1); err != nil {
				t.Fatalf("Allow %d: unexpected error: %v", i+1, err)
			}
		}
	})

	t.Run("exceeding the limit returns ErrTooManyRequests", func(t *testing.T) {
		t.Parallel()
		store := NewChallengeRateLimiterStore(openTestDB(t))
		ctx := context.Background()
		for range challengeRateMaxReqs {
			_ = store.Allow(ctx, addr1)
		}
		if err := store.Allow(ctx, addr1); !errors.Is(err, ports.ErrTooManyRequests) {
			t.Fatalf("want ErrTooManyRequests, got %v", err)
		}
	})

	t.Run("rate limit is per-operator: a different address is not affected", func(t *testing.T) {
		t.Parallel()
		store := NewChallengeRateLimiterStore(openTestDB(t))
		ctx := context.Background()
		for range challengeRateMaxReqs {
			_ = store.Allow(ctx, addr1)
		}
		if err := store.Allow(ctx, addr2); err != nil {
			t.Fatalf("Allow for addr2 after addr1 exhausted limit: %v", err)
		}
	})
}
