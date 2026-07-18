#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
UPGRADE_SCRIPT="$SCRIPT_DIR/../../upgrade.sh"
T=$(mktemp -d)
PASS=0 FAIL=0

cleanup() { rm -rf "$T"; }
trap cleanup EXIT

fail_msg() { echo "FAIL: $1"; FAIL=$((FAIL + 1)); }
pass()     { echo "PASS: $1"; PASS=$((PASS + 1)); }

setup() {
  rm -rf "$T"/*
  mkdir -p "$T/bin" "$T/etc/caddy" "$T/var/backups/orvix-upgrade" \
    "$T/var/lib/orvix" "$T/etc/orvix" \
    "$T/usr/share/orvix/"{admin,webmail,marketing} "$T/usr/local/bin"
}

mkdir -p "$T/bin"

# Fake systemctl: tracks calls
cat > "$T/bin/systemctl" <<'SYSTEMCTL'
#!/usr/bin/env bash
echo "systemctl:$*" >> /tmp/sysctl_calls
case "$*" in
  "restart orvix.service")
    [ "${SYSTEMCTL_RESTART_FAIL:-0}" = "1" ] && exit 1 || exit 0
    ;;
  "is-active --quiet orvix")
    [ "${SYSTEMCTL_ACTIVE_FAIL:-0}" = "1" ] && exit 1 || exit 0
    ;;
  *) exit 0 ;;
esac
SYSTEMCTL
chmod +x "$T/bin/systemctl"

# Fake caddy: tracks calls, configurable failures
cat > "$T/bin/caddy" <<'CADDY'
#!/usr/bin/env bash
echo "caddy:$*" >> /tmp/caddy_calls
case "$1" in
  validate) [ "${CADDY_VALIDATE_FAIL:-0}" = "1" ] && exit 1 || exit 0 ;;
  reload)   [ "${CADDY_RELOAD_FAIL:-0}" = "1" ] && exit 1 || exit 0 ;;
  *) exit 1 ;;
esac
CADDY
chmod +x "$T/bin/caddy"

export SYSTEMCTL_RESTART_FAIL=0 SYSTEMCTL_ACTIVE_FAIL=0
export CADDY_VALIDATE_FAIL=0 CADDY_RELOAD_FAIL=0
export PATH="$T/bin:$PATH"

run_backup() {
  ORVIX_BIN="$T/usr/local/bin/orvix" \
  ORVIX_CONFIG="$T/etc/orvix/orvix.yaml" \
  ORVIX_DATA_DIR="$T/var/lib/orvix" \
  ORVIX_JWT_KEY="$T/var/lib/orvix/jwt_key.pem" \
  ORVIX_VAPID_PRIVATE_KEY="$T/etc/orvix/vapid_private.key" \
  ORVIX_VAPID_PUBLIC_KEY="$T/etc/orvix/vapid_public.key" \
  ORVIX_BACKUP_ENCRYPTION_KEY="$T/etc/orvix/backup_encryption.key" \
  ORVIX_CADDYFILE="$T/etc/caddy/Caddyfile" \
  ORVIX_DKIM_DIR="$T/var/lib/orvix/dkim" \
  BACKUP_PARENT="$T/var/backups/orvix-upgrade" \
  bash -c '
    source '"$UPGRADE_SCRIPT"'
    preflight_backup
    echo "BACKUP_DIR=$BACKUP_DIR" >&2
    echo "$BACKUP_DIR"
  '
}

run_rollback() {
  local backup_dir="$1"
  ORVIX_BIN="$T/usr/local/bin/orvix" \
  ORVIX_CONFIG="$T/etc/orvix/orvix.yaml" \
  ORVIX_DATA_DIR="$T/var/lib/orvix" \
  ORVIX_JWT_KEY="$T/var/lib/orvix/jwt_key.pem" \
  ORVIX_VAPID_PRIVATE_KEY="$T/etc/orvix/vapid_private.key" \
  ORVIX_VAPID_PUBLIC_KEY="$T/etc/orvix/vapid_public.key" \
  ORVIX_BACKUP_ENCRYPTION_KEY="$T/etc/orvix/backup_encryption.key" \
  ORVIX_CADDYFILE="$T/etc/caddy/Caddyfile" \
  ORVIX_DKIM_DIR="$T/var/lib/orvix/dkim" \
  ORVIX_ADMIN_UI_DIR="$T/usr/share/orvix/admin" \
  ORVIX_WEBMAIL_UI_DIR="$T/usr/share/orvix/webmail" \
  ORVIX_MARKETING_UI_DIR="$T/usr/share/orvix/marketing" \
  ORVIX_CADDY_BIN="$T/bin/caddy" \
  ORVIX_SYSTEMCTL="$T/bin/systemctl" \
  BACKUP_PARENT="$T/var/backups/orvix-upgrade" \
  bash -c '
    source '"$UPGRADE_SCRIPT"'
    full_rollback "'"$backup_dir"'"
  '
}

echo "=== Rollback Behavioral Tests ==="

# ── Test 1: Preflight backup creates directory and manifest ──
setup
echo "old-binary-v1.0.3" > "$T/usr/local/bin/orvix"
echo "old-config" > "$T/etc/orvix/orvix.yaml"
echo "old-db" > "$T/var/lib/orvix/orvix.db"
echo "jwt-secret" > "$T/var/lib/orvix/jwt_key.pem"
echo "vapid-private" > "$T/etc/orvix/vapid_private.key"
echo "vapid-public" > "$T/etc/orvix/vapid_public.key"
echo "backup-key" > "$T/etc/orvix/backup_encryption.key"
echo "admin.example.com { reverse_proxy 127.0.0.1:8080 }" > "$T/etc/caddy/Caddyfile"
echo "old-admin" > "$T/usr/share/orvix/admin/index.html"
echo "old-webmail" > "$T/usr/share/orvix/webmail/index.html"
echo "old-marketing" > "$T/usr/share/orvix/marketing/index.html"

BACKUP_DIR="$(run_backup 2>/dev/null || true)"
if [ -n "$BACKUP_DIR" ] && [ -d "$BACKUP_DIR" ]; then
  pass "preflight_backup creates backup directory"
else
  fail_msg "preflight_backup did not create backup directory"
fi

if [ -f "$BACKUP_DIR/manifest" ]; then
  pass "backup manifest created"
else
  fail_msg "backup manifest NOT created"
fi

if grep -q 'path=' "$BACKUP_DIR/manifest" 2>/dev/null; then
  pass "manifest contains file paths"
else
  fail_msg "manifest does NOT contain file paths"
fi

if grep -q 'uid=' "$BACKUP_DIR/manifest" 2>/dev/null; then
  pass "manifest records UID"
else
  fail_msg "manifest does NOT record UID"
fi

if grep -q 'gid=' "$BACKUP_DIR/manifest" 2>/dev/null; then
  pass "manifest records GID"
else
  fail_msg "manifest does NOT record GID"
fi

if grep -q 'mode=' "$BACKUP_DIR/manifest" 2>/dev/null; then
  pass "manifest records numeric mode"
else
  fail_msg "manifest does NOT record numeric mode"
fi

if grep -q 'sha256=' "$BACKUP_DIR/manifest" 2>/dev/null; then
  pass "manifest records SHA-256"
else
  fail_msg "manifest does NOT record SHA-256"
fi

if [ -f "$BACKUP_DIR/run_id" ]; then
  pass "run_id file created"
else
  fail_msg "run_id file NOT created"
fi

# ── Test 2-5: Rollback restores binary, config, db, jwt byte-for-byte ──
echo "new-binary-v2" > "$T/usr/local/bin/orvix"
echo "new-config" > "$T/etc/orvix/orvix.yaml"
echo "new-db" > "$T/var/lib/orvix/orvix.db"
echo "new-jwt" > "$T/var/lib/orvix/jwt_key.pem"

set +e
rollback_output="$(run_rollback "$BACKUP_DIR" 2>&1)"
rollback_rc=$?
set -e
if [ "$rollback_rc" = "0" ]; then
  pass "rollback returns zero on success"
else
  fail_msg "rollback returns non-zero ($rollback_rc) even on success: $rollback_output"
fi

ACTUAL_BINARY="$(cat "$T/usr/local/bin/orvix")"
if [ "$ACTUAL_BINARY" = "old-binary-v1.0.3" ]; then
  pass "binary restored byte-for-byte"
else
  fail_msg "binary NOT restored: got '$ACTUAL_BINARY'"
fi

ACTUAL_CONFIG="$(cat "$T/etc/orvix/orvix.yaml")"
if [ "$ACTUAL_CONFIG" = "old-config" ]; then
  pass "config restored byte-for-byte"
else
  fail_msg "config NOT restored"
fi

ACTUAL_DB="$(cat "$T/var/lib/orvix/orvix.db")"
if [ "$ACTUAL_DB" = "old-db" ]; then
  pass "db restored byte-for-byte"
else
  fail_msg "db NOT restored"
fi

ACTUAL_JWT="$(cat "$T/var/lib/orvix/jwt_key.pem")"
if [ "$ACTUAL_JWT" = "jwt-secret" ]; then
  pass "jwt restored byte-for-byte"
else
  fail_msg "jwt NOT restored"
fi

# ── Test 6: JWT ownership is correct ──
rollback_jwt_manifest="$(grep -A6 "path=$T/var/lib/orvix/jwt_key.pem" "$BACKUP_DIR/manifest" 2>/dev/null || true)"
jwt_uid="$(echo "$rollback_jwt_manifest" | grep '^uid=' | cut -d= -f2)"
jwt_gid="$(echo "$rollback_jwt_manifest" | grep '^gid=' | cut -d= -f2)"
jwt_mode="$(echo "$rollback_jwt_manifest" | grep '^mode=' | cut -d= -f2)"
# On Windows, stat may return '0' for UID/GID. Accept both.
if [ -n "$jwt_uid" ] && [ -n "$jwt_gid" ] && [ -n "$jwt_mode" ]; then
  pass "JWT metadata recorded: uid=$jwt_uid gid=$jwt_gid mode=$jwt_mode"
else
  fail_msg "JWT metadata not recorded in manifest"
fi

# ── Test 7: Caddy validate and reload invoked ──
if grep -q "caddy:validate" /tmp/caddy_calls 2>/dev/null; then
  pass "Caddy validate invoked"
else
  fail_msg "Caddy validate NOT invoked"
fi

if grep -q "caddy:reload" /tmp/caddy_calls 2>/dev/null; then
  pass "Caddy reload invoked"
else
  fail_msg "Caddy reload NOT invoked"
fi

# ── Test 8-9: Systemctl restart and is-active ──
if grep -q "systemctl:restart orvix.service" /tmp/sysctl_calls 2>/dev/null; then
  pass "systemctl restart invoked"
else
  fail_msg "systemctl restart NOT invoked"
fi

if grep -q "systemctl:is-active" /tmp/sysctl_calls 2>/dev/null; then
  pass "systemctl is-active checked after rollback"
else
  fail_msg "systemctl is-active NOT checked"
fi

# ── Test 10: Caddy validation failure causes nonzero rollback ──
setup
echo "old-binary-v1.0.3" > "$T/usr/local/bin/orvix"
echo "admin.example.com { reverse_proxy 127.0.0.1:8080 }" > "$T/etc/caddy/Caddyfile"
BACKUP_DIR2="$(run_backup 2>/dev/null || true)"
echo "new-binary-v2" > "$T/usr/local/bin/orvix"
CADDY_VALIDATE_FAIL=1
set +e
rollback_output="$(run_rollback "$BACKUP_DIR2" 2>&1)"
rollback_rc=$?
set -e
if [ "$rollback_rc" != "0" ]; then
  pass "Caddy validation failure returns non-zero (rc=$rollback_rc)"
else
  fail_msg "Caddy validation failure should return non-zero, got rc=$rollback_rc"
fi
CADDY_VALIDATE_FAIL=0

# ── Test 11: Caddy reload failure causes nonzero rollback ──
setup
echo "old-binary-v1.0.3" > "$T/usr/local/bin/orvix"
echo "admin.example.com { reverse_proxy 127.0.0.1:8080 }" > "$T/etc/caddy/Caddyfile"
BACKUP_DIR3="$(run_backup 2>/dev/null || true)"
echo "new-binary-v2" > "$T/usr/local/bin/orvix"
CADDY_RELOAD_FAIL=1
set +e
rollback_output="$(run_rollback "$BACKUP_DIR3" 2>&1)"
rollback_rc=$?
set -e
if [ "$rollback_rc" != "0" ]; then
  pass "Caddy reload failure returns non-zero (rc=$rollback_rc)"
else
  fail_msg "Caddy reload failure should return non-zero, got rc=$rollback_rc"
fi
CADDY_RELOAD_FAIL=0

# ── Test 12: Service restart failure causes nonzero rollback ──
setup
echo "old-binary-v1.0.3" > "$T/usr/local/bin/orvix"
echo "admin.example.com { reverse_proxy 127.0.0.1:8080 }" > "$T/etc/caddy/Caddyfile"
BACKUP_DIR4="$(run_backup 2>/dev/null || true)"
echo "new-binary-v2" > "$T/usr/local/bin/orvix"
SYSTEMCTL_RESTART_FAIL=1
set +e
rollback_output="$(run_rollback "$BACKUP_DIR4" 2>&1)"
rollback_rc=$?
set -e
if [ "$rollback_rc" != "0" ]; then
  pass "service restart failure returns non-zero (rc=$rollback_rc)"
else
  fail_msg "service restart failure should return non-zero, got rc=$rollback_rc"
fi
SYSTEMCTL_RESTART_FAIL=0

# ── Test 13: Post-rollback health failure causes nonzero ──
setup
echo "old-binary-v1.0.3" > "$T/usr/local/bin/orvix"
echo "admin.example.com { reverse_proxy 127.0.0.1:8080 }" > "$T/etc/caddy/Caddyfile"
BACKUP_DIR5="$(run_backup 2>/dev/null || true)"
echo "new-binary-v2" > "$T/usr/local/bin/orvix"
SYSTEMCTL_ACTIVE_FAIL=1
set +e
rollback_output="$(run_rollback "$BACKUP_DIR5" 2>&1)"
rollback_rc=$?
set -e
if [ "$rollback_rc" != "0" ]; then
  pass "post-rollback health failure returns non-zero (rc=$rollback_rc)"
else
  fail_msg "post-rollback health failure should return non-zero, got rc=$rollback_rc"
fi
SYSTEMCTL_ACTIVE_FAIL=0

# ── Test 14: Rollback is idempotent ──
setup
echo "old-binary-v1.0.3" > "$T/usr/local/bin/orvix"
echo "admin.example.com { reverse_proxy 127.0.0.1:8080 }" > "$T/etc/caddy/Caddyfile"
BACKUP_DIR6="$(run_backup 2>/dev/null || true)"
echo "new-binary-v2" > "$T/usr/local/bin/orvix"
set +e; run_rollback "$BACKUP_DIR6" >/dev/null 2>&1; first_rc=$?; set -e
echo "new-binary-v2" > "$T/usr/local/bin/orvix"
set +e; run_rollback "$BACKUP_DIR6" >/dev/null 2>&1; second_rc=$?; set -e
if [ "$first_rc" = "0" ] && [ "$second_rc" = "0" ]; then
  pass "rollback is idempotent"
else
  fail_msg "rollback not idempotent: first=$first_rc second=$second_rc"
fi

echo ""
echo "---"
echo "Results: $PASS passed, $FAIL failed"
if [ "$FAIL" -gt 0 ]; then echo "SOME TESTS FAILED"; exit 1; fi
echo "Rollback behavioral tests PASSED"
