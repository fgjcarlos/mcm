# Mosquitto Control Manager (MCM)

[![CI](https://github.com/fgjcarlos/mcm/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/fgjcarlos/mcm/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](./LICENSE)
[![Go](https://img.shields.io/badge/go-1.24-00ADD8?logo=go&logoColor=white)](./go.mod)
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

MCM is configured through `MCM_*` environment variables. A YAML file can be mounted for non-secret defaults via `MCM_CONFIG_FILE`; env vars override every field the YAML sets.

| Variable                          | Default            | Notes                                                |
| --------------------------------- | ------------------ | ---------------------------------------------------- |
| `MCM_HTTP_BIND_ADDRESS`           | `0.0.0.0`          |                                                      |
| `MCM_HTTP_PORT`                   | `8080`             |                                                      |
| `MCM_DATABASE_PATH`               | `/var/lib/mcm/mcm.db` | Parent dir must be writable so the JWT-secret bootstrap can persist. |
| `MCM_AUTH_JWT_SECRET`             | *(auto-generated)* | 64-hex-char random secret persisted to `<db dir>/.bootstrap.json` (mode 0600). Set explicitly to control across restarts. |
| `MCM_AUTH_TOKEN_TTL`              | `24h`              | Go duration.                                         |
| `MCM_BOOTSTRAP_ADMIN_USERNAME`    | `admin`            | First-boot only. Leave both empty to auto-generate.  |
| `MCM_BOOTSTRAP_ADMIN_PASSWORD`    | *(auto-generated)* | 24-char random password logged once on first boot.   |
| `MCM_MOSQUITTO_HOST`              | `mosquitto`        | The bundled compose service.                         |
| `MCM_MOSQUITTO_PORT`              | `1883`             |                                                      |
| `MCM_MOSQUITTO_USERNAME`          | `admin`            | Dev default matches the Mosquitto bootstrap script.   |
| `MCM_MOSQUITTO_PASSWORD`          | `mcm-dev-broker-password` | Dev default matches the Mosquitto bootstrap script. Dev-only — production reads this from a secret manager. |
| `MCM_MOSQUITTO_DEPLOY_MODE`       | *(unset)*          | `docker` (dev: shared volume + `docker exec kill -HUP 1`), `file` (production: write into a shared dir, broker reloads on its own trigger), or empty (deploy disabled). |
| `MCM_MOSQUITTO_DEPLOY_ACL_PATH`   | *(unset)*          | On-disk path for the rendered ACL file. Dev: `/var/lib/mosquitto-config/acl` (named volume). |
| `MCM_MOSQUITTO_DEPLOY_PASSWD_PATH`| *(unset)*          | On-disk path for the rendered passwd file. Dev: `/var/lib/mosquitto-config/passwd`. |
| `MCM_MOSQUITTO_DEPLOY_CONTAINER_NAME` | *(unset)*      | Required when `MCM_MOSQUITTO_DEPLOY_MODE=docker`. The Mosquitto container to SIGHUP. |
| `MCM_MOSQUITTO_DEPLOY_RELOAD_STRATEGY` | *(unset)*     | `sighup` (the only supported strategy right now).    |
| `MCM_LOG_LEVEL`                   | `info`             | `debug`, `info`, `warn`, `error`.                    |
| `MCM_LOG_FORMAT`                  | `json`             | `json` or `text`.                                    |
| `MCM_CONFIG_FILE`                 | *(unset)*          | Optional YAML file loaded before env overrides.      |

Full field documentation lives at the top of [`internal/config/config.go`](./internal/config/config.go).

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

- **Go 1.24** + stdlib `net/http` + `log/slog` (no third-party HTTP framework).
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