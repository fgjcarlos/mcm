package server

import (
	"path/filepath"
	"strings"
	"testing"

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
