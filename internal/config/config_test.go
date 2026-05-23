package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseValidConfig(t *testing.T) {
	cfg, err := Parse([]byte(ExampleYAML()))
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
	if cfg.Alerting.Timeout != "5s" || cfg.Alerting.Enabled {
		t.Fatalf("unexpected alerting defaults: %+v", cfg.Alerting)
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
    password: change-this-admin-password
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
    password: change-this-admin-password
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
    password: change-this-admin-password
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
    password: change-this-admin-password
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
    password: change-this-admin-password
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
    password: change-this-admin-password
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
    password: change-this-admin-password
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
    password: change-this-admin-password
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
			name: "deploy file mode missing pid_path fails",
			deploy: `
  deploy:
    mode: file
    acl_path: /etc/mosquitto/acl
    passwd_path: /etc/mosquitto/passwd
    pid_path: ""`,
			wantOK:      false,
			wantProblem: "mosquitto.deploy.pid_path is required",
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
}
