# Quickstart

Run `coordd` locally in a few minutes. This guide assumes you have Go 1.25+ installed.

!!! danger "Not production-ready yet"
seedward-chaincoord is a **feature-complete v1 release candidate** that is **not production-ready yet** — APIs, data
formats, and
behaviours may still change. **Do not use it for mainnet launches or any environment where correctness and availability
are required.**

---

## 1. Build

```bash
git clone https://github.com/ny4rl4th0t3p/seedward-chaincoord.git
cd seedward-chaincoord
make build
```

This produces two binaries:

- `bin/coordd` — the coordination server
- `bin/smoke-signer` — test signing utility (only needed for E2E tests)

---

## 2. Generate keys

`coordd` requires two Ed25519 keys at startup: one for signing audit log entries, one for JWT session tokens. Generate
them with the built-in `keygen` command:

```bash
mkdir -p data
bin/coordd keygen > data/audit_key
bin/coordd keygen > data/jwt_key
chmod 600 data/audit_key data/jwt_key
```

Each key is a base64-encoded 32-byte seed, stored as a single line of text.

---

## 3. Write a config file

Create `config.yaml` in the project root:

```yaml
listen_addr: ":8080"
db_path: "./data/coord.db"
audit_log_path: "./data/audit.jsonl"
files_path: "./data/genesis"
log_level: "debug"
audit_private_key_file: "./data/audit_key"
jwt_private_key_file: "./data/jwt_key"
```

See [Setup & Configuration](../reference/setup.md) for all available options.

---

## 4. Run migrations

```bash
bin/coordd migrate --config config.yaml
```

This creates the SQLite database and applies all schema migrations.

---

## 5. Start the server

```bash
bin/coordd serve --config config.yaml
```

With `log_level: debug` the server emits human-readable output to stderr. You should see:

```
INF coordd listening addr=:8080
```

---

## 6. Verify it is up

```bash
curl http://localhost:8080/healthz
# → {"status":"ok"}
```

---

## Next steps

- [Run with Docker](docker.md) — run `coordd` from the published image. (The full stack — coordd + web — lives in
  seedward-suite.)
- [Smoke Test](smoke-test.md) — run the full end-to-end protocol against a live Cosmos SDK chain
- [Setup & Configuration](../reference/setup.md) — TLS, CORS, production options