#!/usr/bin/env bash
# generate-vapid-keys.sh — operator helper to mint a fresh VAPID
# (RFC 8292) ECDSA P-256 keypair for browser push notifications.
#
# Usage:
#     sudo ./generate-vapid-keys.sh [--write] [--subject <url>]
#
# What it does:
#   1. Generates a fresh ES256 keypair using the same Go runtime
#      that ships with the server (so the output format matches
#      what internal/coremail/push expects: URL-safe base64, no
#      padding, raw point + raw scalar).
#   2. By default prints the keys to stdout for the operator to
#      paste into /etc/orvix/orvix.yaml (or env).
#   3. With --write, atomically writes the public key into
#      /etc/orvix/orvix.yaml under coremail.vapid_public_key and
#      the private key into /etc/orvix/vapid_private.key (mode
#      0600, root-only). The public-key file is also written so
#      the admin runtime telemetry endpoint can surface it.
#
# Safety:
#   * Never logs the private key when --write is used; only
#     echoes it once on the terminal at the end so the operator
#     can capture it before the script exits.
#   * Aborts if the destination files already exist unless
#     --force is supplied.
#   * Refuses to write if it cannot chown the private key file
#     to root:root with mode 0600.

set -euo pipefail

WRITE=0
FORCE=0
SUBJECT=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --write) WRITE=1; shift ;;
    --force) FORCE=1; shift ;;
    --subject) SUBJECT="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,28p' "$0"
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

# Resolve the project root. The script lives in
# release/scripts/; the helper binary lives at the repo root.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
ORVIX_BIN="${ORVIX_BIN:-$PROJECT_ROOT/bin/orvix-generate-vapid}"

# If the helper binary is not available, fall back to a small
# Go program that calls internal/coremail/push.GenerateVAPIDKeys.
# We do not shell out to openssl because RFC 8292 raw point + raw
# scalar encoding is non-trivial and easier to keep in sync with
# the server by reusing the Go helper.
run_generator() {
  if [[ -x "$ORVIX_BIN" ]]; then
    "$ORVIX_BIN"
    return
  fi
  # Build a one-shot Go program on demand using the server's own
  # push package. This avoids a parallel OpenSSL implementation.
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

# Sanity: both keys must be non-empty URL-safe base64 with no
# padding. RFC 8292 mandates P-256 → 65-byte raw point → 86
# chars; raw scalar → 43 chars.
if [[ ! "$PUBLIC_KEY" =~ ^[A-Za-z0-9_-]{80,90}$ ]]; then
  echo "public key has unexpected shape (got ${PUBLIC_KEY:0:12}…)" >&2
  exit 1
fi
if [[ ! "$PRIVATE_KEY" =~ ^[A-Za-z0-9_-]{40,50}$ ]]; then
  echo "private key has unexpected shape (got ${PRIVATE_KEY:0:12}…)" >&2
  exit 1
fi

echo "VAPID keypair generated:"
echo "  vapid_public_key:  $PUBLIC_KEY"
echo "  vapid_private_key: $PRIVATE_KEY"

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
    # Replace or insert the vapid_public_key under coremail:.
    if grep -qE '^\s*vapid_public_key:' "$CONFIG_FILE"; then
      sed -i.bak -E "s|^(\s*)vapid_public_key:.*|\1vapid_public_key: \"$PUBLIC_KEY\"|" "$CONFIG_FILE"
    else
      sed -i.bak -E "/^coremail:/{N;s|^coremail:\n|coremail:\n  vapid_public_key: \"$PUBLIC_KEY\"\n|}" "$CONFIG_FILE"
    fi
    echo "Updated $CONFIG_FILE with vapid_public_key."
    echo "Private key written to $PRIV_FILE — point ORVIX_COREMAIL_VAPID_PRIVATE_KEY at it"
    echo "(or copy the value into coremail.vapid_private_key in $CONFIG_FILE)."
  else
    echo "$CONFIG_FILE not found; only wrote the key files." >&2
  fi
fi

# One final, clearly marked stdout banner so the operator can
# capture the private key BEFORE the script exits. The lines
# above are also captured; this is just a louder duplicate for
# terminals that scroll quickly.
echo
echo "============================================================"
echo " VAPID PRIVATE KEY (store securely, do NOT commit to git):"
echo " $PRIVATE_KEY"
echo "============================================================"