#!/usr/bin/env bash
set -euo pipefail

ARTIFACT="${1:?usage: verify-release-signature.sh ARTIFACT SIGNATURE PUBLIC_KEY}"
SIGNATURE="${2:?signature path required}"
PUBLIC_KEY="${3:?public key path required}"
openssl pkeyutl -verify -rawin -pubin -inkey "$PUBLIC_KEY" -in "$ARTIFACT" -sigfile "$SIGNATURE" >/dev/null
echo "SIGNATURE_VERIFIED=YES"
