# CLI Reference

## coordd

The `coordd` binary is the coordination server. It exposes a subcommand tree; run `coordd --help` for the full list.

```
coordd [--config <path>] <command>
```

The `--config` flag is persistent — it can be passed before any subcommand. When omitted, `coordd` searches for
`config.yaml` in `.`, `$HOME/.coordd`, and `/etc/coordd`.

---

### coordd serve

Start the HTTP server.

```
coordd serve [flags]
```

| Flag                       | Env var                        | Description                                        |
|----------------------------|--------------------------------|----------------------------------------------------|
| `--listen-addr`            | `COORD_LISTEN_ADDR`            | Address to listen on (default `:8080`)             |
| `--db-path`                | `COORD_DB_PATH`                | Path to SQLite database file                       |
| `--audit-log-path`         | `COORD_AUDIT_LOG_PATH`         | Path to audit log JSONL file                       |
| `--files-path`             | `COORD_FILES_PATH`             | Directory for genesis + allocation file storage    |
| `--log-level`              | `COORD_LOG_LEVEL`              | Log verbosity: `debug`, `info`, `warn`, `error`    |
| `--cors-origins`           | `COORD_CORS_ORIGINS`           | Comma-separated allowed CORS origins               |
| `--tls-cert`               | `COORD_TLS_CERT`               | Path to TLS certificate (PEM)                      |
| `--tls-key`                | `COORD_TLS_KEY`                | Path to TLS private key (PEM)                      |
| `--insecure-no-tls`        | `COORD_INSECURE_NO_TLS`        | Suppress TLS warning (infra TLS mode)              |
| `--insecure-no-rate-limit` | `COORD_INSECURE_NO_RATE_LIMIT` | Disable all rate limiters (test environments only) |
| `--genesis-host-mode`      | `COORD_GENESIS_HOST_MODE`      | Accept raw genesis file uploads                    |
| `--genesis-max-bytes`      | `COORD_GENESIS_MAX_BYTES`      | Max genesis upload size in bytes (default 700 MiB) |

Keys and policy are only configurable via env vars or config file — they have no CLI flags:

| Env var                        | Description                                        |
|--------------------------------|----------------------------------------------------|
| `COORD_AUDIT_PRIVATE_KEY`      | Base64 Ed25519 seed for audit log signing          |
| `COORD_AUDIT_PRIVATE_KEY_FILE` | Path to file containing the audit key seed         |
| `COORD_JWT_PRIVATE_KEY`        | Base64 Ed25519 seed for JWT signing                |
| `COORD_JWT_PRIVATE_KEY_FILE`   | Path to file containing the JWT key seed           |
| `COORD_ADMIN_ADDRESSES`        | Comma-separated admin operator addresses           |
| `COORD_LAUNCH_POLICY`          | `open` or `restricted` (default)                   |
| `COORD_INSECURE_NO_SSRF_CHECK` | Disable SSRF check on RPC URLs (trusted envs only) |

The rehearsal/bridge keys (`COORD_REHEARSAL_OPS_TOKEN[_FILE]`, `COORD_REHEARSAL_GATE`,
`COORD_REHEARSAL_LEASE_TTL`) are also env-only. See [Setup & Configuration](setup.md) for the full reference.

---

### coordd migrate

Run database schema migrations and exit. Safe to run repeatedly — migrations are idempotent.

```
coordd migrate [--db-path <path>]
```

Run this before starting `coordd serve` for the first time and after any upgrade.

---

### coordd keygen

Generate a cryptographically random Ed25519 seed and print it as base64 to stdout.

```
coordd keygen
```

Run it twice to produce the two keys `coordd` requires (`audit_private_key` and `jwt_private_key`). They must be
different.

```bash
coordd keygen > data/audit_key
coordd keygen > data/jwt_key
chmod 600 data/audit_key data/jwt_key
```

For production, pipe directly into your secrets manager to avoid the seed appearing in shell history:

```bash
coordd keygen | docker secret create audit_key -
coordd keygen | docker secret create jwt_key -
```

---

### coordd version

Print the build version and exit.

```
coordd version
```

---

### coordd audit verify

Verify the structural integrity and Ed25519 signatures of a local audit log JSONL file.

```
coordd audit verify --file <path> [--pubkey <base64-key>] [--server-url <url>]
```

| Flag           | Description                                                                                          |
|----------------|------------------------------------------------------------------------------------------------------|
| `--file`       | Path to local JSONL audit log file (required)                                                        |
| `--pubkey`     | Base64-encoded Ed25519 public key for signature verification                                         |
| `--server-url` | `coordd` base URL — fetches the audit pubkey via `GET /api/v1/audit/pubkey` if `--pubkey` is omitted |

**What it checks:**

- Every line is valid JSON with required fields (`launch_id`, `event_name`, `occurred_at`, `payload`)
- Timestamps are monotonically non-decreasing
- Ed25519 signatures are valid (when a public key is available)
- `prev_hash` of each entry matches the SHA-256 of the previous line (where present)

**Examples:**

```bash
# Verify offline with an explicit pubkey
coordd audit verify --file audit.jsonl --pubkey <base64-pubkey>

# Fetch pubkey from a live server
coordd audit verify --file audit.jsonl --server-url http://coordd:8080

# Structural check only (no signature verification)
coordd audit verify --file audit.jsonl
```

Exit code is 0 if no anomalies are found, non-zero otherwise.

---

## smoke-signer

`smoke-signer` is a test utility for deterministic secp256k1 signing. It is **not** intended for production use — its
keys are derived from a fixed index, not from a real keychain.

```
smoke-signer <command>
```

### smoke-signer address

Print the bech32 operator address for a key index.

```
smoke-signer address --key-index <n>
```

### smoke-signer pubkey

Print the base64-encoded secp256k1 compressed public key for a key index.

```
smoke-signer pubkey --key-index <n>
```

### smoke-signer privkey

Print the hex-encoded private key for a key index (for importing into a chain keyring).

```
smoke-signer privkey --key-index <n>
```

### smoke-signer sign

Read a JSON payload from stdin, populate `nonce`, `timestamp`, `pubkey_b64`, and `signature`, then print the signed
payload to stdout.

```
echo '<json>' | smoke-signer sign --key-index <n>
```
