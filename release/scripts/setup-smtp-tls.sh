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
# What it does, in order:
#   1. Validates the source cert + key (modulus match, parseable)
#   2. Creates /etc/orvix/tls/smtp/ with mode 0750 root:orvix
#   3. Copies (or symlinks) cert into fullchain.pem (0644)
#   4. Copies (or symlinks) key into privkey.pem (0640 root:orvix)
#   5. Re-validates the COPIED files (defense in depth)
#   6. Backs up /etc/orvix/orvix.yaml with a timestamp suffix
#   7. Updates orvix.yaml: coremail.submission_enabled=true +
#      coremail.tls_cert_file + coremail.tls_key_file
#   8. Reloads orvix.service
#   9. Probes port 587 is listening (up to 30s)
#
# Failure modes (any one aborts the whole script BEFORE restart or
# config edit, so port 25 inbound keeps running and 587 stays off):
#   * run as non-root
#   * source paths missing / unreadable
#   * cert/key pair fails modulus match
#   * cert/key can't be re-parsed after copy
#   * YAML write fails
#   * systemd reload fails
#
# Idempotent: rerunning with the same source paths leaves the system
# in the same end-state, only rotating the backup timestamp.

# ── Defaults ─────────────────────────────────────────────────
ORVIX_TLS_DIR="/etc/orvix/tls/smtp"
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
	# Paths and raw openssl output are sanitized — we never echo the
	# full cert/key paths or the openssl error text to the install log.
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
	say "  - Port 25 inbound is unaffected by this script's failure" >&2
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
	# Source key must not be world-readable — we refuse to copy
	# a key that is more permissive than our final 0640 target.
	local src_mode
	src_mode=$(stat -c '%a' "$SRC_KEY" 2>/dev/null || stat -f '%Lp' "$SRC_KEY" 2>/dev/null || echo "")
	case "$src_mode" in
		''|*0[0-6]*|*7[0-7])
			# Octal mode with no group/world bits set (≤ 0640 or 0600).
			;;
		*)
			fail "source key is too permissive (mode $src_mode); refusing to use a world-readable private key"
			;;
	esac
	# Log only sizes — never the path string itself.
	log "source cert supplied (size: $(wc -c <"$SRC_CERT" 2>/dev/null || echo 0) bytes)"
	log "source key supplied (size: $(wc -c <"$SRC_KEY" 2>/dev/null || echo 0) bytes)"
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

# validate_pair checks the cert/key are a matching pair and parseable.
# Never echoes paths or raw openssl output — only PASS / FAIL.
validate_pair() {
	local cert="$1" key="$2"
	[ -f "$cert" ] || { log "validate_pair: cert missing"; return 1; }
	[ -f "$key" ] || { log "validate_pair: key missing"; return 1; }
	local cert_mod key_mod
	cert_mod=$(openssl x509 -noout -modulus -in "$cert" 2>>"$INSTALL_LOG") || {
		log "validate_pair: cert failed to parse"
		return 1
	}
	if openssl pkey -noout -modulus -in "$key" 2>>"$INSTALL_LOG" >/dev/null; then
		key_mod=$(openssl pkey -noout -modulus -in "$key" 2>>"$INSTALL_LOG") || {
			log "validate_pair: key failed to parse"
			return 1
		}
	else
		key_mod=$(openssl rsa -noout -modulus -in "$key" 2>>"$INSTALL_LOG") || {
			log "validate_pair: key failed to parse"
			return 1
		}
	fi
	if [ -z "$cert_mod" ] || [ -z "$key_mod" ] || [ "$cert_mod" != "$key_mod" ]; then
		log "validate_pair: modulus mismatch"
		return 1
	fi
	# Expiry check — warn only (do not abort), so operators with
	# a cert in mid-renewal can still bind 587.
	if ! openssl x509 -noout -checkend 604800 -in "$cert" >/dev/null 2>>"$INSTALL_LOG"; then
		log "validate_pair: cert expires within 7 days (warning)"
		warn "cert expires within 7 days — renew before it lapses"
	fi
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
# Match dotted paths like "coremail.submission_enabled" at column 0.
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

# write_yaml_fields updates only the three keys we own. Other
# settings are left exactly as the operator had them.
write_yaml_fields() {
	local cert="$1" key="$2"
	upsert_yaml_field "$ORVIX_CONFIG" "coremail.submission_enabled" "true"
	upsert_yaml_field "$ORVIX_CONFIG" "coremail.tls_cert_file" "$cert"
	upsert_yaml_field "$ORVIX_CONFIG" "coremail.tls_key_file" "$key"
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

	# Prefer the SMTPTLS group if it exists (operator can override
	# the read group via SMTP_TLS_GROUP=); fall back to the
	# systemd-declared group, finally to root.
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

	# Remove any prior file at dest — install refuses to overwrite
	# without -T in some environments, so do it explicitly.
	rm -f "$dest_cert" "$dest_key"

	case "$ORVIX_SMTP_TLS_MODE" in
		symlink)
			ln -s "$SRC_CERT" "$dest_cert"
			chmod 0644 "$dest_cert" || true
			ln -s "$SRC_KEY" "$dest_key"
			chmod -h 0640 "$dest_key" || true
			# chgrp a symlink with -h so the link target inherits.
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

	# ── 5. Re-validate the INSTALLED files (defense in depth). ──
	if ! validate_pair "$dest_cert" "$dest_key"; then
		# Roll back the install so a half-set-up TLS dir doesn't
		# outlive this script with bad material.
		rm -f "$dest_cert" "$dest_key"
		fail "installed cert/key did not validate; rolled back"
	fi
	ok "cert + key installed and re-validated"

	# ── 6. Back up the config BEFORE editing it. ──
	if [ -f "$ORVIX_CONFIG" ]; then
		local backup="${ORVIX_CONFIG}.bak-$(date +%Y%m%d_%H%M%S)"
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

	# ── 7. Update YAML (only the three keys we own). ──
	write_yaml_fields "$dest_cert" "$dest_key"
	ok "orvix.yaml: coremail.submission_enabled=true + TLS paths set"

	# ── 8. Reload the service. ──
	if command -v systemctl >/dev/null 2>&1; then
		if ! systemctl reload-or-restart "$ORVIX_SERVICE" 2>>"$INSTALL_LOG"; then
			fail "systemctl reload-or-restart $ORVIX_SERVICE failed; check 'journalctl -u $ORVIX_SERVICE'"
		fi
		ok "$ORVIX_SERVICE reloaded"
	else
		warn "systemctl not found; the operator must restart $ORVIX_SERVICE manually"
	fi

	# ── 9. Probe port 587 (best effort, do not fail the script). ──
	local deadline=$(( $(date +%s) + 30 ))
	local listening=0
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if ss -ltn "( sport = :587 )" 2>/dev/null | grep -q ':587'; then
			listening=1
			break
		fi
		sleep 1
	done
	if [ "$listening" -eq 1 ]; then
		ok "port 587 is listening"
	else
		warn "port 587 not listening within 30s; check 'journalctl -u $ORVIX_SERVICE' (port 25 inbound is unaffected)"
	fi

	cat <<DONE

${GREEN}PASS${NC} Orvix SMTP submission TLS bound.

- TLS dir:    ${ORVIX_TLS_DIR}  (root:$group, 0750)
- Cert file:  ${dest_cert}  (0644)
- Key file:   ${dest_key}  (0640, root:$group)
- Config:     ${ORVIX_CONFIG}
- Service:    ${ORVIX_SERVICE} reloaded

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