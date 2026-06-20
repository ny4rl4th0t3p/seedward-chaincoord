# Roles

chaincoord has three distinct roles. A single person or organisation can hold more than one role.

---

## Lead Coordinator

The lead coordinator is the committee member who creates the launch and declares the initial committee. Their one privilege beyond ordinary committee members is the emergency **cancel**: they can cancel the launch from any non-terminal state without a proposal or quorum. (Opening the application window is *not* lead-exclusive — any committee member may call `open-window`.)

Every committee has exactly one lead. The lead can change if the current lead is replaced via a `REPLACE_COMMITTEE_MEMBER` proposal — the replacement automatically inherits the lead role.

**Responsibilities:**

- Create the launch and declare the initial committee
- Upload the initial genesis file
- Open the application window
- Assemble the final genesis file locally (`gaiad genesis collect-gentxs`) and upload it
- Set the monitor RPC URL so `coordd` polls for block production

---

## Coordinator

Any member of the launch committee. Coordinators govern every state transition and governance decision through proposals.

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

A validator is an operator who wants to participate in the genesis validator set. They interact with `coordd` during `WINDOW_OPEN` (to apply) and again after `GENESIS_READY` (to download the final genesis and confirm readiness).

**What validators do:**

1. Authenticate to `coordd` (same secp256k1 challenge–response as coordinators)
2. Generate a `gentx` locally using their chain binary (e.g. `gaiad genesis gentx`)
3. Submit a join request carrying the `gentx`, peer address, and RPC endpoint (the consensus key is read from the `gentx`)
4. Wait for the committee to approve or reject their application
5. After `GENESIS_READY`: download the final genesis file, verify its SHA256 hash, and submit a readiness confirmation

Validators have no proposal rights and cannot influence governance decisions. Their only active contribution beyond the join request is the readiness confirmation (attesting they have the correct genesis and binary).

---

## Authentication

All three roles authenticate identically: secp256k1 challenge–response.

1. `POST /auth/challenge` with `operator_address` → server returns a nonce
2. Sign a payload containing the challenge with your secp256k1 operator key
3. `POST /auth/verify` with the signed payload → server returns a JWT

The JWT is short-lived and must be included as a `Bearer` token on all subsequent requests.

!!! note
    `coordd` does not store or manage private keys. Signing always happens client-side.