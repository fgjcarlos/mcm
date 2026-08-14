package mosquitto

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Applier writes Mosquitto ACL and password files and signals the broker to
// reload its configuration.
type Applier interface {
	Apply(ctx context.Context, aclBody string, passwdBody string) error
}

// FileApplier writes files directly to the filesystem and optionally sends
// SIGHUP to the Mosquitto process identified by PIDPath.
type FileApplier struct {
	ACLPath    string
	PasswdPath string
	PIDPath    string          // if empty, skip reload
	SignalFunc func(int) error // if nil, uses platform default (SIGHUP)
}

// DockerApplier writes files to paths that are volume-mounted into a Docker
// container, then signals PID 1 in that container via "docker exec … kill -SIGHUP 1".
type DockerApplier struct {
	ACLPath       string
	PasswdPath    string
	ContainerName string
	Runner        CommandRunner
}

// atomicWrite writes content to path using a temp file in the same directory
// (guaranteeing same-filesystem rename) then renames the temp into place.
// The final file has the given permission bits.
func atomicWrite(path, content string, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".mcm-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.WriteString(content); err != nil {
		cleanupTemp(tmp, tmpName, slog.Default())
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanupTemp(tmp, tmpName, slog.Default())
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanupTemp(tmp, tmpName, slog.Default())
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanupTemp(nil, tmpName, slog.Default())
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanupTemp(nil, tmpName, slog.Default())
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

// cleanupTemp is the best-effort cleanup path for atomicWrite's error
// branches. The original write/chmod/sync/close/rename error is returned
// to the caller; cleanup errors are logged via slog.Warn (not returned)
// because they cannot supersede the primary error. A missing temp file at
// Remove time is not an error — it means the temp was never created or
// was already reaped.
func cleanupTemp(tmp *os.File, name string, logger *slog.Logger) {
	if tmp != nil {
		if err := tmp.Close(); err != nil && logger != nil {
			logger.Warn("close temp file failed",
				slog.String("path", name),
				slog.String("error", err.Error()))
		}
	}
	if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) && logger != nil {
		logger.Warn("remove temp file failed",
			slog.String("path", name),
			slog.String("error", err.Error()))
	}
}

// Apply atomically writes the ACL and password files, then sends SIGHUP to
// the broker process if PIDPath is non-empty.
func (f FileApplier) Apply(ctx context.Context, aclBody string, passwdBody string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := atomicWrite(f.ACLPath, aclBody, 0o600); err != nil {
		return err
	}
	if err := atomicWrite(f.PasswdPath, passwdBody, 0o600); err != nil {
		return err
	}
	if f.PIDPath == "" {
		return nil
	}

	data, err := os.ReadFile(f.PIDPath)
	if err != nil {
		return fmt.Errorf("read pid file %q: %w", f.PIDPath, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("parse pid from %q: %w", f.PIDPath, err)
	}
	sigFn := f.SignalFunc
	if sigFn == nil {
		sigFn = defaultSignal
	}
	if err := sigFn(pid); err != nil {
		return fmt.Errorf("send SIGHUP to pid %d: %w", pid, err)
	}
	return nil
}

// Apply atomically writes the ACL and password files, then reloads the broker
// by sending SIGHUP to PID 1 inside the named Docker container.
func (d DockerApplier) Apply(ctx context.Context, aclBody string, passwdBody string) error {
	if d.ContainerName == "" {
		return fmt.Errorf("docker applier: container_name must not be empty")
	}
	if err := atomicWrite(d.ACLPath, aclBody, 0o600); err != nil {
		return err
	}
	if err := atomicWrite(d.PasswdPath, passwdBody, 0o600); err != nil {
		return err
	}
	if _, err := d.Runner.Run(ctx, "docker", "exec", d.ContainerName, "kill", "-HUP", "1"); err != nil {
		return err
	}
	return nil
}
