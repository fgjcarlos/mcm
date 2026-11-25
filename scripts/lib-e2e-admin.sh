#!/usr/bin/env bash
# Explicit test-only admin provisioning for E2E stacks. Never use as a dev default.
e2e_admin_provisioning() {
    local password="${1:-e2e-admin-test-password-1234}"
    export MCM_BOOTSTRAP_ADMIN_USERNAME=admin
    export MCM_BOOTSTRAP_ADMIN_PASSWORD="$password"
}
