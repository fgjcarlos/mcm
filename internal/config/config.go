package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const fileName = "config.yaml"

var validLogLevels = map[string]struct{}{
	"debug": {},
	"info":  {},
	"warn":  {},
	"error": {},
}

// Config holds MCM runtime configuration loaded from YAML.
type Config struct {
	HTTP      HTTPConfig      `yaml:"http"`
	Database  DatabaseConfig  `yaml:"database"`
	Mosquitto MosquittoConfig `yaml:"mosquitto"`
	Logging   LoggingConfig   `yaml:"logging"`
}

// HTTPConfig controls the HTTP listener.
type HTTPConfig struct {
	BindAddress string `yaml:"bind_address"`
	Port        int    `yaml:"port"`
}

// DatabaseConfig controls SQLite persistence.
type DatabaseConfig struct {
	Path string `yaml:"path"`
}

// MosquittoConfig controls MQTT broker connectivity.
type MosquittoConfig struct {
	Host     string             `yaml:"host"`
	Port     int                `yaml:"port"`
	Username string             `yaml:"username"`
	Password string             `yaml:"password"`
	TLS      MosquittoTLSConfig `yaml:"tls"`
}

// MosquittoTLSConfig controls MQTT TLS connectivity.
type MosquittoTLSConfig struct {
	Enabled            bool   `yaml:"enabled"`
	CACertFile         string `yaml:"ca_cert_file"`
	ClientCertFile     string `yaml:"client_cert_file"`
	ClientKeyFile      string `yaml:"client_key_file"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
}

// LoggingConfig controls log verbosity.
type LoggingConfig struct {
	Level string `yaml:"level"`
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
		},
		Database: DatabaseConfig{
			Path: "var/lib/mcm/mcm.db",
		},
		Mosquitto: MosquittoConfig{
			Host: "127.0.0.1",
			Port: 1883,
			TLS: MosquittoTLSConfig{
				Enabled:            false,
				InsecureSkipVerify: false,
			},
		},
		Logging: LoggingConfig{
			Level: "info",
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

	return fmt.Sprintf(`# MCM configuration
# HTTP listener configuration.
http:
  bind_address: %q
  port: %d

# SQLite database location.
database:
  path: %q

# Mosquitto connection settings.
# Prefer environment overrides or secret mounts for credentials in production.
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
    insecure_skip_verify: %t

# Valid levels: debug, info, warn, error.
logging:
  level: %q
`, cfg.HTTP.BindAddress, cfg.HTTP.Port, cfg.Database.Path, cfg.Mosquitto.Host, cfg.Mosquitto.Port, cfg.Mosquitto.TLS.Enabled, cfg.Mosquitto.TLS.InsecureSkipVerify, cfg.Logging.Level)
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

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
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
	if strings.TrimSpace(c.Database.Path) == "" {
		problems = append(problems, "database.path is required")
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

	level := strings.ToLower(strings.TrimSpace(c.Logging.Level))
	if level == "" {
		problems = append(problems, "logging.level is required")
	} else if _, ok := validLogLevels[level]; !ok {
		problems = append(problems, fmt.Sprintf("logging.level must be one of: debug, info, warn, error; got %q", c.Logging.Level))
	}

	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}

	return nil
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

	if err := os.WriteFile(path, []byte(ExampleYAML()), 0o600); err != nil {
		return fmt.Errorf("write config file %q: %w", path, err)
	}

	return nil
}
