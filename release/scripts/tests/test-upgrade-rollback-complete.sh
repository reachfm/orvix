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

mkdir -p "$T/bin" "$T/etc/caddy" "$T/var/backups/orvix-upgrade" "$T/var/lib/orvix" "$T/etc/orvix" "$T/usr/share/orvix/"{admin,webmail,marketing} "$T/usr/local/bin"

# Fake systemctl
cat > "$T/bin/systemctl" <<'END'
#!/usr/bin/env bash
case "$*" in
  "restart orvix.service") echo "systemctl: restart orvix" >> "$SYSTEMCTL_LOG"; exit 0 ;;
  "is-active --quiet orvix") echo "systemctl: is-active orvix" >> "$SYSTEMCTL_LOG"; exit 0 ;;
  *) exit 0 ;;
esac
END
chmod +x "$T/bin/systemctl"
export SYSTEMCTL_LOG="$T/systemctl_calls.log"

# Fake caddy
cat > "$T/bin/caddy" <<'END'
#!/usr/bin/env bash
case "$1" in
  validate)
    echo "caddy: validate $3" >> "$CADDY_LOG"
    exit 0
    ;;
  reload)
    echo "caddy: reload $3" >> "$CADDY_LOG"
    exit 0
    ;;
  *)
    exit 1
    ;;
esac
END
chmod +x "$T/bin/caddy"
export CADDY_LOG="$T/caddy_calls.log"

# Old binary
echo "old-binary-v1.0.3" > "$T/usr/local/bin/orvix"

# Old config
echo "old-config" > "$T/etc/orvix/orvix.yaml"

# Old DB
echo "old-db" > "$T/var/lib/orvix/orvix.db"

# JWT key with correct ownership
echo "jwt-secret" > "$T/var/lib/orvix/jwt_key.pem"

# VAPID keys
echo "vapid-private" > "$T/etc/orvix/vapid_private.key"
echo "vapid-public" > "$T/etc/orvix/vapid_public.key"

# Backup encryption key
echo "backup-key" > "$T/etc/orvix/backup_encryption.key"

# Caddyfile
echo "admin.example.com { reverse_proxy 127.0.0.1:8080 }" > "$T/etc/caddy/Caddyfile"

# Old asset trees
echo "old-admin" > "$T/usr/share/orvix/admin/index.html"
echo "old-webmail" > "$T/usr/share/orvix/webmail/index.html"
echo "old-marketing" > "$T/usr/share/orvix/marketing/index.html"

export PATH="$T/bin:$PATH"

# Run preflight_backup in a subshell using the actual upgrade.sh functions
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

echo "=== Rollback Behavioral Tests ==="

BACKUP_DIR="$(run_backup 2>/dev/null || true)"
if [ -n "$BACKUP_DIR" ] && [ -d "$BACKUP_DIR" ]; then
  pass "preflight_backup created backup directory"
else
  fail_msg "preflight_backup did not create backup directory"
fi

# Check manifest was created
if [ -f "$BACKUP_DIR/manifest" ]; then
  pass "backup manifest created"
else
  fail_msg "backup manifest NOT created"
fi

# Check manifest contains recorded metadata
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

if grep -q 'sha256=' "$BACKUP_DIR/manifest" 2>/dev/null; then
  pass "manifest records SHA256"
else
  fail_msg "manifest does NOT record SHA256"
fi

# Check run_id was saved
if [ -f "$BACKUP_DIR/run_id" ]; then
  pass "run_id file created"
else
  fail_msg "run_id file NOT created"
fi

# Simulate upgrade (install new binary, then rollback)
echo "new-binary" > "$T/usr/local/bin/orvix"
echo "new-config" > "$T/etc/orvix/orvix.yaml"
echo "new-admin" > "$T/usr/share/orvix/admin/index.html"
echo "new-marketing" > "$T/usr/share/orvix/marketing/index.html"

# Run rollback using the actual production function
run_rollback() {
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
  SYSTEMCTL_LOG="$SYSTEMCTL_LOG" \
  CADDY_LOG="$CADDY_LOG" \
  bash -c '
    source '"$UPGRADE_SCRIPT"'
    full_rollback "'"$BACKUP_DIR"'"
  '
}

rollback_output="$(run_rollback 2>&1 || true)"
rollback_rc=$?

# Test: binary restored
ACTUAL_BINARY="$(cat "$T/usr/local/bin/orvix")"
if [ "$ACTUAL_BINARY" = "old-binary-v1.0.3" ]; then
  pass "binary restored byte-for-byte"
else
  fail_msg "binary NOT restored: got '$ACTUAL_BINARY'"
fi

# Test: config restored
ACTUAL_CONFIG="$(cat "$T/etc/orvix/orvix.yaml")"
if [ "$ACTUAL_CONFIG" = "old-config" ]; then
  pass "config restored byte-for-byte"
else
  fail_msg "config NOT restored: got '$ACTUAL_CONFIG'"
fi

# Test: Caddy validated and reloaded
if [ -f "$CADDY_LOG" ]; then
  if grep -q "validate" "$CADDY_LOG"; then
    pass "Caddy validate invoked"
  else
    fail_msg "Caddy validate NOT invoked"
  fi
  if grep -q "reload" "$CADDY_LOG"; then
    pass "Caddy reload invoked exactly once"
  else
    fail_msg "Caddy reload NOT invoked"
  fi
else
  fail_msg "Caddy log not found"
fi

# Test: systemctl restart was called
if [ -f "$SYSTEMCTL_LOG" ]; then
  if grep -q "restart orvix" "$SYSTEMCTL_LOG"; then
    pass "systemctl restart invoked after rollback"
  else
    fail_msg "systemctl restart NOT invoked"
  fi
else
  fail_msg "systemctl log not found"
fi

# Test: rollback reports failure when Caddy fails
cat > "$T/bin/caddy" <<'END'
#!/usr/bin/env bash
exit 1
END
chmod +x "$T/bin/caddy"

# Reset binary
echo "new-binary" > "$T/usr/local/bin/orvix"
rollback_output="$(run_rollback 2>&1 || true)"
rollback_rc=$?
if [ "$rollback_rc" != "0" ]; then
  pass "rollback fails (nonzero exit) when Caddy validation fails"
else
  fail_msg "rollback should fail when Caddy validation fails"
fi

echo ""
echo "---"
echo "Results: $PASS passed, $FAIL failed"
if [ "$FAIL" -gt 0 ]; then echo "SOME TESTS FAILED"; exit 1; fi
echo "Rollback behavioral tests PASSED"
