#!/usr/bin/env bash
set -euo pipefail

ORVIX_CADDYFILE="${ORVIX_CADDYFILE:-/etc/caddy/Caddyfile}"
ORVIX_CADDY_BIN="${ORVIX_CADDY_BIN:-caddy}"
ORVIX_SYSTEMCTL="${ORVIX_SYSTEMCTL:-systemctl}"
ORVIX_ADMIN_DOMAIN="${ORVIX_ADMIN_DOMAIN:-}"
MARKER="# ORVIX_ADMIN_ROOT_REDIRECT_V1"
REDIRECT_RULE="@orvix_admin_root path /${IFS}redir @orvix_admin_root /admin 308"
MODE="${1:---check}"

usage() {
  cat <<EOF
Usage: $0 [--check|--apply|--dry-run]

Modes:
  --check    Check whether the admin root redirect exists (default).
  --apply    Insert the redirect rule if not already present.
  --dry-run  Show what would be changed without modifying files.

Environment:
  ORVIX_CADDYFILE      Path to Caddyfile (default: /etc/caddy/Caddyfile)
  ORVIX_CADDY_BIN      Path to caddy binary (default: caddy)
  ORVIX_SYSTEMCTL      Path to systemctl (default: systemctl)
  ORVIX_ADMIN_DOMAIN   Exact admin hostname to match (default: detect admin.*)
EOF
  exit 1
}

case "$MODE" in
  --check|--apply|--dry-run) ;;
  *) usage ;;
esac

if [ ! -f "$ORVIX_CADDYFILE" ]; then
  echo "Caddyfile not found at $ORVIX_CADDYFILE"
  exit 1
fi

# Check if the marker already exists.
if grep -qF "$MARKER" "$ORVIX_CADDYFILE"; then
  if [ "$MODE" = "--check" ]; then
    echo "Admin root redirect already present in $ORVIX_CADDYFILE"
    exit 0
  elif [ "$MODE" = "--dry-run" ]; then
    echo "Admin root redirect already present — no action needed"
    exit 0
  fi
  echo "Admin root redirect already present in $ORVIX_CADDYFILE — idempotent, skipping"
  exit 0
fi

# Locate the admin server block.
if [ -n "$ORVIX_ADMIN_DOMAIN" ]; then
  block_match="$ORVIX_ADMIN_DOMAIN"
else
  # Find hostname blocks starting with "admin."
  candidates="$(awk '/^[a-zA-Z0-9][a-zA-Z0-9.-]* \{/{name=$1; if(name ~ /^admin\./) print name}' "$ORVIX_CADDYFILE" 2>/dev/null || true)"
  if [ -z "$candidates" ]; then
    echo "ERROR: no admin.* hostname block found in $ORVIX_CADDYFILE"
    exit 1
  fi
  count=$(echo "$candidates" | wc -l | tr -d ' ')
  if [ "$count" -ne 1 ]; then
    echo "ERROR: found $count admin.* hostname blocks — set ORVIX_ADMIN_DOMAIN to disambiguate"
    echo "$candidates"
    exit 1
  fi
  block_match="$candidates"
fi

if [ "$MODE" = "--dry-run" ]; then
  echo "Would insert redirect into $block_match block in $ORVIX_CADDYFILE"
  exit 0
fi

if [ "$MODE" = "--check" ]; then
  echo "Migration needed: admin root redirect not found for $block_match"
  exit 1
fi

# --apply mode
cp "$ORVIX_CADDYFILE" "${ORVIX_CADDYFILE}.backup.$(date +%Y%m%d%H%M%S)"

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

awk -v marker="$MARKER" -v rule="$REDIRECT_RULE" -v block="$block_match" '
BEGIN { in_block = 0; inserted = 0; brace_depth = 0 }
/^[a-zA-Z0-9][a-zA-Z0-9.-]* \{/ {
  name = $1
  if (name == block) { in_block = 1; brace_depth = 1; print; next }
}
in_block {
  print
  if ($0 ~ /{/) { brace_depth++ }
  if ($0 ~ /}/) { brace_depth--; if (brace_depth == 0) { in_block = 0 } }
  if (!inserted && in_block && $0 ~ /reverse_proxy/ && brace_depth == 1) {
    print marker
    split(rule, lines, "\n")
    for (i in lines) print lines[i]
    inserted = 1
  }
  next
}
{ print }
END {
  if (in_block && !inserted) {
    print marker
    split(rule, lines, "\n")
    for (i in lines) print lines[i]
    print "}"  # the awk already printed the opening brace
    inserted = 1
  }
  if (!inserted) {
    print "ERROR: could not locate reverse_proxy in $block_match block" > "/dev/stderr"
    exit 1
  }
}
' "$ORVIX_CADDYFILE" > "$tmp"

if ! "$ORVIX_CADDY_BIN" validate --config "$tmp" >/dev/null 2>&1; then
  echo "ERROR: caddy validate failed for the generated Caddyfile — original preserved"
  rm -f "$tmp"
  exit 1
fi

cp "$tmp" "$ORVIX_CADDYFILE"

if ! "$ORVIX_SYSTEMCTL" reload caddy 2>/dev/null; then
  echo "ERROR: caddy reload failed — restoring backup"
  backup="$(ls -t "${ORVIX_CADDYFILE}.backup."* 2>/dev/null | head -1)"
  if [ -n "$backup" ] && [ -f "$backup" ]; then
    cp "$backup" "$ORVIX_CADDYFILE"
    "$ORVIX_SYSTEMCTL" reload caddy 2>/dev/null || true
  fi
  exit 1
fi

echo "Admin root redirect inserted into $block_match block in $ORVIX_CADDYFILE"
exit 0
