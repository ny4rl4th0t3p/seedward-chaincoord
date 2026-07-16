# API Reference

The `coordd` HTTP API uses JSON for all request and response bodies. Authenticated endpoints take a session token as a
`Bearer` token in the `Authorization` header.

The complete, machine-readable contract is the OpenAPI spec at `docs/mkdocs/api/swagger.yaml`, rendered as an
interactive explorer below. It is **generated from the server** and is the source of truth for every endpoint, request
body, and response shape (grouped by tag: auth, launches, join-requests, proposals, committee, readiness, genesis,
allocations, audit, admin, events). This page covers only the cross-cutting conventions the spec can't explain on its
own.

<swagger-ui src="../api/swagger.yaml"/>

---

## Authentication

All committee members and validators authenticate the same way: a two-step secp256k1 challenge–response. The public key
must
be supplied explicitly — bech32 addresses are hashes, so the server cannot recover the key from them.

1. `POST /auth/challenge` with your `operator_address` → the server returns a short-lived `challenge` nonce. (
   Rate-limited: 10 / IP / min and 5 / operator / 5 min.)
2. Sign the canonical JSON of the challenge payload with your secp256k1 operator key.
3. `POST /auth/verify` with the signed payload → the server returns a session token.

Send the token as `Authorization: Bearer <token>` on every authenticated request. Manage sessions with
`GET /auth/session`, `DELETE /auth/session` (current session), and `DELETE /auth/sessions/all` (every device).

All signing is client-side; `coordd` never holds private keys. See [Roles](../concepts/roles.md) for who can do what.

---

## Request signing

Write requests (join, readiness, proposals, committee, `auth/verify`) carry a `signature` over the **canonical JSON** of
the body, produced with the operator's secp256k1 key. Canonical JSON means keys sorted lexicographically, no
insignificant whitespace, and RFC 3339 UTC timestamps.

The `signature` and `pubkey_b64` fields are **excluded** from the signed bytes; the `nonce` is **included** (so it is
bound to the signature — a captured request can't be replayed by swapping the nonce). The server verifies the signature
against `pubkey_b64`, checks that the derived address matches `operator_address`, and consumes the `(operator, nonce)`
pair once (replay protection).

!!! note "Consensus key is not a request field"
A validator's **consensus** (Ed25519) public key is **extracted from the submitted `gentx`** — it is not sent in the
join request. The only key in the request is the secp256k1 **operator** key (`pubkey_b64`), used to verify the request
signature.

---

## Pagination

The paginated list endpoints — `GET /launches`, `GET /launch/{id}/join`, `GET /launch/{id}/proposals`, and
`GET /admin/coordinators` — accept `?page=` (default `1`) and `?per_page=` (default `20`, max `100`) and return a
paginated envelope:

```json
{
  "items": [
    ...
  ],
  "total": 42,
  "page": 1,
  "per_page": 20
}
```

Other list-style endpoints (`dashboard`, `peers`, `gentxs`, `audit`) return the full set and are not paginated.

---

## Errors

Every error uses one envelope:

```json
{
  "error": {
    "code": "bad_request",
    "message": "human-readable detail",
    "request_id": "<uuid>"
  }
}
```

`code` is a stable machine string (`not_found`, `conflict`, `unauthorized`, `forbidden`, `bad_request`,
`too_many_requests`, `gentx_invalid`, `internal_error`, …). `request_id` is included on `5xx` responses so you can
correlate them with server logs.

### `gentx_invalid` (400)

When a join request's `gentx` fails pre-acceptance validation, the error carries a per-invariant breakdown so the
submitter can see exactly which checks failed (the server runs the same validation library used by the CLI and the
in-browser validator):

```json
{
  "error": {
    "code": "gentx_invalid",
    "message": "gentx validation failed: commission_bounds, self_delegation",
    "invariants": [
      {
        "invariant": "commission_bounds",
        "ok": false,
        "reason": "rate 0.30 above launch ceiling 0.20"
      },
      {
        "invariant": "self_delegation",
        "ok": false,
        "reason": "self-bond 100 below launch floor 1000000"
      },
      {
        "invariant": "bond_denom",
        "ok": true
      }
    ]
  }
}
```

---

## Streaming & health

- `GET /launch/{id}/events` — Server-Sent Events stream; emits on every state change and proposal execution.
  **Visibility-gated** (optionalAuth): launches are private-always, so a caller who is not on the launch's
  committee or members list — including an anonymous one — gets `404`. Connect directly to the server rather
  than through a buffering reverse proxy.
- `GET /healthz` — deep liveness probe: `200 {"status":"ok"}` when the database and audit log are both healthy,
  `503 {"status":"unavailable"}` if a dependency is down (see [Setup → Observability](setup.md#observability)).
  Used by Docker health checks and load-balancer probes.