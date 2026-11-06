package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeBrokerConfig writes a broker configuration file and returns its path.
func writeBrokerConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mosquitto.conf")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write broker config: %v", err)
	}
	return path
}

func TestCheckBrokerEffectiveFiles(t *testing.T) {
	t.Parallel()

	t.Run("aligned paths pass", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		confPath := writeBrokerConfig(t, "allow_anonymous false\n"+
			"password_file /mosquitto/config/passwd\n"+
			"acl_file /mosquitto/config/acl\n")
		err := CheckBrokerEffectiveFiles(EffectiveFilesOptions{
			BrokerConfigPath: confPath,
			BrokerConfigDir:  "/mosquitto/config",
			ACLPath:          filepath.Join(dir, "acl"),
			PasswdPath:       filepath.Join(dir, "passwd"),
		})
		if err != nil {
			t.Fatalf("CheckBrokerEffectiveFiles: %v", err)
		}
	})

	t.Run("broker reads another password file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		confPath := writeBrokerConfig(t, "password_file /mosquitto/config/passwords\n"+
			"acl_file /mosquitto/config/acl\n")
		err := CheckBrokerEffectiveFiles(EffectiveFilesOptions{
			BrokerConfigPath: confPath,
			BrokerConfigDir:  "/mosquitto/config",
			ACLPath:          filepath.Join(dir, "acl"),
			PasswdPath:       filepath.Join(dir, "passwd"),
		})
		if err == nil {
			t.Fatal("CheckBrokerEffectiveFiles: want error for a mismatching password_file")
		}
		if !errors.Is(err, ErrBrokerEffectiveFilesMismatch) {
			t.Errorf("error = %v, want ErrBrokerEffectiveFilesMismatch", err)
		}
		if !strings.Contains(err.Error(), "password_file") {
			t.Errorf("error = %v, want it to mention password_file", err)
		}
	})

	t.Run("broker declares no acl_file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		confPath := writeBrokerConfig(t, "password_file /mosquitto/config/passwd\n")
		err := CheckBrokerEffectiveFiles(EffectiveFilesOptions{
			BrokerConfigPath: confPath,
			BrokerConfigDir:  "/mosquitto/config",
			ACLPath:          filepath.Join(dir, "acl"),
			PasswdPath:       filepath.Join(dir, "passwd"),
		})
		if err == nil {
			t.Fatal("CheckBrokerEffectiveFiles: want error when the broker declares no acl_file")
		}
		if !errors.Is(err, ErrBrokerEffectiveFilesMismatch) {
			t.Errorf("error = %v, want ErrBrokerEffectiveFilesMismatch", err)
		}
		if !strings.Contains(err.Error(), "acl_file") {
			t.Errorf("error = %v, want it to mention acl_file", err)
		}
	})

	t.Run("broker reads another acl file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		confPath := writeBrokerConfig(t, "password_file /mosquitto/config/passwd\n"+
			"acl_file /mosquitto/config/acl.local\n")
		err := CheckBrokerEffectiveFiles(EffectiveFilesOptions{
			BrokerConfigPath: confPath,
			BrokerConfigDir:  "/mosquitto/config",
			ACLPath:          filepath.Join(dir, "acl"),
			PasswdPath:       filepath.Join(dir, "passwd"),
		})
		if err == nil {
			t.Fatal("CheckBrokerEffectiveFiles: want error for a mismatching acl_file")
		}
		if !errors.Is(err, ErrBrokerEffectiveFilesMismatch) {
			t.Errorf("error = %v, want ErrBrokerEffectiveFilesMismatch", err)
		}
	})

	t.Run("per-listener acl_file does not replace the broker-wide file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		confPath := writeBrokerConfig(t, "password_file /mosquitto/config/passwd\n"+
			"acl_file /mosquitto/config/acl\n"+
			"listener 8883 {\n"+
			"  acl_file /mosquitto/config/acl.listener\n"+
			"}\n")
		err := CheckBrokerEffectiveFiles(EffectiveFilesOptions{
			BrokerConfigPath: confPath,
			BrokerConfigDir:  "/mosquitto/config",
			ACLPath:          filepath.Join(dir, "acl"),
			PasswdPath:       filepath.Join(dir, "passwd"),
		})
		if err != nil {
			t.Fatalf("CheckBrokerEffectiveFiles: %v", err)
		}
	})

	t.Run("no broker config path disables the check", func(t *testing.T) {
		t.Parallel()
		if err := CheckBrokerEffectiveFiles(EffectiveFilesOptions{}); err != nil {
			t.Fatalf("CheckBrokerEffectiveFiles: %v", err)
		}
	})

	t.Run("unreadable broker config is reported", func(t *testing.T) {
		t.Parallel()
		err := CheckBrokerEffectiveFiles(EffectiveFilesOptions{
			BrokerConfigPath: filepath.Join(t.TempDir(), "missing.conf"),
			ACLPath:          "/tmp/acl",
			PasswdPath:       "/tmp/passwd",
		})
		if err == nil {
			t.Fatal("CheckBrokerEffectiveFiles: want error for an unreadable broker config")
		}
		if errors.Is(err, ErrBrokerEffectiveFilesMismatch) {
			t.Errorf("error = %v, want a read error rather than a mismatch", err)
		}
	})
}

func TestMapBrokerPath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		broker    string
		brokerDir string
		mcmDir    string
		want      string
	}{
		{name: "same volume, different mount points", broker: "/mosquitto/config/passwd", brokerDir: "/mosquitto/config", mcmDir: "/var/lib/mosquitto-config", want: "/var/lib/mosquitto-config/passwd"},
		{name: "no broker dir keeps the broker path", broker: "/mosquitto/config/passwd", brokerDir: "", mcmDir: "/var/lib/mosquitto-config", want: "/mosquitto/config/passwd"},
		{name: "path outside the broker dir is untouched", broker: "/etc/mosquitto/passwd", brokerDir: "/mosquitto/config", mcmDir: "/var/lib/mosquitto-config", want: "/etc/mosquitto/passwd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := mapBrokerPath(tc.broker, tc.brokerDir, tc.mcmDir); got != tc.want {
				t.Errorf("mapBrokerPath = %q, want %q", got, tc.want)
			}
		})
	}
}
