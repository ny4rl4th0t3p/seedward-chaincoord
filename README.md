# chaincoord

Self-hosted coordination server for **Cosmos SDK** chain launches — M-of-N committee governance, a guarded
launch-lifecycle state machine, gentx intake with pre-acceptance validation, and a tamper-evident,
offline-verifiable audit log.

`chaincoord` is the coordination server (`coordd`) in **Seedward**, the self-hostable operator suite I'm
building for the Cosmos SDK chain lifecycle. Its siblings:

- [seedward-gentool](https://github.com/ny4rl4th0t3p/seedward-gentool) — deterministic genesis-file construction (also the engine `rehearsal` embeds)
- [seedward-rehearsal](https://github.com/ny4rl4th0t3p/seedward-rehearsal) — a pre-flight service that boots a throwaway chain to prove a genesis initializes and advances before anyone commits
- [seedward-cli](https://github.com/ny4rl4th0t3p/seedward-cli) — the unified suite CLI (**planned, post-v1**; its coordd-facing commands are stubs today)
- [pour](https://github.com/ny4rl4th0t3p/pour) — a pure-Go multi-chain faucet

*Published components are Apache-2.0 and self-hostable.*

> **Spec-Driven Development (SDD) project.** The design is mine — the protocol, the M-of-N committee
> governance model, the launch-lifecycle state machine, the threat model, and the offline-verifiable
> audit-log security design — authored as a spec and then implemented with AI assistance under my review.
> The result is **v1.0.0**, the first stable release — feature-complete, though not yet externally
> audited; review the threat model and verify on your own infrastructure before high-value use.

📖 **[Full documentation](https://ny4rl4th0t3p.github.io/seedward-chaincoord/)** · 🏗️ **[Design document](docs/DESIGN.md)**

---

## What it does

Launching a Cosmos SDK chain means assembling a genesis file from validator contributions, reaching M-of-N
agreement on its content, and making sure every participant starts from the same file at the same time.
Done informally — over chat or shared drives — that's error-prone and unaccountable.

`coordd` makes it explicit, auditable, and multi-party: every lifecycle transition
(`DRAFT → PUBLISHED → WINDOW_OPEN → WINDOW_CLOSED → GENESIS_READY → LAUNCHED`) is driven by a **committee
proposal** requiring M-of-N coordinator signatures, and every action lands in a signed, offline-verifiable
**audit log**. See the [concepts overview](https://ny4rl4th0t3p.github.io/seedward-chaincoord/concepts/overview/)
for the full model.

**Scope:** Cosmos SDK chains only (secp256k1, gentx-based genesis, CometBFT RPC). `coordd` never runs a
chain node, never holds a private key (signing is client-side), and never assembles the final genesis — the
coordinator builds that locally with the chain binary (`<chaind> genesis collect-gentxs`). SQLite by
default; storage and RPC layers are interface-backed.

---

## Quick start

`coordd` is a headless coordination server — it has no UI of its own. Two ways to run it:

- **The server alone (this repo).** Run the published image from GHCR — the
  **[Run with Docker](https://ny4rl4th0t3p.github.io/seedward-chaincoord/getting-started/docker/)** guide has
  the `keygen → migrate → serve` sequence and env reference — or build locally with `make build` (see the
  **[Quickstart](https://ny4rl4th0t3p.github.io/seedward-chaincoord/getting-started/quickstart/)**).
- **The full stack — coordd + the web UI, one command.** That lives in
  **[seedward-suite](https://github.com/ny4rl4th0t3p/seedward-suite)** (`make dev-up` runs both). The web
  front end on its own is **[seedward-chaincoord-web](https://github.com/ny4rl4th0t3p/seedward-chaincoord-web)**.

Health check once it's up: `curl http://localhost:8080/healthz` → `{"status":"ok"}`. For TLS and production
configuration, see **[Setup & Configuration](https://ny4rl4th0t3p.github.io/seedward-chaincoord/reference/setup/)**.

---

## Documentation

Full docs: **https://ny4rl4th0t3p.github.io/seedward-chaincoord/**

- **Getting started** — [Quickstart](https://ny4rl4th0t3p.github.io/seedward-chaincoord/getting-started/quickstart/) · [Run with Docker](https://ny4rl4th0t3p.github.io/seedward-chaincoord/getting-started/docker/) · [Smoke test](https://ny4rl4th0t3p.github.io/seedward-chaincoord/getting-started/smoke-test/)
- **Concepts** — [overview](https://ny4rl4th0t3p.github.io/seedward-chaincoord/concepts/overview/): roles, lifecycle, proposals, genesis, readiness, trust model
- **Reference** — [setup & config](https://ny4rl4th0t3p.github.io/seedward-chaincoord/reference/setup/) · [API](https://ny4rl4th0t3p.github.io/seedward-chaincoord/reference/api/) · [audit CLI](https://ny4rl4th0t3p.github.io/seedward-chaincoord/reference/audit/)
- **Decisions** — the [ADR-style record](https://ny4rl4th0t3p.github.io/seedward-chaincoord/decisions/)
</content>