#!/usr/bin/env bash
set -euo pipefail

# Contract test for the per-binary release signature added to
# build-release-bundle.sh + release-bundle.yml. release/scripts/
# upgrade.sh verifies a signature sidecar next to the RAW orvix
# binary (it has no tarball-extraction step of its own) — this test
# asserts the release pipeline actually produces that sidecar, using
# the same signing secret/trust anchor as the tarball, rather than a
# separate or weaker mechanism.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
BUILD_SCRIPT="$REPO_ROOT/release/scripts/build-release-bundle.sh"
WORKFLOW="$REPO_ROOT/.github/workflows/release-bundle.yml"
PASS=0 FAIL=0

fail_msg() { echo "FAIL: $1"; FAIL=$((FAIL + 1)); }
pass()     { echo "PASS: $1"; PASS=$((PASS + 1)); }

echo "=== Release Per-Binary Signing Contract Test ==="

[ -f "$BUILD_SCRIPT" ] || { echo "FAIL: build script not found: $BUILD_SCRIPT"; exit 1; }
[ -f "$WORKFLOW" ] || { echo "FAIL: workflow file not found: $WORKFLOW"; exit 1; }

# 1. The sealed binary is copied out under the archive's naming
#    convention and checksummed, for BOTH the version-named archive
#    and its stable-channel alias.
if grep -qF 'cp "$BIN_OUT" "$ARCHIVE.bin"' "$BUILD_SCRIPT"; then
  pass "version-named archive: binary copied to \$ARCHIVE.bin"
else
  fail_msg "version-named archive: binary is not copied to \$ARCHIVE.bin"
fi
if grep -qF 'cp "$BIN_OUT" "$STABLE_ARCHIVE.bin"' "$BUILD_SCRIPT"; then
  pass "stable-channel archive: binary copied to \$STABLE_ARCHIVE.bin"
else
  fail_msg "stable-channel archive: binary is not copied to \$STABLE_ARCHIVE.bin"
fi

# 2. The signing loop includes $ARCHIVE.bin and $STABLE_ARCHIVE.bin —
#    same loop, same secret, same sign-release-artifact.sh call as the
#    tarball/manifest/sbom, never a separate or weaker signing path.
SIGN_LOOP="$(awk '/^if \[ -n "\$\{ORVIX_RELEASE_SIGNING_KEY_FILE/,/^elif \[ "\$\{ORVIX_REQUIRE_RELEASE_SIGNATURE/' "$BUILD_SCRIPT")"
if echo "$SIGN_LOOP" | grep -qF '"$ARCHIVE.bin"' && echo "$SIGN_LOOP" | grep -qF '"$STABLE_ARCHIVE.bin"'; then
  pass "signing loop includes both \$ARCHIVE.bin and \$STABLE_ARCHIVE.bin"
else
  fail_msg "signing loop is missing \$ARCHIVE.bin and/or \$STABLE_ARCHIVE.bin"
fi
if echo "$SIGN_LOOP" | grep -q 'sign-release-artifact\.sh "\$artifact" "\$ORVIX_RELEASE_SIGNING_KEY_FILE"'; then
  pass "binary signing reuses sign-release-artifact.sh with the same configured key — no separate mechanism"
else
  fail_msg "could not confirm binary signing reuses the same sign-release-artifact.sh call"
fi

# 3. release-bundle.yml's "Build release bundle" step fails closed if
#    the per-binary signature is missing, and locally re-verifies it
#    against the same committed trust anchor as the tarball.
if grep -q 'missing \$ASSET_BASE\.bin\.sig' "$WORKFLOW"; then
  pass "workflow fails closed when \$ASSET_BASE.bin.sig is missing"
else
  fail_msg "workflow does not fail closed on a missing \$ASSET_BASE.bin.sig"
fi
if grep -A2 'in "\$ASSET_BASE\.bin" -sigfile "\$ASSET_BASE\.bin\.sig"' "$WORKFLOW" | grep -q 'per-binary Ed25519 signature verification failed'; then
  pass "workflow locally re-verifies the per-binary signature with openssl"
else
  fail_msg "workflow does not locally re-verify the per-binary signature"
fi
if grep -B4 'in "\$ASSET_BASE\.bin" -sigfile "\$ASSET_BASE\.bin\.sig"' "$WORKFLOW" | grep -qF 'release/trust/orvix-release-signing.pub.pem'; then
  pass "per-binary verification uses the same committed trust anchor as the tarball"
else
  fail_msg "per-binary verification does not use release/trust/orvix-release-signing.pub.pem"
fi

# 4. Publish path (real, non-dry-run releases) uploads the binary
#    sidecars and verifies their presence on the remote release too.
if grep -qF '"$ASSET_BASE.bin.sig"' "$WORKFLOW" && grep -qF '"$ASSET_BASE.bin"' "$WORKFLOW" && grep -qF '"$ASSET_BASE.bin.sha256"' "$WORKFLOW"; then
  pass "publish step uploads \$ASSET_BASE.bin, .bin.sha256 and .bin.sig"
else
  fail_msg "publish step does not upload all three binary sidecar files"
fi
if grep -qF '"$ASSET_NAME.bin" "$ASSET_NAME.bin.sha256" "$ASSET_NAME.bin.sig"' "$WORKFLOW"; then
  pass "remote-asset verification checks for the binary sidecar files"
else
  fail_msg "remote-asset verification does not check for the binary sidecar files"
fi

echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ]
