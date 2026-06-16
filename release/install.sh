#!/usr/bin/env bash
set -euo pipefail

# Orvix Enterprise Mail installer for fresh Ubuntu VPS hosts.
# This path installs the native CoreMail runtime only.

ORVIX_GO_VERSION="${ORVIX_GO_VERSION:-1.26.4}"
ORVIX_SOURCE_DIR="${ORVIX_SOURCE_DIR:-$(pwd)}"
ORVIX_BIN="${ORVIX_BIN:-/usr/local/bin/orvix}"
ORVIX_CONFIG="${ORVIX_CONFIG:-/etc/orvix/orvix.yaml}"
INSTALL_LOG="${INSTALL_LOG:-/var/log/orvix/install.log}"
BOOTSTRAP_ENV="${BOOTSTRAP_ENV:-/etc/orvix/bootstrap.env}"

export DEBIAN_FRONTEND=noninteractive
export NEEDRESTART_MODE=a

BOLD=$'\033[1m'
RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
YELLOW=$'\033[1;33m'
NC=$'\033[0m'

CURRENT_STEP="preflight"
CURRENT_PERCENT=0
CURRENT_STEP_LABEL="Preparing system"
STEP_LABELS=(
	"System preflight"
	"Platform dependencies"
	"Service identity"
	"Administrator enrollment"
	"CoreMail binary deployment"
	"Configuration provisioning"
	"Service activation"
	"Enterprise health verification"
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
              ORVIX ENTERPRISE MAIL
                COREMAIL INSTALLER
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
              ORVIX ENTERPRISE MAIL
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
	local version
	version="$(install_version)"
	local i
	for i in "${!STEP_STATUS[@]}"; do
		STEP_STATUS[$i]="PASS"
	done
	CURRENT_PERCENT=100
	CURRENT_STEP_LABEL="Installation complete"
	clear_screen
	cat <<HEADER
${GREEN}=========================================================
              ORVIX ENTERPRISE MAIL
               INSTALLATION COMPLETE
=========================================================${NC}

HEADER
	progress_bar "$CURRENT_PERCENT"
	cat <<BODY

Product: Orvix Enterprise Mail / CoreMail
Version: ${version}

Admin URL: http://admin.${domain}/admin
Webmail URL: http://admin.${domain}/webmail
JMAP URL: http://mail.${domain}/.well-known/jmap

Mail Hostname: mail.${domain}
SMTP: mail.${domain}:25
IMAP: mail.${domain}:143
POP3: mail.${domain}:110

DNS required: A admin.${domain} -> ${server_ip}
DNS required: A mail.${domain} -> ${server_ip}

Temporary Admin API: http://${server_ip}:8080/admin
Admin email: ${admin_email}
Detailed log: ${INSTALL_LOG}

HTTPS setup command:
sudo $ORVIX_SOURCE_DIR/release/scripts/setup-https.sh ${domain} ${server_ip}

${GREEN}=========================================================${NC}
BODY
}

install_version() {
	if command -v git >/dev/null 2>&1 && git -C "$ORVIX_SOURCE_DIR" rev-parse --short HEAD >/dev/null 2>&1; then
		git -C "$ORVIX_SOURCE_DIR" rev-parse --short HEAD
		return
	fi
	if [ -x "$ORVIX_BIN" ]; then
		"$ORVIX_BIN" version 2>/dev/null | head -n 1 || true
		return
	fi
	printf 'installed build'
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
    if [ "${CURRENT_STEP:-}" = "Verifying install" ]; then
        echo "INSTALLATION VERIFICATION FAILED" >&2
    fi
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
        IFS= read -rsp "Admin password (min 8 chars): " password
        printf '\n' >&2
        IFS= read -rsp "Confirm admin password: " confirm
        printf '\n' >&2
        [ "$password" = "$confirm" ] || { printf 'Passwords do not match\n' >&2; password=""; }
    done
    [ "${#password}" -ge 8 ] || fail "admin password must be at least 8 characters"
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

sqlite_escape() {
	local value="$1"
	printf '%s' "$value" | sed "s/'/''/g"
}

build_login_payload() {
	local email="$1"
	local password="$2"
	printf '{"username":"%s","password":"%s"}' "$(json_escape "$email")" "$(json_escape "$password")"
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
    local admin_host="admin.$domain"

    cat > "$ORVIX_CONFIG" <<YAML
server:
  host: "0.0.0.0"
  port: 80
  admin_port: 8080
  admin_ui_dir: /usr/share/orvix/admin
  webmail_ui_dir: /usr/share/orvix/webmail
  read_timeout: 60s
  write_timeout: 60s
  idle_timeout: 120s
  body_limit: 52428800
  # The webmail SPA is served from admin.$domain and loads module
  # assets with CORS mode. The admin origin must be explicitly
  # allowed or browsers will block /webmail/assets/*.
  allowed_origins:
    - "https://$admin_host"
    - "http://$admin_host"
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
  password_min_len: 8
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
Description=Orvix Enterprise Mail Server
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
    local encoded_password
    encoded_password="$(printf '%s' "$password" | base64 | tr -d '\n')"

    cat > "$BOOTSTRAP_ENV" <<ENV
ORVIX_ADMIN_EMAIL=$email
ORVIX_ADMIN_PASSWORD_B64=$encoded_password
ENV
    chown root:orvix "$BOOTSTRAP_ENV"
    chmod 0640 "$BOOTSTRAP_ENV"
}

install_update_helper() {
    # Install the runtime update systemd oneshot unit.
    local unit_src="${ORVIX_SOURCE_DIR}/release/systemd/orvix-update.service"
    if [ -f "$unit_src" ]; then
        install -m 0644 -o root -g root "$unit_src" /etc/systemd/system/orvix-update.service
        log_detail "installed /etc/systemd/system/orvix-update.service (0644 root:root)"
    else
        log_detail "orvix-update.service not found at $unit_src; skipping"
    fi
    # Install the sudoers drop-in for passwordless systemctl start.
    local sudoers_src="${ORVIX_SOURCE_DIR}/release/sudoers.d/orvix-update"
    if [ -f "$sudoers_src" ]; then
        install -m 0440 -o root -g root "$sudoers_src" /etc/sudoers.d/orvix-update
        log_detail "installed /etc/sudoers.d/orvix-update (0440 root:root)"
    else
        log_detail "sudoers drop-in not found at $sudoers_src; skipping"
    fi
}

# ── Validation helpers ─────────────────────────────────

# validate_systemd verifies that both systemd units exist, are
# enabled (where applicable), and are active. Fails the install
# if any required unit is missing or failed.
validate_systemd() {
    local svc
    for svc in orvix.service; do
        if [ ! -f "/etc/systemd/system/$svc" ]; then
            fail "systemd unit $svc not found at /etc/systemd/system/$svc"
        fi
        if ! systemctl is-enabled --quiet "$svc" 2>/dev/null; then
            fail "systemd unit $svc is not enabled"
        fi
        # Retry active check (service may still be starting).
        local attempt
        for attempt in 1 2 3 4 5; do
            if systemctl is-active --quiet "$svc" 2>/dev/null; then
                break
            fi
            if [ "$attempt" -lt 5 ]; then
                sleep 1
            fi
        done
        if ! systemctl is-active --quiet "$svc" 2>/dev/null; then
            fail "systemd unit $svc is not active after restart"
        fi
        log_detail "VALIDATE systemd $svc: unit present, enabled, active"
    done
    # orvix-update.service is a oneshot helper; must exist but
    # does not need to be enabled or active at rest.
    if [ ! -f "/etc/systemd/system/orvix-update.service" ]; then
        fail "systemd unit orvix-update.service not found at /etc/systemd/system/orvix-update.service"
    fi
    log_detail "VALIDATE systemd orvix-update.service: unit present"
}

# validate_sudoers verifies the sudoers drop-in ownership and
# permissions. The file must be root:root 0440 so that visudo
# does not reject it and a non-root attacker cannot modify it.
validate_sudoers() {
    local path="/etc/sudoers.d/orvix-update"
    if [ ! -f "$path" ]; then
        fail "sudoers drop-in $path not found"
    fi
    local owner
    owner="$(stat -c '%U:%G' "$path" 2>/dev/null || true)"
    if [ "$owner" != "root:root" ]; then
        fail "sudoers drop-in $path owner is $owner, want root:root"
    fi
    local mode
    mode="$(stat -c '%a' "$path" 2>/dev/null || true)"
    if [ "$mode" != "440" ]; then
        fail "sudoers drop-in $path mode is $mode, want 440"
    fi
    log_detail "VALIDATE sudoers $path: root:root 0440"
}

# validate_directory checks that a directory exists with the
# specified owner:group and permissions. If missing, it creates
# it (self-heal). If permissions or ownership are wrong, it
# fixes them. Does NOT fail on self-heal or repair.
#
# Safety: only exact allowed paths may be repaired. Empty,
# relative, root, or path-traversal values are rejected.
validate_directory() {
    local path="$1"
    local owner="$2"
    local perms="$3"
    # Reject empty or unsafe paths.
    if [ -z "$path" ] || [ "$path" = "/" ]; then
        fail "validate_directory: refusing unsafe path '$path'"
    fi
    case "$path" in
        */..*|*\\..*|*/./*|*\\\./*)
            fail "validate_directory: refusing path-traversal '$path'"
            ;;
    esac
    case "$path" in
        /*)
            # Absolute path — check against allowlist.
            local allowed=0
            for ap in \
                /opt/orvix \
                /usr/share/orvix/admin \
                /var/lib/orvix \
                /var/log/orvix \
                /var/lib/orvix/backups \
                /var/lib/orvix/coremail \
                /var/lib/orvix/coremail/mailstore \
                /etc/orvix
            do
                if [ "$path" = "$ap" ]; then
                    allowed=1
                    break
                fi
            done
            if [ "$allowed" -eq 0 ]; then
                fail "validate_directory: path '$path' not in allowlist"
            fi
            ;;
        *)
            fail "validate_directory: refusing relative path '$path'"
            ;;
    esac
    if [ ! -d "$path" ]; then
        log_detail "REPAIR directory $path missing; creating"
        mkdir -p "$path"
    fi
    chown "$owner" "$path"
    chmod "$perms" "$path"
    log_detail "VALIDATE directory $path: $owner $perms"
}

# validate_binary checks that /usr/local/bin/orvix exists and is
# executable. The binary is NEVER invoked before configuration and
# service setup are complete. If file(1) or sha256sum are available
# they are used for additional integrity metadata (non-fatal).
# Fails if the binary does not exist or is not executable.
validate_binary() {
    local bin="$ORVIX_BIN"
    if [ ! -f "$bin" ]; then
        fail "binary $bin does not exist"
    fi
    if [ ! -x "$bin" ]; then
        fail "binary $bin is not executable"
    fi
    local extra=""
    if command -v file >/dev/null 2>&1; then
        extra="$(file -b "$bin" 2>/dev/null || true)"
    fi
    if command -v sha256sum >/dev/null 2>&1; then
        local hash
        hash="$(sha256sum "$bin" 2>/dev/null | cut -d' ' -f1 || true)"
        if [ -n "$hash" ]; then
            extra="${extra:+$extra, }sha256=${hash}"
        fi
    fi
    if [ -n "$extra" ]; then
        log_detail "VALIDATE binary $bin: executable, $extra"
    else
        log_detail "VALIDATE binary $bin: executable"
    fi
}

# validate_admin_ui checks that the admin UI assets were copied
# to /usr/share/orvix/admin and that the required files exist.
# Fails if any required file is missing.
validate_admin_ui() {
    local ui_dir="/usr/share/orvix/admin"
    if [ ! -d "$ui_dir" ]; then
        fail "admin UI directory $ui_dir does not exist"
    fi
    if [ ! -f "$ui_dir/index.html" ]; then
        fail "admin UI index.html not found at $ui_dir/index.html"
    fi
    if [ ! -f "$ui_dir/app.js" ]; then
        fail "admin UI app.js not found at $ui_dir/app.js"
    fi
    log_detail "VALIDATE admin UI $ui_dir: index.html + app.js present"
}

# validate_https_config checks whether a reverse proxy and
# certificate are configured. This is advisory only — the
# installer does NOT obtain certificates during installation.
# Returns 0 if config exists, 1 if not (non-fatal).
validate_https_config() {
    local config_path="/etc/caddy/Caddyfile"
    if [ ! -f "$config_path" ]; then
        log_detail "HTTPS config $config_path not found (advisory, non-fatal)"
        return 1
    fi
    if ! command -v caddy >/dev/null 2>&1; then
        log_detail "HTTPS caddy binary not found (advisory, non-fatal)"
        return 1
    fi
    # Check that the Caddyfile references the admin domain.
    if ! grep -q "reverse_proxy 127.0.0.1:8080" "$config_path" 2>/dev/null; then
        log_detail "HTTPS Caddyfile does not proxy to admin API (non-fatal)"
    fi
    log_detail "HTTPS config $config_path: present"
    return 0
}

# ── Smoke tests ────────────────────────────────────────

# smoke_tests runs the post-install health and reachability
# checks. Tests that require authentication or are
# config-dependent are excluded; only public, always-on
# endpoints are fatal. Fails the install if any fatal smoke
# test fails.
smoke_tests() {
    local failures=0
    local base="http://127.0.0.1:8080"
    local jmap_base="http://127.0.0.1:8081"

    # 1. Health endpoint (fatal — always on, unauthenticated).
    log_detail "SMOKE health $base/api/v1/health"
    if curl -fsS "$base/api/v1/health" >/dev/null 2>&1; then
        log_detail "  PASS health"
    else
        log_detail "  FAIL health"
        failures=$((failures + 1))
    fi

    # 2. Admin login page (fatal — always on, unauthenticated).
    log_detail "SMOKE admin $base/admin"
    if curl -fsSI "$base/admin" >/dev/null 2>&1; then
        log_detail "  PASS admin"
    else
        log_detail "  FAIL admin"
        failures=$((failures + 1))
    fi

    # 3. JMAP discovery (fatal — always on, unauthenticated).
    log_detail "SMOKE jmap $jmap_base/.well-known/jmap"
    if curl -fsS "$jmap_base/.well-known/jmap" >/dev/null 2>&1; then
        log_detail "  PASS jmap"
    else
        log_detail "  FAIL jmap"
        failures=$((failures + 1))
    fi

    # 4. Webmail (fatal — always on, unauthenticated).
    log_detail "SMOKE webmail $base/webmail"
    if curl -fsSI "$base/webmail" >/dev/null 2>&1; then
        log_detail "  PASS webmail"
    else
        log_detail "  FAIL webmail"
        failures=$((failures + 1))
    fi

    # 5. Metrics (advisory — may be disabled in config).
    log_detail "SMOKE metrics $base/metrics"
    if curl -fsSI "$base/metrics" >/dev/null 2>&1; then
        log_detail "  PASS metrics"
    else
        log_detail "  SKIP metrics (advisory)"
    fi

    # Update preflight and backup API are admin-protected;
    # they cannot be verified without authentication in the
    # installer smoke context. Their validation is handled
    # by validate_systemd (helper unit) and validate_directory
    # (backup directory) respectively.

    if [ "$failures" -gt 0 ]; then
        fail "$failures smoke test(s) failed"
    fi
    log_detail "SMOKE all fatal tests passed"
}

# ── Install report ─────────────────────────────────────

# generate_install_report produces a structured summary of
# the installation. The report is logged and appended to the
# install log. It never includes secrets, tokens, or
# environment variable values.
generate_install_report() {
    local version
    version="$(install_version)"
    {
        echo ""
        echo "========================================================="
        echo "              ORVIX INSTALLATION REPORT"
        echo "========================================================="
        echo ""
        echo "Version: $version"
        echo "Timestamp: $(date -Is)"
        echo "Hostname: $(hostname 2>/dev/null || echo unknown)"
        echo "Kernel: $(uname -r 2>/dev/null || echo unknown)"
        echo "OS: $(grep PRETTY_NAME /etc/os-release 2>/dev/null | cut -d= -f2 | tr -d '"' || echo unknown)"
        echo ""
        echo "--- Services ---"
        for svc in orvix.service orvix-update.service redis-server.service; do
            local active
            active="$(systemctl is-active "$svc" 2>/dev/null || echo unknown)"
            local enabled
            enabled="$(systemctl is-enabled "$svc" 2>/dev/null || echo unknown)"
            printf "  %-30s active=%-10s enabled=%s\n" "$svc" "$active" "$enabled"
        done
        echo ""
        echo "--- Ports ---"
        for port in 25 80 110 143 443 8080 8081 6379; do
            if ss -ltn "( sport = :$port )" 2>/dev/null | grep -q ":$port"; then
                echo "  Port $port: LISTENING"
            else
                echo "  Port $port: not listening"
            fi
        done
        echo ""
        echo "--- Directories ---"
        for dir in /opt/orvix /usr/share/orvix/admin /var/lib/orvix /var/log/orvix /etc/orvix; do
            if [ -d "$dir" ]; then
                local dir_owner
                dir_owner="$(stat -c '%a %U:%G' "$dir" 2>/dev/null || echo '?')"
                echo "  $dir ($dir_owner)"
            else
                echo "  $dir (MISSING)"
            fi
        done
        echo ""
        echo "--- Binary ---"
        if [ -x "$ORVIX_BIN" ]; then
            local bin_owner
            local bin_size
            bin_owner="$(stat -c '%a %U:%G' "$ORVIX_BIN" 2>/dev/null || echo '?')"
            bin_size="$(wc -c < "$ORVIX_BIN" 2>/dev/null || echo '?')"
            echo "  $ORVIX_BIN ($bin_owner, $bin_size bytes)"
        else
            echo "  $ORVIX_BIN (MISSING)"
        fi
        echo ""
        echo "--- Smoke Tests ---"
        echo "  See install log for detailed results"
        echo ""
        echo "--- Final Result ---"
        echo "  INSTALLATION COMPLETED SUCCESSFULLY"
        echo ""
        echo "========================================================="
    } >>"$INSTALL_LOG"
    # Also print a short summary to stdout.
    echo ""
    echo "${GREEN}Installation report appended to $INSTALL_LOG${NC}"
}

verify_install() {
	local email="$1"
	local password="$2"
	local login_endpoint="http://127.0.0.1:8080/admin/login"
	local users_count mailbox_count sql_email
	sql_email="$(sqlite_escape "$email")"

	systemctl is-active --quiet redis-server || fail "redis-server is not active"
	systemctl is-active --quiet orvix || fail "orvix is not active"
	systemctl is-enabled --quiet orvix || fail "orvix is not enabled"
	command -v sqlite3 >/dev/null 2>&1 || fail "sqlite3 is not installed"
	[ -f /var/lib/orvix/orvix.db ] || fail "database does not exist at /var/lib/orvix/orvix.db"
	users_count="$(sqlite3 /var/lib/orvix/orvix.db "SELECT COUNT(*) FROM users WHERE email = '$sql_email' AND role = 'admin' AND active = 1;" 2>/dev/null || true)"
	[ "$users_count" = "1" ] || fail "bootstrapped admin user row was not created for $email"
	mailbox_count="$(sqlite3 /var/lib/orvix/orvix.db "SELECT COUNT(*) FROM coremail_mailboxes WHERE email = '$sql_email' AND is_admin = 1 AND status = 'active' AND deleted_at IS NULL;" 2>/dev/null || true)"
	[ "$mailbox_count" = "1" ] || fail "bootstrapped admin mailbox row was not created for $email"
    curl -fsS http://127.0.0.1:8080/api/v1/health >/dev/null || fail "health endpoint failed"
    curl -fsSI http://127.0.0.1:8080/admin >/dev/null || fail "admin UI endpoint failed"
    curl -fsSI http://127.0.0.1:8080/webmail >/dev/null || fail "webmail UI endpoint failed"
    curl -fsS http://127.0.0.1:8081/.well-known/jmap >/dev/null || fail "JMAP endpoint failed"

    for port in 25 110 143 8080 8081 6379; do
        ss -ltn "( sport = :$port )" | grep -q ":$port" || fail "port $port is not listening"
	done

	local login_payload response_file http_code attempt
	login_payload="$(build_login_payload "$email" "$password")"
	response_file="$(mktemp)"
	http_code=""
	for attempt in 1 2 3 4 5; do
		http_code="$(curl -sS -o "$response_file" -w "%{http_code}" -H 'Content-Type: application/json' -d "$login_payload" "$login_endpoint" 2>/dev/null || true)"
		if [ "$http_code" = "200" ]; then
			break
		fi
		log_detail "login attempt $attempt: HTTP $http_code, retrying in 3s"
		sleep 3
	done
	if [ "$http_code" != "200" ]; then
		echo -e "${RED}Admin API login verification failed${NC}" >&2
		echo "Endpoint: $login_endpoint" >&2
		echo "bootstrap.env preserved for diagnosis: $BOOTSTRAP_ENV" >&2
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
	run_quiet rm -f "$BOOTSTRAP_ENV"
	echo "INSTALLATION VERIFICATION PASSED"
}

main() {
	require_root
	prepare_log
	trap on_error ERR
	log_detail "Orvix Enterprise Mail installer started"

	set_step "preparing" "System preflight" 10

	set_step "dependencies" "Platform dependencies" 20
	run_quiet apt-get update -qq
	run_quiet apt-get install -y -qq \
		-o Dpkg::Options::=--force-confdef \
		-o Dpkg::Options::=--force-confold \
		ca-certificates curl jq sqlite3 openssl tar gzip redis-server libcap2-bin iproute2 ufw
	run_quiet systemctl enable --now redis-server

    set_step "user" "Service identity" 35
    if ! id -u orvix >/dev/null 2>&1; then
        run_quiet useradd --system --user-group --create-home --home-dir /var/lib/orvix --shell /usr/sbin/nologin orvix
    fi

    run_quiet install -d -o orvix -g orvix -m 0750 /etc/orvix /var/lib/orvix /var/lib/orvix/coremail /var/lib/orvix/backups /var/log/orvix
    run_quiet install -d -o root -g root -m 0755 /usr/share/orvix/admin
    run_quiet install -d -o root -g root -m 0755 /usr/share/orvix/webmail
    # Validate and self-heal runtime directories.
    validate_directory /opt/orvix root:root 0755
    validate_directory /usr/share/orvix/admin root:root 0755
    validate_directory /var/lib/orvix orvix:orvix 0750
    validate_directory /var/log/orvix orvix:orvix 0750
    validate_directory /var/lib/orvix/backups orvix:orvix 0750

    set_step "configuration-input" "Administrator enrollment" 45
    local primary_domain admin_email admin_password
    primary_domain="$(prompt_domain)"
    admin_email="$(prompt_email)"
    admin_password="$(prompt_password)"

    set_step "binary" "CoreMail binary deployment" 60
    install_binary
    run_quiet chown root:root "$ORVIX_BIN"
    run_quiet chmod 0755 "$ORVIX_BIN"
    run_quiet setcap 'cap_net_bind_service=+ep' "$ORVIX_BIN"
    validate_binary

    set_step "configuration" "Configuration provisioning" 75
    write_config "$primary_domain"
    write_bootstrap_env "$admin_email" "$admin_password"
    run_quiet cp -R "$ORVIX_SOURCE_DIR"/release/admin/. /usr/share/orvix/admin/
    run_quiet cp -R "$ORVIX_SOURCE_DIR"/release/webmail/. /usr/share/orvix/webmail/
    run_quiet chown -R root:root /usr/share/orvix/admin
    run_quiet chown -R root:root /usr/share/orvix/webmail
    run_quiet find /usr/share/orvix/admin -type d -exec chmod 0755 {} +
    run_quiet find /usr/share/orvix/admin -type f -exec chmod 0644 {} +
    run_quiet find /usr/share/orvix/webmail -type d -exec chmod 0755 {} +
    run_quiet find /usr/share/orvix/webmail -type f -exec chmod 0644 {} +
    validate_admin_ui

    set_step "systemd" "Service activation" 85
    write_service
    install_update_helper
    run_quiet systemctl daemon-reload
    run_quiet systemctl enable orvix
    run_quiet systemctl enable orvix-update.service
    run_quiet systemctl restart orvix
    validate_systemd
    validate_sudoers

    set_step "verification" "Enterprise health verification" 95
    run_quiet sleep 5
    verify_install "$admin_email" "$admin_password"
    smoke_tests
    validate_https_config || true
    generate_install_report

    local server_ip
    server_ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
    server_ip="${server_ip:-127.0.0.1}"
    render_success "$primary_domain" "$server_ip" "$admin_email"
}

main "$@"
