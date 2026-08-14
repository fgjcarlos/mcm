# MCM Roadmap

This roadmap describes the product direction for Mosquitto Control Manager
after the **Docker-first pivot** that landed in #226 (2026-Q2). The pivot
removed the `mcm` CLI, the `mcm-agent` edge agent, the in-process broker,
and the Postgres-as-default track. MCM now ships as a single Docker image
(`ghcr.io/fgjcarlos/mcm`), driven locally by `task` + `docker compose`, and
exposes its functionality through the REST/WebSocket API, the embedded web
UI, and a small set of HTTP health endpoints (`/livez`, `/healthz`,
`/readyz`).

The roadmap is split into three layers so a reader can answer three
questions quickly:

1. **Funciona hoy** — what is already shipping in the current
   Docker-first build (no work needed to use it).
2. **Gap para MVP** — what is still open before the project can call the
   MVP done.
3. **Futuro** — what comes after MVP, listed for transparency but not
   committed to.

References to the removed CLI subcommands (`server`, `doctor`, `status`,
`config validate`), the removed `mcm-agent` edge process, the
backup/restore CLI, and Postgres-as-default are **historical**. They
describe pre-#226 scope and are kept here only so contributors can see
what was dropped and why. None of those commands or binaries exist in
the current tree; see `cmd/mcm/main.go` and the ADRs for the live
contract.

---

## Roadmap philosophy

MCM should avoid becoming a large enterprise broker clone. The strongest
positioning remains:

> Make Mosquitto easy to administer, observe, and secure in edge and
> industrial deployments.

Priorities after the pivot:

- Build on top of the single Docker image — no host-side binaries, no CLI
  surface area beyond `--config` / `--version`.
- Keep Mosquitto external and compatible.
- Prefer simple deployments over complex infrastructure.
- Make security and diagnostics part of the first version.
- Delay clustering, multi-tenancy, SSO, and advanced industrial
  integrations until the product proves value.

---

## Funciona hoy (ships in the current Docker-first build)

These capabilities are already in `main` and reachable from the published
image. No further work is needed to use them.

### Single-image deployment

- `ghcr.io/fgjcarlos/mcm:<version>` (multi-arch: `linux/amd64`,
  `linux/arm64`), published by `.github/workflows/release.yml`. Tags are
  derived from the Git tag with the leading `v` stripped (e.g. tag
  `v0.1.0` → image `ghcr.io/fgjcarlos/mcm:0.1.0`).
- Local dev workflow driven by `task` (`task build`, `task up`, `task
  logs`, `task smoke`, `task down`, `task backup`, `task restore`); see
  `Taskfile.yml` for the full recipe list.
- Docker Compose stack at the repo root that wires MCM to a bundled
  Mosquitto broker with auth + ACL enabled (see `docker-compose.yml`).
- Built-in container `HEALTHCHECK` against `GET /livez` (compose augments
  this with the same endpoint).

### HTTP API + embedded UI

- REST endpoints for auth, MQTT users, ACLs, JSON schemas, broker status,
  audit events, and deployments (full contract in `docs/openapi.yaml`).
- WebSocket stream on `/api/v1/broker/events`.
- React + Vite + TypeScript SPA embedded into the Go binary via
  `//go:embed` (no separate frontend container needed).
- JSON-Schema-bound MQTT topic validation helpers and Sparkplug B
  classification helpers (see `docs/json-schemas.md`, `docs/sparkplug.md`).

### Health endpoints

- `GET /livez` — pure process check (HTTP server + SQLite).
- `GET /healthz` — alias of `/livez`, kept for backward compatibility.
- `GET /readyz` — DB + broker reachability; returns 503 with a
  `phase` field in the JSON body when the broker is unreachable.
- `GET /api/v1/status` — human/dashboard status; **not** a liveness
  probe.

### Mosquitto integration

- Internal MQTT probe (`internal/diagnostics.CheckMQTTConnectivity`) that
  reports TCP / TLS / MQTT failure phases.
- Deploy service that renders the current ACL rules + MQTT users into a
  shared `mosquitto_config` volume, with atomic write + SIGHUP reload
  (dev: `docker exec … kill -HUP 1`; production: file mode with a
  broker-side reload trigger).
- Operator-facing guide at `deploy/mosquitto/README.md` covering the dev
  contract, the production TLS checklist, and the shared-volume pattern.

### Configuration

- Configuration via `MCM_*` environment variables (preferred) with an
  optional YAML file mounted through `MCM_CONFIG_FILE`. The binary takes
  exactly two flags: `--config <path>` and `--version` (see
  `cmd/mcm/main.go`). Everything else is environment-driven.
- Auto-generated bootstrap admin password + JWT secret persisted under
  `<db dir>/.bootstrap.json` (mode 0600) so a fresh clone of the dev
  stack works with zero operator input.

### Operations + observability

- Prometheus metrics on `/metrics` (private registry).
- Audit + security events recorded in SQLite.
- Outbound webhook alerting (see `docs/webhook-alerting.md`).
- Backup/restore recipes (`task backup`, `task restore`) that snapshot
  the `mcm_data` named volume.

### CI surface

- `.github/workflows/ci.yml` builds the dev image, smoke-runs it against
  `/livez`, `/healthz`, and `/readyz`, runs `go test ./... -race`, Go
  lint (go vet + golangci-lint), frontend lint + tests, OpenAPI lint,
  Astro site build, and the Quickstart smoke (clean clone, no env).
- `.github/workflows/release.yml` builds + pushes the multi-arch image
  to GHCR with provenance + SBOM attestations and uploads the SPDX SBOM
  as a release asset.
- `.github/workflows/fuzz.yml` runs the Go fuzz targets.
- `.github/workflows/site.yml` deploys the Astro site.

---

## Gap para MVP (open before MVP)

These items are tracked in GitHub issues. They are scoped enough to be
closed individually and have acceptance criteria. MVP is "done" when
this list is empty.

- **Production deployment story**: harden the production checklist
  (reverse-proxy examples already live in `docs/production.md`); align
  `deploy/mcm/README.md` and `deploy/mosquitto/README.md` with the
  shipped artifacts (in flight via this document).
- **Real backup/restore path for production**: `task backup` /
  `task restore` ship today but are volume-only. Document the
  production schedule + off-host sync strategy and ship a restore drill
  helper.
- **Auth hardening**: rotation workflow for `MCM_AUTH_JWT_SECRET`,
  bootstrap-admin disable/rename path, MFA enforcement policy.
- **WebSocket + ACL UX polish**: edge cases surfaced by the Node-RED
  integration guide (`docs/integrations/node-red.md`).
- **Webhook alerting v1**: schema, retries, dead-letter handling.
- **Sparkplug B v1**: classification helpers land; the full Sparkplug
  surface (metric introspection, namespace validation) is tracked
  separately.
- **Prometheus alert templates**: starter rules for `mcm_broker_status`,
  `mcm_login_attempts_total`, `mcm_http_request_duration_seconds`.
- **Documentation coherence**: see #280 — the very issue this roadmap
  update accompanies. The CI check added there fails the build if
  removed CLI commands or stale versions reappear in active docs.

---

## Futuro (post-MVP, not committed)

Items in this list are visible for transparency but are not scheduled
and may be re-scoped or dropped based on adoption.

- Multi-broker management from a single MCM instance.
- Multi-tenancy + role templates.
- High-availability / multi-instance write coordination (this is the
  only path that would re-open the Postgres question; the SQLite-only
  stance is locked for the current MVP).
- SSO / LDAP / OAuth2.
- Advanced backup policies (incremental, cross-region, encryption-at-rest
  helpers beyond the platform secret store).
- Central fleet management (this is roughly what the pre-pivot
  `mcm-agent` was meant to be; the new direction is HTTP/API-driven,
  not agent-driven — see `docs/adr/0001-record-architecture-decisions.md`
  for the recording convention).
- Operator-approval workflows for ACL changes.

---

## Historical scope (removed by the Docker-first pivot, #226)

These items were part of pre-#226 scope and are **not** current. They are
listed here so a contributor who finds an old reference (an issue, a
blog post, a stale ADR) can map it to the current contract.

- `mcm` CLI subcommands (`server`, `doctor`, `status`, `config
  validate`, `version` as a subcommand, etc.) — gone. The binary now
  takes only `--config` and `--version` (see `cmd/mcm/main.go`).
- `mcm-agent` edge agent process — gone. Replaced by HTTP/API + the
  bundled Docker image.
- In-process broker (Moqui/Mochi-MQTT) — gone. MCM integrates with an
  external Mosquitto broker; it does not embed one.
- Postgres as a first-class target — deferred to "Futuro" above, and
  only if HA/multi-instance demand actually appears. The pre-pivot
  Postgres work is captured in ADR-0008 (marked Superseded).
- CLI-driven backup/restore — replaced by `task backup` /
  `task restore`.
- `goreleaser` (the release-tooling package, not the image HEALTHCHECK path) — gone. The release image is built by
  `.github/workflows/release.yml` using `docker buildx build` + GHCR.

ADRs that document the old scope carry an explicit `Status: Superseded`
header and link forward to the live decision. See `docs/adr/0007` (CLI
subcommands) and `docs/adr/0008` (Postgres-as-default) for two
worked examples.

---

## Roadmap as issues: recommended practice

Using issues for the whole roadmap is not ideal if every issue is very
broad, for example "Build MVP". Those become hard to close and do not
guide implementation.

Recommended structure:

- Keep this `ROADMAP.md` for public strategy and the three-layer
  (funciona / gap / futuro) split.
- Use GitHub Milestones for phases, for example `MVP — Docker-first` and
  `Post-MVP`.
- Use GitHub Issues for actionable tasks that can be completed and
  reviewed.
- Keep each issue focused, with context, scope, and acceptance
  criteria.
- Convert large ideas into epics only when they link to smaller issues.

Good issue example:

```markdown
Title: Surface TLS failure phase in `/readyz` JSON body

Context:
MCM already runs an MQTT probe in the readiness handler, but the JSON
only exposes a generic "broker unavailable" string. Operators need to
see whether the failure was TCP, TLS, or MQTT CONNACK so they can pivot
without grepping logs.

Scope:
- Extend `internal/diagnostics.MQTTResult` with a `Phase` field
  (tcp | tls | mqtt).
- Include the phase in the `/readyz` response body.
- Add a test that asserts the phase is set for each failure mode.

Acceptance criteria:
- `/readyz` includes a `phase` field in the JSON body.
- TCP failure → phase="tcp".
- TLS handshake failure → phase="tls".
- MQTT CONNACK rejection → phase="mqtt".
```

Bad issue example:

```markdown
Title: Build observability
```

Too broad, unclear, and hard to close.