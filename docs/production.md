# Production deployment guide

This guide covers running MCM in production behind a reverse proxy with TLS termination, supplying secrets safely, performing upgrades and backups, scraping Prometheus metrics, and applying a baseline hardening checklist. It assumes the **Docker** path (`task` + `docker compose`).

> MCM is pre-1.0 software. Treat the security model as evolving: pin an
> image tag, watch releases, and keep an upgrade window ready. The project
> status is documented in the top-level [README](../README.md#quickstart-docker).

---

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

Bind `mcm` to a loopback or private interface (`MCM_HTTP_BIND_ADDRESS=127.0.0.1`)
and let the proxy own the public address. Do not expose `:8080` directly
to the internet.

---

## 2. Reverse proxy examples

All three snippets assume MCM listens on `127.0.0.1:8080`. Run the container
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

Generate a strong JWT secret with:

```bash
openssl rand -base64 48
```

Pass the value to the container via your platform's secret store. Do
**not** commit real secrets. The default image is safe to ship; the
production values come from the deployment platform.

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
# with the versioned image, then pull and recreate. Example for v0.1.0:

#   services:
#     mcm:
#       image: ghcr.io/fgjcarlos/mcm:v0.1.0
#       # ... rest unchanged

docker compose pull mcm
docker compose up -d
```

Each release ships multi-arch (`linux/amd64`, `linux/arm64`) with provenance
attestations and an SBOM. Inspect the manifest digest from the GitHub Release
page or with `docker buildx imagetools inspect --raw`.

### Upgrade steps

```bash
# 1. Snapshot the data volume first (see §6).
task backup

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

The `mcm_data` named volume holds the SQLite database plus the
`.bootstrap.json` JWT-secret file. The supported backup and restore recipes
ship with the `Taskfile.yml`:

```bash
# Backup: writes backups/mcm-data.tgz from the mcm_data volume
task backup

# Restore: stops the stack, replaces the volume contents from the tgz,
# brings the stack back up
task restore
```

The backup tgz contains everything stored in the volume: admin users,
broker metrics, audit events, security events, and the auto-generated
JWT secret. It does **not** include Mosquitto's own configuration,
password file, ACL file, TLS material, logs, or any file referenced from
your env-var config — back those up separately according to your
platform's process.

### Recommended schedule

| Asset                              | Frequency             | Tool                                   |
| ---------------------------------- | --------------------- | -------------------------------------- |
| `mcm_data` volume (`task backup`)  | Hourly, retained 24h  | cron / systemd timer + off-host sync   |
| `mcm_data` volume                  | Daily, retained 30d   | cron / systemd timer + off-host sync   |
| Mosquitto password file / ACLs     | On change + daily     | your platform's file backup mechanism  |
| TLS certificates and keys          | On renewal            | your platform's secret store           |
| `docker-compose.yml` and env vars  | On change, in git     | version control                        |

### Restore drill

A backup you have never restored is not a backup. Schedule a quarterly drill:

1. Start a temporary stack with a different volume name:
   `docker compose -p mcm-drill up -d`.
2. Run `task restore` (or copy the tgz into the temporary volume).
3. Hit `GET /api/v1/status` and a few admin endpoints to confirm the
   schema is intact.
4. Tear the temporary stack down and record the result.

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
- [ ] `MCM_HTTP_BIND_ADDRESS` is `127.0.0.1` or a private interface; a
      reverse proxy owns the public address.
- [ ] TLS is terminated at the proxy with HSTS, modern ciphers, and ACME
      renewal; built-in TLS is off at the MCM layer unless required.
- [ ] `MCM_HTTP_TRUSTED_PROXIES` lists the proxy's source address/CIDR.
- [ ] The container runs as a non-root user (the image defaults to `mcm`).
- [ ] `MCM_AUTH_JWT_SECRET`, `MCM_BOOTSTRAP_ADMIN_PASSWORD` live in the
      platform's secret store, mode `0600`; nothing real in version control.
- [ ] Mosquitto runs with its own authentication and ACL; MCM is the
      control plane, not a replacement for broker security.
- [ ] Backups run on the schedule in §6 and the quarterly restore drill
      has been executed at least once.
- [ ] Prometheus is scraping `/metrics`; broker-down, login-failure-spike,
      and p95-latency alerts are wired.
- [ ] `task smoke` runs cleanly after every config or topology change.

---

## See also

- [README](../README.md) — quickstart, configuration, development commands.
- [SECURITY.md](../SECURITY.md) — how to report a vulnerability.
- [`docs/openapi.yaml`](./openapi.yaml) — the HTTP API contract.