package launch_test

import (
	"testing"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// ---- OperatorAddress --------------------------------------------------------

func TestNewOperatorAddress_Empty(t *testing.T) {
	_, err := launch.NewOperatorAddress("")
	if err == nil {
		t.Error("expected error for empty string")
	}
}

func TestNewOperatorAddress_InvalidBech32(t *testing.T) {
	_, err := launch.NewOperatorAddress("not-a-bech32-string")
	if err == nil {
		t.Error("expected error for invalid bech32")
	}
}

func TestNewOperatorAddress_Valid(t *testing.T) {
	addr, err := launch.NewOperatorAddress(testAddr1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if addr.String() != testAddr1 {
		t.Errorf("want %s, got %s", testAddr1, addr.String())
	}
}

func TestOperatorAddress_Equal(t *testing.T) {
	a, _ := launch.NewOperatorAddress(testAddr1)
	b, _ := launch.NewOperatorAddress(testAddr1)
	c, _ := launch.NewOperatorAddress(testAddr2)
	if !a.Equal(b) {
		t.Error("same address should be equal")
	}
	if a.Equal(c) {
		t.Error("different addresses should not be equal")
	}
}

// ---- GenesisHash ------------------------------------------------------------

func TestNewGenesisHash_WrongLength(t *testing.T) {
	_, err := launch.NewGenesisHash("abc123") // too short
	if err == nil {
		t.Error("expected error for wrong length")
	}
}

func TestNewGenesisHash_InvalidHex(t *testing.T) {
	// 64 chars but not valid hex (contains 'g')
	_, err := launch.NewGenesisHash("gggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggg")
	if err == nil {
		t.Error("expected error for invalid hex")
	}
}

func TestNewGenesisHash_Valid(t *testing.T) {
	hash := "a665a45920422f9d417e4867efdc4fb8a04a1f3fff1fa07e998e86f7f7a27ae3"
	h, err := launch.NewGenesisHash(hash)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.String() != hash {
		t.Errorf("want %s, got %s", hash, h.String())
	}
}

func TestNewGenesisHash_CaseNormalized(t *testing.T) {
	lower := "a665a45920422f9d417e4867efdc4fb8a04a1f3fff1fa07e998e86f7f7a27ae3"
	upper := "A665A45920422F9D417E4867EFDC4FB8A04A1F3FFF1FA07E998E86F7F7A27AE3"
	h, err := launch.NewGenesisHash(upper)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.String() != lower {
		t.Errorf("want lowercase %s, got %s", lower, h.String())
	}
}

// ---- PeerAddress ------------------------------------------------------------

func TestNewPeerAddress_NoAt(t *testing.T) {
	_, err := launch.NewPeerAddress("1234567890abcdef1234567890abcdef12345678127.0.0.1:26656")
	if err == nil {
		t.Error("expected error: missing @")
	}
}

func TestNewPeerAddress_InvalidNodeID_NonHex(t *testing.T) {
	_, err := launch.NewPeerAddress("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx@127.0.0.1:26656")
	if err == nil {
		t.Error("expected error: node_id not hex")
	}
}

func TestNewPeerAddress_InvalidNodeID_WrongLength(t *testing.T) {
	_, err := launch.NewPeerAddress("abc123@127.0.0.1:26656") // too short
	if err == nil {
		t.Error("expected error: node_id wrong length")
	}
}

func TestNewPeerAddress_MissingPort(t *testing.T) {
	_, err := launch.NewPeerAddress("1234567890abcdef1234567890abcdef12345678@127.0.0.1")
	if err == nil {
		t.Error("expected error: missing port")
	}
}

func TestNewPeerAddress_InvalidPort(t *testing.T) {
	_, err := launch.NewPeerAddress("1234567890abcdef1234567890abcdef12345678@127.0.0.1:99999")
	if err == nil {
		t.Error("expected error: port out of range")
	}
}

func TestNewPeerAddress_Valid(t *testing.T) {
	s := "1234567890abcdef1234567890abcdef12345678@127.0.0.1:26656"
	p, err := launch.NewPeerAddress(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.String() != s {
		t.Errorf("want %s, got %s", s, p.String())
	}
}

// ---- RPCEndpoint ------------------------------------------------------------

func TestNewRPCEndpoint_InvalidURL(t *testing.T) {
	_, err := launch.NewRPCEndpoint("not a url")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestNewRPCEndpoint_WrongScheme(t *testing.T) {
	_, err := launch.NewRPCEndpoint("ftp://localhost:26657")
	if err == nil {
		t.Error("expected error for ftp:// scheme")
	}
}

func TestNewRPCEndpoint_MissingHost(t *testing.T) {
	_, err := launch.NewRPCEndpoint("http:///path")
	if err == nil {
		t.Error("expected error for missing host")
	}
}

func TestNewRPCEndpoint_HTTPValid(t *testing.T) {
	s := "http://localhost:26657"
	e, err := launch.NewRPCEndpoint(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.String() != s {
		t.Errorf("want %s, got %s", s, e.String())
	}
}

func TestNewRPCEndpoint_HTTPSValid(t *testing.T) {
	_, err := launch.NewRPCEndpoint("https://rpc.example.com:443")
	if err != nil {
		t.Errorf("unexpected error for https: %v", err)
	}
}

// ---- Signature --------------------------------------------------------------

func TestNewSignature_Empty(t *testing.T) {
	_, err := launch.NewSignature("")
	if err == nil {
		t.Error("expected error for empty signature")
	}
}

func TestNewSignature_InvalidBase64(t *testing.T) {
	_, err := launch.NewSignature("!!!not base64!!!")
	if err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestNewSignature_WrongLength(t *testing.T) {
	// Valid base64 but only 4 bytes (not 64).
	_, err := launch.NewSignature("AAAA")
	if err == nil {
		t.Error("expected error: decoded length ≠ 64 bytes")
	}
}

func TestNewSignature_Valid(t *testing.T) {
	// 86 'A' chars + "==" = 88 base64 chars → decodes to exactly 64 bytes.
	// (validSig() has 88 unpadded A's which decodes to 66 bytes — unusable here.)
	s := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="
	sig, err := launch.NewSignature(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sig.String() != s {
		t.Errorf("want %s, got %s", s, sig.String())
	}
}

// ---- CommissionRate ---------------------------------------------------------

func TestNewCommissionRate_NotDecimal(t *testing.T) {
	_, err := launch.NewCommissionRate("abc")
	if err == nil {
		t.Error("expected error for non-decimal")
	}
}

func TestNewCommissionRate_Negative(t *testing.T) {
	_, err := launch.NewCommissionRate("-0.01")
	if err == nil {
		t.Error("expected error for negative rate")
	}
}

func TestNewCommissionRate_GreaterThanOne(t *testing.T) {
	_, err := launch.NewCommissionRate("1.01")
	if err == nil {
		t.Error("expected error for rate > 1.0")
	}
}

func TestNewCommissionRate_Zero(t *testing.T) {
	r, err := launch.NewCommissionRate("0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Float64() != 0 {
		t.Errorf("want 0, got %f", r.Float64())
	}
}

func TestNewCommissionRate_One(t *testing.T) {
	_, err := launch.NewCommissionRate("1.0")
	if err != nil {
		t.Errorf("unexpected error for rate 1.0: %v", err)
	}
}

func TestNewCommissionRate_Valid(t *testing.T) {
	r, err := launch.NewCommissionRate("0.20")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.String() != "0.20" {
		t.Errorf("want 0.20, got %s", r.String())
	}
	if r.Float64() != 0.20 {
		t.Errorf("want 0.20 float, got %f", r.Float64())
	}
}

func TestCommissionRate_LessThanOrEqual(t *testing.T) {
	low, _ := launch.NewCommissionRate("0.10")
	high, _ := launch.NewCommissionRate("0.20")
	if !low.LessThanOrEqual(high) {
		t.Error("0.10 should be ≤ 0.20")
	}
	if high.LessThanOrEqual(low) {
		t.Error("0.20 should not be ≤ 0.10")
	}
	if !low.LessThanOrEqual(low) {
		t.Error("0.10 should be ≤ 0.10 (equal)")
	}
}

// ---- Allowlist --------------------------------------------------------------

func TestAllowlist_EmptyContains(t *testing.T) {
	al := launch.NewAllowlist(nil)
	addr, _ := launch.NewOperatorAddress(testAddr1)
	if al.Contains(addr) {
		t.Error("empty allowlist should not contain any address")
	}
}

func TestAllowlist_Add(t *testing.T) {
	addr, _ := launch.NewOperatorAddress(testAddr1)
	al := launch.NewAllowlist(nil)
	al2 := al.Add(addr)
	if al.Contains(addr) {
		t.Error("Add should return a new Allowlist; original should be unchanged")
	}
	if !al2.Contains(addr) {
		t.Error("new allowlist should contain the added address")
	}
}

func TestAllowlist_Remove(t *testing.T) {
	addr1, _ := launch.NewOperatorAddress(testAddr1)
	addr2, _ := launch.NewOperatorAddress(testAddr2)
	al := launch.NewAllowlist([]launch.OperatorAddress{addr1, addr2})
	al2 := al.Remove(addr1)
	if !al.Contains(addr1) {
		t.Error("Remove should return a new Allowlist; original should be unchanged")
	}
	if al2.Contains(addr1) {
		t.Error("removed address should not be in the new allowlist")
	}
	if !al2.Contains(addr2) {
		t.Error("non-removed address should still be present")
	}
}

func TestAllowlist_Len(t *testing.T) {
	addr1, _ := launch.NewOperatorAddress(testAddr1)
	addr2, _ := launch.NewOperatorAddress(testAddr2)
	al := launch.NewAllowlist([]launch.OperatorAddress{addr1, addr2})
	if al.Len() != 2 {
		t.Errorf("want 2, got %d", al.Len())
	}
}

func TestAllowlist_Addresses_Sorted(t *testing.T) {
	addr1, _ := launch.NewOperatorAddress(testAddr1)
	addr2, _ := launch.NewOperatorAddress(testAddr2)
	addr3, _ := launch.NewOperatorAddress(testAddr3)
	al := launch.NewAllowlist([]launch.OperatorAddress{addr3, addr1, addr2})
	addrs := al.Addresses()
	if len(addrs) != 3 {
		t.Fatalf("want 3 addresses, got %d", len(addrs))
	}
	for i := 1; i < len(addrs); i++ {
		if addrs[i-1].String() >= addrs[i].String() {
			t.Errorf("addresses not sorted: %s >= %s", addrs[i-1].String(), addrs[i].String())
		}
	}
}

func TestAllowlist_AddIdempotent(t *testing.T) {
	addr, _ := launch.NewOperatorAddress(testAddr1)
	al := launch.NewAllowlist([]launch.OperatorAddress{addr})
	al2 := al.Add(addr) // add again
	if al2.Len() != 1 {
		t.Errorf("want 1 (idempotent add), got %d", al2.Len())
	}
}
