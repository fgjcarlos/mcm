# Mosquitto Control Manager (MCM)

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

See [ROADMAP.md](./ROADMAP.md) for the planned phases.

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
mcm version          # Print version/build information
```

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

Development instructions will be added once the initial project structure is created.

Expected future workflow:

```bash
# Clone repository
git clone https://github.com/fgjcarlos/mcm.git
cd mcm

# Start development stack
make dev

# Run backend tests
make test

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

MCM is in an early stage. Contributions are welcome once the initial architecture and contribution guidelines are in place.

Good early contributions will include:

- Mosquitto integration research
- ACL model design
- Go backend foundations
- Frontend UX prototypes
- Docker Compose development setup
- Documentation improvements

---

## License

License to be decided before the first public release.

Recommended options:

- **Apache-2.0** if permissive use and future commercial adoption are important.
- **AGPL-3.0** if protecting open source network-service usage is more important.

---

## Name

**MCM** stands for **Mosquitto Control Manager**.
