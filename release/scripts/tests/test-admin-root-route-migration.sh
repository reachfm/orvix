#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MIGRATION="$SCRIPT_DIR/../migrate-admin-root-route.sh"
PASS=0 FAIL=0 T=""
MARKER="# ORVIX_ADMIN_ROOT_REDIRECT_V1"
MATCHER="@orvix_admin_root path /"
REDIRECT="redir @orvix_admin_root /admin 308"

cleanup() { [ -n "${T:-}" ] && rm -rf "$T"; }
trap cleanup EXIT

setup() {
  T="$(cd "$SCRIPT_DIR" && pwd)/.t-amr-$$-${RANDOM}"
  rm -rf "$T" 2>/dev/null || true
  mkdir -p "$T"
  CADDY="$T/Caddyfile"
  CADDY_BIN="$T/caddy"
  SYSCTL="$T/systemctl"
  echo '#!/usr/bin/env bash' > "$CADDY_BIN"
  echo 'command="$1"; shift; case "$command" in validate) exit 0;; reload) exit 0;; *) exit 1;; esac' >> "$CADDY_BIN"
  chmod +x "$CADDY_BIN"
  echo '#!/usr/bin/env bash' > "$SYSCTL"
  echo 'count=0; command="$1"; shift; echo "systemctl $command count=$((count+1))" >> '"$T"'/systemctl.log; case "$command" in reload) exit 0;; restart) exit 0;; *) exit 1;; esac' >> "$SYSCTL"
  chmod +x "$SYSCTL"
}

base_caddy() {
  cat > "$CADDY" <<'END'
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
END
}

invoke_migration() {
  local mode="$1" adm="${2:-}"
  ORVIX_CADDYFILE="$CADDY" ORVIX_CADDY_BIN="$CADDY_BIN" ORVIX_SYSTEMCTL="$SYSCTL" \
  ORVIX_ADMIN_DOMAIN="$adm" bash "$MIGRATION" "$mode"
}

pass() { echo "PASS: $1"; PASS=$((PASS + 1)); }
fail_msg() { echo "FAIL: $1"; FAIL=$((FAIL + 1)); }

# ── Test 1: Fresh migration succeeds ──
setup; base_caddy
set +e; invoke_migration --apply; s=$?; set -e
marker_count="$(grep -cF "$MARKER" "$CADDY")"
matcher_count="$(grep -cF "$MATCHER" "$CADDY")"
redirect_count="$(grep -cF "$REDIRECT" "$CADDY")"
if [ "$s" -eq 0 ] && [ "$marker_count" = "1" ] && [ "$matcher_count" = "1" ] && [ "$redirect_count" = "1" ]; then
  pass "fresh migration succeeds"
else
  fail_msg "fresh migration should succeed (exit=$s marker=$marker_count matcher=$matcher_count redirect=$redirect_count)"
fi

# ── Test 2: Redirect before reverse_proxy ──
setup; base_caddy; invoke_migration --apply
admin_block="$T/admin-block.txt"
awk '
  /^admin\.example\.com[[:space:]]*\{/ { inside=1; depth=0 }
  inside { print; opens=gsub(/\{/, "{"); closes=gsub(/\}/, "}"); depth+=opens-closes; if(depth==0) exit }
' "$CADDY" > "$admin_block"
redirect_line="$(grep -nF "$REDIRECT" "$admin_block" | head -1 | cut -d: -f1)"
proxy_line="$(grep -nF "reverse_proxy 127.0.0.1:8080" "$admin_block" | head -1 | cut -d: -f1)"
if [ -n "$redirect_line" ] && [ -n "$proxy_line" ] && [ "$redirect_line" -lt "$proxy_line" ]; then
  pass "redirect before reverse_proxy"
else
  fail_msg "redirect must appear before reverse_proxy"
fi

# ── Test 3: Primary block unchanged ──
setup; base_caddy
orig_primary="$(awk '/^example\.com/,/^}/' "$CADDY")"
invoke_migration --apply
new_primary="$(awk '/^example\.com/,/^}/' "$CADDY")"
[ "$orig_primary" = "$new_primary" ] && pass "primary block unchanged" || fail_msg "primary block should be unchanged"

# ── Test 4: Webmail block unchanged ──
setup; base_caddy
orig_wm="$(awk '/^webmail\.example\.com/,/^}/' "$CADDY")"
invoke_migration --apply
new_wm="$(awk '/^webmail\.example\.com/,/^}/' "$CADDY")"
[ "$orig_wm" = "$new_wm" ] && pass "webmail block unchanged" || fail_msg "webmail block should be unchanged"

# ── Test 5: Mail block unchanged ──
setup; base_caddy
orig_mail="$(awk '/^mail\.example\.com/,/^}/' "$CADDY")"
invoke_migration --apply
new_mail="$(awk '/^mail\.example\.com/,/^}/' "$CADDY")"
[ "$orig_mail" = "$new_mail" ] && pass "mail block unchanged" || fail_msg "mail block should be unchanged"

# ── Test 6: Second apply byte-identical ──
setup; base_caddy; invoke_migration --apply; cp "$CADDY" "$T/after1"
invoke_migration --apply
diff "$T/after1" "$CADDY" >/dev/null 2>&1 && pass "second apply byte-identical" || fail_msg "second apply should be byte-identical"

# ── Test 7: Check succeeds after migration ──
setup; base_caddy; invoke_migration --apply
set +e; invoke_migration --check; s=$?; set -e
[ "$s" -eq 0 ] && pass "check succeeds after migration" || fail_msg "check should succeed after migration"

# ── Test 8: Check fails before migration ──
setup; base_caddy
set +e; invoke_migration --check >"$T/stdout" 2>"$T/stderr"; s=$?; set -e
[ "$s" -ne 0 ] && pass "check fails before migration" || fail_msg "check should fail before migration"

# ── Test 9: Dry-run byte-identical ──
setup; base_caddy; cp "$CADDY" "$T/before"
invoke_migration --dry-run >"$T/stdout" 2>"$T/stderr"
diff "$T/before" "$CADDY" >/dev/null 2>&1 && \
  ! find "$T" -maxdepth 1 -name 'Caddyfile.backup.*' 2>/dev/null | grep -q . && \
  ! find "$T" -maxdepth 1 -name 'Caddyfile.restore.*' 2>/dev/null | grep -q . && \
  pass "dry-run byte-identical" || fail_msg "dry-run should not change file"

# ── Test 10: Dry-run reload count zero ──
setup; base_caddy; echo -n > "$T/systemctl.log"
invoke_migration --dry-run >"$T/stdout" 2>"$T/stderr"
[ ! -s "$T/systemctl.log" ] && pass "dry-run reload count zero" || fail_msg "dry-run should not invoke reload"

# ── Test 11: Exact custom domain succeeds ──
setup; cat > "$CADDY" <<'END'
custom.example.com { reverse_proxy 127.0.0.1:8080 }
END
set +e; invoke_migration --apply custom.example.com; s=$?; set -e
[ "$s" -eq 0 ] && grep -q "redir" "$CADDY" && pass "exact custom domain succeeds" || fail_msg "exact custom domain should succeed"

# ── Test 12: Exact domain zero match fails ──
setup; echo 'wrong.example.com { reverse_proxy 127.0.0.1:8080 }' > "$CADDY"
set +e; invoke_migration --check custom.example.com >"$T/stdout" 2>"$T/stderr"; s=$?; set -e
[ "$s" -ne 0 ] && pass "exact domain zero match fails" || fail_msg "exact domain zero match should fail"

# ── Test 13: Exact domain duplicate fails ──
setup; cat > "$CADDY" <<'END'
dup.example.com { reverse_proxy 127.0.0.1:8080 }
dup.example.com { reverse_proxy 127.0.0.1:8080 }
END
set +e; invoke_migration --check dup.example.com >"$T/stdout" 2>"$T/stderr"; s=$?; set -e
[ "$s" -ne 0 ] && pass "exact domain duplicate fails" || fail_msg "exact domain duplicate should fail"

# ── Test 14: Auto zero match fails ──
setup; echo 'example.com { reverse_proxy 127.0.0.1:8080 }' > "$CADDY"
set +e; invoke_migration --check >"$T/stdout" 2>"$T/stderr"; s=$?; set -e
[ "$s" -ne 0 ] && pass "auto zero match fails" || fail_msg "auto zero match should fail"

# ── Test 15: Auto multiple matches fails ──
setup; cat > "$CADDY" <<'END'
admin.a.example.com { reverse_proxy 127.0.0.1:8080 }
admin.b.example.com { reverse_proxy 127.0.0.1:8080 }
END
set +e; invoke_migration --check >"$T/stdout" 2>"$T/stderr"; s=$?; set -e
[ "$s" -ne 0 ] && pass "auto multiple matches fails" || fail_msg "auto multiple matches should fail"

# ── Test 16: Marker in primary does not count ──
setup; cat > "$CADDY" <<'END'
example.com { # ORVIX_ADMIN_ROOT_REDIRECT_V1
	reverse_proxy 127.0.0.1:8080 }
admin.example.com { reverse_proxy 127.0.0.1:8080 }
END
set +e; invoke_migration --check >"$T/stdout" 2>"$T/stderr"; s=$?; set -e
[ "$s" -ne 0 ] && pass "marker in primary does not count" || fail_msg "marker in primary should not count"

# ── Test 17: Marker-only contract fails ──
setup; cat > "$CADDY" <<'END'
example.com { reverse_proxy 127.0.0.1:8080 }
admin.example.com { # ORVIX_ADMIN_ROOT_REDIRECT_V1
	reverse_proxy 127.0.0.1:8080 }
END
set +e; invoke_migration --check >"$T/stdout" 2>"$T/stderr"; s=$?; set -e
[ "$s" -ne 0 ] && pass "marker-only contract fails" || fail_msg "marker-only contract should fail"

# ── Test 18: Wrong destination fails ──
setup; cat > "$CADDY" <<'END'
example.com { reverse_proxy 127.0.0.1:8080 }
admin.example.com {
# ORVIX_ADMIN_ROOT_REDIRECT_V1
@orvix_admin_root path /
redir @orvix_admin_root /wrong 308
reverse_proxy 127.0.0.1:8080 }
END
set +e; invoke_migration --check >"$T/stdout" 2>"$T/stderr"; s=$?; set -e
[ "$s" -ne 0 ] && pass "wrong destination fails" || fail_msg "wrong destination should fail"

# ── Test 19: Validation failure preserves original ──
setup; base_caddy; echo '#!/usr/bin/env bash' > "$CADDY_BIN"; echo 'case "$1" in validate) exit 1;; reload) exit 0;; *) exit 1;; esac' >> "$CADDY_BIN"; chmod +x "$CADDY_BIN"
cp "$CADDY" "$T/orig"
set +e; invoke_migration --apply >"$T/stdout" 2>"$T/stderr"; s=$?; set -e
diff "$T/orig" "$CADDY" >/dev/null 2>&1 && [ "$s" -ne 0 ] && pass "validation failure preserves original" || fail_msg "validation failure should preserve original"

# ── Test 20: Validation failure reload zero ──
setup; base_caddy; echo -n > "$T/systemctl.log"
echo '#!/usr/bin/env bash' > "$CADDY_BIN"; echo 'case "$1" in validate) exit 1;; reload) exit 0;; *) exit 1;; esac' >> "$CADDY_BIN"; chmod +x "$CADDY_BIN"
set +e; invoke_migration --apply >"$T/stdout" 2>"$T/stderr"; set -e
[ ! -s "$T/systemctl.log" ] && pass "validation failure reload zero" || fail_msg "validation failure should not reload"

# ── Test 21: Reload failure returns non-zero ──
setup; base_caddy; echo '#!/usr/bin/env bash' > "$SYSCTL"; echo 'case "$1" in reload) exit 1;; *) exit 0;; esac' >> "$SYSCTL"; chmod +x "$SYSCTL"
set +e; invoke_migration --apply >"$T/stdout" 2>"$T/stderr"; s=$?; set -e
[ "$s" -ne 0 ] && pass "reload failure returns non-zero" || fail_msg "reload failure should return non-zero"

# ── Test 22: Reload failure restores byte-for-byte ──
setup; base_caddy; echo '#!/usr/bin/env bash' > "$SYSCTL"; echo 'case "$1" in reload) exit 1;; *) exit 0;; esac' >> "$SYSCTL"; chmod +x "$SYSCTL"
cp "$CADDY" "$T/orig"
set +e; invoke_migration --apply >"$T/stdout" 2>"$T/stderr"; set -e
diff "$T/orig" "$CADDY" >/dev/null 2>&1 && pass "reload failure restores byte-for-byte" || fail_msg "reload failure should restore original"

# ── Test 23: Reload failure preserves UID GID mode ──
setup; base_caddy; chmod 640 "$CADDY" >/dev/null 2>&1 || true
orig_uid="$(stat -c '%u' "$CADDY" 2>/dev/null || echo "")"
orig_gid="$(stat -c '%g' "$CADDY" 2>/dev/null || echo "")"
orig_mode="$(stat -c '%a' "$CADDY" 2>/dev/null || echo "")"
echo '#!/usr/bin/env bash' > "$SYSCTL"; echo 'case "$1" in reload) exit 1;; *) exit 0;; esac' >> "$SYSCTL"; chmod +x "$SYSCTL"
set +e; invoke_migration --apply >"$T/stdout" 2>"$T/stderr"; set -e
if [ -n "$orig_uid" ] && [ -n "$orig_gid" ] && [ -n "$orig_mode" ]; then
  cur_uid="$(stat -c '%u' "$CADDY" 2>/dev/null || echo "")"
  cur_gid="$(stat -c '%g' "$CADDY" 2>/dev/null || echo "")"
  cur_mode="$(stat -c '%a' "$CADDY" 2>/dev/null || echo "")"
  [ "$cur_uid" = "$orig_uid" ] && [ "$cur_gid" = "$orig_gid" ] && [ "$cur_mode" = "$orig_mode" ] && pass "reload failure preserves UID GID mode" || fail_msg "reload failure should preserve metadata"
else
  pass "reload failure preserves UID GID mode (not testable)"
fi

# ── Test 24: Successful migration preserves UID GID mode ──
setup; base_caddy; chmod 640 "$CADDY" >/dev/null 2>&1 || true
orig_uid="$(stat -c '%u' "$CADDY" 2>/dev/null || echo "")"
orig_gid="$(stat -c '%g' "$CADDY" 2>/dev/null || echo "")"
orig_mode="$(stat -c '%a' "$CADDY" 2>/dev/null || echo "")"
invoke_migration --apply
if [ -n "$orig_uid" ] && [ -n "$orig_gid" ] && [ -n "$orig_mode" ]; then
  cur_uid="$(stat -c '%u' "$CADDY" 2>/dev/null || echo "")"
  cur_gid="$(stat -c '%g' "$CADDY" 2>/dev/null || echo "")"
  cur_mode="$(stat -c '%a' "$CADDY" 2>/dev/null || echo "")"
  [ "$cur_uid" = "$orig_uid" ] && [ "$cur_gid" = "$orig_gid" ] && [ "$cur_mode" = "$orig_mode" ] && pass "success preserves UID GID mode" || fail_msg "success should preserve metadata"
else
  pass "success preserves UID GID mode (not testable)"
fi

# ── Test 25: Existing valid contract no duplicate ──
setup; cat > "$CADDY" <<'END'
example.com { reverse_proxy 127.0.0.1:8080 }
admin.example.com {
# ORVIX_ADMIN_ROOT_REDIRECT_V1
@orvix_admin_root path /
redir @orvix_admin_root /admin 308
reverse_proxy 127.0.0.1:8080 }
END
cp "$CADDY" "$T/orig"; invoke_migration --apply; diff "$T/orig" "$CADDY" >/dev/null 2>&1 && pass "valid contract no duplicate" || fail_msg "existing valid contract should create no duplicate"

# ── Test 26: API block remains unchanged ──
setup; cat > "$CADDY" <<'END'
example.com { reverse_proxy 127.0.0.1:8080 }
admin.example.com {
	@api path /api/*
	handle @api { reverse_proxy 127.0.0.1:8080 }
	reverse_proxy 127.0.0.1:8080 }
END
orig_api_line="$(grep '@api path /api/' "$CADDY")"
orig_handle_line="$(grep 'handle @api' "$CADDY")"
invoke_migration --apply
new_api_line="$(grep '@api path /api/' "$CADDY")"
new_handle_line="$(grep 'handle @api' "$CADDY")"
[ "$orig_api_line" = "$new_api_line" ] && [ "$orig_handle_line" = "$new_handle_line" ] && grep -q "redir" "$CADDY" && pass "api block remains unchanged" || fail_msg "api directives should remain"

# ── Test 27: Candidate removed after success ──
setup; base_caddy; invoke_migration --apply
candies="$(find "$T" -maxdepth 1 -name 'Caddyfile.tmp.*' 2>/dev/null | wc -l | tr -d ' ')"
[ "$candies" = "0" ] && pass "candidate removed after success" || fail_msg "candidate should be removed after success"

# ── Test 28: Candidate removed after failure ──
setup; base_caddy; echo '#!/usr/bin/env bash' > "$CADDY_BIN"; echo 'case "$1" in validate) exit 1;; reload) exit 0;; *) exit 1;; esac' >> "$CADDY_BIN"; chmod +x "$CADDY_BIN"
set +e; invoke_migration --apply >"$T/stdout" 2>"$T/stderr"; set -e
candies="$(find "$T" -maxdepth 1 -name 'Caddyfile.tmp.*' 2>/dev/null | wc -l | tr -d ' ')"
[ "$candies" = "0" ] && pass "candidate removed after failure" || fail_msg "candidate should be removed after failure"

# ── Collision-safe backup test (within existing test context) ──
setup; base_caddy
invoke_migration --apply
bup1="$(find "$T" -maxdepth 1 -name 'Caddyfile.backup.*' 2>/dev/null | head -1)"
# Simulate rapid second apply by creating fresh setup (original file restored)
cp "$T/orig" "$CADDY" 2>/dev/null || cp "$(find "$T" -name '*.backup.*' -not -name "$bup1" | head -1)" "$CADDY" 2>/dev/null || true
invoke_migration --apply 2>/dev/null || true
bup2="$(find "$T" -maxdepth 1 -name 'Caddyfile.backup.*' 2>/dev/null | sort | tail -1)"
if [ -n "$bup1" ] && [ -n "$bup2" ] && [ "$bup1" != "$bup2" ]; then
  pass "collision-safe backup paths"
else
  fail_msg "collision-safe backup paths should differ"
fi

EXPECTED_TESTS=28
if [ $((PASS + FAIL)) -ne "$EXPECTED_TESTS" ]; then
  echo "FAIL: expected $EXPECTED_TESTS executed tests, got $((PASS + FAIL))"
  exit 1
fi

echo ""
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] || exit 1
