# MCM Roadmap

This roadmap describes the product direction for Mosquitto Control Manager.

It is intentionally split into two layers:

1. **Roadmap document**: communicates strategy, phases, and scope.
2. **GitHub Issues**: track concrete work items with acceptance criteria.

This is usually better than using only issues as a roadmap. A roadmap explains *why and when*; issues explain *what exactly must be done next*.

---

## Roadmap philosophy

MCM should avoid becoming a large enterprise broker clone. The strongest initial positioning is:

> Make Mosquitto easy to administer, observe, and secure in edge and industrial deployments.

Priorities:

- Build a narrow but usable MVP first.
- Keep Mosquitto external and compatible.
- Prefer simple deployments over complex infrastructure.
- Make security and diagnostics part of the first version.
- Delay clustering, multi-tenancy, SSO, and advanced industrial integrations until the product proves value.

---

## Phase 0 — Project foundation

**Goal:** prepare the repository and define the technical base.

Deliverables:

- Project structure for Go backend and React frontend
- Architecture Decision Records (ADRs)
- Development Docker Compose stack with Mosquitto
- Basic Makefile or task runner
- CI pipeline for linting, tests, and builds
- License decision
- Contribution guidelines

Exit criteria:

- New contributors can clone the repository and run the development environment.
- The stack can start Mosquitto plus the placeholder MCM service locally.
- CI validates the repository on every pull request.

---

## Phase 1 — MVP: visual Mosquitto administration

**Goal:** deliver the smallest usable product for managing a Mosquitto instance.

Scope:

- Admin authentication
- User management
- ACL management
- Basic Mosquitto configuration integration
- Basic broker status dashboard
- Connected clients view
- Topic explorer / MQTT message viewer
- REST API for core resources
- WebSocket channel for realtime updates
- CLI commands: `server`, `doctor`, `status`, `config validate`, `version`
- Docker Compose deployment example
- SQLite persistence

Exit criteria:

- An operator can start Mosquitto + MCM locally.
- An admin can log in, create a user, assign ACLs, and validate access from an MQTT client.
- The UI shows broker status and basic client/topic activity.
- `mcm doctor` detects common configuration problems.

---

## Phase 2 — Observability and operations

**Goal:** make MCM useful for day-to-day monitoring and troubleshooting.

Scope:

- Historical metrics
- Realtime logs view
- Message rate charts
- Top clients and topics
- Authentication and ACL failure visibility
- Audit log for administrative actions
- Backup and restore commands
- Alerting hooks or webhook notifications
- Improved TLS configuration documentation

Exit criteria:

- Operators can diagnose common MQTT issues from the UI and CLI.
- Administrative changes are auditable.
- Metrics can be retained locally with configurable retention.

---

## Phase 3 — Industrial edge features

**Goal:** make MCM more useful in industrial IoT environments.

Scope:

- Sparkplug B awareness
- Payload inspection helpers
- JSON schema validation support
- Node-RED integration examples
- Edge agent concept for remote sites
- Optional PostgreSQL support for larger deployments
- Improved multi-architecture release pipeline

Exit criteria:

- MCM provides value beyond generic MQTT visibility in industrial deployments.
- Edge deployments can be installed and updated predictably on common ARM and x86 devices.

---

## Phase 4 — Advanced / possible open-core features

**Goal:** evaluate whether there is enough adoption to justify advanced commercial or enterprise-oriented functionality.

Possible scope:

- Multi-broker management
- Multi-tenancy
- High availability patterns
- SSO / LDAP / OAuth2
- Advanced backup policies
- Central fleet management
- Role templates and approval workflows

Exit criteria:

- These features should only be built if real users request them or a business model validates them.

---

## Backlog themes

### Backend

- Go HTTP API
- Config management
- SQLite persistence
- Mosquitto integration
- ACL model
- Auth/session management
- CLI commands
- Audit logging

### Frontend

- Login screen
- Main dashboard
- User management UI
- ACL editor
- Topic explorer
- Client list
- Realtime event views
- Responsive layout

### Infrastructure

- Docker Compose
- CI
- GoReleaser
- Multi-architecture images
- Example Mosquitto configs
- Secure defaults

### Documentation

- Quickstart
- Architecture overview
- Security model
- Deployment guide
- Mosquitto integration guide
- Contributor guide

---

## Roadmap as issues: recommended practice

Using issues for the whole roadmap is not ideal if every issue is very broad, for example “Build MVP”. Those become hard to close and do not guide implementation.

Recommended structure:

- Keep this `ROADMAP.md` for public strategy.
- Use GitHub Milestones for phases, for example `Phase 0 — Foundation` and `Phase 1 — MVP`.
- Use GitHub Issues for actionable tasks that can be completed and reviewed.
- Keep each issue focused, with context, scope, and acceptance criteria.
- Convert large ideas into epics only when they link to smaller issues.

Good issue example:

```markdown
Title: Add initial Mosquitto health check to `mcm doctor`

Context:
MCM needs a CLI diagnostic command that verifies the broker is reachable.

Scope:
- Read broker host/port from config
- Connect with an MQTT client
- Report success/failure in terminal output

Acceptance criteria:
- `mcm doctor` exits 0 when Mosquitto is reachable
- `mcm doctor` exits non-zero when Mosquitto is unreachable
- The output includes a human-readable error message
```

Bad issue example:

```markdown
Title: Build observability
```

Too broad, unclear, and hard to close.
