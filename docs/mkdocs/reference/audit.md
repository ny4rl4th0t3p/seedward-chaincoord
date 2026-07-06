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

| Field         | Type                 | Description                                                                                                                |
|---------------|----------------------|----------------------------------------------------------------------------------------------------------------------------|
| `launch_id`   | string (UUID)        | The launch this event belongs to                                                                                           |
| `event_name`  | string               | Event type (see table below)                                                                                               |
| `occurred_at` | RFC3339 timestamp    | When the event occurred                                                                                                    |
| `payload`     | object               | Event-specific data                                                                                                        |
| `prev_hash`   | string (hex SHA-256) | SHA-256 of the previous line's raw JSON bytes. Empty for the first entry in each server session. Covered by the signature. |
| `signature`   | string (base64)      | Ed25519 signature over canonical JSON of the entry *without* the `signature` field (but *with* `prev_hash`)                |

Timestamps are monotonically non-decreasing within a log file. A timestamp that moves backward is flagged as an anomaly
by `coordd audit verify`.

---

## Event types

| Event                     | Trigger                                                                               |
|---------------------------|---------------------------------------------------------------------------------------|
| `LaunchCreated`           | Launch created — committee and chain record set                                       |
| `ChainRecordPublished`    | `PUBLISH_CHAIN_RECORD` proposal executed — launch moves to `PUBLISHED`                |
| `WindowOpened`            | Lead coordinator calls `POST /launch/:id/open-window` — launch moves to `WINDOW_OPEN` |
| `WindowClosed`            | `CLOSE_APPLICATION_WINDOW` proposal executed — launch moves to `WINDOW_CLOSED`        |
| `InitialGenesisUploaded`  | Initial genesis file uploaded or registered via attestor URL                          |
| `FinalGenesisUploaded`    | Final genesis file uploaded or registered via attestor URL                            |
| `GenesisPublished`        | `PUBLISH_GENESIS` proposal executed — launch moves to `GENESIS_READY`                 |
| `GenesisTimeUpdated`      | `UPDATE_GENESIS_TIME` proposal executed                                               |
| `GenesisRevisionApproved` | `REVISE_GENESIS` proposal executed — launch reverts to `WINDOW_CLOSED`                |
| `ValidatorApproved`       | `APPROVE_VALIDATOR` proposal executed                                                 |
| `ValidatorRejected`       | `REJECT_VALIDATOR` proposal executed                                                  |
| `ValidatorRemoved`        | `REMOVE_APPROVED_VALIDATOR` proposal executed                                         |
| `AllocationFileUploaded`  | Allocation file uploaded or registered via attestor URL (status → `PENDING`)          |
| `AllocationFileApproved`  | `APPROVE_ALLOCATION_FILE` proposal executed — file approved                           |
| `AllocationFileRejected`  | `APPROVE_ALLOCATION_FILE` proposal vetoed — file rejected                             |
| `LaunchCancelled`         | Lead coordinator cancels the launch                                                   |
| `LaunchDetected`          | Block monitor observes block 1 — launch moves to `LAUNCHED`                           |
| `RehearsalResultRecorded` | A signature-verified rehearsal result is recorded via the bridge (`POST .../rehearsal-results`); payload carries the outcome, input-set hash, and a `stale` flag |
| `RehearsalAttemptReset`   | A coordinator force-releases a stuck rehearsal run lease (`POST .../rehearsal/{attempt_id}/reset`) |

!!! note
Proposal actions that do not directly transition launch state (e.g. `REPLACE_COMMITTEE_MEMBER`, `EXPAND_COMMITTEE`,
`SHRINK_COMMITTEE`) are recorded in the database but do not produce audit log entries in the current implementation.
Members-list changes (`POST`/`DELETE /launch/{id}/members`) are likewise persisted with add-provenance
(`added_by`/`added_at`) but do not emit a domain event, so they do not appear in the audit log.

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