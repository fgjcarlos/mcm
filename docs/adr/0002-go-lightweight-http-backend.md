# ADR-0002: Use Go with a lightweight HTTP stack for the backend

## Status

Accepted (the Go + stdlib `net/http` decision is still in force).
The "CLI commands" bullet and the Cobra recommendation were dropped by
the Docker-first pivot (#226, 2026-Q2); the binary now exposes only
`--config` and `--version` (see `cmd/mcm/main.go`). Everything below
that references a CLI surface is historical context.

## Context

MCM needs a backend that can provide:

- REST API endpoints
- WebSocket events
- CLI commands
- Mosquitto connectivity checks
- Local persistence
- Embedded frontend assets
- Small edge-friendly deployments

The backend should be easy to cross-compile for Linux, ARM devices, and small industrial edge machines. It should also support a single-binary deployment path.

## Decision

Use Go for the backend.

For the HTTP layer, prefer the Go standard library plus small, focused libraries where they provide clear value. The initial direction is:

- `net/http` as the base HTTP server
- A lightweight router/middleware package if needed, such as `go-chi/chi`
- Gorilla/WebSocket or a similarly maintained WebSocket package for realtime events
- Cobra for CLI commands

Avoid large backend frameworks unless the project has a clear need for them.

## Consequences

Positive:

- Small static-ish binaries are practical.
- Cross-compilation is straightforward.
- Runtime memory usage should stay reasonable for edge deployments.
- Go's standard tooling gives simple testing, formatting, and build workflows.
- A single binary can host the API, CLI, and embedded web UI.

Negative:

- Some features provided by larger frameworks must be implemented or composed manually.
- Frontend developers may need to learn Go conventions to contribute backend work.
- Go dependency choices must be conservative to avoid unnecessary complexity.

## Alternatives considered

### Node.js / TypeScript backend

Rejected for the MVP. It would align well with frontend skills and Node-RED users, but it makes single-binary edge deployment and low-footprint distribution less attractive.

### Python backend

Rejected for the MVP. Python is productive, but packaging reliable edge deployments is more complicated than Go for this project.

### Gin, Echo, or Fiber as the default framework

Deferred. They are viable, but the MVP should start with the smallest useful HTTP stack. If routing, middleware, validation, or performance needs justify a framework later, this ADR can be superseded.
