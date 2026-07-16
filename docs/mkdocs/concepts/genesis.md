# Genesis

A launch carries two genesis hashes, uploaded at different phases:

- **Initial genesis** (`InitialGenesisSHA256`) — the bare, pre-gentx genesis, uploaded in `DRAFT`.
- **Final genesis** (`FinalGenesisSHA256`) — the committee-assembled, post-gentx genesis, uploaded in
  `WINDOW_CLOSED`.

coordd **never assembles genesis** — a committee member builds it locally (gentool folds the approved
allocation files into a base genesis; the chain binary's `collect-gentxs` adds the gentxs) and uploads it.
coordd only runs light well-formedness checks and anchors the hash; **the committee is what validates the
genesis it publishes** — by review (optionally backed by a rehearsal) plus the M-of-N `PUBLISH_GENESIS`
attestation.

## Dual-mode upload

Both go through `POST /launch/{id}/genesis?type=initial|final`, in one of two modes:

- **Attestor mode** (default, `application/json`) — body `{url, sha256[, genesis_time]}`. coordd stores
  the URL + hash only (SSRF-checked), keeps no bytes, and serves reads as a **302** to the external URL.
- **Host mode** (`application/octet-stream`, gated by `COORD_GENESIS_HOST_MODE`) — bytes stream to disk,
  capped by `genesis_max_bytes` (`413` on exceed); reads stream the file.

(This is the same by-reference model allocation files use.)

## Final-genesis checks

coordd runs a few **mechanical guardrail** checks on a final upload (no chain binary invoked): the bytes
are well-formed JSON, `chain_id` matches the record, `genesis_time` is set and in the future,
`len(gen_txs) == len(approved)`, and each approved validator's consensus pubkey appears exactly once (no
duplicates). It then binds `FinalGenesisInputSetHash` (the approved-set fingerprint, re-checked at
`PUBLISH_GENESIS` so a genesis that no longer matches the set can't be finalized). These are guardrails,
**not genesis validation**: the genesis schema, balances, supply, and bonded pool are the committee's to
vet (with gentool / rehearsal) — the file is opaque to coordd.

Because attestor-mode can't read the file, `genesis_time` is a **required request field** for an
attestor-mode final upload (and must be in the future).

## Verifying the genesis — reproduce, don't trust

Those checks are mechanical guardrails, and coordd never assembles the genesis itself — so the trust anchor
is **independent reproduction by the committee**, not the proposer's uploaded file. The approved inputs are
deterministic and pinned: the approved gentxs (`GET /launch/{id}/gentxs`), the approved allocation files, and
the chain record, all fingerprinted by `FinalGenesisInputSetHash`. So any committee member can rebuild the
genesis locally (`gentool` folds the allocation files; the chain binary's `collect-gentxs` adds the gentxs)
and confirm their result's hash equals the proposer's `FinalGenesisSHA256`. The M-of-N `PUBLISH_GENESIS`
signatures are that **reproduction-backed attestation** — each signer vouches for a hash they can regenerate
from the approved set, not one they merely downloaded.

The optional **rehearsal** service is the automated, enforceable form of this: `rehearsald` pulls the same
approved input set, rebuilds the genesis, boots an ephemeral chain, and posts a *signed* PASS/FAIL bound to
the input-set fingerprint — and the rehearsal gate can require a PASS before `PUBLISH_GENESIS` executes.
(Validators, at `GENESIS_READY`, download the file and verify its SHA256 to run their node — last-mile
distribution against the already-attested hash, not the trust anchor.)

## Publishing

Uploading only records the hash. The `WINDOW_CLOSED → GENESIS_READY` transition happens when the
`PUBLISH_GENESIS` committee proposal executes (see [Proposals & M-of-N](proposals.md)).

## Changing the genesis time

`UPDATE_GENESIS_TIME` updates the launch record's `genesis_time` and invalidates readiness confirmations.
Note that coordd does **not** rebuild the genesis file (it never assembles genesis) — producing a new
genesis with a different time is done via `REVISE_GENESIS` → re-upload. *(The exact intended semantics of
`UPDATE_GENESIS_TIME` versus a re-upload are under review.)*
