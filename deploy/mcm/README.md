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

## Endpoints

- MCM API: `http://localhost:8080`
- Mosquitto MQTT: `localhost:1883`
- Mosquitto WebSocket: `localhost:9001`

## Container details

The MCM container:

- Runs as a non-root user (`mcm`).
- Stores the SQLite database at `/var/lib/mcm/mcm.db` (persisted via the `mcm_data` Docker volume).
- Reads config from `/etc/mcm/config.yaml` (bind-mounted read-only from `deploy/mcm/config.yaml`).
- Does not apply `MCM_*` environment overrides at runtime; update the mounted YAML file instead.
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
