package launch_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// Valid bech32 test addresses (generated with cosmos prefix, correct checksums).
const (
	testAddr1 = "cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu"
	testAddr2 = "cosmos1yy3zxfp9ycnjs2f29vkz6t30xqcnyve5j4ep6w"
	testAddr3 = "cosmos1g9pyx3z9ger5sj22fdxy6nj02pg4y5657yq8y0"
	testAddr4 = "cosmos1v93xxer9venks6t2ddkx6mn0wpchyum5nn4cca"
	testAddr5 = "cosmos1sxpg8py9s6rc3zv23wxgmr50jzge9yu5r5slya"
)

func testRecord() launch.ChainRecord {
	return launch.ChainRecord{
		ChainID:               "testchain-1",
		ChainName:             "Test Chain",
		Bech32Prefix:          "cosmos",
		BinaryName:            "testchaind",
		BinaryVersion:         "v1.0.0",
		Denom:                 "utest",
		GentxDeadline:         time.Now().Add(24 * time.Hour),
		ApplicationWindowOpen: time.Now(),
		MinValidatorCount:     4,
	}
}

func testCommittee() launch.Committee {
	sig, _ := launch.NewSignature(validSig())
	return launch.Committee{
		ID:                uuid.New(),
		ThresholdM:        2,
		TotalN:            3,
		LeadAddress:       launch.MustNewOperatorAddress(testAddr1),
		CreationSignature: sig,
		Members: []launch.CommitteeMember{
			{Address: launch.MustNewOperatorAddress(testAddr1), Moniker: "coord-1", PubKeyB64: "AAAA"},
			{Address: launch.MustNewOperatorAddress(testAddr2), Moniker: "coord-2", PubKeyB64: "BBBB"},
			{Address: launch.MustNewOperatorAddress(testAddr3), Moniker: "coord-3", PubKeyB64: "CCCC"},
		},
		CreatedAt: time.Now(),
	}
}

// validSig returns a 64-byte base64 string (all zeros) for test use.
func validSig() string {
	return "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
}

func TestNewLaunch_HappyPath(t *testing.T) {
	l, err := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	require.NoError(t, err)
	assert.Equal(t, launch.StatusDraft, l.Status)
}

func TestNewLaunch_InvalidRecord(t *testing.T) {
	r := testRecord()
	r.ChainID = ""
	_, err := launch.New(uuid.New(), r, launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	assert.Error(t, err, "expected error for empty chain_id")
}

func TestNewLaunch_InvalidCommitteeThreshold(t *testing.T) {
	c := testCommittee()
	c.ThresholdM = 0
	_, err := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, c)
	assert.Error(t, err, "expected error for threshold 0")
}

func TestStateMachine_HappyPath(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())

	require.NoError(t, l.Publish("abc123"))
	assert.Equal(t, launch.StatusPublished, l.Status)

	require.NoError(t, l.OpenWindow())
	assert.Equal(t, launch.StatusWindowOpen, l.Status)
}

func TestStateMachine_InvalidTransitions(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())

	assert.Error(t, l.OpenWindow(), "cannot open window from DRAFT")
	assert.Error(t, l.CloseWindow(10), "cannot close window from DRAFT")
	assert.Error(t, l.PublishGenesis("abc"), "cannot publish genesis from DRAFT")
}

func TestCloseWindow_MinValidatorCount(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	_ = l.Publish("abc123")
	_ = l.OpenWindow()

	assert.Error(t, l.CloseWindow(3), "below min_validator_count (4)")
	assert.NoError(t, l.CloseWindow(4))
}

func TestVotingPowerWarning(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())

	addr1 := launch.MustNewOperatorAddress(testAddr1)
	addr2 := launch.MustNewOperatorAddress(testAddr2)

	l.RecordValidatorApproval(addr1, 40)
	warning := l.RecordValidatorApproval(addr2, 60)
	assert.NotEmpty(t, warning, "expected 33%% warning for addr2 at 60%% voting power")
}

func TestVotingPowerWarning_NoWarningBelow33(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())

	// 4 validators at 25 each — no single entity reaches 33%
	l.RecordValidatorApproval(launch.MustNewOperatorAddress(testAddr1), 25)
	l.RecordValidatorApproval(launch.MustNewOperatorAddress(testAddr2), 25)
	l.RecordValidatorApproval(launch.MustNewOperatorAddress(testAddr3), 25)
	warning := l.RecordValidatorApproval(launch.MustNewOperatorAddress(testAddr4), 25)
	assert.Empty(t, warning)
}

func TestCloseWindow_DominantVotingPowerBlocked(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	_ = l.Publish("abc123")
	_ = l.OpenWindow()

	l.RecordValidatorApproval(launch.MustNewOperatorAddress(testAddr1), 100) // 100% voting power

	assert.Error(t, l.CloseWindow(4), "single entity holds 100%% of voting power")
}

func TestIsVisibleTo(t *testing.T) {
	addr := launch.MustNewOperatorAddress(testAddr1)
	otherAddr := launch.MustNewOperatorAddress(testAddr2)

	al := launch.NewAllowlist([]launch.OperatorAddress{addr})
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypePermissioned, launch.VisibilityAllowlist, testCommittee())
	l.Allowlist = al

	assert.True(t, l.IsVisibleTo(addr.String()), "addr on allowlist should be visible")
	assert.False(t, l.IsVisibleTo(otherAddr.String()), "addr not on allowlist should not be visible")
	assert.False(t, l.IsVisibleTo(""), "unauthenticated should not see ALLOWLIST chain")
}

func TestPublicChainVisibleToAll(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	assert.True(t, l.IsVisibleTo(""), "public chain should be visible to unauthenticated callers")
}

// ---- New — additional error paths -------------------------------------------

func TestNewLaunch_ThresholdExceedsN(t *testing.T) {
	c := testCommittee()
	c.ThresholdM = 4 // TotalN is 3
	_, err := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, c)
	assert.Error(t, err, "threshold (4) > TotalN (3)")
}

func TestNewLaunch_MemberCountMismatch(t *testing.T) {
	c := testCommittee()
	c.TotalN = 5 // Members has only 3 elements
	_, err := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, c)
	assert.Error(t, err, "member count (3) != TotalN (5)")
}

func TestNewLaunch_EmptyBinaryName(t *testing.T) {
	r := testRecord()
	r.BinaryName = ""
	_, err := launch.New(uuid.New(), r, launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	assert.Error(t, err, "expected error for empty binary_name")
}

func TestNewLaunch_EmptyDenom(t *testing.T) {
	r := testRecord()
	r.Denom = ""
	_, err := launch.New(uuid.New(), r, launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	assert.Error(t, err, "expected error for empty denom")
}

func TestNewLaunch_MinValidatorCountZero(t *testing.T) {
	r := testRecord()
	r.MinValidatorCount = 0
	_, err := launch.New(uuid.New(), r, launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	assert.Error(t, err, "expected error for min_validator_count = 0")
}

func TestNewLaunch_ZeroGentxDeadline(t *testing.T) {
	r := testRecord()
	r.GentxDeadline = time.Time{}
	_, err := launch.New(uuid.New(), r, launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	assert.Error(t, err, "expected error for zero gentx_deadline")
}

// ---- Publish error paths ----------------------------------------------------

func TestPublish_NotFromDraft(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	_ = l.Publish("abc123") // now PUBLISHED
	assert.Error(t, l.Publish("def456"), "cannot publish from PUBLISHED")
}

func TestPublish_EmptyGenesisHash(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	assert.Error(t, l.Publish(""), "empty genesis hash")
}

// ---- OpenWindow error paths --------------------------------------------------

func TestOpenWindow_NotFromPublished(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	// Still in DRAFT — OpenWindow should fail.
	assert.Error(t, l.OpenWindow(), "cannot open window from DRAFT")
	// Advance to WINDOW_OPEN and confirm it cannot be re-opened.
	_ = l.Publish("abc123")
	_ = l.OpenWindow()
	assert.Error(t, l.OpenWindow(), "cannot open window from WINDOW_OPEN")
}

// ---- CloseWindow error paths ------------------------------------------------

func TestCloseWindow_NotFromWindowOpen(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	assert.Error(t, l.CloseWindow(10), "cannot close window from DRAFT")
}

func TestCloseWindow_DominantVotingPower_JustAboveThreshold(t *testing.T) {
	// addr1 holds 34% of total (> 33.33%) — window close must be blocked.
	r := testRecord()
	r.MinValidatorCount = 1
	l, _ := launch.New(uuid.New(), r, launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	_ = l.Publish("abc123")
	_ = l.OpenWindow()
	l.RecordValidatorApproval(launch.MustNewOperatorAddress(testAddr1), 34) // 34/100 = 34%
	l.RecordValidatorApproval(launch.MustNewOperatorAddress(testAddr2), 66)
	assert.Error(t, l.CloseWindow(1), "addr1 holds 34%% (≥ 1/3) of voting power")
}

func TestCloseWindow_DominantVotingPower_JustBelowThreshold(t *testing.T) {
	// 4 validators at 25% each — no single entity dominates.
	r := testRecord()
	r.MinValidatorCount = 1
	l, _ := launch.New(uuid.New(), r, launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	_ = l.Publish("abc123")
	_ = l.OpenWindow()
	for _, a := range []string{testAddr1, testAddr2, testAddr3, testAddr4} {
		l.RecordValidatorApproval(launch.MustNewOperatorAddress(a), 25)
	}
	assert.NoError(t, l.CloseWindow(1))
}

// ---- PublishGenesis error paths ---------------------------------------------

func TestPublishGenesis_NotFromWindowClosed(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	assert.Error(t, l.PublishGenesis("abc"), "cannot publish genesis from DRAFT")
	_ = l.Publish("abc123")
	assert.Error(t, l.PublishGenesis("abc"), "cannot publish genesis from PUBLISHED")
}

func TestPublishGenesis_EmptyHash(t *testing.T) {
	r := testRecord()
	r.MinValidatorCount = 1
	l, _ := launch.New(uuid.New(), r, launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	_ = l.Publish("abc123")
	_ = l.OpenWindow()
	_ = l.CloseWindow(1)
	assert.Error(t, l.PublishGenesis(""), "empty final genesis hash")
}

// ---- MarkLaunched -----------------------------------------------------------

func TestMarkLaunched_Success(t *testing.T) {
	r := testRecord()
	r.MinValidatorCount = 1
	l, _ := launch.New(uuid.New(), r, launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	_ = l.Publish("abc123")
	_ = l.OpenWindow()
	_ = l.CloseWindow(1)
	_ = l.PublishGenesis("def456")
	require.NoError(t, l.MarkLaunched())
	assert.Equal(t, launch.StatusLaunched, l.Status)
}

func TestMarkLaunched_NotFromGenesisReady(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	assert.Error(t, l.MarkLaunched(), "cannot mark launched from DRAFT")
}

// ---- Full state machine happy path ------------------------------------------

func TestStateMachine_FullPath(t *testing.T) {
	r := testRecord()
	r.MinValidatorCount = 1
	l, err := launch.New(uuid.New(), r, launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	require.NoError(t, err)
	steps := []struct {
		name string
		fn   func() error
		want launch.Status
	}{
		{"Publish", func() error { return l.Publish("hash1") }, launch.StatusPublished},
		{"OpenWindow", l.OpenWindow, launch.StatusWindowOpen},
		{"CloseWindow", func() error { return l.CloseWindow(1) }, launch.StatusWindowClosed},
		{"PublishGenesis", func() error { return l.PublishGenesis("hash2") }, launch.StatusGenesisReady},
		{"MarkLaunched", l.MarkLaunched, launch.StatusLaunched},
	}
	for _, s := range steps {
		require.NoError(t, s.fn(), s.name)
		assert.Equal(t, s.want, l.Status, "after %s", s.name)
	}
}

// ---- CanValidatorApply ------------------------------------------------------

func TestCanValidatorApply_WindowOpen_Public(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	_ = l.Publish("abc123")
	_ = l.OpenWindow()
	addr := launch.MustNewOperatorAddress(testAddr4)
	assert.NoError(t, l.CanValidatorApply(addr))
}

func TestCanValidatorApply_NotWindowOpen(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	addr := launch.MustNewOperatorAddress(testAddr4)
	// DRAFT
	assert.Error(t, l.CanValidatorApply(addr), "window not open (DRAFT)")
	// PUBLISHED
	_ = l.Publish("abc123")
	assert.Error(t, l.CanValidatorApply(addr), "window not open (PUBLISHED)")
}

func TestCanValidatorApply_AllowlistAddressOnList(t *testing.T) {
	addr := launch.MustNewOperatorAddress(testAddr4)
	al := launch.NewAllowlist([]launch.OperatorAddress{addr})
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypePermissioned, launch.VisibilityAllowlist, testCommittee())
	l.Allowlist = al
	_ = l.Publish("abc123")
	_ = l.OpenWindow()
	assert.NoError(t, l.CanValidatorApply(addr))
}

func TestCanValidatorApply_AllowlistAddressNotOnList(t *testing.T) {
	addr := launch.MustNewOperatorAddress(testAddr4)
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypePermissioned, launch.VisibilityAllowlist, testCommittee())
	l.Allowlist = launch.NewAllowlist(nil) // empty allowlist
	_ = l.Publish("abc123")
	_ = l.OpenWindow()
	assert.Error(t, l.CanValidatorApply(addr), "address not on allowlist")
}

// ---- Voting power helpers ---------------------------------------------------

func TestRecordValidatorApproval_UpdatesExisting(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	addr := launch.MustNewOperatorAddress(testAddr1)
	l.RecordValidatorApproval(addr, 100)
	l.RecordValidatorApproval(addr, 50) // update
	assert.Equal(t, int64(50), l.ApprovedVotingPowerOf(addr))
}

func TestRemoveValidatorApproval(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	addr := launch.MustNewOperatorAddress(testAddr1)
	l.RecordValidatorApproval(addr, 100)
	l.RemoveValidatorApproval(addr)
	assert.Equal(t, int64(0), l.ApprovedVotingPowerOf(addr), "want 0 after removal")
}

func TestApprovedVotingPowerOf_NotFound(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	addr := launch.MustNewOperatorAddress(testAddr1)
	assert.Equal(t, int64(0), l.ApprovedVotingPowerOf(addr), "want 0 for unknown addr")
}

func TestInitVotingPower(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	addr := launch.MustNewOperatorAddress(testAddr1)
	l.InitVotingPower(map[string]int64{addr.String(): 500})
	assert.Equal(t, int64(500), l.ApprovedVotingPowerOf(addr))
}

// ---- Committee.HasMember ----------------------------------------------------

func TestHasMember_PresentAndAbsent(t *testing.T) {
	c := testCommittee()
	addr1 := launch.MustNewOperatorAddress(testAddr1)
	addr4 := launch.MustNewOperatorAddress(testAddr4)
	assert.True(t, c.HasMember(addr1), "addr1 should be a member")
	assert.False(t, c.HasMember(addr4), "addr4 should not be a member")
}

// ---- IsVisibleTo edge cases -------------------------------------------------

func TestIsVisibleTo_InvalidAddress(t *testing.T) {
	al := launch.NewAllowlist([]launch.OperatorAddress{launch.MustNewOperatorAddress(testAddr1)})
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypePermissioned, launch.VisibilityAllowlist, testCommittee())
	l.Allowlist = al
	// An invalid bech32 string should be treated as "not visible".
	assert.False(t, l.IsVisibleTo("not-a-bech32-address"), "invalid address should not be visible")
}

// ---- ReplaceCommitteeMember -------------------------------------------------

func TestLaunch_ReplaceCommitteeMember_Success(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	oldAddr := launch.MustNewOperatorAddress(testAddr2)
	newMember := launch.CommitteeMember{
		Address:   launch.MustNewOperatorAddress(testAddr4),
		Moniker:   "coord-new",
		PubKeyB64: "DDDD",
	}

	require.NoError(t, l.ReplaceCommitteeMember(oldAddr, newMember))

	found := false
	for _, m := range l.Committee.Members {
		if m.Address.String() == testAddr4 {
			found = true
		}
		assert.NotEqual(t, testAddr2, m.Address.String(), "old member still in committee")
	}
	assert.True(t, found, "new member not in committee")
}

func TestLaunch_ReplaceCommitteeMember_UpdatesLead(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	leadAddr := launch.MustNewOperatorAddress(testAddr1)
	newMember := launch.CommitteeMember{
		Address:   launch.MustNewOperatorAddress(testAddr4),
		Moniker:   "new-lead",
		PubKeyB64: "DDDD",
	}

	require.NoError(t, l.ReplaceCommitteeMember(leadAddr, newMember))
	assert.Equal(t, testAddr4, l.Committee.LeadAddress.String(), "lead not updated")
}

func TestLaunch_ReplaceCommitteeMember_NotFound(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	unknownAddr := launch.MustNewOperatorAddress(testAddr5)
	newMember := launch.CommitteeMember{Address: launch.MustNewOperatorAddress(testAddr4)}

	require.Error(t, l.ReplaceCommitteeMember(unknownAddr, newMember), "expected error for unknown old address")
}

// ---- ExpandCommittee --------------------------------------------------------

func TestLaunch_ExpandCommittee_Success(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	newMember := launch.CommitteeMember{
		Address:   launch.MustNewOperatorAddress(testAddr4),
		Moniker:   "coord-4",
		PubKeyB64: "DDDD",
	}

	require.NoError(t, l.ExpandCommittee(newMember, 2))

	assert.Equal(t, 4, l.Committee.TotalN)
	assert.Equal(t, 2, l.Committee.ThresholdM)
	assert.Len(t, l.Committee.Members, 4)
	found := false
	for _, m := range l.Committee.Members {
		if m.Address.String() == testAddr4 {
			found = true
		}
	}
	assert.True(t, found, "new member not found in committee")
}

func TestLaunch_ExpandCommittee_ExplicitThreshold(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	newMember := launch.CommitteeMember{
		Address:   launch.MustNewOperatorAddress(testAddr4),
		Moniker:   "coord-4",
		PubKeyB64: "DDDD",
	}

	require.NoError(t, l.ExpandCommittee(newMember, 3))
	assert.Equal(t, 3, l.Committee.ThresholdM)
}

func TestLaunch_ExpandCommittee_DuplicateMember(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	duplicate := launch.CommitteeMember{
		Address:   launch.MustNewOperatorAddress(testAddr2),
		Moniker:   "dup",
		PubKeyB64: "BBBB",
	}

	assert.Error(t, l.ExpandCommittee(duplicate, 2), "expected error for duplicate member address")
}

func TestLaunch_ExpandCommittee_LivenessGuard(t *testing.T) {
	// 2-of-3 → expand to 4 members with threshold 4 (M == N) should be rejected.
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	newMember := launch.CommitteeMember{
		Address:   launch.MustNewOperatorAddress(testAddr4),
		Moniker:   "coord-4",
		PubKeyB64: "DDDD",
	}

	assert.Error(t, l.ExpandCommittee(newMember, 4), "expected liveness guard error: threshold must be < N")
}

// ---- ShrinkCommittee --------------------------------------------------------

func TestLaunch_ShrinkCommittee_Success(t *testing.T) {
	// 2-of-3 → remove addr3 with threshold 1 → 1-of-2.
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	removeAddr := launch.MustNewOperatorAddress(testAddr3)

	require.NoError(t, l.ShrinkCommittee(removeAddr, 1))

	assert.Equal(t, 2, l.Committee.TotalN)
	assert.Equal(t, 1, l.Committee.ThresholdM)
	assert.Len(t, l.Committee.Members, 2)
	for _, m := range l.Committee.Members {
		assert.NotEqual(t, testAddr3, m.Address.String(), "removed member still present in committee")
	}
}

func TestLaunch_ShrinkCommittee_TransfersLeadWhenRemoved(t *testing.T) {
	// Remove the lead (addr1); lead should transfer to the first remaining member.
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	leadAddr := launch.MustNewOperatorAddress(testAddr1)

	require.NoError(t, l.ShrinkCommittee(leadAddr, 1))
	assert.NotEqual(t, testAddr1, l.Committee.LeadAddress.String(), "lead not transferred after removed member was the lead")
}

func TestLaunch_ShrinkCommittee_NonLeadDoesNotChangeLead(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	removeAddr := launch.MustNewOperatorAddress(testAddr3) // not the lead

	require.NoError(t, l.ShrinkCommittee(removeAddr, 1))
	assert.Equal(t, testAddr1, l.Committee.LeadAddress.String(), "lead changed unexpectedly")
}

func TestLaunch_ShrinkCommittee_MemberNotFound(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	unknownAddr := launch.MustNewOperatorAddress(testAddr5)

	assert.Error(t, l.ShrinkCommittee(unknownAddr, 1), "expected error for unknown member address")
}

func TestLaunch_ShrinkCommittee_LivenessGuard(t *testing.T) {
	// 2-of-3 → remove addr3 with threshold 2 → would produce 2-of-2 (M == N), rejected.
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	removeAddr := launch.MustNewOperatorAddress(testAddr3)

	assert.Error(t, l.ShrinkCommittee(removeAddr, 2), "expected liveness guard error: threshold must be < N")
}

func TestLaunch_ShrinkCommittee_CannotShrinkBelowOneActiveMember(t *testing.T) {
	// Build a 1-of-2 committee and try to shrink to 1 member — always blocked by liveness guard.
	sig, _ := launch.NewSignature(validSig())
	smallCommittee := launch.Committee{
		ID:          uuid.New(),
		ThresholdM:  1,
		TotalN:      2,
		LeadAddress: launch.MustNewOperatorAddress(testAddr1),
		Members: []launch.CommitteeMember{
			{Address: launch.MustNewOperatorAddress(testAddr1), Moniker: "coord-1", PubKeyB64: "AAAA"},
			{Address: launch.MustNewOperatorAddress(testAddr2), Moniker: "coord-2", PubKeyB64: "BBBB"},
		},
		CreationSignature: sig,
		CreatedAt:         time.Now(),
	}
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, smallCommittee)

	assert.Error(t, l.ShrinkCommittee(launch.MustNewOperatorAddress(testAddr2), 1),
		"expected error: cannot shrink to a 1-of-1 committee (liveness guard)")
}

// ---- AllocationFile ---------------------------------------------------------

const testHashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const testHashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestLaunch_UploadAllocationFile_Success(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())

	require.NoError(t, l.UploadAllocationFile(launch.AllocationClaims, testHashA))
	f, ok := l.AllocationFileOf(launch.AllocationClaims)
	require.True(t, ok, "allocation file not stored")
	assert.Equal(t, testHashA, f.SHA256)
	assert.Equal(t, launch.AllocationPending, f.Status)
	assert.Nil(t, f.ApprovedByProposal)
}

func TestLaunch_UploadAllocationFile_InvalidType(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	require.ErrorIs(t, l.UploadAllocationFile(launch.AllocationType("bogus"), testHashA), launch.ErrUnknownAllocationType)
	require.ErrorIs(t, l.UploadAllocationFile(launch.AllocationAccounts, ""), launch.ErrAllocationEmptyHash)
}

func TestLaunch_UploadAllocationFile_ReuploadResetsToPending(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	pid := uuid.New()
	_ = l.UploadAllocationFile(launch.AllocationClaims, testHashA)
	_ = l.ApproveAllocationFile(launch.AllocationClaims, testHashA, pid)

	// Re-upload with a new hash must invalidate the prior approval.
	require.NoError(t, l.UploadAllocationFile(launch.AllocationClaims, testHashB))
	f, _ := l.AllocationFileOf(launch.AllocationClaims)
	assert.Equal(t, testHashB, f.SHA256)
	assert.Equal(t, launch.AllocationPending, f.Status, "status not reset to PENDING")
	assert.Nil(t, f.ApprovedByProposal, "ApprovedByProposal not cleared on re-upload")
	assert.Len(t, l.AllocationFiles, 1, "re-upload should replace, not append")
}

func TestLaunch_ApproveAllocationFile_Success(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	pid := uuid.New()
	_ = l.UploadAllocationFile(launch.AllocationGrants, testHashA)

	require.NoError(t, l.ApproveAllocationFile(launch.AllocationGrants, testHashA, pid))
	f, _ := l.AllocationFileOf(launch.AllocationGrants)
	assert.Equal(t, launch.AllocationApproved, f.Status)
	require.NotNil(t, f.ApprovedByProposal)
	assert.Equal(t, pid, *f.ApprovedByProposal)
}

func TestLaunch_ApproveAllocationFile_StaleHash(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	_ = l.UploadAllocationFile(launch.AllocationGrants, testHashA)

	require.ErrorIs(t, l.ApproveAllocationFile(launch.AllocationGrants, testHashB, uuid.New()), launch.ErrAllocationStaleHash)
}

func TestLaunch_ApproveAllocationFile_NotFound(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	require.ErrorIs(t, l.ApproveAllocationFile(launch.AllocationAuthz, testHashA, uuid.New()), launch.ErrAllocationNotFound)
}

func TestLaunch_RejectAllocationFile(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	_ = l.UploadAllocationFile(launch.AllocationFeegrant, testHashA)

	// A stale veto (hash no longer matches) is a no-op leaving the file PENDING.
	assert.False(t, l.RejectAllocationFile(launch.AllocationFeegrant, testHashB), "stale reject should be a no-op")
	f, _ := l.AllocationFileOf(launch.AllocationFeegrant)
	assert.Equal(t, launch.AllocationPending, f.Status, "stale reject changed status")

	// A matching veto rejects the file.
	assert.True(t, l.RejectAllocationFile(launch.AllocationFeegrant, testHashA))
	f, _ = l.AllocationFileOf(launch.AllocationFeegrant)
	assert.Equal(t, launch.AllocationRejected, f.Status)

	// Rejecting an unknown type reports no rejection (not an error).
	assert.False(t, l.RejectAllocationFile(launch.AllocationAccounts, testHashA))
}

func TestLaunch_AllocationLockedAfterPublish(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	l.Status = launch.StatusGenesisReady // genesis published — allocation set is frozen

	require.ErrorIs(t, l.UploadAllocationFile(launch.AllocationClaims, testHashA), launch.ErrAllocationLocked)
	require.ErrorIs(t, l.ApproveAllocationFile(launch.AllocationClaims, testHashA, uuid.New()), launch.ErrAllocationLocked)
}

// ---- Cancel -----------------------------------------------------------------

func TestCancel_FromAllNonTerminalStatuses(t *testing.T) {
	cases := []struct {
		name  string
		setup func() *launch.Launch
	}{
		{"DRAFT", func() *launch.Launch {
			l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
			return l
		}},
		{"PUBLISHED", func() *launch.Launch {
			l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
			_ = l.Publish("hash")
			return l
		}},
		{"WINDOW_OPEN", func() *launch.Launch {
			l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
			_ = l.Publish("hash")
			_ = l.OpenWindow()
			return l
		}},
		{"WINDOW_CLOSED", func() *launch.Launch {
			r := testRecord()
			r.MinValidatorCount = 1
			l, _ := launch.New(uuid.New(), r, launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
			_ = l.Publish("hash")
			_ = l.OpenWindow()
			_ = l.CloseWindow(1)
			return l
		}},
		{"GENESIS_READY", func() *launch.Launch {
			return advanceToGenesisReady(t)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := tc.setup()
			require.NoError(t, l.Cancel(), "Cancel from %s", tc.name)
			assert.Equal(t, launch.StatusCancelled, l.Status)
		})
	}
}

func TestCancel_TerminalStatuses_Rejected(t *testing.T) {
	t.Run("LAUNCHED", func(t *testing.T) {
		l := advanceToGenesisReady(t)
		_ = l.MarkLaunched()
		assert.Error(t, l.Cancel(), "cannot cancel LAUNCHED chain")
	})
	t.Run("CANCELED", func(t *testing.T) {
		l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
		_ = l.Cancel()
		assert.Error(t, l.Cancel(), "already CANCELED")
	})
}

// ---- ReopenForRevision ------------------------------------------------------

// advanceToGenesisReady is a helper that drives a launch through the happy path
// up to GENESIS_READY using MinValidatorCount = 1.
func advanceToGenesisReady(t *testing.T) *launch.Launch {
	t.Helper()
	r := testRecord()
	r.MinValidatorCount = 1
	l, err := launch.New(uuid.New(), r, launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	require.NoError(t, err)
	_ = l.Publish("initial-hash")
	_ = l.OpenWindow()
	_ = l.CloseWindow(1)
	_ = l.PublishGenesis("final-hash")
	return l
}

func TestReopenForRevision_Success(t *testing.T) {
	l := advanceToGenesisReady(t)

	require.NoError(t, l.ReopenForRevision())
	assert.Equal(t, launch.StatusWindowClosed, l.Status)
	assert.Empty(t, l.FinalGenesisSHA256, "expected FinalGenesisSHA256 cleared")
}

func TestReopenForRevision_WrongStatus(t *testing.T) {
	statuses := []struct {
		name  string
		setup func() *launch.Launch
	}{
		{"DRAFT", func() *launch.Launch {
			l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
			return l
		}},
		{"PUBLISHED", func() *launch.Launch {
			l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
			_ = l.Publish("hash")
			return l
		}},
		{"WINDOW_OPEN", func() *launch.Launch {
			l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
			_ = l.Publish("hash")
			_ = l.OpenWindow()
			return l
		}},
		{"WINDOW_CLOSED", func() *launch.Launch {
			r := testRecord()
			r.MinValidatorCount = 1
			l, _ := launch.New(uuid.New(), r, launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
			_ = l.Publish("hash")
			_ = l.OpenWindow()
			_ = l.CloseWindow(1)
			return l
		}},
		{"LAUNCHED", func() *launch.Launch {
			l := advanceToGenesisReady(t)
			_ = l.MarkLaunched()
			return l
		}},
	}
	for _, tc := range statuses {
		t.Run(tc.name, func(t *testing.T) {
			l := tc.setup()
			assert.Error(t, l.ReopenForRevision(), "expected error when calling ReopenForRevision from %s", tc.name)
		})
	}
}

// ---- ReadinessConfirmation --------------------------------------------------

func TestReadinessConfirmation_IsValid(t *testing.T) {
	rc := launch.ReadinessConfirmation{}
	assert.True(t, rc.IsValid(), "new confirmation should be valid")
}

func TestReadinessConfirmation_Invalidate(t *testing.T) {
	rc := launch.ReadinessConfirmation{}
	at := time.Now().UTC()
	rc.Invalidate(at)
	assert.False(t, rc.IsValid(), "invalidated confirmation should not be valid")
	require.NotNil(t, rc.InvalidatedAt)
	assert.True(t, rc.InvalidatedAt.Equal(at), "InvalidatedAt should be set to the given time")
}
