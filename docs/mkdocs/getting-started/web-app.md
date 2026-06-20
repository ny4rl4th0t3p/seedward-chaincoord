# Web App

!!! warning "Deprecated — moving to a separate repository"
    The web frontend is being extracted into its own repository and reworked. **This page is obsolete and unmaintained** — its contents are not guaranteed to match the current code and it will be removed once the move is complete. Treat everything below as historical.

The `web/app/` directory is a React + TypeScript frontend that lets coordinators and validators interact with the full launch lifecycle from a browser wallet (Keplr or Leap) — no CLI required.

For setup and infrastructure, see [Dev Environment](dev-environment.md).

!!! danger "Proof of concept — not for production use"
    chaincoord is research-grade software. APIs, data formats, and behaviours may change without notice. **Do not use it for mainnet launches or any environment where correctness and availability are required.**

!!! warning "Web UI not fully validated"
    The frontend has not been fully validated end-to-end. Visual and interaction regressions may exist even when the full test suite passes.

---

## Sign-in paths

There are two distinct authentication paths depending on your role.

### Coordinator sign-in (header)

Coordinators authenticate using a chain already registered in their wallet. The header always shows a sign-in dropdown with three pre-registered choices: **Cosmos Hub**, **Osmosis**, and **Juno**.

1. Select a chain from the dropdown (default: Cosmos Hub).
2. Click **Connect Wallet** — the wallet modal opens.
3. Once connected, click **Sign In** — the app requests a challenge and signs it with your key.

The address you authenticate with becomes your operator identity for any launches you coordinate. **You must use the same chain consistently within a single launch** — the committee and all signed actions are tied to that address.

### Validator sign-in (launch detail)

Validators authenticate using the new chain being launched, which is not yet in their wallet. The launch detail page (`/launch/<id>`) guides them through three steps:

1. **Add Chain to Wallet** — fetches chain metadata (`chain_id`, `bech32_prefix`, `denom`) from `GET /launch/<id>/chain-hint` (unauthenticated) and calls `experimentalSuggestChain` on the wallet extension. This step derives the validator's operator address on the new chain.
2. **Connect Wallet** — select the newly registered chain account.
3. **Sign In** — sign a challenge to obtain a JWT session.

Validators who visit a launch URL for the first time will see a choice: **Coordinator or returning user** (sign in via the header) or **Join as Validator** (start the per-launch flow above).

---

## Coordinator flows

After signing in, coordinators whose address appears in the launch committee see the coordinator panel.

**Join request queue** — review pending validator applications and raise approve or reject proposals for each.

**Proposal list** — sign or veto open proposals. A proposal executes automatically once M signatures are collected; one veto kills it immediately.

**Coordinator actions** — available depending on launch status:

| Action | When available |
|---|---|
| Open application window | Status `PUBLISHED` |
| Upload genesis | Any pre-`LAUNCHED` status |
| Set monitor RPC | Any pre-`LAUNCHED` status |
| Download gentxs | Any status (downloads all approved validator gentxs as JSON) |
| Replace committee | Status `DRAFT` only; lead coordinator only |
| Cancel launch | Any non-terminal status; lead coordinator only (two-step confirm) |

---

## Validator flows

After signing in via the per-launch path, validators see the validator panel.

1. **Submit join request** — fill in the `gentx` (paste or file upload), peer address, optional RPC endpoint and memo. Signed with the wallet before submission. (The consensus key is read from the `gentx` — it is not entered separately.)
2. **Track approval** — the join request status panel polls every 15 seconds and shows pending / approved / rejected state with any rejection reason.
3. **Download genesis** — once a final genesis is published, download it. The SHA-256 is verified in-browser via the Web Crypto API.
4. **Confirm readiness** — submit a readiness confirmation with the genesis hash and binary hash (both required).
5. **Peer list** — once approved, load the `persistent_peers` string for all approved validators (one click to copy). Paste this into your node's `config.toml`.

---

## Audit log

Each launch detail page includes a collapsible **Audit Log** section. Clicking "Load Audit Log" fetches all recorded events for that launch, shows the server's Ed25519 public key for offline verification, and lets you expand each entry to see its full payload.

The audit log is also verifiable offline with `coordd audit verify`. See [Audit Log](../reference/audit.md).

---

## Admin panel

The admin panel (`/admin`) is accessible from the sidebar. It checks your JWT on load — if your address is not in `COORD_ADMIN_ADDRESSES` it shows a "not an admin" message.

Admins can:
- **Coordinator allowlist** — add or remove addresses that are permitted to create launches (when the server's `launch_policy` is `restricted`)
- **Session revocation** — invalidate all sessions for a given operator address

See [Dev Environment](dev-environment.md) for how to set `COORD_ADMIN_ADDRESSES`.

---

## Session management

**Sign out** — the Sign Out button in the header revokes only the current session token.

**Revoke all sessions** — a two-step "Revoke All Sessions" confirmation button (next to Sign Out on the launch detail page) calls `DELETE /auth/sessions/all`, invalidating every active session for your address across all devices.

---

## Create launch

Authenticated coordinators can create a new launch from the **New Launch** page (`/launch/new`) or via the sidebar link. The form covers the chain record, launch options (type, visibility, initial allowlist), and committee setup.

The committee `creation_signature` is produced automatically: the app signs the canonical JSON of the committee payload with your wallet on submit.

---

## Architecture notes

- API calls are proxied through Next.js server-side rewrites — the browser never needs to know the backend address.
- SSE (`GET /launch/<id>/events`) connects directly from the browser to `http://localhost:8080`. This avoids buffering issues with streaming responses through the proxy.
- JWT tokens are stored in React state only — never in `localStorage` or cookies. Refreshing the page requires re-authentication.
- All signing uses `signArbitrary` (ADR-036 amino), compatible with the server's `Secp256k1Verifier`.