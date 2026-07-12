package sqlite

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/joinrequest"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/proposal"
)

// openTestDB opens an in-memory SQLite database and runs all migrations.
// It is closed automatically via t.Cleanup.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(":memory:")
	require.NoError(t, err, "openTestDB")
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// --- test fixtures ---

const (
	addr1 = "cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu"
	addr2 = "cosmos1yy3zxfp9ycnjs2f29vkz6t30xqcnyve5j4ep6w"
	addr3 = "cosmos1g9pyx3z9ger5sj22fdxy6nj02pg4y5657yq8y0"

	// 64-byte base64 secp256k1 compact signature (all zeros) for test use.
	// base64(64×0x00) = 86 A's + "==" (88 chars, 64 decoded bytes).
	testSig = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="
)

func mustAddr(s string) launch.AccountID { return launch.MustNewAccountID(s) }
func mustSig() launch.Signature {
	s, err := launch.NewSignature(testSig)
	if err != nil {
		panic(err)
	}
	return s
}

func testCommittee() launch.Committee {
	return launch.Committee{
		ID:                uuid.New(),
		ThresholdM:        2,
		TotalN:            3,
		LeadAddress:       mustAddr(addr1),
		CreationSignature: mustSig(),
		Members: []launch.CommitteeMember{
			{Address: mustAddr(addr1), Moniker: "coord-1", PubKeyB64: "AAAA"},
			{Address: mustAddr(addr2), Moniker: "coord-2", PubKeyB64: "BBBB"},
			{Address: mustAddr(addr3), Moniker: "coord-3", PubKeyB64: "CCCC"},
		},
		CreatedAt: time.Now().UTC(),
	}
}

func testChainRecord() launch.ChainRecord {
	return launch.ChainRecord{
		ChainID:           "testchain-1",
		ChainName:         "Test Chain",
		Bech32Prefix:      "cosmos",
		BinaryName:        "testchaind",
		BinaryVersion:     "v1.0.0",
		BinarySHA256:      "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Denom:             "utest",
		MinSelfDelegation: "1000000",
		GentxDeadline:     time.Now().UTC().Add(24 * time.Hour),
		MinValidatorCount: 2,
	}
}

func testLaunch(t *testing.T) *launch.Launch {
	t.Helper()
	cr, err := launch.NewCommissionRate("0.05")
	require.NoError(t, err)
	rec := testChainRecord()
	rec.MaxCommissionRate = cr
	rec.MaxCommissionChangeRate = cr

	l, err := launch.New(uuid.New(), rec, launch.LaunchTypeTestnet, testCommittee())
	require.NoError(t, err, "testLaunch")
	return l
}

func testJoinRequest(t *testing.T, launchID uuid.UUID) *joinrequest.JoinRequest {
	t.Helper()
	peer, _ := launch.NewPeerAddress("abcdef1234567890abcdef1234567890abcdef12@192.168.1.1:26656")
	rpc, _ := launch.NewRPCEndpoint("https://192.168.1.1:26657")

	// Two random UUIDs concatenated give 32 bytes of entropy for a unique Ed25519 pubkey per call.
	// Uniqueness matters because the DB enforces consensus_pubkey uniqueness among
	// ACTIVE (PENDING/APPROVED) requests per launch (partial unique index, migration 0006).
	id1, id2 := uuid.New(), uuid.New()
	uniquePubKey := base64.StdEncoding.EncodeToString(append(id1[:], id2[:]...))

	gentxBytes, _ := json.Marshal(map[string]any{
		"body": map[string]any{
			"messages": []any{
				map[string]any{
					"@type":       "/cosmos.staking.v1beta1.MsgCreateValidator",
					"description": map[string]any{"moniker": "test-validator"},
					"pubkey": map[string]any{
						"@type": "/cosmos.crypto.ed25519.PubKey",
						"key":   uniquePubKey,
					},
					"value": map[string]any{"denom": "utest", "amount": "2000000"},
				},
			},
		},
		"auth_info":  map[string]any{},
		"signatures": []any{},
	})

	jr := joinrequest.New(
		uuid.New(), launchID,
		mustAddr(addr1), // operator (validator)
		mustAddr(addr1), // submitter
		gentxBytes,
		peer, rpc, "",
		mustSig(),
		uniquePubKey,
		time.Now().UTC(),
	)
	return jr
}

func testProposal(t *testing.T, launchID uuid.UUID) *proposal.Proposal {
	t.Helper()
	payload, _ := json.Marshal(proposal.CloseApplicationWindowPayload{})
	p, err := proposal.New(
		uuid.New(), launchID,
		proposal.ActionCloseApplicationWindow,
		payload,
		mustAddr(addr1), mustSig(),
		48*time.Hour, time.Now().UTC(),
	)
	require.NoError(t, err, "testProposal")
	return p
}
