package cli

import (
	"bytes"
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
		"validate",
		"version",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("help output missing %q; got:\n%s", want, output)
		}
	}
}

func TestPlaceholderCommandsReturnNotImplemented(t *testing.T) {
	tests := [][]string{
		{"doctor"},
		{"status"},
		{"config", "validate"},
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

func TestServerHelpListsRuntimeFlags(t *testing.T) {
	output, err := executeForTest("server", "--help")
	if err != nil {
		t.Fatalf("server help returned error: %v", err)
	}

	for _, want := range []string{
		"--listen-addr",
		"--broker-url",
		":8080",
		"tcp://localhost:1883",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("server help missing %q; got:\n%s", want, output)
		}
	}
}
