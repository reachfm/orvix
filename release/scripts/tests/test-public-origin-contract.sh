#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CHECK="$SCRIPT_DIR/../check-public-origin-contract.sh"
TMPDIR=""

cleanup() {
  [ -n "${TMPDIR:-}" ] && rm -rf "$TMPDIR"
}
trap cleanup EXIT

pass_count=0
fail_count=0

pass() {
  echo "PASS: $1"
  pass_count=$((pass_count + 1))
}

fail() {
  echo "FAIL: $1"
  fail_count=$((fail_count + 1))
}

# Test 1: clean canonical tree passes
TMPDIR="$(mktemp -d)"
mkdir -p "$TMPDIR/web/marketing/src/lib"
mkdir -p "$TMPDIR/web/marketing/public"
mkdir -p "$TMPDIR/release/marketing"

echo 'export const CANONICAL = { marketing: "https://orvix.email" };' > "$TMPDIR/web/marketing/src/lib/links.ts"
echo 'Sitemap: https://orvix.email/sitemap.xml' > "$TMPDIR/web/marketing/public/robots.txt"
echo '<div>Orvix email hosting</div>' > "$TMPDIR/release/marketing/index.html"

if (cd "$TMPDIR" && bash "$CHECK"); then
  pass "clean canonical tree passes"
else
  fail "clean canonical tree should pass"
fi

# Test 2: orvix.com in source fails
echo 'const x = "https://orvix.com";' > "$TMPDIR/web/marketing/src/lib/bad.ts"
if (cd "$TMPDIR" && bash "$CHECK"); then
  fail "orvix.com fixture should fail"
else
  pass "orvix.com fixture fails"
fi

# Test 3: app.orvix.com in public fails
rm -f "$TMPDIR/web/marketing/src/lib/bad.ts"
echo '<a href="https://app.orvix.com/login">Sign in</a>' > "$TMPDIR/web/marketing/public/test.html"
if (cd "$TMPDIR" && bash "$CHECK"); then
  fail "app.orvix.com fixture should fail"
else
  pass "app.orvix.com fixture fails"
fi

# Test 4: .email origins pass
rm -f "$TMPDIR/web/marketing/public/test.html"
echo '<a href="https://admin.orvix.email/admin/login">Sign in</a>' > "$TMPDIR/web/marketing/public/cta.html"
echo '<a href="https://webmail.orvix.email/webmail">Webmail</a>' >> "$TMPDIR/web/marketing/public/cta.html"
echo '<a href="https://docs.orvix.email">Docs</a>' >> "$TMPDIR/web/marketing/public/cta.html"
if (cd "$TMPDIR" && bash "$CHECK"); then
  pass ".email origins pass"
else
  fail ".email origins should pass"
fi

echo ""
echo "Results: $pass_count passed, $fail_count failed"
[ "$fail_count" -eq 0 ] || exit 1
