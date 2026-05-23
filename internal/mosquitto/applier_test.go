package mosquitto

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
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

		fa := FileApplier{
			ACLPath:    aclPath,
			PasswdPath: passwdPath,
		}

		err := fa.Apply(context.Background(), "acl-content", "passwd-content")
		if err != nil {
			t.Fatalf("Apply returned error: %v", err)
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

	t.Run("empty PIDPath skips SIGHUP", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		fa := FileApplier{
			ACLPath:    filepath.Join(dir, "acl"),
			PasswdPath: filepath.Join(dir, "passwd"),
			PIDPath:    "", // no PID file — skip reload
		}

		var killed bool
		orig := osKill
		osKill = func(_ int, _ syscall.Signal) error {
			killed = true
			return nil
		}
		t.Cleanup(func() { osKill = orig })

		err := fa.Apply(context.Background(), "a", "b")
		if err != nil {
			t.Fatalf("Apply returned error: %v", err)
		}
		if killed {
			t.Fatal("osKill was called with empty PIDPath, want no signal sent")
		}
	})

	t.Run("with PIDPath sends SIGHUP to process", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		pidPath := filepath.Join(dir, "mosquitto.pid")
		if err := os.WriteFile(pidPath, []byte("12345\n"), 0o600); err != nil {
			t.Fatalf("WriteFile pid: %v", err)
		}

		fa := FileApplier{
			ACLPath:    filepath.Join(dir, "acl"),
			PasswdPath: filepath.Join(dir, "passwd"),
			PIDPath:    pidPath,
		}

		var gotPID int
		var gotSig syscall.Signal
		orig := osKill
		osKill = func(pid int, sig syscall.Signal) error {
			gotPID = pid
			gotSig = sig
			return nil
		}
		t.Cleanup(func() { osKill = orig })

		err := fa.Apply(context.Background(), "a", "b")
		if err != nil {
			t.Fatalf("Apply returned error: %v", err)
		}
		if gotPID != 12345 {
			t.Fatalf("kill PID = %d, want 12345", gotPID)
		}
		if gotSig != syscall.SIGHUP {
			t.Fatalf("kill signal = %v, want SIGHUP", gotSig)
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

		err := fa.Apply(context.Background(), "a", "b")
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

		err := da.Apply(context.Background(), "acl-body", "passwd-body")
		if err != nil {
			t.Fatalf("Apply returned error: %v", err)
		}

		if runner.name != "docker" {
			t.Fatalf("runner.name = %q, want %q", runner.name, "docker")
		}
		wantArgs := []string{"exec", "mosquitto-broker", "kill", "-SIGHUP", "1"}
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

		err := da.Apply(context.Background(), "a", "b")
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

		err := da.Apply(context.Background(), "my-acl", "my-passwd")
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
}
