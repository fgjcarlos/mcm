# ADR-0007: Use a YAML configuration file for server runtime configuration

## Status

Accepted

## Context

MCM needs configuration for HTTP server settings, database paths, Mosquitto connection details, logging, TLS, and runtime behavior.

The configuration format should be easy for operators to read and edit, suitable for Docker Compose examples, and friendly to industrial/edge deployments where configuration may be managed manually or through provisioning scripts.

The main `mcm` server currently loads configuration from YAML passed through `--config`. The separate `mcm-agent` process supports a small set of `MCM_AGENT_*` environment overrides, but that behavior is not implemented for the main server.

## Decision

Use a YAML configuration file as the runtime configuration source for the main `mcm` server, selected explicitly with `--config`.

The initial CLI should support:

- `mcm config init` to generate an example YAML config
- `mcm config validate` to validate syntax and required values
- Explicit config path selection through a flag such as `--config`

For container deployments, operators can still inject secrets through mounted files, generated YAML, or deployment templating, but those mechanisms happen outside the main server runtime loader.

## Consequences

Positive:

- YAML is readable for operators.
- Docker Compose examples can mount or generate a clear config file.
- The same configuration can support CLI diagnostics and server startup.

Negative:

- YAML parsing and validation must be strict enough to catch mistakes early.
- Secrets in config files remain a risk if operators choose that path.
- Deployments that prefer environment-variable-driven config must template or generate YAML before startup.

## Alternatives considered

### JSON configuration

Rejected for the MVP. JSON is strict and machine-friendly, but less pleasant for hand-edited operational config.

### TOML configuration

Viable but not chosen. TOML is readable, but YAML is more common in Docker/Kubernetes-adjacent operational workflows.

### Environment variables only

Rejected for the main server runtime. Environment-only configuration becomes hard to inspect and document as the number of settings grows.

### Database-only settings

Rejected. MCM needs boot-time settings before the database is available, especially for database location and Mosquitto connectivity.
