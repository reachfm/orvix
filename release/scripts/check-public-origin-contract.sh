#!/usr/bin/env bash
set -euo pipefail

# Fail-closed check: no orvix.com or app.orvix.com in active source
# or generated marketing release assets.

SCAN_DIRS=(
  web/marketing/src
  web/marketing/public
  release/marketing
)
SCAN_FILE="web/marketing/index.html"

found=0

for dir in "${SCAN_DIRS[@]}"; do
  if [ -e "$dir" ]; then
    while IFS=: read -r file line _; do
      echo "$file:$line"
      found=1
    done < <(grep -rnI -E 'orvix\.com|app\.orvix\.com' "$dir" 2>/dev/null || true)
  fi
done

if [ -f "$SCAN_FILE" ]; then
  while IFS=: read -r file line _; do
    echo "$file:$line"
    found=1
  done < <(grep -nIE 'orvix\.com|app\.orvix\.com' "$SCAN_FILE" 2>/dev/null || true)
fi

if [ "$found" -ne 0 ]; then
  echo "FAIL: orvix.com or app.orvix.com found in active marketing source or release assets"
  exit 1
fi

echo "PASS: no forbidden domain references in marketing source or assets"
exit 0
