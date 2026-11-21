#!/usr/bin/env bash
# Tests for scripts/restore.sh write-failure handling (issue #397).
#
# Run from the repo root:
#   bash scripts/tests/test-restore.sh
#
# No real docker is needed: a stub `docker` on PATH accepts every helper
# container, reports empty destination volumes, and fails the copy into
# the volume path named by FAIL_DEST. Each case runs restore.sh end to
# end against a real archive with a valid manifest.

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
# compose (volume ls / ps): print nothing, i.e. no volumes listed and no
# running services.
[ "$1" = "run" ] || exit 0
script="${*: -1}"
case "$script" in
    *"-print -quit"*) exit 1 ;; # volume_has_files: destination is empty
esac
if [ -n "${FAIL_DEST:-}" ] && [[ "$script" == *"cp -p '/staging/"*"' '/data/${FAIL_DEST}'"* ]]; then
    echo "cp: can't create '/data/${FAIL_DEST}': No space left on device" >&2
    exit 1
fi
printf '%s\n' "$script" >> "$DOCKER_LOG"
STUB
chmod +x "$WORK/bin/docker"

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
    PATH="$WORK/bin:$PATH" DOCKER_LOG="$WORK/docker.log" FAIL_DEST="${2:-}" \
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
