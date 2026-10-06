package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestSchemaMigrationHistories(t *testing.T) {
	t.Run("fresh database creates both schemas", func(t *testing.T) {
		store := openSchemaTestStore(t, filepath.Join(t.TempDir(), "fresh.db"))
		assertTableExists(t, store.db, "broker_config_revisions")
		assertTableExists(t, store.db, "broker_config_adoptions")
		assertTableExists(t, store.db, "listener_specs")
	})

	t.Run("main version 17 database gains listener schema", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "main17.db")
		createSchemaThroughVersion(t, path, 17)
		store := openSchemaTestStore(t, path)
		assertTableExists(t, store.db, "broker_config_revisions")
		assertTableExists(t, store.db, "listener_specs")
		assertMigrationRecorded(t, store.db, 17, "add_deployments_kind_reload_kind_conf")
		assertMigrationRecorded(t, store.db, 18, "ensure_broker_config_and_listener_schemas")
	})

	t.Run("legacy listener version 16 preserves rows and gains broker schema", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "legacy-listener16.db")
		createLegacyListener16Database(t, path)

		store := openSchemaTestStore(t, path)
		assertTableExists(t, store.db, "broker_config_revisions")
		assertTableExists(t, store.db, "broker_config_adoptions")
		assertTableExists(t, store.db, "listener_specs")

		var port int
		var protocols string
		if err := store.db.QueryRowContext(context.Background(), `SELECT port, protocols FROM listener_specs WHERE id = 'legacy-listener'`).Scan(&port, &protocols); err != nil {
			t.Fatalf("read preserved listener row: %v", err)
		}
		if port != 1884 || protocols != `["mqtt"]` {
			t.Fatalf("listener row changed: port=%d protocols=%q", port, protocols)
		}
		assertMigrationRecorded(t, store.db, 16, "create_listener_specs")
		assertMigrationRecorded(t, store.db, 17, "add_deployments_kind_reload_kind_conf")
		assertMigrationRecorded(t, store.db, 18, "ensure_broker_config_and_listener_schemas")
	})
}

func openSchemaTestStore(t *testing.T, path string) *Store {
	t.Helper()
	store, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func createSchemaThroughVersion(t *testing.T, path string, version int) {
	t.Helper()
	db, err := sql.Open("sqlite", SQLiteDSN(path))
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("create migration history: %v", err)
	}
	for _, migration := range migrations {
		if migration.version > version {
			continue
		}
		if _, err := db.Exec(migration.sql); err != nil {
			t.Fatalf("apply fixture migration %d (%s): %v", migration.version, migration.name, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, name, applied_at) VALUES(?, ?, ?)`, migration.version, migration.name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("record fixture migration %d: %v", migration.version, err)
		}
	}
}

func createLegacyListener16Database(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", SQLiteDSN(path))
	if err != nil {
		t.Fatalf("open legacy fixture database: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("create legacy migration history: %v", err)
	}
	for _, migration := range migrations {
		if migration.version > 15 {
			continue
		}
		if _, err := db.Exec(migration.sql); err != nil {
			t.Fatalf("apply legacy fixture migration %d (%s): %v", migration.version, migration.name, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, name, applied_at) VALUES(?, ?, ?)`, migration.version, migration.name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("record legacy fixture migration %d: %v", migration.version, err)
		}
	}
	legacyListenerMigration := `
CREATE TABLE listener_specs (
	id TEXT PRIMARY KEY,
	port INTEGER NOT NULL,
	bind TEXT NOT NULL DEFAULT '0.0.0.0',
	protocols TEXT NOT NULL DEFAULT '[]',
	options TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX idx_listener_specs_port_bind ON listener_specs(port, bind);
CREATE INDEX idx_listener_specs_updated_at ON listener_specs(updated_at);
`
	if _, err := db.Exec(legacyListenerMigration); err != nil {
		t.Fatalf("apply legacy listener migration 16: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations(version, name, applied_at) VALUES(16, 'create_listener_specs', ?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("record legacy listener migration 16: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO listener_specs(id, port, bind, protocols, options, created_at, updated_at) VALUES('legacy-listener', 1884, '0.0.0.0', '["mqtt"]', '{}', '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert legacy listener row: %v", err)
	}
}

func assertTableExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	var found string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&found); err != nil {
		t.Fatalf("table %q is missing: %v", name, err)
	}
}

func assertMigrationRecorded(t *testing.T, db *sql.DB, version int, wantName string) {
	t.Helper()
	var name string
	if err := db.QueryRow(`SELECT name FROM schema_migrations WHERE version = ?`, version).Scan(&name); err != nil {
		t.Fatalf("migration %d was not recorded: %v", version, err)
	}
	if name != wantName {
		t.Fatalf("migration %d name = %q, want %q", version, name, wantName)
	}
}
