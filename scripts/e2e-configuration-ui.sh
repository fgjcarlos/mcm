#!/usr/bin/env bash
# Owns only a disposable broker/container/directory through the Go fixture.
set -euo pipefail
case "${1:-}" in
  --help|-h)
    printf '%s\n' \
      'Usage: scripts/e2e-configuration-ui.sh [--help]' \
      'Run the browser import/edit/validate/review/apply/recover journey on an isolated Mosquitto broker.' \
      'Requires Docker, Go, Node, npm dependencies and the local Playwright Chromium installation.' \
      'Never stops the existing Compose stack or removes existing volumes.'
    exit 0
    ;;
  '') ;;
  *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2 ;;
esac
cd "$(dirname "${BASH_SOURCE[0]}")/.."
for tool in docker go node npm; do
  command -v "$tool" >/dev/null || { printf 'Required tool is unavailable: %s\n' "$tool" >&2; exit 2; }
done
if [[ ! -f frontend/node_modules/@playwright/test/package.json ]]; then
  printf '%s\n' 'Install the frontend dependencies first: npm --prefix frontend ci' >&2
  exit 2
fi
export PLAYWRIGHT_BROWSERS_PATH="$PWD/frontend/node_modules/.cache/ms-playwright"
npm --prefix frontend run build
MCM_E2E_UI=1 go test ./internal/server -run '^TestBrokerConfigurationBrowserJourney$' -count=1 -v
