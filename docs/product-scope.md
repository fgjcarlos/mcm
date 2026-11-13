# Graphical Mosquitto configuration: scope and acceptance

## Objective and boundary

MCM should let an operator administer the configuration of one external Mosquitto instance from a browser. After deployment access is configured, routine configuration changes should not require hand-editing files. REST remains the UI's automation interface. SQLite stores management state; Mosquitto remains responsible for MQTT behavior.

This is the agreed target, implemented by [epic #308](https://github.com/fgjcarlos/mcm/issues/308). This document states the acceptance contract and the exact coverage that was delivered; the [roadmap](../ROADMAP.md) and the [support matrix](./support-matrix.md) carry the per-version detail.

## Current coverage

| Area | Current implementation | Target |
| --- | --- | --- |
| Users | Create, remove, disable and rotate generated passwords | Coherent identity lifecycle with ACL references and pending/active state |
| ACL | User/topic rules with read, write and readwrite | Native patterns, deny and listener scope supported by each version |
| Apply | Password/ACL file diff, write, SIGHUP and limited recovery | Immutable revision, validation, reload/restart selection, verification and recovery |
| Configuration | Versioned import, advanced root-file editor, catalog preview, explicit adoption, apply and revision recovery; includes are inspected but not edited | Import and graphical editing of mosquitto.conf and includes |
| General settings | Catalog-driven forms for persistence, queues, sessions and logging with a preview and a reload/restart distinction (issue #302) | Every supported general setting of every supported version |
| Network | Static listeners | MQTT/WebSocket listeners, binding and deployment port compatibility |
| TLS | MCM HTTP TLS and outbound MQTT TLS | Broker certificate lifecycle and listener TLS/mTLS |
| Bridges and plugins | Bridge editor for the connections between brokers: destinations, client ids, credentials, TLS, reconnection and topic rules with a direction, a quality of service and local/remote prefixes, validated against names, dependencies and message loops and activated through a reviewed revision (issue #303). Plugin catalog per broker version with the supported plugins and their options, a lossless editor of the plugin directives of the managed file and a Dynamic Security editor that reads the security database of the plugin from the broker itself (issue #304). | Bridge editor and supported plugins, including Dynamic Security |
| Diagnostics | Wildcard-observed traffic, broker `$SYS` metrics, broker log entries and MCM events | Explicit broker metrics/log sources with stated limitations |
| Recovery | Root-configuration copy/revision recovery from UI; a verified backup and restore of the MCM database, the broker configuration and its certificate material | Consistent, complete, tested backups and UI restore |

The exact directive coverage per broker version is published in the [support matrix](./support-matrix.md) and served over `GET /api/v1/broker/config/support-matrix`.

## Configuration ownership and compatibility

Maintain a versioned directive catalog describing types, scope, multiplicity, defaults, dependencies, deprecations and reload/restart behavior. The Compose file currently selects the Mosquitto 2.0 image family; this does not establish support for every option of every release. Test exact supported releases and publish their matrix before making a completeness claim.

Import must preserve include ordering, repeated listener/bridge blocks, comments and unknown directives. An advanced editor provides access to supported directives without a dedicated form. Unknown syntax must be retained or explicitly rejected, never silently dropped. Import and adoption of existing passwd/ACL files must precede replacing unmanaged content.

Keep desired configuration, reviewed revision, written files and verified active state distinct. Detect external edits before applying. Credentials and private keys must not appear in normal API responses, logs or diffs. MQTT service accounts are visible managed dependencies, not ordinary removable entries.

A deployment adapter reports read, write, reload, restart and log capabilities. MQTT connectivity alone is not an administration transport. A remote broker needs an explicitly configured management path; otherwise the UI must identify observation-only operation. Docker port publication and mounted-file permissions are separate from Mosquitto listener settings.

## General settings (issue #302)

The general settings of the broker are edited through guided forms: persistence and autosave, queues and messages, connections and sessions, and logging. Every field is described by the versioned catalog — type, unit, accepted range, broker default, help text and whether a change needs a reload or a restart — so the bounds the operator sees are the bounds the server enforces.

The forms only manage the global part of `mosquitto.conf`. A directive inside a listener block belongs to that listener and stays in the listener editor. A repeatable directive such as `log_dest` keeps every occurrence, and a directive the operator leaves unset falls back to the broker default. Saving the settings without an edit leaves the file byte-for-byte identical; comments, unknown directives, include order and listener blocks are preserved.

## Listener management (issue #299)

MCM provides CRUD for MQTT and WebSocket listener specifications through reviewed preview/apply revisions; listener changes require a broker restart, and Compose port mappings remain an independent coexistence constraint that must include every managed listener port.

## Bridge management (issue #303)

MCM provides CRUD for the bridge connections between brokers through reviewed preview/apply revisions. A bridge states its destination, its client ids, its credentials, its TLS material and its reconnection behaviour, and it forwards topic rules with a direction, a quality of service and local/remote prefixes. The validator refuses an invalid name or address, a missing dependency such as a client certificate without its private key, and the three shapes of message loop the broker would fall into. A credential is never returned by the API and never travels through a diff or a log line. Mosquitto reads a bridge at start-up, so an activated bridge needs a broker restart. The behaviour is proven against two real Mosquitto instances, including the recovery of the connection when the remote broker comes back.

## Plugin and Dynamic Security management (issue #304)

MCM keeps the plugins of the broker and the security database of the dynamic security plugin apart, and keeps both away from the file backend.

**Plugin catalog.** The catalog lists the plugins MCM is able to configure for a broker version, the module path they are loaded from and the options MCM writes. The editor replaces the plugin directives of the managed `mosquitto.conf` and preserves everything the catalog does not know: a plugin MCM does not manage stays in the file, and a `plugin_opt_*` option MCM does not know stays byte-for-byte next to the options it does write. A plugin is loaded at start-up and Mosquitto never re-reads the directive on SIGHUP, so an activated plugin change needs a broker restart, which the preview states and the apply refuses without an explicit confirmation.

**Dynamic Security adapter.** The clients, the groups, the roles and their access entries live in the JSON database of the plugin, and the plugin is the only authority on what it enforces. MCM reaches that database through the control channel of the plugin (`$CONTROL/dynamic-security/v1`) and reads the state back from the broker. Every command carries a correlation value, every answer is matched with the request it belongs to, a refused command is reported instead of being assumed to have worked, and the state is read back and compared with the requested state after every change. The plugin disconnects the clients it just changed and publishes its answer after that disconnect, so the answer of a change that touches the management account never arrives; the verification of the state is what decides whether the change landed, not the answer.

**Backends kept apart.** The access control of the broker lives either in the password and ACL files or in the database of the plugin. MCM detects which one is in charge and never writes to one while the other is. The migration from the file backend to the plugin is an explicit operation: the operator asks for it, MCM shows what it brings (the clients and the roles the users and the rules become, the configuration revision that loads the plugin and drops the file directives, and the passwords of the clients the migration creates, shown once), and the change only happens when the operator applies that plan.

**Management channel.** The control channel administers every permission of the broker, so MCM reports who may reach it. A client that must not reach it, a default access that lets every client publish or subscribe, and a management account that lost its access are all findings the preview refuses. The behaviour is proven against a real Mosquitto with the plugin: the access entries MCM writes are the ones the broker enforces, and the clients that must not reach the channel are refused by the broker.

## Operator journey

1. Identify the broker version, management capabilities and configuration location.
2. Import existing configuration and review ownership and unsupported options.
3. Edit guided forms or advanced configuration, with scope-aware validation.
4. Review an immutable diff, affected files and required reload/restart.
5. Apply that exact revision and verify its effect, including expected access denials.
6. Recover a previous revision or a verified backup, with a visible result and audit record.

The proposed navigation is Configuration, MQTT Access (MQTT users and broker access), Changes, Diagnostics and Panel administration (panel operators and account security). Below the desktop breakpoint the navigation folds behind a Menu button so the content comes first. Preserve role restrictions, MFA and audit throughout the journey. A saved database record must not be presented as an active broker change.

## Known operational gaps

These gaps are the state after epic #308 was implemented and verified. They are stated so no
consumer assumes coverage MCM does not deliver.

- Native ACL coverage is partial: the directive-level editing of `deny` rules and of the `%u`
  and `%c` pattern forms is not exposed through the graphical editor.
- Five directives per supported version are validated and preserved but have no dedicated
  graphical control, so the operator edits them in the advanced root editor: `memory_limit`,
  `message_size_limit`, `port`, `sys_interval` and `user`. Every other upstream directive is
  preserved byte-for-byte and is not administered. The published
  [support matrix](./support-matrix.md) is the exact list per version.
- The included files of an `include_dir` tree are resolved and inspected on import, but they
  are not editable through the UI.
- The broker-wide `port` directive is informational for the Compose stack: the broker is
  published through explicit `listener` blocks.
- The observed diagnostics are not a complete broker or client inventory. The `$SYS` metrics
  and the broker log entries MCM reads are stated with their limits in the API response.
- The production deployment requires the operator to provide the path alignment, the
  permissions and the reload/restart command of the target host. MCM verifies the effective
  files and the capabilities it was given; it cannot change how the host is operated.

Report sensitive security findings through [SECURITY.md](../SECURITY.md); this scope document is not a vulnerability disclosure.

## Optional scope

Sparkplug decoding and JSON Schema checks inspect observed payloads. They do not configure broker listeners or enforce payload rejection in Mosquitto. Keep these as optional diagnostics. Defer multi-broker, fleet management, SSO, multi-tenancy, HA and PostgreSQL until the one-broker journey is complete.

## Validation and documentation contract

A feature is complete only when UI, API, generated configuration and behavior agree. Use real Mosquitto integration checks for file loading, permissions, TLS, bridges, reload/restart and recovery; UI mocks alone are insufficient. A published endpoint is not proof of end-to-end functionality.

Keep README, roadmap, deployment guides and GitHub Pages explicit about available, partial and planned features. The Pages source is `site/`; `.github/workflows/site.yml` builds and publishes it after changes reach `main`.

Reference semantics against the [Mosquitto configuration manual](https://mosquitto.org/man/mosquitto-conf-5.html) and [Dynamic Security documentation](https://mosquitto.org/documentation/dynamic-security/), then verify against the exact supported release.
