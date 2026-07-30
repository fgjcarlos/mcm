package mosquitto

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestExecRunner(t *testing.T) {
	t.Parallel()

	t.Run("executes command successfully and returns combined output", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			t.Skipf("unix-only: echo not available in PowerShell")
		}
		runner := ExecRunner{}
		out, err := runner.Run(context.Background(), "echo", "hello-mcm")
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
		if !strings.Contains(string(out), "hello-mcm") {
			t.Fatalf("output = %q, want to contain %q", string(out), "hello-mcm")
		}
	})

	t.Run("returns error on non-zero exit code", func(t *testing.T) {
		t.Parallel()
		runner := ExecRunner{}
		// "false" is a standard POSIX command that always exits non-zero.
		_, err := runner.Run(context.Background(), "false")
		if err == nil {
			t.Fatal("Run returned nil error, want non-zero exit error")
		}
	})
}
