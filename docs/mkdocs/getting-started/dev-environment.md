# Dev Environment

The dev environment starts `coordd` with a single command. It is the fastest way to run the backend locally — no
manual key generation or config files needed.

---

## Prerequisites

- Docker with Compose v2 (`docker compose`)
- `make`

---

## Start the stack

```bash
make dev-up
```

This builds the `coordd` image (`Dockerfile`) and starts it:

| Service  | URL                   | Role             |
|----------|-----------------------|------------------|
| `coordd` | http://localhost:8080 | Coordination API |

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

The address must match the operator address you authenticate with.

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

For backend debugging, run `coordd` directly:

```bash
bin/coordd migrate --config config.yaml
bin/coordd serve --config config.yaml
```

See [Quickstart](quickstart.md) to build `bin/coordd` and create a minimal `config.yaml`.

---

## Environment variables

| Variable                | Default                                                                         | Description                                                                                                                                                                   |
|-------------------------|---------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `COORD_ADMIN_ADDRESSES` | *(empty)*                                                                       | Comma-separated operator addresses with admin access                                                                                                                          |