package joinrequest_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/joinrequest"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// Gentx correctness is validated by the shared gentxvalidate library and the
// JoinRequestService; the submission-window deadline is enforced by the service
// too. These tests cover only what New still owns: it is a pure constructor that
// sets PENDING and carries the consensus pubkey through — plus the lifecycle
// transitions and read accessors.

// --- helpers ---

const testOperatorAddr = "cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu"

// osmosisPubKey is the real Ed25519 consensus key from the Bi23Labs Osmosis gentx (32 bytes).
const osmosisPubKey = "f5DzEhtQbnmXE/WZQsX+I8RljPdEU0u0ncVGtniFyEM="

func addr() launch.AccountID {
	return launch.MustNewAccountID(testOperatorAddr)
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

// makeGentx builds a minimal v0.50+ gentx carrying the given self-delegation
// amount. New no longer parses it for validation; the SelfDelegationAmount and
// Moniker accessors read it back out.
func makeGentx(selfDelegation int64) json.RawMessage {
	gentx, _ := json.Marshal(map[string]any{
		"body": map[string]any{
			"messages": []any{
				map[string]any{
					"@type":       "/cosmos.staking.v1beta1.MsgCreateValidator",
					"description": map[string]any{"moniker": "test-validator"},
					"pubkey":      map[string]any{"@type": "/cosmos.crypto.ed25519.PubKey", "key": osmosisPubKey},
					"value":       map[string]any{"denom": "utest", "amount": itoa(selfDelegation)},
				},
			},
		},
		"auth_info":  map[string]any{},
		"signatures": []any{},
	})
	return gentx
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

func newJR(selfDelegation int64) *joinrequest.JoinRequest {
	return joinrequest.New(
		uuid.New(),
		uuid.New(),
		addr(), // operator (validator)
		addr(), // submitter
		makeGentx(selfDelegation),
		peer(),
		rpc(),
		"",
		sig(),
		osmosisPubKey,
		time.Now(),
	)
}

// --- New ---

func TestNew_HappyPath(t *testing.T) {
	jr := newJR(1000000)
	assert.Equal(t, joinrequest.StatusPending, jr.Status)
	assert.Equal(t, osmosisPubKey, jr.ConsensusPubKey, "ConsensusPubKey not set from argument")
}

// --- lifecycle ---

func TestApprove(t *testing.T) {
	jr := newJR(1000000)
	propID := uuid.New()
	require.NoError(t, jr.Approve(propID))
	assert.Equal(t, joinrequest.StatusApproved, jr.Status)
	require.NotNil(t, jr.ApprovedByProposal)
	assert.Equal(t, propID, *jr.ApprovedByProposal)
}

func TestReject(t *testing.T) {
	jr := newJR(1000000)
	require.NoError(t, jr.Reject("bad actor"))
	assert.Equal(t, joinrequest.StatusRejected, jr.Status)
	assert.Equal(t, "bad actor", jr.RejectionReason)
}

func TestExpire(t *testing.T) {
	jr := newJR(1000000)
	require.NoError(t, jr.Expire())
	assert.Equal(t, joinrequest.StatusExpired, jr.Status)
}

func TestRevoke_FromApproved(t *testing.T) {
	jr := newJR(1000000)
	require.NoError(t, jr.Approve(uuid.New()))
	require.NoError(t, jr.Revoke("discovered bad gentx"))
	assert.Equal(t, joinrequest.StatusRejected, jr.Status)
	assert.Nil(t, jr.ApprovedByProposal, "ApprovedByProposal should be cleared on revoke")
}

func TestRevoke_CannotRevokePending(t *testing.T) {
	jr := newJR(1000000)
	require.ErrorIs(t, jr.Revoke("reason"), joinrequest.ErrInvalidJoinRequestStatus,
		"cannot revoke a PENDING request")
}

func TestApprove_CannotApproveTwice(t *testing.T) {
	jr := newJR(1000000)
	require.NoError(t, jr.Approve(uuid.New()))
	require.ErrorIs(t, jr.Approve(uuid.New()), joinrequest.ErrInvalidJoinRequestStatus,
		"cannot approve an already-APPROVED request")
}

// --- accessors ---

func TestSelfDelegationAmount(t *testing.T) {
	jr := newJR(5000000)
	assert.Equal(t, int64(5000000), jr.SelfDelegationAmount())
}

func TestMoniker(t *testing.T) {
	jr := newJR(1000000)
	assert.Equal(t, "test-validator", jr.Moniker())
}
