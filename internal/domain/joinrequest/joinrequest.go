// Package joinrequest contains the JoinRequest aggregate.
package joinrequest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cosmos/btcutil/bech32"
	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/chaincoord/internal/domain/launch"
)

// Status is the join request lifecycle state.
type Status string

const (
	StatusPending  Status = "PENDING"
	StatusApproved Status = "APPROVED"
	StatusRejected Status = "REJECTED"
	StatusExpired  Status = "EXPIRED"
)

const (
	maxMonikerLength  = 70   // Cosmos SDK default (sdk/types/staking.go)
	ed25519PubKeySize = 32   // Ed25519 public key is always 32 bytes
	asciiControlMax   = 0x20 // runes below this are ASCII control characters
)

// JoinRequest is the aggregate root for a validator's application to join a genesis.
type JoinRequest struct {
	ID              uuid.UUID
	LaunchID        uuid.UUID
	OperatorAddress launch.OperatorAddress
	ConsensusPubKey string // base64 Ed25519 consensus pubkey (validator consensus key, not operator key)
	GentxJSON       json.RawMessage
	PeerAddress     launch.PeerAddress
	RPCEndpoint     launch.RPCEndpoint
	Memo            string
	SubmittedAt     time.Time
	// Signature is the operator's secp256k1 sig over canonical JSON of this request.
	OperatorSignature launch.Signature

	Status             Status
	RejectionReason    string
	ApprovedByProposal *uuid.UUID // set when approved
}

// New creates a new JoinRequest in PENDING status and validates it against the
// provided chain record. Validation is structural only — no binary is invoked.
func New(
	id uuid.UUID,
	launchID uuid.UUID,
	operatorAddr launch.OperatorAddress,
	gentxJSON json.RawMessage,
	peerAddr launch.PeerAddress,
	rpcEndpoint launch.RPCEndpoint,
	memo string,
	sig launch.Signature,
	chainRecord launch.ChainRecord,
	launchType launch.LaunchType,
	now time.Time,
) (*JoinRequest, error) {
	jr := &JoinRequest{
		ID:                id,
		LaunchID:          launchID,
		OperatorAddress:   operatorAddr,
		GentxJSON:         gentxJSON,
		PeerAddress:       peerAddr,
		RPCEndpoint:       rpcEndpoint,
		Memo:              memo,
		SubmittedAt:       now,
		OperatorSignature: sig,
		Status:            StatusPending,
	}

	if err := jr.validate(chainRecord, launchType, now); err != nil {
		return nil, err
	}
	return jr, nil
}

// Approve marks the request as approved and records the approving proposal ID.
func (jr *JoinRequest) Approve(proposalID uuid.UUID) error {
	if jr.Status != StatusPending {
		return fmt.Errorf("join request: can only approve PENDING requests, current status: %s", jr.Status)
	}
	jr.Status = StatusApproved
	jr.ApprovedByProposal = &proposalID
	return nil
}

// Reject marks the request as rejected with a reason.
func (jr *JoinRequest) Reject(reason string) error {
	if jr.Status != StatusPending {
		return fmt.Errorf("join request: can only reject PENDING requests, current status: %s", jr.Status)
	}
	jr.Status = StatusRejected
	jr.RejectionReason = reason
	return nil
}

// Expire marks the request as expired (window closed with no decision).
func (jr *JoinRequest) Expire() error {
	if jr.Status != StatusPending {
		return fmt.Errorf("join request: can only expire PENDING requests, current status: %s", jr.Status)
	}
	jr.Status = StatusExpired
	return nil
}

// Revoke transitions an APPROVED request back to a terminal REJECTED state.
// Used by the REMOVE_APPROVED_VALIDATOR proposal flow.
func (jr *JoinRequest) Revoke(reason string) error {
	if jr.Status != StatusApproved {
		return fmt.Errorf("join request: can only revoke APPROVED requests, current status: %s", jr.Status)
	}
	jr.Status = StatusRejected
	jr.RejectionReason = reason
	jr.ApprovedByProposal = nil
	return nil
}

// validate applies the structural validation rules from spec §2.4.
// This does not call the chain binary — all checks are pure Go.
func (jr *JoinRequest) validate(record launch.ChainRecord, lt launch.LaunchType, now time.Time) error {
	// Window check.
	if now.After(record.GentxDeadline) {
		return fmt.Errorf("gentx submission deadline has passed (%s)", record.GentxDeadline.Format(time.RFC3339))
	}

	// Gentx must contain exactly one MsgCreateValidator.
	if err := validateMsgType(jr.GentxJSON); err != nil {
		return fmt.Errorf("gentx: %w", err)
	}

	// Extract consensus pubkey from gentx body.messages[0].pubkey.
	cpKey, err := extractGentxConsensusPubKey(jr.GentxJSON)
	if err != nil {
		return fmt.Errorf("gentx: %w", err)
	}
	cpBytes, err := base64.StdEncoding.DecodeString(cpKey)
	if err != nil {
		return fmt.Errorf("gentx consensus pubkey: not valid base64: %w", err)
	}
	if len(cpBytes) != ed25519PubKeySize {
		return fmt.Errorf("gentx consensus pubkey: must be 32 bytes (Ed25519), got %d", len(cpBytes))
	}
	jr.ConsensusPubKey = cpKey

	// Operator address HRP must match the chain's declared bech32 prefix.
	hrp, _, err := bech32.Decode(jr.OperatorAddress.String(), 1023)
	if err != nil {
		return fmt.Errorf("operator address: bech32 decode: %w", err)
	}
	if hrp != record.Bech32Prefix {
		return fmt.Errorf("operator address prefix %q does not match chain bech32_prefix %q",
			hrp, record.Bech32Prefix)
	}

	// Parse structural fields from the gentx JSON.
	selfDelegation, denom, err := extractGentxFields(jr.GentxJSON)
	if err != nil {
		return fmt.Errorf("gentx: %w", err)
	}

	// Bond denom is required in SDK v0.50+ gentxs and must match the chain's declared denom.
	if denom == "" {
		return fmt.Errorf("gentx: bond denom is required")
	}
	if denom != record.Denom {
		return fmt.Errorf("gentx bond denom %q does not match chain denom %q", denom, record.Denom)
	}

	// Self-delegation floor (mainnet and incentivized testnet).
	if lt == launch.LaunchTypeMainnet || lt == launch.LaunchTypeIncentivizedTestnet || lt == launch.LaunchTypePermissioned {
		if record.MinSelfDelegation != "" && selfDelegation < mustParseInt64(record.MinSelfDelegation) {
			return fmt.Errorf("self_delegation %d is below min_self_delegation %s", selfDelegation, record.MinSelfDelegation)
		}
	}

	// Moniker validation.
	moniker := extractGentxMoniker(jr.GentxJSON)
	if err := validateMoniker(moniker); err != nil {
		return fmt.Errorf("gentx: %w", err)
	}

	// Commission checks: internal consistency (all types) + ceiling vs. launch record
	// (enforced for all types when the coordinator has set a limit).
	if err := jr.validateCommission(record); err != nil {
		return err
	}

	return nil
}

// validateMsgType checks that the gentx body contains exactly one message whose @type
// has the suffix "MsgCreateValidator". Suffix matching allows chain-specific type URL
// namespaces (e.g. /mychain.staking.v1beta1.MsgCreateValidator).
func validateMsgType(gentxJSON json.RawMessage) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(gentxJSON, &raw); err != nil {
		return fmt.Errorf("not valid JSON: %w", err)
	}
	bodyRaw, ok := raw["body"]
	if !ok {
		return fmt.Errorf("missing body")
	}
	var body struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(bodyRaw, &body); err != nil {
		return fmt.Errorf("body: %w", err)
	}
	if len(body.Messages) != 1 {
		return fmt.Errorf("must contain exactly one message, got %d", len(body.Messages))
	}
	var msg struct {
		Type string `json:"@type"`
	}
	if err := json.Unmarshal(body.Messages[0], &msg); err != nil {
		return fmt.Errorf("message: %w", err)
	}
	if !strings.HasSuffix(msg.Type, "MsgCreateValidator") {
		return fmt.Errorf("message @type must be MsgCreateValidator, got %q", msg.Type)
	}
	return nil
}

// extractGentxConsensusPubKey extracts the validator consensus key from
// body.messages[0].pubkey, validates the @type is ed25519, and returns the
// base64-encoded raw key bytes. Returns an error if the field is absent or wrong type.
func extractGentxConsensusPubKey(gentxJSON json.RawMessage) (string, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(gentxJSON, &raw); err != nil {
		return "", fmt.Errorf("not valid JSON: %w", err)
	}
	bodyRaw, ok := raw["body"]
	if !ok {
		return "", fmt.Errorf("missing body")
	}
	var body struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(bodyRaw, &body); err != nil || len(body.Messages) == 0 {
		return "", fmt.Errorf("missing messages")
	}
	var msg struct {
		PubKey struct {
			Type string `json:"@type"`
			Key  string `json:"key"`
		} `json:"pubkey"`
	}
	if err := json.Unmarshal(body.Messages[0], &msg); err != nil {
		return "", fmt.Errorf("consensus pubkey: %w", err)
	}
	if msg.PubKey.Key == "" {
		return "", fmt.Errorf("consensus pubkey missing from gentx")
	}
	if !strings.HasSuffix(msg.PubKey.Type, "ed25519.PubKey") {
		return "", fmt.Errorf("consensus pubkey @type must be ed25519.PubKey, got %q", msg.PubKey.Type)
	}
	return msg.PubKey.Key, nil
}

// validateMoniker checks that the moniker is non-empty, within the Cosmos SDK length
// limit, and contains no ASCII control characters.
func validateMoniker(moniker string) error {
	if moniker == "" {
		return fmt.Errorf("moniker is required")
	}
	if len([]rune(moniker)) > maxMonikerLength {
		return fmt.Errorf("moniker exceeds maximum length of %d characters", maxMonikerLength)
	}
	for _, r := range moniker {
		if r < asciiControlMax {
			return fmt.Errorf("moniker contains control characters")
		}
	}
	return nil
}

func (jr *JoinRequest) validateCommission(record launch.ChainRecord) error {
	commission, err := extractGentxCommission(jr.GentxJSON)
	if err != nil {
		return fmt.Errorf("gentx commission: %w", err)
	}

	// Internal consistency: commission.rate must not exceed commission.max_rate;
	// commission.max_change_rate must not exceed commission.max_rate.
	// These are intra-gentx validity rules — no launch record is involved.
	if commission.MaxRate != "" {
		maxRate, err := launch.NewCommissionRate(commission.MaxRate)
		if err != nil {
			return fmt.Errorf("gentx commission.max_rate: %w", err)
		}
		if commission.Rate != "" {
			rate, err := launch.NewCommissionRate(commission.Rate)
			if err != nil {
				return fmt.Errorf("gentx commission.rate: %w", err)
			}
			if !rate.LessThanOrEqual(maxRate) {
				return fmt.Errorf("gentx commission_rate %s exceeds own max_rate %s",
					commission.Rate, commission.MaxRate)
			}
		}
		if commission.MaxChangeRate != "" {
			maxChange, err := launch.NewCommissionRate(commission.MaxChangeRate)
			if err != nil {
				return fmt.Errorf("gentx commission.max_change_rate: %w", err)
			}
			if !maxChange.LessThanOrEqual(maxRate) {
				return fmt.Errorf("gentx max_commission_change_rate %s exceeds own max_rate %s",
					commission.MaxChangeRate, commission.MaxRate)
			}
		}
	}

	// Ceiling checks vs. launch record — enforced for all launch types when the
	// coordinator has set a limit. record.MaxCommissionRate.String() == "" means the
	// field was never set (zero CommissionRate value), so no ceiling is imposed.
	if record.MaxCommissionRate.String() != "" && commission.Rate != "" {
		rate, err := launch.NewCommissionRate(commission.Rate)
		if err != nil {
			return fmt.Errorf("gentx commission.rate: %w", err)
		}
		if !rate.LessThanOrEqual(record.MaxCommissionRate) {
			return fmt.Errorf("gentx commission_rate %s exceeds max_commission_rate %s",
				commission.Rate, record.MaxCommissionRate)
		}
	}
	if record.MaxCommissionChangeRate.String() != "" && commission.MaxChangeRate != "" {
		maxChange, err := launch.NewCommissionRate(commission.MaxChangeRate)
		if err != nil {
			return fmt.Errorf("gentx commission.max_change_rate: %w", err)
		}
		if !maxChange.LessThanOrEqual(record.MaxCommissionChangeRate) {
			return fmt.Errorf("gentx max_commission_change_rate %s exceeds max_commission_change_rate %s",
				commission.MaxChangeRate, record.MaxCommissionChangeRate)
		}
	}
	return nil
}

type gentxCommission struct {
	Rate          string
	MaxRate       string
	MaxChangeRate string
}

// extractGentxCommission extracts commission.rate, commission.max_rate, and
// commission.max_change_rate from body.messages[0].commission of the gentx JSON.
// Returns zero-value strings if fields are not present.
func extractGentxCommission(gentxJSON json.RawMessage) (gentxCommission, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(gentxJSON, &raw); err != nil {
		return gentxCommission{}, fmt.Errorf("not valid JSON: %w", err)
	}
	bodyRaw, ok := raw["body"]
	if !ok {
		return gentxCommission{}, nil
	}
	var body struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(bodyRaw, &body); err != nil || len(body.Messages) == 0 {
		return gentxCommission{}, nil
	}
	var msg struct {
		Commission struct {
			Rate          string `json:"rate"`
			MaxRate       string `json:"max_rate"`
			MaxChangeRate string `json:"max_change_rate"`
		} `json:"commission"`
	}
	if err := json.Unmarshal(body.Messages[0], &msg); err != nil {
		return gentxCommission{}, nil
	}
	return gentxCommission{
		Rate:          msg.Commission.Rate,
		MaxRate:       msg.Commission.MaxRate,
		MaxChangeRate: msg.Commission.MaxChangeRate,
	}, nil
}

// extractGentxFields parses the gentx JSON to extract self_delegation amount and bond denom
// from body.messages[0].value (SDK v0.50+ Coin struct: {"denom":"...","amount":"<int>"}).
// Returns denom="" when the field is absent; callers must treat that as unverifiable.
func extractGentxFields(gentxJSON json.RawMessage) (selfDelegation int64, denom string, err error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(gentxJSON, &raw); err != nil {
		return 0, "", fmt.Errorf("not valid JSON: %w", err)
	}

	if bodyRaw, ok := raw["body"]; ok {
		var body struct {
			Messages []json.RawMessage `json:"messages"`
		}
		if err := json.Unmarshal(bodyRaw, &body); err == nil && len(body.Messages) > 0 {
			var msg struct {
				Value struct {
					Amount string `json:"amount"`
					Denom  string `json:"denom"`
				} `json:"value"`
			}
			if err := json.Unmarshal(body.Messages[0], &msg); err == nil {
				selfDelegation = parseAmountString(msg.Value.Amount)
				denom = msg.Value.Denom
			}
		}
	}

	return selfDelegation, denom, nil
}

// SelfDelegationAmount parses and returns the self-delegation amount from the gentx.
// Returns 0 if it cannot be determined.
func (jr *JoinRequest) SelfDelegationAmount() int64 {
	amount, _, err := extractGentxFields(jr.GentxJSON)
	if err != nil {
		return 0
	}
	return amount
}

// Moniker returns the validator moniker from the gentx description.
// Returns an empty string if it cannot be determined.
func (jr *JoinRequest) Moniker() string {
	return extractGentxMoniker(jr.GentxJSON)
}

func extractGentxMoniker(gentxJSON json.RawMessage) string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(gentxJSON, &raw); err != nil {
		return ""
	}
	bodyRaw, ok := raw["body"]
	if !ok {
		return ""
	}
	var body struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(bodyRaw, &body); err != nil || len(body.Messages) == 0 {
		return ""
	}
	var msg struct {
		Description struct {
			Moniker string `json:"moniker"`
		} `json:"description"`
	}
	if err := json.Unmarshal(body.Messages[0], &msg); err != nil {
		return ""
	}
	return msg.Description.Moniker
}

// parseAmountString extracts the integer part from a Cosmos amount string like "1000000utoken".
func parseAmountString(s string) int64 {
	var n int64
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int64(c-'0')
		} else {
			break
		}
	}
	return n
}

func mustParseInt64(s string) int64 {
	return parseAmountString(s)
}
