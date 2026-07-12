# Run with Docker

The published image — `ghcr.io/ny4rl4th0t3p/seedward-chaincoord` — is a minimal Alpine image containing just
the `coordd` binary (built from `docker/Dockerfile` by `.github/workflows/publish-image.yml` on every `v*`
tag). It has **no default command and no auto-setup**, so you invoke `coordd`'s subcommands directly:
`keygen`, `migrate`, `serve`.

!!! tip "Want the full stack — coordd + the web UI — in one command?"
    That lives in **[seedward-suite](https://github.com/ny4rl4th0t3p/seedward-suite)** — its `make dev-up`
    runs coordd and the web frontend together. This page runs the coordd **server** on its own.

## Run the server

```bash
IMG=ghcr.io/ny4rl4th0t3p/seedward-chaincoord:latest   # or pin a release, e.g. :v1.0.0
docker volume create coordd-data

# 1. Generate the two Ed25519 signing keys (audit log + session JWTs) into the volume.
docker run --rm -v coordd-data:/data "$IMG" \
  sh -c 'coordd keygen > /data/audit_key && coordd keygen > /data/jwt_key'

# 2. Write the runtime config once (reused by migrate + serve).
cat > coordd.env <<'EOF'
COORD_DB_PATH=/data/coord.db
COORD_AUDIT_LOG_PATH=/data/audit.jsonl
COORD_FILES_PATH=/data/genesis
COORD_AUDIT_PRIVATE_KEY_FILE=/data/audit_key
COORD_JWT_PRIVATE_KEY_FILE=/data/jwt_key
COORD_INSECURE_NO_TLS=true
COORD_ADMIN_ADDRESSES=cosmos1youradminaddr
EOF

# 3. Apply migrations, then serve.
docker run --rm  -v coordd-data:/data --env-file coordd.env "$IMG" coordd migrate
docker run -d --name coordd -p 8080:8080 -v coordd-data:/data --env-file coordd.env "$IMG" coordd serve
```

Check it: `curl http://localhost:8080/healthz` → `{"status":"ok"}`.

`COORD_ADMIN_ADDRESSES` is the wallet address that manages the `/admin` endpoints (the coordinator
allowlist); set it to the address you'll sign in with. `COORD_INSECURE_NO_TLS=true` only suppresses the
plaintext-bind warning — use it when TLS is terminated upstream (reverse proxy / load balancer). See
[Setup & Configuration](../reference/setup.md) for TLS, CORS, the full env-var reference, and production
hardening.

## Build the image locally

`make docker-build` builds the same image from `docker/Dockerfile`:

```bash
make docker-build   # → local image tagged seedward-chaincoord
```
