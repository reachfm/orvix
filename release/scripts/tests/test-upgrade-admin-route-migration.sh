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
  mkdir -p "$T/bin" "$T/usr/share/orvix/scripts" "$T/release/scripts" "$T/etc/caddy" "$T/logs"
  cp "$MIG" "$T/usr/share/orvix/scripts/"
  cp "$MIG" "$T/release/scripts/"
  echo '#!/usr/bin/env bash' > "$T/bin/caddy"; echo 'exit 0' >> "$T/bin/caddy"; chmod +x "$T/bin/caddy"
  echo '#!/usr/bin/env bash' > "$T/bin/systemctl"; echo 'case "$1" in reload) exit 0;; *) exit 0;; esac' >> "$T/bin/systemctl"; chmod +x "$T/bin/systemctl"
  cat > "$T/etc/caddy/Caddyfile" <<'END'
example.com {
    reverse_proxy 127.0.0.1:8080
}
admin.example.com {
    reverse_proxy 127.0.0.1:8080
}
END
  TEST_LOG="$T/logs/log.txt"
  TEST_REPORT="$T/logs/report.txt"
  source "$LIB"
}

pass() { echo "PASS: $1"; PASS=$((PASS + 1)); }
fail_msg() { echo "FAIL: $1"; FAIL=$((FAIL + 1)); }

log() { printf '%s\n' "$*" >> "$TEST_LOG"; }
report() { printf '%s|%s\n' "$1" "$2" >> "$TEST_REPORT"; }

run_it() {
  local dry_run="$1"
  local caddyfile="${2:-$T/etc/caddy/Caddyfile}"
  local caddy_bin="${3:-$T/bin/caddy}"
  local systemctl_bin="${4:-$T/bin/systemctl}"
  local admin_domain="${5:-}"

  : > "$TEST_LOG"
  : > "$TEST_REPORT"

  ORVIX_SOURCE_DIR="$T" \
  ORVIX_CADDYFILE="$caddyfile" \
  ORVIX_CADDY_BIN="$caddy_bin" \
  ORVIX_SYSTEMCTL="$systemctl_bin" \
  ORVIX_ADMIN_DOMAIN="$admin_domain" \
  DRY_RUN="$dry_run" \
  run_admin_route_migration
}

# Test 1: DRY_RUN=1 invokes --dry-run
setup
# Replace first candidate with recording mock
cat > "$T/release/scripts/migrate-admin-root-route.sh" <<'ENDSCRIPT'
#!/usr/bin/env bash
echo "mode=$1" >> "$T/logs/child-env.txt"
echo "caddyfile=$ORVIX_CADDYFILE" >> "$T/logs/child-env.txt"
echo "caddy_bin=$ORVIX_CADDY_BIN" >> "$T/logs/child-env.txt"
echo "systemctl=$ORVIX_SYSTEMCTL" >> "$T/logs/child-env.txt"
echo "admin_domain=${ORVIX_ADMIN_DOMAIN:-}" >> "$T/logs/child-env.txt"
exit 0
ENDSCRIPT
chmod +x "$T/release/scripts/migrate-admin-root-route.sh"
set +e; run_it 1; s=$?; set -e
if [ "$s" -eq 0 ] && grep -q "mode=--dry-run" "$T/logs/child-env.txt"; then
  pass "dry-run invokes child with --dry-run"
else
  fail_msg "dry-run should invoke child with --dry-run"
fi

# Test 2: DRY_RUN=0 invokes --apply
setup
cat > "$T/release/scripts/migrate-admin-root-route.sh" <<'ENDSCRIPT'
#!/usr/bin/env bash
echo "mode=$1" >> "$T/logs/child-env.txt"
exit 0
ENDSCRIPT
chmod +x "$T/release/scripts/migrate-admin-root-route.sh"
set +e; run_it 0; s=$?; set -e
if [ "$s" -eq 0 ] && grep -q "mode=--apply" "$T/logs/child-env.txt"; then
  pass "normal mode invokes child with --apply"
else
  fail_msg "normal mode should invoke child with --apply"
fi

# Test 3: Verify upgrade.sh call order
UPGRADE_PATH="$SCRIPT_DIR/../../upgrade.sh"
if [ -f "$UPGRADE_PATH" ]; then
  pline="$(grep -n 'preflight_backup' "$UPGRADE_PATH" | head -1 | cut -d: -f1)"
  iline="$(grep -n 'install_and_restart' "$UPGRADE_PATH" | head -1 | cut -d: -f1)"
  paline="$(grep -n 'propagate_assets' "$UPGRADE_PATH" | head -1 | cut -d: -f1)"
  mline="$(grep -n 'run_admin_route_migration' "$UPGRADE_PATH" | head -1 | cut -d: -f1)"
  rline="$(grep -n 'systemctl restart orvix' "$UPGRADE_PATH" | head -1 | cut -d: -f1)"
  if [ -n "$pline" ] && [ -n "$iline" ] && [ -n "$paline" ] && [ -n "$mline" ] && \
     [ "$pline" -lt "$iline" ] && [ "$paline" -lt "$mline" ] && [ "$mline" -lt "$rline" ]; then
    pass "upgrade.sh call order correct"
  else
    fail_msg "upgrade.sh call order incorrect"
  fi
else
  fail_msg "upgrade.sh not found"
fi

# Test 4: Caddyfile present, migration missing → fail closed
setup; rm -f "$T/release/scripts/migrate-admin-root-route.sh" "$T/usr/share/orvix/scripts/migrate-admin-root-route.sh"
set +e; run_it 0; s=$?; set -e
[ "$s" -ne 0 ] && grep -q "migration script missing" "$TEST_REPORT" && pass "Caddyfile present missing migration fails" || fail_msg "should fail with migration script missing"

# Test 5: Caddy binary present, migration missing → fail closed
setup; rm -f "$T/release/scripts/migrate-admin-root-route.sh" "$T/usr/share/orvix/scripts/migrate-admin-root-route.sh" "$T/etc/caddy/Caddyfile"
set +e; run_it 0; s=$?; set -e
[ "$s" -ne 0 ] && grep -q "migration script missing" "$TEST_REPORT" && pass "Caddy present missing migration fails" || fail_msg "Caddy present missing migration should fail"

# Test 6: No Caddy + no Caddyfile → warn and continue
setup; rm -f "$T/bin/caddy" "$T/etc/caddy/Caddyfile"
set +e; run_it 0; s=$?; set -e
[ "$s" -eq 0 ] && grep -q "neither Caddy binary nor Caddyfile found" "$TEST_LOG" && grep -q "admin route migration skipped" "$TEST_REPORT" && pass "no Caddy no Caddyfile warns" || fail_msg "should warn and continue"

# Test 7: Invalid-Bash migration fails before execution
setup; rm -f "$T/release/scripts/migrate-admin-root-route.sh"
echo "this is not valid bash {{{{" > "$T/release/scripts/migrate-admin-root-route.sh"
set +e; run_it 0; s=$?; set -e
[ "$s" -ne 0 ] && grep -q "invalid migration script" "$TEST_REPORT" && pass "invalid bash migration fails" || fail_msg "invalid bash migration should fail"

# Test 8: Migration failure makes library fail
setup; rm -f "$T/release/scripts/migrate-admin-root-route.sh"
echo '#!/usr/bin/env bash; exit 1' > "$T/release/scripts/migrate-admin-root-route.sh"; chmod +x "$T/release/scripts/migrate-admin-root-route.sh"
set +e; run_it 0; s=$?; set -e
[ "$s" -ne 0 ] && grep -q "admin root redirect migration failed" "$TEST_REPORT" && pass "migration failure library returns failure" || fail_msg "migration failure should propagate"

# Test 9: Reload failure returns failure, restores Caddyfile
setup; cat > "$T/etc/caddy/Caddyfile" <<'END'
example.com {
    reverse_proxy 127.0.0.1:8080
}
admin.example.com {
    reverse_proxy 127.0.0.1:8080
}
END
echo '#!/usr/bin/env bash' > "$T/bin/systemctl"; echo 'case "$1" in reload) exit 1;; *) exit 0;; esac' >> "$T/bin/systemctl"; chmod +x "$T/bin/systemctl"
cp "$MIG" "$T/release/scripts/"
cp "$T/etc/caddy/Caddyfile" "$T/orig-caddy"
set +e; run_it 0; s=$?; set -e
[ "$s" -ne 0 ] && diff "$T/orig-caddy" "$T/etc/caddy/Caddyfile" >/dev/null 2>&1 && \
  grep -q "admin root redirect migration failed" "$TEST_REPORT" && \
  pass "reload failure restores and reports failure" || fail_msg "reload failure should restore and report failure"

# Test 10: Custom ORVIX_CADDYFILE reaches child
setup
T2="$(mktemp -d)"
echo -e "example.com {\n    reverse_proxy 127.0.0.1:8080\n}\nadmin.example.com {\n    reverse_proxy 127.0.0.1:8080\n}" > "$T2/Caddyfile"
cat > "$T/release/scripts/migrate-admin-root-route.sh" <<'ENDSCRIPT'
#!/usr/bin/env bash
echo "caddyfile=$ORVIX_CADDYFILE" >> "$T/logs/child-env.txt"
exit 0
ENDSCRIPT
chmod +x "$T/release/scripts/migrate-admin-root-route.sh"
set +e; run_it 0 "$T2/Caddyfile"; s=$?; set -e
[ "$s" -eq 0 ] && grep -q "caddyfile=$T2/Caddyfile" "$T/logs/child-env.txt" && pass "custom Caddyfile reaches migration" || fail_msg "custom Caddyfile should reach migration"
rm -rf "$T2"

# Test 11: Custom env vars reach child
setup; rm -f "$T/usr/share/orvix/scripts/migrate-admin-root-route.sh"
cat > "$T/release/scripts/migrate-admin-root-route.sh" <<'ENDSCRIPT'
#!/usr/bin/env bash
echo "bin=$ORVIX_CADDY_BIN" >> "$T/logs/child-env.txt"
echo "sc=$ORVIX_SYSTEMCTL" >> "$T/logs/child-env.txt"
echo "ad=$ORVIX_ADMIN_DOMAIN" >> "$T/logs/child-env.txt"
exit 0
ENDSCRIPT
chmod +x "$T/release/scripts/migrate-admin-root-route.sh"
set +e; run_it 0 "$T/etc/caddy/Caddyfile" "/my/caddy" "/my/systemctl" "myadmin.example.com"; s=$?; set -e
[ "$s" -eq 0 ] && grep -q "bin=/my/caddy" "$T/logs/child-env.txt" && grep -q "sc=/my/systemctl" "$T/logs/child-env.txt" && grep -q "ad=myadmin.example.com" "$T/logs/child-env.txt" && pass "custom env vars reach migration" || fail_msg "custom env vars should reach migration"

# Test 12: Dry-run leaves Caddyfile identical
setup; cat > "$T/etc/caddy/Caddyfile" <<'END'
example.com {
    reverse_proxy 127.0.0.1:8080
}
admin.example.com {
    @api path /api/*
    handle @api { reverse_proxy 127.0.0.1:8080 }
    reverse_proxy 127.0.0.1:8080
}
END
cp "$MIG" "$T/release/scripts/"
cp "$T/etc/caddy/Caddyfile" "$T/orig"
set +e; run_it 1; s=$?; set -e
[ "$s" -eq 0 ] && diff "$T/orig" "$T/etc/caddy/Caddyfile" >/dev/null 2>&1 && \
  grep -q "path /api/" "$T/etc/caddy/Caddyfile" && pass "dry-run preserves Caddyfile" || fail_msg "dry-run should preserve Caddyfile"

EXPECTED_TESTS=12
if [ $((PASS + FAIL)) -ne "$EXPECTED_TESTS" ]; then
  echo "FAIL: expected $EXPECTED_TESTS executed tests, got $((PASS + FAIL))"
  exit 1
fi

echo ""
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] || exit 1
