# coordd — Setup & Configuration

This document covers how to run and configure the `coordd` server in both development and production environments.

!!! note "Newly released"
seedward-chaincoord **v1.0.0** is the first stable release. It has not had an external security audit — review the
threat model and verify on your own setup before high-value use.

---

## Configuration

`coordd` resolves configuration from three sources, in order of precedence (highest first):

1. **CLI flags** (`--listen-addr`, `--db-path`, …)
2. **Environment variables** (`COORD_*`)
3. **Config file** (`config.yaml` — searched in `.`, `$HOME/.coordd`, `/etc/coordd`)

Most options are available through all three sources, but a few have **no CLI flag** and must come from an env var or
the config file: the signing keys (`audit_private_key`/`_file`, `jwt_private_key`/`_file`), `admin_addresses`,
`launch_policy`, and `insecure_no_ssrf_check` (see the Flag column in the reference table below). Environment variables
are the recommended approach for production deployments; the config file is convenient for local development.

---

## Development

### Minimal `config.yaml`

```yaml
listen_addr: ":8080"
db_path: "./data/coord.db"
audit_log_path: "./data/audit.jsonl"
files_path: "./data/genesis"
log_level: "debug"
cors_origins: "http://localhost:3000"
audit_private_key_file: "./data/audit_key"
jwt_private_key_file: "./data/jwt_key"
```

Generate the two key files first:

```bash
mkdir -p data
bin/coordd keygen > data/audit_key
bin/coordd keygen > data/jwt_key
chmod 600 data/audit_key data/jwt_key
```

`log_level: debug` enables the human-readable `ConsoleWriter` output instead of JSON, which is easier to read during
development.

### CORS

The web app (Next.js) runs on `http://localhost:3000` by default. Set `cors_origins` to that origin so the browser
allows cross-origin requests (required for the SSE stream, which connects directly from the browser):

```yaml
# config.yaml (dev)
cors_origins: "http://localhost:3000"
```

Or via environment variable:

```bash
export COORD_CORS_ORIGINS="http://localhost:3000"
```

Multiple origins are comma-separated:

```bash
export COORD_CORS_ORIGINS="http://localhost:3000,https://coord.example.com"
```

> **Do not use `*` in development** unless you have no other option — `AllowCredentials: true` is set on the server, and
> browsers will reject credentialed requests to a wildcard origin. Use the exact origin instead.

### Running the server

```bash
# Build
make build-server

# Run migrations first
./bin/coordd migrate --config config.yaml

# Start
./bin/coordd serve --config config.yaml
```

---

## Production

### TLS

`coordd` supports three deployment modes:

| Mode           | Description                                                                                                       |
|----------------|-------------------------------------------------------------------------------------------------------------------|
| **Native TLS** | `coordd` terminates TLS itself using `--tls-cert` / `--tls-key`                                                   |
| **Infra TLS**  | TLS is terminated at a load balancer, ingress, or reverse proxy; `coordd` binds plain HTTP on a private interface |
| **Local dev**  | Plain HTTP on loopback; TLS not needed                                                                            |

#### Native TLS

Pass paths to a PEM certificate and private key:

```bash
coordd serve --tls-cert /etc/coordd/cert.pem --tls-key /etc/coordd/key.pem ...
```

Or via environment variables:

```bash
export COORD_TLS_CERT=/etc/coordd/cert.pem
export COORD_TLS_KEY=/etc/coordd/key.pem
```

Both must be set together or both left empty. Setting only one is a configuration error.

For local HTTPS testing (e.g. browser wallet testing that requires a secure context):

```bash
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 \
  -keyout key.pem -out cert.pem -days 365 -nodes -subj "/CN=localhost"
coordd serve --tls-cert cert.pem --tls-key key.pem ...
```

#### Infra TLS (reverse proxy / load balancer)

When TLS is terminated upstream, `coordd` binds plain HTTP. Set `COORD_INSECURE_NO_TLS=true` to suppress the startup
warning — this makes the intent self-documenting in your deploy config:

```bash
export COORD_INSECURE_NO_TLS=true
```

Minimal [Caddy](https://caddyserver.com) config (automatic HTTPS via Let's Encrypt):

```
your.domain {
  reverse_proxy localhost:8080
}
```

For nginx, include `proxy_set_header X-Real-IP $remote_addr;` so IP-based rate limiting on `POST /api/v1/auth/challenge` sees
the real client address rather than the proxy address.

#### Local dev

Plain HTTP on loopback (`127.0.0.1` or `::1`) suppresses the warning automatically — no flag needed.

---

## Observability

### Health check — `GET /healthz`

Unauthenticated. Probes liveness dependencies and returns:

- `200 {"status":"ok"}` when the database is queryable (`SELECT 1`) **and** the audit log file is present.
- `503 {"status":"unavailable"}` when either fails — a DB error, or the audit log path can't be `stat`'d
  (unmounted, deleted, or unreadable). It is an existence probe, not a write test, so a mounted-but-full disk still
  passes.

Failure detail is written to the server log, not the response body, so an unauthenticated caller learns only
up/down. Point your orchestrator's liveness/readiness probe at it.

Access lines are **leveled by response class**: a successful request (`< 400`, including this frequent 200 probe)
logs at **debug**, a failed one (`>= 400`) at **info**. So at the normal `info` level the access log shows only
failures — a healthy `/healthz` stays silent, while a failing one (`503`) is visible — and a live switch to `debug`
(see [Log level](#log-level)) turns on full request tracing.

### Metrics — `GET /metrics`

Unauthenticated Prometheus endpoint exposing the default Go runtime and process metrics (goroutines, heap, GC,
open file descriptors, CPU). **Network-restrict it** — it is not behind auth, so keep it off the public
interface and scrape it from your monitoring network.

### Log level

The verbosity is set at startup by [`log_level`](#log_level) and can be changed **live**, without a restart
(admin only):

- `GET /api/v1/admin/log-level` → `{"level":"info"}` — the current level.
- `POST /api/v1/admin/log-level` with `{"level":"debug"}` — set it. Accepts `trace`, `debug`, `info`, `warn`, `error`
  (`fatal`, `panic`, and `disabled` are rejected — they would silence the log).

The change is **in-memory**: it takes effect immediately and reverts to the configured `log_level` on restart. Only
the threshold changes — the console-vs-JSON output format is fixed at startup (JSON unless the server *started* at
`debug`). The change itself is logged at `warn`, recording the old and new level and the admin who made it. A handy pairing
with the access-log leveling above: drop to `debug` to trace all requests (successful ones and health probes
included) live, then back to `info` to see only failures again.

### Security response headers

Every response carries defensive headers:

| Header                      | Value                                 | When                       |
|-----------------------------|---------------------------------------|----------------------------|
| `X-Content-Type-Options`    | `nosniff`                             | always                     |
| `X-Frame-Options`           | `DENY`                                | always                     |
| `Strict-Transport-Security` | `max-age=63072000; includeSubDomains` | when coordd terminates TLS |

HSTS is added only when `coordd` terminates TLS itself (native TLS — `tls_cert`/`tls_key` set). Behind an
upstream TLS proxy (infra TLS mode), add HSTS at the proxy instead.

---

## Configuration Reference

| Key                        | Env var                          | Flag                       | Default               | Required |
|----------------------------|----------------------------------|----------------------------|-----------------------|----------|
| `listen_addr`              | `COORD_LISTEN_ADDR`              | `--listen-addr`            | `:8080`               | No       |
| `db_path`                  | `COORD_DB_PATH`                  | `--db-path`                | —                     | Yes      |
| `audit_log_path`           | `COORD_AUDIT_LOG_PATH`           | `--audit-log-path`         | —                     | Yes      |
| `files_path`               | `COORD_FILES_PATH`               | `--files-path`             | —                     | Yes      |
| `audit_private_key`        | `COORD_AUDIT_PRIVATE_KEY`        | —                          | —                     | Yes¹     |
| `audit_private_key_file`   | `COORD_AUDIT_PRIVATE_KEY_FILE`   | —                          | —                     | Yes¹     |
| `jwt_private_key`          | `COORD_JWT_PRIVATE_KEY`          | —                          | —                     | Yes²     |
| `jwt_private_key_file`     | `COORD_JWT_PRIVATE_KEY_FILE`     | —                          | —                     | Yes²     |
| `log_level`                | `COORD_LOG_LEVEL`                | `--log-level`              | `info`                | No       |
| `cors_origins`             | `COORD_CORS_ORIGINS`             | `--cors-origins`           | *(disabled)*          | No       |
| `admin_addresses`          | `COORD_ADMIN_ADDRESSES`          | —                          | *(none)*              | No       |
| `launch_policy`            | `COORD_LAUNCH_POLICY`            | —                          | `restricted`          | No       |
| `genesis_host_mode`        | `COORD_GENESIS_HOST_MODE`        | `--genesis-host-mode`      | `false`               | No       |
| `genesis_max_bytes`        | `COORD_GENESIS_MAX_BYTES`        | `--genesis-max-bytes`      | `734003200` (700 MiB) | No       |
| `tls_cert`                 | `COORD_TLS_CERT`                 | `--tls-cert`               | —                     | No       |
| `tls_key`                  | `COORD_TLS_KEY`                  | `--tls-key`                | —                     | No       |
| `insecure_no_tls`          | `COORD_INSECURE_NO_TLS`          | `--insecure-no-tls`        | `false`               | No       |
| `insecure_no_rate_limit`   | `COORD_INSECURE_NO_RATE_LIMIT`   | `--insecure-no-rate-limit` | `false`               | No       |
| `insecure_no_ssrf_check`   | `COORD_INSECURE_NO_SSRF_CHECK`   | —                          | `false`               | No       |
| `rehearsal_ops_token`      | `COORD_REHEARSAL_OPS_TOKEN`      | —                          | *(bridge disabled)*   | No       |
| `rehearsal_ops_token_file` | `COORD_REHEARSAL_OPS_TOKEN_FILE` | —                          | *(bridge disabled)*   | No       |
| `rehearsal_lease_ttl`      | `COORD_REHEARSAL_LEASE_TTL`      | —                          | `45m`                 | No       |
| `rehearsal_gate`           | `COORD_REHEARSAL_GATE`           | —                          | `off`                 | No       |
| `audit_startup_verify`     | `COORD_AUDIT_STARTUP_VERIFY`     | —                          | `full`                | No       |

¹ Exactly one of `audit_private_key` (inline base64) or `audit_private_key_file` (path) must be set.  
² Exactly one of `jwt_private_key` (inline base64) or `jwt_private_key_file` (path) must be set.

---

### `audit_private_key` / `audit_private_key_file`

Ed25519 seed for signing audit log entries. Generate with `coordd keygen`. In production, prefer `_FILE` so the raw seed
never appears in environment variable listings or container inspection output:

```bash
coordd keygen | docker secret create audit_key -
# then set COORD_AUDIT_PRIVATE_KEY_FILE=/run/secrets/audit_key
```

### `jwt_private_key` / `jwt_private_key_file`

Ed25519 seed for signing session JWTs. Must be **different** from the audit key. Same file-based pattern applies.

### `admin_addresses`

Comma-separated list of operator addresses that have admin privileges (`/api/v1/admin/*` endpoints). If empty, no address has
admin access.

```bash
export COORD_ADMIN_ADDRESSES="cosmos1abc...,cosmos1def..."
```

### `launch_policy`

Controls who may create new launches:

- `restricted` *(default)* — only addresses on the coordinator allowlist (`/api/v1/admin/coordinators`) may create a launch
- `open` — any authenticated address may create a launch

### `genesis_host_mode`

When `true`, `coordd` accepts raw genesis file uploads (`POST /api/v1/launch/:id/genesis`) and serves them directly from disk.
When `false` (the default), only attestor mode is available — committee members register an external URL and SHA-256
hash.

### `genesis_max_bytes`

Maximum raw genesis upload size in bytes when host mode is enabled. Defaults to 700 MiB. Ignored when
`genesis_host_mode` is `false`.

### `cors_origins`

Comma-separated list of allowed origins for cross-origin requests. Only needed when a browser-based client (the
validator web app) connects to `coordd` from a different origin.

- Leave empty to disable CORS headers entirely (default — safe for API-only or same-origin deployments).
- Set to the exact origin(s) of the web app in both dev and prod. Wildcards are not supported when credentials are
  involved.

### `tls_cert` / `tls_key`

Paths to a PEM-encoded TLS certificate and private key. Both must be set together, or both left empty. When set,
`coordd` calls `ListenAndServeTLS` and handles TLS termination itself (native TLS mode). See the [TLS section](#tls)
above for all deployment modes.

### `insecure_no_tls`

Suppresses the startup warning when TLS is not configured and the listen address is not loopback. Set this when TLS is
terminated upstream (load balancer, ingress, reverse proxy) and `coordd` binds plain HTTP on a private network
interface. The Docker Compose file sets this automatically.

### `insecure_no_rate_limit`

Disables all rate limiters: the HTTP per-IP middleware on `POST /api/v1/auth/challenge` (10 req/IP/min) and validator write
endpoints (60 req/IP/min), and the storage-layer per-operator limit on challenge issuance (5 req/operator/5 min). **Only
for automated test environments** — do not enable in production.

### `insecure_no_ssrf_check`

Disables DNS-resolution and private-IP validation on user-supplied RPC URLs (`monitor_rpc_url`) and genesis attestor
URLs. Only enable this in trusted environments — for example, the smoke-test Docker network, where RPC hostnames are
internal container names that would fail the SSRF check. **Do not enable in production.**

### `rehearsal_ops_token` / `rehearsal_ops_token_file`

Shared bearer token authenticating the **rehearsal bridge** (ops plane) endpoints under
`/api/v1/bridge/*` — a
headless service-to-service credential, not a wallet. It is an arbitrary secret you generate yourself
(any high-entropy string, e.g. `openssl rand -hex 32`), configured identically on both sides. When set,
the rehearsal service presents it as
`Authorization: Bearer <token>` to pull the approved input set and post signed results. **Leave unset to
disable the bridge** (all bridge requests are rejected, fail-closed). Deployment-wide, not per-launch;
prefer the `_file` variant (secret manager) over the plain env var. Rotation is "swap the secret + reload."
Deploy the `/api/v1/bridge/*` endpoints on an **internal network only** (e.g. an ingress rule restricting
the prefix), since the ops plane must not be internet-reachable.

### `rehearsal_lease_ttl`

How long a claimed rehearsal run (`POST /api/v1/bridge/launches/{id}/rehearsal-claim`) holds its single-writer
lease before it is treated as stale and re-claimable. A crashed runner self-heals after this window without
operator intervention; set it comfortably above your longest rehearsal. Accepts a Go duration string
(`45m`, `1h`, `90m`). Defaults to **45m** when unset. For an immediate override of a stuck lease, a committee member can
call
`POST /api/v1/launch/{id}/rehearsal/{attempt_id}/reset` instead of waiting for expiry.

### `rehearsal_gate`

Opt-in policy for whether a launch may finalize genesis (`WINDOW_CLOSED → GENESIS_READY`) only after a
passing rehearsal. **Default `off` — coordd runs fully standalone; rehearsal is an optional bolt-on, never a
hard dependency.**

- `off` (default) — the gate is never consulted. A deployment with no rehearsal service is unaffected.
- `advisory` — the gate is evaluated and, when unsatisfied, recorded in the audit log, but never blocks.
- `required` — publishing genesis is rejected (409) unless the launch's **latest** rehearsal fact is `PASS`
  **and current** (its `input_set_hash` still matches the present approved set). Enforced when the
  `PUBLISH_GENESIS` proposal is raised, with a re-check when it executes.

`required` needs the rehearsal bridge enabled (a `rehearsal_ops_token`) — coordd **refuses to start** with
`rehearsal_gate=required` and no ops token. It also requires a per-launch trusted rehearsal service pubkey
(set via `PATCH /api/v1/launch/{id}`); a `required` launch with no configured service is rejected at publish time.

> Independent of this gate, coordd always enforces that a published genesis matches the approved validator
> set it was assembled from (the set can change in `WINDOW_CLOSED` via approve/remove) — a correctness
> invariant, not an opt-in.

### `log_level`

Controls verbosity at startup. Accepted values: `debug`, `info`, `warn`, `error` (`trace` also works but is rarely
needed).

- `debug` — human-readable console output (stderr), verbose. Use in development only.
- `info` and above — structured JSON to stdout. Use in production.

Changeable at runtime without a restart via [`POST /api/v1/admin/log-level`](#log-level); a runtime change is in-memory and
reverts to this configured value on restart.

### `audit_startup_verify`

Depth of the boot-time audit-log integrity check (see
[Audit Log → Startup and live integrity checks](audit.md#startup-and-live-integrity-checks)):

- `full` (default) — scan the whole log on startup (Ed25519 signatures, hash-chain, timestamps) in addition to
  the cheap chain-tip check. Tamper or corruption **refuses startup**; a backward timestamp only warns.
- `tail` — only the cheap chain-tip check. For operators whose log has grown large; pair it with a scheduled
  `coordd audit verify`.