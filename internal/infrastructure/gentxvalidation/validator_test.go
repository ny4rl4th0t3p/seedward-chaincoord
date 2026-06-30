package gentxvalidation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cosmos/btcutil/bech32"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	require.NoError(t, err)

	out := New().Validate(raw, osmosisParams())

	if !gentxvalidate.AllOK(out.Results) {
		for _, r := range out.Results {
			assert.True(t, r.OK, "invariant %s failed: %s", r.Invariant, r.Reason)
		}
		require.Fail(t, "expected the real osmosis gentx to pass all invariants")
	}

	const wantPubKey = "f5DzEhtQbnmXE/WZQsX+I8RljPdEU0u0ncVGtniFyEM="
	assert.Equal(t, wantPubKey, out.ConsensusPubKeyB64)
}

// TestValidator_Validate_PopulatesValidatorAddress: a passing gentx yields the validator's
// self-delegation account address — the account form of the gentx's valoper validator_address
// (same key, launch HRP), not the operator form.
func TestValidator_Validate_PopulatesValidatorAddress(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "gentx-Bi23Labs.json"))
	require.NoError(t, err)

	out := New().Validate(raw, osmosisParams())
	require.True(t, gentxvalidate.AllOK(out.Results), "expected the real osmosis gentx to pass all invariants")

	require.NotEmpty(t, out.ValidatorAddress, "ValidatorAddress must be populated on a passing gentx")
	assert.True(t, strings.HasPrefix(out.ValidatorAddress, "osmo1"),
		"ValidatorAddress = %q, want the osmo1… account form", out.ValidatorAddress)

	// Same underlying key as the gentx's valoper validator_address (account vs operator HRP).
	g, err := gentxvalidate.Decode(raw)
	require.NoError(t, err)
	acc := decodeBech32Payload(t, out.ValidatorAddress, "osmo")
	val := decodeBech32Payload(t, g.Msg.ValidatorAddress, "osmovaloper")
	assert.Equal(t, val, acc, "account address and valoper validator_address must be the same key")
}

// decodeBech32Payload decodes a bech32 address, asserts its HRP, and returns the 8-bit payload.
func decodeBech32Payload(t *testing.T, addr, wantHRP string) []byte {
	t.Helper()
	hrp, data5, err := bech32.Decode(addr, 1023)
	require.NoError(t, err, "decode %q", addr)
	require.Equal(t, wantHRP, hrp, "unexpected HRP")
	payload, err := bech32.ConvertBits(data5, 5, 8, false)
	require.NoError(t, err, "convert bits")
	return payload
}

// TestValidator_Validate_MalformedReturnsNoPubKey: a failing gentx yields a
// not-OK result set and no consensus pubkey or validator address.
func TestValidator_Validate_MalformedReturnsNoPubKey(t *testing.T) {
	out := New().Validate([]byte("not json"), osmosisParams())

	require.False(t, gentxvalidate.AllOK(out.Results), "malformed gentx must not pass")
	assert.Empty(t, out.ConsensusPubKeyB64, "ConsensusPubKeyB64 must be empty on failure")
	assert.Empty(t, out.ValidatorAddress, "ValidatorAddress must be empty on failure")
}
