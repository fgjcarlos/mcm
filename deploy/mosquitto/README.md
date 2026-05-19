# Mosquitto development deployment

This directory contains the local Eclipse Mosquitto configuration used by the MCM development Docker Compose stack.

## Files

- `config/mosquitto.conf`: local-only Mosquitto configuration.

## Exposed ports

- `1883`: MQTT TCP listener.
- `9001`: MQTT over WebSocket listener.

## Data and logs

The Compose stack stores broker data and logs in named Docker volumes:

- `mosquitto_data`
- `mosquitto_logs`

Inspect them with:

```bash
docker volume ls | grep mcm
```

## Security note

The development configuration enables anonymous access so the stack works without setup friction.

Do not use this configuration in production. Production deployments must use authentication, ACLs, TLS where appropriate, and explicit listener hardening.
