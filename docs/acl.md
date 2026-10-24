# ACL API

MCM manages Mosquitto 2.0 ACL rules. A rule states **who** may act on
**which topics** with **which permission**, and it renders one-for-one
into a Mosquitto ACL file.

This page covers the rule model, the native scopes, the validation rules
and the Mosquitto mapping. The deployment of a rendered file is described
in the [production guide](production.md).

## Rule model

| Field | Meaning |
|-------|---------|
| `id` | MCM-generated rule identifier. |
| `selector` | Scope of the rule: `user`, `anonymous` or `pattern`. |
| `principal` | The MQTT username. Required for `user` only. |
| `topic_filter` | An MQTT topic filter, with wildcards `+` and `#`. |
| `permission` | `read`, `write`, `readwrite` or `deny`. |
| `description` | Optional operator-facing note. |

A request without `selector` means `user`. A request with `principal`
but without `selector` is a legacy user-scoped request.

## Scopes

Mosquitto ACL files distinguish three scopes. MCM models them with
`selector`:

| Selector | Applies to | Rendered as | Principal |
|----------|------------|-------------|-----------|
| `user` | The named MQTT user. | `topic` in a `user` section. | Required |
| `anonymous` | Unauthenticated only. | `topic` before first `user`. | Empty |
| `pattern` | Every user in the ACL. | `pattern` line. | Empty |

Two rules matter when reading a rendered file:

- An anonymous rule is **not** a fallback for authenticated users. In
  Mosquitto a `topic` line before the first `user` line applies to
  anonymous clients only.
- A pattern rule is **globally applicable**. Mosquitto evaluates
  `pattern` lines for every user, whatever `user` section surrounds
  them.

### Backward compatibility

- A request without `selector` keeps the legacy `user` scope, so existing
  clients and stored rules keep their meaning.
- A stored rule created before native selectors existed reads back with
  `selector: "user"`.
- A malformed legacy request (no principal, no selector) is **rejected**
  with `principal is required`. It never silently becomes an anonymous
  rule. Only an explicit `"selector": "anonymous"` selects anonymous
  semantics.

## Permissions

| Permission | Meaning | Broker requirement |
|------------|---------|--------------------|
| `read` | Subscribe to the topic. | Mosquitto 1.x and later |
| `write` | Publish to the topic. | Mosquitto 1.x and later |
| `readwrite` | Subscribe and publish. | Mosquitto 1.x and later |
| `deny` | Deny the action. | Mosquitto 2.0 or later |

MCM supports Mosquitto **2.0**. The `deny` permission is validated
against that version, so a rule that needs a newer broker is rejected at
creation time instead of failing silently at runtime. MCM does not
discover the running broker version; the supported version is a
compile-time constant (`acl.SupportedMosquittoVersion`).

## Placeholders

Mosquitto substitutes two placeholders when it evaluates a pattern rule:

| Placeholder | Substituted with |
|-------------|------------------|
| `%u` | The username of the connecting client. |
| `%c` | The client id of the connecting client. |

Each placeholder must occupy an entire topic level:

- Valid: `clients/%u/state`, `clients/%c/state`
- Invalid: `clients/%uX/state`, `clients/%u%c/state`

The whole-level rule applies to **pattern rules only**. A `user` or
`anonymous` topic filter keeps a literal `%u` or `%c`: MCM neither
rewrites nor rejects it, because Mosquitto only substitutes placeholders
for pattern entries.

## REST endpoints

- `GET /api/v1/acls`: list all ACL rules.
- `POST /api/v1/acls`: create a rule.
- `PUT /api/v1/acls/{id}`: update a rule.
- `DELETE /api/v1/acls/{id}`: delete a rule.

Role requirements are unchanged: `auditor` may list, `operator` may
create, update and delete.

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
  "selector": "user",
  "principal": "operator",
  "topic_filter": "factory/+/temperature",
  "permission": "read",
  "description": "Read all line temperature telemetry"
}
```

Example anonymous rule:

```json
{
  "selector": "anonymous",
  "topic_filter": "public/status",
  "permission": "read"
}
```

Example pattern rule:

```json
{
  "selector": "pattern",
  "topic_filter": "clients/%u/state",
  "permission": "readwrite"
}
```

## Validation

MCM rejects a rule with HTTP `400` and a top-level `error` plus detailed
messages under `details` when:

- `principal` is missing for a `user` rule, or set on an `anonymous` or
  `pattern` rule.
- `selector` is not one of `user`, `anonymous`, `pattern`.
- `permission` is not one of `read`, `write`, `readwrite`, `deny`, or
  `deny` is used with a broker older than 2.0.
- The topic filter breaks the MQTT wildcard rules.
- A pattern topic filter breaks the whole-level placeholder rule.

### Topic-filter validation

- `#` must occupy an entire topic level.
- `#` may only appear in the final topic level.
- `+` must occupy an entire topic level.

Examples:

- Valid: `factory/#`
- Valid: `factory/+/temperature`
- Invalid: `factory/#/temperature`
- Invalid: `factory/area+1/temperature`

### Injection safety

A rule can never introduce a new ACL directive. The principal, the topic
filter and the permission are validated before storage, and a rule whose
scope is not one of the three known selectors is rejected outright, so no
stored value can terminate a line or start a new one.

Concretely:

- Control characters (including newline) are rejected in the principal
  and in the topic filter, so a stored value can never end its ACL line
  and open a new directive.
- `selector` and `permission` are allowlists, so they cannot carry a
  directive or a permission name that Mosquitto would read as something
  else.
- The description is operator-facing metadata and is never rendered into
  the ACL file.

Limitation: the principal is an MQTT username and is rendered verbatim.
MCM does not reject a username that contains spaces, because MQTT
usernames may contain them; Mosquitto's own ACL parser decides what to
do with such a line. Treat unusual usernames as a broker-configuration
concern rather than an ACL-syntax guarantee.

## Mosquitto ACL mapping

MCM renders the rules of one ACL file in a fixed order:

1. The **anonymous** section (`topic` lines) comes first.
2. The **pattern** section (`pattern` lines) follows.
3. The **user** sections come last, grouped by principal in
   lexicographic order.

Given these three rules:

```text
user operator / topic read factory/+/temperature
anonymous / topic read public/status
pattern / topic deny clients/%u/state
```

Mosquitto ACL output:

```text
topic read public/status

pattern deny clients/%u/state

user operator
topic read factory/+/temperature
```

## Deploy behavior

- The managed deploy renders every rule kind: anonymous, pattern, user
  and `deny`, plus the service-generated grants for the MCM broker
  account.
- Orphan detection and the username-rename cascade only apply to
  user-scoped rules. An anonymous or pattern record is not an MQTT
  username, so it never becomes an orphan and is never rewritten by a
  rename.

### Activation probe

After the files are written and the broker is signalled, MCM runs a
bounded activation probe against the live broker. The probe publishes and
subscribes as one identity on one concrete topic. The identity and the
topic are chosen in this order:

1. A **user-scoped `readwrite` grant**. The probe identity is the
   granted user.
2. A **`pattern` `readwrite` grant**. The probe identity is substituted
   into a whole-level `%u` placeholder, so the broker expands the same
   topic the rule describes.
3. The **managed service account** and the grant MCM always emits for
   it (`readwrite mcm/healthcheck`).

The third option is an operational fallback. It proves that the broker
serves the new credentials and the service grant; it does **not** prove
the client policy semantics.

Rules for the probe topic:

- The probe publishes to a concrete topic, never to a filter. A wildcard
  level is replaced by a fixed probe segment.
- A `%c` placeholder is skipped: the verifier does not control the MQTT
  client id, so the placeholder cannot be expanded.
- A grant is only usable when its identity has a known cleartext
  password. MCM never invents credentials.

The deployment status names the probe, for example
`activation probe: pattern grant`.

If no probe is available at all (no usable grant and no configured
service account), the verification stage fails, the deployment is rolled
back and the deployment record names the missing probe.

### What the activation probe does not prove

- The negative stage of the verifier authenticates an **unknown user**
  and expects the broker to reject it. It does not assert that a topic
  is denied.
- A broker that accepts every subscription therefore still passes.
- Deny rules, pattern rules and the anonymous section are not evaluated
  against a running broker by MCM.

## Security event visibility

MCM records sanitized security events for ACL API changes and ACL API
failures. These events include the timestamp, event category/reason,
source IP when available, HTTP method, and path. They do not include
passwords, JWTs, request bodies, or other secrets.

Recent events are available to authenticated admins through
`GET /api/v1/security/events?limit=100`, which returns newest events
first.

Mosquitto-side authentication and ACL denials are not fully visible in
this first slice. The embedded status/event stream currently observes
MCM broker connectivity, topic traffic, and MCM-ingested broker log
events; it does not parse Mosquitto log files nor subscribe to a broker
`$SYS` topic that reliably exposes per-client auth/ACL denials. A future
integration should prefer a configured, least-privilege Mosquitto log
source or plugin hook and continue to redact client secrets and
payloads.

## Limits

The unit tests in this repository cover the stored model, the rendered
file and the HTTP contract. They do **not** prove the runtime behaviour
of a Mosquitto broker: the effective permission table of a real broker
(in particular the interaction between `pattern` lines, `deny` lines and
the anonymous section) is verified in a later task against a running
Mosquitto.
