# Sparkplug B Topic Awareness

MCM includes a topic-level Sparkplug B awareness layer for operators monitoring MQTT traffic. It classifies topics by default and can optionally decode Sparkplug B payloads into a bounded list of metrics; decoding is **off by default**.

> **Diagnostic only:** Classification and any decoding apply only to messages MCM observes. They never filter, block, reject, or rewrite broker publications; Mosquitto delivers every publication regardless of the diagnostic result. A diagnostic failure is information for the operator, not a gate.

## What works today

MCM classifies inbound MQTT messages as Sparkplug B when the topic matches one of these namespace shapes:

```text
spBv1.0/<group_id>/<message_type>/<edge_node_id>
spBv1.0/<group_id>/<message_type>/<edge_node_id>/<device_id>
```

Supported node message types:

- `NBIRTH`
- `NDEATH`
- `NDATA`
- `NCMD`

Supported device message types:

- `DBIRTH`
- `DDEATH`
- `DDATA`
- `DCMD`

When a topic matches, broker events and the dashboard UI include optional `sparkplug` metadata with:

- `namespace`
- `group_id`
- `message_type`
- `edge_node_id`
- `device_id` (when present)

Generic MQTT topics continue to be displayed without Sparkplug metadata.

### Optional payload decoding

Set `MCM_MOSQUITTO_SPARKPLUG_PAYLOAD_DECODE=true` (`mosquitto.sparkplug_payload_decode`) to decode the protobuf payload of Sparkplug messages MCM observes. Broker events then carry a `sparkplug_metrics` object with the payload `timestamp`, `seq`, a `metrics` list (name, alias, datatype, value, timestamp, null flag) and a `truncated` flag, and the dashboard shows the decoded metrics. The decoder (`internal/sparkplug/decode.go`) rejects payloads above 1 MB and keeps at most `MCM_MOSQUITTO_SPARKPLUG_MAX_METRICS` metrics per payload (default 50). A payload that fails to decode keeps its topic metadata and carries no metrics.

## Current limitations

The following table states what is and is not supported:

| Capability | Status |
|------------|--------|
| Topic namespace classification (`spBv1.0/...`) | Supported |
| Group, edge node, and device ID extraction | Supported |
| Message type detection (NBIRTH, DDATA, etc.) | Supported |
| Sparkplug badge and metadata display in dashboard | Supported |
| Protobuf payload decoding | Supported, opt-in (default off) |
| Metric name, value, and type extraction | Supported when decoding is on, capped per payload |
| Metric alias resolution | Not supported |
| Birth/death certificate parsing | Not supported |
| NBIRTH/DBIRTH metric definition tracking | Not supported |
| Historical metric storage or trending | Not supported |
| Sparkplug state/session awareness | Not supported |
| Payload schema validation | Not supported |

The dashboard UI displays Sparkplug badges and topic-level metadata (group, node, device, message type) and, when decoding is on, the decoded metrics of each message. Aliases are shown as received; they are not resolved to the names declared in a birth certificate, and no birth or session state is kept.

## Future work

The sections below were written before decoding existed and are kept as the design notes for the remaining steps.

### Decoding scope

| Feature | Description |
|---------|-------------|
| Protobuf deserialization | Decode `org.eclipse.tahu.protobuf.Payload` from MQTT message bytes |
| Metric extraction | Parse metric name, value, type, timestamp, and properties from the payload |
| Alias resolution | Track aliases declared in NBIRTH/DBIRTH and resolve them in subsequent NDATA/DDATA messages |
| Birth/death tracking | Maintain per-node and per-device birth state; detect session resets via NDEATH/DDEATH |
| Metric definitions | Cache metric schemas from birth certificates for display and optional validation |

### Integration options

| Option | Approach | Tradeoff |
|--------|----------|----------|
| **Eclipse Tahu (Go)** | Use `eclipse/tahu` Go bindings for the Sparkplug B protobuf schema | Full spec coverage; adds a protobuf dependency and generated code |
| **Raw protobuf** | Compile `sparkplug_b.proto` with `protoc-gen-go` directly | Lighter than Tahu; manual schema tracking required |
| **Hybrid** | Decode protobuf payloads only for display; store raw bytes for export | Balances operational visibility with storage simplicity |

### Edge deployment constraints

Sparkplug payloads on edge devices can be large (thousands of metrics in a single NBIRTH). Decoding must respect:

- **Memory bounds**: cap the number of metrics parsed per message (e.g., 10,000) to prevent OOM on constrained devices.
- **CPU budget**: decode on-demand for display, not on every inbound message. The MQTT subscriber should store raw payload bytes and decode lazily.
- **Storage limits**: do not persist decoded metrics by default. Offer an opt-in metric history table with configurable retention.
- **Payload size cap**: reject payloads exceeding a configurable maximum (e.g., 1 MB) before attempting protobuf decode.

### Implementation plan

1. Done differently: the decoder reads the protobuf wire format directly (`protowire`), without generated types.
2. Done: `sparkplug.DecodePayload(data, maxMetrics)` with bounded metric parsing.
3. Done: broker events carry `sparkplug_metrics` when decoding is enabled.
4. Add a dashboard panel for Sparkplug metric inspection (expand a message to see decoded fields).
5. Add optional metric history storage with retention policy.
6. Add NBIRTH/DBIRTH session tracking (per-node birth state, alias map, session sequence number).

Steps 4-6 remain; each can be delivered independently.
