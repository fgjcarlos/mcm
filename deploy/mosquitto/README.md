# Mosquitto deployment

This directory contains the local Eclipse Mosquitto configuration used by the MCM development Docker Compose stack, plus operator guidance for production TLS connectivity from MCM to Mosquitto.

## Files

- `config/mosquitto.conf`: dev-stack Mosquitto configuration with auth + ACL enabled. The dev stack shares this dir with MCM via a named volume; see [Integration with MCM](#integration-with-mcm) below for the deploy flow.
- `config/mosquitto.prod.conf`: production-hardened example. Disables anonymous access, adds a password file, binds plain MQTT to localhost, and configures a TLS listener on `8883`. Review the comments inside the file and this README's TLS checklist before use.
- `config/mosquitto-bootstrap.sh`: dev-only init script that seeds the shared config volume on first boot (see [Integration with MCM](#integration-with-mcm)).

## Exposed ports

- `1883`: MQTT TCP listener.
- `9001`: MQTT over WebSocket listener.

## Data and logs

The Compose stack stores broker data and logs in named Docker volumes:

- `mosquitto_data`
- `mosquitto_logs`
- `mosquitto_config` — the shared passwd/acl volume (see [Integration with MCM](#integration-with-mcm))

Inspect them with:

```bash
docker volume ls | grep mcm
```

## Integration with MCM

The dev Compose stack wires MCM's deploy service directly to the bundled broker:

```
┌─────────────────┐  HTTP API   ┌─────────────────┐  passwd/acl   ┌─────────────────┐
│  MCM container  │ ──────────▶ │  mcm DB (SQLite)│              │  shared volume  │
│  (Go process)   │             │                 │              │  mosquitto_config│
└────────┬────────┘             └─────────────────┘              └────────┬────────┘
         │                                                               │ rw
         │ docker exec                                                    │ rw
         │ kill -HUP 1                                                    │
         ▼                                                               ▼
┌─────────────────┐                                            ┌─────────────────┐
│  docker socket  │                                            │  mosquitto      │
│  (DEV ONLY)     │                                            │  container      │
└─────────────────┘                                            └─────────────────┘
```

The end-to-end flow on every successful deploy apply:

1. MCM renders the current ACL rules + MQTT users into two files (`mosquitto.RenderACLFile` / `mosquitto.RenderPasswdFile`).
2. The deploy service additionally re-hashes the `MCM_MOSQUITTO_PASSWORD` on every render so the service user MCM connects with never drops out of the rendered passwd even when an operator removes it from the MCM DB.
3. `internal/mosquitto.DockerApplier` writes both files atomically (temp + rename) into the named volume that Mosquitto mounts at `/mosquitto/config/`.
4. `docker exec mcm-mosquitto kill -HUP 1` signals the broker to reload the passwd/acl without a container restart.
5. MCM runs a healthcheck (`diagnostics.CheckMQTTConnectivity`) that does a real MQTT CONNECT/CONNACK exchange. If it fails, the deploy service reverts to the snapshot it took before applying and emits an audit event — see `internal/deploy/service.go:276` (rollback path) and the `TestApply_HealthcheckFailure_AuditAndFilesReverted` / `TestApply_RollbackFailure_AuditEmitted` tests.

### Dev-only knobs (don't carry to production)

| Knob | Dev value | Why it's dev-only |
|---|---|---|
| `MCM_MOSQUITTO_PASSWORD` | Hardcoded `mcm-dev-broker-password` in `docker-compose.yml` and `mosquitto-bootstrap.sh` | Production reads the broker password from a secret manager; never embed it in source. |
| `user root` in `mosquitto.conf` | Set | The cross-container shared volume is pre-populated by the image as the `mosquitto` UID; running as root is the simplest way for the broker to read files written by the `mcm` UID. Production should match broker/mcm UIDs (or run both as the same UID). |
| `chmod 777` on the shared config dir | Set by `mosquitto-bootstrap.sh` | Same UID-mismatch workaround. Production matches UIDs, no chmod needed. |
| Mount of `/var/run/docker.sock` into mcm | Set | Lets MCM run `docker exec … kill -HUP 1` against the host daemon. **Do NOT do this in production.** Production uses the `file` deploy mode with a shared filesystem and a broker-side reload trigger, or co-locates mcm + mosquitto on one host so the `pid_path` file mode works. |
| `docker-cli` installed in the mcm image | Set | Companion to the docker socket mount; the `DockerApplier` shells out to `docker exec`. Production does not need the docker CLI. |
| Bootstrap `admin` user seeded by the script | `admin` / `mcm-dev-broker-password` | Production creates the broker user with `mosquitto_passwd -c` on the host as the mosquitto user before the first broker boot. |

### Verifying the deploy flow locally

```bash
task build && task up
docker compose exec mosquitto mosquitto_pub -h 127.0.0.1 -p 1883 \
    -u admin -P mcm-dev-broker-password -t mcm/healthcheck -m "ping"
# Then exercise the API:
ADMIN_PW=$(docker compose logs --no-color mcm | sed -n 's/.*"password":"\([^"]*\)".*/\1/p' | head -1)
TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"admin\",\"password\":\"$ADMIN_PW\"}" | jq -r .token)
curl -s -X POST http://localhost:8080/api/v1/deployments/preview \
    -H "Authorization: Bearer $TOKEN" | jq .
curl -s -X POST http://localhost:8080/api/v1/deployments/apply \
    -H "Authorization: Bearer $TOKEN" | jq .
```

`scripts/e2e-deploy.sh` exercises this end-to-end and is invoked from CI (`.github/workflows/ci.yml::e2e-deploy` job).

## Production TLS for MCM to Mosquitto

Production deployments should connect MCM to a TLS-enabled Mosquitto listener and should authenticate both the broker and the MCM client where possible. The development listener in this repository is intentionally plain TCP and anonymous; do not expose it outside a trusted local development environment.

### MCM TLS settings

Configure these fields under `mosquitto.tls` in the MCM config file:

- `enabled`: Set to `true` to connect to Mosquitto with MQTT over TLS. Use the broker TLS port, commonly `8883`, in `mosquitto.port`.
- `ca_cert_file`: Path inside the MCM container/host to the PEM CA certificate used to verify the Mosquitto server certificate. Use your private CA bundle or the issuing public CA chain. Do not point this at a private key.
- `client_cert_file`: Path to the PEM client certificate presented by MCM when Mosquitto requires mutual TLS.
- `client_key_file`: Path to the PEM private key for `client_cert_file`. Treat this as a secret.
- `insecure_skip_verify`: Disables server certificate verification. Keep this `false` in production. Only set it temporarily for local development diagnostics with disposable, self-signed certificates.

MCM currently validates that `ca_cert_file`, `client_cert_file`, and `client_key_file` are set when `mosquitto.tls.enabled` is `true`, so mount all three files even if your Mosquitto policy is otherwise permissive.

### Production TLS checklist

- Create a dedicated Mosquitto TLS listener, for example on `8883`, with `cafile`, `certfile`, and `keyfile` configured on the broker.
- Mount a `/mosquitto/certs/` volume (e.g. `./deploy/mosquitto/certs:/mosquitto/certs:ro`) in the Mosquitto container when enabling the TLS listener — the paths referenced in `mosquitto.prod.conf` (`/mosquitto/certs/ca.crt`, `/mosquitto/certs/server.crt`, `/mosquitto/certs/server.key`) require this mount to be present.
- Issue a Mosquitto server certificate whose SAN includes the exact DNS name MCM uses in `mosquitto.host` (for example `mosquitto.example.internal` or the Docker service DNS name). Avoid relying on IP addresses unless the certificate contains the IP SAN.
- Issue a dedicated MCM client certificate and key if Mosquitto uses mutual TLS. Do not reuse broker certificates or human/operator certificates.
- Keep `mosquitto.tls.insecure_skip_verify: false` in production.
- Store certificate material outside the image. Mount it as read-only Docker secrets, Kubernetes Secrets, or an edge device secret store.
- Use restrictive file permissions:
  - CA bundle: readable by the MCM process, normally `0444` or `0644`.
  - Client certificate: readable by the MCM process, normally `0444` or `0644`.
  - Client private key: readable only by the MCM process user, normally `0400` or `0440`.
  - Parent directory: avoid world-writable permissions; use `0750` or stricter where possible.
- Run MCM as a non-root user and mount secrets with ownership or group permissions that allow that user to read only the required files.
- Rotate certificates before expiry and restart/reload MCM after updating mounted secret files if your deployment platform does not update mounts atomically.
- Verify connectivity with `mcm doctor --config /path/to/config.yaml` during deployment and after certificate rotation.

### Example production MCM config

```yaml
mosquitto:
  host: mosquitto.example.internal
  port: 8883
  username: ""
  password: ""
  tls:
    enabled: true
    ca_cert_file: /run/secrets/mosquitto-ca.crt
    client_cert_file: /run/secrets/mcm-client.crt
    client_key_file: /run/secrets/mcm-client.key
    insecure_skip_verify: false
```

For Docker or edge deployments, mount the secrets read-only instead of baking them into an image:

```yaml
services:
  mcm:
    image: mcm:latest
    volumes:
      - type: bind
        source: /etc/mcm/secrets
        target: /run/secrets
        read_only: true
```

### `mcm doctor` TLS diagnostics

`mcm doctor` performs a TCP dial, then a TLS handshake when `mosquitto.tls.enabled` is true, then an MQTT CONNECT/CONNACK exchange. Failure messages are categorized to show which phase failed:

- TCP failure: check host, port, listener binding, firewall rules, Docker/Kubernetes networking, and whether Mosquitto is running.
- TLS handshake failure: TCP reached the broker, but certificate validation or mutual TLS failed. Check the CA file, server certificate SANs, client certificate/key pair, Mosquitto TLS listener settings, and system time.
- MQTT CONNACK rejection: TCP and TLS succeeded, but Mosquitto rejected the MQTT connection. Check username/password, ACL/auth plugin configuration, client certificate identity mapping, and broker logs.

### Development-only self-signed example

The following pattern is acceptable only for local testing with disposable certificates. Do not use it in production because it disables server identity verification.

```yaml
mosquitto:
  host: 127.0.0.1
  port: 8883
  username: ""
  password: ""
  tls:
    enabled: true
    ca_cert_file: ./dev-certs/ca.crt
    client_cert_file: ./dev-certs/client.crt
    client_key_file: ./dev-certs/client.key
    insecure_skip_verify: true # development only; never production
```

## Security note

The development configuration enables authentication (no anonymous access) and uses a shared volume so MCM can manage users and ACLs through the deploy service. The dev-only shortcuts above (hardcoded password, broker running as root, docker socket mount, world-writable shared dir) exist purely so a clean clone of the repo works without operator input.

Do not use this configuration in production. Production deployments must use authentication, ACLs, TLS where appropriate, and explicit listener hardening — see the [Production TLS checklist](#production-tls-checklist) below.
