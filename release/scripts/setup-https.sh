#!/usr/bin/env bash
set -euo pipefail

# Orvix RC1 HTTPS reverse proxy setup for Ubuntu VPS hosts.
# This configures Caddy for:
#   admin.<domain>   -> 127.0.0.1:8080
#   webmail.<domain> -> 127.0.0.1:8080   (path-rewritten to /webmail/*)
#   mail.<domain>    -> 127.0.0.1:8080   (/api/* paths)
#   mail.<domain>    -> 127.0.0.1:8081   (everything else: JMAP/SMTP submission web/IMAP/POP3)

PRIMARY_DOMAIN="${1:-${ORVIX_PRIMARY_DOMAIN:-}}"
SERVER_IP="${2:-${ORVIX_SERVER_IP:-}}"
ADMIN_DOMAIN="${ORVIX_ADMIN_DOMAIN:-}"
WEBMAIL_DOMAIN="${ORVIX_WEBMAIL_DOMAIN:-}"
MAIL_DOMAIN="${ORVIX_MAIL_DOMAIN:-}"
INSTALL_LOG="${INSTALL_LOG:-/var/log/orvix/https-setup.log}"

export DEBIAN_FRONTEND=noninteractive
export NEEDRESTART_MODE=a

RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
YELLOW=$'\033[1;33m'
NC=$'\033[0m'

log() {
	mkdir -p "$(dirname "$INSTALL_LOG")"
	printf '[%s] %s\n' "$(date -Is)" "$*" >>"$INSTALL_LOG"
}

run_quiet() {
	log "RUN $*"
	"$@" >>"$INSTALL_LOG" 2>&1
}

fail() {
	echo -e "${RED}ERROR:${NC} $*" >&2
	echo "Detailed log: $INSTALL_LOG" >&2
	if [ -f "$INSTALL_LOG" ]; then
		echo "Last 80 log lines:" >&2
		tail -n 80 "$INSTALL_LOG" >&2 || true
	fi
	exit 1
}

require_root() {
	[ "$(id -u)" -eq 0 ] || fail "run as root or with sudo"
}

# is_valid_public_ipv4 mirrors release/install.sh. Returns 0 iff $1
# is a syntactically valid, routable, public IPv4 address. Rejects
# empty, IPv6 (':' present), 0.0.0.0, loopback (127/8), RFC1918
# private (10/8, 172.16/12, 192.168/16), link-local (169.254/16),
# RFC5737 documentation (192.0.2/24, 198.51.100/24, 203.0.113/24),
# multicast (224/4), and reserved (240/4). See install.sh for the
# full rationale — the runtime rejects every one of these in
# dns.public_ipv4 and the dashboard surfaces a confusing 422 when
# the install wrote junk.
is_valid_public_ipv4() {
	local ip="${1:-}"
	[ -n "$ip" ] || return 1
	# Strict dotted-quad with no leading zeros (rejects
	# `065.75.203.74` as non-canonical). Each octet matches:
	#   25[0-5] | 2[0-4][0-9] | 1[0-9][0-9] | [1-9]?[0-9]
	if ! [[ "$ip" =~ ^((25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])\.){3}(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])$ ]]; then
		return 1
	fi
	local o1 o2 o3 o4
	IFS=. read -r o1 o2 o3 o4 <<< "$ip"
	[ "$o1" -eq 0 ] && return 1
	[ "$o1" -eq 10 ] && return 1
	[ "$o1" -eq 100 ] && [ "$o2" -ge 64 ] && [ "$o2" -le 127 ] && return 1
	[ "$o1" -eq 127 ] && return 1
	[ "$o1" -eq 169 ] && [ "$o2" -eq 254 ] && return 1
	[ "$o1" -eq 172 ] && [ "$o2" -ge 16 ] && [ "$o2" -le 31 ] && return 1
	[ "$o1" -eq 192 ] && [ "$o2" -eq 0 ] && [ "$o3" -eq 0 ] && return 1
	[ "$o1" -eq 192 ] && [ "$o2" -eq 0 ] && [ "$o3" -eq 2 ] && return 1
	[ "$o1" -eq 192 ] && [ "$o2" -eq 168 ] && return 1
	[ "$o1" -eq 198 ] && { [ "$o2" -eq 18 ] || [ "$o2" -eq 19 ]; } && return 1
	[ "$o1" -eq 198 ] && [ "$o2" -eq 51 ] && [ "$o3" -eq 100 ] && return 1
	[ "$o1" -eq 203 ] && [ "$o2" -eq 0 ] && [ "$o3" -eq 113 ] && return 1
	[ "$o1" -ge 224 ] && [ "$o1" -le 239 ] && return 1
	[ "$o1" -ge 240 ] && return 1
	return 0
}

# detect_public_ipv4_from_host_ips iterates the IPv4 addresses
# reported by `hostname -I` and returns the first one that
# passes `is_valid_public_ipv4`. Prints nothing (and exits
# non-zero) when no address is acceptable. Mirrors install.sh.
detect_public_ipv4_from_host_ips() {
	local line ip
	line="$(hostname -I 2>/dev/null || true)"
	[ -n "$line" ] || return 1
	local -a candidates
	# shellcheck disable=SC2206
	candidates=( $line )
	for ip in "${candidates[@]}"; do
		[ -n "$ip" ] || continue
		case "$ip" in
			*:*) continue ;;
		esac
		if is_valid_public_ipv4 "$ip"; then
			printf '%s' "$ip"
			return 0
		fi
	done
	return 1
}

require_input() {
	if [ -z "$PRIMARY_DOMAIN" ]; then
		fail "usage: $0 <primary-domain> [server-ip]"
	fi
	[[ "$PRIMARY_DOMAIN" =~ ^[A-Za-z0-9][A-Za-z0-9.-]*\.[A-Za-z]{2,}$ ]] || fail "invalid primary domain: $PRIMARY_DOMAIN"
	ADMIN_DOMAIN="${ADMIN_DOMAIN:-admin.$PRIMARY_DOMAIN}"
	WEBMAIL_DOMAIN="${WEBMAIL_DOMAIN:-webmail.$PRIMARY_DOMAIN}"
	MAIL_DOMAIN="${MAIL_DOMAIN:-mail.$PRIMARY_DOMAIN}"
	# Detection: prefer the explicit second arg / ORVIX_SERVER_IP
	# (operator-supplied wins). Fall back to scanning hostname -I
	# for the first valid public IPv4. NEVER silently coerce to
	# loopback/private — the previous behaviour would patch
	# dns.public_ipv4 with junk that the runtime then rejects.
	if [ -z "$SERVER_IP" ]; then
		SERVER_IP="$(detect_public_ipv4_from_host_ips || true)"
	fi
	[ -n "$SERVER_IP" ] || fail "server IP could not be detected; pass it as second argument or export ORVIX_SERVER_IP=<ip>"
	# Validate the supplied/detected SERVER_IP. We refuse to
	# write junk into dns.public_ipv4 — the previous behaviour
	# would have patched a private/loopback address, which the
	# runtime then rejected, which the dashboard surfaced as a
	# confusing 422.
	if ! is_valid_public_ipv4 "$SERVER_IP"; then
		fail "invalid public IPv4 for dns.public_ipv4: $SERVER_IP (rejects empty, IPv6, 0.0.0.0, loopback, RFC1918 private, link-local, RFC5737 documentation, multicast, reserved). Pass a real public IPv4 as the second argument or export ORVIX_SERVER_IP=<ip>."
	fi
}

install_caddy() {
	if command -v caddy >/dev/null 2>&1; then
		log "caddy already installed: $(caddy version 2>/dev/null || true)"
		return
	fi
	run_quiet apt-get update -qq
	run_quiet apt-get install -y -qq ca-certificates curl gnupg debian-keyring debian-archive-keyring apt-transport-https ufw
	run_quiet install -d -m 0755 /usr/share/keyrings
	run_quiet bash -c "curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/gpg.key | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg"
	run_quiet bash -c "curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt > /etc/apt/sources.list.d/caddy-stable.list"
	run_quiet apt-get update -qq
	run_quiet apt-get install -y -qq caddy
}

write_caddyfile() {
	cat > /etc/caddy/Caddyfile <<CADDY
$ADMIN_DOMAIN {
	reverse_proxy 127.0.0.1:8080
}

$WEBMAIL_DOMAIN {
	# API requests hit the Go binary directly (no path
	# rewrite) so the same /api/v1/* code paths serve
	# both admin.<domain> and webmail.<domain>.
	@api path /api/*
	handle @api {
		reverse_proxy 127.0.0.1:8080
	}
	# Webmail SPA assets and routes pass through without
	# rewrite. The Go backend serves /webmail/assets/* as
	# static files with correct MIME types and /webmail/*
	# as the SPA fallback (index.html for unknown paths).
	# Without this separate handler, the catch-all below
	# would double-prefix every /webmail/* path with an
	# additional /webmail (rewrite * /webmail{uri}) and the
	# backend would serve text/html for every JS/CSS asset.
	@webmail path /webmail /webmail/*
	handle @webmail {
		reverse_proxy 127.0.0.1:8080
	}
	# Shorthand /assets/* paths — rewrite to /webmail/assets/*
	# so the Go backend can serve them from the same directory.
	@assets path /assets /assets/*
	handle @assets {
		rewrite * /webmail{uri}
		reverse_proxy 127.0.0.1:8080
	}
	# Catch-all: everything else (/, /any/path) is rewritten
	# to /webmail{uri} and served by the Go backend, which
	# returns index.html for the SPA root or unknown routes.
	handle {
		rewrite * /webmail{uri}
		reverse_proxy 127.0.0.1:8080
	}
}

$MAIL_DOMAIN {
	# API requests hit the Go binary at 8080.
	@api path /api/*
	handle @api {
		reverse_proxy 127.0.0.1:8080
	}

	# Webmail static assets and service worker: the Go backend
	# serves /webmail/assets/* and /webmail/sw.js with correct
	# MIME types so the Caddy reverse-proxy does not double-
	# prefix paths. This route is required for the push
	# service worker to be reachable on mail.<domain>.
	@webmail path /webmail /webmail/*
	handle @webmail {
		reverse_proxy 127.0.0.1:8080
	}

	# Shorthand /assets/* paths — rewrite to /webmail/assets/*
	@assets path /assets /assets/*
	handle @assets {
		rewrite * /webmail{uri}
		reverse_proxy 127.0.0.1:8080
	}

	# Mail client autodiscover/autoconfig endpoints are public
	# API routes served by the Go backend. Keep these before
	# the JMAP catch-all so Outlook and Thunderbird do not get
	# sent to the 8081 listener.
	handle /autodiscover/* {
		reverse_proxy 127.0.0.1:8080
	}
	handle /Autodiscover/* {
		reverse_proxy 127.0.0.1:8080
	}
	handle /.well-known/autoconfig/* {
		reverse_proxy 127.0.0.1:8080
	}
	handle /mail/config-v1.1.xml {
		reverse_proxy 127.0.0.1:8080
	}

	# Everything else (JMAP, SMTP submission web, IMAP, POP3)
	# goes to the outbound proxy at 8081.
	handle {
		reverse_proxy 127.0.0.1:8081
	}
}
CADDY
	chown root:caddy /etc/caddy/Caddyfile 2>/dev/null || chown root:root /etc/caddy/Caddyfile
	chmod 0644 /etc/caddy/Caddyfile
}

open_firewall() {
	# Open the ports required for a working mail server. The
	# fresh install installs the ufw package but does NOT enable
	# the firewall (the operator is expected to enable it once
	# they are happy with the rules below). On hosts where ufw
	# is already active, these `allow` rules are required or the
	# VPS will silently reject every SMTP/IMAP/POP3 connection.
	#
	# 25/tcp   — SMTP MX (inbound + outbound delivery)
	# 110/tcp  — POP3 (insecure; advisory only when TLS is on)
	# 143/tcp  — IMAP
	# 587/tcp  — SMTP submission (STARTTLS); only opened when
	#            coremail.submission_enabled is true in config
	# 465/tcp  — SMTP over implicit TLS; only opened when
	#            coremail.smtps_enabled is true in config
	# 993/tcp  — IMAPS (TLS-wrapped IMAP)
	# 995/tcp  — POP3S (TLS-wrapped POP3)
	# 80/tcp   — Caddy HTTP (ACME challenge + redirect)
	# 443/tcp  — Caddy HTTPS
	#
	# 8080/tcp + 8081/tcp (admin + JMAP internal) are NOT opened
	# here. They are bound to 127.0.0.1 only by default so they
	# are unreachable from the public internet regardless of ufw
	# state, and the `post_https_firewall_hardening` block at the
	# bottom of this script emits the explicit deny rules as a
	# belt-and-braces measure for hosts that previously had the
	# old unsafe 0.0.0.0 defaults.
	if command -v ufw >/dev/null 2>&1; then
		run_quiet ufw allow 25/tcp
		run_quiet ufw allow 110/tcp
		run_quiet ufw allow 143/tcp
		run_quiet ufw allow 587/tcp
		run_quiet ufw allow 465/tcp
		run_quiet ufw allow 993/tcp
		run_quiet ufw allow 995/tcp
		run_quiet ufw allow 80/tcp
		run_quiet ufw allow 443/tcp
	fi
}

# post_https_firewall_hardening prints the deny commands the
# operator should run as soon as setup-https.sh has verified
# the public HTTPS endpoints are reachable. It does NOT run them
# automatically — that would lock an operator out of a host whose
# Caddyfile has a typo. The commands are emitted in the success
# banner so they are impossible to miss.
# patch_dns_public_ipv4 ensures /etc/orvix/orvix.yaml has a
# `dns.public_ipv4:` field set to $SERVER_IP. This is required by
# the admin DNS/DKIM dashboard, which refuses to generate A / MX
# / SPF / DKIM / DMARC records unless the public mail IPv4 is
# configured. The install.sh write_config() heredoc already writes
# this on a fresh install; this function is the recovery path for
# hosts where:
#
#   - the installer wrote the config BEFORE server_public_ip was
#     known (legacy installs), OR
#   - the operator re-ran setup-https.sh with a different public IP
#     (e.g. after a VPS migration or IP reassignment).
#
# Behaviour matrix (mirrors release/install.sh:
# migrate_dns_public_ip()):
#
#   dns.public_ipv4 missing         -> ADD `public_ipv4: "$SERVER_IP"`
#                                       under the dns: section (or as a
#                                       fresh dns: block if none exists)
#                                       and return 0 with PATCH_CHANGED=1
#   dns.public_ipv4 == SERVER_IP    -> no change (idempotent)
#                                       and return 0 with PATCH_CHANGED=0
#   dns.public_ipv4 != SERVER_IP    -> FAIL with a clear message
#                                       telling the operator the two
#                                       values differ; NEVER overwrite
#                                       silently (operator's intent)
#   /etc/orvix/orvix.yaml missing   -> FAIL with a clear message;
#                                       install.sh must run first.
#
# We never infer the public IP from coremail.smtp_host. That is a
# listener bind address (default 0.0.0.0) and has nothing to do
# with the public DNS plan.
#
# On success, sets the global PATCH_CHANGED=1 when the config
# file was modified, PATCH_CHANGED=0 otherwise. main() uses this
# to decide whether to restart orvix (we MUST restart after a
# patch so the runtime sees the new value before the dashboard
# verification gate; we MUST NOT restart when the patch was a
# no-op to avoid a needless runtime disruption).
patch_dns_public_ipv4() {
	PATCH_CHANGED=0
	# ORVIX_CONFIG can override the production config path so
	# the installer test harness can drive this function against
	# a temporary file. Production callers (the operator running
	# setup-https.sh directly) leave ORVIX_CONFIG unset, so the
	# default /etc/orvix/orvix.yaml applies.
	local cfg="${ORVIX_CONFIG:-/etc/orvix/orvix.yaml}"
	if [ ! -f "$cfg" ]; then
		fail "$cfg does not exist; run install.sh before setup-https.sh (the public DNS plan requires dns.public_ipv4 written by install.sh)"
	fi
	if ! [ -r "$cfg" ]; then
		fail "$cfg is not readable; rerun setup-https.sh as root"
	fi

	# Locate the dns: section and read the existing public_ipv4
	# value (if any). We use awk with section tracking so we
	# never read a `public_ipv4` key from an unrelated section.
	local existing
	existing="$(awk '
		BEGIN { in_dns = 0 }
		/^dns:/ { in_dns = 1; next }
		in_dns && /^[a-zA-Z][a-zA-Z0-9_-]*:/ { in_dns = 0 }
		in_dns && /^[[:space:]]+public_ipv4:[[:space:]]*/ {
			# Strip the leading `  public_ipv4:` and any
			# surrounding quotes / whitespace.
			val = $0
			sub(/^[[:space:]]+public_ipv4:[[:space:]]*/, "", val)
			gsub(/^"|"$/, "", val)
			gsub(/^'\''|'\''$/, "", val)
			print val
			exit 0
		}
		END { exit 0 }
	' "$cfg" 2>/dev/null || true)"

	if [ -z "$existing" ]; then
		# Missing — ADD it. We refuse to silently fabricate
		# `127.0.0.1` because the DNS plan generator would
		# then refuse to publish; better to fail loudly.
		local has_dns=0
		if grep -qE '^dns:' "$cfg"; then
			has_dns=1
		fi
		if [ "$has_dns" = "1" ]; then
			# Insert as the first key under dns:.
			local tmp
			tmp="$(mktemp "${cfg}.dns-patch-XXXXXX")"
			awk -v ip="$SERVER_IP" '
			BEGIN { in_dns = 0; inserted = 0 }
			/^dns:/ { print; in_dns = 1; next }
			in_dns && !inserted && /^[[:space:]]+[a-zA-Z_][a-zA-Z0-9_-]*:/ {
				print "  public_ipv4: \"" ip "\""
				inserted = 1
				in_dns = 0
			}
			{ print }
			END { if (in_dns && !inserted) print "  public_ipv4: \"" ip "\"" }
			' "$cfg" > "$tmp" && mv "$tmp" "$cfg"
		else
			# No dns: section at all — append a fresh one.
			{
				echo ""
				echo "dns:"
				echo "  public_ipv4: \"$SERVER_IP\""
			} >> "$cfg"
		fi
		chmod 0640 "$cfg" 2>/dev/null || true
		log "patched dns.public_ipv4: ADDED $SERVER_IP to $cfg (was missing)"
		PATCH_CHANGED=1
		return 0
	fi

	if [ "$existing" = "$SERVER_IP" ]; then
		log "patched dns.public_ipv4: $existing == $SERVER_IP (no change)"
		PATCH_CHANGED=0
		return 0
	fi

	# Mismatch — fail loudly. We never silently overwrite a
	# value the operator may have intentionally set to a
	# different public IP (for example, behind a NAT or on a
	# multi-homed host). The operator must resolve the
	# discrepancy before the DNS Ops dashboard can publish
	# records.
	fail "dns.public_ipv4 mismatch: $cfg has $existing, but setup-https.sh was invoked with $SERVER_IP. Edit $cfg manually to set dns.public_ipv4 to the correct public mail IPv4, OR rerun setup-https.sh with the matching IP. The public DNS plan will not publish until this is resolved."
}

# restart_orvix_after_patch restarts the orvix systemd service so
# the runtime process picks up the new dns.public_ipv4 value
# before the dashboard verification gate runs. We only restart
# when PATCH_CHANGED=1; an idempotent no-op (existing value
# already matches) MUST NOT trigger a restart.
#
# Why restart instead of `systemctl reload`: the Go binary reads
# dns.public_ipv4 from /etc/orvix/orvix.yaml at startup. We do
# not implement a hot-reload for that field — restart is the
# only safe path that guarantees the live process sees the new
# value, and the previous "config-patch without restart" was
# leaving the runtime on the stale value while the dashboard
# verification gate failed with 422 ("public mail IPv4 is not
# configured"). The previous symptom was a setup-https.sh that
# patched the config, the dashboard returning 422, and the
# operator concluding the hotfix did not work — when in fact
# the runtime had never been told to re-read the config.
#
# Readiness is gated by `wait_for_orvix_readiness`, which polls
# the same two endpoints install.sh uses (8080/health and
# 8081/jmap) plus the listener sockets, so we never claim a
# ready state while the listener goroutines are still binding
# sockets. Any failure in restart OR readiness fails the whole
# script — we never leave the operator with a "patched config,
# runtime still down" half-state that the previous code shipped.
restart_orvix_after_patch() {
	if [ "${PATCH_CHANGED:-0}" -ne 1 ]; then
		log "restart_orvix_after_patch: PATCH_CHANGED=0 (no patch applied) — skipping restart to avoid needless runtime disruption"
		return 0
	fi
	log "restart_orvix_after_patch: PATCH_CHANGED=1 — restarting orvix so the runtime sees dns.public_ipv4"
	if ! systemctl restart orvix 2>>"$INSTALL_LOG"; then
		log "restart_orvix_after_patch: systemctl restart orvix FAILED (see journalctl -u orvix)"
		fail "systemctl restart orvix failed after patching dns.public_ipv4; runtime config is stale. Check \`journalctl -u orvix --no-pager\` and rerun setup-https.sh once orvix is healthy."
	fi
	wait_for_orvix_readiness
}

# wait_for_orvix_readiness polls the same two HTTP endpoints and
# listener sockets install.sh's
# wait_for_runtime_ready_after_restart probes. We duplicate the
# helper here so setup-https.sh has no install.sh dependency at
# runtime — it must work on a host where install.sh was run by a
# previous operator version and the file is gone, or where the
# host's /etc/orvix is brand-new (recovery path). The deadline is
# bounded by ORVIX_READINESS_DEADLINE_SECONDS (default 30) so a
# truly stuck runtime surfaces a clear error within a minute.
wait_for_orvix_readiness() {
	local deadline_secs="${ORVIX_READINESS_DEADLINE_SECONDS:-30}"
	local deadline=$((SECONDS + deadline_secs))
	local attempt=0
	while [ "$SECONDS" -lt "$deadline" ]; do
		attempt=$((attempt + 1))
		if curl -fsS http://127.0.0.1:8080/api/v1/health >/dev/null 2>&1 \
			&& curl -fsS http://127.0.0.1:8081/.well-known/jmap >/dev/null 2>&1 \
			&& systemctl is-active --quiet orvix 2>/dev/null; then
			log "wait_for_orvix_readiness: orvix ready after $attempt attempt(s)"
			return 0
		fi
		sleep 1
	done
	# Capture diagnostics into the install log and fail closed.
	log "wait_for_orvix_readiness: TIMEOUT after ${deadline_secs}s (attempt=$attempt, PATCH_CHANGED=${PATCH_CHANGED:-0})"
	{
		echo "=== wait_for_orvix_readiness: systemctl status orvix ==="
		systemctl status orvix 2>&1 || true
		echo "=== wait_for_orvix_readiness: journalctl -u orvix --since '2 minutes ago' ==="
		journalctl -u orvix --since "2 minutes ago" --no-pager 2>&1 || true
	} >> "$INSTALL_LOG" 2>&1 || true
	fail "orvix did not become ready within ${deadline_secs}s after setup-https.sh patched dns.public_ipv4. The runtime config may be stale; check \`systemctl status orvix\` and \`journalctl -u orvix --no-pager\`, then rerun setup-https.sh once orvix is healthy. See $INSTALL_LOG for the captured diagnostics."
}

post_https_firewall_hardening() {
	cat <<HARDEN

Post-HTTPS firewall hardening (run as soon as the URLs above
are reachable from a remote browser):

  sudo ufw deny 8080/tcp     # admin + webmail (internal only)
  sudo ufw deny 8081/tcp     # JMAP (internal only)

These deny rules do NOT affect HTTPS — Caddy terminates on
127.0.0.1:8080 and 127.0.0.1:8081, so blocking external access
to those ports breaks nothing the public hostname depends on,
but it stops a misconfigured admin UI from being reachable on
the bare server IP.

Recommended firewall posture after HTTPS:
  sudo ufw default deny incoming
  sudo ufw allow 22/tcp          # SSH (if not already)
  sudo ufw allow 25/tcp          # SMTP MX
  sudo ufw allow 110/tcp         # POP3 (advisory)
  sudo ufw allow 143/tcp         # IMAP
  sudo ufw allow 587/tcp         # submission (if enabled)
  sudo ufw allow 465/tcp         # SMTPS (if enabled)
  sudo ufw allow 993/tcp         # IMAPS
  sudo ufw allow 995/tcp         # POP3S
  sudo ufw allow 80/tcp          # Caddy HTTP (ACME + redirect)
  sudo ufw allow 443/tcp         # Caddy HTTPS
HARDEN
}

resolve_ipv4() {
	local name="$1"
	if command -v dig >/dev/null 2>&1; then
		dig +short A "$name" | tail -n 1
		return
	fi
	getent ahostsv4 "$name" | awk '{print $1; exit}'
}

check_dns() {
	local name="$1"
	local got
	got="$(resolve_ipv4 "$name" || true)"
	if [ "$got" != "$SERVER_IP" ]; then
		fail "DNS A record for $name points to '${got:-none}', expected $SERVER_IP"
	fi
	echo -e "${GREEN}PASS${NC} DNS A $name -> $SERVER_IP"
}

check_local_port() {
	local port="$1"
	ss -ltn "( sport = :$port )" | grep -q ":$port" || fail "local port $port is not listening"
	echo -e "${GREEN}PASS${NC} local port $port listening"
}

check_https() {
	local url="$1"
	local method="${2:-HEAD}"
	local max_attempts=12
	local attempt
	for attempt in $(seq 1 "$max_attempts"); do
		if [ "$method" = "GET" ]; then
			if curl -fsS --connect-timeout 5 --max-time 10 "$url" >/dev/null 2>&1; then
				echo -e "${GREEN}PASS${NC} $url (GET, attempt $attempt)"
				return
			fi
		elif curl -fsSI --connect-timeout 5 --max-time 10 "$url" >/dev/null 2>&1; then
			echo -e "${GREEN}PASS${NC} $url (attempt $attempt)"
			return
		fi
		echo -e "${YELLOW}WAIT${NC} $url not ready yet (attempt $attempt/$max_attempts)"
		sleep 5
	done
	echo -e "${RED}FAIL${NC} $url not ready after $max_attempts attempts"
	echo "Recent Caddy certificate logs:"
	journalctl -u caddy -n 120 --no-pager | grep -Ei 'acme|challenge|certificate|issuer|error|failed' || true
	fail "HTTPS smoke test failed for $url; check DNS, inbound 80/443, and Caddy ACME logs above"
}

# check_content_type verifies that a URL returns the expected
# Content-Type. This catches reverse-proxy rewrite bugs where
# static JS/CSS assets are served as text/html (SPA fallback)
# instead of their actual MIME type.
check_content_type() {
	local url="$1"
	local expected="$2"
	local actual
	actual="$(curl -sSIL --connect-timeout 5 --max-time 10 "$url" 2>/dev/null | grep -i '^Content-Type:' | head -n1 | tr -d '\r' | sed 's/^[Cc]ontent-[Tt]ype:[[:space:]]*//' || true)"
	if [ -z "$actual" ]; then
		echo -e "${RED}FAIL${NC} $url: no Content-Type header received"
		fail "Content-Type check failed for $url"
	fi
	case "$actual" in
		$expected*)
			echo -e "${GREEN}PASS${NC} $url: Content-Type $actual"
			;;
		*)
			echo -e "${RED}FAIL${NC} $url: expected Content-Type \"$expected\", got \"$actual\""
			echo "This is the production symptom of a reverse-proxy rewrite bug: the SPA fallback returned" >&2
			echo "index.html (text/html) for a static asset path. The Caddy webmail vhost route must" >&2
			echo "proxy /webmail/* paths without a /webmail prefix rewrite." >&2
			fail "Content-Type mismatch for $url: got \"$actual\", expected \"$expected\""
			;;
	esac
}

main() {
	require_root
	require_input
	log "Orvix HTTPS setup started for $PRIMARY_DOMAIN ($SERVER_IP)"

	# Patch dns.public_ipv4 BEFORE we hand control to Caddy and
	# the DNS verifier. The admin DNS/DKIM dashboard needs this
	# field to generate A / MX / SPF / DKIM / DMARC records; the
	# function adds it if missing, no-ops on match, and fails
	# loudly on mismatch (never silently overwrites operator-set
	# values).
	#
	# When the patch actually modified the file, the orvix
	# runtime process still holds the OLD config in memory; we
	# MUST restart it (and wait for readiness) BEFORE the
	# dashboard verification gate runs, otherwise the dashboard
	# hits the stale process and fails with "public mail IPv4 is
	# not configured" — the exact symptom the previous version
	# of setup-https.sh shipped. restart_orvix_after_patch is a
	# no-op when PATCH_CHANGED=0, so an idempotent run never
	# restarts the runtime unnecessarily.
	patch_dns_public_ipv4
	restart_orvix_after_patch

	install_caddy
	write_caddyfile
	open_firewall
	run_quiet caddy validate --config /etc/caddy/Caddyfile
	run_quiet systemctl enable caddy
	run_quiet systemctl reload caddy || run_quiet systemctl restart caddy
	systemctl is-active --quiet caddy || fail "caddy service is not active"

	check_dns "$ADMIN_DOMAIN"
	check_dns "$WEBMAIL_DOMAIN"
	check_dns "$MAIL_DOMAIN"
	check_local_port 80
	check_local_port 443

	check_https "https://$ADMIN_DOMAIN/admin" HEAD
	check_https "https://$ADMIN_DOMAIN/api/v1/health" GET
	check_https "https://$WEBMAIL_DOMAIN/webmail/assets/webmail.js" HEAD
	check_https "https://$WEBMAIL_DOMAIN/webmail/assets/webmail.css" HEAD
	check_https "https://$WEBMAIL_DOMAIN/assets/webmail.js" HEAD
	check_https "https://$WEBMAIL_DOMAIN/assets/webmail.css" HEAD
	check_https "https://$WEBMAIL_DOMAIN/" HEAD
	check_https "https://$WEBMAIL_DOMAIN/api/v1/health" GET
	check_https "https://$MAIL_DOMAIN/.well-known/jmap" GET

	check_content_type "https://$WEBMAIL_DOMAIN/webmail/assets/webmail.js" "text/javascript"
	check_content_type "https://$WEBMAIL_DOMAIN/webmail/assets/webmail.css" "text/css"
	check_content_type "https://$WEBMAIL_DOMAIN/assets/webmail.js" "text/javascript"
	check_content_type "https://$WEBMAIL_DOMAIN/assets/webmail.css" "text/css"
	check_content_type "https://$WEBMAIL_DOMAIN/" "text/html"

	cat <<DONE

${GREEN}PASS${NC} Orvix HTTPS reverse proxy verified.

Admin UI:    https://$ADMIN_DOMAIN/admin
Webmail UI:  https://$WEBMAIL_DOMAIN/
JMAP:        https://$MAIL_DOMAIN/.well-known/jmap

Detailed log: $INSTALL_LOG
DONE

	post_https_firewall_hardening
}

main "$@"
