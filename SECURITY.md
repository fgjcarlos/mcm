# Security Policy

MCM is an open source control plane for Eclipse Mosquitto. We take reports
of security vulnerabilities seriously and will work with reporters on
coordinated disclosure.

> MCM is pre-1.0 software. The security model is still maturing; please
> treat releases within the latest minor as the supported line and plan
> for breaking changes between minor versions. The current project
> status is documented in the top-level [README](./README.md#project-status).

---

## Reporting a vulnerability

**Do not open a public GitHub issue for security vulnerabilities.** Public
issue trackers are indexed by search engines and aggregated by bots;
sensitive details reach attackers before a fix is ready.

Use the repository's private vulnerability reporting channel:

- **GitHub Security Advisories (private)**: <https://github.com/fgjcarlos/mcm/security/advisories/new>

If the private channel is not yet enabled on this repository, it can be
turned on by a maintainer at
`https://github.com/fgjcarlos/mcm/settings/security_analysis` → **Private
vulnerability reporting**. Until that is enabled, email the maintainer via
the address listed on their GitHub profile
(<https://github.com/fgjcarlos>).

### What to include

A useful report contains:

- A short description of the issue and its impact.
- Affected version, commit SHA, or release tag (and deployment mode:
  standalone binary, Docker Compose, etc.).
- Reproduction steps or a proof of concept, redacted if sharing it would
  expose other users.
- Environment details that affect exploitability (Mosquitto version, auth
  mode, whether `http.tls.enabled` is on, reverse proxy in front).
- Your suggested disclosure timeline, if you have one.

### What to expect

This project is maintained by a single contributor in their spare time,
and the response SLA reflects that:

| Stage                                    | Target                                       |
| ---------------------------------------- | -------------------------------------------- |
| Initial acknowledgement                  | Best-effort within **7 days** of report      |
| Triage and severity assessment            | Within **14 days** of acknowledgement        |
| Patch release for critical / high issues | As soon as a fix is ready and tested         |
| Patch release for medium / low issues    | Rolled into the next regular release         |
| Public disclosure (CVE request, advisory)| Coordinated with the reporter, default 90 days |

If you have not received an acknowledgement within 7 days, please send a
polite follow-up through the same channel. The maintainer may be on
vacation, between jobs, or dealing with personal matters; assume good
faith and resend once before escalating.

We follow [coordinated disclosure](https://en.wikipedia.org/wiki/Coordinated_vulnerability_disclosure):
please give us a reasonable window to ship a fix before publishing
details. We are happy to credit reporters in the release notes and CVE
record unless you prefer to remain anonymous.

---

## Supported versions

MCM is currently pre-1.0 and has not yet shipped a tagged release. The
policy below applies from the first tagged release onward and will be
updated as the project stabilises.

| Version line            | Support status                  | Security fixes         |
| ----------------------- | ------------------------------- | ---------------------- |
| Latest minor release    | **Active support**              | Yes                    |
| Previous minor release  | **Critical / high fixes only**  | Yes, until the next minor is out for 30 days |
| Older minor releases    | **End of life**                 | No — please upgrade    |

Concretely:

- **Critical and high-severity vulnerabilities** are fixed in the latest
  minor and backported to the previous minor for a 30-day overlap window.
- **Medium and low-severity issues** are fixed in the latest minor only.
- **The `main` branch** may contain unreleased fixes; production
  deployments should pin a tagged release, not a branch.

The first tagged release will be announced in
[docs/releasing.md](./docs/releasing.md); this table will be updated at
that point with concrete version numbers.

---

## Scope

The following areas are considered security-sensitive and are in scope
for reports:

- Authentication, session, and JWT handling.
- Admin user management and role-based access control.
- ACL generation, validation, and persistence.
- Mosquitto configuration writes and password file handling.
- File permissions, secret storage, and TLS material handling.
- HTTP request handling, including CORS, CSP, security headers, and
  body-size limits.
- WebSocket and broker event streaming.
- Container and deployment defaults shipped in
  [`deploy/mcm/`](./deploy/mcm).
- Webhook delivery and signing.
- Audit and security event logging (and the integrity of those trails).

The following are **out of scope**:

- Issues in upstream dependencies that do not affect MCM (please report
  upstream; we are happy to coordinate).
- Denial-of-service attacks that require already-authenticated admin
  access.
- Rate-limit / lockout bypasses for unauthenticated traffic that
  require sustained malicious load (the production deployment guide
  recommends an upstream WAF for that profile).
- Theoretical issues without a concrete impact on a default
  configuration.

---

## Hardening guidance for operators

The production deployment guide covers TLS termination, secrets,
firewalling, and a hardening checklist; see
[`docs/production.md`](./docs/production.md).

The HTTP API and security headers baseline is documented in the
top-level [README](./README.md#security-baseline).

---

## Recognition

We are grateful to the security community. Reporters who follow the
process above and disclose responsibly are credited in the release notes
and any associated GitHub Security Advisory, unless they ask to remain
anonymous.

---

## Acknowledgement

This policy is based on the GitHub Security Lab's recommended
disclosure process and the
[OpenSSF Security Policy guide](https://github.com/ossf/security-policy).
Suggestions for improvement are welcome via a regular pull request to
this file.
