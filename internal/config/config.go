package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const fileName = "config.yaml"

var validLogLevels = map[string]struct{}{
	"debug": {},
	"info":  {},
	"warn":  {},
	"error": {},
}

var validLogFormats = map[string]struct{}{
	"text": {},
	"json": {},
}

var insecureDefaultSecrets = map[string]struct{}{
	"replace-this-secret-with-at-least-32-characters": {},
	"change-this-admin-password":                      {},
	"mcm-dev-secret-change-in-production":             {},
}

// trivialPasswords is a blocklist of passwords that are too common or guessable
// to be accepted as bootstrap admin credentials.
var trivialPasswords = map[string]struct{}{
	"admin":    {},
	"password": {},
	"changeme": {},
	"12345678": {},
}

// Config holds MCM runtime configuration loaded from YAML.
type Config struct {
	HTTP      HTTPConfig      `yaml:"http"`
	Database  DatabaseConfig  `yaml:"database"`
	Auth      AuthConfig      `yaml:"auth"`
	Mosquitto MosquittoConfig `yaml:"mosquitto"`
	Metrics   MetricsConfig   `yaml:"metrics"`
	Status    StatusConfig    `yaml:"status"`
	Alerting  AlertingConfig  `yaml:"alerting"`
	Logging   LoggingConfig   `yaml:"logging"`
}

// CORSConfig controls cross-origin resource sharing for the API.
// AllowedOrigins is a list of exact origin values (scheme + host + optional port)
// that are permitted to make cross-origin requests. An empty list (the default)
// means no cross-origin requests are allowed — strict same-origin policy.
// Never use wildcard origins ("*") here; credentials require an exact match.
type CORSConfig struct {
	AllowedOrigins []string `yaml:"allowed_origins"`
}

// HTTPConfig controls the HTTP listener.
type HTTPConfig struct {
	BindAddress string        `yaml:"bind_address"`
	Port        int           `yaml:"port"`
	TLS         HTTPTLSConfig `yaml:"tls"`
	// TrustedProxies lists IP/CIDR entries whose X-Forwarded-For / X-Real-IP
	// headers are honored when determining the client IP. Empty (default) means
	// no proxy is trusted and the direct peer address is always used.
	TrustedProxies []string   `yaml:"trusted_proxies"`
	CORS           CORSConfig `yaml:"cors"`
}

// HTTPTLSConfig controls HTTPS and optional mTLS for the MCM API.
type HTTPTLSConfig struct {
	Enabled           bool   `yaml:"enabled"`
	CertFile          string `yaml:"cert_file"`
	KeyFile           string `yaml:"key_file"`
	MinVersion        string `yaml:"min_version"`
	ClientCAFile      string `yaml:"client_ca_file"`
	RequireClientCert bool   `yaml:"require_client_cert"`
}

// DatabaseConfig controls persistence. Backend selects "sqlite" (default) or
// "postgres". When backend is "sqlite", Path is required. When backend is
// "postgres", DSN is required.
type DatabaseConfig struct {
	Backend string `yaml:"backend"`
	Path    string `yaml:"path"`
	DSN     string `yaml:"dsn"`
}

// AuthConfig controls API authentication.
type AuthConfig struct {
	JWTSecret      string               `yaml:"jwt_secret"`
	TokenTTL       string               `yaml:"token_ttl"`
	BootstrapAdmin BootstrapAdminConfig `yaml:"bootstrap_admin"`
	LoginLockout   LoginLockoutConfig   `yaml:"login_lockout"`
}

// BootstrapAdminConfig configures one-time bootstrap admin creation.
type BootstrapAdminConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// LoginLockoutConfig configures admin login brute-force protection.
type LoginLockoutConfig struct {
	Window      string `yaml:"window"`
	MaxAttempts int    `yaml:"max_attempts"`
}

// MosquittoConfig controls MQTT broker connectivity.
type MosquittoConfig struct {
	Host                   string             `yaml:"host"`
	Port                   int                `yaml:"port"`
	Username               string             `yaml:"username"`
	Password               string             `yaml:"password"`
	TLS                    MosquittoTLSConfig `yaml:"tls"`
	Deploy                 DeployConfig       `yaml:"deploy"`
	SparkplugPayloadDecode bool               `yaml:"sparkplug_payload_decode"`
	SparkplugMaxMetrics    int                `yaml:"sparkplug_max_metrics"`
}

// DeployConfig controls how MCM writes Mosquitto ACL and password files and
// signals the broker to reload. Mode "file" writes files on the local
// filesystem; mode "docker" writes files and signals via docker exec.
type DeployConfig struct {
	Mode               string        `yaml:"mode"`
	ACLPath            string        `yaml:"acl_path"`
	PasswdPath         string        `yaml:"passwd_path"`
	PIDPath            string        `yaml:"pid_path"`
	ContainerName      string        `yaml:"container_name"`
	ReloadStrategy     string        `yaml:"reload_strategy"`
	HealthcheckTimeout time.Duration `yaml:"healthcheck_timeout"`
}

// MosquittoTLSConfig controls MQTT TLS connectivity.
type MosquittoTLSConfig struct {
	Enabled            bool   `yaml:"enabled"`
	CACertFile         string `yaml:"ca_cert_file"`
	ClientCertFile     string `yaml:"client_cert_file"`
	ClientKeyFile      string `yaml:"client_key_file"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
}

// MetricsConfig controls persisted operational event retention and access control.
type MetricsConfig struct {
	BrokerRetention   string `yaml:"broker_retention"`
	AuditRetention    string `yaml:"audit_retention"`
	SecurityRetention string `yaml:"security_retention"`
	// RequireAuth gates GET /metrics behind a valid admin JWT.
	// Set to false only when a dedicated scraping proxy handles authentication.
	// Defaults to true.
	RequireAuth bool `yaml:"require_auth"`
}

// StatusConfig controls access to GET /api/v1/status.
type StatusConfig struct {
	// RequireAuth gates GET /api/v1/status behind a valid admin JWT.
	// The dashboard always sends its session token so this is transparent
	// to frontend users. Set to false only for unauthenticated monitoring.
	// Defaults to true.
	RequireAuth bool `yaml:"require_auth"`
}

// AlertingConfig controls outbound operational webhook alerts.
type AlertingConfig struct {
	Enabled       bool   `yaml:"enabled"`
	EndpointURL   string `yaml:"endpoint_url"`
	Timeout       string `yaml:"timeout"`
	SigningSecret string `yaml:"signing_secret"`
}

// LoggingConfig controls log verbosity and output format.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// ValidationError holds all config validation failures.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("configuration validation failed:\n- %s", strings.Join(e.Problems, "\n- "))
}

// Default returns the default configuration values used in the example config.
func Default() Config {
	return Config{
		HTTP: HTTPConfig{
			BindAddress: "127.0.0.1",
			Port:        8080,
			TLS: HTTPTLSConfig{
				Enabled:    false,
				MinVersion: "1.2",
			},
		},
		Database: DatabaseConfig{
			Path: "var/lib/mcm/mcm.db",
		},
		Auth: AuthConfig{
			JWTSecret: "0123456789abcdef0123456789abcdef",
			TokenTTL:  "24h",
			BootstrapAdmin: BootstrapAdminConfig{
				Username: "admin",
				Password: "bootstrap-secret-password",
			},
			LoginLockout: LoginLockoutConfig{
				Window:      "15m",
				MaxAttempts: 6,
			},
		},
		Mosquitto: MosquittoConfig{
			Host: "127.0.0.1",
			Port: 1883,
			TLS: MosquittoTLSConfig{
				Enabled:            false,
				InsecureSkipVerify: false,
			},
		},
		Metrics: MetricsConfig{
			BrokerRetention:   "168h",
			AuditRetention:    "2160h",
			SecurityRetention: "2160h",
			RequireAuth:       true,
		},
		Status: StatusConfig{
			RequireAuth: true,
		},
		Alerting: AlertingConfig{
			Enabled: false,
			Timeout: "5s",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

// DefaultPath returns the default config path for the current user.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}

	return filepath.Join(dir, "mcm", fileName), nil
}

// ExampleYAML returns a documented example configuration file.
func ExampleYAML() string {
	cfg := Default()
	return renderYAML(cfg)
}

// InitYAML returns a runnable starter configuration for `mcm config init`.
func InitYAML() string {
	cfg := Default()
	cfg.Auth.JWTSecret = "0123456789abcdef0123456789abcdef"
	cfg.Auth.BootstrapAdmin = BootstrapAdminConfig{
		Username: "admin",
		Password: "bootstrap-secret-password",
	}
	return renderYAML(cfg)
}

func renderYAML(cfg Config) string {
	return fmt.Sprintf(`# MCM configuration
# HTTP listener configuration.
# Enable tls.enabled and point cert_file/key_file at PEM files to serve HTTPS.
# Set require_client_cert and client_ca_file to enforce mTLS.
http:
  bind_address: %q
  port: %d
  # Trusted reverse-proxy IPs/CIDRs. When the direct peer matches one of these,
  # X-Forwarded-For / X-Real-IP is used to determine the client IP (for rate-limit
  # lockout and audit). Empty = trust no headers and always use the peer address.
  trusted_proxies: []
  tls:
    enabled: %t
    cert_file: ""
    key_file: ""
    # Minimum TLS version: "1.2" (broad compatibility) or "1.3".
    min_version: %q
    client_ca_file: ""
    require_client_cert: false

# Database configuration. Backend: "sqlite" (default) or "postgres".
database:
  backend: "sqlite"
  path: %q
  # dsn: "postgres://user:pass@host:5432/mcm?sslmode=require"

# API authentication settings.
auth:
  jwt_secret: %q
  token_ttl: %q
  bootstrap_admin:
    username: %q
    password: %q
  # Admin login brute-force protection. When more than max_attempts failed
  # logins are recorded for the same source IP (or username) inside window,
  # additional attempts return HTTP 429 with a Retry-After header.
  login_lockout:
    window: %q
    max_attempts: %d

# Mosquitto connection settings.
# Prefer secret mounts, generated config, or deployment templating for credentials and TLS files in production.
# For production TLS, use a TLS listener (often port 8883), mount CA/client cert/key
# files read-only, and keep insecure_skip_verify false.
mosquitto:
  host: %q
  port: %d
  username: ""
  password: ""
  tls:
    enabled: %t
    ca_cert_file: ""
    client_cert_file: ""
    client_key_file: ""
    # Development-only escape hatch for self-signed testing; never enable in production.
    insecure_skip_verify: %t
  # deploy controls how MCM writes ACL/password files and signals the broker.
  # mode: "" (disabled), "file" (local filesystem + SIGHUP), "docker" (docker exec).
  # deploy:
  #   mode: file
  #   acl_path: /etc/mosquitto/acl
  #   passwd_path: /etc/mosquitto/passwd
  #   pid_path: /var/run/mosquitto/mosquitto.pid
  #   container_name: ""   # only required for docker mode

# Broker metric/event persistence. Raw message payloads are not stored.
# Audit and security event retention defaults to 90 days.
metrics:
  broker_retention: %q
  audit_retention: %q
  security_retention: %q

# Optional outbound webhook alerts for operational events.
# signing_secret enables the X-MCM-Signature HMAC-SHA256 header.
alerting:
  enabled: %t
  endpoint_url: ""
  timeout: %q
  signing_secret: ""

# Valid levels: debug, info, warn, error.
# Valid formats: json (default, recommended for production / SIEM ingestion) or text.
logging:
  level: %q
  format: %q
`, cfg.HTTP.BindAddress, cfg.HTTP.Port, cfg.HTTP.TLS.Enabled, cfg.HTTP.TLS.MinVersion, cfg.Database.Path, cfg.Auth.JWTSecret, cfg.Auth.TokenTTL, cfg.Auth.BootstrapAdmin.Username, cfg.Auth.BootstrapAdmin.Password, cfg.Auth.LoginLockout.Window, cfg.Auth.LoginLockout.MaxAttempts, cfg.Mosquitto.Host, cfg.Mosquitto.Port, cfg.Mosquitto.TLS.Enabled, cfg.Mosquitto.TLS.InsecureSkipVerify, cfg.Metrics.BrokerRetention, cfg.Metrics.AuditRetention, cfg.Metrics.SecurityRetention, cfg.Alerting.Enabled, cfg.Alerting.Timeout, cfg.Logging.Level, cfg.Logging.Format)
}

// Load reads and validates a configuration file from disk.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config file %q: %w", path, err)
	}

	cfg, err := Parse(data)
	if err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// Parse decodes and validates configuration YAML.
func Parse(data []byte) (Config, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config YAML: %w", err)
	}

	if cfg.Metrics.BrokerRetention == "" {
		cfg.Metrics.BrokerRetention = Default().Metrics.BrokerRetention
	}
	if cfg.Metrics.AuditRetention == "" {
		cfg.Metrics.AuditRetention = Default().Metrics.AuditRetention
	}
	if cfg.Metrics.SecurityRetention == "" {
		cfg.Metrics.SecurityRetention = Default().Metrics.SecurityRetention
	}
	if cfg.Mosquitto.Deploy.HealthcheckTimeout == 0 {
		cfg.Mosquitto.Deploy.HealthcheckTimeout = 5 * time.Second
	}
	if cfg.Mosquitto.SparkplugMaxMetrics <= 0 {
		cfg.Mosquitto.SparkplugMaxMetrics = 50
	}
	if cfg.Alerting.Timeout == "" {
		cfg.Alerting.Timeout = Default().Alerting.Timeout
	}
	if cfg.Auth.LoginLockout.Window == "" {
		cfg.Auth.LoginLockout.Window = Default().Auth.LoginLockout.Window
	}
	if cfg.Auth.LoginLockout.MaxAttempts == 0 {
		cfg.Auth.LoginLockout.MaxAttempts = Default().Auth.LoginLockout.MaxAttempts
	}
	if cfg.HTTP.TLS.MinVersion == "" {
		cfg.HTTP.TLS.MinVersion = Default().HTTP.TLS.MinVersion
	}
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = Default().Logging.Format
	}

	applyEnvOverrides(&cfg)

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// applyEnvOverrides overrides sensitive config fields from environment
// variables when set, so secrets can be injected at runtime instead of
// being baked into the mounted YAML.
func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("MCM_AUTH_JWT_SECRET"); v != "" {
		cfg.Auth.JWTSecret = v
	}
	if v := os.Getenv("MCM_BOOTSTRAP_ADMIN_USERNAME"); v != "" {
		cfg.Auth.BootstrapAdmin.Username = v
	}
	if v := os.Getenv("MCM_BOOTSTRAP_ADMIN_PASSWORD"); v != "" {
		cfg.Auth.BootstrapAdmin.Password = v
	}
}

// Validate checks whether the configuration is complete and internally consistent.
func (c Config) Validate() error {
	var problems []string

	if strings.TrimSpace(c.HTTP.BindAddress) == "" {
		problems = append(problems, "http.bind_address is required")
	}
	if err := validatePort("http.port", c.HTTP.Port); err != nil {
		problems = append(problems, err.Error())
	}
	for _, entry := range c.HTTP.TrustedProxies {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(trimmed); err == nil {
			continue
		}
		if net.ParseIP(trimmed) == nil {
			problems = append(problems, fmt.Sprintf("http.trusted_proxies entry %q must be an IP address or CIDR", trimmed))
		}
	}
	if c.HTTP.TLS.Enabled {
		if strings.TrimSpace(c.HTTP.TLS.CertFile) == "" {
			problems = append(problems, "http.tls.cert_file is required when http.tls.enabled is true")
		}
		if strings.TrimSpace(c.HTTP.TLS.KeyFile) == "" {
			problems = append(problems, "http.tls.key_file is required when http.tls.enabled is true")
		}
		if c.HTTP.TLS.RequireClientCert && strings.TrimSpace(c.HTTP.TLS.ClientCAFile) == "" {
			problems = append(problems, "http.tls.client_ca_file is required when http.tls.require_client_cert is true")
		}
	}
	switch strings.TrimSpace(c.HTTP.TLS.MinVersion) {
	case "1.2", "1.3":
	default:
		problems = append(problems, fmt.Sprintf(`http.tls.min_version must be "1.2" or "1.3"; got %q`, c.HTTP.TLS.MinVersion))
	}
	switch c.Database.Backend {
	case "", "sqlite":
		if strings.TrimSpace(c.Database.Path) == "" {
			problems = append(problems, "database.path is required when database.backend is \"sqlite\"")
		}
	case "postgres":
		if strings.TrimSpace(c.Database.DSN) == "" {
			problems = append(problems, "database.dsn is required when database.backend is \"postgres\"")
		}
	default:
		problems = append(problems, fmt.Sprintf(`database.backend must be "sqlite" or "postgres"; got %q`, c.Database.Backend))
	}
	if len(strings.TrimSpace(c.Auth.JWTSecret)) < 32 {
		problems = append(problems, "auth.jwt_secret must be at least 32 characters")
	} else if isInsecureDefaultSecret(c.Auth.JWTSecret) {
		problems = append(problems, "auth.jwt_secret must not use the insecure default placeholder")
	}
	if strings.TrimSpace(c.Auth.TokenTTL) == "" {
		problems = append(problems, "auth.token_ttl is required")
	} else if _, err := time.ParseDuration(c.Auth.TokenTTL); err != nil {
		problems = append(problems, fmt.Sprintf("auth.token_ttl must be a valid duration: %v", err))
	}
	if (c.Auth.BootstrapAdmin.Username == "") != (c.Auth.BootstrapAdmin.Password == "") {
		problems = append(problems, "auth.bootstrap_admin.username and auth.bootstrap_admin.password must both be set or both be empty")
	}
	if c.Auth.BootstrapAdmin.Username != "" && c.Auth.BootstrapAdmin.Password != "" {
		pw := strings.TrimSpace(c.Auth.BootstrapAdmin.Password)
		user := strings.TrimSpace(c.Auth.BootstrapAdmin.Username)
		switch {
		case isInsecureDefaultSecret(pw):
			problems = append(problems, "auth.bootstrap_admin.password must not use the insecure default placeholder")
		case isTrivialPassword(pw):
			problems = append(problems, "auth.bootstrap_admin.password is too common; choose a stronger password")
		case len(pw) < 8:
			problems = append(problems, "auth.bootstrap_admin.password must be at least 8 characters")
		case strings.EqualFold(pw, user):
			problems = append(problems, "auth.bootstrap_admin.password must not equal the username")
		}
	}
	if window, err := time.ParseDuration(c.Auth.LoginLockout.Window); err != nil {
		problems = append(problems, fmt.Sprintf("auth.login_lockout.window must be a valid duration: %v", err))
	} else if window <= 0 {
		problems = append(problems, "auth.login_lockout.window must be greater than zero")
	}
	if c.Auth.LoginLockout.MaxAttempts < 1 {
		problems = append(problems, "auth.login_lockout.max_attempts must be at least 1")
	}

	if strings.TrimSpace(c.Mosquitto.Host) == "" {
		problems = append(problems, "mosquitto.host is required")
	}
	if err := validatePort("mosquitto.port", c.Mosquitto.Port); err != nil {
		problems = append(problems, err.Error())
	}
	if (c.Mosquitto.Username == "") != (c.Mosquitto.Password == "") {
		problems = append(problems, "mosquitto.username and mosquitto.password must both be set or both be empty")
	}
	if c.Mosquitto.TLS.Enabled {
		if strings.TrimSpace(c.Mosquitto.TLS.CACertFile) == "" {
			problems = append(problems, "mosquitto.tls.ca_cert_file is required when mosquitto.tls.enabled is true")
		}
		if strings.TrimSpace(c.Mosquitto.TLS.ClientCertFile) == "" {
			problems = append(problems, "mosquitto.tls.client_cert_file is required when mosquitto.tls.enabled is true")
		}
		if strings.TrimSpace(c.Mosquitto.TLS.ClientKeyFile) == "" {
			problems = append(problems, "mosquitto.tls.client_key_file is required when mosquitto.tls.enabled is true")
		}
	}

	switch strings.TrimSpace(c.Mosquitto.Deploy.Mode) {
	case "":
		// deploy disabled — no further validation required
	case "file":
		if strings.TrimSpace(c.Mosquitto.Deploy.ACLPath) == "" {
			problems = append(problems, "mosquitto.deploy.acl_path is required when mosquitto.deploy.mode is \"file\"")
		}
		if strings.TrimSpace(c.Mosquitto.Deploy.PasswdPath) == "" {
			problems = append(problems, "mosquitto.deploy.passwd_path is required when mosquitto.deploy.mode is \"file\"")
		}
	case "docker":
		if strings.TrimSpace(c.Mosquitto.Deploy.ACLPath) == "" {
			problems = append(problems, "mosquitto.deploy.acl_path is required when mosquitto.deploy.mode is \"docker\"")
		}
		if strings.TrimSpace(c.Mosquitto.Deploy.PasswdPath) == "" {
			problems = append(problems, "mosquitto.deploy.passwd_path is required when mosquitto.deploy.mode is \"docker\"")
		}
		if strings.TrimSpace(c.Mosquitto.Deploy.ContainerName) == "" {
			problems = append(problems, "mosquitto.deploy.container_name is required when mosquitto.deploy.mode is \"docker\"")
		}
	default:
		problems = append(problems, fmt.Sprintf(`mosquitto.deploy.mode must be "", "file", or "docker"; got %q`, c.Mosquitto.Deploy.Mode))
	}
	if rs := strings.TrimSpace(c.Mosquitto.Deploy.ReloadStrategy); rs != "" && rs != "sighup" {
		problems = append(problems, fmt.Sprintf(`mosquitto.deploy.reload_strategy must be "" or "sighup"; got %q`, rs))
	}
	aclPath := strings.TrimSpace(c.Mosquitto.Deploy.ACLPath)
	passwdPath := strings.TrimSpace(c.Mosquitto.Deploy.PasswdPath)
	if aclPath != "" && passwdPath != "" && aclPath == passwdPath {
		problems = append(problems, "mosquitto.deploy.acl_path and mosquitto.deploy.passwd_path must not be the same path")
	}

	validatePositiveDuration := func(name string, value string) {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, fmt.Sprintf("%s is required", name))
		} else if retention, err := time.ParseDuration(value); err != nil {
			problems = append(problems, fmt.Sprintf("%s must be a valid duration: %v", name, err))
		} else if retention <= 0 {
			problems = append(problems, fmt.Sprintf("%s must be greater than zero", name))
		}
	}
	validatePositiveDuration("metrics.broker_retention", c.Metrics.BrokerRetention)
	validatePositiveDuration("metrics.audit_retention", c.Metrics.AuditRetention)
	validatePositiveDuration("metrics.security_retention", c.Metrics.SecurityRetention)

	if c.Alerting.Enabled && strings.TrimSpace(c.Alerting.EndpointURL) == "" {
		problems = append(problems, "alerting.endpoint_url is required when alerting.enabled is true")
	}
	if strings.TrimSpace(c.Alerting.Timeout) == "" {
		problems = append(problems, "alerting.timeout is required")
	} else if timeout, err := time.ParseDuration(c.Alerting.Timeout); err != nil {
		problems = append(problems, fmt.Sprintf("alerting.timeout must be a valid duration: %v", err))
	} else if timeout <= 0 {
		problems = append(problems, "alerting.timeout must be greater than zero")
	}

	level := strings.ToLower(strings.TrimSpace(c.Logging.Level))
	if level == "" {
		problems = append(problems, "logging.level is required")
	} else if _, ok := validLogLevels[level]; !ok {
		problems = append(problems, fmt.Sprintf("logging.level must be one of: debug, info, warn, error; got %q", c.Logging.Level))
	}
	format := strings.ToLower(strings.TrimSpace(c.Logging.Format))
	if _, ok := validLogFormats[format]; !ok {
		problems = append(problems, fmt.Sprintf(`logging.format must be one of: text, json; got %q`, c.Logging.Format))
	}

	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}

	return nil
}

func isInsecureDefaultSecret(value string) bool {
	_, ok := insecureDefaultSecrets[strings.ToLower(strings.TrimSpace(value))]
	return ok
}

func isTrivialPassword(value string) bool {
	_, ok := trivialPasswords[strings.ToLower(strings.TrimSpace(value))]
	return ok
}

func validatePort(name string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("%s must be between 1 and 65535; got %d", name, port)
	}
	return nil
}

// WriteExample writes the documented example config to path.
func WriteExample(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config directory %q: %w", dir, err)
	}

	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("config file %q already exists", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat config file %q: %w", path, err)
	}

	if err := os.WriteFile(path, []byte(InitYAML()), 0o600); err != nil {
		return fmt.Errorf("write config file %q: %w", path, err)
	}

	return nil
}
