# Run with Docker

The published image — `ghcr.io/ny4rl4th0t3p/seedward-chaincoord` — is a minimal Alpine image containing just
the `coordd` binary (built from `docker/Dockerfile` by `.github/workflows/publish-image.yml` on every `v*`
tag). Its **entrypoint is `coordd`** and its default command is **`serve`**, so `docker run <image>` starts
the server; pass a subcommand (`keygen`, `migrate`) to do anything else — **don't prefix it with `coordd`**.
The image runs as a **non-root user (uid 1000)** and does **no auto-setup**: you generate the signing keys
and apply migrations yourself.

The image publishes with `latest=auto`, so the **`:latest` tag only points at the newest _stable_ release**
— while the project is at release-candidate stage there is no `:latest`. Pin an explicit release tag.

!!! tip "Want the full stack — coordd + the web UI — in one command?"
That lives in **[seedward-suite](https://github.com/ny4rl4th0t3p/seedward-suite)** — its `make dev-up`
runs coordd and the web frontend together. This page runs the coordd **server** on its own.

## Run the server

```bash
# Pin an explicit release tag (`:latest` also tracks the newest stable release).
IMG=ghcr.io/ny4rl4th0t3p/seedward-chaincoord:v1.0.0
docker volume create coordd-data

# The image runs as a non-root user (uid 1000). Hand it ownership of the volume so coordd can write its
# DB, audit log, and files. `--entrypoint chown` overrides the default `coordd` entrypoint for this step.
docker run --rm -u 0 --entrypoint chown -v coordd-data:/data "$IMG" -R 1000:1000 /data

# 1. Generate the two Ed25519 signing keys (audit log + session JWTs) into the volume. `coordd keygen`
#    prints one base64 key to stdout; run it under a shell to redirect into files (owned by uid 1000).
docker run --rm --entrypoint sh -v coordd-data:/data "$IMG" -c \
  'coordd keygen > /data/audit_key && coordd keygen > /data/jwt_key'

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

# 3. Apply migrations, then serve. The entrypoint is `coordd`, so pass the subcommand only (no `coordd`
#    prefix); `serve` is the default command, so the last line needs no subcommand at all.
docker run --rm -v coordd-data:/data --env-file coordd.env "$IMG" migrate
docker run -d --name coordd -p 8080:8080 -v coordd-data:/data --env-file coordd.env "$IMG"
```

Check it: `curl http://localhost:8080/healthz` → `{"status":"ok"}`.

`COORD_ADMIN_ADDRESSES` is the wallet address that manages the `/api/v1/admin` endpoints (the coordinator
allowlist); set it to the address you'll sign in with. `COORD_INSECURE_NO_TLS=true` only suppresses the
plaintext-bind warning — use it when TLS is terminated upstream (reverse proxy / load balancer). See
[Setup & Configuration](../reference/setup.md) for TLS, CORS, the full env-var reference, and production
hardening.

## Build the image locally

`make docker-build` builds the same image from `docker/Dockerfile`:

```bash
make docker-build   # → local image tagged seedward-chaincoord
```
