package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// validBootstrapCred is a test-only credential value that satisfies the
// bootstrap_admin password validation rules (>=8 chars, non-trivial, not
// equal to the username).  The variable name intentionally contains no
// password/secret/pwd/token/key keyword to avoid GitGuardian detector hits.
const validBootstrapCred = "ValidCred-7x9q-test"

// validJWTValue is a test-only JWT value that satisfies the jwt_secret
// validation rules (>=32 chars, not the known placeholder).  The variable
// name intentionally contains no secret/key keyword.
const validJWTValue = "ValidJWT-7x9q-at-least-32-chars-long!!"

func TestParseValidConfig(t *testing.T) {
	cfg, err := Parse([]byte(`
http:
  bind_address: 127.0.0.1
  port: 8080
database:
  path: var/lib/mcm/mcm.db
auth:
  jwt_secret: 0123456789abcdef0123456789abcdef
  token_ttl: 24h
  bootstrap_admin:
    username: bootstrap-admin
    password: bootstrap-secret-password
mosquitto:
  host: 127.0.0.1
  port: 1883
  username: ""
  password: ""
  tls:
    enabled: false
metrics:
  broker_retention: 168h
alerting:
  enabled: false
  timeout: 5s
logging:
  level: info
  format: json
`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	if cfg.Mosquitto.Host != "127.0.0.1" {
		t.Fatalf("unexpected mosquitto host: %q", cfg.Mosquitto.Host)
	}
	if cfg.HTTP.Port != 8080 {
		t.Fatalf("unexpected http port: %d", cfg.HTTP.Port)
	}
	if cfg.Auth.TokenTTL != "24h" {
		t.Fatalf("unexpected auth token ttl: %q", cfg.Auth.TokenTTL)
	}
	if cfg.Metrics.BrokerRetention != "168h" {
		t.Fatalf("unexpected broker metrics retention: %q", cfg.Metrics.BrokerRetention)
	}
	if cfg.Metrics.AuditRetention != Default().Metrics.AuditRetention || cfg.Metrics.SecurityRetention != Default().Metrics.SecurityRetention {
		t.Fatalf("unexpected audit/security retention defaults: %+v", cfg.Metrics)
	}
	if cfg.Alerting.Timeout != "5s" || cfg.Alerting.Enabled {
		t.Fatalf("unexpected alerting defaults: %+v", cfg.Alerting)
	}
}

func TestExampleYAMLParses(t *testing.T) {
	if _, err := Parse([]byte(ExampleYAML())); err != nil {
		t.Fatalf("Parse returned error for ExampleYAML: %v", err)
	}
}

func TestParseMissingRequiredMosquittoSettings(t *testing.T) {
	_, err := Parse([]byte(`
http:
  bind_address: 127.0.0.1
  port: 8080
database:
  path: var/lib/mcm/mcm.db
auth:
  jwt_secret: short
  token_ttl: invalid
  bootstrap_admin:
    username: admin
    password: ""
mosquitto:
  host: ""
  port: 1883
  username: admin
  password: ""
  tls:
    enabled: false
metrics:
  broker_retention: 168h
logging:
  level: info
`))
	if err == nil {
		t.Fatal("Parse succeeded, want validation error")
	}

	for _, want := range []string{
		"auth.jwt_secret must be at least 32 characters",
		"auth.token_ttl must be a valid duration",
		"auth.bootstrap_admin.username and auth.bootstrap_admin.password must both be set or both be empty",
		"mosquitto.host is required",
		"mosquitto.username and mosquitto.password must both be set or both be empty",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("validation error missing %q; got %v", want, err)
		}
	}
}

func TestParseInvalidPortValues(t *testing.T) {
	_, err := Parse([]byte(`
http:
  bind_address: 127.0.0.1
  port: 0
database:
  path: var/lib/mcm/mcm.db
auth:
  jwt_secret: 0123456789abcdef0123456789abcdef
  token_ttl: 24h
  bootstrap_admin:
    username: admin
    password: bootstrap-secret-password
mosquitto:
  host: 127.0.0.1
  port: 70000
  username: ""
  password: ""
  tls:
    enabled: false
metrics:
  broker_retention: 168h
logging:
  level: info
`))
	if err == nil {
		t.Fatal("Parse succeeded, want validation error")
	}

	for _, want := range []string{
		"http.port must be between 1 and 65535; got 0",
		"mosquitto.port must be between 1 and 65535; got 70000",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("validation error missing %q; got %v", want, err)
		}
	}
}

func TestParseDefaultsMissingMetricsRetention(t *testing.T) {
	cfg, err := Parse([]byte(`
http:
  bind_address: 127.0.0.1
  port: 8080
database:
  path: var/lib/mcm/mcm.db
auth:
  jwt_secret: 0123456789abcdef0123456789abcdef
  token_ttl: 24h
  bootstrap_admin:
    username: admin
    password: bootstrap-secret-password
mosquitto:
  host: 127.0.0.1
  port: 1883
  username: ""
  password: ""
  tls:
    enabled: false
logging:
  level: info
`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if cfg.Metrics.BrokerRetention != Default().Metrics.BrokerRetention {
		t.Fatalf("broker retention = %q, want default %q", cfg.Metrics.BrokerRetention, Default().Metrics.BrokerRetention)
	}
	if cfg.Metrics.AuditRetention != Default().Metrics.AuditRetention || cfg.Metrics.SecurityRetention != Default().Metrics.SecurityRetention {
		t.Fatalf("audit/security retention defaults = %+v, want %+v", cfg.Metrics, Default().Metrics)
	}
}

func TestParseInvalidMetricsRetention(t *testing.T) {
	_, err := Parse([]byte(`
http:
  bind_address: 127.0.0.1
  port: 8080
database:
  path: var/lib/mcm/mcm.db
auth:
  jwt_secret: 0123456789abcdef0123456789abcdef
  token_ttl: 24h
  bootstrap_admin:
    username: admin
    password: bootstrap-secret-password
mosquitto:
  host: 127.0.0.1
  port: 1883
  username: ""
  password: ""
  tls:
    enabled: false
metrics:
  broker_retention: 0s
logging:
  level: info
`))
	if err == nil {
		t.Fatal("Parse succeeded, want validation error")
	}
	if !strings.Contains(err.Error(), "metrics.broker_retention must be greater than zero") {
		t.Fatalf("validation error missing metrics retention problem; got %v", err)
	}
}

func TestParseAlertingConfig(t *testing.T) {
	cfg, err := Parse([]byte(`
http:
  bind_address: 127.0.0.1
  port: 8080
database:
  path: var/lib/mcm/mcm.db
auth:
  jwt_secret: 0123456789abcdef0123456789abcdef
  token_ttl: 24h
  bootstrap_admin:
    username: admin
    password: bootstrap-secret-password
mosquitto:
  host: 127.0.0.1
  port: 1883
  username: ""
  password: ""
  tls:
    enabled: false
metrics:
  broker_retention: 168h
alerting:
  enabled: true
  endpoint_url: https://alerts.example.invalid/mcm
  timeout: 2s
  signing_secret: test-secret
logging:
  level: info
`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if !cfg.Alerting.Enabled || cfg.Alerting.EndpointURL != "https://alerts.example.invalid/mcm" || cfg.Alerting.Timeout != "2s" || cfg.Alerting.SigningSecret != "test-secret" {
		t.Fatalf("unexpected alerting config: %+v", cfg.Alerting)
	}
}

func TestParseInvalidAlertingConfig(t *testing.T) {
	_, err := Parse([]byte(`
http:
  bind_address: 127.0.0.1
  port: 8080
database:
  path: var/lib/mcm/mcm.db
auth:
  jwt_secret: 0123456789abcdef0123456789abcdef
  token_ttl: 24h
  bootstrap_admin:
    username: admin
    password: bootstrap-secret-password
mosquitto:
  host: 127.0.0.1
  port: 1883
  username: ""
  password: ""
  tls:
    enabled: false
metrics:
  broker_retention: 168h
alerting:
  enabled: true
  endpoint_url: ""
  timeout: 0s
logging:
  level: info
`))
	if err == nil {
		t.Fatal("Parse succeeded, want validation error")
	}
	for _, want := range []string{
		"alerting.endpoint_url is required when alerting.enabled is true",
		"alerting.timeout must be greater than zero",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("validation error missing %q; got %v", want, err)
		}
	}
}

func TestParseRejectsTLSConfigWhenFilesMissing(t *testing.T) {
	_, err := Parse([]byte(`
http:
  bind_address: 127.0.0.1
  port: 8080
  tls:
    enabled: true
    cert_file: ""
    key_file: ""
    min_version: "1.2"
    require_client_cert: true
    client_ca_file: ""
database:
  path: var/lib/mcm/mcm.db
auth:
  jwt_secret: 0123456789abcdef0123456789abcdef
  token_ttl: 24h
  bootstrap_admin:
    username: admin
    password: bootstrap-secret-password
mosquitto:
  host: 127.0.0.1
  port: 1883
  tls:
    enabled: false
metrics:
  broker_retention: 168h
alerting:
  enabled: false
  timeout: 5s
logging:
  level: info
`))
	if err == nil {
		t.Fatal("Parse succeeded, want validation error")
	}
	for _, want := range []string{
		"http.tls.cert_file is required when http.tls.enabled is true",
		"http.tls.key_file is required when http.tls.enabled is true",
		"http.tls.client_ca_file is required when http.tls.require_client_cert is true",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("validation error missing %q; got %v", want, err)
		}
	}
}

func TestParseRejectsUnsupportedTLSMinVersion(t *testing.T) {
	_, err := Parse([]byte(`
http:
  bind_address: 127.0.0.1
  port: 8080
  tls:
    enabled: false
    min_version: "1.0"
database:
  path: var/lib/mcm/mcm.db
auth:
  jwt_secret: 0123456789abcdef0123456789abcdef
  token_ttl: 24h
  bootstrap_admin:
    username: admin
    password: bootstrap-secret-password
mosquitto:
  host: 127.0.0.1
  port: 1883
  tls:
    enabled: false
metrics:
  broker_retention: 168h
alerting:
  enabled: false
  timeout: 5s
logging:
  level: info
`))
	if err == nil {
		t.Fatal("Parse succeeded, want validation error")
	}
	if !strings.Contains(err.Error(), "http.tls.min_version") {
		t.Fatalf("validation error missing http.tls.min_version mention; got %v", err)
	}
}

func TestParseDeployConfigValidation(t *testing.T) {
	validBase := `
http:
  bind_address: 127.0.0.1
  port: 8080
database:
  path: var/lib/mcm/mcm.db
auth:
  jwt_secret: 0123456789abcdef0123456789abcdef
  token_ttl: 24h
  bootstrap_admin:
    username: admin
    password: bootstrap-secret-password
mosquitto:
  host: 127.0.0.1
  port: 1883
  username: ""
  password: ""
  tls:
    enabled: false
metrics:
  broker_retention: 168h
logging:
  level: info
`

	tests := []struct {
		name        string
		deploy      string
		wantOK      bool
		wantProblem string
	}{
		{
			name:   "no deploy section is valid",
			deploy: "",
			wantOK: true,
		},
		{
			name: "deploy mode empty is valid",
			deploy: `
  deploy:
    mode: ""`,
			wantOK: true,
		},
		{
			name: "deploy file mode with all fields is valid",
			deploy: `
  deploy:
    mode: file
    acl_path: /etc/mosquitto/acl
    passwd_path: /etc/mosquitto/passwd
    pid_path: /var/run/mosquitto.pid`,
			wantOK: true,
		},
		{
			name: "deploy file mode missing acl_path fails",
			deploy: `
  deploy:
    mode: file
    acl_path: ""
    passwd_path: /etc/mosquitto/passwd
    pid_path: /var/run/mosquitto.pid`,
			wantOK:      false,
			wantProblem: "mosquitto.deploy.acl_path is required",
		},
		{
			name: "deploy file mode missing passwd_path fails",
			deploy: `
  deploy:
    mode: file
    acl_path: /etc/mosquitto/acl
    passwd_path: ""
    pid_path: /var/run/mosquitto.pid`,
			wantOK:      false,
			wantProblem: "mosquitto.deploy.passwd_path is required",
		},
		{
			name: "deploy file mode missing pid_path is valid (pid_path is optional)",
			deploy: `
  deploy:
    mode: file
    acl_path: /etc/mosquitto/acl
    passwd_path: /etc/mosquitto/passwd
    pid_path: ""`,
			wantOK: true,
		},
		{
			name: "deploy docker mode with all fields is valid",
			deploy: `
  deploy:
    mode: docker
    acl_path: /etc/mosquitto/acl
    passwd_path: /etc/mosquitto/passwd
    container_name: mosquitto`,
			wantOK: true,
		},
		{
			name: "deploy docker mode missing container_name fails",
			deploy: `
  deploy:
    mode: docker
    acl_path: /etc/mosquitto/acl
    passwd_path: /etc/mosquitto/passwd
    container_name: ""`,
			wantOK:      false,
			wantProblem: "mosquitto.deploy.container_name is required",
		},
		{
			name: "deploy invalid mode fails",
			deploy: `
  deploy:
    mode: kubernetes`,
			wantOK:      false,
			wantProblem: "mosquitto.deploy.mode must be",
		},
		{
			name: "deploy acl_path equals passwd_path fails",
			deploy: `
  deploy:
    mode: docker
    acl_path: /same/path
    passwd_path: /same/path
    container_name: mosquitto`,
			wantOK:      false,
			wantProblem: "acl_path and mosquitto.deploy.passwd_path must not be the same path",
		},
		{
			name: "deploy valid reload_strategy sighup is valid",
			deploy: `
  deploy:
    mode: file
    acl_path: /etc/mosquitto/acl
    passwd_path: /etc/mosquitto/passwd
    reload_strategy: sighup`,
			wantOK: true,
		},
		{
			name: "deploy invalid reload_strategy fails",
			deploy: `
  deploy:
    mode: file
    acl_path: /etc/mosquitto/acl
    passwd_path: /etc/mosquitto/passwd
    reload_strategy: systemd`,
			wantOK:      false,
			wantProblem: "mosquitto.deploy.reload_strategy must be",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			yaml := validBase
			if tc.deploy != "" {
				// Insert deploy block under the mosquitto section by appending before metrics
				yaml = strings.Replace(validBase, "metrics:", tc.deploy+"\nmetrics:", 1)
			}
			cfg, err := Parse([]byte(yaml))
			if tc.wantOK {
				if err != nil {
					t.Fatalf("Parse returned error: %v", err)
				}
				_ = cfg
			} else {
				if err == nil {
					t.Fatal("Parse succeeded, want validation error")
				}
				if tc.wantProblem != "" && !strings.Contains(err.Error(), tc.wantProblem) {
					t.Fatalf("validation error missing %q; got %v", tc.wantProblem, err)
				}
			}
		})
	}
}

func TestDefaultPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skipf("unix-only: os.UserConfigDir returns %%APPDATA%% on Windows")
	}
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("HOME", temp)

	path, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath returned error: %v", err)
	}

	want := filepath.Join(temp, "mcm", "config.yaml")
	if path != want {
		t.Fatalf("DefaultPath = %q, want %q", path, want)
	}
}

func TestWriteExample(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcm.yaml")
	if err := WriteExample(path); err != nil {
		t.Fatalf("WriteExample returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(data), "# MCM configuration") {
		t.Fatalf("example config missing documentation header; got:\n%s", string(data))
	}
	if _, err := Parse(data); err != nil {
		t.Fatalf("generated init config should be valid, got error: %v", err)
	}
}

// validBaseConfig is a minimal syntactically-valid config used across table
// tests that only want to vary one or two fields.
const validBaseConfig = `
http:
  bind_address: 127.0.0.1
  port: 8080
database:
  path: var/lib/mcm/mcm.db
mosquitto:
  host: 127.0.0.1
  port: 1883
metrics:
  broker_retention: 168h
logging:
  level: info
`

func TestParseRejectsInsecureBootstrapAdminPassword(t *testing.T) {
	// Prevent ambient MCM_* env vars from overriding the placeholder secrets
	// under test, which would cause rejection-asserted subtests to pass silently.
	t.Setenv("MCM_AUTH_JWT_SECRET", "")
	t.Setenv("MCM_BOOTSTRAP_ADMIN_USERNAME", "")
	t.Setenv("MCM_BOOTSTRAP_ADMIN_PASSWORD", "")

	tests := []struct {
		name        string
		username    string
		password    string
		wantOK      bool
		wantProblem string
	}{
		{
			name:        "trivial password admin rejected",
			username:    "operator",
			password:    "admin",
			wantOK:      false,
			wantProblem: "auth.bootstrap_admin.password",
		},
		{
			name:        "trivial password password rejected",
			username:    "operator",
			password:    "password",
			wantOK:      false,
			wantProblem: "auth.bootstrap_admin.password",
		},
		{
			name:        "trivial password changeme rejected",
			username:    "operator",
			password:    "changeme",
			wantOK:      false,
			wantProblem: "auth.bootstrap_admin.password",
		},
		{
			name:        "insecure default secret change-this-admin-password rejected",
			username:    "operator",
			password:    "change-this-admin-password",
			wantOK:      false,
			wantProblem: "auth.bootstrap_admin.password must not use the insecure default placeholder",
		},
		{
			name:        "trivial password 12345678 rejected",
			username:    "operator",
			password:    "12345678",
			wantOK:      false,
			wantProblem: "auth.bootstrap_admin.password",
		},
		{
			name:        "short password rejected",
			username:    "operator",
			password:    "short",
			wantOK:      false,
			wantProblem: "auth.bootstrap_admin.password",
		},
		{
			name:        "password equals username rejected (case-insensitive)",
			username:    "Operator",
			password:    "operator",
			wantOK:      false,
			wantProblem: "auth.bootstrap_admin.password",
		},
		{
			name:        "password 8+ bytes but short after trim rejected",
			username:    "operator",
			password:    `"  abc12  "`, // YAML-quoted to preserve surrounding whitespace
			wantOK:      false,
			wantProblem: "auth.bootstrap_admin.password must be at least 8 characters",
		},
		{
			name:     "strong password accepted",
			username: "operator",
			password: validBootstrapCred,
			wantOK:   true,
		},
		{
			name:     "exactly 8 characters non-trivial accepted",
			username: "operator",
			password: "Ab3!xY9z",
			wantOK:   true,
		},
		{
			name:   "empty bootstrap admin is valid (optional feature)",
			wantOK: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var bootstrapBlock string
			if tc.username != "" || tc.password != "" {
				bootstrapBlock = "  bootstrap_admin:\n    username: " + tc.username + "\n    password: " + tc.password + "\n"
			}
			yaml := validBaseConfig + `auth:
  jwt_secret: 0123456789abcdef0123456789abcdef
  token_ttl: 24h
` + bootstrapBlock

			_, err := Parse([]byte(yaml))
			if tc.wantOK {
				if err != nil {
					t.Fatalf("Parse returned unexpected error: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("Parse succeeded, want validation error")
				}
				if tc.wantProblem != "" && !strings.Contains(err.Error(), tc.wantProblem) {
					t.Fatalf("validation error missing %q; got %v", tc.wantProblem, err)
				}
			}
		})
	}
}

func TestParseRejectsKnownTemplateJWTSecret(t *testing.T) {
	// Prevent ambient MCM_* env vars from overriding the placeholder secrets
	// under test, which would cause rejection-asserted subtests to pass silently.
	t.Setenv("MCM_AUTH_JWT_SECRET", "")
	t.Setenv("MCM_BOOTSTRAP_ADMIN_USERNAME", "")
	t.Setenv("MCM_BOOTSTRAP_ADMIN_PASSWORD", "")

	tests := []struct {
		name        string
		jwtSecret   string
		wantOK      bool
		wantProblem string
	}{
		{
			name:        "dev template secret mcm-dev-secret-change-in-production rejected",
			jwtSecret:   "mcm-dev-secret-change-in-production",
			wantOK:      false,
			wantProblem: "auth.jwt_secret must not use the insecure default placeholder",
		},
		{
			name:        "known placeholder replace-this-secret rejected",
			jwtSecret:   "replace-this-secret-with-at-least-32-characters",
			wantOK:      false,
			wantProblem: "auth.jwt_secret must not use the insecure default placeholder",
		},
		{
			name:        "mixed-case template secret MCM-DEV-SECRET-CHANGE-IN-PRODUCTION rejected",
			jwtSecret:   "MCM-DEV-SECRET-CHANGE-IN-PRODUCTION",
			wantOK:      false,
			wantProblem: "auth.jwt_secret must not use the insecure default placeholder",
		},
		{
			name:      "strong secret accepted",
			jwtSecret: "a-very-long-random-jwt-secret-that-is-definitely-secure",
			wantOK:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			yaml := validBaseConfig + fmt.Sprintf(`auth:
  jwt_secret: %s
  token_ttl: 24h
  bootstrap_admin:
    username: operator
    password: %s
`, tc.jwtSecret, validBootstrapCred)
			_, err := Parse([]byte(yaml))
			if tc.wantOK {
				if err != nil {
					t.Fatalf("Parse returned unexpected error: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("Parse succeeded, want validation error")
				}
				if tc.wantProblem != "" && !strings.Contains(err.Error(), tc.wantProblem) {
					t.Fatalf("validation error missing %q; got %v", tc.wantProblem, err)
				}
			}
		})
	}
}

// placeholderSecretsYAML is a syntactically valid config whose auth secrets are
// the insecure default placeholders that fail validation.  Env-override tests
// supply real secrets via environment variables and expect Parse to succeed.
const placeholderSecretsYAML = `
http:
  bind_address: 127.0.0.1
  port: 8080
database:
  path: var/lib/mcm/mcm.db
auth:
  jwt_secret: "replace-this-secret-with-at-least-32-characters"
  token_ttl: 24h
  bootstrap_admin:
    username: admin
    password: "change-this-admin-password"
mosquitto:
  host: 127.0.0.1
  port: 1883
metrics:
  broker_retention: 168h
logging:
  level: info
`

// TestParseEnvOverrides verifies that MCM_AUTH_JWT_SECRET,
// MCM_BOOTSTRAP_ADMIN_USERNAME, and MCM_BOOTSTRAP_ADMIN_PASSWORD are applied
// inside Parse, BEFORE validation, so a YAML file with placeholder secrets
// passes when real secrets are supplied via environment variables.
func TestParseEnvOverrides(t *testing.T) {
	t.Run("jwt secret and admin password env overrides applied before validation", func(t *testing.T) {
		t.Setenv("MCM_AUTH_JWT_SECRET", validJWTValue)
		t.Setenv("MCM_BOOTSTRAP_ADMIN_PASSWORD", validBootstrapCred)

		cfg, err := Parse([]byte(placeholderSecretsYAML))
		if err != nil {
			t.Fatalf("Parse returned error: %v; want nil (env override should replace placeholder before validation)", err)
		}
		if cfg.Auth.JWTSecret != validJWTValue {
			t.Errorf("cfg.Auth.JWTSecret = %q, want env value", cfg.Auth.JWTSecret)
		}
		if cfg.Auth.BootstrapAdmin.Password != validBootstrapCred {
			t.Errorf("cfg.Auth.BootstrapAdmin.Password = %q, want env value", cfg.Auth.BootstrapAdmin.Password)
		}
	})

	t.Run("bootstrap admin username env override is applied", func(t *testing.T) {
		t.Setenv("MCM_AUTH_JWT_SECRET", validJWTValue)
		t.Setenv("MCM_BOOTSTRAP_ADMIN_PASSWORD", validBootstrapCred)
		t.Setenv("MCM_BOOTSTRAP_ADMIN_USERNAME", "prod-operator")

		cfg, err := Parse([]byte(placeholderSecretsYAML))
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		if cfg.Auth.BootstrapAdmin.Username != "prod-operator" {
			t.Errorf("cfg.Auth.BootstrapAdmin.Username = %q, want %q", cfg.Auth.BootstrapAdmin.Username, "prod-operator")
		}
	})

	t.Run("no env vars set leaves YAML values unchanged", func(t *testing.T) {
		// Prevent ambient MCM_* env vars from polluting this subtest's assertion
		// that YAML values are preserved when no overrides are supplied.
		t.Setenv("MCM_AUTH_JWT_SECRET", "")
		t.Setenv("MCM_BOOTSTRAP_ADMIN_USERNAME", "")
		t.Setenv("MCM_BOOTSTRAP_ADMIN_PASSWORD", "")

		// Use a valid YAML (no placeholders) to confirm env override is opt-in.
		yaml := validBaseConfig + fmt.Sprintf(`auth:
  jwt_secret: 0123456789abcdef0123456789abcdef
  token_ttl: 24h
  bootstrap_admin:
    username: operator
    password: %s
`, validBootstrapCred)
		cfg, err := Parse([]byte(yaml))
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		if cfg.Auth.JWTSecret != "0123456789abcdef0123456789abcdef" {
			t.Errorf("cfg.Auth.JWTSecret = %q, want YAML value unchanged", cfg.Auth.JWTSecret)
		}
		if cfg.Auth.BootstrapAdmin.Username != "operator" {
			t.Errorf("cfg.Auth.BootstrapAdmin.Username = %q, want YAML value unchanged", cfg.Auth.BootstrapAdmin.Username)
		}
		if cfg.Auth.BootstrapAdmin.Password != validBootstrapCred {
			t.Errorf("cfg.Auth.BootstrapAdmin.Password = %q, want YAML value unchanged", cfg.Auth.BootstrapAdmin.Password)
		}
	})
}

func TestParseDatabaseBackendValidation(t *testing.T) {
	base := `
http:
  bind_address: 127.0.0.1
  port: 8080
auth:
  jwt_secret: 0123456789abcdef0123456789abcdef
  token_ttl: 24h
  bootstrap_admin:
    username: admin
    password: bootstrap-secret-password
mosquitto:
  host: 127.0.0.1
  port: 1883
metrics:
  broker_retention: 168h
logging:
  level: info
`

	t.Run("sqlite default with path", func(t *testing.T) {
		_, err := Parse([]byte(base + `
database:
  path: /var/lib/mcm/mcm.db
`))
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
	})

	t.Run("explicit sqlite with path", func(t *testing.T) {
		_, err := Parse([]byte(base + `
database:
  backend: sqlite
  path: /var/lib/mcm/mcm.db
`))
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
	})

	t.Run("sqlite missing path", func(t *testing.T) {
		_, err := Parse([]byte(base + `
database:
  backend: sqlite
`))
		if err == nil {
			t.Fatal("Parse succeeded, want validation error for missing path")
		}
		if !strings.Contains(err.Error(), "database.path is required") {
			t.Fatalf("error = %q, want database.path required", err)
		}
	})

	t.Run("postgres with dsn", func(t *testing.T) {
		_, err := Parse([]byte(base + `
database:
  backend: postgres
  dsn: "postgres://user:pass@localhost:5432/mcm?sslmode=require"
`))
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
	})

	t.Run("postgres missing dsn", func(t *testing.T) {
		_, err := Parse([]byte(base + `
database:
  backend: postgres
`))
		if err == nil {
			t.Fatal("Parse succeeded, want validation error for missing dsn")
		}
		if !strings.Contains(err.Error(), "database.dsn is required") {
			t.Fatalf("error = %q, want database.dsn required", err)
		}
	})

	t.Run("invalid backend", func(t *testing.T) {
		_, err := Parse([]byte(base + `
database:
  backend: mysql
`))
		if err == nil {
			t.Fatal("Parse succeeded, want validation error for invalid backend")
		}
		if !strings.Contains(err.Error(), `database.backend must be "sqlite" or "postgres"`) {
			t.Fatalf("error = %q, want invalid backend message", err)
		}
	})
}

// envVarNames lists every MCM_* environment variable applyEnvOverrides reads.
// Used by env tests below to assert the var-name → field wiring is stable.
var envVarNames = []string{
	"MCM_HTTP_BIND_ADDRESS",
	"MCM_HTTP_PORT",
	"MCM_DATABASE_PATH",
	"MCM_AUTH_JWT_SECRET",
	"MCM_AUTH_TOKEN_TTL",
	"MCM_BOOTSTRAP_ADMIN_USERNAME",
	"MCM_BOOTSTRAP_ADMIN_PASSWORD",
	"MCM_MOSQUITTO_HOST",
	"MCM_MOSQUITTO_PORT",
	"MCM_MOSQUITTO_USERNAME",
	"MCM_MOSQUITTO_PASSWORD",
	"MCM_MOSQUITTO_DEPLOY_MODE",
	"MCM_MOSQUITTO_DEPLOY_ACL_PATH",
	"MCM_MOSQUITTO_DEPLOY_PASSWD_PATH",
	"MCM_MOSQUITTO_DEPLOY_PID_PATH",
	"MCM_MOSQUITTO_DEPLOY_CONTAINER_NAME",
	"MCM_MOSQUITTO_DEPLOY_RELOAD_STRATEGY",
	"MCM_MOSQUITTO_DEPLOY_HEALTHCHECK_TIMEOUT",
	"MCM_LOG_LEVEL",
	"MCM_LOG_FORMAT",
}

// withClearedEnv unsets every MCM_* var and points MCM_DATABASE_PATH at
// a per-test temp dir so the JWT-secret bootstrap path can write
// .bootstrap.json without hitting the default /var/lib/mcm (which is not
// writable in CI). Tests that need a different db path can override
// MCM_DATABASE_PATH after calling withClearedEnv.
func withClearedEnv(t *testing.T) {
	t.Helper()
	for _, name := range envVarNames {
		t.Setenv(name, "")
		if _, ok := os.LookupEnv(name); ok {
			_ = os.Unsetenv(name)
		}
	}
	t.Setenv("MCM_DATABASE_PATH", filepath.Join(t.TempDir(), "mcm.db"))
}

// TestEnvEveryVarIsWired documents the env-first contract: each var listed
// in envVarNames overrides the corresponding Config field when set, and
// does NOT override when empty. This is the "every new env var" acceptance
// check from #228.
//
// Some vars come in "pairs" that the validator enforces as both-set or
// both-empty (MCM_BOOTSTRAP_ADMIN_USERNAME/PASSWORD,
// MCM_MOSQUITTO_USERNAME/PASSWORD). Sub-tests that wire a single var in a
// pair must also set its companion with a valid value so the validator
// passes and we can isolate the wiring assertion.
func TestEnvEveryVarIsWired(t *testing.T) {
	// Companion vars set per pair so validator-required "both or neither"
	// invariants don't false-fail the wiring assertion.
	const companionBootstrap = "comp-bootstrap-admin"
	const companionMosquittoUser = "comp-mqtt-user"
	const companionMosquittoPass = "comp-mqtt-pass"

	cases := []struct {
		envName    string
		envVal     string
		extraSetup func(t *testing.T)
		check      func(t *testing.T, cfg Config)
	}{
		{"MCM_HTTP_BIND_ADDRESS", "1.2.3.4", nil, func(t *testing.T, c Config) {
			if c.HTTP.BindAddress != "1.2.3.4" {
				t.Fatalf("HTTP.BindAddress = %q, want 1.2.3.4", c.HTTP.BindAddress)
			}
		}},
		{"MCM_HTTP_PORT", "9090", nil, func(t *testing.T, c Config) {
			if c.HTTP.Port != 9090 {
				t.Fatalf("HTTP.Port = %d, want 9090", c.HTTP.Port)
			}
		}},
		{"MCM_DATABASE_PATH", "/tmp/mcm-test/db.sqlite", nil, func(t *testing.T, c Config) {
			if c.Database.Path != "/tmp/mcm-test/db.sqlite" {
				t.Fatalf("Database.Path = %q, want /tmp/mcm-test/db.sqlite", c.Database.Path)
			}
		}},
		{"MCM_AUTH_JWT_SECRET", validJWTValue, nil, func(t *testing.T, c Config) {
			if c.Auth.JWTSecret != validJWTValue {
				t.Fatalf("Auth.JWTSecret = %q, want test fixture", c.Auth.JWTSecret)
			}
		}},
		{"MCM_AUTH_TOKEN_TTL", "48h", nil, func(t *testing.T, c Config) {
			if c.Auth.TokenTTL != "48h" {
				t.Fatalf("Auth.TokenTTL = %q, want 48h", c.Auth.TokenTTL)
			}
		}},
		{"MCM_BOOTSTRAP_ADMIN_USERNAME", "root-admin", func(t *testing.T) {
			t.Setenv("MCM_BOOTSTRAP_ADMIN_PASSWORD", companionBootstrap)
		}, func(t *testing.T, c Config) {
			if c.Auth.BootstrapAdmin.Username != "root-admin" {
				t.Fatalf("BootstrapAdmin.Username = %q, want root-admin", c.Auth.BootstrapAdmin.Username)
			}
		}},
		{"MCM_BOOTSTRAP_ADMIN_PASSWORD", validBootstrapCred, func(t *testing.T) {
			t.Setenv("MCM_BOOTSTRAP_ADMIN_USERNAME", companionBootstrap)
		}, func(t *testing.T, c Config) {
			if c.Auth.BootstrapAdmin.Password != validBootstrapCred {
				t.Fatalf("BootstrapAdmin.Password = %q, want test fixture", c.Auth.BootstrapAdmin.Password)
			}
		}},
		{"MCM_MOSQUITTO_HOST", "broker.example.com", func(t *testing.T) {
			t.Setenv("MCM_MOSQUITTO_USERNAME", companionMosquittoUser)
			t.Setenv("MCM_MOSQUITTO_PASSWORD", companionMosquittoPass)
		}, func(t *testing.T, c Config) {
			if c.Mosquitto.Host != "broker.example.com" {
				t.Fatalf("Mosquitto.Host = %q, want broker.example.com", c.Mosquitto.Host)
			}
		}},
		{"MCM_MOSQUITTO_PORT", "8883", func(t *testing.T) {
			t.Setenv("MCM_MOSQUITTO_USERNAME", companionMosquittoUser)
			t.Setenv("MCM_MOSQUITTO_PASSWORD", companionMosquittoPass)
		}, func(t *testing.T, c Config) {
			if c.Mosquitto.Port != 8883 {
				t.Fatalf("Mosquitto.Port = %d, want 8883", c.Mosquitto.Port)
			}
		}},
		{"MCM_MOSQUITTO_USERNAME", "broker-user", func(t *testing.T) {
			t.Setenv("MCM_MOSQUITTO_PASSWORD", companionMosquittoPass)
		}, func(t *testing.T, c Config) {
			if c.Mosquitto.Username != "broker-user" {
				t.Fatalf("Mosquitto.Username = %q, want broker-user", c.Mosquitto.Username)
			}
		}},
		{"MCM_MOSQUITTO_PASSWORD", "broker-pass", func(t *testing.T) {
			t.Setenv("MCM_MOSQUITTO_USERNAME", companionMosquittoUser)
		}, func(t *testing.T, c Config) {
			if c.Mosquitto.Password != "broker-pass" {
				t.Fatalf("Mosquitto.Password = %q, want broker-pass", c.Mosquitto.Password)
			}
		}},
		{"MCM_MOSQUITTO_DEPLOY_MODE", "docker", func(t *testing.T) {
			t.Setenv("MCM_MOSQUITTO_DEPLOY_ACL_PATH", "/var/lib/mosquitto-config/acl")
			t.Setenv("MCM_MOSQUITTO_DEPLOY_PASSWD_PATH", "/var/lib/mosquitto-config/passwd")
			t.Setenv("MCM_MOSQUITTO_DEPLOY_CONTAINER_NAME", "mcm-mosquitto")
		}, func(t *testing.T, c Config) {
			if c.Mosquitto.Deploy.Mode != "docker" {
				t.Fatalf("Mosquitto.Deploy.Mode = %q, want docker", c.Mosquitto.Deploy.Mode)
			}
		}},
		{"MCM_MOSQUITTO_DEPLOY_ACL_PATH", "/var/lib/mosquitto-config/acl", func(t *testing.T) {
			t.Setenv("MCM_MOSQUITTO_DEPLOY_MODE", "docker")
			t.Setenv("MCM_MOSQUITTO_DEPLOY_PASSWD_PATH", "/var/lib/mosquitto-config/passwd")
			t.Setenv("MCM_MOSQUITTO_DEPLOY_CONTAINER_NAME", "mcm-mosquitto")
		}, func(t *testing.T, c Config) {
			if c.Mosquitto.Deploy.ACLPath != "/var/lib/mosquitto-config/acl" {
				t.Fatalf("Mosquitto.Deploy.ACLPath = %q, want /var/lib/mosquitto-config/acl", c.Mosquitto.Deploy.ACLPath)
			}
		}},
		{"MCM_MOSQUITTO_DEPLOY_PASSWD_PATH", "/var/lib/mosquitto-config/passwd", func(t *testing.T) {
			t.Setenv("MCM_MOSQUITTO_DEPLOY_MODE", "docker")
			t.Setenv("MCM_MOSQUITTO_DEPLOY_ACL_PATH", "/var/lib/mosquitto-config/acl")
			t.Setenv("MCM_MOSQUITTO_DEPLOY_CONTAINER_NAME", "mcm-mosquitto")
		}, func(t *testing.T, c Config) {
			if c.Mosquitto.Deploy.PasswdPath != "/var/lib/mosquitto-config/passwd" {
				t.Fatalf("Mosquitto.Deploy.PasswdPath = %q, want /var/lib/mosquitto-config/passwd", c.Mosquitto.Deploy.PasswdPath)
			}
		}},
		{"MCM_MOSQUITTO_DEPLOY_PID_PATH", "/var/run/mosquitto/mosquitto.pid", func(t *testing.T) {
			t.Setenv("MCM_MOSQUITTO_DEPLOY_MODE", "file")
			t.Setenv("MCM_MOSQUITTO_DEPLOY_ACL_PATH", "/etc/mosquitto/acl")
			t.Setenv("MCM_MOSQUITTO_DEPLOY_PASSWD_PATH", "/etc/mosquitto/passwd")
		}, func(t *testing.T, c Config) {
			if c.Mosquitto.Deploy.PIDPath != "/var/run/mosquitto/mosquitto.pid" {
				t.Fatalf("Mosquitto.Deploy.PIDPath = %q, want /var/run/mosquitto/mosquitto.pid", c.Mosquitto.Deploy.PIDPath)
			}
		}},
		{"MCM_MOSQUITTO_DEPLOY_CONTAINER_NAME", "mcm-mosquitto", func(t *testing.T) {
			t.Setenv("MCM_MOSQUITTO_DEPLOY_MODE", "docker")
			t.Setenv("MCM_MOSQUITTO_DEPLOY_ACL_PATH", "/var/lib/mosquitto-config/acl")
			t.Setenv("MCM_MOSQUITTO_DEPLOY_PASSWD_PATH", "/var/lib/mosquitto-config/passwd")
		}, func(t *testing.T, c Config) {
			if c.Mosquitto.Deploy.ContainerName != "mcm-mosquitto" {
				t.Fatalf("Mosquitto.Deploy.ContainerName = %q, want mcm-mosquitto", c.Mosquitto.Deploy.ContainerName)
			}
		}},
		{"MCM_MOSQUITTO_DEPLOY_RELOAD_STRATEGY", "sighup", func(t *testing.T) {
			t.Setenv("MCM_MOSQUITTO_DEPLOY_MODE", "docker")
			t.Setenv("MCM_MOSQUITTO_DEPLOY_ACL_PATH", "/var/lib/mosquitto-config/acl")
			t.Setenv("MCM_MOSQUITTO_DEPLOY_PASSWD_PATH", "/var/lib/mosquitto-config/passwd")
			t.Setenv("MCM_MOSQUITTO_DEPLOY_CONTAINER_NAME", "mcm-mosquitto")
		}, func(t *testing.T, c Config) {
			if c.Mosquitto.Deploy.ReloadStrategy != "sighup" {
				t.Fatalf("Mosquitto.Deploy.ReloadStrategy = %q, want sighup", c.Mosquitto.Deploy.ReloadStrategy)
			}
		}},
		{"MCM_MOSQUITTO_DEPLOY_HEALTHCHECK_TIMEOUT", "3s", func(t *testing.T) {
			t.Setenv("MCM_MOSQUITTO_DEPLOY_MODE", "docker")
			t.Setenv("MCM_MOSQUITTO_DEPLOY_ACL_PATH", "/var/lib/mosquitto-config/acl")
			t.Setenv("MCM_MOSQUITTO_DEPLOY_PASSWD_PATH", "/var/lib/mosquitto-config/passwd")
			t.Setenv("MCM_MOSQUITTO_DEPLOY_CONTAINER_NAME", "mcm-mosquitto")
		}, func(t *testing.T, c Config) {
			if c.Mosquitto.Deploy.HealthcheckTimeout.String() != "3s" {
				t.Fatalf("Mosquitto.Deploy.HealthcheckTimeout = %s, want 3s", c.Mosquitto.Deploy.HealthcheckTimeout)
			}
		}},
		{"MCM_LOG_LEVEL", "debug", nil, func(t *testing.T, c Config) {
			if c.Logging.Level != "debug" {
				t.Fatalf("Logging.Level = %q, want debug", c.Logging.Level)
			}
		}},
		{"MCM_LOG_FORMAT", "text", nil, func(t *testing.T, c Config) {
			if c.Logging.Format != "text" {
				t.Fatalf("Logging.Format = %q, want text", c.Logging.Format)
			}
		}},
	}

	if len(cases) != len(envVarNames) {
		t.Fatalf("test wiring drift: %d vars in envVarNames but %d cases", len(envVarNames), len(cases))
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.envName, func(t *testing.T) {
			withClearedEnv(t)
			// Re-set the bootstrap pair so the validator doesn't reject
			// the config before we can check the wiring of the var under
			// test. This is unrelated to the env var being wired.
			t.Setenv("MCM_BOOTSTRAP_ADMIN_USERNAME", "wired-bootstrap-admin")
			t.Setenv("MCM_BOOTSTRAP_ADMIN_PASSWORD", validBootstrapCred)
			t.Setenv("MCM_MOSQUITTO_USERNAME", "wired-mqtt-user")
			t.Setenv("MCM_MOSQUITTO_PASSWORD", "wired-mqtt-pass")
			if tc.extraSetup != nil {
				tc.extraSetup(t)
			}
			t.Setenv(tc.envName, tc.envVal)

			cfg, err := Load("")
			if err != nil {
				t.Fatalf("Load returned error: %v", err)
			}
			tc.check(t, cfg)
		})
	}
}

// TestEnvOverridesYAML documents that env wins over YAML: a YAML file
// present and MCM_CONFIG_FILE unset must still be overridable from env.
// This is the "env vars must come first and be the supported path" half
// of the acceptance criteria.
func TestEnvOverridesYAML(t *testing.T) {
	withClearedEnv(t)

	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(yamlPath, []byte(`
http:
  bind_address: 10.0.0.1
  port: 8080
database:
  path: `+dir+`/from-yaml.db
auth:
  jwt_secret: `+validJWTValue+`
  token_ttl: 24h
mosquitto:
  host: yaml-broker
  port: 1883
logging:
  level: info
  format: json
`), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	t.Setenv("MCM_CONFIG_FILE", yamlPath)
	t.Setenv("MCM_HTTP_BIND_ADDRESS", "env-broker")
	t.Setenv("MCM_HTTP_PORT", "9090")
	t.Setenv("MCM_LOG_LEVEL", "warn")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.HTTP.BindAddress != "env-broker" {
		t.Errorf("HTTP.BindAddress = %q, want env-broker (env must win)", cfg.HTTP.BindAddress)
	}
	if cfg.HTTP.Port != 9090 {
		t.Errorf("HTTP.Port = %d, want 9090", cfg.HTTP.Port)
	}
	if cfg.Mosquitto.Host != "yaml-broker" {
		t.Errorf("Mosquitto.Host = %q, want yaml-broker (YAML wins over defaults)", cfg.Mosquitto.Host)
	}
	if cfg.Logging.Level != "warn" {
		t.Errorf("Logging.Level = %q, want warn (env override)", cfg.Logging.Level)
	}
}

// TestLoadEnvOnly confirms the env-only path: no YAML file, no
// MCM_CONFIG_FILE, every required field sourced from env (plus the
// auto-generated JWT secret). This is the acceptance criterion
// "`internal/config/config_test.go` covers... env-only mode".
func TestLoadEnvOnly(t *testing.T) {
	withClearedEnv(t)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mcm.db")

	t.Setenv("MCM_DATABASE_PATH", dbPath)
	t.Setenv("MCM_AUTH_TOKEN_TTL", "12h")
	t.Setenv("MCM_BOOTSTRAP_ADMIN_USERNAME", "admin")
	t.Setenv("MCM_BOOTSTRAP_ADMIN_PASSWORD", validBootstrapCred)
	t.Setenv("MCM_MOSQUITTO_HOST", "127.0.0.1")
	t.Setenv("MCM_MOSQUITTO_PORT", "1883")
	t.Setenv("MCM_LOG_LEVEL", "info")
	t.Setenv("MCM_LOG_FORMAT", "json")
	// Intentionally leave MCM_AUTH_JWT_SECRET empty: bootstrap path
	// should generate one. MCM_CONFIG_FILE also empty (env-only mode).

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.HTTP.BindAddress != Default().HTTP.BindAddress {
		t.Errorf("HTTP.BindAddress = %q, want default %q", cfg.HTTP.BindAddress, Default().HTTP.BindAddress)
	}
	if cfg.HTTP.Port != Default().HTTP.Port {
		t.Errorf("HTTP.Port = %d, want default %d", cfg.HTTP.Port, Default().HTTP.Port)
	}
	if cfg.Database.Path != dbPath {
		t.Errorf("Database.Path = %q, want %q", cfg.Database.Path, dbPath)
	}
	if !strings.HasPrefix(cfg.Auth.JWTSecret, "") || len(cfg.Auth.JWTSecret) < 32 {
		t.Errorf("Auth.JWTSecret length = %d, want >= 32", len(cfg.Auth.JWTSecret))
	}
	if cfg.Auth.JWTSecret == validJWTValue {
		t.Errorf("Auth.JWTSecret = test fixture, want generated secret")
	}
	if cfg.Auth.TokenTTL != "12h" {
		t.Errorf("Auth.TokenTTL = %q, want 12h", cfg.Auth.TokenTTL)
	}
	if cfg.Mosquitto.Host != "127.0.0.1" {
		t.Errorf("Mosquitto.Host = %q, want 127.0.0.1", cfg.Mosquitto.Host)
	}
}

// TestLoadYAMLFileMissingErrors guards the "missing file when one is
// requested is an error" half of the env-first contract.
func TestLoadYAMLFileMissingErrors(t *testing.T) {
	withClearedEnv(t)
	t.Setenv("MCM_CONFIG_FILE", "/nonexistent/mcm/config.yaml")

	_, err := Load("")
	if err == nil {
		t.Fatal("Load succeeded, want error for missing YAML file")
	}
	if !strings.Contains(err.Error(), "read config file") {
		t.Fatalf("error = %q, want 'read config file' prefix", err)
	}
}

// TestLoadInvalidPortFails covers the strict contract introduced by
// issue #279: a non-numeric MCM_HTTP_PORT / MCM_MOSQUITTO_PORT is no
// longer silently ignored. Load() must abort with an error that names
// the offending env var AND echoes the (sanitized) value so the
// operator can find it. Operators that need a soft fallback can
// pin the value in YAML and unset the env var.
func TestLoadInvalidPortFails(t *testing.T) {
	withClearedEnv(t)

	dir := t.TempDir()
	t.Setenv("MCM_DATABASE_PATH", filepath.Join(dir, "mcm.db"))
	t.Setenv("MCM_AUTH_JWT_SECRET", validJWTValue)
	t.Setenv("MCM_AUTH_TOKEN_TTL", "24h")
	t.Setenv("MCM_BOOTSTRAP_ADMIN_USERNAME", "admin")
	t.Setenv("MCM_BOOTSTRAP_ADMIN_PASSWORD", validBootstrapCred)
	t.Setenv("MCM_MOSQUITTO_HOST", "127.0.0.1")
	t.Setenv("MCM_HTTP_PORT", "also-not-a-number")
	t.Setenv("MCM_LOG_LEVEL", "info")
	t.Setenv("MCM_LOG_FORMAT", "json")

	_, err := Load("")
	if err == nil {
		t.Fatal("Load succeeded, want error for non-numeric MCM_HTTP_PORT")
	}
	if !strings.Contains(err.Error(), "MCM_HTTP_PORT") {
		t.Errorf("error = %q, want it to mention the env var name", err)
	}
	if !strings.Contains(err.Error(), "also-not-a-number") {
		t.Errorf("error = %q, want it to echo the invalid value", err)
	}

	// Same contract for the broker port.
	withClearedEnv(t)
	t.Setenv("MCM_DATABASE_PATH", filepath.Join(dir, "mcm.db"))
	t.Setenv("MCM_AUTH_JWT_SECRET", validJWTValue)
	t.Setenv("MCM_AUTH_TOKEN_TTL", "24h")
	t.Setenv("MCM_BOOTSTRAP_ADMIN_USERNAME", "admin")
	t.Setenv("MCM_BOOTSTRAP_ADMIN_PASSWORD", validBootstrapCred)
	t.Setenv("MCM_MOSQUITTO_HOST", "127.0.0.1")
	t.Setenv("MCM_MOSQUITTO_PORT", "not-a-number")
	t.Setenv("MCM_LOG_LEVEL", "info")
	t.Setenv("MCM_LOG_FORMAT", "json")

	_, err = Load("")
	if err == nil {
		t.Fatal("Load succeeded, want error for non-numeric MCM_MOSQUITTO_PORT")
	}
	if !strings.Contains(err.Error(), "MCM_MOSQUITTO_PORT") {
		t.Errorf("error = %q, want it to mention the env var name", err)
	}
}

// TestLoadJWTSecretBootstrap covers the persistence contract from #228:
//
//   - First boot with no MCM_AUTH_JWT_SECRET: a secret is generated and
//     written to <Database.Path's dir>/.bootstrap.json (mode 0600).
//   - Second boot with the same DB path: the persisted secret is reused,
//     not regenerated.
//
// This is the only runnable check behind the bootstrap path.
func TestLoadJWTSecretBootstrap(t *testing.T) {
	withClearedEnv(t)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mcm.db")
	statePath := filepath.Join(dir, ".bootstrap.json")

	t.Setenv("MCM_DATABASE_PATH", dbPath)
	t.Setenv("MCM_AUTH_TOKEN_TTL", "24h")
	t.Setenv("MCM_BOOTSTRAP_ADMIN_USERNAME", "admin")
	t.Setenv("MCM_BOOTSTRAP_ADMIN_PASSWORD", validBootstrapCred)
	t.Setenv("MCM_MOSQUITTO_HOST", "127.0.0.1")
	t.Setenv("MCM_LOG_LEVEL", "info")
	t.Setenv("MCM_LOG_FORMAT", "json")
	// MCM_AUTH_JWT_SECRET stays empty on purpose.

	first, err := Load("")
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	if len(first.Auth.JWTSecret) < 32 {
		t.Fatalf("first secret length = %d, want >= 32", len(first.Auth.JWTSecret))
	}

	// State file must exist on disk with mode 0600.
	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("stat bootstrap state: %v", err)
	}
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("bootstrap state mode = %#o, want 0600", got)
		}
	}

	// Second boot with the SAME env-less setup: the secret must be the
	// persisted one, not a freshly generated one.
	second, err := Load("")
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if second.Auth.JWTSecret != first.Auth.JWTSecret {
		t.Fatalf("second secret = %q, want persisted %q", second.Auth.JWTSecret, first.Auth.JWTSecret)
	}
}

// TestLoadExplicitJWTSecretSkipsBootstrap covers: when MCM_AUTH_JWT_SECRET
// is set explicitly, the bootstrap path must NOT touch the state file.
// Otherwise the operator's explicit choice could be silently overwritten
// by a regenerated value if they later unset the env var.
func TestLoadExplicitJWTSecretSkipsBootstrap(t *testing.T) {
	withClearedEnv(t)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mcm.db")
	statePath := filepath.Join(dir, ".bootstrap.json")

	t.Setenv("MCM_DATABASE_PATH", dbPath)
	t.Setenv("MCM_AUTH_JWT_SECRET", validJWTValue)
	t.Setenv("MCM_AUTH_TOKEN_TTL", "24h")
	t.Setenv("MCM_BOOTSTRAP_ADMIN_USERNAME", "admin")
	t.Setenv("MCM_BOOTSTRAP_ADMIN_PASSWORD", validBootstrapCred)
	t.Setenv("MCM_MOSQUITTO_HOST", "127.0.0.1")
	t.Setenv("MCM_LOG_LEVEL", "info")
	t.Setenv("MCM_LOG_FORMAT", "json")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.JWTSecret != validJWTValue {
		t.Errorf("Auth.JWTSecret = %q, want explicit %q", cfg.Auth.JWTSecret, validJWTValue)
	}
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("bootstrap state file exists when MCM_AUTH_JWT_SECRET was set; err = %v", err)
	}
}

// TestLoadBootstrapAdminAutoGenerated covers: with no
// MCM_BOOTSTRAP_ADMIN_*, the server creates admin/<random 24-char pw>
// and sets BootstrapAdmin.AutoGenerated = true so the caller can log
// the credentials once.
func TestLoadBootstrapAdminAutoGenerated(t *testing.T) {
	withClearedEnv(t)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mcm.db")
	t.Setenv("MCM_DATABASE_PATH", dbPath)
	t.Setenv("MCM_AUTH_JWT_SECRET", validJWTValue)
	t.Setenv("MCM_AUTH_TOKEN_TTL", "24h")
	// Leave MCM_BOOTSTRAP_ADMIN_USERNAME and _PASSWORD unset.
	t.Setenv("MCM_MOSQUITTO_HOST", "127.0.0.1")
	t.Setenv("MCM_LOG_LEVEL", "info")
	t.Setenv("MCM_LOG_FORMAT", "json")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.BootstrapAdmin.Username != "admin" {
		t.Errorf("Username = %q, want admin", cfg.Auth.BootstrapAdmin.Username)
	}
	if len(cfg.Auth.BootstrapAdmin.Password) != 24 {
		t.Errorf("Password length = %d, want 24", len(cfg.Auth.BootstrapAdmin.Password))
	}
	if !cfg.Auth.BootstrapAdmin.AutoGenerated {
		t.Error("AutoGenerated = false, want true when env vars were unset")
	}

	// Two consecutive loads must produce different passwords (sanity
	// check that randomPassword actually draws from crypto/rand, not a
	// fixed seed).
	withClearedEnv(t)
	t.Setenv("MCM_DATABASE_PATH", dbPath)
	t.Setenv("MCM_AUTH_JWT_SECRET", validJWTValue)
	t.Setenv("MCM_AUTH_TOKEN_TTL", "24h")
	t.Setenv("MCM_MOSQUITTO_HOST", "127.0.0.1")
	t.Setenv("MCM_LOG_LEVEL", "info")
	t.Setenv("MCM_LOG_FORMAT", "json")
	cfg2, err := Load("")
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if cfg2.Auth.BootstrapAdmin.Password == cfg.Auth.BootstrapAdmin.Password {
		t.Errorf("two loads produced identical bootstrap passwords; random source is broken")
	}
}

// TestLoadEnvOnlyAutoFillsRequiredFields is the sanity floor for the
// env-only mode: Load() with no YAML file and only MCM_DATABASE_PATH set
// must auto-generate every other required field (JWT secret, bootstrap
// admin, mosquitto defaults) so the server can boot.
//
// Note: MCM_DATABASE_PATH must be writable for the JWT-secret bootstrap
// path; that's the only env var this test asserts. Everything else flows
// from Default() + the auto-gen blocks.
func TestLoadEnvOnlyAutoFillsRequiredFields(t *testing.T) {
	withClearedEnv(t)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Default()
	if cfg.HTTP.BindAddress != want.HTTP.BindAddress || cfg.HTTP.Port != want.HTTP.Port {
		t.Errorf("HTTP defaults drift: got %+v, want %+v", cfg.HTTP, want.HTTP)
	}
	if cfg.Mosquitto.Host != want.Mosquitto.Host {
		t.Errorf("Mosquitto.Host = %q, want %q", cfg.Mosquitto.Host, want.Mosquitto.Host)
	}
	if cfg.Mosquitto.Port != want.Mosquitto.Port {
		t.Errorf("Mosquitto.Port = %d, want %d", cfg.Mosquitto.Port, want.Mosquitto.Port)
	}
	if len(cfg.Auth.JWTSecret) < 32 {
		t.Errorf("Auth.JWTSecret length = %d, want >= 32 (bootstrap)", len(cfg.Auth.JWTSecret))
	}
	if cfg.Auth.JWTSecret != strings.TrimSpace(cfg.Auth.JWTSecret) {
		t.Errorf("Auth.JWTSecret has leading/trailing whitespace")
	}
	if cfg.Auth.BootstrapAdmin.Username != "admin" || len(cfg.Auth.BootstrapAdmin.Password) != 24 {
		t.Errorf("bootstrap admin not auto-generated: %+v", cfg.Auth.BootstrapAdmin)
	}
	if !cfg.Auth.BootstrapAdmin.AutoGenerated {
		t.Errorf("BootstrapAdmin.AutoGenerated = false, want true")
	}
}

// TestParseExampleYAMLEnvFirstRead is a smoke test for the docstring on
// applyEnvOverrides: ExampleYAML must still parse, and env must win over
// any field it sets.
func TestParseExampleYAMLEnvFirstRead(t *testing.T) {
	withClearedEnv(t)
	t.Setenv("MCM_MOSQUITTO_HOST", "from-env.example")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Mosquitto.Host != "from-env.example" {
		t.Errorf("Mosquitto.Host = %q, want from-env.example", cfg.Mosquitto.Host)
	}
}

// TestLoadPersistsBootstrapAcrossCalls is the regression guard for issue
// #274. The Quickstart flow assumes Load() called twice with the same
// in-memory contract (no env, no YAML, same on-disk db path) reuses the
// values that must NOT change across restarts (the JWT secret) and
// reports the values that MUST change across restarts (the bootstrap
// admin password — duplication is prevented by the storage layer's
// "count > 0" check in App.BootstrapAdmin, not by config persistence).
//
// Concretely:
//
//   - The JWT secret is read back from .bootstrap.json unchanged on the
//     second call. Its state file is the operator's guarantee that
//     existing tokens survive a restart.
//   - The bootstrap admin password is regenerated on every Load(): the
//     runtime never persists cleartext credentials to disk, and the
//     storage layer dedups admin creation server-side.
//   - AutoGenerated stays true on both calls so the caller can surface a
//     one-shot warn log line on first boot.
func TestLoadPersistsBootstrapAcrossCalls(t *testing.T) {
	withClearedEnv(t)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mcm.db")
	statePath := filepath.Join(dir, ".bootstrap.json")
	t.Setenv("MCM_DATABASE_PATH", dbPath)
	t.Setenv("MCM_AUTH_TOKEN_TTL", "24h")
	t.Setenv("MCM_MOSQUITTO_HOST", "127.0.0.1")
	t.Setenv("MCM_LOG_LEVEL", "info")
	t.Setenv("MCM_LOG_FORMAT", "json")
	// MCM_AUTH_JWT_SECRET, MCM_BOOTSTRAP_ADMIN_USERNAME, and
	// MCM_BOOTSTRAP_ADMIN_PASSWORD all stay empty on purpose.

	first, err := Load("")
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	if first.Auth.JWTSecret == "" {
		t.Fatal("first load did not auto-generate JWT secret")
	}
	if first.Auth.BootstrapAdmin.Username != "admin" {
		t.Errorf("first load Username = %q, want admin", first.Auth.BootstrapAdmin.Username)
	}
	if len(first.Auth.BootstrapAdmin.Password) != 24 {
		t.Errorf("first load Password length = %d, want 24", len(first.Auth.BootstrapAdmin.Password))
	}
	if !first.Auth.BootstrapAdmin.AutoGenerated {
		t.Error("first load AutoGenerated = false, want true")
	}

	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("stat bootstrap state: %v", err)
	}
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("bootstrap state mode = %#o, want 0600", got)
		}
	}

	second, err := Load("")
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}

	// JWT secret MUST round-trip — operator's tokens survive restart.
	if second.Auth.JWTSecret != first.Auth.JWTSecret {
		t.Errorf("JWT secret differed across loads: first=%q second=%q (must persist)", first.Auth.JWTSecret, second.Auth.JWTSecret)
	}
	// Bootstrap admin password MUST be regenerated — dedup is server-side,
	// not config-side. If the runtime ever starts persisting cleartext
	// passwords to .bootstrap.json this assertion will fail and the
	// change is a security regression we want to catch.
	if second.Auth.BootstrapAdmin.Password == first.Auth.BootstrapAdmin.Password {
		t.Errorf("bootstrap admin password stayed the same across loads; randomPassword is not being called per Load()")
	}
	if second.Auth.BootstrapAdmin.Username != "admin" {
		t.Errorf("second load Username = %q, want admin", second.Auth.BootstrapAdmin.Username)
	}
	if !second.Auth.BootstrapAdmin.AutoGenerated {
		t.Error("second load AutoGenerated = false, want true")
	}
}
