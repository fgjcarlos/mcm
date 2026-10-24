# ACL API

MCM's initial ACL model is intentionally small and independently testable.

Each ACL rule contains:

- `id`: MCM-generated rule identifier.
- `principal`: the Mosquitto username or application identity the rule applies to.
- `topic_filter`: an MQTT topic filter, including MQTT wildcards such as `+` and `#`.
- `permission`: one of `read`, `write`, `readwrite`, or `deny`.
- `description`: optional operator-facing note.

## REST endpoints

- `GET /api/v1/acls`: list all ACL rules.
- `POST /api/v1/acls`: create a rule.
- `PUT /api/v1/acls/{id}`: update a rule.
- `DELETE /api/v1/acls/{id}`: delete a rule.

Example create request:

```json
{
  "principal": "operator",
  "topic_filter": "factory/+/temperature",
  "permission": "read",
  "description": "Read all line temperature telemetry"
}
```

Example response:

```json
{
  "id": "1",
  "principal": "operator",
  "topic_filter": "factory/+/temperature",
  "permission": "read",
  "description": "Read all line temperature telemetry"
}
```

## Topic-filter validation

MCM validates MQTT wildcard placement before storing a rule.

- `#` must occupy an entire topic level.
- `#` may only appear in the final topic level.
- `+` must occupy an entire topic level.

Examples:

- Valid: `factory/#`
- Valid: `factory/+/temperature`
- Invalid: `factory/#/temperature`
- Invalid: `factory/area+1/temperature`

Validation failures return HTTP `400` with a top-level `error` plus detailed messages under `details`.

## Mosquitto ACL mapping

This first model is designed to map directly onto Mosquitto ACL file concepts.

MCM rule:

```json
{
  "principal": "operator",
  "topic_filter": "factory/+/temperature",
  "permission": "read"
}
```

Mosquitto ACL output:

```text
user operator
topic read factory/+/temperature
```

Another example:

```json
{
  "principal": "writer",
  "topic_filter": "factory/line1/#",
  "permission": "readwrite"
}
```

Mosquitto ACL output:

```text
user writer
topic readwrite factory/line1/#
```

## Deny permission

`deny` blocks a topic filter for a principal. It is the counterpart of the
grant permissions, not a stronger grant: a deny rule never activates an
action.

- `deny` requires Mosquitto 2.0 or later. On older brokers MCM rejects the
  rule with a validation error instead of rendering a line the broker would
  ignore.
- `read`, `write` and `readwrite` keep working on every supported Mosquitto
  version.

Example request:

```json
{
  "principal": "operator",
  "topic_filter": "factory/#",
  "permission": "deny",
  "description": "Block the factory tree for this principal"
}
```

Mosquitto ACL output:

```text
user operator
topic deny factory/#
```

The rule round-trips through the API and the store unchanged: create, list,
update and delete all preserve the `deny` permission.

The current implementation stores ACL rules in memory so the API and validation logic stay isolated and easy to test. Persisting the same model in SQLite can be added later without changing the HTTP contract.

## Deployment activation probe

After a deploy, MCM runs an activation probe against the broker it just
configured. The probe has two stages:

- Positive: subscribe to a granted topic and publish a message as the same
  user. This proves the broker serves the freshly rendered credentials and
  honours the rendered grants.
- Negative: authenticate with a username that is absent from the rendered
  passwd and expect the connection to be rejected. This proves the
  authentication path is enforced. It does **not** prove that a topic
  denial is enforced: the negative stage never evaluates a topic
  permission, so MCM does not claim deny enforcement from this check.

The probe subject is a user-scoped grant. A deny rule is never chosen as
the probe subject, because a deny is not a grant.

When the policy grants nothing usable — a deny-only policy is the typical
case — MCM falls back to the managed service account and the `mcm/healthcheck`
grant it always emits for that account. The fallback requires a configured
service account with known credentials and the generated healthcheck grant in
the rendered ACL. The deployment status labels that probe as an
*operational fallback*: it shows that the broker serves the new credentials
and the service grant, not that every client permission is correct. When the
fallback is not available, the deploy fails and rolls back instead of
reporting an unverified activation.

## Security event visibility

MCM records sanitized security events for ACL API changes and ACL API failures. These events include the timestamp, event category/reason, source IP when available, HTTP method, and path. They do not include passwords, JWTs, request bodies, or other secrets.

Recent events are available to authenticated admins through `GET /api/v1/security/events?limit=100`, which returns newest events first.

Mosquitto-side authentication and ACL denials are not fully visible in this first slice. The embedded status/event stream currently observes MCM broker connectivity, topic traffic, and MCM-ingested broker log events; it does not parse Mosquitto log files nor subscribe to a broker `$SYS` topic that reliably exposes per-client auth/ACL denials. A future integration should prefer a configured, least-privilege Mosquitto log source or plugin hook and continue to redact client secrets and payloads.
