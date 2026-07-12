# Readiness

Once a launch is `GENESIS_READY`, approved validators confirm they're set up with the correct genesis and
binary — the committee's launch go/no-go signal.

## Confirming readiness

An approved validator submits `POST /launch/{id}/ready` with a signed attestation:

- The operator signs (secp256k1, with a nonce + timestamp for replay protection) that
  `genesis_hash_confirmed` equals the launch's `FinalGenesisSHA256`, and `binary_hash_confirmed` equals
  the record's `BinarySHA256` (the binary check is enforced only when the record declares a hash).
- The caller must have an `APPROVED` join request and the launch must be `GENESIS_READY`.
- There is **one valid confirmation per operator** per genesis version.

## Invalidation

A confirmation is invalidated (the validator must re-confirm) when what it attested to changes:
`UPDATE_GENESIS_TIME`, `REVISE_GENESIS`, or a cancel from `GENESIS_READY`.

## The dashboard

`GET /launch/{id}/dashboard` merges the launch state (status, genesis time, countdown, final genesis hash)
with readiness aggregation: per-validator voting-power share, `ConfirmedReady`, `VotingPowerConfirmed`, and
a `ThresholdStatus`:

| Status      | Meaning                           |
|-------------|-----------------------------------|
| `CONFIRMED` | ≥ ⅔ of voting power has confirmed |
| `AT_RISK`   | < 50 % confirmed                  |
| `REACHABLE` | in between                        |

`GET /launch/{id}/peers` returns approved validators' peer addresses (JSON, or `?format=text` for a
`persistent_peers` list).
