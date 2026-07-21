package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/application/services"
	"github.com/ny4rl4th0t3p/seedward-chaincoord/internal/domain/launch"
)

// bridgeWireGolden is the canonical rehearsal-input wire payload.
// It is the coordd side of a drift guard: this test pins coordd's emitted field names/shape,
// and seedward-rehearsal has a mirror consumer test that decodes the SAME payload. The two
// copies are duplicated deliberately (loose client/server coupling) — KEEP THEM IN SYNC with
// each other and with bridge contract. A rename/drop here fails this test loudly.
const bridgeWireGolden = `{
  "schema_version": 1,
  "launch_id": "11111111-1111-1111-1111-111111111111",
  "attempt_id": "33333333-3333-3333-3333-333333333333",
  "generated_at": "2026-01-02T03:04:05Z",
  "status": "WINDOW_OPEN",
  "chain": {
    "chain_id": "test-1",
    "bech32_prefix": "cosmos",
    "denom": "uatom",
    "total_supply": "7000000",
    "min_self_delegation": "1000000",
    "max_commission_rate": "0.20",
    "max_commission_change_rate": "0.01",
    "min_validator_count": 3,
    "genesis_time": "2026-06-01T00:00:00Z",
    "binary": {
      "name": "gaiad",
      "version": "v27.1.0",
      "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
      "repo_url": "https://github.com/cosmos/gaia",
      "repo_commit": "abcdef"
    }
  },
  "gentxs": [
    {"operator_address": "cosmosvaloper1aaa", "consensus_pubkey": "pk1", "moniker": "val-a", "self_delegation": "2000000", "gentx": {"a": 1}},
    {"operator_address": "cosmosvaloper1bbb", "consensus_pubkey": "pk2", "moniker": "val-b", "self_delegation": "3000000", "gentx": {"b": 2}}
  ],
  "allocations": {
    "accounts": {"sha256": "accountshash", "approved_by_proposal": "22222222-2222-2222-2222-222222222222", "url": "/api/v1/bridge/launches/11111111-1111-1111-1111-111111111111/allocations/accounts"}
  },
  "input_set_hash": "deadbeefdeadbeef"
}`

func TestRehearsalInputJSON_WireGolden(t *testing.T) {
	genesisTime := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	in := &services.RehearsalInput{
		LaunchID:    uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		AttemptID:   uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		GeneratedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Status:      launch.StatusWindowOpen,
		Chain: services.RehearsalChain{
			ChainID:                 "test-1",
			Bech32Prefix:            "cosmos",
			Denom:                   "uatom",
			TotalSupply:             "7000000",
			MinSelfDelegation:       "1000000",
			MaxCommissionRate:       "0.20",
			MaxCommissionChangeRate: "0.01",
			MinValidatorCount:       3,
			GenesisTime:             &genesisTime,
			BinaryName:              "gaiad",
			BinaryVersion:           "v27.1.0",
			BinarySHA256:            "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			RepoURL:                 "https://github.com/cosmos/gaia",
			RepoCommit:              "abcdef",
		},
		Gentxs: []services.RehearsalGentx{
			{OperatorAddress: "cosmosvaloper1aaa", ConsensusPubKey: "pk1", Moniker: "val-a", SelfDelegation: "2000000", GentxJSON: json.RawMessage(`{"a":1}`)},
			{OperatorAddress: "cosmosvaloper1bbb", ConsensusPubKey: "pk2", Moniker: "val-b", SelfDelegation: "3000000", GentxJSON: json.RawMessage(`{"b":2}`)},
		},
		Allocations: []services.RehearsalAllocation{
			{Type: "accounts", SHA256: "accountshash", ApprovedByProposal: "22222222-2222-2222-2222-222222222222"},
		},
		InputSetHash: "deadbeefdeadbeef",
	}

	got, err := json.Marshal(rehearsalInputToJSON(in))
	require.NoError(t, err)
	assert.JSONEq(t, bridgeWireGolden, string(got),
		"coordd's rehearsal-input wire shape drifted from the contract; update bridge-contract.md §2 "+
			"and the seedward-rehearsal consumer golden if this is intentional")
}
