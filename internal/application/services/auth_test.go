package services

import (
	"context"
	"testing"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/ports"
)

func newAuthSvc(challenges *fakeChallengeStore, sessions *fakeSessionStore, nonces *fakeNonceStore, verifier *fakeVerifier) *AuthService {
	return NewAuthService(challenges, sessions, nonces, verifier)
}

func TestAuthService_IssueChallenge_EmptyAddr(t *testing.T) {
	svc := newAuthSvc(newFakeChallengeStore(), newFakeSessionStore(), newFakeNonceStore(), &fakeVerifier{})
	_, err := svc.IssueChallenge(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty operator address")
	}
}

func TestAuthService_IssueChallenge_Success(t *testing.T) {
	svc := newAuthSvc(newFakeChallengeStore(), newFakeSessionStore(), newFakeNonceStore(), &fakeVerifier{})
	ch, err := svc.IssueChallenge(context.Background(), testAddr1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch == "" {
		t.Fatal("expected non-empty challenge")
	}
}

func TestAuthService_VerifyChallenge_EmptyAddr(t *testing.T) {
	svc := newAuthSvc(newFakeChallengeStore(), newFakeSessionStore(), newFakeNonceStore(), &fakeVerifier{})
	_, err := svc.VerifyChallenge(context.Background(), VerifyChallengeInput{})
	if err == nil {
		t.Fatal("expected error for empty operator address")
	}
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
	if err == nil {
		t.Fatal("expected error for conflicting nonce")
	}
}

func TestAuthService_VerifyChallenge_BadTimestamp(t *testing.T) {
	svc := newAuthSvc(newFakeChallengeStore(), newFakeSessionStore(), newFakeNonceStore(), &fakeVerifier{})
	_, err := svc.VerifyChallenge(context.Background(), VerifyChallengeInput{
		OperatorAddress: testAddr1,
		Nonce:           "nonce-1",
		Timestamp:       expiredTS(),
		Signature:       testSig,
	})
	if err == nil {
		t.Fatal("expected error for expired timestamp")
	}
}

func TestAuthService_VerifyChallenge_ChallengeNotFound(t *testing.T) {
	// No challenge stored → Consume returns ErrNotFound.
	svc := newAuthSvc(newFakeChallengeStore(), newFakeSessionStore(), newFakeNonceStore(), &fakeVerifier{})
	_, err := svc.VerifyChallenge(context.Background(), VerifyChallengeInput{
		OperatorAddress: testAddr1,
		Nonce:           "nonce-1",
		Timestamp:       nowTS(),
		Challenge:       "anything",
		Signature:       testSig,
	})
	if err == nil {
		t.Fatal("expected error when no challenge exists")
	}
}

func TestAuthService_VerifyChallenge_ChallengeMismatch(t *testing.T) {
	challenges := newFakeChallengeStore()
	// Pre-store a challenge that differs from what the input claims.
	challenges.data[testAddr1] = "stored-challenge"
	svc := newAuthSvc(challenges, newFakeSessionStore(), newFakeNonceStore(), &fakeVerifier{})

	_, err := svc.VerifyChallenge(context.Background(), VerifyChallengeInput{
		OperatorAddress: testAddr1,
		Nonce:           "nonce-1",
		Timestamp:       nowTS(),
		Challenge:       "wrong-challenge",
		Signature:       testSig,
	})
	if err == nil {
		t.Fatal("expected error for challenge mismatch")
	}
}

func TestAuthService_VerifyChallenge_BadSigEncoding(t *testing.T) {
	challenges := newFakeChallengeStore()
	challenges.data[testAddr1] = "stored-challenge"
	svc := newAuthSvc(challenges, newFakeSessionStore(), newFakeNonceStore(), &fakeVerifier{})

	_, err := svc.VerifyChallenge(context.Background(), VerifyChallengeInput{
		OperatorAddress: testAddr1,
		Nonce:           "nonce-1",
		Timestamp:       nowTS(),
		Challenge:       "stored-challenge",
		Signature:       "not-valid-base64!!!", // decodeBase64Sig will fail
	})
	if err == nil {
		t.Fatal("expected error for invalid base64 signature")
	}
}

func TestAuthService_VerifyChallenge_SigFails(t *testing.T) {
	challenges := newFakeChallengeStore()
	challenges.data[testAddr1] = "stored-challenge"
	verifier := &fakeVerifier{err: ports.ErrUnauthorized}
	svc := newAuthSvc(challenges, newFakeSessionStore(), newFakeNonceStore(), verifier)

	_, err := svc.VerifyChallenge(context.Background(), VerifyChallengeInput{
		OperatorAddress: testAddr1,
		Nonce:           "nonce-1",
		Timestamp:       nowTS(),
		Challenge:       "stored-challenge",
		Signature:       testSig,
	})
	if err == nil {
		t.Fatal("expected error when signature verification fails")
	}
}

func TestAuthService_VerifyChallenge_Success(t *testing.T) {
	challenges := newFakeChallengeStore()
	challenges.data[testAddr1] = "stored-challenge"
	sessions := newFakeSessionStore()
	svc := newAuthSvc(challenges, sessions, newFakeNonceStore(), &fakeVerifier{})

	token, err := svc.VerifyChallenge(context.Background(), VerifyChallengeInput{
		OperatorAddress: testAddr1,
		Nonce:           "nonce-1",
		Timestamp:       nowTS(),
		Challenge:       "stored-challenge",
		Signature:       testSig,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty session token")
	}
	// Challenge should be consumed — a second call should fail.
	_, err = svc.VerifyChallenge(context.Background(), VerifyChallengeInput{
		OperatorAddress: testAddr1,
		Nonce:           "nonce-2",
		Timestamp:       nowTS(),
		Challenge:       "stored-challenge",
		Signature:       testSig,
	})
	if err == nil {
		t.Fatal("expected error: challenge already consumed")
	}
}

func TestAuthService_RevokeSession(t *testing.T) {
	sessions := newFakeSessionStore()
	sessions.data["existing-token"] = testAddr1
	svc := newAuthSvc(newFakeChallengeStore(), sessions, newFakeNonceStore(), &fakeVerifier{})

	if err := svc.RevokeSession(context.Background(), "existing-token"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Token should now be gone.
	if _, ok := sessions.data["existing-token"]; ok {
		t.Fatal("expected token to be removed after revoke")
	}
}

func TestAuthService_ValidateSession_Valid(t *testing.T) {
	sessions := newFakeSessionStore()
	sessions.data["tok"] = testAddr1
	svc := newAuthSvc(newFakeChallengeStore(), sessions, newFakeNonceStore(), &fakeVerifier{})

	addr, err := svc.ValidateSession(context.Background(), "tok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if addr != testAddr1 {
		t.Errorf("want %s, got %s", testAddr1, addr)
	}
}

func TestAuthService_ValidateSession_Invalid(t *testing.T) {
	svc := newAuthSvc(newFakeChallengeStore(), newFakeSessionStore(), newFakeNonceStore(), &fakeVerifier{})
	_, err := svc.ValidateSession(context.Background(), "no-such-token")
	if err == nil {
		t.Fatal("expected error for unknown token")
	}
}
