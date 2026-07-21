# Proposals & M-of-N

Every governance decision in seedward-chaincoord goes through a **proposal**. A proposal is a signed, time-limited
action that executes automatically when M committee members sign it.

---

## How proposals work

1. Any committee member raises a proposal (`POST /api/v1/launch/:id/proposal`). The proposer's signature is recorded
   implicitly — raising a proposal counts as a SIGN.
2. Other committee members review the proposal and submit a SIGN or VETO decision (
   `POST /api/v1/launch/:id/proposal/:pid/sign`).
3. The proposal resolves when one of three things happens:
    - **Quorum reached:** M SIGN decisions collected → proposal executes immediately
    - **Vetoed:** any member submits VETO → proposal moves to `VETOED`, no execution
    - **Expired:** TTL elapses before quorum → proposal moves to `EXPIRED`

If M=1 (a 1-of-1 or 1-of-N committee), the proposal executes the moment it is raised.

---

## Proposal states

| State                | Description                    |
|----------------------|--------------------------------|
| `PENDING_SIGNATURES` | Waiting for more signatures    |
| `EXECUTED`           | Quorum reached; action applied |
| `VETOED`             | A member vetoed; no execution  |
| `EXPIRED`            | TTL elapsed before quorum      |

---

## Action types

Every action is gated on the launch's status. Lifecycle transitions are enforced by the domain from-state checks; the
other actions carry explicit status guards (validator actions additionally check the target join-request state). The
server enforces the status columns below.

### Validator management

| Action                      | Effect                                                              | Enforced precondition                                                                |
|-----------------------------|---------------------------------------------------------------------|--------------------------------------------------------------------------------------|
| `APPROVE_VALIDATOR`         | Approves a pending join request; adds validator to the approved set | Target join request is `pending`                                                     |
| `REJECT_VALIDATOR`          | Rejects a pending join request with a reason                        | Target join request is `pending`                                                     |
| `REMOVE_APPROVED_VALIDATOR` | Revokes an already-approved validator                               | Target join request is `approved` **and** launch is `WINDOW_OPEN` or `WINDOW_CLOSED` |

`APPROVE`/`REJECT` aren't checked against launch status directly, but pending join requests only exist during
`WINDOW_OPEN` (they are expired when the window closes), so they apply only then. `REMOVE_APPROVED_VALIDATOR` is also
blocked once the genesis is published — revert to `WINDOW_CLOSED` via `REVISE_GENESIS` first.

### Lifecycle transitions

| Action                     | Transition                        | Required status (enforced) |
|----------------------------|-----------------------------------|----------------------------|
| `PUBLISH_CHAIN_RECORD`     | `DRAFT` → `PUBLISHED`             | `DRAFT`                    |
| `CLOSE_APPLICATION_WINDOW` | `WINDOW_OPEN` → `WINDOW_CLOSED`   | `WINDOW_OPEN`              |
| `PUBLISH_GENESIS`          | `WINDOW_CLOSED` → `GENESIS_READY` | `WINDOW_CLOSED`            |
| `REVISE_GENESIS`           | `GENESIS_READY` → `WINDOW_CLOSED` | `GENESIS_READY`            |
| `CANCEL_LAUNCH`            | any non-terminal → `CANCELED`     | any non-terminal           |

`CANCEL_LAUNCH` is the **governed** cancel path (see the direct/proposal split under
[Not everything is a proposal](../decisions/index.md#not-everything-is-a-proposal)). It is the *only*
way to cancel once a launch is past `PUBLISHED`, and it stays available in `DRAFT`/`PUBLISHED` too so a
non-lead committee member can still initiate a governed cancel. Canceling from `GENESIS_READY`
invalidates existing readiness confirmations, exactly as the direct path did.

### Genesis metadata & allocations

`UPDATE_GENESIS_TIME` is blocked once the launch is terminal (`LAUNCHED` or `CANCELED`); it is designed to run at
`GENESIS_READY` as part of the revise flow, where it invalidates existing readiness confirmations.
`APPROVE_ALLOCATION_FILE` is rejected once the genesis is
published (`GENESIS_READY`, `LAUNCHED`, or `CANCELED`) — an approval could no longer affect the published file.

| Action                    | Effect                                                                    | Allowed status          |
|---------------------------|---------------------------------------------------------------------------|-------------------------|
| `UPDATE_GENESIS_TIME`     | Updates the `genesis_time` field; invalidates all readiness confirmations | any non-terminal status |
| `APPROVE_ALLOCATION_FILE` | Approves the curated allocation file of one type, bound to its SHA-256    | before `GENESIS_READY`  |

#### Allocation files

Genesis allocations (`accounts`, `claims`, `grants`, `authz`, `feegrant`) are governed as **whole files**, not
per-entry. A committee member uploads the curated file for a type (`POST /api/v1/launch/{id}/allocations/{type}`, dual-mode
like genesis: attestor URL+hash or host bytes); it lands in `PENDING`. The content is **opaque** to coordd — gentool
emits CSV/TSV, not JSON — so the server stores and hashes the bytes but does not parse them.

Each file is then governed by its own `APPROVE_ALLOCATION_FILE` proposal, carrying `{type, hash}`:

- The payload `hash` must equal the file's **current** SHA-256 when the proposal executes. If the file was re-uploaded
  in the meantime (new hash), execution fails — you cannot approve bytes that have since changed.
- On quorum the file becomes `APPROVED` (bound to the executing proposal). A single **VETO** marks it `REJECTED`.
- Re-uploading a corrected file resets it to `PENDING` for a fresh approval, invalidating any prior decision.

This supersedes the old per-entry `ADD`/`REMOVE`/`MODIFY_GENESIS_ACCOUNT` proposals, which no longer exist — curated
files are reviewed and approved as a unit by humans, with the hash as the integrity anchor.

### Finalizing genesis: consistency guards + the optional rehearsal gate

`PUBLISH_GENESIS` carries safety beyond the status guard, because the approved set can still change in
`WINDOW_CLOSED` (a validator approve/remove) after a final genesis was uploaded:

- **Genesis ↔ approved-set consistency (always on).** The final genesis is bound at upload to an
  `input_set_hash` over the approved gentxs + allocation files + chain params. `PUBLISH_GENESIS` re-checks that
  hash at both raise and execute and refuses a genesis that no longer matches the current set. While a
  `PUBLISH_GENESIS` is pending, validator approve/remove proposals are frozen (and vice-versa), so the set
  cannot drift underneath it.
- **Rehearsal gate (opt-in).** `COORD_REHEARSAL_GATE` (default `off`) can additionally require the latest
  rehearsal to be a current `PASS` before the transition is allowed — see
  [`rehearsal_gate`](../reference/setup.md) in the configuration reference.

### Committee management

| Action                     | Effect                                                                                              |
|----------------------------|-----------------------------------------------------------------------------------------------------|
| `REPLACE_COMMITTEE_MEMBER` | Swaps one member for another; if the replaced member was the lead, the replacement becomes the lead |
| `EXPAND_COMMITTEE`         | Adds a new member; optionally sets a new threshold M                                                |
| `SHRINK_COMMITTEE`         | Removes a member; M must remain < N (liveness guard)                                                |

---

## Signing a proposal

Each signature is a secp256k1 signature over the **canonical JSON** of the request with the `signature` and `pubkey_b64`
fields removed. The signed bytes therefore include the committee member's address, the decision (`SIGN` or `VETO`), the
timestamp, and the `nonce` — the nonce is bound to the signature, so a captured request can't be replayed by swapping
it.

The server verifies the signature against the `pubkey_b64` carried in the request, requiring that pubkey to hash to the
signer's committee address (so it can't be spoofed, and it works under any address prefix) — a member registers no key
in advance; they are recognised when they sign. It then consumes the `(member, nonce)` pair once for replay
protection.

---

## Liveness guard

`EXPAND_COMMITTEE` and `SHRINK_COMMITTEE` proposals require the resulting threshold to satisfy `M < N` — strictly less
than the new committee size. This keeps the committee able to reach quorum even if one member is permanently offline.

This guard applies only to committee **modification**. At launch creation (and when setting the committee on a `DRAFT`
launch) any threshold from `1` to `N` is accepted, including `M = N` — so a deadlock-prone committee such as 3-of-3
*can* be created directly (it just can't be reached afterward via expand/shrink). A 1-of-1 committee (`M = N = 1`) is
always valid, since there is no other member to lose.

---

## Known limitation — a member can veto their own removal

The single-VETO kill switch is deliberate — any member with a strong objection can block a proposal — but
it has a sharp edge. The **only** veto exclusion is that the proposer can't veto their own proposal (raising
it implicitly casts their SIGN). There is **no exclusion for the target of a removal**: a member removed via
`SHRINK_COMMITTEE` / `REPLACE_COMMITTEE_MEMBER` (raised by someone else) is still a committee member at vote
time, so **they can veto their own removal** and remain. Past `PUBLISHED` they can also veto `CANCEL_LAUNCH`,
so a single rogue member can **freeze a launch**: it can't advance, they can't be removed, and it can't be
canceled (proposals TTL-expire after 48 h, but re-raising just gets re-vetoed).

This is a **safety-over-liveness tradeoff, not an oversight**. Forbidding self-veto would let a malicious
*majority* eject an honest minority member to clear the way for something that member would have blocked —
there is no free lunch. The design accepts the griefing risk because:

- A rogue can only **block**, never execute or steal — the worst case is a denial of service on *this*
  launch, and VETO is observable and audited, so misuse is visible and reputationally costly.
- The committee is a **small, chosen set for a time-boxed launch**, not an open-membership DAO — pick
  members you trust.
- **Escape hatches by stage:** in `DRAFT` the lead can `SetCommittee` (a direct, wholesale reconfigure — not
  a vetoable proposal) and drop the rogue; in `DRAFT`/`PUBLISHED` the lead can direct-cancel. From
  `WINDOW_OPEN` on there is no in-launch escape — abandon the launch and start a fresh one (a rogue can kill
  *this* launch but cannot hijack it or carry anything over).

---

## BFT voting power warning

When a validator is approved, the server recalculates the share of total committed self-delegation held by each
operator. If any single entity reaches or exceeds 1/3 of the total, a warning is included in the approval response. The
same check is enforced as a hard precondition when closing the application window — a launch cannot move to
`WINDOW_CLOSED` if any entity holds ≥ 1/3 of voting power.

This is a structural check only. It does not account for stake that will be delegated after genesis.