# Roles

seedward-chaincoord has three distinct roles. A single person or organisation can hold more than one role.

---

## Lead Coordinator

The lead coordinator is the committee member who creates the launch and declares the initial committee. Their one
privilege beyond ordinary committee members is the emergency **cancel**: they can cancel the launch from any
non-terminal state without a proposal or quorum. (Opening the application window is *not* lead-exclusive — any committee
member may call `open-window`.)

Every committee has exactly one lead. The lead can change if the current lead is replaced via a
`REPLACE_COMMITTEE_MEMBER` proposal — the replacement automatically inherits the lead role.

**Responsibilities:**

- Create the launch and declare the initial committee
- Upload the initial genesis file
- Open the application window
- Assemble the final genesis file locally (`gaiad genesis collect-gentxs`) and upload it
- Set the monitor RPC URL so `coordd` polls for block production

---

## Coordinator

Any member of the launch committee. Coordinators govern every state transition and governance decision through
proposals.

**Key facts:**

- Identified by their Cosmos SDK operator address (bech32)
- Authenticate to `coordd` via a secp256k1 challenge–response (the same key used on-chain)
- Can raise any proposal type at any time (subject to the launch's current status)
- Each coordinator signs or vetoes proposals raised by others
- Signatures are verified server-side against the member's declared public key

**What coordinators cannot do unilaterally:**

- Execute any state transition alone (unless M=1)
- Modify the launch record outside of proposals
- Override another coordinator's veto

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
3. Authenticate to `coordd` with their **hot** address (same secp256k1 challenge–response as coordinators).
4. Submit a join request carrying the `gentx`, peer address, and RPC endpoint. The consensus key is read from the
   gentx; the operator address is **derived from the gentx's signer** (then checked against its self-declared
   `validator_address`). The server rejects an invalid gentx with a `gentx_invalid` error + per-invariant detail.
5. Wait for the committee to approve or reject. The committee reviews applications **grouped by submitter**
   (`GET /launch/{id}/join/grouped`), matching each submitted operator address and self-delegation against the off-band
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

- `POST /launch/{id}/members` — add an address + label (the label points to off-band verification of who that operator
  is; it is not proof of identity).
- `DELETE /launch/{id}/members/{address}` — revoke.
- `GET /launch/{id}/members` — committee-only; lists addresses, labels, and add provenance.

For v1 the committee collects operators' hot addresses off-band and adds them directly. (Invite tokens — a labeled,
redeemable token so the address need not be known up front — are planned for v1.x.) The label is what makes
approval-review meaningful: the committee vets each submitted **operator address + self-delegation** against the label's
expectation, so an unexpected operator address under a member surfaces at approval.

---

## Authentication

All three roles authenticate identically: secp256k1 challenge–response.

1. `POST /auth/challenge` with `operator_address` → server returns a nonce
2. Sign a payload containing the challenge with your secp256k1 operator key
3. `POST /auth/verify` with the signed payload → server returns a JWT

The JWT is short-lived and must be included as a `Bearer` token on all subsequent requests.

!!! note
`coordd` does not store or manage private keys. Signing always happens client-side.