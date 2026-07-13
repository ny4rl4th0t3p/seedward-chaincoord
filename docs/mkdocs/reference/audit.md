# Audit Log

`coordd` appends an entry to a JSONL (newline-delimited JSON) file for every state-changing event. Each entry is signed
with the server's Ed25519 key and carries a `prev_hash` field containing the SHA-256 of the previous line's raw bytes,
covered by the current entry's signature. A modified entry fails signature verification. A deleted entry breaks the hash
chain — the next entry's `prev_hash` will not match the actual previous line. The chain tip is persisted to the database
so deletions between server restarts are caught at startup; the server refuses to start if the log and database
disagree.

---

## Entry format

Each line is a JSON object:

```json
{
  "launch_id": "<uuid>",
  "event_name": "ValidatorApproved",
  "occurred_at": "2026-04-13T10:00:00Z",
  "payload": {
    ...
  },
  "prev_hash": "<sha256-hex of previous line's JSON bytes>",
  "signature": "<base64 Ed25519 sig>"
}
```

| Field         | Type                 | Description                                                                                                                                                                         |
|---------------|----------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `launch_id`   | string (UUID)        | The launch this event belongs to                                                                                                                                                    |
| `event_name`  | string               | Event type (see table below)                                                                                                                                                        |
| `occurred_at` | RFC3339 timestamp    | When the event occurred                                                                                                                                                             |
| `payload`     | object               | Event-specific data                                                                                                                                                                 |
| `prev_hash`   | string (hex SHA-256) | SHA-256 of the previous line's raw JSON bytes. Empty only for the very first entry in the log; across restarts it continues from the persisted chain tip. Covered by the signature. |
| `signature`   | string (base64)      | Ed25519 signature over canonical JSON of the entry *without* the `signature` field (but *with* `prev_hash`)                                                                         |

Timestamps are monotonically non-decreasing within a log file. A timestamp that moves backward is flagged as an anomaly
by `coordd audit verify`.

---

## Event types

| Event                        | Trigger                                                                                                                                                          |
|------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `LaunchCreated`              | Launch created — committee and chain record set                                                                                                                  |
| `ChainRecordPublished`       | `PUBLISH_CHAIN_RECORD` proposal executed — launch moves to `PUBLISHED`                                                                                           |
| `WindowOpened`               | A committee member calls `POST /launch/:id/open-window` — launch moves to `WINDOW_OPEN`                                                                          |
| `WindowClosed`               | `CLOSE_APPLICATION_WINDOW` proposal executed — launch moves to `WINDOW_CLOSED`                                                                                   |
| `InitialGenesisUploaded`     | Initial genesis file uploaded or registered via attestor URL                                                                                                     |
| `FinalGenesisUploaded`       | Final genesis file uploaded or registered via attestor URL                                                                                                       |
| `GenesisPublished`           | `PUBLISH_GENESIS` proposal executed — launch moves to `GENESIS_READY`                                                                                            |
| `GenesisTimeUpdated`         | `UPDATE_GENESIS_TIME` proposal executed                                                                                                                          |
| `GenesisRevisionApproved`    | `REVISE_GENESIS` proposal executed — launch reverts to `WINDOW_CLOSED`                                                                                           |
| `ValidatorApproved`          | `APPROVE_VALIDATOR` proposal executed                                                                                                                            |
| `ValidatorRejected`          | `REJECT_VALIDATOR` proposal executed                                                                                                                             |
| `ValidatorRemoved`           | `REMOVE_APPROVED_VALIDATOR` proposal executed                                                                                                                    |
| `CommitteeMemberReplaced`    | `REPLACE_COMMITTEE_MEMBER` proposal executed — payload carries the committee membership and threshold before and after                                           |
| `CommitteeExpanded`          | `EXPAND_COMMITTEE` proposal executed — payload carries the committee membership and threshold before and after                                                   |
| `CommitteeShrunk`            | `SHRINK_COMMITTEE` proposal executed — payload carries the committee membership and threshold before and after                                                   |
| `AllocationFileUploaded`     | Allocation file uploaded or registered via attestor URL (status → `PENDING`)                                                                                     |
| `AllocationFileApproved`     | `APPROVE_ALLOCATION_FILE` proposal executed — file approved                                                                                                      |
| `AllocationFileRejected`     | `APPROVE_ALLOCATION_FILE` proposal vetoed — file rejected                                                                                                        |
| `LaunchCancelled`            | Lead coordinator cancels the launch                                                                                                                              |
| `LaunchDetected`             | Block monitor observes block 1 — launch moves to `LAUNCHED`                                                                                                      |
| `RehearsalResultRecorded`    | A signature-verified rehearsal result is recorded via the bridge (`POST .../rehearsal-results`); payload carries the outcome, input-set hash, and a `stale` flag |
| `RehearsalAttemptReset`      | A coordinator force-releases a stuck rehearsal run lease (`POST .../rehearsal/{attempt_id}/reset`)                                                               |
| `RehearsalServiceKeyChanged` | A committee member changes the launch's trusted rehearsal service key via `PATCH /launch/{id}` — payload carries the old and new keys                            |
| `JoinRequestSubmitted`       | A validator submits a join request (`POST /launch/{id}/join`) — payload carries the join-request ID and the operator and submitter addresses                     |
| `ReadinessConfirmed`         | A validator confirms readiness (`POST /launch/{id}/readiness`) — payload carries the operator address                                                            |

Admin-plane events — `CoordinatorAdded`, `CoordinatorRemoved` (coordinator allowlist) and `SessionsRevoked`
(session revocation) — have no launch, so they are recorded under the **`global`** scope (see below).
Proposal execution is recorded in two phases: `ProposalExecuting` (intent) and `ProposalExecutionAborted`
(see [Two-phase proposal execution](#two-phase-proposal-execution)).

!!! note "Not yet in the audit log"
Some non-transition mutations are persisted but do not yet emit an audited event: members-list changes
(`POST`/`DELETE /launch/{id}/members`, kept with `added_by`/`added_at` provenance), the initial DRAFT
committee (`SetCommittee`), and other DRAFT chain-field patches (`monitor_rpc_url` / `rehearsal_endpoint`).
These are tracked follow-ups.

---

## Scopes: launch and global

Every entry carries a `launch_id`. Most events are scoped to a specific launch; admin-plane actions have no
launch and use the sentinel `launch_id` value **`global`** — `CoordinatorAdded`, `CoordinatorRemoved`, and
`SessionsRevoked`. They ride the same hash-chained, signed log (and are covered by `coordd audit verify`);
filter them with `launch_id == "global"`.

## Two-phase proposal execution

Governance actions (M-of-N proposals) are audited in **two phases**, so a critical action can never execute
unaudited even if the audit backend fails:

1. **Intent** — before a quorum-reached proposal's state change is committed, coordd writes `ProposalExecuting`.
   If this write fails, the proposal is **aborted** and never executes — no unaudited governance. Nothing
   committed and nothing was logged, so there is no entry at all.
2. **Completion** — after the change commits, coordd writes the action's event (e.g. `CommitteeExpanded`). If
   this write fails, the intent is already recorded and the change has committed, so coordd **logs at fatal
   level and stops the process** rather than run on in an unauditable state; the operator restarts after fixing
   the audit backend. No completion event — and **no aborted entry** — is written; the lone intent plus the
   halted process are the signal.

The `ProposalExecutionAborted` entry belongs to a **different** case: if the execution transaction itself fails
and rolls back *after* the intent was recorded, there is no completion phase — instead coordd writes
`ProposalExecutionAborted` (best-effort, log-and-continue), so the pair `ProposalExecuting` +
`ProposalExecutionAborted` self-explains: the action was attempted and did not happen. (A completion-write
failure never produces this entry — it stops the process instead.)

Cross-checked with the launch's state, the entries present in the log tell you exactly what happened:

| Scenario                | Entries in the log                               | Launch state | Reading                                                       |
|-------------------------|--------------------------------------------------|--------------|---------------------------------------------------------------|
| Executed cleanly        | `ProposalExecuting` + completion event           | executed     | executed and fully audited                                    |
| Execution rolled back   | `ProposalExecuting` + `ProposalExecutionAborted` | not executed | it **did not** happen — the aborted entry closes the intent   |
| Completion write failed | `ProposalExecuting` only (process stopped)       | executed     | it **did** happen — lone intent + halted process flag the gap |
| Intent write failed     | none                                             | not executed | never executed; nothing committed, nothing logged             |

## Audit log vs operational log

The audit log is the **forensic** record — tamper-evident, signed, hash-chained, verified offline; it is not
meant for live monitoring. For a live view, `coordd` also emits an **operational** log to stderr (level set by
`log_level`): one `INFO` line per recorded action, plus per-request access logs. Watch the operational log to
see the system work; read the audit log to prove what happened.

---

## Signature verification

The signature covers the canonical JSON of the entry with the `signature` field omitted (`prev_hash` is included).
Canonical JSON means:

- Keys sorted lexicographically
- No insignificant whitespace
- Timestamps serialised as RFC3339 UTC

The server's Ed25519 public key is available at `GET /audit/pubkey` on any running `coordd` instance.

---

## Offline verification with `coordd audit verify`

```bash
# Fetch the pubkey from a live server and verify
coordd audit verify \
  --file audit.jsonl \
  --server-url http://coordd:8080

# Verify with an explicit pubkey (fully offline)
coordd audit verify \
  --file audit.jsonl \
  --pubkey <base64-ed25519-pubkey>

# Structural check only (no signature verification)
coordd audit verify --file audit.jsonl
```

**What the command checks:**

1. Every line is valid JSON
2. Required fields are present (`launch_id`, `event_name`, `occurred_at`, `payload`)
3. Timestamps are monotonically non-decreasing
4. Ed25519 signatures are valid (when a public key is supplied)
5. `prev_hash` of each entry matches the SHA-256 of the previous line (where present)

**Output example:**

```
entries:    142
time range: 2026-04-01T08:00:00Z → 2026-04-13T10:00:00Z
signatures: verified (where present)
chain:      verified (where present)
result:     OK — no anomalies found
```

Exit code is `0` on success, non-zero if any anomaly is found.

---

## Keeping the log

The audit log is append-only by design. `coordd` never modifies or truncates it. Back it up alongside your database —
the two together form the complete record of a launch.

For archival purposes, the log is human-readable and requires no special tooling beyond `coordd audit verify` and a
standard JSON processor (`jq`).