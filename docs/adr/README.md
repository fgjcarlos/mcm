# Architecture Decision Records

This directory contains Architecture Decision Records (ADRs) for Mosquitto Control Manager.

ADRs document important technical and product architecture choices so future contributors can understand not only *what* was chosen, but also *why*.

## Index

- [ADR-0001: Record architecture decisions](./0001-record-architecture-decisions.md)
- [ADR-0002: Use Go with a lightweight HTTP stack for the backend](./0002-go-lightweight-http-backend.md)
- [ADR-0003: Use SQLite as the default MVP persistence layer](./0003-sqlite-default-persistence.md)
- [ADR-0004: Use React, Vite, and TypeScript for the web UI](./0004-react-vite-typescript-frontend.md)
- [ADR-0005: Embed built frontend assets into the Go binary](./0005-embed-frontend-assets.md)
- [ADR-0006: Keep Mosquitto external to MCM for the MVP](./0006-external-mosquitto-broker.md)
- [ADR-0007: Use a YAML configuration file for server runtime configuration](./0007-yaml-configuration-with-env-overrides.md)

## ADR format

Each ADR should include:

- Status
- Context
- Decision
- Consequences
- Alternatives considered

## Status values

- `Proposed`: suggested but not yet validated by implementation
- `Accepted`: current project direction
- `Superseded`: replaced by a newer ADR
- `Rejected`: explicitly not chosen
