#!/usr/bin/env bash
set -euo pipefail

# Orvix Enterprise Mail — public one-line installer entrypoint.
#
# This script is the SINGLE entrypoint operators run from the public
# release host. It is responsible for:
#
#   1. Resolving which Orvix release to install (version, commit,
#      channel, bundle URL, or GitHub ref).
#   2. Downloading the FULL release bundle — never just install.sh —
#      because install.sh requires the admin / webmail / systemd /
#      sudoers / scripts / configs assets that used to be silently
#      expected to come from a developer worktree.
#   3. Validating the bundle before handing control to install.sh.
#      If the bundle is incomplete, the installer fails closed with
#      a clear list of the missing files rather than reaching
#      install.sh and discovering the problem halfway through the
#      install.
#   4. Verifying the bundle cryptographically before install:
#      SHA-256, the Ed25519 bundle signature, the signed release
#      manifest, and the signed SBOM — plus the embedded BUILDINFO
#      and the prebuilt binary's embedded version/commit/channel.
#      ANY checksum / signature / commit / version mismatch aborts
#      before anything is installed.
#   5. Delegating to install.sh with ORVIX_SOURCE_DIR pointing at the
#      extracted bundle.
#
# Usage:
#   curl -fsSLo /root/install-orvix.sh \
#     https://raw.githubusercontent.com/reachfm/orvix/v1.0.4-rc2/release/install-public.sh
#   chmod 700 /root/install-orvix.sh
#   ORVIX_VERSION=1.0.4-rc2 bash /root/install-orvix.sh
#
#   (Always save the script to a file first and run the file: the
#   installer prompts read from /dev/tty, so a curl-pipe install
#   cannot read the administrator password from the terminal.)
#
# Resolution order (first wins):
#   1. --bundle-url <url>            explicit tarball (escape hatch for air-gap)
#   2. ORVIX_BUNDLE_URL              same, via env var
#   3. --github-ref / ORVIX_GITHUB_REF
#                                    archives the matching tag/branch/commit
#                                    via codeload tarball (dev/test ONLY:
#                                    source archives are unsigned)
#   4. --version / ORVIX_VERSION     downloads the EXACT release version from
#      its immutable release tag:
#        https://github.com/reachfm/orvix/releases/download/v<ver>/...
#      Explicit versions — including prereleases — NEVER resolve through
#      releases/latest/download.
#   5. (default)                     installs the latest stable release from
#                                    GitHub Releases (reachfm/orvix) via the
#                                    channel alias asset.
#
# The installer prefers bundles over GitHub archives because:
#   * A bundle is the audited, signed artifact (sha256 sidecar +
#     Ed25519 signature + signed manifest + signed SBOM).
#   * A GitHub source archive is whatever happens to be on the named
#     ref right now — useful for dev/test, never for production.
#   * A bundle contains only the runtime assets; a GitHub archive
#     contains the entire repo (CI scaffolding, tests, docs).
#
# Environment variables (full list):
#   ORVIX_PRIMARY_DOMAIN    Primary mail domain
#   ORVIX_DOMAIN            Backward-compatible primary mail domain alias
#   ORVIX_PUBLIC_IPV4       Public IPv4 of this host
#   ORVIX_ADMIN_EMAIL       Admin email address
#   ORVIX_ADMIN_PASSWORD    Admin password (8-72 bytes)
#   ORVIX_ADMIN_PASSWORD_B64
#                           Base64 admin password alternative
#   ORVIX_SETUP_HTTPS       If set, invoke setup-https.sh after install
#   ORVIX_HARDEN_FIREWALL   If set, run firewall hardening after install
#   ORVIX_NON_INTERACTIVE   If set, require ORVIX_DOMAIN + ORVIX_PUBLIC_IPV4
#                           and run without any prompts.
#
#   ORVIX_VERSION           Exact release version (e.g. 1.0.4-rc2).
#                           Resolves to the immutable release tag
#                           v<version>. Default: stable alias.
#   ORVIX_CHANNEL           Release channel used for the default
#                           (no-version) resolution (stable, rc, dev).
#                           Default: stable.
#   ORVIX_COMMIT            Expected commit SHA (used to verify the
#                           bundle's binary against the expected one).
#   ORVIX_BUNDLE_URL        Direct override of the bundle download URL.
#   ORVIX_BUNDLE_SHA256     Expected sha256 of the bundle (optional but
#                           recommended — checks against the bundle
#                           sha256 sidecar when supplied).
#   ORVIX_GITHUB_REPO       GitHub repo slug (default: reachfm/orvix).
#   ORVIX_GITHUB_REF        Git ref (tag, branch, commit) for a GitHub
#                           source archive. Behaves EXACTLY like
#                           --github-ref <ref> (dev/test only).
#   ORVIX_GITHUB_BASE       Codeload base URL override (testing only).
#   ORVIX_RELEASES_BASE     Base URL for the default stable/channel
#                           alias resolution (default: GitHub latest).
#   ORVIX_RELEASE_DOWNLOAD_BASE
#                           Base URL for exact-version release tags
#                           (default: https://github.com/reachfm/orvix/releases/download).
#   ORVIX_SKIP_BUNDLE_VERIFY Disable bundle sha256/signature/sidecar
#                           verification (NOT recommended; only for
#                           local air-gapped test rigs).
#   ORVIX_REQUIRE_RELEASE_SIGNATURE
#                           Require detached .sig verification for release
#                           bundles + signed manifest + signed SBOM.
#                           Default: 1.
#   ORVIX_RELEASE_VERIFYING_KEY_FILE
#                           Optional path to a trusted Ed25519 public key.
#

ORVIX_DOCS_URL="${ORVIX_DOCS_URL:-https://docs.orvix.email}"
ORVIX_RELEASES_BASE="${ORVIX_RELEASES_BASE:-https://github.com/reachfm/orvix/releases/latest/download}"
ORVIX_RELEASE_DOWNLOAD_BASE="${ORVIX_RELEASE_DOWNLOAD_BASE:-https://github.com/reachfm/orvix/releases/download}"
ORVIX_GITHUB_REPO="${ORVIX_GITHUB_REPO:-reachfm/orvix}"
ORVIX_GITHUB_BASE="${ORVIX_GITHUB_BASE:-https://codeload.github.com}"
ORVIX_CHANNEL="${ORVIX_CHANNEL:-stable}"
ORVIX_VERSION="${ORVIX_VERSION:-}"
ORVIX_COMMIT="${ORVIX_COMMIT:-}"
ORVIX_BUNDLE_URL="${ORVIX_BUNDLE_URL:-}"
ORVIX_BUNDLE_SHA256="${ORVIX_BUNDLE_SHA256:-}"
ORVIX_GITHUB_REF="${ORVIX_GITHUB_REF:-}"
ORVIX_SKIP_BUNDLE_VERIFY="${ORVIX_SKIP_BUNDLE_VERIFY:-}"
ORVIX_REQUIRE_RELEASE_SIGNATURE="${ORVIX_REQUIRE_RELEASE_SIGNATURE:-1}"
ORVIX_RELEASE_VERIFYING_KEY_FILE="${ORVIX_RELEASE_VERIFYING_KEY_FILE:-}"

INSTALLER_VERSION="3.1.0"

RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
YELLOW=$'\033[1;33m'
NC=$'\033[0m'

usage() {
    cat <<USAGE
Orvix Enterprise Mail — public installer entrypoint

Usage:
  curl -fsSLo /root/install-orvix.sh \\
    https://raw.githubusercontent.com/reachfm/orvix/v1.0.4-rc2/release/install-public.sh
  chmod 700 /root/install-orvix.sh
  ORVIX_VERSION=1.0.4-rc2 bash /root/install-orvix.sh

Non-interactive:
  ORVIX_PRIMARY_DOMAIN=example.com \
  ORVIX_ADMIN_EMAIL=admin@example.com \
  ORVIX_ADMIN_PASSWORD='strong-password' \
  bash /root/install-orvix.sh

Resolution modes (first match wins):
  --bundle-url <url>            Install from a specific bundle tarball URL
                                (escape hatch for air-gapped prod).
  --github-ref <ref>            Install from a GitHub SOURCE ARCHIVE instead
                                of a signed bundle (dev/test only; source
                                archives are unsigned and unverified).
  --version <semver>            Install the exact release <semver> (e.g.
                                1.0.4-rc2) from its immutable release tag:
                                ${ORVIX_RELEASE_DOWNLOAD_BASE}/v<semver>/orvix-enterprise-mail-<semver>-linux-amd64.tar.gz
  --channel <chan>              Use <chan> for the default URL when no
                                --version is supplied (default: stable).

Environment variables:
  ORVIX_PRIMARY_DOMAIN          Primary mail domain (required in non-interactive mode)
  ORVIX_DOMAIN                  Backward-compatible primary mail domain alias
  ORVIX_PUBLIC_IPV4              Public IPv4 (required in non-interactive mode)
  ORVIX_ADMIN_EMAIL              Admin email (optional; prompted if unset)
  ORVIX_ADMIN_PASSWORD           Admin password (optional; prompted if unset)
  ORVIX_ADMIN_PASSWORD_B64       Base64 admin password alternative
  ORVIX_SETUP_HTTPS              Run HTTPS setup after install
  ORVIX_HARDEN_FIREWALL          Run firewall hardening after install
  ORVIX_NON_INTERACTIVE          Non-interactive mode
  ORVIX_COMMIT                   Expected commit SHA — install.sh verifies it
  ORVIX_BUNDLE_URL               Direct bundle URL override
  ORVIX_BUNDLE_SHA256            Expected sha256 of the bundle
  ORVIX_GITHUB_REPO              GitHub repo slug (default: reachfm/orvix)
  ORVIX_GITHUB_REF               GitHub ref to install from a source archive
                                 (same as --github-ref; dev/test only)
  ORVIX_SKIP_BUNDLE_VERIFY       Skip sha256/signature verification (NOT recommended)
  ORVIX_REQUIRE_RELEASE_SIGNATURE Require .sig verification for bundles (default 1)
  ORVIX_RELEASE_VERIFYING_KEY_FILE Trusted Ed25519 public key override

Flags:
  --help, -h                    Show this message
  --installer-version, -V       Show version info for the installer script
                                (use --version <semver> to select a release)

Docs: $ORVIX_DOCS_URL
USAGE
}

# ── Validation helpers ─────────────────────────────────────────────

is_valid_public_ipv4() {
    local ip="${1:-}"
    [ -n "$ip" ] || return 1
    if ! [[ "$ip" =~ ^((25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])\.){3}(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])$ ]]; then
        return 1
    fi
    local o1 o2 o3 o4
    IFS=. read -r o1 o2 o3 o4 <<< "$ip"
    [ "$o1" -eq 0 ]   && return 1
    [ "$o1" -eq 10 ]  && return 1
    if [ "$o1" -eq 100 ] && [ "$o2" -ge 64 ] && [ "$o2" -le 127 ]; then return 1; fi
    [ "$o1" -eq 127 ] && return 1
    if [ "$o1" -eq 169 ] && [ "$o2" -eq 254 ]; then return 1; fi
    if [ "$o1" -eq 172 ] && [ "$o2" -ge 16 ] && [ "$o2" -le 31 ]; then return 1; fi
    if [ "$o1" -eq 192 ] && [ "$o2" -eq 0 ] && [ "$o3" -eq 0 ]; then return 1; fi
    if [ "$o1" -eq 192 ] && [ "$o2" -eq 0 ] && [ "$o3" -eq 2 ]; then return 1; fi
    [ "$o1" -eq 192 ] && [ "$o2" -eq 168 ] && return 1
    if [ "$o1" -eq 198 ] && { [ "$o2" -eq 18 ] || [ "$o2" -eq 19 ]; }; then return 1; fi
    if [ "$o1" -eq 198 ] && [ "$o2" -eq 51 ] && [ "$o3" -eq 100 ]; then return 1; fi
    if [ "$o1" -eq 203 ] && [ "$o2" -eq 0 ] && [ "$o3" -eq 113 ]; then return 1; fi
    if [ "$o1" -ge 224 ] && [ "$o1" -le 239 ]; then return 1; fi
    [ "$o1" -ge 240 ] && return 1
    return 0
}

fail() {
    printf '%bERROR:%b %s\n' "$RED" "$NC" "$*" >&2
    exit 1
}

warn() {
    printf '%bWARN:%b %s\n' "$YELLOW" "$NC" "$*" >&2
}

info() {
    printf '%bINFO:%b %s\n' "$GREEN" "$NC" "$*" >&2
}

# ── Temporary-file hygiene ─────────────────────────────────────────
# Every temp file/dir the installer creates is registered here and
# removed on EVERY exit path (success or failure). Nothing is ever
# left behind in /tmp on a failed install.
ORVIX_TMP_FILES=()

cleanup_tmp() {
    local entry
    for entry in "${ORVIX_TMP_FILES[@]:-}"; do
        [ -n "$entry" ] || continue
        rm -rf "$entry" 2>/dev/null || true
    done
}
trap cleanup_tmp EXIT

# ── Detect a developer worktree (for CI / dev only) ───────────────

detect_worktree() {
    local d
    d="$(pwd)"
    while [ -n "$d" ] && [ "$d" != "/" ]; do
        if [ -f "$d/release/install.sh" ] && [ -f "$d/go.mod" ]; then
            if grep -q 'module github.com/orvix/orvix' "$d/go.mod" 2>/dev/null; then
                printf '%s' "$d"
                return 0
            fi
        fi
        d="$(dirname "$d")"
    done
    return 1
}

# ── Bundle resolution ──────────────────────────────────────────────

# resolve_bundle_url picks the bundle URL + sha256 to use, based on
# flags + env vars. Echoes "<url>" on stdout, "<sha>" in $ORVIX_RESOLVED_SHA.
#
# Exact versions (including prereleases such as 1.0.4-rc2) ALWAYS
# resolve to their immutable release tag:
#   ${ORVIX_RELEASE_DOWNLOAD_BASE}/v<version>/orvix-enterprise-mail-<version>-linux-amd64.tar.gz
# The releases/latest/download mechanism is used ONLY for the default
# stable / channel-alias resolution.
resolve_bundle_url() {
    if [ -n "$ORVIX_BUNDLE_URL" ]; then
        printf '%s\n' "$ORVIX_BUNDLE_URL"
        ORVIX_RESOLVED_SHA="$ORVIX_BUNDLE_SHA256"
        return 0
    fi
    if [ -n "$ORVIX_VERSION" ]; then
        if [ "$ORVIX_VERSION" = "stable" ]; then
            # Backward-compatible stable alias via the latest-release
            # mechanism (the build pipeline publishes the alias asset).
            printf '%s/orvix-enterprise-mail-stable-linux-amd64.tar.gz\n' "$ORVIX_RELEASES_BASE"
        else
            # Exact immutable tag — never releases/latest/download.
            printf '%s/v%s/orvix-enterprise-mail-%s-linux-amd64.tar.gz\n' \
                "$ORVIX_RELEASE_DOWNLOAD_BASE" "$ORVIX_VERSION" "$ORVIX_VERSION"
        fi
        ORVIX_RESOLVED_SHA=""
        return 0
    fi
    # Default: channel alias of the latest release.
    printf '%s/orvix-enterprise-mail-%s-linux-amd64.tar.gz\n' "$ORVIX_RELEASES_BASE" "$ORVIX_CHANNEL"
    ORVIX_RESOLVED_SHA=""
    return 0
}

# resolve_github_archive_url echoes the codeload URL for the
# supplied ref. Used when --github-ref / ORVIX_GITHUB_REF is set.
resolve_github_archive_url() {
    local ref="$1"
    printf '%s/%s/tar.gz/%s\n' "$ORVIX_GITHUB_BASE" "$ORVIX_GITHUB_REPO" "$ref"
}

# download_bundle_or_archive fetches $1 to stdout's caller via a
# tmpfile. Echoes the LOCAL path of the downloaded bundle. The
# caller is responsible for cleanup.
download_to_tmp() {
    local url="$1"
    local label="$2"
    local tmp
    tmp="$(mktemp /tmp/orvix-bundle.XXXXXX.tar.gz)"
    ORVIX_TMP_FILES+=("$tmp")
    info "downloading $label from $url"
    if ! curl -fsSL --retry 3 --max-time 600 -o "$tmp" "$url"; then
        fail "could not download $label from $url (check connectivity; the URL must be reachable from this host)"
    fi
    # Sanity: tarball should be at least a few KB and not the HTML
    # error page that some CDNs return.
    local size
    size="$(wc -c < "$tmp" 2>/dev/null || echo 0)"
    if [ "$size" -lt 1024 ]; then
        fail "downloaded artifact from $url is only $size bytes (this is usually an HTML error page, not the bundle)"
    fi
    printf '%s\n' "$tmp"
}

# verify_bundle_sha256 checks $1 against the expected sha256 in $2
# when $2 is non-empty. Skips silently when $2 is empty. Errors loud
# on mismatch. Prints the ACTUAL sha256 via $ORVIX_ACTUAL_SHA so
# callers can cross-check it against the signed release manifest.
verify_bundle_sha256() {
    local path="$1"
    local expected="$2"
    local actual
    actual="$(sha256sum "$path" | awk '{print $1}')"
    ORVIX_ACTUAL_SHA="$actual"
    if [ -z "$expected" ]; then
        warn "no bundle sha256 supplied (set ORVIX_BUNDLE_SHA256 or --bundle-url with checksums.txt for production)"
        return 0
    fi
    if [ "$actual" != "$expected" ]; then
        fail "bundle sha256 mismatch (expected $expected, got $actual)"
    fi
    info "bundle sha256 verified: $actual"
}

trusted_release_key_file() {
    if [ -n "$ORVIX_RELEASE_VERIFYING_KEY_FILE" ]; then
        [ -f "$ORVIX_RELEASE_VERIFYING_KEY_FILE" ] || fail "release verifying key not found: $ORVIX_RELEASE_VERIFYING_KEY_FILE"
        printf '%s\n' "$ORVIX_RELEASE_VERIFYING_KEY_FILE"
        return 0
    fi
    local tmp
    tmp="$(mktemp /tmp/orvix-release-key.XXXXXX.pem)"
    ORVIX_TMP_FILES+=("$tmp")
    # The embedded release signing public key. MUST stay in sync with
    # release/trust/orvix-release-signing.pub.pem (see the regression
    # test release/scripts/tests/test-install-public.sh). This key is
    # what the release-bundle.yml workflow signs every bundle with.
    cat >"$tmp" <<'KEY'
-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAtDa30+3Lpsa7YhNmf4fDiiGr6fFJh3fSki0ZiMFGEe4=
-----END PUBLIC KEY-----
KEY
    printf '%s\n' "$tmp"
}

verify_bundle_signature() {
    local bundle_url="$1"
    local bundle_path="$2"
    if [ "$ORVIX_REQUIRE_RELEASE_SIGNATURE" != "1" ]; then
        warn "release signature verification disabled by ORVIX_REQUIRE_RELEASE_SIGNATURE=$ORVIX_REQUIRE_RELEASE_SIGNATURE"
        return 0
    fi
    command -v openssl >/dev/null 2>&1 || fail "openssl is required for release signature verification"
    local sig_url="${bundle_url}.sig"
    local sig_file key_file
    sig_file="$(mktemp /tmp/orvix-bundle.XXXXXX.sig)"
    ORVIX_TMP_FILES+=("$sig_file")
    if ! curl -fsSL --max-time 30 -o "$sig_file" "$sig_url"; then
        fail "cannot verify bundle authenticity: signature sidecar not found at $sig_url"
    fi
    key_file="$(trusted_release_key_file)"
    if ! openssl pkeyutl -verify -rawin -pubin -inkey "$key_file" -in "$bundle_path" -sigfile "$sig_file" >/dev/null 2>&1; then
        fail "bundle signature verification failed"
    fi
    info "bundle signature verified"
}

# try_download_sha256 attempts to download a .sha256 sidecar for the
# given bundle URL. On success it prints the sha256 hash and returns 0.
# On failure (missing sidecar, network error) it returns 1 silently.
try_download_sha256() {
    local bundle_url="$1"
    local sha_url="${bundle_url}.sha256"
    local tmp
    tmp="$(mktemp /tmp/orvix-sha256.XXXXXX)"
    if curl -fsSL --max-time 15 -o "$tmp" "$sha_url" 2>/dev/null; then
        local hash
        hash="$(awk '{print $1}' "$tmp" 2>/dev/null || true)"
        rm -f "$tmp"
        if [ -n "$hash" ]; then
            printf '%s' "$hash"
            return 0
        fi
    else
        rm -f "$tmp"
    fi
    return 1
}

# manifest_field extracts a string field from the release manifest JSON.
# All manifest values are strings; pure grep/sed keeps the installer
# free of a jq dependency on the target host.
manifest_field() {
    local file="$1" key="$2"
    grep -o "\"$key\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" "$file" 2>/dev/null \
        | head -n1 | sed -E 's/^[^:]*:[[:space:]]*"([^"]*)"/\1/'
}

# verify_release_sidecars downloads the signed release manifest and
# the signed SBOM for a remote bundle and verifies:
#   - manifest Ed25519 signature
#   - manifest artifact_sha256 == sha256 of the downloaded bundle
#   - manifest artifact name == downloaded bundle name
#   - manifest target == linux/amd64
#   - SBOM Ed25519 signature + non-empty SPDX document
# The parsed manifest fields are stored in ORVIX_MANIFEST_* globals for
# the post-extraction identity cross-check (verify_bundle_identity).
verify_release_sidecars() {
    local bundle_url="$1"
    local bundle_path="$2"
    if [ "$ORVIX_REQUIRE_RELEASE_SIGNATURE" != "1" ]; then
        warn "release manifest/SBOM verification disabled by ORVIX_REQUIRE_RELEASE_SIGNATURE=$ORVIX_REQUIRE_RELEASE_SIGNATURE"
        return 0
    fi
    command -v openssl >/dev/null 2>&1 || fail "openssl is required for release manifest/SBOM verification"

    local actual_sha
    actual_sha="$(sha256sum "$bundle_path" | awk '{print $1}')"

    local manifest_file manifest_sig sbom_file sbom_sig key_file
    manifest_file="$(mktemp /tmp/orvix-manifest.XXXXXX.json)"
    ORVIX_TMP_FILES+=("$manifest_file")
    manifest_sig="$(mktemp /tmp/orvix-manifest.XXXXXX.sig)"
    ORVIX_TMP_FILES+=("$manifest_sig")
    if ! curl -fsSL --max-time 30 -o "$manifest_file" "${bundle_url}.manifest.json"; then
        fail "cannot verify release manifest: ${bundle_url}.manifest.json is not reachable"
    fi
    if ! curl -fsSL --max-time 30 -o "$manifest_sig" "${bundle_url}.manifest.json.sig"; then
        fail "cannot verify release manifest: signature sidecar ${bundle_url}.manifest.json.sig is missing"
    fi
    key_file="$(trusted_release_key_file)"
    if ! openssl pkeyutl -verify -rawin -pubin -inkey "$key_file" \
        -in "$manifest_file" -sigfile "$manifest_sig" >/dev/null 2>&1; then
        fail "release manifest signature verification failed"
    fi

    local m_sha m_version m_commit m_channel m_target m_artifact url_base
    m_sha="$(manifest_field "$manifest_file" artifact_sha256)"
    m_version="$(manifest_field "$manifest_file" version)"
    m_commit="$(manifest_field "$manifest_file" commit)"
    m_channel="$(manifest_field "$manifest_file" channel)"
    m_target="$(manifest_field "$manifest_file" target)"
    m_artifact="$(manifest_field "$manifest_file" artifact)"
    [ -n "$m_sha" ]      || fail "release manifest is missing artifact_sha256"
    [ -n "$m_version" ]  || fail "release manifest is missing version"
    [ -n "$m_commit" ]   || fail "release manifest is missing commit"
    [ -n "$m_channel" ]  || fail "release manifest is missing channel"
    [ -n "$m_target" ]   || fail "release manifest is missing target"
    [ -n "$m_artifact" ] || fail "release manifest is missing artifact"
    [ "$m_sha" = "$actual_sha" ] \
        || fail "release manifest artifact_sha256 ($m_sha) does not match the downloaded bundle ($actual_sha)"
    url_base="$(basename "$bundle_url")"
    [ "$m_artifact" = "$url_base" ] \
        || fail "release manifest artifact ($m_artifact) does not match the downloaded bundle name ($url_base)"
    [ "$m_target" = "linux/amd64" ] \
        || fail "release manifest target $m_target is not linux/amd64 (this installer only supports linux/amd64 bundles)"
    ORVIX_MANIFEST_SHA="$m_sha"
    ORVIX_MANIFEST_VERSION="$m_version"
    ORVIX_MANIFEST_COMMIT="$m_commit"
    ORVIX_MANIFEST_CHANNEL="$m_channel"
    ORVIX_MANIFEST_TARGET="$m_target"
    info "release manifest verified: version=$m_version commit=${m_commit:0:12} channel=$m_channel target=$m_target"

    sbom_file="$(mktemp /tmp/orvix-sbom.XXXXXX.spdx)"
    ORVIX_TMP_FILES+=("$sbom_file")
    sbom_sig="$(mktemp /tmp/orvix-sbom.XXXXXX.sig)"
    ORVIX_TMP_FILES+=("$sbom_sig")
    if ! curl -fsSL --max-time 30 -o "$sbom_file" "${bundle_url}.sbom.spdx"; then
        fail "cannot verify release SBOM: ${bundle_url}.sbom.spdx is not reachable"
    fi
    if ! curl -fsSL --max-time 30 -o "$sbom_sig" "${bundle_url}.sbom.spdx.sig"; then
        fail "cannot verify release SBOM: signature sidecar ${bundle_url}.sbom.spdx.sig is missing"
    fi
    if ! openssl pkeyutl -verify -rawin -pubin -inkey "$key_file" \
        -in "$sbom_file" -sigfile "$sbom_sig" >/dev/null 2>&1; then
        fail "release SBOM signature verification failed"
    fi
    local sbom_size
    sbom_size="$(wc -c < "$sbom_file" 2>/dev/null || echo 0)"
    [ "$sbom_size" -ge 200 ] || fail "release SBOM is empty or truncated ($sbom_size bytes)"
    grep -q 'SPDXVersion:' "$sbom_file" || fail "release SBOM is not a valid SPDX document"
    info "release SBOM verified ($sbom_size bytes)"
}

# verify_bundle_binary_metadata runs the prebuilt binary's
# `version --full` and cross-checks its embedded version / commit /
# channel against BUILDINFO. ANY mismatch fails the install before
# anything is installed. A binary that cannot be executed on this host
# (cross-arch fixture) is left to install.sh's own enforcement.
verify_bundle_binary_metadata() {
    local root="$1"
    local bin="$root/bin/orvix"
    [ -x "$bin" ] || fail "bundle binary $bin is not executable; refusing to install"
    local out
    if ! out="$("$bin" version --full 2>/dev/null)"; then
        warn "could not execute $bin version --full on this host; install.sh will enforce embedded metadata at install time"
        return 0
    fi
    local got_version got_commit got_channel
    got_version="$(printf '%s\n' "$out" | awk 'NR==1 && $1=="orvix" {print $2; exit}')"
    got_commit="$(printf '%s\n' "$out" | awk -F': *' '/^[[:space:]]*commit:/{print $2; exit}')"
    got_channel="$(printf '%s\n' "$out" | awk -F': *' '/^[[:space:]]*channel:/{print $2; exit}')"
    got_version="${got_version//[$'\r\n ']/}"
    got_commit="${got_commit//[$'\r\n ']/}"
    got_channel="${got_channel//[$'\r\n ']/}"
    [ -n "$got_commit" ] || fail "bundle binary reports no commit metadata (unsafe build; refusing to install)"

    local exp_version exp_commit exp_channel
    exp_version="$(awk -F= '/^version=/ {print $2; exit}' "$root/BUILDINFO" || true)"
    exp_commit="$(awk -F= '/^commit=/ {print $2; exit}' "$root/BUILDINFO" || true)"
    exp_channel="$(awk -F= '/^channel=/ {print $2; exit}' "$root/BUILDINFO" || true)"
    [ "$got_commit" = "$exp_commit" ] \
        || fail "bundle binary embeds commit=$got_commit but BUILDINFO says $exp_commit (stale or tampered bundle; refusing to install)"
    [ "$got_channel" = "$exp_channel" ] \
        || fail "bundle binary embeds channel=$got_channel but BUILDINFO says $exp_channel (refusing to install)"
    [ "$got_version" = "$exp_version" ] \
        || fail "bundle binary embeds version=$got_version but BUILDINFO says $exp_version (refusing to install)"
    info "bundle binary metadata verified: version=$got_version commit=$got_commit channel=$got_channel"
}

# verify_bundle_identity cross-checks the extracted bundle's VERSION
# and BUILDINFO files against each other and against the signed
# release manifest downloaded earlier, then verifies the prebuilt
# binary's embedded metadata and the trust key shipped in the bundle.
verify_bundle_identity() {
    local root="$1"
    [ -f "$root/VERSION" ] || fail "bundle is missing VERSION"
    [ -f "$root/BUILDINFO" ] || fail "bundle is missing BUILDINFO"
    local file_version
    file_version="$(tr -d '[:space:]' < "$root/VERSION")"
    local bi_version bi_commit bi_channel bi_target_os bi_target_arch
    bi_version="$(awk -F= '/^version=/ {print $2; exit}' "$root/BUILDINFO" || true)"
    bi_commit="$(awk -F= '/^commit=/ {print $2; exit}' "$root/BUILDINFO" || true)"
    bi_channel="$(awk -F= '/^channel=/ {print $2; exit}' "$root/BUILDINFO" || true)"
    bi_target_os="$(awk -F= '/^target_os=/ {print $2; exit}' "$root/BUILDINFO" || true)"
    bi_target_arch="$(awk -F= '/^target_arch=/ {print $2; exit}' "$root/BUILDINFO" || true)"
    [ -n "$bi_version" ] || fail "BUILDINFO is missing version"
    [ -n "$bi_commit" ]  || fail "BUILDINFO is missing commit"
    [ -n "$bi_channel" ] || fail "BUILDINFO is missing channel"

    if [ -n "$file_version" ] && [ "$file_version" != "$bi_version" ]; then
        fail "VERSION ($file_version) disagrees with BUILDINFO version ($bi_version); refusing to install"
    fi
    if [ -n "${ORVIX_MANIFEST_VERSION:-}" ]; then
        [ "$ORVIX_MANIFEST_VERSION" = "$bi_version" ] \
            || fail "release manifest version ($ORVIX_MANIFEST_VERSION) disagrees with BUILDINFO ($bi_version); refusing to install"
        [ "$ORVIX_MANIFEST_COMMIT" = "$bi_commit" ] \
            || fail "release manifest commit ($ORVIX_MANIFEST_COMMIT) disagrees with BUILDINFO ($bi_commit); refusing to install"
        [ "$ORVIX_MANIFEST_CHANNEL" = "$bi_channel" ] \
            || fail "release manifest channel ($ORVIX_MANIFEST_CHANNEL) disagrees with BUILDINFO ($bi_channel); refusing to install"
        if [ -n "$ORVIX_MANIFEST_TARGET" ] && [ -n "$bi_target_os" ] && [ -n "$bi_target_arch" ]; then
            [ "$ORVIX_MANIFEST_TARGET" = "$bi_target_os/$bi_target_arch" ] \
                || fail "release manifest target ($ORVIX_MANIFEST_TARGET) disagrees with BUILDINFO ($bi_target_os/$bi_target_arch); refusing to install"
        fi
    fi
    info "bundle identity verified: version=$bi_version commit=$bi_commit channel=$bi_channel"

    # Trust chain: when no explicit key file is supplied, the key the
    # installer embeds must match the public key shipped inside the
    # bundle, otherwise the trust chain is broken — refuse to install.
    if [ -z "$ORVIX_RELEASE_VERIFYING_KEY_FILE" ] && [ -f "$root/release/trust/orvix-release-signing.pub.pem" ]; then
        local embedded bundle_key
        embedded="$(trusted_release_key_file)"
        bundle_key="$root/release/trust/orvix-release-signing.pub.pem"
        if ! cmp -s "$embedded" "$bundle_key"; then
            fail "bundle trust key does not match the installer's embedded release signing key; refusing to install"
        fi
        info "bundle trust key matches the installer's embedded release signing key"
    fi

    verify_bundle_binary_metadata "$root"
}

# validate_bundle_layout enforces that the extracted bundle at $1
# contains every file install.sh needs. We refuse to hand control to
# install.sh when any required file is missing — the installer must
# never silently fall back to building on the operator's host.
#
# Required files mirror the build contract from build-release-bundle.sh:
#   - bin/orvix (the verified binary)
#   - VERSION, BUILDINFO, checksums.txt, SBOM.spdx
#   - release/install.sh, release/upgrade.sh, release/uninstall.sh
#   - release/install-public.sh (so re-runs can self-resolve)
#   - release/systemd/orvix.service, orvix-update.service,
#     orvix-restore.{service,path}, external-backup units
#   - release/sudoers.d/orvix-update
#   - release/scripts/{smoke-admin-js.sh, smoke-admin-ui.sh, smoke-upgrade.sh,
#                     orvix-doctor.sh, lib-asset-propagate.sh, apply-runtime-update.sh,
#                     generate-vapid-keys.sh, reset-admin-password.sh, setup-https.sh,
#                     setup-smtp-tls.sh, healthcheck.sh, diagnostics.sh, ...}
#   - release/admin/index.html + theme-init.js plus whatever ES module
#     index.html references (the production Admin UI is the React/Vite
#     build under web/admin; its bundle filenames are content-hashed
#     per build, e.g. release/admin/assets/index-<hash>.js)
#   - release/webmail/{index.html, sw.js, assets/auth-gate.js, assets/webmail.js}
#   - release/marketing/{index.html, 404.html, robots.txt, sitemap.xml, marketing-assets/*.js}
#   - release/configs/orvix.yaml.example, release/trust/orvix-release-signing.pub.pem
validate_bundle_layout() {
    local root="$1"
    [ -d "$root" ] || { printf 'NOT_A_DIR %s\n' "$root"; return 1; }
    local missing=()
    local rel
    while IFS= read -r rel; do
        [ -e "$root/$rel" ] || missing+=("$rel")
    done <<REQUIRED
bin/orvix
VERSION
BUILDINFO
checksums.txt
SBOM.spdx
release/install.sh
release/install-public.sh
release/upgrade.sh
release/uninstall.sh
release/systemd/orvix.service
release/systemd/orvix-update.service
release/systemd/orvix-restore.service
release/systemd/orvix-restore.path
release/systemd/orvix-external-backup.service
release/systemd/orvix-external-backup.timer
release/systemd/orvix-external-backup-check-weekly.service
release/systemd/orvix-external-backup-check-weekly.timer
release/systemd/orvix-external-backup-check-monthly.service
release/systemd/orvix-external-backup-check-monthly.timer
release/scripts/external-backup-stage.sh
release/scripts/external-backup-run.sh
release/scripts/external-backup-check.sh
release/scripts/external-backup-restore-drill.sh
release/sudoers.d/orvix-update
release/scripts/smoke-admin-js.sh
release/scripts/smoke-admin-ui.sh
release/scripts/smoke-admin-browser.sh
release/scripts/smoke-admin-import-graph.mjs
release/scripts/smoke-admin-runtime.mjs
release/scripts/smoke-install-bundle.sh
release/scripts/smoke-install-public.sh
release/scripts/smoke-upgrade.sh
release/scripts/orvix-doctor.sh
release/scripts/lib-asset-propagate.sh
release/scripts/apply-runtime-update.sh
release/scripts/generate-vapid-keys.sh
release/scripts/reset-admin-password.sh
release/scripts/setup-https.sh
release/scripts/setup-smtp-tls.sh
release/scripts/check-smtp-tls.sh
release/scripts/publish-github-release.sh
release/scripts/verify-github-release-assets.sh
release/scripts/verify-fresh-vps-one-command.sh
release/scripts/build-release-bundle.sh
release/scripts/check-public-origin-contract.sh
release/scripts/generate-sbom.sh
release/scripts/lib-admin-build.sh
release/scripts/sign-release-artifact.sh
release/scripts/verify-release-signature.sh
release/scripts/migrate-admin-root-route.sh
release/scripts/lib-admin-route-migration.sh
release/trust/orvix-release-signing.pub.pem
release/scripts/healthcheck.sh
release/scripts/diagnostics.sh
release/admin/index.html
release/admin/theme-init.js
release/webmail/index.html
release/webmail/sw.js
release/webmail/assets/auth-gate.js
release/webmail/assets/webmail.js
release/marketing/index.html
release/marketing/404.html
release/marketing/robots.txt
release/marketing/sitemap.xml
release/configs/orvix.yaml.example
REQUIRED
    if [ "${#missing[@]}" -gt 0 ]; then
        printf 'BUNDLE_MISSING_FILES:\n' >&2
        for rel in "${missing[@]}"; do
            printf '  - %s\n' "$rel" >&2
        done
        return 1
    fi
    find "$root/release/marketing/marketing-assets" -maxdepth 1 -type f -name '*.js' -print -quit 2>/dev/null | grep -q . || {
        printf 'BUNDLE_MISSING_FILES:\n  - release/marketing/marketing-assets/*.js\n' >&2
        return 1
    }
    # The Admin UI is a React/Vite build with content-hashed asset
    # filenames (release/admin/assets/index-<hash>.js) that differ every
    # build, so its entrypoint can't be a fixed name in the REQUIRED list
    # above. Instead, parse index.html's own <script type="module" src="...">
    # reference and confirm THAT file exists in the bundle — this is the
    # same check release/scripts/smoke-admin-browser.sh performs at
    # release-build time, repeated here against the actual shipped bundle.
    local admin_index="$root/release/admin/index.html"
    local admin_module_src
    admin_module_src="$(grep -oE 'type="module"[^>]*src="[^"]*"' "$admin_index" 2>/dev/null | head -n1 | sed -E 's/.*src="([^"]*)".*/\1/')"
    if [ -z "$admin_module_src" ]; then
        printf 'BUNDLE_MISSING_FILES:\n  - release/admin/index.html has no <script type="module" src="..."> entrypoint\n' >&2
        return 1
    fi
    local admin_module_rel="${admin_module_src#/admin/}"
    [ -e "$root/release/admin/$admin_module_rel" ] || {
        printf 'BUNDLE_MISSING_FILES:\n  - release/admin/%s (referenced by index.html as %s)\n' "$admin_module_rel" "$admin_module_src" >&2
        return 1
    }
    return 0
}

# validate_source_tree_layout enforces that the extracted GitHub SOURCE
# archive at $1 contains every source/build/runtime asset the dev/test
# install path needs. This is a DIFFERENT contract from
# validate_bundle_layout:
#   - a source archive has no bin/orvix, VERSION, BUILDINFO, SBOM or
#     checksums.txt — it is built on the target host by install.sh;
#   - a source archive is UNSIGNED — it is dev/test only, never a
#     production install;
#   - if the extracted tree actually IS a prebuilt bundle, this
#     validator rejects it with a pointer to the bundle flags.
validate_source_tree_layout() {
    local root="$1"
    [ -d "$root" ] || { printf 'NOT_A_DIR %s\n' "$root"; return 1; }
    if [ -f "$root/bin/orvix" ] && [ -f "$root/BUILDINFO" ] && [ -f "$root/VERSION" ]; then
        printf 'SOURCE_TREE_IS_BUNDLE: %s looks like a prebuilt release bundle, not a source archive\n' "$root" >&2
        printf '  install prebuilt bundles with --bundle-url / --version, not --github-ref\n' >&2
        return 1
    fi
    local missing=()
    local rel
    while IFS= read -r rel; do
        [ -e "$root/$rel" ] || missing+=("$rel")
    done <<SOURCE_REQUIRED
go.mod
cmd/orvix/main.go
internal
web/admin/package.json
web/admin/public/theme-init.js
web/marketing/package.json
web/webmail/package.json
release/install.sh
release/install-public.sh
release/upgrade.sh
release/uninstall.sh
release/systemd/orvix.service
release/systemd/orvix-update.service
release/systemd/orvix-restore.service
release/systemd/orvix-restore.path
release/systemd/orvix-external-backup.service
release/systemd/orvix-external-backup.timer
release/systemd/orvix-external-backup-check-weekly.service
release/systemd/orvix-external-backup-check-weekly.timer
release/systemd/orvix-external-backup-check-monthly.service
release/systemd/orvix-external-backup-check-monthly.timer
release/scripts/external-backup-stage.sh
release/scripts/external-backup-run.sh
release/scripts/external-backup-check.sh
release/scripts/external-backup-restore-drill.sh
release/sudoers.d/orvix-update
release/scripts/orvix-doctor.sh
release/scripts/lib-asset-propagate.sh
release/scripts/apply-runtime-update.sh
release/scripts/generate-vapid-keys.sh
release/scripts/reset-admin-password.sh
release/scripts/setup-https.sh
release/scripts/setup-smtp-tls.sh
release/scripts/check-smtp-tls.sh
release/scripts/healthcheck.sh
release/scripts/diagnostics.sh
release/trust/orvix-release-signing.pub.pem
release/admin/index.html
release/admin/theme-init.js
release/webmail/index.html
release/webmail/sw.js
release/webmail/assets/auth-gate.js
release/webmail/assets/webmail.js
release/marketing/index.html
release/marketing/404.html
release/marketing/robots.txt
release/marketing/sitemap.xml
release/configs/orvix.yaml.example
SOURCE_REQUIRED
    if [ "${#missing[@]}" -gt 0 ]; then
        printf 'SOURCE_TREE_MISSING_FILES:\n' >&2
        for rel in "${missing[@]}"; do
            printf '  - %s\n' "$rel" >&2
        done
        return 1
    fi
    grep -q 'module github.com/orvix/orvix' "$root/go.mod" 2>/dev/null || {
        printf 'SOURCE_TREE_MISSING_FILES:\n  - go.mod does not declare module github.com/orvix/orvix\n' >&2
        return 1
    }
    find "$root/release/marketing/marketing-assets" -maxdepth 1 -type f -name '*.js' -print -quit 2>/dev/null | grep -q . || {
        printf 'SOURCE_TREE_MISSING_FILES:\n  - release/marketing/marketing-assets/*.js\n' >&2
        return 1
    }
    local admin_index="$root/release/admin/index.html"
    local admin_module_src
    admin_module_src="$(grep -oE 'type="module"[^>]*src="[^"]*"' "$admin_index" 2>/dev/null | head -n1 | sed -E 's/.*src="([^"]*)".*/\1/')"
    if [ -z "$admin_module_src" ]; then
        printf 'SOURCE_TREE_MISSING_FILES:\n  - release/admin/index.html has no <script type="module" src="..."> entrypoint\n' >&2
        return 1
    fi
    local admin_module_rel="${admin_module_src#/admin/}"
    [ -e "$root/release/admin/$admin_module_rel" ] || {
        printf 'SOURCE_TREE_MISSING_FILES:\n  - release/admin/%s (referenced by index.html as %s)\n' "$admin_module_rel" "$admin_module_src" >&2
        return 1
    }
    return 0
}

# ── Main ──────────────────────────────────────────────────────────

main() {
    local non_interactive="${ORVIX_NON_INTERACTIVE:-}"
    local domain="${ORVIX_PRIMARY_DOMAIN:-${ORVIX_DOMAIN:-}}"
    local public_ipv4="${ORVIX_PUBLIC_IPV4:-}"
    local admin_email="${ORVIX_ADMIN_EMAIL:-}"
    local admin_password="${ORVIX_ADMIN_PASSWORD:-}"
    local setup_https="${ORVIX_SETUP_HTTPS:-}"
    local harden_firewall="${ORVIX_HARDEN_FIREWALL:-}"
    local bundle_override=""
    local bundle_sha=""
    local use_github_archive=0
    local github_ref_override=""

    while [ $# -gt 0 ]; do
        case "$1" in
            --help|-h)
                usage
                exit 0
                ;;
            --installer-version|-V)
                printf 'Orvix Enterprise Mail — public installer v%s\n' "$INSTALLER_VERSION"
                exit 0
                ;;
            --version)
                [ $# -ge 2 ] || fail "--version requires a release value (e.g. --version 1.0.4-rc2); use --installer-version or -V to print the installer program version"
                ORVIX_VERSION="$2"
                shift 2
                ;;
            --version-string|--semver)
                [ $# -ge 2 ] || fail "--semver requires a value"
                ORVIX_VERSION="$2"
                shift 2
                ;;
            --bundle-url)
                [ $# -ge 2 ] || fail "--bundle-url requires a value"
                bundle_override="$2"
                shift 2
                ;;
            --bundle-sha256)
                [ $# -ge 2 ] || fail "--bundle-sha256 requires a value"
                bundle_sha="$2"
                shift 2
                ;;
            --channel)
                [ $# -ge 2 ] || fail "--channel requires a value"
                ORVIX_CHANNEL="$2"
                shift 2
                ;;
            --github-ref)
                [ $# -ge 2 ] || fail "--github-ref requires a value"
                github_ref_override="$2"
                use_github_archive=1
                shift 2
                ;;
            --github-repo)
                [ $# -ge 2 ] || fail "--github-repo requires a value"
                ORVIX_GITHUB_REPO="$2"
                shift 2
                ;;
            --skip-bundle-verify)
                ORVIX_SKIP_BUNDLE_VERIFY=1
                shift
                ;;
            -*)
                warn "unrecognised argument: $1 (use --help for usage)"
                shift
                ;;
            *)
                warn "unrecognised positional arg: $1"
                shift
                ;;
        esac
    done

    # ORVIX_BUNDLE_URL takes precedence over flags
    [ -n "$bundle_override" ] && { ORVIX_BUNDLE_URL="$bundle_override"; ORVIX_BUNDLE_SHA256="$bundle_sha"; }
    [ -n "$github_ref_override" ] && ORVIX_GITHUB_REF="$github_ref_override"

    # ORVIX_GITHUB_REF supplied through the environment behaves EXACTLY
    # like --github-ref <ref>: it selects the GitHub source-archive
    # (dev/test only) installation mode.
    [ -n "$ORVIX_GITHUB_REF" ] && use_github_archive=1

    # Non-interactive mode requires domain + IP
    if [ -n "$non_interactive" ]; then
        if [ -z "$domain" ]; then fail "ORVIX_DOMAIN is required in non-interactive mode"; fi
        if [ -z "$public_ipv4" ]; then fail "ORVIX_PUBLIC_IPV4 is required in non-interactive mode"; fi
    fi

    if [ -n "$public_ipv4" ]; then
        if ! is_valid_public_ipv4 "$public_ipv4"; then
            cat >&2 <<ERR
${RED}ERROR: invalid ORVIX_PUBLIC_IPV4: $public_ipv4${NC}

Must be a routable public IPv4. Loopback, 0.0.0.0, RFC1918,
link-local, multicast, CGNAT, and documentation ranges are all rejected.

Set ORVIX_PUBLIC_IPV4 to your VPS public IPv4 address.
ERR
            exit 1
        fi
    fi

    # ── Locate the bundle ──
    local bundle_path bundle_extract
    bundle_extract="$(mktemp -d -t orvix-install.XXXXXX)"
    ORVIX_TMP_FILES+=("$bundle_extract")

    if [ -n "$ORVIX_GITHUB_REF" ] && [ "$use_github_archive" = "1" ]; then
        warn "installing from a GitHub SOURCE ARCHIVE (ref=$ORVIX_GITHUB_REF); the archive is UNSIGNED"
        warn "this path is for dev/CI ONLY; production installs must use --version or --bundle-url with a signed release bundle"
        local gh_url
        gh_url="$(resolve_github_archive_url "$ORVIX_GITHUB_REF")"
        info "downloading GitHub source archive: $gh_url"
        local gh_tar
        gh_tar="$(download_to_tmp "$gh_url" "github source archive ref=$ORVIX_GITHUB_REF")"
        info "extracting GitHub source archive to $bundle_extract"
        if ! tar -xzf "$gh_tar" -C "$bundle_extract" --strip-components=1; then
            fail "could not extract GitHub source archive; aborting"
        fi
        bundle_path="$bundle_extract"
    else
        # Prefer a release bundle. Either explicit --bundle-url, or a
        # versioned URL derived from --version / ORVIX_VERSION /
        # ORVIX_CHANNEL.
        local bundle_url
        bundle_url="$(resolve_bundle_url)"
        info "installing release bundle from $bundle_url"
        local bundle_sha_used=""
        bundle_sha_used="${ORVIX_RESOLVED_SHA:-}"
        [ -n "$bundle_sha" ] && bundle_sha_used="$bundle_sha"

        # When no explicit sha256 is provided, download the .sha256
        # sidecar from the same base URL (GitHub Releases workflow).
        # The sidecar MUST be present — remote bundles are never
        # accepted without checksum verification. Only local developer
        # artifacts (--skip-bundle-verify) may bypass this gate.
        if [ -z "$bundle_sha_used" ]; then
            if [ -n "$ORVIX_SKIP_BUNDLE_VERIFY" ]; then
                warn "bundle sha256 verification SKIPPED (--skip-bundle-verify). Only use this for local development."
            else
                local auto_sha
                auto_sha="$(try_download_sha256 "$bundle_url" || true)"
                if [ -n "$auto_sha" ]; then
                    bundle_sha_used="$auto_sha"
                    info "auto-resolved bundle sha256: $bundle_sha_used"
                else
                    fail "cannot verify bundle integrity: .sha256 sidecar not found at ${bundle_url}.sha256 (use --skip-bundle-verify to bypass)"
                fi
            fi
        fi

        if [ -z "$ORVIX_SKIP_BUNDLE_VERIFY" ] && [ -n "$bundle_sha_used" ]; then
            info "expected bundle sha256: $bundle_sha_used"
        fi

        local bundle_tar
        bundle_tar="$(download_to_tmp "$bundle_url" "release bundle")"

        if [ -z "$ORVIX_SKIP_BUNDLE_VERIFY" ]; then
            verify_bundle_sha256 "$bundle_tar" "$bundle_sha_used"
            verify_bundle_signature "$bundle_url" "$bundle_tar"
            verify_release_sidecars "$bundle_url" "$bundle_tar"
        fi

        info "extracting bundle to $bundle_extract"
        # Bundles are sealed with a top-level "orvix/" directory;
        # GitHub archives use "<repo>-<sha>/". Use --strip-components
        # so install.sh sees the same layout regardless of source.
        if tar -tzf "$bundle_tar" 2>/dev/null | head -n 1 | grep -qE '^orvix/'; then
            tar -xzf "$bundle_tar" -C "$bundle_extract"
            bundle_path="$bundle_extract/orvix"
        else
            tar -xzf "$bundle_tar" -C "$bundle_extract" --strip-components=1
            bundle_path="$bundle_extract"
        fi
    fi

    # ── Validate the release tree ──
    if [ "$use_github_archive" = "1" ]; then
        info "validating source tree layout..."
        if ! validate_source_tree_layout "$bundle_path"; then
            fail "downloaded GitHub source archive is incomplete; refusing to install from a half-complete source tree"
        fi
        info "source tree validated: go.mod + cmd/orvix + web sources + install.sh + admin + webmail + systemd + sudoers + scripts present"
    else
        info "validating release tree..."
        if ! validate_bundle_layout "$bundle_path"; then
            fail "downloaded release is missing required files; refusing to install a half-complete bundle"
        fi
        info "release tree validated: install.sh + admin + webmail + systemd + sudoers + scripts + checksums + SBOM present"
        verify_bundle_identity "$bundle_path"
    fi

    # ── Print install plan ──
    local version_from_bundle="" commit_from_bundle="" channel_from_bundle=""
    if [ -f "$bundle_path/BUILDINFO" ]; then
        version_from_bundle="$(awk -F= '/^version=/ {print $2; exit}' "$bundle_path/BUILDINFO" || true)"
        commit_from_bundle="$(awk -F= '/^commit=/ {print $2; exit}' "$bundle_path/BUILDINFO" || true)"
        channel_from_bundle="$(awk -F= '/^channel=/ {print $2; exit}' "$bundle_path/BUILDINFO" || true)"
    fi
    [ -z "$version_from_bundle" ] && [ -f "$bundle_path/VERSION" ] && version_from_bundle="$(cat "$bundle_path/VERSION" | tr -d '[:space:]')"
    if [ -n "$commit_from_bundle" ]; then
        commit_from_bundle="${commit_from_bundle:0:12}"
    fi

    info "install plan:"
    info "  source       : $bundle_path"
    info "  version      : ${version_from_bundle:-unknown}"
    info "  commit       : ${commit_from_bundle:-unknown}"
    info "  channel      : ${channel_from_bundle:-$ORVIX_CHANNEL}"
    info "  domain       : ${domain:-<interactive prompt>}"
    info "  public IPv4  : ${public_ipv4:-<auto or interactive prompt>}"

    # ── Export env vars install.sh expects ──
    export ORVIX_PRIMARY_DOMAIN="$domain"
    export ORVIX_DOMAIN="$domain"
    export ORVIX_PUBLIC_IPV4="$public_ipv4"
    export ORVIX_ADMIN_EMAIL="$admin_email"
    export ORVIX_ADMIN_PASSWORD="$admin_password"
    export ORVIX_SETUP_HTTPS="$setup_https"
    export ORVIX_HARDEN_FIREWALL="$harden_firewall"
    export ORVIX_NON_INTERACTIVE="$non_interactive"
    export ORVIX_VERSION="${ORVIX_VERSION:-$version_from_bundle}"
    export ORVIX_COMMIT="${ORVIX_COMMIT:-$commit_from_bundle}"
    export ORVIX_CHANNEL="${ORVIX_CHANNEL:-${channel_from_bundle:-stable}}"
    export ORVIX_SOURCE_DIR="$bundle_path"

    local installer="$bundle_path/release/install.sh"
    if [ ! -f "$installer" ]; then
        fail "expected release/install.sh at $installer but it is missing; bundle is corrupt"
    fi
    if ! bash -n "$installer" 2>/dev/null; then
        fail "release/install.sh has a bash syntax error; refusing to install a corrupt installer"
    fi

    info "delegating to $installer"
    exec bash "$installer"
}

main "$@"
