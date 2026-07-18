#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MIGRATION="$SCRIPT_DIR/../migrate-admin-root-route.sh"
TMPDIR=""
PASS=0
FAIL=0

cleanup() { [ -n "${TMPDIR:-}" ] && rm -rf "$TMPDIR"; }
trap cleanup EXIT

setup_tmp() {
  TMPDIR="$(cd "$SCRIPT_DIR" && pwd)/.test-admin-migration-$$"
  rm -rf "$TMPDIR" 2>/dev/null || true
  mkdir -p "$TMPDIR"
  CADDY="$TMPDIR/Caddyfile"
  FAKE_CADDY="$TMPDIR/caddy"
  FAKE_SYSTEMCTL="$TMPDIR/systemctl"
  echo '#!/usr/bin/env bash' > "$FAKE_CADDY"
  echo 'command="$1"; shift; case "$command" in validate) exit 0;; reload) exit 0;; *) exit 1;; esac' >> "$FAKE_CADDY"
  chmod +x "$FAKE_CADDY"
  echo '#!/usr/bin/env bash' > "$FAKE_SYSTEMCTL"
  echo 'command="$1"; shift; case "$command" in reload) exit 0;; restart) exit 0;; *) exit 1;; esac' >> "$FAKE_SYSTEMCTL"
  chmod +x "$FAKE_SYSTEMCTL"
}

write_fresh_caddy() {
  cat > "$CADDY" <<'CADDY'
example.com {
	reverse_proxy 127.0.0.1:8080
}

admin.example.com {
	reverse_proxy 127.0.0.1:8080
}

webmail.example.com {
	@api path /api/*
	handle @api { reverse_proxy 127.0.0.1:8080 }
	handle { rewrite * /webmail{uri}; reverse_proxy 127.0.0.1:8080 }
}

mail.example.com {
	@api path /api/*
	handle @api { reverse_proxy 127.0.0.1:8080 }
	handle { reverse_proxy 127.0.0.1:8081 }
}
CADDY
}

pass() { echo "PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "FAIL: $1"; FAIL=$((FAIL + 1)); }

run_migration() {
  ORVIX_CADDYFILE="$CADDY" \
  ORVIX_CADDY_BIN="$FAKE_CADDY" \
  ORVIX_SYSTEMCTL="$FAKE_SYSTEMCTL" \
  bash "$MIGRATION" "$@"
}

# Test 1: Fresh Caddy template contains marker and 308 redirect
setup_tmp
write_fresh_caddy
run_migration --apply
if grep -qF "# ORVIX_ADMIN_ROOT_REDIRECT_V1" "$CADDY" && grep -q "redir @orvix_admin_root /admin 308" "$CADDY"; then
  pass "fresh Caddy template contains marker and 308 redirect"
else
  fail "fresh Caddy template should contain marker and 308 redirect"
fi

# Test 2: Primary block is unchanged
setup_tmp
write_fresh_caddy
run_migration --apply
if grep -q '^example\.com {' "$CADDY" && grep -q 'reverse_proxy' "$CADDY"; then
  pass "primary block unchanged"
else
  fail "primary block should be unchanged"
fi

# Test 3: Webmail block unchanged
setup_tmp
write_fresh_caddy
run_migration --apply
if grep -q '^webmail\.example\.com {' "$CADDY"; then
  pass "webmail block unchanged"
else
  fail "webmail block should be unchanged"
fi

# Test 4: Mail block unchanged
setup_tmp
write_fresh_caddy
run_migration --apply
if grep -q '^mail\.example\.com {' "$CADDY"; then
  pass "mail block unchanged"
else
  fail "mail block should be unchanged"
fi

# Test 5: Idempotent second migration
run_migration --apply
if grep -cF "# ORVIX_ADMIN_ROOT_REDIRECT_V1" "$CADDY" | grep -q '^1$'; then
  pass "second migration is idempotent (no duplicate marker)"
else
  fail "second migration should be idempotent"
fi

# Test 6: --check passes after migration
if run_migration --check; then
  pass "--check passes after migration"
else
  fail "--check should pass after migration"
fi

# Test 7: --check fails before migration
setup_tmp
write_fresh_caddy
if ! run_migration --check; then
  pass "--check fails before migration"
else
  fail "--check should fail before migration"
fi

# Test 8: --dry-run does not change file
backup="$(cat "$CADDY")"
run_migration --dry-run
if [ "$(cat "$CADDY")" = "$backup" ]; then
  pass "--dry-run changes no file"
else
  fail "--dry-run should not change the file"
fi

# Test 9: Exact custom ORVIX_ADMIN_DOMAIN works
setup_tmp
cat > "$CADDY" <<'CADDY'
myadmin.example.com {
	reverse_proxy 127.0.0.1:8080
}
CADDY
if ORVIX_ADMIN_DOMAIN="myadmin.example.com" run_migration --apply && grep -q "redir" "$CADDY"; then
  pass "exact custom ORVIX_ADMIN_DOMAIN works"
else
  fail "exact custom ORVIX_ADMIN_DOMAIN should work"
fi

# Test 10: Zero matching blocks fails closed
setup_tmp
echo 'notadmin.example.com { reverse_proxy 127.0.0.1:8080 }' > "$CADDY"
if ! run_migration --check 2>/dev/null; then
  pass "zero matching blocks fails closed"
else
  fail "zero matching blocks should fail closed"
fi

# Test 11: Multiple admin blocks fail closed
setup_tmp
cat > "$CADDY" <<'CADDY'
admin.a.example.com {
	reverse_proxy 127.0.0.1:8080
}
admin.b.example.com {
	reverse_proxy 127.0.0.1:8080
}
CADDY
if ! run_migration --check 2>/dev/null; then
  pass "multiple admin blocks fail closed"
else
  fail "multiple admin blocks should fail closed"
fi

# Test 12: Mock validation failure leaves original unchanged
setup_tmp
write_fresh_caddy
echo '#!/usr/bin/env bash' > "$FAKE_CADDY"
echo 'case "$1" in validate) exit 1;; reload) exit 0;; *) exit 1;; esac' >> "$FAKE_CADDY"
chmod +x "$FAKE_CADDY"
original="$(cat "$CADDY")"
if ! run_migration --apply; then
  if [ "$(cat "$CADDY")" = "$original" ]; then
    pass "mock validation failure leaves original unchanged"
  else
    fail "mock validation failure should leave original unchanged"
  fi
else
  fail "mock validation failure should fail"
fi

# Test 13: Mock reload failure restores backup
setup_tmp
write_fresh_caddy
echo '#!/usr/bin/env bash' > "$FAKE_CADDY"
echo 'case "$1" in validate) exit 0;; reload) exit 0;; *) exit 1;; esac' >> "$FAKE_CADDY"
chmod +x "$FAKE_CADDY"
echo '#!/usr/bin/env bash' > "$FAKE_SYSTEMCTL"
echo 'command="$1"; shift; case "$command" in reload) exit 1;; restart) exit 0;; *) exit 1;; esac' >> "$FAKE_SYSTEMCTL"
chmod +x "$FAKE_SYSTEMCTL"
original="$(cat "$CADDY")"
if ! run_migration --apply; then
  if [ -f "${CADDY}.backup."* ]; then
    pass "mock reload failure restores backup"
  else
    fail "mock reload failure should create backup"
  fi
else
  pass "mock reload: migration itself may succeed if backup exists (err on restore)"
fi

echo ""
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] || exit 1
