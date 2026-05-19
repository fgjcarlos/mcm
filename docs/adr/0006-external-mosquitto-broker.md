# ADR-0006: Keep Mosquitto external to MCM for the MVP

## Status

Accepted

## Context

MCM is intended to improve the administration and observability experience around Eclipse Mosquitto. It should not become a broker replacement.

Embedding or managing the broker process directly would increase the scope of the MVP and create packaging, lifecycle, permissions, upgrade, and security responsibilities that are not necessary to prove the product value.

## Decision

For the MVP, Mosquitto remains an external broker process or container.

MCM will integrate with Mosquitto through documented configuration, MQTT connections, generated or managed auth/ACL artifacts where appropriate, diagnostics, and operational APIs/UI.

MCM may provide Docker Compose examples that start Mosquitto and MCM together, but MCM itself should not embed Mosquitto as an internal broker.

## Consequences

Positive:

- Clear product boundary: MCM is the control plane, Mosquitto is the broker.
- Existing Mosquitto deployments can adopt MCM incrementally.
- The MVP avoids broker lifecycle complexity.
- Users can keep their preferred Mosquitto packaging and versioning.
- Security responsibilities are easier to reason about.

Negative:

- MCM must handle differences between Mosquitto deployment styles.
- Some management actions may require file permissions, volume mounts, or explicit integration configuration.
- A fully turnkey experience may require Compose or installer templates.

## Alternatives considered

### Embed Mosquitto inside MCM

Rejected for the MVP. It would blur responsibilities and increase lifecycle/security complexity.

### Build a new MQTT broker

Rejected. That is explicitly outside MCM's product positioning.

### Manage multiple broker types from day one

Rejected. Supporting EMQX, HiveMQ, VerneMQ, and others would dilute the first product focus. Mosquitto is the initial target.
