package cmd

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/crypto/ripemd160" //nolint:gosec,staticcheck // ripemd160 is required by the Cosmos address derivation spec

	"github.com/cosmos/btcutil/bech32"
	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/spf13/cobra"
)

const seedPrefix = "chaincoord-smoke-signer-v1:"

// secp256k1PrivKeyLen is the byte length of a raw secp256k1 private key.
const secp256k1PrivKeyLen = 32

// privKeyFromIndex returns the deterministic secp256k1 key for the given index — the fixed-seed
// scheme the smoke test relies on (same index → same key and address across runs).
func privKeyFromIndex(index int) *secp.PrivateKey {
	seed := sha256.Sum256([]byte(fmt.Sprintf("%s%d", seedPrefix, index)))
	return secp.PrivKeyFromBytes(seed[:])
}

// privKeyFromHex parses a raw 32-byte secp256k1 private key from a hex string (as produced by
// `gaiad keys export --unarmored-hex`). This lets the demo seeder sign coordd auth as HD-derived,
// wallet-importable accounts rather than the deterministic key-index keys, while reusing the exact
// ADR-036 signing + address derivation below (which match coordd's verifier).
func privKeyFromHex(h string) (*secp.PrivateKey, error) {
	b, err := hex.DecodeString(strings.TrimSpace(h))
	if err != nil {
		return nil, fmt.Errorf("decoding privkey hex: %w", err)
	}
	if len(b) != secp256k1PrivKeyLen {
		return nil, fmt.Errorf("privkey must be %d bytes, got %d", secp256k1PrivKeyLen, len(b))
	}
	return secp.PrivKeyFromBytes(b), nil
}

// compressedPubKey returns the 33-byte compressed public key.
func compressedPubKey(priv *secp.PrivateKey) []byte {
	return priv.PubKey().SerializeCompressed()
}

// pubKeyB64 returns the base64-encoded compressed public key.
func pubKeyB64(priv *secp.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(compressedPubKey(priv))
}

// privKeyHex returns the raw private key bytes as a hex string (for gaiad import-hex).
func privKeyHex(priv *secp.PrivateKey) string {
	return hex.EncodeToString(priv.Serialize())
}

// deriveAddress returns the bech32 address under the given HRP.
// Derivation follows the Cosmos SDK convention: bech32(hrp, ripemd160(sha256(pubkey))).
func deriveAddress(priv *secp.PrivateKey, hrp string) (string, error) {
	pub := compressedPubKey(priv)
	sha := sha256.Sum256(pub)
	ripe := ripemd160.New() //nolint:gosec // ripemd160 is required by the Cosmos address derivation spec
	ripe.Write(sha[:])
	addrBytes := ripe.Sum(nil)

	converted, err := bech32.ConvertBits(addrBytes, 8, 5, true)
	if err != nil {
		return "", fmt.Errorf("converting address bits: %w", err)
	}
	addr, err := bech32.Encode(hrp, converted)
	if err != nil {
		return "", fmt.Errorf("encoding address: %w", err)
	}
	return addr, nil
}

// signADR036 signs payload using ADR-036 amino bytes with the given key.
// Returns the 64-byte compact r‖s signature (recovery byte stripped), base64-encoded.
func signADR036(priv *secp.PrivateKey, signerAddr string, payload []byte) string {
	adr036 := buildADR036AminoBytes(signerAddr, payload)
	msgHash := sha256.Sum256(adr036)
	compactSig := ecdsa.SignCompact(priv, msgHash[:], true)
	return base64.StdEncoding.EncodeToString(compactSig[1:]) // strip 1-byte recovery flag
}

// buildADR036AminoBytes constructs the canonical amino JSON sign bytes used by ADR-036.
// Matches internal/infrastructure/crypto.BuildADR036AminoBytes exactly.
func buildADR036AminoBytes(signerAddr string, payload []byte) []byte {
	data := base64.StdEncoding.EncodeToString(payload)
	return []byte(fmt.Sprintf(
		`{"account_number":"0","chain_id":"","fee":{"amount":[],"gas":"0"},"memo":"",`+
			`"msgs":[{"type":"sign/MsgSignData","value":{"data":"%s","signer":"%s"}}],"sequence":"0"}`,
		data, signerAddr,
	))
}

// addKeySelectionFlags registers the mutually-exclusive key-selection flags shared by every command
// that needs a signing key: --key-index (deterministic smoke-test keys) and --privkey-hex (a raw
// 32-byte secp256k1 key as hex, e.g. from `gaiad keys export --unarmored-hex`, so the demo seeder
// can act as HD-derived, wallet-importable accounts). Exactly one must be provided.
func addKeySelectionFlags(cmd *cobra.Command) {
	cmd.Flags().Int("key-index", 0, "deterministic key index (0 = coordinator, 1-4 = validators)")
	cmd.Flags().String("privkey-hex", "", "raw 32-byte secp256k1 private key as hex (alternative to --key-index)")
	cmd.MarkFlagsMutuallyExclusive("key-index", "privkey-hex")
	cmd.MarkFlagsOneRequired("key-index", "privkey-hex")
}

// resolvePrivKey returns the signing key selected by the command's flags: --privkey-hex if set,
// otherwise the deterministic key for --key-index. The flags are mutually exclusive and exactly one
// is required (enforced by addKeySelectionFlags).
func resolvePrivKey(cmd *cobra.Command) (*secp.PrivateKey, error) {
	if cmd.Flags().Changed("privkey-hex") {
		h, _ := cmd.Flags().GetString("privkey-hex")
		return privKeyFromHex(h)
	}
	index, _ := cmd.Flags().GetInt("key-index")
	return privKeyFromIndex(index), nil
}
