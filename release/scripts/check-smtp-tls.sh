#!/usr/bin/env bash
set -euo pipefail

# Orvix SMTP TLS readiness check (SUBMISSION-3D doctor).
#
# Reads the active orvix.yaml, verifies the cert/key files referenced
# under coremail.tls_cert_file / coremail.tls_key_file, and reports
# whether the runtime is in a state where port 587 can actually bind.
# Exits 0 if everything is consistent, non-zero otherwise.
#
# Crucially: this script does NOT echo cert/key paths or raw openssl
# error text on failure. It returns PASS / WARN / FAIL with reason
# codes only.
#
# Usage:
#   bash check-smtp-tls.sh
#   bash check-smtp-tls.sh --verbose

ORVIX_CONFIG="${ORVIX_CONFIG:-/etc/orvix/orvix.yaml}"
ORVIX_TLS_DIR="${ORVIX_TLS_DIR:-/etc/orvix/tls/smtp}"
ORVIX_SERVICE="${ORVIX_SERVICE:-orvix.service}"
VERBOSE=false
[ "${1:-}" = "--verbose" ] || [ "${1:-}" = "-v" ] && VERBOSE=true

failures=0

RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[1;33m'; NC=$'\033[0m'

log_verbose() { $VERBOSE && printf '  \302\267 %s\n' "$*" >&2; }
ok()   { printf '%b\n' "  ${GREEN}PASS${NC}  $*"; }
warn() { printf '%b\n' "  ${YELLOW}WARN${NC}  $*"; failures=$((failures + 1)); }
fail() { printf '%b\n' "  ${RED}FAIL${NC}  $*"; failures=$((failures + 1)); }

# yaml_get reads a top-level or coremail.* field using Python so we
# never depend on yq being installed.
yaml_get() {
	local key="$1"
	python3 - "$ORVIX_CONFIG" "$key" <<'PYEOF'
import sys, re, pathlib
cfg_path, key = sys.argv[1], sys.argv[2]
p = pathlib.Path(cfg_path)
text = p.read_text() if p.exists() else ""
if "." in key:
    section, leaf = key.split(".", 1)
    sec = re.search(rf'(?ms)^{re.escape(section)}:\s*\n(.*?)(?=^\S|\Z)', text)
    if not sec:
        sys.exit(0)
    m = re.search(rf'(?m)^\s*{re.escape(leaf)}:\s*(.*?)\s*$', sec.group(1))
    if m:
        val = m.group(1).strip()
        if val and val != '""' and val != "''":
            print(val)
else:
    m = re.search(rf'(?m)^{re.escape(key)}:\s*(.*?)\s*$', text)
    if m:
        val = m.group(1).strip()
        if val and val != '""' and val != "''":
            print(val)
PYEOF
}

# validate_key_perms verifies a private key file has safe permissions.
# Accepted: 0600, 0640.  Rejected: anything with world bits, 0644, 0666, 0777.
validate_key_perms() {
	local file="$1" label="$2"
	local mode
	mode=$(stat -c '%a' "$file" 2>/dev/null || stat -f '%Lp' "$file" 2>/dev/null || echo "")
	local trimmed="${mode##0}"
	trimmed="${trimmed##0}"
	case "${trimmed:-0}" in
		600|640)
			ok "key file $label permissions OK (mode ${mode})"
			;;
		*)
			fail "key file $label has unsafe permissions (mode ${mode}); chmod 0600 or 0640"
			;;
	esac
}

printf 'Orvix SMTP TLS readiness check (%s)\n' "$ORVIX_CONFIG"

# ── 1. Service running? ──
if command -v systemctl >/dev/null 2>&1; then
	if systemctl is-active --quiet "$ORVIX_SERVICE"; then
		ok "service $ORVIX_SERVICE is active"
	else
		fail "service $ORVIX_SERVICE is not active (start with: systemctl start $ORVIX_SERVICE)"
	fi
else
	warn "systemctl not available; cannot verify service status"
fi

# ── 2. Port 25 must always be listening. ──
if ss -ltn "( sport = :25 )" 2>/dev/null | grep -q ':25'; then
	ok "port 25 is listening (inbound MX)"
else
	fail "port 25 is NOT listening — inbound mail is broken"
fi

# ── 3. Read submission + TLS fields. ──
SUBMISSION_ENABLED_RAW=$(yaml_get "coremail.submission_enabled" || true)
case "${SUBMISSION_ENABLED_RAW,,}" in
	true|yes|1)
		SUBMISSION_ENABLED=1
		;;
	*)
		SUBMISSION_ENABLED=0
		;;
esac

CERT_FILE=$(yaml_get "coremail.tls_cert_file" || true)
KEY_FILE=$(yaml_get "coremail.tls_key_file" || true)

log_verbose "submission_enabled=$SUBMISSION_ENABLED_RAW cert_file=<present:${CERT_FILE:+yes}> key_file=<present:${KEY_FILE:+yes}>"

if [ "$SUBMISSION_ENABLED" -eq 0 ]; then
	ok "coremail.submission_enabled is false (587 will NOT start, port 25 unaffected)"
	if [ -n "$CERT_FILE" ] || [ -n "$KEY_FILE" ]; then
		warn "TLS paths are configured but submission is disabled — cert files will not be loaded"
	fi
	printf '\n587 status: DISABLED (safe default)\n'
	if [ "$failures" -eq 0 ]; then
		printf '%b\n' "${GREEN}OVERALL: PASS${NC}"
		exit 0
	else
		printf '%b\n' "${YELLOW}OVERALL: WARN${NC} (failures=$failures)"
		exit 1
	fi
fi

# Submission is enabled — we MUST have working TLS.
if [ -z "$CERT_FILE" ]; then
	fail "coremail.submission_enabled=true but coremail.tls_cert_file is empty"
	printf '\n587 status: BROKEN (config mismatch)\n'
	exit 2
fi
if [ -z "$KEY_FILE" ]; then
	fail "coremail.submission_enabled=true but coremail.tls_key_file is empty"
	printf '\n587 status: BROKEN (config mismatch)\n'
	exit 2
fi

# ── 4. Files exist? ──
if [ ! -f "$CERT_FILE" ]; then
	fail "cert file does not exist"
	printf '\n587 status: BROKEN (missing cert)\n'
	exit 2
fi
if [ ! -f "$KEY_FILE" ]; then
	fail "key file does not exist"
	printf '\n587 status: BROKEN (missing key)\n'
	exit 2
fi
ok "cert file exists"
ok "key file exists"

# ── 5. Key permissions (using numeric octal validation). ──
validate_key_perms "$KEY_FILE" "privkey"

# ── 6. Cert/key pair validates. ──
CERT_MOD=$(openssl x509 -noout -modulus -in "$CERT_FILE" 2>/dev/null || true)
if openssl pkey -noout -modulus -in "$KEY_FILE" >/dev/null 2>&1; then
	KEY_MOD=$(openssl pkey -noout -modulus -in "$KEY_FILE" 2>/dev/null || true)
else
	KEY_MOD=$(openssl rsa -noout -modulus -in "$KEY_FILE" 2>/dev/null || true)
fi
if [ -z "$CERT_MOD" ] || [ -z "$KEY_MOD" ] || [ "$CERT_MOD" != "$KEY_MOD" ]; then
	fail "cert/key pair modulus does not match"
	printf '\n587 status: BROKEN (cert/key mismatch)\n'
	exit 2
fi
ok "cert/key pair validates (modulus match)"

# ── 7. Cert expiry. ──
if openssl x509 -noout -checkend 2592000 -in "$CERT_FILE" >/dev/null 2>&1; then
	ok "cert is valid for at least 30 more days"
else
	warn "cert expires within 30 days — schedule renewal"
fi

# ── 8. Port 587 listener state. ──
if ss -ltn "( sport = :587 )" 2>/dev/null | grep -q ':587'; then
	ok "port 587 is listening (TLS active)"
else
	fail "port 587 is NOT listening — runtime did not start the submission listener; check 'journalctl -u $ORVIX_SERVICE'"
fi

# ── 9. Port 465 still disabled (honest only). ──
if ss -ltn "( sport = :465 )" 2>/dev/null | grep -q ':465'; then
	warn "port 465 is listening — SMTPS is not implemented in this build; review whether that is intended"
else
	ok "port 465 is not listening (SMTPS honest-disabled)"
fi

# ── 10. Orvix runtime telemetry (best effort, safe temp file). ──
local listeners_temp
listeners_temp=$(mktemp /tmp/orvix-listeners.XXXXXX)
trap 'rm -f "$listeners_temp"' EXIT
if curl -fsSL --connect-timeout 3 --max-time 5 "http://127.0.0.1:8080/api/v1/admin/runtime/listeners" >"$listeners_temp" 2>/dev/null; then
	if grep -q '"smtp-submission"' "$listeners_temp" 2>/dev/null; then
		if grep -A2 '"smtp-submission"' "$listeners_temp" | grep -q '"status":"ok"'; then
			ok "admin runtime telemetry reports smtp-submission=ok"
		else
			warn "admin runtime telemetry reports smtp-submission not ok — see /api/v1/admin/runtime/listeners"
		fi
	else
		warn "admin runtime telemetry did not include smtp-submission key"
	fi
else
	log_verbose "could not reach /api/v1/admin/runtime/listeners (admin API not exposed or auth required)"
fi
rm -f "$listeners_temp"

printf '\n587 status: '
if [ "$failures" -eq 0 ]; then
	printf '%b\n' "${GREEN}READY${NC}"
	exit 0
fi
printf '%b\n' "${RED}NOT READY${NC} (failures=$failures)"
exit 2
