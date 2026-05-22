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

This edge-safe subset is intentionally bounded so the validator stays cheap on edge hardware. Malformed schemas, invalid MQTT topic filters, oversized schemas, unknown `type` values, and any malformed keyword from the table below are rejected at create/update time.

| Keyword | Applies to | Notes |
|---|---|---|
| `type` | any | One of `object`, `array`, `string`, `number`, `integer`, `boolean`, `null`. |
| `required` | object | Array of property names that must be present. |
| `properties` | object | Per-property nested schema. Recurses. |
| `additionalProperties` | object | Boolean. `false` rejects unknown keys. |
| `enum` | any | Non-empty array of allowed values. Deep-equality match. |
| `minimum` / `maximum` | number / integer | Inclusive numeric bounds. |
| `minLength` / `maxLength` | string | Non-negative integers; measured in UTF-8 runes. |
| `pattern` | string | Go (RE2) regular expression. Validated at create/update time so a bad pattern fails fast. |
| `items` | array | Single object schema applied to every element. Recurses. |

### Non-goals (current release)

These are intentionally not implemented and are rejected silently (ignored) if present on a schema, but new schemas should not rely on them:

- `$ref` and any form of remote schema reference.
- Composition keywords (`allOf`, `anyOf`, `oneOf`, `not`).
- Tuple-style `items` (an array of schemas) — single-schema only.
- `format`, `propertyNames`, `dependencies`, `if`/`then`/`else`, `const`.
- Schema dialects other than the constrained subset documented above.

`$ref` and composition keywords may be revisited once a use case justifies the validator size and the audit complexity they bring.

### Error envelope

Every operator-visible validation surface (broker WebSocket frames, audit metadata, API responses) shares the same shape: a boolean `valid` plus an `errors` array capped at five entries followed by `"additional validation errors omitted"` when truncated. Raw MQTT payloads are never persisted; only the bounded error strings, payload format, byte length, and truncation flag.

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
