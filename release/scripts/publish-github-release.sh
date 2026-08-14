#!/usr/bin/env bash
# publish-github-release.sh — Build + publish a verified, SIGNED release bundle.
#
# This is the BLOCKER 8 release pipeline. It:
#   1. Builds a Linux amd64 release bundle via build-release-bundle.sh
#      (the bundle is the sealed, sha256- AND Ed25519-signed release
#      artifact install-public.sh downloads on a fresh VPS). The
#      ORVIX_RELEASE_SIGNING_KEY_FILE private key is REQUIRED — this
#      script refuses to publish an unsigned bundle.
#   2. Uploads the COMPLETE sidecar set for the version-named bundle
#      AND its channel alias: .sha256, .sig, .manifest.json,
#      .manifest.json.sig, .sbom.spdx, .sbom.spdx.sig. A release with
#      only a checksum but no .sig is exactly the failure that made
#      install-public.sh's signature gate 404 on the previous stable
#      release — never publish that shape again.
#   3. Creates a GitHub Release for the supplied tag (or updates
#      an existing one).
#   4. Runs verify-github-release-assets.sh against the published
#      release so the operator / CI knows the assets are reachable
#      AND signature-verifiable BEFORE they hand the install command
#      to a customer.
#
# Usage:
#   ORVIX_GITHUB_REPO=reachfm/orvix \
#   ORVIX_RELEASE_TAG=v1.0.4-rc2 \
#   ORVIX_CHANNEL=rc \
#   ORVIX_RELEASE_SIGNING_KEY_FILE=/secure/path/signing-key.pem \
#   bash release/scripts/publish-github-release.sh
#
# Required environment:
#   ORVIX_GITHUB_REPO    GitHub repo slug (default: reachfm/orvix)
#   ORVIX_RELEASE_TAG    Git tag for the release (required)
#   ORVIX_CHANNEL        Channel alias to repoint (default: stable)
#   ORVIX_RELEASE_SIGNING_KEY_FILE
#                        Path to the Ed25519 private key used to sign
#                        the bundle + manifest + SBOM. REQUIRED — an
#                        unsigned bundle is never publishable.
#   ORVIX_GH_TOKEN       GitHub token with repo:release scope.
#                        The script refuses to run without one.
#   ORVIX_DRY_RUN        If set, do everything except the actual
#                        `gh release upload` call.
#
# Optional:
#   ORVIX_BUILD_DIR      Output directory for the bundle (default:
#                        dist). The published .tar.gz and sidecars
#                        live here.
#
# Why this script is required:
#   The previous release process was manual (VPS build + `gh
#   release upload --clobber`). A missed step in that flow left
#   `latest/download` pointing at a stale bundle and shipped the
#   wrong binary for a release. This script makes the publish a
#   single command that is itself verified end-to-end.

set -euo pipefail

REPO_ROOT="${REPO_ROOT:-$(cd "$(dirname "$0")/../.." && pwd)}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BUILD_DIR="${ORVIX_BUILD_DIR:-$REPO_ROOT/dist}"

GITHUB_REPO="${ORVIX_GITHUB_REPO:-reachfm/orvix}"
RELEASE_TAG="${ORVIX_RELEASE_TAG:-}"
CHANNEL="${ORVIX_CHANNEL:-stable}"
DRY_RUN="${ORVIX_DRY_RUN:-}"

RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
YELLOW=$'\033[1;33m'
NC=$'\033[0m'

log() { printf '[%s] %s\n' "$(date -Is)" "$*" >&2; }
fail() { printf '%bERROR:%b %s\n' "$RED" "$NC" "$*" >&2; exit 1; }
warn() { printf '%bWARN:%b %s\n' "$YELLOW" "$NC" "$*" >&2; }

# ── 1. Pre-flight checks ──────────────────────────────────────────
[ -n "$RELEASE_TAG" ] || fail "ORVIX_RELEASE_TAG is required (e.g. v1.0.4-rc2)"
[[ "$RELEASE_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9._-]+)?$ ]] \
    || fail "ORVIX_RELEASE_TAG must look like a semver tag (got '$RELEASE_TAG')"

# Never publish an unsigned bundle: the previous stable release was
# published with only a .sha256 sidecar, so install-public.sh's
# signature gate could not verify it (.sig returned 404). The signing
# private key is REQUIRED here; we never generate or substitute a key.
if [ -z "${ORVIX_RELEASE_SIGNING_KEY_FILE:-}" ]; then
    fail "ORVIX_RELEASE_SIGNING_KEY_FILE is required (refusing to publish an unsigned bundle)"
fi
[ -f "$ORVIX_RELEASE_SIGNING_KEY_FILE" ] \
    || fail "ORVIX_RELEASE_SIGNING_KEY_FILE not found: $ORVIX_RELEASE_SIGNING_KEY_FILE"

if [ -z "${ORVIX_GH_TOKEN:-}" ]; then
    if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
        log "using gh CLI authentication (gh auth status OK)"
    else
        fail "either ORVIX_GH_TOKEN env var or an authenticated 'gh' CLI is required (BLOCKER 8 fail-closed gate)"
    fi
fi

if ! command -v curl >/dev/null 2>&1; then
    fail "curl is required to publish + verify release assets"
fi
if ! command -v sha256sum >/dev/null 2>&1; then
    fail "sha256sum is required to generate the bundle sidecar"
fi

# ── 2. Build the signed release bundle ────────────────────────────
log "building signed release bundle (Linux amd64)..."
if ! ORVIX_REQUIRE_RELEASE_SIGNATURE=1 \
    ORVIX_RELEASE_SIGNING_KEY_FILE="$ORVIX_RELEASE_SIGNING_KEY_FILE" \
    bash "$SCRIPT_DIR/build-release-bundle.sh" 2>&1; then
    fail "build-release-bundle.sh failed; refusing to publish a half-built bundle"
fi

# Resolve the version-named archive (never hardcode "stable").
RESOLVED_VERSION="$(ls "$BUILD_DIR" | sed -n "s/^orvix-enterprise-mail-\(.*\)-linux-amd64\.tar\.gz\$/\1/p" | grep -v "^${CHANNEL}\$" | head -n1)"
[ -n "$RESOLVED_VERSION" ] || fail "could not resolve the version-named bundle from $BUILD_DIR"

TARBALL="$BUILD_DIR/orvix-enterprise-mail-${RESOLVED_VERSION}-linux-amd64.tar.gz"
SIDECAR="$TARBALL.sha256"
for sidecar in \
    "$TARBALL" "$TARBALL.sha256" "$TARBALL.sig" \
    "$TARBALL.manifest.json" "$TARBALL.manifest.json.sig" \
    "$TARBALL.sbom.spdx" "$TARBALL.sbom.spdx.sig"; do
    [ -f "$sidecar" ] || fail "expected $sidecar after build but it is missing (signed sidecar set incomplete)"
done

SHA="$(awk '{print $1}' "$SIDECAR" | tr -d '\r\n')"
SIZE="$(wc -c < "$TARBALL" | tr -d ' \r\n')"
log "bundle: $TARBALL (version=$RESOLVED_VERSION)"
log "size:   $SIZE bytes"
log "sha256: $SHA"

# Re-verify the local signature before anything is uploaded.
if ! openssl pkeyutl -verify -rawin -pubin \
    -inkey "$REPO_ROOT/release/trust/orvix-release-signing.pub.pem" \
    -in "$TARBALL" -sigfile "$TARBALL.sig"; then
    fail "local Ed25519 signature verification failed for $TARBALL (refusing to publish)"
fi
log "local bundle signature verified against release/trust/orvix-release-signing.pub.pem"

# ── 3. Create or fetch the GitHub Release ─────────────────────────
RELEASE_JSON="$(mktemp)"
trap 'rm -f "$RELEASE_JSON"' EXIT

if [ -n "$DRY_RUN" ]; then
    log "DRY RUN: would create/update release $RELEASE_TAG on $GITHUB_REPO"
else
    log "creating release $RELEASE_TAG on $GITHUB_REPO..."
    if [ -n "${ORVIX_GH_TOKEN:-}" ]; then
        curl -fsSL \
            -H "Authorization: token $ORVIX_GH_TOKEN" \
            -H "Accept: application/vnd.github+json" \
            -X POST \
            "https://api.github.com/repos/$GITHUB_REPO/releases" \
            -d "$(printf '{"tag_name":"%s","name":"%s","body":"Orvix Enterprise Mail %s (channel=%s)\\n\\nSee release notes for the changelog.","draft":false,"prerelease":%s}' \
                "$RELEASE_TAG" "$RELEASE_TAG" "$RELEASE_TAG" "$CHANNEL" \
                "$([ "$CHANNEL" = "stable" ] && echo false || echo true)")" \
            > "$RELEASE_JSON" 2>/dev/null \
            || warn "release may already exist; proceeding to upload assets to the existing release"
    elif command -v gh >/dev/null 2>&1; then
        gh release create "$RELEASE_TAG" \
            --repo "$GITHUB_REPO" \
            --title "$RELEASE_TAG" \
            --notes "Orvix Enterprise Mail $RELEASE_TAG (channel=$CHANNEL)" \
            2>&1 || warn "release may already exist; proceeding to upload assets"
    fi
fi

# ── 4. Upload the FULL signed sidecar set to the release ─────────
# The previous stable release shipped only the bundle + .sha256;
# install-public.sh then 404'd on the .sig it requires. Uploading
# the complete 7-file set (bundle, .sha256, .sig, .manifest.json,
# .manifest.json.sig, .sbom.spdx, .sbom.spdx.sig) for BOTH the
# version-named and the channel-alias artifact is mandatory.
SIDECARS=(
    "$TARBALL"
    "$TARBALL.sha256"
    "$TARBALL.sig"
    "$TARBALL.manifest.json"
    "$TARBALL.manifest.json.sig"
    "$TARBALL.sbom.spdx"
    "$TARBALL.sbom.spdx.sig"
)
if [ -n "$DRY_RUN" ]; then
    for f in "${SIDECARS[@]}"; do
        log "DRY RUN: would upload $(basename "$f") to $RELEASE_TAG"
    done
else
    log "uploading full signed sidecar set to $RELEASE_TAG..."
    if [ -n "${ORVIX_GH_TOKEN:-}" ]; then
        # gh-style "upload" via the GitHub API upload endpoint
        UPLOAD_URL="$(curl -fsSL \
            -H "Authorization: token $ORVIX_GH_TOKEN" \
            -H "Accept: application/vnd.github+json" \
            "https://api.github.com/repos/$GITHUB_REPO/releases/tags/$RELEASE_TAG" \
            | grep -oE '"upload_url"\s*:\s*"[^"]+"' | head -n1 | sed -E 's/.*"([^"]+)".*/\1/' | sed 's/{?name,label}//')"
        [ -n "$UPLOAD_URL" ] || fail "could not resolve upload URL for $RELEASE_TAG (release missing?)"
        for f in "${SIDECARS[@]}"; do
            name="$(basename "$f")"
            log "uploading $name"
            curl -fsSL \
                -H "Authorization: token $ORVIX_GH_TOKEN" \
                -H "Content-Type: application/octet-stream" \
                --data-binary "@$f" \
                "$UPLOAD_URL?name=$name" >/dev/null
        done
    elif command -v gh >/dev/null 2>&1; then
        gh release upload "$RELEASE_TAG" "${SIDECARS[@]}" \
            --repo "$GITHUB_REPO" \
            --clobber
    fi
fi

# ── 5. Repoint the channel alias assets (stable/rc/dev) ──────────
# install-public.sh's no-version path uses
#   ${ORVIX_RELEASES_BASE}/orvix-enterprise-mail-<channel>-linux-amd64.tar.gz
# which GitHub resolves via the "latest" release. Publish the channel
# alias under the FULL sidecar set too, so the alias path is just as
# verifiable as the version-named path.
if [ -n "$DRY_RUN" ]; then
    log "DRY RUN: would repoint $CHANNEL alias assets"
else
    ALIAS_BASE="orvix-enterprise-mail-${CHANNEL}-linux-amd64.tar.gz"
    if [ -n "${ORVIX_GH_TOKEN:-}" ]; then
        UPLOAD_URL="$(curl -fsSL \
            -H "Authorization: token $ORVIX_GH_TOKEN" \
            -H "Accept: application/vnd.github+json" \
            "https://api.github.com/repos/$GITHUB_REPO/releases/tags/$RELEASE_TAG" \
            | grep -oE '"upload_url"\s*:\s*"[^"]+"' | head -n1 | sed -E 's/.*"([^"]+)".*/\1/' | sed 's/{?name,label}//')"
        for f in "${SIDECARS[@]}"; do
            alias_name="$(basename "$f" | sed "s/-$RESOLVED_VERSION-/-${CHANNEL}-/")"
            log "uploading alias $alias_name"
            curl -fsSL \
                -H "Authorization: token $ORVIX_GH_TOKEN" \
                -H "Content-Type: application/octet-stream" \
                --data-binary "@$f" \
                "$UPLOAD_URL?name=$alias_name" >/dev/null
        done
    elif command -v gh >/dev/null 2>&1; then
        ALIAS_FILES=()
        for f in "${SIDECARS[@]}"; do
            alias_name="$(basename "$f" | sed "s/-$RESOLVED_VERSION-/-${CHANNEL}-/")"
            cp "$f" "$BUILD_DIR/$alias_name"
            ALIAS_FILES+=("$BUILD_DIR/$alias_name")
        done
        gh release upload "$RELEASE_TAG" "${ALIAS_FILES[@]}" \
            --repo "$GITHUB_REPO" \
            --clobber
    fi
fi

# ── 6. Verify the published assets are reachable ──────────────────
log "verifying published assets via verify-github-release-assets.sh..."
if ! bash "$SCRIPT_DIR/verify-github-release-assets.sh" \
    --repo "$GITHUB_REPO" \
    --tag "$RELEASE_TAG" \
    --asset "orvix-enterprise-mail-${RESOLVED_VERSION}-linux-amd64.tar.gz" \
    --expected-sha "$SHA" \
    --expected-version "$RESOLVED_VERSION" 2>&1; then
    fail "verify-github-release-assets.sh reported the published release is not reachable (BLOCKER 8 fail-closed gate)"
fi
if ! bash "$SCRIPT_DIR/verify-github-release-assets.sh" \
    --repo "$GITHUB_REPO" \
    --tag "$RELEASE_TAG" \
    --asset "orvix-enterprise-mail-${CHANNEL}-linux-amd64.tar.gz" \
    --expected-sha "$SHA" \
    --expected-version "$RESOLVED_VERSION" 2>&1; then
    fail "verify-github-release-assets.sh reported the $CHANNEL alias assets are not reachable (BLOCKER 8 fail-closed gate)"
fi

log "release published + verified: $RELEASE_TAG (channel=$CHANNEL, version=$RESOLVED_VERSION)"
printf '\n%sPublished %s to %s (channel=%s, sha256=%s, size=%s)%s\n' \
    "$GREEN" "$TARBALL" "$GITHUB_REPO" "$CHANNEL" "${SHA:0:12}" "$SIZE" "$NC"
exit 0
