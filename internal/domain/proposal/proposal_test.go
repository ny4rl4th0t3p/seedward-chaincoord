package proposal_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/proposal"
)

// Valid bech32 test addresses.
const (
	testAddr1 = "cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu"
	testAddr2 = "cosmos1yy3zxfp9ycnjs2f29vkz6t30xqcnyve5j4ep6w"
	testAddr3 = "cosmos1g9pyx3z9ger5sj22fdxy6nj02pg4y5657yq8y0"
)

func newAddr(s string) launch.OperatorAddress {
	return launch.MustNewOperatorAddress(s)
}

func newSig() launch.Signature {
	s, _ := launch.NewSignature("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	return s
}

func mustPayload(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

const thresholdM = 2

// validApprovePayload returns a valid APPROVE_VALIDATOR payload.
func validApprovePayload() []byte {
	return mustPayload(proposal.ApproveValidatorPayload{
		JoinRequestID:   uuid.New(),
		OperatorAddress: testAddr1,
	})
}

func newProposal(actionType proposal.ActionType, payload []byte) *proposal.Proposal {
	p, err := proposal.New(uuid.New(), uuid.New(), actionType, payload,
		newAddr(testAddr1), newSig(), 48*time.Hour, time.Now())
	if err != nil {
		panic("newProposal: " + err.Error())
	}
	return p
}

func TestNewProposal_ProposerSignatureAdded(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())
	if p.SignCount() != 1 {
		t.Errorf("expected 1 signature after creation (proposer), got %d", p.SignCount())
	}
	if p.Status != proposal.StatusPendingSignatures {
		t.Errorf("expected PENDING_SIGNATURES, got %s", p.Status)
	}
}

func TestProposal_ReachesQuorum(t *testing.T) {
	p := newProposal(proposal.ActionCloseApplicationWindow, mustPayload(proposal.CloseApplicationWindowPayload{}))

	if err := p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now()); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if p.Status != proposal.StatusExecuted {
		t.Errorf("expected EXECUTED after quorum, got %s", p.Status)
	}
	if p.ExecutedAt == nil {
		t.Error("ExecutedAt should be set on EXECUTED proposal")
	}
}

func TestProposal_VetoImmediatelyTerminates(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())

	if err := p.Sign(newAddr(testAddr2), proposal.DecisionVeto, newSig(), thresholdM, time.Now()); err != nil {
		t.Fatalf("Sign veto: %v", err)
	}

	if p.Status != proposal.StatusVetoed {
		t.Errorf("expected VETOED after veto, got %s", p.Status)
	}
}

func TestProposal_CannotSignTwice(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())
	// proposer already signed on creation
	err := p.Sign(newAddr(testAddr1), proposal.DecisionSign, newSig(), thresholdM, time.Now())
	if err == nil {
		t.Error("expected error for duplicate signature from same coordinator")
	}
}

func TestProposal_CannotSignExecuted(t *testing.T) {
	p := newProposal(proposal.ActionCloseApplicationWindow, mustPayload(proposal.CloseApplicationWindowPayload{}))
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now())
	// now EXECUTED

	if err := p.Sign(newAddr(testAddr3), proposal.DecisionSign, newSig(), thresholdM, time.Now()); err == nil {
		t.Error("expected error: cannot sign an EXECUTED proposal")
	}
}

func TestProposal_TTLExpiry(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())

	expired := p.ExpireIfStale(time.Now().Add(49 * time.Hour))
	if !expired {
		t.Error("expected proposal to be expired")
	}
	if p.Status != proposal.StatusExpired {
		t.Errorf("expected EXPIRED, got %s", p.Status)
	}

	if err := p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now().Add(49*time.Hour)); err == nil {
		t.Error("expected error: cannot sign expired proposal")
	}
}

func TestProposal_NotExpiredYet(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())
	expired := p.ExpireIfStale(time.Now().Add(1 * time.Hour))
	if expired {
		t.Error("should not expire before TTL")
	}
}

func TestProposal_EmitsDomainEventOnExecute(t *testing.T) {
	jrID := uuid.New()
	p := newProposal(proposal.ActionApproveValidator, mustPayload(proposal.ApproveValidatorPayload{
		JoinRequestID:   jrID,
		OperatorAddress: testAddr1,
	}))

	_ = p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now())

	events := p.PopEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 domain event, got %d", len(events))
	}
	ev, ok := events[0].(domain.ValidatorApproved)
	if !ok {
		t.Fatalf("expected ValidatorApproved event, got %T", events[0])
	}
	if ev.JoinRequestID != jrID {
		t.Errorf("JoinRequestID mismatch: got %s, want %s", ev.JoinRequestID, jrID)
	}
}

func TestProposal_PopEventsClearsBuffer(t *testing.T) {
	p := newProposal(proposal.ActionCloseApplicationWindow, mustPayload(proposal.CloseApplicationWindowPayload{}))
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now())

	_ = p.PopEvents()
	second := p.PopEvents()
	if len(second) != 0 {
		t.Error("PopEvents should clear the buffer; second call should return empty")
	}
}

func TestProposal_InvalidPayload_Rejected(t *testing.T) {
	// APPROVE_VALIDATOR with missing required fields must fail at creation
	_, err := proposal.New(uuid.New(), uuid.New(),
		proposal.ActionApproveValidator,
		mustPayload(map[string]string{"irrelevant": "data"}),
		newAddr(testAddr1), newSig(), 48*time.Hour, time.Now(),
	)
	if err == nil {
		t.Error("expected error: ApproveValidator payload missing join_request_id")
	}
}

func TestProposal_ValidatePayload_UnknownAction(t *testing.T) {
	err := proposal.ValidatePayload("NONEXISTENT_ACTION", []byte(`{}`))
	if err == nil {
		t.Error("expected error for unknown action type")
	}
}

// ---- New: nil payload -------------------------------------------------------

func TestNew_NilPayload(t *testing.T) {
	_, err := proposal.New(uuid.New(), uuid.New(),
		proposal.ActionApproveValidator, nil,
		newAddr(testAddr1), newSig(), 48*time.Hour, time.Now())
	if err == nil {
		t.Error("expected error for nil payload")
	}
}

// ---- ValidatePayload: valid payloads for all action types -------------------

func TestValidatePayload_RejectValidator_Valid(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionRejectValidator, mustPayload(proposal.RejectValidatorPayload{
		JoinRequestID:   uuid.New(),
		OperatorAddress: testAddr1,
		Reason:          "bad actor",
	}))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePayload_RemoveApprovedValidator_Valid(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionRemoveApprovedValidator, mustPayload(proposal.RemoveApprovedValidatorPayload{
		JoinRequestID:   uuid.New(),
		OperatorAddress: testAddr1,
		Reason:          "slashed",
	}))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePayload_AddGenesisAccount_Valid(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionAddGenesisAccount, mustPayload(proposal.AddGenesisAccountPayload{
		Address: testAddr1,
		Amount:  "1000uatom",
	}))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePayload_ModifyGenesisAccount_Valid(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionModifyGenesisAccount, mustPayload(proposal.AddGenesisAccountPayload{
		Address: testAddr1,
		Amount:  "2000uatom",
	}))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePayload_RemoveGenesisAccount_Valid(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionRemoveGenesisAccount, mustPayload(proposal.RemoveGenesisAccountPayload{
		Address: testAddr1,
	}))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePayload_PublishGenesis_Valid(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionPublishGenesis, mustPayload(proposal.PublishGenesisPayload{
		GenesisHash: "a665a45920422f9d417e4867efdc4fb8a04a1f3fff1fa07e998e86f7f7a27ae3",
	}))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePayload_UpdateGenesisTime_Valid(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionUpdateGenesisTime, mustPayload(proposal.UpdateGenesisTimePayload{
		NewGenesisTime:  time.Now().Add(24 * time.Hour),
		PrevGenesisTime: time.Now(),
	}))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePayload_ReplaceCommitteeMember_Valid(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionReplaceCommitteeMember, mustPayload(proposal.ReplaceCommitteeMemberPayload{
		OldAddress: testAddr1,
		NewAddress: testAddr2,
		NewMoniker: "new-node",
		NewPubKey:  "AAAA",
	}))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePayload_CloseApplicationWindow_Valid(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionCloseApplicationWindow, mustPayload(proposal.CloseApplicationWindowPayload{}))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- ValidatePayload: error paths -------------------------------------------

func TestValidatePayload_ApproveValidator_MissingJoinRequestID(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionApproveValidator, mustPayload(proposal.ApproveValidatorPayload{
		OperatorAddress: testAddr1,
	}))
	if err == nil {
		t.Error("expected error: join_request_id is required")
	}
}

func TestValidatePayload_ApproveValidator_MissingOperatorAddress(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionApproveValidator, mustPayload(proposal.ApproveValidatorPayload{
		JoinRequestID: uuid.New(),
	}))
	if err == nil {
		t.Error("expected error: operator_address is required")
	}
}

func TestValidatePayload_RejectValidator_MissingJoinRequestID(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionRejectValidator, mustPayload(proposal.RejectValidatorPayload{
		OperatorAddress: testAddr1,
	}))
	if err == nil {
		t.Error("expected error: join_request_id is required")
	}
}

func TestValidatePayload_PublishGenesis_MissingHash(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionPublishGenesis, mustPayload(proposal.PublishGenesisPayload{}))
	if err == nil {
		t.Error("expected error: genesis_hash is required")
	}
}

func TestValidatePayload_UpdateGenesisTime_ZeroTime(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionUpdateGenesisTime, mustPayload(proposal.UpdateGenesisTimePayload{}))
	if err == nil {
		t.Error("expected error: new_genesis_time is required")
	}
}

func TestValidatePayload_AddGenesisAccount_MissingAddress(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionAddGenesisAccount, mustPayload(proposal.AddGenesisAccountPayload{
		Amount: "1000uatom",
	}))
	if err == nil {
		t.Error("expected error: address is required")
	}
}

func TestValidatePayload_AddGenesisAccount_MissingAmount(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionAddGenesisAccount, mustPayload(proposal.AddGenesisAccountPayload{
		Address: testAddr1,
	}))
	if err == nil {
		t.Error("expected error: amount is required")
	}
}

func TestValidatePayload_RemoveGenesisAccount_MissingAddress(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionRemoveGenesisAccount, mustPayload(proposal.RemoveGenesisAccountPayload{}))
	if err == nil {
		t.Error("expected error: address is required")
	}
}

func TestValidatePayload_ReplaceCommitteeMember_MissingAddresses(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionReplaceCommitteeMember, mustPayload(proposal.ReplaceCommitteeMemberPayload{
		NewMoniker: "node",
	}))
	if err == nil {
		t.Error("expected error: old_address and new_address are required")
	}
}

// ---- ValidatePayload: ExpandCommittee ---------------------------------------

func validExpandPayload() proposal.ExpandCommitteePayload {
	return proposal.ExpandCommitteePayload{
		NewMember: proposal.CommitteeMemberSpec{
			Address:   testAddr1,
			Moniker:   "new-node",
			PubKeyB64: "AAAA",
		},
	}
}

func TestValidatePayload_ExpandCommittee_Valid(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionExpandCommittee, mustPayload(validExpandPayload()))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePayload_ExpandCommittee_WithThreshold(t *testing.T) {
	m := 2
	p := validExpandPayload()
	p.NewThresholdM = &m
	err := proposal.ValidatePayload(proposal.ActionExpandCommittee, mustPayload(p))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePayload_ExpandCommittee_MissingAddress(t *testing.T) {
	p := validExpandPayload()
	p.NewMember.Address = ""
	err := proposal.ValidatePayload(proposal.ActionExpandCommittee, mustPayload(p))
	if err == nil {
		t.Error("expected error: new_member.address is required")
	}
}

func TestValidatePayload_ExpandCommittee_MissingMoniker(t *testing.T) {
	p := validExpandPayload()
	p.NewMember.Moniker = ""
	err := proposal.ValidatePayload(proposal.ActionExpandCommittee, mustPayload(p))
	if err == nil {
		t.Error("expected error: new_member.moniker is required")
	}
}

func TestValidatePayload_ExpandCommittee_MissingPubKey(t *testing.T) {
	p := validExpandPayload()
	p.NewMember.PubKeyB64 = ""
	err := proposal.ValidatePayload(proposal.ActionExpandCommittee, mustPayload(p))
	if err == nil {
		t.Error("expected error: new_member.pubkey_base64 is required")
	}
}

// ---- ValidatePayload: ShrinkCommittee ---------------------------------------

func TestValidatePayload_ShrinkCommittee_Valid(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionShrinkCommittee, mustPayload(proposal.ShrinkCommitteePayload{
		RemoveAddress: testAddr1,
	}))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePayload_ShrinkCommittee_WithThreshold(t *testing.T) {
	m := 1
	err := proposal.ValidatePayload(proposal.ActionShrinkCommittee, mustPayload(proposal.ShrinkCommitteePayload{
		RemoveAddress: testAddr1,
		NewThresholdM: &m,
	}))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePayload_ShrinkCommittee_MissingRemoveAddress(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionShrinkCommittee, mustPayload(proposal.ShrinkCommitteePayload{}))
	if err == nil {
		t.Error("expected error: remove_address is required")
	}
}

// ---- Sign: terminal status paths --------------------------------------------

func TestProposal_CannotSignVetoed(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionVeto, newSig(), thresholdM, time.Now())
	err := p.Sign(newAddr(testAddr3), proposal.DecisionSign, newSig(), thresholdM, time.Now())
	if err == nil {
		t.Error("expected error: cannot sign VETOED proposal")
	}
}

func TestProposal_CannotSignExpired(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())
	future := time.Now().Add(49 * time.Hour)
	_ = p.ExpireIfStale(future)
	err := p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, future)
	if err == nil {
		t.Error("expected error: cannot sign EXPIRED proposal")
	}
}

// ---- ExpireIfStale: terminal states are not re-expired ----------------------

func TestExpireIfStale_ExecutedNotChanged(t *testing.T) {
	p := newProposal(proposal.ActionCloseApplicationWindow, mustPayload(proposal.CloseApplicationWindowPayload{}))
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now())
	changed := p.ExpireIfStale(time.Now().Add(49 * time.Hour))
	if changed {
		t.Error("ExpireIfStale should not change an EXECUTED proposal")
	}
	if p.Status != proposal.StatusExecuted {
		t.Errorf("expected EXECUTED, got %s", p.Status)
	}
}

func TestExpireIfStale_VetoedNotChanged(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionVeto, newSig(), thresholdM, time.Now())
	changed := p.ExpireIfStale(time.Now().Add(49 * time.Hour))
	if changed {
		t.Error("ExpireIfStale should not change a VETOED proposal")
	}
	if p.Status != proposal.StatusVetoed {
		t.Errorf("expected VETOED, got %s", p.Status)
	}
}

// ---- CheckQuorum ------------------------------------------------------------

func TestCheckQuorum_ExecutesWhenAlreadyAtThreshold(t *testing.T) {
	p := newProposal(proposal.ActionCloseApplicationWindow, mustPayload(proposal.CloseApplicationWindowPayload{}))
	p.CheckQuorum(1, time.Now())
	if p.Status != proposal.StatusExecuted {
		t.Errorf("expected EXECUTED after CheckQuorum with M=1, got %s", p.Status)
	}
	if p.ExecutedAt == nil {
		t.Error("ExecutedAt should be set")
	}
}

func TestCheckQuorum_NoopWhenBelowThreshold(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())
	p.CheckQuorum(thresholdM, time.Now())
	if p.Status != proposal.StatusPendingSignatures {
		t.Errorf("expected PENDING_SIGNATURES, got %s", p.Status)
	}
}

func TestCheckQuorum_NoopOnNonPendingStatus(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionVeto, newSig(), thresholdM, time.Now())
	p.CheckQuorum(1, time.Now())
	if p.Status != proposal.StatusVetoed {
		t.Errorf("expected VETOED, got %s", p.Status)
	}
}

// ---- SignCount --------------------------------------------------------------

func TestSignCount_VetoNotCounted(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionVeto, newSig(), thresholdM, time.Now())
	if p.SignCount() != 1 {
		t.Errorf("want SignCount=1 (veto does not increment sign count), got %d", p.SignCount())
	}
}

// ---- PopEvents: no events ---------------------------------------------------

func TestPopEvents_EmptyOnNewProposal(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())
	events := p.PopEvents()
	if len(events) != 0 {
		t.Errorf("expected no events on fresh proposal, got %d", len(events))
	}
}

// ---- Domain events for each action type -------------------------------------

func TestProposal_EmitsDomainEvent_RejectValidator(t *testing.T) {
	jrID := uuid.New()
	p := newProposal(proposal.ActionRejectValidator, mustPayload(proposal.RejectValidatorPayload{
		JoinRequestID:   jrID,
		OperatorAddress: testAddr1,
		Reason:          "spam",
	}))
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now())

	events := p.PopEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev, ok := events[0].(domain.ValidatorRejected)
	if !ok {
		t.Fatalf("expected ValidatorRejected, got %T", events[0])
	}
	if ev.JoinRequestID != jrID {
		t.Errorf("JoinRequestID mismatch: got %s, want %s", ev.JoinRequestID, jrID)
	}
	if ev.Reason != "spam" {
		t.Errorf("Reason mismatch: got %q", ev.Reason)
	}
}

func TestProposal_EmitsDomainEvent_RemoveApprovedValidator(t *testing.T) {
	jrID := uuid.New()
	p := newProposal(proposal.ActionRemoveApprovedValidator, mustPayload(proposal.RemoveApprovedValidatorPayload{
		JoinRequestID:   jrID,
		OperatorAddress: testAddr1,
		Reason:          "offline",
	}))
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now())

	events := p.PopEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev, ok := events[0].(domain.ValidatorRemoved)
	if !ok {
		t.Fatalf("expected ValidatorRemoved, got %T", events[0])
	}
	if ev.JoinRequestID != jrID {
		t.Errorf("JoinRequestID mismatch: got %s, want %s", ev.JoinRequestID, jrID)
	}
}

func TestProposal_EmitsDomainEvent_WindowClosed(t *testing.T) {
	p := newProposal(proposal.ActionCloseApplicationWindow, mustPayload(proposal.CloseApplicationWindowPayload{}))
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now())

	events := p.PopEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if _, ok := events[0].(domain.WindowClosed); !ok {
		t.Fatalf("expected WindowClosed, got %T", events[0])
	}
}

func TestProposal_EmitsDomainEvent_GenesisPublished(t *testing.T) {
	hash := "a665a45920422f9d417e4867efdc4fb8a04a1f3fff1fa07e998e86f7f7a27ae3"
	p := newProposal(proposal.ActionPublishGenesis, mustPayload(proposal.PublishGenesisPayload{
		GenesisHash: hash,
	}))
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now())

	events := p.PopEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev, ok := events[0].(domain.GenesisPublished)
	if !ok {
		t.Fatalf("expected GenesisPublished, got %T", events[0])
	}
	if ev.GenesisHash != hash {
		t.Errorf("GenesisHash mismatch: got %q, want %q", ev.GenesisHash, hash)
	}
}

func TestProposal_EmitsDomainEvent_GenesisTimeUpdated(t *testing.T) {
	newTime := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	prevTime := time.Now().UTC().Truncate(time.Second)
	p := newProposal(proposal.ActionUpdateGenesisTime, mustPayload(proposal.UpdateGenesisTimePayload{
		NewGenesisTime:  newTime,
		PrevGenesisTime: prevTime,
	}))
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now())

	events := p.PopEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev, ok := events[0].(domain.GenesisTimeUpdated)
	if !ok {
		t.Fatalf("expected GenesisTimeUpdated, got %T", events[0])
	}
	if !ev.NewGenesisTime.Equal(newTime) {
		t.Errorf("NewGenesisTime mismatch: got %v, want %v", ev.NewGenesisTime, newTime)
	}
}

func TestProposal_NoEventForNonEmitting_AddGenesisAccount(t *testing.T) {
	p := newProposal(proposal.ActionAddGenesisAccount, mustPayload(proposal.AddGenesisAccountPayload{
		Address: testAddr1,
		Amount:  "1000uatom",
	}))
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now())
	if p.Status != proposal.StatusExecuted {
		t.Fatalf("expected EXECUTED, got %s", p.Status)
	}
	events := p.PopEvents()
	if len(events) != 0 {
		t.Errorf("expected no domain events for AddGenesisAccount, got %d", len(events))
	}
}
