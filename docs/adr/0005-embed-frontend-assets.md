# ADR-0005: Embed built frontend assets into the Go binary

## Status

Accepted

## Context

MCM should be easy to install on edge devices and small servers. A deployment that requires copying a backend binary plus a separate frontend directory is more error-prone, especially for industrial or remote-site installations.

Go supports embedding static files at build time with `embed`, which allows the backend to serve the compiled web UI directly.

## Decision

For release builds, embed the built frontend assets into the Go binary.

The expected build flow is:

1. Build the frontend into `frontend/dist`.
2. Embed `frontend/dist` into the Go binary using `//go:embed`.
3. Serve the embedded assets from the backend HTTP server.

Development can still use Vite's dev server with API proxying if that improves frontend developer experience.

## Consequences

Positive:

- Releases can be distributed as a single binary.
- Edge installs are simpler and less fragile.
- Frontend/backend version mismatch is reduced.
- Docker images can stay small.

Negative:

- Build pipeline must coordinate frontend and backend builds.
- Local development needs a clear distinction between dev-server mode and embedded-assets mode.
- Binary size will increase with frontend assets.

## Alternatives considered

### Ship frontend files separately

Rejected for default releases. This is flexible, but it increases deployment complexity and makes version mismatches easier.

### Serve the UI from a separate Node.js service

Rejected. A separate Node.js service conflicts with the goal of a lightweight edge-friendly deployment.

### Use only the Vite dev server

Rejected for production. Vite is excellent for development but is not the intended production serving model for MCM.
