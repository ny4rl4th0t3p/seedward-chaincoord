package proposal_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

func newAddr(s string) launch.AccountID {
	return launch.MustNewAccountID(s)
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
	assert.Equal(t, 1, p.SignCount(), "expected 1 signature after creation (proposer)")
	assert.Equal(t, proposal.StatusPendingSignatures, p.Status)
}

func TestProposal_ReachesQuorum(t *testing.T) {
	p := newProposal(proposal.ActionCloseApplicationWindow, mustPayload(proposal.CloseApplicationWindowPayload{}))

	require.NoError(t, p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now()))

	assert.Equal(t, proposal.StatusExecuted, p.Status, "expected EXECUTED after quorum")
	assert.NotNil(t, p.ExecutedAt, "ExecutedAt should be set on EXECUTED proposal")
}

func TestProposal_VetoImmediatelyTerminates(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())

	require.NoError(t, p.Sign(newAddr(testAddr2), proposal.DecisionVeto, newSig(), thresholdM, time.Now()))

	assert.Equal(t, proposal.StatusVetoed, p.Status, "expected VETOED after veto")
}

func TestProposal_CannotSignTwice(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())
	// proposer already signed on creation
	err := p.Sign(newAddr(testAddr1), proposal.DecisionSign, newSig(), thresholdM, time.Now())
	assert.ErrorIs(t, err, proposal.ErrMemberAlreadySigned, "duplicate signature from same committee member")
}

func TestProposal_CannotSignExecuted(t *testing.T) {
	p := newProposal(proposal.ActionCloseApplicationWindow, mustPayload(proposal.CloseApplicationWindowPayload{}))
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now())
	// now EXECUTED

	err := p.Sign(newAddr(testAddr3), proposal.DecisionSign, newSig(), thresholdM, time.Now())
	assert.ErrorIs(t, err, proposal.ErrProposalNotPending, "cannot sign an EXECUTED proposal")
}

func TestProposal_TTLExpiry(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())

	expired := p.ExpireIfStale(time.Now().Add(49 * time.Hour))
	assert.True(t, expired, "expected proposal to be expired")
	assert.Equal(t, proposal.StatusExpired, p.Status)

	// The proposal was already moved to EXPIRED by ExpireIfStale, so the status guard
	// fires before the TTL guard.
	err := p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now().Add(49*time.Hour))
	assert.ErrorIs(t, err, proposal.ErrProposalNotPending, "cannot sign an already-EXPIRED proposal")
}

func TestProposal_NotExpiredYet(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())
	expired := p.ExpireIfStale(time.Now().Add(1 * time.Hour))
	assert.False(t, expired, "should not expire before TTL")
}

func TestProposal_EmitsDomainEventOnExecute(t *testing.T) {
	jrID := uuid.New()
	p := newProposal(proposal.ActionApproveValidator, mustPayload(proposal.ApproveValidatorPayload{
		JoinRequestID:   jrID,
		OperatorAddress: testAddr1,
	}))

	_ = p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now())

	events := p.PopEvents()
	require.Len(t, events, 1)
	ev, ok := events[0].(domain.ValidatorApproved)
	require.True(t, ok, "expected ValidatorApproved event, got %T", events[0])
	assert.Equal(t, jrID, ev.JoinRequestID)
}

func TestProposal_PopEventsClearsBuffer(t *testing.T) {
	p := newProposal(proposal.ActionCloseApplicationWindow, mustPayload(proposal.CloseApplicationWindowPayload{}))
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now())

	_ = p.PopEvents()
	second := p.PopEvents()
	assert.Empty(t, second, "PopEvents should clear the buffer; second call should return empty")
}

func TestProposal_InvalidPayload_Rejected(t *testing.T) {
	// APPROVE_VALIDATOR with missing required fields must fail at creation
	_, err := proposal.New(uuid.New(), uuid.New(),
		proposal.ActionApproveValidator,
		mustPayload(map[string]string{"irrelevant": "data"}),
		newAddr(testAddr1), newSig(), 48*time.Hour, time.Now(),
	)
	assert.Error(t, err, "ApproveValidator payload missing join_request_id")
}

func TestProposal_ValidatePayload_UnknownAction(t *testing.T) {
	err := proposal.ValidatePayload("NONEXISTENT_ACTION", []byte(`{}`))
	assert.Error(t, err, "expected error for unknown action type")
}

// ---- New: nil payload -------------------------------------------------------

func TestNew_NilPayload(t *testing.T) {
	_, err := proposal.New(uuid.New(), uuid.New(),
		proposal.ActionApproveValidator, nil,
		newAddr(testAddr1), newSig(), 48*time.Hour, time.Now())
	assert.ErrorIs(t, err, proposal.ErrProposalPayloadRequired, "expected error for nil payload")
}

// ---- ValidatePayload: valid payloads for the main action types --------------

func TestValidatePayload_RejectValidator_Valid(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionRejectValidator, mustPayload(proposal.RejectValidatorPayload{
		JoinRequestID:   uuid.New(),
		OperatorAddress: testAddr1,
		Reason:          "bad actor",
	}))
	assert.NoError(t, err)
}

func TestValidatePayload_RemoveApprovedValidator_Valid(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionRemoveApprovedValidator, mustPayload(proposal.RemoveApprovedValidatorPayload{
		JoinRequestID:   uuid.New(),
		OperatorAddress: testAddr1,
		Reason:          "slashed",
	}))
	assert.NoError(t, err)
}

func TestValidatePayload_ApproveAllocationFile_Valid(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionApproveAllocationFile, mustPayload(proposal.ApproveAllocationFilePayload{
		Type: "claims",
		Hash: "a665a45920422f9d417e4867efdc4fb8a04a1f3fff1fa07e998e86f7f7a27ae3",
	}))
	assert.NoError(t, err)
}

func TestValidatePayload_PublishGenesis_Valid(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionPublishGenesis, mustPayload(proposal.PublishGenesisPayload{
		GenesisHash: "a665a45920422f9d417e4867efdc4fb8a04a1f3fff1fa07e998e86f7f7a27ae3",
	}))
	assert.NoError(t, err)
}

func TestValidatePayload_UpdateGenesisTime_Valid(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionUpdateGenesisTime, mustPayload(proposal.UpdateGenesisTimePayload{
		NewGenesisTime:  time.Now().Add(24 * time.Hour),
		PrevGenesisTime: time.Now(),
	}))
	assert.NoError(t, err)
}

func TestValidatePayload_ReplaceCommitteeMember_Valid(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionReplaceCommitteeMember, mustPayload(proposal.ReplaceCommitteeMemberPayload{
		OldAddress: testAddr1,
		NewAddress: testAddr2,
		NewMoniker: "new-node",
		NewPubKey:  "AAAA",
	}))
	assert.NoError(t, err)
}

func TestValidatePayload_CancelLaunch_Valid(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionCancelLaunch, mustPayload(proposal.CancelLaunchPayload{}))
	require.NoError(t, err)
}

func TestValidatePayload_CloseApplicationWindow_Valid(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionCloseApplicationWindow, mustPayload(proposal.CloseApplicationWindowPayload{}))
	assert.NoError(t, err)
}

// ---- ValidatePayload: error paths -------------------------------------------

func TestValidatePayload_ApproveValidator_MissingJoinRequestID(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionApproveValidator, mustPayload(proposal.ApproveValidatorPayload{
		OperatorAddress: testAddr1,
	}))
	assert.Error(t, err, "join_request_id is required")
}

func TestValidatePayload_ApproveValidator_MissingOperatorAddress(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionApproveValidator, mustPayload(proposal.ApproveValidatorPayload{
		JoinRequestID: uuid.New(),
	}))
	assert.Error(t, err, "operator_address is required")
}

func TestValidatePayload_RejectValidator_MissingJoinRequestID(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionRejectValidator, mustPayload(proposal.RejectValidatorPayload{
		OperatorAddress: testAddr1,
	}))
	assert.Error(t, err, "join_request_id is required")
}

func TestValidatePayload_PublishGenesis_MissingHash(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionPublishGenesis, mustPayload(proposal.PublishGenesisPayload{}))
	assert.Error(t, err, "genesis_hash is required")
}

func TestValidatePayload_UpdateGenesisTime_ZeroTime(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionUpdateGenesisTime, mustPayload(proposal.UpdateGenesisTimePayload{}))
	assert.Error(t, err, "new_genesis_time is required")
}

func TestValidatePayload_ApproveAllocationFile_InvalidType(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionApproveAllocationFile, mustPayload(proposal.ApproveAllocationFilePayload{
		Type: "bogus",
		Hash: "a665a45920422f9d417e4867efdc4fb8a04a1f3fff1fa07e998e86f7f7a27ae3",
	}))
	assert.Error(t, err, "invalid allocation type")
}

func TestValidatePayload_ApproveAllocationFile_MissingHash(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionApproveAllocationFile, mustPayload(proposal.ApproveAllocationFilePayload{
		Type: "accounts",
	}))
	assert.Error(t, err, "hash is required")
}

func TestValidatePayload_ReplaceCommitteeMember_MissingAddresses(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionReplaceCommitteeMember, mustPayload(proposal.ReplaceCommitteeMemberPayload{
		NewMoniker: "node",
	}))
	assert.Error(t, err, "old_address and new_address are required")
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
	assert.NoError(t, err)
}

func TestValidatePayload_ExpandCommittee_WithThreshold(t *testing.T) {
	m := 2
	p := validExpandPayload()
	p.NewThresholdM = &m
	err := proposal.ValidatePayload(proposal.ActionExpandCommittee, mustPayload(p))
	assert.NoError(t, err)
}

func TestValidatePayload_ExpandCommittee_MissingAddress(t *testing.T) {
	p := validExpandPayload()
	p.NewMember.Address = ""
	err := proposal.ValidatePayload(proposal.ActionExpandCommittee, mustPayload(p))
	assert.Error(t, err, "new_member.address is required")
}

func TestValidatePayload_ExpandCommittee_MissingMoniker(t *testing.T) {
	p := validExpandPayload()
	p.NewMember.Moniker = ""
	err := proposal.ValidatePayload(proposal.ActionExpandCommittee, mustPayload(p))
	assert.Error(t, err, "new_member.moniker is required")
}

func TestValidatePayload_ExpandCommittee_MissingPubKey(t *testing.T) {
	p := validExpandPayload()
	p.NewMember.PubKeyB64 = ""
	err := proposal.ValidatePayload(proposal.ActionExpandCommittee, mustPayload(p))
	assert.Error(t, err, "new_member.pubkey_base64 is required")
}

// ---- ValidatePayload: ShrinkCommittee ---------------------------------------

func TestValidatePayload_ShrinkCommittee_Valid(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionShrinkCommittee, mustPayload(proposal.ShrinkCommitteePayload{
		RemoveAddress: testAddr1,
	}))
	assert.NoError(t, err)
}

func TestValidatePayload_ShrinkCommittee_WithThreshold(t *testing.T) {
	m := 1
	err := proposal.ValidatePayload(proposal.ActionShrinkCommittee, mustPayload(proposal.ShrinkCommitteePayload{
		RemoveAddress: testAddr1,
		NewThresholdM: &m,
	}))
	assert.NoError(t, err)
}

func TestValidatePayload_ShrinkCommittee_MissingRemoveAddress(t *testing.T) {
	err := proposal.ValidatePayload(proposal.ActionShrinkCommittee, mustPayload(proposal.ShrinkCommitteePayload{}))
	assert.Error(t, err, "remove_address is required")
}

// ---- Sign: terminal status paths --------------------------------------------

func TestProposal_CannotSignVetoed(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionVeto, newSig(), thresholdM, time.Now())
	err := p.Sign(newAddr(testAddr3), proposal.DecisionSign, newSig(), thresholdM, time.Now())
	assert.ErrorIs(t, err, proposal.ErrProposalNotPending, "cannot sign VETOED proposal")
}

func TestProposal_CannotSignExpired(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())
	future := time.Now().Add(49 * time.Hour)
	_ = p.ExpireIfStale(future)
	// Already moved to EXPIRED, so the status guard fires before the TTL guard.
	err := p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, future)
	assert.ErrorIs(t, err, proposal.ErrProposalNotPending, "cannot sign an already-EXPIRED proposal")
}

func TestProposal_TTLGuardOnStillPending(t *testing.T) {
	// A still-PENDING proposal signed past its TTL (without ExpireIfStale having run)
	// hits the TTL guard specifically.
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())
	err := p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now().Add(49*time.Hour))
	assert.ErrorIs(t, err, proposal.ErrProposalTTLExpired, "signing past the TTL is blocked by the TTL guard")
}

// ---- ExpireIfStale: terminal states are not re-expired ----------------------

func TestExpireIfStale_ExecutedNotChanged(t *testing.T) {
	p := newProposal(proposal.ActionCloseApplicationWindow, mustPayload(proposal.CloseApplicationWindowPayload{}))
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now())
	changed := p.ExpireIfStale(time.Now().Add(49 * time.Hour))
	assert.False(t, changed, "ExpireIfStale should not change an EXECUTED proposal")
	assert.Equal(t, proposal.StatusExecuted, p.Status)
}

func TestExpireIfStale_VetoedNotChanged(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionVeto, newSig(), thresholdM, time.Now())
	changed := p.ExpireIfStale(time.Now().Add(49 * time.Hour))
	assert.False(t, changed, "ExpireIfStale should not change a VETOED proposal")
	assert.Equal(t, proposal.StatusVetoed, p.Status)
}

// ---- CheckQuorum ------------------------------------------------------------

func TestCheckQuorum_ExecutesWhenAlreadyAtThreshold(t *testing.T) {
	p := newProposal(proposal.ActionCloseApplicationWindow, mustPayload(proposal.CloseApplicationWindowPayload{}))
	p.CheckQuorum(1, time.Now())
	assert.Equal(t, proposal.StatusExecuted, p.Status, "expected EXECUTED after CheckQuorum with M=1")
	assert.NotNil(t, p.ExecutedAt, "ExecutedAt should be set")
}

func TestCheckQuorum_NoopWhenBelowThreshold(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())
	p.CheckQuorum(thresholdM, time.Now())
	assert.Equal(t, proposal.StatusPendingSignatures, p.Status)
}

func TestCheckQuorum_NoopOnNonPendingStatus(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionVeto, newSig(), thresholdM, time.Now())
	p.CheckQuorum(1, time.Now())
	assert.Equal(t, proposal.StatusVetoed, p.Status)
}

// ---- SignCount --------------------------------------------------------------

func TestSignCount_VetoNotCounted(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionVeto, newSig(), thresholdM, time.Now())
	assert.Equal(t, 1, p.SignCount(), "veto does not increment sign count")
}

// ---- PopEvents: no events ---------------------------------------------------

func TestPopEvents_EmptyOnNewProposal(t *testing.T) {
	p := newProposal(proposal.ActionApproveValidator, validApprovePayload())
	events := p.PopEvents()
	assert.Empty(t, events, "expected no events on fresh proposal")
}

// ---- Domain events for several action types ---------------------------------

// TestProposal_ExecutionEvent_ForEveryActionType is the completeness guard: every proposal
// ActionType must map to a known outcome on execution — either a specific domain event emitted by
// emitExecutionEvents, or "" for the committee resizes whose events the application layer records
// (they need launch-aggregate state the payload can't carry). Adding a new ActionType without a
// case here fails the length assertion.
func TestProposal_ExecutionEvent_ForEveryActionType(t *testing.T) {
	genesisHash := "a665a45920422f9d417e4867efdc4fb8a04a1f3fff1fa07e998e86f7f7a27ae3"
	cases := []struct {
		action  proposal.ActionType
		payload any
		want    string // domain event name; "" = emitted by the application layer, not the domain
	}{
		{proposal.ActionApproveValidator, proposal.ApproveValidatorPayload{JoinRequestID: uuid.New(), OperatorAddress: testAddr1}, "ValidatorApproved"},
		{proposal.ActionRejectValidator, proposal.RejectValidatorPayload{JoinRequestID: uuid.New(), OperatorAddress: testAddr1, Reason: "x"}, "ValidatorRejected"},
		{proposal.ActionRemoveApprovedValidator, proposal.RemoveApprovedValidatorPayload{JoinRequestID: uuid.New(), OperatorAddress: testAddr1, Reason: "x"}, "ValidatorRemoved"},
		{proposal.ActionApproveAllocationFile, proposal.ApproveAllocationFilePayload{Type: "claims", Hash: genesisHash}, "AllocationFileApproved"},
		{proposal.ActionPublishChainRecord, proposal.PublishChainRecordPayload{InitialGenesisHash: genesisHash}, "ChainRecordPublished"},
		{proposal.ActionCloseApplicationWindow, proposal.CloseApplicationWindowPayload{}, "WindowClosed"},
		{proposal.ActionPublishGenesis, proposal.PublishGenesisPayload{GenesisHash: genesisHash}, "GenesisPublished"},
		{proposal.ActionUpdateGenesisTime, proposal.UpdateGenesisTimePayload{NewGenesisTime: time.Now().Add(time.Hour), PrevGenesisTime: time.Now()}, "GenesisTimeUpdated"},
		{proposal.ActionReviseGenesis, proposal.ReviseGenesisPayload{}, "GenesisRevisionApproved"},
		{proposal.ActionReplaceCommitteeMember, proposal.ReplaceCommitteeMemberPayload{OldAddress: testAddr1, NewAddress: testAddr2, NewMoniker: "n", NewPubKey: "AAAA"}, ""},
		{proposal.ActionExpandCommittee, validExpandPayload(), ""},
		{proposal.ActionShrinkCommittee, proposal.ShrinkCommitteePayload{RemoveAddress: testAddr1}, ""},
		{proposal.ActionCancelLaunch, proposal.CancelLaunchPayload{}, "LaunchCancelled"},
	}
	require.Len(t, cases, 13, "every proposal ActionType must be covered — add a case (and bump) when adding an action")

	for _, tc := range cases {
		t.Run(string(tc.action), func(t *testing.T) {
			p := newProposal(tc.action, mustPayload(tc.payload))
			require.NoError(t, p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now()))
			events := p.PopEvents()
			if tc.want == "" {
				assert.Empty(t, events, "%s emits its event from the application layer, not the domain", tc.action)
				return
			}
			require.Len(t, events, 1, "%s must emit exactly one domain event", tc.action)
			assert.Equal(t, tc.want, events[0].EventName())
		})
	}
}

func TestProposal_EmitsDomainEvent_RejectValidator(t *testing.T) {
	jrID := uuid.New()
	p := newProposal(proposal.ActionRejectValidator, mustPayload(proposal.RejectValidatorPayload{
		JoinRequestID:   jrID,
		OperatorAddress: testAddr1,
		Reason:          "spam",
	}))
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now())

	events := p.PopEvents()
	require.Len(t, events, 1)
	ev, ok := events[0].(domain.ValidatorRejected)
	require.True(t, ok, "expected ValidatorRejected, got %T", events[0])
	assert.Equal(t, jrID, ev.JoinRequestID)
	assert.Equal(t, "spam", ev.Reason)
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
	require.Len(t, events, 1)
	ev, ok := events[0].(domain.ValidatorRemoved)
	require.True(t, ok, "expected ValidatorRemoved, got %T", events[0])
	assert.Equal(t, jrID, ev.JoinRequestID)
}

func TestProposal_EmitsDomainEvent_WindowClosed(t *testing.T) {
	p := newProposal(proposal.ActionCloseApplicationWindow, mustPayload(proposal.CloseApplicationWindowPayload{}))
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now())

	events := p.PopEvents()
	require.Len(t, events, 1)
	_, ok := events[0].(domain.WindowClosed)
	assert.True(t, ok, "expected WindowClosed, got %T", events[0])
}

func TestProposal_EmitsDomainEvent_GenesisPublished(t *testing.T) {
	hash := "a665a45920422f9d417e4867efdc4fb8a04a1f3fff1fa07e998e86f7f7a27ae3"
	p := newProposal(proposal.ActionPublishGenesis, mustPayload(proposal.PublishGenesisPayload{
		GenesisHash: hash,
	}))
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now())

	events := p.PopEvents()
	require.Len(t, events, 1)
	ev, ok := events[0].(domain.GenesisPublished)
	require.True(t, ok, "expected GenesisPublished, got %T", events[0])
	assert.Equal(t, hash, ev.GenesisHash)
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
	require.Len(t, events, 1)
	ev, ok := events[0].(domain.GenesisTimeUpdated)
	require.True(t, ok, "expected GenesisTimeUpdated, got %T", events[0])
	assert.True(t, ev.NewGenesisTime.Equal(newTime), "NewGenesisTime mismatch: got %v, want %v", ev.NewGenesisTime, newTime)
}

func TestProposal_EmitsDomainEvent_AllocationFileApproved(t *testing.T) {
	hash := "a665a45920422f9d417e4867efdc8fb8a04a1f3fff1fa07e9a8e86f7f7a27ae3"
	p := newProposal(proposal.ActionApproveAllocationFile, mustPayload(proposal.ApproveAllocationFilePayload{
		Type: "claims",
		Hash: hash,
	}))
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now())

	events := p.PopEvents()
	require.Len(t, events, 1)
	ev, ok := events[0].(domain.AllocationFileApproved)
	require.True(t, ok, "expected AllocationFileApproved, got %T", events[0])
	assert.Equal(t, "claims", ev.AllocationType)
	assert.Equal(t, hash, ev.SHA256)
}

func TestProposal_NoEventForNonEmitting_ExpandCommittee(t *testing.T) {
	p := newProposal(proposal.ActionExpandCommittee, mustPayload(validExpandPayload()))
	_ = p.Sign(newAddr(testAddr2), proposal.DecisionSign, newSig(), thresholdM, time.Now())
	require.Equal(t, proposal.StatusExecuted, p.Status)
	events := p.PopEvents()
	assert.Empty(t, events, "expected no domain events for ExpandCommittee")
}
