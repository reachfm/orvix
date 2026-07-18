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

# ── Test 1: DRY_RUN=1 invokes --dry-run ──
setup
child_log="$T/logs/child-env.txt"
cat > "$T/release/scripts/migrate-admin-root-route.sh" <<EOF
#!/usr/bin/env bash
printf 'mode=%s\n' "\$1" >> "$child_log"
exit 0
EOF
chmod +x "$T/release/scripts/migrate-admin-root-route.sh"
set +e; run_it 1; s=$?; set -e
if [ "$s" -eq 0 ] && [ -f "$child_log" ] && grep -q "mode=--dry-run" "$child_log" && grep -q "green|admin route migration dry-run passed" "$TEST_REPORT"; then
  pass "dry-run invokes child with --dry-run"
else fail_msg "dry-run should invoke child with --dry-run (exit=$s)" ; fi

# ── Test 2: DRY_RUN=0 invokes --apply ──
setup
child_log="$T/logs/child-env.txt"
cat > "$T/release/scripts/migrate-admin-root-route.sh" <<EOF
#!/usr/bin/env bash
printf 'mode=%s\n' "\$1" >> "$child_log"
exit 0
EOF
chmod +x "$T/release/scripts/migrate-admin-root-route.sh"
set +e; run_it 0; s=$?; set -e
if [ "$s" -eq 0 ] && grep -q "mode=--apply" "$child_log" && grep -q "green|admin root redirect migration applied" "$TEST_REPORT"; then
  pass "normal mode invokes child with --apply"
else fail_msg "normal mode should invoke child with --apply (exit=$s)" ; fi

# ── Test 3: Verify upgrade.sh call order ──
UPGRADE_PATH="$SCRIPT_DIR/../../upgrade.sh"
if [ -f "$UPGRADE_PATH" ]; then
  main_body="$(awk '/^main\(\)/,/^}/' "$UPGRADE_PATH" 2>/dev/null)"
  iar_body="$(awk '/^install_and_restart\(\)/,/^}/' "$UPGRADE_PATH" 2>/dev/null)"
  pb_line="$(echo "$main_body" | grep -n 'preflight_backup' | head -1 | cut -d: -f1)"
  ir_line="$(echo "$main_body" | grep -n 'install_and_restart' | head -1 | cut -d: -f1)"
  pa_line="$(echo "$iar_body" | grep -n 'propagate_assets' | head -1 | cut -d: -f1)"
  am_line="$(echo "$iar_body" | grep -n 'run_admin_route_migration' | head -1 | cut -d: -f1)"
  rs_line="$(echo "$iar_body" | grep -n 'systemctl restart orvix' | head -1 | cut -d: -f1)"
  if [ -n "$pb_line" ] && [ -n "$ir_line" ] && [ -n "$pa_line" ] && [ -n "$am_line" ] && [ -n "$rs_line" ] && \
     [ "$pb_line" -lt "$ir_line" ] && [ "$pa_line" -lt "$am_line" ] && [ "$am_line" -lt "$rs_line" ]; then
    pass "upgrade.sh call order correct"
  else fail_msg "upgrade.sh call order incorrect" ; fi
else fail_msg "upgrade.sh not found" ; fi

# ── Test 4: Caddyfile present, migration missing fails ──
setup; rm -f "$T/release/scripts/migrate-admin-root-route.sh" "$T/usr/share/orvix/scripts/migrate-admin-root-route.sh"
set +e; run_it 0; s=$?; set -e
[ "$s" -ne 0 ] && grep -q "migration script missing" "$TEST_REPORT" && pass "Caddyfile present missing migration fails" || fail_msg "should fail when migration missing"

# ── Test 5: Caddy binary present, migration missing fails ──
setup; rm -f "$T/release/scripts/migrate-admin-root-route.sh" "$T/usr/share/orvix/scripts/migrate-admin-root-route.sh" "$T/etc/caddy/Caddyfile"
set +e; run_it 0; s=$?; set -e
[ "$s" -ne 0 ] && grep -q "migration script missing" "$TEST_REPORT" && pass "Caddy present missing migration fails" || fail_msg "Caddy present missing migration should fail"

# ── Test 6: No Caddy + no Caddyfile warns ──
setup; rm -f "$T/bin/caddy" "$T/etc/caddy/Caddyfile"
set +e; run_it 0; s=$?; set -e
[ "$s" -eq 0 ] && grep -q "neither Caddy binary nor Caddyfile found" "$TEST_LOG" && grep -q "admin route migration skipped" "$TEST_REPORT" && pass "no Caddy no Caddyfile warns" || fail_msg "should warn and continue"

# ── Test 7: Invalid-Bash migration fails ──
setup; rm -f "$T/usr/share/orvix/scripts/migrate-admin-root-route.sh"
echo "this is not valid bash {{{{" > "$T/release/scripts/migrate-admin-root-route.sh"
chmod +x "$T/release/scripts/migrate-admin-root-route.sh"
set +e; run_it 0; s=$?; set -e
[ "$s" -ne 0 ] && grep -q "invalid migration script" "$TEST_REPORT" && pass "invalid bash migration fails" || fail_msg "invalid bash migration should fail"

# ── Test 8: Migration failure makes library fail ──
setup; rm -f "$T/release/scripts/migrate-admin-root-route.sh"
cat > "$T/release/scripts/migrate-admin-root-route.sh" <<'ENDSCRIPT'
#!/usr/bin/env bash
exit 1
ENDSCRIPT
chmod +x "$T/release/scripts/migrate-admin-root-route.sh"
set +e; run_it 0; s=$?; set -e
[ "$s" -ne 0 ] && grep -q "red|admin root redirect migration failed" "$TEST_REPORT" && pass "migration failure library returns failure" || fail_msg "migration failure should propagate"

# ── Test 9: Reload failure returns failure, restores Caddyfile ──
setup
cat > "$T/etc/caddy/Caddyfile" <<'END'
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
[ "$s" -ne 0 ] && diff "$T/orig-caddy" "$T/etc/caddy/Caddyfile" >/dev/null 2>&1 && grep -q "red|admin root redirect migration failed" "$TEST_REPORT" && pass "reload failure restores and reports failure" || fail_msg "reload failure should restore and report"

# ── Test 10: Custom ORVIX_CADDYFILE reaches child ──
setup
child_log="$T/logs/child-env.txt"
T2="$(mktemp -d)"
printf 'example.com {\n    reverse_proxy 127.0.0.1:8080\n}\nadmin.example.com {\n    reverse_proxy 127.0.0.1:8080\n}\n' > "$T2/Caddyfile"
cat > "$T/release/scripts/migrate-admin-root-route.sh" <<EOF
#!/usr/bin/env bash
printf 'caddyfile=%s\n' "\$ORVIX_CADDYFILE" >> "$child_log"
exit 0
EOF
chmod +x "$T/release/scripts/migrate-admin-root-route.sh"
set +e; run_it 0 "$T2/Caddyfile"; s=$?; set -e
[ "$s" -eq 0 ] && grep -q "caddyfile=$T2/Caddyfile" "$child_log" && pass "custom Caddyfile reaches migration" || fail_msg "custom Caddyfile should reach migration"
rm -rf "$T2"

# ── Test 11: Custom env vars reach child ──
setup
child_log="$T/logs/child-env.txt"
rm -f "$T/usr/share/orvix/scripts/migrate-admin-root-route.sh"
custom_caddy="$T/bin/custom-caddy"
custom_sysctl="$T/bin/custom-systemctl"
echo '#!/usr/bin/env bash; exit 0' > "$custom_caddy"; chmod +x "$custom_caddy"
echo '#!/usr/bin/env bash; exit 0' > "$custom_sysctl"; chmod +x "$custom_sysctl"
cat > "$T/release/scripts/migrate-admin-root-route.sh" <<EOF
#!/usr/bin/env bash
printf 'caddy_bin=%s\n' "\$ORVIX_CADDY_BIN" >> "$child_log"
printf 'systemctl=%s\n' "\$ORVIX_SYSTEMCTL" >> "$child_log"
printf 'admin_domain=%s\n' "\${ORVIX_ADMIN_DOMAIN:-}" >> "$child_log"
exit 0
EOF
chmod +x "$T/release/scripts/migrate-admin-root-route.sh"
set +e; run_it 0 "$T/etc/caddy/Caddyfile" "$custom_caddy" "$custom_sysctl" "myadmin.example.com"; s=$?; set -e
[ "$s" -eq 0 ] && grep -q "caddy_bin=$custom_caddy" "$child_log" && grep -q "systemctl=$custom_sysctl" "$child_log" && grep -q "admin_domain=myadmin.example.com" "$child_log" && pass "custom env vars reach migration" || fail_msg "custom env vars should reach migration"

# ── Test 12: Dry-run leaves Caddyfile identical ──
setup
cat > "$T/etc/caddy/Caddyfile" <<'END'
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
[ "$s" -eq 0 ] && diff "$T/orig" "$T/etc/caddy/Caddyfile" >/dev/null 2>&1 && grep -q "path /api/" "$T/etc/caddy/Caddyfile" && pass "dry-run preserves Caddyfile" || fail_msg "dry-run should preserve Caddyfile"

EXPECTED_TESTS=12
if [ $((PASS + FAIL)) -ne "$EXPECTED_TESTS" ]; then
  echo "FAIL: expected $EXPECTED_TESTS executed tests, got $((PASS + FAIL))"
  exit 1
fi

echo ""
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] || exit 1
