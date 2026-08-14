# Mosquitto deployment

This directory contains the local Eclipse Mosquitto configuration used by the MCM development Docker Compose stack, plus operator guidance for production TLS connectivity from MCM to Mosquitto.

## Files

- `config/mosquitto.conf`: local-only Mosquitto configuration (anonymous, plain TCP — intentionally permissive for development).
- `config/mosquitto.prod.conf`: production-hardened example.  Disables anonymous access, adds a password file, binds plain MQTT to localhost, and configures a TLS listener on `8883`.  Review the comments inside the file and this README's TLS checklist before use.

## Exposed ports

- `1883`: MQTT TCP listener.
- `9001`: MQTT over WebSocket listener.

## Data and logs

The Compose stack stores broker data and logs in named Docker volumes:

- `mosquitto_data`
- `mosquitto_logs`

Inspect them with:

```bash
docker volume ls | grep mcm
```

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
- Verify connectivity with `curl -fsS http://localhost:8080/readyz` during deployment and after certificate rotation. `/readyz` runs an MQTT CONNECT/CONNACK probe and surfaces the failure phase (TCP / TLS / MQTT) in the JSON body. For external checks, `mosquitto_pub -h <host> -p <port> -t mcm/healthcheck -m ping` from any host that can reach the broker is a good end-to-end probe.

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

### MQTT readiness diagnostics

`GET /readyz` performs, internally, a TCP dial, then a TLS handshake when `mosquitto.tls.enabled` is true, then an MQTT CONNECT/CONNACK exchange (via `internal/diagnostics.CheckMQTTConnectivity`). The JSON body carries the failure phase so the operator can pivot:

- `error="broker unavailable"` (or `tcp: ...`): check host, port, listener binding, firewall rules, Docker/Kubernetes networking, and whether Mosquitto is running.
- TLS handshake failure: TCP reached the broker, but certificate validation or mutual TLS failed. Check the CA file, server certificate SANs, client certificate/key pair, Mosquitto TLS listener settings, and system time.
- MQTT CONNACK rejection: TCP and TLS succeeded, but Mosquitto rejected the MQTT connection. Check username/password, ACL/auth plugin configuration, client certificate identity mapping, and broker logs.

The HTTP status is `200` when the broker is reachable and `503` otherwise. The `/livez` endpoint is independent and only checks the HTTP server + SQLite, so it stays `200` even when the broker is down.

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

The development configuration enables anonymous access so the stack works without setup friction.

Do not use this configuration in production. Production deployments must use authentication, ACLs, TLS where appropriate, and explicit listener hardening.
