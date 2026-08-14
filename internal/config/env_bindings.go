package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// envBinding is one row of the canonical MCM_* environment variable table.
//
// The table is the SINGLE source of truth for env-var parsing: applyEnvOverrides
// walks it on every Load/Parse, and EnvBindingsMarkdown renders it into the
// README/production docs. Adding a new MCM_* var means adding one entry here;
// both the runtime and the docs pick it up automatically.
//
// Design rules (see internal/config/config_test.go for the contract):
//
//   - Name is the MCM_-prefixed env var.
//   - Path is the dotted YAML path ("http.tls.cert_file") for the README.
//   - Apply is responsible for parsing and assigning the field. Errors must
//     include enough context for the operator to fix the env var; the caller
//     wraps them with the var name and sanitizes the value.
//   - Secret is true for fields whose value must never appear in logs (JWT
//     secret, passwords, signing secret, broker passwd, bootstrap admin pw,
//     PEM file contents, etc.).
//   - Doc is the one-line human description used by EnvBindingsMarkdown and
//     the README. Keep it short and operational, not aspirational.
//
// If Apply returns an error, Load/Parse wrap it as
//
//	"env var %s is invalid: %s (value: %q)"
//
// where %q is sanitized (secret values are length-only; non-secrets are
// truncated to 80 runes). The caller fails the boot.
type envBinding struct {
	Name   string
	Path   string
	Doc    string
	Secret bool
	Apply  func(cfg *Config, raw string) error
}

// envBindings is the canonical list of every supported MCM_* env var.
//
// Order is "feature group first" (HTTP, Database, Auth, Mosquitto, Metrics,
// Alerting, Logging) and matches the README. Adding a new env var: append
// to the appropriate block, add a test in TestEnvEveryVarIsWired (or a
// dedicated Test* function for fields that need richer assertions), and
// update the README via EnvBindingsMarkdown if the description changed.
//
// NOTE: Keep this table in sync with internal/config/env_table.md (the
// generated docs file) — EnvBindingsMarkdown is the generator and
// TestEnvTableMatchesREADME is the guard against drift.
var envBindings = []envBinding{
	// ── HTTP listener ─────────────────────────────────────────────────
	{
		Name: "MCM_HTTP_BIND_ADDRESS",
		Path: "http.bind_address",
		Doc:  "Interface the HTTP API binds to. Loopback/private in production (proxy owns the public address).",
		Apply: func(cfg *Config, raw string) error {
			cfg.HTTP.BindAddress = raw
			return nil
		},
	},
	{
		Name: "MCM_HTTP_PORT",
		Path: "http.port",
		Doc:  "TCP port for the HTTP API. Default 8080.",
		Apply: func(cfg *Config, raw string) error {
			port, err := strconv.Atoi(raw)
			if err != nil {
				return fmt.Errorf("must be a base-10 integer between 1 and 65535; got %q", raw)
			}
			if port < 1 || port > 65535 {
				return fmt.Errorf("must be between 1 and 65535; got %d", port)
			}
			cfg.HTTP.Port = port
			return nil
		},
	},
	{
		Name: "MCM_HTTP_TRUSTED_PROXIES",
		Path: "http.trusted_proxies",
		Doc:  "Comma-separated IP/CIDR list. MCM honors X-Forwarded-For / X-Real-IP from peers in this list. Empty (default) trusts no proxy.",
		Apply: func(cfg *Config, raw string) error {
			list, err := parseStringSliceCSV(raw)
			if err != nil {
				return fmt.Errorf("must be a comma-separated list of IP/CIDR values: %w", err)
			}
			cfg.HTTP.TrustedProxies = list
			return nil
		},
	},

	// ── HTTP TLS / mTLS ───────────────────────────────────────────────
	{
		Name: "MCM_HTTP_TLS_ENABLED",
		Path: "http.tls.enabled",
		Doc:  "Serve HTTPS directly from MCM. Off (default) means terminate TLS at the proxy.",
		Apply: func(cfg *Config, raw string) error {
			b, err := parseBoolish(raw)
			if err != nil {
				return fmt.Errorf("must be a boolean (true/false/1/0/yes/no); got %q", raw)
			}
			cfg.HTTP.TLS.Enabled = b
			return nil
		},
	},
	{
		Name: "MCM_HTTP_TLS_CERT_FILE",
		Path: "http.tls.cert_file",
		Doc:  "Path to PEM-encoded server certificate. Required when http.tls.enabled is true.",
		Apply: func(cfg *Config, raw string) error {
			cfg.HTTP.TLS.CertFile = raw
			return nil
		},
	},
	{
		Name: "MCM_HTTP_TLS_KEY_FILE",
		Path: "http.tls.key_file",
		Doc:  "Path to PEM-encoded server private key. Required when http.tls.enabled is true.",
		Apply: func(cfg *Config, raw string) error {
			cfg.HTTP.TLS.KeyFile = raw
			return nil
		},
	},
	{
		Name: "MCM_HTTP_TLS_MIN_VERSION",
		Path: "http.tls.min_version",
		Doc:  `Minimum TLS version for the HTTPS listener. "1.2" or "1.3". Default "1.2".`,
		Apply: func(cfg *Config, raw string) error {
			switch raw {
			case "1.2", "1.3":
			default:
				return fmt.Errorf(`must be "1.2" or "1.3"; got %q`, raw)
			}
			cfg.HTTP.TLS.MinVersion = raw
			return nil
		},
	},
	{
		Name: "MCM_HTTP_TLS_CLIENT_CA_FILE",
		Path: "http.tls.client_ca_file",
		Doc:  "Path to PEM-encoded CA bundle for mTLS client cert verification. Required when require_client_cert is true.",
		Apply: func(cfg *Config, raw string) error {
			cfg.HTTP.TLS.ClientCAFile = raw
			return nil
		},
	},
	{
		Name: "MCM_HTTP_TLS_REQUIRE_CLIENT_CERT",
		Path: "http.tls.require_client_cert",
		Doc:  "Enforce mTLS: every request must present a client certificate signed by client_ca_file.",
		Apply: func(cfg *Config, raw string) error {
			b, err := parseBoolish(raw)
			if err != nil {
				return fmt.Errorf("must be a boolean (true/false/1/0/yes/no); got %q", raw)
			}
			cfg.HTTP.TLS.RequireClientCert = b
			return nil
		},
	},

	// ── HTTP CORS ─────────────────────────────────────────────────────
	{
		Name: "MCM_HTTP_CORS_ALLOWED_ORIGINS",
		Path: "http.cors.allowed_origins",
		Doc:  `Comma-separated list of exact origins (scheme://host[:port]) permitted to make cross-origin requests. Empty (default) = same-origin only.`,
		Apply: func(cfg *Config, raw string) error {
			list, err := parseStringSliceCSV(raw)
			if err != nil {
				return fmt.Errorf("must be a comma-separated list of origins: %w", err)
			}
			cfg.HTTP.CORS.AllowedOrigins = list
			return nil
		},
	},

	// ── Database ──────────────────────────────────────────────────────
	{
		Name: "MCM_DATABASE_BACKEND",
		Path: "database.backend",
		Doc:  `Storage backend. "sqlite" (default; uses database.path) or "postgres" (uses database.dsn).`,
		Apply: func(cfg *Config, raw string) error {
			switch raw {
			case "sqlite", "postgres":
			default:
				return fmt.Errorf(`must be "sqlite" or "postgres"; got %q`, raw)
			}
			cfg.Database.Backend = raw
			return nil
		},
	},
	{
		Name: "MCM_DATABASE_PATH",
		Path: "database.path",
		Doc:  "SQLite file path. Parent dir must be writable so the JWT-secret bootstrap can persist.",
		Apply: func(cfg *Config, raw string) error {
			cfg.Database.Path = raw
			return nil
		},
	},
	{
		Name: "MCM_DATABASE_DSN",
		Path: "database.dsn",
		Doc:  `Postgres connection string. Required when database.backend is "postgres".`,
		Apply: func(cfg *Config, raw string) error {
			cfg.Database.DSN = raw
			return nil
		},
	},

	// ── Auth ──────────────────────────────────────────────────────────
	{
		Name:   "MCM_AUTH_JWT_SECRET",
		Path:   "auth.jwt_secret",
		Doc:    "HMAC secret for signing JWTs. >=32 chars. If unset, a random one is generated and persisted to <db dir>/.bootstrap.json (mode 0600).",
		Secret: true,
		Apply: func(cfg *Config, raw string) error {
			cfg.Auth.JWTSecret = raw
			return nil
		},
	},
	{
		Name: "MCM_AUTH_TOKEN_TTL",
		Path: "auth.token_ttl",
		Doc:  "JWT lifetime. Go duration (e.g. \"24h\").",
		Apply: func(cfg *Config, raw string) error {
			if _, err := time.ParseDuration(raw); err != nil {
				return fmt.Errorf("must be a Go duration (e.g. \"24h\", \"30m\"): %w", err)
			}
			cfg.Auth.TokenTTL = raw
			return nil
		},
	},
	{
		Name: "MCM_BOOTSTRAP_ADMIN_USERNAME",
		Path: "auth.bootstrap_admin.username",
		Doc:  "First-boot admin username. Leave empty (with _PASSWORD) to auto-generate admin/<random 24-char pw>.",
		Apply: func(cfg *Config, raw string) error {
			cfg.Auth.BootstrapAdmin.Username = raw
			return nil
		},
	},
	{
		Name:   "MCM_BOOTSTRAP_ADMIN_PASSWORD",
		Path:   "auth.bootstrap_admin.password",
		Doc:    "First-boot admin password. >=8 chars, non-trivial. Auto-generated if both this and _USERNAME are empty.",
		Secret: true,
		Apply: func(cfg *Config, raw string) error {
			cfg.Auth.BootstrapAdmin.Password = raw
			return nil
		},
	},
	{
		Name: "MCM_AUTH_LOGIN_LOCKOUT_WINDOW",
		Path: "auth.login_lockout.window",
		Doc:  "Sliding window for failed-login counting. Go duration. Default 15m.",
		Apply: func(cfg *Config, raw string) error {
			if _, err := time.ParseDuration(raw); err != nil {
				return fmt.Errorf("must be a Go duration (e.g. \"15m\"): %w", err)
			}
			cfg.Auth.LoginLockout.Window = raw
			return nil
		},
	},
	{
		Name: "MCM_AUTH_LOGIN_LOCKOUT_MAX_ATTEMPTS",
		Path: "auth.login_lockout.max_attempts",
		Doc:  "Maximum failed logins within window before the source is locked out. >=1. Default 6.",
		Apply: func(cfg *Config, raw string) error {
			n, err := strconv.Atoi(raw)
			if err != nil {
				return fmt.Errorf("must be a base-10 integer >= 1; got %q", raw)
			}
			if n < 1 {
				return fmt.Errorf("must be >= 1; got %d", n)
			}
			cfg.Auth.LoginLockout.MaxAttempts = n
			return nil
		},
	},
	{
		Name: "MCM_AUTH_LOGIN_LOCKOUT_COOLDOWN",
		Path: "auth.login_lockout.cooldown",
		Doc:  "After the lockout window expires, how long the source remains blocked before retry is allowed. Go duration. Default 15m.",
		Apply: func(cfg *Config, raw string) error {
			if _, err := time.ParseDuration(raw); err != nil {
				return fmt.Errorf("must be a Go duration (e.g. \"15m\"): %w", err)
			}
			cfg.Auth.LoginLockout.Cooldown = raw
			return nil
		},
	},

	// ── Mosquitto broker connection ───────────────────────────────────
	{
		Name: "MCM_MOSQUITTO_HOST",
		Path: "mosquitto.host",
		Doc:  "Broker hostname or IP. Default \"mosquitto\" (bundled compose service).",
		Apply: func(cfg *Config, raw string) error {
			cfg.Mosquitto.Host = raw
			return nil
		},
	},
	{
		Name: "MCM_MOSQUITTO_PORT",
		Path: "mosquitto.port",
		Doc:  "Broker TCP port. Default 1883 (plain) or 8883 (TLS).",
		Apply: func(cfg *Config, raw string) error {
			port, err := strconv.Atoi(raw)
			if err != nil {
				return fmt.Errorf("must be a base-10 integer between 1 and 65535; got %q", raw)
			}
			if port < 1 || port > 65535 {
				return fmt.Errorf("must be between 1 and 65535; got %d", port)
			}
			cfg.Mosquitto.Port = port
			return nil
		},
	},
	{
		Name: "MCM_MOSQUITTO_USERNAME",
		Path: "mosquitto.username",
		Doc:  "Broker service user. Both _USERNAME and _PASSWORD must be set or both empty.",
		Apply: func(cfg *Config, raw string) error {
			cfg.Mosquitto.Username = raw
			return nil
		},
	},
	{
		Name:   "MCM_MOSQUITTO_PASSWORD",
		Path:   "mosquitto.password",
		Doc:    "Broker service user password. Read from a secret manager in production.",
		Secret: true,
		Apply: func(cfg *Config, raw string) error {
			cfg.Mosquitto.Password = raw
			return nil
		},
	},

	// ── Mosquitto TLS ─────────────────────────────────────────────────
	{
		Name: "MCM_MOSQUITTO_TLS_ENABLED",
		Path: "mosquitto.tls.enabled",
		Doc:  "Connect to the broker over TLS (typically port 8883). Off by default.",
		Apply: func(cfg *Config, raw string) error {
			b, err := parseBoolish(raw)
			if err != nil {
				return fmt.Errorf("must be a boolean (true/false/1/0/yes/no); got %q", raw)
			}
			cfg.Mosquitto.TLS.Enabled = b
			return nil
		},
	},
	{
		Name: "MCM_MOSQUITTO_TLS_CA_CERT_FILE",
		Path: "mosquitto.tls.ca_cert_file",
		Doc:  "Path to PEM-encoded CA bundle used to verify the broker certificate. Required when mosquitto.tls.enabled is true.",
		Apply: func(cfg *Config, raw string) error {
			cfg.Mosquitto.TLS.CACertFile = raw
			return nil
		},
	},
	{
		Name: "MCM_MOSQUITTO_TLS_CLIENT_CERT_FILE",
		Path: "mosquitto.tls.client_cert_file",
		Doc:  "Path to PEM-encoded client certificate for mTLS to the broker. Required when mosquitto.tls.enabled is true.",
		Apply: func(cfg *Config, raw string) error {
			cfg.Mosquitto.TLS.ClientCertFile = raw
			return nil
		},
	},
	{
		Name: "MCM_MOSQUITTO_TLS_CLIENT_KEY_FILE",
		Path: "mosquitto.tls.client_key_file",
		Doc:  "Path to PEM-encoded client private key for mTLS to the broker. Required when mosquitto.tls.enabled is true.",
		Apply: func(cfg *Config, raw string) error {
			cfg.Mosquitto.TLS.ClientKeyFile = raw
			return nil
		},
	},
	{
		Name: "MCM_MOSQUITTO_TLS_INSECURE_SKIP_VERIFY",
		Path: "mosquitto.tls.insecure_skip_verify",
		Doc:  "Skip broker certificate verification. DEV-ONLY escape hatch for self-signed testing; never enable in production.",
		Apply: func(cfg *Config, raw string) error {
			b, err := parseBoolish(raw)
			if err != nil {
				return fmt.Errorf("must be a boolean (true/false/1/0/yes/no); got %q", raw)
			}
			cfg.Mosquitto.TLS.InsecureSkipVerify = b
			return nil
		},
	},

	// ── Mosquitto deploy (file / docker) ──────────────────────────────
	{
		Name: "MCM_MOSQUITTO_DEPLOY_MODE",
		Path: "mosquitto.deploy.mode",
		Doc:  `Deploy strategy. "" (disabled), "file" (write passwd/acl on disk + SIGHUP), or "docker" (write files + docker exec).`,
		Apply: func(cfg *Config, raw string) error {
			switch raw {
			case "", "file", "docker":
			default:
				return fmt.Errorf(`must be "", "file", or "docker"; got %q`, raw)
			}
			cfg.Mosquitto.Deploy.Mode = raw
			return nil
		},
	},
	{
		Name: "MCM_MOSQUITTO_DEPLOY_ACL_PATH",
		Path: "mosquitto.deploy.acl_path",
		Doc:  "On-disk path for the rendered ACL file. Required when deploy.mode is \"file\" or \"docker\".",
		Apply: func(cfg *Config, raw string) error {
			cfg.Mosquitto.Deploy.ACLPath = raw
			return nil
		},
	},
	{
		Name: "MCM_MOSQUITTO_DEPLOY_PASSWD_PATH",
		Path: "mosquitto.deploy.passwd_path",
		Doc:  "On-disk path for the rendered passwd file. Required when deploy.mode is \"file\" or \"docker\".",
		Apply: func(cfg *Config, raw string) error {
			cfg.Mosquitto.Deploy.PasswdPath = raw
			return nil
		},
	},
	{
		Name: "MCM_MOSQUITTO_DEPLOY_PID_PATH",
		Path: "mosquitto.deploy.pid_path",
		Doc:  "Path to the broker's PID file. Optional even when deploy.mode is \"file\".",
		Apply: func(cfg *Config, raw string) error {
			cfg.Mosquitto.Deploy.PIDPath = raw
			return nil
		},
	},
	{
		Name: "MCM_MOSQUITTO_DEPLOY_CONTAINER_NAME",
		Path: "mosquitto.deploy.container_name",
		Doc:  "Mosquitto container name for the \"docker\" deploy strategy (used by `docker exec kill -HUP 1`).",
		Apply: func(cfg *Config, raw string) error {
			cfg.Mosquitto.Deploy.ContainerName = raw
			return nil
		},
	},
	{
		Name: "MCM_MOSQUITTO_DEPLOY_RELOAD_STRATEGY",
		Path: "mosquitto.deploy.reload_strategy",
		Doc:  `Reload strategy for the "file" deploy mode. "" or "sighup" (the only supported strategy right now).`,
		Apply: func(cfg *Config, raw string) error {
			if raw != "" && raw != "sighup" {
				return fmt.Errorf(`must be "" or "sighup"; got %q`, raw)
			}
			cfg.Mosquitto.Deploy.ReloadStrategy = raw
			return nil
		},
	},
	{
		Name: "MCM_MOSQUITTO_DEPLOY_HEALTHCHECK_TIMEOUT",
		Path: "mosquitto.deploy.healthcheck_timeout",
		Doc:  "Max time the deploy service waits for the broker to come back healthy after a reload. Go duration. Default 5s.",
		Apply: func(cfg *Config, raw string) error {
			d, err := time.ParseDuration(raw)
			if err != nil {
				return fmt.Errorf("must be a Go duration (e.g. \"5s\"): %w", err)
			}
			if d <= 0 {
				return fmt.Errorf("must be greater than zero; got %s", d)
			}
			cfg.Mosquitto.Deploy.HealthcheckTimeout = d
			return nil
		},
	},
	{
		Name: "MCM_MOSQUITTO_DEPLOY_WORKDIR",
		Path: "mosquitto.deploy.workdir",
		Doc:  "Working directory for the deploy service when writing passwd/acl files. Defaults to the deploy service's CWD.",
		Apply: func(cfg *Config, raw string) error {
			cfg.Mosquitto.Deploy.Workdir = raw
			return nil
		},
	},

	// ── Mosquitto runtime paths (config / data directories) ───────────
	{
		Name: "MCM_MOSQUITTO_CONFIG_DIR",
		Path: "mosquitto.config_dir",
		Doc:  "Directory containing the Mosquitto configuration. Surfaced for operators that pin the broker config dir separately from deploy paths.",
		Apply: func(cfg *Config, raw string) error {
			cfg.Mosquitto.ConfigDir = raw
			return nil
		},
	},
	{
		Name: "MCM_MOSQUITTO_DATA_DIR",
		Path: "mosquitto.data_dir",
		Doc:  "Directory for Mosquitto persistent data (e.g. retained messages, persistence file).",
		Apply: func(cfg *Config, raw string) error {
			cfg.Mosquitto.DataDir = raw
			return nil
		},
	},

	// ── Mosquitto Sparkplug tuning ────────────────────────────────────
	{
		Name: "MCM_MOSQUITTO_SPARKPLUG_PAYLOAD_DECODE",
		Path: "mosquitto.sparkplug_payload_decode",
		Doc:  "Decode Sparkplug B payloads into typed metrics on the broker events stream. Default false.",
		Apply: func(cfg *Config, raw string) error {
			b, err := parseBoolish(raw)
			if err != nil {
				return fmt.Errorf("must be a boolean (true/false/1/0/yes/no); got %q", raw)
			}
			cfg.Mosquitto.SparkplugPayloadDecode = b
			return nil
		},
	},
	{
		Name: "MCM_MOSQUITTO_SPARKPLUG_MAX_METRICS",
		Path: "mosquitto.sparkplug_max_metrics",
		Doc:  "Cap on the number of metrics kept per Sparkplug payload (defends against unbounded payloads). >=1. Default 50.",
		Apply: func(cfg *Config, raw string) error {
			n, err := strconv.Atoi(raw)
			if err != nil {
				return fmt.Errorf("must be a base-10 integer >= 1; got %q", raw)
			}
			if n < 1 {
				return fmt.Errorf("must be >= 1; got %d", n)
			}
			cfg.Mosquitto.SparkplugMaxMetrics = n
			return nil
		},
	},

	// ── Metrics / event retention ─────────────────────────────────────
	{
		Name: "MCM_METRICS_BROKER_RETENTION",
		Path: "metrics.broker_retention",
		Doc:  "How long broker events are persisted. Go duration. Default 168h (7d).",
		Apply: func(cfg *Config, raw string) error {
			if _, err := time.ParseDuration(raw); err != nil {
				return fmt.Errorf("must be a Go duration (e.g. \"168h\"): %w", err)
			}
			cfg.Metrics.BrokerRetention = raw
			return nil
		},
	},
	{
		Name: "MCM_METRICS_AUDIT_RETENTION",
		Path: "metrics.audit_retention",
		Doc:  "How long audit events are persisted. Go duration. Default 2160h (90d).",
		Apply: func(cfg *Config, raw string) error {
			if _, err := time.ParseDuration(raw); err != nil {
				return fmt.Errorf("must be a Go duration (e.g. \"2160h\"): %w", err)
			}
			cfg.Metrics.AuditRetention = raw
			return nil
		},
	},
	{
		Name: "MCM_METRICS_SECURITY_RETENTION",
		Path: "metrics.security_retention",
		Doc:  "How long security events are persisted. Go duration. Default 2160h (90d).",
		Apply: func(cfg *Config, raw string) error {
			if _, err := time.ParseDuration(raw); err != nil {
				return fmt.Errorf("must be a Go duration (e.g. \"2160h\"): %w", err)
			}
			cfg.Metrics.SecurityRetention = raw
			return nil
		},
	},

	// ── Alerting (outbound operational webhooks) ──────────────────────
	{
		Name: "MCM_ALERTING_ENABLED",
		Path: "alerting.enabled",
		Doc:  "Send operational alerts to the configured webhook endpoint. Default false.",
		Apply: func(cfg *Config, raw string) error {
			b, err := parseBoolish(raw)
			if err != nil {
				return fmt.Errorf("must be a boolean (true/false/1/0/yes/no); got %q", raw)
			}
			cfg.Alerting.Enabled = b
			return nil
		},
	},
	{
		Name: "MCM_ALERTING_ENDPOINT_URL",
		Path: "alerting.endpoint_url",
		Doc:  "Webhook URL to receive operational alerts. Required when alerting.enabled is true.",
		Apply: func(cfg *Config, raw string) error {
			cfg.Alerting.EndpointURL = raw
			return nil
		},
	},
	{
		Name: "MCM_ALERTING_TIMEOUT",
		Path: "alerting.timeout",
		Doc:  "Timeout for individual webhook POSTs. Go duration. Default 5s.",
		Apply: func(cfg *Config, raw string) error {
			if _, err := time.ParseDuration(raw); err != nil {
				return fmt.Errorf("must be a Go duration (e.g. \"5s\"): %w", err)
			}
			cfg.Alerting.Timeout = raw
			return nil
		},
	},
	{
		Name:   "MCM_ALERTING_SIGNING_SECRET",
		Path:   "alerting.signing_secret",
		Doc:    "HMAC-SHA256 secret used to sign the X-MCM-Signature header on outbound alerts.",
		Secret: true,
		Apply: func(cfg *Config, raw string) error {
			cfg.Alerting.SigningSecret = raw
			return nil
		},
	},
	{
		Name: "MCM_ALERTING_COOLDOWN",
		Path: "alerting.cooldown",
		Doc:  "Minimum interval between repeated alerts of the same class. Go duration. Default 5m.",
		Apply: func(cfg *Config, raw string) error {
			if _, err := time.ParseDuration(raw); err != nil {
				return fmt.Errorf("must be a Go duration (e.g. \"5m\"): %w", err)
			}
			cfg.Alerting.Cooldown = raw
			return nil
		},
	},

	// ── Logging ───────────────────────────────────────────────────────
	{
		Name: "MCM_LOG_LEVEL",
		Path: "logging.level",
		Doc:  `Log verbosity. One of "debug", "info", "warn", "error". Default "info".`,
		Apply: func(cfg *Config, raw string) error {
			switch raw {
			case "debug", "info", "warn", "error":
			default:
				return fmt.Errorf(`must be one of: debug, info, warn, error; got %q`, raw)
			}
			cfg.Logging.Level = raw
			return nil
		},
	},
	{
		Name: "MCM_LOG_FORMAT",
		Path: "logging.format",
		Doc:  `Log output format. "json" (default, recommended for production / SIEM) or "text".`,
		Apply: func(cfg *Config, raw string) error {
			switch raw {
			case "json", "text":
			default:
				return fmt.Errorf(`must be "json" or "text"; got %q`, raw)
			}
			cfg.Logging.Format = raw
			return nil
		},
	},
}

// envBindingByName is built once at package init for O(1) Apply lookup and
// for the "unknown MCM_* var" detection in applyEnvOverrides. Generated from
// envBindings; do not edit directly.
var envBindingByName = func() map[string]envBinding {
	m := make(map[string]envBinding, len(envBindings))
	for _, b := range envBindings {
		m[b.Name] = b
	}
	return m
}()

// EnvBindingNames returns the sorted list of every MCM_* env var name in
// the canonical table. Tests and docs use it to assert coverage.
func EnvBindingNames() []string {
	names := make([]string, 0, len(envBindings))
	for _, b := range envBindings {
		names = append(names, b.Name)
	}
	sort.Strings(names)
	return names
}

// EnvBindingsMarkdown renders the canonical env-var table as Markdown.
//
// The output is meant to be embedded into README.md and docs/production.md
// so the three stay in lockstep with the code. TestEnvTableMatchesREADME
// is the drift guard: it parses the README and asserts every row in this
// table appears there.
//
// The renderer groups rows by section, in the same order as envBindings.
func EnvBindingsMarkdown() string {
	var b strings.Builder
	b.WriteString("| Variable | YAML path | Description |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, eb := range envBindings {
		fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", eb.Name, eb.Path, eb.Doc)
	}
	return b.String()
}

// applyEnvOverrides walks envBindings once and applies every MCM_* var that
// is set in the process environment. An invalid value aborts the boot with
// an actionable error; an unknown MCM_* var (set but not in the table) is
// also an error so a typo doesn't silently no-op.
//
// The order is the table order; bindings are independent so this is safe.
//
// Security: when an Apply error is returned the value is sanitized before
// being added to the error message. Secret-marked vars render as
// `<redacted:N bytes>`; non-secret vars render as the value itself,
// truncated to 80 runes (no truncation for shorter values). PEM blobs and
// file paths are NOT marked secret — they're paths, not credentials — but
// their values still go through the same truncation rule so a path that
// happens to contain a password fragment doesn't dump it to the log.
func applyEnvOverrides(cfg *Config) error {
	// First pass: apply every binding whose env var is set and non-empty.
	for _, eb := range envBindings {
		raw, ok := os.LookupEnv(eb.Name)
		if !ok || raw == "" {
			continue
		}
		if err := eb.Apply(cfg, raw); err != nil {
			return fmt.Errorf("invalid env var %s: %s (value: %s)",
				eb.Name, err.Error(), sanitizeForLog(raw, eb.Secret))
		}
	}

	// Second pass: detect any MCM_* var set in the environment that is not
	// in the canonical table. This catches typos (e.g. MCM_HTTP_BIN_ADDRESS)
	// and gives operators a clear error pointing at the var name.
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(name, "MCM_") {
			continue
		}
		// MCM_CONFIG_FILE is handled by Load(), not the table; allow it.
		if name == "MCM_CONFIG_FILE" {
			continue
		}
		if _, ok := envBindingByName[name]; ok {
			continue
		}
		return fmt.Errorf("unknown env var %s: MCM_* environment variables must be declared in internal/config.envBindings; check the spelling against the README", name)
	}

	return nil
}

// sanitizeForLog renders a raw env-var value for an error message.
//
//   - Secret values (JWT secret, passwords, signing secret) are reduced to
//     "<redacted:N bytes>" so a log capture can confirm the value was set
//     without ever showing the credential.
//   - Non-secret values are returned verbatim but truncated to 80 runes so
//     an accidentally-large value (e.g. a pasted private key) doesn't dump
//     itself into operator-visible logs.
//
// The truncation uses runes (not bytes) to avoid breaking multi-byte UTF-8.
func sanitizeForLog(raw string, secret bool) string {
	if secret {
		return fmt.Sprintf("<redacted:%d bytes>", len(raw))
	}
	const max = 80
	runes := []rune(raw)
	if len(runes) <= max {
		return strconv.Quote(raw)
	}
	return strconv.Quote(string(runes[:max])) + "...(truncated)"
}

// parseBoolish accepts the same boolean literals the rest of the codebase
// uses (true/false/1/0/yes/no, case-insensitive). Centralized so every
// bool-typed binding stays consistent.
func parseBoolish(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	default:
		return false, errors.New("not a recognized boolean literal")
	}
}

// parseStringSliceCSV splits a comma-separated list, trims each entry, and
// drops empty entries. Returns an error if any entry contains a control
// character that would indicate a malformed value rather than a real list.
func parseStringSliceCSV(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		for _, r := range trimmed {
			if r < 0x20 || r == 0x7f {
				return nil, fmt.Errorf("entry %q contains control character", trimmed)
			}
		}
		out = append(out, trimmed)
	}
	return out, nil
}
