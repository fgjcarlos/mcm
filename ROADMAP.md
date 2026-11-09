# MCM roadmap

Updated after the configuration epic [#308](https://github.com/fgjcarlos/mcm/issues/308) was implemented and verified.

## Product goal

Manage the configuration of one external Mosquitto instance from the web: import, edit, validate, review, apply, verify and recover. Coverage is defined per supported broker version. The goal includes advanced directives; a version-aware advanced editor complements guided forms.

The core administration journey is implemented. [Epic #308](https://github.com/fgjcarlos/mcm/issues/308) tracked its delivery and [product scope](./docs/product-scope.md) defines the acceptance contract. The exact directive coverage is published in the [support matrix](./docs/support-matrix.md).

## Available today

- Embedded React UI and REST/WebSocket API in a Go application image, with an external Mosquitto container.
- MQTT users and basic user/topic ACLs; a separate Deploy action writes password and ACL files.
- Password/ACL diff, deployment history, SIGHUP activation and rollback after a failed connectivity check.
- Operator accounts, roles, MFA, audit/security events, observed traffic and connection events.
- SQLite persistence for MCM; Prometheus integration and optional JSON Schema/Sparkplug diagnostics.
- The advanced root `mosquitto.conf` editor with import, draft validation, immutable review, apply and revision recovery, next to the listener, bridge, plugin and Dynamic Security editors.
- The general broker settings (persistence, queues, sessions and logging) are editable through catalog-driven settings forms.
- A versioned [directive support matrix](./docs/support-matrix.md) that states, per broker version, which directives MCM manages, partially supports and does not administer.
- A verified backup and restore path that captures and restores the MCM database, the broker configuration and its certificate material.

The current image family is Mosquitto 2.0. The support matrix covers Mosquitto 2.0 and 2.1 and is not a compatibility guarantee across every Mosquitto release.

## Delivered work

Every work unit of epic #308 landed behind its own pull request, verified with unit, integration and real-broker tests.

| Priority | Work | Issue | Pull request |
| --- | --- | --- | --- |
| P0 | Input validation for Mosquitto files | [#291](https://github.com/fgjcarlos/mcm/issues/291) | [#349](https://github.com/fgjcarlos/mcm/pull/349) |
| P0 | Recovery after partial writes and cancellation | [#292](https://github.com/fgjcarlos/mcm/issues/292) | [#350](https://github.com/fgjcarlos/mcm/pull/350) |
| P0 | Verified activation and explicit pending state | [#293](https://github.com/fgjcarlos/mcm/issues/293) | [#351](https://github.com/fgjcarlos/mcm/pull/351) |
| P0 | Production paths, permissions and activation adapter | [#294](https://github.com/fgjcarlos/mcm/issues/294) | [#352](https://github.com/fgjcarlos/mcm/pull/352) |
| P0 | Consistent backup and verified restore | [#295](https://github.com/fgjcarlos/mcm/issues/295) | [#361](https://github.com/fgjcarlos/mcm/pull/361) |
| P1 | Versioned configuration model and lossless import | [#298](https://github.com/fgjcarlos/mcm/issues/298) | [#362](https://github.com/fgjcarlos/mcm/pull/362) |
| P1 | Immutable preview/apply revisions | [#296](https://github.com/fgjcarlos/mcm/issues/296) | [#314](https://github.com/fgjcarlos/mcm/pull/314), [#316](https://github.com/fgjcarlos/mcm/pull/316) |
| P1 | MQTT identity and ACL consistency | [#297](https://github.com/fgjcarlos/mcm/issues/297) | [#363](https://github.com/fgjcarlos/mcm/pull/363) |
| P1 | MQTT and WebSocket listeners | [#299](https://github.com/fgjcarlos/mcm/issues/299) | [#364](https://github.com/fgjcarlos/mcm/pull/364) |
| P1 | Broker TLS and certificate lifecycle | [#300](https://github.com/fgjcarlos/mcm/issues/300) | [#365](https://github.com/fgjcarlos/mcm/pull/365) |
| P1 | Persistence, limits, sessions and logging | [#302](https://github.com/fgjcarlos/mcm/issues/302) | [#366](https://github.com/fgjcarlos/mcm/pull/366) |
| P1 | Bridges between brokers | [#303](https://github.com/fgjcarlos/mcm/issues/303) | [#367](https://github.com/fgjcarlos/mcm/pull/367) |
| P1 | Configuration and recovery UI workflow | [#305](https://github.com/fgjcarlos/mcm/issues/305) | [#335](https://github.com/fgjcarlos/mcm/pull/335), [#336](https://github.com/fgjcarlos/mcm/pull/336) |
| P2 | Broker diagnostics with explicit data sources | [#306](https://github.com/fgjcarlos/mcm/issues/306) | [#337](https://github.com/fgjcarlos/mcm/pull/337), [#338](https://github.com/fgjcarlos/mcm/pull/338) |
| P2 | Remove unsupported options and isolate optional extensions | [#307](https://github.com/fgjcarlos/mcm/issues/307) | [#343](https://github.com/fgjcarlos/mcm/pull/343) |
| P2 | Plugins and Dynamic Security | [#304](https://github.com/fgjcarlos/mcm/issues/304) | [#330](https://github.com/fgjcarlos/mcm/pull/330) |

[#301](https://github.com/fgjcarlos/mcm/issues/301) (native authentication and ACL coverage) was audited against its acceptance criteria and required no code change.

## Definition of done

- [x] Start with an existing broker and complete import → edit → validate → review → apply → verify → recover from the UI without manually editing files.
- [x] Publish a tested matrix of supported, partial and unsupported directives for each supported version — [support matrix](./docs/support-matrix.md).
- [x] Preserve comments, include order, repeated sections and unknown directives; reject conflicts rather than silently overwriting external changes.
- [x] Show pending versus active configuration and distinguish reload from restart.
- [x] Apply the reviewed revision, verify its effect, and recover after partial failure or loss of connectivity.
- [x] Exercise positive/negative authentication and ACL checks, listener/TLS changes, bridges and restoration with real Mosquitto.
- [x] Keep credentials protected, MQTT permissions separate from MCM roles, and changes auditable.
- [x] Keep README, roadmap, deployment guides and GitHub Pages explicit about available, partial and planned features.

## Remaining work

The core journey is delivered. The known gaps are stated in the [support matrix](./docs/support-matrix.md) and the [product scope](./docs/product-scope.md): five directives per supported version have no dedicated graphical control, the included files of an `include_dir` tree are read but not edited, and the native ACL coverage remains partial.

## Optional and deferred work

Sparkplug B, JSON Schema inspection, webhook enhancements and advanced dashboards do not block the core configuration journey. Existing roles, MFA and audit remain part of the core. Multi-broker administration, fleet agents, SSO, multi-tenancy, HA and PostgreSQL are deferred. SQLite is the only implemented persistence backend.

## Historical scope

The Docker-first pivot (#226) removed the old command suite, edge agent and embedded broker approach. The executable retains `--config` and `--version`. Superseded ADRs preserve those historical decisions; they are not current capabilities.
