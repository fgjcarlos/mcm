package diagnostics

import (
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/config"
)

func TestCheckDeployConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     config.DeployConfig
		wantOK  bool
		wantMsg string // substring expected in Message
	}{
		{
			name:    "mode empty means deploy disabled — OK",
			cfg:     config.DeployConfig{},
			wantOK:  true,
			wantMsg: "disabled",
		},
		{
			name: "file mode all fields present — OK",
			cfg: config.DeployConfig{
				Mode:       "file",
				ACLPath:    "/etc/mosquitto/acl",
				PasswdPath: "/etc/mosquitto/passwd",
				PIDPath:    "/var/run/mosquitto.pid",
			},
			wantOK:  true,
			wantMsg: "file",
		},
		{
			name: "file mode missing acl_path — FAIL",
			cfg: config.DeployConfig{
				Mode:       "file",
				ACLPath:    "",
				PasswdPath: "/etc/mosquitto/passwd",
				PIDPath:    "/var/run/mosquitto.pid",
			},
			wantOK:  false,
			wantMsg: "acl_path",
		},
		{
			name: "file mode missing passwd_path — FAIL",
			cfg: config.DeployConfig{
				Mode:       "file",
				ACLPath:    "/etc/mosquitto/acl",
				PasswdPath: "",
				PIDPath:    "/var/run/mosquitto.pid",
			},
			wantOK:  false,
			wantMsg: "passwd_path",
		},
		{
			name: "file mode missing pid_path — FAIL",
			cfg: config.DeployConfig{
				Mode:       "file",
				ACLPath:    "/etc/mosquitto/acl",
				PasswdPath: "/etc/mosquitto/passwd",
				PIDPath:    "",
			},
			wantOK:  false,
			wantMsg: "pid_path",
		},
		{
			name: "docker mode all fields present — OK",
			cfg: config.DeployConfig{
				Mode:          "docker",
				ACLPath:       "/etc/mosquitto/acl",
				PasswdPath:    "/etc/mosquitto/passwd",
				ContainerName: "mosquitto",
			},
			wantOK:  true,
			wantMsg: "docker",
		},
		{
			name: "docker mode missing acl_path — FAIL",
			cfg: config.DeployConfig{
				Mode:          "docker",
				ACLPath:       "",
				PasswdPath:    "/etc/mosquitto/passwd",
				ContainerName: "mosquitto",
			},
			wantOK:  false,
			wantMsg: "acl_path",
		},
		{
			name: "docker mode missing passwd_path — FAIL",
			cfg: config.DeployConfig{
				Mode:          "docker",
				ACLPath:       "/etc/mosquitto/acl",
				PasswdPath:    "",
				ContainerName: "mosquitto",
			},
			wantOK:  false,
			wantMsg: "passwd_path",
		},
		{
			name: "docker mode missing container_name — FAIL",
			cfg: config.DeployConfig{
				Mode:          "docker",
				ACLPath:       "/etc/mosquitto/acl",
				PasswdPath:    "/etc/mosquitto/passwd",
				ContainerName: "",
			},
			wantOK:  false,
			wantMsg: "container_name",
		},
		{
			name: "acl_path equals passwd_path — FAIL",
			cfg: config.DeployConfig{
				Mode:          "docker",
				ACLPath:       "/etc/mosquitto/same",
				PasswdPath:    "/etc/mosquitto/same",
				ContainerName: "mosquitto",
			},
			wantOK:  false,
			wantMsg: "acl_path",
		},
		{
			name: "invalid mode — FAIL",
			cfg: config.DeployConfig{
				Mode: "kubernetes",
			},
			wantOK:  false,
			wantMsg: "mode",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := CheckDeployConfig(tc.cfg)
			if result.OK != tc.wantOK {
				t.Fatalf("CheckDeployConfig OK = %v, want %v; message: %q", result.OK, tc.wantOK, result.Message)
			}
			if tc.wantMsg != "" && !strings.Contains(result.Message, tc.wantMsg) {
				t.Fatalf("CheckDeployConfig message = %q, want substring %q", result.Message, tc.wantMsg)
			}
		})
	}
}
