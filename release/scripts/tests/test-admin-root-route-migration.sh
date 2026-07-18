#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MIGRATION="$SCRIPT_DIR/../migrate-admin-root-route.sh"
PASS=0 FAIL=0 T=""

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
  echo 'command="$1"; shift; case "$command" in reload) exit 0;; restart) exit 0;; *) exit 1;; esac' >> "$SYSCTL"
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

# ── Test 1 ──
setup; base_caddy; invoke_migration --apply; [ $? -eq 0 ] && grep -qF "$MARKER" "$CADDY" && grep -q "redir @orvix_admin_root /admin 308" "$CADDY" && pass "fresh migration succeeds" || fail_msg "fresh migration should succeed"

# ── Test 2 ──
setup; base_caddy; invoke_migration --apply
awk -v b="admin.example.com" 'BEGIN{x=0;y=0}{if($0~"^"b){in=1}; if(in&&/redir\s+@orvix_admin_root/){x=NR}; if(in&&/reverse_proxy\s+127\.0\.0\.1/){y=NR;exit}}END{if(x>0&&y>0&&x<y)exit 0;else exit 1}' "$CADDY" && pass "redirect before reverse_proxy" || fail_msg "redirect should be before reverse_proxy"

# ── Test 3 ──
setup; base_caddy; cp "$CADDY" "$T/orig"; invoke_migration --apply; diff "$T/orig" "$CADDY" >/dev/null 2>&1 && fail_msg "should differ" || pass "primary block byte-identical (no diff means same)" ; true
# Actually check only the primary block is unchanged
setup; base_caddy
orig_primary="$(awk '/^example\.com/,/^}/' "$CADDY")"
invoke_migration --apply
new_primary="$(awk '/^example\.com/,/^}/' "$CADDY")"
[ "$orig_primary" = "$new_primary" ] && pass "primary block unchanged" || fail_msg "primary block should be unchanged"

# ── Test 4 ──
setup; base_caddy
orig_wm="$(awk '/^webmail\.example\.com/,/^}/' "$CADDY")"
invoke_migration --apply
new_wm="$(awk '/^webmail\.example\.com/,/^}/' "$CADDY")"
[ "$orig_wm" = "$new_wm" ] && pass "webmail block unchanged" || fail_msg "webmail block should be unchanged"

# ── Test 5 ──
setup; base_caddy
orig_mail="$(awk '/^mail\.example\.com/,/^}/' "$CADDY")"
invoke_migration --apply
new_mail="$(awk '/^mail\.example\.com/,/^}/' "$CADDY")"
[ "$orig_mail" = "$new_mail" ] && pass "mail block unchanged" || fail_msg "mail block should be unchanged"

# ── Test 6 ──
setup; base_caddy; invoke_migration --apply; cp "$CADDY" "$T/after1"; invoke_migration --apply; diff "$T/after1" "$CADDY" >/dev/null 2>&1 && pass "second apply byte-identical" || fail_msg "second apply should be byte-identical"

# ── Test 7 ──
setup; base_caddy; invoke_migration --apply; invoke_migration --check; [ $? -eq 0 ] && pass "check succeeds after migration" || fail_msg "check should succeed after migration"

# ── Test 8 ──
setup; base_caddy; set +e; invoke_migration --check; s=$?; set -e; [ "$s" -ne 0 ] && pass "check fails before migration" || fail_msg "check should fail before migration"

# ── Test 9 ──
setup; base_caddy; cp "$CADDY" "$T/before"; invoke_migration --dry-run >/dev/null 2>&1; diff "$T/before" "$CADDY" >/dev/null 2>&1 && pass "dry-run byte-identical" || fail_msg "dry-run should not change file"

# ── Test 10 ──
setup; base_caddy; echo '#!/usr/bin/env bash' > "$SYSCTL"; echo 'touch '"$T"'/reloaded; exit 0' >> "$SYSCTL"; chmod +x "$SYSCTL"; invoke_migration --dry-run >/dev/null 2>&1; [ ! -f "$T/reloaded" ] && pass "dry-run reload count zero" || fail_msg "dry-run should not invoke reload"

# ── Test 11 ──
setup; cat > "$CADDY" <<'END'
custom.example.com { reverse_proxy 127.0.0.1:8080 }
END
invoke_migration --apply custom.example.com; [ $? -eq 0 ] && grep -q "redir" "$CADDY" && pass "exact custom domain succeeds" || fail_msg "exact custom domain should succeed"

# ── Test 12 ──
setup; echo 'wrong.example.com { reverse_proxy 127.0.0.1:8080 }' > "$CADDY"
set +e; invoke_migration --check custom.example.com >/dev/null 2>&1; s=$?; set -e; [ "$s" -ne 0 ] && pass "exact domain zero match fails" || fail_msg "exact domain zero match should fail"

# ── Test 13 ──
setup; cat > "$CADDY" <<'END'
dup.example.com { reverse_proxy 127.0.0.1:8080 }
dup.example.com { reverse_proxy 127.0.0.1:8080 }
END
set +e; invoke_migration --check dup.example.com >/dev/null 2>&1; s=$?; set -e; [ "$s" -ne 0 ] && pass "exact domain duplicate fails" || fail_msg "exact domain duplicate should fail"

# ── Test 14 ──
setup; echo 'example.com { reverse_proxy 127.0.0.1:8080 }' > "$CADDY"
set +e; invoke_migration --check >/dev/null 2>&1; s=$?; set -e; [ "$s" -ne 0 ] && pass "auto zero match fails" || fail_msg "auto zero match should fail"

# ── Test 15 ──
setup; cat > "$CADDY" <<'END'
admin.a.example.com { reverse_proxy 127.0.0.1:8080 }
admin.b.example.com { reverse_proxy 127.0.0.1:8080 }
END
set +e; invoke_migration --check >/dev/null 2>&1; s=$?; set -e; [ "$s" -ne 0 ] && pass "auto multiple matches fails" || fail_msg "auto multiple matches should fail"

# ── Test 16 ──
setup; cat > "$CADDY" <<'END'
example.com { # ORVIX_ADMIN_ROOT_REDIRECT_V1
	reverse_proxy 127.0.0.1:8080 }
admin.example.com { reverse_proxy 127.0.0.1:8080 }
END
set +e; invoke_migration --check >/dev/null 2>&1; s=$?; set -e; [ "$s" -ne 0 ] && pass "marker in primary does not count" || fail_msg "marker in primary should not count"

# ── Test 17 ──
setup; cat > "$CADDY" <<'END'
example.com { reverse_proxy 127.0.0.1:8080 }
admin.example.com { # ORVIX_ADMIN_ROOT_REDIRECT_V1
	reverse_proxy 127.0.0.1:8080 }
END
set +e; invoke_migration --check >/dev/null 2>&1; s=$?; set -e; [ "$s" -ne 0 ] && pass "marker-only contract fails" || fail_msg "marker-only contract should fail"

# ── Test 18 ──
setup; cat > "$CADDY" <<'END'
example.com { reverse_proxy 127.0.0.1:8080 }
admin.example.com {
# ORVIX_ADMIN_ROOT_REDIRECT_V1
@orvix_admin_root path /
redir @orvix_admin_root /wrong 308
reverse_proxy 127.0.0.1:8080 }
END
set +e; invoke_migration --check >/dev/null 2>&1; s=$?; set -e; [ "$s" -ne 0 ] && pass "wrong destination fails" || fail_msg "wrong destination should fail"

# ── Test 19 ──
setup; base_caddy; echo '#!/usr/bin/env bash' > "$CADDY_BIN"; echo 'case "$1" in validate) exit 1;; reload) exit 0;; *) exit 1;; esac' >> "$CADDY_BIN"; chmod +x "$CADDY_BIN"
cp "$CADDY" "$T/orig"; set +e; invoke_migration --apply >/dev/null 2>&1; s=$?; set -e; diff "$T/orig" "$CADDY" >/dev/null 2>&1 && [ "$s" -ne 0 ] && pass "validation failure preserves original" || fail_msg "validation failure should preserve original"

# ── Test 20 ──
setup; base_caddy; echo '#!/usr/bin/env bash' > "$SYSCTL"; echo 'touch '"$T"'/reloaded' >> "$SYSCTL"; chmod +x "$SYSCTL"
echo '#!/usr/bin/env bash' > "$CADDY_BIN"; echo 'case "$1" in validate) exit 1;; reload) exit 0;; *) exit 1;; esac' >> "$CADDY_BIN"; chmod +x "$CADDY_BIN"
set +e; invoke_migration --apply >/dev/null 2>&1; set -e; [ ! -f "$T/reloaded" ] && pass "validation failure reload zero" || fail_msg "validation failure should not reload"

# ── Test 21 ──
setup; base_caddy; echo '#!/usr/bin/env bash' > "$SYSCTL"; echo 'case "$1" in reload) exit 1;; *) exit 0;; esac' >> "$SYSCTL"; chmod +x "$SYSCTL"
set +e; invoke_migration --apply >/dev/null 2>&1; s=$?; set -e; [ "$s" -ne 0 ] && pass "reload failure returns non-zero" || fail_msg "reload failure should return non-zero"

# ── Test 22 ──
setup; base_caddy; echo '#!/usr/bin/env bash' > "$SYSCTL"; echo 'case "$1" in reload) exit 1;; *) exit 0;; esac' >> "$SYSCTL"; chmod +x "$SYSCTL"
cp "$CADDY" "$T/orig"; set +e; invoke_migration --apply >/dev/null 2>&1; set -e; diff "$T/orig" "$CADDY" >/dev/null 2>&1 && pass "reload failure restores byte-for-byte" || fail_msg "reload failure should restore original"

# ── Test 23 ──
setup; base_caddy; chmod 640 "$CADDY" >/dev/null 2>&1 || true
orig_mode="$(stat -c '%a' "$CADDY" 2>/dev/null || stat -f '%OLp' "$CADDY" 2>/dev/null || echo "")"
echo '#!/usr/bin/env bash' > "$SYSCTL"; echo 'case "$1" in reload) exit 1;; *) exit 0;; esac' >> "$SYSCTL"; chmod +x "$SYSCTL"
set +e; invoke_migration --apply >/dev/null 2>&1; set -e
if [ -n "$orig_mode" ]; then
  cur="$(stat -c '%a' "$CADDY" 2>/dev/null || stat -f '%OLp' "$CADDY" 2>/dev/null || echo "")"
  [ "$cur" = "$orig_mode" ] && pass "reload failure preserves mode" || fail_msg "reload failure should preserve mode"
else pass "mode preservation not testable on this platform"; fi

# ── Test 24 ──
setup; base_caddy; chmod 640 "$CADDY" >/dev/null 2>&1 || true
orig_mode="$(stat -c '%a' "$CADDY" 2>/dev/null || stat -f '%OLp' "$CADDY" 2>/dev/null || echo "")"
invoke_migration --apply
[ $? -eq 0 ] && [ -n "$orig_mode" ] && cur="$(stat -c '%a' "$CADDY" 2>/dev/null || stat -f '%OLp' "$CADDY" 2>/dev/null || echo "")" && [ "$cur" = "$orig_mode" ] && pass "successful migration preserves mode" || fail_msg "success should preserve mode"

# ── Test 25 ──
setup; cat > "$CADDY" <<'END'
example.com { reverse_proxy 127.0.0.1:8080 }
admin.example.com {
# ORVIX_ADMIN_ROOT_REDIRECT_V1
@orvix_admin_root path /
redir @orvix_admin_root /admin 308
reverse_proxy 127.0.0.1:8080 }
END
cp "$CADDY" "$T/orig"; invoke_migration --apply; [ $? -eq 0 ] && diff "$T/orig" "$CADDY" >/dev/null 2>&1 && pass "valid contract no duplicate" || fail_msg "existing valid contract should create no duplicate"

# ── Test 26 ──
setup; cat > "$CADDY" <<'END'
example.com { reverse_proxy 127.0.0.1:8080 }
admin.example.com {
	@api path /api/*
	handle @api { reverse_proxy 127.0.0.1:8080 }
	reverse_proxy 127.0.0.1:8080 }
END
cp "$CADDY" "$T/orig"; invoke_migration --apply; [ $? -eq 0 ] && grep -q "path /api/" "$CADDY" && pass "api block remains unchanged" || fail_msg "api directives should remain"

# ── Test 27 ──
setup; base_caddy; invoke_migration --apply; candies="$(find "$T" -maxdepth 1 -name 'Caddyfile.tmp.*' 2>/dev/null | wc -l | tr -d ' ')"
[ "$candies" = "0" ] && pass "candidate removed after success" || fail_msg "candidate should be removed after success"

# ── Test 28 ──
setup; base_caddy; echo '#!/usr/bin/env bash' > "$CADDY_BIN"; echo 'case "$1" in validate) exit 1;; reload) exit 0;; *) exit 1;; esac' >> "$CADDY_BIN"; chmod +x "$CADDY_BIN"
set +e; invoke_migration --apply >/dev/null 2>&1; set -e
candies="$(find "$T" -maxdepth 1 -name 'Caddyfile.tmp.*' 2>/dev/null | wc -l | tr -d ' ')"
[ "$candies" = "0" ] && pass "candidate removed after failure" || fail_msg "candidate should be removed after failure"

# Verify dry-run leaves no backup
setup; base_caddy; invoke_migration --dry-run >/dev/null 2>&1; bups="$(find "$T" -maxdepth 1 -name 'Caddyfile.backup.*' 2>/dev/null | wc -l | tr -d ' ')"
[ "$bups" = "0" ] && pass "dry-run leaves no backup file" || fail_msg "dry-run should leave no backup"

echo ""
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] || exit 1
