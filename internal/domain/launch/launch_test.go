package launch_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/chaincoord/internal/domain/launch"
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l.Status != launch.StatusDraft {
		t.Errorf("expected DRAFT, got %s", l.Status)
	}
}

func TestNewLaunch_InvalidRecord(t *testing.T) {
	r := testRecord()
	r.ChainID = ""
	_, err := launch.New(uuid.New(), r, launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	if err == nil {
		t.Error("expected error for empty chain_id")
	}
}

func TestNewLaunch_InvalidCommitteeThreshold(t *testing.T) {
	c := testCommittee()
	c.ThresholdM = 0
	_, err := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, c)
	if err == nil {
		t.Error("expected error for threshold 0")
	}
}

func TestStateMachine_HappyPath(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())

	if err := l.Publish("abc123"); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if l.Status != launch.StatusPublished {
		t.Errorf("expected PUBLISHED after Publish, got %s", l.Status)
	}

	if err := l.OpenWindow(); err != nil {
		t.Fatalf("OpenWindow: %v", err)
	}
	if l.Status != launch.StatusWindowOpen {
		t.Errorf("expected WINDOW_OPEN after OpenWindow, got %s", l.Status)
	}
}

func TestStateMachine_InvalidTransitions(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())

	if err := l.OpenWindow(); err == nil {
		t.Error("expected error: cannot open window from DRAFT")
	}
	if err := l.CloseWindow(10); err == nil {
		t.Error("expected error: cannot close window from DRAFT")
	}
	if err := l.PublishGenesis("abc"); err == nil {
		t.Error("expected error: cannot publish genesis from DRAFT")
	}
}

func TestCloseWindow_MinValidatorCount(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	_ = l.Publish("abc123")
	_ = l.OpenWindow()

	if err := l.CloseWindow(3); err == nil {
		t.Error("expected error: below min_validator_count (4)")
	}
	if err := l.CloseWindow(4); err != nil {
		t.Errorf("unexpected error at min count: %v", err)
	}
}

func TestVotingPowerWarning(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())

	addr1 := launch.MustNewOperatorAddress(testAddr1)
	addr2 := launch.MustNewOperatorAddress(testAddr2)

	l.RecordValidatorApproval(addr1, 40)
	warning := l.RecordValidatorApproval(addr2, 60)
	if warning == "" {
		t.Error("expected 33%% warning for addr2 at 60%% voting power")
	}
}

func TestVotingPowerWarning_NoWarningBelow33(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())

	// 4 validators at 25 each — no single entity reaches 33%
	l.RecordValidatorApproval(launch.MustNewOperatorAddress(testAddr1), 25)
	l.RecordValidatorApproval(launch.MustNewOperatorAddress(testAddr2), 25)
	l.RecordValidatorApproval(launch.MustNewOperatorAddress(testAddr3), 25)
	warning := l.RecordValidatorApproval(launch.MustNewOperatorAddress(testAddr4), 25)
	if warning != "" {
		t.Errorf("unexpected warning: %s", warning)
	}
}

func TestCloseWindow_DominantVotingPowerBlocked(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	_ = l.Publish("abc123")
	_ = l.OpenWindow()

	l.RecordValidatorApproval(launch.MustNewOperatorAddress(testAddr1), 100) // 100% voting power

	if err := l.CloseWindow(4); err == nil {
		t.Error("expected error: single entity holds 100%% of voting power")
	}
}

func TestIsVisibleTo(t *testing.T) {
	addr := launch.MustNewOperatorAddress(testAddr1)
	otherAddr := launch.MustNewOperatorAddress(testAddr2)

	al := launch.NewAllowlist([]launch.OperatorAddress{addr})
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypePermissioned, launch.VisibilityAllowlist, testCommittee())
	l.Allowlist = al

	if !l.IsVisibleTo(addr.String()) {
		t.Error("addr on allowlist should be visible")
	}
	if l.IsVisibleTo(otherAddr.String()) {
		t.Error("addr not on allowlist should not be visible")
	}
	if l.IsVisibleTo("") {
		t.Error("unauthenticated should not see ALLOWLIST chain")
	}
}

func TestPublicChainVisibleToAll(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	if !l.IsVisibleTo("") {
		t.Error("public chain should be visible to unauthenticated callers")
	}
}

// ---- New — additional error paths -------------------------------------------

func TestNewLaunch_ThresholdExceedsN(t *testing.T) {
	c := testCommittee()
	c.ThresholdM = 4 // TotalN is 3
	_, err := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, c)
	if err == nil {
		t.Error("expected error: threshold (4) > TotalN (3)")
	}
}

func TestNewLaunch_MemberCountMismatch(t *testing.T) {
	c := testCommittee()
	c.TotalN = 5 // Members has only 3 elements
	_, err := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, c)
	if err == nil {
		t.Error("expected error: member count (3) != TotalN (5)")
	}
}

func TestNewLaunch_EmptyBinaryName(t *testing.T) {
	r := testRecord()
	r.BinaryName = ""
	_, err := launch.New(uuid.New(), r, launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	if err == nil {
		t.Error("expected error for empty binary_name")
	}
}

func TestNewLaunch_EmptyDenom(t *testing.T) {
	r := testRecord()
	r.Denom = ""
	_, err := launch.New(uuid.New(), r, launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	if err == nil {
		t.Error("expected error for empty denom")
	}
}

func TestNewLaunch_MinValidatorCountZero(t *testing.T) {
	r := testRecord()
	r.MinValidatorCount = 0
	_, err := launch.New(uuid.New(), r, launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	if err == nil {
		t.Error("expected error for min_validator_count = 0")
	}
}

func TestNewLaunch_ZeroGentxDeadline(t *testing.T) {
	r := testRecord()
	r.GentxDeadline = time.Time{}
	_, err := launch.New(uuid.New(), r, launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	if err == nil {
		t.Error("expected error for zero gentx_deadline")
	}
}

// ---- Publish error paths ----------------------------------------------------

func TestPublish_NotFromDraft(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	_ = l.Publish("abc123") // now PUBLISHED
	if err := l.Publish("def456"); err == nil {
		t.Error("expected error: cannot publish from PUBLISHED")
	}
}

func TestPublish_EmptyGenesisHash(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	if err := l.Publish(""); err == nil {
		t.Error("expected error: empty genesis hash")
	}
}

// ---- OpenWindow error paths --------------------------------------------------

func TestOpenWindow_NotFromPublished(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	// Still in DRAFT — OpenWindow should fail.
	if err := l.OpenWindow(); err == nil {
		t.Error("expected error: cannot open window from DRAFT")
	}
	// Advance to WINDOW_OPEN and confirm it cannot be re-opened.
	_ = l.Publish("abc123")
	_ = l.OpenWindow()
	if err := l.OpenWindow(); err == nil {
		t.Error("expected error: cannot open window from WINDOW_OPEN")
	}
}

// ---- CloseWindow error paths ------------------------------------------------

func TestCloseWindow_NotFromWindowOpen(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	if err := l.CloseWindow(10); err == nil {
		t.Error("expected error: cannot close window from DRAFT")
	}
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
	if err := l.CloseWindow(1); err == nil {
		t.Error("expected error: addr1 holds 34%% (≥ 1/3) of voting power")
	}
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
	if err := l.CloseWindow(1); err != nil {
		t.Errorf("unexpected error at 25%% each: %v", err)
	}
}

// ---- PublishGenesis error paths ---------------------------------------------

func TestPublishGenesis_NotFromWindowClosed(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	if err := l.PublishGenesis("abc"); err == nil {
		t.Error("expected error: cannot publish genesis from DRAFT")
	}
	_ = l.Publish("abc123")
	if err := l.PublishGenesis("abc"); err == nil {
		t.Error("expected error: cannot publish genesis from PUBLISHED")
	}
}

func TestPublishGenesis_EmptyHash(t *testing.T) {
	r := testRecord()
	r.MinValidatorCount = 1
	l, _ := launch.New(uuid.New(), r, launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	_ = l.Publish("abc123")
	_ = l.OpenWindow()
	_ = l.CloseWindow(1)
	if err := l.PublishGenesis(""); err == nil {
		t.Error("expected error: empty final genesis hash")
	}
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
	if err := l.MarkLaunched(); err != nil {
		t.Fatalf("MarkLaunched: %v", err)
	}
	if l.Status != launch.StatusLaunched {
		t.Errorf("expected LAUNCHED, got %s", l.Status)
	}
}

func TestMarkLaunched_NotFromGenesisReady(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	if err := l.MarkLaunched(); err == nil {
		t.Error("expected error: cannot mark launched from DRAFT")
	}
}

// ---- Full state machine happy path ------------------------------------------

func TestStateMachine_FullPath(t *testing.T) {
	r := testRecord()
	r.MinValidatorCount = 1
	l, err := launch.New(uuid.New(), r, launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
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
		if err := s.fn(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if l.Status != s.want {
			t.Errorf("after %s: want %s, got %s", s.name, s.want, l.Status)
		}
	}
}

// ---- CanValidatorApply ------------------------------------------------------

func TestCanValidatorApply_WindowOpen_Public(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	_ = l.Publish("abc123")
	_ = l.OpenWindow()
	addr := launch.MustNewOperatorAddress(testAddr4)
	if err := l.CanValidatorApply(addr); err != nil {
		t.Errorf("expected success for public WINDOW_OPEN, got: %v", err)
	}
}

func TestCanValidatorApply_NotWindowOpen(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	addr := launch.MustNewOperatorAddress(testAddr4)
	// DRAFT
	if err := l.CanValidatorApply(addr); err == nil {
		t.Error("expected error: window not open (DRAFT)")
	}
	// PUBLISHED
	_ = l.Publish("abc123")
	if err := l.CanValidatorApply(addr); err == nil {
		t.Error("expected error: window not open (PUBLISHED)")
	}
}

func TestCanValidatorApply_AllowlistAddressOnList(t *testing.T) {
	addr := launch.MustNewOperatorAddress(testAddr4)
	al := launch.NewAllowlist([]launch.OperatorAddress{addr})
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypePermissioned, launch.VisibilityAllowlist, testCommittee())
	l.Allowlist = al
	_ = l.Publish("abc123")
	_ = l.OpenWindow()
	if err := l.CanValidatorApply(addr); err != nil {
		t.Errorf("expected success for allowlisted address, got: %v", err)
	}
}

func TestCanValidatorApply_AllowlistAddressNotOnList(t *testing.T) {
	addr := launch.MustNewOperatorAddress(testAddr4)
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypePermissioned, launch.VisibilityAllowlist, testCommittee())
	l.Allowlist = launch.NewAllowlist(nil) // empty allowlist
	_ = l.Publish("abc123")
	_ = l.OpenWindow()
	if err := l.CanValidatorApply(addr); err == nil {
		t.Error("expected error: address not on allowlist")
	}
}

// ---- Voting power helpers ---------------------------------------------------

func TestRecordValidatorApproval_UpdatesExisting(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	addr := launch.MustNewOperatorAddress(testAddr1)
	l.RecordValidatorApproval(addr, 100)
	l.RecordValidatorApproval(addr, 50) // update
	if got := l.ApprovedVotingPowerOf(addr); got != 50 {
		t.Errorf("want 50, got %d", got)
	}
}

func TestRemoveValidatorApproval(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	addr := launch.MustNewOperatorAddress(testAddr1)
	l.RecordValidatorApproval(addr, 100)
	l.RemoveValidatorApproval(addr)
	if got := l.ApprovedVotingPowerOf(addr); got != 0 {
		t.Errorf("want 0 after removal, got %d", got)
	}
}

func TestApprovedVotingPowerOf_NotFound(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	addr := launch.MustNewOperatorAddress(testAddr1)
	if got := l.ApprovedVotingPowerOf(addr); got != 0 {
		t.Errorf("want 0 for unknown addr, got %d", got)
	}
}

func TestInitVotingPower(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	addr := launch.MustNewOperatorAddress(testAddr1)
	l.InitVotingPower(map[string]int64{addr.String(): 500})
	if got := l.ApprovedVotingPowerOf(addr); got != 500 {
		t.Errorf("want 500, got %d", got)
	}
}

// ---- Committee.HasMember ----------------------------------------------------

func TestHasMember_PresentAndAbsent(t *testing.T) {
	c := testCommittee()
	addr1 := launch.MustNewOperatorAddress(testAddr1)
	addr4 := launch.MustNewOperatorAddress(testAddr4)
	if !c.HasMember(addr1) {
		t.Error("addr1 should be a member")
	}
	if c.HasMember(addr4) {
		t.Error("addr4 should not be a member")
	}
}

// ---- IsVisibleTo edge cases -------------------------------------------------

func TestIsVisibleTo_InvalidAddress(t *testing.T) {
	al := launch.NewAllowlist([]launch.OperatorAddress{launch.MustNewOperatorAddress(testAddr1)})
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypePermissioned, launch.VisibilityAllowlist, testCommittee())
	l.Allowlist = al
	// An invalid bech32 string should be treated as "not visible".
	if l.IsVisibleTo("not-a-bech32-address") {
		t.Error("invalid address should not be visible")
	}
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

	if err := l.ReplaceCommitteeMember(oldAddr, newMember); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, m := range l.Committee.Members {
		if m.Address.String() == testAddr4 {
			found = true
		}
		if m.Address.String() == testAddr2 {
			t.Error("old member still in committee")
		}
	}
	if !found {
		t.Error("new member not in committee")
	}
}

func TestLaunch_ReplaceCommitteeMember_UpdatesLead(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	leadAddr := launch.MustNewOperatorAddress(testAddr1)
	newMember := launch.CommitteeMember{
		Address:   launch.MustNewOperatorAddress(testAddr4),
		Moniker:   "new-lead",
		PubKeyB64: "DDDD",
	}

	if err := l.ReplaceCommitteeMember(leadAddr, newMember); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l.Committee.LeadAddress.String() != testAddr4 {
		t.Errorf("lead not updated: got %s", l.Committee.LeadAddress)
	}
}

func TestLaunch_ReplaceCommitteeMember_NotFound(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	unknownAddr := launch.MustNewOperatorAddress(testAddr5)
	newMember := launch.CommitteeMember{Address: launch.MustNewOperatorAddress(testAddr4)}

	if err := l.ReplaceCommitteeMember(unknownAddr, newMember); err == nil {
		t.Fatal("expected error for unknown old address")
	}
}

// ---- ExpandCommittee --------------------------------------------------------

func TestLaunch_ExpandCommittee_Success(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	newMember := launch.CommitteeMember{
		Address:   launch.MustNewOperatorAddress(testAddr4),
		Moniker:   "coord-4",
		PubKeyB64: "DDDD",
	}

	if err := l.ExpandCommittee(newMember, 2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if l.Committee.TotalN != 4 {
		t.Errorf("TotalN: want 4, got %d", l.Committee.TotalN)
	}
	if l.Committee.ThresholdM != 2 {
		t.Errorf("ThresholdM: want 2, got %d", l.Committee.ThresholdM)
	}
	if len(l.Committee.Members) != 4 {
		t.Errorf("len(Members): want 4, got %d", len(l.Committee.Members))
	}
	found := false
	for _, m := range l.Committee.Members {
		if m.Address.String() == testAddr4 {
			found = true
		}
	}
	if !found {
		t.Error("new member not found in committee")
	}
}

func TestLaunch_ExpandCommittee_ExplicitThreshold(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	newMember := launch.CommitteeMember{
		Address:   launch.MustNewOperatorAddress(testAddr4),
		Moniker:   "coord-4",
		PubKeyB64: "DDDD",
	}

	if err := l.ExpandCommittee(newMember, 3); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l.Committee.ThresholdM != 3 {
		t.Errorf("ThresholdM: want 3, got %d", l.Committee.ThresholdM)
	}
}

func TestLaunch_ExpandCommittee_DuplicateMember(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	duplicate := launch.CommitteeMember{
		Address:   launch.MustNewOperatorAddress(testAddr2),
		Moniker:   "dup",
		PubKeyB64: "BBBB",
	}

	if err := l.ExpandCommittee(duplicate, 2); err == nil {
		t.Error("expected error for duplicate member address")
	}
}

func TestLaunch_ExpandCommittee_LivenessGuard(t *testing.T) {
	// 2-of-3 → expand to 4 members with threshold 4 (M == N) should be rejected.
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	newMember := launch.CommitteeMember{
		Address:   launch.MustNewOperatorAddress(testAddr4),
		Moniker:   "coord-4",
		PubKeyB64: "DDDD",
	}

	if err := l.ExpandCommittee(newMember, 4); err == nil {
		t.Error("expected liveness guard error: threshold must be < N")
	}
}

// ---- ShrinkCommittee --------------------------------------------------------

func TestLaunch_ShrinkCommittee_Success(t *testing.T) {
	// 2-of-3 → remove addr3 with threshold 1 → 1-of-2.
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	removeAddr := launch.MustNewOperatorAddress(testAddr3)

	if err := l.ShrinkCommittee(removeAddr, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if l.Committee.TotalN != 2 {
		t.Errorf("TotalN: want 2, got %d", l.Committee.TotalN)
	}
	if l.Committee.ThresholdM != 1 {
		t.Errorf("ThresholdM: want 1, got %d", l.Committee.ThresholdM)
	}
	if len(l.Committee.Members) != 2 {
		t.Errorf("len(Members): want 2, got %d", len(l.Committee.Members))
	}
	for _, m := range l.Committee.Members {
		if m.Address.String() == testAddr3 {
			t.Error("removed member still present in committee")
		}
	}
}

func TestLaunch_ShrinkCommittee_TransfersLeadWhenRemoved(t *testing.T) {
	// Remove the lead (addr1); lead should transfer to the first remaining member.
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	leadAddr := launch.MustNewOperatorAddress(testAddr1)

	if err := l.ShrinkCommittee(leadAddr, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l.Committee.LeadAddress.String() == testAddr1 {
		t.Error("lead not transferred after removed member was the lead")
	}
}

func TestLaunch_ShrinkCommittee_NonLeadDoesNotChangeLead(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	removeAddr := launch.MustNewOperatorAddress(testAddr3) // not the lead

	if err := l.ShrinkCommittee(removeAddr, 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l.Committee.LeadAddress.String() != testAddr1 {
		t.Errorf("lead changed unexpectedly: got %s", l.Committee.LeadAddress)
	}
}

func TestLaunch_ShrinkCommittee_MemberNotFound(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	unknownAddr := launch.MustNewOperatorAddress(testAddr5)

	if err := l.ShrinkCommittee(unknownAddr, 1); err == nil {
		t.Error("expected error for unknown member address")
	}
}

func TestLaunch_ShrinkCommittee_LivenessGuard(t *testing.T) {
	// 2-of-3 → remove addr3 with threshold 2 → would produce 2-of-2 (M == N), rejected.
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	removeAddr := launch.MustNewOperatorAddress(testAddr3)

	if err := l.ShrinkCommittee(removeAddr, 2); err == nil {
		t.Error("expected liveness guard error: threshold must be < N")
	}
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

	if err := l.ShrinkCommittee(launch.MustNewOperatorAddress(testAddr2), 1); err == nil {
		t.Error("expected error: cannot shrink to a 1-of-1 committee (liveness guard)")
	}
}

// ---- GenesisAccount ---------------------------------------------------------

func TestLaunch_AddGenesisAccount_Success(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	acc := launch.GenesisAccount{Address: testAddr2, Amount: "1000utest"}

	if err := l.AddGenesisAccount(acc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(l.GenesisAccounts) != 1 || l.GenesisAccounts[0].Address != testAddr2 {
		t.Errorf("genesis account not added: %+v", l.GenesisAccounts)
	}
}

func TestLaunch_AddGenesisAccount_Duplicate(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	acc := launch.GenesisAccount{Address: testAddr2, Amount: "1000utest"}
	_ = l.AddGenesisAccount(acc)

	if err := l.AddGenesisAccount(acc); err == nil {
		t.Fatal("expected error for duplicate genesis account")
	}
}

func TestLaunch_RemoveGenesisAccount_Success(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	_ = l.AddGenesisAccount(launch.GenesisAccount{Address: testAddr2, Amount: "1000utest"})

	if err := l.RemoveGenesisAccount(testAddr2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(l.GenesisAccounts) != 0 {
		t.Errorf("genesis account not removed")
	}
}

func TestLaunch_RemoveGenesisAccount_NotFound(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())

	if err := l.RemoveGenesisAccount(testAddr2); err == nil {
		t.Fatal("expected error for non-existent account")
	}
}

func TestLaunch_ModifyGenesisAccount_Success(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	_ = l.AddGenesisAccount(launch.GenesisAccount{Address: testAddr2, Amount: "1000utest"})

	if err := l.ModifyGenesisAccount(testAddr2, "9999utest", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l.GenesisAccounts[0].Amount != "9999utest" {
		t.Errorf("amount not updated: %s", l.GenesisAccounts[0].Amount)
	}
}

func TestLaunch_ModifyGenesisAccount_NotFound(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())

	if err := l.ModifyGenesisAccount(testAddr2, "9999utest", nil); err == nil {
		t.Fatal("expected error for non-existent account")
	}
}

func TestLaunch_GenesisAccountsLockedAfterPublish(t *testing.T) {
	l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
	l.Status = launch.StatusGenesisReady // genesis published — account set is frozen

	if err := l.AddGenesisAccount(launch.GenesisAccount{Address: testAddr2, Amount: "1000utest"}); err == nil {
		t.Error("AddGenesisAccount: expected error once genesis is published")
	}
	if err := l.RemoveGenesisAccount(testAddr2); err == nil {
		t.Error("RemoveGenesisAccount: expected error once genesis is published")
	}
	if err := l.ModifyGenesisAccount(testAddr2, "2000utest", nil); err == nil {
		t.Error("ModifyGenesisAccount: expected error once genesis is published")
	}
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
			if err := l.Cancel(); err != nil {
				t.Fatalf("Cancel from %s: unexpected error: %v", tc.name, err)
			}
			if l.Status != launch.StatusCancelled {
				t.Errorf("want CANCELED, got %s", l.Status)
			}
		})
	}
}

func TestCancel_TerminalStatuses_Rejected(t *testing.T) {
	t.Run("LAUNCHED", func(t *testing.T) {
		l := advanceToGenesisReady(t)
		_ = l.MarkLaunched()
		if err := l.Cancel(); err == nil {
			t.Error("expected error: cannot cancel LAUNCHED chain")
		}
	})
	t.Run("CANCELED", func(t *testing.T) {
		l, _ := launch.New(uuid.New(), testRecord(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee())
		_ = l.Cancel()
		if err := l.Cancel(); err == nil {
			t.Error("expected error: already CANCELED")
		}
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
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = l.Publish("initial-hash")
	_ = l.OpenWindow()
	_ = l.CloseWindow(1)
	_ = l.PublishGenesis("final-hash")
	return l
}

func TestReopenForRevision_Success(t *testing.T) {
	l := advanceToGenesisReady(t)

	if err := l.ReopenForRevision(); err != nil {
		t.Fatalf("ReopenForRevision: %v", err)
	}
	if l.Status != launch.StatusWindowClosed {
		t.Errorf("expected WINDOW_CLOSED, got %s", l.Status)
	}
	if l.FinalGenesisSHA256 != "" {
		t.Errorf("expected FinalGenesisSHA256 cleared, got %q", l.FinalGenesisSHA256)
	}
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
			if err := l.ReopenForRevision(); err == nil {
				t.Errorf("expected error when calling ReopenForRevision from %s", tc.name)
			}
		})
	}
}

// ---- ReadinessConfirmation --------------------------------------------------

func TestReadinessConfirmation_IsValid(t *testing.T) {
	rc := launch.ReadinessConfirmation{}
	if !rc.IsValid() {
		t.Error("new confirmation should be valid")
	}
}

func TestReadinessConfirmation_Invalidate(t *testing.T) {
	rc := launch.ReadinessConfirmation{}
	at := time.Now().UTC()
	rc.Invalidate(at)
	if rc.IsValid() {
		t.Error("invalidated confirmation should not be valid")
	}
	if rc.InvalidatedAt == nil || !rc.InvalidatedAt.Equal(at) {
		t.Error("InvalidatedAt should be set to the given time")
	}
}
