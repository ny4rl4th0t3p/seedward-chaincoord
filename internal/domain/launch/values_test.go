package launch_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// ---- OperatorAddress --------------------------------------------------------

func TestNewOperatorAddress_Empty(t *testing.T) {
	_, err := launch.NewOperatorAddress("")
	require.Error(t, err, "expected error for empty string")
}

func TestNewOperatorAddress_InvalidBech32(t *testing.T) {
	_, err := launch.NewOperatorAddress("not-a-bech32-string")
	require.Error(t, err, "expected error for invalid bech32")
}

func TestNewOperatorAddress_Valid(t *testing.T) {
	addr, err := launch.NewOperatorAddress(testAddr1)
	require.NoError(t, err)
	assert.Equal(t, testAddr1, addr.String())
}

func TestOperatorAddress_Equal(t *testing.T) {
	a, _ := launch.NewOperatorAddress(testAddr1)
	b, _ := launch.NewOperatorAddress(testAddr1)
	c, _ := launch.NewOperatorAddress(testAddr2)
	assert.True(t, a.Equal(b), "same address should be equal")
	assert.False(t, a.Equal(c), "different addresses should not be equal")
}

// ---- GenesisHash ------------------------------------------------------------

func TestNewGenesisHash_WrongLength(t *testing.T) {
	_, err := launch.NewGenesisHash("abc123") // too short
	require.Error(t, err, "expected error for wrong length")
}

func TestNewGenesisHash_InvalidHex(t *testing.T) {
	// 64 chars but not valid hex (contains 'g')
	_, err := launch.NewGenesisHash("gggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggg")
	require.Error(t, err, "expected error for invalid hex")
}

func TestNewGenesisHash_Valid(t *testing.T) {
	hash := "a665a45920422f9d417e4867efdc4fb8a04a1f3fff1fa07e998e86f7f7a27ae3"
	h, err := launch.NewGenesisHash(hash)
	require.NoError(t, err)
	assert.Equal(t, hash, h.String())
}

func TestNewGenesisHash_CaseNormalized(t *testing.T) {
	lower := "a665a45920422f9d417e4867efdc4fb8a04a1f3fff1fa07e998e86f7f7a27ae3"
	upper := "A665A45920422F9D417E4867EFDC4FB8A04A1F3FFF1FA07E998E86F7F7A27AE3"
	h, err := launch.NewGenesisHash(upper)
	require.NoError(t, err)
	assert.Equal(t, lower, h.String(), "hash should be normalized to lowercase")
}

func TestGenesisHash_Equal(t *testing.T) {
	hash := "a665a45920422f9d417e4867efdc4fb8a04a1f3fff1fa07e998e86f7f7a27ae3"
	other := "b665a45920422f9d417e4867efdc4fb8a04a1f3fff1fa07e998e86f7f7a27ae3"
	a, _ := launch.NewGenesisHash(hash)
	b, _ := launch.NewGenesisHash(hash)
	c, _ := launch.NewGenesisHash(other)
	assert.True(t, a.Equal(b), "same hash should be equal")
	assert.False(t, a.Equal(c), "different hashes should not be equal")
}

// ---- PeerAddress ------------------------------------------------------------

func TestNewPeerAddress_NoAt(t *testing.T) {
	_, err := launch.NewPeerAddress("1234567890abcdef1234567890abcdef12345678127.0.0.1:26656")
	require.Error(t, err, "expected error: missing @")
}

func TestNewPeerAddress_InvalidNodeID_NonHex(t *testing.T) {
	_, err := launch.NewPeerAddress("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx@127.0.0.1:26656")
	require.Error(t, err, "expected error: node_id not hex")
}

func TestNewPeerAddress_InvalidNodeID_WrongLength(t *testing.T) {
	_, err := launch.NewPeerAddress("abc123@127.0.0.1:26656") // too short
	require.Error(t, err, "expected error: node_id wrong length")
}

func TestNewPeerAddress_MissingPort(t *testing.T) {
	_, err := launch.NewPeerAddress("1234567890abcdef1234567890abcdef12345678@127.0.0.1")
	require.Error(t, err, "expected error: missing port")
}

func TestNewPeerAddress_InvalidPort(t *testing.T) {
	_, err := launch.NewPeerAddress("1234567890abcdef1234567890abcdef12345678@127.0.0.1:99999")
	require.Error(t, err, "expected error: port out of range")
}

func TestNewPeerAddress_Valid(t *testing.T) {
	s := "1234567890abcdef1234567890abcdef12345678@127.0.0.1:26656"
	p, err := launch.NewPeerAddress(s)
	require.NoError(t, err)
	assert.Equal(t, s, p.String())
}

// ---- RPCEndpoint ------------------------------------------------------------

func TestNewRPCEndpoint_InvalidURL(t *testing.T) {
	_, err := launch.NewRPCEndpoint("not a url")
	require.Error(t, err, "expected error for invalid URL")
}

func TestNewRPCEndpoint_WrongScheme(t *testing.T) {
	_, err := launch.NewRPCEndpoint("ftp://localhost:26657")
	require.Error(t, err, "expected error for ftp:// scheme")
}

func TestNewRPCEndpoint_MissingHost(t *testing.T) {
	_, err := launch.NewRPCEndpoint("http:///path")
	require.Error(t, err, "expected error for missing host")
}

func TestNewRPCEndpoint_HTTPValid(t *testing.T) {
	s := "http://localhost:26657"
	e, err := launch.NewRPCEndpoint(s)
	require.NoError(t, err)
	assert.Equal(t, s, e.String())
}

func TestNewRPCEndpoint_HTTPSValid(t *testing.T) {
	_, err := launch.NewRPCEndpoint("https://rpc.example.com:443")
	require.NoError(t, err, "unexpected error for https")
}

// ---- Signature --------------------------------------------------------------

func TestNewSignature_Empty(t *testing.T) {
	_, err := launch.NewSignature("")
	require.Error(t, err, "expected error for empty signature")
}

func TestNewSignature_InvalidBase64(t *testing.T) {
	_, err := launch.NewSignature("!!!not base64!!!")
	require.Error(t, err, "expected error for invalid base64")
}

func TestNewSignature_WrongLength(t *testing.T) {
	// Valid base64 but only 4 bytes (not 64).
	_, err := launch.NewSignature("AAAA")
	require.Error(t, err, "expected error: decoded length ≠ 64 bytes")
}

func TestNewSignature_Valid(t *testing.T) {
	// 86 'A' chars + "==" = 88 base64 chars → decodes to exactly 64 bytes.
	s := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="
	sig, err := launch.NewSignature(s)
	require.NoError(t, err)
	assert.Equal(t, s, sig.String())
}

// ---- CommissionRate ---------------------------------------------------------

func TestNewCommissionRate_NotDecimal(t *testing.T) {
	_, err := launch.NewCommissionRate("abc")
	require.Error(t, err, "expected error for non-decimal")
}

func TestNewCommissionRate_Negative(t *testing.T) {
	_, err := launch.NewCommissionRate("-0.01")
	require.Error(t, err, "expected error for negative rate")
}

func TestNewCommissionRate_GreaterThanOne(t *testing.T) {
	_, err := launch.NewCommissionRate("1.01")
	require.Error(t, err, "expected error for rate > 1.0")
}

func TestNewCommissionRate_Zero(t *testing.T) {
	r, err := launch.NewCommissionRate("0")
	require.NoError(t, err)
	assert.Zero(t, r.Float64())
}

func TestNewCommissionRate_One(t *testing.T) {
	_, err := launch.NewCommissionRate("1.0")
	require.NoError(t, err, "unexpected error for rate 1.0")
}

func TestNewCommissionRate_Valid(t *testing.T) {
	r, err := launch.NewCommissionRate("0.20")
	require.NoError(t, err)
	assert.Equal(t, "0.20", r.String())
	assert.InDelta(t, 0.20, r.Float64(), 1e-9)
}

func TestCommissionRate_LessThanOrEqual(t *testing.T) {
	low, _ := launch.NewCommissionRate("0.10")
	high, _ := launch.NewCommissionRate("0.20")
	assert.True(t, low.LessThanOrEqual(high), "0.10 should be ≤ 0.20")
	assert.False(t, high.LessThanOrEqual(low), "0.20 should not be ≤ 0.10")
	assert.True(t, low.LessThanOrEqual(low), "0.10 should be ≤ 0.10 (equal)")
}

// ---- Allowlist --------------------------------------------------------------

func TestAllowlist_EmptyContains(t *testing.T) {
	al := launch.NewAllowlist(nil)
	addr, _ := launch.NewOperatorAddress(testAddr1)
	assert.False(t, al.Contains(addr), "empty allowlist should not contain any address")
}

func TestAllowlist_Add(t *testing.T) {
	addr, _ := launch.NewOperatorAddress(testAddr1)
	al := launch.NewAllowlist(nil)
	al2 := al.Add(addr)
	assert.False(t, al.Contains(addr), "Add should return a new Allowlist; original unchanged")
	assert.True(t, al2.Contains(addr), "new allowlist should contain the added address")
}

func TestAllowlist_Remove(t *testing.T) {
	addr1, _ := launch.NewOperatorAddress(testAddr1)
	addr2, _ := launch.NewOperatorAddress(testAddr2)
	al := launch.NewAllowlist([]launch.OperatorAddress{addr1, addr2})
	al2 := al.Remove(addr1)
	assert.True(t, al.Contains(addr1), "Remove should return a new Allowlist; original unchanged")
	assert.False(t, al2.Contains(addr1), "removed address should not be in the new allowlist")
	assert.True(t, al2.Contains(addr2), "non-removed address should still be present")
}

func TestAllowlist_Len(t *testing.T) {
	addr1, _ := launch.NewOperatorAddress(testAddr1)
	addr2, _ := launch.NewOperatorAddress(testAddr2)
	al := launch.NewAllowlist([]launch.OperatorAddress{addr1, addr2})
	assert.Equal(t, 2, al.Len())
}

func TestAllowlist_Addresses_Sorted(t *testing.T) {
	addr1, _ := launch.NewOperatorAddress(testAddr1)
	addr2, _ := launch.NewOperatorAddress(testAddr2)
	addr3, _ := launch.NewOperatorAddress(testAddr3)
	al := launch.NewAllowlist([]launch.OperatorAddress{addr3, addr1, addr2})
	addrs := al.Addresses()
	require.Len(t, addrs, 3)
	for i := 1; i < len(addrs); i++ {
		assert.Less(t, addrs[i-1].String(), addrs[i].String(), "addresses not sorted")
	}
}

func TestAllowlist_AddIdempotent(t *testing.T) {
	addr, _ := launch.NewOperatorAddress(testAddr1)
	al := launch.NewAllowlist([]launch.OperatorAddress{addr})
	al2 := al.Add(addr) // add again
	assert.Equal(t, 1, al2.Len(), "add should be idempotent")
}
