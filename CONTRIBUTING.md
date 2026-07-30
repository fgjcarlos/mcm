# Contributing to MCM

Thanks for your interest in MCM. The project is public and feedback is welcome, but it is currently maintainer-led. The maintainer decides what fits the roadmap and when changes are merged.

## Governance

- Project direction, roadmap prioritization, and final merge decisions belong to @fgjcarlos.
- Issues and pull requests that do not align with the roadmap may be closed, even if the idea is valid in general.
- Please keep discussions practical, respectful, and focused on MCM's goal: a lightweight control plane for Eclipse Mosquitto.

## Before opening an issue

Use issues for actionable work:

- Bug reports with reproduction steps.
- Focused feature proposals.
- Documentation improvements.
- Small implementation tasks from the roadmap.

Please do not use issues for broad support requests, vague ideas, or unrelated MQTT/Mosquitto troubleshooting. Low-context issues may be closed.

## Before opening a pull request

For non-trivial changes, open an issue first and wait for maintainer feedback before investing time.

Pull requests should:

- Target the `main` branch.
- Be small and focused on one topic.
- Reference an issue whenever possible.
- Include tests when behavior changes.
- Update documentation when user-facing behavior changes.
- Avoid unrelated formatting or refactoring.
- Avoid committing secrets, real credentials, private broker URLs, or local machine paths.

## Development workflow

1. Fork the repository.
2. Create a branch from `main`.
3. Make a focused change.
4. Run the relevant checks locally.
5. Open a pull request using the PR template.

For the current Go backend, run:

```bash
make test
```

`make test` and `make build` generate `frontend/dist` before invoking the Go toolchain, which keeps the embedded frontend contract explicit in local development and CI. If you use `go test`, `go build`, or `go run` directly, run `npm --prefix frontend run build` first.

### Windows notes

On Windows, `go test -race` requires GCC. If you are using TDM-GCC, the test may fail to link. Use MinGW-w64 (via msys2) for `-race` support on Windows.

## Review and merge policy

- All changes to `main` go through pull requests.
- Code owner review is required before merging.
- Stale approvals are dismissed after new commits.
- Conversations must be resolved before merge.
- The maintainer may request changes, re-scope a PR, or close it if it does not fit the roadmap.

## Security

Do not report security issues in public issues. See `SECURITY.md`.
