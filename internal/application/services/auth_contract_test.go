package services_test

// Contract test for VerifyChallengeInput canonical JSON signing bytes.
//
// The TypeScript validator web app must produce byte-identical output to
// canonicaljson.MarshalForSigning(VerifyChallengeInput{...}) before calling
// signArbitrary. This test pins the exact output so any Go-side change that
// would break the TypeScript client causes an immediate build failure.
//
// If this test needs to change, the TypeScript buildAuthPayload function in
// web/app/utils/auth.ts must be updated to match before merging.

import (
	"strings"
	"testing"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/services"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/pkg/canonicaljson"
)

func TestVerifyChallengeInput_CanonicalSigningBytes(t *testing.T) {
	input := services.VerifyChallengeInput{
		OperatorAddress: "cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu",
		PubKeyB64:       "A1B2C3D4E5F6A1B2C3D4E5F6A1B2C3D4E5F6A1B2C3D4==",
		Challenge:       "dGVzdC1jaGFsbGVuZ2U=",
		Nonce:           "unique-nonce-abc",
		Timestamp:       "2026-01-01T00:00:00Z",
		Signature:       "should-be-stripped",
	}

	got, err := canonicaljson.MarshalForSigning(input)
	if err != nil {
		t.Fatalf("MarshalForSigning: %v", err)
	}

	// This is the exact string the TypeScript client must produce.
	// Field order: challenge → nonce → operator_address → timestamp (lexicographic).
	// nonce is KEPT (bound to the signature for replay protection); pubkey_b64 and
	// signature are stripped.
	want := `{"challenge":"dGVzdC1jaGFsbGVuZ2U=","nonce":"unique-nonce-abc","operator_address":"cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu","timestamp":"2026-01-01T00:00:00Z"}`

	if string(got) != want {
		t.Errorf("canonical signing bytes mismatch\ngot:  %s\nwant: %s", got, want)
	}
}

func TestVerifyChallengeInput_StrippedFields(t *testing.T) {
	// Verify individually that each stripped field is absent regardless of value.
	input := services.VerifyChallengeInput{
		OperatorAddress: "cosmos1abc",
		PubKeyB64:       "pubkey",
		Challenge:       "challenge",
		Nonce:           "nonce",
		Timestamp:       "2026-06-01T12:00:00Z",
		Signature:       "sig",
	}

	got, err := canonicaljson.MarshalForSigning(input)
	if err != nil {
		t.Fatalf("MarshalForSigning: %v", err)
	}

	s := string(got)
	for _, forbidden := range []string{"pubkey_b64", "\"signature\""} {
		if strings.Contains(s, forbidden) {
			t.Errorf("signing bytes must not contain %q, got: %s", forbidden, s)
		}
	}
	// nonce must be present — it is bound to the signature for replay protection.
	if !strings.Contains(s, "\"nonce\"") {
		t.Errorf("signing bytes must contain nonce, got: %s", s)
	}
}
