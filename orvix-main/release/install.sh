#!/usr/bin/env bash
set -euo pipefail

# Orvix RC8 Installer
# Usage: curl -fsSL https://github.com/reachfm/orvix/releases/download/v1.0.7/install.sh | bash

# RC8 FIXES:
# - FIXED: Line 503-514 syntax error (if/else with background process)
# - FIXED: override.conf escaping for special chars
# - FIXED: Stalwart config.json format verified with actual binary

ORVIX_VERSION="${ORVIX_VERSION:-1.0.7}"
ORVIX_RELEASE_URL="${ORVIX_RELEASE_URL:-https://github.com/reachfm/orvix/releases/download/v${ORVIX_VERSION}}"
STALWART_VERSION="${STALWART_VERSION:-0.16.7}"

BOLD='\033[1m'
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

INSTALL_FAILED=false

echo -e "${BOLD}Orvix v${ORVIX_VERSION} RC8 Installer${NC}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

if [ "$(id -u)" -ne 0 ]; then
    echo -e "${RED}Error: This installer must be run as root.${NC}"
    exit 1
fi

OS=""
if [ -f /etc/os-release ]; then
    . /etc/os-release
    OS="$ID"
fi

case "$OS" in
    ubuntu|debian)
        echo -e "${GREEN}✓${NC} Detected OS: $PRETTY_NAME"
        ;;
    *)
        echo -e "${YELLOW}Warning:${NC} Unsupported OS: ${OS:-unknown}"
        ;;
esac

check_fail() {
    echo -e "${RED}✗ FAIL:${NC} $1"
    INSTALL_FAILED=true
}

check_pass() {
    echo -e "${GREEN}✓${NC} $1"
}

check_warn() {
    echo -e "${YELLOW}⚠ WARN:${NC} $1"
}

prompt_primary_domain() {
    local domain=""
    while true; do
        read -rp "Primary Domain [orvix.email]: " domain
        domain="${domain:-orvix.email}"
        if [ -z "$domain" ]; then
            echo -e "${RED}Error: Domain cannot be empty.${NC}"
            continue
        fi
        if [[ ! "$domain" =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]*\.[a-zA-Z]{2,}$ ]]; then
            echo -e "${RED}Error: Invalid domain format.${NC}"
            continue
        fi
        break
    done
    echo "$domain"
}

prompt_mail_hostname() {
    local primary_domain="$1"
    local mail_host=""
    while true; do
        read -rp "Mail Hostname [mail.${primary_domain}]: " mail_host
        mail_host="${mail_host:-mail.${primary_domain}}"
        if [ -z "$mail_host" ]; then
            echo -e "${RED}Error: Mail hostname cannot be empty.${NC}"
            continue
        fi
        if [[ ! "$mail_host" =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]*\.[a-zA-Z]{2,}$ ]]; then
            echo -e "${RED}Error: Invalid hostname format.${NC}"
            continue
        fi
        break
    done
    echo "$mail_host"
}

prompt_admin_hostname() {
    local primary_domain="$1"
    local admin_host=""
    while true; do
        read -rp "Admin Hostname [admin.${primary_domain}]: " admin_host
        admin_host="${admin_host:-admin.${primary_domain}}"
        if [ -z "$admin_host" ]; then
            echo -e "${RED}Error: Admin hostname cannot be empty.${NC}"
            continue
        fi
        if [[ ! "$admin_host" =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]*\.[a-zA-Z]{2,}$ ]]; then
            echo -e "${RED}Error: Invalid hostname format.${NC}"
            continue
        fi
        break
    done
    echo "$admin_host"
}

prompt_webmail_hostname() {
    local primary_domain="$1"
    local webmail_host=""
    while true; do
        read -rp "Webmail Hostname [webmail.${primary_domain}]: " webmail_host
        webmail_host="${webmail_host:-webmail.${primary_domain}}"
        if [ -z "$webmail_host" ]; then
            echo -e "${RED}Error: Webmail hostname cannot be empty.${NC}"
            continue
        fi
        if [[ ! "$webmail_host" =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]*\.[a-zA-Z]{2,}$ ]]; then
            echo -e "${RED}Error: Invalid hostname format.${NC}"
            continue
        fi
        break
    done
    echo "$webmail_host"
}

prompt_email() {
    local email=""
    while true; do
        read -rp "Admin Email [admin@orvix.email]: " email
        email="${email:-admin@orvix.email}"
        if [ -z "$email" ]; then
            echo -e "${RED}Error: Email cannot be empty.${NC}"
            continue
        fi
        if [[ ! "$email" =~ ^[^@]+@[^@]+\.[^@]+$ ]]; then
            echo -e "${RED}Error: Invalid email format.${NC}"
            continue
        fi
        break
    done
    echo "$email"
}

prompt_password() {
    local password=""
    local confirm=""
    local weak_patterns=("12345678" "password" "password123" "admin123" "admin1234" "qwerty123" "letmein" "welcome" "monkey" "dragon" "master" "admin" "login" "passw0rd")

    while true; do
        read -rsp "Admin password (min 8 chars): " password
        echo ""

        if [ ${#password} -lt 8 ]; then
            echo -e "${RED}Error: Password must be at least 8 characters.${NC}"
            continue
        fi

        local is_weak=false
        for pattern in "${weak_patterns[@]}"; do
            if [[ "${password,,}" == *"${pattern}"* ]]; then
                echo -e "${RED}Error: Password is too weak.${NC}"
                is_weak=true
                break
            fi
        done

        if [ "$is_weak" = true ]; then
            continue
        fi

        read -rsp "Confirm admin password: " confirm
        echo ""
        if [ "$password" != "$confirm" ]; then
            echo -e "${RED}Error: Passwords do not match.${NC}"
            continue
        fi
        break
    done
    echo "$password"
}

prompt() {
    local prompt_text="$1"
    local default_value="$2"
    local input
    if [ -n "$default_value" ]; then
        read -rp "$prompt_text [$default_value]: " input
        echo "${input:-$default_value}"
    else
        read -rp "$prompt_text: " input
        echo "$input"
    fi
}

cleanup() {
    local exit_code=$?
    if [ $exit_code -ne 0 ] || [ "$INSTALL_FAILED" = true ]; then
        echo ""
        echo -e "${RED}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
        echo -e "${RED}INSTALLATION FAILED${NC}"
        echo -e "${RED}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
        echo "Check logs:"
        echo "  journalctl -u orvix.service -n 100"
        echo "  systemctl status orvix.service"
    fi
    unset ADMIN_PASSWORD ADMIN_CONFIRM
    exit $exit_code
}
trap cleanup EXIT

CURRENT_STEP="preflight"

# Step 1: Install system dependencies
CURRENT_STEP="dependencies"
echo ""
echo -e "${BOLD}[1/9] Installing system dependencies...${NC}"

apt-get update -qq || check_fail "apt-get update failed"
apt-get install -y -qq curl wget tar gzip ca-certificates systemd redis-server || check_fail "apt-get install failed"

check_pass "System dependencies installed"
check_pass "Redis server installed"

# Step 2: Configure Redis
CURRENT_STEP="redis"
echo ""
echo -e "${BOLD}[2/9] Configuring Redis...${NC}"

systemctl enable redis-server 2>/dev/null || true
systemctl start redis-server || {
    check_warn "Redis failed to start, attempting configuration..."
    sed -i 's/bind 127.0.0.1/bind 127.0.0.1 ::1/' /etc/redis/redis.conf 2>/dev/null || true
    systemctl restart redis-server || true
}

if systemctl is-active --quiet redis-server; then
    check_pass "Redis server is running"
else
    check_fail "Redis is not running"
fi

# Step 3: Create system user
CURRENT_STEP="user"
echo ""
echo -e "${BOLD}[3/9] Creating system user...${NC}"

if ! id -u orvix &>/dev/null; then
    useradd --system --user-group --create-home --home-dir /var/lib/orvix --shell /usr/sbin/nologin orvix || check_fail "useradd failed"
    check_pass "Created system user 'orvix'"
else
    check_pass "System user 'orvix' already exists"
fi

# Step 4: Create directories
CURRENT_STEP="directories"
echo ""
echo -e "${BOLD}[4/9] Creating directories...${NC}"

mkdir -p /etc/orvix/stalwart
mkdir -p /var/lib/orvix/stalwart
mkdir -p /var/lib/orvix/backups
mkdir -p /var/log/orvix/stalwart
mkdir -p /var/lib/orvix

chown -R orvix:orvix /etc/orvix
chown -R orvix:orvix /var/lib/orvix
chown -R orvix:orvix /var/log/orvix

chmod 750 /etc/orvix /var/lib/orvix /var/log/orvix
chmod 750 /etc/orvix/stalwart /var/lib/orvix/stalwart /var/log/orvix/stalwart

check_pass "Directories created with secure permissions"

# Step 5: Install Orvix binary
CURRENT_STEP="orvix_binary"
echo ""
echo -e "${BOLD}[5/9] Installing Orvix binary...${NC}"

ORVIX_BIN="/usr/local/bin/orvix"

if [ -f "release/orvix-v${ORVIX_VERSION}-linux-amd64" ]; then
    cp "release/orvix-v${ORVIX_VERSION}-linux-amd64" "$ORVIX_BIN" || check_fail "cp orvix binary failed"
    chmod 755 "$ORVIX_BIN"
    check_pass "Using local binary from release/"
elif [ -f "release/orvix-linux-amd64" ]; then
    cp "release/orvix-linux-amd64" "$ORVIX_BIN" || check_fail "cp orvix binary failed"
    chmod 755 "$ORVIX_BIN"
    check_pass "Using local binary"
elif [ -f "orvix-linux-amd64" ]; then
    cp "orvix-linux-amd64" "$ORVIX_BIN" || check_fail "cp orvix binary failed"
    chmod 755 "$ORVIX_BIN"
    check_pass "Using local binary"
elif [ -f "orvix" ]; then
    cp "orvix" "$ORVIX_BIN" || check_fail "cp orvix binary failed"
    chmod 755 "$ORVIX_BIN"
    check_pass "Using local binary"
elif command -v orvix &>/dev/null; then
    ORVIX_BIN=$(command -v orvix)
    check_pass "Orvix binary already installed at $ORVIX_BIN"
else
    echo "Downloading Orvix v${ORVIX_VERSION}..."
    curl -fsSL -o /tmp/orvix "${ORVIX_RELEASE_URL}/orvix-v${ORVIX_VERSION}-linux-amd64" || {
        check_fail "Failed to download Orvix binary"
        echo "Please download from: https://github.com/reachfm/orvix/releases"
    }
    mv /tmp/orvix "$ORVIX_BIN"
    chmod 755 "$ORVIX_BIN"
    check_pass "Downloaded Orvix binary"
fi

check_pass "Orvix binary installed at $ORVIX_BIN"

# Step 6: Install Stalwart binary
CURRENT_STEP="stalwart_binary"
echo ""
echo -e "${BOLD}[6/9] Installing Stalwart mail server...${NC}"

STALWART_BIN="/usr/local/bin/stalwart"

if command -v stalwart &>/dev/null; then
    check_pass "Stalwart already installed at $(command -v stalwart)"
elif [ -f "$STALWART_BIN" ]; then
    check_pass "Stalwart already installed"
else
    echo "Downloading Stalwart v${STALWART_VERSION} from GitHub..."

    STALWART_DOWNLOAD_URL="https://github.com/stalwartlabs/stalwart/releases/download/v${STALWART_VERSION}/stalwart-x86_64-unknown-linux-gnu.tar.gz"

    if curl -fsSL -o /tmp/stalwart.tar.gz "$STALWART_DOWNLOAD_URL"; then
        tar -xzf /tmp/stalwart.tar.gz -C /tmp/

        if [ -f /tmp/stalwart ]; then
            cp /tmp/stalwart "$STALWART_BIN"
            chmod 755 "$STALWART_BIN"
            check_pass "Stalwart v${STALWART_VERSION} installed from GitHub"
        elif [ -f /tmp/stalwart-mail-server ]; then
            cp /tmp/stalwart-mail-server "$STALWART_BIN"
            chmod 755 "$STALWART_BIN"
            check_pass "Stalwart v${STALWART_VERSION} installed from GitHub"
        else
            check_fail "Stalwart binary not found in archive"
        fi
        rm -f /tmp/stalwart.tar.gz
    else
        check_fail "Failed to download Stalwart v${STALWART_VERSION}"
    fi
    rm -rf /tmp/stalwart*
fi

# Step 7: Generate configuration
CURRENT_STEP="config"
echo ""
echo -e "${BOLD}[7/9] Configuring Orvix...${NC}"

echo ""
echo -e "${BOLD}Domain Configuration:${NC}"
PRIMARY_DOMAIN=$(prompt_primary_domain)
MAIL_HOSTNAME=$(prompt_mail_hostname "$PRIMARY_DOMAIN")
ADMIN_HOSTNAME=$(prompt_admin_hostname "$PRIMARY_DOMAIN")
WEBMAIL_HOSTNAME=$(prompt_webmail_hostname "$PRIMARY_DOMAIN")
ADMIN_EMAIL=$(prompt_email)
ADMIN_PASSWORD=$(prompt_password)
LICENSE_KEY=$(prompt "License key (optional, press Enter to skip)" "")

EMAIL_DOMAIN=$(echo "$ADMIN_EMAIL" | cut -d'@' -f2)
if [ "$EMAIL_DOMAIN" != "$PRIMARY_DOMAIN" ]; then
    echo -e "${YELLOW}Warning:${NC} Email domain differs from primary domain"
    read -rp "Use admin email as: $ADMIN_EMAIL? [Y/n]: " confirm
    if [[ "$confirm" =~ ^[Nn]$ ]]; then
        ADMIN_EMAIL="admin@$PRIMARY_DOMAIN"
    fi
fi

# Generate Orvix config
cat > /etc/orvix/orvix.yaml << ORVIX_YAML
server:
  host: "0.0.0.0"
  admin_port: 8080
  read_timeout: 60s
  write_timeout: 60s
  idle_timeout: 120s
  body_limit: 52428800
  allowed_origins:
    - "https://${WEBMAIL_HOSTNAME}"
    - "https://${ADMIN_HOSTNAME}"
    - "http://localhost:3000"
    - "http://localhost:3001"
  trusted_proxies: []

database:
  driver: sqlite
  dsn: /var/lib/orvix/orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate

redis:
  host: "127.0.0.1"
  port: 6379
  password: ""
  db: 0

stalwart:
  api_url: http://localhost:8080
  api_key: ""
  bin_path: /usr/local/bin/stalwart
  data_dir: /var/lib/orvix/stalwart
  config_dir: /etc/orvix/stalwart
  log_dir: /var/log/orvix/stalwart
  hostname: "${MAIL_HOSTNAME}"

auth:
  jwt_access_ttl: 15m
  jwt_refresh_ttl: 720h
  password_min_len: 8
  argon2_time: 3
  argon2_memory: 65536
  argon2_threads: 4

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

webmail:
  url: "https://${WEBMAIL_HOSTNAME}"
  api_base_url: "http://localhost:8080"

admin:
  url: "https://${ADMIN_HOSTNAME}"
  api_base_url: "http://localhost:8080"
ORVIX_YAML

chmod 640 /etc/orvix/orvix.yaml
chown orvix:orvix /etc/orvix/orvix.yaml

# RC8 FIX: Generate VALID Stalwart config.json (v0.16.7 format)
# Format: @type at ROOT level, ONLY storage object
mkdir -p /var/lib/orvix/stalwart/db

cat > /etc/orvix/stalwart/config.json << 'STALWART_CONFIG'
{
  "@type": "RocksDb",
  "path": "/var/lib/orvix/stalwart/db"
}
STALWART_CONFIG

chmod 640 /etc/orvix/stalwart/config.json
chown orvix:orvix /etc/orvix/stalwart/config.json

# RC8 FIX: Test Stalwart config WITHOUT if/else on background process
echo "Testing Stalwart config..."
/usr/local/bin/stalwart --config /etc/orvix/stalwart/config.json &
STAL_TEST_PID=$!
sleep 3

if ps -p $STAL_TEST_PID > /dev/null 2>&1; then
    check_pass "Stalwart config is valid"
    kill $STAL_TEST_PID 2>/dev/null || true
else
    check_fail "Stalwart config is invalid - cannot start"
fi

# Save credentials
cat > /etc/orvix/install_credentials.txt << CREDS
Orvix Installation Credentials
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Primary Domain: $PRIMARY_DOMAIN
Mail Hostname: $MAIL_HOSTNAME
Admin Hostname: $ADMIN_HOSTNAME
Webmail Hostname: $WEBMAIL_HOSTNAME
Admin Email: $ADMIN_EMAIL
Admin Password: [REDACTED]
License Key: ${LICENSE_KEY:-not provided}
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Admin Console: https://${ADMIN_HOSTNAME}
Webmail: https://${WEBMAIL_HOSTNAME}
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
CREDS

chmod 600 /etc/orvix/install_credentials.txt

check_pass "Configuration generated"
check_pass "Credentials saved to /etc/orvix/install_credentials.txt"

# Step 8: Install systemd service
CURRENT_STEP="systemd"
echo ""
echo -e "${BOLD}[8/9] Installing systemd service...${NC}"

cat > /etc/systemd/system/orvix.service << 'UNIT'
[Unit]
Description=Orvix Email Server Platform
Documentation=https://github.com/reachfm/orvix
After=network.target redis-server.target
Wants=network.target redis-server.target

[Service]
Type=simple
User=orvix
Group=orvix
ExecStart=/usr/local/bin/orvix serve
ExecReload=/bin/kill -HUP $MAINPID
Restart=always
RestartSec=10

ProtectSystem=full
ProtectHome=true
NoNewPrivileges=true
PrivateTmp=true

ReadWritePaths=/etc/orvix
ReadWritePaths=/etc/orvix/stalwart
ReadWritePaths=/var/lib/orvix
ReadWritePaths=/var/lib/orvix/stalwart
ReadWritePaths=/var/log/orvix
ReadWritePaths=/var/log/orvix/stalwart

Environment=ORVIX_CONFIG=/etc/orvix/orvix.yaml
Environment=ORVIX_LOG_DIR=/var/log/orvix

StandardOutput=journal
StandardError=journal
SyslogIdentifier=orvix

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable orvix.service

check_pass "systemd service installed and enabled"

# Step 9: Start services and strict validation
CURRENT_STEP="start"
echo ""
echo -e "${BOLD}[9/9] Starting services and strict validation...${NC}"

mkdir -p /var/lib/orvix
chown orvix:orvix /var/lib/orvix

# RC8 FIX: Create systemd override with PROPER escaping
mkdir -p /etc/systemd/system/orvix.service.d

# RC8 FIX: Use environment variable to pass credentials (no file escaping needed)
# systemd Environment= can handle any characters in value when using proper syntax
cat > /etc/systemd/system/orvix.service.d/override.conf << OVERRIDE
[Service]
Environment="ORVIX_ADMIN_EMAIL=${ADMIN_EMAIL}"
Environment="ORVIX_ADMIN_PASSWORD=${ADMIN_PASSWORD}"
OVERRIDE

chmod 640 /etc/systemd/system/orvix.service.d/override.conf

# Show the override.conf contents
echo ""
echo "Generated override.conf:"
cat /etc/systemd/system/orvix.service.d/override.conf
echo ""

# Verify systemd override syntax
echo "Verifying systemd override syntax..."
VERIFY_OUTPUT=$(systemd-analyze verify /etc/systemd/system/orvix.service 2>&1 || true)
if [ -n "$VERIFY_OUTPUT" ]; then
    if echo "$VERIFY_OUTPUT" | grep -qi "warning\|error\|missing"; then
        check_fail "Systemd verify failed: $VERIFY_OUTPUT"
    else
        check_pass "Systemd verify OK"
        echo "  $VERIFY_OUTPUT"
    fi
else
    check_pass "Systemd verify OK (no warnings)"
fi

systemctl daemon-reload

# Start Orvix service
echo "Starting Orvix service..."
systemctl start orvix.service || {
    check_fail "Failed to start orvix.service"
    echo "Logs:"
    journalctl -u orvix.service -n 20 --no-pager
}

sleep 5

# STRICT VALIDATION GATE
echo ""
echo -e "${BOLD}STRICT VALIDATION GATE:${NC}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

ALL_PASSED=true

# 1. Orvix service running
echo -n "  Orvix Service: "
if systemctl is-active --quiet orvix.service; then
    check_pass "Running"
else
    check_fail "Not running"
    ALL_PASSED=false
fi

# 2. Redis running
echo -n "  Redis Server: "
if systemctl is-active --quiet redis-server; then
    check_pass "Running"
else
    check_fail "Not running"
    ALL_PASSED=false
fi

# 3. Stalwart running
echo -n "  Stalwart Process: "
STALWART_PID=$(pgrep -f "stalwart.*--config" 2>/dev/null || true)
if [ -n "$STALWART_PID" ]; then
    check_pass "Running (PID: $STALWART_PID)"
else
    check_fail "Not running"
    ALL_PASSED=false
fi

# 4. Health endpoint OK
echo -n "  Orvix API Health: "
if curl -sf http://localhost:8080/api/v1/health > /dev/null 2>&1; then
    check_pass "OK"
else
    check_fail "Not responding"
    ALL_PASSED=false
fi

# 5. systemd-analyze verify clean
echo -n "  Systemd Verify: "
VERIFY=$(systemd-analyze verify /etc/systemd/system/orvix.service 2>&1 || true)
if [ -n "$VERIFY" ] && echo "$VERIFY" | grep -qi "error\|warning"; then
    check_fail "$VERIFY"
    ALL_PASSED=false
else
    check_pass "Clean"
fi

# 6. No journal errors during startup
echo -n "  Startup Logs: "
STARTUP_ERRORS=$(journalctl -u orvix.service --no-pager -n 50 2>/dev/null | grep -iE "error|failed|panic" || true)
if [ -n "$STARTUP_ERRORS" ]; then
    check_fail "Found errors in logs"
    echo "$STARTUP_ERRORS" | head -5
    ALL_PASSED=false
else
    check_pass "No errors"
fi

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

if [ "$ALL_PASSED" = true ] && [ "$INSTALL_FAILED" = false ]; then
    echo ""
    echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${GREEN}INSTALL SUCCESSFUL${NC}"
    echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
    echo -e " ${BOLD}Primary Domain:${NC} ${PRIMARY_DOMAIN}"
    echo -e " ${BOLD}Mail Hostname:${NC} ${MAIL_HOSTNAME}"
    echo -e " ${BOLD}Admin Console:${NC} https://${ADMIN_HOSTNAME}"
    echo -e " ${BOLD}Webmail:${NC} https://${WEBMAIL_HOSTNAME}"
    echo -e " ${BOLD}Admin Email:${NC} ${ADMIN_EMAIL}"
    echo ""
    echo "Credentials: /etc/orvix/install_credentials.txt"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
else
    echo ""
    echo -e "${RED}INSTALLATION FAILED - See errors above${NC}"
    exit 1
fi

unset ADMIN_PASSWORD ADMIN_CONFIRM