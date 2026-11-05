package server

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/config"
)

func TestOpenConfiguredStorageRejectsPostgres(t *testing.T) {
	// Postgres is accepted by config validation (forward-compat per ADR-0008)
	// but is not yet implemented. Boot must fail loudly instead of silently
	// falling back to SQLite.
	_, err := openConfiguredStorage(config.DatabaseConfig{
		Backend: "postgres",
		DSN:     "postgres://user:pass@localhost:5432/mcm",
	})
	if err == nil {
		t.Fatal("expected error for postgres backend, got nil")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "postgres") || !strings.Contains(msg, "not") {
		t.Fatalf("error should clearly state postgres is unsupported; got %q", err.Error())
	}
}

func TestRunRejectsPostgresBeforeOpeningSQLiteOrHTTP(t *testing.T) {
	dir := t.TempDir()
	cfg := newRunTestConfig(t, dir)
	cfg.Database.Backend = "postgres"
	cfg.Database.DSN = "postgres://user:pass@localhost:5432/mcm"

	started := time.Now()
	err := Run(context.Background(), cfg)
	if err == nil {
		t.Fatal("Run returned nil; want postgres startup error")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Run took %s to reject postgres; want fail-fast error", elapsed)
	}
	if msg := strings.ToLower(err.Error()); !strings.Contains(msg, "postgres") || !strings.Contains(msg, "sqlite") {
		t.Fatalf("Run error = %q, want it to mention postgres and sqlite", err.Error())
	}

	if _, statErr := os.Stat(cfg.Database.Path); !os.IsNotExist(statErr) {
		t.Fatalf("SQLite database path %q was touched: stat error = %v", cfg.Database.Path, statErr)
	}

	addr := net.JoinHostPort(cfg.HTTP.BindAddress, fmt.Sprint(cfg.HTTP.Port))
	listener, listenErr := net.Listen("tcp", addr)
	if listenErr != nil {
		t.Fatalf("HTTP address %q remained bound after Run returned: %v", addr, listenErr)
	}
	if closeErr := listener.Close(); closeErr != nil {
		t.Fatalf("close probe listener: %v", closeErr)
	}
}

func TestOpenConfiguredStorageOpensSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcm.db")
	for _, backend := range []string{"", "sqlite"} {
		store, err := openConfiguredStorage(config.DatabaseConfig{
			Backend: backend,
			Path:    path,
		})
		if err != nil {
			t.Fatalf("backend %q: unexpected error: %v", backend, err)
		}
		if store == nil {
			t.Fatalf("backend %q: expected a store, got nil", backend)
		}
		store.Close()
	}
}
