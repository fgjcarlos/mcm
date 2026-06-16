# Edge Sites

Edge sites are remote MCM agents that periodically report their health status to the central MCM instance. Each site identifies itself with a stable string ID and sends a heartbeat containing its version, operational status, and an optional human-readable message.

## Non-Goals

- Fleet management or remote configuration push
- Telemetry relay or metrics aggregation from edge nodes
- Automatic remediation or alerting based on site status
- Persistent log collection from edge agents

## Authentication

All edge site endpoints require a valid Bearer token. Use the same token issued by `POST /api/v1/auth/login` for an operator or viewer account.

- `POST /api/v1/edge/heartbeat` — requires **operator** role or above
- `GET /api/v1/edge/sites` — requires **viewer** role or above
- `GET /api/v1/edge/sites/{id}` — requires **viewer** role or above

## Stale Site Classification

MCM does not automatically mark sites stale. The recommended threshold is **5 minutes**: if a site has not sent a heartbeat within the last 5 minutes, the UI or monitoring layer should treat it as stale. The `last_seen_at` field in each site record provides the timestamp for this calculation.

## API

### POST /api/v1/edge/heartbeat

Register or update an edge site's status. If the `site_id` already exists the record is updated in place (upsert); otherwise a new site record is created.

Request body:

```json
{
  "site_id":  "factory-floor-gw-01",
  "name":     "Factory Floor Gateway",
  "version":  "0.1.0",
  "status":   "healthy",
  "message":  "all systems nominal"
}
```

Field | Required | Values
------|----------|---------
`site_id` | yes | any non-empty string; recommended: stable hostname or UUID
`name`    | no  | human-readable label for the site
`version` | no  | software version string
`status`  | yes | `healthy`, `degraded`, or `unknown`
`message` | no  | optional details or error description

Response `200 OK`:

```json
{
  "id":           "factory-floor-gw-01",
  "name":         "Factory Floor Gateway",
  "version":      "0.1.0",
  "status":       "healthy",
  "message":      "all systems nominal",
  "last_seen_at": "2026-05-23T10:00:00Z",
  "created_at":   "2026-05-23T09:00:00Z",
  "updated_at":   "2026-05-23T10:00:00Z"
}
```

### GET /api/v1/edge/sites

List all known edge sites ordered by `last_seen_at` descending (most recently active first).

Response `200 OK`:

```json
{
  "sites": [
    {
      "id":           "factory-floor-gw-01",
      "name":         "Factory Floor Gateway",
      "version":      "0.1.0",
      "status":       "healthy",
      "last_seen_at": "2026-05-23T10:00:00Z",
      "created_at":   "2026-05-23T09:00:00Z",
      "updated_at":   "2026-05-23T10:00:00Z"
    }
  ]
}
```

### GET /api/v1/edge/sites/{id}

Retrieve a single edge site by its ID.

Response `200 OK` — same shape as the object in the list above.

Response `404 Not Found` when the ID does not exist.

## Deploying the MCM Edge Agent

The `mcm-agent` binary runs on each edge device and automatically sends heartbeats to the central MCM server.

### Installation

Download the `mcm-agent_<version>_<os>_<arch>.tar.gz` archive for your platform from the [releases page](https://github.com/fgjcarlos/mcm/releases) (see [docs/releasing.md](releasing.md#selecting-the-right-artifact) for the full artifact matrix), then extract and place it in `/usr/local/bin/mcm-agent`:

```bash
# Linux amd64 example
curl -fsSL -O https://github.com/fgjcarlos/mcm/releases/download/v1.0.0/mcm-agent_1.0.0_linux_amd64.tar.gz
curl -fsSL -O https://github.com/fgjcarlos/mcm/releases/download/v1.0.0/checksums.txt
sha256sum -c checksums.txt --ignore-missing
tar -xzf mcm-agent_1.0.0_linux_amd64.tar.gz -C /usr/local/bin mcm-agent
```

Verify the signature on `checksums.txt` following [docs/releasing.md](releasing.md#verifying-release-signatures) before installing.

### Configuration

Copy the example config and fill in your site details:

```bash
cp /usr/local/share/mcm-agent/config.example.yaml /etc/mcm-agent/config.yaml
```

Minimum required fields:

```yaml
server:
  url: "https://mcm.example.com"
  token: "<your-bearer-token>"   # or use username/password

site:
  id: "factory-floor-gw-01"
  name: "Factory Floor Gateway"
```

Full field reference: see `deploy/mcm-agent/config.example.yaml`.

### Running with systemd

```ini
# /etc/systemd/system/mcm-agent.service
[Unit]
Description=MCM Edge Agent
After=network.target mosquitto.service

[Service]
ExecStart=/usr/local/bin/mcm-agent -config /etc/mcm-agent/config.yaml
Restart=on-failure
RestartSec=10s
# Sensitive values can be passed as environment variables instead of
# storing credentials in the config file.
# EnvironmentFile=/etc/mcm-agent/env

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
systemctl daemon-reload
systemctl enable --now mcm-agent
journalctl -u mcm-agent -f
```

## curl Examples

Send a heartbeat:

```bash
TOKEN="<your-operator-token>"

curl -s -X POST https://mcm.example.com/api/v1/edge/heartbeat \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "site_id":  "factory-floor-gw-01",
    "name":     "Factory Floor Gateway",
    "version":  "0.1.0",
    "status":   "healthy",
    "message":  "all systems nominal"
  }' | jq .
```

List all sites:

```bash
curl -s https://mcm.example.com/api/v1/edge/sites \
  -H "Authorization: Bearer $TOKEN" | jq .
```

Get a single site:

```bash
curl -s https://mcm.example.com/api/v1/edge/sites/factory-floor-gw-01 \
  -H "Authorization: Bearer $TOKEN" | jq .
```
