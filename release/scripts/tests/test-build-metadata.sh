#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
PASS=0 FAIL=0 T=""

cleanup() { [ -n "${T:-}" ] && rm -rf "$T"; }
trap cleanup EXIT

fail_msg() { echo "FAIL: $1"; FAIL=$((FAIL + 1)); }
pass()     { echo "PASS: $1"; PASS=$((PASS + 1)); }

echo "=== Build Metadata Test ==="

# Test 1: Default build has correct metadata (not dev defaults)
echo ""
echo "--- Default build ---"
cd "$REPO_ROOT"
make clean 2>/dev/null || true
make build 2>&1
if [ ! -f "build/orvix" ] && [ ! -f "build/orvix.exe" ]; then
  fail_msg "make build did not produce binary"
else
  pass "make build produced binary"
fi

BIN="./build/orvix"
[ -f "./build/orvix.exe" ] && BIN="./build/orvix.exe"

VERSION_OUT="$("$BIN" version 2>/dev/null || echo '')"
if [ -z "$VERSION_OUT" ]; then
  fail_msg "binary did not produce version output"
else
  pass "binary produced version output"
fi

if echo "$VERSION_OUT" | grep -q '0.0.0-dev'; then
  fail_msg "version contains 0.0.0-dev: $VERSION_OUT"
else
  pass "version does NOT contain 0.0.0-dev"
fi

if echo "$VERSION_OUT" | grep -q 'dev build'; then
  fail_msg "version output shows 'dev build': $VERSION_OUT"
else
  pass "version does NOT show 'dev build'"
fi

if echo "$VERSION_OUT" | grep -q 'channel: rc'; then
  pass "channel is rc"
else
  fail_msg "channel is not rc: $VERSION_OUT"
fi

# CI runs on a merge commit; relax to check at least first 7 chars
ACTUAL_COMMIT="$(git rev-parse HEAD | head -c7)"
if echo "$VERSION_OUT" | grep -q "$ACTUAL_COMMIT"; then
  pass "commit matches current HEAD (first 7 chars)"
else
  ACTUAL_FULL="$(git rev-parse HEAD)"
  fail_msg "commit does not match HEAD(${ACTUAL_FULL}): $VERSION_OUT"
fi

FULL_OUT="$("$BIN" version --full 2>/dev/null || echo '')"
if [ -n "$FULL_OUT" ]; then
  pass "version --full produced output"
else
  fail_msg "version --full produced no output"
fi

if echo "$FULL_OUT" | grep -q 'development'; then
  fail_msg "build_time contains 'development' in --full"
else
  pass "build_time does NOT contain 'development'"
fi

# Test 2: Override all metadata values
echo ""
echo "--- Metadata override build ---"
go build \
  -ldflags "-X github.com/orvix/orvix/internal/buildinfo.Version=test-version -X github.com/orvix/orvix/internal/buildinfo.Commit=0123456789012345678901234567890123456789 -X github.com/orvix/orvix/internal/buildinfo.Channel=test-channel -X github.com/orvix/orvix/internal/buildinfo.BuildTime=2026-01-02T03:04:05Z" \
  -o build/orvix-test ./cmd/orvix/ 2>&1

BIN_TEST="./build/orvix-test"
[ -f "./build/orvix-test.exe" ] && BIN_TEST="./build/orvix-test.exe"

VERSION_TEST="$("$BIN_TEST" version 2>/dev/null || echo '')"
if echo "$VERSION_TEST" | grep -q 'test-version'; then
  pass "version override works: test-version"
else
  fail_msg "version override failed: $VERSION_TEST"
fi

if echo "$VERSION_TEST" | grep -q '012345678901'; then
  pass "commit override works (12-char short SHA)"
else
  fail_msg "commit override failed: $VERSION_TEST"
fi

if echo "$VERSION_TEST" | grep -q 'channel: test-channel'; then
  pass "channel override works"
else
  fail_msg "channel override failed: $VERSION_TEST"
fi

if echo "$VERSION_TEST" | grep -q '2026-01-02T03:04:05Z'; then
  pass "build time override works"
else
  fail_msg "build time override failed: $VERSION_TEST"
fi

echo ""
echo "---"
echo "Results: $PASS passed, $FAIL failed"
if [ "$FAIL" -gt 0 ]; then echo "SOME TESTS FAILED"; exit 1; fi
echo "Build metadata tests PASSED"
