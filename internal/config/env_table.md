# MCM environment variable reference

Generated from `internal/config.EnvBindingsMarkdown()` — DO NOT EDIT BY HAND.
Regenerate with `go run ./scripts/gen-env-table`.

| Variable | YAML path | Description |
| --- | --- | --- |
| `MCM_HTTP_BIND_ADDRESS` | `http.bind_address` | Interface the HTTP API binds to. Loopback/private in production (proxy owns the public address). |
| `MCM_HTTP_PORT` | `http.port` | TCP port for the HTTP API. Default 8080. |
| `MCM_HTTP_TRUSTED_PROXIES` | `http.trusted_proxies` | Comma-separated IP/CIDR list. MCM honors X-Forwarded-For / X-Real-IP from peers in this list. Empty (default) trusts no proxy. |
| `MCM_HTTP_TLS_ENABLED` | `http.tls.enabled` | Serve HTTPS directly from MCM. Off (default) means terminate TLS at the proxy. |
| `MCM_HTTP_TLS_CERT_FILE` | `http.tls.cert_file` | Path to PEM-encoded server certificate. Required when http.tls.enabled is true. |
| `MCM_HTTP_TLS_KEY_FILE` | `http.tls.key_file` | Path to PEM-encoded server private key. Required when http.tls.enabled is true. |
| `MCM_HTTP_TLS_MIN_VERSION` | `http.tls.min_version` | Minimum TLS version for the HTTPS listener. "1.2" or "1.3". Default "1.2". |
| `MCM_HTTP_TLS_CLIENT_CA_FILE` | `http.tls.client_ca_file` | Path to PEM-encoded CA bundle for mTLS client cert verification. Required when require_client_cert is true. |
| `MCM_HTTP_TLS_REQUIRE_CLIENT_CERT` | `http.tls.require_client_cert` | Enforce mTLS: every request must present a client certificate signed by client_ca_file. |
| `MCM_HTTP_CORS_ALLOWED_ORIGINS` | `http.cors.allowed_origins` | Comma-separated list of exact origins (scheme://host[:port]) permitted to make cross-origin requests. Empty (default) = same-origin only. |
| `MCM_DATABASE_BACKEND` | `database.backend` | Storage backend. "sqlite" (default; uses database.path) or "postgres" (uses database.dsn). |
| `MCM_DATABASE_PATH` | `database.path` | SQLite file path. Parent dir must be writable so the JWT-secret bootstrap can persist. |
| `MCM_DATABASE_DSN` | `database.dsn` | Postgres connection string. Required when database.backend is "postgres". |
| `MCM_AUTH_JWT_SECRET` | `auth.jwt_secret` | HMAC secret for signing JWTs. >=32 chars. If unset, a random one is generated and persisted to <db dir>/.bootstrap.json (mode 0600). |
| `MCM_AUTH_TOKEN_TTL` | `auth.token_ttl` | JWT lifetime. Go duration (e.g. "24h"). |
| `MCM_BOOTSTRAP_ADMIN_USERNAME` | `auth.bootstrap_admin.username` | First-boot admin username. Leave empty (with _PASSWORD) to auto-generate admin/<random 24-char pw>. |
| `MCM_BOOTSTRAP_ADMIN_PASSWORD` | `auth.bootstrap_admin.password` | First-boot admin password. >=8 chars, non-trivial. Auto-generated if both this and _USERNAME are empty. |
| `MCM_AUTH_LOGIN_LOCKOUT_WINDOW` | `auth.login_lockout.window` | Sliding window for failed-login counting. Go duration. Default 15m. |
| `MCM_AUTH_LOGIN_LOCKOUT_MAX_ATTEMPTS` | `auth.login_lockout.max_attempts` | Maximum failed logins within window before the source is locked out. >=1. Default 6. |
| `MCM_AUTH_LOGIN_LOCKOUT_COOLDOWN` | `auth.login_lockout.cooldown` | After the lockout window expires, how long the source remains blocked before retry is allowed. Go duration. Default 15m. |
| `MCM_MOSQUITTO_HOST` | `mosquitto.host` | Broker hostname or IP. Default "mosquitto" (bundled compose service). |
| `MCM_MOSQUITTO_PORT` | `mosquitto.port` | Broker TCP port. Default 1883 (plain) or 8883 (TLS). |
| `MCM_MOSQUITTO_USERNAME` | `mosquitto.username` | Broker service user. Both _USERNAME and _PASSWORD must be set or both empty. |
| `MCM_MOSQUITTO_PASSWORD` | `mosquitto.password` | Broker service user password. Read from a secret manager in production. |
| `MCM_MOSQUITTO_TLS_ENABLED` | `mosquitto.tls.enabled` | Connect to the broker over TLS (typically port 8883). Off by default. |
| `MCM_MOSQUITTO_TLS_CA_CERT_FILE` | `mosquitto.tls.ca_cert_file` | Path to PEM-encoded CA bundle used to verify the broker certificate. Required when mosquitto.tls.enabled is true. |
| `MCM_MOSQUITTO_TLS_CLIENT_CERT_FILE` | `mosquitto.tls.client_cert_file` | Path to PEM-encoded client certificate for mTLS to the broker. Required when mosquitto.tls.enabled is true. |
| `MCM_MOSQUITTO_TLS_CLIENT_KEY_FILE` | `mosquitto.tls.client_key_file` | Path to PEM-encoded client private key for mTLS to the broker. Required when mosquitto.tls.enabled is true. |
| `MCM_MOSQUITTO_TLS_INSECURE_SKIP_VERIFY` | `mosquitto.tls.insecure_skip_verify` | Skip broker certificate verification. DEV-ONLY escape hatch for self-signed testing; never enable in production. |
| `MCM_MOSQUITTO_DEPLOY_MODE` | `mosquitto.deploy.mode` | Deploy strategy. "" (disabled), "file" (write passwd/acl on disk + SIGHUP), or "docker" (write files + docker exec). |
| `MCM_MOSQUITTO_DEPLOY_ACL_PATH` | `mosquitto.deploy.acl_path` | On-disk path for the rendered ACL file. Required when deploy.mode is "file" or "docker". |
| `MCM_MOSQUITTO_DEPLOY_PASSWD_PATH` | `mosquitto.deploy.passwd_path` | On-disk path for the rendered passwd file. Required when deploy.mode is "file" or "docker". |
| `MCM_MOSQUITTO_DEPLOY_PID_PATH` | `mosquitto.deploy.pid_path` | Path to the broker's PID file. Optional even when deploy.mode is "file". |
| `MCM_MOSQUITTO_DEPLOY_CONTAINER_NAME` | `mosquitto.deploy.container_name` | Mosquitto container name for the "docker" deploy strategy (used by `docker exec kill -HUP 1`). |
| `MCM_MOSQUITTO_DEPLOY_RELOAD_STRATEGY` | `mosquitto.deploy.reload_strategy` | Reload strategy for the "file" deploy mode. "" or "sighup" (the only supported strategy right now). |
| `MCM_MOSQUITTO_DEPLOY_HEALTHCHECK_TIMEOUT` | `mosquitto.deploy.healthcheck_timeout` | Max time the deploy service waits for the broker to come back healthy after a reload. Go duration. Default 5s. |
| `MCM_MOSQUITTO_DEPLOY_WORKDIR` | `mosquitto.deploy.workdir` | Working directory for the deploy service when writing passwd/acl files. Defaults to the deploy service's CWD. |
| `MCM_MOSQUITTO_CONFIG_DIR` | `mosquitto.config_dir` | Directory containing the Mosquitto configuration. Surfaced for operators that pin the broker config dir separately from deploy paths. |
| `MCM_MOSQUITTO_DATA_DIR` | `mosquitto.data_dir` | Directory for Mosquitto persistent data (e.g. retained messages, persistence file). |
| `MCM_MOSQUITTO_SPARKPLUG_PAYLOAD_DECODE` | `mosquitto.sparkplug_payload_decode` | Decode Sparkplug B payloads into typed metrics on the broker events stream. Default false. |
| `MCM_MOSQUITTO_SPARKPLUG_MAX_METRICS` | `mosquitto.sparkplug_max_metrics` | Cap on the number of metrics kept per Sparkplug payload (defends against unbounded payloads). >=1. Default 50. |
| `MCM_METRICS_BROKER_RETENTION` | `metrics.broker_retention` | How long broker events are persisted. Go duration. Default 168h (7d). |
| `MCM_METRICS_AUDIT_RETENTION` | `metrics.audit_retention` | How long audit events are persisted. Go duration. Default 2160h (90d). |
| `MCM_METRICS_SECURITY_RETENTION` | `metrics.security_retention` | How long security events are persisted. Go duration. Default 2160h (90d). |
| `MCM_ALERTING_ENABLED` | `alerting.enabled` | Send operational alerts to the configured webhook endpoint. Default false. |
| `MCM_ALERTING_ENDPOINT_URL` | `alerting.endpoint_url` | Webhook URL to receive operational alerts. Required when alerting.enabled is true. |
| `MCM_ALERTING_TIMEOUT` | `alerting.timeout` | Timeout for individual webhook POSTs. Go duration. Default 5s. |
| `MCM_ALERTING_SIGNING_SECRET` | `alerting.signing_secret` | HMAC-SHA256 secret used to sign the X-MCM-Signature header on outbound alerts. |
| `MCM_ALERTING_COOLDOWN` | `alerting.cooldown` | Minimum interval between repeated alerts of the same class. Go duration. Default 5m. |
| `MCM_LOG_LEVEL` | `logging.level` | Log verbosity. One of "debug", "info", "warn", "error". Default "info". |
| `MCM_LOG_FORMAT` | `logging.format` | Log output format. "json" (default, recommended for production / SIEM) or "text". |
