# Releasing MCM

MCM uses [GoReleaser](https://goreleaser.com/) to build multi-architecture binaries and container images.

## Supported architectures

| OS | Arch | `mcm` binary | `mcm-agent` binary | Container |
|----|------|--------------|--------------------|-----------|
| Linux | amd64 | Yes | Yes | Yes |
| Linux | arm64 | Yes | Yes | Yes |
| macOS | arm64 | Yes | Yes | No |
| Windows | amd64 | Yes | Yes (no `systemd` example) | No |

## Creating a release

1. Ensure `main` is in a releasable state (CI green, all intended PRs merged).
2. Tag the release:

```bash
git tag v0.1.0
git push origin v0.1.0
```

3. The `release.yml` workflow runs automatically on tag push:
   - Builds the frontend (`npm ci && npm run build`).
   - Builds Go binaries for all platforms via GoReleaser.
   - Publishes multi-arch container images to `ghcr.io/fgjcarlos/mcm`.
   - Creates a GitHub Release with binaries, checksums, and changelog.

## Artifacts

Each release publishes:

- `mcm_<version>_linux_amd64.tar.gz`
- `mcm_<version>_linux_arm64.tar.gz`
- `mcm_<version>_darwin_arm64.tar.gz`
- `mcm_<version>_windows_amd64.zip`
- `mcm-agent_<version>_linux_amd64.tar.gz`
- `mcm-agent_<version>_linux_arm64.tar.gz`
- `mcm-agent_<version>_darwin_arm64.tar.gz`
- `mcm-agent_<version>_windows_amd64.zip`
- `checksums.txt` (SHA-256)

## Container images

Multi-arch images are published to GitHub Container Registry:

```bash
docker pull ghcr.io/fgjcarlos/mcm:latest
docker pull ghcr.io/fgjcarlos/mcm:0.1.0
```

The `latest` and version tags are multi-arch manifests supporting `linux/amd64` and `linux/arm64`.

## Selecting the right artifact

| Deployment target | Artifact |
|-------------------|----------|
| x86 server or VM (MCM server) | `mcm_linux_amd64` binary or `ghcr.io/fgjcarlos/mcm:latest` (amd64) |
| Raspberry Pi 4/5, ARM server (MCM server) | `mcm_linux_arm64` binary or `ghcr.io/fgjcarlos/mcm:latest` (arm64) |
| macOS (Apple Silicon) — server | `mcm_darwin_arm64` binary |
| Windows server | `mcm_windows_amd64` zip |
| x86 edge device running the agent | `mcm-agent_linux_amd64` binary |
| ARM edge device running the agent | `mcm-agent_linux_arm64` binary |
| macOS (Apple Silicon) — agent | `mcm-agent_darwin_arm64` binary |
| Windows edge device running the agent | `mcm-agent_windows_amd64` zip |

For Docker deployments, `docker pull` automatically selects the correct architecture.

## Verifying checksums

```bash
sha256sum -c checksums.txt
```

## Verifying release signatures

All releases are signed with [cosign](https://docs.sigstore.dev/cosign/overview/) using **keyless signing** (Sigstore OIDC). No long-lived key material is stored.

### Verify the checksum file

```bash
cosign verify-blob \
  --certificate checksums.txt-keyless.pem \
  --signature checksums.txt.sig \
  --certificate-identity-regexp "https://github.com/fgjcarlos/mcm/.github/workflows/release.yml" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

### Verify the container image

```bash
cosign verify \
  --certificate-identity-regexp "https://github.com/fgjcarlos/mcm/.github/workflows/release.yml" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/fgjcarlos/mcm:0.1.0
```

### Inspect the SBOM

Each release archive ships with a companion SPDX SBOM (e.g. `mcm_0.1.0_linux_amd64.tar.gz.spdx.json`).
To inspect it:

```bash
# Using syft
syft convert mcm_0.1.0_linux_amd64.tar.gz.spdx.json -o table

# Using grype for vulnerability scanning against the SBOM
grype sbom:mcm_0.1.0_linux_amd64.tar.gz.spdx.json
```

## Local dry run

To test the release pipeline locally without publishing:

```bash
goreleaser release --snapshot --clean
```

This builds all artifacts in `dist/` without pushing to GitHub or GHCR.

## CI validation

The `goreleaser check` step runs on every PR to validate `.goreleaser.yaml` syntax. No secrets are required for PR validation.
