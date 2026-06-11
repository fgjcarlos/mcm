# Mosquitto Control Manager (MCM)

[![CI](https://github.com/fgjcarlos/mcm/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/fgjcarlos/mcm/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](./LICENSE)
[![Go](https://img.shields.io/badge/go-1.24-00ADD8?logo=go&logoColor=white)](./go.mod)
[![Node](https://img.shields.io/badge/node-22.13%20%7C%2024-339933?logo=node.js&logoColor=white)](./frontend/.nvmrc)
[![OpenAPI](https://img.shields.io/badge/OpenAPI-3.1-6BA539?logo=openapiinitiative&logoColor=white)](./docs/openapi.yaml)

**Website**: [fgjcarlos.github.io/mcm](https://fgjcarlos.github.io/mcm) · **MCM is an open source control plane for Eclipse Mosquitto.**

It aims to make Mosquitto easier to operate by adding a modern web UI, REST API, realtime observability, user management, ACL management, and deployment tooling — without replacing Mosquitto as the MQTT broker.

> The goal is simple: **the stability and small footprint of Mosquitto, with a modern administration and observability experience.**

---

## Why MCM?

Mosquitto is lightweight, reliable, widely adopted, and ideal for edge and industrial MQTT deployments. But day-to-day operation often still depends on manually editing configuration files, ACL files, external dashboards, scripts, and ad-hoc tooling.

MCM focuses on the missing operational layer:

- Visual user and ACL management
- MQTT traffic and client observability
- Topic exploration
- Simple Docker and standalone deployments
- API-first administration
- Edge/industrial-friendly operation

MCM is not intended to compete with enterprise MQTT brokers at the broker level. It is designed to make **existing Mosquitto deployments easier to manage**.

---

## Project status

**Status: Alpha — the MVP is feature-complete and the project is in a hardening phase.**

MCM now ships an end-to-end stack: backend API + CLI, persistent SQLite, MQTT 3.1.1 client (paho), realtime WebSocket stream, dashboard UI with real login, RBAC, structured logging, Prometheus metrics, optional HTTPS / mTLS, audit and security event trails, and webhook alerting. See [What works today](#what-works-today) for the full list.

Reference docs:

- [ROADMAP.md](./ROADMAP.md) — high-level phases and direction.
- [docs/openapi.yaml](./docs/openapi.yaml) — full REST + WebSocket API contract (OpenAPI 3.1).
- [docs/adr](./docs/adr/) — architecture decision records.
- [docs/acl.md](./docs/acl.md) — ACL model and REST shape.
- [docs/json-schemas.md](./docs/json-schemas.md) — JSON Schema validators bound to MQTT topic filters.
- [docs/sparkplug.md](./docs/sparkplug.md) — Sparkplug B topic awareness.
- [docs/webhook-alerting.md](./docs/webhook-alerting.md) — outbound operational webhooks.
- [docs/integrations/node-red.md](./docs/integrations/node-red.md) — Node-RED integration examples.

---

## Core principles

- **Mosquitto stays the broker**: MCM acts as a control plane, not a broker replacement.
- **Edge-first**: low resource usage, simple deployment, Raspberry Pi / ARM compatibility.
- **Simple operations**: Docker Compose for fast setup, standalone binary for production-like edge installs.
- **Open source first**: useful community edition before considering any open-core model.
- **Industrial-friendly**: clear APIs, diagnostics, auditability, and predictable deployment patterns.
- **Secure by default**: password hashing, scoped permissions, TLS support, audit logs, and explicit configuration.

---

## Target users

- Industrial IoT engineers
- Automation and OT/IT integrators
- Node-RED users
- Homelab and maker users running Mosquitto
- Edge computing deployments
- Smart building, energy, agriculture, and Industry 4.0 projects

---

## What works today

**Backend**

- Admin authentication with Argon2id-hashed passwords (PHC-formatted), JWT tokens, and an /auth/me session check.
- Optional TOTP MFA per admin user: `/auth/mfa/setup` returns an `otpauth://` URL and ten single-use recovery codes (hashed at rest); enrolled users complete login via a short-lived MFA challenge token.
- Login brute-force protection: per-IP and per-username failure counters with HTTP 429 + `Retry-After` lockouts and security events.
- Role-based access control with four roles (`viewer < auditor < operator < admin`); every protected endpoint declares a minimum role.
- ACL management persisted in SQLite, MQTT wildcard validation, and Mosquitto ACL line projection.
- JSON Schema validators attached to MQTT topic filters, evaluated live against incoming payloads.
- Sparkplug B namespace classification on observed topics.
- MQTT subscription via the maintained `eclipse/paho.mqtt.golang` client (keepalive, auto-reconnect, TLS).
- WebSocket event stream at `/api/v1/broker/events` authenticated via `Sec-WebSocket-Protocol` (no token in URLs).
- Optional HTTPS and mTLS on the MCM HTTP API.
- Persistent admin audit trail and operator-facing security event log.
- Outbound webhook alerting with HMAC signatures.
- Prometheus `/metrics` endpoint with bounded label cardinality, plus a starter Grafana dashboard.
- Structured `log/slog` JSON logs with request-ID propagation (`X-Request-ID`).
- OpenAPI 3.1 contract for every endpoint, linted in CI.

**Frontend**

- Real login screen against `/api/v1/auth/login`; the token is the single source of truth for protected API calls and the broker WebSocket subprotocol handshake.
- Dashboard with broker status, traffic widgets, topic explorer, realtime log feed, audit + security panels.
- Logout from the sidebar; expired or 401 responses bounce the user back to the login screen.

**Operations**

- Single-binary CLI (`mcm server | doctor | status | config | backup | version`).
- Docker Compose dev stack for a local Mosquitto broker.
- GoReleaser packaging targeting Linux amd64/arm64, macOS arm64, Windows amd64.
- CI on every PR: `go test -race` + cross-OS builds + frontend lint/build + OpenAPI lint.

**Out of scope for the first stable release**

- Multi-broker management
- Clustering / high availability
- Multi-tenancy
- SSO / LDAP / SAML
- Advanced industrial protocol bridges
- Embedded broker mode (Mosquitto stays the broker)

---

## Proposed architecture

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
       │                  │                  │
       ▼                  ▼                  ▼
┌──────────────┐   ┌──────────────┐   ┌──────────────┐
│ Mosquitto    │   │ SQLite       │   │ Realtime     │
│ Broker       │   │ Config/Auth  │   │ Events       │
└──────────────┘   └──────────────┘   └──────────────┘
```

The frontend can be embedded into the Go binary for simple distribution:

```go
//go:embed all:frontend/dist
```

---

## Tech stack

### Backend

- **Go 1.24** with the stdlib `net/http` ServeMux (no third-party HTTP framework) and `log/slog` for structured logging.
- **SQLite** via the pure-Go `modernc.org/sqlite` driver so the binary cross-compiles to every supported target without CGO.
- **Cobra** for the CLI.
- **MQTT**: `eclipse/paho.mqtt.golang` for broker subscription, status, and topic inspection.
- **Auth**: `golang-jwt/jwt/v5` + `golang.org/x/crypto/argon2`/bcrypt for password hashing.
- **Metrics**: `prometheus/client_golang` on a private registry.
- **Tests**: stdlib `testing`; in-process MQTT broker for integration via `mochi-mqtt/server/v2`.

### Frontend

- **React 19** + **Vite 8** + **TypeScript 6**
- **Tailwind CSS 4** via `@tailwindcss/vite`
- **ESLint 10** (with `eslint-plugin-react-hooks` 7)
- Reproducible installs pinned by `frontend/package-lock.json` and `frontend/.nvmrc`.

### Deployment

- Single statically linked Go binary (CGO disabled; pure-Go SQLite driver covers every target).
- Docker Compose stack for local Mosquitto.
- **GoReleaser** multi-arch builds for `linux/amd64`, `linux/arm64`, `darwin/arm64`, `windows/amd64`.

---

## CLI

```bash
mcm server           # Start API, web UI, and Mosquitto integration
mcm doctor           # Run diagnostics against Mosquitto, config, DB, TLS, disk
mcm status           # Show broker and MCM runtime status
mcm config init      # Create initial config file
mcm config validate  # Validate config file
mcm backup create    # Create a portable SQLite backup artifact
mcm backup restore   # Restore local state from a SQLite backup artifact
mcm version          # Print version/build information
```

Every command takes a global `--config` flag pointing at the YAML file documented in [docs/openapi.yaml](./docs/openapi.yaml) and the [`config init` template](./internal/config/). The main `mcm` server reads runtime configuration from that YAML file; the server honors `MCM_AUTH_JWT_SECRET`, `MCM_BOOTSTRAP_ADMIN_USERNAME`, and `MCM_BOOTSTRAP_ADMIN_PASSWORD` environment overrides (other fields come from the YAML).

### Backup and restore

MCM stores its local application state in the SQLite database configured by `database.path`. Operators can create a consistent backup artifact and restore it later with the CLI:

```bash
# Create a backup from the configured SQLite database.
mcm backup create --config ./mcm.yaml --output ./backups/mcm.db

# Restore into the configured database path.
# If the target database already exists, --force is required to overwrite it.
mcm backup restore --config ./mcm.yaml --input ./backups/mcm.db --force
```

Backup artifacts are SQLite database files created from a consistent snapshot of the configured MCM database. They include MCM state stored in SQLite, such as admin users, persisted broker metrics, security events, and audit events.

Backups do **not** include files outside the MCM SQLite database, including external Mosquitto configuration, Mosquitto password files, ACL files, TLS CA/client certificates or keys, Docker volumes, logs, or files referenced from configuration. Back up those assets separately according to your deployment process.

Restore validates the input artifact before writing it. To protect running installations from accidental data loss, restore refuses to overwrite an existing configured database unless `--force` is provided. Stop the MCM server before restore so no process is using the database while it is replaced.

---

## Security baseline

The project should treat security as a product requirement, not a later add-on:

- Password hashing with a modern algorithm
- JWT/session security with explicit expiration
- TLS support documentation
- Principle-of-least-privilege ACL model
- Audit log for administrative actions
- Safe handling of Mosquitto password and ACL files
- Clear warnings for insecure development-only settings

---

## Development setup

### Mosquitto development stack

The repository includes a Docker Compose stack for local Mosquitto development.

```bash
# Clone repository
git clone https://github.com/fgjcarlos/mcm.git
cd mcm

# Start local Mosquitto
docker compose up -d

# Follow broker logs
docker compose logs -f mosquitto

# Stop the stack
docker compose down
```

The development broker exposes:

- MQTT TCP: `localhost:1883`
- MQTT over WebSocket: `localhost:9001`

The local Mosquitto configuration lives in [`deploy/mosquitto/config/mosquitto.conf`](./deploy/mosquitto/config/mosquitto.conf).

> This configuration allows anonymous access for local development only. Do not use it as-is in production.

### MCM CLI development

The Go CLI lives under [`cmd/mcm`](./cmd/mcm); production wiring is in [`internal/cli`](./internal/cli) and [`internal/server`](./internal/server).

```bash
# Show available commands
go run ./cmd/mcm --help

# Print build/version information
go run ./cmd/mcm version

# Start the initial API server
go run ./cmd/mcm server --config ./mcm.yaml

# Run tests
go test ./...
# or
make test

# Build the CLI binary
make build
```

`make build` and `make test` treat the embedded frontend as part of the Go build contract: they build `frontend/dist` first, then compile or test the Go code. If you call `go build`, `go test`, or `go run` directly, build `frontend/dist` yourself first with `npm --prefix frontend run build`.

## ACL API

The initial ACL model and REST API are documented in [`docs/acl.md`](./docs/acl.md).

The current API provides:

- `GET /api/v1/acls`
- `POST /api/v1/acls`
- `PUT /api/v1/acls/{id}`
- `DELETE /api/v1/acls/{id}`

It supports MQTT wildcard topic filters, validates invalid wildcard placement, and maps permissions directly to Mosquitto ACL lines such as `topic read ...`, `topic write ...`, and `topic readwrite ...`.

### Frontend development

The React + Vite frontend lives under [`frontend/`](./frontend/). It ships a login screen, a broker dashboard, audit/security panels, and the realtime topic explorer.

**Toolchain requirements:**

- Node.js `^22.13.0` (LTS Jod) or `>=24.0.0` (LTS), enforced via the `engines` field in [`frontend/package.json`](./frontend/package.json).
- npm `>=10.0.0`.

A [`frontend/.nvmrc`](./frontend/.nvmrc) pins a tested LTS version — run `nvm use` from `frontend/` to switch.

Reproducible installs rely on the committed `frontend/package-lock.json`. Use `npm ci` in clean environments (such as CI) to install the exact resolved tree; use `npm install` only when you intentionally want to update dependencies.

```bash
# One-time, reproducible install (matches package-lock.json exactly)
cd frontend
npm ci

# Start the Vite development server
npm run dev

# Create a production build
npm run build
```

The production Go binary embeds `frontend/dist/index.html` and the rest of `frontend/dist`. That means the frontend build must exist before any direct `go build`, `go test`, or `go run` of the embedded application.

### API specification

The full REST + WebSocket API is described in OpenAPI 3.1 at [`docs/openapi.yaml`](./docs/openapi.yaml). It covers every endpoint MCM currently exposes (health, metrics, auth, admin users, ACLs, JSON schemas, audit/security events, broker WebSocket) with request/response schemas, auth requirements, and per-route role floors.

Render or browse the spec with any OpenAPI viewer, for example:

```bash
# Local Swagger UI (no install)
docker run --rm -p 8081:8080 -e SWAGGER_JSON=/spec/openapi.yaml \
  -v "$(pwd)/docs:/spec" swaggerapi/swagger-ui

# Or Redocly preview
npx -y @redocly/cli@latest preview-docs docs/openapi.yaml
```

The spec is linted on every PR via `npx @redocly/cli lint` in CI ([`.github/workflows/ci.yml`](./.github/workflows/ci.yml), `openapi` job), with rule overrides documented in [`redocly.yaml`](./redocly.yaml).

### Health, readiness, and observability

MCM exposes unauthenticated probe endpoints for operators and orchestrators:

- `GET /healthz` — liveness only. It returns `{"status":"ok"}` when the HTTP process is running and should be used for restart decisions.
- `GET /readyz` — readiness. It verifies required serving dependencies (currently SQLite) and returns `{"status":"ready"}` with HTTP 200 when the instance should receive traffic, or HTTP 503 with `{"status":"not_ready"}` while unavailable.
- `GET /api/v1/status` — operational status snapshot for dashboards and humans, including broker connectivity and event metrics. Do not use it as a liveness probe; broker disconnects are reported here without implying the MCM process should be restarted.

MCM exposes Prometheus metrics at `GET /metrics` (text exposition format, unauthenticated by design — gate it at your reverse proxy or via network policy in production).

Exposed metric families:

- `mcm_http_requests_total{method, route, status}` — HTTP request counter. `method` is the request method, `route` is the ServeMux path pattern without the method prefix (for example `/api/v1/admin-users/{id}`), and `status` is the three-digit response status code. Unmatched requests use `route="unmatched"`; raw URLs and IDs are never labels, so cardinality stays bounded.
- `mcm_http_request_duration_seconds{method, route}` — request latency histogram (default Prometheus buckets) using the same `method` and bounded `route` label contract.
- `mcm_broker_status` — 1 when Mosquitto is connected, 0 otherwise.
- `mcm_broker_reconnects_total` — broker disconnect transitions (each triggers an auto-reconnect attempt).
- `mcm_broker_messages_total` — MQTT topic messages observed. Topic names are intentionally **not** labels.
- `mcm_broker_payload_bytes_total` — total observed payload bytes.
- `mcm_login_attempts_total{result}` — admin login attempts (`success` or `failure`).
- `mcm_audit_events_total{result}` — administrative audit events.
- `mcm_security_events_total{category}` — security events, labeled by category (bounded set).

Sample Prometheus scrape:

```yaml
scrape_configs:
  - job_name: mcm
    metrics_path: /metrics
    scheme: http  # or https when http.tls.enabled is true
    static_configs:
      - targets: ["mcm-host:8080"]
```

A starter Grafana dashboard is committed at [`deploy/grafana/mcm-dashboard.json`](./deploy/grafana/mcm-dashboard.json) (broker status, reconnect rate, message rate, HTTP request/latency by route, login attempts, security/audit events). Import it in Grafana and pick your Prometheus datasource.

### HTTPS and optional mTLS

The MCM HTTP API can serve HTTPS by populating `http.tls` in the configuration:

```yaml
http:
  bind_address: 0.0.0.0
  port: 8443
  tls:
    enabled: true
    cert_file: /etc/mcm/tls/server.crt
    key_file: /etc/mcm/tls/server.key
    min_version: "1.2"        # or "1.3"
    client_ca_file: ""         # set to enable client cert verification
    require_client_cert: false # set to true with client_ca_file for strict mTLS
```

When `tls.enabled` is true:

- Cert and key are loaded with [`tls.LoadX509KeyPair`](https://pkg.go.dev/crypto/tls#LoadX509KeyPair); validation fails fast if either file is missing.
- The minimum negotiated TLS version is enforced (`1.2` for broad compatibility, `1.3` for stricter deployments).
- All responses include `Strict-Transport-Security: max-age=31536000; includeSubDomains`.
- Setting `client_ca_file` enables client certificate verification: optional unless `require_client_cert: true`, in which case clients without a CA-signed certificate are rejected at the handshake.

**Local development with `mkcert`** (trusted by the dev machine, no browser warning):

```bash
mkcert -install
mkcert -cert-file dev.crt -key-file dev.key 127.0.0.1 localhost
```

Point `http.tls.cert_file`/`key_file` at `dev.crt`/`dev.key`.

**Quick self-signed test certificate with `openssl`** (useful for non-browser clients):

```bash
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 \
  -keyout server.key -out server.crt -days 365 -nodes \
  -subj "/CN=mcm-local" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"
```

**Production guidance**: use certificates issued by your internal CA (or a public ACME provider for internet-exposed deployments), keep `key_file` mode `0600`, and bind only to the interface that should accept inbound traffic. For mTLS, ship the trusted client CA bundle to `client_ca_file` and enable `require_client_cert: true` to make a missing or invalid client certificate fail closed.

### Running MCM alongside Mosquitto

The Compose stack starts Mosquitto only; the MCM service is launched separately with the CLI so operators control its lifecycle and TLS posture. The intended deployment shape (and the operator-facing variables) is documented in [`deploy/mcm/README.md`](./deploy/mcm/README.md).

End-to-end development loop:

```bash
# 1. Start the local broker
docker compose up -d

# 2. Start MCM against the dev config
go run ./cmd/mcm server --config ./mcm.yaml

# 3. In a second terminal, build the frontend (or `npm run dev` for HMR)
npm --prefix frontend run build
```

---

## Roadmap

The roadmap is tracked in two places:

- [ROADMAP.md](./ROADMAP.md): high-level product direction and phases
- GitHub Issues: actionable tasks with acceptance criteria

This keeps the public vision readable while making actual execution trackable.

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
