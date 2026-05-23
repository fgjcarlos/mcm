package mosquitto

import (
	"context"
	"os/exec"
)

// CommandRunner abstracts command execution for testability.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner is the production CommandRunner that calls the real OS exec.
type ExecRunner struct{}

// Run executes name with args under ctx and returns combined output.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}
