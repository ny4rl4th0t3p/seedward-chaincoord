package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/joinrequest"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

func newLaunchSvc(launchRepo *fakeLaunchRepo, genesisStore *fakeGenesisStore) *LaunchService {
	return NewLaunchService(launchRepo, newFakeJoinRequestRepo(), newFakeReadinessRepo(), genesisStore, &fakeEventPublisher{}, &fakeAuditLogWriter{})
}

// --- CreateLaunch ---

func TestLaunchService_CreateLaunch_Success(t *testing.T) {
	repo := newFakeLaunchRepo()
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	l, err := svc.CreateLaunch(context.Background(), CreateLaunchInput{
		Record:     testChainRecord(),
		LaunchType: launch.LaunchTypeTestnet,
		Visibility: launch.VisibilityPublic,
		Committee:  testCommittee(1, 1),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l.ID == uuid.Nil {
		t.Fatal("expected a non-nil launch ID")
	}
	if _, err := repo.FindByID(context.Background(), l.ID); err != nil {
		t.Fatalf("launch not persisted: %v", err)
	}
}

func TestLaunchService_CreateLaunch_SaveFails(t *testing.T) {
	repo := newFakeLaunchRepo()
	repo.saveErr = errors.New("db error")
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	_, err := svc.CreateLaunch(context.Background(), CreateLaunchInput{
		Record:    testChainRecord(),
		Committee: testCommittee(1, 1),
	})
	if err == nil {
		t.Fatal("expected error when save fails")
	}
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
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestLaunchService_UploadInitialGenesis_WrongStatus(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusPublished // not DRAFT
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	_, err := svc.UploadInitialGenesis(context.Background(), l.ID, validGenesisJSON(l.Record.ChainID), testAddr1)
	if err == nil {
		t.Fatal("expected error for non-DRAFT launch")
	}
}

func TestLaunchService_UploadInitialGenesis_InvalidJSON(t *testing.T) {
	l := testLaunch()
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	_, err := svc.UploadInitialGenesis(context.Background(), l.ID, []byte("not-json"), testAddr1)
	if err == nil {
		t.Fatal("expected error for invalid JSON genesis")
	}
}

func TestLaunchService_UploadInitialGenesis_ChainIDMismatch(t *testing.T) {
	l := testLaunch()
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	_, err := svc.UploadInitialGenesis(context.Background(), l.ID, validGenesisJSON("wrong-chain-id"), testAddr1)
	if err == nil {
		t.Fatal("expected error for chain_id mismatch")
	}
}

func TestLaunchService_UploadInitialGenesis_Success(t *testing.T) {
	l := testLaunch()
	repo := newFakeLaunchRepo(l)
	genesis := newFakeGenesisStore()
	svc := newLaunchSvc(repo, genesis)

	hash, err := svc.UploadInitialGenesis(context.Background(), l.ID, validGenesisJSON(l.Record.ChainID), testAddr1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty SHA256 hash")
	}
	if _, ok := genesis.initial[l.ID.String()]; !ok {
		t.Fatal("genesis not stored")
	}
	stored, _ := repo.FindByID(context.Background(), l.ID)
	if stored.InitialGenesisSHA256 != hash {
		t.Errorf("hash not persisted on launch: want %s got %s", hash, stored.InitialGenesisSHA256)
	}
}

func TestLaunchService_UploadInitialGenesis_NonCommitteeMember(t *testing.T) {
	l := test1of1Launch() // DRAFT; committee = {testAddr1}
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	// testAddr2 is a valid address but not a member of this launch's committee.
	_, err := svc.UploadInitialGenesis(context.Background(), l.ID, validGenesisJSON(l.Record.ChainID), testAddr2)
	if !errors.Is(err, ports.ErrForbidden) {
		t.Fatalf("want ErrForbidden for non-committee caller, got %v", err)
	}
}

// --- UploadFinalGenesis ---

func TestLaunchService_UploadFinalGenesis_WrongStatus(t *testing.T) {
	l := testLaunch() // DRAFT
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	_, err := svc.UploadFinalGenesis(context.Background(), l.ID, validGenesisJSON(l.Record.ChainID), testAddr1)
	if err == nil {
		t.Fatal("expected error for DRAFT launch (need WINDOW_CLOSED)")
	}
}

func TestLaunchService_UploadFinalGenesis_Success(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowClosed
	repo := newFakeLaunchRepo(l)
	genesis := newFakeGenesisStore()
	svc := newLaunchSvc(repo, genesis)

	hash, err := svc.UploadFinalGenesis(context.Background(), l.ID, validFinalGenesisJSON(l.Record.ChainID), testAddr1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty SHA256 hash")
	}
	if _, ok := genesis.final[l.ID.String()]; !ok {
		t.Fatal("final genesis not stored")
	}

	// The genesis_time from the file must be synced into the launch record.
	stored, _ := repo.FindByID(context.Background(), l.ID)
	wantTime := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if stored.Record.GenesisTime == nil {
		t.Fatal("GenesisTime not synced from genesis file: got nil")
	}
	if !stored.Record.GenesisTime.Equal(wantTime) {
		t.Errorf("GenesisTime: want %s, got %s", wantTime, stored.Record.GenesisTime)
	}
}

// --- UploadFinalGenesis structural validation (M2) ---

// newLaunchSvcWithJR creates a LaunchService with a custom join request repo.
func newLaunchSvcWithJR(launchRepo *fakeLaunchRepo, jrRepo *fakeJoinRequestRepo, genesisStore *fakeGenesisStore) *LaunchService {
	return NewLaunchService(launchRepo, jrRepo, newFakeReadinessRepo(), genesisStore, &fakeEventPublisher{}, &fakeAuditLogWriter{})
}

func TestLaunchService_UploadFinalGenesis_GenesisTimeZero(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowClosed
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	noTime := []byte(`{"chain_id":"` + l.Record.ChainID + `","genesis_time":"0001-01-01T00:00:00Z","app_state":{"genutil":{"gen_txs":[]}}}`)
	_, err := svc.UploadFinalGenesis(context.Background(), l.ID, noTime, testAddr1)
	if err == nil {
		t.Fatal("expected error for zero genesis_time")
	}
}

func TestLaunchService_UploadFinalGenesis_GenesisTimePast(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowClosed
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	past := []byte(`{"chain_id":"` + l.Record.ChainID + `","genesis_time":"2000-01-01T00:00:00Z","app_state":{"genutil":{"gen_txs":[]}}}`)
	_, err := svc.UploadFinalGenesis(context.Background(), l.ID, past, testAddr1)
	if err == nil {
		t.Fatal("expected error for past genesis_time")
	}
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
	if err == nil {
		t.Fatal("expected error: approved validator missing from gen_txs")
	}
}

func TestLaunchService_UploadFinalGenesis_UnapprovedGentx(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowClosed
	// No approved validators but genesis has one gentx.
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	withExtra := []byte(`{"chain_id":"` + l.Record.ChainID + `","genesis_time":"2030-01-01T00:00:00Z","app_state":{"genutil":{"gen_txs":[{"body":{"messages":[{"pubkey":{"key":"AAEC"}}]}}]}}}`)
	_, err := svc.UploadFinalGenesis(context.Background(), l.ID, withExtra, testAddr1)
	if err == nil {
		t.Fatal("expected error: unapproved gentx in genesis")
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
}

// --- UploadInitialGenesisRef ---

const validSHA256 = "a3f9b72c1d4e8f05a6b2c3d4e5f67890a1b2c3d4e5f6789012345678901234ab"

func TestLaunchService_UploadInitialGenesisRef_WrongStatus(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusPublished
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	err := svc.UploadInitialGenesisRef(context.Background(), l.ID, "https://example.com/genesis.json", validSHA256, testAddr1)
	if err == nil {
		t.Fatal("expected error for non-DRAFT launch")
	}
}

func TestLaunchService_UploadInitialGenesisRef_InvalidURL(t *testing.T) {
	l := testLaunch() // DRAFT
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	err := svc.UploadInitialGenesisRef(context.Background(), l.ID, "not-a-url", validSHA256, testAddr1)
	if !errors.Is(err, ports.ErrBadRequest) {
		t.Fatalf("want ErrBadRequest for invalid URL, got %v", err)
	}
}

func TestLaunchService_UploadInitialGenesisRef_InvalidSHA256(t *testing.T) {
	l := testLaunch() // DRAFT
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	err := svc.UploadInitialGenesisRef(context.Background(), l.ID, "https://example.com/genesis.json", "tooshort", testAddr1)
	if !errors.Is(err, ports.ErrBadRequest) {
		t.Fatalf("want ErrBadRequest for invalid sha256, got %v", err)
	}
}

func TestLaunchService_UploadInitialGenesisRef_Success(t *testing.T) {
	l := testLaunch() // DRAFT
	genesis := newFakeGenesisStore()
	svc := newLaunchSvc(newFakeLaunchRepo(l), genesis)

	err := svc.UploadInitialGenesisRef(context.Background(), l.ID, "https://example.com/genesis.json", validSHA256, testAddr1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ref, ok := genesis.initialRef[l.ID.String()]
	if !ok {
		t.Fatal("ref not stored")
	}
	if ref.ExternalURL != "https://example.com/genesis.json" {
		t.Errorf("ExternalURL: got %q", ref.ExternalURL)
	}
	if ref.SHA256 != validSHA256 {
		t.Errorf("SHA256: got %q", ref.SHA256)
	}
}

// --- UploadFinalGenesisRef ---

func TestLaunchService_UploadFinalGenesisRef_WrongStatus(t *testing.T) {
	l := testLaunch() // DRAFT
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())
	futureTime := time.Now().Add(48 * time.Hour).UTC()

	err := svc.UploadFinalGenesisRef(context.Background(), l.ID, "https://example.com/genesis.json", validSHA256, futureTime, testAddr1)
	if err == nil {
		t.Fatal("expected error for non-WINDOW_CLOSED launch")
	}
}

func TestLaunchService_UploadFinalGenesisRef_ZeroGenesisTime(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowClosed
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	err := svc.UploadFinalGenesisRef(context.Background(), l.ID, "https://example.com/final-genesis.json", validSHA256, time.Time{}, testAddr1)
	if !errors.Is(err, ports.ErrBadRequest) {
		t.Fatalf("want ErrBadRequest for zero genesis_time, got %v", err)
	}
}

func TestLaunchService_UploadFinalGenesisRef_PastGenesisTime(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowClosed
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	err := svc.UploadFinalGenesisRef(context.Background(), l.ID, "https://example.com/final-genesis.json", validSHA256, time.Now().Add(-1*time.Hour).UTC(), testAddr1)
	if !errors.Is(err, ports.ErrBadRequest) {
		t.Fatalf("want ErrBadRequest for past genesis_time, got %v", err)
	}
}

func TestLaunchService_UploadFinalGenesisRef_Success(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowClosed
	genesis := newFakeGenesisStore()
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, genesis)
	futureTime := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)

	err := svc.UploadFinalGenesisRef(context.Background(), l.ID, "https://example.com/final-genesis.json", validSHA256, futureTime, testAddr1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ref, ok := genesis.finalRef[l.ID.String()]
	if !ok {
		t.Fatal("ref not stored")
	}
	if ref.ExternalURL != "https://example.com/final-genesis.json" {
		t.Errorf("ExternalURL: got %q", ref.ExternalURL)
	}
	stored, _ := repo.FindByID(context.Background(), l.ID)
	if stored.Record.GenesisTime == nil {
		t.Fatal("GenesisTime not synced into launch record")
	}
	if !stored.Record.GenesisTime.Equal(futureTime) {
		t.Errorf("GenesisTime: want %s, got %s", futureTime, stored.Record.GenesisTime)
	}
}

// makeApprovedJoinRequest creates a JoinRequest with the given consensusPubKey already approved.
func makeApprovedJoinRequest(t *testing.T, launchID uuid.UUID, operatorAddr, consensusPubKey string) *joinrequest.JoinRequest {
	t.Helper()
	jr := makeJoinRequest(t, launchID, operatorAddr)
	jr.ConsensusPubKey = consensusPubKey
	proposalID := uuid.New()
	if err := jr.Approve(proposalID); err != nil {
		t.Fatalf("approve join request: %v", err)
	}
	return jr
}

// --- PatchLaunch ---

func TestLaunchService_PatchLaunch_LaunchNotFound(t *testing.T) {
	svc := newLaunchSvc(newFakeLaunchRepo(), newFakeGenesisStore())
	_, err := svc.PatchLaunch(context.Background(), uuid.New(), PatchLaunchInput{}, testAddr1)
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestLaunchService_PatchLaunch_NotCommitteeMember(t *testing.T) {
	// Use a 1-of-1 committee with only testAddr1; testAddr2 is not a member.
	l := testLaunch()
	l.Committee = testCommittee(1, 1) // only testAddr1
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	name := "new-name"
	_, err := svc.PatchLaunch(context.Background(), l.ID, PatchLaunchInput{ChainName: &name}, testAddr2)
	if !errors.Is(err, ports.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestLaunchService_PatchLaunch_DraftFieldsOnNonDraft(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen // past DRAFT
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	name := "updated-name"
	_, err := svc.PatchLaunch(context.Background(), l.ID, PatchLaunchInput{ChainName: &name}, testAddr1)
	if !errors.Is(err, ports.ErrForbidden) {
		t.Fatalf("want ErrForbidden for draft-only field on non-DRAFT launch, got %v", err)
	}
}

func TestLaunchService_PatchLaunch_MonitorRPCURLOnAnyStatus(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	// 203.0.113.x is documentation/TEST-NET-3 — public, not in any blocked range.
	url := "http://203.0.113.1:26657"
	updated, err := svc.PatchLaunch(context.Background(), l.ID, PatchLaunchInput{MonitorRPCURL: &url}, testAddr1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.MonitorRPCURL != url {
		t.Errorf("want MonitorRPCURL %q, got %q", url, updated.MonitorRPCURL)
	}
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
			if err == nil {
				t.Fatalf("expected error for private URL %q, got nil", privateURL)
			}
			if !errors.Is(err, ports.ErrBadRequest) {
				t.Errorf("expected ErrBadRequest, got: %v", err)
			}
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
	if err != nil {
		t.Fatalf("unexpected error clearing MonitorRPCURL: %v", err)
	}
	if updated.MonitorRPCURL != "" {
		t.Errorf("expected empty MonitorRPCURL, got %q", updated.MonitorRPCURL)
	}
}

func TestLaunchService_PatchLaunch_DraftSuccess(t *testing.T) {
	l := testLaunch()
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	name := "new-name"
	updated, err := svc.PatchLaunch(context.Background(), l.ID, PatchLaunchInput{ChainName: &name}, testAddr1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.Record.ChainName != name {
		t.Errorf("want ChainName %q, got %q", name, updated.Record.ChainName)
	}
}

// --- SetCommittee ---

func TestLaunchService_SetCommittee_NotDraft(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusPublished
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	err := svc.SetCommittee(context.Background(), l.ID, testCommittee(1, 1), testAddr1)
	if !errors.Is(err, ports.ErrForbidden) {
		t.Fatalf("want ErrForbidden for non-DRAFT launch, got %v", err)
	}
}

func TestLaunchService_SetCommittee_NotLead(t *testing.T) {
	l := testLaunch() // lead is testAddr1
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	err := svc.SetCommittee(context.Background(), l.ID, testCommittee(1, 1), testAddr2)
	if !errors.Is(err, ports.ErrForbidden) {
		t.Fatalf("want ErrForbidden for non-lead caller, got %v", err)
	}
}

func TestLaunchService_SetCommittee_BadThreshold(t *testing.T) {
	l := testLaunch()
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	bad := testCommittee(1, 1)
	bad.ThresholdM = 5 // > TotalN
	err := svc.SetCommittee(context.Background(), l.ID, bad, testAddr1)
	if err == nil {
		t.Fatal("expected error for threshold > total_n")
	}
}

func TestLaunchService_SetCommittee_MemberCountMismatch(t *testing.T) {
	l := testLaunch()
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	bad := testCommittee(1, 2)
	bad.Members = bad.Members[:1] // says TotalN=2 but only 1 member
	err := svc.SetCommittee(context.Background(), l.ID, bad, testAddr1)
	if err == nil {
		t.Fatal("expected error for member count mismatch")
	}
}

func TestLaunchService_SetCommittee_Success(t *testing.T) {
	l := testLaunch()
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	newComm := testCommittee(1, 1)
	if err := svc.SetCommittee(context.Background(), l.ID, newComm, testAddr1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stored, _ := repo.FindByID(context.Background(), l.ID)
	if stored.Committee.ThresholdM != 1 {
		t.Errorf("committee not updated")
	}
}

// --- GetLaunch ---

func TestLaunchService_GetLaunch_NotFound(t *testing.T) {
	svc := newLaunchSvc(newFakeLaunchRepo(), newFakeGenesisStore())
	_, err := svc.GetLaunch(context.Background(), uuid.New(), "")
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestLaunchService_GetLaunch_AllowlistHidden(t *testing.T) {
	l := testLaunch()
	l.Visibility = launch.VisibilityAllowlist
	// Allowlist is empty — no one is on it.
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	_, err := svc.GetLaunch(context.Background(), l.ID, testAddr1) // addr not on allowlist
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want ErrNotFound for invisible launch, got %v", err)
	}
}

func TestLaunchService_GetLaunch_Success(t *testing.T) {
	l := testLaunch()
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	got, err := svc.GetLaunch(context.Background(), l.ID, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != l.ID {
		t.Errorf("ID mismatch")
	}
}

// --- GetDashboard ---

func TestLaunchService_GetDashboard_WithGenesisTime(t *testing.T) {
	l := testLaunch()
	gt := time.Now().Add(24 * time.Hour)
	l.Record.GenesisTime = &gt
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	dash, err := svc.GetDashboard(context.Background(), l.ID, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dash.Countdown == nil {
		t.Fatal("expected non-nil countdown when genesis time is set")
	}
}

func TestLaunchService_GetDashboard_NoGenesisTime(t *testing.T) {
	l := testLaunch()
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	dash, err := svc.GetDashboard(context.Background(), l.ID, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dash.Countdown != nil {
		t.Fatal("expected nil countdown when no genesis time set")
	}
}

// --- OpenWindow ---

func TestLaunchService_OpenWindow_Success(t *testing.T) {
	l := testLaunch()
	if err := l.Publish("abc123"); err != nil {
		t.Fatal(err)
	}
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	if err := svc.OpenWindow(context.Background(), l.ID, testAddr1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := repo.FindByID(context.Background(), l.ID)
	if got.Status != launch.StatusWindowOpen {
		t.Errorf("want WINDOW_OPEN, got %s", got.Status)
	}
}

func TestLaunchService_OpenWindow_NotFound(t *testing.T) {
	svc := newLaunchSvc(newFakeLaunchRepo(), newFakeGenesisStore())
	err := svc.OpenWindow(context.Background(), uuid.New(), testAddr1)
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestLaunchService_OpenWindow_DraftWithoutGenesis_BadRequest(t *testing.T) {
	l := testLaunch() // DRAFT, no genesis uploaded
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())
	err := svc.OpenWindow(context.Background(), l.ID, testAddr1)
	if !errors.Is(err, ports.ErrBadRequest) {
		t.Fatalf("want ErrBadRequest, got %v", err)
	}
}

func TestLaunchService_OpenWindow_WrongStatus(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowOpen // already open — invalid transition
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())
	err := svc.OpenWindow(context.Background(), l.ID, testAddr1)
	if !errors.Is(err, ports.ErrBadRequest) {
		t.Fatalf("want ErrBadRequest, got %v", err)
	}
}

func TestLaunchService_OpenWindow_AutoPublishFromDraft(t *testing.T) {
	l := testLaunch() // DRAFT
	l.InitialGenesisSHA256 = "abc123"
	repo := newFakeLaunchRepo(l)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	if err := svc.OpenWindow(context.Background(), l.ID, testAddr1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := repo.FindByID(context.Background(), l.ID)
	if got.Status != launch.StatusWindowOpen {
		t.Errorf("want WINDOW_OPEN, got %s", got.Status)
	}
}

// --- ListLaunches ---

func TestLaunchService_ListLaunches_DelegatesToRepo(t *testing.T) {
	l1 := testLaunch()
	l2, _ := launch.New(uuid.New(), func() launch.ChainRecord {
		r := testChainRecord()
		r.ChainID = "other-chain-1"
		return r
	}(), launch.LaunchTypeTestnet, launch.VisibilityPublic, testCommittee(1, 1))
	repo := newFakeLaunchRepo(l1, l2)
	svc := newLaunchSvc(repo, newFakeGenesisStore())

	launches, total, err := svc.ListLaunches(context.Background(), "", 1, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 2 || len(launches) != 2 {
		t.Errorf("expected 2 launches, got total=%d len=%d", total, len(launches))
	}
}

// --- IsCoordinator ---

func TestLaunchService_IsCoordinator_True(t *testing.T) {
	l := testLaunch() // committee has testAddr1
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	ok, err := svc.IsCoordinator(context.Background(), l.ID, testAddr1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("testAddr1 should be a coordinator")
	}
}

func TestLaunchService_IsCoordinator_False(t *testing.T) {
	l := testLaunch()
	l.Committee = testCommittee(1, 1) // only testAddr1
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	ok, err := svc.IsCoordinator(context.Background(), l.ID, testAddr2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("testAddr2 should not be a coordinator in a 1-member committee")
	}
}

func TestLaunchService_IsCoordinator_NotFound(t *testing.T) {
	svc := newLaunchSvc(newFakeLaunchRepo(), newFakeGenesisStore())
	_, err := svc.IsCoordinator(context.Background(), uuid.New(), testAddr1)
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// --- GetCommittee ---

func TestLaunchService_GetCommittee_Success(t *testing.T) {
	l := testLaunch()
	svc := newLaunchSvc(newFakeLaunchRepo(l), newFakeGenesisStore())

	c, err := svc.GetCommittee(context.Background(), l.ID, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.ThresholdM != l.Committee.ThresholdM {
		t.Errorf("ThresholdM mismatch")
	}
}

func TestLaunchService_GetCommittee_NotFound(t *testing.T) {
	svc := newLaunchSvc(newFakeLaunchRepo(), newFakeGenesisStore())
	_, err := svc.GetCommittee(context.Background(), uuid.New(), "")
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// --- CancelLaunch ---

func newLaunchSvcWithReadiness(launchRepo *fakeLaunchRepo, readinessRepo *fakeReadinessRepo) *LaunchService {
	return NewLaunchService(launchRepo, newFakeJoinRequestRepo(), readinessRepo, newFakeGenesisStore(), &fakeEventPublisher{}, &fakeAuditLogWriter{})
}

func newLaunchSvcWithAudit(launchRepo *fakeLaunchRepo, genesisStore *fakeGenesisStore, audit *fakeAuditLogWriter) *LaunchService {
	return NewLaunchService(launchRepo, newFakeJoinRequestRepo(), newFakeReadinessRepo(), genesisStore, &fakeEventPublisher{}, audit)
}

func TestLaunchService_CancelLaunch_Success(t *testing.T) {
	l := testLaunch() // DRAFT, lead = testAddr1
	lRepo := newFakeLaunchRepo(l)
	svc := newLaunchSvcWithReadiness(lRepo, newFakeReadinessRepo())

	if err := svc.CancelLaunch(context.Background(), l.ID, testAddr1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stored, _ := lRepo.FindByID(context.Background(), l.ID)
	if stored.Status != launch.StatusCancelled {
		t.Errorf("want CANCELED, got %s", stored.Status)
	}
}

func TestLaunchService_CancelLaunch_NonLeadForbidden(t *testing.T) {
	l := testLaunch() // lead = testAddr1
	svc := newLaunchSvcWithReadiness(newFakeLaunchRepo(l), newFakeReadinessRepo())

	err := svc.CancelLaunch(context.Background(), l.ID, testAddr2) // not the lead
	if !errors.Is(err, ports.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestLaunchService_CancelLaunch_AlreadyCancelled(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusCancelled
	svc := newLaunchSvcWithReadiness(newFakeLaunchRepo(l), newFakeReadinessRepo())

	if err := svc.CancelLaunch(context.Background(), l.ID, testAddr1); err == nil {
		t.Fatal("expected error for already-canceled launch")
	}
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

	if err := svc.CancelLaunch(context.Background(), l.ID, testAddr1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if readinessRepo.data[rc.ID].IsValid() {
		t.Error("readiness confirmation should have been invalidated")
	}
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

	if err := svc.CancelLaunch(context.Background(), l.ID, testAddr1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !readinessRepo.data[rc.ID].IsValid() {
		t.Error("readiness confirmation should not have been invalidated for non-GENESIS_READY cancel")
	}
}

// ---- audit log tests ----

func TestLaunchService_CreateLaunch_AuditEvent(t *testing.T) {
	audit := &fakeAuditLogWriter{}
	svc := newLaunchSvcWithAudit(newFakeLaunchRepo(), newFakeGenesisStore(), audit)

	l, err := svc.CreateLaunch(context.Background(), CreateLaunchInput{
		Record:     testChainRecord(),
		LaunchType: launch.LaunchTypeTestnet,
		Visibility: launch.VisibilityPublic,
		Committee:  testCommittee(1, 1),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(audit.events) != 1 {
		t.Fatalf("want 1 audit event, got %d", len(audit.events))
	}
	ev := audit.events[0]
	if ev.EventName != "LaunchCreated" {
		t.Errorf("want event LaunchCreated, got %q", ev.EventName)
	}
	if ev.LaunchID != l.ID.String() {
		t.Errorf("want launch ID %s, got %s", l.ID, ev.LaunchID)
	}
}

func TestLaunchService_CancelLaunch_AuditEvent(t *testing.T) {
	l := testLaunch()
	audit := &fakeAuditLogWriter{}
	svc := NewLaunchService(newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeReadinessRepo(), newFakeGenesisStore(), &fakeEventPublisher{}, audit)

	if err := svc.CancelLaunch(context.Background(), l.ID, testAddr1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(audit.events) != 1 {
		t.Fatalf("want 1 audit event, got %d", len(audit.events))
	}
	ev := audit.events[0]
	if ev.EventName != "LaunchCancelled" {
		t.Errorf("want event LaunchCancelled, got %q", ev.EventName)
	}
	if ev.LaunchID != l.ID.String() {
		t.Errorf("want launch ID %s, got %s", l.ID, ev.LaunchID)
	}
}

func TestLaunchService_OpenWindow_AuditEvent(t *testing.T) {
	l := testLaunch()
	if err := l.Publish("abc123"); err != nil {
		t.Fatal(err)
	}
	audit := &fakeAuditLogWriter{}
	svc := newLaunchSvcWithAudit(newFakeLaunchRepo(l), newFakeGenesisStore(), audit)

	if err := svc.OpenWindow(context.Background(), l.ID, testAddr1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(audit.events) != 1 {
		t.Fatalf("want 1 audit event, got %d", len(audit.events))
	}
	ev := audit.events[0]
	if ev.EventName != "WindowOpened" {
		t.Errorf("want event WindowOpened, got %q", ev.EventName)
	}
	if ev.LaunchID != l.ID.String() {
		t.Errorf("want launch ID %s, got %s", l.ID, ev.LaunchID)
	}
}

func TestLaunchService_UploadInitialGenesis_AuditEvent(t *testing.T) {
	l := testLaunch()
	audit := &fakeAuditLogWriter{}
	svc := newLaunchSvcWithAudit(newFakeLaunchRepo(l), newFakeGenesisStore(), audit)

	hash, err := svc.UploadInitialGenesis(context.Background(), l.ID, validGenesisJSON(l.Record.ChainID), testAddr1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(audit.events) != 1 {
		t.Fatalf("want 1 audit event, got %d", len(audit.events))
	}
	ev := audit.events[0]
	if ev.EventName != "InitialGenesisUploaded" {
		t.Errorf("want event InitialGenesisUploaded, got %q", ev.EventName)
	}
	if ev.LaunchID != l.ID.String() {
		t.Errorf("want launch ID %s, got %s", l.ID, ev.LaunchID)
	}
	if string(ev.Payload) == "" {
		t.Error("want non-empty payload")
	}
	_ = hash
}

func TestLaunchService_UploadFinalGenesis_AuditEvent(t *testing.T) {
	l := testLaunch()
	l.Status = launch.StatusWindowClosed
	audit := &fakeAuditLogWriter{}
	svc := newLaunchSvcWithAudit(newFakeLaunchRepo(l), newFakeGenesisStore(), audit)

	_, err := svc.UploadFinalGenesis(context.Background(), l.ID, validFinalGenesisJSON(l.Record.ChainID), testAddr1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(audit.events) != 1 {
		t.Fatalf("want 1 audit event, got %d", len(audit.events))
	}
	ev := audit.events[0]
	if ev.EventName != "FinalGenesisUploaded" {
		t.Errorf("want event FinalGenesisUploaded, got %q", ev.EventName)
	}
	if ev.LaunchID != l.ID.String() {
		t.Errorf("want launch ID %s, got %s", l.ID, ev.LaunchID)
	}
}
