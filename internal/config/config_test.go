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
}

func TestParseMissingRequiredMosquittoSettings(t *testing.T) {
	_, err := Parse([]byte(`
http:
  bind_address: 127.0.0.1
  port: 8080
database:
  path: var/lib/mcm/mcm.db
mosquitto:
  host: ""
  port: 1883
  username: admin
  password: ""
  tls:
    enabled: false
logging:
  level: info
`))
	if err == nil {
		t.Fatal("Parse succeeded, want validation error")
	}

	for _, want := range []string{
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
mosquitto:
  host: 127.0.0.1
  port: 70000
  username: ""
  password: ""
  tls:
    enabled: false
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
