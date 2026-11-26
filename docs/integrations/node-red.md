# Node-RED Integration

This guide shows how to connect Node-RED to an MCM-managed Mosquitto broker. It includes example flows for publishing industrial telemetry and verifying ACL behavior.

To check a new MQTT user from any client, Node-RED included, finish
[the onboarding journey](../mqtt-onboarding.md#the-connection-test): its
last step shows the connection data of the chosen listener and a
concrete test topic, and MCM reports the connection and the operation it
observes on the broker. The flows below are longer examples for
Node-RED.

## Prerequisites

- A running Mosquitto broker managed by MCM (see [deploy/mosquitto](../../deploy/mosquitto/README.md)).
- Node-RED installed locally or running in Docker. See [nodered.org/docs/getting-started](https://nodered.org/docs/getting-started/).
- The core `mqtt in` / `mqtt out` nodes and the `mqtt-broker` config node (built into Node-RED).

## Broker connection settings

### Local Docker Compose (development)

The MCM development stack exposes Mosquitto on the host:

| Setting | Value |
|---------|-------|
| Broker host | `localhost` (or `mosquitto` from within Compose) |
| MQTT port | `1883` |
| WebSocket port | `9001` |
| Username | An MQTT user created in MCM (the dev broker rejects anonymous clients) |
| Password | That user's password |
| TLS | Disabled |

### Production / edge deployment

| Setting | Value |
|---------|-------|
| Broker host | Your Mosquitto hostname or IP |
| MQTT port | `8883` (TLS) |
| Username | MCM-managed MQTT user |
| Password | MCM-managed MQTT password |
| TLS | Enabled — see [Production TLS](../../deploy/mosquitto/README.md#production-tls-for-mcm-to-mosquitto) |

In Node-RED, open the MQTT broker node configuration and enable **Use TLS**. Provide the CA certificate file and, if mutual TLS is required, the client certificate and key files. These should match the certificates described in the [production TLS checklist](../../deploy/mosquitto/README.md#production-tls-checklist).

## Example flows

Import the example flows from [`node-red-flow-example.json`](./node-red-flow-example.json) into Node-RED:

1. Open the Node-RED editor.
2. Click the hamburger menu (top-right) and select **Import**.
3. Choose **select a file to import**, pick `node-red-flow-example.json` and click **Import**.
4. Open the imported `mcm-broker` config node, enter the MQTT user and password created in MCM (the example ships without credentials), then click **Deploy**.

The imported tab, **MCM Mosquitto Examples**, contains three flows.

### Flow 1: Publish JSON telemetry

Publishes a simulated sensor payload to `plant/area-1/sensor-01/telemetry` every 10 seconds:

```
[Every 10s] → [Build telemetry payload] → [Publish to Mosquitto]
```

The payload follows a typical industrial telemetry shape:

```json
{
  "device_id": "sensor-01",
  "temperature": 28.41,
  "humidity": 63.2,
  "pressure": 1018.7,
  "timestamp": "2026-05-23T12:00:00.000Z"
}
```

Customize the topic, device ID, and fields in the **Build telemetry payload** function node to match your deployment.

### Flow 2: Subscribe and verify

Subscribes to `plant/#` and logs every received message to the debug sidebar:

```
[Subscribe plant/#] → [Log received message]
```

After deploying both flows, the debug panel should show telemetry arriving every 10 seconds. This confirms the broker accepts connections and the MQTT user has read access to the `plant/#` topic tree.

### Flow 3: ACL denial test

Attempts to publish to `$SYS/broker/version`, a system topic that a properly configured ACL should deny to normal users:

```
[Test ACL denial] → [Publish to $SYS (denied)]
```

Click the inject button manually. When ACLs are enforced:

- The message is silently dropped by Mosquitto (MQTT does not send publish errors to the client).
- Check the Mosquitto log for a denial entry. With the development stack: `docker compose logs mosquitto | grep -i denied`.
- The MCM dashboard (when available) shows the denial in the audit trail.

The test is meaningful only after MCM has applied an ACL configuration that does not grant the user write access to that topic.

## Configuring credentials

Never commit real credentials into Node-RED flows. Use one of these approaches:

**Environment variables** (recommended for Docker):

Set broker credentials as environment variables and reference them in the MQTT broker node:

```
NODERED_MQTT_HOST=mosquitto.example.internal
NODERED_MQTT_PORT=8883
NODERED_MQTT_USER=nodered-client
NODERED_MQTT_PASS=changeme
```

In the Node-RED MQTT broker node, use `${NODERED_MQTT_HOST}` syntax in the server field. These are Node-RED variables, not MCM settings.

**Node-RED credentials file**:

Node-RED encrypts credentials in `flows_cred.json` automatically. Enter the username and password directly in the broker node configuration and ensure `flows_cred.json` is excluded from version control.

## Topic naming conventions

MCM validates MQTT payloads against JSON Schemas bound to topic filters (see [docs/json-schemas.md](../json-schemas.md)). When publishing from Node-RED, use topic names that match the configured filters so payloads are validated by the broker pipeline.

A common industrial pattern:

```
{site}/{area}/{device-id}/{data-type}
```

Examples: `plant/area-1/sensor-01/telemetry`, `plant/area-1/plc-03/status`.

## Troubleshooting

| Symptom | Check |
|---------|-------|
| Node-RED shows "connection refused" | Verify Mosquitto is running and the host/port are correct. For Docker Compose, ensure Node-RED can reach the Mosquitto container (use the service name `mosquitto` if both are in the same Compose network). |
| "Not authorized" on connect | Check the MQTT username and password in the broker node. Verify the user exists in MCM. |
| Messages published but not received | Check topic spelling and wildcards. Verify ACL grants read access on the subscribed topic for the connecting user. |
| TLS handshake failure | Verify the CA certificate matches the broker's server certificate. Check certificate expiry. See [MQTT readiness diagnostics](../../deploy/mosquitto/README.md#mqtt-readiness-diagnostics) for how MCM reports the broker connection; `GET /readyz` does not run a fresh MQTT probe. |
| ACL denial not visible | Check Mosquitto log level includes `log_type notice`. Mosquitto silently drops denied publishes without notifying the client. |
