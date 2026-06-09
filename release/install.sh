#!/usr/bin/env bash
set -euo pipefail

# Orvix RC1 clean installer for fresh Ubuntu VPS hosts.
# This path installs the native CoreMail runtime only.

ORVIX_GO_VERSION="${ORVIX_GO_VERSION:-1.26.4}"
ORVIX_SOURCE_DIR="${ORVIX_SOURCE_DIR:-$(pwd)}"
ORVIX_BIN="${ORVIX_BIN:-/usr/local/bin/orvix}"
ORVIX_CONFIG="${ORVIX_CONFIG:-/etc/orvix/orvix.yaml}"
INSTALL_LOG="${INSTALL_LOG:-/var/log/orvix/install.log}"

export DEBIAN_FRONTEND=noninteractive
export NEEDRESTART_MODE=a

BOLD='\033[1m'
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

CURRENT_STEP="preflight"
CURRENT_PERCENT=0
CURRENT_STEP_LABEL="Preparing system"
STEP_LABELS=(
	"Preparing system"
	"Installing dependencies"
	"Creating service account"
	"Collecting admin settings"
	"Installing Orvix binary"
	"Writing configuration"
	"Starting services"
	"Verifying install"
)
STEP_STATUS=(
	"PENDING"
	"PENDING"
	"PENDING"
	"PENDING"
	"PENDING"
	"PENDING"
	"PENDING"
	"PENDING"
)

clear_screen() {
	printf '\033[H\033[2J'
}

progress_bar() {
	local percent="$1"
	local width=30
	local filled=$((percent * width / 100))
	local empty=$((width - filled))
	local bar=""
	local i
	for ((i = 0; i < filled; i++)); do
		bar="${bar}#"
	done
	for ((i = 0; i < empty; i++)); do
		bar="${bar}-"
	done
	printf '[%s] %s%%' "$bar" "$percent"
}

step_index() {
	case "$1" in
		preparing) echo 0 ;;
		dependencies) echo 1 ;;
		user) echo 2 ;;
		configuration-input) echo 3 ;;
		binary) echo 4 ;;
		configuration) echo 5 ;;
		systemd) echo 6 ;;
		verification) echo 7 ;;
		*) echo 0 ;;
	esac
}

status_line() {
	local status="$1"
	local label="$2"
	case "$status" in
		PASS) printf '%b%-8s%b %s\n' "$GREEN" "$status" "$NC" "$label" ;;
		RUNNING) printf '%b%-8s%b %s\n' "$YELLOW" "$status" "$NC" "$label" ;;
		FAIL) printf '%b%-8s%b %s\n' "$RED" "$status" "$NC" "$label" ;;
		*) printf '%-8s %s\n' "$status" "$label" ;;
	esac
}

render_dashboard() {
	clear_screen
	cat <<'HEADER'
=========================================================
                 ORVIX MAIL PLATFORM
              RC1 CLEAN INSTALLER
=========================================================

HEADER
	progress_bar "$CURRENT_PERCENT"
	printf '\n\nCurrent Step:\n[%s%%] %s\n\nStatus:\n' "$CURRENT_PERCENT" "$CURRENT_STEP_LABEL"
	local i
	for i in "${!STEP_LABELS[@]}"; do
		status_line "${STEP_STATUS[$i]}" "${STEP_LABELS[$i]}"
	done
	cat <<FOOTER

Detailed log:
$INSTALL_LOG

=========================================================
FOOTER
}

set_step() {
	local key="$1"
	local label="$2"
	local percent="$3"
	local index
	index="$(step_index "$key")"
	CURRENT_STEP="$label"
	CURRENT_STEP_LABEL="$label"
	CURRENT_PERCENT="$percent"
	local i
	for i in "${!STEP_STATUS[@]}"; do
		if [ "$i" -lt "$index" ]; then
			STEP_STATUS[$i]="PASS"
		elif [ "$i" -eq "$index" ]; then
			STEP_STATUS[$i]="RUNNING"
		else
			STEP_STATUS[$i]="PENDING"
		fi
	done
	render_dashboard
	log_detail "STEP ${percent}%: $label"
}

render_failure() {
	local failed_step="${1:-unknown}"
	local index
	index="$(step_index_by_label "$failed_step")"
	if [ "$index" -ge 0 ]; then
		STEP_STATUS[$index]="FAIL"
	fi
	clear_screen
	cat <<HEADER
${RED}=========================================================
                 ORVIX MAIL PLATFORM
                 INSTALLATION FAILED
=========================================================${NC}

Failed Step:
$failed_step

Detailed log:
$INSTALL_LOG

Last 80 log lines:
HEADER
	if [ -f "$INSTALL_LOG" ]; then
		tail -n 80 "$INSTALL_LOG" || true
	fi
	cat <<FOOTER

${RED}=========================================================${NC}
FOOTER
}

step_index_by_label() {
	local label="$1"
	local i
	for i in "${!STEP_LABELS[@]}"; do
		if [ "${STEP_LABELS[$i]}" = "$label" ]; then
			echo "$i"
			return
		fi
	done
	echo "-1"
}

render_success() {
	local domain="$1"
	local server_ip="$2"
	local admin_email="$3"
	local i
	for i in "${!STEP_STATUS[@]}"; do
		STEP_STATUS[$i]="PASS"
	done
	CURRENT_PERCENT=100
	CURRENT_STEP_LABEL="Installation complete"
	clear_screen
	cat <<HEADER
${GREEN}=========================================================
                 ORVIX MAIL PLATFORM
              INSTALLATION COMPLETE
=========================================================${NC}

HEADER
	progress_bar "$CURRENT_PERCENT"
	cat <<BODY

Admin UI: http://admin.${domain}
Mail Hostname: mail.${domain}
SMTP: mail.${domain}:25
IMAP: mail.${domain}:143
POP3: mail.${domain}:110

DNS required: A admin.${domain} -> ${server_ip}
DNS required: A mail.${domain} -> ${server_ip}

Temporary Admin API: http://${server_ip}:8080/admin
Admin email: ${admin_email}
Detailed log: ${INSTALL_LOG}

${GREEN}=========================================================${NC}
BODY
}

prepare_log() {
	mkdir -p "$(dirname "$INSTALL_LOG")"
	touch "$INSTALL_LOG"
	chmod 0640 "$INSTALL_LOG"
}

log_detail() {
	printf '[%s] %s\n' "$(date -Is)" "$*" >>"$INSTALL_LOG"
}

run_quiet() {
	log_detail "RUN $*"
	"$@" >>"$INSTALL_LOG" 2>&1
}

on_error() {
	local exit_code=$?
	render_failure "${CURRENT_STEP:-unknown}" >&2
	exit "$exit_code"
}

fail() {
    if [ -d "$(dirname "$INSTALL_LOG")" ]; then
        log_detail "ERROR: $*" || true
    fi
    render_failure "${CURRENT_STEP:-unknown}" >&2
    echo -e "${RED}ERROR:${NC} $*" >&2
    exit 1
}

require_root() {
    [ "$(id -u)" -eq 0 ] || fail "run as root or with sudo"
}

prompt_domain() {
    local domain="${ORVIX_PRIMARY_DOMAIN:-}"
    while [ -z "$domain" ]; do
        read -rp "Primary email domain (example.com): " domain
    done
    [[ "$domain" =~ ^[A-Za-z0-9][A-Za-z0-9.-]*\.[A-Za-z]{2,}$ ]] || fail "invalid domain: $domain"
    echo "$domain"
}

prompt_email() {
    local email="${ORVIX_ADMIN_EMAIL:-}"
    while [ -z "$email" ]; do
        read -rp "Admin email address: " email
    done
    [[ "$email" =~ ^[^@]+@[^@]+\.[^@]+$ ]] || fail "invalid email: $email"
    echo "$email"
}

prompt_password() {
    local password="${ORVIX_ADMIN_PASSWORD:-}"
    local confirm
    while [ -z "$password" ]; do
        read -rsp "Admin password (min 12 chars): " password
        echo
        read -rsp "Confirm admin password: " confirm
        echo
        [ "$password" = "$confirm" ] || { echo "Passwords do not match"; password=""; }
    done
    [ "${#password}" -ge 12 ] || fail "admin password must be at least 12 characters"
    echo "$password"
}

version_ge() {
	[ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -n1)" = "$2" ]
}

json_escape() {
	local value="$1"
	value="${value//\\/\\\\}"
	value="${value//\"/\\\"}"
	value="${value//$'\n'/\\n}"
	value="${value//$'\r'/\\r}"
	value="${value//$'\t'/\\t}"
	printf '%s' "$value"
}

build_login_payload() {
	local email="$1"
	local password="$2"
	printf '{"email":"%s","password":"%s"}' "$(json_escape "$email")" "$(json_escape "$password")"
}

install_go_toolchain() {
	if command -v go >/dev/null 2>&1; then
		local current
		current="$(go env GOVERSION | sed 's/^go//')"
		if version_ge "$current" "1.25.0"; then
			log_detail "Go $current available"
			return
		fi
		log_detail "Go $current is too old; installing Go ${ORVIX_GO_VERSION}"
	fi

	local archive="/tmp/go${ORVIX_GO_VERSION}.linux-amd64.tar.gz"
	run_quiet curl -fsSL -o "$archive" "https://go.dev/dl/go${ORVIX_GO_VERSION}.linux-amd64.tar.gz"
	run_quiet rm -rf /usr/local/go
	run_quiet tar -C /usr/local -xzf "$archive"
	run_quiet rm -f "$archive"
	export PATH="/usr/local/go/bin:$PATH"
	go version >>"$INSTALL_LOG" 2>&1
}

install_binary() {
    local local_bin=""
    for candidate in \
        "$ORVIX_SOURCE_DIR/release/orvix-linux-amd64" \
        "$ORVIX_SOURCE_DIR/orvix-linux-amd64" \
        "$ORVIX_SOURCE_DIR/orvix"; do
        if [ -f "$candidate" ] && [ -x "$candidate" ]; then
            local_bin="$candidate"
            break
        fi
    done

	if [ -n "$local_bin" ]; then
		run_quiet install -m 0755 "$local_bin" "$ORVIX_BIN"
		log_detail "installed prebuilt binary from $local_bin"
		return
	fi

	[ -f "$ORVIX_SOURCE_DIR/go.mod" ] || fail "no prebuilt binary found and no Go source tree at $ORVIX_SOURCE_DIR"
	install_go_toolchain
	(cd "$ORVIX_SOURCE_DIR" && go build -o "$ORVIX_BIN" ./cmd/orvix) >>"$INSTALL_LOG" 2>&1
	run_quiet chmod 0755 "$ORVIX_BIN"
	log_detail "built Orvix from source"
}

write_config() {
    local domain="$1"
    local hostname="mail.$domain"

    cat > "$ORVIX_CONFIG" <<YAML
server:
  host: "0.0.0.0"
  port: 80
  admin_port: 8080
  admin_ui_dir: /usr/share/orvix/admin
  read_timeout: 60s
  write_timeout: 60s
  idle_timeout: 120s
  body_limit: 52428800
  allowed_origins:
    - "https://$hostname"
    - "http://$hostname"
  trusted_proxies: []

database:
  driver: sqlite
  sqlite_path: /var/lib/orvix/orvix.db
  dsn: /var/lib/orvix/orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate

redis:
  host: 127.0.0.1
  port: 6379
  db: 0

coremail:
  enabled: true
  hostname: $hostname
  data_path: /var/lib/orvix/coremail
  mailstore_path: /var/lib/orvix/coremail/mailstore
  smtp_host: 0.0.0.0
  smtp_port: 25
  imap_host: 0.0.0.0
  imap_port: 143
  pop3_host: 0.0.0.0
  pop3_port: 110
  jmap_host: 0.0.0.0
  jmap_port: 8081
  require_tls_for_auth: true
  queue_workers: 1
  worker_interval: 5s
  license_file_path: /etc/orvix/license.json
  license_authority_cache_path: /var/lib/orvix/license-cache.json

auth:
  jwt_key_path: /var/lib/orvix/jwt_key.pem
  jwt_access_ttl: 15m
  jwt_refresh_ttl: 720h
  password_min_len: 12
  argon2_time: 3
  argon2_memory: 65536
  argon2_threads: 4
  login_rate_limit: 5
  rate_window: 15m

logging:
  level: info
  format: json
  output: stdout
  log_dir: /var/log/orvix

metrics:
  enabled: true
  path: /metrics

update:
  channel: stable
  auto_apply: false
  backup_before: true

backup:
  dir: /var/lib/orvix/backups
YAML
    chown orvix:orvix "$ORVIX_CONFIG"
    chmod 0640 "$ORVIX_CONFIG"
}

write_service() {
    cat > /etc/systemd/system/orvix.service <<'UNIT'
[Unit]
Description=Orvix RC1 Mail Server
Documentation=https://github.com/reachfm/orvix
After=network-online.target redis-server.service
Wants=network-online.target redis-server.service

[Service]
Type=simple
User=orvix
Group=orvix
WorkingDirectory=/var/lib/orvix
ExecStart=/usr/local/bin/orvix serve
ExecReload=/bin/kill -HUP $MAINPID
Restart=always
RestartSec=5

Environment=ORVIX_CONFIG=/etc/orvix/orvix.yaml
Environment=ORVIX_LOG_DIR=/var/log/orvix
EnvironmentFile=-/etc/orvix/bootstrap.env

AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=full
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/orvix /var/log/orvix /etc/orvix
LimitNOFILE=65536

StandardOutput=journal
StandardError=journal
SyslogIdentifier=orvix

[Install]
WantedBy=multi-user.target
UNIT
}

write_bootstrap_env() {
    local email="$1"
    local password="$2"
    local escaped_password
    escaped_password="${password//\\/\\\\}"
    escaped_password="${escaped_password//\"/\\\"}"

    cat > /etc/orvix/bootstrap.env <<ENV
ORVIX_ADMIN_EMAIL=$email
ORVIX_ADMIN_PASSWORD="$escaped_password"
ENV
    chown root:orvix /etc/orvix/bootstrap.env
    chmod 0640 /etc/orvix/bootstrap.env
}

verify_install() {
	local email="$1"
	local password="$2"
	local login_endpoint="http://127.0.0.1:8080/api/v1/auth/login"

	systemctl is-active --quiet redis-server || fail "redis-server is not active"
	systemctl is-active --quiet orvix || fail "orvix is not active"
    curl -fsS http://127.0.0.1:8080/api/v1/health >/dev/null || fail "health endpoint failed"
    curl -fsSI http://127.0.0.1:8080/admin >/dev/null || fail "admin UI endpoint failed"

    for port in 25 110 143 8080 6379; do
        ss -ltn "( sport = :$port )" | grep -q ":$port" || fail "port $port is not listening"
	done

	local login_payload response_file http_code
	login_payload="$(build_login_payload "$email" "$password")"
	response_file="$(mktemp)"
	http_code="$(curl -sS -o "$response_file" -w "%{http_code}" -H 'Content-Type: application/json' -d "$login_payload" "$login_endpoint" || true)"
	if [ "$http_code" != "200" ]; then
		echo -e "${RED}Admin API login verification failed${NC}" >&2
		echo "Endpoint: $login_endpoint" >&2
		echo "Request shape: {\"email\":\"$email\",\"password\":\"[REDACTED]\",\"password_length\":${#password}}" >&2
		echo "HTTP status: ${http_code:-curl_failed}" >&2
		echo "Response body:" >&2
		cat "$response_file" >&2 || true
		echo >&2
		echo "Recent Orvix journal:" >&2
		journalctl -u orvix.service -n 80 --no-pager >&2 || true
		rm -f "$response_file"
		fail "admin API login failed"
	fi
	rm -f "$response_file"
}

main() {
	require_root
	prepare_log
	trap on_error ERR
	log_detail "Orvix RC1 clean installer started"

	set_step "preparing" "Preparing system" 10

	set_step "dependencies" "Installing dependencies" 20
	run_quiet apt-get update -qq
	run_quiet apt-get install -y -qq \
		-o Dpkg::Options::=--force-confdef \
		-o Dpkg::Options::=--force-confold \
		ca-certificates curl tar gzip redis-server libcap2-bin iproute2
	run_quiet systemctl enable --now redis-server

    set_step "user" "Creating service account" 35
    if ! id -u orvix >/dev/null 2>&1; then
        run_quiet useradd --system --user-group --create-home --home-dir /var/lib/orvix --shell /usr/sbin/nologin orvix
    fi

    run_quiet install -d -o orvix -g orvix -m 0750 /etc/orvix /var/lib/orvix /var/lib/orvix/coremail /var/lib/orvix/backups /var/log/orvix
    run_quiet install -d -o root -g root -m 0755 /usr/share/orvix/admin

    set_step "configuration-input" "Collecting admin settings" 45
    local primary_domain admin_email admin_password
    primary_domain="$(prompt_domain)"
    admin_email="$(prompt_email)"
    admin_password="$(prompt_password)"

    set_step "binary" "Installing Orvix binary" 60
    install_binary
    run_quiet chown root:root "$ORVIX_BIN"
    run_quiet chmod 0755 "$ORVIX_BIN"
    run_quiet setcap 'cap_net_bind_service=+ep' "$ORVIX_BIN"

    set_step "configuration" "Writing configuration" 75
    write_config "$primary_domain"
    write_bootstrap_env "$admin_email" "$admin_password"
    run_quiet cp -R "$ORVIX_SOURCE_DIR"/release/admin/. /usr/share/orvix/admin/
    run_quiet chown -R root:root /usr/share/orvix/admin
    run_quiet find /usr/share/orvix/admin -type d -exec chmod 0755 {} +
    run_quiet find /usr/share/orvix/admin -type f -exec chmod 0644 {} +

    set_step "systemd" "Starting services" 85
    write_service
    run_quiet systemctl daemon-reload
    run_quiet systemctl enable orvix
    run_quiet systemctl restart orvix

    set_step "verification" "Verifying install" 95
    run_quiet sleep 5
    verify_install "$admin_email" "$admin_password"

    local server_ip
    server_ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
    server_ip="${server_ip:-127.0.0.1}"
    render_success "$primary_domain" "$server_ip" "$admin_email"
}

main "$@"
