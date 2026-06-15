# MCM Docker deployment

This directory contains the MCM configuration used by the Docker Compose stack.

## Files

- `config.yaml`: configuration template for the MCM container.

## Quick start

From the repository root:

1. Edit `deploy/mcm/config.yaml` and replace the placeholder values:
   - `auth.jwt_secret` — set a random string of at least 32 characters.
   - `auth.bootstrap_admin.password` — set a strong password (at least 8 characters, not `admin` or other common values).

2. Start the stack:

```bash
docker compose up --build
```

MCM will not start if either placeholder is left unchanged — the config validator rejects known template secrets.

This starts both Mosquitto and MCM. The MCM service waits for Mosquitto to be healthy before starting.

## From dev compose to production

### Injecting secrets at runtime

Instead of editing `config.yaml` directly, you can supply secrets via environment variables.  MCM honors the following overrides at startup — they replace the corresponding fields in the mounted YAML before validation runs:

| Variable | Config field |
|---|---|
| `MCM_AUTH_JWT_SECRET` | `auth.jwt_secret` |
| `MCM_BOOTSTRAP_ADMIN_USERNAME` | `auth.bootstrap_admin.username` |
| `MCM_BOOTSTRAP_ADMIN_PASSWORD` | `auth.bootstrap_admin.password` |

This lets you ship `config.yaml` with placeholders and inject real secrets from a `.env` file, Docker secrets, or your orchestrator's secret store.  `mcm config validate` and the server both apply these `MCM_*` overrides before validating, so a placeholder-carrying YAML will validate when the env vars are set.  Uncomment the `environment:` block in `docker-compose.yml` under the `mcm` service to enable it:

```bash
MCM_AUTH_JWT_SECRET=<32+ char random string> \
MCM_BOOTSTRAP_ADMIN_PASSWORD=<strong password> \
docker compose up --build
```

Or use a `.env` file in the repo root (keep it out of version control):

```
MCM_AUTH_JWT_SECRET=<32+ char random string>
MCM_BOOTSTRAP_ADMIN_PASSWORD=<strong password>
```

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
- Reads config from `/etc/mcm/config.yaml` (bind-mounted read-only from `deploy/mcm/config.yaml`).
- Honors `MCM_AUTH_JWT_SECRET`, `MCM_BOOTSTRAP_ADMIN_USERNAME`, and `MCM_BOOTSTRAP_ADMIN_PASSWORD` environment overrides at startup (other fields come from the mounted YAML).
- Exposes port `8080` for the HTTP API.
- Includes a healthcheck via `mcm doctor`.

## Building the image standalone

```bash
docker build -t mcm:latest .
```

## Running standalone

```bash
docker run --rm -p 8080:8080 \
  -v $(pwd)/deploy/mcm/config.yaml:/etc/mcm/config.yaml:ro \
  mcm:latest
```

## Smoke test

After starting the stack:

```bash
# Check MCM liveness and readiness
curl -s http://localhost:8080/healthz | jq .
curl -s http://localhost:8080/readyz | jq .

# Login — use the password you set in config.yaml
curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<your-password>"}' | jq .

# Check broker/MCM operational status
curl -s http://localhost:8080/api/v1/status | jq .
```

## Security note

This template ships with placeholder credentials that the config validator rejects — MCM will not start until you replace them. Production deployments must also use strong secrets, HTTPS, and follow the [production TLS checklist](../mosquitto/README.md#production-tls-checklist).
