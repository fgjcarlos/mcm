# ADR-0004: Use React, Vite, and TypeScript for the web UI

## Status

Accepted

## Context

MCM needs a modern web UI for broker status, users, ACLs, clients, topic exploration, diagnostics, and settings.

The frontend should be approachable for contributors, fast to develop, and easy to build into static assets that can be served by the Go backend.

## Decision

Use React, Vite, and TypeScript for the web UI.

The initial frontend stack should be:

- React
- TypeScript
- Vite
- Tailwind CSS
- shadcn/ui or similarly accessible component primitives
- WebSocket client for realtime broker and topic events

The UI should be designed for desktop-first administration while remaining usable on tablets and smaller screens where practical.

## Consequences

Positive:

- React has a large contributor base and ecosystem.
- Vite gives a fast local developer experience.
- TypeScript improves maintainability for API and realtime event contracts.
- Static build output can be embedded into the Go binary.
- Tailwind and component primitives enable a polished UI without building every component from scratch.

Negative:

- Adds Node.js tooling to the development workflow.
- Frontend dependency management needs care to avoid unnecessary bloat.
- API/event contracts need explicit typing to avoid frontend/backend drift.

## Alternatives considered

### Server-rendered HTML only

Rejected for the MVP target UX. MCM needs interactive views for ACL editing, live events, dashboards, and topic exploration.

### Vue or Svelte

Viable but not chosen. React has the broadest contributor familiarity for this project and aligns with the current preferred stack.

### Next.js

Rejected for now. MCM does not need SSR or a separate Node.js runtime. Static assets served by the Go backend are simpler for edge deployment.
