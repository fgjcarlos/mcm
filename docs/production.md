# Production deployment guide

This guide covers running MCM in production: putting it behind a reverse proxy
with TLS termination, supplying secrets safely, performing upgrades and
backups, scraping Prometheus metrics, and applying a baseline hardening
checklist. It assumes the **standalone binary** path; the Docker flow lives
in [`deploy/mcm/README.md`](../deploy/mcm/README.md).

> MCM is pre-1.0 software. Treat the security model as evolving: pin a
> version, watch releases, and keep an upgrade window ready. The project
> status is documented in the top-level [README](../README.md#project-status).

---

## 1. Deployment shape

The recommended production shape is:

```
┌──────────┐    TLS     ┌────────────────┐    plain    ┌──────────────┐
│ Browsers │  ────────▶ │ Reverse proxy  │  ────────▶  │  mcm server  │  ──▶ Mosquitto
│ / API    │   :443     │  (Caddy/nginx  │   :8080     │  :8080       │      :1883 / :8883
│ clients  │            │   / Traefik)   │             │  (loopback)  │      (plain / TLS)
└──────────┘            └────────────────┘             └──────────────┘
```

Why a proxy in front of `mcm server`:

- **TLS termination** with HSTS, modern ciphers, and ACME automation that the
  Go `net/http` server does not provide.
- **Single port for HTTP and WebSocket traffic** (the broker events stream on
  `/api/v1/broker/events` is a WebSocket; proxies must forward `Upgrade`).
- **`trusted_proxies` integration** so the rate-limit lockout and audit logs
  see the real client IP, not the proxy's.

Bind `mcm` to a loopback or private interface (`http.bind_address:
127.0.0.1` in [`config.yaml`](../deploy/mcm/config.yaml)) and let the proxy
own the public address. Do not expose `:8080` directly to the internet.

---

## 2. Reverse proxy examples

All three snippets assume:

- MCM listens on `127.0.0.1:8080`.
- TLS is terminated at the proxy.
- The proxy is the only thing reachable on `:443`.

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

`trusted_proxies` must list the proxy's source address (for Caddy on the
same host, `127.0.0.1` is implicit when `bind_address` is loopback; for a
remote proxy, add its CIDR).

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

If you terminate TLS at the proxy, you can leave MCM's built-in
`http.tls.enabled` set to `false`. The HTTPS guidance in the
[README](../README.md#https-and-optional-mtls) still applies if you
prefer MCM to serve TLS directly (common in air-gapped edge installs where
a proxy is overkill).

---

## 3. Secrets management

`config.yaml` ships with **placeholder values that fail validation** (see the
header comment in [`deploy/mcm/config.yaml`](../deploy/mcm/config.yaml)).
Three fields must be replaced before `mcm server` will start:

- `auth.jwt_secret` — at least 32 random characters.
- `auth.bootstrap_admin.username`
- `auth.bootstrap_admin.password` — change after first login.

The server honors three environment overrides that take precedence over the
YAML values at startup, which lets you keep the YAML in version control with
placeholders and inject real secrets from a `.env` file, Docker secret, or
orchestrator secret store:

| Environment variable             | YAML field                          |
| --------------------------------- | ----------------------------------- |
| `MCM_AUTH_JWT_SECRET`             | `auth.jwt_secret`                   |
| `MCM_BOOTSTRAP_ADMIN_USERNAME`    | `auth.bootstrap_admin.username`     |
| `MCM_BOOTSTRAP_ADMIN_PASSWORD`    | `auth.bootstrap_admin.password`     |

Generate a strong JWT secret with:

```bash
openssl rand -base64 48
```

Validate the resulting configuration before restarting:

```bash
mcm config validate --config /etc/mcm/config.yaml
```

Do **not** commit real secrets. The placeholder YAML is safe to commit; the
production values come from the deployment platform's secret store.

---

## 4. Running `mcm` as a service

MCM does not ship a systemd unit. A minimal one is shown below — adapt the
user, paths, and `EnvironmentFile=` to your platform. Place it at
`/etc/systemd/system/mcm.service` and `systemctl daemon-reload`.

```ini
[Unit]
Description=Mosquitto Control Manager
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=mcm
Group=mcm
EnvironmentFile=/etc/mcm/mcm.env
ExecStart=/usr/local/bin/mcm server --config /etc/mcm/config.yaml
Restart=on-failure
RestartSec=5s

# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/mcm
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

`/etc/mcm/mcm.env` carries the three `MCM_*` overrides from §3 with mode
`0600`, owned by `mcm:mcm`.

Operational commands:

```bash
systemctl status mcm           # health and recent logs
journalctl -u mcm -f           # follow logs
mcm doctor --config /etc/mcm/config.yaml   # run diagnostics
mcm status --config /etc/mcm/config.yaml   # check broker reachability
```

`mcm doctor` validates config, database, TLS, and broker connectivity; run
it after any config change and as part of incident triage.

---

## 5. Upgrades

MCM tracks its schema with an internal `schema_migrations` table. The server
applies pending migrations on startup, so the upgrade is a stop → replace
binary → start cycle:

```bash
# 1. Snapshot the database first (see §6).
mcm backup create --config /etc/mcm/config.yaml --output /var/backups/mcm/pre-upgrade.db

# 2. Stop the service.
sudo systemctl stop mcm

# 3. Replace the binary.
sudo install -m 0755 ./mcm /usr/local/bin/mcm

# 4. Start; the server applies migrations and reopens the listener.
sudo systemctl start mcm
sudo journalctl -u mcm -f   # watch for "migrations applied" or errors
```

A new minor version is **backwards-compatible** at the SQLite level:
existing rows are preserved, and migrations add columns or tables
additively. **Major versions may require the documented upgrade path in
[docs/releasing.md](./releasing.md)**; check the release notes before
upgrading across major boundaries.

Roll back the same way: stop, restore the binary, start. The database
migrations are forward-only; rolling back the binary on a database that has
already been migrated to a newer schema is **not supported** — restore the
pre-upgrade backup instead.

---

## 6. Backup and restore

The SQLite database is the only stateful component. `mcm backup create`
takes a consistent snapshot, and `mcm backup restore` writes it back:

```bash
# Backup
mcm backup create \
    --config /etc/mcm/config.yaml \
    --output /var/backups/mcm/mcm-$(date +%Y%m%d-%H%M%S).db

# Restore (overwrites an existing database; --force is required)
sudo systemctl stop mcm
mcm backup restore \
    --config /etc/mcm/config.yaml \
    --input /var/backups/mcm/mcm-20260615-120000.db \
    --force
sudo systemctl start mcm
```

The backup artifact contains everything stored in SQLite: admin users,
broker metrics, audit events, security events. It does **not** include
Mosquitto's own configuration, password file, ACL file, TLS material, logs,
or any other file referenced from `config.yaml` — back those up separately
according to your platform's process.

### Recommended schedule

| Asset                           | Frequency             | Tool                                   |
| ------------------------------- | --------------------- | -------------------------------------- |
| MCM SQLite (`mcm backup create`)| Hourly, retained 24h  | cron / systemd timer + off-host sync   |
| MCM SQLite                      | Daily, retained 30d   | cron / systemd timer + off-host sync   |
| Mosquitto password file / ACLs  | On change + daily     | your platform's file backup mechanism  |
| TLS certificates and keys       | On renewal            | your platform's secret store           |
| `config.yaml` and overrides     | On change, in git     | version control                        |

### Restore drill

A backup you have never restored is not a backup. Schedule a quarterly drill:

1. Restore the most recent artifact to a **separate** database path.
2. Start a second `mcm server` against the restored database on a
   non-default port (use `--config` with a temporary `config.yaml`).
3. Hit `GET /api/v1/status` and a few admin endpoints to confirm the
   schema is intact.
4. Tear the temporary instance down and record the result.

---

## 7. Monitoring

MCM exposes Prometheus metrics on `/metrics` and operational status on
`/api/v1/status`. The full metric inventory and scrape config are in the
top-level [README](../README.md#health-readiness-and-observability); a starter
Grafana dashboard lives at
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

For liveness/readiness probes, prefer a process-level check (e.g. systemd
`Type=notify` once MCM adds it, or simply `mcm status --config ...` from
the node) over hitting `/api/v1/status`: that endpoint intentionally
reports broker disconnects without implying the MCM process should be
restarted, so it is not a liveness signal.

---

## 8. Hardening checklist

Cross-linked from the production readiness milestone in
[ROADMAP.md](../ROADMAP.md).

- [ ] `auth.jwt_secret` is at least 32 random characters, rotated on
      suspected compromise.
- [ ] `auth.bootstrap_admin.password` is set to a unique value, the default
      account is renamed or disabled after first login.
- [ ] `mcm` binds to `127.0.0.1` or a private interface; a reverse proxy
      owns the public address.
- [ ] TLS is terminated at the proxy with HSTS, modern ciphers, and ACME
      renewal; `http.tls.enabled` is off at the MCM layer unless required.
- [ ] `http.trusted_proxies` lists the proxy's source address/CIDR.
- [ ] MCM is run as a dedicated unprivileged user; systemd unit applies
      `NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome=true`.
- [ ] `MCM_AUTH_JWT_SECRET`, `MCM_BOOTSTRAP_ADMIN_PASSWORD` live in the
      platform's secret store, mode `0600`; nothing real in `config.yaml`
      in version control.
- [ ] Mosquitto runs with its own authentication and ACL; MCM is the
      control plane, not a replacement for broker security.
- [ ] Backups run on the schedule in §6 and the quarterly restore drill
      has been executed at least once.
- [ ] Prometheus is scraping `/metrics`; broker-down, login-failure-spike,
      and p95-latency alerts are wired.
- [ ] `mcm doctor` runs cleanly after every config or topology change.

---

## See also

- [README](../README.md) — what works today, CLI, security baseline.
- [`deploy/mcm/README.md`](../deploy/mcm/README.md) — Docker deployment.
- [SECURITY.md](../SECURITY.md) — how to report a vulnerability.
- [`docs/openapi.yaml`](./openapi.yaml) — the HTTP API contract.
