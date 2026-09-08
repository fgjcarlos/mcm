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
	"time"
)

// internalRollbackTimeout bounds the applier's internal rollback when a
// partial apply needs to be reverted from snapshot. The applier derives a
// fresh context from context.Background() with this timeout so a cancelled
// request context does not abort the rollback (issue #292, acceptance
// criterion 3).
const internalRollbackTimeout = 10 * time.Second

// ErrApplyRestored is wrapped in the error returned by Apply when the new
// configuration could not be written but the previous configuration was
// restored from snapshot. The caller should treat the apply as failed but
// not require operator intervention to bring the broker back to a known
// state — the broker is serving the previous configuration.
var ErrApplyRestored = errors.New("apply failed; restored from snapshot")

// ErrRollbackFailed is wrapped in the error returned by Apply when the new
// configuration could not be written AND the restore from snapshot also
// failed. The broker is in an indeterminate state and requires operator
// intervention to reconcile.
var ErrRollbackFailed = errors.New("rollback failed")

// ErrReloadNotSignaled is returned by FileApplier.Apply when PIDPath is
// empty. The applier refuses to silently skip SIGHUP (issue #293,
// acceptance criterion 2): a deploy cannot be marked active without a
// real reload signal, so the operator must either provide a PIDPath or
// configure a reload strategy that does not depend on signal-based
// reload (out of scope for the MVP).
var ErrReloadNotSignaled = errors.New("reload not signaled: FileApplier requires PIDPath")

// Applier writes Mosquitto ACL and password files and signals the broker to
// reload its configuration.
//
// Issue #292 (P0): a sequential ACL→passwd→SIGHUP apply leaves the broker
// in an indeterminate state when any step fails. Apply now performs the
// write+rename for both files before signalling reload, and restores both
// files from the provided snapshot when any step fails.
type Applier interface {
	// Apply atomically writes ACL and passwd files, signals the broker to
	// reload, and restores from snapshot on partial failure.
	//
	// aclSnapshot and passwdSnapshot must be the on-disk content BEFORE the
	// apply (taken by the caller). They are used to restore the broker's
	// configuration when the apply cannot complete cleanly — e.g. the
	// second file write fails, a rename fails, SIGHUP fails, or the
	// context is cancelled mid-apply.
	//
	// On internal rollback success, the returned error wraps
	// ErrApplyRestored. On internal rollback failure, the returned error
	// wraps ErrRollbackFailed. On a pre-cancelled context, the returned
	// error wraps context.Canceled (or context.DeadlineExceeded) and
	// neither file is touched.
	Apply(ctx context.Context, aclBody, passwdBody, aclSnapshot, passwdSnapshot string) error
}

// FileApplier writes files directly to the filesystem and signals
// Mosquitto to reload them. Two reload mechanisms are supported:
//
//   - PIDPath + SignalFunc: legacy SIGHUP-to-PID path used by the dev
//     Compose stack. Kept for backwards compatibility but is NOT
//     recommended for production — it requires MCM and Mosquitto to
//     share a PID namespace, which is rare outside of containers.
//
//   - ReloadCommand + ReloadRunner: production reload mechanism
//     (issue #294). The applier invokes ReloadCommand[0] with
//     ReloadCommand[1:] as argv via ReloadRunner. This lets operators
//     plug in `systemctl reload mosquitto`, an SSH hop, a k8s rollout
//     trigger, or any other sidecar without giving MCM access to the
//     Docker socket.
//
// If both are set, ReloadCommand takes precedence and the SIGHUP path
// is ignored. If neither is set, Apply returns ErrReloadNotSignaled.
type FileApplier struct {
	ACLPath       string
	PasswdPath    string
	PIDPath       string          // if empty AND no ReloadCommand, Apply refuses
	SignalFunc    func(int) error // if nil, uses platform default (SIGHUP)
	ReloadCommand []string        // production: command + argv to invoke after a successful write
	ReloadRunner  CommandRunner   // if nil, defaults to ExecRunner{}
}

// DockerApplier writes files to paths that are volume-mounted into a Docker
// container, then signals PID 1 in that container via "docker exec … kill -SIGHUP 1".
type DockerApplier struct {
	ACLPath       string
	PasswdPath    string
	ContainerName string
	Runner        CommandRunner
}

// writeStage writes content to a new temp file in the same directory as
// path, with the given permission bits. The returned path is the temp file
// path; the caller is responsible for either committing it via
// commitStage (renaming into place) or discarding via cleanupTempFile.
//
// Splitting the temp-file creation from the rename is what enables the
// transactional behavior: both files can be staged before either is
// renamed into place, so a failure in the second stage never leaves the
// first file half-applied.
func writeStage(path, content string, perm os.FileMode) (string, error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".mcm-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.WriteString(content); err != nil {
		cleanupTempFile(tmp, tmpName)
		return "", fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanupTempFile(tmp, tmpName)
		return "", fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanupTempFile(tmp, tmpName)
		return "", fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanupTempFile(nil, tmpName)
		return "", fmt.Errorf("close temp file: %w", err)
	}
	return tmpName, nil
}

// commitStage renames the staged temp file at tmpName to finalPath. The
// temp file must already be closed. On rename failure the temp file is
// cleaned up.
func commitStage(tmpName, finalPath string) error {
	if err := os.Rename(tmpName, finalPath); err != nil {
		cleanupTempFile(nil, tmpName)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

// cleanupTempFile is the best-effort cleanup for staged temp files. The
// file may be nil if it was already closed. Errors are logged via
// slog.Warn (not returned) because they cannot supersede the primary
// error. A missing temp file at Remove time is not an error — it means the
// temp was never created or was already reaped.
func cleanupTempFile(tmp *os.File, name string) {
	if tmp != nil {
		if err := tmp.Close(); err != nil {
			slog.Default().Warn("close temp file failed",
				slog.String("path", name),
				slog.String("error", err.Error()))
		}
	}
	if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Default().Warn("remove temp file failed",
			slog.String("path", name),
			slog.String("error", err.Error()))
	}
}

// atomicWrite writes content to path using a temp file in the same directory
// (guaranteeing same-filesystem rename) then renames the temp into place.
// The final file has the given permission bits. Kept for callers that want
// single-file atomicity without the two-file transactional semantics.
func atomicWrite(path, content string, perm os.FileMode) error {
	tmpName, err := writeStage(path, content, perm)
	if err != nil {
		return err
	}
	return commitStage(tmpName, path)
}

// rollbackFromSnapshot writes the snapshot ACL and passwd atomically using
// the same staged-temp-then-rename approach. Used for internal rollback on
// partial apply failure. ctx must be independent of any request context
// that may have been cancelled — the caller is expected to derive it from
// context.Background().
func rollbackFromSnapshot(ctx context.Context, aclPath, passwdPath, aclSnapshot, passwdSnapshot string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := atomicWrite(aclPath, aclSnapshot, 0o600); err != nil {
		return fmt.Errorf("restore acl: %w", err)
	}
	if err := atomicWrite(passwdPath, passwdSnapshot, 0o600); err != nil {
		return fmt.Errorf("restore passwd: %w", err)
	}
	return nil
}

// Apply atomically writes the ACL and password files, then sends SIGHUP to
// the broker process if PIDPath is non-empty. The two writes happen
// against staged temp files before either rename, so a failure in the
// second stage does not leave the first file half-applied. A failure in
// the rename or SIGHUP step triggers an internal rollback from the
// provided snapshot using a fresh bounded context.
func (f FileApplier) Apply(ctx context.Context, aclBody, passwdBody, aclSnapshot, passwdSnapshot string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Issue #293: refuse to silently skip SIGHUP. An apply without
	// a reload signal cannot be verified-active (the broker would
	// keep serving the previous config until something external
	// triggers the reload), so the deploy service must NOT mark
	// this active. ReloadCommand (issue #294) is also an acceptable
	// reload mechanism; reloadBroker picks the right one.
	if f.PIDPath == "" && len(f.ReloadCommand) == 0 {
		return ErrReloadNotSignaled
	}

	// Stage 1: write both temp files. Neither rename has happened yet.
	aclTmp, err := writeStage(f.ACLPath, aclBody, 0o600)
	if err != nil {
		return err
	}
	passwdTmp, err := writeStage(f.PasswdPath, passwdBody, 0o600)
	if err != nil {
		cleanupTempFile(nil, aclTmp)
		return err
	}

	// Stage 2: rename both temp files into place. A failure here leaves
	// the broker half-applied, so we trigger an internal rollback.
	if err := commitStage(aclTmp, f.ACLPath); err != nil {
		cleanupTempFile(nil, passwdTmp)
		return f.rollbackAfterPartialApply("rename acl", err, aclSnapshot, passwdSnapshot)
	}
	if err := commitStage(passwdTmp, f.PasswdPath); err != nil {
		return f.rollbackAfterPartialApply("rename passwd", err, aclSnapshot, passwdSnapshot)
	}

	// Stage 3: signal the broker. If the reload fails after the files were
	// already renamed, the broker is now serving the new configuration
	// without having reloaded it — rollback from snapshot.
	if err := f.reloadBroker(); err != nil {
		return f.rollbackAfterPartialApply("signal reload", err, aclSnapshot, passwdSnapshot)
	}

	return nil
}

// rollbackAfterPartialApply restores both files from the provided snapshot
// using a fresh bounded context derived from context.Background(). This
// guarantees that a cancelled request context cannot abort the rollback
// (issue #292, acceptance criterion 3).
//
// Returns an error wrapping ErrApplyRestored when the restore succeeded,
// or ErrRollbackFailed when the restore also failed. In both cases the
// underlying cause is also wrapped so callers can inspect it via errors.Is.
func (f FileApplier) rollbackAfterPartialApply(stage string, cause error, aclSnapshot, passwdSnapshot string) error {
	rollbackCtx, cancel := context.WithTimeout(context.Background(), internalRollbackTimeout)
	defer cancel()

	if err := rollbackFromSnapshot(rollbackCtx, f.ACLPath, f.PasswdPath, aclSnapshot, passwdSnapshot); err != nil {
		return fmt.Errorf("%s: %w", stage, errors.Join(ErrRollbackFailed, cause, err))
	}
	return fmt.Errorf("%s: %w", stage, errors.Join(ErrApplyRestored, cause))
}

// signalReload reads the PID file and sends SIGHUP.
func (f FileApplier) signalReload() error {
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

// reloadBroker picks the right reload mechanism for the applier
// configuration. ReloadCommand takes precedence over PIDPath+SIGHUP —
// production deploys use ReloadCommand so MCM does not need the
// Docker socket. If neither is set, returns ErrReloadNotSignaled.
func (f FileApplier) reloadBroker() error {
	if len(f.ReloadCommand) > 0 {
		return f.reloadViaCommand()
	}
	if f.PIDPath != "" {
		return f.signalReload()
	}
	return ErrReloadNotSignaled
}

// reloadViaCommand invokes ReloadCommand[0] with the remaining argv
// entries via ReloadRunner (defaults to ExecRunner{}). The command and
// its arguments are passed literally to exec.Command, never through a
// shell — so MCM operators can safely template paths into the argv.
func (f FileApplier) reloadViaCommand() error {
	if len(f.ReloadCommand) == 0 {
		return fmt.Errorf("reload command is empty: at least the command name is required")
	}
	runner := f.ReloadRunner
	if runner == nil {
		runner = ExecRunner{}
	}
	name := f.ReloadCommand[0]
	args := f.ReloadCommand[1:]
	if _, err := runner.Run(context.Background(), name, args...); err != nil {
		return fmt.Errorf("reload command %q: %w", name, err)
	}
	return nil
}

// Apply atomically writes the ACL and password files, then reloads the
// broker by sending SIGHUP to PID 1 inside the named Docker container.
// Uses the same two-stage transactional behavior as FileApplier: both
// files are staged before either rename, and a failure in docker exec
// triggers an internal rollback from the provided snapshot.
func (d DockerApplier) Apply(ctx context.Context, aclBody, passwdBody, aclSnapshot, passwdSnapshot string) error {
	if d.ContainerName == "" {
		return fmt.Errorf("docker applier: container_name must not be empty")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// Stage 1: write both temp files.
	aclTmp, err := writeStage(d.ACLPath, aclBody, 0o600)
	if err != nil {
		return err
	}
	passwdTmp, err := writeStage(d.PasswdPath, passwdBody, 0o600)
	if err != nil {
		cleanupTempFile(nil, aclTmp)
		return err
	}

	// Stage 2: rename both temp files into place.
	if err := commitStage(aclTmp, d.ACLPath); err != nil {
		cleanupTempFile(nil, passwdTmp)
		return d.rollbackAfterPartialApply("rename acl", err, aclSnapshot, passwdSnapshot)
	}
	if err := commitStage(passwdTmp, d.PasswdPath); err != nil {
		return d.rollbackAfterPartialApply("rename passwd", err, aclSnapshot, passwdSnapshot)
	}

	// Stage 3: docker exec kill -HUP 1.
	if _, err := d.Runner.Run(ctx, "docker", "exec", d.ContainerName, "kill", "-HUP", "1"); err != nil {
		return d.rollbackAfterPartialApply("docker exec", err, aclSnapshot, passwdSnapshot)
	}
	return nil
}

// rollbackAfterPartialApply is the DockerApplier counterpart of
// FileApplier.rollbackAfterPartialApply; same semantics.
func (d DockerApplier) rollbackAfterPartialApply(stage string, cause error, aclSnapshot, passwdSnapshot string) error {
	rollbackCtx, cancel := context.WithTimeout(context.Background(), internalRollbackTimeout)
	defer cancel()

	if err := rollbackFromSnapshot(rollbackCtx, d.ACLPath, d.PasswdPath, aclSnapshot, passwdSnapshot); err != nil {
		return fmt.Errorf("%s: %w", stage, errors.Join(ErrRollbackFailed, cause, err))
	}
	return fmt.Errorf("%s: %w", stage, errors.Join(ErrApplyRestored, cause))
}
