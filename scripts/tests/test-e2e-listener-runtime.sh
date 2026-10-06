#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
DOCKERFILE="$ROOT/Dockerfile"
WORKFLOW="$ROOT/.github/workflows/ci.yml"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

fail() {
    printf 'FAIL: %s\n' "$1" >&2
    exit 1
}

pass() {
    printf 'PASS: %s\n' "$1"
}

# The Compose plugin belongs only in the dev stage; prod stays minimal.
awk '/^FROM prod AS dev$/{dev=1} dev && /docker-cli-compose/{found=1} END{if (!found) exit 1}' "$DOCKERFILE" \
    || fail 'dev stage does not install docker-cli-compose with docker-cli'
if awk '/^FROM alpine:3.21 AS prod$/{prod=1} /^FROM prod AS dev$/{exit} prod && /docker-cli-compose/{found=1} END{exit !found}' "$DOCKERFILE"; then
    fail 'production stage includes the Compose plugin'
fi
pass 'Compose plugin is dev-only'

# The listener job must load the image built by the image job, not rebuild it.
listener_job="$(awk '/^  e2e-listeners:/{job=1} job{print}' "$WORKFLOW")"
[[ "$listener_job" == *'needs: image'* ]] || fail 'listener job no longer depends on image'
[[ "$listener_job" == *'name: mcm-dev-image'* ]] || fail 'listener job does not download the shared image artifact'
[[ "$listener_job" == *'docker load -i /tmp/mcm-dev/mcm-dev.tar'* ]] || fail 'listener job does not load the shared image artifact'
[[ "$listener_job" != *'docker build'* ]] || fail 'listener job rebuilds the dev image'
[[ "$listener_job" == *'docker compose -f docker-compose.yml -p mcm'*'up --no-build -d'* ]] || fail 'listener stack does not use --no-build and project mcm'
[[ "$listener_job" == *'docker run --rm --entrypoint docker mcm:dev compose version'* ]] || fail 'isolated Compose plugin preflight is missing'
pass 'listener job reuses the image artifact and uses project mcm without rebuilding'

# Extract the actual override heredoc and require the minimal standalone model.
awk '
    /cat > \/tmp\/docker-compose.e2e.yml <<EOF/ {capture=1; next}
    capture && /^[[:space:]]*EOF$/ {exit}
    capture {print substr($0, 11)}
' "$WORKFLOW" > "$TMP_DIR/docker-compose.e2e.yml"
[[ -s "$TMP_DIR/docker-compose.e2e.yml" ]] || fail 'could not extract listener override heredoc'
grep -Eq '^  mosquitto:$' "$TMP_DIR/docker-compose.e2e.yml" || fail 'override lacks mosquitto service'
grep -Eq '^    image: eclipse-mosquitto:2\.0$' "$TMP_DIR/docker-compose.e2e.yml" || fail 'override mosquitto image does not match base'
grep -Eq '^  mcm:$' "$TMP_DIR/docker-compose.e2e.yml" || fail 'override lacks mcm service'
grep -Eq '^    image: mcm:dev$' "$TMP_DIR/docker-compose.e2e.yml" || fail 'override mcm image does not match base'
for port in 1883 9001 1884; do
    grep -Eq "^[[:space:]]*- \"${port}:${port}\"$" "$TMP_DIR/docker-compose.e2e.yml" || fail "override is missing port ${port}"
done
grep -Fq 'MCM_MOSQUITTO_COMPOSE_PATH: /tmp/docker-compose.e2e.yml' "$TMP_DIR/docker-compose.e2e.yml" || fail 'override lost the compose path environment'
grep -Fq '/tmp/docker-compose.e2e.yml:/tmp/docker-compose.e2e.yml:ro' "$TMP_DIR/docker-compose.e2e.yml" || fail 'override lost its read-only self-mount'
pass 'standalone override has expected services, images, environment, mount, and ports'

if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    docker compose -f "$TMP_DIR/docker-compose.e2e.yml" -p mcm config --quiet \
        || fail 'standalone listener override fails Compose config validation'
    pass 'standalone override passes daemon-free Compose config validation'
else
    printf 'SKIP: docker Compose plugin unavailable; standalone config validation not run\n'
fi
