# Mosquitto Control Manager (MCM)

[![CI](https://github.com/fgjcarlos/mcm/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/fgjcarlos/mcm/actions/workflows/ci.yml)

**MCM is an open source control plane for Eclipse Mosquitto.**

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

**Status: early concept / pre-MVP.**

The repository is currently being prepared for the first technical milestone. The first target is a small, usable MVP that can manage a Mosquitto instance in a local or edge deployment.

See [ROADMAP.md](./ROADMAP.md) for the planned phases and [docs/adr](./docs/adr/) for architecture decision records. Operational webhook alerting is documented in [docs/webhook-alerting.md](./docs/webhook-alerting.md), topic-level Sparkplug B awareness is documented in [docs/sparkplug.md](./docs/sparkplug.md), and JSON schema validation is documented in [docs/json-schemas.md](./docs/json-schemas.md).

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

## Planned MVP scope

The first usable version should stay intentionally small:

- Authentication and admin login
- User management
- Visual ACL management
- Basic Mosquitto configuration integration
- Basic dashboard with broker/client status
- Topic explorer / MQTT message viewer
- REST API
- WebSocket events for realtime UI updates
- Docker Compose development environment
- SQLite-based local persistence
- Initial CLI commands: `server`, `doctor`, `status`, `config validate`, `version`

Non-goals for the MVP:

- Multi-broker management
- Clustering / high availability
- Multi-tenancy
- SSO / LDAP
- Advanced industrial protocol bridges
- Automatic broker replacement or embedded broker mode

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

## Suggested technology stack

### Backend

- Go
- HTTP framework: Echo, Gin, or Fiber
- SQLite for MVP persistence
- Cobra for CLI commands
- MQTT client integration for broker status, topic inspection, and realtime events

### Frontend

- React
- Vite
- TypeScript
- Tailwind CSS
- shadcn/ui
- Realtime updates through WebSocket

### Deployment

- Docker Compose for development and quick trials
- Single binary for lightweight production installs
- Future multi-arch builds with GoReleaser

Target platforms:

- linux-amd64
- linux-arm64
- linux-arm/v7
- windows-amd64
- darwin-arm64

---

## Initial CLI concept

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

The initial Go CLI skeleton is available under [`cmd/mcm`](./cmd/mcm).

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

## ACL API

The initial ACL model and REST API are documented in [`docs/acl.md`](./docs/acl.md).

The current API provides:

- `GET /api/v1/acls`
- `POST /api/v1/acls`
- `PUT /api/v1/acls/{id}`
- `DELETE /api/v1/acls/{id}`

It supports MQTT wildcard topic filters, validates invalid wildcard placement, and maps permissions directly to Mosquitto ACL lines such as `topic read ...`, `topic write ...`, and `topic readwrite ...`.

### Frontend development

The React and Vite frontend skeleton lives under [`frontend/`](./frontend/).

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

### Future MCM service

The Compose stack currently starts Mosquitto only because the backend service is still a skeleton. The expected future MCM service shape is documented in [`deploy/mcm/README.md`](./deploy/mcm/README.md).

Expected future workflow once the backend server and frontend exist:

```bash
# Start full development stack
make dev

# Build frontend + backend
make build
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
