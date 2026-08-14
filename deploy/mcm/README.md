# MCM Docker deployment

This directory contains the MCM configuration used by the Docker Compose stack.

## Files

- `config.yaml`: PRODUCTION template for the MCM container. The dev
  Compose stack does NOT mount this file — it relies on the Go
  runtime's auto-generation/persistence contract (see "Quick start"
  below). This template is shipped for production deployments that
  want to pin every field explicitly.

## Quick start (dev)

From the repository root:

```bash
task build               # build the mcm:dev image
task up                  # start the mcm + mosquitto stack
task logs                # capture the auto-generated bootstrap admin password
curl http://localhost:8080/healthz
```

On first boot, the MCM container:

- Generates a 32-byte JWT secret and writes it to
  `/var/lib/mcm/.bootstrap.json` (mode 0600). It is reused on every
  subsequent restart, so existing tokens survive.
- Generates a 24-char bootstrap admin password and logs it once on
  first boot. Grab it from `task logs` or `docker compose logs mcm` —
  the log line is:

  ```
  level=WARN msg="bootstrap admin created; capture these credentials now — they will not be logged again" username=admin password=<...>
  ```

- Open `http://localhost:8080` and sign in with `admin` + the
  password from the logs. The admin is then persisted in SQLite, so
  later restarts do not regenerate it.

You do not need to edit `config.yaml` or set any environment variable
for the dev flow. Compose will not block first boot.

## Production deployment

Production deployments must NOT rely on auto-generated credentials. Two
supported options:

### Option A — use the secrets override

The repo ships `docker-compose.secrets.yml` at the root, which layers
`:?required` semantics for the JWT secret and the bootstrap admin
password back into the `mcm` service. Set the values in your shell or
in a gitignored `.env` file, then:

```bash
MCM_AUTH_JWT_SECRET="$(openssl rand -hex 32)" \
MCM_BOOTSTRAP_ADMIN_PASSWORD="$(openssl rand -base64 24)" \
docker compose -f docker-compose.yml -f docker-compose.secrets.yml up -d
```

If you forget to set either variable, Compose will refuse to start the
stack with the same `:?required` error the dev compose used to raise.

This is a **secrets-only override**. It does not by itself harden the
runtime: the dev compose it layers on top of still exposes
localhost-bound plaintext MQTT/WebSocket, publishes MCM's HTTP API
without TLS, and uses the dev Mosquitto conf (anonymous + plain TCP).
For production, also apply the network/auth/TLS hardening from
[deploy/mosquitto/README.md](../mosquitto/README.md) and the rest of
this file's production checklist. The override is meant to be the
*first* layer, not the whole one.

### Option B — mount `config.yaml` and set `MCM_*` overrides

If you want full control over every config field, mount this file and
override the secrets via env vars. The image's `ENTRYPOINT` is a bare
`mcm`, so the YAML is read **only** when `MCM_CONFIG_FILE` is set
(this is the contract documented in `cmd/mcm/main.go`):

```bash
docker run --rm -p 8080:8080 \
  -v mcm_data:/var/lib/mcm \
  -v $(pwd)/deploy/mcm/config.yaml:/etc/mcm/config.yaml:ro \
  -e MCM_CONFIG_FILE=/etc/mcm/config.yaml \
  -e MCM_AUTH_JWT_SECRET="$(openssl rand -hex 32)" \
  -e MCM_BOOTSTRAP_ADMIN_PASSWORD="$(openssl rand -base64 24)" \
  mcm:dev
```

Without `MCM_CONFIG_FILE`, the YAML is silently ignored — the container
runs with `Default()` plus the env vars. The env vars replace
`auth.jwt_secret` and `auth.bootstrap_admin.password` before validation
runs; without them, the validator rejects the shipped placeholder
values.

The `mcm_data` Docker volume must be kept across restarts so the
generated `.bootstrap.json` survives. A fresh `docker volume rm`
followed by an up will generate a new JWT secret and invalidate
existing tokens.

### Switching Mosquitto to production auth and TLS

The development `mosquitto.conf` ships with `allow_anonymous false` plus a shared passwd/acl file pattern (see [Integration with MCM](../mosquitto/README.md#integration-with-mcm) in the Mosquitto README), which mirrors the production contract — there is no "permissive dev mode" you have to undo. The differences vs. production are:

- **Dev**: `user root` in the broker config so the broker can read passwd/acl files written by the mcm UID across the shared volume. The bootstrap admin password is hardcoded (`mcm-dev-broker-password`) in both `mosquitto-bootstrap.sh` and `docker-compose.yml` so a clean clone works without operator input. The mcm service mounts `/var/run/docker.sock` to run `docker exec … kill -HUP 1` for reloads.
- **Production**: drop the `user root` directive, source the broker password from a secret manager (do NOT hardcode), do NOT mount the Docker socket into mcm, and use [`deploy/mosquitto/config/mosquitto.prod.conf`](../mosquitto/config/mosquitto.prod.conf) as the broker config (TLS on port `8883`, `require_certificate true` for mutual TLS). Configure MCM's `mosquitto.tls` settings to connect on `8883`.

For production:

1. Copy `deploy/mosquitto/config/mosquitto.prod.conf` to your deployment and follow the comments inside it to:
   - Create a password file with `mosquitto_passwd` as the broker user (UID 1883).
   - Supply TLS certificates on port `8883`.
2. Mount `mosquitto.prod.conf` as `/mosquitto/config/mosquitto.conf` in your Mosquitto container (update the volume binding in `docker-compose.yml`).
3. Configure MCM's `mosquitto.tls` settings to connect on port `8883`.

See [deploy/mosquitto/README.md](../mosquitto/README.md) for the full TLS checklist, the MCM-side config fields, and the integration model.

### Image HEALTHCHECK

Both the development `Dockerfile` and the GoReleaser release image include a built-in `HEALTHCHECK` instruction that hits `GET /livez` every 30 seconds. Docker Compose augments this with compose-level healthcheck settings in `docker-compose.yml`. You do not need to add a healthcheck yourself — it is already built into the image.

Note: `/livez` only verifies the HTTP server is up and the SQLite DB is reachable. The Mosquitto reachability is exposed via `/readyz`, which returns 503 with `error="broker unavailable"` when the broker cannot be reached. A container started without a reachable Mosquitto (e.g. standalone `docker run` without the Compose stack) will report `healthy` for `/livez` and `503` for `/readyz` — both are expected. To verify the broker side, run `docker compose exec mosquitto mosquitto_pub -h 127.0.0.1 -p 1883 -t mcm/healthcheck -m ping` or use `mosquitto_pub` from the host.

## Endpoints

- MCM API: `http://localhost:8080`
- Mosquitto MQTT: `localhost:1883`
- Mosquitto WebSocket: `localhost:9001`

## Container details

The MCM container:

- Runs as a non-root user (`mcm`).
- Stores the SQLite database at `/var/lib/mcm/mcm.db` (persisted via the `mcm_data` Docker volume).
- Persists the auto-generated JWT secret to `/var/lib/mcm/.bootstrap.json` (mode 0600) so it survives restarts.
- Honors every `MCM_*` environment variable in the strict-parser table at startup (see [`../../internal/config/env_bindings.go`](../../internal/config/env_bindings.go)). The required secrets are:
  - `MCM_AUTH_JWT_SECRET` — at least 32 random characters; persisted to `.bootstrap.json` only when unset.
  - `MCM_BOOTSTRAP_ADMIN_USERNAME` and `MCM_BOOTSTRAP_ADMIN_PASSWORD` — first-boot admin; both empty triggers auto-generation.
- Other fields come from the mounted YAML or `internal/config.Default()`. A typo in a `MCM_*` name or a malformed value aborts startup with an actionable error (issue #279 contract).
- Exposes port `8080` for the HTTP API.
- Includes a healthcheck via `/livez`.

## Building the image standalone

```bash
docker build -t mcm:latest .
```

## Running standalone

```bash
docker run --rm -p 8080:8080 mcm:dev
```

The standalone container will auto-generate credentials on first boot
and log the bootstrap admin password to stdout. Capture it before
discarding the container logs.

## Smoke test

After starting the stack:

```bash
# Check MCM liveness and readiness
curl -s http://localhost:8080/healthz | jq .
curl -s http://localhost:8080/readyz | jq .

# Login — use the password from `task logs` / `docker compose logs mcm`
curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<password from logs>"}' | jq .

# Check broker/MCM operational status
curl -s http://localhost:8080/api/v1/status | jq .
```

## Security note

The dev Compose stack relies on auto-generated credentials stored
inside the `mcm_data` Docker volume. Treat that volume as
sensitive — anyone who can read it can mint JWTs. Production
deployments must use the prod override (or mount `config.yaml` with
explicit env vars), use strong secrets, terminate HTTPS in front of
MCM, and follow the [production TLS checklist](../mosquitto/README.md#production-tls-checklist).
