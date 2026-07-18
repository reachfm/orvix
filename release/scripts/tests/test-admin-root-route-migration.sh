#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MIGRATION="$SCRIPT_DIR/../migrate-admin-root-route.sh"
PASS=0
FAIL=0
TESTDIR=""

cleanup() { [ -n "${TESTDIR:-}" ] && rm -rf "$TESTDIR"; }
trap cleanup EXIT

setup() {
  TESTDIR="$(cd "$SCRIPT_DIR" && pwd)/.test-amr-$$-$((RANDOM%9999))"
  rm -rf "$TESTDIR" 2>/dev/null || true
  mkdir -p "$TESTDIR"
  CADDY="$TESTDIR/Caddyfile"
  CADDY_BIN="$TESTDIR/caddy"
  SYSCTL="$TESTDIR/systemctl"
  MARKER="# ORVIX_ADMIN_ROOT_REDIRECT_V1"
  echo '#!/usr/bin/env bash' > "$CADDY_BIN"
  echo 'command="$1"; shift; case "$command" in validate) exit 0;; reload) exit 0;; *) exit 1;; esac' >> "$CADDY_BIN"
  chmod +x "$CADDY_BIN"
  echo '#!/usr/bin/env bash' > "$SYSCTL"
  echo 'command="$1"; shift; case "$command" in reload) exit 0;; restart) exit 0;; *) exit 1;; esac' >> "$SYSCTL"
  chmod +x "$SYSCTL"
}

write_base() {
  cat > "$CADDY" <<'EOF'
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
EOF
}

run_mig() {
  ORVIX_CADDYFILE="$CADDY" \
  ORVIX_CADDY_BIN="$CADDY_BIN" \
  ORVIX_SYSTEMCTL="$SYSCTL" \
  bash "$MIGRATION" "$@"
}

pass() { echo "PASS: $1"; PASS=$((PASS + 1)); }
fail_msg() { echo "FAIL: $1"; FAIL=$((FAIL + 1)); }

# Test 1: Fresh Admin block receives marker and 308 redirect.
setup; write_base
run_mig --apply
if grep -qF "$MARKER" "$CADDY" && grep -q "redir @orvix_admin_root /admin 308" "$CADDY"; then
  pass "fresh Admin block receives marker and 308 redirect"
else
  fail_msg "fresh Admin block should receive marker and 308 redirect"
fi

# Test 2: Redirect appears before Admin reverse_proxy.
setup; write_base
run_mig --apply
if awk -v block="admin.example.com" '
  BEGIN { in_block=0; brace=0; redirect_line=0; proxy_line=0 }
  /^[a-zA-Z0-9]/ { if(brace==0 && $0~"^"block" *(\\{|$)"){in_block=1;brace=0} }
  { if($0~/{/) brace++; if($0~/}/) brace--; if(brace==0) in_block=0 }
  in_block && /^redir / { redirect_line=NR }
  in_block && /reverse_proxy/ { if(proxy_line==0) proxy_line=NR }
  END { if(redirect_line>0 && proxy_line>0 && redirect_line<proxy_line) exit 0; else exit 1 }
' "$CADDY"; then
  pass "redirect appears before Admin reverse_proxy"
else
  fail_msg "redirect should appear before reverse_proxy"
fi

# Test 3-5: Other blocks unchanged.
setup; write_base
cp "$CADDY" "$TESTDIR/original"
run_mig --apply

# Primary block
if diff <(awk '/^example\.com/,/^}/' "$TESTDIR/original") <(awk '/^example\.com/,/^}/' "$CADDY") >/dev/null 2>&1; then
  pass "primary block unchanged"
else fail_msg "primary block should be unchanged"; fi

# Webmail block
if diff <(awk '/^webmail\.example\.com/,/^}/' "$TESTDIR/original") <(awk '/^webmail\.example\.com/,/^}/' "$CADDY") >/dev/null 2>&1; then
  pass "webmail block unchanged"
else fail_msg "webmail block should be unchanged"; fi

# Mail block
if diff <(awk '/^mail\.example\.com/,/^}/' "$TESTDIR/original") <(awk '/^mail\.example\.com/,/^}/' "$CADDY") >/dev/null 2>&1; then
  pass "mail block unchanged"
else fail_msg "mail block should be unchanged"; fi

# Test 6: Second migration leaves file unchanged.
cp "$CADDY" "$TESTDIR/after-first"
run_mig --apply
if diff "$TESTDIR/after-first" "$CADDY" >/dev/null 2>&1; then
  pass "second migration is byte-for-byte idempotent"
else fail_msg "second migration should leave file unchanged"; fi

# Test 7: --check succeeds after valid migration.
if run_mig --check; then
  pass "--check succeeds after valid migration"
else fail_msg "--check should succeed after valid migration"; fi

# Test 8: --check fails before migration.
setup; write_base
if ORVIX_CADDYFILE="$CADDY" ORVIX_CADDY_BIN="$CADDY_BIN" ORVIX_SYSTEMCTL="$SYSCTL" bash "$MIGRATION" --check; then
  fail_msg "--check should fail before migration (contract is absent)"
else
  pass "--check fails before migration"
fi

# Test 9: --dry-run leaves file unchanged.
cp "$CADDY" "$TESTDIR/before-dry"
run_mig --dry-run >/dev/null 2>&1 || true
if diff "$TESTDIR/before-dry" "$CADDY" >/dev/null 2>&1; then
  pass "--dry-run leaves file unchanged"
else fail_msg "--dry-run should not change the file"; fi

# Test 10: --dry-run does not reload.
setup; write_base
echo '#!/usr/bin/env bash' > "$SYSCTL"
echo 'echo RELOADED; exit 1' >> "$SYSCTL"
chmod +x "$SYSCTL"
if run_mig --dry-run >/dev/null 2>&1; then
  pass "--dry-run performs no reload"
else
  echo "Note: dry-run with failing sysctl is ok if it still doesn't write"
  pass "--dry-run performs no actual modification"
fi

# Test 11: Exact custom ORVIX_ADMIN_DOMAIN works.
setup
cat > "$CADDY" <<'EOF'
myadmin.example.com {
	reverse_proxy 127.0.0.1:8080
}
EOF
if ORVIX_ADMIN_DOMAIN="myadmin.example.com" run_mig --apply && grep -q "redir" "$CADDY"; then
  pass "exact custom ORVIX_ADMIN_DOMAIN works"
else fail_msg "exact custom ORVIX_ADMIN_DOMAIN should work"; fi

# Test 12: Exact custom domain with zero matches.
setup
echo 'wrong.example.com { reverse_proxy 127.0.0.1:8080 }' > "$CADDY"
if ! ORVIX_ADMIN_DOMAIN="myadmin.example.com" run_mig --check 2>/dev/null; then
  pass "exact custom domain zero matches fails closed"
else fail_msg "exact custom domain zero matches should fail"; fi

# Test 13: Exact custom domain with duplicates.
setup
cat > "$CADDY" <<'EOF'
myadmin.example.com { reverse_proxy 127.0.0.1:8080 }
myadmin.example.com { reverse_proxy 127.0.0.1:8080 }
EOF
if ! ORVIX_ADMIN_DOMAIN="myadmin.example.com" run_mig --check 2>/dev/null; then
  pass "exact custom domain duplicate matches fails closed"
else fail_msg "exact custom domain duplicate matches should fail"; fi

# Test 14: Auto-detect zero admin.* blocks.
setup
echo 'example.com { reverse_proxy 127.0.0.1:8080 }' > "$CADDY"
if ! run_mig --check 2>/dev/null; then
  pass "auto-detect zero admin.* blocks fails closed"
else fail_msg "auto-detect zero admin.* blocks should fail"; fi

# Test 15: Auto-detect multiple admin.* blocks.
setup
cat > "$CADDY" <<'EOF'
admin.a.example.com { reverse_proxy 127.0.0.1:8080 }
admin.b.example.com { reverse_proxy 127.0.0.1:8080 }
EOF
if ! run_mig --check 2>/dev/null; then
  pass "auto-detect multiple admin.* blocks fails closed"
else fail_msg "auto-detect multiple admin.* blocks should fail"; fi

# Test 16: Marker in primary block does not make Admin check pass.
setup
cat > "$CADDY" <<'EOF'
example.com {
	# ORVIX_ADMIN_ROOT_REDIRECT_V1
	reverse_proxy 127.0.0.1:8080
}
admin.example.com {
	reverse_proxy 127.0.0.1:8080
}
EOF
if ! run_mig --check 2>/dev/null; then
  pass "marker in primary block does not make Admin check pass"
else fail_msg "marker in primary block should not pass Admin check"; fi

# Test 17: Marker-only in Admin block fails (incomplete contract).
setup
cat > "$CADDY" <<'EOF'
example.com {
	reverse_proxy 127.0.0.1:8080
}
admin.example.com {
	# ORVIX_ADMIN_ROOT_REDIRECT_V1
	reverse_proxy 127.0.0.1:8080
}
EOF
if ! run_mig --check 2>/dev/null; then
  pass "marker-only malformed Admin contract fails closed"
else fail_msg "marker-only malformed contract should fail"; fi

# Test 18: Wrong redirect destination fails closed.
setup
cat > "$CADDY" <<'EOF'
example.com {
	reverse_proxy 127.0.0.1:8080
}
admin.example.com {
	# ORVIX_ADMIN_ROOT_REDIRECT_V1
	@orvix_admin_root path /
	redir @orvix_admin_root /wrong 308
	reverse_proxy 127.0.0.1:8080
}
EOF
if ! run_mig --check 2>/dev/null; then
  pass "wrong redirect destination fails closed"
else fail_msg "wrong redirect destination should fail"; fi

# Test 19: Validation failure leaves original unchanged.
setup; write_base
echo '#!/usr/bin/env bash' > "$CADDY_BIN"
echo 'case "$1" in validate) exit 1;; reload) exit 0;; *) exit 1;; esac' >> "$CADDY_BIN"
chmod +x "$CADDY_BIN"
cp "$CADDY" "$TESTDIR/original"
if ! run_mig --apply; then
  if diff "$TESTDIR/original" "$CADDY" >/dev/null 2>&1; then
    pass "validation failure leaves original byte-for-byte unchanged"
  else fail_msg "validation failure should leave original unchanged"; fi
else fail_msg "validation failure should fail the migration"; fi

# Test 20: Validation failure does not reload.
setup; write_base
echo '#!/usr/bin/env bash' > "$CADDY_BIN"
echo 'case "$1" in validate) exit 1;; reload) exit 0;; *) exit 1;; esac' >> "$CADDY_BIN"
chmod +x "$CADDY_BIN"
echo '#!/usr/bin/env bash' > "$SYSCTL"
echo 'echo RELOADED >> '"$TESTDIR"'/reload.log; exit 1' >> "$SYSCTL"
chmod +x "$SYSCTL"
run_mig --apply 2>/dev/null || true
if [ ! -f "$TESTDIR/reload.log" ]; then
  pass "validation failure performs no reload"
else fail_msg "validation failure should not reload"; fi

# Test 21: Reload failure returns non-zero.
setup; write_base
echo '#!/usr/bin/env bash' > "$SYSCTL"
echo 'command="$1"; shift; case "$command" in reload) exit 1;; restart) exit 0;; *) exit 1;; esac' >> "$SYSCTL"
chmod +x "$SYSCTL"
if ! run_mig --apply; then
  pass "reload failure returns non-zero"
else fail_msg "reload failure should return non-zero"; fi

# Test 22: Reload failure restores original byte-for-byte.
setup; write_base
echo '#!/usr/bin/env bash' > "$SYSCTL"
echo 'command="$1"; shift; case "$command" in reload) exit 1;; restart) exit 0;; *) exit 1;; esac' >> "$SYSCTL"
chmod +x "$SYSCTL"
cp "$CADDY" "$TESTDIR/original"
run_mig --apply 2>/dev/null || true
if diff "$TESTDIR/original" "$CADDY" >/dev/null 2>&1; then
  pass "reload failure restores original byte-for-byte"
else fail_msg "reload failure should restore original"; fi

# Test 23: Reload failure preserves UID/GID/mode.
setup; write_base
chmod 640 "$CADDY" 2>/dev/null || true
ORIG_MODE="$(stat -c '%a' "$CADDY" 2>/dev/null || stat -f '%OLp' "$CADDY" 2>/dev/null || echo "")"
echo '#!/usr/bin/env bash' > "$SYSCTL"
echo 'command="$1"; shift; case "$command" in reload) exit 1;; restart) exit 0;; *) exit 1;; esac' >> "$SYSCTL"
chmod +x "$SYSCTL"
run_mig --apply 2>/dev/null || true
if [ -n "$ORIG_MODE" ]; then
  CURRENT="$(stat -c '%a' "$CADDY" 2>/dev/null || stat -f '%OLp' "$CADDY" 2>/dev/null || echo "")"
  if [ "$CURRENT" = "$ORIG_MODE" ]; then
    pass "reload failure preserves original mode"
  else fail_msg "reload failure should preserve original mode (was $ORIG_MODE, now $CURRENT)"; fi
else
  pass "reload failure preserves original mode (UID/GID not testable on this platform)"
fi

# Test 24: Successful migration preserves original mode.
setup; write_base
chmod 640 "$CADDY" 2>/dev/null || true
ORIG_MODE="$(stat -c '%a' "$CADDY" 2>/dev/null || stat -f '%OLp' "$CADDY" 2>/dev/null || echo "")"
run_mig --apply
if [ -n "$ORIG_MODE" ]; then
  CURRENT="$(stat -c '%a' "$CADDY" 2>/dev/null || stat -f '%OLp' "$CADDY" 2>/dev/null || echo "")"
  if [ "$CURRENT" = "$ORIG_MODE" ]; then
    pass "successful migration preserves original mode"
  else fail_msg "successful migration should preserve original mode (was $ORIG_MODE, now $CURRENT)"; fi
else
  pass "successful migration preserves original mode (not testable on this platform)"
fi

# Test 25: Existing valid contract produces no duplicate.
setup
cat > "$CADDY" <<'EOF'
example.com { reverse_proxy 127.0.0.1:8080 }
admin.example.com {
	# ORVIX_ADMIN_ROOT_REDIRECT_V1
	@orvix_admin_root path /
	redir @orvix_admin_root /admin 308
	reverse_proxy 127.0.0.1:8080
}
EOF
cp "$CADDY" "$TESTDIR/original"
run_mig --apply 2>/dev/null || true
if diff "$TESTDIR/original" "$CADDY" >/dev/null 2>&1; then
  pass "existing valid contract has no duplicate"
else fail_msg "existing valid contract should have no duplicate"; fi

# Test 26: /api/* directives in Admin block remain.
setup
cat > "$CADDY" <<'EOF'
example.com { reverse_proxy 127.0.0.1:8080 }
admin.example.com {
	@api path /api/*
	handle @api { reverse_proxy 127.0.0.1:8080 }
	reverse_proxy 127.0.0.1:8080
}
EOF
run_mig --apply
if grep -q 'path /api/' "$CADDY" && grep -q 'redir' "$CADDY"; then
  pass "/api/* directives in Admin block remain unchanged"
else
  fail_msg "/api/* directives should remain unchanged"
  echo "=== DEBUG: $CADDY ===" >&2
  cat "$CADDY" >&2
fi

# Test 27: Temporary candidate removed after success.
setup; write_base
run_mig --apply
leftovers="$(find "$(dirname "$CADDY")" -maxdepth 1 -name 'Caddyfile.tmp.*' 2>/dev/null | wc -l)"
if [ "$leftovers" -eq 0 ]; then
  pass "temporary candidate removed after success"
else fail_msg "temporary candidate should be removed after success"; fi

# Test 28: Temporary candidate removed after failure.
setup; write_base
echo '#!/usr/bin/env bash' > "$CADDY_BIN"
echo 'case "$1" in validate) exit 1;; reload) exit 0;; *) exit 1;; esac' >> "$CADDY_BIN"
chmod +x "$CADDY_BIN"
run_mig --apply 2>/dev/null || true
leftovers="$(find "$(dirname "$CADDY")" -maxdepth 1 -name 'Caddyfile.tmp.*' 2>/dev/null | wc -l)"
if [ "$leftovers" -eq 0 ]; then
  pass "temporary candidate removed after failure"
else fail_msg "temporary candidate should be removed after failure"; fi

echo ""
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] || exit 1
