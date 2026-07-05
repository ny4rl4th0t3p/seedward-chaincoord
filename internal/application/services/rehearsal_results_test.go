package services

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-libs/canonicaljson"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// resultSvc builds a LaunchService for result-recording tests and returns it with the attempt and
// result fakes so tests can inspect stored state. The launch trusts the returned public key.
func resultSvc(t *testing.T, l *launch.Launch) (*LaunchService, *fakeRehearsalResultRepo, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	l.RehearsalServicePubKey = base64.StdEncoding.EncodeToString(pub)

	results := newFakeRehearsalResultRepo()
	svc := NewLaunchService(
		newFakeLaunchRepo(l), newFakeJoinRequestRepo(), newFakeReadinessRepo(),
		newFakeGenesisStore(), newFakeAllocationStore(), &fakeEventPublisher{}, &fakeAuditLogWriter{},
		newFakeRehearsalAttemptRepo(), results,
	)
	return svc, results, priv
}

// signedFact builds a result fact for (launchID, hash, attemptID) with the given outcome and signs
// it exactly as the daemon does: canonicaljson strips "signature", Ed25519 over the rest.
func signedFact(t *testing.T, launchID, hash, attemptID string, outcome launch.RehearsalOutcome, priv ed25519.PrivateKey) RehearsalResultFact {
	t.Helper()
	att := attemptID
	fact := RehearsalResultFact{
		SchemaVersion: 1,
		LaunchID:      launchID,
		InputSetHash:  hash,
		AttemptID:     &att,
		Outcome:       string(outcome),
		Summary:       "ok",
		Steps:         []RehearsalResultFactStep{{Name: "boot", Status: "PASS"}},
		Rehearsal:     RehearsalResultFactMeta{EngineVersion: "eng1", BinaryName: "gaiad", Validators: 2},
		StartedAt:     "2026-01-01T00:00:00Z",
		FinishedAt:    "2026-01-01T00:00:05Z",
	}
	fact.ServicePubkey = base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))
	msg, err := canonicaljson.MarshalForSigning(&fact)
	require.NoError(t, err)
	fact.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, msg))
	return fact
}

func TestRecordRehearsalResult_HappyPathNotStale(t *testing.T) {
	l := testLaunch()
	svc, results, priv := resultSvc(t, l)

	in, err := svc.ClaimRehearsalRun(context.Background(), l.ID, "runner-1")
	require.NoError(t, err)

	fact := signedFact(t, l.ID.String(), in.InputSetHash, in.AttemptID.String(), launch.OutcomePass, priv)
	res, err := svc.RecordRehearsalResult(context.Background(), l.ID, fact)
	require.NoError(t, err)

	assert.Equal(t, launch.OutcomePass, res.Outcome)
	assert.False(t, res.Stale, "matches current input set")
	assert.Equal(t, in.AttemptID, res.AttemptID)
	require.Len(t, results.byLaunch[l.ID], 1)
}

func TestRecordRehearsalResult_Stale(t *testing.T) {
	l := testLaunch()
	svc, _, priv := resultSvc(t, l)

	in, err := svc.ClaimRehearsalRun(context.Background(), l.ID, "runner-1")
	require.NoError(t, err)
	fact := signedFact(t, l.ID.String(), in.InputSetHash, in.AttemptID.String(), launch.OutcomePass, priv)

	// The approved input set drifts after the attempt was served (an allocation is approved).
	propID := uuid.New()
	l.AllocationFiles = append(l.AllocationFiles, launch.AllocationFile{
		Type: launch.AllocationAccounts, SHA256: "accountshash",
		Status: launch.AllocationApproved, ApprovedByProposal: &propID,
	})

	res, err := svc.RecordRehearsalResult(context.Background(), l.ID, fact)
	require.NoError(t, err)
	assert.True(t, res.Stale, "attempt's input set is no longer current")
}

func TestRecordRehearsalResult_Skipped(t *testing.T) {
	l := testLaunch()
	svc, _, priv := resultSvc(t, l)
	in, err := svc.ClaimRehearsalRun(context.Background(), l.ID, "runner-1")
	require.NoError(t, err)

	fact := signedFact(t, l.ID.String(), in.InputSetHash, in.AttemptID.String(), launch.OutcomeSkipped, priv)
	res, err := svc.RecordRehearsalResult(context.Background(), l.ID, fact)
	require.NoError(t, err)
	assert.Equal(t, launch.OutcomeSkipped, res.Outcome)
}

func TestRecordRehearsalResult_Idempotent(t *testing.T) {
	l := testLaunch()
	svc, results, priv := resultSvc(t, l)
	in, err := svc.ClaimRehearsalRun(context.Background(), l.ID, "runner-1")
	require.NoError(t, err)
	fact := signedFact(t, l.ID.String(), in.InputSetHash, in.AttemptID.String(), launch.OutcomePass, priv)

	first, err := svc.RecordRehearsalResult(context.Background(), l.ID, fact)
	require.NoError(t, err)
	second, err := svc.RecordRehearsalResult(context.Background(), l.ID, fact)
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID, "re-POST returns the same stored result")
	require.Len(t, results.byLaunch[l.ID], 1, "no duplicate row")
}

func TestRecordRehearsalResult_FabricatedAttempt(t *testing.T) {
	l := testLaunch()
	svc, _, priv := resultSvc(t, l)
	in, err := svc.ClaimRehearsalRun(context.Background(), l.ID, "runner-1")
	require.NoError(t, err)

	// A random attempt_id coordd never minted → rejected.
	fact := signedFact(t, l.ID.String(), in.InputSetHash, uuid.New().String(), launch.OutcomePass, priv)
	_, err = svc.RecordRehearsalResult(context.Background(), l.ID, fact)
	require.ErrorIs(t, err, ports.ErrBadRequest)
}

func TestRecordRehearsalResult_HashMismatch(t *testing.T) {
	l := testLaunch()
	svc, _, priv := resultSvc(t, l)
	in, err := svc.ClaimRehearsalRun(context.Background(), l.ID, "runner-1")
	require.NoError(t, err)

	// Correct attempt, but a hash that does not match it → rejected (signed, but inconsistent).
	fact := signedFact(t, l.ID.String(), "deadbeef", in.AttemptID.String(), launch.OutcomePass, priv)
	_, err = svc.RecordRehearsalResult(context.Background(), l.ID, fact)
	require.ErrorIs(t, err, ports.ErrBadRequest)
}

func TestRecordRehearsalResult_BadSignature(t *testing.T) {
	l := testLaunch()
	svc, _, _ := resultSvc(t, l)
	in, err := svc.ClaimRehearsalRun(context.Background(), l.ID, "runner-1")
	require.NoError(t, err)

	// Sign with a DIFFERENT key than the launch trusts.
	_, wrongPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	fact := signedFact(t, l.ID.String(), in.InputSetHash, in.AttemptID.String(), launch.OutcomePass, wrongPriv)

	_, err = svc.RecordRehearsalResult(context.Background(), l.ID, fact)
	require.ErrorIs(t, err, ports.ErrUnauthorized)
}

func TestRecordRehearsalResult_NoTrustedKey(t *testing.T) {
	l := testLaunch()
	svc, _, priv := resultSvc(t, l)
	in, err := svc.ClaimRehearsalRun(context.Background(), l.ID, "runner-1")
	require.NoError(t, err)
	fact := signedFact(t, l.ID.String(), in.InputSetHash, in.AttemptID.String(), launch.OutcomePass, priv)

	l.RehearsalServicePubKey = "" // launch not configured to accept results
	_, err = svc.RecordRehearsalResult(context.Background(), l.ID, fact)
	require.ErrorIs(t, err, ports.ErrConflict)
}

func TestListRehearsalResults_CommitteeOnly(t *testing.T) {
	l := testLaunch()
	l.Committee = testCommittee(1, 1) // sole member: testAddr1
	svc, _, priv := resultSvc(t, l)

	// Record one result through the flow.
	in, err := svc.ClaimRehearsalRun(context.Background(), l.ID, "runner-1")
	require.NoError(t, err)
	fact := signedFact(t, l.ID.String(), in.InputSetHash, in.AttemptID.String(), launch.OutcomePass, priv)
	_, err = svc.RecordRehearsalResult(context.Background(), l.ID, fact)
	require.NoError(t, err)

	// Committee member sees it.
	got, err := svc.ListRehearsalResults(context.Background(), l.ID, testAddr1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, launch.OutcomePass, got[0].Outcome)

	// Non-member is forbidden.
	_, err = svc.ListRehearsalResults(context.Background(), l.ID, testAddr2)
	require.ErrorIs(t, err, ports.ErrForbidden)

	// Unknown launch is 404.
	_, err = svc.ListRehearsalResults(context.Background(), uuid.New(), testAddr1)
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestRecordRehearsalResult_LaunchNotFound(t *testing.T) {
	l := testLaunch()
	svc, _, priv := resultSvc(t, l)
	fact := signedFact(t, uuid.New().String(), "h", uuid.New().String(), launch.OutcomePass, priv)
	_, err := svc.RecordRehearsalResult(context.Background(), uuid.New(), fact)
	require.ErrorIs(t, err, ports.ErrNotFound)
}
