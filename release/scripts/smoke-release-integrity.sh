#!/usr/bin/env bash
set -euo pipefail

ROOT="${REPO_ROOT:-$(git rev-parse --show-toplevel)}"
TMP="$(mktemp -d -t orvix-release-integrity.XXXXXX)"
trap 'rm -rf "$TMP"' EXIT

openssl genpkey -algorithm ED25519 -out "$TMP/test-signing.pem" >/dev/null 2>&1
openssl pkey -in "$TMP/test-signing.pem" -pubout -out "$TMP/test-public.pem" >/dev/null 2>&1
printf 'orvix release integrity fixture\n' >"$TMP/artifact"
bash "$ROOT/release/scripts/sign-release-artifact.sh" "$TMP/artifact" "$TMP/test-signing.pem" "$TMP/artifact.sig"
bash "$ROOT/release/scripts/verify-release-signature.sh" "$TMP/artifact" "$TMP/artifact.sig" "$TMP/test-public.pem"
printf 'tamper\n' >>"$TMP/artifact"
if bash "$ROOT/release/scripts/verify-release-signature.sh" "$TMP/artifact" "$TMP/artifact.sig" "$TMP/test-public.pem" >/dev/null 2>&1; then
    echo "tampered artifact passed signature verification" >&2
    exit 1
fi

bash "$ROOT/release/scripts/generate-sbom.sh" "$TMP/SBOM.spdx" "$ROOT/go.mod" "test" "$(git -C "$ROOT" rev-parse HEAD)"
grep -q '^SPDXVersion: SPDX-2.3$' "$TMP/SBOM.spdx"
grep -q '^PackageName: github.com/orvix/orvix$' "$TMP/SBOM.spdx"
grep -q 'tracked working tree is dirty' "$ROOT/release/scripts/build-release-bundle.sh"
grep -q 'ORVIX_REQUIRE_RELEASE_SIGNATURE' "$ROOT/release/scripts/build-release-bundle.sh"
if ORVIX_ALLOW_DIRTY_BUILD=1 bash "$ROOT/release/scripts/build-release-bundle.sh" \
    --channel '../escape' --output "$TMP/out" >"$TMP/invalid-channel.log" 2>&1; then
    echo "unsafe release channel was accepted" >&2
    exit 1
fi
grep -q 'invalid release channel token' "$TMP/invalid-channel.log"
if ORVIX_ALLOW_DIRTY_BUILD=1 bash "$ROOT/release/scripts/build-release-bundle.sh" \
    --version '1.0.0"broken' --output "$TMP/out" >"$TMP/invalid-version.log" 2>&1; then
    echo "unsafe release version was accepted" >&2
    exit 1
fi
grep -q 'invalid release version token' "$TMP/invalid-version.log"
echo "PASS release integrity: SBOM + Ed25519 sign/verify + tamper rejection + dirty-tree/input gates"
