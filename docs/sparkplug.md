# Sparkplug B Topic Awareness

MCM includes a topic-level Sparkplug B awareness layer for operators monitoring MQTT traffic. This is **topic metadata classification only** — MCM does not decode Sparkplug protobuf payloads in the current phase.

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

## Current limitations

MCM provides **topic-level awareness only**. The following capabilities are explicitly **not** supported in the current phase:

| Capability | Status |
|------------|--------|
| Topic namespace classification (`spBv1.0/...`) | Supported |
| Group, edge node, and device ID extraction | Supported |
| Message type detection (NBIRTH, DDATA, etc.) | Supported |
| Sparkplug badge and metadata display in dashboard | Supported |
| Protobuf payload decoding | Not supported |
| Metric name, value, and type extraction | Not supported |
| Metric alias resolution | Not supported |
| Birth/death certificate parsing | Not supported |
| NBIRTH/DBIRTH metric definition tracking | Not supported |
| Historical metric storage or trending | Not supported |
| Sparkplug state/session awareness | Not supported |
| Payload schema validation | Not supported |

The dashboard UI displays Sparkplug badges and topic-level metadata (group, node, device, message type). It does not display decoded payload fields, metric values, or birth certificate contents. No UI text implies full Sparkplug payload support.

## Future: protobuf payload decoding

A future phase can add real Sparkplug B payload decoding. The following outlines the scope, approach, and constraints.

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

1. Add the Sparkplug B protobuf schema (`sparkplug_b.proto`) and generate Go types.
2. Add a `sparkplug.DecodePayload(messageType, bytes) (Payload, error)` function with bounded metric parsing.
3. Extend broker event metadata to include decoded metric summaries (count, names, types) when opt-in is enabled.
4. Add a dashboard panel for Sparkplug metric inspection (expand a message to see decoded fields).
5. Add optional metric history storage with retention policy.
6. Add NBIRTH/DBIRTH session tracking (per-node birth state, alias map, session sequence number).

Each step can be delivered independently. Step 1-2 is the foundation; steps 3-6 build on it incrementally.
