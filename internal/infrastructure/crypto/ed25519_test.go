package crypto_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"github.com/cosmos/btcutil/bech32"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/crypto"
)

// deriveTestAddress computes the Cosmos SDK bech32 address for a given Ed25519
// public key: sha256(pubkeyBytes)[0:20], encoded with the "cosmos" HRP.
func deriveTestAddress(pub ed25519.PublicKey) string {
	hash := sha256.Sum256(pub)
	converted, err := bech32.ConvertBits(hash[:20], 8, 5, true)
	if err != nil {
		panic(err)
	}
	addr, err := bech32.Encode("cosmos", converted)
	if err != nil {
		panic(err)
	}
	return addr
}

// knownAddr is a valid bech32 address used as a placeholder in tests where the
// specific address value does not matter (the error being tested is caught before
// the address check).
const knownAddr = "cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu"

func TestEd25519Verifier_ValidSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("test message")
	sig := ed25519.Sign(priv, msg)
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	addr := deriveTestAddress(pub)

	v := crypto.NewEd25519Verifier()
	if err := v.Verify(addr, pubB64, msg, sig); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestEd25519Verifier_EmptyOperatorAddr(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("test message")
	sig := ed25519.Sign(priv, msg)
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	v := crypto.NewEd25519Verifier()
	if err := v.Verify("", pubB64, msg, sig); err == nil {
		t.Fatal("expected error for empty operator address")
	}
}

func TestEd25519Verifier_EmptyPublicKey(t *testing.T) {
	v := crypto.NewEd25519Verifier()
	err := v.Verify(knownAddr, "", []byte("msg"), []byte("sig"))
	if err == nil {
		t.Fatal("expected error for empty public key")
	}
}

func TestEd25519Verifier_BadBase64PublicKey(t *testing.T) {
	v := crypto.NewEd25519Verifier()
	err := v.Verify(knownAddr, "not-valid-base64!!!", []byte("msg"), []byte("sig"))
	if err == nil {
		t.Fatal("expected error for bad base64 public key")
	}
}

func TestEd25519Verifier_WrongSizePublicKey(t *testing.T) {
	short := base64.StdEncoding.EncodeToString([]byte("tooshort"))
	v := crypto.NewEd25519Verifier()
	err := v.Verify(knownAddr, short, []byte("msg"), []byte("sig"))
	if err == nil {
		t.Fatal("expected error for wrong-size public key")
	}
}

func TestEd25519Verifier_AddressMismatch(t *testing.T) {
	// Generate two independent keypairs.
	pub1, priv1, _ := ed25519.GenerateKey(rand.Reader)
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)

	msg := []byte("test message")
	sig := ed25519.Sign(priv1, msg)

	pub1B64 := base64.StdEncoding.EncodeToString(pub1)
	// Use the address derived from pub2 — does not match pub1.
	addr2 := deriveTestAddress(pub2)

	v := crypto.NewEd25519Verifier()
	err := v.Verify(addr2, pub1B64, msg, sig)
	if err == nil {
		t.Fatal("expected error: pubkey does not correspond to claimed address")
	}
}

func TestEd25519Verifier_TamperedMessage(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("original message")
	sig := ed25519.Sign(priv, msg)
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	addr := deriveTestAddress(pub)

	v := crypto.NewEd25519Verifier()
	tampered := []byte("tampered message")
	if err := v.Verify(addr, pubB64, tampered, sig); err == nil {
		t.Fatal("expected error for tampered message")
	}
}
