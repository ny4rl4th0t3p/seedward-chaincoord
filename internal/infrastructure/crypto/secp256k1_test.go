package crypto_test

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"golang.org/x/crypto/ripemd160" //nolint:gosec,staticcheck // ripemd160 is required by the Cosmos address derivation spec (RIPEMD160(SHA256(pubkey)))

	"github.com/cosmos/btcutil/bech32"
	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/infrastructure/crypto"
)

// deriveSecp256k1Address computes the Cosmos SDK bech32 address for a
// compressed secp256k1 public key: ripemd160(sha256(pubKeyBytes))[0:20].
func deriveSecp256k1Address(pubKeyBytes []byte) string {
	sha := sha256.Sum256(pubKeyBytes)
	ripe := ripemd160.New() //nolint:gosec // ripemd160 is required by the Cosmos address derivation spec
	ripe.Write(sha[:])
	addrBytes := ripe.Sum(nil)
	converted, err := bech32.ConvertBits(addrBytes, 8, 5, true)
	if err != nil {
		panic(err)
	}
	addr, err := bech32.Encode("cosmos", converted)
	if err != nil {
		panic(err)
	}
	return addr
}

// signMsg produces a 64-byte compact secp256k1 ECDSA signature (r‖s) over
// the ADR-036 amino sign bytes for (addr, msg). Uses SignCompact and strips
// the recovery byte. This matches what `gaiad tx sign --sign-mode amino-json` produces.
func signMsg(privKey *secp.PrivateKey, addr string, msg []byte) []byte {
	aminoBytes := crypto.BuildADR036AminoBytes(addr, msg)
	msgHash := sha256.Sum256(aminoBytes)
	compactSig := ecdsa.SignCompact(privKey, msgHash[:], true)
	return compactSig[1:] // strip 1-byte recovery flag, keep 64-byte r‖s
}

func TestSecp256k1Verifier_ValidSignature(t *testing.T) {
	privKey, err := secp.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	pubKeyBytes := privKey.PubKey().SerializeCompressed()
	pubKeyB64 := base64.StdEncoding.EncodeToString(pubKeyBytes)
	addr := deriveSecp256k1Address(pubKeyBytes)

	msg := []byte("test message")
	sig := signMsg(privKey, addr, msg)

	v := crypto.NewSecp256k1Verifier()
	if err := v.Verify(addr, pubKeyB64, msg, sig); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestSecp256k1Verifier_WrongPublicKey(t *testing.T) {
	privKey1, _ := secp.GeneratePrivateKey()
	privKey2, _ := secp.GeneratePrivateKey()

	pub1Bytes := privKey1.PubKey().SerializeCompressed()
	pub2Bytes := privKey2.PubKey().SerializeCompressed()
	addr1 := deriveSecp256k1Address(pub1Bytes)

	msg := []byte("test message")
	sig := signMsg(privKey1, addr1, msg)

	// Use pub2's address but pub1's pubkey — address/pubkey mismatch.
	addr2 := deriveSecp256k1Address(pub2Bytes)
	pub1B64 := base64.StdEncoding.EncodeToString(pub1Bytes)

	v := crypto.NewSecp256k1Verifier()
	if err := v.Verify(addr2, pub1B64, msg, sig); err == nil {
		t.Fatal("expected error: pubkey does not correspond to claimed address")
	}
}

func TestSecp256k1Verifier_TamperedMessage(t *testing.T) {
	privKey, _ := secp.GeneratePrivateKey()
	pubKeyBytes := privKey.PubKey().SerializeCompressed()
	pubKeyB64 := base64.StdEncoding.EncodeToString(pubKeyBytes)
	addr := deriveSecp256k1Address(pubKeyBytes)

	msg := []byte("original message")
	sig := signMsg(privKey, addr, msg)

	v := crypto.NewSecp256k1Verifier()
	if err := v.Verify(addr, pubKeyB64, []byte("tampered message"), sig); err == nil {
		t.Fatal("expected error for tampered message")
	}
}

func TestSecp256k1Verifier_AddressMismatch(t *testing.T) {
	privKey1, _ := secp.GeneratePrivateKey()
	privKey2, _ := secp.GeneratePrivateKey()

	pub1Bytes := privKey1.PubKey().SerializeCompressed()
	pub2Bytes := privKey2.PubKey().SerializeCompressed()
	addr1 := deriveSecp256k1Address(pub1Bytes)

	msg := []byte("test message")
	sig := signMsg(privKey1, addr1, msg)

	// Correct pubkey for sig, but address derived from a different key.
	pub1B64 := base64.StdEncoding.EncodeToString(pub1Bytes)
	addr2 := deriveSecp256k1Address(pub2Bytes)

	v := crypto.NewSecp256k1Verifier()
	if err := v.Verify(addr2, pub1B64, msg, sig); err == nil {
		t.Fatal("expected error: pubkey does not derive to claimed address")
	}
}

func TestSecp256k1Verifier_BadSignatureLength(t *testing.T) {
	privKey, _ := secp.GeneratePrivateKey()
	pubKeyBytes := privKey.PubKey().SerializeCompressed()
	pubKeyB64 := base64.StdEncoding.EncodeToString(pubKeyBytes)
	addr := deriveSecp256k1Address(pubKeyBytes)

	msg := []byte("test message")

	v := crypto.NewSecp256k1Verifier()
	if err := v.Verify(addr, pubKeyB64, msg, []byte("tooshort")); err == nil {
		t.Fatal("expected error for bad signature length")
	}
}

func TestSecp256k1Verifier_EmptyOperatorAddr(t *testing.T) {
	privKey, _ := secp.GeneratePrivateKey()
	pubKeyBytes := privKey.PubKey().SerializeCompressed()
	pubKeyB64 := base64.StdEncoding.EncodeToString(pubKeyBytes)
	addr := deriveSecp256k1Address(pubKeyBytes)
	msg := []byte("test message")
	sig := signMsg(privKey, addr, msg)

	v := crypto.NewSecp256k1Verifier()
	if err := v.Verify("", pubKeyB64, msg, sig); err == nil {
		t.Fatal("expected error for empty operator address")
	}
}

func TestSecp256k1Verifier_EmptyPublicKey(t *testing.T) {
	privKey, _ := secp.GeneratePrivateKey()
	pubKeyBytes := privKey.PubKey().SerializeCompressed()
	addr := deriveSecp256k1Address(pubKeyBytes)
	msg := []byte("test message")
	sig := signMsg(privKey, addr, msg)

	v := crypto.NewSecp256k1Verifier()
	if err := v.Verify(addr, "", msg, sig); err == nil {
		t.Fatal("expected error for empty public key")
	}
}

func TestSecp256k1Verifier_BadBase64PublicKey(t *testing.T) {
	privKey, _ := secp.GeneratePrivateKey()
	pubKeyBytes := privKey.PubKey().SerializeCompressed()
	addr := deriveSecp256k1Address(pubKeyBytes)
	msg := []byte("test message")
	sig := signMsg(privKey, addr, msg)

	v := crypto.NewSecp256k1Verifier()
	if err := v.Verify(addr, "not-valid-base64!!!", msg, sig); err == nil {
		t.Fatal("expected error for bad base64 public key")
	}
}

func TestSecp256k1Verifier_WrongSizePublicKey(t *testing.T) {
	privKey, _ := secp.GeneratePrivateKey()
	pubKeyBytes := privKey.PubKey().SerializeCompressed()
	addr := deriveSecp256k1Address(pubKeyBytes)
	msg := []byte("test message")
	sig := signMsg(privKey, addr, msg)

	// 32 bytes instead of the required 33.
	shortB64 := base64.StdEncoding.EncodeToString(pubKeyBytes[:32])
	v := crypto.NewSecp256k1Verifier()
	if err := v.Verify(addr, shortB64, msg, sig); err == nil {
		t.Fatal("expected error for wrong-size public key")
	}
}

func TestSecp256k1Verifier_InvalidPublicKeyPoint(t *testing.T) {
	privKey, _ := secp.GeneratePrivateKey()
	pubKeyBytes := privKey.PubKey().SerializeCompressed()
	addr := deriveSecp256k1Address(pubKeyBytes)
	msg := []byte("test message")
	sig := signMsg(privKey, addr, msg)

	// 33 bytes but not a valid secp256k1 point: flip the prefix byte.
	bad := make([]byte, 33)
	copy(bad, pubKeyBytes)
	bad[0] = 0x00
	badB64 := base64.StdEncoding.EncodeToString(bad)

	v := crypto.NewSecp256k1Verifier()
	if err := v.Verify(addr, badB64, msg, sig); err == nil {
		t.Fatal("expected error for invalid secp256k1 point")
	}
}

func TestSecp256k1Verifier_InvalidOperatorAddress(t *testing.T) {
	privKey, _ := secp.GeneratePrivateKey()
	pubKeyBytes := privKey.PubKey().SerializeCompressed()
	pubKeyB64 := base64.StdEncoding.EncodeToString(pubKeyBytes)
	addr := deriveSecp256k1Address(pubKeyBytes)
	msg := []byte("test message")
	sig := signMsg(privKey, addr, msg)

	v := crypto.NewSecp256k1Verifier()
	if err := v.Verify("not-a-valid-bech32-address", pubKeyB64, msg, sig); err == nil {
		t.Fatal("expected error for invalid operator address")
	}
}
