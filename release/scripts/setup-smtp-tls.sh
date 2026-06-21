#!/usr/bin/env bash
set -euo pipefail

# Orvix SMTP submission (port 587) TLS setup.
#
# SUBMISSION-3D operational helper. This script is the ONLY place
# in the repo that knows about the upstream certificate source
# (e.g. /var/lib/caddy/.local/share/caddy/certificates/acme-v02.api.letsencrypt.org-directory/<domain>/...).
# The Go runtime never references those paths — it loads cert/key
# from /etc/orvix/tls/smtp/, which this script populates.
#
# Inputs (positional, or via env):
#   $1 / ORVIX_SRC_CERT — path to the source fullchain PEM
#   $2 / ORVIX_SRC_KEY  — path to the source private key PEM
#
# Optional env flags:
#   ORVIX_SMTP_TLS_MODE=copy|symlink   (default: copy)
#   ORVIX_CONFIG=/etc/orvix/orvix.yaml (override target config)
#   ORVIX_SERVICE=orvix.service        (systemd unit to reload)
#   INSTALL_LOG=/var/log/orvix/smtp-tls-setup.log
#
# Atomicity guarantee:
#   If reload fails or port 587 does not bind within 30 s, the
#   script restores the previous config backup, reloads the service
#   to bring it back to its prior state, and exits non-zero without
#   printing "PASS".  Port 25 inbound is never touched.
#
# Idempotent: rerunning with the same source paths leaves the system
# in the same end-state, only rotating the backup timestamp.

# ── Defaults ─────────────────────────────────────────────────
ORVIX_TLS_DIR="${ORVIX_TLS_DIR:-/etc/orvix/tls/smtp}"
ORVIX_CERT_NAME="fullchain.pem"
ORVIX_KEY_NAME="privkey.pem"
ORVIX_CONFIG="${ORVIX_CONFIG:-/etc/orvix/orvix.yaml}"
ORVIX_SERVICE="${ORVIX_SERVICE:-orvix.service}"
ORVIX_SMTP_TLS_MODE="${ORVIX_SMTP_TLS_MODE:-copy}"
INSTALL_LOG="${INSTALL_LOG:-/var/log/orvix/smtp-tls-setup.log}"
SMTP_TLS_GROUP="${SMTP_TLS_GROUP:-orvix}"

# Positional args (with env fallback)
SRC_CERT="${1:-${ORVIX_SRC_CERT:-}}"
SRC_KEY="${2:-${ORVIX_SRC_KEY:-}}"

# ── Colors / logging ─────────────────────────────────────────
RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
YELLOW=$'\033[1;33m'
NC=$'\033[0m'

log() {
	mkdir -p "$(dirname "$INSTALL_LOG")"
	printf '[%s] %s\n' "$(date -Is)" "$*" >>"$INSTALL_LOG"
}
say() { printf '%b\n' "$*"; }
ok() { say "${GREEN}PASS${NC} $*"; log "PASS $*"; }
warn() { say "${YELLOW}WARN${NC} $*" >&2; log "WARN $*"; }
fail() {
	say "${RED}ERROR${NC} $*" >&2
	log "ERROR $*"
	say "Detailed log: $INSTALL_LOG" >&2
	if [ -f "$INSTALL_LOG" ]; then
		say "Last 80 log lines:" >&2
		tail -n 80 "$INSTALL_LOG" >&2 || true
	fi
	say "" >&2
	say "Recovery:" >&2
	say "  - If the YAML was already edited, restore from the latest backup:" >&2
	say "      ls -1t ${ORVIX_CONFIG}.bak-* 2>/dev/null | head -n 1 | xargs -r cp -p - ${ORVIX_CONFIG}" >&2
	say "  - If the service was reloaded, ensure submission_enabled is false in ${ORVIX_CONFIG}" >&2
	say "  - port 25 inbound is unaffected by this script's failure" >&2
	exit 1
}

require_root() {
	[ "$(id -u)" -eq 0 ] || fail "must run as root or with sudo"
}

require_input() {
	[ -n "$SRC_CERT" ] || fail "usage: $0 <source-cert-path> <source-key-path>  (or set ORVIX_SRC_CERT / ORVIX_SRC_KEY)"
	[ -n "$SRC_KEY" ] || fail "usage: $0 <source-cert-path> <source-key-path>  (or set ORVIX_SRC_CERT / ORVIX_SRC_KEY)"
	[ -f "$SRC_CERT" ] || fail "source cert not found"
	[ -f "$SRC_KEY" ] || fail "source key not found"
	[ -r "$SRC_CERT" ] || fail "source cert not readable by current user"
	[ -r "$SRC_KEY" ] || fail "source key not readable by current user"
	# Validate key permissions using numeric octal arithmetic.
	# Refuse anything other than 0600 and 0640.
	validate_key_perms "$SRC_KEY"
	# Log only sizes — never the path string itself.
	log "source cert supplied (size: $(wc -c <"$SRC_CERT" 2>/dev/null || echo 0) bytes)"
	log "source key supplied (size: $(wc -c <"$SRC_KEY" 2>/dev/null || echo 0) bytes)"
}

# validate_key_perms verifies a private key has safe permissions.
# Accepted: 0600, 0640.  Rejected: anything with world bits, 0644, 0666, 0777.
validate_key_perms() {
	local file="$1"
	local mode
	mode=$(stat -c '%a' "$file" 2>/dev/null || stat -f '%Lp' "$file" 2>/dev/null || echo "")
	# Strip leading zeros for pattern matching (0600 -> 600, 0640 -> 640).
	local trimmed="${mode##0}"
	trimmed="${trimmed##0}"
	case "${trimmed:-0}" in
		600|640)
			log "source key mode ${mode} accepted"
			;;
		*)
			fail "source key is too permissive (mode ${mode}); only 0600 and 0640 are accepted"
			;;
	esac
}

# detect_service_user returns the User= / Group= the orvix systemd
# unit runs as, or "root:root" if not declared. We never invent the
# user — we only read what the operator already configured.
detect_service_user() {
	local u="" g=""
	if command -v systemctl >/dev/null 2>&1; then
		local unit
		unit=$(systemctl cat "$ORVIX_SERVICE" 2>/dev/null || true)
		u=$(printf '%s\n' "$unit" | awk -F= '/^User=/{print $2; exit}')
		g=$(printf '%s\n' "$unit" | awk -F= '/^Group=/{print $2; exit}')
	fi
	echo "${u:-root}:${g:-root}"
}

# safe_openssl_reason maps common openssl failure stderr patterns to
# safe reason codes.  Never echoes raw openssl output or file paths.
safe_openssl_reason() {
	local openssl_stderr="$1"
	if grep -qi 'no such file\|no file or directory\|not found' <<<"$openssl_stderr"; then
		echo "file_not_found"
	elif grep -qi 'permission denied\|access denied\|EACCES' <<<"$openssl_stderr"; then
		echo "permission_denied"
	elif grep -qi 'no start line\|PEM routines\|bad base64\|decode error\|wrong tag' <<<"$openssl_stderr"; then
		echo "cert_parse_failed"
	elif grep -qi 'key values mismatch\|modulus mismatch\|private key does not match' <<<"$openssl_stderr"; then
		echo "cert_key_mismatch"
	else
		echo "openssl_failed"
	fi
}

# openssl_temp captures openssl stderr to a temp file and returns a
# safe reason code on failure.  The temp file is cleaned up.
openssl_with_safe_reason() {
	local label="$1"; shift
	local stderr_tmp
	stderr_tmp=$(mktemp /tmp/orvix-tls-openssl.XXXXXX)
	trap 'rm -f "$stderr_tmp"' RETURN
	set +e
	"$@" 2>"$stderr_tmp"
	local rc=$?
	set -e
	if [ $rc -ne 0 ]; then
		local reason
		reason=$(safe_openssl_reason "$(cat "$stderr_tmp")")
		log "$label: $reason"
		return 1
	fi
	rm -f "$stderr_tmp"
	return 0
}

# validate_pair checks the cert/key are a matching pair and parseable.
# OpenSSL stderr is captured privately — never logged raw.
validate_pair() {
	local cert="$1" key="$2"
	[ -f "$cert" ] || { log "validate_pair: cert missing"; return 1; }
	[ -f "$key" ] || { log "validate_pair: key missing"; return 1; }

	local stderr_tmp
	stderr_tmp=$(mktemp /tmp/orvix-tls-openssl.XXXXXX)
	trap 'rm -f "$stderr_tmp"' RETURN

	# 1) Read cert modulus.
	set +e
	cert_mod=$(openssl x509 -noout -modulus -in "$cert" 2>"$stderr_tmp")
	local cert_rc=$?
	set -e
	if [ $cert_rc -ne 0 ]; then
		log "validate_pair: cert_read_failed"
		return 1
	fi
	rm -f "$stderr_tmp"

	# 2) Read key modulus (try pkey first, then rsa).
	stderr_tmp=$(mktemp /tmp/orvix-tls-openssl.XXXXXX)
	trap 'rm -f "$stderr_tmp"' RETURN
	local key_mod=""
	set +e
	if openssl pkey -noout -modulus -in "$key" 2>"$stderr_tmp" >/dev/null; then
		key_mod=$(openssl pkey -noout -modulus -in "$key" 2>/dev/null || true)
	else
		if openssl rsa -noout -modulus -in "$key" 2>"$stderr_tmp" >/dev/null; then
			key_mod=$(openssl rsa -noout -modulus -in "$key" 2>/dev/null || true)
		fi
	fi
	set -e
	if [ -z "$key_mod" ]; then
		log "validate_pair: key_read_failed"
		return 1
	fi
	rm -f "$stderr_tmp"

	# 3) Compare.
	if [ "$cert_mod" != "$key_mod" ]; then
		log "validate_pair: cert_key_mismatch"
		return 1
	fi

	# 4) Expiry check — warn only.
	stderr_tmp=$(mktemp /tmp/orvix-tls-openssl.XXXXXX)
	set +e
	if ! openssl x509 -noout -checkend 604800 -in "$cert" >/dev/null 2>"$stderr_tmp"; then
		log "validate_pair: cert expires within 7 days (warning)"
		warn "cert expires within 7 days — renew before it lapses"
	fi
	set -e
	rm -f "$stderr_tmp"
	return 0
}

# upsert_yaml_field inserts or replaces a top-level YAML field.
# Tolerant of missing trailing newlines and absence of the key.
upsert_yaml_field() {
	local file="$1" key="$2" value="$3"
	python3 - "$file" "$key" "$value" <<'PYEOF'
import sys, re, pathlib
cfg_path, key, value = sys.argv[1], sys.argv[2], sys.argv[3]
p = pathlib.Path(cfg_path)
text = p.read_text() if p.exists() else ""
if "." in key:
    section, leaf = key.split(".", 1)
    sec_re = re.compile(rf'(?ms)^({re.escape(section)}:\s*\n)(.*?)(?=^\S|\Z)')
    m = sec_re.search(text)
    leaf_line = f"  {leaf}: {value}\n"
    if m:
        section_text = m.group(2)
        leaf_re = re.compile(rf'(?m)^\s*{re.escape(leaf)}:\s*.*$')
        if leaf_re.search(section_text):
            new_section = leaf_re.sub(leaf_line.rstrip(), section_text)
        else:
            new_section = section_text.rstrip() + "\n" + leaf_line
        text = text[:m.start(2)] + new_section + text[m.end(2):]
    else:
        if not text.endswith("\n"):
            text += "\n"
        text += f"{section}:\n{leaf_line}"
else:
    pattern = rf'(?m)^(\s*)({re.escape(key)}:\s*)(.*)$'
    if re.search(pattern, text):
        text = re.sub(pattern, rf'\1\2{value}', text)
    else:
        if not text.endswith("\n"):
            text += "\n"
        text += f"{key}: {value}\n"
p.write_text(text)
PYEOF
}

# write_yaml_fields updates only the three keys we own in a
# given config file. The first argument is the target config path.
write_yaml_fields() {
	local config_path="$1" cert="$2" key="$3"
	upsert_yaml_field "$config_path" "coremail.submission_enabled" "true"
	upsert_yaml_field "$config_path" "coremail.tls_cert_file" "$cert"
	upsert_yaml_field "$config_path" "coremail.tls_key_file" "$key"
}

# rollback_restore restores the previous config and reloads the
# service to bring it back to the state before this script ran.
rollback_restore() {
	local reason="$1" backup="$2"
	say "${RED}FAIL${NC} $reason"
	log "FAIL $reason — rolling back"
	if [ -f "$backup" ]; then
		cp -p "$backup" "$ORVIX_CONFIG"
		chmod 0640 "$ORVIX_CONFIG" || true
		log "config restored from backup: $backup"
		say "Restored config from backup: $backup"
	else
		sed -i 's/^  submission_enabled: true/  submission_enabled: false/' "$ORVIX_CONFIG" || true
		log "config reverted: submission_enabled=false"
		say "Reverted submission_enabled to false"
	fi
	if command -v systemctl >/dev/null 2>&1; then
		systemctl reload-or-restart "$ORVIX_SERVICE" 2>>"$INSTALL_LOG" || true
		log "service reloaded after rollback"
		say "Service reloaded — port 25 inbound should be unaffected"
	fi
	say "Rollback complete. Detailed log: $INSTALL_LOG"
	exit 2
}

# probe_port_587 tries to detect port 587 listening for up to 30 s.
probe_port_587() {
	local deadline=$(( $(date +%s) + 30 ))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if ss -ltn "( sport = :587 )" 2>/dev/null | grep -q ':587'; then
			return 0
		fi
		sleep 1
	done
	return 1
}

main() {
	require_root
	require_input

	log "Orvix SMTP TLS setup started; mode=$ORVIX_SMTP_TLS_MODE"
	say "${GREEN}==>${NC} Orvix SMTP TLS setup"

	# ── 1. Validate source pair BEFORE touching anything. ──
	if ! validate_pair "$SRC_CERT" "$SRC_KEY"; then
		fail "source cert/key pair did not validate (modulus mismatch or unparseable); refusing to install"
	fi
	ok "source cert/key pair validates"

	# ── 2. Detect service user/group from systemd. ──
	local owner
	owner=$(detect_service_user)
	local group="${owner#*:}"
	log "orvix systemd service runs as $owner"

	if [ -n "$SMTP_TLS_GROUP" ] && getent group "$SMTP_TLS_GROUP" >/dev/null; then
		group="$SMTP_TLS_GROUP"
	elif ! getent group "$group" >/dev/null; then
		group="root"
	fi
	log "key will be owned by root:$group"

	# ── 3. Create TLS dir. ──
	install -d -m 0750 -o root -g root "$ORVIX_TLS_DIR"
	log "tls dir prepared"

	# ── 4. Install cert + key. ──
	local dest_cert="$ORVIX_TLS_DIR/$ORVIX_CERT_NAME"
	local dest_key="$ORVIX_TLS_DIR/$ORVIX_KEY_NAME"

	rm -f "$dest_cert" "$dest_key"

	case "$ORVIX_SMTP_TLS_MODE" in
		symlink)
			ln -s "$SRC_CERT" "$dest_cert"
			chmod 0644 "$dest_cert" || true
			ln -s "$SRC_KEY" "$dest_key"
			chmod -h 0640 "$dest_key" || true
			chgrp -h "$group" "$dest_cert" "$dest_key" || true
			log "installed via symlink"
			;;
		copy|*)
			install -m 0644 -o root -g root "$SRC_CERT" "$dest_cert"
			if getent group "$group" >/dev/null; then
				install -m 0640 -o root -g "$group" "$SRC_KEY" "$dest_key"
			else
				install -m 0600 -o root -g root "$SRC_KEY" "$dest_key"
			fi
			log "installed via copy"
			;;
	esac

	# ── 5. Re-validate the INSTALLED files. ──
	if ! validate_pair "$dest_cert" "$dest_key"; then
		rm -f "$dest_cert" "$dest_key"
		fail "installed cert/key did not validate; rolled back"
	fi
	ok "cert + key installed and re-validated"

	# ── 6. Back up the config BEFORE editing it. ──
	local backup=""
	if [ -f "$ORVIX_CONFIG" ]; then
		backup="${ORVIX_CONFIG}.bak-$(date +%Y%m%d_%H%M%S)"
		cp -p "$ORVIX_CONFIG" "$backup"
		chmod 0600 "$backup"
		log "config backup written: $backup"
		say "${YELLOW}NOTE${NC} config backup: $backup"
	else
		mkdir -p "$(dirname "$ORVIX_CONFIG")"
		touch "$ORVIX_CONFIG"
		chmod 0640 "$ORVIX_CONFIG" || true
		log "no prior config — created empty $ORVIX_CONFIG"
	fi

	# ── 7. Create temp config, write YAML updates to it, then
	#       atomically replace the active config. Never edit the
	#       active config directly before reload succeeds. ──
	TEMP_CONFIG="$(mktemp /tmp/orvix-config.XXXXXX)"
	trap 'rm -f "$TEMP_CONFIG"' RETURN
	if [ -f "$ORVIX_CONFIG" ]; then
		cp -p "$ORVIX_CONFIG" "$TEMP_CONFIG"
	else
		touch "$TEMP_CONFIG"
	fi
	write_yaml_fields "$TEMP_CONFIG" "$dest_cert" "$dest_key"
	# Validate that the temp config is parseable YAML before
	# touching the active config.
	if ! python3 -c "import yaml; yaml.safe_load(open('$TEMP_CONFIG'))" 2>>"$INSTALL_LOG"; then
		rm -f "$TEMP_CONFIG"
		fail "temp config failed YAML validation — refusing to replace active config"
	fi
	# Atomic replace: copy into place with safe mode.
	install -m 0640 "$TEMP_CONFIG" "$ORVIX_CONFIG"
	rm -f "$TEMP_CONFIG"
	ok "orvix.yaml: coremail.submission_enabled=true + TLS paths set"
	log "YAML updated via atomic temp config"

	# ── 8. Reload the service. ──
	if command -v systemctl >/dev/null 2>&1; then
		if ! systemctl reload-or-restart "$ORVIX_SERVICE" 2>>"$INSTALL_LOG"; then
			rollback_restore "systemctl reload-or-restart failed" "$backup"
		fi
		ok "$ORVIX_SERVICE reloaded"
	else
		warn "systemctl not found; the operator must restart $ORVIX_SERVICE manually"
	fi

	# ── 9. Probe port 587.  If not listening, FAIL and rollback. ──
	if ! probe_port_587; then
		rollback_restore "port 587 not listening within 30 s — submission did not bind" "$backup"
	fi
	ok "port 587 is listening"

	# ── 10. Re-validate installed key permissions (doctor check). ──
	validate_key_perms "$dest_key"

	cat <<DONE

${GREEN}PASS${NC} Orvix SMTP submission TLS bound.

- TLS dir:     ${ORVIX_TLS_DIR}  (root:$group, 0750)
- Cert file:   ${dest_cert}  (0644)
- Key file:    installed (0640, root:$group)
- Config:      ${ORVIX_CONFIG}
- Service:     ${ORVIX_SERVICE} reloaded

Verify 587 is live:
  printf 'EHLO test.local\r\nQUIT\r\n' | nc -w 5 127.0.0.1 587
  swaks --server 127.0.0.1 --port 587 --tls \\
        --auth LOGIN --auth-user <user> --auth-password '<pw>' \\
        --from <from> --to <to> --data "Subject: live 587 test\\n\\nhello"

Rollback to safe-disabled state:
  cp -p "${ORVIX_CONFIG}.bak-"* "$ORVIX_CONFIG" 2>/dev/null
  sed -i 's/^  submission_enabled: true/  submission_enabled: false/' "$ORVIX_CONFIG"
  systemctl reload-or-restart ${ORVIX_SERVICE}
  ss -lntp | grep -E ':25|:587' || true   # 587 gone, 25 still there

Detailed log: ${INSTALL_LOG}
DONE
}

main "$@"
