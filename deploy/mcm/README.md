# Future MCM service shape

The initial Docker Compose stack starts Mosquitto only because the backend skeleton has not been created yet.

Once the Go backend exists, the Compose stack should add an `mcm` service with roughly this shape:

```yaml
mcm:
  build:
    context: .
    dockerfile: deploy/mcm/Dockerfile.dev
  depends_on:
    mosquitto:
      condition: service_healthy
  environment:
    MCM_MQTT_HOST: mosquitto
    MCM_MQTT_PORT: "1883"
    MCM_HTTP_ADDR: 0.0.0.0:8080
  ports:
    - "8080:8080"
```

The exact configuration keys should follow the config model introduced by the backend foundation issues.
