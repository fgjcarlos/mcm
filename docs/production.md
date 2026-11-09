# Production deployment guide

This guide covers running MCM in production behind a reverse proxy with TLS termination, supplying secrets safely, performing upgrades and backups, scraping Prometheus metrics, and applying a baseline hardening checklist. It assumes the **Docker** path (`task` + `docker compose`).

> MCM is pre-1.0 software. Treat the security model as evolving: pin an
> image tag, watch releases, and keep an upgrade window ready. The project
> status is documented in the top-level [README](../README.md#project-status).

Every `MCM_*` environment variable mentioned below is enforced by the strict table-driven parser in [`internal/config/env_bindings.go`](../internal/config/env_bindings.go). A typo in a name or a malformed value aborts startup with an actionable error — there are no silent fallbacks to YAML or defaults. The full canonical list (with defaults and notes) lives in the [Configuration section of the README](../README.md#configuration).

---

## Current limitations

MCM is alpha. The graphical configuration and recovery workflow is implemented and verified against a real Mosquitto; see [product scope](product-scope.md), the [support matrix](support-matrix.md) and [epic #308](https://github.com/fgjcarlos/mcm/issues/308).

- The broker configuration is written through a reviewed revision, so an apply is bound to the exact preview that was approved. A change that the broker only reads at start-up (a listener, a bridge, a plugin) is reported as `pending_activation` until the restart is confirmed, not as active.
- The activation path is operator-provided. MCM writes the files and then runs the reload or restart command you configure; it does not bundle a broker-side trigger. Without `MCM_MOSQUITTO_DEPLOY_RELOAD_COMMAND` and `MCM_MOSQUITTO_DEPLOY_RESTART_COMMAND`, a change that needs a restart stays `pending_activation` instead of being reported active. See [section 3](#3-secrets-management).
- File mode requires compatible filesystem ownership. The atomic replacement needs MCM to own the target directory, and the broker must be able to read the result.
- Five directives per supported version are validated and preserved but have no dedicated graphical control, so the operator edits them in the advanced root editor. The [support matrix](support-matrix.md) lists them per version.
- The included files of an `include_dir` tree are resolved and inspected on import, but they are not editable through the UI.
- Read [section 6](#6-backup-and-restore) before upgrading or restoring: the backup is a script you run, and it captures the MCM database, the broker configuration and its certificate material. The broker's own persistence data is opt-in.
- SQLite is the only implemented database backend; PostgreSQL and multiple-writer HA are not available.


## 1. Deployment shape

The recommended production shape is:

```
┌──────────┐    TLS     ┌────────────────┐    plain    ┌──────────────┐
│ Browsers │  ────────▶ │ Reverse proxy  │  ────────▶  │   mcm (docker)│ ──▶ Mosquitto
│ / API    │   :443     │  (Caddy/nginx  │   :8080     │   :8080      │     :1883 / :8883
│ clients  │            │   / Traefik)   │             │   (loopback) │     (plain / TLS)
└──────────┘            └────────────────┘             └──────────────┘
```

Why a proxy in front of `mcm`:

- **TLS termination** with HSTS, modern ciphers, and ACME automation that the
  Go `net/http` server does not provide.
- **Single port for HTTP and WebSocket traffic** (the broker events stream on
  `/api/v1/broker/events` is a WebSocket; proxies must forward `Upgrade`).
- **`trusted_proxies` integration** so the rate-limit lockout and audit logs
  see the real client IP, not the proxy's.

For a container, keep MCM listening on `0.0.0.0:8080` inside its network namespace and restrict the published host address as shown below. A bare-metal process can bind to loopback directly. Let the reverse proxy own the public address.

---

## 2. Reverse proxy examples

All three snippets assume the proxy reaches MCM at the host address `127.0.0.1:8080`. Run the container
with `-p 127.0.0.1:8080:8080` (or compose `ports: ["127.0.0.1:8080:8080"]`)
so it is not reachable on the host's external interfaces.

### Caddy (simplest, ACME built in)

```caddyfile
mcm.example.com {
    encode zstd gzip
    reverse_proxy 127.0.0.1:8080 {
        # Forward the real client IP; MCM honors X-Forwarded-For
        # only when the peer is in `http.trusted_proxies`.
        header_up X-Forwarded-For {remote_host}
        header_up X-Forwarded-Proto {scheme}
    }
}
```

`trusted_proxies` must list the proxy's source address. Set
`MCM_HTTP_TRUSTED_PROXIES=127.0.0.1/32` for a same-host proxy, or the
remote proxy's CIDR for a remote one. (If the env var name for your build
differs, check [`internal/config/config.go`](../internal/config/config.go).)

### nginx

```nginx
server {
    listen 443 ssl http2;
    server_name mcm.example.com;

    ssl_certificate     /etc/letsencrypt/live/mcm.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/mcm.example.com/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;
    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;

    # WebSocket upgrade map (broker events stream).
    map $http_upgrade $connection_upgrade {
        default upgrade;
        ''      close;
    }

    location / {
        proxy_pass         http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Real-IP         $remote_addr;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
        proxy_set_header   Upgrade           $http_upgrade;
        proxy_set_header   Connection        $connection_upgrade;
        proxy_read_timeout 86400s;   # long-lived WebSocket
    }
}
```

### Traefik (Docker labels)

```yaml
labels:
  - "traefik.enable=true"
  - "traefik.http.routers.mcm.rule=Host(`mcm.example.com`)"
  - "traefik.http.routers.mcm.tls=true"
  - "traefik.http.routers.mcm.tls.certresolver=letsencrypt"
  - "traefik.http.services.mcm.loadbalancer.server.port=8080"
  - "traefik.http.middlewares.mcm-headers.headers.customrequestheaders.X-Forwarded-For={{.RemoteAddr}}"
```

If you terminate TLS at the proxy, leave MCM's built-in HTTPS off (default).
For air-gapped edge installs where a proxy is overkill, you can terminate
TLS directly at the MCM container by mounting certificates and setting the
corresponding `MCM_HTTP_TLS_*` env vars; see
[`internal/config/config.go`](../internal/config/config.go) for the field
list.

---

## 3. Secrets management

Configure MCM through `MCM_*` environment variables. The supported pattern
is to commit a `docker-compose.override.yml` (gitignored) with the real
secrets, or supply them via your orchestrator's secret store.

Two values must be set explicitly so the server can boot:

- `MCM_AUTH_JWT_SECRET` — at least 32 random characters. If unset, the
  server generates one and persists it to
  `<MCM_DATABASE_PATH's dir>/.bootstrap.json` (mode 0600).
- `MCM_BOOTSTRAP_ADMIN_USERNAME` and `MCM_BOOTSTRAP_ADMIN_PASSWORD` — the
  first-boot admin. If both are unset, the server creates `admin` with a
  random 24-char password and logs it once.
- `MCM_AUTH_TOKEN_TTL` — JWT lifetime, Go duration (default `24h`).

Generate a strong JWT secret with:

```bash
openssl rand -base64 48
```

Pass the value to the container via your platform's secret store. Do
**not** commit real secrets. The default image is safe to ship; the
production values come from the deployment platform.

### Production env-var reference

Every variable below is enforced by the strict parser (issue #279): a
typo in a name aborts startup with `unknown env var ...`; a malformed
value aborts with `invalid env var ...`. The YAML file is loaded first,
then the env vars override it field-by-field.

#### Listener

- `MCM_HTTP_BIND_ADDRESS` (default `0.0.0.0`) — bind to loopback
  (`127.0.0.1`) so the proxy owns the public address.
- `MCM_HTTP_PORT` (default `8080`) — exposed on the loopback interface.
- `MCM_HTTP_TRUSTED_PROXIES` — comma-separated IP/CIDR list of proxies
  whose `X-Forwarded-For` / `X-Real-IP` headers MCM honors. Empty
  (default) trusts no proxy and the direct peer is always used as the
  client IP.
- `MCM_HTTP_CORS_ALLOWED_ORIGINS` — comma-separated exact origins
  permitted to make cross-origin requests. Empty = same-origin only.

#### TLS (only when terminating TLS at MCM directly)

- `MCM_HTTP_TLS_ENABLED` (default `false`) — leave off when terminating
  TLS at the proxy.
- `MCM_HTTP_TLS_CERT_FILE` — PEM-encoded server certificate path.
- `MCM_HTTP_TLS_KEY_FILE` — PEM-encoded server private key path.
- `MCM_HTTP_TLS_MIN_VERSION` (default `1.2`) — `"1.2"` or `"1.3"`.
- `MCM_HTTP_TLS_CLIENT_CA_FILE` — CA bundle for client cert verification.
- `MCM_HTTP_TLS_REQUIRE_CLIENT_CERT` (default `false`) — enable for mTLS.

#### Database

- `MCM_DATABASE_BACKEND` (default `sqlite`) — only `"sqlite"` is implemented. Selecting `"postgres"` aborts startup.
- `MCM_DATABASE_PATH` (default `/var/lib/mcm/mcm.db`) — SQLite path; parent
  dir must be writable.
- `MCM_DATABASE_DSN` — reserved for an unimplemented backend; not usable in this release.

#### Auth

- `MCM_AUTH_JWT_SECRET` — see [§3 Secrets management](#3-secrets-management).
- `MCM_AUTH_TOKEN_TTL` (default `24h`) — JWT lifetime.
- `MCM_AUTH_LOGIN_LOCKOUT_WINDOW` (default `15m`) — sliding window for
  failed-login counting.
- `MCM_AUTH_LOGIN_LOCKOUT_MAX_ATTEMPTS` (default `6`) — lockout threshold.
- `MCM_AUTH_LOGIN_LOCKOUT_COOLDOWN` (default `15m`) — how long the source
  remains blocked after the window expires.
- `MCM_BOOTSTRAP_ADMIN_USERNAME`, `MCM_BOOTSTRAP_ADMIN_PASSWORD` — see
  [§3 Secrets management](#3-secrets-management).

#### Mosquitto broker connection

- `MCM_MOSQUITTO_HOST` (default `mosquitto`) — broker hostname.
- `MCM_MOSQUITTO_PORT` (default `1883`, use `8883` for TLS) — broker port.
- `MCM_MOSQUITTO_USERNAME` / `MCM_MOSQUITTO_PASSWORD` — broker service user
  (production reads from a secret manager).
- `MCM_MOSQUITTO_TLS_ENABLED` (default `false`) — connect over TLS.
- `MCM_MOSQUITTO_TLS_CA_CERT_FILE`, `MCM_MOSQUITTO_TLS_CLIENT_CERT_FILE`,
  `MCM_MOSQUITTO_TLS_CLIENT_KEY_FILE` — required when TLS is enabled.
- `MCM_MOSQUITTO_TLS_INSECURE_SKIP_VERIFY` (default `false`) — never
  enable in production.
- `MCM_MOSQUITTO_CONFIG_DIR`, `MCM_MOSQUITTO_DATA_DIR` — broker
  configuration and persistent data directories.
- `MCM_MOSQUITTO_BROKER_CONFIG_DIR` (default `/mosquitto/config`) —
  broker-side path of the configuration directory that holds the broker
  certificate material. It must point at the same volume as
  `MCM_MOSQUITTO_CONFIG_DIR` under a different mount. The certificate
  surface stays disabled while `MCM_MOSQUITTO_CONFIG_DIR` is unset; see
  [the broker TLS guide](../deploy/mosquitto/README.md#managing-the-broker-tls-material-from-mcm).

#### Mosquitto deploy (write passwd/acl + reload)

- `MCM_MOSQUITTO_DEPLOY_MODE` — `""` (disabled), `"file"` (production),
  or `"docker"` (dev compose).
- `MCM_MOSQUITTO_DEPLOY_ACL_PATH` — on-disk ACL file path.
- `MCM_MOSQUITTO_DEPLOY_PASSWD_PATH` — on-disk passwd file path.
- `MCM_MOSQUITTO_DEPLOY_CONTAINER_NAME` — required when mode is `"docker"`.
- `MCM_MOSQUITTO_DEPLOY_RELOAD_STRATEGY` (default `""`) — `"sighup"`.
- `MCM_MOSQUITTO_DEPLOY_RELOAD_COMMAND` (default unset) — whitespace-
  separated command + argv that MCM invokes after a successful deploy
  write. Takes precedence over `reload_strategy=sighup` and `pid_path`
  when set. Example: `"systemctl reload mosquitto.service"`. Required for
  production deployments that do NOT mount the Docker socket into MCM
  (issue #294).
- `MCM_MOSQUITTO_DEPLOY_HEALTHCHECK_TIMEOUT` (default `5s`) — broker
  healthcheck wait after reload.

##### Production reload command (issue #294)

Production deployments must NOT mount `/var/run/docker.sock` into the
MCM container — a process with socket access is effectively root on
the host, and the broker reload is trivial to perform out-of-band.
The `MCM_MOSQUITTO_DEPLOY_RELOAD_COMMAND` setting lets MCM hand the
reload off to a sidecar without escalating privileges:

- **systemd**: `"systemctl reload mosquitto.service"` — runs on the
  broker host (typically via SSH). The MCM container runs with the
  `CAP_KILL` capability (or as a user with write access to the broker's
  PID file).
- **Podman / k8s / ECS sidecar**: a small helper binary that signals
  the broker process via a unix socket or HTTP endpoint exposed by
  the broker sidecar. Pass the helper's argv verbatim.
- **Custom script**: any executable + argv that exits 0 on success.
  The command is invoked via `exec.Command(name, args...)` — there is
  no shell, so paths with spaces or shell metacharacters are safe.

The dev Compose stack continues to use the legacy `docker exec kill -HUP 1`
path (which requires `/var/run/docker.sock` mounted into MCM) — this is
the only deploy mode that still benefits from the legacy `pid_path` +
`signal_func` plumbing. Production deployments should set
`MCM_MOSQUITTO_DEPLOY_MODE=file` and `MCM_MOSQUITTO_DEPLOY_RELOAD_COMMAND`
exclusively.

##### Restarting the broker (issue #294)

A RELOAD is not a RESTART. Mosquitto reads a bridge, a plugin or the
dynamic security plugin only at start-up, so a change to those surfaces
needs a broker restart to become active. `MCM_MOSQUITTO_DEPLOY_RESTART_COMMAND`
(default unset) is the controlled restart mechanism, separate from the
reload command on purpose:

- When it is set (e.g. `"systemctl restart mosquitto.service"`, or an
  SSH hop / sidecar that bounces the broker), a change that needs a
  restart is applied and the broker is restarted through that command.
- When it is unset, MCM cannot restart the broker: a change that needs
  a restart is refused before anything is written (or lands in
  `pending_activation` where the surface cannot be verified), instead of
  being reported active after a mere reload.

The example production stack in `docker-compose.prod.yml` does NOT set
a restart command — restarting a Compose service from inside a sibling
container would need the Docker socket, which this deployment model
forbids. Set the restart command when the broker runs under systemd,
Kubernetes or another supervisor that can bounce it out-of-band.

##### Production path alignment (issue #294)

Both `mosquitto.conf` and `mosquitto.prod.conf` reference the same
file paths (`/mosquitto/config/passwd` + `/mosquitto/config/acl`) so
the MCM render output lands where the broker expects it. Operators
migrating from the legacy `passwords` (with `s`) path must rename the
file before deploying — the MCM startup capability check will refuse
to boot if the configured paths are not writable.

The MCM process MUST have write access to the parent directory of
both `ACLPath` and `PasswdPath`. The atomic-rename applier writes a
temp file in the parent and `os.Rename`s it into place — if the parent
is not writable, the apply fails. To satisfy this in production:

- Run MCM and Mosquitto as the same UID (recommended), or in the same
  supplementary group with write access to the shared config dir.
- Set the shared config dir's group to that group and apply the
  setgid bit (`chmod g+s`); MCM's atomic-rename preserves the group.
- Do NOT use `chmod 777` in production — it is a dev shortcut that
  the bootstrap script applies only for the local dev stack.


#### Retention

- `MCM_METRICS_BROKER_RETENTION` (default `168h`, 7d).
- `MCM_METRICS_AUDIT_RETENTION` (default `2160h`, 90d).
- `MCM_METRICS_SECURITY_RETENTION` (default `2160h`, 90d).

#### Alerting

- `MCM_ALERTING_ENABLED` (default `false`).
- `MCM_ALERTING_ENDPOINT_URL` — webhook URL (required when enabled).
- `MCM_ALERTING_TIMEOUT` (default `5s`) — per-POST timeout.
- `MCM_ALERTING_SIGNING_SECRET` — HMAC-SHA256 secret for the
  `X-MCM-Signature` header.
- `MCM_ALERTING_COOLDOWN` (default `5m`) — minimum interval between
  repeated alerts of the same class.

#### Logging

- `MCM_LOG_LEVEL` (default `info`) — `debug`, `info`, `warn`, `error`.
- `MCM_LOG_FORMAT` (default `json`) — `json` (recommended for SIEM) or
  `text`.

---

## 4. Operating the stack

The supported daily workflow uses the [`Taskfile.yml`](../Taskfile.yml):

```bash
task up                  # start mcm + mosquitto
task logs                # tail logs; bootstrap admin prints here on first boot
task ps                  # confirm both services healthy
task smoke               # curl /healthz, /readyz, /api/v1/status
task ready               # block until /healthz returns 200
task down                # stop the stack
```

For liveness, point your orchestrator at the container's
`GET /healthz`. For readiness, use `GET /readyz`. For human / dashboard
status, use `GET /api/v1/status` (do **not** treat it as a liveness probe —
broker disconnects are reported here without implying the MCM process
should be restarted).

---

## 5. Upgrades

MCM tracks its schema with an internal `schema_migrations` table. The server
applies pending migrations on startup, so the upgrade is a stop → replace
image → start cycle.

### Pulling a tagged release

Published images live at `ghcr.io/fgjcarlos/mcm`. Pin a specific tag in
production — the `latest` tag moves on every release and is convenient for
home labs, not for production stability:

```bash
# Replace the mcm service's `build:` and `image:` lines in docker-compose.yml
# with the versioned image, then pull and recreate. Example for tag v0.1.0:

#   services:
#     mcm:
#       image: ghcr.io/fgjcarlos/mcm:0.1.0  # release.yml strips the leading v from GHCR tag
#       # ... rest unchanged

docker compose pull mcm
docker compose up -d
```

Each release ships multi-arch (`linux/amd64`, `linux/arm64`) with provenance
attestations and an SBOM. Inspect the manifest digest from the GitHub Release
page or with `docker buildx imagetools inspect --raw`.

### Upgrade steps

```bash
# First obtain and verify a consistent backup using the process in section 6.
# Verify the restore path on a disposable deployment before you need it.

# 2. Pull the new image and recreate the container.
docker compose pull mcm
docker compose up -d

# 3. Watch the logs until migrations complete.
task logs
```

A new minor version is **backwards-compatible** at the SQLite level:
existing rows are preserved, and migrations add columns or tables
additively. **Major versions may require the documented upgrade path in
the release notes**; check the release notes before upgrading across major
boundaries.

Roll back the same way: stop the stack, point at the previous image tag,
restore the volume snapshot if needed.

---

## 6. Backup and restore

The MCM deploy service exposes two scripts — `scripts/backup.sh` and
`scripts/restore.sh` — that capture a consistent, restartable
recovery set covering the MCM database, the Mosquitto configuration,
and the broker certificates. They were rewritten in #295 to fix
three defects the legacy Taskfile recipes had:

- The legacy archive put files at `data/...` and extracted to `/data`,
  producing `/data/data/...` rather than restoring files to their
  original volume paths. The new scripts use relative paths in the
  helper containers so each file lands at the right volume path.
- The legacy recipes hardcoded `mcm_mcm_data`; the new scripts
  resolve volumes via `docker compose --project-name volume ls`, so
  any `COMPOSE_PROJECT_NAME` works.
- The legacy backup copied a live SQLite database without a consistent
  snapshot. The new backup uses `sqlite3 .backup` for a
  WAL-inclusive snapshot — no server downtime, no torn writes.

### 6.1 What the backup captures

By default (without `--include-broker-data`):

- `mcm_data` volume:
  - `mcm.db`, `mcm.db-shm`, `mcm.db-wal` — the SQLite database + WAL.
  - `.bootstrap.json` — the JWT secret.
- `mosquitto_config` volume:
  - `passwd` — broker password file.
  - `acl` — broker ACL file.
  - `mosquitto.conf` — broker configuration.
  - `certs/` — broker certificates (when present).

`mosquitto_data` and `mosquitto_logs` are skipped. Use
`--include-broker-data` to also capture the broker's persistent
message store (often larger; the operator may prefer a separate
retention strategy).

### 6.2 Running a backup

```sh
task backup                 # writes backups/mcm-data.tgz
# or:
bash scripts/backup.sh /path/to/backup-$(date +%Y%m%d).tgz
```

`scripts/backup.sh` accepts these flags:

- `--include-broker-data`: also backup the `mosquitto_data` volume.
- `--skip-mosquitto`: skip the broker config (MCM-only backups).
- `--operator NAME`: tag the manifest with an operator name.
- `--alpine-image IMAGE`: override the alpine image used by helpers
  (default `alpine:3.21`).

The output is a single `.tgz` containing the captured files plus a
`manifest.json` with sha256 of every payload file (including optional
certificates and broker persistence). The archive is created with mode
`0600` and private member permissions because it contains the MCM JWT
bootstrap secret, MQTT password hashes and may include TLS private keys.
The archive is **not encrypted**: store it in an access-controlled,
encrypted backup location and do not attach it to issues or logs. Restore
validates the sha256 against the staging tree and refuses to proceed on
mismatch.

### 6.3 Running a restore

```sh
# Stop the MCM service so it doesn't write to the volume mid-restore.
task down

# Into an EMPTY deployment (fresh bootstrap or wiped volumes):
bash scripts/restore.sh --confirm backups/mcm-data.tgz

# Over an existing deployment with data (replaces its state):
bash scripts/restore.sh --confirm --overwrite backups/mcm-data.tgz
# Equivalent Taskfile recipe:
task restore RESTORE_FLAGS=--overwrite

# Restart the MCM service AND the broker — the broker must re-read
# the restored passwd/acl files; MCM's in-memory verifier cleartext
# store (#293) is empty after a container restart, so we restart both
# services rather than relying on a deploy apply.
docker compose up -d
docker compose restart mosquitto
```

`scripts/restore.sh --confirm` requires explicit confirmation, and
additionally requires `--overwrite` when the destination volumes
already contain data (an existing `mcm.db`, `passwd` or `acl`): a
restore replaces them, and the script refuses to destroy existing
data on a mere `--confirm`. Restoring over a freshly bootstrapped
empty stack is the intended `--overwrite` case. The script:

1. Extracts the archive to a private staging directory.
2. Re-reads `manifest.json` and verifies every file's sha256 — every
   captured file is enumerated, including certificates and broker
   persistence when `--include-broker-data` was used.
3. Resolves the volumes via `docker compose --project-name volume ls`.
4. Writes each file to its original volume path using a helper
   container that sets the correct UID/GID (MCM runs as UID 100 in
   the alpine-based image; the broker as UID 1883).
5. Preserves file modes (`0600` for sensitive files like mcm.db and
   .bootstrap.json; `0644` for the broker config).
6. VERIFIES the restored database content: SQLite `integrity_check`
   plus a non-empty schema read-back. Stale `mcm.db-wal`/`mcm.db-shm`
   sidecars from older archives are ignored — the `sqlite3 .backup`
   snapshot is self-contained.

### 6.4 Recovery validation

Run the recovery drill in an isolated environment with explicitly
selected volumes (`task down` first; do not operate on a live
deployment). The `scripts/e2e-recovery.sh` test in CI runs the full
flow end-to-end:

1. Apply a known-good config (creates an MQTT user + ACL).
2. Run `scripts/backup.sh` to capture the state.
3. `docker compose down -v` (wipe EVERYTHING).
4. `docker compose up -d` (re-up empty; MCM bootstraps a new admin).
5. Run `scripts/restore.sh --confirm --overwrite` to replace the
   freshly-bootstrapped files with the captured ones.
6. Restart mosquitto (so it reads the restored passwd/acl).
7. Verify: the original admin password works (proves .bootstrap.json
   + SQLite round-tripped), the captured user still exists, and the
   broker accepts the user's original cleartext password on the
   granted topic.

A failed drill exposes missing files, wrong perms, or volume
resolution bugs that would otherwise surface only in a real outage.
Re-run after every release that touches `internal/storage`,
`internal/server`, or `scripts/`.

### 6.5 Operating notes

- The archive contains secrets (`.bootstrap.json` is the JWT signing
  key, `passwd` is the broker password file). Encrypt at rest
  (LUKS, cloud KMS, age, etc.) and never commit a backup archive to
  version control.
- A backup is only useful if the verifier can re-arm after a
  restore. The deploy verifier (#293) holds cleartext passwords in
  memory — after a container restart this map is empty. Operators
  who resume deploys after a restore need to either re-create users
  via the API or wait for the next password rotation. The
  `scripts/e2e-recovery.sh` test accounts for this and does NOT run
  a deploy apply after the restore; it restarts mosquitto to pick
  up the restored passwd/acl directly.
- Backup frequency and retention are operational choices outside
  the scope of these scripts. Schedule a daily backup with a 30-day
  retention as a starting point; tune to your recovery objectives.

---

## 7. Monitoring

MCM exposes Prometheus metrics on `/metrics` and operational status on
`/api/v1/status`. The full metric inventory and scrape config are in the
top-level [README](../README.md#reference); a starter Grafana dashboard lives at
[`deploy/grafana/mcm-dashboard.json`](../deploy/grafana/mcm-dashboard.json).

Minimal scrape config:

```yaml
scrape_configs:
  - job_name: mcm
    metrics_path: /metrics
    scheme: http   # use https when terminating TLS at MCM directly
    static_configs:
      - targets: ["mcm.internal:8080"]
```

Recommended alerts:

- `mcm_broker_status == 0` for more than 2 minutes.
- `rate(mcm_login_attempts_total{result="failure"}[5m]) > 5` sustained
  (possible credential stuffing).
- `mcm_http_request_duration_seconds:rate5m` p95 above your SLO.
- `mcm_up == 0` (add a `probe` job that hits `/api/v1/status` from outside
  the proxy to catch proxy outages).

---

## 8. Hardening checklist

- [ ] `MCM_AUTH_JWT_SECRET` is at least 32 random characters, rotated on
      suspected compromise.
- [ ] `MCM_BOOTSTRAP_ADMIN_PASSWORD` is set to a unique value, the default
      account is renamed or disabled after first login.
- [ ] `MCM_AUTH_TOKEN_TTL` is pinned to your session policy (default
      `24h`).
- [ ] `MCM_AUTH_LOGIN_LOCKOUT_WINDOW`, `MCM_AUTH_LOGIN_LOCKOUT_MAX_ATTEMPTS`,
      and `MCM_AUTH_LOGIN_LOCKOUT_COOLDOWN` are tuned to your threat model
      (defaults `15m` / `6` / `15m`).
- [ ] `MCM_HTTP_BIND_ADDRESS` is `127.0.0.1` or a private interface; a
      reverse proxy owns the public address.
- [ ] TLS is terminated at the proxy with HSTS, modern ciphers, and ACME
      renewal; built-in TLS is off at the MCM layer unless required
      (`MCM_HTTP_TLS_ENABLED=false`).
- [ ] If MCM terminates TLS directly: `MCM_HTTP_TLS_ENABLED=true`,
      `MCM_HTTP_TLS_CERT_FILE`, `MCM_HTTP_TLS_KEY_FILE`,
      `MCM_HTTP_TLS_MIN_VERSION=1.3`, and (for mTLS)
      `MCM_HTTP_TLS_CLIENT_CA_FILE` + `MCM_HTTP_TLS_REQUIRE_CLIENT_CERT=true`.
- [ ] `MCM_HTTP_TRUSTED_PROXIES` lists the proxy's source address/CIDR so
      the rate-limit lockout and audit logs see the real client IP.
- [ ] `MCM_DATABASE_BACKEND=sqlite` with one MCM writer; PostgreSQL/HA are not implemented.
- [ ] `MCM_MOSQUITTO_TLS_ENABLED=true` with the CA / client cert / key
      paths for broker mTLS on `MCM_MOSQUITTO_PORT=8883`.
- [ ] `MCM_MOSQUITTO_TLS_INSECURE_SKIP_VERIFY` is `false` (never enable
      in production).
- [ ] `MCM_MOSQUITTO_DEPLOY_MODE=file` with verified signaling (or empty to disable deployment) and `MCM_MOSQUITTO_DEPLOY_ACL_PATH` /
      `MCM_MOSQUITTO_DEPLOY_PASSWD_PATH` are mounted from a writable
      shared volume.
- [ ] `MCM_METRICS_BROKER_RETENTION`, `MCM_METRICS_AUDIT_RETENTION`, and
      `MCM_METRICS_SECURITY_RETENTION` are sized to your retention
      obligations (defaults `168h` / `2160h` / `2160h`).
- [ ] `MCM_ALERTING_ENABLED=true` with `MCM_ALERTING_ENDPOINT_URL` and
      `MCM_ALERTING_SIGNING_SECRET` set when an on-call channel exists;
      otherwise leave `MCM_ALERTING_ENABLED=false`.
- [ ] The container runs as a non-root user (the image defaults to `mcm`).
- [ ] `MCM_AUTH_JWT_SECRET`, `MCM_BOOTSTRAP_ADMIN_PASSWORD`,
      `MCM_MOSQUITTO_PASSWORD`, `MCM_ALERTING_SIGNING_SECRET` live in the
      platform's secret store, mode `0600`; nothing real in version
      control.
- [ ] Mosquitto runs with its own authentication and ACL; MCM is the
      control plane, not a replacement for broker security.
- [ ] An operator-managed consistent backup process exists and an isolated restore drill
      has been executed at least once.
- [ ] Prometheus is scraping `/metrics`; broker-down, login-failure-spike,
      and p95-latency alerts are wired.
- [ ] `task smoke` runs cleanly after every config or topology change.

---

## See also

- [README](../README.md) — quickstart, configuration, development commands.
- [SECURITY.md](../SECURITY.md) — how to report a vulnerability.
- [`docs/openapi.yaml`](./openapi.yaml) — the HTTP API contract.