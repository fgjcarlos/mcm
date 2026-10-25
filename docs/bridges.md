# Bridges API

MCM manages the Mosquitto bridge connections that link the managed broker
to remote brokers. A bridge states **which remote broker** to reach,
**which topics** travel over it and **in which direction**, and it renders
one-for-one into the connection sections of `mosquitto.conf`.

This page covers the bridge model, the rendered Mosquitto syntax, the
validation rules and the review-and-activation flow. The deployment of a
rendered file is described in the [production guide](production.md).

## Bridge model

| Field | Meaning |
|-------|---------|
| `name` | Connection name. Unique in the file. |
| `address` | Address of the remote broker, `host` or `host:port`. The port defaults to `1883`. |
| `remote_client_id` | Client id the bridge uses on the remote broker. |
| `local_client_id` | Client id the bridge uses on the local broker. |
| `username` | User name the bridge authenticates with on the remote broker. |
| `password` | Password the bridge authenticates with on the remote broker. **Write-only.** |
| `local_username` | User name the bridge authenticates with on the local broker. |
| `local_password` | Password the bridge authenticates with on the local broker. **Write-only.** |
| `protocol_version` | MQTT version spoken with the remote broker: `mqttv31`, `mqttv311` or `mqttv50`. |
| `clean_session` | Clean session flag of the remote connection. |
| `local_clean_session` | Clean session flag of the local side of the bridge. |
| `keepalive_seconds` | Keepalive of the remote connection, 5 to 65535 seconds. |
| `restart_timeout` | Reconnection backoff: a number of seconds, or a base and cap pair such as `5 30`. |
| `notifications` | Publishes the state of the connection on the notification topic. |
| `notifications_local_only` | Keeps the notifications inside the local broker. |
| `notification_topic` | Topic the connection state is published on. |
| `try_private` | Uses the private flag of the forwarded messages. |
| `start_type` | When the connection is established: `automatic`, `lazy` or `once`. |
| `threshold` | Number of queued messages that triggers a reconnection. |
| `idle_timeout_seconds` | Closes an idle connection. |
| `round_robin` | Spreads the outgoing messages over the addresses of the bridge. |
| `tls` | Transport security of the connection — see below. |
| `topics` | The topic rules of the connection — see below. |

Every field that is not set is omitted from the rendered file, and
Mosquitto applies its own default.

### TLS options

| Field | Rendered as | Meaning |
|-------|-------------|---------|
| `ca_file` | `bridge_cafile` | CA certificate file used to verify the remote broker. |
| `ca_path` | `bridge_capath` | Directory of CA certificates. |
| `cert_file` | `bridge_certfile` | Client certificate presented to the remote broker. |
| `key_file` | `bridge_keyfile` | Private key of the client certificate. |
| `insecure` | `bridge_insecure` | Skips the verification of the remote certificate. |
| `tls_version` | `bridge_tls_version` | `tlsv1.2` or `tlsv1.3`. |
| `alpn` | `bridge_alpn` | Application protocol offered during the handshake. |
| `psk` | `bridge_psk` | Pre-shared key used instead of a certificate. **Write-only.** |
| `identity` | `bridge_identity` | Identity of the pre-shared key. |
| `require_ocsp` | `bridge_require_ocsp` | Requires a stapled OCSP response. |
| `outgoing_retain` | `bridge_outgoing_retain` | Keeps the retain flag of the forwarded messages. |
| `max_packet_size` | `bridge_max_packet_size` | Largest message sent to the remote broker. `0` means no limit. |

### Topic rules

| Field | Meaning |
|-------|---------|
| `topic` | MQTT topic pattern the bridge forwards. |
| `direction` | `in` (remote to local), `out` (local to remote) or `both`. Defaults to `both`. |
| `qos` | Quality of service the rule forwards with: `0`, `1` or `2`. |
| `local_prefix` | Prefix added to the topic on the local broker. |
| `remote_prefix` | Prefix added to the topic on the remote broker. |

A bridge needs at least one topic rule: Mosquitto refuses a connection
without topics.

## Rendered Mosquitto syntax

MCM renders the Mosquitto 2.0 connection form. Mosquitto 2.0 does not
accept the older `bridge { … }` block form, and MCM refuses such a file
instead of rewriting it.

```mosquitto
connection upstream
address remote.example.org:1884
remote_clientid mcm-upstream
remote_username mcm
remote_password ********
bridge_protocol_version mqttv311
cleansession false
keepalive_interval 60
restart_timeout 5 30
try_private true
start_type automatic
threshold 10
idle_timeout 60
bridge_cafile /etc/mosquitto/ca.pem
bridge_insecure false
bridge_tls_version tlsv1.3
topic both/# both 1 local/ remote/
topic sensors/+ in 0
topic commands/# out 2 localcmd/ remotecmd/
```

The `topic` line follows the grammar Mosquitto accepts:

```text
topic <pattern> [<direction> <qos> [<local-prefix> [<remote-prefix>]]]
```

`direction` is one of `in`, `out` or `both`. A line with only a pattern
bridges the topic in both directions.

The file keeps every part MCM does not own: comments, blank lines, global
directives, listener blocks and the include order. Only the `connection`
lines and the connection-scoped directives that belong to them are
rewritten, and they are written back at the position of the first
connection they replace.

MCM manages the bridges of the managed `mosquitto.conf`. A bridge that
lives in an included file belongs to that file and is outside the surface
MCM owns.

## Validation

The preview refuses a document with an error. A warning is advisory and
never blocks the change.

| Kind | Severity | Raised when |
|------|----------|-------------|
| `name` | error | The name is missing, longer than 64 characters, or holds a character outside letters, digits, dots, dashes and underscores. |
| `address` | error | The address is missing, carries a scheme or a path, names an invalid host, or carries a port outside 1 to 65535. |
| `duplicate` | error | Two bridges share a name, a remote client id, a local client id, or a bridge declares the same topic rule twice. |
| `dependency` | error | A bridge has no topic rule, a client certificate has no private key (or the other way round), a pre-shared key has no identity, or a notification topic is set while the notifications are disabled. |
| `loop` | error | The configuration would feed the broker its own traffic — see below. |
| `value` | error | A topic pattern, a direction, a quality of service, a protocol version, a start type, a TLS version or a numeric range is invalid. |
| `secret` | warning | A user name has no password, a password has no user name, or the bridge does not verify the certificate of the remote broker. |

### Loop detection

A bridge can put the broker into a feedback loop. MCM refuses three
shapes:

1. **The bridge connects the broker to itself.** A bridge whose address
   matches one of the listeners of the managed broker reaches the same
   broker, and every message the broker sends comes back to it. A
   listener bound to `0.0.0.0` matches every address of the host.
2. **Two rules of the same bridge bounce the same traffic.** An outbound
   rule and an inbound rule that carry the same traffic on the remote
   side and on the local side: the bridge forwards its own message out
   and receives it back in. Use a single `both` rule instead of an `out`
   rule and an `in` rule for the same topics.
3. **Two bridges share a remote broker and overlap.** The traffic the
   first bridge sends to the remote broker comes back through the second
   one and is forwarded again.

The `try_private` option of Mosquitto suppresses the echo of the
messages a bridge receives from the remote broker. It is enabled by
default in the editor.

## Secrets

A bridge password is a secret and the API never returns it:

- `GET /api/v1/broker/bridges` reports `password_set`, `local_password_set`
  and `psk_set` instead of the values.
- The diff and the rendered body of a preview are redacted: a credential
  never travels through a response, a log line or an audit record.
- A password the client does not send keeps the value it has on disk, so
  an edit that does not touch a credential does not clear it.
- The file on disk and the stored revision keep the real value, because
  Mosquitto reads the password from the file.

## Review and activation

The surface follows the same flow as the rest of the configuration
management: the desired state, the reviewed revision, the written file
and the verified active state stay distinct.

| Step | Endpoint | Role |
|------|----------|------|
| Read the bridges | `GET /api/v1/broker/bridges` | auditor |
| Preview the edited bridges | `POST /api/v1/broker/bridges/preview` | operator |
| Apply the reviewed revision | `POST /api/v1/broker/bridges/apply` | admin |

The preview body is the complete set of bridges the operator wants:

```json
{
  "bridges": [
    {
      "name": "upstream",
      "address": "remote.example.org:1884",
      "remote_client_id": "mcm-upstream",
      "username": "mcm",
      "password": "the-secret",
      "try_private": true,
      "keepalive_seconds": 60,
      "restart_timeout": "5 30",
      "tls": { "ca_file": "/etc/mosquitto/ca.pem" },
      "topics": [
        { "topic": "both/#", "direction": "both", "qos": 1 }
      ]
    }
  ]
}
```

The response carries the revision id, the diff, the rendered file, the
directives that changed and whether the change needs a restart.

**A bridge is only read by Mosquitto at start-up.** A change therefore
needs a broker restart, and the preview reports `requires_restart`. The
apply refuses the revision with `409` unless the operator confirms the
restart.

The activation follows the deployment adapter:

- When the adapter can restart the broker — the Docker adapter restarts the
  container, the file adapter runs the restart command the operator
  configured — the broker is restarted and the deployment ends in
  `active_verified`.
- When the adapter cannot restart the broker, the change is still written
  and the deployment ends in `pending_activation`: the bridge is in the
  file, but the broker still runs the previous connection set and needs a
  restart. The UI says so instead of claiming a verified activation.

## Tests against a real broker

A bridge is a connection between two brokers, so the behaviour is proven
against two real Mosquitto 2.0 instances:

```bash
bash scripts/tests/test-e2e-bridges.sh
```

The test brings up a remote broker and a bridged broker whose
configuration is rendered by MCM, and asserts that

- the rendered configuration loads in the broker,
- the traffic flows from the local broker to the remote one,
- the traffic flows from the remote broker back to the local one,
- the bridge reconnects and the traffic flows again after the remote
  broker was restarted.

The test uses the `mosquitto` binary when `MOSQUITTO_BIN` points at one,
and the `eclipse-mosquitto` container image otherwise.
