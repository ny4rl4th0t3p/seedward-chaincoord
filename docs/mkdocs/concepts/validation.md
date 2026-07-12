# Gentx validation

Every gentx submitted to join a launch is **validated server-side before it is stored** — a bad gentx is
rejected at submission (400) with a precise, per-invariant reason, and never persisted. coordd runs the
shared `gentxvalidate` library from seedward-libs (the same implementation the CLI and the browser use),
so the rules never drift between client and server.

## What is checked (`RunAll`)

coordd runs the **full** invariant set (server-grade), not the browser's advisory subset:

| Invariant                                      | Checks                                                               |
|------------------------------------------------|----------------------------------------------------------------------|
| `well_formed`                                  | exactly one `MsgCreateValidator`, decodable structure                |
| `bond_denom`                                   | the self-delegation is in the launch's bond denom                    |
| `self_delegation`                              | ≥ the launch's floor (see below)                                     |
| `commission_consistency` / `commission_bounds` | rate ≤ max-rate ≤ 1; within the launch's commission bounds           |
| `moniker`                                      | non-empty, ≤ 70 bytes, valid UTF-8, no control chars                 |
| `operator_address`                             | derives from the signing account under the launch's bech32 prefix    |
| `consensus_pubkey`                             | present and well-formed                                              |
| `signature_direct` / `signature_amino_json`    | the gentx **signature verifies** (the heavy check the browser omits) |

## Derived identity

On a pass, validation also **derives** the consensus pubkey and the **operator (validator) address** from
the gentx's signer — coordd trusts the *derived* validator identity, not anything self-declared in the
request. This is the cold operator identity, distinct from the hot submitter that signed the submission.

## Self-delegation floor

The self-delegation floor is applied by launch **type** — enforced for `MAINNET`,
`INCENTIVIZED_TESTNET`, and `PERMISSIONED` launches; a plain testnet may set none.

## Failure response

A failed gentx returns **400** with a structured body listing each failed invariant:

```json
{
  "error": {
    "code": "gentx_invalid",
    "message": "gentx validation failed: signature_direct",
    "invariants": [
      {
        "invariant": "signature_direct",
        "ok": false,
        "reason": "…"
      }
    ]
  }
}
```

The web renders this inline so a validator sees exactly which checks failed before resubmitting.
