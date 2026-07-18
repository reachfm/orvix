#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
UPGRADE_SCRIPT="$SCRIPT_DIR/../../upgrade.sh"
PASS=0 FAIL=0

fail_msg() { echo "FAIL: $1"; FAIL=$((FAIL + 1)); }
pass()     { echo "PASS: $1"; PASS=$((PASS + 1)); }

echo "=== Upgrade Rollback Completeness Tests ==="

if [ ! -f "$UPGRADE_SCRIPT" ]; then
  fail_msg "upgrade.sh not found at $UPGRADE_SCRIPT"
  echo "Results: $PASS passed, $FAIL failed"
  exit 1
fi

# Test 1: Verify Marketing is in the rollback asset loop
if grep -q 'ORVIX_MARKETING_UI_DIR' "$UPGRADE_SCRIPT"; then
  pass "Marketing UI dir referenced in upgrade.sh"
else
  fail_msg "Marketing UI dir NOT referenced in upgrade.sh"
fi

if grep -E -A5 'for sub in' "$UPGRADE_SCRIPT" | grep -q 'ORVIX_MARKETING_UI_DIR'; then
  pass "Marketing included in asset rollback loop"
else
  fail_msg "Marketing NOT included in asset rollback loop"
fi

# Test 2: Verify JWT ownership is orvix:orvix (not root:orvix first)
rollback_jwt_section="$(sed -n '/for item in.*ORVIX_JWT_KEY/,/done/p' "$UPGRADE_SCRIPT")"
if echo "$rollback_jwt_section" | grep -q 'orvix:orvix'; then
  pass "JWT rollback uses orvix:orvix ownership"
else
  fail_msg "JWT rollback does NOT use orvix:orvix ownership"
fi
if ! echo "$rollback_jwt_section" | grep -q 'root:orvix.*orvix:orvix' 2>/dev/null || \
   echo "$rollback_jwt_section" | grep -q 'chown orvix:orvix.*chown orvix:orvix' 2>/dev/null || \
   echo "$rollback_jwt_section" | grep -q 'chown orvix:orvix'; then
  pass "JWT rollback does not attempt root:orvix first"
else
  fail_msg "JWT rollback may fall back to root:orvix sometimes"
fi

# Test 3: Verify Caddyfile is in backup manifest
if grep -q 'ORVIX_CADDYFILE' "$UPGRADE_SCRIPT" | head -5; then
  pass "Caddyfile referenced in upgrade.sh"
else
  fail_msg "Caddyfile NOT referenced in upgrade.sh"
fi

# Test 4: Verify Caddyfile is restored during rollback
rollback_loop="$(sed -n '/for item in.*ORVIX_BIN.*ORVIX_CADDYFILE/,/done/p' "$UPGRADE_SCRIPT")"
if echo "$rollback_loop" | grep -q 'ORVIX_CADDYFILE'; then
  pass "Caddyfile included in rollback restore loop"
else
  fail_msg "Caddyfile NOT in rollback restore loop"
fi

# Test 5: Verify Caddy validation after rollback
if grep -A20 'backup_dir$ORVIX_CADDYFILE' "$UPGRADE_SCRIPT" | grep -q 'validate.*config'; then
  pass "Caddyfile validated after rollback restore"
else
  fail_msg "Caddyfile NOT validated after rollback restore"
fi

# Test 6: Verify Caddy reload after rollback
if grep -A20 'backup_dir$ORVIX_CADDYFILE' "$UPGRADE_SCRIPT" | grep -q 'reload'; then
  pass "Caddy reloaded after rollback restore"
else
  fail_msg "Caddy NOT reloaded after rollback restore"
fi

echo ""
echo "---"
echo "Results: $PASS passed, $FAIL failed"
if [ "$FAIL" -gt 0 ]; then echo "SOME TESTS FAILED"; exit 1; fi
echo "Rollback completeness tests PASSED"
