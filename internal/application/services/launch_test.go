package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/joinrequest"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

func newLaunchSvc(launchRepo *fakeLaunchRepo, genesisStore *fakeGenesisStore) *LaunchService {
	return NewLaunchService(launchRepo, newFakeJoinRequestRepo(), newFakeReadinessRepo(), genesisStore, newFakeAllocationStore(), &fakeEventPublisher{}, &fakeAuditLogWriter{}, newFakeRehearsalAttemptRepo(), newFakeRehearsalResultRepo())
}

// --- CreateLaunch ---

func TestLaunchService_CreateLaunch_Success(t *testing.T) {
	repo := newFakeLaunchRepo()
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	l, err := svc.CreateLaunch(context.Background(), CreateLaunchInput{
		Record:     testChainRecord(),
		LaunchType: launch.LaunchTypeTestnet,
		Committee:  testCommittee(1, 1),
	})
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, l.ID, "expected a non-nil launch ID")
	_, err = repo.FindByID(context.Background(), l.ID)
	require.NoError(t, err, "launch not persisted")
}

func TestLaunchService_CreateLaunch_SaveFails(t *testing.T) {
	repo := newFakeLaunchRepo()
	repo.saveErr = errors.New("db error")
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	_, err := svc.CreateLaunch(context.Background(), CreateLaunchInput{
		Record:    testChainRecord(),
		Committee: testCommittee(1, 1),
	})
	require.Error(t, err, "expected error when save fails")
}

// --- UploadInitialGenesis ---

func validGenesisJSON(chainID string) []byte {
	return []byte(`{"chain_id":"` + chainID + `","app_state":{}}`)
}

// validFinalGenesisJSON returns a minimal final genesis with a future genesis_time
// and no gen_txs (matches an empty approved validator set).
func validFinalGenesisJSON(chainID string) []byte {
	return []byte(`{"chain_id":"` + chainID + `","genesis_time":"2030-01-01T00:00:00Z","app_state":{"genutil":{"gen_txs":[]}}}`)
}

func TestLaunchService_UploadInitialGenesis_LaunchNotFound(t *testing.T) {
	svc := newLaunchSvc(newFakeLaunchRepo(), newFakeGenesisStore())
	_, err := svc.UploadInitialGenesis(context.Background(), uuid.New(), validGenesisJSON("testchain-1"), testAddr1)
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestLaunchService_UploadInitialGenesis_WrongStatus(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusPublished // not DRAFT
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	_, err := svc.UploadInitialGenesis(context.Background(), l.ID, validGenesisJSON(l.Record.ChainID), testAddr1)
	require.ErrorIs(t, err, ports.ErrConflict, "uploading to a non-DRAFT launch is a state conflict")
}

func TestLaunchService_UploadInitialGenesis_InvalidJSON(t *testing.T) {
	l := testLaunch()
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	_, err := svc.UploadInitialGenesis(context.Background(), l.ID, []byte("not-json"), testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest, "invalid JSON genesis is a 400")
}

func TestLaunchService_UploadInitialGenesis_ChainIDMismatch(t *testing.T) {
	l := testLaunch()
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	_, err := svc.UploadInitialGenesis(context.Background(), l.ID, validGenesisJSON("wrong-chain-id"), testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest, "chain_id mismatch is a 400")
}

func TestLaunchService_UploadInitialGenesis_Success(t *testing.T) {
	l := testLaunch()
	repo := newFakeLaunchRepo(l)
	genesis := newFakeGenesisStore()
	svc := newLaunchSvc(repo, genesis)

	hash, err := svc.UploadInitialGenesis(context.Background(), l.ID, validGenesisJSON(l.Record.ChainID), testAddr1)
	require.NoError(t, err)
	require.NotEmpty(t, hash, "expected non-empty SHA256 hash")
	_, ok := genesis.initial[l.ID.String()]
	require.True(t, ok, "genesis not stored")
	stored, _ := repo.FindByID(context.Background(), l.ID)
	assert.Equal(t, hash, stored.InitialGenesisSHA256, "hash not persisted on launch")
}

func TestLaunchService_UploadInitialGenesis_NonCommitteeMember(t *testing.T) {
	l := test1of1Launch() // DRAFT; committee = {testAddr1}
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	// testAddr2 is a valid address but not a member of this launch's committee.
	_, err := svc.UploadInitialGenesis(context.Background(), l.ID, validGenesisJSON(l.Record.ChainID), testAddr2)
	require.ErrorIs(t, err, ports.ErrForbidden, "want ErrForbidden for non-committee caller")
}

// --- UploadFinalGenesis ---

func TestLaunchService_UploadFinalGenesis_WrongStatus(t *testing.T) {
	l := testLaunch() // DRAFT
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	_, err := svc.UploadFinalGenesis(context.Background(), l.ID, validGenesisJSON(l.Record.ChainID), testAddr1)
	require.ErrorIs(t, err, ports.ErrConflict, "DRAFT launch (need WINDOW_CLOSED) is a state conflict")
}

func TestLaunchService_UploadFinalGenesis_Success(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowClosed
	repo := newFakeLaunchRepo(l)
	genesis := newFakeGenesisStore()
	svc := newLaunchSvc(repo, genesis)

	hash, err := svc.UploadFinalGenesis(context.Background(), l.ID, validFinalGenesisJSON(l.Record.ChainID), testAddr1)
	require.NoError(t, err)
	require.NotEmpty(t, hash, "expected non-empty SHA256 hash")
	_, ok := genesis.final[l.ID.String()]
	require.True(t, ok, "final genesis not stored")

	// The genesis_time from the file must be synced into the launch record.
	stored, _ := repo.FindByID(context.Background(), l.ID)
	wantTime := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NotNil(t, stored.Record.GenesisTime, "GenesisTime not synced from genesis file")
	assert.True(t, stored.Record.GenesisTime.Equal(wantTime), "GenesisTime: want %s, got %s", wantTime, stored.Record.GenesisTime)
}

// --- UploadFinalGenesis structural validation (M2) ---

// newLaunchSvcWithJR creates a LaunchService with a custom join request repo.
func newLaunchSvcWithJR(launchRepo *fakeLaunchRepo, jrRepo *fakeJoinRequestRepo, genesisStore *fakeGenesisStore) *LaunchService {
	return NewLaunchService(launchRepo, jrRepo, newFakeReadinessRepo(), genesisStore, newFakeAllocationStore(), &fakeEventPublisher{}, &fakeAuditLogWriter{}, newFakeRehearsalAttemptRepo(), newFakeRehearsalResultRepo())
}

func TestLaunchService_UploadFinalGenesis_GenesisTimeZero(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowClosed
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	noTime := []byte(`{"chain_id":"` + l.Record.ChainID + `","genesis_time":"0001-01-01T00:00:00Z","app_state":{"genutil":{"gen_txs":[]}}}`)
	_, err := svc.UploadFinalGenesis(context.Background(), l.ID, noTime, testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest, "zero genesis_time is a 400")
}

func TestLaunchService_UploadFinalGenesis_GenesisTimePast(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowClosed
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	past := []byte(`{"chain_id":"` + l.Record.ChainID + `","genesis_time":"2000-01-01T00:00:00Z","app_state":{"genutil":{"gen_txs":[]}}}`)
	_, err := svc.UploadFinalGenesis(context.Background(), l.ID, past, testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest, "past genesis_time is a 400")
}

func TestLaunchService_UploadFinalGenesis_MissingApprovedValidator(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowClosed

	jr := makeApprovedJoinRequest(t, l.ID, testAddr2, "pubkeyAAA")
	jrRepo := newFakeJoinRequestRepo(jr)
	svc := newLaunchSvcWithJR(newFakeLaunchRepo(l), jrRepo, newFakeGenesisStore())

	// No gen_txs but one approved validator → mismatch
	noGenTx := []byte(`{"chain_id":"` + l.Record.ChainID + `","genesis_time":"2030-01-01T00:00:00Z","app_state":{"genutil":{"gen_txs":[]}}}`)
	_, err := svc.UploadFinalGenesis(context.Background(), l.ID, noGenTx, testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest, "approved validator missing from gen_txs is a 400")
}

func TestLaunchService_UploadFinalGenesis_UnapprovedGentx(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowClosed
	// No approved validators but genesis has one gentx.
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	withExtra := []byte(`{"chain_id":"` + l.Record.ChainID + `","genesis_time":"2030-01-01T00:00:00Z","app_state":{"genutil":{"gen_txs":[{"body":{"messages":[{"pubkey":{"key":"AAEC"}}]}}]}}}`)
	_, err := svc.UploadFinalGenesis(context.Background(), l.ID, withExtra, testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest, "unapproved gentx in genesis is a 400")
}

func TestLaunchService_UploadFinalGenesis_ValidWithApprovedValidator(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowClosed

	const pubKey = "AAEC"
	jr := makeApprovedJoinRequest(t, l.ID, testAddr2, pubKey)
	jrRepo := newFakeJoinRequestRepo(jr)
	svc := newLaunchSvcWithJR(newFakeLaunchRepo(l), jrRepo, newFakeGenesisStore())

	data := []byte(`{"chain_id":"` + l.Record.ChainID + `","genesis_time":"2030-01-01T00:00:00Z","app_state":{"genutil":{"gen_txs":[{"body":{"messages":[{"pubkey":{"key":"` + pubKey + `"}}]}}]}}}`)
	hash, err := svc.UploadFinalGenesis(context.Background(), l.ID, data, testAddr1)
	require.NoError(t, err)
	require.NotEmpty(t, hash, "expected non-empty hash")
}

// --- UploadInitialGenesisRef ---

const validSHA256 = "a3f9b72c1d4e8f05a6b2c3d4e5f67890a1b2c3d4e5f6789012345678901234ab"

func TestLaunchService_UploadInitialGenesisRef_WrongStatus(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusPublished
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	err := svc.UploadInitialGenesisRef(context.Background(), l.ID, "https://example.com/genesis.json", validSHA256, testAddr1)
	require.ErrorIs(t, err, ports.ErrConflict, "non-DRAFT launch is a state conflict")
}

func TestLaunchService_UploadInitialGenesisRef_InvalidURL(t *testing.T) {
	l := testLaunch() // DRAFT
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	err := svc.UploadInitialGenesisRef(context.Background(), l.ID, "not-a-url", validSHA256, testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest, "want ErrBadRequest for invalid URL")
}

func TestLaunchService_UploadInitialGenesisRef_InvalidSHA256(t *testing.T) {
	l := testLaunch() // DRAFT
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	err := svc.UploadInitialGenesisRef(context.Background(), l.ID, "https://example.com/genesis.json", "tooshort", testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest, "want ErrBadRequest for invalid sha256")
}

func TestLaunchService_UploadInitialGenesisRef_Success(t *testing.T) {
	l := testLaunch() // DRAFT
	genesis := newFakeGenesisStore()
	svc := newLaunchSvc(newFakeLaunchRepo(l), genesis)

	err := svc.UploadInitialGenesisRef(context.Background(), l.ID, "https://example.com/genesis.json", validSHA256, testAddr1)
	require.NoError(t, err)
	ref, ok := genesis.initialRef[l.ID.String()]
	require.True(t, ok, "ref not stored")
	assert.Equal(t, "https://example.com/genesis.json", ref.ExternalURL)
	assert.Equal(t, validSHA256, ref.SHA256)
}

// --- UploadFinalGenesisRef ---

func TestLaunchService_UploadFinalGenesisRef_WrongStatus(t *testing.T) {
	l := testLaunch() // DRAFT
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())
	futureTime := time.Now().Add(48 * time.Hour).UTC()

	err := svc.UploadFinalGenesisRef(context.Background(), l.ID, "https://example.com/genesis.json", validSHA256, futureTime, testAddr1)
	require.ErrorIs(t, err, ports.ErrConflict, "non-WINDOW_CLOSED launch is a state conflict")
}

func TestLaunchService_UploadFinalGenesisRef_ZeroGenesisTime(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowClosed
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	err := svc.UploadFinalGenesisRef(context.Background(), l.ID, "https://example.com/final-genesis.json", validSHA256, time.Time{}, testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest, "want ErrBadRequest for zero genesis_time")
}

func TestLaunchService_UploadFinalGenesisRef_PastGenesisTime(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowClosed
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	err := svc.UploadFinalGenesisRef(context.Background(), l.ID, "https://example.com/final-genesis.json", validSHA256, time.Now().Add(-1*time.Hour).UTC(), testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest, "want ErrBadRequest for past genesis_time")
}

func TestLaunchService_UploadFinalGenesisRef_Success(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowClosed
	genesis := newFakeGenesisStore()
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, genesis)
	futureTime := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)

	err := svc.UploadFinalGenesisRef(context.Background(), l.ID, "https://example.com/final-genesis.json", validSHA256, futureTime, testAddr1)
	require.NoError(t, err)
	ref, ok := genesis.finalRef[l.ID.String()]
	require.True(t, ok, "ref not stored")
	assert.Equal(t, "https://example.com/final-genesis.json", ref.ExternalURL)
	stored, _ := repo.FindByID(context.Background(), l.ID)
	require.NotNil(t, stored.Record.GenesisTime, "GenesisTime not synced into launch record")
	assert.True(t, stored.Record.GenesisTime.Equal(futureTime), "GenesisTime: want %s, got %s", futureTime, stored.Record.GenesisTime)
}

// makeApprovedJoinRequest creates a JoinRequest with the given consensusPubKey already approved.
func makeApprovedJoinRequest(t *testing.T, launchID uuid.UUID, operatorAddr, consensusPubKey string) *joinrequest.JoinRequest {
	t.Helper()
	jr := makeJoinRequest(t, launchID, operatorAddr)
	jr.ConsensusPubKey = consensusPubKey
	proposalID := uuid.New()
	require.NoError(t, jr.Approve(proposalID), "approve join request")
	return jr
}

// --- PatchLaunch ---

func TestLaunchService_PatchLaunch_LaunchNotFound(t *testing.T) {
	svc := newLaunchSvc(newFakeLaunchRepo(), newFakeGenesisStore())
	_, err := svc.PatchLaunch(context.Background(), uuid.New(), PatchLaunchInput{}, testAddr1)
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestLaunchService_PatchLaunch_NotCommitteeMember(t *testing.T) {
	// Use a 1-of-1 committee with only testAddr1; testAddr2 is not a member.
	l := testLaunch()
	l.Committee = testCommittee(1, 1) // only testAddr1
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	name := "new-name"
	_, err := svc.PatchLaunch(context.Background(), l.ID, PatchLaunchInput{ChainName: &name}, testAddr2)
	require.ErrorIs(t, err, ports.ErrForbidden)
}

// ---- M2 members management ----

func TestLaunchService_AddMember_Success(t *testing.T) {
	l := test1of1Launch() // committee = testAddr1; status DRAFT
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	m, err := svc.AddMember(context.Background(), l.ID, testAddr2, "acme-fleet", testAddr1)
	require.NoError(t, err)
	assert.Equal(t, testAddr2, m.Address.String())
	assert.Equal(t, "acme-fleet", m.Label)
	assert.Equal(t, testAddr1, m.AddedBy, "provenance records the adding committee member")
	assert.False(t, m.AddedAt.IsZero(), "added_at is stamped")

	got, err := repo.FindByID(context.Background(), l.ID)
	require.NoError(t, err)
	assert.Equal(t, "acme-fleet", got.Allowlist.Label(mustAddr(testAddr2)), "member persisted with label")
	assert.True(t, got.IsVisibleTo(testAddr2), "an added member can now see + submit to the launch")
}

func TestLaunchService_AddMember_UsableDuringWindowOpen(t *testing.T) {
	l := test1of1Launch()
	l.Status = launch.StatusWindowOpen // onboarding during the open window is the point (E1)
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	_, err := svc.AddMember(context.Background(), l.ID, testAddr2, "late-joiner", testAddr1)
	require.NoError(t, err, "members must be addable during WINDOW_OPEN")
}

func TestLaunchService_AddMember_NotCommittee(t *testing.T) {
	l := test1of1Launch() // committee = testAddr1 only
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())
	_, err := svc.AddMember(context.Background(), l.ID, testAddr3, "x", testAddr2) // testAddr2 not committee
	require.ErrorIs(t, err, ports.ErrForbidden)
}

func TestLaunchService_AddMember_WrongStatus(t *testing.T) {
	l := test1of1Launch()
	l.Status = launch.StatusWindowClosed // frozen (E1)
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())
	_, err := svc.AddMember(context.Background(), l.ID, testAddr2, "x", testAddr1)
	require.ErrorIs(t, err, ports.ErrConflict, "members list is frozen after WINDOW_OPEN")
}

func TestLaunchService_AddMember_InvalidAddress(t *testing.T) {
	l := test1of1Launch()
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())
	_, err := svc.AddMember(context.Background(), l.ID, "not-a-bech32-addr", "x", testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest)
}

func TestLaunchService_AddMember_LabelTooLong(t *testing.T) {
	l := test1of1Launch()
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())
	_, err := svc.AddMember(context.Background(), l.ID, testAddr2, strings.Repeat("a", maxMemberLabelLen+1), testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest)
}

func TestLaunchService_RemoveMember_Success(t *testing.T) {
	l := test1of1Launch()
	l.Allowlist = launch.NewAllowlistFromMembers([]launch.Member{{Address: mustAddr(testAddr2), Label: "x"}})
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	require.NoError(t, svc.RemoveMember(context.Background(), l.ID, testAddr2, testAddr1))
	got, err := repo.FindByID(context.Background(), l.ID)
	require.NoError(t, err)
	assert.False(t, got.Allowlist.Contains(mustAddr(testAddr2)), "removed member no longer on the list")
	assert.False(t, got.IsVisibleTo(testAddr2), "removed member loses see + submit access")
}

func TestLaunchService_RemoveMember_Absent(t *testing.T) {
	l := test1of1Launch() // empty allowlist
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())
	err := svc.RemoveMember(context.Background(), l.ID, testAddr2, testAddr1)
	require.ErrorIs(t, err, ports.ErrNotFound, "removing a non-member is 404")
}

func TestLaunchService_RemoveMember_NotCommittee(t *testing.T) {
	l := test1of1Launch()
	l.Allowlist = launch.NewAllowlistFromMembers([]launch.Member{{Address: mustAddr(testAddr2), Label: "x"}})
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())
	err := svc.RemoveMember(context.Background(), l.ID, testAddr2, testAddr3) // testAddr3 not committee
	require.ErrorIs(t, err, ports.ErrForbidden)
}

func TestLaunchService_ListMembers_SortedForCommittee(t *testing.T) {
	l := test1of1Launch()
	l.Allowlist = launch.NewAllowlistFromMembers([]launch.Member{
		{Address: mustAddr(testAddr3), Label: "c"},
		{Address: mustAddr(testAddr2), Label: "b"},
	})
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	members, err := svc.ListMembers(context.Background(), l.ID, testAddr1)
	require.NoError(t, err)
	require.Len(t, members, 2)
	for i := 1; i < len(members); i++ {
		assert.Less(t, members[i-1].Address.Hex(), members[i].Address.Hex(), "members sorted by account")
	}
}

func TestLaunchService_ListMembers_NotCommittee(t *testing.T) {
	l := test1of1Launch()
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())
	_, err := svc.ListMembers(context.Background(), l.ID, testAddr2) // not committee
	require.ErrorIs(t, err, ports.ErrForbidden, "member list is committee-only")
}

func TestLaunchService_PatchLaunch_DraftFieldsOnNonDraft(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen // past DRAFT
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	name := "updated-name"
	_, err := svc.PatchLaunch(context.Background(), l.ID, PatchLaunchInput{ChainName: &name}, testAddr1)
	require.ErrorIs(t, err, ports.ErrConflict, "draft-only field on a non-DRAFT launch is a state conflict (409)")
}

func TestLaunchService_PatchLaunch_MonitorRPCURLOnAnyStatus(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	// 203.0.113.x is documentation/TEST-NET-3 — public, not in any blocked range.
	url := "http://203.0.113.1:26657"
	updated, err := svc.PatchLaunch(context.Background(), l.ID, PatchLaunchInput{MonitorRPCURL: &url}, testAddr1)
	require.NoError(t, err)
	assert.Equal(t, url, updated.MonitorRPCURL)
}

func TestLaunchService_PatchLaunch_MonitorRPCURL_PrivateIPRejected(t *testing.T) {
	privateURLs := []string{
		"http://127.0.0.1:26657",
		"http://192.168.1.1:26657",
		"http://10.0.0.1:26657",
		"http://172.16.0.1:26657",
		"http://169.254.169.254/latest/meta-data/",
		"http://localhost:26657",
	}
	for _, privateURL := range privateURLs {
		t.Run(privateURL, func(t *testing.T) {
			l := testLaunch()
			repo := newFakeLaunchRepo(l)
			svc := newLaunchSvc(repo, newFakeGenesisStore())

			u := privateURL
			_, err := svc.PatchLaunch(context.Background(), l.ID, PatchLaunchInput{MonitorRPCURL: &u}, testAddr1)
			require.Error(t, err, "expected error for private URL %q", privateURL)
			assert.ErrorIs(t, err, ports.ErrBadRequest)
		})
	}
}

func TestLaunchService_PatchLaunch_MonitorRPCURL_EmptyAllowed(t *testing.T) {
	// Empty string clears the URL — should not trigger validation.
	l := testLaunch()
	l.MonitorRPCURL = "http://203.0.113.1:26657"
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	empty := ""
	updated, err := svc.PatchLaunch(context.Background(), l.ID, PatchLaunchInput{MonitorRPCURL: &empty}, testAddr1)
	require.NoError(t, err, "unexpected error clearing MonitorRPCURL")
	assert.Empty(t, updated.MonitorRPCURL)
}

func TestLaunchService_PatchLaunch_DraftSuccess(t *testing.T) {
	l := testLaunch()
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	name := "new-name"
	updated, err := svc.PatchLaunch(context.Background(), l.ID, PatchLaunchInput{ChainName: &name}, testAddr1)
	require.NoError(t, err)
	assert.Equal(t, name, updated.Record.ChainName)
}

func TestLaunchService_PatchLaunch_AllDraftFields(t *testing.T) {
	// Exercises every branch of applyDraftFields in one PATCH.
	l := testLaunch() // DRAFT
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	name := "chain-x"
	binVer := "v2.3.4"
	binHash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	repoURL := "https://github.com/example/chain"
	repoCommit := "abc123def"
	gt := time.Now().Add(72 * time.Hour).UTC()
	minVal := 7
	allow := []launch.AccountID{mustAddr(testAddr2)}

	updated, err := svc.PatchLaunch(context.Background(), l.ID, PatchLaunchInput{
		ChainName:         &name,
		BinaryVersion:     &binVer,
		BinarySHA256:      &binHash,
		RepoURL:           &repoURL,
		RepoCommit:        &repoCommit,
		GenesisTime:       &gt,
		MinValidatorCount: &minVal,
		Allowlist:         allow,
	}, testAddr1)
	require.NoError(t, err)
	assert.Equal(t, name, updated.Record.ChainName)
	assert.Equal(t, binVer, updated.Record.BinaryVersion)
	assert.Equal(t, binHash, updated.Record.BinarySHA256)
	assert.Equal(t, repoURL, updated.Record.RepoURL)
	assert.Equal(t, repoCommit, updated.Record.RepoCommit)
	require.NotNil(t, updated.Record.GenesisTime)
	assert.True(t, updated.Record.GenesisTime.Equal(gt))
	assert.Equal(t, minVal, updated.Record.MinValidatorCount)
	assert.True(t, updated.Allowlist.Contains(mustAddr(testAddr2)), "allowlist not applied")
}

func TestLaunchService_UploadFinalGenesis_GentxNoMessages(t *testing.T) {
	// One approved validator and one gentx that has no messages: the gen_txs count
	// matches, so validation reaches extractGenTxPubKey, which rejects it as a 400.
	l := testLaunch()
	l.Status = launch.StatusWindowClosed
	jr := makeApprovedJoinRequest(t, l.ID, testAddr2, "AAEC")
	svc := newLaunchSvcWithJR(newFakeLaunchRepo(l), newFakeJoinRequestRepo(jr), newFakeGenesisStore())

	noMsgs := []byte(`{"chain_id":"` + l.Record.ChainID + `","genesis_time":"2030-01-01T00:00:00Z","app_state":{"genutil":{"gen_txs":[{"body":{"messages":[]}}]}}}`)
	_, err := svc.UploadFinalGenesis(context.Background(), l.ID, noMsgs, testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest, "a gentx with no messages is a 400")
}

// --- PatchLaunch bridge fields (B0) ---

func TestLaunchService_PatchLaunch_RehearsalFields(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen // operational fields settable at any status
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	pk := osmosisPubKey // a valid 32-byte ed25519 key, base64
	ep := "https://rehearsal.example.com"
	updated, err := svc.PatchLaunch(context.Background(), l.ID, PatchLaunchInput{
		RehearsalServicePubKey: &pk,
		RehearsalEndpoint:      &ep,
	}, testAddr1)
	require.NoError(t, err)
	assert.Equal(t, osmosisPubKey, updated.RehearsalServicePubKey)
	assert.Equal(t, ep, updated.RehearsalEndpoint)
}

func TestLaunchService_PatchLaunch_BadRehearsalPubKey(t *testing.T) {
	l := testLaunch()
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())
	bad := "not-valid-base64!!!"
	_, err := svc.PatchLaunch(context.Background(), l.ID,
		PatchLaunchInput{RehearsalServicePubKey: &bad}, testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest, "a malformed trusted pubkey is a 400")
}

func TestLaunchService_PatchLaunch_BadTotalSupply(t *testing.T) {
	l := testLaunch() // DRAFT — total_supply is a draft-only field
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())
	bad := "-5"
	_, err := svc.PatchLaunch(context.Background(), l.ID,
		PatchLaunchInput{TotalSupply: &bad}, testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest, "a negative total_supply is a 400")
}

func TestLaunchService_PatchLaunch_RevalidatesRecord(t *testing.T) {
	// Patching any chain field to an invalid value re-validates the whole record → 400.
	l := testLaunch() // DRAFT
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())
	badHash := "not-a-64-char-hex"
	_, err := svc.PatchLaunch(context.Background(), l.ID,
		PatchLaunchInput{BinarySHA256: &badHash}, testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest, "patching binary_sha256 to garbage must 400")
}

// --- SetCommittee ---

func TestLaunchService_SetCommittee_NotDraft(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusPublished
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	err := svc.SetCommittee(context.Background(), l.ID, testCommittee(1, 1), testAddr1)
	require.ErrorIs(t, err, ports.ErrConflict, "a non-DRAFT launch is a state conflict (409)")
}

func TestLaunchService_SetCommittee_NotLead(t *testing.T) {
	l := testLaunch() // lead is testAddr1
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	err := svc.SetCommittee(context.Background(), l.ID, testCommittee(1, 1), testAddr2)
	require.ErrorIs(t, err, ports.ErrForbidden, "want ErrForbidden for non-lead caller")
}

func TestLaunchService_SetCommittee_BadThreshold(t *testing.T) {
	l := testLaunch()
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	bad := testCommittee(1, 1)
	bad.ThresholdM = 5 // > TotalN
	err := svc.SetCommittee(context.Background(), l.ID, bad, testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest, "threshold > total_n is a 400")
}

func TestLaunchService_SetCommittee_MemberCountMismatch(t *testing.T) {
	l := testLaunch()
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	bad := testCommittee(1, 2)
	bad.Members = bad.Members[:1] // says TotalN=2 but only 1 member
	err := svc.SetCommittee(context.Background(), l.ID, bad, testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest, "member count mismatch is a 400")
}

func TestLaunchService_SetCommittee_Success(t *testing.T) {
	l := testLaunch()
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	newComm := testCommittee(1, 1)
	require.NoError(t, svc.SetCommittee(context.Background(), l.ID, newComm, testAddr1))
	stored, _ := repo.FindByID(context.Background(), l.ID)
	assert.Equal(t, 1, stored.Committee.ThresholdM, "committee not updated")
}

// --- UploadAllocationFile ---

func TestLaunchService_UploadAllocationFileBytes_UnknownType(t *testing.T) {
	l := testLaunch()
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	_, err := svc.UploadAllocationFileBytes(context.Background(), l.ID, "bogus-type", []byte("data"), testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest, "an unknown allocation type is a 400")
}

// A locked allocation set (status frozen) maps to 409 via mapAllocationDomainErr,
// preserving the domain sentinel — this is the path that was previously 0% covered.
func TestLaunchService_UploadAllocationFileBytes_LockedIsConflict(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusGenesisReady // allocations are frozen past this point
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	_, err := svc.UploadAllocationFileBytes(context.Background(), l.ID, string(launch.AllocationAccounts), []byte("data"), testAddr1)
	require.ErrorIs(t, err, ports.ErrConflict, "a frozen allocation set is a state conflict")
	require.ErrorIs(t, err, launch.ErrAllocationLocked, "and preserves the domain sentinel")
}

func TestLaunchService_UploadAllocationFileRef_UnknownType(t *testing.T) {
	l := testLaunch()
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	err := svc.UploadAllocationFileRef(context.Background(), l.ID, "bogus-type", "https://example.com/a.csv", validSHA256, testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest, "an unknown allocation type is a 400")
}

// --- GetLaunch ---

func TestLaunchService_GetLaunch_NotFound(t *testing.T) {
	svc := newLaunchSvc(newFakeLaunchRepo(), newFakeGenesisStore())
	_, err := svc.GetLaunch(context.Background(), uuid.New(), "")
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestLaunchService_GetLaunch_Hidden(t *testing.T) {
	// A private launch is invisible to a caller who is neither committee nor allowlisted.
	l := test1of1Launch() // committee = testAddr1 only; empty allowlist
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	_, err := svc.GetLaunch(context.Background(), l.ID, testAddr2) // not committee, not allowlisted
	require.ErrorIs(t, err, ports.ErrNotFound, "want ErrNotFound for a launch the caller can't see")
}

func TestLaunchService_GetLaunch_Success(t *testing.T) {
	l := testLaunch()
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	// testAddr1 is a committee member → the private launch is visible to it.
	got, err := svc.GetLaunch(context.Background(), l.ID, testAddr1)
	require.NoError(t, err)
	assert.Equal(t, l.ID, got.ID)
}

// --- GetDashboard ---

func TestLaunchService_GetDashboard_WithGenesisTime(t *testing.T) {
	l := testLaunch()
	gt := time.Now().Add(24 * time.Hour)
	l.Record.GenesisTime = &gt
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	dash, err := svc.GetDashboard(context.Background(), l.ID, testAddr1)
	require.NoError(t, err)
	assert.NotNil(t, dash.Countdown, "expected non-nil countdown when genesis time is set")
}

func TestLaunchService_GetDashboard_NoGenesisTime(t *testing.T) {
	l := testLaunch()
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	dash, err := svc.GetDashboard(context.Background(), l.ID, testAddr1)
	require.NoError(t, err)
	assert.Nil(t, dash.Countdown, "expected nil countdown when no genesis time set")
}

// --- OpenWindow ---

func TestLaunchService_OpenWindow_Success(t *testing.T) {
	l := testLaunch()
	require.NoError(t, l.Publish("1111111111111111111111111111111111111111111111111111111111111111"))
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	require.NoError(t, svc.OpenWindow(context.Background(), l.ID, testAddr1))
	got, _ := repo.FindByID(context.Background(), l.ID)
	assert.Equal(t, launch.StatusWindowOpen, got.Status)
}

func TestLaunchService_OpenWindow_NotFound(t *testing.T) {
	svc := newLaunchSvc(newFakeLaunchRepo(), newFakeGenesisStore())
	err := svc.OpenWindow(context.Background(), uuid.New(), testAddr1)
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestLaunchService_OpenWindow_DraftWithoutGenesis_BadRequest(t *testing.T) {
	l := testLaunch() // DRAFT, no genesis uploaded
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())
	err := svc.OpenWindow(context.Background(), l.ID, testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest)
}

func TestLaunchService_OpenWindow_WrongStatus(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen // already open — invalid transition
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())
	err := svc.OpenWindow(context.Background(), l.ID, testAddr1)
	require.ErrorIs(t, err, ports.ErrBadRequest)
}

func TestLaunchService_OpenWindow_AutoPublishFromDraft(t *testing.T) {
	l := testLaunch() // DRAFT
	l.InitialGenesisSHA256 = "1111111111111111111111111111111111111111111111111111111111111111"
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	require.NoError(t, svc.OpenWindow(context.Background(), l.ID, testAddr1))
	got, _ := repo.FindByID(context.Background(), l.ID)
	assert.Equal(t, launch.StatusWindowOpen, got.Status)
}

// --- ListLaunches ---

func TestLaunchService_ListLaunches_DelegatesToRepo(t *testing.T) {
	l1 := testLaunch()
	l2, _ := launch.New(uuid.New(), func() launch.ChainRecord {
		r := testChainRecord()
		r.ChainID = "other-chain-1"
		return r
	}(), launch.LaunchTypeTestnet, testCommittee(1, 1))
	repo := newFakeLaunchRepo(l1, l2)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	launches, total, err := svc.ListLaunches(context.Background(), "", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Len(t, launches, 2)
}

// --- IsCommitteeMember ---

func TestLaunchService_IsCommitteeMember_True(t *testing.T) {
	l := testLaunch() // committee has testAddr1
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	ok, err := svc.IsCommitteeMember(context.Background(), l.ID, testAddr1)
	require.NoError(t, err)
	assert.True(t, ok, "testAddr1 should be a coordinator")
}

func TestLaunchService_IsCommitteeMember_False(t *testing.T) {
	l := testLaunch()
	l.Committee = testCommittee(1, 1) // only testAddr1
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	ok, err := svc.IsCommitteeMember(context.Background(), l.ID, testAddr2)
	require.NoError(t, err)
	assert.False(t, ok, "testAddr2 should not be a coordinator in a 1-member committee")
}

func TestLaunchService_IsCommitteeMember_NotFound(t *testing.T) {
	svc := newLaunchSvc(newFakeLaunchRepo(), newFakeGenesisStore())
	_, err := svc.IsCommitteeMember(context.Background(), uuid.New(), testAddr1)
	require.ErrorIs(t, err, ports.ErrNotFound)
}

// --- GetCommittee ---

func TestLaunchService_GetCommittee_Success(t *testing.T) {
	l := testLaunch()
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	c, err := svc.GetCommittee(context.Background(), l.ID, testAddr1)
	require.NoError(t, err)
	assert.Equal(t, l.Committee.ThresholdM, c.ThresholdM)
}

func TestLaunchService_GetCommittee_NotFound(t *testing.T) {
	svc := newLaunchSvc(newFakeLaunchRepo(), newFakeGenesisStore())
	_, err := svc.GetCommittee(context.Background(), uuid.New(), "")
	require.ErrorIs(t, err, ports.ErrNotFound)
}

// --- CancelLaunch ---

func newLaunchSvcWithReadiness(launchRepo *fakeLaunchRepo, readinessRepo *fakeReadinessRepo) *LaunchService {
	return NewLaunchService(launchRepo, newFakeJoinRequestRepo(), readinessRepo, newFakeGenesisStore(), newFakeAllocationStore(), &fakeEventPublisher{}, &fakeAuditLogWriter{}, newFakeRehearsalAttemptRepo(), newFakeRehearsalResultRepo())
}

func newLaunchSvcWithAudit(launchRepo *fakeLaunchRepo, genesisStore *fakeGenesisStore, audit *fakeAuditLogWriter) *LaunchService {
	return NewLaunchService(launchRepo, newFakeJoinRequestRepo(), newFakeReadinessRepo(), genesisStore, newFakeAllocationStore(), &fakeEventPublisher{}, audit, newFakeRehearsalAttemptRepo(), newFakeRehearsalResultRepo())
}

func TestLaunchService_CancelLaunch_Success(t *testing.T) {
	l := testLaunch() // DRAFT, lead = testAddr1
	lRepo := newFakeLaunchRepo(l)
	svc := newLaunchSvcWithReadiness(lRepo, newFakeReadinessRepo())

	require.NoError(t, svc.CancelLaunch(context.Background(), l.ID, testAddr1))
	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	assert.Equal(t, launch.StatusCancelled, stored.Status)
}

func TestLaunchService_CancelLaunch_NonLeadForbidden(t *testing.T) {
	l := testLaunch() // lead = testAddr1
	svc := newLaunchSvcWithReadiness(newFakeLaunchRepo(l), newFakeReadinessRepo())

	err := svc.CancelLaunch(context.Background(), l.ID, testAddr2) // not the lead
	require.ErrorIs(t, err, ports.ErrForbidden)
}

func TestLaunchService_CancelLaunch_AlreadyCancelled(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusCancelled
	svc := newLaunchSvcWithReadiness(newFakeLaunchRepo(l), newFakeReadinessRepo())

	err := svc.CancelLaunch(context.Background(), l.ID, testAddr1)
	require.ErrorIs(t, err, ports.ErrConflict, "canceling an already-canceled launch is a conflict")
}

func TestLaunchService_CancelLaunch_FromGenesisReady_InvalidatesReadiness(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusGenesisReady
	rc := &launch.ReadinessConfirmation{
		ID:              uuid.New(),
		LaunchID:        l.ID,
		OperatorAddress: mustAddr(testAddr2),
		ConfirmedAt:     time.Now().UTC(),
	}
	readinessRepo := newFakeReadinessRepo(rc)
	svc := newLaunchSvcWithReadiness(newFakeLaunchRepo(l), readinessRepo)

	require.NoError(t, svc.CancelLaunch(context.Background(), l.ID, testAddr1))
	assert.False(t, readinessRepo.data[rc.ID].IsValid(), "readiness confirmation should have been invalidated")
}

func TestLaunchService_CancelLaunch_NotGenesisReady_DoesNotInvalidate(t *testing.T) {
	l := testLaunch() // DRAFT — no readiness records should be touched
	rc := &launch.ReadinessConfirmation{
		ID:              uuid.New(),
		LaunchID:        l.ID,
		OperatorAddress: mustAddr(testAddr2),
		ConfirmedAt:     time.Now().UTC(),
	}
	readinessRepo := newFakeReadinessRepo(rc)
	svc := newLaunchSvcWithReadiness(newFakeLaunchRepo(l), readinessRepo)

	require.NoError(t, svc.CancelLaunch(context.Background(), l.ID, testAddr1))
	assert.True(t, readinessRepo.data[rc.ID].IsValid(), "readiness confirmation should not have been invalidated for non-GENESIS_READY cancel")
}

// ---- audit log tests ----

func TestLaunchService_CreateLaunch_AuditEvent(t *testing.T) {
	audit := &fakeAuditLogWriter{}
	svc := newLaunchSvcWithAudit(newFakeLaunchRepo(), newFakeGenesisStore(), audit)

	l, err := svc.CreateLaunch(context.Background(), CreateLaunchInput{
		Record:     testChainRecord(),
		LaunchType: launch.LaunchTypeTestnet,
		Committee:  testCommittee(1, 1),
	})
	require.NoError(t, err)

	require.Len(t, audit.events, 1)
	ev := audit.events[0]
	assert.Equal(t, "LaunchCreated", ev.EventName)
	assert.Equal(t, l.ID.String(), ev.LaunchID)
}

func TestLaunchService_CancelLaunch_AuditEvent(t *testing.T) {
	l := testLaunch()
	audit := &fakeAuditLogWriter{}
	svc := NewLaunchService(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeReadinessRepo(), newFakeGenesisStore(), newFakeAllocationStore(), &fakeEventPublisher{}, audit, newFakeRehearsalAttemptRepo(), newFakeRehearsalResultRepo())

	require.NoError(t, svc.CancelLaunch(context.Background(), l.ID, testAddr1))

	require.Len(t, audit.events, 1)
	ev := audit.events[0]
	assert.Equal(t, "LaunchCancelled", ev.EventName)
	assert.Equal(t, l.ID.String(), ev.LaunchID)
}

func TestLaunchService_OpenWindow_AuditEvent(t *testing.T) {
	l := testLaunch()
	require.NoError(t, l.Publish("1111111111111111111111111111111111111111111111111111111111111111"))
	audit := &fakeAuditLogWriter{}
	svc := newLaunchSvcWithAudit(newFakeLaunchRepo(l), newFakeGenesisStore(), audit)

	require.NoError(t, svc.OpenWindow(context.Background(), l.ID, testAddr1))

	require.Len(t, audit.events, 1)
	ev := audit.events[0]
	assert.Equal(t, "WindowOpened", ev.EventName)
	assert.Equal(t, l.ID.String(), ev.LaunchID)
}

func TestLaunchService_UploadInitialGenesis_AuditEvent(t *testing.T) {
	l := testLaunch()
	audit := &fakeAuditLogWriter{}
	svc := newLaunchSvcWithAudit(newFakeLaunchRepo(l), newFakeGenesisStore(), audit)

	_, err := svc.UploadInitialGenesis(context.Background(), l.ID, validGenesisJSON(l.Record.ChainID), testAddr1)
	require.NoError(t, err)

	require.Len(t, audit.events, 1)
	ev := audit.events[0]
	assert.Equal(t, "InitialGenesisUploaded", ev.EventName)
	assert.Equal(t, l.ID.String(), ev.LaunchID)
	assert.NotEmpty(t, ev.Payload, "want non-empty payload")
}

func TestLaunchService_UploadFinalGenesis_AuditEvent(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowClosed
	audit := &fakeAuditLogWriter{}
	svc := newLaunchSvcWithAudit(newFakeLaunchRepo(l), newFakeGenesisStore(), audit)

	_, err := svc.UploadFinalGenesis(context.Background(), l.ID, validFinalGenesisJSON(l.Record.ChainID), testAddr1)
	require.NoError(t, err)

	require.Len(t, audit.events, 1)
	ev := audit.events[0]
	assert.Equal(t, "FinalGenesisUploaded", ev.EventName)
	assert.Equal(t, l.ID.String(), ev.LaunchID)
}
