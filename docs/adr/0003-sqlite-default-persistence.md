# ADR-0003: Use SQLite as the default MVP persistence layer

## Status

Accepted

## Context

MCM needs persistence for local application state such as admin users, sessions or tokens, managed MQTT users, ACL rules, audit entries, and local settings.

The MVP targets single-node Mosquitto deployments, edge machines, local gateways, and small industrial installations. Requiring PostgreSQL or another external database would make first installation more complex.

## Decision

Use SQLite as the default persistence layer for the MVP.

The database should be stored in a configurable local path. Schema changes should be managed through migrations from the beginning so upgrades remain predictable.

The data model should avoid SQLite-specific assumptions where practical, but PostgreSQL compatibility is not a Phase 1 requirement.

## Consequences

Positive:

- Very simple local installation.
- No external database service required.
- Good fit for edge and single-node deployments.
- Easy backup and restore story for the MVP.
- Works well with a single-binary deployment model.

Negative:

- Not suitable for multi-node write-heavy deployments.
- Care is needed around backups, file permissions, and concurrent writes.
- If larger installations need PostgreSQL later, the persistence layer will need abstraction and migration work.

## Alternatives considered

### PostgreSQL first

Rejected for the MVP. PostgreSQL is a strong option for larger deployments, but it adds operational complexity too early.

### BoltDB / bbolt

Rejected for now. It is simple and embeddable, but SQL is more familiar for relational data like users, ACLs, sessions, and audit logs.

### No database, only Mosquitto files

Rejected. MCM needs its own application state, auditability, and UI/API models that should not be stored only as generated Mosquitto files.
