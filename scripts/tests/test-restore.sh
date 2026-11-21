#!/usr/bin/env bash
# Tests for scripts/restore.sh write-failure handling (issue #397) and
# snapshot validation before any destination change (issue #399).
#
# Run from the repo root:
#   bash scripts/tests/test-restore.sh
#
# No real docker is needed: a stub `docker` on PATH accepts every helper
# container, reports empty destination volumes, and fails the copy into
# the volume path named by FAIL_DEST. The database check runs its real
# script against a fake sqlite3 that reports FAKE_TABLES schema objects.
# Each case runs restore.sh end to end against a real archive with a
# valid manifest.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../lib-backup.sh
source "${REPO_ROOT}/scripts/lib-backup.sh"

PASS=0
FAIL=0
WORK="$(mktemp -d -t mcm-test-restore-XXXXXX)"
trap 'rm -rf "$WORK"' EXIT

check() {
    local msg="$1"
    shift
    if "$@"; then
        PASS=$((PASS + 1))
    else
        FAIL=$((FAIL + 1))
        echo "FAIL: $msg" >&2
        echo "  --- stdout ---" >&2; sed 's/^/  /' "$WORK/out" >&2
        echo "  --- stderr ---" >&2; sed 's/^/  /' "$WORK/err" >&2
    fi
}

mkdir -p "$WORK/bin"
cat > "$WORK/bin/docker" <<'STUB'
#!/usr/bin/env bash
# compose ps lists the services named in RUNNING (none by default);
# volume ls prints nothing, i.e. no volumes listed.
if [ "$1" != "run" ]; then
    [ "${2:-}" = "ps" ] && [ -n "${RUNNING:-}" ] && printf '%s\n' $RUNNING
    exit 0
fi
script="${*: -1}"
case "$script" in
    *"-print -quit"*) exit 1 ;; # volume_has_files: destination is empty
    *"PRAGMA integrity_check"*) PATH="$FAKE_BIN:$PATH" exec sh -c "$script" ;;
esac
if [ -n "${FAIL_DEST:-}" ] && [[ "$script" == *"cp -p '/staging/"*"' '/data/${FAIL_DEST}'"* ]]; then
    echo "cp: can't create '/data/${FAIL_DEST}': No space left on device" >&2
    exit 1
fi
printf '%s\n' "$script" >> "$DOCKER_LOG"
STUB
chmod +x "$WORK/bin/docker"

mkdir -p "$WORK/fakebin"
cat > "$WORK/fakebin/sqlite3" <<'STUB'
#!/bin/sh
echo "sqlite3 $1" >> "$DOCKER_LOG"
case "$2" in
    *integrity_check*) echo ok ;;
    *sqlite_master*) echo "${FAKE_TABLES:-3}" ;;
esac
STUB
chmod +x "$WORK/fakebin/sqlite3"

# make_archive <name> <staged relpath>... builds <name>.tar.gz with the
# given files and a manifest covering them.
make_archive() {
    local name="$1"
    shift
    local staging="$WORK/staging-$name" args=() rel
    rm -rf "$staging"
    for rel in "$@"; do
        mkdir -p "$staging/$(dirname "$rel")"
        printf 'content of %s\n' "$rel" > "$staging/$rel"
        args+=(--file "$staging/$rel" --label "$rel")
    done
    write_manifest "$staging" "operator=test" "${args[@]}" > /dev/null
    tar czf "$WORK/$name.tar.gz" -C "$staging" .
}

# run_restore <archive name> [FAIL_DEST]; sets RC, output in $WORK/out|err.
run_restore() {
    : > "$WORK/docker.log"
    set +e
    PATH="$WORK/bin:$PATH" DOCKER_LOG="$WORK/docker.log" FAIL_DEST="${2:-}" RUNNING="${RUNNING:-}" \
        FAKE_BIN="$WORK/fakebin" FAKE_TABLES="${FAKE_TABLES:-3}" \
        bash "$REPO_ROOT/scripts/restore.sh" --confirm "$WORK/$1.tar.gz" \
        > "$WORK/out" 2> "$WORK/err"
    RC=$?
    set -e
}

out_has() { grep -qF -- "$1" "$WORK/out"; }
err_has() { grep -qF -- "$1" "$WORK/err"; }
no_success() { ! out_has "restore.sh: SUCCESS"; }
wrote() { grep -qF -- "'/data/$1'" "$WORK/docker.log"; }

FULL=(mcm/mcm.db mcm/.bootstrap.json mosquitto/passwd mosquitto/acl mosquitto/mosquitto.conf)
make_archive full "${FULL[@]}"

# Previous behaviour: a complete archive restores every file and succeeds.
run_restore full
check "full archive exits 0" [ "$RC" -eq 0 ]
check "full archive prints SUCCESS" out_has "restore.sh: SUCCESS"
for f in mcm.db .bootstrap.json passwd acl mosquitto.conf; do
    check "full archive writes $f" wrote "$f"
done

check "full archive verifies the staged mcm.db" \
    grep -qF "sqlite3 file:/staging/mcm/mcm.db" "$WORK/docker.log"

# Issue #399: a snapshot whose checksum matches but whose schema is empty
# is rejected before the destination is touched: no sidecar removal and
# no file written to any volume.
FAKE_TABLES=0 run_restore full
check "empty-schema snapshot exits 1 (got $RC)" [ "$RC" -eq 1 ]
check "empty-schema snapshot is reported" \
    err_has "restore.sh: snapshot mcm.db has an empty schema; refusing to restore"
check "empty-schema snapshot keeps existing WAL/SHM" \
    bash -c "! grep -qF 'rm -f /data/mcm.db-wal' '$WORK/docker.log'"
for f in mcm.db .bootstrap.json passwd acl mosquitto.conf; do
    check "empty-schema snapshot does not write $f" bash -c "! grep -qF \"'/data/$f'\" '$WORK/docker.log'"
done
check "empty-schema snapshot does not print SUCCESS" no_success

# Issue #397: a Mosquitto file present in the archive that fails to write
# aborts the restore, names the file and never reports SUCCESS.
for f in passwd acl mosquitto.conf; do
    run_restore full "$f"
    check "write failure of $f exits 1 (got $RC)" [ "$RC" -eq 1 ]
    check "write failure of $f names mosquitto/$f" \
        err_has "restore.sh: failed to write mosquitto/$f to volume"
    check "write failure of $f does not print SUCCESS" no_success
done

# The same rule holds for .bootstrap.json (it carries the JWT secret):
# a write failure must not be reported as "absent from the archive".
run_restore full .bootstrap.json
check "write failure of .bootstrap.json exits 1 (got $RC)" [ "$RC" -eq 1 ]
check "write failure of .bootstrap.json names it" \
    err_has "restore.sh: failed to write mcm/.bootstrap.json to volume"
check "write failure of .bootstrap.json is not reported as absent" \
    bash -c "! grep -qF 'no .bootstrap.json in archive' '$WORK/out'"
check "write failure of .bootstrap.json does not print SUCCESS" no_success

# Issue #400: restoring broker persistence while Mosquitto runs is
# refused before any volume is touched.
make_archive broker mcm/mcm.db mosquitto/passwd mosquitto-data/mosquitto.db
no_volume_writes() { ! grep -qF '/data' "$WORK/docker.log"; }
RUNNING=mosquitto run_restore broker
check "broker data with Mosquitto running exits 2 (got $RC)" [ "$RC" -eq 2 ]
check "broker data with Mosquitto running is reported" \
    err_has "restore.sh: Mosquitto is running; stop it with 'docker compose stop mosquitto' before restoring broker persistence"
check "broker data with Mosquitto running touches no volume" no_volume_writes
check "broker data with Mosquitto running does not print SUCCESS" no_success

# With both services stopped the broker persistence is restored.
run_restore broker
check "broker data with services stopped exits 0 (got $RC)" [ "$RC" -eq 0 ]
check "broker data with services stopped writes persistence" \
    grep -qF 'cp -rp /staging/mosquitto-data/. /data/' "$WORK/docker.log"

# Previous behaviour: a running MCM is still refused.
RUNNING=mcm run_restore full
check "running MCM exits 2 (got $RC)" [ "$RC" -eq 2 ]
check "running MCM is reported" \
    err_has "restore.sh: MCM is running; stop it with 'docker compose stop mcm' before restoring SQLite"
check "running MCM touches no volume" no_volume_writes

# Previous behaviour: without broker persistence in the archive a running
# Mosquitto does not block the restore (it is restarted afterwards).
RUNNING=mosquitto run_restore full
check "no broker data with Mosquitto running exits 0 (got $RC)" [ "$RC" -eq 0 ]
check "no broker data with Mosquitto running prints SUCCESS" out_has "restore.sh: SUCCESS"

# Previous behaviour: files absent from an (older) archive stay optional.
make_archive partial mcm/mcm.db mosquitto/passwd
run_restore partial
check "partial archive exits 0" [ "$RC" -eq 0 ]
check "partial archive prints SUCCESS" out_has "restore.sh: SUCCESS"
check "partial archive notes missing .bootstrap.json" \
    out_has "restore.sh: no .bootstrap.json in archive; MCM will regenerate it on first boot"
check "partial archive notes missing acl" out_has "restore.sh: no mosquitto/acl in archive; skipping"
check "partial archive notes missing mosquitto.conf" \
    out_has "restore.sh: no mosquitto/mosquitto.conf in archive; skipping"
check "partial archive writes passwd" wrote passwd

echo "test-restore: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
