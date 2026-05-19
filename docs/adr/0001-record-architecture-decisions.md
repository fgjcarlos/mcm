# ADR-0001: Record architecture decisions

## Status

Accepted

## Context

MCM is starting from an early concept stage. The project will need decisions about backend architecture, persistence, frontend technology, deployment model, configuration, and Mosquitto integration before the codebase grows.

Without a lightweight decision log, important context can be lost in issues, commits, and conversations. Future contributors may otherwise repeat the same discussions or make changes without understanding why the original architecture was chosen.

## Decision

The project will use Architecture Decision Records (ADRs) stored in `docs/adr/`.

Each ADR will document:

- Status
- Context
- Decision
- Consequences
- Alternatives considered

ADRs should be added for decisions that meaningfully affect product direction, deployment, security, compatibility, operations, or developer experience.

## Consequences

Positive:

- Contributors can understand why major choices were made.
- Tradeoffs remain explicit.
- Future changes can supersede previous decisions instead of silently contradicting them.
- The roadmap and issues can reference stable architecture documents.

Negative:

- Contributors need to keep ADRs updated when major decisions change.
- Small decisions may not justify an ADR, so the team must avoid unnecessary process overhead.

## Alternatives considered

### Keep decisions only in GitHub Issues

Rejected. Issues are useful for work tracking, but they can become noisy, closed, or hard to discover later.

### Keep decisions only in README

Rejected. The README should communicate the project clearly, but detailed tradeoffs would make it too long.

### Use a full documentation site from the beginning

Rejected for now. A documentation site may be useful later, but Markdown files are enough for the MVP foundation.
