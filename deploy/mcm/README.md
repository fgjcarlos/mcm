# MCM Docker deployment

This directory contains the MCM configuration used by the Docker Compose stack.

## Files

- `config.yaml`: PRODUCTION template for the MCM container. The dev
  Compose stack does NOT mount this file — it relies on the Go
  defaults plus the `MCM_*` variables in `docker-compose.yml`. This template is shipped for production deployments that
  want to pin every field explicitly.

## Quick start (dev)

Follow the [Quickstart in the top-level README](../../README.md#quickstart-docker):
`task build`, export `MCM_SETUP_TOKEN`, `task up`, then create the first
administrator at `http://localhost:8080`. The token only authorizes that
setup; it is not the administrator password or `MCM_MOSQUITTO_PASSWORD`,
and MCM logs neither. Once the first administrator exists, setup stays
closed across restarts and changing the token does not reopen it. For
automated provisioning, set both `MCM_BOOTSTRAP_ADMIN_USERNAME` and
`MCM_BOOTSTRAP_ADMIN_PASSWORD` instead; the secret-handling rules for
production are in [production section 3](../../docs/production.md#3-secrets-management).

## Production deployment

Production deployments must NOT rely on auto-generated credentials. Two
supported options:

### Option A — use the secrets override

The repo ships `docker-compose.secrets.yml` at the root. It requires the
JWT secret and passes through either `MCM_SETUP_TOKEN` for interactive
first-admin setup or both explicit bootstrap admin variables for
automated provisioning. Set the values in your shell or a gitignored
`.env` file. The setup token is separate from the MQTT broker password.
For interactive setup:

```bash
export MCM_AUTH_JWT_SECRET="$(openssl rand -hex 32)"
export MCM_SETUP_TOKEN="$(openssl rand -hex 32)"
docker compose -f docker-compose.yml -f docker-compose.secrets.yml up -d
```

Use the configured setup token in the browser; keep it private. Compose
refuses to start without the JWT secret. For automated
provisioning instead, set both bootstrap admin variables and leave the
setup token unset; the interactive setup token is not required in that
case.

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
(see `Load` in `internal/config/config.go`):

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
values. To use the interactive path with this template instead, clear both
`auth.bootstrap_admin` fields and set `MCM_SETUP_TOKEN` in place of
`MCM_BOOTSTRAP_ADMIN_PASSWORD`.

The `mcm_data` Docker volume must be kept across restarts so the
generated `.bootstrap.json` survives. A fresh `docker volume rm`
followed by an up will generate a new JWT secret and invalidate
existing tokens.

### Switching Mosquitto to production auth and TLS

The development `mosquitto.conf` ships with `allow_anonymous false` plus a shared passwd/acl file pattern (see [Integration with MCM](../mosquitto/README.md#integration-with-mcm) in the Mosquitto README), but the production template still requires file, ACL and activation alignment. See [known limitations](../../docs/production.md#current-limitations). The development shortcuts are:

- **Dev**: the bootstrap runs the broker as the mcm UID/GID (`MCM_SERVICE_UID`/`MCM_SERVICE_GID`), so the broker owns the passwd/acl files MCM writes into the shared volume, as Mosquitto requires. The bootstrap admin password is hardcoded (`mcm-dev-broker-password`) in both `mosquitto-bootstrap.sh` and `docker-compose.yml` so a clean clone works without operator input. The mcm service mounts `/var/run/docker.sock` to run `docker exec … kill -HUP 1` for reloads.
- **Production**: drop the `user root` directive, source the broker password from a secret manager (do NOT hardcode), do NOT mount the Docker socket into mcm, and use [`deploy/mosquitto/config/mosquitto.prod.conf`](../mosquitto/config/mosquitto.prod.conf) as the broker config (TLS on port `8883`, uncomment `require_certificate true` if mutual TLS is required). Configure MCM's `mosquitto.tls` settings to connect on `8883`.

For production:

1. Copy `deploy/mosquitto/config/mosquitto.prod.conf` to your deployment and follow the comments inside it to:
   - Create a password file with `mosquitto_passwd` as the broker user (UID 1883).
   - Supply TLS certificates on port `8883`.
2. Mount `mosquitto.prod.conf` as `/mosquitto/config/mosquitto.conf` in your Mosquitto container (update the volume binding in `docker-compose.yml`).
3. Align the broker password and ACL references with MCM deploy paths, then verify filesystem ownership and signaling. File mode without a PID path does not signal the broker; configure the reload and restart commands described in [production section 3](../../docs/production.md#3-secrets-management).
4. Configure MCM's `mosquitto.tls` settings to connect on port `8883` and verify authentication plus allowed/denied operations.

See [deploy/mosquitto/README.md](../mosquitto/README.md) for the full TLS checklist, the MCM-side config fields, and the integration model.

### Image HEALTHCHECK

The development `Dockerfile` and the release image built by `.github/workflows/release.yml` (multi-arch, via `docker buildx`) both include a built-in `HEALTHCHECK` instruction that hits `GET /livez` every 30 seconds. Docker Compose augments this with compose-level healthcheck settings in `docker-compose.yml`. You do not need to add a healthcheck yourself — it is already built into the image.

Note: `/livez` is a process check; it does not access SQLite or the broker. The Mosquitto reachability is exposed via `/readyz`, which returns 503 with `error="broker unavailable"` when the broker cannot be reached. A container started without a reachable Mosquitto (e.g. standalone `docker run` without the Compose stack) will report `healthy` for `/livez` and `503` for `/readyz` — both are expected. To verify the broker side, use `mosquitto_pub` with the configured credentials and TLS options from a host that can reach the broker.

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
  - `MCM_SETUP_TOKEN` — required for interactive first-admin setup through the UI. Supply it through a secret store when deploying behind a proxy or remotely; do not put it in a URL or logs.
- `MCM_BOOTSTRAP_ADMIN_USERNAME` and `MCM_BOOTSTRAP_ADMIN_PASSWORD` — explicit first-boot admin provisioning; set both to retain automated provisioning.
- Other fields come from the mounted YAML or `internal/config.Default()`. A typo in a `MCM_*` name or a malformed value aborts startup with an actionable error (issue #279 contract).
- Exposes port `8080` for the HTTP API.
- Includes a healthcheck via `/livez`.

## Building the image standalone

```bash
docker build --target prod -t mcm:latest .   # production image
docker build -t mcm:dev .                    # dev image (last stage; adds docker-cli)
```

The Dockerfile's last stage is `dev`, so a build without `--target` produces the dev image.

## Running standalone

```bash
docker run --rm -p 8080:8080 mcm:dev
```

For interactive first-admin setup, configure `MCM_SETUP_TOKEN` through
a secret store and enter it in the setup form; without it, account
creation stays locked. To provision non-interactively, explicitly set
both `MCM_BOOTSTRAP_ADMIN_USERNAME` and
`MCM_BOOTSTRAP_ADMIN_PASSWORD`. Neither path retrieves an admin password
from logs.

## Smoke test

After starting the stack:

```bash
# Check MCM liveness and readiness
curl -s http://localhost:8080/healthz | jq .
curl -s http://localhost:8080/readyz | jq .

# Create the first administrator in the web UI using the configured setup token,
# then sign in with the username and password you chose.

# Check broker/MCM operational status
curl -s http://localhost:8080/api/v1/status | jq .
```

## Security note

The dev Compose stack stores administrator state inside the `mcm_data`
Docker volume. Treat that volume as sensitive — anyone who can read it
can mint JWTs. Production
deployments must use the prod override (or mount `config.yaml` with
explicit env vars), use strong secrets, terminate HTTPS in front of
MCM, and follow the [production TLS checklist](../mosquitto/README.md#production-tls-checklist).
