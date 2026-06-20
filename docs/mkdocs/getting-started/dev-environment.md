# Dev Environment

The dev environment starts `coordd` and the web frontend together with a single command. It is the fastest way to
explore the full system locally — no manual key generation or config files needed.

!!! warning "Web frontend is being extracted"
The `web` service in this environment (and the [Web App](web-app.md) page) is **deprecated** — the frontend is moving to
its own repository. The `coordd` backend setup described here remains current and supported, but the bundled `web`
container, the frontend-specific environment variables (`NEXT_PUBLIC_API_URL`, `COORD_BACKEND_URL`), and the "local
development without Docker" frontend steps may change or stop working in upcoming iterations.

---

## Prerequisites

- Docker with Compose v2 (`docker compose`)
- `make`
- Keplr or Leap wallet browser extension

---

## Start the stack

```bash
make dev-up
```

This builds both images (`Dockerfile` for the Go backend, `Dockerfile.web` for the Next.js frontend) and starts the
following services:

| Service  | URL                   | Role             |
|----------|-----------------------|------------------|
| `coordd` | http://localhost:8080 | Coordination API |
| `web`    | http://localhost:3000 | Web frontend     |

Open **http://localhost:3000** in a browser with your wallet extension active.

On first boot, `coordd` auto-generates its Ed25519 audit and JWT keys and runs database migrations. All data persists in
the `coordd-dev-data` Docker volume across restarts.

---

## Admin setup

Server admins can manage the coordinator allowlist and revoke sessions. You must set your address **before** starting
the stack — it is read at boot.

Create a `.env` file in the project root:

```bash
# .env
COORD_ADMIN_ADDRESSES=cosmos1youraddresshere
```

Copy `.env.example` as a starting point:

```bash
cp .env.example .env
# edit COORD_ADMIN_ADDRESSES
```

Multiple admins are supported as a comma-separated list:

```bash
COORD_ADMIN_ADDRESSES=cosmos1abc,cosmos1def
```

The address must match the one you sign in with in the browser (Cosmos Hub, Osmosis, or Juno — whichever chain you
select in the header sign-in dropdown).

!!! tip
If you forget to set this before `make dev-up`, stop the stack, update `.env`, and run `make dev-up` again. The
environment variable is read by `coordd` at startup.

---

## Stop and reset

```bash
make dev-down      # stop containers and delete the data volume
```

Removing the volume resets the database and keys — the next `make dev-up` starts fresh.

---

## Local development without Docker

For frontend hot-reload or backend debugging, run the two components separately:

```bash
# Terminal 1 — backend
bin/coordd migrate --config config.yaml
bin/coordd serve --config config.yaml

# Terminal 2 — frontend
cd web/app
yarn install
yarn dev
```

The Next.js dev server runs on `http://localhost:3000` and proxies API calls to `http://localhost:8080` by default. No
extra env vars needed.

See [Quickstart](quickstart.md) to build `bin/coordd` and create a minimal `config.yaml`.

---

## Environment variables

| Variable                | Default                                                                         | Description                                                                                                                                                                   |
|-------------------------|---------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `COORD_ADMIN_ADDRESSES` | *(empty)*                                                                       | Comma-separated operator addresses with admin access                                                                                                                          |
| `NEXT_PUBLIC_API_URL`   | `http://localhost:8080`                                                         | Backend URL used by the browser for SSE. Baked at build time.                                                                                                                 |
| `COORD_BACKEND_URL`     | `http://coordd:8080` (dev image); `http://localhost:8080` (non-Docker fallback) | Backend URL used by Next.js server-side rewrites. The dev image (`Dockerfile.web`) bakes the `coordd` compose service name; outside Docker it falls back to `localhost:8080`. |