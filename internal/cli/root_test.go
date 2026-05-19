package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		{"doctor"},
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

func TestServerHelpIncludesConfigFlag(t *testing.T) {
	output, err := executeForTest("server", "--help")
	if err != nil {
		t.Fatalf("server help returned error: %v", err)
	}

	for _, want := range []string{
		"Start the MCM API and web UI server",
		"--config",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("server help output missing %q; got:\n%s", want, output)
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
