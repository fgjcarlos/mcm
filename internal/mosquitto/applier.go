package mosquitto

import (
	"context"
	"fmt"
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
		//nolint:errcheck // best-effort cleanup; original write error is the one to surface
		tmp.Close()
		//nolint:errcheck // best-effort cleanup; original write error is the one to surface
		os.Remove(tmpName)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		//nolint:errcheck // best-effort cleanup; original chmod error is the one to surface
		tmp.Close()
		//nolint:errcheck // best-effort cleanup; original chmod error is the one to surface
		os.Remove(tmpName)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		//nolint:errcheck // best-effort cleanup; original sync error is the one to surface
		tmp.Close()
		//nolint:errcheck // best-effort cleanup; original sync error is the one to surface
		os.Remove(tmpName)
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		//nolint:errcheck // best-effort cleanup; original close error is the one to surface
		os.Remove(tmpName)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		//nolint:errcheck // best-effort cleanup; original rename error is the one to surface
		os.Remove(tmpName)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
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
