# Security Policy

MCM is early-stage software. Please avoid using it as the only control layer for production Mosquitto deployments until the security model has matured.

## Reporting a vulnerability

Please do not open a public GitHub issue for security vulnerabilities.

Instead, contact the maintainer privately with:

- A short description of the issue.
- Reproduction steps or proof of concept, if safe to share.
- Affected version, commit, or deployment mode.
- Potential impact.

If no private contact channel is listed in the repository profile, open a minimal public issue asking for a private security contact without disclosing technical details.

## Scope

Security-sensitive areas include:

- Authentication and session handling.
- Admin user management.
- ACL generation and validation.
- Mosquitto configuration writes.
- File permissions and secret handling.
- Container and deployment defaults.

## Expectations

The maintainer will triage reports as availability allows. Coordinated disclosure is appreciated.
