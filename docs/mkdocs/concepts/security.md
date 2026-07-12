# Trust model

What the coordination server **can** and **cannot** do. coordd is a *coordinator*, not a custodian.

## The server holds no user keys

Coordinators and validators authenticate by signing a challenge with their own wallet; coordd stores no
private keys and pre-registers no public keys — identity is the address, proven per request. So coordd
**cannot impersonate** a coordinator or a validator. (See [Roles → Authentication](roles.md).)

## Governance decisions require M-of-N

Every **governance decision** that moves a launch forward — approving or removing validators, closing the
window, publishing genesis, changing the committee, approving an allocation file — is an M-of-N committee
proposal. coordd **cannot execute a quorum action below the threshold**, and a single VETO kills a
proposal. coordd can *reject*, *filter*, and *rate-limit* — it cannot *forge* a committee decision.
Operational steps (uploading a genesis or allocation file, editing the members list, opening the window,
or the lead's emergency cancel) are single-actor by design. (See [Proposals & M-of-N](proposals.md).)

## Coordinate over signed facts

The server stores signed facts and applies rules over them; it never fabricates them. A rehearsal result
is an Ed25519-signed fact from the rehearsal service, verified against a per-launch trusted key — coordd
**records** it, it doesn't produce it.

## The history can't be rewritten

Every committee proposal, genesis upload, and lifecycle transition is recorded in a tamper-evident, hash-chained, signed audit log (see
[Reference → Audit Log](../reference/audit.md)): an entry can't be altered or removed without detection,
and the server **refuses to start on a broken chain**. So even a compromised server can't silently rewrite
the past.

## What coordd is trusted for — and its limits

coordd is trusted to serve state honestly and to enforce the state machine + quorum. It is **not** trusted
with keys, and it **cannot** forge signatures, execute below quorum, or rewrite history. The residual
trust — that coordd serves the *correct current* state — is bounded by the audit log: every governance
action and lifecycle transition is a signed, chained entry any committee member can independently verify.
