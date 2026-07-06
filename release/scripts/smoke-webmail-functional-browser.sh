#!/usr/bin/env bash
# smoke-webmail-functional-browser.sh — Self-contained
# headless Chrome functional smoke for the Orvix Webmail SPA.
#
# The .mjs companion script (smoke-webmail-functional-browser.mjs)
# is fully self-contained: it spawns a local Node HTTP server
# that serves the webmail bundle AND mocks the few API
# endpoints the auth-gate + SPA shell probe. This means the
# smoke can PASS on any host that has Node + a Chromium-class
# browser on disk, with no live Orvix backend, no network
# reachability, and no port collisions.
#
# Locates:
#   1. Node (NODE override → command -v node → ... → common
#      Windows / Linux paths).
#   2. Chrome / Chromium / Edge (CHROME override → which →
#      common Windows / Linux paths).
#
# Exits 0 on PASS, 1 on FAIL. Never fakes PASS.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WEBMAIL_DIR="${WEBMAIL_DIR:-$SCRIPT_DIR/../webmail}"

# Convert POSIX /mnt/... and /c/... paths to native Windows
# paths for the Chromium binary and Node script. Some Node
# versions on Windows refuse to spawn binaries through a
# non-canonical path even though file exists at it.
to_win_path() {
    local p="$1"
    case "$p" in
        /mnt/*)
            local drive="${p#/mnt/}"
            drive="${drive:0:1}"
            local rest="${p#/mnt/?}"
            printf '%s:\\%s' "$(printf '%s' "$drive" | tr '[:lower:]' '[:upper:]')" "${rest#/}"
            ;;
        /[a-zA-Z]/*)
            local drive="${p:1:1}"
            local rest="${p:2}"
            printf '%s:\\%s' "$(printf '%s' "$drive" | tr '[:lower:]' '[:upper:]')" "$rest"
            ;;
        *)
            printf '%s' "$p"
            ;;
    esac
}

probe_node() {
    local __var="$1"; shift
    for candidate in "$@"; do
        [ -n "$candidate" ] || continue
        candidate="${candidate%$'\r'}"
        candidate="$(printf '%s' "$candidate" | sed -E 's/^[[:space:]]+//;s/[[:space:]]+$//')"
        [ -n "$candidate" ] || continue
        if [ -x "$candidate" ]; then
            eval "$__var=\"\$candidate\""
            return 0
        fi
    done
    return 1
}

# ── 1. Node discovery ──────────────────────────────────────────
NODE_BIN="${NODE:-}"
if [ -z "$NODE_BIN" ]; then
    if command -v node >/dev/null 2>&1; then
        NODE_BIN="$(command -v node)"
    elif command -v nodejs >/dev/null 2>&1; then
        NODE_BIN="$(command -v nodejs)"
    fi
fi
if [ -z "$NODE_BIN" ]; then
    WIN_CANDIDATES=(
        "/c/Program Files/nodejs/node.exe"
        "/c/Program Files (x86)/nodejs/node.exe"
        "/mnt/c/Program Files/nodejs/node.exe"
        "/mnt/c/Program Files (x86)/nodejs/node.exe"
        "C:/Program Files/nodejs/node.exe"
        "C:/Program Files (x86)/nodejs/node.exe"
    )
    probe_node NODE_BIN "${WIN_CANDIDATES[@]}" || true
fi
if [ -z "$NODE_BIN" ] && command -v where.exe >/dev/null 2>&1; then
    probe_node NODE_BIN "$(where.exe node 2>/dev/null | tr -d '\r' | head -n1)" || true
fi
if [ -z "$NODE_BIN" ] && command -v powershell.exe >/dev/null 2>&1; then
    PS_PATH="$(powershell.exe -NoProfile -Command "(Get-Command node -ErrorAction SilentlyContinue).Source" 2>/dev/null | tr -d '\r' | head -n1)"
    probe_node NODE_BIN "$PS_PATH" || true
fi
if [ -z "$NODE_BIN" ]; then
    echo "FAIL smoke-webmail-functional-browser: node is required" >&2
    exit 1
fi
if ! "$NODE_BIN" --version >/dev/null 2>&1; then
    echo "FAIL smoke-webmail-functional-browser: $NODE_BIN does not execute" >&2
    exit 1
fi

# ── 2. Chrome / Chromium / Edge discovery ─────────────────────
CHROME_BIN="${CHROME:-}"
if [ -z "$CHROME_BIN" ]; then
    for candidate in google-chrome google-chrome-stable chromium chromium-browser /snap/bin/chromium; do
        if command -v "$candidate" >/dev/null 2>&1; then
            CHROME_BIN="$(command -v "$candidate")"
            break
        fi
    done
fi
if [ -z "$CHROME_BIN" ]; then
    for prefix in "" "/mnt"; do
        for candidate in \
            "$prefix/c/Program Files/Google/Chrome/Application/chrome.exe" \
            "$prefix/c/Program Files (x86)/Google/Chrome/Application/chrome.exe" \
            "$prefix/c/Program Files/Microsoft/Edge/Application/msedge.exe" \
            "$prefix/c/Program Files (x86)/Microsoft/Edge/Application/msedge.exe"; do
            if [ -x "$candidate" ] 2>/dev/null || [ -f "$candidate" ] 2>/dev/null; then
                CHROME_BIN="$candidate"
                break 2
            fi
        done
    done
fi
if [ -z "$CHROME_BIN" ] && command -v where.exe >/dev/null 2>&1; then
    CHROME_BIN="$(where.exe chrome 2>/dev/null | tr -d '\r' | head -n1)" || true
fi
if [ -z "$CHROME_BIN" ] && command -v where.exe >/dev/null 2>&1; then
    CHROME_BIN="$(where.exe msedge 2>/dev/null | tr -d '\r' | head -n1)" || true
fi
if [ -z "$CHROME_BIN" ] && command -v powershell.exe >/dev/null 2>&1; then
    PS_PATH="$(powershell.exe -NoProfile -Command "(Get-Command chrome -ErrorAction SilentlyContinue).Source" 2>/dev/null | tr -d '\r' | head -n1)"
    [ -n "$PS_PATH" ] && CHROME_BIN="$PS_PATH"
fi
if [ -z "$CHROME_BIN" ] && command -v powershell.exe >/dev/null 2>&1; then
    PS_PATH="$(powershell.exe -NoProfile -Command "(Get-Command msedge -ErrorAction SilentlyContinue).Source" 2>/dev/null | tr -d '\r' | head -n1)"
    [ -n "$PS_PATH" ] && CHROME_BIN="$PS_PATH"
fi
if [ -z "$CHROME_BIN" ]; then
    if [ "${ORVIX_ALLOW_BROWSER_SMOKE_SKIP:-}" = "1" ]; then
        echo "SKIP smoke-webmail-functional-browser: Chrome/Chromium not found and ORVIX_ALLOW_BROWSER_SMOKE_SKIP=1"
        exit 0
    fi
    echo "FAIL smoke-webmail-functional-browser: Chrome/Chromium not found" >&2
    echo "Set CHROME=/path/to/chrome or install google-chrome/chromium." >&2
    exit 1
fi
if [ ! -x "$CHROME_BIN" ] && [ ! -f "$CHROME_BIN" ]; then
    echo "FAIL smoke-webmail-functional-browser: CHROME_BIN=$CHROME_BIN does not exist" >&2
    exit 1
fi

# ── 3. Verify the webmail bundle exists ──────────────────────
if [ ! -f "$WEBMAIL_DIR/index.html" ] || [ ! -f "$WEBMAIL_DIR/assets/webmail.js" ]; then
    echo "FAIL smoke-webmail-functional-browser: webmail bundle missing at $WEBMAIL_DIR" >&2
    exit 1
fi

# ── 4. Invoke Node ────────────────────────────────────────────
echo "functional browser smoke: using browser at $CHROME_BIN"
echo "functional browser smoke: using node at $NODE_BIN ($( "$NODE_BIN" --version 2>/dev/null || echo unknown ))"

# The .mjs expects argv[2] = webmailDir (a Windows-native path
# so the in-browser fetch sees file://-stable URLs through
# the bundled http server), argv[3] = CHROME_BROWSER env-style
# chrome binary path (Windows-native, since Node on Windows
# cannot always spawn a /mnt/c/... path cleanly).
NODE_SCRIPT="$(to_win_path "$SCRIPT_DIR/smoke-webmail-functional-browser.mjs")"
WEBMAIL_DIR_W="$(to_win_path "$WEBMAIL_DIR")"
CHROME_BIN_W="$(to_win_path "$CHROME_BIN")"

CHROME_BROWSER="$CHROME_BIN_W" "$NODE_BIN" "$NODE_SCRIPT" "$WEBMAIL_DIR_W" "$CHROME_BIN_W"
echo "functional browser smoke: PASS"
