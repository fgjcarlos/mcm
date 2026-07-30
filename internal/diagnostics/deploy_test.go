package diagnostics

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/config"
)

func TestCheckDeployConfig(t *testing.T) {
	t.Parallel()

	t.Run("mode empty means deploy disabled — OK", func(t *testing.T) {
		t.Parallel()
		result := CheckDeployConfig(config.DeployConfig{})
		if !result.OK {
			t.Fatalf("CheckDeployConfig OK = false, want true; message: %q", result.Message)
		}
		if !strings.Contains(result.Message, "disabled") {
			t.Fatalf("message = %q, want substring %q", result.Message, "disabled")
		}
	})

	t.Run("file mode all fields present and directories writable — OK", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		result := CheckDeployConfig(config.DeployConfig{
			Mode:       "file",
			ACLPath:    filepath.Join(dir, "acl"),
			PasswdPath: filepath.Join(dir, "passwd"),
		})
		if !result.OK {
			t.Fatalf("CheckDeployConfig OK = false, want true; message: %q", result.Message)
		}
		if !strings.Contains(result.Message, "file") {
			t.Fatalf("message = %q, want substring %q", result.Message, "file")
		}
	})

	t.Run("file mode with pid_path — OK (pid_path is optional)", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		result := CheckDeployConfig(config.DeployConfig{
			Mode:       "file",
			ACLPath:    filepath.Join(dir, "acl"),
			PasswdPath: filepath.Join(dir, "passwd"),
			PIDPath:    filepath.Join(dir, "mosquitto.pid"),
		})
		if !result.OK {
			t.Fatalf("CheckDeployConfig OK = false, want true; message: %q", result.Message)
		}
	})

	t.Run("file mode missing acl_path — FAIL", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		result := CheckDeployConfig(config.DeployConfig{
			Mode:       "file",
			ACLPath:    "",
			PasswdPath: filepath.Join(dir, "passwd"),
		})
		if result.OK {
			t.Fatal("CheckDeployConfig OK = true, want false")
		}
		if !strings.Contains(result.Message, "acl_path") {
			t.Fatalf("message = %q, want substring %q", result.Message, "acl_path")
		}
	})

	t.Run("file mode missing passwd_path — FAIL", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		result := CheckDeployConfig(config.DeployConfig{
			Mode:       "file",
			ACLPath:    filepath.Join(dir, "acl"),
			PasswdPath: "",
		})
		if result.OK {
			t.Fatal("CheckDeployConfig OK = true, want false")
		}
		if !strings.Contains(result.Message, "passwd_path") {
			t.Fatalf("message = %q, want substring %q", result.Message, "passwd_path")
		}
	})

	t.Run("file mode acl directory not writable — FAIL", func(t *testing.T) {
		t.Parallel()
		// Use a path in a non-existent directory to trigger the writability failure.
		result := CheckDeployConfig(config.DeployConfig{
			Mode:       "file",
			ACLPath:    "/nonexistent-mcm-test-dir/acl",
			PasswdPath: "/nonexistent-mcm-test-dir/passwd",
		})
		if result.OK {
			t.Fatal("CheckDeployConfig OK = true, want false for non-writable directory")
		}
		if !strings.Contains(result.Message, "acl_path") {
			t.Fatalf("message = %q, want substring %q", result.Message, "acl_path")
		}
	})

	t.Run("file mode passwd directory not writable — FAIL", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		// ACL dir exists and is writable; passwd dir does not exist.
		result := CheckDeployConfig(config.DeployConfig{
			Mode:       "file",
			ACLPath:    filepath.Join(dir, "acl"),
			PasswdPath: "/nonexistent-mcm-test-dir/passwd",
		})
		if result.OK {
			t.Fatal("CheckDeployConfig OK = true, want false for non-writable passwd directory")
		}
		if !strings.Contains(result.Message, "passwd_path") {
			t.Fatalf("message = %q, want substring %q", result.Message, "passwd_path")
		}
	})

	t.Run("docker mode all fields present and directories writable — OK", func(t *testing.T) {
		t.Parallel()
		// Skip if docker is not present — this test verifies the positive path.
		if _, err := exec.LookPath("docker"); err != nil {
			t.Skip("docker not on PATH; skipping docker-ok test")
		}
		dir := t.TempDir()
		result := CheckDeployConfig(config.DeployConfig{
			Mode:          "docker",
			ACLPath:       filepath.Join(dir, "acl"),
			PasswdPath:    filepath.Join(dir, "passwd"),
			ContainerName: "mosquitto",
		})
		if !result.OK {
			t.Fatalf("CheckDeployConfig OK = false, want true; message: %q", result.Message)
		}
		if !strings.Contains(result.Message, "docker") {
			t.Fatalf("message = %q, want substring %q", result.Message, "docker")
		}
	})

	t.Run("docker mode missing acl_path — FAIL", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		result := CheckDeployConfig(config.DeployConfig{
			Mode:          "docker",
			ACLPath:       "",
			PasswdPath:    filepath.Join(dir, "passwd"),
			ContainerName: "mosquitto",
		})
		if result.OK {
			t.Fatal("CheckDeployConfig OK = true, want false")
		}
		if !strings.Contains(result.Message, "acl_path") {
			t.Fatalf("message = %q, want substring %q", result.Message, "acl_path")
		}
	})

	t.Run("docker mode missing passwd_path — FAIL", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		result := CheckDeployConfig(config.DeployConfig{
			Mode:          "docker",
			ACLPath:       filepath.Join(dir, "acl"),
			PasswdPath:    "",
			ContainerName: "mosquitto",
		})
		if result.OK {
			t.Fatal("CheckDeployConfig OK = true, want false")
		}
		if !strings.Contains(result.Message, "passwd_path") {
			t.Fatalf("message = %q, want substring %q", result.Message, "passwd_path")
		}
	})

	t.Run("docker mode missing container_name — FAIL", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		result := CheckDeployConfig(config.DeployConfig{
			Mode:          "docker",
			ACLPath:       filepath.Join(dir, "acl"),
			PasswdPath:    filepath.Join(dir, "passwd"),
			ContainerName: "",
		})
		if result.OK {
			t.Fatal("CheckDeployConfig OK = true, want false")
		}
		if !strings.Contains(result.Message, "container_name") {
			t.Fatalf("message = %q, want substring %q", result.Message, "container_name")
		}
	})

	t.Run("docker mode acl_path equals passwd_path — FAIL", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		same := filepath.Join(dir, "same")
		result := CheckDeployConfig(config.DeployConfig{
			Mode:          "docker",
			ACLPath:       same,
			PasswdPath:    same,
			ContainerName: "mosquitto",
		})
		if result.OK {
			t.Fatal("CheckDeployConfig OK = true, want false")
		}
		if !strings.Contains(result.Message, "acl_path") {
			t.Fatalf("message = %q, want substring %q", result.Message, "acl_path")
		}
	})

	// docker binary check is tested separately in TestCheckDeployConfigDockerBinary
	// because t.Setenv is incompatible with parallel parent tests.

	t.Run("invalid mode — FAIL", func(t *testing.T) {
		t.Parallel()
		result := CheckDeployConfig(config.DeployConfig{
			Mode: "kubernetes",
		})
		if result.OK {
			t.Fatal("CheckDeployConfig OK = true, want false")
		}
		if !strings.Contains(result.Message, "mode") {
			t.Fatalf("message = %q, want substring %q", result.Message, "mode")
		}
	})
}

// TestCheckDirWritable tests the internal writability helper directly.
func TestCheckDirWritable(t *testing.T) {
	t.Parallel()

	t.Run("writable directory succeeds", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		err := checkDirWritable(filepath.Join(dir, "somefile.txt"))
		if err != nil {
			t.Fatalf("checkDirWritable returned error for writable dir: %v", err)
		}
	})

	t.Run("non-existent directory fails", func(t *testing.T) {
		t.Parallel()
		err := checkDirWritable("/nonexistent-mcm-test-dir/file.txt")
		if err == nil {
			t.Fatal("checkDirWritable returned nil for non-existent directory")
		}
	})

	t.Run("read-only directory fails", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			t.Skipf("unix-only: read-only enforcement not available on Windows")
		}
		// Skip on systems where we might be running as root.
		if os.Getuid() == 0 {
			t.Skip("skipping read-only test when running as root")
		}
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o444); err != nil {
			t.Fatalf("Chmod: %v", err)
		}
		t.Cleanup(func() { os.Chmod(dir, 0o755) })
		err := checkDirWritable(filepath.Join(dir, "file.txt"))
		if err == nil {
			t.Fatal("checkDirWritable returned nil for read-only directory")
		}
	})
}

// TestCheckDeployConfigDockerBinaryMissing tests the C4 requirement that the
// docker binary check fails when docker is not on PATH.
// This must be a separate top-level test because t.Setenv cannot be called in
// a subtest whose parent called t.Parallel().
func TestCheckDeployConfigDockerBinaryMissing(t *testing.T) {
	// Do NOT call t.Parallel() here — t.Setenv requires a sequential test.
	t.Setenv("PATH", "")
	dir := t.TempDir()
	result := CheckDeployConfig(config.DeployConfig{
		Mode:          "docker",
		ACLPath:       filepath.Join(dir, "acl"),
		PasswdPath:    filepath.Join(dir, "passwd"),
		ContainerName: "mosquitto",
	})
	if result.OK {
		t.Fatal("CheckDeployConfig OK = true, want false when docker not on PATH")
	}
	if !strings.Contains(result.Message, "docker") {
		t.Fatalf("message = %q, want substring containing %q", result.Message, "docker")
	}
}
