package proposal

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// Payload types for each action. These are serialized to canonical JSON and stored
// in the Proposal.Payload field.

type ApproveValidatorPayload struct {
	JoinRequestID   uuid.UUID `json:"join_request_id"`
	OperatorAddress string    `json:"operator_address"`
}

type RejectValidatorPayload struct {
	JoinRequestID   uuid.UUID `json:"join_request_id"`
	OperatorAddress string    `json:"operator_address"`
	Reason          string    `json:"reason"`
}

type RemoveApprovedValidatorPayload struct {
	JoinRequestID   uuid.UUID `json:"join_request_id"`
	OperatorAddress string    `json:"operator_address"`
	Reason          string    `json:"reason"`
}

// ApproveAllocationFilePayload approves the curated allocation file of the given type.
// Hash binds the approval to the file's content (sha256 hex); if the file is re-uploaded
// with a different hash, this approval no longer applies (the file resets to PENDING).
type ApproveAllocationFilePayload struct {
	Type string `json:"type"` // one of the fixed launch.AllocationType values
	Hash string `json:"hash"` // sha256 hex of the approved file contents
}

// PublishChainRecordPayload carries the initial genesis hash that the committee is
// attesting to. When the proposal executes, the server verifies this hash matches
// the one stored on the launch (uploaded via POST /launch/:id/genesis?type=initial).
type PublishChainRecordPayload struct {
	InitialGenesisHash string `json:"initial_genesis_sha256"`
}

type CloseApplicationWindowPayload struct {
	// No additional fields; the launch_id on the proposal is sufficient.
}

// ReviseGenesisPayload carries no fields. Presence of the proposal itself is sufficient
// to authorize reopening the launch for a corrected genesis file upload.
type ReviseGenesisPayload struct{}

// CancelLaunchPayload carries no fields — the launch_id on the proposal is sufficient. Used for
// the M-of-N cancel path required once a launch is past PUBLISHED (WINDOW_OPEN and later).
type CancelLaunchPayload struct{}

type PublishGenesisPayload struct {
	GenesisHash string `json:"genesis_hash"`
}

type UpdateGenesisTimePayload struct {
	NewGenesisTime  time.Time `json:"new_genesis_time"`
	PrevGenesisTime time.Time `json:"prev_genesis_time"`
}

type ReplaceCommitteeMemberPayload struct {
	OldAddress string `json:"old_address"`
	NewAddress string `json:"new_address"`
	NewMoniker string `json:"new_moniker"`
	NewPubKey  string `json:"new_pubkey_base64"`
}

// CommitteeMemberSpec describes a new committee member in expand/replace payloads.
type CommitteeMemberSpec struct {
	Address   string `json:"address"`
	Moniker   string `json:"moniker"`
	PubKeyB64 string `json:"pubkey_base64"`
}

// ExpandCommitteePayload adds a new member to the committee.
// NewThresholdM is optional; if nil the current M is preserved.
type ExpandCommitteePayload struct {
	NewMember     CommitteeMemberSpec `json:"new_member"`
	NewThresholdM *int                `json:"new_threshold_m,omitempty"`
}

// ShrinkCommitteePayload removes an existing member from the committee.
// NewThresholdM is optional; if nil a safe default is applied (see service layer).
type ShrinkCommitteePayload struct {
	RemoveAddress string `json:"remove_address"`
	NewThresholdM *int   `json:"new_threshold_m,omitempty"`
}

// ValidatePayload checks that the payload is valid JSON and contains the required
// fields for the given action type. Called at proposal creation time so that
// malformed payloads are rejected before any signature is collected.
func ValidatePayload(actionType ActionType, payload []byte) error {
	switch actionType {
	case ActionApproveValidator:
		return validateApproveValidatorPayload(actionType, payload)
	case ActionRejectValidator, ActionRemoveApprovedValidator:
		return validateRejectValidatorPayload(actionType, payload)
	case ActionPublishGenesis:
		return validatePublishGenesisPayload(actionType, payload)
	case ActionUpdateGenesisTime:
		return validateUpdateGenesisTimePayload(actionType, payload)
	case ActionApproveAllocationFile:
		return validateApproveAllocationFilePayload(actionType, payload)
	case ActionReplaceCommitteeMember:
		return validateReplaceCommitteeMemberPayload(actionType, payload)
	case ActionPublishChainRecord:
		return validatePublishChainRecordPayload(actionType, payload)
	case ActionExpandCommittee:
		return validateExpandCommitteePayload(actionType, payload)
	case ActionShrinkCommittee:
		return validateShrinkCommitteePayload(actionType, payload)
	case ActionCloseApplicationWindow, ActionReviseGenesis, ActionCancelLaunch:
		return nil // No fields required.
	default:
		return fmt.Errorf("unknown action type %q", actionType)
	}
}

func validateApproveValidatorPayload(actionType ActionType, payload []byte) error {
	var p ApproveValidatorPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("payload for %s: %w", actionType, err)
	}
	if p.JoinRequestID == uuid.Nil {
		return fmt.Errorf("payload for %s: join_request_id is required", actionType)
	}
	if p.OperatorAddress == "" {
		return fmt.Errorf("payload for %s: operator_address is required", actionType)
	}
	return nil
}

func validateRejectValidatorPayload(actionType ActionType, payload []byte) error {
	var p RejectValidatorPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("payload for %s: %w", actionType, err)
	}
	if p.JoinRequestID == uuid.Nil {
		return fmt.Errorf("payload for %s: join_request_id is required", actionType)
	}
	return nil
}

func validatePublishGenesisPayload(actionType ActionType, payload []byte) error {
	var p PublishGenesisPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("payload for %s: %w", actionType, err)
	}
	if p.GenesisHash == "" {
		return fmt.Errorf("payload for %s: genesis_hash is required", actionType)
	}
	return nil
}

func validateUpdateGenesisTimePayload(actionType ActionType, payload []byte) error {
	var p UpdateGenesisTimePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("payload for %s: %w", actionType, err)
	}
	if p.NewGenesisTime.IsZero() {
		return fmt.Errorf("payload for %s: new_genesis_time is required", actionType)
	}
	return nil
}

func validateApproveAllocationFilePayload(actionType ActionType, payload []byte) error {
	var p ApproveAllocationFilePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("payload for %s: %w", actionType, err)
	}
	if !launch.ValidAllocationType(launch.AllocationType(p.Type)) {
		return fmt.Errorf("payload for %s: invalid allocation type %q", actionType, p.Type)
	}
	if p.Hash == "" {
		return fmt.Errorf("payload for %s: hash is required", actionType)
	}
	return nil
}

func validateReplaceCommitteeMemberPayload(actionType ActionType, payload []byte) error {
	var p ReplaceCommitteeMemberPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("payload for %s: %w", actionType, err)
	}
	if p.OldAddress == "" || p.NewAddress == "" {
		return fmt.Errorf("payload for %s: old_address and new_address are required", actionType)
	}
	return nil
}

func validatePublishChainRecordPayload(actionType ActionType, payload []byte) error {
	var p PublishChainRecordPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("payload for %s: %w", actionType, err)
	}
	if p.InitialGenesisHash == "" {
		return fmt.Errorf("payload for %s: initial_genesis_sha256 is required", actionType)
	}
	return nil
}

func validateExpandCommitteePayload(actionType ActionType, payload []byte) error {
	var p ExpandCommitteePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("payload for %s: %w", actionType, err)
	}
	if p.NewMember.Address == "" {
		return fmt.Errorf("payload for %s: new_member.address is required", actionType)
	}
	if p.NewMember.Moniker == "" {
		return fmt.Errorf("payload for %s: new_member.moniker is required", actionType)
	}
	if p.NewMember.PubKeyB64 == "" {
		return fmt.Errorf("payload for %s: new_member.pubkey_base64 is required", actionType)
	}
	return nil
}

func validateShrinkCommitteePayload(actionType ActionType, payload []byte) error {
	var p ShrinkCommitteePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("payload for %s: %w", actionType, err)
	}
	if p.RemoveAddress == "" {
		return fmt.Errorf("payload for %s: remove_address is required", actionType)
	}
	return nil
}

// --- payload extraction helpers used by emitExecutionEvents ---
// These are called only after ValidatePayload has passed at creation time,
// so unmarshal errors here indicate a programming error and panic.

func extractValidatorFields(payload []byte) (joinRequestID uuid.UUID, operatorAddress string) {
	var p ApproveValidatorPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		panic(fmt.Sprintf("extractValidatorFields: payload failed to unmarshal after prior validation: %v", err))
	}
	return p.JoinRequestID, p.OperatorAddress
}

func extractValidatorRejectFields(payload []byte) (joinRequestID uuid.UUID, reason string) {
	var p RejectValidatorPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		panic(fmt.Sprintf("extractValidatorRejectFields: payload failed to unmarshal after prior validation: %v", err))
	}
	return p.JoinRequestID, p.Reason
}

func extractInitialGenesisHash(payload []byte) string {
	var p PublishChainRecordPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		panic(fmt.Sprintf("extractInitialGenesisHash: payload failed to unmarshal after prior validation: %v", err))
	}
	return p.InitialGenesisHash
}

func extractGenesisHash(payload []byte) string {
	var p PublishGenesisPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		panic(fmt.Sprintf("extractGenesisHash: payload failed to unmarshal after prior validation: %v", err))
	}
	return p.GenesisHash
}

func extractGenesisTimes(payload []byte) (newGenesisTime, prevGenesisTime time.Time) {
	var p UpdateGenesisTimePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		panic(fmt.Sprintf("extractGenesisTimes: payload failed to unmarshal after prior validation: %v", err))
	}
	return p.NewGenesisTime, p.PrevGenesisTime
}

func extractAllocationFields(payload []byte) (allocationType, hash string) {
	var p ApproveAllocationFilePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		panic(fmt.Sprintf("extractAllocationFields: payload failed to unmarshal after prior validation: %v", err))
	}
	return p.Type, p.Hash
}
