// Package listener manages Mosquitto listener preview and restart operations.
package listener

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

var (
	// ErrListenerRestartConfigMissing is returned when a restart runner lacks required configuration.
	ErrListenerRestartConfigMissing = errors.New("listener restart runner missing configuration")
	// ErrListenerRestartFailed identifies a failed listener restart during apply.
	ErrListenerRestartFailed = errors.New("listener restart failed")
)

// ListenerRestartRunner restarts the broker service that serves listeners.
type ListenerRestartRunner interface {
	Restart(ctx context.Context, target string) error
}

// ExecutorFunc executes a command represented as a complete argv slice.
type ExecutorFunc func(ctx context.Context, argv []string) error

// NoopRestartRunner records restart targets without performing an external operation.
type NoopRestartRunner struct {
	mu sync.Mutex
	// Targets contains restart targets in invocation order.
	Targets []string
}

// Restart records target and completes without restarting a process.
func (r *NoopRestartRunner) Restart(_ context.Context, target string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Targets = append(r.Targets, target)
	return nil
}

// DockerComposeRestartRunner restarts a service through Docker Compose.
type DockerComposeRestartRunner struct {
	ComposePath string
	ServiceName string
	ProjectName string
	Executor    ExecutorFunc
}

// Restart invokes docker compose restart for target or the configured service.
func (r DockerComposeRestartRunner) Restart(ctx context.Context, target string) error {
	service := strings.TrimSpace(target)
	if service == "" {
		service = strings.TrimSpace(r.ServiceName)
	}
	if strings.TrimSpace(r.ComposePath) == "" || service == "" {
		return ErrListenerRestartConfigMissing
	}
	project := strings.TrimSpace(r.ProjectName)
	if project == "" {
		project = "mcm"
	}
	executor := r.Executor
	if executor == nil {
		executor = execExecutor
	}
	argv := []string{"docker", "compose", "-f", r.ComposePath, "-p", project, "restart", service}
	if err := executor(ctx, argv); err != nil {
		return fmt.Errorf("docker compose restart %s: %w", service, err)
	}
	return nil
}

func execExecutor(ctx context.Context, argv []string) error {
	if len(argv) == 0 {
		return ErrListenerRestartConfigMissing
	}
	return exec.CommandContext(ctx, argv[0], argv[1:]...).Run()
}
