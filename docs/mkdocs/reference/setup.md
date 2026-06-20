# coordd — Setup & Configuration

This document covers how to run and configure the `coordd` server in both development and production environments.

!!! danger "Proof of concept — not for production use"
seedward-chaincoord is research-grade software. APIs, data formats, and behaviours may change without notice. **Do not
use it for mainnet launches or any environment where correctness and availability are required.**

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
genesis_path: "./data/genesis"
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

For nginx, include `proxy_set_header X-Real-IP $remote_addr;` so IP-based rate limiting on `POST /auth/challenge` sees
the real client address rather than the proxy address.

#### Local dev

Plain HTTP on loopback (`127.0.0.1` or `::1`) suppresses the warning automatically — no flag needed.

---

## Configuration Reference

| Key                      | Env var                        | Flag                       | Default               | Required |
|--------------------------|--------------------------------|----------------------------|-----------------------|----------|
| `listen_addr`            | `COORD_LISTEN_ADDR`            | `--listen-addr`            | `:8080`               | No       |
| `db_path`                | `COORD_DB_PATH`                | `--db-path`                | —                     | Yes      |
| `audit_log_path`         | `COORD_AUDIT_LOG_PATH`         | `--audit-log-path`         | —                     | Yes      |
| `genesis_path`           | `COORD_GENESIS_PATH`           | `--genesis-path`           | —                     | Yes      |
| `audit_private_key`      | `COORD_AUDIT_PRIVATE_KEY`      | —                          | —                     | Yes¹     |
| `audit_private_key_file` | `COORD_AUDIT_PRIVATE_KEY_FILE` | —                          | —                     | Yes¹     |
| `jwt_private_key`        | `COORD_JWT_PRIVATE_KEY`        | —                          | —                     | Yes²     |
| `jwt_private_key_file`   | `COORD_JWT_PRIVATE_KEY_FILE`   | —                          | —                     | Yes²     |
| `log_level`              | `COORD_LOG_LEVEL`              | `--log-level`              | `info`                | No       |
| `cors_origins`           | `COORD_CORS_ORIGINS`           | `--cors-origins`           | *(disabled)*          | No       |
| `admin_addresses`        | `COORD_ADMIN_ADDRESSES`        | —                          | *(none)*              | No       |
| `launch_policy`          | `COORD_LAUNCH_POLICY`          | —                          | `restricted`          | No       |
| `genesis_host_mode`      | `COORD_GENESIS_HOST_MODE`      | `--genesis-host-mode`      | `false`               | No       |
| `genesis_max_bytes`      | `COORD_GENESIS_MAX_BYTES`      | `--genesis-max-bytes`      | `734003200` (700 MiB) | No       |
| `tls_cert`               | `COORD_TLS_CERT`               | `--tls-cert`               | —                     | No       |
| `tls_key`                | `COORD_TLS_KEY`                | `--tls-key`                | —                     | No       |
| `insecure_no_tls`        | `COORD_INSECURE_NO_TLS`        | `--insecure-no-tls`        | `false`               | No       |
| `insecure_no_rate_limit` | `COORD_INSECURE_NO_RATE_LIMIT` | `--insecure-no-rate-limit` | `false`               | No       |
| `insecure_no_ssrf_check` | `COORD_INSECURE_NO_SSRF_CHECK` | —                          | `false`               | No       |

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

Comma-separated list of operator addresses that have admin privileges (`/admin/*` endpoints). If empty, no address has
admin access.

```bash
export COORD_ADMIN_ADDRESSES="cosmos1abc...,cosmos1def..."
```

### `launch_policy`

Controls who may create new launches:

- `restricted` *(default)* — only addresses on the coordinator allowlist (`/admin/coordinators`) may create a launch
- `open` — any authenticated address may create a launch

### `genesis_host_mode`

When `true`, `coordd` accepts raw genesis file uploads (`POST /launch/:id/genesis`) and serves them directly from disk.
When `false` (the default), only attestor mode is available — coordinators register an external URL and SHA-256 hash.

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

Disables all rate limiters: the HTTP per-IP middleware on `POST /auth/challenge` (10 req/IP/min) and validator write
endpoints (60 req/IP/min), and the storage-layer per-operator limit on challenge issuance (5 req/operator/5 min). **Only
for automated test environments** — do not enable in production.

### `insecure_no_ssrf_check`

Disables DNS-resolution and private-IP validation on user-supplied RPC URLs (`monitor_rpc_url`) and genesis attestor
URLs. Only enable this in trusted environments — for example, the smoke-test Docker network, where RPC hostnames are
internal container names that would fail the SSRF check. **Do not enable in production.**

### `log_level`

Controls verbosity. Accepted values: `debug`, `info`, `warn`, `error`.

- `debug` — human-readable console output (stderr), verbose. Use in development only.
- `info` and above — structured JSON to stdout. Use in production.