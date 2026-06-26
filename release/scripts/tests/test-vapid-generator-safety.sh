#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GENERATOR="$(cd "$SCRIPT_DIR/.." && pwd)/generate-vapid-keys.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

grep -q "go run" "$GENERATOR" && fail "generator must not use go run"
grep -q "ORVIX_BIN" "$GENERATOR" && fail "generator must not require an Orvix helper binary"
grep -q "node " "$GENERATOR" && fail "generator must not require Node"
grep -q "npm " "$GENERATOR" && fail "generator must not require npm"

bash -n "$GENERATOR"

if ! command -v openssl >/dev/null 2>&1 || ! command -v python3 >/dev/null 2>&1; then
  echo "PASS: openssl/python3 unavailable; static generator safety checks passed"
  exit 0
fi

out="$("$GENERATOR" --subject mailto:admin@example.com)"
echo "$out" | grep -q "vapid_public_key:" || fail "manual output must include public key"
echo "$out" | grep -q "vapid_private_key:" && fail "manual output must not print private key by default"

pub="$(printf '%s\n' "$out" | awk '/vapid_public_key:/ {print $2}')"
[[ "$pub" =~ ^[A-Za-z0-9_-]{80,90}$ ]] || fail "public key shape is invalid"

priv_out="$("$GENERATOR" --subject mailto:admin@example.com --print-private-key)"
priv="$(printf '%s\n' "$priv_out" | awk '/vapid_private_key:/ {print $2}')"
[[ "$priv" =~ ^[A-Za-z0-9_-]{40,50}$ ]] || fail "private key shape is invalid"

echo "PASS: VAPID generator uses openssl/python3 and keeps private key hidden by default"
