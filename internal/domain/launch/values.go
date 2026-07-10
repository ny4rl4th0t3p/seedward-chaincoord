package launch

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cosmos/btcutil/bech32"
)

const (
	sha256HexLen    = 64 // hex-encoded SHA-256 digest length in characters
	secp256k1SigLen = 64 // secp256k1 compact signature length in bytes
)

// Sentinel errors for AccountID parsing, so callers (auth → 400) and tests can
// distinguish the cases with errors.Is.
var (
	ErrAccountIDEmpty    = errors.New("account id: empty")
	ErrAccountIDInvalid  = errors.New("account id: invalid bech32 account address")
	ErrNotAccountAddress = errors.New("account id: validator-entity address (valoper/valcons) is not an account")
)

// AccountID is the HRP-independent identity of a Cosmos SDK account: the decoded
// account bytes (ripemd160(sha256(pubkey)) for a secp256k1 wallet), independent of
// the bech32 human-readable prefix. cosmos1<h>, osmo1<h>, and a launch's own
// network1<h> all decode to the SAME account and are therefore the SAME AccountID —
// equality and map-keying are on the account bytes, never the display string.
//
// Only *account*-form addresses are accepted; validator-entity forms (…valoper…,
// …valcons…) are network-bound and rejected — they are never an account identity.
type AccountID struct {
	acct string // lowercase hex of the decoded account bytes — the canonical identity
	orig string // the bech32 as provided, for display; empty when built without one
}

func NewAccountID(s string) (AccountID, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return AccountID{}, ErrAccountIDEmpty
	}
	// Decode validates the checksum, HRP, and base32 data encoding. The limit of
	// 1023 is the maximum bech32 length per BIP-0173; Cosmos addresses are well under.
	hrp, data5, err := bech32.Decode(s, 1023)
	if err != nil {
		return AccountID{}, fmt.Errorf("%w %q: %w", ErrAccountIDInvalid, s, err)
	}
	if hrp == "" {
		return AccountID{}, fmt.Errorf("%w: missing human-readable prefix in %q", ErrAccountIDInvalid, s)
	}
	// An account HRP is the bare chain prefix (cosmos, osmo, a launch's network);
	// valoper/valcons forms are network-bound validator entities, never an account.
	if strings.HasSuffix(hrp, "valoper") || strings.HasSuffix(hrp, "valcons") {
		return AccountID{}, fmt.Errorf("%w: %q", ErrNotAccountAddress, s)
	}
	acctBytes, err := bech32.ConvertBits(data5, 5, 8, false)
	if err != nil {
		return AccountID{}, fmt.Errorf("%w %q: cannot decode account bytes: %w", ErrAccountIDInvalid, s, err)
	}
	return AccountID{acct: hex.EncodeToString(acctBytes), orig: s}, nil
}

// String returns the address for display: the bech32 form as provided, falling
// back to the canonical hex account when built without one. It is NOT a stable
// identity — use Hex()/Equal for that, and Bech32(hrp) to render under a prefix.
func (a AccountID) String() string {
	if a.orig != "" {
		return a.orig
	}
	return a.acct
}

// Hex is the canonical, HRP-independent account identity (lowercase hex).
func (a AccountID) Hex() string { return a.acct }

// Equal reports whether two AccountIDs are the same account, ignoring HRP.
func (a AccountID) Equal(other AccountID) bool { return a.acct == other.acct }

// IsZero reports whether the AccountID is unset.
func (a AccountID) IsZero() bool { return a.acct == "" }

// Bech32 renders the account under the given human-readable prefix.
func (a AccountID) Bech32(hrp string) (string, error) {
	b, err := hex.DecodeString(a.acct)
	if err != nil {
		return "", fmt.Errorf("account id: corrupt account hex %q: %w", a.acct, err)
	}
	data5, err := bech32.ConvertBits(b, 8, 5, true)
	if err != nil {
		return "", fmt.Errorf("account id: cannot convert account bytes: %w", err)
	}
	return bech32.Encode(hrp, data5)
}

// MustNewAccountID creates an AccountID and panics if invalid.
// Use only in tests and package-level initialisers.
func MustNewAccountID(s string) AccountID {
	a, err := NewAccountID(s)
	if err != nil {
		panic(err)
	}
	return a
}

// GenesisHash is a validated SHA256 hash in lowercase hex.
type GenesisHash struct {
	value string
}

func NewGenesisHash(s string) (GenesisHash, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) != sha256HexLen {
		return GenesisHash{}, fmt.Errorf("genesis hash: must be 64 hex chars, got %d", len(s))
	}
	if _, err := hex.DecodeString(s); err != nil {
		return GenesisHash{}, fmt.Errorf("genesis hash: not valid hex: %w", err)
	}
	return GenesisHash{value: s}, nil
}

func (h GenesisHash) String() string { return h.value }
func (h GenesisHash) Equal(other GenesisHash) bool {
	return h.value == other.value
}

var nodeIDPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

// PeerAddress is a validated <node_id>@<ip>:<port> address.
type PeerAddress struct {
	value string
}

func NewPeerAddress(s string) (PeerAddress, error) {
	parts := strings.SplitN(s, "@", 2)
	if len(parts) != 2 {
		return PeerAddress{}, fmt.Errorf("peer address: must be <node_id>@<host>:<port>, got %q", s)
	}
	nodeID, hostPort := parts[0], parts[1]
	if !nodeIDPattern.MatchString(nodeID) {
		return PeerAddress{}, fmt.Errorf("peer address: node_id must be 40 hex chars, got %q", nodeID)
	}
	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		return PeerAddress{}, fmt.Errorf("peer address: invalid host:port %q: %w", hostPort, err)
	}
	if host == "" {
		return PeerAddress{}, fmt.Errorf("peer address: host is empty")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return PeerAddress{}, fmt.Errorf("peer address: invalid port %q", portStr)
	}
	return PeerAddress{value: s}, nil
}

func (p PeerAddress) String() string { return p.value }

// RPCEndpoint is a validated https URL for a node's RPC interface.
type RPCEndpoint struct {
	value string
}

func NewRPCEndpoint(s string) (RPCEndpoint, error) {
	if s == "" {
		return RPCEndpoint{}, fmt.Errorf("rpc endpoint: empty")
	}
	u, err := url.ParseRequestURI(s)
	if err != nil {
		return RPCEndpoint{}, fmt.Errorf("rpc endpoint: invalid URL %q: %w", s, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return RPCEndpoint{}, fmt.Errorf("rpc endpoint: scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return RPCEndpoint{}, fmt.Errorf("rpc endpoint: missing host")
	}
	return RPCEndpoint{value: s}, nil
}

func (r RPCEndpoint) String() string { return r.value }

// Signature is a base64-encoded secp256k1 compact signature (r‖s, 64 bytes).
type Signature struct {
	value string
}

func NewSignature(s string) (Signature, error) {
	if s == "" {
		return Signature{}, fmt.Errorf("signature: empty")
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return Signature{}, fmt.Errorf("signature: not valid base64: %w", err)
	}
	if len(b) != secp256k1SigLen {
		return Signature{}, fmt.Errorf("signature: secp256k1 compact signature must be 64 bytes, got %d", len(b))
	}
	return Signature{value: s}, nil
}

func (s Signature) String() string { return s.value }

// CommissionRate is a validated decimal in the range [0.00, 1.00].
type CommissionRate struct {
	value string // stored as string to preserve precision
	f     float64
}

func NewCommissionRate(s string) (CommissionRate, error) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return CommissionRate{}, fmt.Errorf("commission rate: not a valid decimal %q: %w", s, err)
	}
	if f < 0 || f > 1 {
		return CommissionRate{}, fmt.Errorf("commission rate: must be between 0.00 and 1.00, got %v", f)
	}
	return CommissionRate{value: s, f: f}, nil
}

func (c CommissionRate) String() string   { return c.value }
func (c CommissionRate) Float64() float64 { return c.f }
func (c CommissionRate) LessThanOrEqual(other CommissionRate) bool {
	return c.f <= other.f
}

// Member is an entry in a launch's members list: a hot actor address permitted to see
// and submit to the launch, with a label pointing to the committee's off-band
// verification of who holds it. AddedBy/AddedAt record the committee member who added
// the entry and when; both are zero for entries created before provenance was tracked
// (e.g. the address-only create/patch path).
type Member struct {
	Address AccountID
	Label   string
	AddedBy string
	AddedAt time.Time
}

// memberMeta is the per-account value stored in an Allowlist: everything about a member
// except the account itself (which is the map key). display is the member's bech32 as
// added, kept for rendering.
type memberMeta struct {
	display string
	label   string
	addedBy string
	addedAt time.Time
}

// Allowlist is an immutable set of member accounts, keyed on the HRP-independent
// AccountID (so a member added under one prefix is recognized under any), each carrying
// a label and add-provenance. The zero value is an empty (open) allowlist.
type Allowlist struct {
	members map[string]memberMeta // account hex → metadata
}

// NewAllowlist builds an Allowlist from bare accounts, each with empty metadata.
func NewAllowlist(addresses []AccountID) Allowlist {
	m := make(map[string]memberMeta, len(addresses))
	for _, a := range addresses {
		m[a.Hex()] = memberMeta{display: a.String()}
	}
	return Allowlist{members: m}
}

// NewAllowlistFromMembers builds an Allowlist from full members. A later entry for the
// same account wins, mirroring set semantics.
func NewAllowlistFromMembers(members []Member) Allowlist {
	m := make(map[string]memberMeta, len(members))
	for _, mem := range members {
		m[mem.Address.Hex()] = memberMeta{display: mem.Address.String(), label: mem.Label, addedBy: mem.AddedBy, addedAt: mem.AddedAt}
	}
	return Allowlist{members: m}
}

func (al Allowlist) Contains(addr AccountID) bool {
	_, ok := al.members[addr.Hex()]
	return ok
}

// Label returns the label for addr, or "" if addr is not a member.
func (al Allowlist) Label(addr AccountID) string {
	return al.members[addr.Hex()].label
}

// Add returns a copy with addr added, carrying empty metadata.
func (al Allowlist) Add(addr AccountID) Allowlist {
	return al.AddMember(Member{Address: addr})
}

// AddMember returns a copy with the member added, replacing any existing entry for the
// same account (label and provenance are overwritten).
func (al Allowlist) AddMember(mem Member) Allowlist {
	m := make(map[string]memberMeta, len(al.members)+1)
	for k, v := range al.members {
		m[k] = v
	}
	m[mem.Address.Hex()] = memberMeta{display: mem.Address.String(), label: mem.Label, addedBy: mem.AddedBy, addedAt: mem.AddedAt}
	return Allowlist{members: m}
}

func (al Allowlist) Remove(addr AccountID) Allowlist {
	m := make(map[string]memberMeta, len(al.members))
	for k, v := range al.members {
		m[k] = v
	}
	delete(m, addr.Hex())
	return Allowlist{members: m}
}

func (al Allowlist) Addresses() []AccountID {
	out := make([]AccountID, 0, len(al.members))
	for k, v := range al.members {
		out = append(out, AccountID{acct: k, orig: v.display})
	}
	// Sort for deterministic output — callers must not depend on insertion order.
	sort.Slice(out, func(i, j int) bool { return out[i].acct < out[j].acct })
	return out
}

// Members returns the full members, sorted by account.
func (al Allowlist) Members() []Member {
	out := make([]Member, 0, len(al.members))
	for k, v := range al.members {
		out = append(out, Member{Address: AccountID{acct: k, orig: v.display}, Label: v.label, AddedBy: v.addedBy, AddedAt: v.addedAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address.acct < out[j].Address.acct })
	return out
}

func (al Allowlist) Len() int { return len(al.members) }
