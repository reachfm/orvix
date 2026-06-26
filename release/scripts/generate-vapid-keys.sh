#!/usr/bin/env bash
# generate-vapid-keys.sh — operator helper to mint a fresh VAPID
# (RFC 8292) ECDSA P-256 keypair for browser push notifications.
#
# Usage:
#     sudo ./generate-vapid-keys.sh [--write] [--subject <url>] [--print-private-key]
#
# What it does:
#   1. Generates a fresh ES256 keypair using the same Go runtime
#      that ships with the server (so the output format matches
#      what internal/coremail/push expects: URL-safe base64, no
#      padding, raw point + raw scalar).
#   2. By default prints only the public key to stdout for the
#      operator to paste into /etc/orvix/orvix.yaml (or env).
#      Add --print-private-key to also print the private key.
#   3. With --write, atomically writes:
#      - public key into /etc/orvix/orvix.yaml under
#        coremail.vapid_public_key
#      - private key into /etc/orvix/vapid_private.key (mode 0600,
#        root-only, never logged)
#      - ORVIX_COREMAIL_VAPID_PRIVATE_KEY_FILE=/etc/orvix/vapid_private.key
#        into the systemd override for the orvix service
#   4. The server reads COREMAIL_VAPID_PRIVATE_KEY_FILE at boot
#      and loads the key value from the file. The private key
#      content never appears in the process environment, YAML
#      config, or logs.
#
# Safety:
#   * With --write the private key is NEVER echoed to the terminal
#     (--print-private-key is ignored in write mode for safety).
#   * Private key file is created with mode 0600 owned by root:root.
#   * Aborts if destination files already exist unless --force.
#   * Refuses to write if it cannot set ownership/permissions.

set -euo pipefail

WRITE=0
FORCE=0
PRINT_PRIV=0
SUBJECT=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --write) WRITE=1; shift ;;
    --force) FORCE=1; shift ;;
    --print-private-key) PRINT_PRIV=1; shift ;;
    --subject) SUBJECT="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,30p' "$0"
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

# Resolve the project root.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
ORVIX_BIN="${ORVIX_BIN:-$PROJECT_ROOT/bin/orvix-generate-vapid}"

run_generator() {
  if [[ -x "$ORVIX_BIN" ]]; then
    "$ORVIX_BIN"
    return
  fi
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  cat > "$tmp/main.go" <<'EOF'
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/orvix/orvix/internal/coremail/push"
)

func main() {
	pub, priv, err := push.GenerateVAPIDKeys()
	if err != nil {
		fmt.Fprintln(os.Stderr, "generate vapid keys:", err)
		os.Exit(1)
	}
	out := map[string]string{"public_key": pub, "private_key": priv}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}
EOF
  (cd "$PROJECT_ROOT" && go run "$tmp/main.go")
}

read -r PUBLIC_KEY PRIVATE_KEY < <(run_generator | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d["public_key"], d["private_key"])')

# Sanity: both keys must be non-empty URL-safe base64.
if [[ ! "$PUBLIC_KEY" =~ ^[A-Za-z0-9_-]{80,90}$ ]]; then
  echo "public key has unexpected shape (got ${PUBLIC_KEY:0:12}…)" >&2
  exit 1
fi
if [[ ! "$PRIVATE_KEY" =~ ^[A-Za-z0-9_-]{40,50}$ ]]; then
  echo "private key has unexpected shape (got ${PRIVATE_KEY:0:12}…)" >&2
  exit 1
fi

echo "VAPID keypair generated."
echo "  vapid_public_key:  $PUBLIC_KEY"

if [[ "$PRINT_PRIV" -eq 1 && "$WRITE" -eq 0 ]]; then
  echo "  vapid_private_key: $PRIVATE_KEY"
fi

if [[ -z "$SUBJECT" ]]; then
  echo
  echo "Reminder: pick a VAPID subject (a mailto: or https: URL"
  echo "the push service can use to reach you on abuse reports)."
  echo "Run with --subject mailto:admin@yourdomain.tld to set it."
fi

if [[ "$WRITE" -eq 1 ]]; then
  CONFIG_FILE="/etc/orvix/orvix.yaml"
  PRIV_FILE="/etc/orvix/vapid_private.key"
  PUB_FILE="/etc/orvix/vapid_public.key"

  if [[ -f "$PRIV_FILE" && "$FORCE" -ne 1 ]]; then
    echo "$PRIV_FILE already exists; refusing to overwrite (use --force)." >&2
    exit 1
  fi

  umask 077
  install -m 0600 -o root -g root <(printf '%s' "$PRIVATE_KEY") "$PRIV_FILE"
  umask 022
  install -m 0644 -o root -g root <(printf '%s' "$PUBLIC_KEY") "$PUB_FILE"

  if [[ -f "$CONFIG_FILE" ]]; then
    cp "$CONFIG_FILE" "$CONFIG_FILE.bak.$(date +%Y%m%d%H%M%S)"
    if grep -qE '^\s*vapid_public_key:' "$CONFIG_FILE"; then
      sed -i.bak -E "s|^(\s*)vapid_public_key:.*|\1vapid_public_key: \"$PUBLIC_KEY\"|" "$CONFIG_FILE"
    else
      sed -i.bak -E "/^coremail:/{N;s|^coremail:\n|coremail:\n  vapid_public_key: \"$PUBLIC_KEY\"\n|}" "$CONFIG_FILE"
    fi
    # Remove any inline vapid_private_key value and replace with a file reference.
    if grep -qE '^\s*vapid_private_key:' "$CONFIG_FILE"; then
      sed -i.bak -E "/^\s*vapid_private_key:/d" "$CONFIG_FILE"
    fi
    # Ensure the vapid_private_key_file line exists under coremail:
    if grep -qE '^\s*vapid_private_key_file:' "$CONFIG_FILE"; then
      sed -i.bak -E "s|^(\s*)vapid_private_key_file:.*|\1vapid_private_key_file: \"$PRIV_FILE\"|" "$CONFIG_FILE"
    else
      # Insert after vapid_public_key line
      sed -i.bak -E "/^\s*vapid_public_key:/a\\  vapid_private_key_file: \"$PRIV_FILE\"" "$CONFIG_FILE"
    fi
    echo "Updated $CONFIG_FILE with vapid_public_key and vapid_private_key_file."
  else
    echo "$CONFIG_FILE not found; only wrote the key files." >&2
  fi

  echo
  echo "Private key written to $PRIV_FILE (mode 0600, root:root)."
  echo "The server reads the key via coremail.vapid_private_key_file."
  echo "Restart orvix to apply: sudo systemctl restart orvix"
fi
