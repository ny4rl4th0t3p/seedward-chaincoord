# Design decisions

Architecture and internal decisions for chaincoord (the coordination server, `coordd`). The
cross-cutting coordination model — the [launch lifecycle](../concepts/lifecycle.md) state machine and
the M-of-N committee proposal governance — is recorded in the suite ADR log; this section records
chaincoord's own architecture and the choices that only matter inside this codebase.

## Architecture

### Hexagonal / DDD

Business rules live in pure domain aggregates (`internal/domain/{launch,proposal,joinrequest}`); the
application services (`LaunchService`, `ProposalService`, …) orchestrate them through **ports**
(repository + service interfaces); infrastructure (HTTP, sqlite, crypto, audit) implements the ports.
Domain events are dispatched by the application layer **after** the transaction commits — never from
inside the domain.

### Launch is the aggregate root

The `Launch` aggregate owns the `Committee` (M-of-N, no independent lifecycle) and the `Allowlist`, plus
readiness confirmations, allocation files, and rehearsal config. `JoinRequest` and `Proposal` are
**sibling** aggregates that reference a launch by UUID only — not nested inside it. Each aggregate keeps a
small transaction boundary while the launch stays the single consistency root for its own committee and
configuration.

## Committee & proposals — internal specifics

### Action types

Twelve proposal action types (`internal/domain/proposal/payload.go`), each with a typed payload:
`APPROVE_VALIDATOR`, `REJECT_VALIDATOR`, `REMOVE_APPROVED_VALIDATOR`, `APPROVE_ALLOCATION_FILE`,
`PUBLISH_CHAIN_RECORD`, `CLOSE_APPLICATION_WINDOW`, `PUBLISH_GENESIS`, `UPDATE_GENESIS_TIME`,
`REPLACE_COMMITTEE_MEMBER`, `REVISE_GENESIS`, `EXPAND_COMMITTEE`, `SHRINK_COMMITTEE`.

### Not everything is a proposal

`OpenWindow` and `Cancel` are **direct** API calls, not proposals. `OpenWindow` auto-publishes from
`DRAFT` when the initial genesis hash is already present (a single-coordinator convenience). `Cancel` is
**lead-only** (`ErrForbidden` for a non-lead) and publishes `LaunchCancelled` directly — it is *not* an
M-of-N vote.

### Proposal TTL

Proposals expire after **48 h** (`defaultProposalTTL`); a background job scans pending proposals and
expires the stale ones.

### Quorum & veto mechanics

Execution counts `SIGN` entries against `ThresholdM`; each coordinator signs at most once; a single
`VETO` short-circuits to `VETOED`. Vetoing an `APPROVE_ALLOCATION_FILE` is the **only** veto with a side
effect — it marks the bound file `REJECTED` and emits `AllocationFileRejected`.

### Events

Most executed actions emit one domain execution event; the committee-resize actions
(`REPLACE/EXPAND/SHRINK_COMMITTEE`) emit **none** — the new committee is persisted directly.

### Committee-resize safety

A resize first **expires all pending proposals** for the launch (`ExpireAllPending`) — they were sized
against the old threshold. When `new_threshold_m` is omitted, M is clamped to `[1, newN-1]`
(`ResolveThreshold`). The `M < N` liveness guard is enforced **only** on expand/shrink; committee
*creation* allows `M = N`.

### Sentinel errors

Domain errors are exported sentinels matched with `errors.Is` and mapped to HTTP status by
`mapLaunchDomainErr` / `mapProposalDomainErr` (invalid transition / insufficient validators / dominant
voting power / genesis-hash-required / committee-member-not-found|exists → 400/404/409; proposal
not-pending / TTL-expired / already-signed → 409). Callers and tests distinguish failure kinds by the
sentinel, never by string matching.

Construction-time validation follows suit: `ChainRecord.Validate` / `launch.New` return per-field sentinels
(`ErrChainIDRequired`, `ErrCommitteeThresholdRange`, …) so tests pin the exact cause and `CreateLaunch` maps
them to `400`, not `500`. At the storage layer, a SQLite unique-index violation in
`JoinRequestRepository.Save` (the active-validator / consensus-key race backstop, past the service's
pre-checks) maps to `ErrConflict` → `409` via `isConstraintViolation`, not a raw `500`.

## Authentication, identity, and membership

The auth *model* (ADR-036 challenge-response, **HRP-independent account identity** — ADR-0011 + ADR-0024),
the membership/visibility model, and the submitter≠validator identity split are suite ADRs; the mechanics
below are chaincoord-internal.

### HRP-independent identity (the hot side)

Identity is the 20-byte account, not the bech32 string, so a key authenticates under any account prefix as
one identity. Authorization compares on the account
(`launch.AccountID.Equal`), and identity state is keyed on it: challenge + nonce (per-account replay
protection), the `operator_revocations` fence, the admin set, and the coordinator allowlist. Launch-scoped
addresses (members, committee, join-request submitter + operator) are stored **under the launch's bech32
prefix**; global identities (coordinator allowlist, revocation fence) as the **account hex** — a startup
backfill canonicalizes existing rows. `GET /launch/{id}/chain-hint` is gated behind the visibility check
(404 for non-members): a validator authenticates first, then reads the launch prefix for their gentx.

### Challenge / nonce / sessions

- Auth challenges have a **5-min TTL**; an unexpired challenge for an operator is reused (conditional
  upsert) and consumed once on verification.
- **Replay protection:** each `(operator, nonce)` is consumed once, with a **10-min** nonce TTL (must
  exceed the signed-payload timestamp skew); the nonce is carried in the signed canonical bytes and
  consumed before the challenge check.
- **Challenge rate limits:** 10/IP/min (chi `httprate`) + 5/operator/5-min.
- **Sessions** are stateless Ed25519 JWTs, **1 h** TTL. No per-token revocation; bulk revocation uses an
  `operator_revocations` fence table (`RevokeAllForOperator`), exposed as `DELETE /auth/sessions/all`
  (self) and `DELETE /admin/sessions/{address}` (admin).
- `POST /auth/verify` returns a **uniform 401** for every failure (anti-enumeration), bypassing the normal
  error responder.

### Members API & storage

- `POST` (201) / `DELETE` (204) / `GET` (committee-only array with provenance); authz ladder
  401 → 404 (missing) → 403 (non-committee) → 409 (frozen); labels capped at 128 chars; editable only in
  DRAFT/PUBLISHED/WINDOW_OPEN.
- Storage **reused the existing `allowlist` table + name** (a rename to "members" was declined as cosmetic
  churn); a `label` column + `added_by`/`added_at` provenance were added by migration. The domain type is
  `Allowlist` with `Member{Address, Label, AddedBy, AddedAt}`.

### Submission cap & dedup

- Per-submitter cap of **50** open submissions (`CountBySubmitter`). Dedup keys on the derived validator
  identity and non-terminal status (a PENDING request supersedes; APPROVED locks; REJECTED/EXPIRED don't
  block).

### Global coordinator allowlist (display only)

- An admin-managed `coordinator_allowlist` exists but only feeds the `is_coordinator` display flag on
  `GET /auth/session`. Stored and compared as the **account hex**, so a coordinator is recognized under any
  prefix. Per-launch governance authz is `Committee.HasMember`, unrelated to it.

### Invite-token onboarding (v1.x)

- Deferred: an `invite_token{hash, launch_id, label, uses_remaining, …}` mint/redeem flow to onboard
  members and carry chain params — which would also close the `chain-hint` bootstrap need. Not yet
  implemented.

## Allocation files & gentx validation

The allocation-file governance model and the pre-acceptance validation *decisions* are suite ADRs; the
mechanics below are chaincoord-internal.

### Allocation state on the aggregate

Allocation state lives on the `Launch` aggregate as `[]AllocationFile{Type, SHA256, Status,
ApprovedByProposal, UploadedAt}` with domain methods `UploadAllocationFile` / `ApproveAllocationFile` /
`RejectAllocationFile` / `allocationLocked()` and sentinels
`ErrAllocation{Locked,StaleHash,NotFound,EmptyHash}` + `ErrUnknownAllocationType`. A re-upload clears
`ApprovedByProposal` and rewrites the hash → back to `PENDING`.

### Approval wiring

`ActionApproveAllocationFile` + `ApproveAllocationFilePayload{Type, Hash}`, applied by
`applyApproveAllocationFile`; the VETO side-effect is handled by `applyAllocationVeto` in the Sign path,
emitting `AllocationFileRejected`.

### Storage

A `launch_allocation_files` **metadata** table (migration 0004, PK `(launch_id, alloc_type)`, **no blob**);
the bytes/ref live in the fs `AllocationStore`. Host-mode uploads reuse the existing `genesis_host_mode`
flag + `genesis_max_bytes` cap (no new allocation config). The migration also drops the old
`launch_genesis_accounts` table.

### Gentx-validation wiring

`ports.GentxValidator` returns `GentxValidationOutcome{Results, ConsensusPubKeyB64, ValidatorAddress}`; the
`infrastructure/gentxvalidation` adapter double-decodes on the pass path only and derives `ValidatorAddress`
via `g.AccountAddress(prefix)`. `JoinRequestService.Submit` uses it as the validator (self-delegator)
identity for dedup (`supersedePending`) and committee vetting, decoupled from the submitter. The
self-delegation floor is a service-layer `Params` gate (`requiresSelfDelegationFloor`).

## Security hardening

The trust model and the tamper-evident audit log are in [Trust Model](../concepts/security.md) + the suite
ADR log; the hardening mechanics below are chaincoord-internal.

### SSRF guard

`netutil.ValidateRPCURL` resolves the host and rejects RFC1918 / loopback / link-local / CGNAT / ULA /
IMDS (`169.254.0.0/16`) — applied to operator-supplied monitor RPC + attestor genesis URLs (and again in
the monitor job as defense-in-depth). `COORD_INSECURE_NO_SSRF_CHECK` downgrades to format-only for trusted
smoke networks.

### TLS posture

Three modes: native TLS (`tls_cert`/`tls_key`, paired-or-empty), behind-infra (loopback bind, TLS
terminated upstream), and `insecure_no_tls` (explicit opt-out). Non-loopback plaintext without the flag is
refused; `ReadHeaderTimeout = 10s` guards slowloris.

### Secret & key handling

Every secret has a `_FILE` variant (`audit_private_key_file`, `jwt_private_key_file`,
`rehearsal_ops_token_file`) alongside the inline form (inline takes precedence, whitespace-trimmed).
`coordd keygen` prints a random Ed25519 seed with `docker secret` guidance; the compose omits inline keys.

### DoS caps

`maxJSONBody = 1 MiB`; genesis/allocation uploads capped by `genesis_max_bytes` (700 MiB default → `413`);
per-IP rate limits (challenge 10/min, validator writes 60/min) + the per-operator challenge limiter.

### Audit-log internals

Per-entry Ed25519 signature + `prev_hash` SHA-256 chaining; the chain tip is persisted via
`AuditChainStore`, and `verifyLastLineHash` refuses startup on a tip mismatch; `coordd audit verify`
re-derives + checks the whole chain (signatures, monotonic timestamps, `prev_hash`). See
[reference/audit.md](../reference/audit.md).

`occurred_at` is **record time** — the funnel (`writeAuditEvent`) stamps it at write time; the `WithTime`
seam is reserved for an authoritative domain time, which no current event uses. This keeps `occurred_at`
monotonic along the chain, and coordd **detects, never clamps** a backward step: it warns live (at append)
and during startup, preserving the raw value rather than laundering a clock anomaly. Startup depth is set by
`audit_startup_verify` (`full` default | `tail`): `full` scans the whole log (shared code with `audit
verify`) and refuses on tamper/corruption while only warning on a backward timestamp; `tail` is the
large-log escape hatch. Coverage is guarded by a reflection test — every exported `LaunchService` /
`ProposalService` method must be classified audited-or-not, and there are currently no mutation exemptions
(`ClaimRehearsalRun` → `RehearsalRunClaimed`, `ExpireStale` → `ProposalExpired`; only queries and builders
are unaudited). A `PATCH /launch/{id}` emits one `LaunchPatched` event carrying a per-field old→new diff — the trusted
rehearsal key folded in, no longer a special-cased event.

Audit-write error policy is split by criticality: a direct action **logs and continues** on a failed audit
write (the mutation already committed), while a governance proposal is **two-phase** — a `ProposalExecuting`
intent is written *before* the state change commits (abort if that write fails) and the completion event
*after* (a failure here is **fatal**: coordd exits rather than run on accumulating unauditable governance).
A rollback after the intent records an explicit `ProposalExecutionAborted`, so the trail self-explains.

### Admin set (accepted for v1)

The admin set is boot-time config (`COORD_ADMIN_ADDRESSES`); rotating an admin needs a restart and isn't
self-audited. **Accepted for v1**; a future `admins` table + management endpoints (mirroring the
coordinator allowlist) is a possible enhancement, not scheduled.

### Observability & response headers

`GET /healthz` probes liveness (DB `SELECT 1` + audit-log stat) → `503` on a dependency failure, detail
logged not returned. `GET /metrics` serves the default Go runtime + process metrics via `promhttp`,
unauthenticated (network-restrict it in deploy). A `securityHeaders` middleware sets `nosniff` +
`X-Frame-Options: DENY` on every response, plus HSTS **only** when coordd terminates TLS itself — in
infra-TLS mode (a proxy terminates TLS, coordd sees plain HTTP) it defers HSTS to the proxy.

## Genesis, readiness, and launch

The genesis storage model (attestor/host), the coordinator-built-genesis boundary, and readiness
attestation are suite ADRs; the mechanics below are chaincoord-internal.

### Genesis fields and gates

`InitialGenesisSHA256` (uploaded in `DRAFT`), `FinalGenesisSHA256` + `FinalGenesisInputSetHash` (uploaded in
`WINDOW_CLOSED`). `validateFinalGenesis` runs the structural checks (`chain_id`, gen_tx count = approved,
consensus-pubkey presence + dedup, future `genesis_time`) and syncs the file's `genesis_time` into
`Record.GenesisTime`; an attestor-mode final upload takes `genesis_time` as a required RFC3339 request
field. Host mode is gated by `COORD_GENESIS_HOST_MODE` + `genesis_max_bytes` (shared with allocation
uploads). `GET /launch/{id}/genesis/hash` returns both `initial_sha256` and `final_sha256`.

### Readiness dashboard aggregation

`ThresholdStatus` cutoffs: `CONFIRMED` at ≥ `bftThreshold` (200/3 %), `AT_RISK` below `atRiskBelow` (50 %),
else `REACHABLE`. Voting-power % is computed from each confirmed operator's `SelfDelegationAmount`; only
valid (non-invalidated) confirmations count.

### Block monitor

`RunLaunchMonitor` (started in `serve.go`, 1-minute cadence) polls each `GENESIS_READY` launch with a
non-empty `MonitorRPCURL` — a single URL set by a committee member via `PATCH /launch/{id}`, re-read each
tick so a `PATCH` takes effect without restart: `GET <rpc>/block?height=1` (5 s timeout, SSRF-guarded), and
on a `200` with a non-null `result.block` calls `MarkLaunched` and emits `LaunchDetected{LaunchID,
SourceRPC}`. (Block-*identity* verification — `chain_id` / height — is a queued security follow-up.)

## API & CLI

The OpenAPI-as-source-of-truth decision is a suite ADR; the routing + CLI structure below is
chaincoord-internal.

### Auth-tier route taxonomy

Routes are grouped into five tiers on the Chi router (`server.go`):

- **public** — `GET /healthz`, `GET /audit/pubkey`, `POST /auth/challenge` (rate-limited), `POST
  /auth/verify`, and `GET`/`DELETE /auth/session`.
- **optionalAuth** — resolves the caller if a token is present, else anonymous → visibility gating (the
  reads: `GET /launches`, `/launch/{id}`, `/committee/{id}`, `/launch/{id}/chain-hint`, genesis, allocations,
  dashboard, peers, audit, events).
- **requireAuth** — the authenticated actions + reads (create/patch, committee create, members, uploads,
  join/proposal/ready writes, the launch read GETs, `DELETE /auth/sessions/all`).
- **requireOps** — the four `/bridge/*` endpoints (shared `rehearsal_ops_token`, **fail-closed** when
  unset).
- **requireAdmin** — `/admin/*` (coordinators + `DELETE /admin/sessions/{address}`).

### Single-binary coordd

coordd is one cobra binary with subcommands `serve` / `migrate` / `keygen` / `version` / `audit verify` —
replacing the originally-specced separate `coord` client CLI, which was never built: interactive use is the
web, and test signing is the separate `smoke-signer` binary.

### Swagger toolchain

The spec is generated by `swag init` and lint-checked by `vacuum`, both pinned as `go.mod` tool deps;
`make swagger-check` is the CI drift gate that fails if the committed `swagger.yaml` lags the handlers.
