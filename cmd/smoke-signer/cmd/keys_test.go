package cmd

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The whole point of --privkey-hex: a key exported as hex and re-imported must yield
// byte-identical key material, pubkey, and address. This is what lets the demo seeder derive keys
// elsewhere (gaiad HD derivation) yet have smoke-signer sign coordd auth for the same account.
func TestPrivKeyFromHexRoundTripsKey(t *testing.T) {
	idxKey := privKeyFromIndex(3)

	got, err := privKeyFromHex(privKeyHex(idxKey))
	require.NoError(t, err)

	assert.Equal(t, idxKey.Serialize(), got.Serialize(), "private key bytes")
	assert.Equal(t, pubKeyB64(idxKey), pubKeyB64(got), "pubkey")

	want, err := deriveAddress(idxKey, "cosmos")
	require.NoError(t, err)
	gotAddr, err := deriveAddress(got, "cosmos")
	require.NoError(t, err)
	assert.Equal(t, want, gotAddr, "address")
}

func TestPrivKeyFromHexRejectsBadInput(t *testing.T) {
	t.Run("non-hex", func(t *testing.T) {
		_, err := privKeyFromHex("not-hex")
		require.Error(t, err)
	})

	t.Run("wrong length", func(t *testing.T) {
		_, err := privKeyFromHex("abcd")
		require.Error(t, err)
	})

	t.Run("trims surrounding whitespace", func(t *testing.T) {
		h := privKeyHex(privKeyFromIndex(1))
		got, err := privKeyFromHex("  " + h + "\n")
		require.NoError(t, err)
		assert.Equal(t, privKeyFromIndex(1).Serialize(), got.Serialize())
	})
}

func TestDeriveAddressUsesHRP(t *testing.T) {
	priv := privKeyFromIndex(0)

	cosmosAddr, err := deriveAddress(priv, "cosmos")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(cosmosAddr, "cosmos1"), "got %s", cosmosAddr)

	osmoAddr, err := deriveAddress(priv, "osmo")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(osmoAddr, "osmo1"), "got %s", osmoAddr)
}

// signADR036 must emit a valid secp256k1 signature over exactly the ADR-036 amino bytes coordd
// reconstructs — verify it against the pubkey so a change to either side is caught.
func TestSignADR036ProducesValidSignature(t *testing.T) {
	priv := privKeyFromIndex(2)
	addr, err := deriveAddress(priv, "cosmos")
	require.NoError(t, err)

	payload := []byte(`{"challenge":"abc"}`)
	rs, err := base64.StdEncoding.DecodeString(signADR036(priv, addr, payload))
	require.NoError(t, err)
	require.Len(t, rs, 64, "compact r||s signature (recovery byte stripped)")

	msgHash := sha256.Sum256(buildADR036AminoBytes(addr, payload))
	var r, s secp.ModNScalar
	require.False(t, r.SetByteSlice(rs[:32]), "r in range")
	require.False(t, s.SetByteSlice(rs[32:]), "s in range")
	assert.True(t, ecdsa.NewSignature(&r, &s).Verify(msgHash[:], priv.PubKey()),
		"signature verifies against the derived pubkey")
}
