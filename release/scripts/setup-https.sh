#!/usr/bin/env bash
set -euo pipefail

# Orvix RC1 HTTPS reverse proxy setup for Ubuntu VPS hosts.
# This configures Caddy for:
#   admin.<domain> -> 127.0.0.1:8080
#   mail.<domain>  -> 127.0.0.1:8081

PRIMARY_DOMAIN="${1:-${ORVIX_PRIMARY_DOMAIN:-}}"
SERVER_IP="${2:-${ORVIX_SERVER_IP:-}}"
ADMIN_DOMAIN="${ORVIX_ADMIN_DOMAIN:-}"
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

$MAIL_DOMAIN {
	reverse_proxy 127.0.0.1:8081
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
	local max_attempts=12
	local attempt
	for attempt in $(seq 1 "$max_attempts"); do
		if curl -fsSI --connect-timeout 5 --max-time 10 "$url" >/dev/null 2>&1; then
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
	check_dns "$MAIL_DOMAIN"
	check_local_port 80
	check_local_port 443

	check_https "https://$ADMIN_DOMAIN/admin"
	check_https "https://$ADMIN_DOMAIN/api/v1/health"
	check_https "https://$MAIL_DOMAIN/.well-known/jmap"

	cat <<DONE

${GREEN}PASS${NC} Orvix HTTPS reverse proxy verified.

Admin UI: https://$ADMIN_DOMAIN/admin
Admin API health: https://$ADMIN_DOMAIN/api/v1/health
JMAP discovery: https://$MAIL_DOMAIN/.well-known/jmap

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
