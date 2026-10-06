#!/usr/bin/env bash
# Regression: broker-config E2E must reuse the preloaded mcm:dev image.
set -euo pipefail

SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/e2e-broker-config.sh"

if ! grep -Fq '$COMPOSE up -d --no-build' "$SCRIPT"; then
    echo "expected broker-config E2E to start Compose without rebuilding the preloaded image" >&2
    exit 1
fi

echo "broker-config E2E reuses the preloaded image"
