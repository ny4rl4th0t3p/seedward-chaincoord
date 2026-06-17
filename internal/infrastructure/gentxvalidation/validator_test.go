package gentxvalidation

import (
	"os"
	"path/filepath"
	"testing"

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

// TestValidator_Validate_MalformedReturnsNoPubKey: a failing gentx yields a
// not-OK result set and no consensus pubkey.
func TestValidator_Validate_MalformedReturnsNoPubKey(t *testing.T) {
	out := New().Validate([]byte("not json"), osmosisParams())

	if gentxvalidate.AllOK(out.Results) {
		t.Fatal("malformed gentx must not pass")
	}
	if out.ConsensusPubKeyB64 != "" {
		t.Errorf("ConsensusPubKeyB64 must be empty on failure, got %q", out.ConsensusPubKeyB64)
	}
}
