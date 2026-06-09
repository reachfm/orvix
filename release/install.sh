#!/usr/bin/env bash
set -euo pipefail

# Orvix RC1 clean installer for fresh Ubuntu VPS hosts.
# This path installs the native CoreMail runtime only.

ORVIX_GO_VERSION="${ORVIX_GO_VERSION:-1.26.4}"
ORVIX_SOURCE_DIR="${ORVIX_SOURCE_DIR:-$(pwd)}"
ORVIX_BIN="${ORVIX_BIN:-/usr/local/bin/orvix}"
ORVIX_CONFIG="${ORVIX_CONFIG:-/etc/orvix/orvix.yaml}"

export DEBIAN_FRONTEND=noninteractive
export NEEDRESTART_MODE=a

BOLD='\033[1m'
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

CURRENT_STEP="preflight"

fail() {
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
            echo -e "${GREEN}ok${NC} Go $current available"
            return
        fi
        echo -e "${YELLOW}warning:${NC} Go $current is too old; installing Go ${ORVIX_GO_VERSION}"
    fi

    local archive="/tmp/go${ORVIX_GO_VERSION}.linux-amd64.tar.gz"
    curl -fsSL -o "$archive" "https://go.dev/dl/go${ORVIX_GO_VERSION}.linux-amd64.tar.gz"
    rm -rf /usr/local/go
    tar -C /usr/local -xzf "$archive"
    rm -f "$archive"
    export PATH="/usr/local/go/bin:$PATH"
    go version
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
        install -m 0755 "$local_bin" "$ORVIX_BIN"
        echo -e "${GREEN}ok${NC} installed prebuilt binary from $local_bin"
        return
    fi

    [ -f "$ORVIX_SOURCE_DIR/go.mod" ] || fail "no prebuilt binary found and no Go source tree at $ORVIX_SOURCE_DIR"
    install_go_toolchain
    (cd "$ORVIX_SOURCE_DIR" && go build -o "$ORVIX_BIN" ./cmd/orvix)
    chmod 0755 "$ORVIX_BIN"
    echo -e "${GREEN}ok${NC} built Orvix from source"
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
    echo -e "${BOLD}Orvix RC1 clean installer${NC}"

	CURRENT_STEP="dependencies"
	apt-get update -qq
	apt-get install -y -qq \
		-o Dpkg::Options::=--force-confdef \
		-o Dpkg::Options::=--force-confold \
		ca-certificates curl tar gzip redis-server libcap2-bin iproute2
    systemctl enable --now redis-server

    CURRENT_STEP="user"
    if ! id -u orvix >/dev/null 2>&1; then
        useradd --system --user-group --create-home --home-dir /var/lib/orvix --shell /usr/sbin/nologin orvix
    fi

    CURRENT_STEP="directories"
    install -d -o orvix -g orvix -m 0750 /etc/orvix /var/lib/orvix /var/lib/orvix/coremail /var/lib/orvix/backups /var/log/orvix
    install -d -o root -g root -m 0755 /usr/share/orvix/admin

    CURRENT_STEP="prompts"
    local primary_domain admin_email admin_password
    primary_domain="$(prompt_domain)"
    admin_email="$(prompt_email)"
    admin_password="$(prompt_password)"

    CURRENT_STEP="binary"
    install_binary
    chown root:root "$ORVIX_BIN"
    chmod 0755 "$ORVIX_BIN"
    setcap 'cap_net_bind_service=+ep' "$ORVIX_BIN"

    CURRENT_STEP="configuration"
    write_config "$primary_domain"
    write_bootstrap_env "$admin_email" "$admin_password"
    install -m 0644 "$ORVIX_SOURCE_DIR/release/admin/index.html" /usr/share/orvix/admin/index.html

    CURRENT_STEP="systemd"
    write_service
    systemctl daemon-reload
    systemctl enable orvix
    systemctl restart orvix

    CURRENT_STEP="verification"
    sleep 5
    verify_install "$admin_email" "$admin_password"

    echo -e "${GREEN}PASS${NC} Orvix RC1 clean install verified"
    echo "Admin API: http://$(hostname -I | awk '{print $1}'):8080"
    echo "Admin email: $admin_email"
}

main "$@"
