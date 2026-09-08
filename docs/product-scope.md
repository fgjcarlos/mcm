# Graphical Mosquitto configuration: scope and acceptance

## Objective and boundary

MCM should let an operator administer the configuration of one external Mosquitto instance from a browser. After deployment access is configured, routine configuration changes should not require hand-editing files. REST remains the UI's automation interface. SQLite stores management state; Mosquitto remains responsible for MQTT behavior.

This is the agreed target, not a description of completed functionality. Implementation is tracked in [epic #308](https://github.com/fgjcarlos/mcm/issues/308) and the [roadmap](../ROADMAP.md).

## Current coverage

| Area | Current implementation | Target |
| --- | --- | --- |
| Users | Create, remove, disable and rotate generated passwords | Coherent identity lifecycle with ACL references and pending/active state |
| ACL | User/topic rules with read, write and readwrite | Native patterns, deny and listener scope supported by each version |
| Apply | Password/ACL file diff, write, SIGHUP and limited recovery | Immutable revision, validation, reload/restart selection, verification and recovery |
| Configuration | Static templates; MCM settings exposed read-only | Import and graphical editing of mosquitto.conf and includes |
| Network | Static listeners | MQTT/WebSocket listeners, binding and deployment port compatibility |
| TLS | MCM HTTP TLS and outbound MQTT TLS | Broker certificate lifecycle and listener TLS/mTLS |
| General settings | Static broker configuration | Persistence, queues, limits, sessions and logging |
| Bridges and plugins | No management UI | Bridge editor and supported plugins, including Dynamic Security |
| Diagnostics | Wildcard-observed traffic and MCM events | Explicit broker metrics/log sources with stated limitations |
| Recovery | Legacy volume recipes with known defects | Consistent, complete, tested backups and UI restore |

## Configuration ownership and compatibility

Maintain a versioned directive catalog describing types, scope, multiplicity, defaults, dependencies, deprecations and reload/restart behavior. The Compose file currently selects the Mosquitto 2.0 image family; this does not establish support for every option of every release. Test exact supported releases and publish their matrix before making a completeness claim.

Import must preserve include ordering, repeated listener/bridge blocks, comments and unknown directives. An advanced editor provides access to supported directives without a dedicated form. Unknown syntax must be retained or explicitly rejected, never silently dropped. Import and adoption of existing passwd/ACL files must precede replacing unmanaged content.

Keep desired configuration, reviewed revision, written files and verified active state distinct. Detect external edits before applying. Credentials and private keys must not appear in normal API responses, logs or diffs. MQTT service accounts are visible managed dependencies, not ordinary removable entries.

A deployment adapter reports read, write, reload, restart and log capabilities. MQTT connectivity alone is not an administration transport. A remote broker needs an explicitly configured management path; otherwise the UI must identify observation-only operation. Docker port publication and mounted-file permissions are separate from Mosquitto listener settings.

## Operator journey

1. Identify the broker version, management capabilities and configuration location.
2. Import existing configuration and review ownership and unsupported options.
3. Edit guided forms or advanced configuration, with scope-aware validation.
4. Review an immutable diff, affected files and required reload/restart.
5. Apply that exact revision and verify its effect, including expected access denials.
6. Recover a previous revision or a verified backup, with a visible result and audit record.

The proposed navigation is Configuration, MQTT Access, Changes, Diagnostics and Administration. Preserve role restrictions, MFA and audit throughout the journey. A saved database record must not be presented as an active broker change.

## Known operational gaps

- File replacement is atomic per file, not across the ACL/passwd pair; recovery does not currently cover every applier failure (#292).
- File mode can skip signaling when PIDPath is absent, and CONNECT/CONNACK does not establish that the intended policy is active (#293).
- The production template requires explicit path/ACL alignment and a verified permission/activation mechanism (#294).
- Preview and Apply are not bound to an immutable revision (#296).
- Renames and existing unmanaged file entries need an ownership and reference policy (#297, #298).
- Backup/restore paths, snapshot consistency, volume selection and complete broker coverage need correction (#295).

Report sensitive security findings through [SECURITY.md](../SECURITY.md); this scope document is not a vulnerability disclosure.

## Optional scope

Sparkplug decoding and JSON Schema checks inspect observed payloads. They do not configure broker listeners or enforce payload rejection in Mosquitto. Keep these as optional diagnostics. Defer multi-broker, fleet management, SSO, multi-tenancy, HA and PostgreSQL until the one-broker journey is complete.

## Validation and documentation contract

A feature is complete only when UI, API, generated configuration and behavior agree. Use real Mosquitto integration checks for file loading, permissions, TLS, bridges, reload/restart and recovery; UI mocks alone are insufficient. A published endpoint is not proof of end-to-end functionality.

Keep README, roadmap, deployment guides and GitHub Pages explicit about available, partial and planned features. The Pages source is `site/`; `.github/workflows/site.yml` builds and publishes it after changes reach `main`.

Reference semantics against the [Mosquitto configuration manual](https://mosquitto.org/man/mosquitto-conf-5.html) and [Dynamic Security documentation](https://mosquitto.org/documentation/dynamic-security/), then verify against the exact supported release.
