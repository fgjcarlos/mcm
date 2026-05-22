# Sparkplug B topic awareness

MCM includes a first topic-level Sparkplug B awareness layer for operators monitoring MQTT traffic.

## Supported detection

MCM classifies inbound MQTT topic messages as Sparkplug B when the topic matches one of these namespace shapes:

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

When a topic matches, broker events include optional `sparkplug` metadata with:

- `namespace`
- `group_id`
- `message_type`
- `edge_node_id`
- `device_id` when present

Generic MQTT topics continue to be displayed without Sparkplug metadata.

## Scope and limitations

This is intentionally topic-level awareness only. MCM does not decode Sparkplug protobuf payloads, validate metric schemas, retain raw payload data, or act as a Sparkplug historian/broker. Safe payload previews remain bounded by the existing MQTT topic explorer behavior, and broker metric persistence stores topic/payload metadata rather than raw payload contents.

Full Sparkplug protobuf decoding, metric inspection, historical analysis, and richer validation can be added in later issues.
