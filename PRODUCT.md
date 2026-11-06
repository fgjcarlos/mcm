# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Anyone who wants to try MCM. The owner confirmed the audience is intentionally broad ("cualquier usuario que quiera entrar"): the landing must work for a first-time general visitor, not a specialized persona. In practice three overlapping groups arrive: operators evaluating whether to run MCM, developers reading the code and roadmap before contributing, and people arriving from the Mosquitto ecosystem.

## Product Purpose

MCM (Mosquitto Control Manager) is an open source graphical configuration manager for Eclipse Mosquitto. Goal, verbatim from the README: "import, edit, validate, review, apply, and recover the configuration of one Mosquitto instance from a web interface, for explicitly supported broker versions."

Success for the public site is balanced (owner-confirmed): the visitor understands exactly what is ready and what is not, and has a fast, reproducible path to a first successful local run. Neither goal may cannibalize the other.

## Positioning

- One managed external Mosquitto broker administered from a browser: Mosquitto remains the MQTT broker; MCM is the administration UI and API with SQLite management state.
- Verified changes as the unit of trust: immutable revisions, activation verification probes, and revision recovery after failures. An unverified activation is never presented as verified.
- Radical status honesty: capabilities are presented as available vs. planned, and diagnostics (Sparkplug B, JSON Schema) are explicitly inspection-only, never enforcement (issue #307).

## Operating Context

Self-hosted deployments: a Go application image with its embedded React UI plus a separate Mosquitto broker container; Docker Compose and Taskfile for local development; REST and authenticated WebSocket APIs. The site ships on GitHub Pages (fgjcarlos.github.io/mcm); the repository lives on GitHub with CI suites that exercise a live Mosquitto.

## Capabilities and Constraints

Available today: MQTT users and basic ACLs with review and apply; MQTT listeners and broker TLS material; general settings forms; bridges; plugins including Dynamic Security; an advanced root `mosquitto.conf` editor; operator accounts, roles, MFA and audit; observed traffic; configuration recovery.

Constraints that must stay visible: SQLite is the only implemented persistence backend (a `postgres` value is accepted in config for forward-compatibility but aborts at boot — ADR-0008, issue #307); apply requires recent MFA; observed traffic is not a complete client inventory; Sparkplug B and JSON Schema diagnostics never filter or reject broker traffic; backup/restore has known gaps (issue #295); the project is alpha and the graphical configuration MVP is not complete (epic #308).

## Brand Commitments

The name "Mosquitto Control Manager (MCM)" is binding. The factual, honest, technical tone of the project is binding (owner-confirmed). License: Apache 2.0. Site content in English.

## Evidence on Hand

Real, citable evidence: the repository with CI badges (`README.md`), live-Mosquitto e2e suites in `.github/workflows/ci.yml`, acceptance tables in `docs/product-scope.md`, the roadmap in `ROADMAP.md`, and reproducible quickstart commands (`task build`, `task up`, `task logs`). Absences that future work must not fabricate: no testimonials, no customer logos, no usage or benchmark numbers, no screenshots of features that do not render.

## Product Principles

1. Honesty outranks selling: state what works, what is planned, and what is out of scope.
2. The unit of trust is the verified change: review → apply → verified activation → recovery.
3. Mosquitto stays the broker; MCM administers it — no reimplementation of MQTT behavior.
4. A general visitor must orient within one viewport; the alpha state is never hidden behind marketing.
5. The path from the landing to a running local stack is short and reproducible.
