package agent_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/agent"
)

func writeConfigFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

const validFullConfig = `
server:
  url: "https://mcm.example.com"
  username: "admin"
  password: "secret"
  token: ""
site:
  id: "site-abc"
  name: "Farm A"
mosquitto:
  host: "127.0.0.1"
  port: 1883
heartbeat:
  interval: "30s"
  timeout: "3s"
`

func TestLoadConfig(t *testing.T) {
	t.Run("valid full config loads successfully", func(t *testing.T) {
		path := writeConfigFile(t, validFullConfig)
		cfg, err := agent.LoadConfig(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Server.URL != "https://mcm.example.com" {
			t.Errorf("server.url = %q, want %q", cfg.Server.URL, "https://mcm.example.com")
		}
		if cfg.Site.ID != "site-abc" {
			t.Errorf("site.id = %q, want %q", cfg.Site.ID, "site-abc")
		}
		if cfg.Site.Name != "Farm A" {
			t.Errorf("site.name = %q, want %q", cfg.Site.Name, "Farm A")
		}
		if cfg.Mosquitto.Host != "127.0.0.1" {
			t.Errorf("mosquitto.host = %q, want %q", cfg.Mosquitto.Host, "127.0.0.1")
		}
		if cfg.Mosquitto.Port != 1883 {
			t.Errorf("mosquitto.port = %d, want %d", cfg.Mosquitto.Port, 1883)
		}
	})

	t.Run("missing site_id returns validation error", func(t *testing.T) {
		content := `
server:
  url: "https://mcm.example.com"
site:
  name: "Farm A"
`
		path := writeConfigFile(t, content)
		_, err := agent.LoadConfig(path)
		if err == nil {
			t.Fatal("expected error for missing site.id, got nil")
		}
		if !strings.Contains(err.Error(), "site.id") {
			t.Errorf("error should mention site.id, got: %v", err)
		}
	})

	t.Run("missing server.url returns validation error", func(t *testing.T) {
		content := `
site:
  id: "site-abc"
  name: "Farm A"
`
		path := writeConfigFile(t, content)
		_, err := agent.LoadConfig(path)
		if err == nil {
			t.Fatal("expected error for missing server.url, got nil")
		}
		if !strings.Contains(err.Error(), "server.url") {
			t.Errorf("error should mention server.url, got: %v", err)
		}
	})

	t.Run("empty config file returns error", func(t *testing.T) {
		path := writeConfigFile(t, "")
		_, err := agent.LoadConfig(path)
		if err == nil {
			t.Fatal("expected error for empty config, got nil")
		}
	})

	t.Run("default values applied when not specified", func(t *testing.T) {
		content := `
server:
  url: "https://mcm.example.com"
site:
  id: "site-abc"
  name: "Farm A"
`
		path := writeConfigFile(t, content)
		cfg, err := agent.LoadConfig(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Mosquitto.Host != "127.0.0.1" {
			t.Errorf("mosquitto.host default = %q, want %q", cfg.Mosquitto.Host, "127.0.0.1")
		}
		if cfg.Mosquitto.Port != 1883 {
			t.Errorf("mosquitto.port default = %d, want %d", cfg.Mosquitto.Port, 1883)
		}
		if cfg.Heartbeat.Interval != "60s" {
			t.Errorf("heartbeat.interval default = %q, want %q", cfg.Heartbeat.Interval, "60s")
		}
		if cfg.Heartbeat.Timeout != "5s" {
			t.Errorf("heartbeat.timeout default = %q, want %q", cfg.Heartbeat.Timeout, "5s")
		}
	})

	t.Run("env var MCM_AGENT_TOKEN overrides yaml token", func(t *testing.T) {
		content := `
server:
  url: "https://mcm.example.com"
  token: "yaml-token"
site:
  id: "site-abc"
  name: "Farm A"
`
		t.Setenv("MCM_AGENT_TOKEN", "env-token")
		path := writeConfigFile(t, content)
		cfg, err := agent.LoadConfig(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Server.Token != "env-token" {
			t.Errorf("server.token = %q, want %q (env override)", cfg.Server.Token, "env-token")
		}
	})

	t.Run("env var MCM_AGENT_SERVER_URL overrides yaml server.url", func(t *testing.T) {
		content := `
server:
  url: "https://yaml.example.com"
site:
  id: "site-abc"
  name: "Farm A"
`
		t.Setenv("MCM_AGENT_SERVER_URL", "https://env.example.com")
		path := writeConfigFile(t, content)
		cfg, err := agent.LoadConfig(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Server.URL != "https://env.example.com" {
			t.Errorf("server.url = %q, want %q (env override)", cfg.Server.URL, "https://env.example.com")
		}
	})

	t.Run("invalid port 0 returns error", func(t *testing.T) {
		content := `
server:
  url: "https://mcm.example.com"
site:
  id: "site-abc"
  name: "Farm A"
mosquitto:
  host: "127.0.0.1"
  port: 0
`
		path := writeConfigFile(t, content)
		_, err := agent.LoadConfig(path)
		if err == nil {
			t.Fatal("expected error for port 0, got nil")
		}
		if !strings.Contains(err.Error(), "mosquitto.port") {
			t.Errorf("error should mention mosquitto.port, got: %v", err)
		}
	})

	t.Run("invalid port >65535 returns error", func(t *testing.T) {
		content := `
server:
  url: "https://mcm.example.com"
site:
  id: "site-abc"
  name: "Farm A"
mosquitto:
  host: "127.0.0.1"
  port: 70000
`
		path := writeConfigFile(t, content)
		_, err := agent.LoadConfig(path)
		if err == nil {
			t.Fatal("expected error for port 70000, got nil")
		}
		if !strings.Contains(err.Error(), "mosquitto.port") {
			t.Errorf("error should mention mosquitto.port, got: %v", err)
		}
	})

	t.Run("invalid interval (unparseable) returns error", func(t *testing.T) {
		content := `
server:
  url: "https://mcm.example.com"
site:
  id: "site-abc"
  name: "Farm A"
heartbeat:
  interval: "notaduration"
  timeout: "5s"
`
		path := writeConfigFile(t, content)
		_, err := agent.LoadConfig(path)
		if err == nil {
			t.Fatal("expected error for invalid interval, got nil")
		}
		if !strings.Contains(err.Error(), "heartbeat.interval") {
			t.Errorf("error should mention heartbeat.interval, got: %v", err)
		}
	})

	t.Run("invalid interval (negative) returns error", func(t *testing.T) {
		content := `
server:
  url: "https://mcm.example.com"
site:
  id: "site-abc"
  name: "Farm A"
heartbeat:
  interval: "-10s"
  timeout: "5s"
`
		path := writeConfigFile(t, content)
		_, err := agent.LoadConfig(path)
		if err == nil {
			t.Fatal("expected error for negative interval, got nil")
		}
		if !strings.Contains(err.Error(), "heartbeat.interval") {
			t.Errorf("error should mention heartbeat.interval, got: %v", err)
		}
	})

	t.Run("env var MCM_AGENT_SITE_ID overrides yaml site.id", func(t *testing.T) {
		content := `
server:
  url: "https://mcm.example.com"
site:
  id: "yaml-site"
  name: "Farm A"
`
		t.Setenv("MCM_AGENT_SITE_ID", "env-site")
		path := writeConfigFile(t, content)
		cfg, err := agent.LoadConfig(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Site.ID != "env-site" {
			t.Errorf("site.id = %q, want %q (env override)", cfg.Site.ID, "env-site")
		}
	})

	t.Run("env var MCM_AGENT_USERNAME overrides yaml username", func(t *testing.T) {
		content := `
server:
  url: "https://mcm.example.com"
  username: "yaml-user"
  password: "yaml-pass"
site:
  id: "site-abc"
  name: "Farm A"
`
		t.Setenv("MCM_AGENT_USERNAME", "env-user")
		t.Setenv("MCM_AGENT_PASSWORD", "env-pass")
		path := writeConfigFile(t, content)
		cfg, err := agent.LoadConfig(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Server.Username != "env-user" {
			t.Errorf("server.username = %q, want %q (env override)", cfg.Server.Username, "env-user")
		}
		if cfg.Server.Password != "env-pass" {
			t.Errorf("server.password = %q, want %q (env override)", cfg.Server.Password, "env-pass")
		}
	})
}
