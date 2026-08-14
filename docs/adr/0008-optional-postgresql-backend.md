# ADR-0008: Optional PostgreSQL storage backend

## Status

Superseded by the Docker-first pivot (#226, 2026-Q2) and the post-pivot
`ROADMAP.md` ("Futuro" section). PostgreSQL is not a current goal and
the `postgres` storage backend code introduced by this ADR is dormant.
The current MVP is SQLite-only, with HA / multi-instance write
coordination listed in the post-MVP "Futuro" bucket — and that bucket is
the only path that would re-open the Postgres question. New
contributors should treat the rest of this document as historical
context.

## Context

ADR-0003 chose SQLite for the MVP. MCM now has 36+ storage methods on a single concrete `*storage.Store` struct. Larger deployments and multi-instance setups need PostgreSQL, but the current code couples directly to `*storage.Store` throughout the server layer.

## Decision

Introduce the configuration shape and validation for a `postgres` database backend now, and document the interface extraction strategy as follow-up work. The full PostgreSQL implementation is deferred until the interface boundaries are in place.

### Config shape

```yaml
database:
  backend: "sqlite"          # "sqlite" (default) or "postgres"
  path: "/var/lib/mcm/mcm.db"  # required for sqlite
  dsn: ""                     # required for postgres
```

- `backend` defaults to `"sqlite"` when empty (backward compatible).
- Validation rejects unknown backends and enforces `path` for sqlite, `dsn` for postgres.

### Interface extraction strategy

The server layer currently depends on `*storage.Store` directly (36 methods). To support multiple backends, the following refactor is needed:

#### Phase 1 — Domain-scoped interfaces (recommended next step)

Split the monolithic store into domain-scoped interfaces consumed by each handler group:

| Interface | Methods | Consumer |
|-----------|---------|----------|
| `AdminUserStore` | CountAdminUsers, CreateAdminUser, GetAdminUserByID, GetAdminUserByUsername, ListAdminUsers, UpdateAdminUser, DeleteAdminUser, SetAdminUserMFA, ReplaceRecoveryCodes, DeleteRecoveryCodes, UnusedRecoveryCodeHashes, ConsumeRecoveryCode | Auth handlers |
| `MQTTUserStore` | CreateMQTTUser, GetMQTTUser, ListMQTTUsers, UpdateMQTTUser, DeleteMQTTUser | MQTT user handlers, deploy service |
| `AuditStore` | RecordAuditEvent, ListAuditEvents, RecordSecurityEvent, ListSecurityEvents, RecordLoginAttempt, CountFailedLoginAttemptsByIP, CountFailedLoginAttemptsByUsername, PruneLoginAttempts | Audit handlers, login handler |
| `JSONSchemaStore` | CreateJSONSchema, ListJSONSchemas, UpdateJSONSchema, DeleteJSONSchema | Schema handlers |
| `BrokerMetricStore` | RecordBrokerMetricEvent, ListBrokerMetricEvents, PruneBrokerMetrics | Broker event hub |
| `DeploymentStore` | InsertDeployment, GetDeployment, UpdateDeploymentStatus, ListDeployments | Deploy service |

The existing `acl.Store` interface is already extracted (used by `aclAPI`). Follow the same pattern.

#### Phase 2 — PostgreSQL implementation

Once interfaces are in place, add a `storage/postgres` package that satisfies all interfaces using `pgx` or `database/sql` with `lib/pq`. Migrations would use the same numbered scheme but with PostgreSQL-compatible SQL.

#### Phase 3 — Factory function

Add a `storage.Open(cfg DatabaseConfig)` factory that returns the correct backend based on `cfg.Backend`. The server layer calls `storage.Open()` once at startup.

### Migration compatibility

- SQLite migrations use `INTEGER PRIMARY KEY AUTOINCREMENT`; PostgreSQL uses `SERIAL` or `BIGSERIAL`.
- Timestamps stored as TEXT (RFC3339Nano) in SQLite; PostgreSQL should use `TIMESTAMPTZ`.
- Boolean columns stored as INTEGER in SQLite; PostgreSQL uses native `BOOLEAN`.
- Migration files should be backend-specific: `migrations/sqlite/` and `migrations/postgres/`.

### Operational trade-offs

| Aspect | SQLite | PostgreSQL |
|--------|--------|------------|
| Setup complexity | None (embedded) | External service required |
| Backup | File copy | `pg_dump` |
| Concurrency | Single writer | Full MVCC |
| Multi-instance | Not supported | Supported |
| Edge deployment | Ideal | Requires network database |
| Storage size | Limited by filesystem | Practically unlimited |

## Consequences

Positive:

- Config shape is ready for PostgreSQL without breaking existing SQLite users.
- Interface extraction strategy is documented and scoped.
- Each phase can be delivered independently.

Negative:

- PostgreSQL is not yet usable — this is architecture preparation only.
- The 36-method interface split is non-trivial refactor work.

## Follow-up issues needed

1. Extract domain-scoped storage interfaces (Phase 1 above).
2. Implement PostgreSQL backend with `pgx` (Phase 2).
3. Add `storage.Open()` factory function (Phase 3).
4. Add integration tests with test PostgreSQL (Docker).
