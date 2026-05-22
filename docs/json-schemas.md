# JSON schema validation

MCM supports a bounded JSON Schema MVP for validating observed MQTT JSON payloads against operator-defined topic filters.

## Scope

- Schema definitions are stored in SQLite and tied to MQTT topic filters (`+` and terminal `#` wildcards).
- CRUD endpoints live under `/api/v1/json-schemas` and require admin authentication.
- Incoming JSON topic messages are validated against the first enabled schema whose topic filter matches the topic.
- Validation results are exposed on broker topic events as `schema_validation` and shown in the topic explorer.
- Raw MQTT payloads are not persisted; only bounded validation status/errors and payload metadata are surfaced.

## API

Create a schema:

```bash
curl -X POST http://127.0.0.1:8080/api/v1/json-schemas \
  -H "Authorization: Bearer $MCM_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Temperature payload",
    "topic_filter": "factory/+/temperature",
    "schema": {
      "type": "object",
      "required": ["temperature", "unit"],
      "properties": {
        "temperature": {"type": "number"},
        "unit": {"type": "string"}
      },
      "additionalProperties": false
    },
    "description": "Line temperature telemetry",
    "enabled": true
  }'
```

List schemas:

```bash
curl -H "Authorization: Bearer $MCM_TOKEN" \
  http://127.0.0.1:8080/api/v1/json-schemas
```

Update and delete use:

- `PUT /api/v1/json-schemas/{id}`
- `DELETE /api/v1/json-schemas/{id}`

## Supported JSON Schema subset

This edge-safe MVP intentionally supports a constrained subset:

- `type`: `object`, `array`, `string`, `number`, `integer`, `boolean`, `null`
- `required` for object properties
- `properties` for nested validation
- `additionalProperties: false` to reject unknown object keys

Unsupported keywords are ignored for now. Malformed schemas, invalid MQTT topic filters, oversized schemas, and unsupported `type` values are rejected at create/update time.

## Broker event result

A matching topic event includes:

```json
{
  "schema_validation": {
    "schema_id": 1,
    "schema_name": "Temperature payload",
    "topic_filter": "factory/+/temperature",
    "valid": false,
    "errors": ["$.temperature must be number", "$.unit is required"]
  }
}
```

Validation errors are bounded so noisy payloads cannot flood the UI or API responses.
