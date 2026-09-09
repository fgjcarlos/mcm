#!/usr/bin/env bash
# Unit tests for scripts/lib-backup.sh.
#
# Run from the repo root:
#   bash scripts/tests/test-lib-backup.sh
#
# Convention: lib-backup.sh is sourced, each test is a function named
# test_*. assert_* helpers report pass/fail. Exit code is non-zero on the
# first failure (or at the end with the summary).
#
# These tests do NOT require docker — they exercise the pure helpers
# (project-name resolver, sha256, manifest writer/parser). The
# docker-dependent resolvers (compose_volume_name) have a docker-aware
# path AND a fallback that uses the dir basename, which we test by
# stubbing the docker binary.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../lib-backup.sh
source "${REPO_ROOT}/scripts/lib-backup.sh"

PASS=0
FAIL=0

# --- minimal assert helpers -------------------------------------------------

assert_eq() {
    local expected="$1" actual="$2" msg="${3:-}"
    if [ "$expected" = "$actual" ]; then
        PASS=$((PASS + 1))
    else
        FAIL=$((FAIL + 1))
        echo "FAIL: $msg" >&2
        echo "  expected: $expected" >&2
        echo "  actual:   $actual" >&2
    fi
}

assert_true() {
    local actual="$1" msg="${2:-}"
    if [ "$actual" = "true" ] || [ "$actual" = "0" ]; then
        PASS=$((PASS + 1))
    else
        FAIL=$((FAIL + 1))
        echo "FAIL: $msg (got: $actual)" >&2
    fi
}

assert_contains() {
    local haystack="$1" needle="$2" msg="${3:-}"
    if [[ "$haystack" == *"$needle"* ]]; then
        PASS=$((PASS + 1))
    else
        FAIL=$((FAIL + 1))
        echo "FAIL: $msg" >&2
        echo "  haystack: $haystack" >&2
        echo "  needle:   $needle" >&2
    fi
}

# --- tests --------------------------------------------------------------------

test_compose_project_name_defaults_to_dir_basename() {
    local pwd_basename
    pwd_basename="$(basename "$REPO_ROOT")"
    local result
    result="$(compose_project_name "$REPO_ROOT")"
    assert_eq "$pwd_basename" "$result" "compose_project_name should default to dir basename ($pwd_basename)"
}

test_compose_project_name_respects_explicit_env() {
    local result
    result="$(COMPOSE_PROJECT_NAME=myproj compose_project_name "$REPO_ROOT")"
    assert_eq "myproj" "$result" "compose_project_name should honour COMPOSE_PROJECT_NAME"
}

test_compose_volume_name_default_suffix() {
    # Stub docker so the test does not need a running daemon.
    local stub_dir
    stub_dir="$(mktemp -d)"
    cat > "${stub_dir}/docker" <<'EOF'
#!/usr/bin/env bash
# Stub: print a predictable volume name regardless of args.
if [ "$1" = "compose" ] && [ "$2" = "--project-name" ] && [ "$4" = "volume" ] && [ "$5" = "ls" ]; then
    echo "${PROJECT_NAME}_${VOLUME_SUFFIX}"
    exit 0
fi
exit 0
EOF
    chmod +x "${stub_dir}/docker"

    local result
    result="$(PATH="${stub_dir}:$PATH" COMPOSE_PROJECT_NAME="acme" VOLUME_SUFFIX="mcm_data" compose_volume_name "/nonexistent" "mcm_data")"
    assert_eq "acme_mcm_data" "$result" "compose_volume_name should suffix with volume key"

    rm -rf "$stub_dir"
}

test_sha256_file_for_empty_input() {
    local f
    f="$(mktemp)"
    local result
    result="$(sha256_file "$f")"
    # sha256 of an empty file
    assert_eq "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" "$result" "sha256_file for empty input"

    rm -f "$f"
}

test_sha256_file_for_known_content() {
    local f
    f="$(mktemp)"
    printf 'hello\n' > "$f"
    local result
    result="$(sha256_file "$f")"
    # sha256 of "hello\n"
    assert_eq "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03" "$result" "sha256_file for known content"
    rm -f "$f"
}

test_write_manifest_round_trip() {
    local staging
    staging="$(mktemp -d)"
    mkdir -p "${staging}/mcm" "${staging}/mosquitto"
    echo "db" > "${staging}/mcm/mcm.db"
    echo "passwd" > "${staging}/mosquitto/passwd"

    local manifest_path
    manifest_path="$(write_manifest "$staging" \
        "operator=tester" \
        "git_commit=abc123" \
        "mcm_version=v0.1.0" \
        --file "${staging}/mcm/mcm.db" --label "mcm.db" \
        --file "${staging}/mosquitto/passwd" --label "mosquitto/passwd")"

    assert_contains "$manifest_path" "manifest.json" "manifest path should end with manifest.json"
    [ -f "$manifest_path" ] || { FAIL=$((FAIL+1)); echo "FAIL: manifest file not written" >&2; return; }
    PASS=$((PASS+1))

    local parsed
    parsed="$(parse_manifest "$manifest_path")"
    assert_contains "$parsed" '"operator": "tester"' "manifest should round-trip operator"
    assert_contains "$parsed" '"git_commit": "abc123"' "manifest should round-trip git_commit"
    assert_contains "$parsed" '"mcm_version": "v0.1.0"' "manifest should round-trip mcm_version"
    assert_contains "$parsed" 'mcm.db' "manifest should list mcm.db"
    assert_contains "$parsed" 'mosquitto/passwd' "manifest should list mosquitto/passwd"
    assert_contains "$parsed" '"sha256":' "manifest should include sha256 per entry"

    rm -rf "$staging"
}

test_normalize_archive_target_dir() {
    # Volume mounts sometimes end with a trailing slash; normalize them.
    local result
    result="$(normalize_archive_target_dir "/var/lib/mcm/")"
    assert_eq "/var/lib/mcm" "$result" "trailing slash should be stripped"

    result="$(normalize_archive_target_dir "/var/lib/mcm")"
    assert_eq "/var/lib/mcm" "$result" "no trailing slash should be untouched"

    result="$(normalize_archive_target_dir "")"
    assert_eq "" "$result" "empty path should be empty"
}

# --- runner ------------------------------------------------------------------

run_tests() {
    test_compose_project_name_defaults_to_dir_basename
    test_compose_project_name_respects_explicit_env
    test_compose_volume_name_default_suffix
    test_sha256_file_for_empty_input
    test_sha256_file_for_known_content
    test_write_manifest_round_trip
    test_normalize_archive_target_dir
}

run_tests

echo
echo "lib-backup.sh tests: ${PASS} passed, ${FAIL} failed"
if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
