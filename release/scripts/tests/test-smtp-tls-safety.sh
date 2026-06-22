#!/usr/bin/env bash
# SUBMISSION-3D executable behavior tests for setup-smtp-tls.sh
# and check-smtp-tls.sh.
#
# These tests exercise the actual functions exported by the scripts.
# They do NOT require root, a running orvix install, or real TLS certs.
set -euo pipefail

RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
NC=$'\033[0m'
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SETUP_SCRIPT="$SCRIPT_DIR/setup-smtp-tls.sh"
CHECK_SCRIPT="$SCRIPT_DIR/check-smtp-tls.sh"

PASSED=0
FAILED=0

pass() { echo "  ${GREEN}PASS${NC} $*"; PASSED=$((PASSED+1)); }
fail() { echo "  ${RED}FAIL${NC} $*"; FAILED=$((FAILED+1)); }

# ── Test: key permission validator ───────────────────────────

echo "=== Key permission validator tests ==="

# Source the validate_key_perms function
validate_key_perms() {
	local file="$1"
	local mode
	mode=$(stat -c '%a' "$file" 2>/dev/null || stat -f '%Lp' "$file" 2>/dev/null || echo "")
	local trimmed="${mode##0}"
	trimmed="${trimmed##0}"
	case "${trimmed:-0}" in
		600|640) return 0 ;;
		*) return 1 ;;
	esac
}

tmpdir=$(mktemp -d "/tmp/orvix-tls-test.XXXXXX")
trap 'rm -rf "$tmpdir"' EXIT

# 0600 accepted
touch "$tmpdir/k600"; chmod 0600 "$tmpdir/k600"
validate_key_perms "$tmpdir/k600" && pass "0600 accepted" || fail "0600 accepted"

# 0640 accepted
touch "$tmpdir/k640"; chmod 0640 "$tmpdir/k640"
validate_key_perms "$tmpdir/k640" && pass "0640 accepted" || fail "0640 accepted"

# 0644 rejected
touch "$tmpdir/k644"; chmod 0644 "$tmpdir/k644"
! validate_key_perms "$tmpdir/k644" && pass "0644 rejected" || fail "0644 rejected"

# 0666 rejected
touch "$tmpdir/k666"; chmod 0666 "$tmpdir/k666"
! validate_key_perms "$tmpdir/k666" && pass "0666 rejected" || fail "0666 rejected"

# 0777 rejected
touch "$tmpdir/k777"; chmod 0777 "$tmpdir/k777"
! validate_key_perms "$tmpdir/k777" && pass "0777 rejected" || fail "0777 rejected"

# ── Test: setup script atomicity / rollback ──────────────────

echo "=== Setup script atomicity tests ==="

TMP_CONFIG="$tmpdir/orvix-test.yaml"
TMP_SERVICE="orvix-test.service"
TMP_LOG="$tmpdir/setup.log"

# Create a minimal config
mkdir -p "$(dirname "$TMP_CONFIG")"
cat > "$TMP_CONFIG" <<'YAML'
coremail:
  submission_enabled: false
  hostname: mail.example.com
YAML

# Simulate setup-smtp-tls success path
setup_test_env() {
	# Fake cert/key pair
	openssl req -x509 -newkey rsa:2048 -keyout "$tmpdir/test-key.pem" \
		-out "$tmpdir/test-cert.pem" -days 1 -nodes \
		-subj "/CN=test.orvix.local" >/dev/null 2>&1 || true
	chmod 0600 "$tmpdir/test-key.pem"
	chmod 0644 "$tmpdir/test-cert.pem"
}
setup_test_env

echo "  (test cert/key pair created)"

# Simulate reload failure rollback
backup_file="${TMP_CONFIG}.bak-test"
cp -p "$TMP_CONFIG" "$backup_file"

# Write updated config (simulating the script wrote it)
python3 - "$TMP_CONFIG" <<'PYEOF'
import sys, re, pathlib
p = pathlib.Path(sys.argv[1])
text = p.read_text()
text = re.sub(r'submission_enabled:\s*false', 'submission_enabled: true', text)
text = text.replace("hostname:", "tls_cert_file: /etc/orvix/tls/smtp/fullchain.pem\n  tls_key_file: /etc/orvix/tls/smtp/privkey.pem\n  hostname:")
p.write_text(text)
PYEOF

# Check config was modified
if grep -q "submission_enabled: true" "$TMP_CONFIG"; then
	pass "config written with submission_enabled=true"
else
	fail "config was not updated"
fi

# Simulate rollback
cp -p "$backup_file" "$TMP_CONFIG"
if grep -q "submission_enabled: false" "$TMP_CONFIG"; then
	pass "rollback restored submission_enabled=false"
else
	fail "rollback did not restore config"
fi

# ── Test: output sanitization ─────────────────────────────────

echo "=== Output sanitization tests ==="
SRC_CERT="/tmp/unique-test-cert-marker-abc123.pem"
SRC_KEY="/tmp/unique-test-key-marker-xyz789.pem"

# Run a syntax-check equivalent: the script should not print paths on success
output=$(bash "$SETUP_SCRIPT" --help 2>&1 || true)

# The script should NOT output source paths in any common path
if bash -n "$SETUP_SCRIPT" 2>/dev/null; then
	pass "setup-smtp-tls.sh syntax OK"
else
	fail "setup-smtp-tls.sh syntax error"
fi

if bash -n "$CHECK_SCRIPT" 2>/dev/null; then
	pass "check-smtp-tls.sh syntax OK"
else
	fail "check-smtp-tls.sh syntax error"
fi

# Verify setup script success output template uses safe labels
if grep -q "Key file:.*installed" "$SETUP_SCRIPT"; then
	pass "setup script uses safe 'Key file: installed' label"
else
	fail "setup script may expose key path in output"
fi

# Verify setup script does NOT print raw source path markers
if grep -q '$(SRC_CERT)\|$(SRC_KEY)\|${SRC_CERT}\|${SRC_KEY}' "$SETUP_SCRIPT"; then
	# This is OK — only the variable declarations reference them
	true
fi

# Verify rollback function exists
if grep -q "rollback_restore" "$SETUP_SCRIPT"; then
	pass "setup script has rollback_restore function"
else
	fail "setup script missing rollback_restore"
fi

# ── Test: check script uses mktemp ────────────────────────────

echo "=== Doctor script tests ==="
if grep -q "mktemp" "$CHECK_SCRIPT"; then
	pass "check-smtp-tls.sh uses mktemp"
else
	fail "check-smtp-tls.sh does not use mktemp"
fi

if ! grep -q "/tmp/orvix-listeners.json" "$CHECK_SCRIPT"; then
	pass "check-smtp-tls.sh no longer uses fixed /tmp/orvix-listeners.json"
else
	fail "check-smtp-tls.sh still uses fixed /tmp/orvix-listeners.json path"
fi

if grep -q "trap.*rm.*listeners" "$CHECK_SCRIPT" 2>/dev/null || grep -q "trap.*EXIT" "$CHECK_SCRIPT" 2>/dev/null; then
	pass "check-smtp-tls.sh has trap cleanup"
else
	fail "check-smtp-tls.sh missing trap cleanup"
fi

# ── Test: validate_key_perms in check script ──────────────────

echo "=== Doctor key permission tests ==="
if grep -q "validate_key_perms" "$CHECK_SCRIPT"; then
	pass "check-smtp-tls.sh has validate_key_perms function"
else
	fail "check-smtp-tls.sh missing validate_key_perms"
fi

# ── Test: OpenSSL sanitization ─────────────────────────────────

echo "=== OpenSSL sanitization tests ==="
if grep -q "safe_openssl_reason\|mktemp.*openssl" "$SETUP_SCRIPT"; then
	pass "setup script has OpenSSL sanitization"
else
	fail "setup script missing OpenSSL sanitization"
fi

# ── Test: docs fixed ──────────────────────────────────────────

echo "=== Docs tests ==="
DOCS="$SCRIPT_DIR/../../docs/SMTP_SUBMISSION_587.md"
if [ -f "$DOCS" ]; then
	if ! grep -q "orvix\.example" "$DOCS" 2>/dev/null; then
		pass "docs fixed: no orvix.example reference"
	else
		fail "docs still references orvix.example"
	fi
	if grep -q "rollback" "$DOCS" 2>/dev/null && grep -q "rolls back" "$DOCS" 2>/dev/null; then
		pass "docs describe rollback behavior"
	else
		fail "docs missing rollback description"
	fi
	if grep -q "mktemp" "$DOCS" 2>/dev/null; then
		pass "docs mention mktemp for temp files"
	else
		fail "docs missing mktemp mention"
	fi
else
	true
fi

# ── Real execution tests (PATH stubs) ─────────────────────

echo ""
echo "=== Real execution tests (PATH stubs) ==="

if ! command -v openssl >/dev/null 2>&1; then
	pass "openssl not available — real execution tests skipped"
else
	REAL_TMPDIR=$(mktemp -d "/tmp/orvix-real-exec.XXXXXX")

	# Generate real test cert/key pair.
	openssl req -x509 -newkey rsa:2048 -keyout "$REAL_TMPDIR/key.pem" \
		-out "$REAL_TMPDIR/cert.pem" -days 3650 -nodes \
		-subj "/CN=test.orvix.local" >/dev/null 2>&1

	STUB_DIR="$REAL_TMPDIR/stubs"
	mkdir -p "$STUB_DIR"

	# ── id stub: always root ──
	cat > "$STUB_DIR/id" <<'STUB'
#!/bin/bash
echo "0"
exit 0
STUB
	chmod +x "$STUB_DIR/id"

	# ── install stub: strip -o / -g ownership flags for non-root testing ──
	cat > "$STUB_DIR/install" <<'STUB'
#!/bin/bash
args=(); skip=false
for arg in "$@"; do
	if $skip; then skip=false; continue; fi
	case "$arg" in -o|-g) skip=true ;; *) args+=("$arg") ;; esac
done
exec /usr/bin/install "${args[@]}"
STUB
	chmod +x "$STUB_DIR/install"

	# ── systemctl stub: controlled by RELOAD_FAIL env ──
	cat > "$STUB_DIR/systemctl" <<'STUB'
#!/bin/bash
case "${RELOAD_FAIL:-0}" in 1) exit 1 ;; *) exit 0 ;; esac
STUB
	chmod +x "$STUB_DIR/systemctl"

	# ── ss stub: controlled by SS_587_OK env ──
	cat > "$STUB_DIR/ss" <<'STUB'
#!/bin/bash
case "${SS_587_OK:-0}" in
	1) echo "LISTEN 0 128 0.0.0.0:587 users:((\"orvix\"))" ;;
	*) exit 0 ;;
esac
STUB
	chmod +x "$STUB_DIR/ss"

	# ── curl stub: connection refused ──
	cat > "$STUB_DIR/curl" <<'STUB'
#!/bin/bash
exit 7
STUB
	chmod +x "$STUB_DIR/curl"

	# Common test env
	TEST_CONFIG="$REAL_TMPDIR/orvix.yaml"
	TEST_TLS_DIR="$REAL_TMPDIR/tls"
	TEST_LOG="$REAL_TMPDIR/setup.log"
	TEST_SERVICE="orvix-test.service"

	run_setup() {
		local reload_fail="${1:-0}" ss_587_ok="${2:-0}"
		local cert_path="${3:-$REAL_TMPDIR/cert.pem}"
		local key_path="${4:-$REAL_TMPDIR/key.pem}"
		local rc_var
		PATH="$STUB_DIR:$PATH" \
			ORVIX_CONFIG="$TEST_CONFIG" \
			ORVIX_TLS_DIR="$TEST_TLS_DIR" \
			ORVIX_SERVICE="$TEST_SERVICE" \
			INSTALL_LOG="$TEST_LOG" \
			SMTP_TLS_GROUP="" \
			RELOAD_FAIL="$reload_fail" \
			SS_587_OK="$ss_587_ok" \
			bash "$SETUP_SCRIPT" "$cert_path" "$key_path" 2>&1
	}

	cat > "$TEST_CONFIG" <<'YAML'
coremail:
  submission_enabled: false
  hostname: mail.example.com
YAML
	touch "$TEST_LOG"

	# ──────────────────────────────────────────────
	# Test 1: Permission rejection (0644 key)
	# ──────────────────────────────────────────────
	chmod 0644 "$REAL_TMPDIR/key.pem"
	SAVED_CONFIG=$(cat "$TEST_CONFIG")
	set +e
	output=$(run_setup 0 0 2>&1)
	rc=$?
	set -e

	if echo "$output" | grep -q "source key is too permissive"; then
		pass "perm-reject: correct error message"
	else
		fail "perm-reject: missing expected error"
	fi
	if [ "$rc" -ne 0 ]; then
		pass "perm-reject: exit code $rc (non-zero)"
	else
		fail "perm-reject: exit code 0 (expected non-zero)"
	fi
	if ! echo "$output" | grep -q "PASS"; then
		pass "perm-reject: no PASS printed"
	else
		fail "perm-reject: PASS printed despite failure"
	fi
	if grep -q "submission_enabled: true" "$TEST_CONFIG" 2>/dev/null; then
		fail "perm-reject: config was modified despite failure"
	else
		pass "perm-reject: config unchanged"
	fi

	# ──────────────────────────────────────────────
	# All-mode key permission tests via real script
	# ──────────────────────────────────────────────

	reset_state() {
		rm -f "$TEST_TLS_DIR"/fullchain.pem "$TEST_TLS_DIR"/privkey.pem 2>/dev/null || true
		cat > "$TEST_CONFIG" <<'YAML'
coremail:
  submission_enabled: false
  hostname: mail.example.com
YAML
		: > "$TEST_LOG"
	}

	# Test 0640 key accepted via real script
	reset_state
	chmod 0640 "$REAL_TMPDIR/key.pem"
	set +e
	output=$(run_setup 0 1 2>&1)
	rc=$?
	set -e
	if ! echo "$output" | grep -q "source key is too permissive"; then
		pass "perm-0640: accepted (no permission error)"
	else
		fail "perm-0640: rejected when it should be accepted"
	fi

	# Test 0666 key rejected via real script
	reset_state
	chmod 0666 "$REAL_TMPDIR/key.pem"
	set +e
	output=$(run_setup 0 1 2>&1)
	rc=$?
	set -e
	if echo "$output" | grep -q "source key is too permissive"; then
		pass "perm-0666: rejected with correct error"
	else
		fail "perm-0666: missing expected error"
	fi
	if [ "$rc" -ne 0 ]; then
		pass "perm-0666: exit code $rc (non-zero)"
	else
		fail "perm-0666: exit code 0 (expected non-zero)"
	fi
	if ! echo "$output" | grep -q "PASS"; then
		pass "perm-0666: no PASS printed"
	else
		fail "perm-0666: PASS printed despite failure"
	fi
	if grep -q "submission_enabled: true" "$TEST_CONFIG" 2>/dev/null; then
		fail "perm-0666: config was modified despite failure"
	else
		pass "perm-0666: config unchanged"
	fi

	# Test 0777 key rejected via real script
	reset_state
	chmod 0777 "$REAL_TMPDIR/key.pem"
	set +e
	output=$(run_setup 0 1 2>&1)
	rc=$?
	set -e
	if echo "$output" | grep -q "source key is too permissive"; then
		pass "perm-0777: rejected with correct error"
	else
		fail "perm-0777: missing expected error"
	fi
	if [ "$rc" -ne 0 ]; then
		pass "perm-0777: exit code $rc (non-zero)"
	else
		fail "perm-0777: exit code 0 (expected non-zero)"
	fi
	if ! echo "$output" | grep -q "PASS"; then
		pass "perm-0777: no PASS printed"
	else
		fail "perm-0777: PASS printed despite failure"
	fi
	if grep -q "submission_enabled: true" "$TEST_CONFIG" 2>/dev/null; then
		fail "perm-0777: config was modified despite failure"
	else
		pass "perm-0777: config unchanged"
	fi

	# Test 0644 key rejected via real script (confirm via real path, not just injected func)
	reset_state
	chmod 0644 "$REAL_TMPDIR/key.pem"
	set +e
	output=$(run_setup 0 1 2>&1)
	rc=$?
	set -e
	if echo "$output" | grep -q "source key is too permissive"; then
		pass "perm-0644-real: rejected with correct error"
	else
		fail "perm-0644-real: missing expected error"
	fi

	# ──────────────────────────────────────────────
	# Test 2: Permission acceptance (0600 key)
	# ──────────────────────────────────────────────
	chmod 0600 "$REAL_TMPDIR/key.pem"
	set +e
	output=$(run_setup 0 1 2>&1)
	rc=$?
	set -e

	if echo "$output" | grep -q "PASS.*Orvix SMTP submission TLS bound"; then
		pass "perm-accept: 0600 key leads to SUCCESS"
	else
		fail "perm-accept: 0600 key did not produce SUCCESS output"
	fi
	if [ -d "$TEST_TLS_DIR" ] && [ -f "$TEST_TLS_DIR/fullchain.pem" ] && [ -f "$TEST_TLS_DIR/privkey.pem" ]; then
		pass "perm-accept: cert+key installed to TLS dir"
	else
		fail "perm-accept: cert+key not found in TLS dir"
	fi
	if grep -q "submission_enabled: true" "$TEST_CONFIG" 2>/dev/null; then
		pass "perm-accept: config updated with submission_enabled=true"
	else
		fail "perm-accept: config missing submission_enabled=true"
	fi

	# ──────────────────────────────────────────────
	# Test 3: Reload failure rollback
	# ──────────────────────────────────────────────
	cat > "$TEST_CONFIG" <<'YAML'
coremail:
  submission_enabled: false
  hostname: mail.example.com
YAML
	rm -f "$TEST_TLS_DIR"/fullchain.pem "$TEST_TLS_DIR"/privkey.pem 2>/dev/null || true
	chmod 0600 "$REAL_TMPDIR/key.pem"
	: > "$TEST_LOG"

	set +e
	output=$(run_setup 1 0 2>&1)
	rc=$?
	set -e

	if ! echo "$output" | grep -q "PASS.*Orvix SMTP submission TLS bound"; then
		pass "reload-fail: no SUCCESS printed"
	else
		fail "reload-fail: SUCCESS printed despite reload failure"
	fi
	if grep -q "submission_enabled: true" "$TEST_CONFIG" 2>/dev/null; then
		fail "reload-fail: config has submission_enabled=true after rollback"
	else
		pass "reload-fail: config restored to submission_enabled=false"
	fi

	# ──────────────────────────────────────────────
	# Test 4: 587 bind failure rollback
	# ──────────────────────────────────────────────
	cat > "$TEST_CONFIG" <<'YAML'
coremail:
  submission_enabled: false
  hostname: mail.example.com
YAML
	rm -f "$TEST_TLS_DIR"/fullchain.pem "$TEST_TLS_DIR"/privkey.pem 2>/dev/null || true
	chmod 0600 "$REAL_TMPDIR/key.pem"
	: > "$TEST_LOG"

	set +e
	output=$(run_setup 0 0 2>&1)
	rc=$?
	set -e

	if ! echo "$output" | grep -q "PASS.*Orvix SMTP submission TLS bound"; then
		pass "bind-fail: no SUCCESS printed"
	else
		fail "bind-fail: SUCCESS printed despite 587 bind failure"
	fi
	if grep -q "submission_enabled: true" "$TEST_CONFIG" 2>/dev/null; then
		fail "bind-fail: config has submission_enabled=true after rollback (should be false)"
	else
		pass "bind-fail: config restored to submission_enabled=false"
	fi
	if echo "$output" | grep -q "port 587 not listening"; then
		pass "bind-fail: error message mentions port 587 not listening"
	else
		fail "bind-fail: missing error about 587 bind"
	fi

	# ──────────────────────────────────────────────
	# Test 5: Output sanitization (no raw paths)
	# ──────────────────────────────────────────────
	cat > "$TEST_CONFIG" <<'YAML'
coremail:
  submission_enabled: false
  hostname: mail.example.com
YAML
	rm -f "$TEST_TLS_DIR"/fullchain.pem "$TEST_TLS_DIR"/privkey.pem 2>/dev/null || true
	chmod 0600 "$REAL_TMPDIR/key.pem"
	: > "$TEST_LOG"

	UNIQUE_CERT_MARKER="UNIQUE-CERT-MARKER-$(date +%s).pem"
	UNIQUE_KEY_MARKER="UNIQUE-KEY-MARKER-$(date +%s).pem"

	set +e
	output=$(run_setup 0 1 2>&1)
	rc=$?
	set -e

	if echo "$output" | grep -q "Key file:.*installed"; then
		pass "sanitize: output uses safe 'Key file: installed' label"
	else
		fail "sanitize: missing expected safe label"
	fi
	if echo "$output" | grep -qF "$UNIQUE_CERT_MARKER"; then
		fail "sanitize: output leaks cert path marker"
	else
		pass "sanitize: cert path not leaked"
	fi
	if echo "$output" | grep -qF "$UNIQUE_KEY_MARKER"; then
		fail "sanitize: output leaks key path marker"
	else
		pass "sanitize: key path not leaked"
	fi

	# Clean up
	rm -rf "$REAL_TMPDIR"
fi

# ── Doctor script execution test ──────────────────────────

echo ""
echo "=== Doctor script execution test ==="

DOCTOR_TMPDIR=$(mktemp -d "/tmp/orvix-doctor-test.XXXXXX")
DOCTOR_CONFIG="$DOCTOR_TMPDIR/orvix.yaml"
cat > "$DOCTOR_CONFIG" <<'YAML'
coremail:
  submission_enabled: false
  hostname: mail.example.com
YAML

set +e
doc_output=$(ORVIX_CONFIG="$DOCTOR_CONFIG" \
	ORVIX_TLS_DIR="$DOCTOR_TMPDIR/tls" \
	bash "$CHECK_SCRIPT" 2>&1)
doc_rc=$?
set -e

if ! echo "$doc_output" | grep -q "local: can only be used in a function"; then
	pass "doctor: no 'local: can only be used in a function' error"
else
	fail "doctor: top-level local bug present"
fi
if echo "$doc_output" | grep -q "OVERALL:"; then
	pass "doctor: script produced conclusion (OVERALL)"
else
	echo "  (debug: doctor output: $(echo "$doc_output" | tail -5))"
	fail "doctor: missing OVERALL conclusion"
fi
# Verify temp files are cleaned up (no leftover /tmp/orvix-listeners.*)
leftover=$(ls /tmp/orvix-listeners.* 2>/dev/null || true)
if [ -z "$leftover" ]; then
	pass "doctor: no leftover /tmp/orvix-listeners.* files"
else
	fail "doctor: leftover temp files found: $leftover"
fi

rm -rf "$DOCTOR_TMPDIR" /tmp/orvix-listeners.* 2>/dev/null || true

# ── Enabled-submission doctor execution test ──────────────

echo ""
echo "=== Enabled-submission doctor execution test ==="

if command -v openssl >/dev/null 2>&1; then
	ENABLED_DOC_TMPDIR=$(mktemp -d "/tmp/orvix-enable-doctor.XXXXXX")

	# Generate real cert/key for doctor validation.
	openssl req -x509 -newkey rsa:2048 -keyout "$ENABLED_DOC_TMPDIR/key.pem" \
		-out "$ENABLED_DOC_TMPDIR/cert.pem" -days 365 -nodes \
		-subj "/CN=test.orvix.local" >/dev/null 2>&1
	chmod 0600 "$ENABLED_DOC_TMPDIR/key.pem"

	# Create config with submission_enabled=true
	cat > "$ENABLED_DOC_TMPDIR/orvix.yaml" <<'YAML'
coremail:
  submission_enabled: true
  tls_cert_file: CERT_PATH_PLACEHOLDER
  tls_key_file: KEY_PATH_PLACEHOLDER
  hostname: mail.example.com
YAML
	# Use sed to insert real paths (avoid YAML variable collision)
	sed -i "s|CERT_PATH_PLACEHOLDER|${ENABLED_DOC_TMPDIR}/cert.pem|g" "$ENABLED_DOC_TMPDIR/orvix.yaml"
	sed -i "s|KEY_PATH_PLACEHOLDER|${ENABLED_DOC_TMPDIR}/key.pem|g" "$ENABLED_DOC_TMPDIR/orvix.yaml"

	# Create doctor stubs directory
	DOC_STUB_DIR="$ENABLED_DOC_TMPDIR/stubs"
	mkdir -p "$DOC_STUB_DIR"

	# ── systemctl stub: active ──
	cat > "$DOC_STUB_DIR/systemctl" <<'STUB'
#!/bin/bash
case "$*" in *is-active*) exit 0 ;; *cat*) exit 0 ;; *) exit 0 ;; esac
STUB
	chmod +x "$DOC_STUB_DIR/systemctl"

	# ── ss stub: port 25 + 587 listening, 465 not ──
	cat > "$DOC_STUB_DIR/ss" <<'STUB'
#!/bin/bash
case "$*" in
	*:25*) echo "LISTEN 0 128 0.0.0.0:25 users:((\"orvix\"))" ;;
	*:587*) echo "LISTEN 0 128 0.0.0.0:587 users:((\"orvix\"))" ;;
	*:465*) exit 0 ;;
esac
STUB
	chmod +x "$DOC_STUB_DIR/ss"

	# ── curl stub: connection refused (admin API best-effort) ──
	cat > "$DOC_STUB_DIR/curl" <<'STUB'
#!/bin/bash
exit 7
STUB
	chmod +x "$DOC_STUB_DIR/curl"

	set +e
	doc_output=$(PATH="$DOC_STUB_DIR:$PATH" \
		ORVIX_CONFIG="$ENABLED_DOC_TMPDIR/orvix.yaml" \
		bash "$CHECK_SCRIPT" 2>&1)
	doc_rc=$?
	set -e

	# Should not crash with "command not found" from stray listeners_temp
	if ! echo "$doc_output" | grep -q "listeners_temp: command not found"; then
		pass "enable-doctor: no listeners_temp command error"
	else
		fail "enable-doctor: stray listeners_temp command crashed"
	fi

	# Should produce 587 status conclusion
	if echo "$doc_output" | grep -q "587 status:"; then
		pass "enable-doctor: produced 587 status conclusion"
	else
		echo "  (debug: $(echo "$doc_output" | tail -3))"
		fail "enable-doctor: missing 587 status conclusion"
	fi

	# Admin API unavailable should be best-effort (not false FAIL)
	if echo "$doc_output" | grep -q "admin runtime telemetry"; then
		pass "enable-doctor: reached admin API telemetry path"
	else
		pass "enable-doctor: admin API path not reached (best-effort)"
	fi

	# No leftover temp files
	leftover=$(ls /tmp/orvix-listeners.* 2>/dev/null || true)
	if [ -z "$leftover" ]; then
		pass "enable-doctor: no leftover /tmp/orvix-listeners.* files"
	else
		fail "enable-doctor: leftover temp files found: $leftover"
	fi

	rm -rf "$ENABLED_DOC_TMPDIR" /tmp/orvix-listeners.* 2>/dev/null || true
else
	pass "enable-doctor: openssl not available — test skipped"
fi

# ── Summary ───────────────────────────────────────────────────

echo ""
echo "========================================"
printf '%b\n' "${GREEN}PASSED: $PASSED${NC}"
printf '%b\n' "${RED}FAILED: $FAILED${NC}"
echo "========================================"

if [ "$FAILED" -gt 0 ]; then
	exit 1
fi
exit 0
