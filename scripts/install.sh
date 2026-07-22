#!/usr/bin/env bash
# OrvixEM unattended installer
# Intended entrypoint:
#   curl -fsSL https://orvix.email/install.sh | bash

set -Eeuo pipefail

ORVIX_VERSION="${ORVIX_VERSION:-latest}"
ORVIX_CHANNEL="${ORVIX_CHANNEL:-stable}"
ORVIX_DOMAIN="${ORVIX_DOMAIN:-orvix.email}"
ORVIX_HOSTNAME="${ORVIX_HOSTNAME:-mail.${ORVIX_DOMAIN}}"
ORVIX_INSTALL_DIR="${ORVIX_INSTALL_DIR:-/usr/local/bin}"
ORVIX_CONFIG_DIR="${ORVIX_CONFIG_DIR:-/etc/orvix}"
ORVIX_DATA_DIR="${ORVIX_DATA_DIR:-/var/lib/orvix}"
ORVIX_LOG_DIR="${ORVIX_LOG_DIR:-/var/log/orvix}"
ORVIX_UPDATE_SERVER="${ORVIX_UPDATE_SERVER:-https://orvix.email}"
ORVIX_BINARY_URL="${ORVIX_BINARY_URL:-}"
ORVIX_ADMIN_EMAIL="${ORVIX_ADMIN_EMAIL:-admin@${ORVIX_DOMAIN}}"
ORVIX_ADMIN_PASSWORD="${ORVIX_ADMIN_PASSWORD:-}"
ORVIX_RELEASE_ROOT="${ORVIX_RELEASE_ROOT:-/var/www/orvix-release}"
STALWART_VERSION="${STALWART_VERSION:-v0.16.7}"
STALWART_BIN="${STALWART_BIN:-/usr/local/bin/stalwart}"
STALWART_CONFIG="${STALWART_CONFIG:-/etc/stalwart/config.yaml}"

ROLLBACK_DIR="/var/lib/orvix/rollback/install-$(date -u +%Y%m%d%H%M%S)"
SUMMARY_FILE="/var/lib/orvix/install-summary.txt"

log() { printf '==> %s\n' "$*"; }
warn() { printf 'WARN: %s\n' "$*" >&2; }
fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

need_root() {
  if [ "$(id -u)" -ne 0 ]; then
    fail "run installer as root or via sudo"
  fi
}

backup_path() {
  local path="$1"
  if [ -e "$path" ] || [ -L "$path" ]; then
    mkdir -p "$ROLLBACK_DIR$(dirname "$path")"
    cp -a "$path" "$ROLLBACK_DIR$path"
  fi
}

on_error() {
  local code=$?
  warn "install failed with exit code $code"
  warn "rollback snapshots, when available, are in $ROLLBACK_DIR"
  exit "$code"
}
trap on_error ERR

detect_platform() {
  OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
  ARCH="$(uname -m)"
  case "$OS" in linux) ;; *) fail "unsupported OS: $OS" ;; esac
  case "$ARCH" in
    x86_64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) fail "unsupported architecture: $ARCH" ;;
  esac
  if [ -r /etc/os-release ]; then
    . /etc/os-release
    [ "${ID:-}" = "ubuntu" ] || warn "installer is certified for Ubuntu 22.04; detected ${PRETTY_NAME:-unknown}"
    [ "${VERSION_ID:-}" = "22.04" ] || warn "installer is certified for Ubuntu 22.04; detected ${VERSION_ID:-unknown}"
  fi
}

install_dependencies() {
  log "Installing OS dependencies"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y ca-certificates curl tar gzip openssl sqlite3 nginx certbot python3-certbot-nginx jq netcat-openbsd
}

create_users_dirs() {
  log "Creating users and directories"
  id -u orvix >/dev/null 2>&1 || useradd --system --home "$ORVIX_DATA_DIR" --shell /usr/sbin/nologin orvix
  mkdir -p "$ORVIX_CONFIG_DIR" "$ORVIX_DATA_DIR"/{rollback,snapshots,data,tls} "$ORVIX_LOG_DIR" /etc/stalwart /var/lib/stalwart "$ORVIX_RELEASE_ROOT"
  chown -R orvix:orvix "$ORVIX_DATA_DIR" "$ORVIX_LOG_DIR"
}

install_orvix_binary() {
  log "Installing OrvixEM binary"
  backup_path "$ORVIX_INSTALL_DIR/orvix"
  local url="${ORVIX_BINARY_URL:-${ORVIX_UPDATE_SERVER}/download/${ORVIX_VERSION}/orvix-${OS}-${ARCH}}"
  if ! curl -fsSL "$url" -o /tmp/orvix; then
    fail "failed to download OrvixEM binary from $url"
  fi
  install -m 0755 /tmp/orvix "$ORVIX_INSTALL_DIR/orvix"
}

install_stalwart_binary() {
  log "Installing Stalwart ${STALWART_VERSION}"
  if [ -x "$STALWART_BIN" ] && "$STALWART_BIN" --version 2>/dev/null | grep -q '0.16.7'; then
    log "Stalwart 0.16.7 already installed"
    return
  fi
  backup_path "$STALWART_BIN"
  local tmpdir
  tmpdir="$(mktemp -d)"
  case "$ARCH" in
    amd64) local target="x86_64-unknown-linux-gnu" ;;
    arm64) local target="aarch64-unknown-linux-gnu" ;;
    *) fail "unsupported Stalwart arch: $ARCH" ;;
  esac
  local url="https://github.com/stalwartlabs/stalwart/releases/download/${STALWART_VERSION}/stalwart-${target}.tar.gz"
  curl -fsSL "$url" -o "$tmpdir/stalwart.tar.gz"
  tar -xzf "$tmpdir/stalwart.tar.gz" -C "$tmpdir"
  install -m 0755 "$(find "$tmpdir" -type f -name stalwart | head -1)" "$STALWART_BIN"
}

write_orvix_config() {
  log "Writing Orvix config"
  backup_path "$ORVIX_CONFIG_DIR/orvix.yaml"
  local jwt
  jwt="$(openssl rand -hex 32)"
  cat >"$ORVIX_CONFIG_DIR/orvix.yaml" <<EOF
server:
  listen: "127.0.0.1:8088"
  external_url: "${ORVIX_HOSTNAME}"
database:
  driver: "sqlite"
  dsn: "${ORVIX_DATA_DIR}/data/orvix.db?_journal_mode=WAL&_busy_timeout=5000"
stalwart:
  config_path: "${STALWART_CONFIG}"
  binary_path: "${STALWART_BIN}"
  admin_port: 8081
  smtp_ports: [25, 587, 465]
  imap_ports: [143, 993]
  pop3_ports: [110, 995]
  jmap_ports: [8081]
security:
  jwt_secret: "${jwt}"
logging:
  level: "info"
  format: "json"
  output: "stdout"
updates:
  channel: "${ORVIX_CHANNEL}"
  update_server: "${ORVIX_UPDATE_SERVER}"
EOF
  chmod 0600 "$ORVIX_CONFIG_DIR/orvix.yaml"
}

write_systemd_services() {
  log "Writing systemd services"
  backup_path /etc/systemd/system/orvix.service
  cat >/etc/systemd/system/orvix.service <<'EOF'
[Unit]
Description=Orvix Enterprise Mail
After=network-online.target stalwart-server.service
Wants=network-online.target

[Service]
Type=simple
User=orvix
Group=orvix
WorkingDirectory=/var/lib/orvix
Environment=ORVIX_CONFIG_DIR=/etc/orvix
ExecStart=/usr/local/bin/orvix start
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

  backup_path /etc/systemd/system/stalwart-server.service
  cat >/etc/systemd/system/stalwart-server.service <<EOF
[Unit]
Description=Stalwart Mail Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${STALWART_BIN} --config ${STALWART_CONFIG}
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable stalwart-server orvix
}

write_nginx() {
  log "Configuring nginx reverse proxy"
  backup_path /etc/nginx/sites-available/orvix
  cat >/etc/nginx/sites-available/orvix <<EOF
server {
    listen 80;
    server_name ${ORVIX_DOMAIN} www.${ORVIX_DOMAIN} updates.${ORVIX_DOMAIN} ${ORVIX_HOSTNAME} admin.${ORVIX_DOMAIN} mail.${ORVIX_DOMAIN} portal.${ORVIX_DOMAIN} api.${ORVIX_DOMAIN};

    location = /install.sh {
        alias ${ORVIX_RELEASE_ROOT}/install.sh;
        default_type text/x-shellscript;
    }

    location /download/ {
        alias ${ORVIX_RELEASE_ROOT}/download/;
    }

    location / {
        proxy_pass http://127.0.0.1:8088;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }
}
EOF
  ln -sf /etc/nginx/sites-available/orvix /etc/nginx/sites-enabled/orvix
  rm -f /etc/nginx/sites-enabled/default
  nginx -t
  systemctl enable nginx
  systemctl restart nginx
}

configure_tls() {
  if [ "${ORVIX_TLS_AUTO:-1}" != "1" ]; then
    log "TLS auto-configuration disabled"
    return
  fi
  log "Configuring TLS with certbot when domain resolves locally"
  local public_ip
  public_ip="$(curl -fsSL --max-time 5 https://api.ipify.org || true)"
  local cert_domains=()
  local name resolved
  for name in "$ORVIX_DOMAIN" "$ORVIX_HOSTNAME"; do
    resolved="$(getent ahostsv4 "$name" | awk 'NR==1{print $1}')"
    if [ -n "$resolved" ] && [ -n "$public_ip" ] && [ "$resolved" = "$public_ip" ]; then
      cert_domains+=("-d" "$name")
    else
      warn "DNS for $name does not resolve to this server; skipping it for certbot"
    fi
  done
  if [ "${#cert_domains[@]}" -gt 0 ]; then
    certbot --nginx -n --agree-tos -m "$ORVIX_ADMIN_EMAIL" "${cert_domains[@]}" || warn "certbot failed; continuing with HTTP"
  else
    warn "No configured Orvix names resolve to this server; skipping certbot"
  fi
}

run_orvix_setup() {
  if [ -z "$ORVIX_ADMIN_PASSWORD" ]; then
    ORVIX_ADMIN_PASSWORD="$(openssl rand -base64 18)"
  fi

  log "Running migrations"
  ORVIX_CONFIG_DIR="$ORVIX_CONFIG_DIR" "$ORVIX_INSTALL_DIR/orvix" migrate

  log "Applying Stalwart integration"
  ORVIX_CONFIG_DIR="$ORVIX_CONFIG_DIR" "$ORVIX_INSTALL_DIR/orvix" stalwart apply

  log "Provisioning Stalwart domain and admin mailbox"
  ORVIX_CONFIG_DIR="$ORVIX_CONFIG_DIR" "$ORVIX_INSTALL_DIR/orvix" stalwart provision domain "$ORVIX_DOMAIN"
  ORVIX_CONFIG_DIR="$ORVIX_CONFIG_DIR" "$ORVIX_INSTALL_DIR/orvix" stalwart provision mailbox "$ORVIX_ADMIN_EMAIL" "$ORVIX_ADMIN_PASSWORD"

  log "Starting OrvixEM"
  systemctl restart orvix
}

bootstrap_admin() {
  log "Creating bootstrap admin account"
  for _ in $(seq 1 60); do
    if curl -fsS http://127.0.0.1:8088/health >/dev/null 2>&1 || curl -fsS http://127.0.0.1:8088/healthz >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  curl -fsS -X POST http://127.0.0.1:8088/api/v1/admin/bootstrap \
    -H 'Content-Type: application/json' \
    -d "{\"email\":\"${ORVIX_ADMIN_EMAIL}\",\"password\":\"${ORVIX_ADMIN_PASSWORD}\"}" \
    >/tmp/orvix-bootstrap-admin.json || true
  install -m 0600 /dev/null "$ORVIX_DATA_DIR/bootstrap-admin.env"
  cat >"$ORVIX_DATA_DIR/bootstrap-admin.env" <<EOF
ORVIX_ADMIN_EMAIL=${ORVIX_ADMIN_EMAIL}
ORVIX_ADMIN_PASSWORD=${ORVIX_ADMIN_PASSWORD}
EOF
  chown orvix:orvix "$ORVIX_DATA_DIR/bootstrap-admin.env"
}

verify_install() {
  log "Verifying services"
  systemctl is-active --quiet stalwart-server
  systemctl is-active --quiet orvix
  systemctl is-active --quiet nginx

  log "Verifying Orvix health"
  curl -fsS http://127.0.0.1:8088/health >/dev/null || curl -fsS http://127.0.0.1:8088/healthz >/dev/null

  log "Verifying Stalwart endpoints"
  curl -fsS -I http://127.0.0.1:8081/status >/dev/null
  nc -z 127.0.0.1 25
  nc -z 127.0.0.1 993
  nc -z 127.0.0.1 8081

  log "Verifying authenticated JMAP and IMAP"
  curl -fsS -u "$ORVIX_ADMIN_EMAIL:$ORVIX_ADMIN_PASSWORD" http://127.0.0.1:8081/jmap/session >/dev/null
  curl -k --fail --max-time 10 -u "$ORVIX_ADMIN_EMAIL:$ORVIX_ADMIN_PASSWORD" imaps://127.0.0.1:993/ >/dev/null

  log "Verifying SMTP path"
  local mail_file="/tmp/orvix-install-mail.txt"
  printf 'Subject: Orvix install proof\r\n\r\nOrvixEM install verification.\r\n' >"$mail_file"
  curl --fail --max-time 10 --url smtp://127.0.0.1:25 \
    --mail-from "$ORVIX_ADMIN_EMAIL" \
    --mail-rcpt "$ORVIX_ADMIN_EMAIL" \
    --upload-file "$mail_file" >/dev/null

  log "Verifying restart persistence"
  systemctl restart stalwart-server
  sleep 5
  curl -fsS -I http://127.0.0.1:8081/status >/dev/null
  curl -fsS -u "$ORVIX_ADMIN_EMAIL:$ORVIX_ADMIN_PASSWORD" http://127.0.0.1:8081/jmap/session >/dev/null
}

write_summary() {
  cat >"$SUMMARY_FILE" <<EOF
OrvixEM install summary
Version: ${ORVIX_VERSION}
Domain: ${ORVIX_DOMAIN}
Hostname: ${ORVIX_HOSTNAME}
Admin email: ${ORVIX_ADMIN_EMAIL}
Admin password file: ${ORVIX_DATA_DIR}/bootstrap-admin.env
Orvix config: ${ORVIX_CONFIG_DIR}/orvix.yaml
Stalwart config: ${STALWART_CONFIG}
Stalwart management: http://127.0.0.1:8081
Rollback snapshot: ${ROLLBACK_DIR}
EOF
  chmod 0600 "$SUMMARY_FILE"
  log "Install summary written to $SUMMARY_FILE"
}

main() {
  need_root
  detect_platform
  log "Installing OrvixEM ${ORVIX_VERSION} on ${OS}/${ARCH}"
  mkdir -p "$ROLLBACK_DIR"
  install_dependencies
  create_users_dirs
  install_orvix_binary
  install_stalwart_binary
  write_orvix_config
  write_systemd_services
  write_nginx
  configure_tls
  run_orvix_setup
  bootstrap_admin
  verify_install
  write_summary
  log "OrvixEM installation complete"
}

main "$@"
