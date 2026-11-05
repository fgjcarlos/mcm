# Issue #306 — Real broker metrics and logs with explicit limits

Branch: `feat/306-broker-observability` (cut from `origin/main`). Delivery: one pull request with reviewable work-unit commits, unless the parent session splits it. The issue is P2 ("cobertura avanzada o simplificación posterior"), so the scope stays bounded to the four acceptance criteria and nothing that the issue does not name.

Writer: delegated to `gpt-luna-6` (model `openai/gpt-6-luna`), one bounded unit per delivery stage, sequentially. The parent session owns the plan, the commits, the review disposition and the user.

## Specs

S1. "Distinguir tráfico observado, eventos MCM, logs del broker y métricas internas."
S2. "Suscripción explícita a $SYS y permisos mínimos cuando corresponda."
S3. "Ingesta de logs mediante adaptador con filtros y límites; no inferir client IDs desde publicaciones normales."
S4. "API diagnóstica TCP/TLS/MQTT documentada, sin atribuir capacidades inexistentes a /readyz."
S5. "El monitor se suscribe a #; logs y métricas observadas no son un inventario completo de Mosquitto."
S6. "La auditoría fue principalmente estática; verificar los cambios de comportamiento con pruebas de integración. No declarar una capacidad completa por disponer únicamente de un endpoint."
S7. "Evidencia: `internal/server/broker_events.go`, `internal/server/app.go`, revisión `2d53ea5fbb56028c759e524f7499ca5b4f8eeaff`."
S8. "P2: cobertura avanzada o simplificación posterior."

## Contracts and constraints

C1 — `GET /api/v1/status` keeps its current response shape. The frontend (`frontend/src/features/broker/useBrokerStream.ts`) and the scripts read it; the source separation lands on a NEW documented route instead of reshaping the existing one.
C2 — `internal/server/openapi_sync_test.go` fails when a `mux.Handle*` route is missing from `docs/openapi.yaml` or when the document names a route the router does not serve. The new route and every schema it uses are added to `docs/openapi.yaml` in the same change.
C3 — `frontend/dist/index.html` is the only tracked file of the built frontend (`frontend/.gitignore` ignores the rest). A frontend change rebuilds it and commits the new `index.html`.
C4 — The service-user ACL is rendered by `serviceUserACL` in `internal/deploy/service.go`; it already grants `read #` and `readwrite mcm/healthcheck`. A `$SYS/#` grant is added there, and the assertions over the rendered ACL (`internal/deploy/acl_native_test.go` and neighbours) are updated in the same change.
C5 — MQTT wildcards never match `$`-prefixed topics, and Mosquitto refuses a subscription the ACL does not authorize. The `$SYS` subscription is therefore explicit, and a refusal degrades the feature (reported as unavailable with the broker's reason) instead of failing the monitor.
C6 — The broker log volume `mosquitto_logs` is mounted in the broker container only (`docker-compose.yml`). The adapter needs it mounted read-only in the `mcm` service, and the production template must match.
C7 — The WebSocket contract already carries a `source` field whose current value for MCM-generated events is the ambiguous string `"broker"`. The source vocabulary is redefined so an operator can tell the four origins apart; the frontend consumes the new values in the same delivery.
C8 — The log adapter reads a file the operator controls. It must be bounded in bytes read per poll, in entry count, in message length and in age, and it must survive a rotated or truncated file.

## API contract

`GET /api/v1/broker/diagnostics` — role `auditor` (the same role the other broker read routes require). It never opens a new MQTT session; the MQTT section reports the monitor's live session.

```json
{
  "generated_at": "2026-01-01T00:00:00Z",
  "broker": {
    "status": "connected",
    "target": {"host": "mosquitto", "port": 1883, "address": "mosquitto:1883", "tls_enabled": false},
    "monitor": {"client_id": "mcm-ui-123", "subscriptions": ["#", "$SYS/#"], "connected_at": "2026-01-01T00:00:00Z", "reconnects": 0}
  },
  "sources": {
    "observed_traffic": { "...": "the existing BrokerTrafficMetrics object, unchanged" },
    "mcm_events": {"status_events": 0, "topic_messages": 0, "event_subscribers": 0, "last_message_at": null, "note": "Events produced by MCM itself. They are not broker-sourced."},
    "broker_logs": {"available": true, "source": "mosquitto_log", "path": "/mosquitto/log/mosquitto.log", "entries": [], "severity_counts": {"error": 0, "warning": 0, "info": 0, "debug": 0}, "limits": {"max_entries": 200, "max_entries_per_read": 50, "max_message_bytes": 1024, "max_age_seconds": 900}, "note": "..."},
    "internal_metrics": {"endpoint": "/metrics", "format": "prometheus", "note": "MCM's own process metrics. They are not broker-sourced.", "counters": {}}
  },
  "broker_sys": {"available": true, "reason": "", "subscriptions": ["$SYS/#"], "metrics": {"broker/clients/connected": {"value": "3", "observed_at": "2026-01-01T00:00:00Z"}}, "note": "..."},
  "diagnostics": {
    "tcp": {"status": "reachable", "address": "mosquitto:1883", "latency_ms": 1.2, "error": ""},
    "tls": {"status": "disabled|connected|failed", "version": "", "cipher": "", "error": ""},
    "mqtt": {"status": "connected", "client_id": "mcm-ui-123", "subscriptions": ["#", "$SYS/#"], "error": ""}
  }
}
```

Query parameters: `severity` (minimum severity of the returned log entries: `debug`, `info`, `warning`, `error`) and `limit` (entries returned, capped by the server-side maximum). Both are optional; an unknown severity or an out-of-range limit is a `400`.

Source vocabulary on the WebSocket events (`internal/server/broker_events.go`):

| event type | `source` | meaning |
|---|---|---|
| `topic_message` | `observed` | an application message the monitor's own `#` subscription delivered |
| `broker_status` | `mcm` | a connection-lifecycle event produced by MCM |
| `broker_log` | `mcm` | a log line produced by MCM |
| `broker_sys_metric` | `mosquitto_sys` | a `$SYS` value published by the broker |
| `broker_log_entry` | `mosquitto_log` | a line ingested from the broker's own log file |

`$SYS` values never enter the observed-traffic counters, and a client identity is never derived from an application publication: `top_clients` stays unavailable for observed traffic, and any client identity the feature exposes comes from a broker-sourced record and says so.

## Tasks

- T1 | S3, S5, C8 | unit 1 (`gpt-luna-6`) | New `internal/brokerlog` package: a `Source` interface with a file implementation that parses Mosquitto log lines into bounded entries (timestamp, severity, kind, client id when the line names one, topic when the line names one, message), a `Filter` (minimum severity, include/exclude patterns) and `Limits` (max entries, max entries per read, max message bytes, max age). The reader is byte-bounded per poll, detects a rotated or truncated file, and never allocates without a cap. Unit tests cover the parsing table, the filters, every limit and the rotation case.
- T2 | S2, C4, C5, C6 | unit 1 (`gpt-luna-6`) | Minimum permission for the monitor: `serviceUserACL` grants the service user `read $SYS/#` next to its existing `read #`, with the rendered-ACL assertions updated. The Mosquitto templates (dev and production) declare `sys_interval 10` and keep the log destination. The broker log volume is mounted read-only in the `mcm` service of `docker-compose.yml` and `docker-compose.prod.yml`, and the new `log_path` configuration field is documented.
- T3 | S1, S2, S4, S5, S6, C1, C2, C5, C7 | unit 2 (`gpt-luna-6`) | Server wiring: the monitor subscribes explicitly to `$SYS/#` beside `#` and classifies `$SYS` messages as `broker_sys_metric` outside the observed-traffic counters; the log adapter is polled into a bounded ring buffer and published as `broker_log_entry` events; `GET /api/v1/broker/diagnostics` serves the contract above with a bounded TCP probe, a TLS handshake report when TLS is enabled and the live MQTT session state; the OpenAPI document gains the route, its schemas, the query parameters and the corrected `/readyz` description. Go tests cover the `$SYS` classification, the source vocabulary, the degraded `$SYS` refusal, the diagnostics contract, the `/readyz` contract and the unchanged `/api/v1/status` shape.
- T4 | S1, S3, S5, C3, C7 | unit 3 (`gpt-luna-6`) | Frontend: the diagnostics workspace shows the four sources separately (observed traffic, MCM events, broker logs, internal metrics) plus the broker-sourced `$SYS` metrics, honours the severity and limit filters of the route, and states plainly when a source is unavailable and why. The source vocabulary of the WebSocket events is consumed with the new values. Vitest coverage for the new panel and the source grouping.
- T5 | S6 | verification | The behaviour is verified with integration tests, not only with static review: an integration test that observes a `$SYS` metric from a real broker and a broker-log line ingested from a real log file, plus the CI script that exercises the route against the Compose stack. The differential check for `GET /api/v1/status` compares stdout, stderr and exit code against the base commit. The independent verifier covers the two declared risks (permission boundary and contract truthfulness).

## Log

L1. The user's request, verbatim: "Crea un plan para resolver la issue #306 y delega en gpt-luna-6 para la implementacion."
L2. The issue was retrieved from `github.com/fgjcarlos/mcm` (`gh issue view 306`); its acceptance text is quoted in the Specs and its priority is P2.
L3. The exploration map that grounds this plan: `internal/server/broker_events.go` subscribes to `#` at QoS 0 (`buildMQTTClient`, line 845) and therefore never observes `$SYS`, because MQTT wildcards do not match `$` topics; `internal/server/app.go` serves `/api/v1/status` (line 439) with the traffic metrics, `/metrics` (line 411) with MCM's own Prometheus exposition and `/readyz` (line 410) as a database plus monitor-state gate; `internal/mosquitto/applier.go` holds the `Applier`/`BrokerRestarter` adapter pattern the new log adapter follows; `internal/deploy/service.go` (`serviceUserACL`, line 931) renders the service-user grants; `deploy/mosquitto/config/mosquitto.conf` logs to `/mosquitto/log/mosquitto.log` inside the `mosquitto_logs` volume, which only the broker container mounts today.
L4. The gap this plan closes: today a `broker_log` event on the WebSocket is an MCM-generated line whose `source` reads `"broker"`, so the operator cannot tell an MCM event from a broker log, and `top_clients` is permanently unavailable with a note that names broker-side metrics as a future feature.
L5. Delivery decisions: the implementation is delegated to `gpt-luna-6`, a writer agent pinned to `openai/gpt-6-luna`; the units are sequential because T3 consumes the adapter contract of T1 and T4 consumes the wire contract of T3, so no two units are independent; the parent owns the commits and the review.
L6. Unit 1 (`gpt-luna-6`, `openai/gpt-6-luna`) delivered T1 and T2 in three commits: `2fb091b` (the local Pi runtime state entry of `.gitignore`), `161c9e1` (the `internal/brokerlog` adapter and the `mosquitto.log_path` configuration) and `89205e5` (the `read $SYS/#` grant for the service user, `sys_interval 10` in both Mosquitto templates and the read-only mount of the broker log volume). The unit reported one finding it was not allowed to fix: `internal/config/env_table_test.go` requires every environment variable of the canonical table to appear in `README.md`, and the new `MCM_MOSQUITTO_LOG_PATH` was missing there. The parent added the README row and re-ran the package tests: `go build ./...` and `go test ./internal/brokerlog/... ./internal/config/... ./internal/deploy/... -count=1` are green.
L7. The slice boundary: the three commits of L6 add 626 authored lines in total, above the ~400-line target of a single reviewable slice. They are split by behaviour (adapter and configuration on one side, deployment and permissions on the other) so no single commit asks the reviewer to hold more than one behaviour in mind. The next commits follow the same rule.
L8. Unit 2 (`gpt-luna-6`, `openai/gpt-6-luna`) delivered T3 in three commits: `f93a6cc` (the explicit `$SYS/#` subscription beside `#`, the source vocabulary and the classification of a `$SYS` value outside the observed-traffic counters), `2d2930a` (the `GET /api/v1/broker/diagnostics` route, the bounded broker-log ingestion and its ring buffer) and `d15150f` (the OpenAPI document of the route and of the `/readyz` boundary, plus the `sys_interval` entry of the Mosquitto 2.0 directive catalog).
L9. The unit reported that `go test ./...` failed because the Mosquitto 2.0 catalog did not know `sys_interval`, the directive the shipped configuration now carries. The catalog is the authority MCM validates an operator's configuration against, so an unknown directive there was a real gap, not a test artefact: the parent added the directive (global scope, integer seconds, 0 disables the publication, `reload_kind: restart` because the publication timer is created when the broker starts) and the whole-module suite went green.
L10. The parent changed one thing in the unit's own code: `BrokerLogEvent` took a `source` argument that the new vocabulary fixed to `mcm`, so the argument was silently ignored. The parameter is gone and the function documents that the origin of a line is never inferred from its text.
L11. The TLS check of the diagnostics route reports `connected` for a successful handshake, where this document originally wrote `enabled`. The implementation and the OpenAPI document agree on `connected`, which states the outcome of the handshake instead of the configuration; the contract of this document was corrected to match.
L12. The broker-log filter and the limits are not operator-configurable in this delivery: the adapter carries them, the route applies the requested minimum severity and reports the effective limits, and the include/exclude patterns stay empty by default. Making them configurable is a deliberate deferral for a P2 issue, not an oversight; it is recorded here so the next reader does not mistake the default for a limitation of the adapter.
L13. The cumulative slice of the feature is 1458 authored lines in six commits, each of them a behaviour on its own: the local runtime state entry, the adapter and its configuration, the deployment permissions, the event vocabulary, the diagnostics route and the API document. The two remaining units (the frontend workspace and the E2E script with its CI job) follow the same rule.
L14. The independent verifier (`gentle-ai-verify`) returned FAIL on both declared risks against the committed HEAD and produced six blockers. Every one of them was a real defect and every one of them is corrected:
- **A refused `$SYS/#` subscription was reported as available.** Paho v1.5.1 reports the broker's refusal (the SUBACK code 0x80) only in the token's result map, never in its `Error`, so the monitor reported a successful subscription the broker had dropped. The parent added a per-filter refusal check for both `#` and `$SYS/#` (`subscribeTopic` and `subscriptionRefusal` in `internal/server/broker_events.go`) with a unit test.
- **Viewers received the broker log entries through the WebSocket.** The diagnostics route requires the auditor role, but the event stream only authenticates. The hub now delivers the `broker_log_entry` events only to the subscribers whose role is at least auditor (`brokerSubscriber.role`), with a test.
- **The `$SYS` events displaced the application traffic from the persisted traffic window.** The persisted broker metric events feed the observed-traffic window, whose query is bounded; the broker publishes roughly forty `$SYS` values every ten seconds, so the window lost real application messages. The broker-sourced event types are no longer persisted, with a test.
- **An operator-selected FIFO could block the ingestion and the shutdown.** The adapter opened the path without checking what it was. It now ingests regular files only, with a test.
- **The E2E script sent the administrator token in its "unauthenticated" request** (`${3:-$TOKEN}` substitutes the default for an empty argument), so the script could never pass its own 401 assertion. The unauthenticated call has its own helper now.
- **The shipped broker log was unreadable by the MCM service.** Mosquitto creates the log file 0600 owned by the user it runs as, and the MCM container runs as another UID. The broker bootstrap now pre-creates the log file with a readable mode.
L15. The real local run of the E2E script against the Compose stack was what exposed the last two defects; a static review had not. The script has three further fixes of the same kind: the `$SYS` publication arrives every `sys_interval` seconds, so the client count is polled instead of read once; the unescaped `$SYS` in a double-quoted message aborted the script under `set -u`; and the publication uses the only topic the service user may write (`mcm/healthcheck`), because the ACL grants write on that topic alone. With those fixes the script passes every invariant against a real Mosquitto 2.0.22: the 401 without a token, the reachable TCP and connected MQTT, the `$SYS` client metric of the connected monitor, the application publication counted while no `$SYS` topic appears among the observed topics, the broker's own connection line ingested from its log, the source notes, the 400 answers for an unknown severity and an out-of-range limit, and a `/readyz` that still answers only the readiness status.
L16. The Mosquitto 2.0 manual page states that `sys_interval` is "Reloaded on reload signal". The catalog entry of the parent had claimed a restart; the entry now says reload, which is what the documentation states.
L17. The verifier could not confirm the catalog entry against the official documentation (it had no network access). The parent confirmed it against the `mosquitto.conf(5)` manual page: "The integer number of seconds between updates of the $SYS subscription hierarchy... If unset, defaults to 10 seconds. Set to 0 to disable publishing the $SYS hierarchy completely. This option applies globally. Reloaded on reload signal."
L18. The final state of the branch: fifteen work-unit commits, the whole Go suite green (`go test ./... -count=1`), the frontend suite green (287 tests), `gofmt` clean, the OpenAPI synchronization test green, the documentation lint green, the differential check of `/api/v1/status` against the base commit and the real E2E run against a live Mosquitto green.

