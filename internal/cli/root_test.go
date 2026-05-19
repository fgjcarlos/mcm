package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/config"
)

func executeForTest(args ...string) (string, error) {
	cmd := NewRootCommand(VersionInfo{
		Version: "test-version",
		Commit:  "test-commit",
		Date:    "test-date",
	})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	_, err := cmd.ExecuteC()
	return buf.String(), err
}

func TestVersionCommandPrintsBuildInformation(t *testing.T) {
	output, err := executeForTest("version")
	if err != nil {
		t.Fatalf("version command returned error: %v", err)
	}

	for _, want := range []string{
		"MCM",
		"version: test-version",
		"commit: test-commit",
		"date: test-date",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("version output missing %q; got:\n%s", want, output)
		}
	}
}

func TestRootHelpListsInitialCommands(t *testing.T) {
	output, err := executeForTest("--help")
	if err != nil {
		t.Fatalf("help command returned error: %v", err)
	}

	for _, want := range []string{
		"server",
		"doctor",
		"status",
		"config",
		"version",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("help output missing %q; got:\n%s", want, output)
		}
	}
}

func TestConfigHelpListsSubcommands(t *testing.T) {
	output, err := executeForTest("config", "--help")
	if err != nil {
		t.Fatalf("config help returned error: %v", err)
	}

	for _, want := range []string{
		"init",
		"validate",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("config help output missing %q; got:\n%s", want, output)
		}
	}
}

func TestPlaceholderCommandsReturnNotImplemented(t *testing.T) {
	tests := [][]string{
		{"server"},
		{"status"},
	}

	for _, args := range tests {
		output, err := executeForTest(args...)
		if err != nil {
			t.Fatalf("%v returned unexpected error: %v", args, err)
		}
		if !strings.Contains(output, "not implemented yet") {
			t.Fatalf("%v output missing placeholder message; got:\n%s", args, output)
		}
	}
}

func TestDoctorReportsReachableBroker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doctor.yaml")
	if err := os.WriteFile(path, []byte(config.ExampleYAML()), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	cmd := newDoctorCommandWithCheck(func(ctx context.Context, cfg config.MosquittoConfig) error {
		if cfg.Host != "127.0.0.1" {
			t.Fatalf("unexpected host: %q", cfg.Host)
		}
		if cfg.Port != 1883 {
			t.Fatalf("unexpected port: %d", cfg.Port)
		}
		return nil
	})

	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--config", path})

	if _, err := cmd.ExecuteC(); err != nil {
		t.Fatalf("doctor command returned error: %v", err)
	}

	output := buf.String()
	for _, want := range []string{
		"Checking Mosquitto connectivity at 127.0.0.1:1883...",
		"Mosquitto: reachable (127.0.0.1:1883)",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("doctor output missing %q; got:\n%s", want, output)
		}
	}
}

func TestDoctorReportsUnreachableBroker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doctor.yaml")
	if err := os.WriteFile(path, []byte(config.ExampleYAML()), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	cmd := newDoctorCommandWithCheck(func(context.Context, config.MosquittoConfig) error {
		return errors.New("dial tcp 127.0.0.1:1883: connect: connection refused")
	})

	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--config", path})

	_, err := cmd.ExecuteC()
	if err == nil {
		t.Fatal("doctor command succeeded, want error")
	}
	if err.Error() != "mosquitto broker is unreachable" {
		t.Fatalf("unexpected doctor error: %v", err)
	}

	output := buf.String()
	for _, want := range []string{
		"Checking Mosquitto connectivity at 127.0.0.1:1883...",
		"Mosquitto: unreachable (127.0.0.1:1883): dial tcp 127.0.0.1:1883: connect: connection refused",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("doctor output missing %q; got:\n%s", want, output)
		}
	}
}

func TestConfigInitWritesExampleConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcm.yaml")

	output, err := executeForTest("config", "init", "--config", path)
	if err != nil {
		t.Fatalf("config init returned error: %v", err)
	}
	if !strings.Contains(output, "wrote example config") {
		t.Fatalf("config init output missing success message; got:\n%s", output)
	}

	validateOutput, err := executeForTest("config", "validate", "--config", path)
	if err != nil {
		t.Fatalf("config validate returned error for generated config: %v", err)
	}
	if !strings.Contains(validateOutput, "configuration is valid") {
		t.Fatalf("config validate output missing success message; got:\n%s", validateOutput)
	}
}

func TestConfigValidateReturnsErrorForInvalidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(path, []byte(`
http:
  bind_address: 127.0.0.1
  port: 8080
database:
  path: var/lib/mcm/mcm.db
mosquitto:
  host: ""
  port: 99999
  username: admin
  password: ""
  tls:
    enabled: false
logging:
  level: info
`), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	output, err := executeForTest("config", "validate", "--config", path)
	if err == nil {
		t.Fatal("config validate succeeded, want error")
	}

	for _, want := range []string{
		"configuration validation failed",
		"mosquitto.host is required",
		"mosquitto.port must be between 1 and 65535; got 99999",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("config validate output missing %q; got:\n%s", want, output)
		}
	}
}
