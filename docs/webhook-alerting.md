# Webhook alerting

MCM can send minimal outbound webhook alerts for operational events. Webhooks are disabled by default and are intended for operator-owned receivers such as incident routing tools, chat bridges, or lightweight automation.

## Configuration

```yaml
alerting:
  enabled: true
  endpoint_url: "https://alerts.example.com/mcm"
  timeout: "5s"
  signing_secret: "replace-with-secret-from-your-receiver"
```

- `enabled`: set to `true` to send webhook alerts.
- `endpoint_url`: HTTPS endpoint that receives `POST` requests with JSON payloads. Required when enabled.
- `timeout`: per-delivery timeout. Delivery is attempted once for the first MVP slice; failures are logged and dropped so broker monitoring and HTTP handlers are not blocked.
- `signing_secret`: optional HMAC-SHA256 secret. When set, MCM sends `X-MCM-Signature: sha256=<hex-digest>` over the raw JSON body.

MCM also sends `X-MCM-Event` with the alert type and uses `Content-Type: application/json`.

## Events

Current webhook alert types:

- `broker_status`: emitted when the broker monitor observes a connect/reconnect or disconnect transition.
- `security_event`: emitted for selected security/audit-relevant events such as failed admin logins or rejected protected API access.

## Example receiver payload

```json
{
  "id": "broker_status-1779348600000000000",
  "type": "broker_status",
  "severity": "warning",
  "source": "broker",
  "message": "Broker disconnected: EOF",
  "observed_at": "2026-05-21T07:30:00Z",
  "details": {
    "status": "disconnected"
  }
}
```

Example security payload:

```json
{
  "id": "security_event-1779348600000000000",
  "type": "security_event",
  "severity": "warning",
  "source": "http_api",
  "message": "Security event: admin_login_failed (invalid_credentials)",
  "observed_at": "2026-05-21T07:30:00Z",
  "details": {
    "category": "admin_login_failed",
    "reason": "invalid_credentials",
    "username": "admin",
    "source_ip": "192.0.2.10",
    "method": "POST",
    "path": "/api/v1/auth/login"
  }
}
```

Receivers should verify `X-MCM-Signature` when `signing_secret` is configured and return any 2xx status to acknowledge delivery.
