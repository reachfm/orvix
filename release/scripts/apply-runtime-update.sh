#!/usr/bin/env bash
# Orvix CoreMail runtime update — binary + admin, webmail, and marketing UIs.
#
# This script builds the Go binary from the current source tree,
# installs it to /usr/local/bin/orvix, copies release/admin and
# release/webmail and release/marketing to /usr/share/orvix/admin,
# /usr/share/orvix/webmail, and /usr/share/orvix/marketing, then restarts the
# orvix service and probes /api/v1/health.
#
# Idempotency: each install step uses `install -m ...` or
# `cp -r` which overwrites in place. Files that exist on disk
# but not in the source tree are NOT removed — that is by
# design. Removing unrelated files would risk deleting
# operator-placed assets like custom CSS overrides.
#
# Exceptions: the Webmail Enterprise v1 migration removed the
# Vite/React prototype bundle (index-CmhA8wNq.js,
# vendor-xxE1au3H.js, index-BiLI_Nmd.css). Step 5 sweeps
# those specific filenames before copy so a host updated
# from a Pre-v1 release converges on the same assets the
# fresh installer would produce. Operator-placed custom CSS
# in the same directory is preserved.
#
# Permissions: binaries and directories are root-owned,
# world-readable+executable where appropriate. The webmail
# and admin UI files keep the same 0644/0755 mode the
# installer uses, so the runtime update is a no-op from a
# permissions perspective.
#
# Service restart: prefer systemctl if active; fall back to
# init.d; otherwise pkill and let the operator restart.
#
# Production-readiness gate BLOCKER 3: this script is INSTALLED
# to /usr/share/orvix/scripts/apply-runtime-update.sh and runs
# from there. /usr/share/orvix/scripts is NOT a git worktree,
# so `git rev-parse --show-toplevel` from this script's own
# directory would fail. The script now reads ORVIX_REPO_DIR
# from the environment (the systemd unit at
# release/systemd/orvix-update.service sets it to /opt/orvix).
# Operators can override by exporting ORVIX_REPO_DIR=/path/to/repo
# before running the script manually.
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "ERROR: This script must be run as root." >&2
  exit 1
fi

# Resolve repo root from ORVIX_REPO_DIR, NOT from $0. The
# systemd oneshot unit exports ORVIX_REPO_DIR=/opt/orvix; this
# is also the default when the script is run manually.
REPO_ROOT="${ORVIX_REPO_DIR:-/opt/orvix}"
if [ ! -d "$REPO_ROOT" ]; then
  echo "ERROR: ORVIX_REPO_DIR does not exist: $REPO_ROOT" >&2
  echo "Set ORVIX_REPO_DIR to your orvix source tree (e.g. /opt/orvix)" >&2
  echo "or clone the repo:" >&2
  echo "    sudo git clone https://github.com/reachfm/orvix.git /opt/orvix" >&2
  exit 1
fi
if [ ! -d "$REPO_ROOT/.git" ] && [ ! -f "$REPO_ROOT/.git" ]; then
  echo "ERROR: ORVIX_REPO_DIR is not a git worktree: $REPO_ROOT" >&2
  echo "Expected a .git directory (or .git file for worktrees) inside the repo." >&2
  echo "Either:" >&2
  echo "  (a) clone the repo fresh:" >&2
  echo "        sudo git clone https://github.com/reachfm/orvix.git $REPO_ROOT" >&2
  echo "  (b) point ORVIX_REPO_DIR at an existing clone:" >&2
  echo "        sudo ORVIX_REPO_DIR=/path/to/existing/clone systemctl start orvix-update.service" >&2
  exit 1
fi

# Production-readiness gate BLOCKER 4: setcap needs CAP_SETFCAP.
# The systemd oneshot unit grants that capability; if the
# operator runs this script OUTSIDE the unit (e.g. from a
# shell as root), the kernel still grants CAP_SETFCAP to UID 0,
# so setcap works. We deliberately do NOT skip setcap — it is
# required to restore the low-port bind capability on the new
# binary after `install -m 0755` strips it.
REPO_ROOT="$(cd "$REPO_ROOT" && git rev-parse --show-toplevel 2>/dev/null || echo "$REPO_ROOT")"
cd "$REPO_ROOT"

echo "=== Orvix CoreMail Runtime Update ==="
echo "Repository root: $REPO_ROOT"

# ─── Step 1 of 7: Build binary ──────────────────────────────────
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

# ─── Step 2 of 7: Install binary ────────────────────────────────
echo "[2/7] Installing binary to /usr/local/bin/orvix..."
install -m 0755 /tmp/orvix-next /usr/local/bin/orvix
rm -f /tmp/orvix-next

# ─── Step 3 of 7: Set capabilities ─────────────────────────────
echo "[3/7] Setting capabilities..."
if command -v setcap >/dev/null 2>&1; then
  setcap cap_net_bind_service=+ep /usr/local/bin/orvix
  echo "  cap_net_bind_service set."
else
  echo "  setcap not found, skipping capability setting."
fi

# ─── Step 4 of 7: Install admin UI ──────────────────────────────
# Idempotent: overwrites in place. Does NOT recursively delete
# /usr/share/orvix/admin first — that would risk wiping
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

# Verify the deployed admin UI is intact. Accept the built React SPA
# (index.html + assets/*.js) shipped by build-release-bundle.sh as well as the
# legacy plain-JS admin (index.html + app.js).
if [ ! -f /usr/share/orvix/admin/index.html ]; then
  echo "ERROR: admin UI deployment incomplete (index.html missing)." >&2
  exit 1
fi
if [ ! -f /usr/share/orvix/admin/app.js ] && ! ls /usr/share/orvix/admin/assets/*.js >/dev/null 2>&1; then
  echo "ERROR: admin UI deployment incomplete (neither assets/*.js nor app.js present)." >&2
  exit 1
fi

# ─── Step 5 of 7: Install webmail UI ────────────────────────────
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
# One-shot cleanup of assets removed by the Webmail
# Enterprise v1 migration. Targeted rm so operator-placed
# custom CSS in the same directory is preserved. These
# three filenames are the Vite/React prototype bundle
# (Pre-Enterprise v1). Any host updated from a Pre-v1
# release will have them on disk; this sweep brings the
# runtime in line with the source tree.
for stale in index-CmhA8wNq.js vendor-xxE1au3H.js index-BiLI_Nmd.css; do
  if [ -f "/usr/share/orvix/webmail/assets/$stale" ]; then
    rm -f "/usr/share/orvix/webmail/assets/$stale" || true
    echo "  removed stale asset: $stale"
  fi
done
cp -r "$REPO_ROOT/release/webmail/." /usr/share/orvix/webmail/
find /usr/share/orvix/webmail -type d -exec chmod 0755 {} +
find /usr/share/orvix/webmail -type f -exec chmod 0644 {} +
chown -R root:root /usr/share/orvix/webmail

echo "[5/7] Installing marketing UI to /usr/share/orvix/marketing..."
if [ ! -d "$REPO_ROOT/release/marketing" ]; then
  echo "ERROR: $REPO_ROOT/release/marketing does not exist; cannot install marketing UI." >&2
  exit 1
fi
install -d -o root -g root -m 0755 /usr/share/orvix/marketing
rm -rf /usr/share/orvix/marketing/marketing-assets
cp -r "$REPO_ROOT/release/marketing/." /usr/share/orvix/marketing/
find /usr/share/orvix/marketing -type d -exec chmod 0755 {} +
find /usr/share/orvix/marketing -type f -exec chmod 0644 {} +
chown -R root:root /usr/share/orvix/marketing
if [ ! -f /usr/share/orvix/marketing/index.html ] || \
   [ ! -f /usr/share/orvix/marketing/404.html ] || \
   ! ls /usr/share/orvix/marketing/marketing-assets/*.js >/dev/null 2>&1; then
  echo "ERROR: marketing UI deployment incomplete." >&2
  exit 1
fi

# Defensive regression guard. The webmail v1 ships with
# auth-gate.js + webmail.js. If any of the legacy Vite/
# React bundle filenames reappear in the deployed assets
# directory for any reason (manual file copy, broken
# build, side-loaded prebuilt tarball), the runtime
# update aborts loudly rather than serving the demo
# bundle to operators. The legacy bundle calls
# /api/v1/queue which is an admin-only endpoint and
# would render admin queue data in the user-facing
# webmail.
for forbidden in index-CmhA8wNq.js vendor-xxE1au3H.js index-BiLI_Nmd.css; do
  if [ -f "/usr/share/orvix/webmail/assets/$forbidden" ]; then
    echo "ERROR: webmail UI ships the legacy demo React bundle ($forbidden); remove it and rebuild" >&2
    exit 1
  fi
done

# Verify the deployed webmail has the auth gate. A missing
# gate means unauthenticated users would see the React mail
# UI rendering into #root even though every API call
# returns 401 — the exact production symptom this gate was
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

# ─── Step 6 of 7: Restart service ──────────────────────────────
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

# ─── Step 7 of 7: Verify installed version + health ─────────────
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
