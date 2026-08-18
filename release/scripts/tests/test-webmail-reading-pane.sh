#!/usr/bin/env bash
# test-webmail-reading-pane.sh — wrapper for
# test-webmail-reading-pane.mjs: regression test for the webmail
# reading-pane raw-MIME rendering bug (renderBody must use the
# server's parsed html_body/text_body fields, never msg.rfc822).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if ! command -v node >/dev/null 2>&1; then
  echo "SKIP: node not found on PATH"
  exit 0
fi

exec node "$SCRIPT_DIR/test-webmail-reading-pane.mjs"
