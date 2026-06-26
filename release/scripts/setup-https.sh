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

require_input() {
	if [ -z "$PRIMARY_DOMAIN" ]; then
		fail "usage: $0 <primary-domain> [server-ip]"
	fi
	[[ "$PRIMARY_DOMAIN" =~ ^[A-Za-z0-9][A-Za-z0-9.-]*\.[A-Za-z]{2,}$ ]] || fail "invalid primary domain: $PRIMARY_DOMAIN"
	ADMIN_DOMAIN="${ADMIN_DOMAIN:-admin.$PRIMARY_DOMAIN}"
	WEBMAIL_DOMAIN="${WEBMAIL_DOMAIN:-webmail.$PRIMARY_DOMAIN}"
	MAIL_DOMAIN="${MAIL_DOMAIN:-mail.$PRIMARY_DOMAIN}"
	if [ -z "$SERVER_IP" ]; then
		SERVER_IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
	fi
	[ -n "$SERVER_IP" ] || fail "server IP could not be detected; pass it as second argument"
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
	if command -v ufw >/dev/null 2>&1; then
		run_quiet ufw allow 80/tcp
		run_quiet ufw allow 443/tcp
	fi
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

Recommended after HTTPS passes:
- restrict external access to TCP 8080 and 8081
- keep mail protocol ports 25, 110, and 143 unchanged
- keep TCP 80 and 443 open for Caddy and ACME renewal

Firewall hardening commands:
sudo ufw deny 8080/tcp
sudo ufw deny 8081/tcp

Detailed log: $INSTALL_LOG
DONE
}

main "$@"
