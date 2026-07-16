# Concepts Overview

seedward-chaincoord coordinates the genesis launch of a **Cosmos SDK** chain between a group of committee members and a
set
of
validator applicants. It provides a structured protocol, an HTTP API, and a tamper-evident audit log — but it does not
run a chain node, does not hold keys on behalf of anyone, and does not require trust in a central authority beyond the
declared committee.

---

## The problem

Launching a Cosmos SDK chain requires assembling a genesis file from validator contributions (`gentx`s), agreeing on its
content, and ensuring every participant starts with the same file at the same time. Doing this informally — over chat,
shared drives, or ad-hoc scripts — introduces opportunities for error, manipulation, and lack of accountability.

seedward-chaincoord makes the process explicit, auditable, and multi-party.

---

## Core building blocks

### Launch

A **Launch** is the top-level object that represents one chain's genesis coordination effort. It holds:

- The **chain record** — chain ID, binary name and version, denom, commission limits, deadlines
- The **committee** — who governs the launch and what threshold is required
- The **allocation files** — curated genesis allocations (accounts/claims/grants/authz/feegrant), each approved as a
  whole file by committee proposal
- The current **lifecycle status**

A launch moves through a fixed sequence of states. No state can be skipped; all transitions are driven by committee
action (except opening the application window, which any committee member triggers directly).

### Committee

A **Committee** is the M-of-N group of committee members that governs a launch. Any member can raise a proposal; a
proposal
executes when M members sign it. A single VETO from any member kills it immediately.

The committee is declared at launch creation and can be modified later via proposals (`REPLACE_COMMITTEE_MEMBER`,
`EXPAND_COMMITTEE`, `SHRINK_COMMITTEE`). There is always at least one member. At creation any threshold from 1 to N is
allowed (including M = N); the stricter liveness guard (M strictly less than N, so the committee can still act when one
member is absent) is enforced only when the committee is modified via `EXPAND_COMMITTEE` / `SHRINK_COMMITTEE`.

### Proposal

A **Proposal** is a signed, time-limited committee action. Every governance decision — and every state transition except
opening the application window — goes through a proposal. See [Proposals & M-of-N](proposals.md) for the full list of
action types and how signing works.

### Membership & privacy

Every launch is **private-always**: visible to — and submittable by — exactly its **committee** plus the addresses on
its **members list** (per-launch hot addresses and labels, managed directly by the committee). A leaked URL and a fresh
address see nothing (`404`). See [Roles → Membership](roles.md#membership).

### Join Request

A **Join Request** is a validator's application to participate in a genesis, submitted by a **member** (its hot actor
address; the on-chain validator/operator address is **derived from the gentx's signer**, not a self-declared field). It
carries:

- Their `gentx` (the Cosmos SDK genesis transaction that creates their validator) — the consensus public key and the
  operator address are taken from it (the operator address by deriving it from the signing key)
- Their peer address and RPC endpoint
- A secp256k1 signature over the full request payload

The server validates the `gentx` at submission against the launch's rules (chain ID, self-delegation floor, commission
bounds, consensus-key shape, signature) using a shared validation library — an invalid `gentx` is rejected with a
`gentx_invalid` error carrying a per-invariant breakdown of what failed. It does **not** invoke the chain binary; full
semantic validation happens when a committee member runs `gaiad genesis collect-gentxs` during genesis assembly.

### Audit Log

Every state-changing event is appended to a JSONL file, with each entry signed by the server's Ed25519 key. Each entry
also carries a `prev_hash` field — the SHA-256 of the previous line — covered by the current entry's signature. The log
can be verified offline with `coordd audit verify` to confirm that entries have not been modified or deleted.
See [Audit Log](../reference/audit.md).

### Rehearsal (pre-flight)

Before publishing the final genesis, the committee can **pre-flight** the approved input set: an external **rehearsal
service** assembles a candidate genesis, boots an ephemeral chain on substitute validators, runs assertions, tears it
down, and posts back a **signed** PASS/FAIL result. `coordd` never boots a chain — it exposes an ops-plane **bridge**
(`/bridge/*`) that serves the approved input, mints a single-writer **claim** so two runs can't collide, and records the
signed result (rejecting any it didn't itself serve). The committee reads results back at `GET /launch/{id}/rehearsal`.
A PASS certifies the input set assembles and a representative chain advances — it is **not** a guarantee the real
network produces blocks. (Rehearsal defaults to off and is manually triggered; the opt-in gate can require a current
passing rehearsal before publishing genesis — advisory records the check, required blocks — with auto-triggering a later
addition.)

---

## What seedward-chaincoord does not do

- It does not run or connect to a chain node during the launch (monitoring polls CometBFT RPC only after
  `GENESIS_READY`)
- It does not store private keys for committee members or validators
- It does not assemble the final genesis file — that step is done locally by a committee member using
  `gaiad genesis collect-gentxs`
- It does not guarantee BFT safety beyond a warning when a single entity reaches or exceeds 1/3 of committed voting
  power (and a hard precondition that blocks closing the window in that case)

---

## Further reading

- [Roles](roles.md) — coordinator, committee member, lead, validator
- [Launch Lifecycle](lifecycle.md) — the seven states in detail
- [Proposals & M-of-N](proposals.md) — how committee decisions work