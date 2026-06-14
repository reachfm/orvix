#!/usr/bin/env bash
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "ERROR: This script must be run as root." >&2
  exit 1
fi

REPO_ROOT="$(cd "$(dirname "$0")/.." && git rev-parse --show-toplevel 2>/dev/null || echo "")"
if [ -z "$REPO_ROOT" ]; then
  echo "ERROR: Cannot detect repository root. Run this script from the repo or a subdirectory." >&2
  exit 1
fi

cd "$REPO_ROOT"

echo "=== Orvix CoreMail Runtime Update ==="
echo "Repository root: $REPO_ROOT"

echo "[1/6] Building orvix binary..."
go build -o /tmp/orvix-next ./cmd/orvix
if [ ! -f /tmp/orvix-next ]; then
  echo "ERROR: Build failed, binary not found." >&2
  exit 1
fi

echo "[2/6] Installing binary to /usr/local/bin/orvix..."
install -m 0755 /tmp/orvix-next /usr/local/bin/orvix
rm -f /tmp/orvix-next

echo "[3/6] Setting capabilities..."
if command -v setcap >/dev/null 2>&1; then
  setcap cap_net_bind_service=+ep /usr/local/bin/orvix
  echo "  cap_net_bind_service set."
else
  echo "  setcap not found, skipping capability setting."
fi

echo "[4/6] Installing admin UI to /usr/share/orvix/admin..."
mkdir -p /usr/share/orvix/admin
cp -r release/admin/* /usr/share/orvix/admin/
chmod -R 0755 /usr/share/orvix/admin
chown -R root:root /usr/share/orvix/admin

echo "[5/6] Restarting orvix service..."
if systemctl is-active --quiet orvix 2>/dev/null; then
  systemctl restart orvix
  echo "  orvix service restarted via systemctl."
elif [ -f /etc/init.d/orvix ]; then
  /etc/init.d/orvix restart
  echo "  orvix service restarted via init.d."
else
  pkill -x orvix || true
  sleep 1
  echo "  orvix process killed (manual restart may be required)."
fi

echo "[6/6] Verifying installed version..."
SHA="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
echo "  Installed git SHA: $SHA"

echo "=== Smoke test: health endpoint ==="
HEALTH_STATUS=""
for i in 1 2 3 4 5; do
  HEALTH_STATUS="$(curl -sf -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/v1/health 2>/dev/null || echo '')"
  if [ "$HEALTH_STATUS" = "200" ]; then
    break
  fi
  sleep 2
done

if [ "$HEALTH_STATUS" = "200" ]; then
  echo "  Health check passed (HTTP 200)."
else
  echo "ERROR: Health check did not return 200 (got: '${HEALTH_STATUS}'). Check orvix service status." >&2
  exit 1
fi

echo ""
echo "Runtime update complete. SHA: $SHA"