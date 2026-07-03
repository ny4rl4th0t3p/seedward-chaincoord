package sqlite

import (
	"context"
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
				require.NoError(t, repo.Save(ctx, l), "Save")
				got, err := repo.FindByID(ctx, l.ID)
				require.NoError(t, err, "FindByID")
				assert.Equal(t, l.ID, got.ID, "ID mismatch")
				assert.Equal(t, l.Record.ChainID, got.Record.ChainID, "ChainID mismatch")
				assert.Equal(t, l.Status, got.Status, "Status mismatch")
				assert.Equal(t, l.Committee.ThresholdM, got.Committee.ThresholdM, "Committee.ThresholdM mismatch")
				assert.Len(t, got.Committee.Members, 3)
			},
		},
		{
			name: "persists status update on subsequent save",
			run: func(t *testing.T, repo *LaunchRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				require.NoError(t, repo.Save(ctx, l), "initial Save")
				require.NoError(t, l.Publish("deadbeef"), "Publish")
				require.NoError(t, repo.Save(ctx, l), "update Save")
				got, err := repo.FindByID(ctx, l.ID)
				require.NoError(t, err, "FindByID")
				assert.Equal(t, launch.StatusPublished, got.Status)
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
		{
			name: "round-trips labeled members — allowlist labels survive save/load",
			run: func(t *testing.T, repo *LaunchRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				addr1 := mustAddr("cosmos1v93xxer9venks6t2ddkx6mn0wpchyum5nn4cca")
				addr2 := mustAddr("cosmos1sxpg8py9s6rc3zv23wxgmr50jzge9yu5r5slya")
				l.Allowlist = launch.NewAllowlistFromMembers([]launch.Member{
					{Address: addr1, Label: "acme-fleet"},
					{Address: addr2, Label: ""}, // a bare member — the empty label must round-trip too
				})
				require.NoError(t, repo.Save(ctx, l))

				got, err := repo.FindByID(ctx, l.ID)
				require.NoError(t, err)
				require.Equal(t, 2, got.Allowlist.Len())
				assert.True(t, got.Allowlist.Contains(addr1))
				assert.Equal(t, "acme-fleet", got.Allowlist.Label(addr1), "label must round-trip")
				assert.Empty(t, got.Allowlist.Label(addr2), "empty label must round-trip")
			},
		},
		{
			name: "round-trips member provenance — added_by/added_at survive",
			run: func(t *testing.T, repo *LaunchRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				addr := mustAddr("cosmos1v93xxer9venks6t2ddkx6mn0wpchyum5nn4cca")
				added := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
				l.Allowlist = launch.NewAllowlistFromMembers([]launch.Member{
					{Address: addr, Label: "acme", AddedBy: "cosmos1sxpg8py9s6rc3zv23wxgmr50jzge9yu5r5slya", AddedAt: added},
				})
				require.NoError(t, repo.Save(ctx, l))

				got, err := repo.FindByID(ctx, l.ID)
				require.NoError(t, err)
				members := got.Allowlist.Members()
				require.Len(t, members, 1)
				assert.Equal(t, "acme", members[0].Label)
				assert.Equal(t, "cosmos1sxpg8py9s6rc3zv23wxgmr50jzge9yu5r5slya", members[0].AddedBy, "added_by round-trips")
				assert.True(t, members[0].AddedAt.Equal(added), "added_at round-trips")
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
				require.NoError(t, repo.Save(context.Background(), l))
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
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
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
				require.NoError(t, repo.Save(ctx, l))
				got, err := repo.FindByChainID(ctx, l.Record.ChainID)
				require.NoError(t, err, "FindByChainID")
				assert.Equal(t, l.ID, got.ID, "ID mismatch")
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

	// Valid bech32 addresses that are NOT in the test committee (addr1/2/3).
	const (
		outsider = "cosmos1v93xxer9venks6t2ddkx6mn0wpchyum5nn4cca"
		stranger = "cosmos1sxpg8py9s6rc3zv23wxgmr50jzge9yu5r5slya"
	)

	tests := []struct {
		name string
		run  func(t *testing.T, repo *LaunchRepository)
	}{
		{
			name: "unauthenticated caller sees nothing (private-always)",
			run: func(t *testing.T, repo *LaunchRepository) {
				ctx := context.Background()
				require.NoError(t, repo.Save(ctx, testLaunch(t)))
				launches, total, err := repo.FindAll(ctx, "", 1, 10)
				require.NoError(t, err, "FindAll")
				assert.Zero(t, total)
				assert.Empty(t, launches)
			},
		},
		{
			name: "committee member sees their launch",
			run: func(t *testing.T, repo *LaunchRepository) {
				ctx := context.Background()
				l := testLaunch(t) // committee includes addr1
				require.NoError(t, repo.Save(ctx, l))
				launches, total, err := repo.FindAll(ctx, addr1, 1, 10)
				require.NoError(t, err, "FindAll")
				require.Equal(t, 1, total)
				require.Len(t, launches, 1)
				assert.Equal(t, l.ID, launches[0].ID)
			},
		},
		{
			name: "allowlisted member sees it; a non-member does not",
			run: func(t *testing.T, repo *LaunchRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				l.Allowlist = launch.NewAllowlist([]launch.OperatorAddress{mustAddr(outsider)})
				require.NoError(t, repo.Save(ctx, l))

				_, total, err := repo.FindAll(ctx, outsider, 1, 10)
				require.NoError(t, err)
				assert.Equal(t, 1, total, "allowlisted member should see the launch")

				_, total, err = repo.FindAll(ctx, stranger, 1, 10)
				require.NoError(t, err)
				assert.Zero(t, total, "a non-member should see nothing")
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
				require.NoError(t, repo.Save(context.Background(), l))
				return l.ID
			},
			status: launch.StatusDraft,
		},
		{
			name: "returns published launches",
			setup: func(t *testing.T, repo *LaunchRepository) uuid.UUID {
				ctx := context.Background()
				l := testLaunch(t)
				require.NoError(t, repo.Save(ctx, l))
				require.NoError(t, l.Publish("abc123"))
				require.NoError(t, repo.Save(ctx, l))
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
			require.NoError(t, err, "FindByStatus(%q)", tc.status)
			require.Len(t, got, 1)
			assert.Equal(t, id, got[0].ID)
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
				require.NoError(t, lRepo.Save(ctx, l))

				jr := testJoinRequest(t, l.ID)
				require.NoError(t, jrRepo.Save(ctx, jr))
				require.NoError(t, jr.Approve(uuid.New()))
				require.NoError(t, jrRepo.Save(ctx, jr))

				got, err := lRepo.FindByID(ctx, l.ID)
				require.NoError(t, err, "FindByID")
				assert.NotZero(t, got.ApprovedVotingPowerOf(mustAddr(addr1)), "expected non-zero voting power after hydration")
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
				require.NoError(t, lRepo.Save(ctx, l))

				jr := testJoinRequest(t, l.ID)
				require.NoError(t, jrRepo.Save(ctx, jr), "Save")
				got, err := jrRepo.FindByID(ctx, jr.ID)
				require.NoError(t, err, "FindByID")
				assert.Equal(t, jr.ID, got.ID, "ID mismatch")
				assert.Equal(t, joinrequest.StatusPending, got.Status)
			},
		},
		{
			name: "persists approved status and proposal reference",
			run: func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				require.NoError(t, lRepo.Save(ctx, l))
				jr := testJoinRequest(t, l.ID)
				require.NoError(t, jrRepo.Save(ctx, jr))

				propID := uuid.New()
				require.NoError(t, jr.Approve(propID))
				require.NoError(t, jrRepo.Save(ctx, jr), "Save after Approve")
				got, _ := jrRepo.FindByID(ctx, jr.ID)
				assert.Equal(t, joinrequest.StatusApproved, got.Status)
				require.NotNil(t, got.ApprovedByProposal, "ApprovedByProposal not persisted")
				assert.Equal(t, propID, *got.ApprovedByProposal, "ApprovedByProposal not persisted correctly")
			},
		},
		{
			name: "persists and hydrates the validator/submitter identity split",
			run: func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				require.NoError(t, lRepo.Save(ctx, l))
				jr := testJoinRequest(t, l.ID)
				jr.OperatorAddress = mustAddr(addr1)  // validator (operator)
				jr.SubmitterAddress = mustAddr(addr2) // distinct signer
				require.NoError(t, jrRepo.Save(ctx, jr), "Save")
				got, err := jrRepo.FindByID(ctx, jr.ID)
				require.NoError(t, err, "FindByID")
				assert.Equal(t, addr1, got.OperatorAddress.String(), "OperatorAddress")
				assert.Equal(t, addr2, got.SubmitterAddress.String(), "SubmitterAddress")
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
				require.NoError(t, lRepo.Save(ctx, l))
				require.NoError(t, jrRepo.Save(ctx, testJoinRequest(t, l.ID)))

				n, err := jrRepo.CountBySubmitter(ctx, l.ID, addr1)
				require.NoError(t, err, "CountBySubmitter")
				assert.Equal(t, 1, n)
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
				require.NoError(t, lRepo.Save(ctx, l))

				pending := testJoinRequest(t, l.ID)
				rejected := testJoinRequest(t, l.ID)
				require.NoError(t, rejected.Reject("bad commission"))
				expired := testJoinRequest(t, l.ID)
				require.NoError(t, expired.Expire())
				for _, jr := range []*joinrequest.JoinRequest{pending, rejected, expired} {
					require.NoError(t, jrRepo.Save(ctx, jr))
				}

				n, err := jrRepo.CountBySubmitter(ctx, l.ID, addr1)
				require.NoError(t, err, "CountBySubmitter")
				assert.Equal(t, 3, n, "expected all 3 statuses counted")
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
				require.NoError(t, lRepo.Save(ctx, l))

				jr1 := testJoinRequest(t, l.ID)
				jr2 := testJoinRequest(t, l.ID)
				peer, _ := launch.NewPeerAddress("abcdef1234567890abcdef1234567890abcdef12@10.0.0.2:26656")
				rpc, _ := launch.NewRPCEndpoint("https://10.0.0.2:26657")
				jr2.PeerAddress = peer
				jr2.RPCEndpoint = rpc
				jr2.OperatorAddress = mustAddr(addr2)
				require.NoError(t, jrRepo.Save(ctx, jr1))
				require.NoError(t, jrRepo.Save(ctx, jr2))
				return l.ID
			},
			wantTotal: 2,
		},
		{
			name: "returns only approved join requests with status filter",
			setup: func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository) uuid.UUID {
				ctx := context.Background()
				l := testLaunch(t)
				require.NoError(t, lRepo.Save(ctx, l))
				jr := testJoinRequest(t, l.ID)
				require.NoError(t, jrRepo.Save(ctx, jr))
				require.NoError(t, jr.Approve(uuid.New()))
				require.NoError(t, jrRepo.Save(ctx, jr))
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
			require.NoError(t, err, "FindByLaunch")
			assert.Equal(t, tc.wantTotal, total, "total")
			assert.Len(t, got, tc.wantTotal)
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
			require.NoError(t, lRepo.Save(ctx, l))
			if tc.seed {
				require.NoError(t, jrRepo.Save(ctx, testJoinRequest(t, l.ID)))
			}

			_, err := jrRepo.FindByOperator(ctx, l.ID, addr1)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
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
				require.NoError(t, lRepo.Save(ctx, l))
				jr := testJoinRequest(t, l.ID)
				require.NoError(t, jrRepo.Save(ctx, jr))
				require.NoError(t, jr.Approve(uuid.New()))
				require.NoError(t, jrRepo.Save(ctx, jr))

				approved, err := jrRepo.FindApprovedByLaunch(ctx, l.ID)
				require.NoError(t, err, "FindApprovedByLaunch")
				require.Len(t, approved, 1)
				assert.Equal(t, jr.ID, approved[0].ID)
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
				require.NoError(t, lRepo.Save(ctx, l))
				// A terminal (rejected) and an active (pending) request for the same validator (addr1).
				rejected := testJoinRequest(t, l.ID)
				require.NoError(t, rejected.Reject("bad commission"))
				require.NoError(t, jrRepo.Save(ctx, rejected))
				pending := testJoinRequest(t, l.ID)
				require.NoError(t, jrRepo.Save(ctx, pending))

				got, err := jrRepo.FindActiveByValidator(ctx, l.ID, addr1)
				require.NoError(t, err, "FindActiveByValidator")
				assert.Equal(t, pending.ID, got.ID, "want the active request")
			},
		},
		{
			name: "returns ErrNotFound when only terminal requests exist",
			run: func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				require.NoError(t, lRepo.Save(ctx, l))
				rejected := testJoinRequest(t, l.ID)
				require.NoError(t, rejected.Reject("bad commission"))
				require.NoError(t, jrRepo.Save(ctx, rejected))

				_, err := jrRepo.FindActiveByValidator(ctx, l.ID, addr1)
				require.ErrorIs(t, err, ports.ErrNotFound)
			},
		},
		{
			name: "partial unique index blocks a second active request for the same validator",
			run: func(t *testing.T, lRepo *LaunchRepository, jrRepo *JoinRequestRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				require.NoError(t, lRepo.Save(ctx, l))
				require.NoError(t, jrRepo.Save(ctx, testJoinRequest(t, l.ID)))
				// A second PENDING request for the same validator (addr1) must violate idx_jr_active_validator.
				err := jrRepo.Save(ctx, testJoinRequest(t, l.ID))
				require.Error(t, err, "expected a unique-index violation for a second active request")
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
				require.NoError(t, lRepo.Save(ctx, l))
				p := testProposal(t, l.ID)
				require.NoError(t, pRepo.Save(ctx, p), "Save")
				got, err := pRepo.FindByID(ctx, p.ID)
				require.NoError(t, err, "FindByID")
				assert.Equal(t, p.ID, got.ID, "ID mismatch")
				assert.Equal(t, proposal.ActionCloseApplicationWindow, got.ActionType, "ActionType mismatch")
				assert.Len(t, got.Signatures, 1)
			},
		},
		{
			name: "persists additional signatures on subsequent save",
			run: func(t *testing.T, lRepo *LaunchRepository, pRepo *ProposalRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				require.NoError(t, lRepo.Save(ctx, l))
				p := testProposal(t, l.ID)
				require.NoError(t, pRepo.Save(ctx, p))

				require.NoError(t, p.Sign(mustAddr(addr2), proposal.DecisionSign, mustSig(), 2, time.Now()))
				require.NoError(t, pRepo.Save(ctx, p), "Save after Sign")
				got, _ := pRepo.FindByID(ctx, p.ID)
				assert.Len(t, got.Signatures, 2)
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
				require.NoError(t, lRepo.Save(ctx, l))
				require.NoError(t, pRepo.Save(ctx, testProposal(t, l.ID)))
				require.NoError(t, pRepo.Save(ctx, testProposal(t, l.ID)))

				pending, err := pRepo.FindPending(ctx)
				require.NoError(t, err, "FindPending")
				assert.Len(t, pending, 2)
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
				require.NoError(t, lRepo.Save(ctx, l))
				require.NoError(t, pRepo.Save(ctx, testProposal(t, l.ID)))
				require.NoError(t, pRepo.Save(ctx, testProposal(t, l.ID)))

				other := testLaunch(t)
				other.Record.ChainID = "other-2"
				require.NoError(t, lRepo.Save(ctx, other))
				require.NoError(t, pRepo.Save(ctx, testProposal(t, other.ID)))

				got, total, err := pRepo.FindByLaunch(ctx, l.ID, 1, 10)
				require.NoError(t, err, "FindByLaunch")
				assert.Equal(t, 2, total, "expected 2 proposals for launch")
				assert.Len(t, got, 2)
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
				require.NoError(t, lRepo.Save(ctx, l))
				p1 := testProposal(t, l.ID)
				p2 := testProposal(t, l.ID)
				require.NoError(t, pRepo.Save(ctx, p1))
				require.NoError(t, pRepo.Save(ctx, p2))

				require.NoError(t, pRepo.ExpireAllPending(ctx, l.ID), "ExpireAllPending")

				got1, err := pRepo.FindByID(ctx, p1.ID)
				require.NoError(t, err, "FindByID p1")
				got2, err := pRepo.FindByID(ctx, p2.ID)
				require.NoError(t, err, "FindByID p2")
				assert.Equal(t, proposal.StatusExpired, got1.Status, "p1")
				assert.Equal(t, proposal.StatusExpired, got2.Status, "p2")
			},
		},
		{
			name: "does not expire proposals for a different launch",
			run: func(t *testing.T, lRepo *LaunchRepository, pRepo *ProposalRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				require.NoError(t, lRepo.Save(ctx, l))
				other := testLaunch(t)
				other.Record.ChainID = "other-chain"
				require.NoError(t, lRepo.Save(ctx, other))

				pOther := testProposal(t, other.ID)
				require.NoError(t, pRepo.Save(ctx, pOther))

				require.NoError(t, pRepo.ExpireAllPending(ctx, l.ID), "ExpireAllPending")

				got, err := pRepo.FindByID(ctx, pOther.ID)
				require.NoError(t, err, "FindByID")
				assert.Equal(t, proposal.StatusPendingSignatures, got.Status, "other launch proposal")
			},
		},
		{
			name: "does not expire already-executed proposals",
			run: func(t *testing.T, lRepo *LaunchRepository, pRepo *ProposalRepository) {
				ctx := context.Background()
				l := testLaunch(t)
				require.NoError(t, lRepo.Save(ctx, l))
				p := testProposal(t, l.ID)
				p.Status = proposal.StatusExecuted
				require.NoError(t, pRepo.Save(ctx, p))

				require.NoError(t, pRepo.ExpireAllPending(ctx, l.ID), "ExpireAllPending")

				got, err := pRepo.FindByID(ctx, p.ID)
				require.NoError(t, err, "FindByID")
				assert.Equal(t, proposal.StatusExecuted, got.Status, "executed proposal")
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
				require.NoError(t, lRepo.Save(ctx, l))
				jr := testJoinRequest(t, l.ID)
				require.NoError(t, jrRepo.Save(ctx, jr))

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
				require.NoError(t, rRepo.Save(ctx, rc), "Save")
				got, err := rRepo.FindByOperator(ctx, l.ID, addr1)
				require.NoError(t, err, "FindByOperator")
				assert.Equal(t, rc.ID, got.ID, "ID mismatch")
				assert.True(t, got.IsValid(), "confirmation should be valid")
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
				require.NoError(t, lRepo.Save(ctx, l))
				jr := testJoinRequest(t, l.ID)
				require.NoError(t, jrRepo.Save(ctx, jr))

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
				require.NoError(t, rRepo.Save(ctx, rc))

				require.NoError(t, rRepo.InvalidateByLaunch(ctx, l.ID), "InvalidateByLaunch")
				got, _ := rRepo.FindByOperator(ctx, l.ID, addr1)
				assert.False(t, got.IsValid(), "confirmation should be invalidated")
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
			require.NoError(t, lRepo.Save(ctx, l))

			if tc.seed {
				jr := testJoinRequest(t, l.ID)
				require.NoError(t, jrRepo.Save(ctx, jr))
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
				require.NoError(t, rRepo.Save(ctx, rc))
			}

			got, err := rRepo.FindByLaunch(ctx, l.ID)
			require.NoError(t, err, "FindByLaunch")
			assert.Len(t, got, tc.wantLen)
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
				require.NoError(t, err, "Issue")
				assert.NotEmpty(t, ch, "expected non-empty challenge")
			},
		},
		{
			name: "second issue before expiry returns the same challenge (idempotent)",
			run: func(t *testing.T, store *ChallengeStore) {
				ctx := context.Background()
				first, err := store.Issue(ctx, addr1)
				require.NoError(t, err, "first Issue")
				second, err := store.Issue(ctx, addr1)
				require.NoError(t, err, "second Issue")
				assert.Equal(t, first, second, "expected idempotent challenge")
			},
		},
		{
			name: "issue for different addresses returns independent challenges",
			run: func(t *testing.T, store *ChallengeStore) {
				ctx := context.Background()
				ch1, _ := store.Issue(ctx, addr1)
				ch2, _ := store.Issue(ctx, addr2)
				assert.NotEqual(t, ch1, ch2, "expected distinct challenges for different addresses")
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
				require.NoError(t, err)
				return c
			},
		},
		{
			name: "second consume returns ErrNotFound",
			setup: func(t *testing.T, store *ChallengeStore) string {
				ctx := context.Background()
				_, err := store.Issue(ctx, addr1)
				require.NoError(t, err)
				_, err = store.Consume(ctx, addr1)
				require.NoError(t, err)
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
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, want, got)
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
				require.NoError(t, store.Consume(context.Background(), addr1, "nonce-abc"))
			},
			operator: addr1,
			nonce:    "nonce-abc",
			wantErr:  ports.ErrConflict,
		},
		{
			name: "same nonce is allowed for different operators",
			setup: func(t *testing.T, store *NonceStore) {
				require.NoError(t, store.Consume(context.Background(), addr1, "shared-nonce"))
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
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
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
		require.NoError(t, err, "Contains before Add")
		assert.False(t, ok, "should not contain addr1 before Add")

		require.NoError(t, repo.Add(ctx, addr1, addr2), "Add")

		ok, err = repo.Contains(ctx, addr1)
		require.NoError(t, err, "Contains after Add")
		assert.True(t, ok, "should contain addr1 after Add")
	})

	t.Run("Add is idempotent", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		repo := NewCoordinatorAllowlistRepo(openTestDB(t))

		require.NoError(t, repo.Add(ctx, addr1, addr2), "first Add")
		require.NoError(t, repo.Add(ctx, addr1, addr3), "second Add (duplicate)")
	})

	t.Run("Remove existing entry", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		repo := NewCoordinatorAllowlistRepo(openTestDB(t))

		require.NoError(t, repo.Add(ctx, addr1, addr2), "Add")
		require.NoError(t, repo.Remove(ctx, addr1), "Remove")
		ok, err := repo.Contains(ctx, addr1)
		require.NoError(t, err, "Contains after Remove")
		assert.False(t, ok, "should not contain addr1 after Remove")
	})

	t.Run("Remove missing entry returns ErrNotFound", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		repo := NewCoordinatorAllowlistRepo(openTestDB(t))

		err := repo.Remove(ctx, addr1)
		require.ErrorIs(t, err, ports.ErrNotFound, "Remove missing")
	})

	t.Run("List pagination", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		repo := NewCoordinatorAllowlistRepo(openTestDB(t))

		addrs := []string{addr1, addr2, addr3}
		for _, a := range addrs {
			require.NoError(t, repo.Add(ctx, a, "admin"), "Add %s", a)
		}

		entries, total, err := repo.List(ctx, 1, 2)
		require.NoError(t, err, "List")
		assert.Equal(t, 3, total, "total")
		assert.Len(t, entries, 2, "page 1 entries")

		entries2, _, err := repo.List(ctx, 2, 2)
		require.NoError(t, err, "List page 2")
		assert.Len(t, entries2, 1, "page 2 entries")
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
			require.NoError(t, store.Allow(ctx, addr1), "Allow %d: unexpected error", i+1)
		}
	})

	t.Run("exceeding the limit returns ErrTooManyRequests", func(t *testing.T) {
		t.Parallel()
		store := NewChallengeRateLimiterStore(openTestDB(t))
		ctx := context.Background()
		for range challengeRateMaxReqs {
			_ = store.Allow(ctx, addr1)
		}
		err := store.Allow(ctx, addr1)
		require.ErrorIs(t, err, ports.ErrTooManyRequests)
	})

	t.Run("rate limit is per-operator: a different address is not affected", func(t *testing.T) {
		t.Parallel()
		store := NewChallengeRateLimiterStore(openTestDB(t))
		ctx := context.Background()
		for range challengeRateMaxReqs {
			_ = store.Allow(ctx, addr1)
		}
		require.NoError(t, store.Allow(ctx, addr2), "Allow for addr2 after addr1 exhausted limit")
	})
}
