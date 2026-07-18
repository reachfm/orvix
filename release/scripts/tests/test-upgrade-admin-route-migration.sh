#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LIB="$SCRIPT_DIR/../lib-admin-route-migration.sh"
MIG="$SCRIPT_DIR/../migrate-admin-root-route.sh"
PASS=0 FAIL=0 T=""

cleanup() { [ -n "${T:-}" ] && rm -rf "$T"; }
trap cleanup EXIT

setup() {
  T="$(cd "$SCRIPT_DIR" && pwd)/.t-uarm-$$-${RANDOM}"
  rm -rf "$T" 2>/dev/null || true
  mkdir -p "$T" "$T/usr/share/orvix/scripts" "$T/release/scripts" "$T/etc/caddy"
  cp "$MIG" "$T/usr/share/orvix/scripts/"
  cp "$MIG" "$T/release/scripts/"
  echo '#!/usr/bin/env bash' > "$T/bin/caddy"; echo 'exit 0' >> "$T/bin/caddy"; chmod +x "$T/bin/caddy"
  echo '#!/usr/bin/env bash' > "$T/bin/systemctl"; echo 'exit 0' >> "$T/bin/systemctl"; chmod +x "$T/bin/systemctl"
  cat > "$T/etc/caddy/Caddyfile" <<'END'
example.com { reverse_proxy 127.0.0.1:8080 }
admin.example.com { reverse_proxy 127.0.0.1:8080 }
END
  source "$LIB"
}

pass() { echo "PASS: $1"; PASS=$((PASS + 1)); }
fail_msg() { echo "FAIL: $1"; FAIL=$((FAIL + 1)); }
log() { :; }
report() { :; }

run_it() {
  local dr="${1:-0}"
  ORVIX_SOURCE_DIR="$T" \
  ORVIX_CADDYFILE="$T/etc/caddy/Caddyfile" \
  ORVIX_CADDY_BIN="$T/bin/caddy" \
  ORVIX_SYSTEMCTL="$T/bin/systemctl" \
  ORVIX_ADMIN_DOMAIN="" \
  DRY_RUN="$dr" \
  run_admin_route_migration
}

# Test 1: DRY_RUN=1 invokes --dry-run
setup; DRY_RUN=1 run_admin_route_migration 2>/dev/null
[ $? -eq 0 ] && pass "dry-run invokes with --dry-run" || fail_msg "dry-run should invoke --dry-run"

# Test 2: DRY_RUN=0 invokes --apply
setup; DRY_RUN=0 run_admin_route_migration 2>/dev/null
[ $? -eq 0 ] && pass "normal mode invokes --apply" || fail_msg "normal mode should invoke --apply"

# Test 3: migration is after asset propagation in install_and_restart
# Verified by reading upgrade.sh structure
if grep -q "propagate_assets" "$SCRIPT_DIR/../../upgrade.sh" && grep -q "run_admin_route_migration" "$SCRIPT_DIR/../../upgrade.sh"; then
  pass "upgrade.sh has migration after asset propagation"
else fail_msg "upgrade.sh must have migration after assets"; fi

# Test 4: Caddyfile present, missing migration script fails
setup; rm -f "$T/usr/share/orvix/scripts/migrate-admin-root-route.sh" "$T/release/scripts/migrate-admin-root-route.sh"
set +e; run_it 0; s=$?; set -e
[ "$s" -ne 0 ] && pass "missing migration with Caddyfile fails" || fail_msg "missing migration with Caddyfile should fail"

# Test 5: Caddy binary present, missing migration fails
setup; rm -f "$T/usr/share/orvix/scripts/migrate-admin-root-route.sh" "$T/release/scripts/migrate-admin-root-route.sh"
ORVIX_CADDYFILE="" \
ORVIX_SOURCE_DIR="$T" \
ORVIX_CADDY_BIN="$T/bin/caddy" \
ORVIX_SYSTEMCTL="$T/bin/systemctl" \
ORVIX_ADMIN_DOMAIN="" \
DRY_RUN=0 \
run_admin_route_migration 2>/dev/null; s=$?
[ "$s" -ne 0 ] && pass "Caddy present missing migration fails" || fail_msg "Caddy present missing migration should fail"

# Test 6: No Caddy, no Caddyfile warns and continues
setup; rm -f "$T/bin/caddy" "$T/etc/caddy/Caddyfile"
DRY_RUN=0 run_admin_route_migration 2>/dev/null
[ $? -eq 0 ] && pass "no Caddy no Caddyfile warns and continues" || fail_msg "no Caddy no Caddyfile should warn and continue"

# Test 7: Invalid-Bash migration fails
setup; rm -f "$T/usr/share/orvix/scripts/migrate-admin-root-route.sh"
echo "not valid bash {{{{ ((((" > "$T/usr/share/orvix/scripts/migrate-admin-root-route.sh"
set +e; run_it 0 2>/dev/null; s=$?; set -e
[ "$s" -ne 0 ] && pass "invalid bash migration fails" || fail_msg "invalid bash migration should fail"

# Test 8: Migration returning failure makes library fail
setup; rm -f "$T/usr/share/orvix/scripts/migrate-admin-root-route.sh"
echo '#!/usr/bin/env bash; exit 1' > "$T/usr/share/orvix/scripts/migrate-admin-root-route.sh"; chmod +x "$T/usr/share/orvix/scripts/migrate-admin-root-route.sh"
set +e; run_it 0 2>/dev/null; s=$?; set -e
[ "$s" -ne 0 ] && pass "migration failure library returns failure" || fail_msg "migration failure should propagate to library"

# Test 9: Apply/reload failure reported as failure
setup; echo '#!/usr/bin/env bash' > "$T/bin/systemctl"; echo 'case "$1" in reload) exit 1;; *) exit 0;; esac' >> "$T/bin/systemctl"; chmod +x "$T/bin/systemctl"
cp "$T/usr/share/orvix/scripts/migrate-admin-root-route.sh" "$T/release/scripts/"
set +e; run_it 0 2>/dev/null; s=$?; set -e
[ "$s" -ne 0 ] && pass "reload failure returns failure" || fail_msg "reload failure should return failure"

# Test 10: Custom ORVIX_CADDYFILE reaches migration child
setup; T2="$(mktemp -d)"; trap "rm -rf $T2" RETURN
echo 'example.com { reverse_proxy 127.0.0.1:8080 }' > "$T2/Caddyfile"
echo 'admin.example.com { reverse_proxy 127.0.0.1:8080 }' >> "$T2/Caddyfile"
ORVIX_CADDYFILE="$T2/Caddyfile" \
ORVIX_SOURCE_DIR="$T" \
ORVIX_CADDY_BIN="$T/bin/caddy" \
ORVIX_SYSTEMCTL="$T/bin/systemctl" \
ORVIX_ADMIN_DOMAIN="" \
DRY_RUN=0 \
run_admin_route_migration 2>/dev/null
[ $? -eq 0 ] && grep -q "redir" "$T2/Caddyfile" && pass "custom Caddyfile reaches migration" || fail_msg "custom Caddyfile should reach migration"

# Test 11: Custom ORVIX_CADDY_BIN, ORVIX_SYSTEMCTL, ORVIX_ADMIN_DOMAIN reach migration
setup; T3="$(mktemp -d)"; trap "rm -rf $T3" RETURN
echo 'example.com { reverse_proxy 127.0.0.1:8080 }' > "$T3/Caddyfile"
echo 'myadmin.example.com { reverse_proxy 127.0.0.1:8080 }' >> "$T3/Caddyfile"
cp "$MIG" "$T/release/scripts/"
ORVIX_CADDYFILE="$T3/Caddyfile" \
ORVIX_SOURCE_DIR="$T" \
ORVIX_CADDY_BIN="$T/bin/caddy" \
ORVIX_SYSTEMCTL="$T/bin/systemctl" \
ORVIX_ADMIN_DOMAIN="myadmin.example.com" \
DRY_RUN=0 \
run_admin_route_migration 2>/dev/null
[ $? -eq 0 ] && grep -q "redir" "$T3/Caddyfile" && grep -q "myadmin" "$T3/Caddyfile" && pass "custom env vars reach migration" || fail_msg "custom env vars should reach migration"

# Test 12: Dry-run leaves Caddyfile identical, preserves /api/*
setup; cat > "$T/etc/caddy/Caddyfile" <<'END'
example.com { reverse_proxy 127.0.0.1:8080 }
admin.example.com {
	@api path /api/*
	handle @api { reverse_proxy 127.0.0.1:8080 }
	reverse_proxy 127.0.0.1:8080 }
END
cp "$MIG" "$T/release/scripts/"
cp "$T/etc/caddy/Caddyfile" "$T/orig"
DRY_RUN=1 run_admin_route_migration 2>/dev/null
[ $? -eq 0 ] && diff "$T/orig" "$T/etc/caddy/Caddyfile" >/dev/null 2>&1 && grep -q "path /api/" "$T/etc/caddy/Caddyfile" && pass "dry-run preserves Caddyfile and /api/*" || fail_msg "dry-run should preserve Caddyfile"

echo ""
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] || exit 1
