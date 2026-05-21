package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/spf13/cobra"
	_ "modernc.org/sqlite"
)

const backupTimeout = 30 * time.Second

func newBackupCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Back up and restore local MCM state",
		Long: strings.TrimSpace(`Back up and restore the local MCM SQLite database.

Backups include MCM state stored in SQLite, such as admin users, metrics, security events, and audit events. They do not include external Mosquitto configuration, password files, ACL files, TLS certificates, or other files referenced by configuration.`),
	}

	cmd.AddCommand(newBackupCreateCommand(), newBackupRestoreCommand())
	return cmd
}

func newBackupCreateCommand() *cobra.Command {
	var configPath string
	var outputPath string

	cmd := &cobra.Command{
		Use:          "create",
		Short:        "Create a portable SQLite backup artifact",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(outputPath) == "" {
				return fmt.Errorf("--output is required")
			}

			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), backupTimeout)
			defer cancel()

			if err := createSQLiteBackup(ctx, cfg.Database.Path, outputPath); err != nil {
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Backup created: %s\n", outputPath)
			return err
		},
	}

	addConfigFlag(cmd, &configPath)
	cmd.Flags().StringVar(&outputPath, "output", "", "Backup artifact path to create")
	_ = cmd.MarkFlagRequired("output")
	return cmd
}

func newBackupRestoreCommand() *cobra.Command {
	var configPath string
	var inputPath string
	var force bool

	cmd := &cobra.Command{
		Use:          "restore",
		Short:        "Restore local MCM state from a SQLite backup artifact",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(inputPath) == "" {
				return fmt.Errorf("--input is required")
			}

			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), backupTimeout)
			defer cancel()

			if err := restoreSQLiteBackup(ctx, inputPath, cfg.Database.Path, force); err != nil {
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Backup restored to: %s\n", cfg.Database.Path)
			return err
		},
	}

	addConfigFlag(cmd, &configPath)
	cmd.Flags().StringVar(&inputPath, "input", "", "Backup artifact path to restore")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite the configured SQLite database if it already exists")
	_ = cmd.MarkFlagRequired("input")
	return cmd
}

func createSQLiteBackup(ctx context.Context, sourcePath string, outputPath string) error {
	if strings.TrimSpace(sourcePath) == "" {
		return fmt.Errorf("database path is required")
	}
	if strings.TrimSpace(outputPath) == "" {
		return fmt.Errorf("backup output path is required")
	}

	info, err := os.Stat(sourcePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("database %q does not exist", sourcePath)
		}
		return fmt.Errorf("stat database %q: %w", sourcePath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("database path %q is a directory", sourcePath)
	}

	if _, err := os.Stat(outputPath); err == nil {
		return fmt.Errorf("backup output %q already exists", outputPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat backup output %q: %w", outputPath, err)
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("create backup output directory: %w", err)
	}

	db, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		return fmt.Errorf("open database %q: %w", sourcePath, err)
	}
	defer db.Close()

	if err := validateMCMDatabase(ctx, db); err != nil {
		return fmt.Errorf("validate source database: %w", err)
	}

	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", outputPath); err != nil {
		_ = os.Remove(outputPath)
		return fmt.Errorf("create SQLite backup: %w", err)
	}

	return nil
}

func restoreSQLiteBackup(ctx context.Context, inputPath string, targetPath string, force bool) error {
	if strings.TrimSpace(inputPath) == "" {
		return fmt.Errorf("backup input path is required")
	}
	if strings.TrimSpace(targetPath) == "" {
		return fmt.Errorf("database path is required")
	}

	inputInfo, err := os.Stat(inputPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("backup input %q does not exist", inputPath)
		}
		return fmt.Errorf("stat backup input %q: %w", inputPath, err)
	}
	if inputInfo.IsDir() {
		return fmt.Errorf("backup input %q is a directory", inputPath)
	}

	if err := validateSQLiteBackup(ctx, inputPath); err != nil {
		return err
	}

	if targetInfo, err := os.Stat(targetPath); err == nil {
		if targetInfo.IsDir() {
			return fmt.Errorf("database path %q is a directory", targetPath)
		}
		if !force {
			return fmt.Errorf("database %q already exists; re-run with --force to overwrite it", targetPath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat database %q: %w", targetPath, err)
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(targetPath), ".mcm-restore-*.db")
	if err != nil {
		return fmt.Errorf("create temporary restore file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	in, err := os.Open(inputPath)
	if err != nil {
		_ = tmp.Close()
		return fmt.Errorf("open backup input %q: %w", inputPath, err)
	}
	_, copyErr := io.Copy(tmp, in)
	closeInErr := in.Close()
	closeTmpErr := tmp.Close()
	if copyErr != nil {
		return fmt.Errorf("copy backup to temporary restore file: %w", copyErr)
	}
	if closeInErr != nil {
		return fmt.Errorf("close backup input: %w", closeInErr)
	}
	if closeTmpErr != nil {
		return fmt.Errorf("close temporary restore file: %w", closeTmpErr)
	}

	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("set restored database permissions: %w", err)
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return fmt.Errorf("replace database %q: %w", targetPath, err)
	}

	return nil
}

func validateSQLiteBackup(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open backup input %q: %w", path, err)
	}
	defer db.Close()

	if err := validateMCMDatabase(ctx, db); err != nil {
		return fmt.Errorf("invalid backup %q: %w", path, err)
	}
	return nil
}

func validateMCMDatabase(ctx context.Context, db *sql.DB) error {
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return fmt.Errorf("run SQLite integrity check: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("SQLite integrity check failed: %s", integrity)
	}

	var tableName string
	if err := db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'").Scan(&tableName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("missing MCM schema_migrations table")
		}
		return fmt.Errorf("check MCM schema: %w", err)
	}

	var migrationCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&migrationCount); err != nil {
		return fmt.Errorf("count MCM schema migrations: %w", err)
	}
	if migrationCount == 0 {
		return fmt.Errorf("missing MCM schema migrations")
	}

	return nil
}
