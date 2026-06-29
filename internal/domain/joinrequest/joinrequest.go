// Package joinrequest contains the JoinRequest aggregate.
package joinrequest

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// Status is the join request lifecycle state.
type Status string

const (
	StatusPending  Status = "PENDING"
	StatusApproved Status = "APPROVED"
	StatusRejected Status = "REJECTED"
	StatusExpired  Status = "EXPIRED"
)

// JoinRequest is the aggregate root for a validator's application to join a genesis.
type JoinRequest struct {
	ID       uuid.UUID
	LaunchID uuid.UUID
	// OperatorAddress is the validator operator (self-delegator) account, extracted from the
	// verified gentx — the genesis-relevant validator identity (voting power, dedup, downstream).
	OperatorAddress launch.OperatorAddress
	// SubmitterAddress is the account that signed the submission request (provenance/auth). It may
	// differ from OperatorAddress: an authorized uploader can submit a validator's gentx on its behalf.
	SubmitterAddress launch.OperatorAddress
	ConsensusPubKey  string // base64 Ed25519 consensus pubkey (validator consensus key, not operator key)
	GentxJSON        json.RawMessage
	PeerAddress      launch.PeerAddress
	RPCEndpoint      launch.RPCEndpoint
	Memo             string
	SubmittedAt      time.Time
	// Signature is the operator's secp256k1 sig over canonical JSON of this request.
	OperatorSignature launch.Signature

	Status             Status
	RejectionReason    string
	ApprovedByProposal *uuid.UUID // set when approved
}

// New creates a new JoinRequest in PENDING status. It is a pure constructor:
// gentx correctness is validated upstream by the shared gentxvalidate library
// (which also extracts the consensus pubkey passed in here), and the
// submission-window deadline is a launch-state gate enforced by the
// JoinRequestService alongside the WINDOW_OPEN check.
func New(
	id uuid.UUID,
	launchID uuid.UUID,
	operatorAddr launch.OperatorAddress, // validator operator (from the gentx)
	submitterAddr launch.OperatorAddress, // request signer
	gentxJSON json.RawMessage,
	peerAddr launch.PeerAddress,
	rpcEndpoint launch.RPCEndpoint,
	memo string,
	sig launch.Signature,
	consensusPubKeyB64 string,
	now time.Time,
) *JoinRequest {
	return &JoinRequest{
		ID:                id,
		LaunchID:          launchID,
		OperatorAddress:   operatorAddr,
		SubmitterAddress:  submitterAddr,
		ConsensusPubKey:   consensusPubKeyB64,
		GentxJSON:         gentxJSON,
		PeerAddress:       peerAddr,
		RPCEndpoint:       rpcEndpoint,
		Memo:              memo,
		SubmittedAt:       now,
		OperatorSignature: sig,
		Status:            StatusPending,
	}
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

// SelfDelegationAmount parses and returns the self-delegation amount from the gentx.
// Returns 0 if it cannot be determined. This is a read accessor over the stored
// gentx (not validation — correctness is the library's job at submission time).
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
