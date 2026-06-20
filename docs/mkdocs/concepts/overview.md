# Concepts Overview

chaincoord coordinates the genesis launch of a **Cosmos SDK** chain between a group of coordinators and a set of
validator applicants. It provides a structured protocol, an HTTP API, and a tamper-evident audit log — but it does not
run a chain node, does not hold keys on behalf of anyone, and does not require trust in a central authority beyond the
declared committee.

---

## The problem

Launching a Cosmos SDK chain requires assembling a genesis file from validator contributions (`gentx`s), agreeing on its
content, and ensuring every participant starts with the same file at the same time. Doing this informally — over chat,
shared drives, or ad-hoc scripts — introduces opportunities for error, manipulation, and lack of accountability.

chaincoord makes the process explicit, auditable, and multi-party.

---

## Core building blocks

### Launch

A **Launch** is the top-level object that represents one chain's genesis coordination effort. It holds:

- The **chain record** — chain ID, binary name and version, denom, commission limits, deadlines
- The **committee** — who governs the launch and what threshold is required
- The **genesis accounts** — pre-funded accounts managed by committee proposal
- The current **lifecycle status**

A launch moves through a fixed sequence of states. No state can be skipped; all transitions are driven by committee
action (except opening the application window, which the lead coordinator triggers directly).

### Committee

A **Committee** is the M-of-N group of coordinators that governs a launch. Any member can raise a proposal; a proposal
executes when M members sign it. A single VETO from any member kills it immediately.

The committee is declared at launch creation and can be modified later via proposals (`REPLACE_COMMITTEE_MEMBER`,
`EXPAND_COMMITTEE`, `SHRINK_COMMITTEE`). There is always at least one member. At creation any threshold from 1 to N is
allowed (including M = N); the stricter liveness guard (M strictly less than N, so the committee can still act when one
member is absent) is enforced only when the committee is modified via `EXPAND_COMMITTEE` / `SHRINK_COMMITTEE`.

### Proposal

A **Proposal** is a signed, time-limited committee action. Every state transition and governance decision goes through a
proposal. See [Proposals & M-of-N](proposals.md) for the full list of action types and how signing works.

### Join Request

A **Join Request** is a validator's application to participate in a genesis. It carries:

- The validator's operator address and consensus public key
- Their `gentx` (the Cosmos SDK genesis transaction that creates their validator)
- Their peer address and RPC endpoint
- A secp256k1 signature over the full request payload

The server validates the `gentx` at submission against the launch's rules (chain ID, self-delegation floor, commission
bounds, consensus-key shape, signature) using a shared validation library — an invalid `gentx` is rejected with a
`gentx_invalid` error carrying a per-invariant breakdown of what failed. It does **not** invoke the chain binary; full
semantic validation happens when the coordinator runs `gaiad genesis collect-gentxs` during genesis assembly.

### Audit Log

Every state-changing event is appended to a JSONL file, with each entry signed by the server's Ed25519 key. Each entry
also carries a `prev_hash` field — the SHA-256 of the previous line — covered by the current entry's signature. The log
can be verified offline with `coordd audit verify` to confirm that entries have not been modified or deleted.
See [Audit Log](../reference/audit.md).

---

## What chaincoord does not do

- It does not run or connect to a chain node during the launch (monitoring polls CometBFT RPC only after
  `GENESIS_READY`)
- It does not store private keys for coordinators or validators
- It does not assemble the final genesis file — that step is done locally by the coordinator using
  `gaiad genesis collect-gentxs`
- It does not guarantee BFT safety beyond a warning when a single entity reaches or exceeds 1/3 of committed voting
  power (and a hard precondition that blocks closing the window in that case)

---

## Further reading

- [Roles](roles.md) — coordinator, lead coordinator, validator
- [Launch Lifecycle](lifecycle.md) — the seven states in detail
- [Proposals & M-of-N](proposals.md) — how committee decisions work