# MCM roadmap

Updated after the configuration audit of `2d53ea5fbb56028c759e524f7499ca5b4f8eeaff`.

## Product goal

Manage the configuration of one external Mosquitto instance from the web: import, edit, validate, review, apply, verify and recover. Coverage is defined per supported broker version. The goal includes advanced directives; a version-aware advanced editor complements guided forms.

The current alpha is **not feature-complete for this goal**. [Epic #308](https://github.com/fgjcarlos/mcm/issues/308) tracks implementation. [Product scope](./docs/product-scope.md) defines the acceptance contract. Documentation changes do not close implementation issues.

## Available today

- Embedded React UI and REST/WebSocket API in a Go application image, with an external Mosquitto container.
- MQTT users and basic user/topic ACLs; a separate Deploy action writes password and ACL files.
- Password/ACL diff, deployment history, SIGHUP activation and rollback after a failed connectivity check, with known gaps below.
- Operator accounts, roles, MFA, audit/security events, observed traffic and connection events.
- SQLite persistence for MCM; Prometheus integration and optional JSON Schema/Sparkplug diagnostics.

There is no graphical `mosquitto.conf` editor, listener/bridge editor, broker certificate manager or graphical backup/restore workflow. Settings are read-only. The current image family is Mosquitto 2.0; a tested directive/version matrix remains to be built.

## Delivery order and tracked work

P0 fixes precede expansion of configuration writes. P1 completes the core administration journey. P2 extends coverage or simplifies secondary features; plugin coverage remains part of the full product goal.

| Priority | Work | Issue |
| --- | --- | --- |
| P0 | Input validation for Mosquitto files | [#291](https://github.com/fgjcarlos/mcm/issues/291) |
| P0 | Recovery after partial writes and cancellation | [#292](https://github.com/fgjcarlos/mcm/issues/292) |
| P0 | Verified activation and explicit pending state | [#293](https://github.com/fgjcarlos/mcm/issues/293) |
| P0 | Production paths, permissions and activation adapter | [#294](https://github.com/fgjcarlos/mcm/issues/294) |
| P0 | Consistent backup and verified restore | [#295](https://github.com/fgjcarlos/mcm/issues/295) |
| P1 | Immutable preview/apply revisions | [#296](https://github.com/fgjcarlos/mcm/issues/296) |
| P1 | MQTT identity and ACL consistency | [#297](https://github.com/fgjcarlos/mcm/issues/297) |
| P1 | Versioned configuration model and lossless import | [#298](https://github.com/fgjcarlos/mcm/issues/298) |
| P1 | MQTT and WebSocket listeners | [#299](https://github.com/fgjcarlos/mcm/issues/299) |
| P1 | Broker TLS and certificate lifecycle | [#300](https://github.com/fgjcarlos/mcm/issues/300) |
| P1 | Native authentication and ACL coverage | [#301](https://github.com/fgjcarlos/mcm/issues/301) |
| P1 | Persistence, limits, sessions and logging | [#302](https://github.com/fgjcarlos/mcm/issues/302) |
| P1 | Bridges between brokers | [#303](https://github.com/fgjcarlos/mcm/issues/303) |
| P2 | Plugins and Dynamic Security | [#304](https://github.com/fgjcarlos/mcm/issues/304) |
| P1 | Configuration and recovery UI workflow | [#305](https://github.com/fgjcarlos/mcm/issues/305) |
| P2 | Broker diagnostics with explicit data sources | [#306](https://github.com/fgjcarlos/mcm/issues/306) |
| P2 | Remove unsupported options and isolate optional extensions | [#307](https://github.com/fgjcarlos/mcm/issues/307) |

### Dependencies

1. Resolve #291–#295 before expanding broker configuration writes.
2. Immutable revisions (#296) and the versioned model/importer (#298) underpin listeners, TLS, access, general settings, bridges and plugins (#299–#304).
3. Identity consistency (#297) and native ACL coverage (#301) must agree on ownership and references.
4. The UI workflow (#305) integrates these capabilities and verified backups (#295).
5. Diagnostics (#306), optional-feature simplification (#307), and Dynamic Security (#304) follow the core work.

## Definition of done

- Start with an existing broker and complete import → edit → validate → review → apply → verify → recover from the UI without manually editing files.
- Publish a tested matrix of supported, partial and unsupported directives for each supported version.
- Preserve comments, include order, repeated sections and unknown directives; reject conflicts rather than silently overwriting external changes.
- Show pending versus active configuration and distinguish reload from restart.
- Apply the reviewed revision, verify its effect, and recover after partial failure or loss of connectivity.
- Exercise positive/negative authentication and ACL checks, listener/TLS changes, bridges and restoration with real Mosquitto.
- Keep credentials protected, MQTT permissions separate from MCM roles, and changes auditable.

## Optional and deferred work

Sparkplug B, JSON Schema inspection, webhook enhancements and advanced dashboards do not block the core configuration journey. Existing roles, MFA and audit remain part of the core. Multi-broker administration, fleet agents, SSO, multi-tenancy, HA and PostgreSQL are deferred. SQLite is the only implemented persistence backend.

## Historical scope

The Docker-first pivot (#226) removed the old command suite, edge agent and embedded broker approach. The executable retains `--config` and `--version`. Superseded ADRs preserve those historical decisions; they are not current capabilities. The existing Taskfile backup/restore recipes are incomplete and tracked in #295.
