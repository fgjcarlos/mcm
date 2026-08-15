# Mosquitto Control Manager (MCM)

[![CI](https://github.com/fgjcarlos/mcm/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/fgjcarlos/mcm/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](./LICENSE)
[![Go](https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white)](./go.mod)
[![Node](https://img.shields.io/badge/node-22.13%20%7C%2024-339933?logo=node.js&logoColor=white)](./frontend/.nvmrc)
[![OpenAPI](https://img.shields.io/badge/OpenAPI-3.1-6BA539?logo=openapiinitiative&logoColor=white)](./docs/openapi.yaml)

**Website**: [fgjcarlos.github.io/mcm](https://fgjcarlos.github.io/mcm) · MCM is an open source control plane for Eclipse Mosquitto.

It aims to make Mosquitto easier to operate by adding a modern web UI, REST API, realtime observability, user management, ACL management, and deployment tooling — without replacing Mosquitto as the MQTT broker.

> The goal is simple: **the stability and small footprint of Mosquitto, with a modern administration and observability experience.**

---

## Quickstart (Docker)

MCM ships as a single Docker image. No CLI, no host binaries — `task` is the only tool you need.

```bash
git clone https://github.com/fgjcarlos/mcm.git
cd mcm
task build               # build the mcm:dev image
task up                  # start the mcm + mosquitto stack
task logs                # tail mcm logs; the bootstrap admin password prints here
```

Open <http://localhost:8080> and sign in with `admin` + the password from `task logs`. Stop the stack with `task down`.

> The dev stack now ships with Mosquitto authentication **enabled** (no anonymous clients). The bundled broker is seeded with a single dev admin user by `deploy/mosquitto/config/mosquitto-bootstrap.sh`; the matching credentials are hardcoded in `docker-compose.yml` so MCM can connect at startup. MCM's deploy service then writes the canonical passwd/acl files into a shared named volume (`mosquitto_config`) and SIGHUPs the broker on every successful apply, so user/ACL changes created via the UI/API reach the live broker without an operator running `mosquitto_passwd` by hand. This is a **dev-only** convenience — production uses [`deploy/mosquitto/config/mosquitto.prod.conf`](./deploy/mosquitto/config/mosquitto.prod.conf) and the production checklist in [`docs/production.md`](./docs/production.md).

---

## Configuration

MCM is configured through `MCM_*` environment variables. A YAML file can be mounted for non-secret defaults via `MCM_CONFIG_FILE`; env vars override every field the YAML sets. Every variable below is enforced by `internal/config/env_bindings.go` — a typo in a name or a malformed value aborts startup with an actionable error (see [issue #279](https://github.com/fgjcarlos/mcm/issues/279) for the strict-parsing contract).

### HTTP listener

| Variable | Default | Notes |
| --- | --- | --- |
| `MCM_HTTP_BIND_ADDRESS` | `0.0.0.0` | Interface the HTTP API binds to. Loopback/private in production (proxy owns the public address). |
| `MCM_HTTP_PORT` | `8080` | TCP port for the HTTP API. 1..65535. |
| `MCM_HTTP_TRUSTED_PROXIES` | *(unset)* | Comma-separated IP/CIDR list. MCM honors `X-Forwarded-For` / `X-Real-IP` from peers in this list. Empty (default) trusts no proxy. |
| `MCM_HTTP_CORS_ALLOWED_ORIGINS` | *(unset)* | Comma-separated exact origins (scheme://host[:port]) permitted to make cross-origin requests. Empty = same-origin only. |

### HTTP TLS / mTLS

| Variable | Default | Notes |
| --- | --- | --- |
| `MCM_HTTP_TLS_ENABLED` | `false` | Serve HTTPS directly from MCM. Off (default) means terminate TLS at the proxy. |
| `MCM_HTTP_TLS_CERT_FILE` | *(unset)* | Path to PEM-encoded server certificate. Required when `MCM_HTTP_TLS_ENABLED=true`. |
| `MCM_HTTP_TLS_KEY_FILE` | *(unset)* | Path to PEM-encoded server private key. Required when `MCM_HTTP_TLS_ENABLED=true`. |
| `MCM_HTTP_TLS_MIN_VERSION` | `1.2` | `"1.2"` or `"1.3"`. |
| `MCM_HTTP_TLS_CLIENT_CA_FILE` | *(unset)* | Path to PEM-encoded CA bundle for mTLS client cert verification. Required when `MCM_HTTP_TLS_REQUIRE_CLIENT_CERT=true`. |
| `MCM_HTTP_TLS_REQUIRE_CLIENT_CERT` | `false` | Enforce mTLS: every request must present a client certificate signed by `MCM_HTTP_TLS_CLIENT_CA_FILE`. |

### Database

| Variable | Default | Notes |
| --- | --- | --- |
| `MCM_DATABASE_BACKEND` | `sqlite` | `"sqlite"` (uses `MCM_DATABASE_PATH`) or `"postgres"` (uses `MCM_DATABASE_DSN`). |
| `MCM_DATABASE_PATH` | `/var/lib/mcm/mcm.db` | SQLite file path. Parent dir must be writable so the JWT-secret bootstrap can persist. |
| `MCM_DATABASE_DSN` | *(unset)* | Postgres connection string. Required when `MCM_DATABASE_BACKEND=postgres`. |

### Auth

| Variable | Default | Notes |
| --- | --- | --- |
| `MCM_AUTH_JWT_SECRET` | *(auto-generated)* | 64-hex-char random secret persisted to `<db dir>/.bootstrap.json` (mode 0600). Set explicitly to control across restarts. |
| `MCM_AUTH_TOKEN_TTL` | `24h` | Go duration (e.g. `"24h"`, `"30m"`). |
| `MCM_BOOTSTRAP_ADMIN_USERNAME` | `admin` | First-boot only. Leave both empty to auto-generate. |
| `MCM_BOOTSTRAP_ADMIN_PASSWORD` | *(auto-generated)* | 24-char random password logged once on first boot. |
| `MCM_AUTH_LOGIN_LOCKOUT_WINDOW` | `15m` | Sliding window for failed-login counting. Go duration. |
| `MCM_AUTH_LOGIN_LOCKOUT_MAX_ATTEMPTS` | `6` | Maximum failed logins within window before the source is locked out. `>=1`. |
| `MCM_AUTH_LOGIN_LOCKOUT_COOLDOWN` | `15m` | How long a source remains blocked after the lockout window expires. Go duration. |

### Mosquitto broker connection

| Variable | Default | Notes |
| --- | --- | --- |
| `MCM_MOSQUITTO_HOST` | `mosquitto` | Broker hostname or IP. Default matches the bundled compose service. |
| `MCM_MOSQUITTO_PORT` | `1883` | Broker TCP port. 1..65535. Use `8883` for TLS. |
| `MCM_MOSQUITTO_USERNAME` | `admin` | Broker service user. Both `_USERNAME` and `_PASSWORD` must be set or both empty. Dev default matches the Mosquitto bootstrap script. |
| `MCM_MOSQUITTO_PASSWORD` | `mcm-dev-broker-password` | Broker service user password. Read from a secret manager in production. Dev-only. |
| `MCM_MOSQUITTO_CONFIG_DIR` | *(unset)* | Directory containing the Mosquitto configuration. |
| `MCM_MOSQUITTO_DATA_DIR` | *(unset)* | Directory for Mosquitto persistent data (retained messages, persistence file). |

### Mosquitto TLS

| Variable | Default | Notes |
| --- | --- | --- |
| `MCM_MOSQUITTO_TLS_ENABLED` | `false` | Connect to the broker over TLS (typically port 8883). |
| `MCM_MOSQUITTO_TLS_CA_CERT_FILE` | *(unset)* | Path to PEM-encoded CA bundle used to verify the broker certificate. Required when `MCM_MOSQUITTO_TLS_ENABLED=true`. |
| `MCM_MOSQUITTO_TLS_CLIENT_CERT_FILE` | *(unset)* | Path to PEM-encoded client certificate for mTLS to the broker. Required when `MCM_MOSQUITTO_TLS_ENABLED=true`. |
| `MCM_MOSQUITTO_TLS_CLIENT_KEY_FILE` | *(unset)* | Path to PEM-encoded client private key for mTLS to the broker. Required when `MCM_MOSQUITTO_TLS_ENABLED=true`. |
| `MCM_MOSQUITTO_TLS_INSECURE_SKIP_VERIFY` | `false` | Skip broker certificate verification. DEV-ONLY escape hatch for self-signed testing; never enable in production. |

### Mosquitto deploy (file / docker)

| Variable | Default | Notes |
| --- | --- | --- |
| `MCM_MOSQUITTO_DEPLOY_MODE` | *(unset)* | `""` (disabled), `"file"` (write passwd/acl on disk + SIGHUP), or `"docker"` (write files + `docker exec`). |
| `MCM_MOSQUITTO_DEPLOY_ACL_PATH` | *(unset)* | On-disk path for the rendered ACL file. Required when `deploy.mode` is `"file"` or `"docker"`. |
| `MCM_MOSQUITTO_DEPLOY_PASSWD_PATH` | *(unset)* | On-disk path for the rendered passwd file. Required when `deploy.mode` is `"file"` or `"docker"`. |
| `MCM_MOSQUITTO_DEPLOY_PID_PATH` | *(unset)* | Path to the broker's PID file. Optional. |
| `MCM_MOSQUITTO_DEPLOY_CONTAINER_NAME` | *(unset)* | Mosquitto container name for the `"docker"` deploy strategy (used by `docker exec kill -HUP 1`). |
| `MCM_MOSQUITTO_DEPLOY_RELOAD_STRATEGY` | *(unset)* | `""` or `"sighup"` (the only supported strategy right now). |
| `MCM_MOSQUITTO_DEPLOY_HEALTHCHECK_TIMEOUT` | `5s` | Max time the deploy service waits for the broker to come back healthy after a reload. Go duration. |
| `MCM_MOSQUITTO_DEPLOY_WORKDIR` | *(unset)* | Working directory for the deploy service when writing passwd/acl files. |

### Mosquitto Sparkplug tuning

| Variable | Default | Notes |
| --- | --- | --- |
| `MCM_MOSQUITTO_SPARKPLUG_PAYLOAD_DECODE` | `false` | Decode Sparkplug B payloads into typed metrics on the broker events stream. |
| `MCM_MOSQUITTO_SPARKPLUG_MAX_METRICS` | `50` | Cap on the number of metrics kept per Sparkplug payload (defends against unbounded payloads). `>=1`. |

### Metrics / event retention

| Variable | Default | Notes |
| --- | --- | --- |
| `MCM_METRICS_BROKER_RETENTION` | `168h` | How long broker events are persisted. Go duration. Default 7d. |
| `MCM_METRICS_AUDIT_RETENTION` | `2160h` | How long audit events are persisted. Go duration. Default 90d. |
| `MCM_METRICS_SECURITY_RETENTION` | `2160h` | How long security events are persisted. Go duration. Default 90d. |

### Alerting (outbound operational webhooks)

| Variable | Default | Notes |
| --- | --- | --- |
| `MCM_ALERTING_ENABLED` | `false` | Send operational alerts to the configured webhook endpoint. |
| `MCM_ALERTING_ENDPOINT_URL` | *(unset)* | Webhook URL to receive operational alerts. Required when `MCM_ALERTING_ENABLED=true`. |
| `MCM_ALERTING_TIMEOUT` | `5s` | Timeout for individual webhook POSTs. Go duration. |
| `MCM_ALERTING_SIGNING_SECRET` | *(unset)* | HMAC-SHA256 secret used to sign the `X-MCM-Signature` header on outbound alerts. |
| `MCM_ALERTING_COOLDOWN` | `5m` | Minimum interval between repeated alerts of the same class. Go duration. |

### Logging

| Variable | Default | Notes |
| --- | --- | --- |
| `MCM_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. |
| `MCM_LOG_FORMAT` | `json` | `json` (default, recommended for production / SIEM) or `text`. |

### Other

| Variable | Default | Notes |
| --- | --- | --- |
| `MCM_CONFIG_FILE` | *(unset)* | Optional YAML file loaded before env overrides. Not part of the strict-parser table; missing file is an error. |

Full field documentation lives at the top of [`internal/config/config.go`](./internal/config/config.go). The canonical table (and the test that guards it) is in [`internal/config/env_bindings.go`](./internal/config/env_bindings.go).

---

## Development

The local workflow is driven by [`Taskfile.yml`](./Taskfile.yml). Run `task` with no arguments to list every recipe.

| Command           | Purpose                                                   |
| ----------------- | --------------------------------------------------------- |
| `task build`      | Build the `mcm:dev` image.                                |
| `task up`         | Start `mcm` + `mosquitto` in the background.              |
| `task down`       | Stop the stack.                                           |
| `task ps`         | List running services.                                    |
| `task logs`       | Tail `mcm` logs (bootstrap admin prints here).            |
| `task ready`      | Block until `/healthz` returns 200.                       |
| `task smoke`      | Curl `/healthz`, `/readyz`, `/api/v1/status`.             |
| `task backup`     | Snapshot the `mcm_data` volume into `backups/mcm-data.tgz`. |
| `task restore`    | Restore `mcm_data` from `backups/mcm-data.tgz`.           |
| `task test:go`    | `go test ./... -race -count=1`.                           |
| `task test:frontend` | Install `frontend/` deps and run Vitest.              |
| `task test`       | Both test suites.                                         |
| `task lint:go`    | `go vet ./...`.                                           |
| `task lint:frontend` | ESLint over `frontend/`.                              |
| `task lint:openapi` | Redocly lint over `docs/openapi.yaml`.                 |
| `task lint:site`  | Build the Astro site under `site/`.                       |
| `task lint`       | Every linter.                                             |
| `task format`     | `gofmt -l -w .`                                           |
| `task clean`      | Remove the dev image, data volume, build artifacts.       |
| `task help`       | Print the quick-start recipe.                             |

Backend tests, frontend tests, four-platform builds, OpenAPI lint, frontend lint, and an image smoke run all live in CI ([`.github/workflows/ci.yml`](./.github/workflows/ci.yml)). A successful run takes ~6 min.

Frontend work happens under [`frontend/`](./frontend/). Toolchain is pinned by [`frontend/.nvmrc`](./frontend/.nvmrc) and [`frontend/package-lock.json`](./frontend/package-lock.json) — use `npm ci` for reproducible installs.

---

## Architecture

```text
                ┌────────────────────┐
                │     Web UI          │
                │ React + Vite        │
                └─────────┬──────────┘
                          │
                    REST / WebSocket
                          │
                ┌─────────▼──────────┐
                │      MCM API        │
                │        Go           │
                └─────────┬──────────┘
                          │
       ┌──────────────────┼──────────────────┐
       ▼                  ▼                  ▼
┌──────────────┐   ┌──────────────┐   ┌──────────────┐
│ Mosquitto    │   │ SQLite       │   │ Realtime     │
│ Broker       │   │ Config/Auth  │   │ Events       │
└──────────────┘   └──────────────┘   └──────────────┘
```

- **Go 1.25** + stdlib `net/http` + `log/slog` (no third-party HTTP framework).
- **SQLite** via the pure-Go `modernc.org/sqlite` driver (CGO disabled).
- **MQTT**: `eclipse/paho.mqtt.golang` (keepalive, auto-reconnect, TLS).
- **Frontend**: React 19 + Vite 8 + TypeScript 6 + Tailwind CSS 4.
- **Auth**: `golang-jwt/jwt/v5` + Argon2id.
- **Metrics**: `prometheus/client_golang` on a private registry.

---

## Reference

### CLI flags

| Flag       | Default          | Description                                            |
| ---------- | ---------------- | ------------------------------------------------------ |
| `--config` | (none, env-only) | Path to a YAML config file. All settings can also be set via `MCM_*` environment variables. See [deploy/mcm/README.md](./deploy/mcm/README.md). |
| `--version`| `false`          | Print build info (`mcm <version> (commit <sha>, built <iso8601>)`) and exit. Values come from `-ldflags` at build time; a plain `docker buildx build` (no `--build-arg`) prints `mcm dev (commit none, built unknown)`. |

### Health probes

| Endpoint     | Body                              | Status           | Semantics                                                                                  |
| ------------ | --------------------------------- | ---------------- | ------------------------------------------------------------------------------------------ |
| `GET /livez` | `{"status":"alive"}`              | `200`            | Pure process check — never touches the DB or the broker. Use for K8s/Compose liveness.     |
| `GET /healthz` | `{"status":"ok"}`              | `200`            | Alias of `/livez`, kept for backward compatibility with existing probes.                   |
| `GET /readyz` | `{"status":"ready"\|"not_ready"}` | `200` / `503`    | DB **and** broker must be reachable; returns `503` otherwise. Use for readiness gates.     |

### More

- [ROADMAP.md](./ROADMAP.md) — high-level product direction.
- [docs/openapi.yaml](./docs/openapi.yaml) — full REST + WebSocket contract.
- [docs/production.md](./docs/production.md) — production deployment guide.
- [docs/acl.md](./docs/acl.md) — ACL model and REST shape.
- [docs/json-schemas.md](./docs/json-schemas.md) — JSON Schema validators bound to MQTT topic filters.
- [docs/sparkplug.md](./docs/sparkplug.md) — Sparkplug B namespace classification.
- [docs/webhook-alerting.md](./docs/webhook-alerting.md) — outbound operational webhooks.
- [docs/adr](./docs/adr/) — architecture decision records.
- [deploy/mcm/README.md](./deploy/mcm/README.md) — operator-facing variable reference.

---

## Contributing

MCM is public and feedback is welcome, but it is currently maintainer-led. Issues and pull requests should be focused, actionable, and aligned with the roadmap.

Before opening a non-trivial pull request, please open an issue first and wait for maintainer feedback. All changes to `main` go through pull requests and require maintainer/code owner review.

See [CONTRIBUTING.md](./CONTRIBUTING.md) for the contribution workflow and [SECURITY.md](./SECURITY.md) for security reporting.

---

## License

MCM is licensed under the [Apache License 2.0](./LICENSE).

Please preserve copyright, license, and attribution notices when redistributing or modifying the project. See [NOTICE](./NOTICE).

If you use MCM in research, documentation, talks, articles, commercial case studies, or public deployments, citation is appreciated. See [CITATION.cff](./CITATION.cff).

---

## Name

**MCM** stands for **Mosquitto Control Manager**.