package joinrequest_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

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
	if jr.Status != joinrequest.StatusPending {
		t.Errorf("expected PENDING, got %s", jr.Status)
	}
	if jr.ConsensusPubKey != osmosisPubKey {
		t.Errorf("ConsensusPubKey not set from argument: %q", jr.ConsensusPubKey)
	}
}

// --- lifecycle ---

func TestApprove(t *testing.T) {
	jr := newJR(1000000)
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
	jr := newJR(1000000)
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
	jr := newJR(1000000)
	if err := jr.Expire(); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if jr.Status != joinrequest.StatusExpired {
		t.Errorf("expected EXPIRED, got %s", jr.Status)
	}
}

func TestRevoke_FromApproved(t *testing.T) {
	jr := newJR(1000000)
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
	jr := newJR(1000000)
	if err := jr.Revoke("reason"); err == nil {
		t.Error("expected error: cannot revoke a PENDING request")
	}
}

func TestApprove_CannotApproveTwice(t *testing.T) {
	jr := newJR(1000000)
	_ = jr.Approve(uuid.New())
	if err := jr.Approve(uuid.New()); err == nil {
		t.Error("expected error: cannot approve an already-APPROVED request")
	}
}

// --- accessors ---

func TestSelfDelegationAmount(t *testing.T) {
	jr := newJR(5000000)
	if got := jr.SelfDelegationAmount(); got != 5000000 {
		t.Errorf("SelfDelegationAmount: got %d, want 5000000", got)
	}
}

func TestMoniker(t *testing.T) {
	jr := newJR(1000000)
	if got := jr.Moniker(); got != "test-validator" {
		t.Errorf("Moniker: got %q, want %q", got, "test-validator")
	}
}
