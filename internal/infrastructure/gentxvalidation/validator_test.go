package gentxvalidation

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cosmos/btcutil/bech32"

	"github.com/ny4rl4th0t3p/seedward-libs/gentxvalidate"
)

func osmosisParams() gentxvalidate.Params {
	return gentxvalidate.Params{
		ChainID:           "osmosis-1",
		BondDenom:         "uosmo",
		Bech32Prefix:      "osmo",
		MinSelfDelegation: "1",
	}
}

// TestValidator_Validate_RealGentxPasses runs the adapter over a real signed
// osmosis-1 mainnet gentx: every invariant (including the cryptographic
// signature) must pass, and the consensus pubkey must round-trip to the exact
// base64 string coordd persists.
func TestValidator_Validate_RealGentxPasses(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "gentx-Bi23Labs.json"))
	if err != nil {
		t.Fatal(err)
	}

	out := New().Validate(raw, osmosisParams())

	if !gentxvalidate.AllOK(out.Results) {
		for _, r := range out.Results {
			if !r.OK {
				t.Errorf("invariant %s failed: %s", r.Invariant, r.Reason)
			}
		}
		t.Fatal("expected the real osmosis gentx to pass all invariants")
	}

	const wantPubKey = "f5DzEhtQbnmXE/WZQsX+I8RljPdEU0u0ncVGtniFyEM="
	if out.ConsensusPubKeyB64 != wantPubKey {
		t.Errorf("ConsensusPubKeyB64 = %q, want %q", out.ConsensusPubKeyB64, wantPubKey)
	}
}

// TestValidator_Validate_PopulatesValidatorAddress: a passing gentx yields the validator's
// self-delegation account address — the account form of the gentx's valoper validator_address
// (same key, launch HRP), not the operator form.
func TestValidator_Validate_PopulatesValidatorAddress(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "gentx-Bi23Labs.json"))
	if err != nil {
		t.Fatal(err)
	}

	out := New().Validate(raw, osmosisParams())
	if !gentxvalidate.AllOK(out.Results) {
		t.Fatal("expected the real osmosis gentx to pass all invariants")
	}

	if out.ValidatorAddress == "" {
		t.Fatal("ValidatorAddress must be populated on a passing gentx")
	}
	if !strings.HasPrefix(out.ValidatorAddress, "osmo1") {
		t.Errorf("ValidatorAddress = %q, want the osmo1… account form", out.ValidatorAddress)
	}

	// Same underlying key as the gentx's valoper validator_address (account vs operator HRP).
	g, err := gentxvalidate.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if acc, val := decodeBech32Payload(t, out.ValidatorAddress, "osmo"),
		decodeBech32Payload(t, g.Msg.ValidatorAddress, "osmovaloper"); !bytes.Equal(acc, val) {
		t.Errorf("account address and valoper validator_address are different keys:\n acc: %x\n val: %x", acc, val)
	}
}

// decodeBech32Payload decodes a bech32 address, asserts its HRP, and returns the 8-bit payload.
func decodeBech32Payload(t *testing.T, addr, wantHRP string) []byte {
	t.Helper()
	hrp, data5, err := bech32.Decode(addr, 1023)
	if err != nil {
		t.Fatalf("decode %q: %v", addr, err)
	}
	if hrp != wantHRP {
		t.Fatalf("HRP %q, want %q", hrp, wantHRP)
	}
	payload, err := bech32.ConvertBits(data5, 5, 8, false)
	if err != nil {
		t.Fatalf("convert bits: %v", err)
	}
	return payload
}

// TestValidator_Validate_MalformedReturnsNoPubKey: a failing gentx yields a
// not-OK result set and no consensus pubkey or validator address.
func TestValidator_Validate_MalformedReturnsNoPubKey(t *testing.T) {
	out := New().Validate([]byte("not json"), osmosisParams())

	if gentxvalidate.AllOK(out.Results) {
		t.Fatal("malformed gentx must not pass")
	}
	if out.ConsensusPubKeyB64 != "" {
		t.Errorf("ConsensusPubKeyB64 must be empty on failure, got %q", out.ConsensusPubKeyB64)
	}
	if out.ValidatorAddress != "" {
		t.Errorf("ValidatorAddress must be empty on failure, got %q", out.ValidatorAddress)
	}
}
