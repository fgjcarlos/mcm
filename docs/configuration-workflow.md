# Broker configuration workflow and recent MFA

MCM manages the configured `mosquitto.conf` through immutable previews. Importing, editing, recovering a revision, or importing a configuration copy prepares a candidate; none of those operations changes broker files. Only an explicit apply can do that.

## Quick path

1. Open **Configuration** and import the existing root configuration. Review the on-disk body and catalog warnings before editing.
2. Edit the draft and validate it. Review the exact revision, diff, affected file, and reload/restart impact. Reload disk state to check for external edits; a changed base requires a new preview.
3. As an administrator, enable MFA under **Administration → Account**. Confirm a fresh TOTP code before apply or recovery. An enrollment/login code already used in the same 30-second window cannot be reused.
4. Apply the reviewed revision. Read its result in **Changes**; saved files and a successful reload signal are not, by themselves, proof of MQTT activation. If the apply is refused because the broker cannot be restarted or because the activation could not be verified, nothing is written: fix the cause and apply the same revision again.
5. To recover, select an earlier revision and prepare a new preview. Review it against today's configuration, then explicitly apply it.

The MQTT user alta has its own journey in the *Users* panel: it chains
the user, its access rule, the preview and the apply, and it keeps the
same gates (recent MFA, activation verification with rollback). See
[the onboarding guide](mqtt-onboarding.md).

## The workflow

1. **Import.** `POST /api/v1/broker/config/import` with an empty body previews the root file as it is on disk. Include files stay separate: the root file is never rewritten with their bodies appended, and unknown directives are preserved.
2. **Edit and validate.** `POST /api/v1/broker/config/preview` takes the edited `body` and the `base_hash` it was derived from. A stale base hash is refused with 409 and no revision is created; an invalid value is refused with 422 and the `validation_issues` that explain it. The response carries an immutable `revision_id`.
3. **Adopt.** `POST /api/v1/broker/config/adopt` records the explicit adoption that the apply requires. It is an administrative decision, not a preview.
4. **Apply.** `POST /api/v1/broker/config/apply` applies that exact `revision_id` with `adopt: true`. Every attempt consumes the revision, including an attempt that rolls back.
5. **Recover.** `GET /api/v1/broker/config/revisions` lists the latest 100 revisions. `POST /api/v1/broker/config/revisions/{id}/recover` never reuses a consumed revision: it revalidates the selected bytes against today's files and returns a **new** preview against the current base, which then goes through review and an explicit apply like any other candidate.

## Authorization and recent MFA

| Operation | Minimum role | Recent MFA |
| --- | --- | --- |
| Inspect configuration, catalog and revision metadata | Auditor | No |
| Import current configuration or validate an edited draft | Operator | No |
| Adopt and apply configuration | Admin | Apply requires it |
| Prepare recovery, import or export a configuration copy | Admin | Yes |
| Apply MQTT users/ACL changes | Admin | Yes |

Sensitive operations ask the operator to confirm a second factor first. `POST /api/v1/auth/mfa/step-up` exchanges an unused TOTP code for a short-lived proof.

```bash
curl -sS -X POST http://localhost:8080/api/v1/auth/mfa/step-up \
  -H "Authorization: Bearer ${TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{"code":"123456"}'
```

```json
{ "proof": "<jwt>", "expires_at": "2026-11-03T09:05:00Z" }
```

The request needs an authenticated session whose account has MFA enabled. A wrong code, a code already used in the same 30-second window, or an account without an enrollment is refused; failed attempts are audited and share the configured login lockout budget with the login flow.

The proof is a JWT with a dedicated `action_mfa` purpose. It is **not** an access token:

- It authenticates nothing. Presenting it as a bearer token is rejected.
- It is bound to the exact `Authorization` header value of the session that requested it. The same account signed in on a second device cannot reuse the proof from the first.
- It is bound to the current MFA enrollment. Rotating or removing the factor invalidates proofs that were already issued.
- It expires five minutes after it is issued.
- It never carries a role. A sensitive operation still checks the caller's role, so a proof cannot elevate an operator's privileges.

Send the proof in the `X-MCM-MFA` header alongside the original bearer token. Keep it in browser memory only — do not store it in `localStorage` and do not log it.

In the UI, open **Administration → Account**. With MFA enabled, the *Recent MFA confirmation* form asks for an authenticator code and reports that the session is confirmed for up to five minutes. A code already used to confirm the enrollment or to sign in cannot be reused within the same 30-second window; wait for the next one.

## Configuration copies are not full backups

`GET /api/v1/broker/config/copy` exports a JSON attachment with format `mcm-broker-config/v1`, the root `body`, and its SHA-256 `hash`. `POST` to the same path validates the format/hash and returns a fresh preview. Treat exported configuration as sensitive operator material.

These copies **do not contain or restore** include files, MQTT passwords, ACL files, certificates/private keys, SQLite, retained messages, or broker persistence. They reference the include files and dependencies already present on this instance. They are not a replacement for a complete deployment backup; use the backup and restore path in [production section 6](./production.md#6-backup-and-restore) for that. Never delete an existing broker or its volumes to test this workflow.

Revision history returns the latest 100 entries. An apply-attempt timestamp is not a successful activation: the deployment linked to the exact revision supplies the outcome. Recovering historical content always revalidates it and uses a new revision against the current base.

## Isolated real-broker/browser check

Run from the repository root:

```bash
npm --prefix frontend ci
PLAYWRIGHT_BROWSERS_PATH="$PWD/frontend/node_modules/.cache/ms-playwright" frontend/node_modules/.bin/playwright install chromium
scripts/e2e-configuration-ui.sh
```

The runner builds the embedded UI and starts one uniquely named disposable Mosquitto container on a random loopback port, with its own temporary storage. A real Chromium browser creates an MQTT test identity and ACL, enrolls/confirms MFA, applies access rules, imports/edits/validates/reviews/applies configuration, exports/imports a root copy and recovers the initial revision. Backend probes verify allowed and denied MQTT behavior. No broker files are hand-edited after startup. Cleanup removes only the container this fixture created; it never runs `compose down -v` or touches existing volumes.

The normal Go suite skips this opt-in test. `MCM_E2E_UI=1` makes missing Docker, browser or other prerequisites a failure, not a skipped success. First-time browser installation requires network access and platform browser libraries. The check may wait for fresh 30-second TOTP windows to avoid replaying enrollment/confirmation codes. `scripts/e2e-configuration-ui.sh --help` describes the runner.

CI runs the same script in the *Configuration journey in a real browser* job, and `scripts/e2e-broker-config.sh` covers the API surface of recovery, copies and the MFA refusals against the same live broker.

## State and compatibility limits

- The displayed root body/hash describe disk state, not an independently observed runtime configuration.
- The loaded directive catalog is a validation aid, not proof of the broker's detected version or complete Mosquitto syntax validation. An unobserved broker version is explicitly shown as unknown.
- Known inline password/secret/token directives are rejected rather than exposing their values in the editor or diffs; archived bodies containing them are withheld. Externalize inline credentials before graphical import. Configuration copies still require sensitive handling.
- Unknown syntax is preserved; catalog warnings and unsupported activation capabilities must remain visible. Review unknown directives carefully.
- Includes remain separate; the root file is never rewritten with their bodies appended. Included `.conf` files are hashed again before apply; escaping symlinks are rejected.
- A restart-required change is refused unless the configured adapter actually supports restart; `force` cannot turn SIGHUP into restart.
- Configuration apply requires a usable managed MQTT test user, a known in-memory password and ACL grant, and a configured verifier. After a server restart, rotate/create the test credential through MCM before applying. Preflight fails before writing if verification is unavailable.
- Successful activation verifies allowed/denied MQTT access after reload. It does not prove every directive's runtime value. On failure, MCM restores the previous root bytes and file permissions, signals activation again, and verifies recovery; `rollback_failed` requires operator intervention.
- See [product scope](product-scope.md) and [OpenAPI](openapi.yaml) for boundaries and HTTP contracts.
