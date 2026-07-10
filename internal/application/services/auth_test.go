package services

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

func newAuthSvc(challenges *fakeChallengeStore, sessions *fakeSessionStore, nonces *fakeNonceStore, verifier *fakeVerifier) *AuthService {
	return NewAuthService(challenges, sessions, nonces, verifier)
}

// acctKey is the HRP-independent account key the auth service keys challenge and
// nonce state on for testAddr1 — fixtures must seed under this, not the bech32 address.
func acctKey() string {
	k, _ := accountKey(testAddr1)
	return k
}

func TestAuthService_IssueChallenge_EmptyAddr(t *testing.T) {
	svc := newAuthSvc(newFakeChallengeStore(), newFakeSessionStore(), newFakeNonceStore(), &fakeVerifier{})
	_, err := svc.IssueChallenge(context.Background(), "")
	require.ErrorIs(t, err, ports.ErrBadRequest, "empty operator address is a 400")
}

func TestAuthService_IssueChallenge_Success(t *testing.T) {
	svc := newAuthSvc(newFakeChallengeStore(), newFakeSessionStore(), newFakeNonceStore(), &fakeVerifier{})
	ch, err := svc.IssueChallenge(context.Background(), testAddr1)
	require.NoError(t, err)
	assert.NotEmpty(t, ch, "expected non-empty challenge")
}

func TestAuthService_IssueChallenge_RejectsValoper(t *testing.T) {
	svc := newAuthSvc(newFakeChallengeStore(), newFakeSessionStore(), newFakeNonceStore(), &fakeVerifier{})
	valoper, err := launch.MustNewAccountID(testAddr1).Bech32("cosmosvaloper")
	require.NoError(t, err)
	_, err = svc.IssueChallenge(context.Background(), valoper)
	require.ErrorIs(t, err, ports.ErrBadRequest, "a valoper address cannot authenticate")
}

func TestAuthService_VerifyChallenge_CrossHRP(t *testing.T) {
	// A challenge issued while presenting one prefix is verifiable when the same
	// account presents a different prefix — auth state is keyed on the account.
	challenges := newFakeChallengeStore()
	svc := newAuthSvc(challenges, newFakeSessionStore(), newFakeNonceStore(), &fakeVerifier{})

	ch, err := svc.IssueChallenge(context.Background(), testAddr1)
	require.NoError(t, err)

	otherHRP, err := launch.MustNewAccountID(testAddr1).Bech32("network")
	require.NoError(t, err)

	token, err := svc.VerifyChallenge(context.Background(), VerifyChallengeInput{
		OperatorAddress: otherHRP,
		Nonce:           "nonce-xhrp",
		Timestamp:       nowTS(),
		Challenge:       ch,
		Signature:       testSig,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, token, "same account under a different prefix authenticates")
}

func TestAuthService_VerifyChallenge_EmptyAddr(t *testing.T) {
	svc := newAuthSvc(newFakeChallengeStore(), newFakeSessionStore(), newFakeNonceStore(), &fakeVerifier{})
	_, err := svc.VerifyChallenge(context.Background(), VerifyChallengeInput{})
	require.ErrorIs(t, err, ports.ErrBadRequest, "empty operator address is a 400")
}

func TestAuthService_VerifyChallenge_NonceConflict(t *testing.T) {
	nonces := newFakeNonceStore()
	nonces.consumeErr = ports.ErrConflict
	svc := newAuthSvc(newFakeChallengeStore(), newFakeSessionStore(), nonces, &fakeVerifier{})

	_, err := svc.VerifyChallenge(context.Background(), VerifyChallengeInput{
		OperatorAddress: testAddr1,
		Nonce:           "nonce-1",
		Timestamp:       nowTS(),
		Signature:       testSig,
	})
	require.ErrorIs(t, err, ports.ErrConflict, "a rejected nonce must surface as a conflict")
}

func TestAuthService_VerifyChallenge_BadTimestamp(t *testing.T) {
	svc := newAuthSvc(newFakeChallengeStore(), newFakeSessionStore(), newFakeNonceStore(), &fakeVerifier{})
	_, err := svc.VerifyChallenge(context.Background(), VerifyChallengeInput{
		OperatorAddress: testAddr1,
		Nonce:           "nonce-1",
		Timestamp:       expiredTS(),
		Signature:       testSig,
	})
	require.ErrorIs(t, err, ports.ErrUnauthorized, "expired timestamp is an auth failure")
}

func TestAuthService_VerifyChallenge_ChallengeNotFound(t *testing.T) {
	// No challenge stored → Consume returns ErrNotFound, surfaced as-is.
	svc := newAuthSvc(newFakeChallengeStore(), newFakeSessionStore(), newFakeNonceStore(), &fakeVerifier{})
	_, err := svc.VerifyChallenge(context.Background(), VerifyChallengeInput{
		OperatorAddress: testAddr1,
		Nonce:           "nonce-1",
		Timestamp:       nowTS(),
		Challenge:       "anything",
		Signature:       testSig,
	})
	require.ErrorIs(t, err, ports.ErrNotFound, "a missing challenge surfaces the store's not-found")
}

func TestAuthService_VerifyChallenge_ChallengeMismatch(t *testing.T) {
	challenges := newFakeChallengeStore()
	// Pre-store a challenge that differs from what the input claims.
	challenges.data[acctKey()] = "stored-challenge"
	svc := newAuthSvc(challenges, newFakeSessionStore(), newFakeNonceStore(), &fakeVerifier{})

	_, err := svc.VerifyChallenge(context.Background(), VerifyChallengeInput{
		OperatorAddress: testAddr1,
		Nonce:           "nonce-1",
		Timestamp:       nowTS(),
		Challenge:       "wrong-challenge",
		Signature:       testSig,
	})
	require.ErrorIs(t, err, ports.ErrChallengeMismatch)
	assert.ErrorIs(t, err, ports.ErrUnauthorized, "should map to 401")
}

func TestAuthService_VerifyChallenge_BadSigEncoding(t *testing.T) {
	challenges := newFakeChallengeStore()
	challenges.data[acctKey()] = "stored-challenge"
	svc := newAuthSvc(challenges, newFakeSessionStore(), newFakeNonceStore(), &fakeVerifier{})

	_, err := svc.VerifyChallenge(context.Background(), VerifyChallengeInput{
		OperatorAddress: testAddr1,
		Nonce:           "nonce-1",
		Timestamp:       nowTS(),
		Challenge:       "stored-challenge",
		Signature:       "not-valid-base64!!!", // decodeBase64Sig will fail
	})
	require.ErrorIs(t, err, ports.ErrBadRequest, "malformed signature is a 400")
}

func TestAuthService_VerifyChallenge_SigFails(t *testing.T) {
	challenges := newFakeChallengeStore()
	challenges.data[acctKey()] = "stored-challenge"
	verifier := &fakeVerifier{err: ports.ErrUnauthorized}
	svc := newAuthSvc(challenges, newFakeSessionStore(), newFakeNonceStore(), verifier)

	_, err := svc.VerifyChallenge(context.Background(), VerifyChallengeInput{
		OperatorAddress: testAddr1,
		Nonce:           "nonce-1",
		Timestamp:       nowTS(),
		Challenge:       "stored-challenge",
		Signature:       testSig,
	})
	require.ErrorIs(t, err, ports.ErrUnauthorized, "invalid signature must map to 401")
}

func TestAuthService_VerifyChallenge_Success(t *testing.T) {
	challenges := newFakeChallengeStore()
	challenges.data[acctKey()] = "stored-challenge"
	sessions := newFakeSessionStore()
	svc := newAuthSvc(challenges, sessions, newFakeNonceStore(), &fakeVerifier{})

	token, err := svc.VerifyChallenge(context.Background(), VerifyChallengeInput{
		OperatorAddress: testAddr1,
		Nonce:           "nonce-1",
		Timestamp:       nowTS(),
		Challenge:       "stored-challenge",
		Signature:       testSig,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, token, "expected non-empty session token")

	// Challenge should be consumed — a second call finds no challenge.
	_, err = svc.VerifyChallenge(context.Background(), VerifyChallengeInput{
		OperatorAddress: testAddr1,
		Nonce:           "nonce-2",
		Timestamp:       nowTS(),
		Challenge:       "stored-challenge",
		Signature:       testSig,
	})
	require.ErrorIs(t, err, ports.ErrNotFound, "challenge already consumed → not found")
}

func TestAuthService_RevokeSession(t *testing.T) {
	sessions := newFakeSessionStore()
	sessions.data["existing-token"] = testAddr1
	svc := newAuthSvc(newFakeChallengeStore(), sessions, newFakeNonceStore(), &fakeVerifier{})

	require.NoError(t, svc.RevokeSession(context.Background(), "existing-token"))
	_, ok := sessions.data["existing-token"]
	assert.False(t, ok, "expected token to be removed after revoke")
}

func TestAuthService_GetSessionInfo(t *testing.T) {
	sessions := newFakeSessionStore()
	sessions.data["tok"] = testAddr1
	svc := newAuthSvc(newFakeChallengeStore(), sessions, newFakeNonceStore(), &fakeVerifier{})

	info, err := svc.GetSessionInfo("tok")
	require.NoError(t, err)
	assert.Equal(t, testAddr1, info.OperatorAddress)
	assert.False(t, info.ExpiresAt.IsZero(), "expiry should be populated")

	_, err = svc.GetSessionInfo("no-such-token")
	require.Error(t, err)
}

func TestAuthService_ValidateSession_Valid(t *testing.T) {
	sessions := newFakeSessionStore()
	sessions.data["tok"] = testAddr1
	svc := newAuthSvc(newFakeChallengeStore(), sessions, newFakeNonceStore(), &fakeVerifier{})

	addr, err := svc.ValidateSession(context.Background(), "tok")
	require.NoError(t, err)
	assert.Equal(t, testAddr1, addr)
}

func TestAuthService_ValidateSession_Invalid(t *testing.T) {
	svc := newAuthSvc(newFakeChallengeStore(), newFakeSessionStore(), newFakeNonceStore(), &fakeVerifier{})
	_, err := svc.ValidateSession(context.Background(), "no-such-token")
	require.Error(t, err, "expected error for unknown token")
}
