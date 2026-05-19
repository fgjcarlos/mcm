# ADR-0007: Use a YAML configuration file with environment overrides

## Status

Accepted

## Context

MCM needs configuration for HTTP server settings, database paths, Mosquitto connection details, logging, TLS, and runtime behavior.

The configuration format should be easy for operators to read and edit, suitable for Docker Compose examples, and friendly to industrial/edge deployments where configuration may be managed manually or through provisioning scripts.

Secrets such as Mosquitto passwords should not be forced into plain text files when environment variables or secret mounts are available.

## Decision

Use a YAML configuration file as the primary configuration format, with environment variable overrides for deployment-specific values and secrets.

The initial CLI should support:

- `mcm config init` to generate an example YAML config
- `mcm config validate` to validate syntax and required values
- Explicit config path selection through a flag such as `--config`
- Environment overrides using a documented prefix such as `MCM_`

Sensitive values should be documented with environment-variable alternatives.

## Consequences

Positive:

- YAML is readable for operators.
- Docker Compose examples can mount or generate a clear config file.
- Environment overrides work well for containers and secret management.
- The same configuration can support CLI diagnostics and server startup.

Negative:

- YAML parsing and validation must be strict enough to catch mistakes early.
- Environment override precedence must be clearly documented.
- Secrets in config files remain a risk if operators choose that path.

## Alternatives considered

### JSON configuration

Rejected for the MVP. JSON is strict and machine-friendly, but less pleasant for hand-edited operational config.

### TOML configuration

Viable but not chosen. TOML is readable, but YAML is more common in Docker/Kubernetes-adjacent operational workflows.

### Environment variables only

Rejected. Environment-only configuration becomes hard to inspect and document as the number of settings grows.

### Database-only settings

Rejected. MCM needs boot-time settings before the database is available, especially for database location and Mosquitto connectivity.
