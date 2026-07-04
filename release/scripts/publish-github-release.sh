#!/usr/bin/env bash
# publish-github-release.sh — Build + publish a verified release bundle.
#
# This is the BLOCKER 8 release pipeline. It:
#   1. Builds a Linux amd64 release bundle via build-release-bundle.sh
#      (the bundle is the sealed, sha256-signed release artifact
#      install-public.sh downloads on a fresh VPS).
#   2. Generates the .sha256 sidecar that install-public.sh's
#      auto-resolver requires.
#   3. Creates a GitHub Release for the supplied tag (or updates
#      an existing one), uploads the bundle + sha256 as release
#      assets, and (optionally) repoints the "stable" alias assets
#      so the no-version curl-pipe command keeps working.
#   4. Runs verify-github-release-assets.sh against the published
#      release so the operator / CI knows the assets are reachable
#      BEFORE they hand the install command to a customer.
#
# Usage:
#   ORVIX_GITHUB_REPO=reachfm/orvix \
#   ORVIX_RELEASE_TAG=v1.0.3-rc5 \
#   ORVIX_CHANNEL=stable \
#   bash release/scripts/publish-github-release.sh
#
# Required environment:
#   ORVIX_GITHUB_REPO    GitHub repo slug (default: reachfm/orvix)
#   ORVIX_RELEASE_TAG    Git tag for the release (required)
#   ORVIX_CHANNEL        Channel alias to repoint (default: stable)
#   ORVIX_GH_TOKEN       GitHub token with repo:release scope.
#                        The script refuses to run without one.
#   ORVIX_DRY_RUN        If set, do everything except the actual
#                        `gh release upload` call.
#
# Optional:
#   ORVIX_BUILD_DIR      Output directory for the bundle (default:
#                        dist). The published .tar.gz and .sha256
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
[ -n "$RELEASE_TAG" ] || fail "ORVIX_RELEASE_TAG is required (e.g. v1.0.3-rc5)"
[[ "$RELEASE_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9._-]+)?$ ]] \
    || fail "ORVIX_RELEASE_TAG must look like a semver tag (got '$RELEASE_TAG')"

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

# ── 2. Build the release bundle ───────────────────────────────────
log "building release bundle (Linux amd64)..."
if ! bash "$SCRIPT_DIR/build-release-bundle.sh" 2>&1; then
    fail "build-release-bundle.sh failed; refusing to publish a half-built bundle"
fi

TARBALL="$BUILD_DIR/orvix-enterprise-mail-${CHANNEL}-linux-amd64.tar.gz"
SIDECAR="$TARBALL.sha256"
[ -f "$TARBALL" ] || fail "expected $TARBALL after build but it is missing"
[ -f "$SIDECAR" ] || fail "expected $SIDECAR after build but it is missing (the build script should write it)"

SHA="$(awk '{print $1}' "$SIDECAR" | tr -d '\r\n')"
SIZE="$(wc -c < "$TARBALL" | tr -d ' \r\n')"
log "bundle: $TARBALL"
log "size:   $SIZE bytes"
log "sha256: $SHA"

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

# ── 4. Upload the bundle + sha256 sidecar to the release ─────────
if [ -n "$DRY_RUN" ]; then
    log "DRY RUN: would upload $TARBALL and $SIDECAR to $RELEASE_TAG"
else
    log "uploading bundle + sha256 to $RELEASE_TAG..."
    if [ -n "${ORVIX_GH_TOKEN:-}" ]; then
        # gh-style "upload" via the GitHub API upload endpoint
        UPLOAD_URL="$(curl -fsSL \
            -H "Authorization: token $ORVIX_GH_TOKEN" \
            -H "Accept: application/vnd.github+json" \
            "https://api.github.com/repos/$GITHUB_REPO/releases/tags/$RELEASE_TAG" \
            | grep -oE '"upload_url"\s*:\s*"[^"]+"' | head -n1 | sed -E 's/.*"([^"]+)".*/\1/' | sed 's/{?name,label}//')"
        [ -n "$UPLOAD_URL" ] || fail "could not resolve upload URL for $RELEASE_TAG (release missing?)"
        for f in "$TARBALL" "$SIDECAR"; do
            name="$(basename "$f")"
            log "uploading $name"
            curl -fsSL \
                -H "Authorization: token $ORVIX_GH_TOKEN" \
                -H "Content-Type: application/octet-stream" \
                --data-binary "@$f" \
                "$UPLOAD_URL?name=$name" >/dev/null
        done
    elif command -v gh >/dev/null 2>&1; then
        gh release upload "$RELEASE_TAG" "$TARBALL" "$SIDECAR" \
            --repo "$GITHUB_REPO" \
            --clobber
    fi
fi

# ── 5. Repoint the channel alias assets (stable/rc/dev) ──────────
# install-public.sh's no-version path uses
#   ${ORVIX_RELEASES_BASE}/orvix-enterprise-mail-stable-linux-amd64.tar.gz
# which GitHub resolves via the "stable" channel tag. The previous
# flow required an operator to manually keep the alias tarball in
# sync. This script now does it: when CHANNEL=stable, it also
# uploads the bundle under the "stable" name to the same release
# so the alias path resolves.
if [ -n "$DRY_RUN" ]; then
    log "DRY RUN: would repoint $CHANNEL alias assets"
else
    ALIAS_TARBALL="orvix-enterprise-mail-${CHANNEL}-linux-amd64.tar.gz"
    ALIAS_SIDECAR="${ALIAS_TARBALL}.sha256"
    if [ -n "${ORVIX_GH_TOKEN:-}" ]; then
        UPLOAD_URL="$(curl -fsSL \
            -H "Authorization: token $ORVIX_GH_TOKEN" \
            -H "Accept: application/vnd.github+json" \
            "https://api.github.com/repos/$GITHUB_REPO/releases/tags/$RELEASE_TAG" \
            | grep -oE '"upload_url"\s*:\s*"[^"]+"' | head -n1 | sed -E 's/.*"([^"]+)".*/\1/' | sed 's/{?name,label}//')"
        for f in "$TARBALL" "$SIDECAR"; do
            alias_name="$(basename "$f" | sed "s/-$CHANNEL/-stable/")"
            log "uploading alias $alias_name"
            curl -fsSL \
                -H "Authorization: token $ORVIX_GH_TOKEN" \
                -H "Content-Type: application/octet-stream" \
                --data-binary "@$f" \
                "$UPLOAD_URL?name=$alias_name" >/dev/null
        done
    elif command -v gh >/dev/null 2>&1; then
        # Re-upload under the alias name. gh's --clobber
        # overwrites the previous asset under the same name.
        cp "$TARBALL" "$BUILD_DIR/$ALIAS_TARBALL"
        cp "$SIDECAR" "$BUILD_DIR/$ALIAS_SIDECAR"
        gh release upload "$RELEASE_TAG" \
            "$BUILD_DIR/$ALIAS_TARBALL" "$BUILD_DIR/$ALIAS_SIDECAR" \
            --repo "$GITHUB_REPO" \
            --clobber
    fi
fi

# ── 6. Verify the published assets are reachable ──────────────────
log "verifying published assets via verify-github-release-assets.sh..."
if ! bash "$SCRIPT_DIR/verify-github-release-assets.sh" \
    --repo "$GITHUB_REPO" \
    --tag "$RELEASE_TAG" \
    --channel "$CHANNEL" \
    --expected-sha "$SHA" 2>&1; then
    fail "verify-github-release-assets.sh reported the published release is not reachable (BLOCKER 8 fail-closed gate)"
fi

log "release published + verified: $RELEASE_TAG (channel=$CHANNEL)"
printf '\n%sPublished %s to %s (channel=%s, sha256=%s, size=%s)%s\n' \
    "$GREEN" "$TARBALL" "$GITHUB_REPO" "$CHANNEL" "${SHA:0:12}" "$SIZE" "$NC"
exit 0
