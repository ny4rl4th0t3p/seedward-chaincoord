# Roles

seedward-chaincoord separates one **server-plane** role (admin) from the **per-launch** roles
(coordinator, committee member, lead, validator). A single person or organisation can hold more than one.

The distinction that trips people up: **admin** runs the whole server; a **coordinator** is merely *permitted
to create a launch*; the **committee members** are the ones who actually *govern* a launch; and a **validator**
*joins the chain* the launch produces. "Coordinator" is **not** a synonym for "committee member."

---

## Admin

*Server plane — the whole coordd deployment.*

Whoever operates the coordd server: the operator addresses listed in `COORD_ADMIN_ADDRESSES`. Admin holds the
`/api/v1/admin/*` endpoints; concretely, admin **manages the coordinator allowlist** (who may create launches when
`launch_policy=restricted`) and can revoke sessions. Admin is **not a participant in any launch** — it never
declares a committee, signs a proposal, or submits a gentx. See [Setup](../reference/setup.md).

---

## Coordinator

*The party permitted to **create** a launch.*

Under the default `restricted` policy that permission is the **coordinator allowlist** the admin manages; under
`open` policy any authenticated address may create one. A coordinator:

- Creates the launch (the chain record) and **declares its initial committee** — the M-of-N governance group.
- **Need not be a member of that committee.** Creation is gated only by the allowlist, so a coordinator may hand
  governance to an entirely external committee ("full delegation"). In the common case the coordinator places
  itself as the committee's first member and is therefore *also* the **lead** (below) — but that is a choice, not a
  requirement.

Beyond creating the launch and declaring its committee, a coordinator has **no special power** over it — all
subsequent governance belongs to the committee. The "coordinator allowlist" is a **global, admin-managed gate**;
it is **not** any launch's committee, and being on it does not make you a committee member of anything.

---

## Committee member

*One launch — a member of its governing committee.*

The **committee** is the M-of-N group that governs a single launch. Its members — **committee members** — drive
every state transition and governance decision through **proposals**.

**Key facts:**

- Identified by their Cosmos SDK operator address (bech32)
- Authenticate to `coordd` via a secp256k1 challenge–response (the same key used on-chain)
- Can raise any proposal type at any time (subject to the launch's current status)
- Each committee member signs or vetoes proposals raised by others
- Signatures are verified against the compressed secp256k1 public key the caller supplies with each request,
  which the server proves derives to the claimed operator address — no public key is pre-registered

**What a committee member cannot do unilaterally:**

- Execute any state transition alone (unless M=1)
- Modify the launch record outside of proposals
- Override another committee member's veto

---

## Lead

*One launch — the committee's first member (`Members[0]`).*

The lead is an ordinary committee member with two extra privileges, both deliberately narrow:

- **Direct cancel in `DRAFT`/`PUBLISHED`** — a lead-only shortcut to scrap a launch before any validator has
  committed, without a proposal. Once a launch is past `PUBLISHED` this shortcut is gone: canceling then
  requires an M-of-N `CANCEL_LAUNCH` committee proposal (which *any* committee member can raise, so the
  committee stays in control). This is the lead's *only* unilateral, irreversible power, and it is confined to
  the harmless early stages on purpose.
- **Reconfigure the committee while the launch is still in `DRAFT`** — wholesale replacement, before governance
  is live.

By convention the lead also handles the operational **DRAFT setup and genesis assembly** — upload the initial
genesis, assemble the final genesis locally (`gaiad genesis collect-gentxs`) and upload it, and set the monitor
RPC URL so `coordd` polls for block production. (These, and opening the application window, are open to **any**
committee member; only the direct early-stage cancel and the `DRAFT` committee reconfigure are lead-exclusive.)

**Leadership is position 0, not a separate credential.** The lead is simply whoever sits at `Members[0]` of the
declared committee — it is **not** defined by having created the launch (that is the coordinator's action). Every
committee has exactly one lead; if the lead is removed or replaced via a `REPLACE_COMMITTEE_MEMBER` proposal, the
lead role transfers automatically to the new `Members[0]`.

---

## Validator

A validator is an operator who wants to participate in the genesis validator set. They interact with `coordd` during
`WINDOW_OPEN` (to apply) and again after `GENESIS_READY` (to download the final genesis and confirm readiness).

**Two address classes.** A validator works with two distinct addresses, and keeping them separate is the core of the
onboarding model:

- **Hot actor address** — the disposable address the operator logs into `coordd` with and signs the join-request
  *submission*. This is the address that must be on the launch's [members list](#membership). The real (cold)
  validator key never touches the app.
- **Cold validator (operator) address** — the validator's on-chain account (the `valoper` identity in account form).
  `coordd` does not trust a self-declared field: it **derives** this address from the key that *signed* the gentx
  (`RIPEMD160(SHA256(pubkey))`, bech32-encoded) and verifies it matches the gentx's `validator_address`. That
  cryptographic binding is why it's the stable anchor the committee vets against — forging a gentx for someone else's
  operator address would need their cold key. (The deprecated `delegator_address` field is empty in modern gentxs and is
  not used as the identity.)

**What validators do:**

1. Be added to the launch's **members list** by the committee (off-band, with a label) — a non-member can't see the
   launch or submit (see [Membership](#membership)).
2. Generate a `gentx` locally using their chain binary (e.g. `gaiad genesis gentx`).
3. Authenticate to `coordd` with their **hot** address (same secp256k1 challenge–response as committee members).
4. Submit a join request carrying the `gentx`, peer address, and RPC endpoint. The consensus key is read from the
   gentx; the operator address is **derived from the gentx's signer** (then checked against its self-declared
   `validator_address`). The server rejects an invalid gentx with a `gentx_invalid` error + per-invariant detail.
5. Wait for the committee to approve or reject. The committee reviews applications **grouped by submitter**
   (`GET /api/v1/launch/{id}/join/grouped`), matching each submitted operator address and self-delegation against the off-band
   expectation tied to that member's label.
6. After `GENESIS_READY`: download the final genesis file, verify its SHA256 hash, and submit a readiness confirmation.

Validators have no proposal rights and cannot influence governance decisions. Their only active contribution beyond the
join request is the readiness confirmation (attesting they have the correct genesis and binary).

---

## Membership

Every launch is **private-always** — there is no public/browsable launch. A launch is visible to (and submittable by)
exactly its **committee ∪ its member list**; everyone else, even with the launch URL, gets a `404`. A leaked URL plus a
fresh address grants **nothing**.

The **members list** is the per-launch set of **hot actor addresses**, each with a **label**. The committee manages it
directly (no proposal needed) while the launch is in `DRAFT`/`PUBLISHED`/`WINDOW_OPEN`:

- `POST /api/v1/launch/{id}/members` — add an address + label (the label points to off-band verification of who that operator
  is; it is not proof of identity).
- `DELETE /api/v1/launch/{id}/members/{address}` — revoke.
- `GET /api/v1/launch/{id}/members` — committee-only; lists addresses, labels, and add provenance.

For v1 the committee collects operators' hot addresses off-band and adds them directly. (Invite tokens — a labeled,
redeemable token so the address need not be known up front — are planned for v1.x.) The label is what makes
approval-review meaningful: the committee vets each submitted **operator address + self-delegation** against the label's
expectation, so an unexpected operator address under a member surfaces at approval.

---

## Authentication

Every role that signs (coordinator, committee member, validator, and admin) authenticates identically: secp256k1
challenge–response.

1. `POST /api/v1/auth/challenge` with `operator_address` → server returns a nonce
2. Sign a payload containing the challenge with your secp256k1 operator key
3. `POST /api/v1/auth/verify` with the signed payload → server returns a JWT

The JWT is short-lived and must be included as a `Bearer` token on all subsequent requests.

The identity coordd derives is the **HRP-independent account** (the 20 bytes `ripemd160(sha256(pubkey))`),
not the bech32 string — so the **same key authenticates under any account prefix as one identity**
(`cosmos1…` and a launch's own `network1…` are the same member). Only account-form addresses are accepted;
`…valoper…` / `…valcons…` forms are rejected. Challenge, nonce, and session revocation are keyed on the account.

!!! note
`coordd` does not store or manage private keys. Signing always happens client-side.
