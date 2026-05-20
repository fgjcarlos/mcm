package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
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

func TestStatusPlaceholderCommandReturnsNotImplemented(t *testing.T) {
	output, err := executeForTest("status")
	if err != nil {
		t.Fatalf("status returned unexpected error: %v", err)
	}
	if !strings.Contains(output, "not implemented yet") {
		t.Fatalf("status output missing placeholder message; got:\n%s", output)
	}
}

func TestDoctorCommandReportsReachableMosquitto(t *testing.T) {
	listener := startCLIMQTTTestBroker(t, []byte{0x20, 0x02, 0x00, 0x00})
	configPath := writeCLIConfig(t, listener.Addr().(*net.TCPAddr).Port)

	output, err := executeForTest("doctor", "--config", configPath)
	if err != nil {
		t.Fatalf("doctor returned error for reachable broker: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "OK: Mosquitto is reachable") {
		t.Fatalf("doctor output missing success message; got:\n%s", output)
	}
}

func TestDoctorCommandReportsUnreachableMosquitto(t *testing.T) {
	listener := startCLIMQTTTestBroker(t, []byte{0x20, 0x02, 0x00, 0x00})
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	configPath := writeCLIConfig(t, port)

	output, err := executeForTest("doctor", "--config", configPath)
	if err == nil {
		t.Fatal("doctor succeeded for unreachable broker, want error")
	}
	if !strings.Contains(output, "FAIL: Mosquitto is unreachable") {
		t.Fatalf("doctor output missing failure message; got:\n%s", output)
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

func writeCLIConfig(t *testing.T, mosquittoPort int) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "mcm.yaml")
	content := fmt.Sprintf(`http:
  bind_address: 127.0.0.1
  port: 8080
database:
  path: var/lib/mcm/mcm.db
auth:
  jwt_secret: replace-this-secret-with-at-least-32-characters
  token_ttl: 24h
  bootstrap_admin:
    username: admin
    password: change-this-admin-password
mosquitto:
  host: 127.0.0.1
  port: %d
  username: ""
  password: ""
  tls:
    enabled: false
logging:
  level: info
`, mosquittoPort)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	return path
}

func startCLIMQTTTestBroker(t *testing.T, connack []byte) net.Listener {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}

	done := make(chan struct{})
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})

	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			t.Errorf("Accept returned error: %v", err)
			return
		}
		defer conn.Close()

		buf := make([]byte, 256)
		if _, err := conn.Read(buf); err != nil && !errors.Is(err, io.EOF) {
			t.Errorf("Read returned error: %v", err)
			return
		}
		if _, err := conn.Write(connack); err != nil {
			t.Errorf("Write returned error: %v", err)
		}
	}()

	return listener
}
