#!/usr/bin/env bash
# smoke-test.sh — end-to-end chaincoord smoke test
#
# Runs inside the smoke-test container. Orchestrates the full protocol:
#   gaiad init → smoke-signer auth → launch create (with committee)
#   → upload initial genesis → publish chain record → open window
#   → 4x validator join → approve all → close window
#   → assemble final genesis → upload final → publish genesis (GENESIS_READY)
#   → verify hash → ready → start chain → wait for LAUNCHED
#
# All signing is done by smoke-signer using deterministic secp256k1 keys.
# Key index 0 = coordinator; indices 1-4 = validators.
#
# Exit 0 on success, non-zero on any failure (set -e).

set -euo pipefail

SERVER="${COORD_SERVER:-http://coordd:8080}"
CHAIN_ID="${CHAIN_ID:-gaia-smoke-1}"
GAIA_SHARED="${GAIA_SHARED:-/gaia}"
STAKE_DENOM="uatom"
INIT_BALANCE="1000000000${STAKE_DENOM}"
DELEGATION="100000000${STAKE_DENOM}"

# gaiad_val <n> <args...>: gaiad using validator N's home dir.
gaiad_val() {
  local n="$1"; shift
  gaiad --home "${GAIA_SHARED}/val${n}" "$@"
}

# gaiad_coord <args...>: gaiad using the coordinator's home dir.
gaiad_coord() {
  gaiad --home "${GAIA_SHARED}/coord" "$@"
}

# curl_check: wrapper around curl that prints the response body to stderr on
# HTTP >= 400 or curl connection failure, then returns 1.  On success the body
# is echoed to stdout so callers can capture or pipe it as normal.
curl_check() {
  local tmp code
  tmp=$(mktemp)
  if ! code=$(curl -s -o "${tmp}" -w "%{http_code}" "$@"); then
    echo "ERROR: curl failed (network/DNS) for: $*" >&2
    rm -f "${tmp}"
    return 1
  fi
  if [ "${code}" -ge 400 ]; then
    echo "ERROR: HTTP ${code} for: $*" >&2
    cat "${tmp}" >&2
    rm -f "${tmp}"
    return 1
  fi
  cat "${tmp}"
  rm -f "${tmp}"
}

# auth_and_get_token <key_index>: authenticate with coordd and print the JWT token.
auth_and_get_token() {
  local idx="$1"
  local addr
  addr=$(smoke-signer address --key-index "${idx}")

  local challenge_resp challenge
  challenge_resp=$(curl_check -X POST "${SERVER}/auth/challenge" \
    -H "Content-Type: application/json" \
    -d "{\"operator_address\":\"${addr}\"}")
  challenge=$(echo "${challenge_resp}" | jq -r '.challenge')

  local signed_payload token
  signed_payload=$(echo \
    "{\"operator_address\":\"${addr}\",\"challenge\":\"${challenge}\",\"nonce\":\"\",\"timestamp\":\"\",\"pubkey_b64\":\"\",\"signature\":\"\"}" \
    | smoke-signer sign --key-index "${idx}")

  token=$(curl_check -X POST "${SERVER}/auth/verify" \
    -H "Content-Type: application/json" \
    -d "${signed_payload}" | jq -r '.token')
  echo "${token}"
}

# propose_and_sign <token> <launch_id> <action_type> <payload_json>
# Creates a proposal and signs it only if it is still PENDING_SIGNATURES
# (with 1-of-1 committee the proposal executes immediately on creation).
# Prints the proposal ID.
propose_and_sign() {
  local token="$1" launch_id="$2" action_type="$3" payload_json="$4"
  local coord_addr
  coord_addr=$(smoke-signer address --key-index 0)

  local raise_template resp prop_id prop_status
  raise_template=$(printf \
    '{"member_address":"%s","action_type":"%s","payload":%s,"nonce":"","timestamp":"","pubkey_b64":"","signature":""}' \
    "${coord_addr}" "${action_type}" "${payload_json}")
  local signed_raise
  signed_raise=$(echo "${raise_template}" | smoke-signer sign --key-index 0)

  resp=$(curl_check -X POST "${SERVER}/launch/${launch_id}/proposal" \
    -H "Authorization: Bearer ${token}" \
    -H "Content-Type: application/json" \
    -d "${signed_raise}")
  prop_id=$(echo "${resp}" | jq -r '.id')
  prop_status=$(echo "${resp}" | jq -r '.status')

  if [ "${prop_status}" = "PENDING_SIGNATURES" ]; then
    local sign_template signed_sign
    sign_template=$(printf \
      '{"member_address":"%s","decision":"SIGN","nonce":"","timestamp":"","pubkey_b64":"","signature":""}' \
      "${coord_addr}")
    signed_sign=$(echo "${sign_template}" | smoke-signer sign --key-index 0)
    curl_check -X POST "${SERVER}/launch/${launch_id}/proposal/${prop_id}/sign" \
      -H "Authorization: Bearer ${token}" \
      -H "Content-Type: application/json" \
      -d "${signed_sign}" > /dev/null
  fi
  echo "${prop_id}"
}

# ---------------------------------------------------------------------------
# Step 1 — Wait for coordd
# ---------------------------------------------------------------------------
echo "==> [1/20] waiting for coordd..."
for i in $(seq 1 30); do
  if curl_check "${SERVER}/healthz" > /dev/null 2>&1; then
    echo "    coordd is up"
    break
  fi
  if [ "$i" -eq 30 ]; then
    echo "TIMEOUT: coordd did not become healthy within 30s"
    exit 1
  fi
  sleep 1
done

# ---------------------------------------------------------------------------
# Step 2 — Init all gaiad environments
# ---------------------------------------------------------------------------
echo "==> [2/20] initialising gaiad environments..."
gaiad_coord init coordinator --chain-id "${CHAIN_ID}"
# gaiad init defaults bond_denom to "stake"; patch it to match STAKE_DENOM.
# Validators copy this genesis before creating their gentxs, so only the
# coordinator's copy needs patching.
jq --arg denom "${STAKE_DENOM}" '
  .app_state.staking.params.bond_denom   = $denom |
  .app_state.mint.params.mint_denom      = $denom |
  .app_state.crisis.constant_fee.denom   = $denom |
  .app_state.gov.params.min_deposit[0].denom = $denom
' "${GAIA_SHARED}/coord/config/genesis.json" > /tmp/genesis_denom.json
mv /tmp/genesis_denom.json "${GAIA_SHARED}/coord/config/genesis.json"

for i in 1 2 3 4; do
  gaiad_val "${i}" init "val${i}" --chain-id "${CHAIN_ID}"
done

# ---------------------------------------------------------------------------
# Step 3 — Create operator keys for each validator
# ---------------------------------------------------------------------------
echo "==> [3/20] creating validator operator keys via smoke-signer..."
for i in 1 2 3 4; do
  gaiad_val "${i}" keys import-hex operator \
    "$(smoke-signer privkey --key-index "${i}")" \
    --keyring-backend test
done

# ---------------------------------------------------------------------------
# Step 4 — Coordinator auth
# ---------------------------------------------------------------------------
echo "==> [4/20] authenticating coordinator..."
COORD_TOKEN=$(auth_and_get_token 0)
COORD_ADDR=$(smoke-signer address --key-index 0)
COORD_PUBKEY=$(smoke-signer pubkey --key-index 0)
echo "    coordinator: ${COORD_ADDR}"

# ---------------------------------------------------------------------------
# Step 5 — Create launch (with inline committee)
# ---------------------------------------------------------------------------
echo "==> [5/20] creating launch..."
# No committee signature: committee members register their pubkey when they first sign a proposal
# (the ADR-036 envelope carries it), so the member's pub_key_b64 below is optional.

# Launches are private-always: validators must be pre-vetted into the members allowlist to see/join.
# These addresses are deterministic — the gaiad operator keys are imported from the same smoke-signer
# keys (indices 1-4), so they match the operator_address each validator submits with.
VAL_ADDR_1=$(smoke-signer address --key-index 1)
VAL_ADDR_2=$(smoke-signer address --key-index 2)
VAL_ADDR_3=$(smoke-signer address --key-index 3)
VAL_ADDR_4=$(smoke-signer address --key-index 4)

LAUNCH_BODY=$(jq -n \
  --arg chain_id       "${CHAIN_ID}" \
  --arg coord_addr     "${COORD_ADDR}" \
  --arg coord_pubkey   "${COORD_PUBKEY}" \
  --arg val1           "${VAL_ADDR_1}" \
  --arg val2           "${VAL_ADDR_2}" \
  --arg val3           "${VAL_ADDR_3}" \
  --arg val4           "${VAL_ADDR_4}" \
  '{
    launch_type: "PERMISSIONLESS",
    allowlist:   [$val1, $val2, $val3, $val4],
    record: {
      chain_id:                   $chain_id,
      chain_name:                 "Gaia Smoke Test",
      bech32_prefix:              "cosmos",
      binary_name:                "gaiad",
      binary_version:             "v27.1.0",
      denom:                      "uatom",
      min_self_delegation:        "1",
      max_commission_rate:        "0.50",
      max_commission_change_rate: "0.10",
      gentx_deadline:             "2099-01-01T00:00:00Z",
      application_window_open:    "2099-01-01T00:00:00Z",
      genesis_time:               "2099-01-01T00:00:00Z",
      min_validator_count:        4
    },
    committee: {
      members:            [{ address: $coord_addr, moniker: "coordinator", pub_key_b64: $coord_pubkey }],
      threshold_m:        1,
      total_n:            1,
      lead_address:       $coord_addr
    }
  }')

LAUNCH_ID=$(curl_check -X POST "${SERVER}/launch" \
  -H "Authorization: Bearer ${COORD_TOKEN}" \
  -H "Content-Type: application/json" \
  -d "${LAUNCH_BODY}" | jq -r '.id')
echo "    launch ID: ${LAUNCH_ID}"

# ---------------------------------------------------------------------------
# Step 6 — Upload initial genesis (host mode — raw bytes)
# ---------------------------------------------------------------------------
echo "==> [6/20] uploading initial genesis..."
INITIAL_GENESIS_FILE="${GAIA_SHARED}/coord/config/genesis.json"
INITIAL_GENESIS_HASH=$(sha256sum "${INITIAL_GENESIS_FILE}" | awk '{print $1}')
curl_check -X POST "${SERVER}/launch/${LAUNCH_ID}/genesis?type=initial" \
  -H "Authorization: Bearer ${COORD_TOKEN}" \
  -H "Content-Type: application/octet-stream" \
  --data-binary @"${INITIAL_GENESIS_FILE}" > /dev/null
echo "    initial genesis SHA256: ${INITIAL_GENESIS_HASH}"

# ---------------------------------------------------------------------------
# Step 7 — Publish chain record (DRAFT → PUBLISHED)
# ---------------------------------------------------------------------------
echo "==> [7/20] publishing chain record..."
PROP_ID=$(propose_and_sign "${COORD_TOKEN}" "${LAUNCH_ID}" \
  "PUBLISH_CHAIN_RECORD" \
  "{\"initial_genesis_sha256\":\"${INITIAL_GENESIS_HASH}\"}")
echo "    published via proposal ${PROP_ID}"

# ---------------------------------------------------------------------------
# Step 8 — Open application window (requires PUBLISHED status)
# ---------------------------------------------------------------------------
echo "==> [8/20] opening application window..."
curl_check -X POST "${SERVER}/launch/${LAUNCH_ID}/open-window" \
  -H "Authorization: Bearer ${COORD_TOKEN}" > /dev/null

# ---------------------------------------------------------------------------
# Step 9 — For each validator: auth, generate gentx, submit join request
# ---------------------------------------------------------------------------
echo "==> [9/20] validator auth + gentx + join..."
for i in 1 2 3 4; do
  echo "    --- val${i} ---"

  OP_ADDR=$(gaiad_val "${i}" keys show operator --keyring-backend test -a)
  eval "OP_ADDR_${i}=${OP_ADDR}"
  NODE_ID=$(gaiad_val "${i}" comet show-node-id)
  PEER_ADDR="${NODE_ID}@val${i}:26656"

  # Each validator works from a fresh copy of the coordinator's genesis template
  # (no accounts yet).  They register their own account with 0uatom — just to
  # create the auth entry so the account exists — then produce their gentx.
  # The actual staking tokens are added by the coordinator in the final-genesis
  # assembly step.  This mirrors the real-network workflow: validators operate
  # independently without needing to know each other's addresses or balances in
  # advance.
  cp "${GAIA_SHARED}/coord/config/genesis.json" \
     "${GAIA_SHARED}/val${i}/config/genesis.json"
  gaiad_val "${i}" genesis add-genesis-account "${OP_ADDR}" "${DELEGATION}"
  gaiad_val "${i}" genesis gentx operator "${DELEGATION}" \
    --chain-id            "${CHAIN_ID}" \
    --keyring-backend     test \
    --moniker             "val${i}" \
    --commission-rate     "0.05" \
    --commission-max-rate "0.20" \
    --commission-max-change-rate "0.01" \
    --min-self-delegation "1"

  GENTX_FILE=$(ls "${GAIA_SHARED}/val${i}/config/gentx/gentx-"*.json | head -1)
  GENTX_JSON=$(cat "${GENTX_FILE}")

  # Authenticate this validator with coordd.
  VAL_TOKEN=$(auth_and_get_token "${i}")

  # Build and sign join request payload.
  VAL_PUBKEY=$(smoke-signer pubkey --key-index "${i}")
  RPC_ENDPOINT="http://val${i}:26657"
  JOIN_TEMPLATE=$(printf \
    '{"operator_address":"%s","chain_id":"%s","gentx":%s,"peer_address":"%s","rpc_endpoint":"%s","memo":"","nonce":"","timestamp":"","pubkey_b64":"","signature":""}' \
    "${OP_ADDR}" "${CHAIN_ID}" "${GENTX_JSON}" "${PEER_ADDR}" "${RPC_ENDPOINT}")
  SIGNED_JOIN=$(echo "${JOIN_TEMPLATE}" | smoke-signer sign --key-index "${i}")

  JR_ID=$(curl_check -X POST "${SERVER}/launch/${LAUNCH_ID}/join" \
    -H "Authorization: Bearer ${VAL_TOKEN}" \
    -H "Content-Type: application/json" \
    -d "${SIGNED_JOIN}" | jq -r '.id')

  eval "JR_ID_${i}=${JR_ID}"
  eval "OP_ADDR_${i}=${OP_ADDR}"
  eval "VAL_TOKEN_${i}=${VAL_TOKEN}"
  echo "    val${i}: join request ${JR_ID}"
done

# ---------------------------------------------------------------------------
# Step 11 — Approve all validators
# ---------------------------------------------------------------------------
echo "==> [10/20] approving validators..."
for i in 1 2 3 4; do
  eval "JR_ID=\${JR_ID_${i}}"
  eval "OP_ADDR=\${OP_ADDR_${i}}"
  PROP_ID=$(propose_and_sign "${COORD_TOKEN}" "${LAUNCH_ID}" \
    "APPROVE_VALIDATOR" \
    "{\"join_request_id\":\"${JR_ID}\",\"operator_address\":\"${OP_ADDR}\"}")
  echo "    val${i}: approved via proposal ${PROP_ID}"
done

# ---------------------------------------------------------------------------
# Step 12 — Close application window
# ---------------------------------------------------------------------------
echo "==> [11/20] closing application window..."
PROP_ID=$(propose_and_sign "${COORD_TOKEN}" "${LAUNCH_ID}" \
  "CLOSE_APPLICATION_WINDOW" '{}')
echo "    closed via proposal ${PROP_ID}"

# ---------------------------------------------------------------------------
# Step 12 — Assemble final genesis on the coordinator's gaiad home
#
# The coordinator is now the one who knows all validator addresses (gathered
# from the approved join requests) and assigns real token balances.  This is
# the standard Cosmos pattern: validators registered 0-balance accounts just
# to produce valid gentxs; the coordinator funds them here.
# ---------------------------------------------------------------------------
echo "==> [12/20] assembling final genesis..."
for i in 1 2 3 4; do
  eval "OP_ADDR=\${OP_ADDR_${i}}"
  gaiad_coord genesis add-genesis-account "${OP_ADDR}" "${INIT_BALANCE}"
done

mkdir -p "${GAIA_SHARED}/coord/config/gentx"
curl_check "${SERVER}/launch/${LAUNCH_ID}/gentxs" \
  -H "Authorization: Bearer ${COORD_TOKEN}" \
  | jq -c '.gentxs[]' \
  | while IFS= read -r entry; do
      jr_id=$(echo "${entry}" | jq -r '.join_request_id')
      echo "${entry}" | jq -c '.gentx' \
        > "${GAIA_SHARED}/coord/config/gentx/gentx-${jr_id}.json"
    done

gaiad_coord genesis collect-gentxs
# Set genesis_time to ~60s from now.  coordd requires it to be in the future
# at upload time; setting it far in the future (e.g. 2099) would cause gaiad
# to sit idle forever waiting to produce the first block.
GENESIS_TIME=$(date -u -d "@$(($(date +%s) + 60))" +"%Y-%m-%dT%H:%M:%SZ")
jq --arg t "${GENESIS_TIME}" '.genesis_time = $t' \
  "${GAIA_SHARED}/coord/config/genesis.json" > /tmp/genesis_patched.json
mv /tmp/genesis_patched.json "${GAIA_SHARED}/coord/config/genesis.json"
echo "    genesis_time: ${GENESIS_TIME}"
gaiad_coord genesis validate

# ---------------------------------------------------------------------------
# Step 13 — Upload final genesis
# ---------------------------------------------------------------------------
echo "==> [13/20] uploading final genesis..."
FINAL_GENESIS_FILE="${GAIA_SHARED}/coord/config/genesis.json"
FINAL_GENESIS_HASH=$(sha256sum "${FINAL_GENESIS_FILE}" | awk '{print $1}')
curl_check -X POST "${SERVER}/launch/${LAUNCH_ID}/genesis?type=final" \
  -H "Authorization: Bearer ${COORD_TOKEN}" \
  -H "Content-Type: application/octet-stream" \
  --data-binary @"${FINAL_GENESIS_FILE}" > /dev/null

# ---------------------------------------------------------------------------
# Step 14 — Publish genesis (WINDOW_CLOSED → GENESIS_READY)
# ---------------------------------------------------------------------------
echo "==> [14/20] publishing genesis..."
PROP_ID=$(propose_and_sign "${COORD_TOKEN}" "${LAUNCH_ID}" \
  "PUBLISH_GENESIS" \
  "{\"genesis_hash\":\"${FINAL_GENESIS_HASH}\"}")
echo "    genesis published via proposal ${PROP_ID}"

# ---------------------------------------------------------------------------
# Step 15 — Download final genesis, distribute, and verify hash inline
# ---------------------------------------------------------------------------
echo "==> [15/20] distributing and verifying final genesis..."
curl_check -L "${SERVER}/launch/${LAUNCH_ID}/genesis" \
  -H "Authorization: Bearer ${COORD_TOKEN}" > /tmp/final_genesis.json

for i in 1 2 3 4; do
  cp /tmp/final_genesis.json "${GAIA_SHARED}/val${i}/config/genesis.json"
done

LOCAL_HASH=$(sha256sum /tmp/final_genesis.json | awk '{print $1}')
SERVER_HASH=$(curl_check "${SERVER}/launch/${LAUNCH_ID}/genesis/hash" \
  -H "Authorization: Bearer ${COORD_TOKEN}" | jq -r '.final_sha256')
if [ "${LOCAL_HASH}" != "${SERVER_HASH}" ]; then
  echo "HASH MISMATCH: local=${LOCAL_HASH} server=${SERVER_HASH}"
  exit 1
fi
GENESIS_HASH="${LOCAL_HASH}"
echo "    genesis SHA256: ${GENESIS_HASH} (verified)"

# ---------------------------------------------------------------------------
# Step 16 — Configure persistent_peers in each validator's config.toml
# ---------------------------------------------------------------------------
echo "==> [16/20] configuring persistent_peers..."
PEERS=$(curl_check "${SERVER}/launch/${LAUNCH_ID}/peers?format=text" \
  -H "Authorization: Bearer ${COORD_TOKEN}")
for i in 1 2 3 4; do
  cfg="${GAIA_SHARED}/val${i}/config/config.toml"
  sed -i "s|^persistent_peers = .*|persistent_peers = \"${PEERS}\"|" "${cfg}"
  # Allow private/non-routable addresses — required for Docker bridge networks.
  sed -i "s|^addr_book_strict = .*|addr_book_strict = false|" "${cfg}"
done

# ---------------------------------------------------------------------------
# Step 17 — Each validator confirms readiness
# ---------------------------------------------------------------------------
echo "==> [17/20] confirming validator readiness..."
BINARY_HASH=$(sha256sum "$(which gaiad)" | awk '{print $1}')

for i in 1 2 3 4; do
  eval "VAL_TOKEN=\${VAL_TOKEN_${i}}"
  OP_ADDR=$(smoke-signer address --key-index "${i}")
  VAL_PUBKEY=$(smoke-signer pubkey --key-index "${i}")
  READY_TEMPLATE=$(printf \
    '{"operator_address":"%s","genesis_hash_confirmed":"%s","binary_hash_confirmed":"%s","nonce":"","timestamp":"","pubkey_b64":"","signature":""}' \
    "${OP_ADDR}" "${GENESIS_HASH}" "${BINARY_HASH}")
  SIGNED_READY=$(echo "${READY_TEMPLATE}" | smoke-signer sign --key-index "${i}")

  curl_check -X POST "${SERVER}/launch/${LAUNCH_ID}/ready" \
    -H "Authorization: Bearer ${VAL_TOKEN}" \
    -H "Content-Type: application/json" \
    -d "${SIGNED_READY}" > /dev/null
  echo "    val${i}: ready confirmed"
done

# ---------------------------------------------------------------------------
# Step 18 — Signal validator containers to start gaiad
# ---------------------------------------------------------------------------
echo "==> [18/20] signalling validators to start..."
for i in 1 2 3 4; do
  touch "${GAIA_SHARED}/val${i}/ready"
done

# ---------------------------------------------------------------------------
# Step 19 — Set monitor RPC URL
# ---------------------------------------------------------------------------
echo "==> [19/20] setting monitor RPC URL..."
curl_check -X PATCH "${SERVER}/launch/${LAUNCH_ID}" \
  -H "Authorization: Bearer ${COORD_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"monitor_rpc_url":"http://val1:26657"}' > /dev/null

# ---------------------------------------------------------------------------
# Step 20 — Wait for LAUNCHED
# ---------------------------------------------------------------------------
echo "==> [20/20] waiting for LAUNCHED status (180s timeout)..."
for i in $(seq 1 60); do
  STATUS=$(curl_check "${SERVER}/launch/${LAUNCH_ID}" \
    -H "Authorization: Bearer ${COORD_TOKEN}" | jq -r '.status')
  if [ "${STATUS}" = "LAUNCHED" ]; then
    echo "SUCCESS: chain launched"
    exit 0
  fi
  echo "    status: ${STATUS} (attempt ${i}/60)"
  sleep 3
done

echo "TIMEOUT: chain did not reach LAUNCHED status within 180s"
exit 1
