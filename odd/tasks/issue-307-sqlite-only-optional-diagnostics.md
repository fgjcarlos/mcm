# Issue #307 — Retire unimplemented options, keep extensions out of the core

Source: GitHub issue fgjcarlos/mcm#307 ("[P2] Retirar opciones no implementadas y mantener extensiones fuera del núcleo", P2, OPEN). Goal: SQLite as the only operational backend with coherent docs, Sparkplug/JSON Schema framed as optional diagnostics that never filter broker traffic, and deferred capabilities documented as deferred, verified with integration tests.

## Specs

- S1: "SQLite como único backend operativo; error temprano para PostgreSQL y documentación coherente."
- S2: "Sparkplug y JSON Schema agrupados como diagnósticos opcionales."
- S3: "Documentar que la validación de mensajes observados no filtra publicaciones en el broker."
- S4: "Posponer flotas, multi-broker, SSO, multi-tenancy y HA hasta completar una instancia."
- S5: "La auditoría fue principalmente estática; verificar los cambios de comportamiento con pruebas de integración. No declarar una capacidad completa por disponer únicamente de un endpoint."
- Context quote: "PostgreSQL se acepta en configuración pero falla al arrancar; Sparkplug/JSON Schema no deben bloquear el gestor."

Decisions already settled by the repository (do not reopen):
- Config validation KEEPS accepting the `postgres` shape (ADR-0008 forward-compat); the loud boot-time abort in `internal/server/run.go` `openConfiguredStorage` IS the "error temprano". Coherence work is documentation: `internal/config/config.go` (DatabaseConfig doc comment + embedded example config) and `internal/config/env_table.md` must say postgres is accepted-but-unimplemented and aborts startup.
- `docs/product-scope.md` ("Optional scope" section) and `ROADMAP.md` already state S2/S4; do not rewrite them, only fix drift.

## Tasks

- T1 — S1: Make persistence documentation coherent: annotate postgres as accepted-but-unimplemented (aborts startup) in `internal/config/config.go` doc comment + embedded YAML example and `internal/config/env_table.md` rows (`MCM_DATABASE_BACKEND`, `MCM_DATABASE_DSN`). Keep validation behavior unchanged. Route: gpt-luna-6 (unit A). Commit: `feat(config): document sqlite as the only implemented storage backend (issue #307)`.
- T2 — S5/S1: Add an integration test through the public boot path (`Run` in `internal/server/run.go`, next to `run_test.go`) proving a `database.backend: postgres` config aborts early: error mentions postgres and points to "sqlite", no SQLite database file is created, and no HTTP listener is bound. Route: gpt-luna-6 (unit A). Commit: same work unit as T1.
- T3 — S3: Add an explicit statement to `docs/json-schemas.md` and `docs/sparkplug.md`: validation/decoding inspects observed messages only and never filters, blocks, or rejects publications on the broker; Mosquitto publishes regardless of validation outcome. Route: gpt-luna-6 (unit B). Status: done — "Diagnostic only" callout added at the top of both files.
- T4 — S2/S4: Verify deferral coherence after T1–T3: `docs/product-scope.md`, `ROADMAP.md`, and `site/` must not advertise fleets, multi-broker, SSO, multi-tenancy, HA, or PostgreSQL as available capabilities; fix only drift found, no rewrites. Route: gpt-luna-6 (unit B). Status: done — audit found no drift: site/index.astro, product-scope.md, ROADMAP.md and README.md (line 30 verified by parent) already frame diagnostics as non-enforcing and defer the listed capabilities.
- T5 — S1–S5: Close: run `go test ./...` (and frontend tests if any frontend file changed — none expected), verify the checklist of the issue, record commits in this document, append Log evidence. Route: parent. Commit: `docs(odd): record the closing evidence of issue #307`.

## Log

- L1: "Crea un plan para resolver la issue #307 y delega la implementacion al modelos chatgp-luna-6" (the project agent is `gpt-luna-6`, pinned to gpt-6-luna).
- L2: Exploration evidence: at audit commit `2d53ea5` run.go already aborted on postgres; product-scope "Optional scope" and ROADMAP deferral lines were added after the audit (f376e35, #309). Gaps found: config.go comments/example + env_table.md present postgres as a normal option; json-schemas.md and sparkplug.md never state validation does not filter publications; no integration test through `Run` for the postgres abort; site/ not yet audited for deferred-capability claims.
- L3: Unit B (gpt-luna-6, mw3lkjol-2-pfjh) delivered T3+T4: "Diagnostic only" callouts added to docs/json-schemas.md and docs/sparkplug.md; deferral audit of site/, product-scope.md, ROADMAP.md, README.md returned coherent with no drift. Parent verified the diff (2 lines per file, no restructures) and README.md:30 claim.
