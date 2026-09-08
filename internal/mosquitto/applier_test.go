package mosquitto

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeRunner records the last call for test inspection.
type fakeRunner struct {
	name   string
	args   []string
	err    error
	output []byte
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.name = name
	f.args = args
	return f.output, f.err
}

func TestAtomicWrite(t *testing.T) {
	t.Parallel()

	t.Run("writes content and sets permissions 0600", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			t.Skipf("unix-only: Windows ignores 0600 bitmask")
		}
		dir := t.TempDir()
		path := filepath.Join(dir, "target.txt")
		content := "hello world\n"

		err := atomicWrite(path, content, 0o600)
		if err != nil {
			t.Fatalf("atomicWrite returned error: %v", err)
		}

		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile returned error: %v", err)
		}
		if string(got) != content {
			t.Fatalf("content = %q, want %q", string(got), content)
		}

		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat returned error: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("permissions = %o, want %o", perm, 0o600)
		}
	})

	t.Run("returns error for bad directory", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join("/nonexistent-directory-mcm-test", "target.txt")
		err := atomicWrite(path, "data", 0o600)
		if err == nil {
			t.Fatal("atomicWrite returned nil error, want error for bad directory")
		}
		if !strings.Contains(err.Error(), "create temp file") {
			t.Fatalf("error = %q, want message containing 'create temp file'", err.Error())
		}
	})
}

func TestFileApplierApply(t *testing.T) {
	t.Parallel()

	t.Run("writes both files correctly", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		aclPath := filepath.Join(dir, "acl")
		passwdPath := filepath.Join(dir, "passwd")
		pidPath := filepath.Join(dir, "mosquitto.pid")
		if err := os.WriteFile(pidPath, []byte("12345\n"), 0o600); err != nil {
			t.Fatalf("seed pid: %v", err)
		}

		var signalled bool
		fa := FileApplier{
			ACLPath:    aclPath,
			PasswdPath: passwdPath,
			PIDPath:    pidPath,
			SignalFunc: func(_ int) error {
				signalled = true
				return nil
			},
		}

		err := fa.Apply(context.Background(), "acl-content", "passwd-content", "", "")
		if err != nil {
			t.Fatalf("Apply returned error: %v", err)
		}
		if !signalled {
			t.Fatal("SignalFunc was not called after a successful apply")
		}

		gotACL, err := os.ReadFile(aclPath)
		if err != nil {
			t.Fatalf("ReadFile acl: %v", err)
		}
		if string(gotACL) != "acl-content" {
			t.Fatalf("acl content = %q, want %q", string(gotACL), "acl-content")
		}

		gotPasswd, err := os.ReadFile(passwdPath)
		if err != nil {
			t.Fatalf("ReadFile passwd: %v", err)
		}
		if string(gotPasswd) != "passwd-content" {
			t.Fatalf("passwd content = %q, want %q", string(gotPasswd), "passwd-content")
		}
	})

	t.Run("empty PIDPath returns ErrReloadNotSignaled", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		var killed bool
		fa := FileApplier{
			ACLPath:    filepath.Join(dir, "acl"),
			PasswdPath: filepath.Join(dir, "passwd"),
			PIDPath:    "",
			SignalFunc: func(_ int) error {
				killed = true
				return nil
			},
		}

		err := fa.Apply(context.Background(), "a", "b", "", "")
		if !errors.Is(err, ErrReloadNotSignaled) {
			t.Fatalf("Apply error = %v, want ErrReloadNotSignaled", err)
		}
		if killed {
			t.Fatal("SignalFunc was called with empty PIDPath, want no signal sent")
		}
		// Neither file must be created — the apply aborted before I/O.
		if _, statErr := os.Stat(fa.ACLPath); statErr == nil {
			t.Fatal("ACL file was created before PIDPath check; expected no I/O")
		}
		if _, statErr := os.Stat(fa.PasswdPath); statErr == nil {
			t.Fatal("passwd file was created before PIDPath check; expected no I/O")
		}
	})

	t.Run("with PIDPath sends signal to process", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		pidPath := filepath.Join(dir, "mosquitto.pid")
		if err := os.WriteFile(pidPath, []byte("12345\n"), 0o600); err != nil {
			t.Fatalf("WriteFile pid: %v", err)
		}

		var gotPID int
		fa := FileApplier{
			ACLPath:    filepath.Join(dir, "acl"),
			PasswdPath: filepath.Join(dir, "passwd"),
			PIDPath:    pidPath,
			SignalFunc: func(pid int) error {
				gotPID = pid
				return nil
			},
		}

		err := fa.Apply(context.Background(), "a", "b", "", "")
		if err != nil {
			t.Fatalf("Apply returned error: %v", err)
		}
		if gotPID != 12345 {
			t.Fatalf("kill PID = %d, want 12345", gotPID)
		}
	})

	t.Run("PID file not found returns error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		fa := FileApplier{
			ACLPath:    filepath.Join(dir, "acl"),
			PasswdPath: filepath.Join(dir, "passwd"),
			PIDPath:    filepath.Join(dir, "missing.pid"),
		}

		err := fa.Apply(context.Background(), "a", "b", "", "")
		if err == nil {
			t.Fatal("Apply returned nil error, want error for missing PID file")
		}
	})
}

func TestDockerApplierApply(t *testing.T) {
	t.Parallel()

	t.Run("sends correct docker exec command", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		runner := &fakeRunner{}
		da := DockerApplier{
			ACLPath:       filepath.Join(dir, "acl"),
			PasswdPath:    filepath.Join(dir, "passwd"),
			ContainerName: "mosquitto-broker",
			Runner:        runner,
		}

		err := da.Apply(context.Background(), "acl-body", "passwd-body", "", "")
		if err != nil {
			t.Fatalf("Apply returned error: %v", err)
		}

		if runner.name != "docker" {
			t.Fatalf("runner.name = %q, want %q", runner.name, "docker")
		}
		wantArgs := []string{"exec", "mosquitto-broker", "kill", "-HUP", "1"}
		if fmt.Sprint(runner.args) != fmt.Sprint(wantArgs) {
			t.Fatalf("runner.args = %v, want %v", runner.args, wantArgs)
		}
	})

	t.Run("runner error propagates", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		runErr := errors.New("docker not found")
		runner := &fakeRunner{err: runErr}
		da := DockerApplier{
			ACLPath:       filepath.Join(dir, "acl"),
			PasswdPath:    filepath.Join(dir, "passwd"),
			ContainerName: "mosquitto-broker",
			Runner:        runner,
		}

		err := da.Apply(context.Background(), "a", "b", "", "")
		if err == nil {
			t.Fatal("Apply returned nil error, want runner error propagated")
		}
		if !errors.Is(err, runErr) {
			t.Fatalf("Apply error = %v, want to contain %v", err, runErr)
		}
	})

	t.Run("writes both files before exec", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		runner := &fakeRunner{}
		da := DockerApplier{
			ACLPath:       filepath.Join(dir, "acl"),
			PasswdPath:    filepath.Join(dir, "passwd"),
			ContainerName: "test-container",
			Runner:        runner,
		}

		err := da.Apply(context.Background(), "my-acl", "my-passwd", "", "")
		if err != nil {
			t.Fatalf("Apply returned error: %v", err)
		}

		gotACL, err := os.ReadFile(da.ACLPath)
		if err != nil {
			t.Fatalf("ReadFile acl: %v", err)
		}
		if string(gotACL) != "my-acl" {
			t.Fatalf("acl content = %q, want %q", string(gotACL), "my-acl")
		}

		gotPasswd, err := os.ReadFile(da.PasswdPath)
		if err != nil {
			t.Fatalf("ReadFile passwd: %v", err)
		}
		if string(gotPasswd) != "my-passwd" {
			t.Fatalf("passwd content = %q, want %q", string(gotPasswd), "my-passwd")
		}
	})

	t.Run("empty ContainerName returns error before any I/O", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		runner := &fakeRunner{}
		da := DockerApplier{
			ACLPath:       filepath.Join(dir, "acl"),
			PasswdPath:    filepath.Join(dir, "passwd"),
			ContainerName: "",
			Runner:        runner,
		}

		err := da.Apply(context.Background(), "acl", "passwd", "", "")
		if err == nil {
			t.Fatal("Apply returned nil error, want error for empty ContainerName")
		}
		if !strings.Contains(err.Error(), "container_name") {
			t.Fatalf("error = %q, want message containing 'container_name'", err.Error())
		}
		// Verify no I/O was performed — neither file should exist.
		if _, statErr := os.Stat(da.ACLPath); statErr == nil {
			t.Fatal("ACL file was created before container_name check; expected no I/O")
		}
	})
}

func TestFileApplierContextCancellation(t *testing.T) {
	t.Parallel()

	t.Run("cancelled context returns error before I/O", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		ctx, cancel := context.WithTimeout(context.Background(), 0)
		defer cancel()
		// Ensure the context is already expired.
		time.Sleep(1 * time.Millisecond)

		fa := FileApplier{
			ACLPath:    filepath.Join(dir, "acl"),
			PasswdPath: filepath.Join(dir, "passwd"),
		}

		err := fa.Apply(ctx, "acl-content", "passwd-content", "", "")
		if err == nil {
			t.Fatal("Apply returned nil error, want context error")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Apply error = %v, want context.DeadlineExceeded", err)
		}
	})
}

func TestFileApplierInvalidPIDContent(t *testing.T) {
	t.Parallel()

	t.Run("non-integer PID content returns parse error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		pidPath := filepath.Join(dir, "mosquitto.pid")
		if err := os.WriteFile(pidPath, []byte("not-a-pid\n"), 0o600); err != nil {
			t.Fatalf("WriteFile pid: %v", err)
		}

		fa := FileApplier{
			ACLPath:    filepath.Join(dir, "acl"),
			PasswdPath: filepath.Join(dir, "passwd"),
			PIDPath:    pidPath,
		}

		err := fa.Apply(context.Background(), "acl-content", "passwd-content", "", "")
		if err == nil {
			t.Fatal("Apply returned nil error, want parse error for invalid PID content")
		}
		if !strings.Contains(err.Error(), "parse pid") {
			t.Fatalf("error = %q, want message containing 'parse pid'", err.Error())
		}
		// Both files should be written before the PID parse attempt.
		if _, statErr := os.Stat(filepath.Join(dir, "acl")); statErr != nil {
			t.Fatalf("ACL file not written before PID check: %v", statErr)
		}
		if _, statErr := os.Stat(filepath.Join(dir, "passwd")); statErr != nil {
			t.Fatalf("passwd file not written before PID check: %v", statErr)
		}
	})
}

// TestFileApplierApply_Transactional covers the four partial-failure scenarios
// required by issue #292: the second file write must NOT leave the first file
// half-applied, a failed rename must roll back, a failed SIGHUP must roll
// back, and a pre-cancelled context must not touch either file. On successful
// internal rollback the returned error wraps ErrApplyRestored; on rollback
// failure it wraps ErrRollbackFailed.
func TestFileApplierApply_Transactional(t *testing.T) {
	t.Parallel()

	t.Run("second file write fails leaves first untouched", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		aclPath := filepath.Join(dir, "acl")
		passwdDir := filepath.Join(dir, "passwd_dir")
		passwdPath := filepath.Join(passwdDir, "passwd")

		if err := os.MkdirAll(passwdDir, 0o700); err != nil {
			t.Fatalf("mkdir passwd_dir: %v", err)
		}
		if err := os.WriteFile(aclPath, []byte("old acl"), 0o600); err != nil {
			t.Fatalf("seed acl: %v", err)
		}
		if err := os.WriteFile(passwdPath, []byte("old passwd"), 0o600); err != nil {
			t.Fatalf("seed passwd: %v", err)
		}

		// Make passwdDir read-only so the applier cannot create a temp file
		// inside it for the second write. The ACL parent (dir) is still
		// writable, but neither file has been renamed yet when the second
		// write fails — so neither file is touched on disk and no
		// rollback is needed.
		if err := os.Chmod(passwdDir, 0o500); err != nil {
			t.Fatalf("chmod passwd_dir: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(passwdDir, 0o700) })

		fa := FileApplier{ACLPath: aclPath, PasswdPath: passwdPath}

		err := fa.Apply(context.Background(), "new acl", "new passwd", "old acl", "old passwd")
		if err == nil {
			t.Fatal("Apply returned nil error, want error when passwd write fails")
		}
		if errors.Is(err, ErrApplyRestored) {
			t.Errorf("error wraps ErrApplyRestored; no rollback needed when nothing was renamed")
		}
		if errors.Is(err, ErrRollbackFailed) {
			t.Errorf("error wraps ErrRollbackFailed; no rollback was attempted")
		}

		// Restore perms so we can read back the files.
		if err := os.Chmod(passwdDir, 0o700); err != nil {
			t.Fatalf("chmod passwd_dir back: %v", err)
		}

		// ACL must be untouched (still snapshot). The temp file we
		// staged for ACL was cleaned up by the applier on second-write
		// failure, so the broker is serving the previous configuration.
		gotACL, err := os.ReadFile(aclPath)
		if err != nil {
			t.Fatalf("ReadFile acl after failure: %v", err)
		}
		if string(gotACL) != "old acl" {
			t.Errorf("acl on disk = %q, want %q (untouched)", string(gotACL), "old acl")
		}
		// Passwd must be untouched.
		gotPasswd, err := os.ReadFile(passwdPath)
		if err != nil {
			t.Fatalf("ReadFile passwd: %v", err)
		}
		if string(gotPasswd) != "old passwd" {
			t.Errorf("passwd on disk = %q, want %q (untouched)", string(gotPasswd), "old passwd")
		}
	})

	t.Run("SIGHUP failure restores both files from snapshot", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		aclPath := filepath.Join(dir, "acl")
		passwdPath := filepath.Join(dir, "passwd")
		pidPath := filepath.Join(dir, "mosquitto.pid")

		if err := os.WriteFile(aclPath, []byte("old acl"), 0o600); err != nil {
			t.Fatalf("seed acl: %v", err)
		}
		if err := os.WriteFile(passwdPath, []byte("old passwd"), 0o600); err != nil {
			t.Fatalf("seed passwd: %v", err)
		}
		if err := os.WriteFile(pidPath, []byte("12345\n"), 0o600); err != nil {
			t.Fatalf("seed pid: %v", err)
		}

		fa := FileApplier{
			ACLPath:    aclPath,
			PasswdPath: passwdPath,
			PIDPath:    pidPath,
			SignalFunc: func(pid int) error {
				return fmt.Errorf("signal: process %d not found", pid)
			},
		}

		err := fa.Apply(context.Background(), "new acl", "new passwd", "old acl", "old passwd")
		if err == nil {
			t.Fatal("Apply returned nil error, want error when SIGHUP fails")
		}
		if !errors.Is(err, ErrApplyRestored) {
			t.Errorf("error = %v, want wrap of ErrApplyRestored", err)
		}

		// Both files must be reverted to the snapshot.
		gotACL, err := os.ReadFile(aclPath)
		if err != nil {
			t.Fatalf("ReadFile acl after rollback: %v", err)
		}
		if string(gotACL) != "old acl" {
			t.Errorf("acl on disk = %q, want %q (snapshot restored)", string(gotACL), "old acl")
		}
		gotPasswd, err := os.ReadFile(passwdPath)
		if err != nil {
			t.Fatalf("ReadFile passwd after rollback: %v", err)
		}
		if string(gotPasswd) != "old passwd" {
			t.Errorf("passwd on disk = %q, want %q (snapshot restored)", string(gotPasswd), "old passwd")
		}
	})

	t.Run("rollback also failing wraps ErrRollbackFailed", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		aclPath := filepath.Join(dir, "acl")
		passwdPath := filepath.Join(dir, "passwd")

		if err := os.WriteFile(aclPath, []byte("old acl"), 0o600); err != nil {
			t.Fatalf("seed acl: %v", err)
		}
		if err := os.WriteFile(passwdPath, []byte("old passwd"), 0o600); err != nil {
			t.Fatalf("seed passwd: %v", err)
		}

		// Replace the passwd file with a NON-EMPTY directory so that
		// BOTH the initial apply's passwd rename AND the rollback's
		// passwd rename fail (os.Rename cannot replace a non-empty dir).
		// The ACL rename still succeeds (so rollback IS triggered), but
		// the rollback cannot write the snapshot passwd back.
		if err := os.Remove(passwdPath); err != nil {
			t.Fatalf("remove passwd: %v", err)
		}
		if err := os.MkdirAll(passwdPath, 0o700); err != nil {
			t.Fatalf("mkdir passwd: %v", err)
		}
		if err := os.WriteFile(filepath.Join(passwdPath, "marker"), []byte("x"), 0o600); err != nil {
			t.Fatalf("marker: %v", err)
		}

		// Provide a PIDPath so the apply progresses past the
		// ErrReloadNotSignaled guard (issue #293). The path doesn't
		// need to point at a real file — we want the rename of the
		// passwd to fail, not the SIGHUP.
		pidPath := filepath.Join(dir, "mosquitto.pid")
		if err := os.WriteFile(pidPath, []byte("12345\n"), 0o600); err != nil {
			t.Fatalf("seed pid: %v", err)
		}
		fa := FileApplier{ACLPath: aclPath, PasswdPath: passwdPath, PIDPath: pidPath}

		err := fa.Apply(context.Background(), "new acl", "new passwd", "old acl", "old passwd")
		if err == nil {
			t.Fatal("Apply returned nil error, want error")
		}
		if !errors.Is(err, ErrRollbackFailed) {
			t.Errorf("error = %v, want wrap of ErrRollbackFailed", err)
		}
	})

	t.Run("pre-cancelled context leaves files untouched", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		aclPath := filepath.Join(dir, "acl")
		passwdPath := filepath.Join(dir, "passwd")

		if err := os.WriteFile(aclPath, []byte("old acl"), 0o600); err != nil {
			t.Fatalf("seed acl: %v", err)
		}
		if err := os.WriteFile(passwdPath, []byte("old passwd"), 0o600); err != nil {
			t.Fatalf("seed passwd: %v", err)
		}

		fa := FileApplier{ACLPath: aclPath, PasswdPath: passwdPath}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := fa.Apply(ctx, "new acl", "new passwd", "old acl", "old passwd")
		if err == nil {
			t.Fatal("Apply returned nil error, want context error")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want context.Canceled", err)
		}
		// Neither file must have been touched.
		gotACL, err := os.ReadFile(aclPath)
		if err != nil {
			t.Fatalf("ReadFile acl: %v", err)
		}
		if string(gotACL) != "old acl" {
			t.Errorf("acl on disk = %q, want %q (untouched)", string(gotACL), "old acl")
		}
		gotPasswd, err := os.ReadFile(passwdPath)
		if err != nil {
			t.Fatalf("ReadFile passwd: %v", err)
		}
		if string(gotPasswd) != "old passwd" {
			t.Errorf("passwd on disk = %q, want %q (untouched)", string(gotPasswd), "old passwd")
		}
	})
}

// TestDockerApplierApply_Transactional covers the same partial-failure
// scenarios for DockerApplier: a docker exec failure must restore both files
// from snapshot.
func TestDockerApplierApply_Transactional(t *testing.T) {
	t.Parallel()

	t.Run("docker exec failure restores both files from snapshot", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		aclPath := filepath.Join(dir, "acl")
		passwdPath := filepath.Join(dir, "passwd")

		if err := os.WriteFile(aclPath, []byte("old acl"), 0o600); err != nil {
			t.Fatalf("seed acl: %v", err)
		}
		if err := os.WriteFile(passwdPath, []byte("old passwd"), 0o600); err != nil {
			t.Fatalf("seed passwd: %v", err)
		}

		runErr := errors.New("container not running")
		da := DockerApplier{
			ACLPath:       aclPath,
			PasswdPath:    passwdPath,
			ContainerName: "mosquitto-broker",
			Runner:        &fakeRunner{err: runErr},
		}

		err := da.Apply(context.Background(), "new acl", "new passwd", "old acl", "old passwd")
		if err == nil {
			t.Fatal("Apply returned nil error, want error when docker exec fails")
		}
		if !errors.Is(err, ErrApplyRestored) {
			t.Errorf("error = %v, want wrap of ErrApplyRestored", err)
		}

		gotACL, err := os.ReadFile(aclPath)
		if err != nil {
			t.Fatalf("ReadFile acl: %v", err)
		}
		if string(gotACL) != "old acl" {
			t.Errorf("acl on disk = %q, want %q (snapshot restored)", string(gotACL), "old acl")
		}
		gotPasswd, err := os.ReadFile(passwdPath)
		if err != nil {
			t.Fatalf("ReadFile passwd: %v", err)
		}
		if string(gotPasswd) != "old passwd" {
			t.Errorf("passwd on disk = %q, want %q (snapshot restored)", string(gotPasswd), "old passwd")
		}
	})
}
