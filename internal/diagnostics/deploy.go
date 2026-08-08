package diagnostics

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fgjcarlos/mcm/internal/config"
)

// DeployResult describes the outcome of a deploy configuration check.
type DeployResult struct {
	OK      bool
	Message string
}

// checkDirWritable verifies that the parent directory of path exists and is
// writable by attempting to create and immediately remove a temporary file.
func checkDirWritable(path string) error {
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("directory %q does not exist or is not accessible: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", dir)
	}
	tmp, err := os.CreateTemp(dir, ".mcm-write-check-*")
	if err != nil {
		return fmt.Errorf("directory %q is not writable: %w", dir, err)
	}
	//nolint:errcheck // best-effort cleanup of a probe file; diagnostic only
	tmp.Close()
	//nolint:errcheck // best-effort cleanup of a probe file; diagnostic only
	os.Remove(tmp.Name())
	return nil
}

// CheckDeployConfig validates the deploy section of the Mosquitto configuration.
// If Mode is empty the deploy feature is disabled and the result is OK.
func CheckDeployConfig(cfg config.DeployConfig) DeployResult {
	mode := strings.TrimSpace(cfg.Mode)

	if mode == "" {
		return DeployResult{OK: true, Message: "deploy is disabled (mosquitto.deploy.mode is empty)"}
	}

	switch mode {
	case "file":
		var missing []string
		if strings.TrimSpace(cfg.ACLPath) == "" {
			missing = append(missing, "acl_path")
		}
		if strings.TrimSpace(cfg.PasswdPath) == "" {
			missing = append(missing, "passwd_path")
		}
		if len(missing) > 0 {
			return DeployResult{
				OK:      false,
				Message: fmt.Sprintf("deploy file mode is missing required fields: %s", strings.Join(missing, ", ")),
			}
		}
		aclPath := strings.TrimSpace(cfg.ACLPath)
		passwdPath := strings.TrimSpace(cfg.PasswdPath)
		if aclPath == passwdPath {
			return DeployResult{
				OK:      false,
				Message: "deploy acl_path and passwd_path must not be the same path",
			}
		}
		if err := checkDirWritable(aclPath); err != nil {
			return DeployResult{
				OK:      false,
				Message: fmt.Sprintf("acl_path parent directory check failed: %v; ensure the directory exists and the process has write permission", err),
			}
		}
		if err := checkDirWritable(passwdPath); err != nil {
			return DeployResult{
				OK:      false,
				Message: fmt.Sprintf("passwd_path parent directory check failed: %v; ensure the directory exists and the process has write permission", err),
			}
		}
		return DeployResult{OK: true, Message: fmt.Sprintf("deploy file mode configured: acl=%s passwd=%s — parent directories are writable", cfg.ACLPath, cfg.PasswdPath)}

	case "docker":
		var missing []string
		if strings.TrimSpace(cfg.ACLPath) == "" {
			missing = append(missing, "acl_path")
		}
		if strings.TrimSpace(cfg.PasswdPath) == "" {
			missing = append(missing, "passwd_path")
		}
		if strings.TrimSpace(cfg.ContainerName) == "" {
			missing = append(missing, "container_name")
		}
		if len(missing) > 0 {
			return DeployResult{
				OK:      false,
				Message: fmt.Sprintf("deploy docker mode is missing required fields: %s", strings.Join(missing, ", ")),
			}
		}
		aclPath := strings.TrimSpace(cfg.ACLPath)
		passwdPath := strings.TrimSpace(cfg.PasswdPath)
		if aclPath == passwdPath {
			return DeployResult{
				OK:      false,
				Message: "deploy acl_path and passwd_path must not be the same path",
			}
		}
		if err := checkDirWritable(aclPath); err != nil {
			return DeployResult{
				OK:      false,
				Message: fmt.Sprintf("acl_path parent directory check failed: %v; ensure the directory exists and the process has write permission", err),
			}
		}
		if err := checkDirWritable(passwdPath); err != nil {
			return DeployResult{
				OK:      false,
				Message: fmt.Sprintf("passwd_path parent directory check failed: %v; ensure the directory exists and the process has write permission", err),
			}
		}
		if _, err := exec.LookPath("docker"); err != nil {
			return DeployResult{
				OK:      false,
				Message: "docker binary not found on PATH; install Docker or add the docker binary to PATH",
			}
		}
		return DeployResult{OK: true, Message: fmt.Sprintf("deploy docker mode configured: container=%s acl=%s passwd=%s — directories writable, docker binary found", cfg.ContainerName, cfg.ACLPath, cfg.PasswdPath)}

	default:
		return DeployResult{
			OK:      false,
			Message: fmt.Sprintf(`deploy mode must be "", "file", or "docker"; got %q`, mode),
		}
	}
}
