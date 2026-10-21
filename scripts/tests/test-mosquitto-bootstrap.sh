#!/usr/bin/env bash
# Exercise the production bootstrap logic against isolated filesystem fixtures.
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
BOOTSTRAP="$ROOT/deploy/mosquitto/config/mosquitto-bootstrap.sh"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

fail() { printf 'FAIL: %s\n' "$1" >&2; exit 1; }
pass() { printf 'PASS: %s\n' "$1"; }

CONFIG_DIR="$TMP_DIR/config"
INIT_DIR="$TMP_DIR/init"
mkdir -p "$CONFIG_DIR" "$INIT_DIR" "$TMP_DIR/bin"
cat > "$CONFIG_DIR/mosquitto.conf" <<'CONF'
# Place your local configuration in this file.
#listener 1883
#allow_anonymous false
CONF
cat > "$INIT_DIR/mosquitto.conf" <<'CONF'
listener 1883 0.0.0.0
allow_anonymous false
password_file /mosquitto/config/passwd
acl_file /mosquitto/config/acl
CONF
cat > "$TMP_DIR/bin/mosquitto_passwd" <<'SH'
#!/bin/sh
printf 'seeded:%s:%s\n' "$4" "$5" > "$3"
SH
cat > "$TMP_DIR/bin/mosquitto" <<'SH'
#!/bin/sh
printf 'broker launched\n' >> "$BROKER_LOG"
SH
chmod +x "$TMP_DIR/bin/mosquitto_passwd" "$TMP_DIR/bin/mosquitto"

# Run the actual production script with only its filesystem locations and final
# broker exec redirected into this private fixture. Every replacement is exact.
python3 - "$BOOTSTRAP" "$TMP_DIR/bootstrap.sh" "$CONFIG_DIR" "$INIT_DIR" "$TMP_DIR/bin/mosquitto" <<'PY'
from pathlib import Path
import sys
source, destination, config, init, broker = sys.argv[1:]
text = Path(source).read_text()
replacements = {
    'CONFIG_DIR="/mosquitto/config"': f'CONFIG_DIR="{config}"',
    'INIT_DIR="${MOSQUITTO_INIT_DIR:-/mosquitto-init}"': f'INIT_DIR="{init}"',
    'exec /usr/sbin/mosquitto -c "$CONFIG_DIR/mosquitto.conf"': f'exec "{broker}" -c "$CONFIG_DIR/mosquitto.conf"',
}
for old, new in replacements.items():
    if text.count(old) != 1:
        raise SystemExit(f'expected exactly one bootstrap injection point: {old}')
    text = text.replace(old, new)
Path(destination).write_text(text)
PY
chmod +x "$TMP_DIR/bootstrap.sh"
run_bootstrap() {
    BROKER_LOG="$TMP_DIR/broker.log" PATH="$TMP_DIR/bin:/usr/bin:/bin" \
        MCM_DEV_BROKER_USER=admin MCM_DEV_BROKER_PASSWORD=test-password \
        "$TMP_DIR/bootstrap.sh" >/dev/null
}

run_bootstrap || fail 'fresh-volume bootstrap failed'
cmp -s "$INIT_DIR/mosquitto.conf" "$CONFIG_DIR/mosquitto.conf" || fail 'comment-only image-default config was not replaced by auth-enabled source'
grep -Fxq 'seeded:admin:test-password' "$CONFIG_DIR/passwd" || fail 'passwd was not seeded'
grep -Fq 'user admin' "$CONFIG_DIR/acl" || fail 'ACL service-user grants were not seeded'
marker="$CONFIG_DIR/.mcm-bootstrap-initialized"
[[ -f "$marker" ]] || fail 'successful initial seed did not create its marker'
pass 'fresh image-default volume is seeded with authenticated config, passwd, and ACL'

printf '\nlistener 1884 0.0.0.0\n' >> "$CONFIG_DIR/mosquitto.conf"
printf 'managed passwd\n' > "$CONFIG_DIR/passwd"
printf 'managed acl\n' > "$CONFIG_DIR/acl"
printf 'changed source template\nallow_anonymous true\n' > "$INIT_DIR/mosquitto.conf"
rm "$marker"
managed_config="$(<"$CONFIG_DIR/mosquitto.conf")"
run_bootstrap || fail 'second bootstrap failed'
[[ "$(<"$CONFIG_DIR/mosquitto.conf")" == "$managed_config" ]] || fail 'markerless managed config was overwritten on restart'
grep -Fq 'listener 1884 0.0.0.0' "$CONFIG_DIR/mosquitto.conf" || fail 'managed listener was erased on second startup'
[[ "$(<"$CONFIG_DIR/passwd")" == 'managed passwd' ]] || fail 'existing passwd was overwritten'
[[ "$(<"$CONFIG_DIR/acl")" == 'managed acl' ]] || fail 'existing ACL was overwritten'
pass 'managed config, passwd, and ACL survive restart despite a changed source template'

# A markerless config that cannot be inspected must never be replaced: an
# unreadable file is not evidence of an empty config. Root bypasses file
# permissions, so the case only applies to a non-root user.
if [ "$(id -u)" -ne 0 ]; then
    rm -f "$marker"
    chmod 000 "$CONFIG_DIR/mosquitto.conf"
    if run_bootstrap >"$TMP_DIR/unreadable.out" 2>&1; then
        chmod 644 "$CONFIG_DIR/mosquitto.conf"
        fail 'bootstrap replaced a config it could not inspect'
    fi
    grep -Fq 'refusing to replace it' "$TMP_DIR/unreadable.out" || {
        chmod 644 "$CONFIG_DIR/mosquitto.conf"
        fail 'bootstrap did not report the unreadable config'
    }
    [[ ! -e "$marker" ]] || fail 'failed inspection left a success marker'
    chmod 644 "$CONFIG_DIR/mosquitto.conf"
    grep -Fq 'listener 1884 0.0.0.0' "$CONFIG_DIR/mosquitto.conf" || fail 'unreadable config was not preserved verbatim'
    pass 'unreadable markerless config is reported and preserved, never replaced'
else
    printf 'SKIP: unreadable-config case requires a non-root user\n'
fi

rm "$CONFIG_DIR/mosquitto.conf"
run_bootstrap || fail 'missing config was not reseeded'
grep -Fq 'changed source template' "$CONFIG_DIR/mosquitto.conf" || fail 'missing config was not reseeded from current source'
pass 'missing config is reseeded after initialization'

rm -f "$marker" "$CONFIG_DIR/mosquitto.conf"
rm "$INIT_DIR/mosquitto.conf"
launches_before_failure="$(wc -l < "$TMP_DIR/broker.log")"
if run_bootstrap >"$TMP_DIR/missing-seed.out" 2>&1; then
    fail 'bootstrap succeeded without its initialization source'
fi
[[ ! -e "$marker" ]] || fail 'failed seed left a success marker'
[[ "$(wc -l < "$TMP_DIR/broker.log")" == "$launches_before_failure" ]] || fail 'broker launched after seed failure'
pass 'missing initialization source leaves no marker and does not launch broker'

printf 'PASS: Mosquitto bootstrap regression\n'
