# Proposals & M-of-N

Every governance decision in chaincoord goes through a **proposal**. A proposal is a signed, time-limited action that executes automatically when M coordinators sign it.

---

## How proposals work

1. Any committee member raises a proposal (`POST /launch/:id/proposal`). The proposer's signature is recorded implicitly — raising a proposal counts as a SIGN.
2. Other committee members review the proposal and submit a SIGN or VETO decision (`POST /launch/:id/proposal/:pid/sign`).
3. The proposal resolves when one of three things happens:
   - **Quorum reached:** M SIGN decisions collected → proposal executes immediately
   - **Vetoed:** any member submits VETO → proposal moves to `VETOED`, no execution
   - **Expired:** TTL elapses before quorum → proposal moves to `EXPIRED`

If M=1 (a 1-of-1 or 1-of-N committee), the proposal executes the moment it is raised.

---

## Proposal states

| State | Description |
|---|---|
| `PENDING_SIGNATURES` | Waiting for more signatures |
| `EXECUTED` | Quorum reached; action applied |
| `VETOED` | A member vetoed; no execution |
| `EXPIRED` | TTL elapsed before quorum |

---

## Action types

Every action is gated on the launch's status. Lifecycle transitions are enforced by the domain from-state checks; the
other actions carry explicit status guards (validator actions additionally check the target join-request state). The
server enforces the status columns below.

### Validator management

| Action | Effect | Enforced precondition |
|---|---|---|
| `APPROVE_VALIDATOR` | Approves a pending join request; adds validator to the approved set | Target join request is `pending` |
| `REJECT_VALIDATOR` | Rejects a pending join request with a reason | Target join request is `pending` |
| `REMOVE_APPROVED_VALIDATOR` | Revokes an already-approved validator | Target join request is `approved` **and** launch is `WINDOW_OPEN` or `WINDOW_CLOSED` |

`APPROVE`/`REJECT` aren't checked against launch status directly, but pending join requests only exist during
`WINDOW_OPEN` (they are expired when the window closes), so they apply only then. `REMOVE_APPROVED_VALIDATOR` is also
blocked once the genesis is published — revert to `WINDOW_CLOSED` via `REVISE_GENESIS` first.

### Lifecycle transitions

| Action | Transition | Required status (enforced) |
|---|---|---|
| `PUBLISH_CHAIN_RECORD` | `DRAFT` → `PUBLISHED` | `DRAFT` |
| `CLOSE_APPLICATION_WINDOW` | `WINDOW_OPEN` → `WINDOW_CLOSED` | `WINDOW_OPEN` |
| `PUBLISH_GENESIS` | `WINDOW_CLOSED` → `GENESIS_READY` | `WINDOW_CLOSED` |
| `REVISE_GENESIS` | `GENESIS_READY` → `WINDOW_CLOSED` | `GENESIS_READY` |

### Genesis metadata

Genesis-account changes are rejected once the genesis is published (`GENESIS_READY`, `LAUNCHED`, or `CANCELED`) — they
could no longer affect the published file. `UPDATE_GENESIS_TIME` is blocked only after `LAUNCHED` (it is designed to run
at `GENESIS_READY` as part of the revise flow, where it invalidates existing readiness confirmations).

| Action | Effect | Allowed status |
|---|---|---|
| `UPDATE_GENESIS_TIME` | Updates the `genesis_time` field; invalidates all readiness confirmations | any pre-`LAUNCHED` |
| `ADD_GENESIS_ACCOUNT` | Adds a pre-funded account to the genesis | before `GENESIS_READY` (address must be new) |
| `REMOVE_GENESIS_ACCOUNT` | Removes a pre-funded account | before `GENESIS_READY` (account must exist) |
| `MODIFY_GENESIS_ACCOUNT` | Changes amount or vesting schedule | before `GENESIS_READY` (account must exist) |

### Committee management

| Action | Effect |
|---|---|
| `REPLACE_COMMITTEE_MEMBER` | Swaps one member for another; if the replaced member was the lead, the replacement becomes the lead |
| `EXPAND_COMMITTEE` | Adds a new member; optionally sets a new threshold M |
| `SHRINK_COMMITTEE` | Removes a member; M must remain < N (liveness guard) |

---

## Signing a proposal

Each signature is a secp256k1 signature over the **canonical JSON** of the request with the `signature` and `pubkey_b64`
fields removed. The signed bytes therefore include the coordinator's address, the decision (`SIGN` or `VETO`), the
timestamp, and the `nonce` — the nonce is bound to the signature, so a captured request can't be replayed by swapping it.

The server verifies the signature against the member's declared public key, then consumes the `(coordinator, nonce)`
pair once for replay protection.

---

## Liveness guard

`EXPAND_COMMITTEE` and `SHRINK_COMMITTEE` proposals require the resulting threshold to satisfy `M < N` — strictly less than the new committee size. This keeps the committee able to reach quorum even if one member is permanently offline.

This guard applies only to committee **modification**. At launch creation (and when setting the committee on a `DRAFT` launch) any threshold from `1` to `N` is accepted, including `M = N` — so a deadlock-prone committee such as 3-of-3 *can* be created directly (it just can't be reached afterward via expand/shrink). A 1-of-1 committee (`M = N = 1`) is always valid, since there is no other member to lose.

---

## BFT voting power warning

When a validator is approved, the server recalculates the share of total committed self-delegation held by each operator. If any single entity reaches or exceeds 1/3 of the total, a warning is included in the approval response. The same check is enforced as a hard precondition when closing the application window — a launch cannot move to `WINDOW_CLOSED` if any entity holds ≥ 1/3 of voting power.

This is a structural check only. It does not account for stake that will be delegated after genesis.