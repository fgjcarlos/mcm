# Issue #305 — graphical configuration and recovery

Branch stack: `feat/305-01-recent-mfa-proof` → `feat/305-02-config-backend` → `feat/305-03-ui-workspace` → `feat/305-04-e2e-ci`; base: `origin/main` (`7e6d43d`). Delivery: stacked pull requests against `main`, one per slice (user selected a split instead of a single pull request). The original construction branch `feat/305-08-mfa-e2e-scripts` (`04cee50`) is the reference tree; the stack is rebuilt from its content because the original commits interleave across `internal/deploy/service.go`, `internal/server/app.go`, `internal/server/security.go` and `docs/openapi.yaml`, and a commit-boundary cut would produce broken intermediate states.

Verification: Go race suite, Go vet, gofmt, golangci-lint, frontend Vitest/build/lint, Redocly OpenAPI lint, docs lint, isolated live-Mosquitto E2E and the browser journey. Independent verification of the four declared risks is delegated to a verifier agent; a provider-quota failure is recorded as unverified, never substituted.

## Specs

S1. "Administrar Mosquitto desde la interfaz gráfica, con cambios verificables y recuperables." "Falta el recorrido importar → editar → validar → revisar → aplicar → recuperar."
S2. "Navegación: Configuración, Acceso MQTT, Cambios, Diagnóstico y Administración."
S3. "Mostrar versión/capacidades, configuración activa, borrador y deriva externa."
S4. "Diff comprensible, impacto operativo y resultado por revisión."
S5. "Recuperar revisiones y exportar/importar copias desde UI con RBAC/MFA/auditoría."
S6. "Prueba completa desde un broker existente sin editar archivos a mano." "La auditoría fue principalmente estática; verificar los cambios de comportamiento con pruebas de integración. No declarar una capacidad completa por disponer únicamente de un endpoint."
S7. "Exigir MFA configurado y una comprobación reciente antes de ejecutar estas operaciones sensibles." Applies to applying changes, recovering revisions and importing copies.

## Constraints on the cut

- **C1** — MFA enforcement on `POST /broker/config/apply` and `POST /deployments/apply` breaks the pre-rewrite UI and the `e2e-{deploy,prod,recovery,rollback,verify}.sh` scripts, which send no `X-MCM-MFA`. The enforcement therefore ships with the UI that can satisfy it, and with the scripts updated in the same change.
- **C2** — `golangci-lint` enables `unused`. `requireRecentMFA` is unexported, so it may only land in the pull request that first uses it.
- **C3** — The truthfulness fix (`pending_activation` instead of an unverified `active_verified`) changes what `scripts/e2e-broker-config.sh` observes, so the fix and the script update ship together.
- **C4** — The original commits interleave; the stack is rebuilt by content, not by cherry-picking whole commits.
- **C5** — `frontend/dist/index.html` is versioned and embedded (`//go:embed all:dist`). Every pull request that touches the frontend rebuilds it.

## Tasks

- T1 | S7 | PR 1 (`feat/305-01-recent-mfa-proof`) | done | Session-bound recent-MFA proof mechanism: `POST /api/v1/auth/mfa/step-up` issues a five-minute proof bound to the exact bearer session and the live MFA factor; the proof authenticates nothing, expires, survives neither a session change nor a factor rotation, and is kept in browser memory only. Reusable UI confirmation mounted under Administration → Account. No route requires the proof yet, so the existing UI and CI scripts keep working. RED: the endpoint did not exist; GREEN: `internal/auth/action_mfa_test.go` (binding, expiry, tampering, not-a-token) and `internal/server/recent_mfa_test.go` (issue, replay, wrong code, no enrollment, anonymous).
- T2 | S1,S3,S4,S5 | PR 2 (`feat/305-02-config-backend`) | pending | Editable root preview, disk drift and verified state, truthful activation with rollback, audited revision recovery and configuration copies.
- T3 | S1,S2,S3,S4,S5,S7 | PR 3 (`feat/305-03-ui-workspace`) | pending | Grouped navigation and the configuration/changes workspace, plus the enforcement of recent MFA on apply and the CI scripts that must send the proof.
- T4 | S6 | PR 4 (`feat/305-04-e2e-ci`) | pending | Browser journey against a real broker, API-level CI coverage of recovery/copies/MFA, and the Chromium CI job.
- T5 | S1-S7 | verification | pending | Independent verifier over the four declared risks.

## Log

L1. The issue was retrieved from `github.com/fgjcarlos/mcm`; the full acceptance text is quoted in the Specs. The implementation already existed on `feat/305-08-mfa-e2e-scripts` (`04cee50`) and was re-verified green in this session: `go test ./... -count=1` passed in all 24 packages with tests, `npm --prefix frontend run test:run` passed 283 tests across 21 files, and `scripts/lint-docs.sh` reported 0 violations.
L2. The user chose three delivery decisions: split the work into several pull requests, cover the new surfaces in CI at both the API and browser levels, and delegate the independent verification that the provider quota had blocked.
L3. The dependency analysis that fixes the cut points is recorded under "Constraints on the cut".
L4. PR 1 verification: `go build ./...`, `go test ./... -race -count=1`, `go vet ./...`, `gofmt -l .`, golangci-lint, `npm --prefix frontend run build|lint|test:run` (280 tests), `npx @redocly/cli lint docs/openapi.yaml` and `scripts/lint-docs.sh`. A pre-existing defect was found and fixed here: the OpenAPI document on the reference tree referenced `#/components/responses/BadRequest`, which was never defined, so the Redocly lint failed with `no-unresolved-refs`. The `BadRequest` response component is now defined.
