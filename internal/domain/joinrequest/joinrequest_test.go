package joinrequest_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/chaincoord/internal/domain/joinrequest"
	"github.com/ny4rl4th0t3p/chaincoord/internal/domain/launch"
)

// --- helpers ---

const testOperatorAddr = "cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu"

// osmosisPubKey is the real Ed25519 consensus key from the Bi23Labs Osmosis gentx (32 bytes).
const osmosisPubKey = "f5DzEhtQbnmXE/WZQsX+I8RljPdEU0u0ncVGtniFyEM="

func addr() launch.OperatorAddress {
	return launch.MustNewOperatorAddress(testOperatorAddr)
}

func peer() launch.PeerAddress {
	p, _ := launch.NewPeerAddress("abcdef1234567890abcdef1234567890abcdef12@192.168.1.1:26656")
	return p
}

func rpc() launch.RPCEndpoint {
	r, _ := launch.NewRPCEndpoint("https://192.168.1.1:26657")
	return r
}

func sig() launch.Signature {
	s, _ := launch.NewSignature("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==")
	return s
}

func baseRecord() launch.ChainRecord {
	return launch.ChainRecord{
		ChainID:               "testchain-1",
		ChainName:             "Test Chain",
		Bech32Prefix:          "cosmos", // matches testOperatorAddr prefix
		BinaryName:            "testchaind",
		BinaryVersion:         "v1.0.0",
		Denom:                 "utest",
		MinSelfDelegation:     "1000000",
		GentxDeadline:         time.Now().Add(24 * time.Hour),
		ApplicationWindowOpen: time.Now(),
		MinValidatorCount:     4,
	}
}

func mainnetRecord() launch.ChainRecord {
	r := baseRecord()
	r.MaxCommissionRate, _ = launch.NewCommissionRate("0.20")
	r.MaxCommissionChangeRate, _ = launch.NewCommissionRate("0.01")
	return r
}

// makeGentx builds a v0.50+ gentx (SIGN_MODE_DIRECT, no chain_id) matching real-world format.
// The consensus pubkey is embedded in body.messages[0].pubkey.
func makeGentx(selfDelegation int64) json.RawMessage {
	return makeGentxFull(selfDelegation, osmosisPubKey, "", "")
}

// makeGentxFull builds a gentx with optional commission rate fields and a custom pubkey.
func makeGentxFull(selfDelegation int64, consensusPubKey, commissionRate, maxChangeRate string) json.RawMessage {
	msg := map[string]any{
		"@type": "/cosmos.staking.v1beta1.MsgCreateValidator",
		"description": map[string]any{
			"moniker": "test-validator",
		},
		"pubkey": map[string]any{
			"@type": "/cosmos.crypto.ed25519.PubKey",
			"key":   consensusPubKey,
		},
		"value": map[string]any{
			"denom":  "utest",
			"amount": itoa(selfDelegation),
		},
	}
	if commissionRate != "" || maxChangeRate != "" {
		msg["commission"] = map[string]any{
			"rate":            commissionRate,
			"max_change_rate": maxChangeRate,
		}
	}
	gentx := map[string]any{
		"body": map[string]any{
			"messages": []any{msg},
			"memo":     "abcdef1234567890abcdef1234567890abcdef12@192.168.1.1:26656",
		},
		"auth_info":  map[string]any{},
		"signatures": []any{},
	}
	b, _ := json.Marshal(gentx)
	return b
}

// makeGentxWithCommission builds a gentx with optional commission rate fields.
func makeGentxWithCommission(selfDelegation int64, commissionRate, maxChangeRate string) json.RawMessage {
	return makeGentxFull(selfDelegation, osmosisPubKey, commissionRate, maxChangeRate)
}

// makeGentxWithFullCommission builds a gentx with rate, max_rate, and max_change_rate.
func makeGentxWithFullCommission(selfDelegation int64, rate, maxRate, maxChangeRate string) json.RawMessage {
	commission := map[string]any{}
	if rate != "" {
		commission["rate"] = rate
	}
	if maxRate != "" {
		commission["max_rate"] = maxRate
	}
	if maxChangeRate != "" {
		commission["max_change_rate"] = maxChangeRate
	}
	msg := map[string]any{
		"@type": "/cosmos.staking.v1beta1.MsgCreateValidator",
		"description": map[string]any{
			"moniker": "test-validator",
		},
		"pubkey": map[string]any{
			"@type": "/cosmos.crypto.ed25519.PubKey",
			"key":   osmosisPubKey,
		},
		"value": map[string]any{
			"denom":  "utest",
			"amount": itoa(selfDelegation),
		},
	}
	if len(commission) > 0 {
		msg["commission"] = commission
	}
	gentx := map[string]any{
		"body": map[string]any{
			"messages": []any{msg},
		},
		"auth_info":  map[string]any{},
		"signatures": []any{},
	}
	b, _ := json.Marshal(gentx)
	return b
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func newJR(selfDelegation int64, record launch.ChainRecord, lt launch.LaunchType) (*joinrequest.JoinRequest, error) {
	return joinrequest.New(
		uuid.New(),
		uuid.New(),
		addr(),
		makeGentx(selfDelegation),
		peer(),
		rpc(),
		"",
		sig(),
		record,
		lt,
		time.Now(),
	)
}

// --- tests ---

func TestNew_HappyPath_Testnet(t *testing.T) {
	jr, err := newJR(100, baseRecord(), launch.LaunchTypeTestnet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if jr.Status != joinrequest.StatusPending {
		t.Errorf("expected PENDING, got %s", jr.Status)
	}
	if jr.ConsensusPubKey != osmosisPubKey {
		t.Errorf("consensus pubkey not extracted from gentx")
	}
}

func TestNew_HappyPath_Mainnet_SufficientDelegation(t *testing.T) {
	_, err := newJR(1000000, baseRecord(), launch.LaunchTypeMainnet)
	if err != nil {
		t.Fatalf("unexpected error for sufficient delegation: %v", err)
	}
}

func TestNew_BelowMinSelfDelegation_Mainnet(t *testing.T) {
	_, err := newJR(500000, baseRecord(), launch.LaunchTypeMainnet)
	if err == nil {
		t.Error("expected error: self_delegation below min_self_delegation for mainnet")
	}
}

func TestNew_BelowMinSelfDelegation_IncentivizedTestnet(t *testing.T) {
	_, err := newJR(500000, baseRecord(), launch.LaunchTypeIncentivizedTestnet)
	if err == nil {
		t.Error("expected error: self_delegation below min_self_delegation for incentivized testnet")
	}
}

func TestNew_BelowMinSelfDelegation_Testnet_Allowed(t *testing.T) {
	_, err := newJR(1, baseRecord(), launch.LaunchTypeTestnet)
	if err != nil {
		t.Errorf("unexpected error for testnet with low delegation: %v", err)
	}
}

func TestNew_DeadlinePassed(t *testing.T) {
	record := baseRecord()
	record.GentxDeadline = time.Now().Add(-1 * time.Hour)
	_, err := newJR(1000000, record, launch.LaunchTypeTestnet)
	if err == nil {
		t.Error("expected error: gentx deadline passed")
	}
}

func TestNew_MalformedGentxJSON(t *testing.T) {
	_, err := joinrequest.New(
		uuid.New(), uuid.New(),
		addr(),
		json.RawMessage(`not-valid-json`),
		peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err == nil {
		t.Error("expected error: malformed gentx JSON")
	}
}

// --- consensus pubkey extraction tests ---

func TestNew_GentxConsensusPubKey_Missing(t *testing.T) {
	msg := map[string]any{
		"@type":       "/cosmos.staking.v1beta1.MsgCreateValidator",
		"description": map[string]any{"moniker": "test-validator"},
		"value":       map[string]any{"denom": "utest", "amount": "1000000"},
	}
	gentx := buildGentx(msg)
	_, err := joinrequest.New(
		uuid.New(), uuid.New(), addr(),
		gentx, peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "consensus pubkey missing") {
		t.Errorf("expected 'consensus pubkey missing' error, got: %v", err)
	}
}

func TestNew_GentxConsensusPubKey_WrongType(t *testing.T) {
	msg := map[string]any{
		"@type":       "/cosmos.staking.v1beta1.MsgCreateValidator",
		"description": map[string]any{"moniker": "test-validator"},
		"pubkey": map[string]any{
			"@type": "/cosmos.crypto.secp256k1.PubKey",
			"key":   "ArmZEcgQzVkdodLNI9VSCMsJVHTJcVpIFVdTDZk/+7qq",
		},
		"value": map[string]any{"denom": "utest", "amount": "1000000"},
	}
	gentx := buildGentx(msg)
	_, err := joinrequest.New(
		uuid.New(), uuid.New(), addr(),
		gentx, peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "ed25519.PubKey") {
		t.Errorf("expected ed25519.PubKey type error, got: %v", err)
	}
}

func TestNew_GentxConsensusPubKey_NotBase64(t *testing.T) {
	msg := map[string]any{
		"@type":       "/cosmos.staking.v1beta1.MsgCreateValidator",
		"description": map[string]any{"moniker": "test-validator"},
		"pubkey": map[string]any{
			"@type": "/cosmos.crypto.ed25519.PubKey",
			"key":   "not-valid-base64!!!",
		},
		"value": map[string]any{"denom": "utest", "amount": "1000000"},
	}
	gentx := buildGentx(msg)
	_, err := joinrequest.New(
		uuid.New(), uuid.New(), addr(),
		gentx, peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "not valid base64") {
		t.Errorf("expected 'not valid base64' error, got: %v", err)
	}
}

func TestNew_GentxConsensusPubKey_TooShort(t *testing.T) {
	// 16 bytes in base64
	msg := map[string]any{
		"@type":       "/cosmos.staking.v1beta1.MsgCreateValidator",
		"description": map[string]any{"moniker": "test-validator"},
		"pubkey": map[string]any{
			"@type": "/cosmos.crypto.ed25519.PubKey",
			"key":   "AAAAAAAAAAAAAAAAAAAAAA==",
		},
		"value": map[string]any{"denom": "utest", "amount": "1000000"},
	}
	gentx := buildGentx(msg)
	_, err := joinrequest.New(
		uuid.New(), uuid.New(), addr(),
		gentx, peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "got 16") {
		t.Errorf("expected 'got 16' error, got: %v", err)
	}
}

func TestNew_GentxConsensusPubKey_TooLong(t *testing.T) {
	// 33 bytes in base64 (secp256k1 compressed — wrong type for consensus key)
	msg := map[string]any{
		"@type":       "/cosmos.staking.v1beta1.MsgCreateValidator",
		"description": map[string]any{"moniker": "test-validator"},
		"pubkey": map[string]any{
			"@type": "/cosmos.crypto.ed25519.PubKey",
			"key":   "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		},
		"value": map[string]any{"denom": "utest", "amount": "1000000"},
	}
	gentx := buildGentx(msg)
	_, err := joinrequest.New(
		uuid.New(), uuid.New(), addr(),
		gentx, peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "got 33") {
		t.Errorf("expected 'got 33' error, got: %v", err)
	}
}

func TestNew_GentxConsensusPubKey_OsmosisRealKey(t *testing.T) {
	// The real Osmosis Bi23Labs consensus key — should pass all validation.
	jr, err := newJR(1000000, baseRecord(), launch.LaunchTypeTestnet)
	if err != nil {
		t.Fatalf("real Osmosis consensus key should be accepted: %v", err)
	}
	if jr.ConsensusPubKey != osmosisPubKey {
		t.Errorf("ConsensusPubKey not populated from gentx")
	}
}

// --- message type tests ---

func TestNew_MsgType_MissingBody(t *testing.T) {
	_, err := joinrequest.New(
		uuid.New(), uuid.New(), addr(),
		json.RawMessage(`{"auth_info":{},"signatures":[]}`),
		peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "missing body") {
		t.Errorf("expected 'missing body' error, got: %v", err)
	}
}

func TestNew_MsgType_ZeroMessages(t *testing.T) {
	gentx := json.RawMessage(`{"body":{"messages":[]}}`)
	_, err := joinrequest.New(
		uuid.New(), uuid.New(), addr(), gentx,
		peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "exactly one message") {
		t.Errorf("expected 'exactly one message' error, got: %v", err)
	}
}

func TestNew_MsgType_TwoMessages(t *testing.T) {
	msg := map[string]any{
		"@type": "/cosmos.staking.v1beta1.MsgCreateValidator",
		"pubkey": map[string]any{
			"@type": "/cosmos.crypto.ed25519.PubKey",
			"key":   osmosisPubKey,
		},
	}
	body := map[string]any{"messages": []any{msg, msg}}
	gentx, _ := json.Marshal(map[string]any{"body": body})
	_, err := joinrequest.New(
		uuid.New(), uuid.New(), addr(), gentx,
		peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "exactly one message") {
		t.Errorf("expected 'exactly one message' error, got: %v", err)
	}
}

func TestNew_MsgType_WrongType(t *testing.T) {
	msg := map[string]any{
		"@type":       "/cosmos.bank.v1beta1.MsgSend",
		"description": map[string]any{"moniker": "test"},
		"value":       map[string]any{"denom": "utest", "amount": "1000000"},
	}
	gentx := buildGentx(msg)
	_, err := joinrequest.New(
		uuid.New(), uuid.New(), addr(), gentx,
		peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "MsgCreateValidator") {
		t.Errorf("expected 'MsgCreateValidator' error, got: %v", err)
	}
}

func TestNew_MsgType_CustomNamespace(t *testing.T) {
	msg := map[string]any{
		"@type":       "/mychain.staking.v1beta1.MsgCreateValidator",
		"description": map[string]any{"moniker": "test-validator"},
		"pubkey": map[string]any{
			"@type": "/cosmos.crypto.ed25519.PubKey",
			"key":   osmosisPubKey,
		},
		"value": map[string]any{"denom": "utest", "amount": "1000000"},
	}
	gentx := buildGentx(msg)
	_, err := joinrequest.New(
		uuid.New(), uuid.New(), addr(), gentx,
		peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err != nil {
		t.Fatalf("custom namespace MsgCreateValidator should be accepted: %v", err)
	}
}

// --- operator address HRP tests ---

func TestNew_OperatorHRP_WrongPrefix(t *testing.T) {
	record := baseRecord()
	record.Bech32Prefix = "osmosis"
	_, err := newJR(1000000, record, launch.LaunchTypeTestnet)
	if err == nil || !strings.Contains(err.Error(), "bech32_prefix") {
		t.Errorf("expected bech32_prefix mismatch error, got: %v", err)
	}
}

func TestNew_OperatorHRP_CorrectPrefix(t *testing.T) {
	_, err := newJR(1000000, baseRecord(), launch.LaunchTypeTestnet)
	if err != nil {
		t.Fatalf("correct HRP should be accepted: %v", err)
	}
}

// --- bond denom tests ---

func TestNew_BondDenom_Mismatch(t *testing.T) {
	msg := map[string]any{
		"@type":       "/cosmos.staking.v1beta1.MsgCreateValidator",
		"description": map[string]any{"moniker": "test-validator"},
		"pubkey": map[string]any{
			"@type": "/cosmos.crypto.ed25519.PubKey",
			"key":   osmosisPubKey,
		},
		"value": map[string]any{"denom": "uwrong", "amount": "1000000"},
	}
	gentx := buildGentx(msg)
	_, err := joinrequest.New(
		uuid.New(), uuid.New(), addr(), gentx,
		peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "bond denom") {
		t.Errorf("expected bond denom mismatch error, got: %v", err)
	}
}

func TestNew_BondDenom_MatchV50(t *testing.T) {
	_, err := newJR(1000000, baseRecord(), launch.LaunchTypeTestnet)
	if err != nil {
		t.Fatalf("v0.50+ denom extraction should work: %v", err)
	}
}

func TestNew_BondDenom_Absent(t *testing.T) {
	msg := map[string]any{
		"@type":       "/cosmos.staking.v1beta1.MsgCreateValidator",
		"description": map[string]any{"moniker": "test-validator"},
		"pubkey": map[string]any{
			"@type": "/cosmos.crypto.ed25519.PubKey",
			"key":   osmosisPubKey,
		},
	}
	gentx := buildGentx(msg)
	_, err := joinrequest.New(
		uuid.New(), uuid.New(), addr(), gentx,
		peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "bond denom is required") {
		t.Errorf("expected 'bond denom is required' error, got: %v", err)
	}
}

// --- commission internal consistency tests ---

func TestNew_CommissionInternal_RateExceedsMaxRate(t *testing.T) {
	gentx := makeGentxWithFullCommission(1000000, "0.20", "0.10", "0.01")
	_, err := joinrequest.New(
		uuid.New(), uuid.New(), addr(), gentx,
		peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "exceeds own max_rate") {
		t.Errorf("expected 'exceeds own max_rate' error, got: %v", err)
	}
}

func TestNew_CommissionInternal_MaxChangeRateExceedsMaxRate(t *testing.T) {
	gentx := makeGentxWithFullCommission(1000000, "0.05", "0.10", "0.15")
	_, err := joinrequest.New(
		uuid.New(), uuid.New(), addr(), gentx,
		peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "exceeds own max_rate") {
		t.Errorf("expected 'exceeds own max_rate' error, got: %v", err)
	}
}

func TestNew_CommissionInternal_RateEqualsMaxRate_Passes(t *testing.T) {
	gentx := makeGentxWithFullCommission(1000000, "0.10", "0.10", "0.05")
	_, err := joinrequest.New(
		uuid.New(), uuid.New(), addr(), gentx,
		peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err != nil {
		t.Fatalf("rate == max_rate should be accepted: %v", err)
	}
}

func TestNew_CommissionInternal_NoMaxRate_SkipsConsistencyCheck(t *testing.T) {
	_, err := newJR(1000000, baseRecord(), launch.LaunchTypeTestnet)
	if err != nil {
		t.Fatalf("absent max_rate should skip internal consistency: %v", err)
	}
}

// --- commission ceiling vs. launch record tests ---

func TestNew_CommissionRate_Mainnet_AtLimit(t *testing.T) {
	_, err := newJRWithCommission("0.20", "0.01", mainnetRecord(), launch.LaunchTypeMainnet)
	if err != nil {
		t.Fatalf("unexpected error at commission limit: %v", err)
	}
}

func TestNew_CommissionRate_Mainnet_ExceedsLimit(t *testing.T) {
	_, err := newJRWithCommission("0.21", "0.01", mainnetRecord(), launch.LaunchTypeMainnet)
	if err == nil {
		t.Error("expected error: commission_rate 0.21 exceeds max 0.20")
	}
}

func TestNew_MaxChangeRate_Mainnet_ExceedsLimit(t *testing.T) {
	_, err := newJRWithCommission("0.10", "0.02", mainnetRecord(), launch.LaunchTypeMainnet)
	if err == nil {
		t.Error("expected error: max_change_rate 0.02 exceeds max 0.01")
	}
}

func TestNew_CommissionCeiling_EnforcedForAllTypes(t *testing.T) {
	types := []launch.LaunchType{
		launch.LaunchTypeTestnet,
		launch.LaunchTypeIncentivizedTestnet,
		launch.LaunchTypeMainnet,
		launch.LaunchTypePermissioned,
	}
	for _, lt := range types {
		_, err := newJRWithCommission("0.99", "0.01", mainnetRecord(), lt)
		if err == nil {
			t.Errorf("launch type %s: expected error for rate 0.99 exceeding ceiling 0.20", lt)
		}
	}
}

func TestNew_CommissionCeiling_SkippedWhenRecordHasNoCeiling(t *testing.T) {
	_, err := newJRWithCommission("0.99", "0.01", baseRecord(), launch.LaunchTypeTestnet)
	if err != nil {
		t.Errorf("unexpected error when record has no commission ceiling: %v", err)
	}
}

func TestNew_CommissionRate_Permissioned_Enforced(t *testing.T) {
	_, err := newJRWithCommission("0.21", "0.01", mainnetRecord(), launch.LaunchTypePermissioned)
	if err == nil {
		t.Error("expected error: commission_rate exceeded for permissioned launch")
	}
}

// --- moniker tests ---

func TestNew_Moniker_Empty(t *testing.T) {
	msg := map[string]any{
		"@type":       "/cosmos.staking.v1beta1.MsgCreateValidator",
		"description": map[string]any{"moniker": ""},
		"pubkey":      map[string]any{"@type": "/cosmos.crypto.ed25519.PubKey", "key": osmosisPubKey},
		"value":       map[string]any{"denom": "utest", "amount": "1000000"},
	}
	_, err := joinrequest.New(
		uuid.New(), uuid.New(), addr(), buildGentx(msg),
		peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "moniker is required") {
		t.Errorf("expected 'moniker is required' error, got: %v", err)
	}
}

func TestNew_Moniker_Absent(t *testing.T) {
	msg := map[string]any{
		"@type":  "/cosmos.staking.v1beta1.MsgCreateValidator",
		"pubkey": map[string]any{"@type": "/cosmos.crypto.ed25519.PubKey", "key": osmosisPubKey},
		"value":  map[string]any{"denom": "utest", "amount": "1000000"},
	}
	_, err := joinrequest.New(
		uuid.New(), uuid.New(), addr(), buildGentx(msg),
		peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "moniker is required") {
		t.Errorf("expected 'moniker is required' error, got: %v", err)
	}
}

func TestNew_Moniker_TooLong(t *testing.T) {
	msg := map[string]any{
		"@type":       "/cosmos.staking.v1beta1.MsgCreateValidator",
		"description": map[string]any{"moniker": strings.Repeat("a", 71)},
		"pubkey":      map[string]any{"@type": "/cosmos.crypto.ed25519.PubKey", "key": osmosisPubKey},
		"value":       map[string]any{"denom": "utest", "amount": "1000000"},
	}
	_, err := joinrequest.New(
		uuid.New(), uuid.New(), addr(), buildGentx(msg),
		peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "maximum length") {
		t.Errorf("expected 'maximum length' error, got: %v", err)
	}
}

func TestNew_Moniker_ExactlyAtLimit(t *testing.T) {
	msg := map[string]any{
		"@type":       "/cosmos.staking.v1beta1.MsgCreateValidator",
		"description": map[string]any{"moniker": strings.Repeat("a", 70)},
		"pubkey":      map[string]any{"@type": "/cosmos.crypto.ed25519.PubKey", "key": osmosisPubKey},
		"value":       map[string]any{"denom": "utest", "amount": "1000000"},
	}
	_, err := joinrequest.New(
		uuid.New(), uuid.New(), addr(), buildGentx(msg),
		peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err != nil {
		t.Fatalf("70-char moniker should be accepted: %v", err)
	}
}

func TestNew_Moniker_ControlCharacter(t *testing.T) {
	msg := map[string]any{
		"@type":       "/cosmos.staking.v1beta1.MsgCreateValidator",
		"description": map[string]any{"moniker": "valid\x01moniker"},
		"pubkey":      map[string]any{"@type": "/cosmos.crypto.ed25519.PubKey", "key": osmosisPubKey},
		"value":       map[string]any{"denom": "utest", "amount": "1000000"},
	}
	_, err := joinrequest.New(
		uuid.New(), uuid.New(), addr(), buildGentx(msg),
		peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "control characters") {
		t.Errorf("expected 'control characters' error, got: %v", err)
	}
}

// TestNew_Moniker_SpaceAllowed verifies "Bi23 Labs" style monikers (with spaces) are valid.
func TestNew_Moniker_SpaceAllowed(t *testing.T) {
	msg := map[string]any{
		"@type":       "/cosmos.staking.v1beta1.MsgCreateValidator",
		"description": map[string]any{"moniker": "Bi23 Labs"},
		"pubkey":      map[string]any{"@type": "/cosmos.crypto.ed25519.PubKey", "key": osmosisPubKey},
		"value":       map[string]any{"denom": "utest", "amount": "1000000"},
	}
	_, err := joinrequest.New(
		uuid.New(), uuid.New(), addr(), buildGentx(msg),
		peer(), rpc(), "", sig(),
		baseRecord(), launch.LaunchTypeTestnet, time.Now(),
	)
	if err != nil {
		t.Fatalf("moniker with space should be accepted (real Osmosis example): %v", err)
	}
}

// --- lifecycle tests ---

func TestApprove(t *testing.T) {
	jr, _ := newJR(1000000, baseRecord(), launch.LaunchTypeTestnet)
	propID := uuid.New()
	if err := jr.Approve(propID); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if jr.Status != joinrequest.StatusApproved {
		t.Errorf("expected APPROVED, got %s", jr.Status)
	}
	if jr.ApprovedByProposal == nil || *jr.ApprovedByProposal != propID {
		t.Error("ApprovedByProposal not set correctly")
	}
}

func TestReject(t *testing.T) {
	jr, _ := newJR(1000000, baseRecord(), launch.LaunchTypeTestnet)
	if err := jr.Reject("bad actor"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if jr.Status != joinrequest.StatusRejected {
		t.Errorf("expected REJECTED, got %s", jr.Status)
	}
	if jr.RejectionReason != "bad actor" {
		t.Errorf("unexpected rejection reason: %s", jr.RejectionReason)
	}
}

func TestExpire(t *testing.T) {
	jr, _ := newJR(1000000, baseRecord(), launch.LaunchTypeTestnet)
	if err := jr.Expire(); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if jr.Status != joinrequest.StatusExpired {
		t.Errorf("expected EXPIRED, got %s", jr.Status)
	}
}

func TestRevoke_FromApproved(t *testing.T) {
	jr, _ := newJR(1000000, baseRecord(), launch.LaunchTypeTestnet)
	_ = jr.Approve(uuid.New())
	if err := jr.Revoke("discovered bad gentx"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if jr.Status != joinrequest.StatusRejected {
		t.Errorf("expected REJECTED after revoke, got %s", jr.Status)
	}
	if jr.ApprovedByProposal != nil {
		t.Error("ApprovedByProposal should be cleared on revoke")
	}
}

func TestRevoke_CannotRevokePending(t *testing.T) {
	jr, _ := newJR(1000000, baseRecord(), launch.LaunchTypeTestnet)
	if err := jr.Revoke("reason"); err == nil {
		t.Error("expected error: cannot revoke a PENDING request")
	}
}

func TestApprove_CannotApproveTwice(t *testing.T) {
	jr, _ := newJR(1000000, baseRecord(), launch.LaunchTypeTestnet)
	_ = jr.Approve(uuid.New())
	if err := jr.Approve(uuid.New()); err == nil {
		t.Error("expected error: cannot approve an already-APPROVED request")
	}
}

func TestSelfDelegationAmount(t *testing.T) {
	jr, _ := newJR(5000000, baseRecord(), launch.LaunchTypeTestnet)
	if got := jr.SelfDelegationAmount(); got != 5000000 {
		t.Errorf("SelfDelegationAmount: got %d, want 5000000", got)
	}
}

// --- shared helpers ---

const testSelfDelegation int64 = 1000000

func newJRWithCommission(commRate, maxChangeRate string, record launch.ChainRecord, lt launch.LaunchType) (*joinrequest.JoinRequest, error) {
	return joinrequest.New(
		uuid.New(),
		uuid.New(),
		addr(),
		makeGentxWithCommission(testSelfDelegation, commRate, maxChangeRate),
		peer(),
		rpc(),
		"",
		sig(),
		record,
		lt,
		time.Now(),
	)
}

// buildGentx wraps a single message in a v0.50+ gentx envelope for test use.
func buildGentx(msg map[string]any) json.RawMessage {
	gentx, _ := json.Marshal(map[string]any{
		"body": map[string]any{
			"messages": []any{msg},
		},
		"auth_info":  map[string]any{},
		"signatures": []any{},
	})
	return gentx
}
