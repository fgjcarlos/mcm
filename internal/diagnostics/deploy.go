package diagnostics

import (
	"fmt"
	"strings"

	"github.com/fgjcarlos/mcm/internal/config"
)

// DeployResult describes the outcome of a deploy configuration check.
type DeployResult struct {
	OK      bool
	Message string
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
		if strings.TrimSpace(cfg.PIDPath) == "" {
			missing = append(missing, "pid_path")
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
		return DeployResult{OK: true, Message: fmt.Sprintf("deploy file mode configured: acl=%s passwd=%s pid=%s", cfg.ACLPath, cfg.PasswdPath, cfg.PIDPath)}

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
		return DeployResult{OK: true, Message: fmt.Sprintf("deploy docker mode configured: container=%s acl=%s passwd=%s", cfg.ContainerName, cfg.ACLPath, cfg.PasswdPath)}

	default:
		return DeployResult{
			OK:      false,
			Message: fmt.Sprintf(`deploy mode must be "", "file", or "docker"; got %q`, mode),
		}
	}
}
