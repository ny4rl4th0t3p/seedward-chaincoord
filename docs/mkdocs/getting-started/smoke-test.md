# Smoke Test

The smoke test runs the full chaincoord protocol end-to-end inside Docker: coordinator setup, four validator applications, M-of-N approvals, genesis assembly, and block production — all verified against a live `gaiad` network.

!!! note "Cosmos SDK chain required"
    The smoke test uses `gaiad` (Gaia / Cosmos Hub) as the validator binary. It is bundled in `Dockerfile.smoke` and downloaded at image build time. The test is intentionally tied to a real Cosmos SDK binary to validate the gentx and genesis assembly steps.

---

## Prerequisites

- Docker with Compose v2
- `make`
- `bin/coordd` built locally (needed to generate keys — `make build`)

---

## Run it

```bash
make test-smoke
```

This single target:

1. Brings down any previous run (`test-down-smoke`)
2. Generates `docker/secrets/audit_key` and `docker/secrets/jwt_key` if missing (`test-secrets-smoke`)
3. Builds all Docker images from `Dockerfile.smoke` (via `--build`)
4. Starts the Compose stack and runs to completion
5. Tears everything down on exit

---

## What it tests

The smoke test script (`docker/smoke-test.sh`) drives the full 20-step protocol:

| Step | Action |
|---|---|
| 1 | Wait for `coordd` health check |
| 2 | Initialise `gaiad` environments for coordinator and 4 validators |
| 3 | Import deterministic secp256k1 operator keys via `smoke-signer` |
| 4 | Authenticate coordinator, obtain JWT |
| 5 | Create launch with a 1-of-1 committee |
| 6 | Upload initial genesis (host mode) |
| 7 | Publish chain record (`DRAFT` → `PUBLISHED`) |
| 8 | Open application window (`PUBLISHED` → `WINDOW_OPEN`) |
| 9 | Each validator authenticates, generates a gentx, and submits a join request |
| 10 | Coordinator approves all 4 validators via proposals |
| 11 | Close application window (`WINDOW_OPEN` → `WINDOW_CLOSED`) |
| 12 | Coordinator assembles the final genesis (`collect-gentxs`) |
| 13 | Upload final genesis |
| 14 | Publish genesis (`WINDOW_CLOSED` → `GENESIS_READY`) |
| 15 | Download and verify final genesis SHA256 |
| 16 | Configure `persistent_peers` in each validator's `config.toml` |
| 17 | Each validator confirms readiness (genesis hash + binary hash) |
| 18 | Signal validator containers to start `gaiad` |
| 19 | Set monitor RPC URL so `coordd` polls for block production |
| 20 | Wait for `LAUNCHED` status (180 s timeout) |

---

## Tear down only

```bash
make test-down-smoke
```

Removes containers and volumes without rebuilding or re-running the test.

---

## Signing

All signing in the smoke test is done by `smoke-signer` using deterministic secp256k1 keys derived from an index (0 = coordinator, 1–4 = validators). This removes the need for a real keychain or hardware wallet in CI.

---

## Docker image

The smoke test uses `docker/Dockerfile.smoke`, which is separate from the lean `docker/Dockerfile` used by the dev environment. `Dockerfile.smoke` builds `coordd` + `smoke-signer` and downloads `gaiad` — none of which belong in a standard `coordd` deployment image.