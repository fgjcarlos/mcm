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
//
// Issue #298 adds a single-file Apply (AtomicWriteConf) so the broker-config
// flow can reuse the same atomic-rename semantics without duplicating
// write/commit logic. ReloadBrokerOnly signals a reload without writing
// any files; the broker-config path uses it after its own write.
type Applier interface {
	// Apply atomically writes the ACL and passwd files, signals the broker
	// to reload, and restores from snapshot on partial failure.
	//
	// The snapshots in req must be the on-disk content BEFORE the apply
	// (taken by the caller). They are used to restore the broker's
	// configuration when the apply cannot complete cleanly - e.g. the
	// second file write fails, a rename fails, SIGHUP fails, or the
	// context is cancelled mid-apply.
	//
	// On internal rollback success, the returned error wraps
	// ErrApplyRestored. On internal rollback failure, the returned error
	// wraps ErrRollbackFailed. On a pre-cancelled context, the returned error
	// wraps context.Canceled (or context.DeadlineExceeded) and no file is
	// touched.
	Apply(ctx context.Context, req ApplyRequest) error
	// ReloadBrokerOnly signals the broker to reload its configuration
	// without writing any files. Used by the broker-config path after it
	// has already written mosquitto.conf via AtomicWriteConf.
	ReloadBrokerOnly() error
}

// ApplyRequest describes the files one apply writes and the snapshots used
// to restore them when the apply cannot complete cleanly.
//
// Mosquitto resolves `acl_file` per listener once
// `per_listener_settings true` is set, so MCM can manage more than one ACL
// file: the broker-wide file plus one file per listener that owns a scoped
// rule. All of them are written in the same transaction, so a failure never
// leaves the broker with a mixture of old and new access control.
type ApplyRequest struct {
	// ACLBody is the rendered body of the broker-wide ACL file.
	ACLBody string
	// ACLSnapshot is the on-disk content of the broker-wide ACL file
	// before the apply.
	ACLSnapshot string
	// ListenerACLFiles are the per-listener ACL files the apply writes.
	// Every entry carries its own snapshot.
	ListenerACLFiles []ACLFile
	// RemovedACLFiles lists the ACL files the apply deletes. Their
	// snapshot carries the content the rollback restores when the apply
	// fails.
	RemovedACLFiles []ACLFile
	// PasswdBody is the rendered password file body.
	PasswdBody string
	// PasswdSnapshot is the on-disk password file content before the
	// apply.
	PasswdSnapshot string
}

// ACLFile is one ACL file an apply writes.
type ACLFile struct {
	// Path is the absolute path of the file.
	Path string
	// Body is the rendered content of the file.
	Body string
	// Snapshot is the on-disk content of the file before the apply. It is
	// restored when the apply fails.
	Snapshot string
}

// AtomicWriteConf writes a single Mosquitto config file using the same
// atomic-rename semantics as the ACL/passwd path. Exposed at package
// level so callers outside the Applier (the broker-config deploy flow)
// can reuse it without owning a full Applier. Issue #298.
func AtomicWriteConf(path, content string, perm os.FileMode) error {
	return atomicWrite(path, content, perm)
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
	// ACLPath is the absolute path of the broker-wide ACL file.
	ACLPath string
	// PasswdPath is the absolute path of the password file. The
	// per-listener ACL file paths are carried by the ApplyRequest because
	// an apply can write one ACL file per listener.
	PasswdPath    string
	PIDPath       string          // if empty AND no ReloadCommand, Apply refuses
	SignalFunc    func(int) error // if nil, uses platform default (SIGHUP)
	ReloadCommand []string        // production: command + argv to invoke after a successful write
	ReloadRunner  CommandRunner   // if nil, defaults to ExecRunner{}
}

// DockerApplier writes files to paths that are volume-mounted into a Docker
// container, then signals PID 1 in that container via "docker exec … kill -SIGHUP 1".
type DockerApplier struct {
	// ACLPath is the absolute path of the broker-wide ACL file.
	ACLPath string
	// PasswdPath is the absolute path of the password file. The
	// per-listener ACL file paths are carried by the ApplyRequest because
	// an apply can write one ACL file per listener.
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
func rollbackFromSnapshot(ctx context.Context, aclFiles []ACLFile, removedACLFiles []ACLFile, passwdPath, passwdSnapshot string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, file := range aclFiles {
		if err := atomicWrite(file.Path, file.Snapshot, 0o600); err != nil {
			return fmt.Errorf("restore acl %q: %w", file.Path, err)
		}
	}
	// A file the apply deleted is restored from its snapshot so the
	// listener keeps the access it had before the apply.
	for _, file := range removedACLFiles {
		if err := atomicWrite(file.Path, file.Snapshot, 0o600); err != nil {
			return fmt.Errorf("restore removed acl %q: %w", file.Path, err)
		}
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
func (f FileApplier) Apply(ctx context.Context, req ApplyRequest) error {
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
	files := append([]ACLFile{{Path: f.ACLPath, Body: req.ACLBody, Snapshot: req.ACLSnapshot}}, req.ListenerACLFiles...)

	// Stage 1: write every temp file. No rename has happened yet, so a
	// failure here leaves the broker on the previous configuration.
	aclTmps := make([]string, 0, len(files))
	for _, file := range files {
		tmp, err := writeStage(file.Path, file.Body, 0o600)
		if err != nil {
			for _, staged := range aclTmps {
				cleanupTempFile(nil, staged)
			}
			return err
		}
		aclTmps = append(aclTmps, tmp)
	}
	passwdTmp, err := writeStage(f.PasswdPath, req.PasswdBody, 0o600)
	if err != nil {
		for _, staged := range aclTmps {
			cleanupTempFile(nil, staged)
		}
		return err
	}

	// Stage 2: rename every temp file into place. A failure here leaves
	// the broker half-applied, so we trigger an internal rollback.
	for idx, file := range files {
		if err := commitStage(aclTmps[idx], file.Path); err != nil {
			cleanupTempFile(nil, passwdTmp)
			for _, staged := range aclTmps[idx+1:] {
				cleanupTempFile(nil, staged)
			}
			return f.rollbackAfterPartialApply("rename acl", err, files, req)
		}
	}
	if err := commitStage(passwdTmp, f.PasswdPath); err != nil {
		return f.rollbackAfterPartialApply("rename passwd", err, files, req)
	}

	// Stage 3: remove the ACL files the deploy retired. A listener whose
	// scoped rules are gone must not keep serving the access the operator
	// removed, and MCM writes the broker-wide file into the listener
	// again once the listener no longer declares its own.
	for _, file := range req.RemovedACLFiles {
		if err := os.Remove(file.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return f.rollbackAfterPartialApply("remove acl", err, files, req)
		}
	}

	// Stage 4: signal the broker. If the reload fails after the files were
	// already renamed, the broker is now serving the new configuration
	// without having reloaded it — rollback from snapshot.
	if err := f.reloadBroker(); err != nil {
		return f.rollbackAfterPartialApply("signal reload", err, files, req)
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
func (f FileApplier) rollbackAfterPartialApply(stage string, cause error, files []ACLFile, req ApplyRequest) error {
	rollbackCtx, cancel := context.WithTimeout(context.Background(), internalRollbackTimeout)
	defer cancel()

	if err := rollbackFromSnapshot(rollbackCtx, files, req.RemovedACLFiles, f.PasswdPath, req.PasswdSnapshot); err != nil {
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

// ReloadBrokerOnly signals the broker to reload without writing any
// files. Used by the broker-config deploy flow after its own atomic
// write. It mirrors reloadBroker's behaviour (ReloadCommand preferred,
// PIDPath+SIGHUP fallback, ErrReloadNotSignaled when neither is set).
func (f FileApplier) ReloadBrokerOnly() error {
	if f.PIDPath == "" && len(f.ReloadCommand) == 0 {
		return ErrReloadNotSignaled
	}
	return f.reloadBroker()
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
func (d DockerApplier) Apply(ctx context.Context, req ApplyRequest) error {
	if d.ContainerName == "" {
		return fmt.Errorf("docker applier: container_name must not be empty")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	files := append([]ACLFile{{Path: d.ACLPath, Body: req.ACLBody, Snapshot: req.ACLSnapshot}}, req.ListenerACLFiles...)

	// Stage 1: write every temp file.
	aclTmps := make([]string, 0, len(files))
	for _, file := range files {
		tmp, err := writeStage(file.Path, file.Body, 0o600)
		if err != nil {
			for _, staged := range aclTmps {
				cleanupTempFile(nil, staged)
			}
			return err
		}
		aclTmps = append(aclTmps, tmp)
	}
	passwdTmp, err := writeStage(d.PasswdPath, req.PasswdBody, 0o600)
	if err != nil {
		for _, staged := range aclTmps {
			cleanupTempFile(nil, staged)
		}
		return err
	}

	// Stage 2: rename every temp file into place.
	for idx, file := range files {
		if err := commitStage(aclTmps[idx], file.Path); err != nil {
			cleanupTempFile(nil, passwdTmp)
			for _, staged := range aclTmps[idx+1:] {
				cleanupTempFile(nil, staged)
			}
			return d.rollbackAfterPartialApply("rename acl", err, files, req)
		}
	}
	if err := commitStage(passwdTmp, d.PasswdPath); err != nil {
		return d.rollbackAfterPartialApply("rename passwd", err, files, req)
	}

	// Stage 3: remove the ACL files the deploy retired.
	for _, file := range req.RemovedACLFiles {
		if err := os.Remove(file.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return d.rollbackAfterPartialApply("remove acl", err, files, req)
		}
	}

	// Stage 4: docker exec kill -HUP 1.
	if _, err := d.Runner.Run(ctx, "docker", "exec", d.ContainerName, "kill", "-HUP", "1"); err != nil {
		return d.rollbackAfterPartialApply("docker exec", err, files, req)
	}
	return nil
}

// rollbackAfterPartialApply is the DockerApplier counterpart of
// FileApplier.rollbackAfterPartialApply; same semantics.
func (d DockerApplier) rollbackAfterPartialApply(stage string, cause error, files []ACLFile, req ApplyRequest) error {
	rollbackCtx, cancel := context.WithTimeout(context.Background(), internalRollbackTimeout)
	defer cancel()

	if err := rollbackFromSnapshot(rollbackCtx, files, req.RemovedACLFiles, d.PasswdPath, req.PasswdSnapshot); err != nil {
		return fmt.Errorf("%s: %w", stage, errors.Join(ErrRollbackFailed, cause, err))
	}
	return fmt.Errorf("%s: %w", stage, errors.Join(ErrApplyRestored, cause))
}

// ReloadBrokerOnly signals the broker to reload its configuration via
// docker exec kill -HUP 1 without writing any files. Issue #298.
func (d DockerApplier) ReloadBrokerOnly() error {
	if d.ContainerName == "" {
		return fmt.Errorf("docker applier: container_name must not be empty")
	}
	runner := d.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	if output, err := runner.Run(context.Background(), "docker", "exec", d.ContainerName, "kill", "-HUP", "1"); err != nil {
		return fmt.Errorf("docker exec reload: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
