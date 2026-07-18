#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
T=$(mktemp -d)
PASS=0 FAIL=0

cleanup() { rm -rf "$T"; }
trap cleanup EXIT

fail_msg() { echo "FAIL: $1"; FAIL=$((FAIL + 1)); }
pass()     { echo "PASS: $1"; PASS=$((PASS + 1)); }

cat > "$T/fake_curl" <<'FAKECURL'
#!/usr/bin/env bash
# Mimics curl -sS -o /dev/null -w '%{http_code}' --max-time 5 URL
# Controlled by FAKE_CURL_CODE env var; 000 when unset.
echo "${FAKE_CURL_CODE:-000}"
FAKECURL
chmod +x "$T/fake_curl"

run_real_doctor() {
  local code="$1"
  PATH="$T:$PATH" \
    ORVIX_CONFIG="/nonexistent" \
    ORVIX_DB="/nonexistent" \
    DNS_PROVIDERS_URL="http://127.0.0.1:1" \
    FAKE_CURL_CODE="$code" \
    bash -c '
      curl() {
        echo "${FAKE_CURL_CODE:-000}"
      }
      export -f curl
      source "'"$SCRIPT_DIR"'/../orvix-doctor.sh"
      check_dns_readiness
      for entry in "${CHECKS[@]}"; do
        printf "%s\n" "$entry"
      done
    '
}

echo "=== Doctor DNS Auth Check Tests (real production code) ==="

assert_result() {
  local code="$1" expected_status="$2" desc="$3"
  local output
  output="$(run_real_doctor "$code" 2>/dev/null || true)"
  local actual_status
  actual_status="$(echo "$output" | grep -o '"status":"[^"]*"' | head -1 | sed 's/"status":"//;s/"//')"
  local actual_message
  actual_message="$(echo "$output" | grep -o '"message":"[^"]*"' | head -1 | sed 's/"message":"//;s/"//' || true)"

  if [ "$actual_status" = "$expected_status" ]; then
    pass "$desc (HTTP $code -> $expected_status)"
  else
    fail_msg "$desc: expected status '$expected_status', got '$actual_status' (message: $actual_message)"
  fi

  local name_found
  name_found="$(echo "$output" | grep -c '"name":"dns providers readiness"' || true)"
  if [ "$name_found" -ge 1 ]; then
    pass "  check name verified for HTTP $code"
  else
    fail_msg "  check name NOT 'dns providers readiness' for HTTP $code"
  fi
}

assert_result "200" "PASS" "HTTP 200 is PASS"
assert_result "401" "PASS" "HTTP 401 is PASS (protected endpoint)"
assert_result "403" "PASS" "HTTP 403 is PASS (protected endpoint)"
assert_result "000" "WARN" "HTTP 000 is WARN (unreachable)"
assert_result "404" "FAIL" "HTTP 404 is FAIL (endpoint missing)"
assert_result "500" "FAIL" "HTTP 500 is FAIL (server error)"
assert_result "502" "FAIL" "HTTP 502 is FAIL (server error)"
assert_result "999" "FAIL" "HTTP 999 is FAIL (unexpected)"

# Also verify the final exit semantics of Doctor when a FAIL exists
echo "--- Exit semantics test ---"
bash "$SCRIPT_DIR/../orvix-doctor.sh" --quiet 2>/dev/null && exit_code=0 || exit_code=$?
if [ "$exit_code" = "1" ]; then
  pass "Doctor with failing check exits 1 (unhealthy)"
else
  fail_msg "Doctor with failing check exits $exit_code, expected 1"
fi

echo ""
echo "---"
echo "Results: $PASS passed, $FAIL failed"
if [ "$FAIL" -gt 0 ]; then echo "SOME TESTS FAILED"; exit 1; fi
echo "Doctor DNS auth check tests PASSED"
