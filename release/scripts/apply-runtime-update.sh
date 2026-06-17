#!/usr/bin/env bash
# Orvix CoreMail runtime update â€” binary + admin UI + webmail UI.
#
# This script builds the Go binary from the current source tree,
# installs it to /usr/local/bin/orvix, copies release/admin and
# release/webmail to /usr/share/orvix/admin and
# /usr/share/orvix/webmail respectively, then restarts the
# orvix service and probes /api/v1/health.
#
# Idempotency: each install step uses `install -m ...` or
# `cp -r` which overwrites in place. Files that exist on disk
# but not in the source tree are NOT removed â€” that is by
# design. Removing unrelated files would risk deleting
# operator-placed assets like custom CSS overrides.
#
# Permissions: binaries and directories are root-owned,
# world-readable+executable where appropriate. The webmail
# and admin UI files keep the same 0644/0755 mode the
# installer uses, so the runtime update is a no-op from a
# permissions perspective.
#
# Service restart: prefer systemctl if active; fall back to
# init.d; otherwise pkill and let the operator restart.
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

# â”€â”€ Step 1 of 7: Build binary â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
echo "[1/7] Building orvix binary..."
GO_CMD=""
if command -v go >/dev/null 2>&1; then
  GO_CMD="go"
elif [ -x /usr/local/go/bin/go ]; then
  GO_CMD="/usr/local/go/bin/go"
else
  echo "ERROR: go not found in PATH or /usr/local/go/bin/go. Install Go first." >&2
  exit 1
fi
"$GO_CMD" build -o /tmp/orvix-next ./cmd/orvix
if [ ! -f /tmp/orvix-next ]; then
  echo "ERROR: Build failed, binary not found." >&2
  exit 1
fi

# â”€â”€ Step 2 of 7: Install binary â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
echo "[2/7] Installing binary to /usr/local/bin/orvix..."
install -m 0755 /tmp/orvix-next /usr/local/bin/orvix
rm -f /tmp/orvix-next

# â”€â”€ Step 3 of 7: Set capabilities â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
echo "[3/7] Setting capabilities..."
if command -v setcap >/dev/null 2>&1; then
  setcap cap_net_bind_service=+ep /usr/local/bin/orvix
  echo "  cap_net_bind_service set."
else
  echo "  setcap not found, skipping capability setting."
fi

# â”€â”€ Step 4 of 7: Install admin UI â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
# Idempotent: overwrites in place. Does NOT recursively delete
# /usr/share/orvix/admin first â€” that would risk wiping
# operator-placed files. The fresh-install installer creates
# the directory; this script only writes files.
echo "[4/7] Installing admin UI to /usr/share/orvix/admin..."
if [ ! -d "$REPO_ROOT/release/admin" ]; then
  echo "ERROR: $REPO_ROOT/release/admin does not exist; cannot install admin UI." >&2
  exit 1
fi
mkdir -p /usr/share/orvix/admin
install -d -o root -g root -m 0755 /usr/share/orvix/admin
cp -r "$REPO_ROOT/release/admin/." /usr/share/orvix/admin/
find /usr/share/orvix/admin -type d -exec chmod 0755 {} +
find /usr/share/orvix/admin -type f -exec chmod 0644 {} +
chown -R root:root /usr/share/orvix/admin

# Verify the deployed admin UI is intact.
if [ ! -f /usr/share/orvix/admin/index.html ] || [ ! -f /usr/share/orvix/admin/app.js ]; then
  echo "ERROR: admin UI deployment incomplete (index.html or app.js missing)." >&2
  exit 1
fi

# â”€â”€ Step 5 of 7: Install webmail UI â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
# Idempotent same way: overwrites files in place, does NOT
# recursively delete the directory. The fresh-install
# installer also writes here; the runtime update keeps the
# two paths in sync so an operator who only ever ran the
# runtime update still gets the auth gate on /webmail.
echo "[5/7] Installing webmail UI to /usr/share/orvix/webmail..."
if [ ! -d "$REPO_ROOT/release/webmail" ]; then
  echo "ERROR: $REPO_ROOT/release/webmail does not exist; cannot install webmail UI." >&2
  exit 1
fi
mkdir -p /usr/share/orvix/webmail
install -d -o root -g root -m 0755 /usr/share/orvix/webmail
cp -r "$REPO_ROOT/release/webmail/." /usr/share/orvix/webmail/
find /usr/share/orvix/webmail -type d -exec chmod 0755 {} +
find /usr/share/orvix/webmail -type f -exec chmod 0644 {} +
chown -R root:root /usr/share/orvix/webmail

# Verify the deployed webmail has the auth gate. A missing
# gate means unauthenticated users would see the React mail
# UI rendering into #root even though every API call
# returns 401 â€” the exact production symptom this gate was
# added to prevent.
if [ ! -f /usr/share/orvix/webmail/index.html ]; then
  echo "ERROR: webmail UI deployment incomplete (index.html missing)." >&2
  exit 1
fi
if [ ! -f /usr/share/orvix/webmail/assets/auth-gate.js ]; then
  echo "ERROR: webmail UI deployment incomplete (auth-gate.js missing); unauthenticated users would see Inbox/Compose with no API access." >&2
  exit 1
fi
if [ ! -f /usr/share/orvix/webmail/assets/auth-gate.css ]; then
  echo "ERROR: webmail UI deployment incomplete (auth-gate.css missing)." >&2
  exit 1
fi
if ! grep -q 'auth-gate\.js' /usr/share/orvix/webmail/index.html; then
  echo "ERROR: deployed webmail index.html does not reference auth-gate.js." >&2
  exit 1
fi

# â”€â”€ Step 6 of 7: Restart service â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
echo "[6/7] Restarting orvix service..."
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

# â”€â”€ Step 7 of 7: Verify installed version + health â”€â”€â”€â”€â”€â”€
echo "[7/7] Verifying installed version + health..."
SHA="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
echo "  Installed git SHA: $SHA"

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

# Probe webmail index.html to confirm the gate is reachable
# via the live server. The smoke check would also catch this
# but a runtime update that ships a broken webmail is the
# exact failure mode that prompted the gate.
WEBMAIL_STATUS="$(curl -sf -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/webmail 2>/dev/null || echo '')"
if [ "$WEBMAIL_STATUS" = "200" ]; then
  echo "  Webmail index reachable (HTTP 200)."
else
  echo "WARN: /webmail probe returned '${WEBMAIL_STATUS}' (expected 200). Check admin SPA route." >&2
fi

echo ""
echo "Runtime update complete. SHA: $SHA"
