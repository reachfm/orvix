#!/usr/bin/env bash
set -euo pipefail

ARTIFACT="${1:?usage: sign-release-artifact.sh ARTIFACT PRIVATE_KEY OUTPUT_SIGNATURE}"
PRIVATE_KEY="${2:?private key path required}"
SIGNATURE="${3:?signature output required}"
[ -f "$ARTIFACT" ] || { echo "artifact not found" >&2; exit 1; }
[ -f "$PRIVATE_KEY" ] || { echo "signing key not found" >&2; exit 1; }
openssl pkeyutl -sign -rawin -inkey "$PRIVATE_KEY" -in "$ARTIFACT" -out "$SIGNATURE"
chmod 0644 "$SIGNATURE"
echo "SIGNATURE=$SIGNATURE"
