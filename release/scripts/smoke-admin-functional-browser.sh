#!/usr/bin/env bash
# Browser-functional smoke for the Admin SPA core v1 flows.
#
# This is intentionally stronger than the static module smokes:
# it launches a real headless Chrome/Chromium instance, serves the
# release/admin bundle with a mocked local Admin API, signs in, and
# navigates the core Dashboard / Domains / Accounts routes.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ADMIN_DIR="${ADMIN_DIR:-$SCRIPT_DIR/../admin}"
NODE_BIN="${NODE:-}"

to_win_path() {
    local p="$1"
    case "$p" in
        /mnt/*)
            local drive="${p#/mnt/}"
            drive="${drive:0:1}"
            local rest="${p#/mnt/?}"
            printf '%s:\\%s' "$(echo "$drive" | tr '[:lower:]' '[:upper:]')" "${rest#/}"
            ;;
        /[a-zA-Z]/*)
            local drive="${p:1:1}"
            local rest="${p:2}"
            printf '%s:\\%s' "$(echo "$drive" | tr '[:lower:]' '[:upper:]')" "${rest#/}"
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
    echo "FAIL node is required for smoke-admin-functional-browser" >&2
    exit 1
fi

# Fail-closed browser detection.
# The caller (CI workflow) MUST provide CHROME, or we auto-detect.
# Only ORVIX_ALLOW_BROWSER_SMOKE_SKIP=1 may bypass (not used in CI).
CHROME_BIN="${CHROME:-}"
# 1. Linux native binaries
if [ -z "$CHROME_BIN" ]; then
    for candidate in google-chrome google-chrome-stable chromium chromium-browser /snap/bin/chromium; do
        if command -v "$candidate" >/dev/null 2>&1; then
            CHROME_BIN="$(command -v "$candidate")"
            break
        fi
    done
fi
# 2. Windows paths under Git Bash (/c/) or WSL (/mnt/c/)
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
# 3. Windows where.exe / powershell.exe probe (last resort)
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
        echo "SKIP smoke-admin-functional-browser: Chrome/Chromium not found and ORVIX_ALLOW_BROWSER_SMOKE_SKIP=1"
        exit 0
    fi
    echo "FAIL smoke-admin-functional-browser: Chrome/Chromium not found." >&2
    echo "Set CHROME=/path/to/chrome or install google-chrome/chromium." >&2
    exit 1
fi
# Accept both -x (native binary) and -f (Windows .exe on WSL mount without +x)
if [ ! -x "$CHROME_BIN" ] && [ ! -f "$CHROME_BIN" ]; then
    echo "FAIL smoke-admin-functional-browser: CHROME=$CHROME_BIN does not exist" >&2
    exit 1
fi

echo "functional browser smoke: using browser at $CHROME_BIN"
CHROME="$CHROME_BIN" "$NODE_BIN" "$(to_win_path "$SCRIPT_DIR/smoke-admin-functional-browser.mjs")" "$(to_win_path "$ADMIN_DIR")"
echo "functional browser smoke: PASS"
