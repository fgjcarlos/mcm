package agent

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// AgentConfig holds the edge agent runtime configuration.
type AgentConfig struct {
	Server    ServerConfig    `yaml:"server"`
	Site      SiteConfig      `yaml:"site"`
	Mosquitto MosquittoConfig `yaml:"mosquitto"`
	Heartbeat HeartbeatConfig `yaml:"heartbeat"`
}

// ServerConfig holds connection parameters for the MCM server.
type ServerConfig struct {
	URL      string `yaml:"url"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Token    string `yaml:"token"`
}

// SiteConfig identifies this edge installation.
type SiteConfig struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
}

// MosquittoConfig holds local broker connection parameters.
type MosquittoConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// HeartbeatConfig controls how often the agent reports health to the server.
type HeartbeatConfig struct {
	Interval string `yaml:"interval"`
	Timeout  string `yaml:"timeout"`
}

// AgentValidationError holds all config validation failures.
type AgentValidationError struct {
	Problems []string
}

func (e *AgentValidationError) Error() string {
	return fmt.Sprintf("agent configuration validation failed:\n- %s", strings.Join(e.Problems, "\n- "))
}

// LoadConfig reads a YAML config file from path, applies environment variable
// overrides, and validates the result.
func LoadConfig(path string) (AgentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return AgentConfig{}, fmt.Errorf("read agent config %q: %w", path, err)
	}

	if len(bytes.TrimSpace(data)) == 0 {
		return AgentConfig{}, fmt.Errorf("agent config %q is empty", path)
	}

	// Pre-seed with defaults so fields not present in YAML keep their defaults,
	// but fields explicitly set in YAML (even to zero) will override them.
	cfg := defaultAgentConfig()

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	if err := dec.Decode(&cfg); err != nil {
		return AgentConfig{}, fmt.Errorf("parse agent config YAML: %w", err)
	}

	applyEnvOverrides(&cfg)

	if err := cfg.Validate(); err != nil {
		return AgentConfig{}, err
	}

	return cfg, nil
}

// defaultAgentConfig returns a config pre-seeded with default values.
// Fields not present in YAML will keep these defaults; fields explicitly set in
// YAML (even to zero or empty) will override them after decoding.
func defaultAgentConfig() AgentConfig {
	return AgentConfig{
		Mosquitto: MosquittoConfig{
			Host: "127.0.0.1",
			Port: 1883,
		},
		Heartbeat: HeartbeatConfig{
			Interval: "60s",
			Timeout:  "5s",
		},
	}
}

// applyEnvOverrides overrides config fields from environment variables when set.
func applyEnvOverrides(cfg *AgentConfig) {
	if v := os.Getenv("MCM_AGENT_SERVER_URL"); v != "" {
		cfg.Server.URL = v
	}
	if v := os.Getenv("MCM_AGENT_TOKEN"); v != "" {
		cfg.Server.Token = v
	}
	if v := os.Getenv("MCM_AGENT_SITE_ID"); v != "" {
		cfg.Site.ID = v
	}
	if v := os.Getenv("MCM_AGENT_USERNAME"); v != "" {
		cfg.Server.Username = v
	}
	if v := os.Getenv("MCM_AGENT_PASSWORD"); v != "" {
		cfg.Server.Password = v
	}
}

// Validate checks whether the configuration is complete and internally consistent.
func (c AgentConfig) Validate() error {
	var problems []string

	if strings.TrimSpace(c.Server.URL) == "" {
		problems = append(problems, "server.url is required")
	}

	if strings.TrimSpace(c.Site.ID) == "" {
		problems = append(problems, "site.id is required")
	}
	if strings.TrimSpace(c.Site.Name) == "" {
		problems = append(problems, "site.name is required")
	}

	if c.Mosquitto.Port < 1 || c.Mosquitto.Port > 65535 {
		problems = append(problems, fmt.Sprintf("mosquitto.port must be between 1 and 65535; got %d", c.Mosquitto.Port))
	}

	if interval, err := time.ParseDuration(c.Heartbeat.Interval); err != nil {
		problems = append(problems, fmt.Sprintf("heartbeat.interval must be a valid duration: %v", err))
	} else if interval <= 0 {
		problems = append(problems, "heartbeat.interval must be greater than zero")
	}

	if timeout, err := time.ParseDuration(c.Heartbeat.Timeout); err != nil {
		problems = append(problems, fmt.Sprintf("heartbeat.timeout must be a valid duration: %v", err))
	} else if timeout <= 0 {
		problems = append(problems, "heartbeat.timeout must be greater than zero")
	}

	if len(problems) > 0 {
		return &AgentValidationError{Problems: problems}
	}
	return nil
}
