# seedward-chaincoord — design

A self-hosted coordination system for Cosmos SDK chain launches. This document explains the design decisions behind it,
the alternatives that were considered, and the consequences of each choice. It is not a user guide; for that, see the
README and the [documentation site](https://ny4rl4th0t3p.github.io/seedward-chaincoord).

## Context

Launching a Cosmos SDK chain is fundamentally a multi-party process. The chain team prepares a baseline genesis.
Validators independently produce gentx files containing their consensus pubkey, operator address, and self-delegation
amount. The chain team collects those gentxs, assembles the final genesis, distributes it, and the validators all start
their nodes from the same file at the same time. Between the "we are launching" announcement and "block 1 is produced",
there are typically 50–200 discrete decisions that need to be agreed on by 2–20 humans across multiple timezones.

In practice, this coordination happens over Discord channels, shared spreadsheets, and ad-hoc messaging. The problems
with this state of the art:

- **No source of truth.** Decisions are scattered across chat history, pinned messages, and a Google Sheet that someone
  forgot to share. When something goes wrong, nobody can reconstruct who approved what when.
- **No accountability.** "Did we agree that validator X should be in the initial set?" is a question that has no
  authoritative answer in a Discord-coordinated launch.
- **No tamper-evident audit trail.** A genesis file is the foundation of a state machine that may hold billions in
  value. The decisions that produced it should be reviewable forever, not lost in a chat scroll.
- **Single-coordinator centralization.** Most chain teams designate one person to assemble the genesis. That person has
  unilateral authority by accident; if they make a mistake or act in bad faith, there is no structural check.
- **Validator gentx errors are caught late.** Validators submit gentxs informally; errors are discovered when the chain
  fails to boot or when post-launch audits reveal discrepancies.

`seedward-chaincoord` is the coordination layer that addresses these problems with explicit committee governance, a
state machine
for the launch lifecycle, and a tamper-evident audit log. It does not replace the chain binary or the genesis-assembly
tool (`gentool`); it wraps the social process around them.

## What this is

A self-hosted server with a web UI where a chain team's committee members and validators interact through their existing
Cosmos wallets to drive a chain launch through a defined lifecycle.

**In scope:**

- M-of-N committee governance over launch lifecycle decisions
- Explicit launch lifecycle state machine with automated launch detection via CometBFT RPC monitoring
- Validator gentx submission and tracking
- Validator readiness confirmations after genesis publication
- Genesis file management in two modes: attestor (external URL + hash attestation) and host (server stores and serves
  the file)
- Offline-verifiable audit log; per-entry Ed25519 signatures detect content modification and timestamp reversals; hash
  chaining links each entry to the SHA-256 of the previous line, making entry deletion detectable across server restarts
- Keplr / Leap wallet sign-in for both committee members and validators
- Client-side signing of all user actions (no user wallet key custody on the server)
- Self-hostable: a single Go server binary (`coordd`) paired with a companion Next.js web UI deployed as a separate
  service
- SQLite-backed storage by default; interface-backed for alternative backends

**Out of scope:**

- Final genesis assembly (delegated to the chain binary's `collect-gentxs` and to `gentool`)
- Running or operating a chain node; the server reads from an optional committee-configured CometBFT RPC endpoint for
  launch detection but does not run consensus or chain execution
- Custodial management of user wallet keys; the server manages its own operational keys (audit log signing, JWT signing)
  but never holds keys that enable user-attributable actions
- Mainnet operations beyond the launch event (the tool is launch-focused)
- Replacement of the chain's binary for any chain-execution function

## Key design decisions

### 1. M-of-N committee with VETO

**Alternatives considered:**

- Single coordinator authority
- Simple majority committee
- Unanimous committee
- Token-weighted voting

**Choice:** An M-of-N committee where any member can raise a proposal, M members must sign for it to execute, and any
single member can VETO to kill a proposal. Raising a proposal implicitly casts the proposer's SIGN, so the proposer
cannot subsequently VETO their own proposal.

**Rationale:** Chain launches are high-stakes, low-frequency events. A single coordinator is structurally too
centralized; one mistake or one bad actor compromises the launch. Unanimous consent is too brittle; one absent
committee member blocks every decision. Simple majority handles normal disagreement but does not protect against "the
majority is about to make a serious mistake" cases. The M-of-N with VETO model resembles real-world governance bodies:
most decisions require a meaningful threshold of agreement, but any single member with strong objection can block the
action and force discussion. Token-weighted voting is inappropriate because the committee predates the chain's token
existence.

**Consequences:**

- Routine decisions move at committee speed (M signatures), not at unanimity speed
- A single committee member can stop a decision they consider seriously wrong, raising the cost of mistakes
- VETO is observable and audited, so misuse is visible and politically costly
- Committee proposals govern the committee composition itself, so the membership can evolve during the launch

### 2. Explicit launch lifecycle state machine

**Alternatives considered:**

- Free-form state (any action available at any time)
- Two-state model (preparing / launched)
- Workflow tool integration (e.g., GitHub Issues, Jira) for state tracking

**Choice:** A defined lifecycle of seven states — DRAFT, PUBLISHED, WINDOW_OPEN, WINDOW_CLOSED, GENESIS_READY, LAUNCHED,
with CANCELED as an escape hatch from any state. Most transitions are driven by committee proposals. `open-window`
(PUBLISHED → WINDOW_OPEN) is a direct action any committee member can call without a proposal. Cancellation is **hybrid
**:
the lead may cancel directly from `DRAFT`/`PUBLISHED` (no validators have committed yet), but once a launch is past
`PUBLISHED` the direct path is closed (409) and cancellation requires an M-of-N `CANCEL_LAUNCH` proposal — so a single
seat cannot destroy a committee-approved, readiness-confirmed launch. The GENESIS_READY → LAUNCHED transition is
automated: the server polls a
committee-configured CometBFT RPC endpoint for block 1 and executes the transition when the chain is observed to
have started.

**Rationale:** Launch coordination has a natural sequence: the chain team prepares the baseline (DRAFT), publishes the
launch parameters (PUBLISHED), opens the window for validators to submit gentxs (WINDOW_OPEN), closes the window
(WINDOW_CLOSED), assembles and distributes the genesis (GENESIS_READY), and the chain starts (LAUNCHED). Free-form state
allows actions to happen in the wrong order (validators submitting before the window opens, the chain team finalizing
the genesis before validators have submitted). Two-state models are too coarse to express the actual workflow. External
workflow tools are not designed for cryptographic signing and audit, and they require committee members and validators
to use
a separate system from the one that handles their actual transactions.

**Consequences:**

- Actions are gated on the current state; the tool refuses to accept validator submissions while WINDOW_OPEN is not the
  current state
- The CANCELED escape hatch from any state allows the committee to abort a launch cleanly if conditions change
- The state machine is documented in the project docs, so new committee members can orient themselves before joining a
  launch
- One regression path exists: GENESIS_READY → WINDOW_CLOSED via the `REVISE_GENESIS` proposal, allowing the committee
  to correct a genesis file after it has been published; this requires a proposal, making the regression auditable
- The `open-window` and early-stage `cancel` direct-action paths are intentional exceptions to the
  proposal-for-everything model: `open-window` (callable by any committee member) is normally used from `PUBLISHED`, but
  from `DRAFT` it auto-publishes first when the initial genesis hash is already set — a single-step shortcut
  equivalent to executing `PUBLISH_CHAIN_RECORD` — and carries low risk on its own; the direct `cancel` gives the lead
  an
  emergency scrap in `DRAFT`/`PUBLISHED` that cannot be blocked by an absent quorum. Past `PUBLISHED` this shortcut is
  withdrawn: cancellation becomes a governed `CANCEL_LAUNCH` proposal, because by then external parties have committed
  and the cost of a unilateral, irreversible cancel is too high — the authority to cancel tracks that cost
- The GENESIS_READY → LAUNCHED transition is informational: the coordination work is complete at GENESIS_READY; LAUNCHED
  records that the chain actually produced block 1

### 3. Ed25519-signed, offline-verifiable audit log

**Alternatives considered:**

- Plain text log file
- Database-only audit table
- Append-to-IPFS / public storage
- On-chain audit log on a separate Cosmos chain

**Choice:** Append-only JSONL file where the server Ed25519-signs each entry, with a CLI (`coordd audit verify`)
that can verify the entire log offline given the server's public key. The public key is available via
`GET /audit/pubkey` while the server is running and should be preserved alongside the log file so verification
remains possible after the server is decommissioned.

**Rationale:** The audit log is the document of record for what happened during the launch. It needs to be authentic
(only the server could have produced it), tamper-evident (individual entry modification is detectable via signature;
entry deletion is detectable via the hash chain — each entry carries the SHA-256 of the previous line, covered by the
current entry's signature, and the chain tip is persisted to the database so deletions are caught on restart), and
durable beyond the server's lifetime. Plain logs fail the tamper-evidence requirement. A database table is opaque and
depends on the server's availability for verification. IPFS / public storage solves durability but adds operational
complexity and exposes information that may be sensitive during a private testnet launch. On-chain audit on a separate
Cosmos chain solves durability and verifiability but adds an unnecessary dependency. Local JSONL with cryptographic
signing balances the requirements: the file is portable, the signature chain is verifiable, and the operator can publish
the file post-launch as a public record.

**Consequences:**

- Anyone with the log file and the server's public key can verify what happened
- The log keeps working as evidence even if the server is taken down years later
- A leaked log does not compromise key material; the server's signing key is the only sensitive identifier
- The operator can choose to publish the log openly, share it under NDA, or retain it privately — the cryptographic
  properties are the same

### 4. Keplr / Leap wallet sign-in

**Alternatives considered:**

- Email + password authentication
- OAuth via GitHub / Google

**Choice:** Standard Cosmos wallet sign-in using existing wallet extensions (Keplr, Leap). The user's Cosmos address is
their identity.

**Rationale:** The committee members and validators in a Cosmos chain launch already have Cosmos wallets. They already
know
how to use them. Their existing keys are the natural identity to bind authority to. Email and password add a new
credential to manage and fall back to email-based recovery, which is the weak link. OAuth ties the launch's authority
to a third-party service.

**Consequences:**

- Sign-in is one-click for anyone who already has a Cosmos wallet
- The identity space is already keyed by Cosmos addresses, so validator and committee-member identities compose
  naturally
- The server never sees a password and cannot leak one
- A user who loses their wallet recovery phrase loses access; this is the same risk model they already accept for their
  on-chain funds

### 5. Client-side signing only

**Alternatives considered:**

- Server-held delegate keys for convenience signing
- Optional hosted signing for users without local wallets
- Hybrid (local primary, hosted fallback)

**Choice:** All signing happens in the user's wallet extension, in their browser. The server never holds user wallet
private keys. It manages its own operational keys — an Ed25519 key for signing audit log entries and a separate
Ed25519 key for signing JWTs — but neither can be used to perform any user-attributable action.

**Rationale:** The server should not be a custody risk. Holding user wallet keys for convenience creates a target that,
if compromised, gives the attacker full control of every stored wallet: they can forge coordination approvals, but also
drain funds, cast governance votes, and perform any other on-chain action those keys authorize. A launch coordination
tool is a high-value target during the launch window; reducing its blast radius to "the server can be compromised and
the only thing it can do is corrupt its own audit log, not forge user actions" is the correct stance.

**Consequences:**

- Users must have a wallet extension installed; the tool is not accessible from a fresh device
- The server cannot perform any user-attributable action; every action is cryptographically attributable to a user's
  wallet
- Recovery of user identity is the user's wallet recovery process, not a server-side reset
- The trust required of the operator is minimal: even an operator running a compromised binary cannot forge user actions

### 6. SQLite default with interface-backed storage

**Alternatives considered:**

- Postgres-only (production-grade default)
- SQLite-only (no alternative)
- File-based storage

**Choice:** SQLite as the default backend, with the storage layer behind an interface, so Postgres, MySQL, or other
backends can be added by implementing the interface and wiring at startup.

**Rationale:** A chain launch is a low-volume, single-server workload. SQLite handles it comfortably and has zero
operational dependencies. Requiring Postgres for a tool that runs once per chain launch is operational overkill. But
organizations running coordination as a hosted service for many chains will want a real database for backup,
replication, and observability. The interface allows both without bifurcating the codebase.

**Consequences:**

- A fresh chain team can clone the repo and run the server with no infrastructure decisions to make
- Operators running at scale can drop in an alternative backend without touching the application logic
- Migrations are SQLite-shaped by default; adding a Postgres backend requires equivalent migration files
- The single-file SQLite database is also a natural backup artifact; the operator can archive it alongside the audit log

### 7. Server does not assemble the final genesis

**Alternatives considered:**

- Server assembles and serves the final genesis
- Server runs `<chaind> collect-gentxs` internally
- Server integrates with `gentool` directly as a library

**Choice:** The server records gentxs and committee decisions, then leaves the final genesis assembly to a committee
member
running the chain binary (or `gentool`) locally.

**Rationale:** The server's job is to coordinate, not to execute. Genesis assembly requires access to the specific chain
binary's behavior, which varies across chains and SDK versions. Embedding that logic in the coordination server couples
them tightly and forces the server to track every chain binary it might be used with. The clean boundary is: the server
is the source of truth for what was decided; the chain binary (or `gentool`) is the source of truth for what the
resulting genesis looks like. The committee member who runs `gaiad genesis collect-gentxs` (or equivalent) is taking on
accountability for the final result, which is the correct allocation of responsibility.

**Consequences:**

- The server has no dependency on any chain binary
- A new chain on a new SDK version requires zero changes to the server
- `gentool` and `coordd` compose: coordd coordinates the human process; gentool deterministically assembles the
  result; the chain binary validates and runs it
- The handoff from coordination to assembly is explicit and auditable; the audit log records the moment the committee
  member
  marked the gentxs as final

### 8. Join requests carry the validator's full submission

**Alternatives considered:**

- Validators submit only an operator address; gentx attached later out-of-band
- Validators submit identity first; a committee member manually links to gentx
- Validators submit gentx URLs; server fetches the file

**Choice:** A validator's join request includes the gentx file, operator address, and self-delegation amount in a single
signed submission.

**Rationale:** A validator's gentx is the only thing that actually matters for the genesis; everything else is metadata.
Splitting submission across multiple steps invites mismatch (the operator who submitted the identity is not the operator
who submitted the gentx). A single signed submission ties the validator's wallet identity to a specific gentx file,
making provenance unambiguous.

**Consequences:**

- The submission flow is one step from the validator's perspective
- The signature attests to the validator's claim of authorship of the gentx
- The committee reviews a complete submission rather than a partial one
- A validator who needs to change their gentx submits a new join request, with the old one available in the audit log

### 9. Signed-proposal pattern for every committee action

**Alternatives considered:**

- Direct mutations by committee members with role-based permissions
- Single-signature actions for low-stakes operations
- Off-chain Snapshot-style polling

**Choice:** Actions that change server state — adding or removing validators, most lifecycle transitions, committee
modifications, and genesis account management — are structured as signed proposals that require M-of-N committee
signatures within a time limit before they execute. `open-window` (PUBLISHED → WINDOW_OPEN, callable by any committee
member)
is a direct action rather than a proposal, and the lead's `cancel` is a direct action in `DRAFT`/`PUBLISHED` only —
cancelling a launch past `PUBLISHED` requires a `CANCEL_LAUNCH` proposal. See decision 2 for the rationale.

**Rationale:** The audit log's value comes from the property that nothing happens without a verifiable trail. Direct
mutations that bypass the audit entirely break the model. Single-signature low-stakes operations create ambiguity about
what is and isn't audited; the simpler rule is "everything is a proposal or a direct action with its own audit event."
Time limits prevent stale proposals from being signed and applied months later, which is a real risk if the proposal
pool is unbounded.

**Consequences:**

- The proposal is the dominant primitive for state changes; the direct-action exceptions (`open-window`, and the lead's
  early-stage `cancel` in `DRAFT`/`PUBLISHED`) are intentional carve-outs for a routine committee-member operation and a
  pre-commitment emergency scrap, both of which are audited
- The audit log is comprehensive; every proposal-driven change is preceded by a proposal record and signature records,
  and every direct action (open-window, cancel) writes its own audit event
- Routine operations are slightly more verbose than direct mutation; this is a deliberate trade for auditability
- The time-limit forces the committee to actively decide on a proposal rather than letting it linger

### 10. Distinct roles: coordinator, committee member, lead, validator

**Alternatives considered:**

- Flat committee-member role; any committee member can do anything within their share of M-of-N
- Hierarchical roles (chain founder → committee members → validators)
- Permissionless validator role; any wallet can submit a gentx

**Choice:** Four per-launch roles, plus a server-plane admin. A **coordinator** is a party permitted to *create* a
launch (gated by the admin-managed allowlist) and declare its committee; it need **not** sit on that committee. A
**committee member** is one of the M-of-N who govern the launch through proposals. The **lead** is the committee's
first member (`Members[0]`), distinguished only by the early-stage emergency cancel and the DRAFT committee reconfigure.
A **validator** submits a join request but does not sit on the committee.

**Rationale:** A chain launch has a natural division of labour, but folding it all into one "coordinator" concept
blurred who decides what. Someone must be permitted to *start* a launch — the coordinator, gated by the admin's
allowlist — but who *governs* it is the committee, and the coordinator may delegate governance to an entirely external
committee. The lead is **not** a separate seat, and **not** defined by having created the launch: it is simply position
0 of the committee, holding an emergency abort that cannot be blocked by a quorum that may not be reachable. Separating
creation, governance, and chain participation makes authority explicit.

**Consequences:**

- The lead is always a committee member — position 0 of the committee, not a separate seat and not necessarily the
  launch's creator. The lead participates in all committee votes like any other member, and additionally holds the
  direct early-stage `cancel` shortcut (`DRAFT`/`PUBLISHED` only); past `PUBLISHED`, cancellation is an M-of-N
  `CANCEL_LAUNCH` proposal that any committee member may raise. `open-window` is not lead-exclusive either — any
  committee member may call it
- A coordinator need not be on the committee: a launch can be created and its governance delegated wholesale to an
  external committee (full delegation)
- Validators have a defined seat at the table (their join request) without sitting on the committee
- The role assignments are themselves audited, so changes in a role are visible
- A committee member can also be a validator, with the two roles cleanly separated in the audit trail

### 11. Automated LAUNCHED detection via CometBFT block polling

**Alternatives considered:**

- The lead manually marks the launch as LAUNCHED via a direct action
- No LAUNCHED state (GENESIS_READY as the terminal coordination state)
- WebSocket subscription to a chain node for new-block events

**Choice:** A background server job polls a committee-configured CometBFT RPC endpoint at a fixed interval for a block

1. When a non-null block is returned, the server transitions the aggregate to LAUNCHED and publishes a `LaunchDetected`
   event. The `monitor_rpc_url` is set by any committee member via `PATCH /launch/{id}` and can be updated at any time
   without a server restart.

**Rationale:** The moment the chain produces block 1 is the natural end of the coordination record. Recording it
automatically avoids requiring someone to be online at exactly the right moment and ensures the audit log has a clean
terminal state. A manual transition relies on human attention during a chaotic window. WebSocket subscriptions
provide lower latency but require a persistent connection and add failure modes; polling is simpler and resilient to
temporary RPC unavailability. Omitting a LAUNCHED state entirely would leave the audit record open-ended, reducing
its value as a historical document.

**Consequences:**

- GENESIS_READY → LAUNCHED happens automatically once block 1 is observed, with no user action required
- Operators who do not configure `monitor_rpc_url` will see launches remain at GENESIS_READY indefinitely; this does
  not break any functionality since coordination is complete at that state
- The server rejects `monitor_rpc_url` values that resolve to a hardcoded set of CIDRs (RFC1918, loopback, link-local,
  carrier-grade NAT) at save time and before each poll; this is best-effort mitigation — DNS rebinding between
  validation and connection is not prevented
- The URL can be set after genesis is published, so operators do not need to know their final RPC endpoint at launch
  creation time

## Known limitations

- **Cosmos SDK chains only.** The tool assumes secp256k1 keys, gentx-based genesis, and CometBFT RPC. Non-Cosmos chains
  are out of scope.
- **Block monitoring requires outbound RPC access.** When `monitor_rpc_url` is set on a launch, the server polls the
  configured CometBFT endpoint for block 1 and auto-transitions to LAUNCHED. Operators in air-gapped or
  network-restricted environments should leave this field unset; the launch will remain in GENESIS_READY, which is
  harmless — all coordination work is complete at that point. The server never participates in consensus or executes
  chain logic.
- **No key custody, including for convenience.** Users without a wallet extension cannot use the tool. This is
  intentional and not negotiable.
- **No final genesis assembly.** The tool produces the coordination record; the operator produces the final genesis with
  the chain binary or `gentool`. This is the right boundary, but it does mean the operator needs two separate tools
  rather than one.
- **SQLite default is not HA.** Operators running coordination as a hosted service for many chains should swap in an
  alternative backend; the interface supports it but the swap is operator-managed as of today.
- **Web UI is not fully validated end-to-end.** Visual and interaction regressions may exist even when the full test
  suite passes. The tool is described as research-grade and should be validated against a real test launch before any
  production use.
- **Validator SET is not bounded by an allowlist — admission rests on committee approval (open).** Membership
  (see + submit) gates the hot *submitter* address; the *validator* operator address inside a gentx is **not**
  pre-allowlisted. A vetted member can therefore submit a valid gentx for any validator operator address, and the
  committee is the sole gate that vets it (anchored off-band on the operator/self-delegation, per decision #8). This is
  deliberate — a validator-set allowlist would force every fleet node to pre-register a valoper + hot wallet, which is
  friction for ~zero gain, and the blast radius is bounded (an attacker can only submit for *their own* validator, can't
  overwrite a member's pending gentx, and the anomaly surfaces at approval). It remains **open to revisit**: whether
  v1.x
  should offer an optional per-launch validator-address allow/deny list for committee members who want the set bounded
  before
  approval rather than at it. Tracked against the membership/onboarding plan.
- **Authored spec, AI-assisted build.** This is a Spec-Driven Development project: the architecture — the protocol, the
  M-of-N committee governance model, the lifecycle state machine, the threat model, and the offline-verifiable audit-log
  security design — is mine, authored as a spec and then implemented with AI assistance under my review. The
  architecture
  is intentional and the test coverage is real; the maturity is "interesting prototype," not "battle-tested production
  tool" — validate against a real test launch before production use.

## What's next

The natural direction of travel:

- **Tighter composition with `gentool`.** The coordination tool can hand off the collected gentxs directly to `gentool`
  as the assembly engine, closing the loop between "what the committee decided" and "what the resulting genesis looks
  like" without requiring a committee member to invoke a separate tool manually.
- **Production hardening of the web UI.** The current UI has not been fully validated end-to-end; reaching a state where
  it can be recommended for real chain launches requires sustained UI testing work.
- **First real launch dogfood.** The tool has no real users yet. Using it for an actual chain launch — either an
  external chain that adopts it, or a small dogfood chain run for this purpose — is the next milestone. The
  experience will likely surface issues that no amount of design can predict.
- **Alternative backends as needed.** The storage interface allows Postgres or other backends; concrete implementations
  will follow real operator demand.
- **Maturity track to v1.0.** Stabilization of the HTTP API, the audit log format, and the proposal types under semver
  guarantees, so operators can build the process on top of the tool without worrying about breaking changes.
