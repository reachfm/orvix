#!/usr/bin/env bash
# verify-github-release-assets.sh — Fail-closed check of published release.
#
# Proves the assets install-public.sh relies on are actually
# reachable and that the published .sha256 matches the local
# bundle we expect to install. This is the BLOCKER 8 acceptance
# gate; without it a release can be tagged but the assets
# still 404 on the operator's first install attempt.
#
# Checks performed (all must pass; the script exits non-zero on
# the first failure):
#
#   1. `curl -fIL <bundle_url>`           : the .tar.gz asset
#                                            responds 200 with a
#                                            non-empty body.
#   2. `curl -fIL <sidecar_url>`          : the .sha256 sidecar
#                                            responds 200.
#   3. sidecar content == expected sha    : the published sha256
#                                            matches either the
#                                            --expected-sha arg or
#                                            a local file.
#   4. local bundle sha256 == sidecar      : when the local bundle
#                                            is available, its
#                                            sha256 matches the
#                                            published sidecar.
#   5. tag URL resolves                   : the release tag exists
#                                            on GitHub and is not
#                                            a 404.
#
# Usage:
#   bash release/scripts/verify-github-release-assets.sh \
#       --repo reachfm/orvix \
#       --tag v1.0.3-rc5 \
#       --channel stable \
#       --expected-sha 2d5bb8c3015c145e8ffb49b45d9b41ac4962908575e0020f725626093628adeb
#
# Or against the local dist/ directory only:
#   bash release/scripts/verify-github-release-assets.sh --local-only
#
# Exit codes:
#   0  every check passed
#   1  one or more checks failed
#   2  invalid CLI args

set -euo pipefail

REPO_ROOT="${REPO_ROOT:-$(cd "$(dirname "$0")/../.." && pwd)}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BUILD_DIR="${ORVIX_BUILD_DIR:-$REPO_ROOT/dist}"

GITHUB_REPO="${ORVIX_GITHUB_REPO:-reachfm/orvix}"
RELEASE_TAG=""
CHANNEL="stable"
EXPECTED_SHA=""
LOCAL_ONLY=0

usage() {
    cat <<USAGE
verify-github-release-assets.sh — fail-closed check of published release.

Options:
  --repo <slug>         GitHub repo slug (default: reachfm/orvix)
  --tag <tag>           Git tag of the release to verify
  --channel <chan>      Channel alias to verify (default: stable)
  --expected-sha <hex>  Expected sha256 of the bundle
  --local-only          Only verify the local dist/ artifacts (skip GitHub probes)
  -h, --help            Show this message

Environment:
  ORVIX_GITHUB_REPO, ORVIX_RELEASE_TAG, ORVIX_CHANNEL, ORVIX_BUNDLE_SHA256

Exit codes:
  0  every check passed
  1  one or more checks failed (see diagnostics)
  2  invalid CLI args
USAGE
}

while [ $# -gt 0 ]; do
    case "$1" in
        --repo) GITHUB_REPO="$2"; shift 2 ;;
        --tag)  RELEASE_TAG="$2"; shift 2 ;;
        --channel) CHANNEL="$2"; shift 2 ;;
        --expected-sha) EXPECTED_SHA="$2"; shift 2 ;;
        --local-only) LOCAL_ONLY=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *)  printf 'ERROR: unrecognised argument: %s\n' "$1" >&2; usage; exit 2 ;;
    esac
done

[ -n "${ORVIX_RELEASE_TAG:-}" ] && [ -z "$RELEASE_TAG" ] && RELEASE_TAG="$ORVIX_RELEASE_TAG"
[ -n "${ORVIX_CHANNEL:-}" ]      && [ "$CHANNEL" = "stable" ] && CHANNEL="$ORVIX_CHANNEL"
[ -n "${ORVIX_BUNDLE_SHA256:-}" ] && [ -z "$EXPECTED_SHA" ] && EXPECTED_SHA="$ORVIX_BUNDLE_SHA256"

RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
NC=$'\033[0m'

log() { printf '[%s] %s\n' "$(date -Is)" "$*" >&2; }
pass() { printf '%sPASS%s  %s\n' "$GREEN" "$NC" "$*" >&2; }
fail() { printf '%sFAIL%s  %s\n' "$RED" "$NC" "$*" >&2; exit 1; }

# ── 1. Local artifact sanity ─────────────────────────────────────
TARBALL="$BUILD_DIR/orvix-enterprise-mail-${CHANNEL}-linux-amd64.tar.gz"
SIDECAR="$TARBALL.sha256"
LOCAL_SHA=""

if [ -f "$SIDECAR" ]; then
    LOCAL_SHA="$(awk '{print $1}' "$SIDECAR" | tr -d '\r\n')"
    pass "local sidecar present: $SIDECAR (sha256=${LOCAL_SHA:0:12}...)"
else
    log "local sidecar $SIDECAR not found (will rely on --expected-sha and remote check)"
fi

if [ -f "$TARBALL" ]; then
    ACTUAL_LOCAL_SHA="$(sha256sum "$TARBALL" | awk '{print $1}')"
    pass "local bundle present: $TARBALL ($(wc -c < "$TARBALL" | tr -d ' \r\n') bytes, sha256=${ACTUAL_LOCAL_SHA:0:12}...)"
    if [ -n "$LOCAL_SHA" ] && [ "$ACTUAL_LOCAL_SHA" != "$LOCAL_SHA" ]; then
        fail "local bundle sha256 ($ACTUAL_LOCAL_SHA) does not match sidecar ($LOCAL_SHA); sidecar is stale"
    fi
    if [ -n "$ACTUAL_LOCAL_SHA" ] && [ -z "$LOCAL_SHA" ]; then
        LOCAL_SHA="$ACTUAL_LOCAL_SHA"
    fi
    if [ -n "$EXPECTED_SHA" ] && [ "$ACTUAL_LOCAL_SHA" != "$EXPECTED_SHA" ]; then
        fail "local bundle sha256 ($ACTUAL_LOCAL_SHA) does not match --expected-sha ($EXPECTED_SHA)"
    fi
fi

[ -n "$LOCAL_SHA" ] || [ -n "$EXPECTED_SHA" ] \
    || fail "no sha256 available (no local sidecar, no local bundle, no --expected-sha)"

# Prefer the local computed sha for verification.
EFFECTIVE_SHA="${EXPECTED_SHA:-$LOCAL_SHA}"

# ── 2. Local-only mode skips GitHub probes ───────────────────────
if [ "$LOCAL_ONLY" = "1" ]; then
    pass "local-only mode: GitHub probes skipped"
    log "OK — local artifacts verified"
    exit 0
fi

# ── 3. Tag + repo presence ────────────────────────────────────────
[ -n "$RELEASE_TAG" ] || fail "--tag is required (or set ORVIX_RELEASE_TAG)"
command -v curl >/dev/null 2>&1 || fail "curl is required for the remote probes"

TAG_URL="https://github.com/$GITHUB_REPO/releases/tag/$RELEASE_TAG"
log "probing release tag URL: $TAG_URL"
if ! curl -fsI --max-time 30 "$TAG_URL" >/dev/null 2>&1; then
    fail "release tag $RELEASE_TAG does not resolve on $GITHUB_REPO (URL $TAG_URL)"
fi
pass "release tag $RELEASE_TAG resolves on $GITHUB_REPO"

# ── 4. Bundle asset reachable ────────────────────────────────────
BUNDLE_URL="https://github.com/$GITHUB_REPO/releases/download/$RELEASE_TAG/orvix-enterprise-mail-${CHANNEL}-linux-amd64.tar.gz"
log "probing bundle URL: $BUNDLE_URL"
# Use -fIL: -f fails on 4xx/5xx, -I issues HEAD, -L follows redirects.
# The first redirect goes to GitHub's S3-backed release-assets bucket;
# the final response must be 200.
HEAD_CODE="$(curl -s -o /dev/null -w '%{http_code}' -I -L --max-time 60 "$BUNDLE_URL" || echo "000")"
if [ "$HEAD_CODE" != "200" ]; then
    fail "bundle URL did not return HTTP 200 (got $HEAD_CODE): $BUNDLE_URL"
fi
pass "bundle URL returns HTTP 200: $BUNDLE_URL"

# ── 5. sha256 sidecar reachable ──────────────────────────────────
SIDECAR_URL="$BUNDLE_URL.sha256"
log "probing sidecar URL: $SIDECAR_URL"
HEAD_CODE="$(curl -s -o /dev/null -w '%{http_code}' -I -L --max-time 60 "$SIDECAR_URL" || echo "000")"
if [ "$HEAD_CODE" != "200" ]; then
    fail "sidecar URL did not return HTTP 200 (got $HEAD_CODE): $SIDECAR_URL"
fi
pass "sidecar URL returns HTTP 200: $SIDECAR_URL"

# ── 6. Sidecar content matches expected sha ──────────────────────
SIDECAR_BODY="$(mktemp)"
trap 'rm -f "$SIDECAR_BODY"' EXIT
if ! curl -fsSL --max-time 30 "$SIDECAR_URL" -o "$SIDECAR_BODY"; then
    fail "could not download sidecar body: $SIDECAR_URL"
fi
PUBLISHED_SHA="$(awk '{print $1}' "$SIDECAR_BODY" | tr -d '\r\n' | head -n1)"
if [ -z "$PUBLISHED_SHA" ]; then
    fail "sidecar body is empty or malformed: $SIDECAR_BODY"
fi
if [ "$PUBLISHED_SHA" != "$EFFECTIVE_SHA" ]; then
    fail "published sha256 ($PUBLISHED_SHA) does not match expected ($EFFECTIVE_SHA) (BLOCKER 8: install-public.sh would refuse this bundle)"
fi
pass "published sha256 matches expected: ${PUBLISHED_SHA:0:12}..."

# ── 7. Downloaded bundle byte-for-byte matches ───────────────────
DOWNLOAD_TMP="$(mktemp)"
trap 'rm -f "$SIDECAR_BODY" "$DOWNLOAD_TMP"' EXIT
if ! curl -fsSL --max-time 300 "$BUNDLE_URL" -o "$DOWNLOAD_TMP"; then
    fail "could not download bundle: $BUNDLE_URL"
fi
DOWNLOAD_SHA="$(sha256sum "$DOWNLOAD_TMP" | awk '{print $1}')"
if [ "$DOWNLOAD_SHA" != "$EFFECTIVE_SHA" ]; then
    fail "downloaded bundle sha256 ($DOWNLOAD_SHA) does not match expected ($EFFECTIVE_SHA)"
fi
pass "downloaded bundle sha256 matches expected: ${DOWNLOAD_SHA:0:12}..."

# ── 8. Re-extract smoke — bundle must contain install-public.sh ──
EXTRACT_DIR="$(mktemp -d)"
trap 'rm -rf "$SIDECAR_BODY" "$DOWNLOAD_TMP" "$EXTRACT_DIR"' EXIT
if ! tar -xzf "$DOWNLOAD_TMP" -C "$EXTRACT_DIR" --strip-components=0 2>/dev/null; then
    fail "downloaded bundle is not a valid tar.gz"
fi
# Bundles are sealed with a top-level "orvix/" directory; the
# path inside may also be the bare repo (--strip-components=1).
if [ -d "$EXTRACT_DIR/orvix" ]; then
    ROOT="$EXTRACT_DIR/orvix"
else
    ROOT="$EXTRACT_DIR"
fi
[ -f "$ROOT/release/install-public.sh" ] || fail "downloaded bundle is missing release/install-public.sh (republish required)"
[ -f "$ROOT/BUILDINFO" ] || fail "downloaded bundle is missing BUILDINFO (republish required)"
[ -f "$ROOT/bin/orvix" ] || fail "downloaded bundle is missing bin/orvix (republish required)"
pass "downloaded bundle re-extracts cleanly and contains install-public.sh + BUILDINFO + bin/orvix"

log "OK — release assets verified end-to-end"
exit 0
