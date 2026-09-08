package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"log/slog"

	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
)

// TestCheckDeployCapabilities covers issue #294 startup capability
// checks: paths writable, PID file readable, reload command present
// when in file mode.
func TestCheckDeployCapabilities(t *testing.T) {
	t.Parallel()

	t.Run("file mode: writable paths pass", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		aclPath := filepath.Join(dir, "acl")
		passwdPath := filepath.Join(dir, "passwd")
		pidPath := filepath.Join(dir, "mosquitto.pid")
		if err := os.WriteFile(pidPath, []byte("12345\n"), 0o600); err != nil {
			t.Fatalf("seed pid: %v", err)
		}

		deployCfg := config.DeployConfig{
			Mode:       "file",
			ACLPath:    aclPath,
			PasswdPath: passwdPath,
			PIDPath:    pidPath,
		}
		applier := mosquitto.FileApplier{
			ACLPath:    aclPath,
			PasswdPath: passwdPath,
			PIDPath:    pidPath,
		}
		if err := checkDeployCapabilities(deployCfg, applier, discardLogger()); err != nil {
			t.Fatalf("checkDeployCapabilities: %v", err)
		}
	})

	t.Run("file mode: ACLPath parent not writable fails", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		readOnly := filepath.Join(dir, "ro")
		if err := os.MkdirAll(readOnly, 0o500); err != nil {
			t.Fatalf("mkdir read-only: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) })

		deployCfg := config.DeployConfig{
			Mode:       "file",
			ACLPath:    filepath.Join(readOnly, "acl"),
			PasswdPath: filepath.Join(dir, "passwd"),
			PIDPath:    filepath.Join(dir, "mosquitto.pid"),
		}
		applier := mosquitto.FileApplier{
			ACLPath:    deployCfg.ACLPath,
			PasswdPath: deployCfg.PasswdPath,
			PIDPath:    deployCfg.PIDPath,
		}
		err := checkDeployCapabilities(deployCfg, applier, discardLogger())
		if err == nil {
			t.Fatal("checkDeployCapabilities: want error when ACL parent is not writable")
		}
		if !strings.Contains(err.Error(), "not writable") {
			t.Errorf("error = %v, want it to mention 'not writable'", err)
		}
	})

	t.Run("file mode: missing PIDPath without ReloadCommand warns", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		deployCfg := config.DeployConfig{
			Mode:       "file",
			ACLPath:    filepath.Join(dir, "acl"),
			PasswdPath: filepath.Join(dir, "passwd"),
			// Neither PIDPath nor ReloadCommand set.
		}
		applier := mosquitto.FileApplier{
			ACLPath:    deployCfg.ACLPath,
			PasswdPath: deployCfg.PasswdPath,
		}
		// Should NOT fail — the apply will return ErrReloadNotSignaled
		// at runtime, which is the existing behavior (#293). The
		// capability check just logs a warning.
		if err := checkDeployCapabilities(deployCfg, applier, discardLogger()); err != nil {
			t.Fatalf("checkDeployCapabilities: %v (warning-only)", err)
		}
	})

	t.Run("file mode: ReloadCommand replaces PIDPath requirement", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		deployCfg := config.DeployConfig{
			Mode:          "file",
			ACLPath:       filepath.Join(dir, "acl"),
			PasswdPath:    filepath.Join(dir, "passwd"),
			ReloadCommand: []string{"/bin/true"},
		}
		applier := mosquitto.FileApplier{
			ACLPath:       deployCfg.ACLPath,
			PasswdPath:    deployCfg.PasswdPath,
			ReloadCommand: deployCfg.ReloadCommand,
			ReloadRunner:  mosquitto.ExecRunner{},
		}
		if err := checkDeployCapabilities(deployCfg, applier, discardLogger()); err != nil {
			t.Fatalf("checkDeployCapabilities: %v", err)
		}
	})

	t.Run("file mode: PIDPath unreadable fails", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		noRead := filepath.Join(dir, "noread")
		if err := os.MkdirAll(noRead, 0o000); err != nil {
			t.Fatalf("mkdir unreadable: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(noRead, 0o700) })
		pidPath := filepath.Join(noRead, "mosquitto.pid")

		deployCfg := config.DeployConfig{
			Mode:       "file",
			ACLPath:    filepath.Join(dir, "acl"),
			PasswdPath: filepath.Join(dir, "passwd"),
			PIDPath:    pidPath,
		}
		applier := mosquitto.FileApplier{
			ACLPath:    deployCfg.ACLPath,
			PasswdPath: deployCfg.PasswdPath,
			PIDPath:    pidPath,
		}
		err := checkDeployCapabilities(deployCfg, applier, discardLogger())
		if err == nil {
			t.Fatal("checkDeployCapabilities: want error when PID path is unreadable")
		}
		if !strings.Contains(err.Error(), "PIDPath") {
			t.Errorf("error = %v, want it to mention PIDPath", err)
		}
	})

	t.Run("disabled mode: skipped", func(t *testing.T) {
		t.Parallel()
		deployCfg := config.DeployConfig{} // Mode empty → deploy disabled
		applier := mosquitto.FileApplier{}
		if err := checkDeployCapabilities(deployCfg, applier, discardLogger()); err != nil {
			t.Fatalf("checkDeployCapabilities: %v", err)
		}
	})

	t.Run("docker mode: presence of container name passes", func(t *testing.T) {
		t.Parallel()
		deployCfg := config.DeployConfig{
			Mode:          "docker",
			ACLPath:       "/var/lib/mosquitto-config/acl",
			PasswdPath:    "/var/lib/mosquitto-config/passwd",
			ContainerName: "mcm-mosquitto",
		}
		applier := mosquitto.DockerApplier{
			ACLPath:       deployCfg.ACLPath,
			PasswdPath:    deployCfg.PasswdPath,
			ContainerName: deployCfg.ContainerName,
		}
		if err := checkDeployCapabilities(deployCfg, applier, discardLogger()); err != nil {
			t.Fatalf("checkDeployCapabilities: %v", err)
		}
	})

	t.Run("docker mode: missing container name fails", func(t *testing.T) {
		t.Parallel()
		deployCfg := config.DeployConfig{
			Mode:       "docker",
			ACLPath:    "/var/lib/mosquitto-config/acl",
			PasswdPath: "/var/lib/mosquitto-config/passwd",
		}
		applier := mosquitto.DockerApplier{
			ACLPath:    deployCfg.ACLPath,
			PasswdPath: deployCfg.PasswdPath,
		}
		err := checkDeployCapabilities(deployCfg, applier, discardLogger())
		if err == nil {
			t.Fatal("checkDeployCapabilities: want error when container name is missing")
		}
		if !strings.Contains(err.Error(), "container_name") {
			t.Errorf("error = %v, want it to mention container_name", err)
		}
	})
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(silentLoggerWriter{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

type silentLoggerWriter struct{}

func (silentLoggerWriter) Write(p []byte) (int, error) { return len(p), nil }

// ensure imports referenced are exercised when the file is compiled
// without the run.go wiring test suite.
var _ = time.Second
