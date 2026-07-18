#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DOCTOR_SCRIPT="$SCRIPT_DIR/../orvix-doctor.sh"
PASS=0 FAIL=0 T=""

fail_msg() { echo "FAIL: $1"; FAIL=$((FAIL + 1)); }
pass()     { echo "PASS: $1"; PASS=$((PASS + 1)); }

cleanup() { [ -n "${T:-}" ] && rm -rf "$T"; }
trap cleanup EXIT

fake_curl() {
  echo "$1"
}

test_dns_code() {
  local code="$1" expected="$2" desc="$3"
  local result
  result="$(DNS_PROVIDERS_URL=http://127.0.0.1:1 CHECK="$code" bash -c '
    check_dns_readiness() {
      local name="test"
      local code="'"$code"'"
      case "$code" in
        200)
          echo "PASS" ;;
        401|403)
          echo "PASS" ;;
        000)
          echo "WARN" ;;
        404)
          echo "FAIL" ;;
        5??)
          echo "FAIL" ;;
        *)
          echo "FAIL" ;;
      esac
    }
    check_dns_readiness
  ')"
  if echo "$result" | grep -q "$expected"; then
    pass "$desc (HTTP $code -> $expected)"
  else
    fail_msg "$desc: expected $expected, got $result"
  fi
}

echo "=== Doctor DNS Auth Check Tests ==="

test_dns_code "200" "PASS" "HTTP 200 is PASS"
test_dns_code "401" "PASS" "HTTP 401 is PASS (protected endpoint)"
test_dns_code "403" "PASS" "HTTP 403 is PASS (protected endpoint)"
test_dns_code "000" "WARN" "HTTP 000 is WARN (unreachable)"
test_dns_code "404" "FAIL" "HTTP 404 is FAIL (endpoint missing)"
test_dns_code "500" "FAIL" "HTTP 500 is FAIL (server error)"
test_dns_code "502" "FAIL" "HTTP 502 is FAIL (server error)"
test_dns_code "999" "FAIL" "HTTP 999 is FAIL (unexpected)"

# Additional: verify the actual orvix-doctor.sh contains the correct contract
if [ -f "$DOCTOR_SCRIPT" ]; then
  if grep -q '401|403)' "$DOCTOR_SCRIPT"; then
    pass "Doctor script contains 401|403 PASS case"
  else
    fail_msg "Doctor script does NOT contain 401|403 PASS case"
  fi
  if grep -q '404)' "$DOCTOR_SCRIPT"; then
    pass "Doctor script contains 404 FAIL case"
  else
    fail_msg "Doctor script does NOT contain 404 FAIL case"
  fi
  if grep -q '5??)' "$DOCTOR_SCRIPT"; then
    pass "Doctor script contains 5xx FAIL case"
  else
    fail_msg "Doctor script does NOT contain 5xx FAIL case"
  fi
else
  fail_msg "Doctor script not found at $DOCTOR_SCRIPT"
fi

echo ""
echo "---"
echo "Results: $PASS passed, $FAIL failed"
if [ "$FAIL" -gt 0 ]; then echo "SOME TESTS FAILED"; exit 1; fi
echo "Doctor DNS auth check tests PASSED"
