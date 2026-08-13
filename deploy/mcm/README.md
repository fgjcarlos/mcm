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

### Option A — use the prod override

The repo ships `docker-compose.prod.yml` at the root, which layers
`:?required` semantics for the JWT secret and the bootstrap admin
password back into the `mcm` service. Set the values in your shell or
in a gitignored `.env` file, then:

```bash
MCM_AUTH_JWT_SECRET="$(openssl rand -hex 32)" \
MCM_BOOTSTRAP_ADMIN_PASSWORD="$(openssl rand -base64 24)" \
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

If you forget to set either variable, Compose will refuse to start the
stack with the same `:?required` error the dev compose used to raise.

### Option B — mount `config.yaml` and set `MCM_*` overrides

If you want full control over every config field, mount this file and
override the secrets via env vars:

```bash
docker run --rm -p 8080:8080 \
  -v mcm_data:/var/lib/mcm \
  -v $(pwd)/deploy/mcm/config.yaml:/etc/mcm/config.yaml:ro \
  -e MCM_AUTH_JWT_SECRET="$(openssl rand -hex 32)" \
  -e MCM_BOOTSTRAP_ADMIN_PASSWORD="$(openssl rand -base64 24)" \
  mcm:dev
```

The mounted `config.yaml` is read first and the env vars replace
`auth.jwt_secret` and `auth.bootstrap_admin.password` before
validation runs. Without the env vars, the validator rejects the
shipped placeholder values.

The `mcm_data` Docker volume must be kept across restarts so the
generated `.bootstrap.json` survives. A fresh `docker volume rm`
followed by an up will generate a new JWT secret and invalidate
existing tokens.

### Switching Mosquitto to production auth and TLS

The development `mosquitto.conf` allows anonymous connections and plain TCP, which is intentional for local iteration.  For production:

1. Copy `deploy/mosquitto/config/mosquitto.prod.conf` to your deployment and follow the comments inside it to:
   - Create a password file with `mosquitto_passwd`.
   - Supply TLS certificates on port `8883`.
2. Mount `mosquitto.prod.conf` as `/mosquitto/config/mosquitto.conf` in your Mosquitto container (update the volume binding in `docker-compose.yml`).
3. Configure MCM's `mosquitto.tls` settings to connect on port `8883`.

See [deploy/mosquitto/README.md](../mosquitto/README.md) for the full TLS checklist and the MCM-side config fields.

### Image HEALTHCHECK

Both the development `Dockerfile` and the GoReleaser release image include a built-in `HEALTHCHECK` instruction that runs `mcm doctor` every 30 seconds.  Docker Compose augments this with compose-level healthcheck settings in `docker-compose.yml`.  You do not need to add a healthcheck yourself — it is already built into the image.

Note: `mcm doctor` dials Mosquitto as part of its check; a container started without a reachable Mosquitto (e.g. standalone `docker run` without the Compose stack) will report `unhealthy` — this is expected. Ensure Mosquitto is running and reachable before relying on the healthcheck result.

## Endpoints

- MCM API: `http://localhost:8080`
- Mosquitto MQTT: `localhost:1883`
- Mosquitto WebSocket: `localhost:9001`

## Container details

The MCM container:

- Runs as a non-root user (`mcm`).
- Stores the SQLite database at `/var/lib/mcm/mcm.db` (persisted via the `mcm_data` Docker volume).
- Persists the auto-generated JWT secret to `/var/lib/mcm/.bootstrap.json` (mode 0600) so it survives restarts.
- Honors `MCM_AUTH_JWT_SECRET`, `MCM_BOOTSTRAP_ADMIN_USERNAME`, and `MCM_BOOTSTRAP_ADMIN_PASSWORD` environment overrides at startup (other fields come from the mounted YAML or `internal/config.Default()`).
- Exposes port `8080` for the HTTP API.
- Includes a healthcheck via `mcm doctor`.

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

