package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/storage"
)

func TestBackupCreateReturnsErrorForMissingDatabase(t *testing.T) {
	dir := t.TempDir()
	configPath := writeBackupConfig(t, filepath.Join(dir, "missing.db"))
	backupPath := filepath.Join(dir, "backup.db")

	output, err := executeForTest("backup", "create", "--config", configPath, "--output", backupPath)
	if err == nil {
		t.Fatal("backup create succeeded for missing database, want error")
	}
	if !strings.Contains(output, "does not exist") {
		t.Fatalf("backup create output missing missing database message; got:\n%s", output)
	}
	if _, statErr := os.Stat(backupPath); !os.IsNotExist(statErr) {
		t.Fatalf("backup file exists after failed create; stat err=%v", statErr)
	}
}

func TestBackupCreateAndRestoreSuccess(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mcm.db")
	createTestDatabase(t, dbPath)
	configPath := writeBackupConfig(t, dbPath)
	backupPath := filepath.Join(dir, "backup.db")

	output, err := executeForTest("backup", "create", "--config", configPath, "--output", backupPath)
	if err != nil {
		t.Fatalf("backup create returned error: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "Backup created") {
		t.Fatalf("backup create output missing success message; got:\n%s", output)
	}

	restoredPath := filepath.Join(dir, "restored.db")
	restoreConfigPath := writeBackupConfig(t, restoredPath)
	restoreOutput, err := executeForTest("backup", "restore", "--config", restoreConfigPath, "--input", backupPath)
	if err != nil {
		t.Fatalf("backup restore returned error: %v\noutput:\n%s", err, restoreOutput)
	}
	if !strings.Contains(restoreOutput, "Backup restored") {
		t.Fatalf("backup restore output missing success message; got:\n%s", restoreOutput)
	}

	store, err := storage.Open(restoredPath)
	if err != nil {
		t.Fatalf("Open restored database returned error: %v", err)
	}
	defer store.Close()
	users, err := store.ListAdminUsers(context.Background())
	if err != nil {
		t.Fatalf("ListAdminUsers returned error: %v", err)
	}
	if len(users) != 1 || users[0].Username != "backup-admin" {
		t.Fatalf("restored users = %#v, want backup-admin", users)
	}
}

func TestBackupRestoreRejectsInvalidBackup(t *testing.T) {
	dir := t.TempDir()
	invalidPath := filepath.Join(dir, "invalid.db")
	if err := os.WriteFile(invalidPath, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	configPath := writeBackupConfig(t, filepath.Join(dir, "mcm.db"))

	output, err := executeForTest("backup", "restore", "--config", configPath, "--input", invalidPath)
	if err == nil {
		t.Fatal("backup restore succeeded for invalid backup, want error")
	}
	if !strings.Contains(output, "invalid backup") {
		t.Fatalf("backup restore output missing invalid backup message; got:\n%s", output)
	}
}

func TestBackupRestoreRequiresForceToOverwrite(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.db")
	createTestDatabase(t, sourcePath)
	backupPath := filepath.Join(dir, "backup.db")
	createConfigPath := writeBackupConfig(t, sourcePath)
	if output, err := executeForTest("backup", "create", "--config", createConfigPath, "--output", backupPath); err != nil {
		t.Fatalf("backup create returned error: %v\noutput:\n%s", err, output)
	}

	targetPath := filepath.Join(dir, "target.db")
	createTestDatabase(t, targetPath)
	restoreConfigPath := writeBackupConfig(t, targetPath)

	output, err := executeForTest("backup", "restore", "--config", restoreConfigPath, "--input", backupPath)
	if err == nil {
		t.Fatal("backup restore overwrote existing database without --force, want error")
	}
	if !strings.Contains(output, "--force") {
		t.Fatalf("backup restore output missing force guidance; got:\n%s", output)
	}

	forceOutput, err := executeForTest("backup", "restore", "--config", restoreConfigPath, "--input", backupPath, "--force")
	if err != nil {
		t.Fatalf("backup restore --force returned error: %v\noutput:\n%s", err, forceOutput)
	}
	if !strings.Contains(forceOutput, "Backup restored") {
		t.Fatalf("backup restore --force output missing success message; got:\n%s", forceOutput)
	}
}

func createTestDatabase(t *testing.T, path string) {
	t.Helper()

	store, err := storage.Open(path)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	if _, err := store.CreateAdminUser(context.Background(), storage.CreateAdminUserParams{
		Username:     "backup-admin",
		PasswordHash: "hash",
	}); err != nil {
		t.Fatalf("CreateAdminUser returned error: %v", err)
	}
}

func writeBackupConfig(t *testing.T, dbPath string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "mcm.yaml")
	content := `http:
  bind_address: 127.0.0.1
  port: 8080
database:
  path: "` + dbPath + `"
auth:
  jwt_secret: replace-this-secret-with-at-least-32-characters
  token_ttl: 24h
  bootstrap_admin:
    username: admin
    password: change-this-admin-password
mosquitto:
  host: 127.0.0.1
  port: 1883
  username: ""
  password: ""
  tls:
    enabled: false
logging:
  level: info
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	return path
}
