# MCM Docker deployment

This directory contains the local MCM configuration used by the Docker Compose stack.

## Files

- `config.yaml`: local development configuration for the MCM container.

## Quick start

From the repository root:

```bash
docker compose up --build
```

This starts both Mosquitto and MCM. The MCM service waits for Mosquitto to be healthy before starting.

## Endpoints

- MCM API: `http://localhost:8080`
- Mosquitto MQTT: `localhost:1883`
- Mosquitto WebSocket: `localhost:9001`

## Default credentials

The development config bootstraps an admin user:

- Username: `admin`
- Password: `admin`

Do not use these credentials in production.

## Container details

The MCM container:

- Runs as a non-root user (`mcm`).
- Stores the SQLite database at `/var/lib/mcm/mcm.db` (persisted via the `mcm_data` Docker volume).
- Reads config from `/etc/mcm/config.yaml` (bind-mounted read-only from `deploy/mcm/config.yaml`).
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
# Check MCM health
curl -s http://localhost:8080/api/v1/health | jq .

# Login
curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin"}' | jq .

# Check broker status (use the token from login)
curl -s http://localhost:8080/api/v1/broker/status \
  -H 'Authorization: Bearer <token>' | jq .
```

## Security note

The development configuration uses weak credentials and no TLS. Production deployments must use strong secrets, HTTPS, and follow the [production TLS checklist](../mosquitto/README.md#production-tls-checklist).
