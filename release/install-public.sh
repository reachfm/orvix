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
#   4. Delegating to install.sh with ORVIX_SOURCE_DIR pointing at the
#      extracted bundle.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/reachfm/orvix/main/release/install-public.sh | sudo bash
#
# Resolution order (first wins):
#   1. --bundle-url <url>            explicit tarball (escape hatch for air-gap)
#   2. ORVIX_BUNDLE_URL              same, via env var
#   3. --github-repo / ORVIX_GITHUB_REPO + --github-ref / ORVIX_GITHUB_REF
#                                    archives the matching tag/branch/commit
#                                    via codeload tarball
#   4. --version / ORVIX_VERSION     downloads from
#      ${ORVIX_RELEASES_BASE}/orvix-enterprise-mail-<ver>-linux-amd64.tar.gz
#   5. (default)                     installs the latest stable release from
#                                    GitHub Releases (reachfm/orvix)
#
# The installer prefers bundles over GitHub archives because:
#   * A bundle is the audited artifact (sha256 in checksums.txt).
#   * A GitHub archive is whatever happens to be on the named ref
#     right now — useful for dev/test, never for production installs.
#   * A bundle contains only the runtime assets; a GitHub archive
#     contains the entire repo (CI scaffolding, tests, docs) which
#     the install path never needs and which adds a few MB.
#
# Environment variables (full list):
#   ORVIX_DOMAIN            Primary mail domain
#   ORVIX_PUBLIC_IPV4       Public IPv4 of this host
#   ORVIX_ADMIN_EMAIL       Admin email address
#   ORVIX_ADMIN_PASSWORD    Admin password (8-72 bytes)
#   ORVIX_SETUP_HTTPS       If set, invoke setup-https.sh after install
#   ORVIX_HARDEN_FIREWALL   If set, run firewall hardening after install
#   ORVIX_NON_INTERACTIVE   If set, require ORVIX_DOMAIN + ORVIX_PUBLIC_IPV4
#                           and run without any prompts.
#
#   ORVIX_VERSION           Release version (e.g. 1.0.3-rc5).
#                           Default: stable.
#   ORVIX_CHANNEL           Release channel (stable, rc, dev).
#                           Default: stable.
#   ORVIX_COMMIT            Expected commit SHA (used to verify the
#                           bundle's binary against the expected one).
#   ORVIX_BUNDLE_URL        Direct override of the bundle download URL.
#   ORVIX_BUNDLE_SHA256     Expected sha256 of the bundle (optional but
#                           recommended — checks against the bundle
#                           sha256 sidecar when supplied).
#   ORVIX_GITHUB_REPO       GitHub repo slug (default: orvix/orvix).
#   ORVIX_GITHUB_REF        Git ref (tag, branch, commit). Bypasses the
#                           bundle flow to install from the GitHub
#                           archive. Off by default.
#   ORVIX_SKIP_BUNDLE_VERIFY Disable bundle sha256 check (NOT recommended;
#                           only for air-gapped test rigs).
#

ORVIX_DOCS_URL="${ORVIX_DOCS_URL:-https://docs.orvix.email}"
ORVIX_RELEASES_BASE="${ORVIX_RELEASES_BASE:-https://github.com/reachfm/orvix/releases/latest/download}"
ORVIX_GITHUB_REPO="${ORVIX_GITHUB_REPO:-orvix/orvix}"
ORVIX_GITHUB_BASE="${ORVIX_GITHUB_BASE:-https://codeload.github.com}"
ORVIX_CHANNEL="${ORVIX_CHANNEL:-stable}"
ORVIX_VERSION="${ORVIX_VERSION:-}"
ORVIX_COMMIT="${ORVIX_COMMIT:-}"
ORVIX_BUNDLE_URL="${ORVIX_BUNDLE_URL:-}"
ORVIX_BUNDLE_SHA256="${ORVIX_BUNDLE_SHA256:-}"
ORVIX_GITHUB_REF="${ORVIX_GITHUB_REF:-}"
ORVIX_SKIP_BUNDLE_VERIFY="${ORVIX_SKIP_BUNDLE_VERIFY:-}"

RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
YELLOW=$'\033[1;33m'
NC=$'\033[0m'

usage() {
    cat <<USAGE
Orvix Enterprise Mail — public installer entrypoint

Usage:
  curl -fsSL https://raw.githubusercontent.com/reachfm/orvix/main/release/install-public.sh | sudo bash

Resolution modes (first match wins):
  --bundle-url <url>            Install from a specific bundle tarball URL
                                (escape hatch for air-gapped prod).
  --github-ref <ref>            Install from a GitHub archive instead of a
                                bundle (dev/test only; bypasses bundle
                                sha256 verification).
  --version <semver>            Install version <semver> from
                                ${ORVIX_RELEASES_BASE}/orvix-enterprise-mail-<semver>-linux-amd64.tar.gz
  --channel <chan>              Use <chan> for the default URL when no
                                --version is supplied (default: stable).

Environment variables:
  ORVIX_DOMAIN                  Primary mail domain (required in non-interactive mode)
  ORVIX_PUBLIC_IPV4              Public IPv4 (required in non-interactive mode)
  ORVIX_ADMIN_EMAIL              Admin email (optional; prompted if unset)
  ORVIX_ADMIN_PASSWORD           Admin password (optional; prompted if unset)
  ORVIX_SETUP_HTTPS              Run HTTPS setup after install
  ORVIX_HARDEN_FIREWALL          Run firewall hardening after install
  ORVIX_NON_INTERACTIVE          Non-interactive mode
  ORVIX_COMMIT                   Expected commit SHA — install.sh verifies it
  ORVIX_BUNDLE_URL               Direct bundle URL override
  ORVIX_BUNDLE_SHA256            Expected sha256 of the bundle
  ORVIX_GITHUB_REPO              GitHub repo slug (default: orvix/orvix)
  ORVIX_GITHUB_REF               GitHub ref to install (bypasses bundle)
  ORVIX_SKIP_BUNDLE_VERIFY       Skip sha256 verification (NOT recommended)

Flags:
  --help, -h                    Show this message
  --version                     Show version info for the installer script

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
resolve_bundle_url() {
    if [ -n "$ORVIX_BUNDLE_URL" ]; then
        printf '%s\n' "$ORVIX_BUNDLE_URL"
        ORVIX_RESOLVED_SHA="$ORVIX_BUNDLE_SHA256"
        return 0
    fi
    if [ -n "$ORVIX_VERSION" ]; then
        printf '%s/orvix-enterprise-mail-%s-linux-amd64.tar.gz\n' "$ORVIX_RELEASES_BASE" "$ORVIX_VERSION"
        ORVIX_RESOLVED_SHA=""
        return 0
    fi
    # Default: latest stable
    printf '%s/orvix-enterprise-mail-stable-linux-amd64.tar.gz\n' "$ORVIX_RELEASES_BASE"
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
    info "downloading $label from $url"
    if ! curl -fsSL --retry 3 --max-time 600 -o "$tmp" "$url"; then
        rm -f "$tmp"
        fail "could not download $label from $url (check connectivity; the URL must be reachable from this host)"
    fi
    # Sanity: tarball should be at least a few KB and not the HTML
    # error page that some CDNs return.
    local size
    size="$(wc -c < "$tmp" 2>/dev/null || echo 0)"
    if [ "$size" -lt 1024 ]; then
        rm -f "$tmp"
        fail "downloaded artifact from $url is only $size bytes (this is usually an HTML error page, not the bundle)"
    fi
    printf '%s\n' "$tmp"
}

# verify_bundle_sha256 checks $1 against the expected sha256 in $2
# when $2 is non-empty. Skips silently when $2 is empty. Errors loud
# on mismatch.
verify_bundle_sha256() {
    local path="$1"
    local expected="$2"
    if [ -z "$expected" ]; then
        warn "no bundle sha256 supplied (set ORVIX_BUNDLE_SHA256 or --bundle-url with checksums.txt for production)"
        return 0
    fi
    local actual
    actual="$(sha256sum "$path" | awk '{print $1}')"
    if [ "$actual" != "$expected" ]; then
        fail "bundle sha256 mismatch (expected $expected, got $actual)"
    fi
    info "bundle sha256 verified: $actual"
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

# validate_bundle_layout enforces that the extracted bundle at $1
# contains every file install.sh needs. We refuse to hand control to
# install.sh when any required file is missing — the installer must
# never silently fall back to building on the operator's host.
#
# Required files mirror the BUILD 4 contract from the bundle script:
#   - bin/orvix (the verified binary)
#   - release/install.sh, release/upgrade.sh, release/uninstall.sh
#   - release/install-public.sh (so re-runs can self-resolve)
#   - release/systemd/orvix.service, release/systemd/orvix-update.service
#   - release/sudoers.d/orvix-update
#   - release/scripts/{smoke-admin-js.sh, smoke-admin-ui.sh, smoke-upgrade.sh,
#                     orvix-doctor.sh, lib-asset-propagate.sh, apply-runtime-update.sh,
#                     generate-vapid-keys.sh, reset-admin-password.sh, setup-https.sh,
#                     setup-smtp-tls.sh, healthcheck.sh, diagnostics.sh}
#   - release/admin/{index.html, app.js, styles.css}
#   - release/webmail/{index.html, sw.js, assets/auth-gate.js, assets/webmail.js}
#   - VERSION, BUILDINFO
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
release/install.sh
release/install-public.sh
release/upgrade.sh
release/uninstall.sh
release/systemd/orvix.service
release/systemd/orvix-update.service
release/sudoers.d/orvix-update
release/scripts/smoke-admin-js.sh
release/scripts/smoke-admin-ui.sh
release/scripts/smoke-upgrade.sh
release/scripts/orvix-doctor.sh
release/scripts/lib-asset-propagate.sh
release/scripts/apply-runtime-update.sh
release/scripts/generate-vapid-keys.sh
release/scripts/reset-admin-password.sh
release/scripts/setup-https.sh
release/scripts/setup-smtp-tls.sh
release/scripts/healthcheck.sh
release/scripts/diagnostics.sh
release/admin/index.html
release/admin/app.js
release/admin/styles.css
release/webmail/index.html
release/webmail/sw.js
release/webmail/assets/auth-gate.js
release/webmail/assets/webmail.js
REQUIRED
    if [ "${#missing[@]}" -gt 0 ]; then
        printf 'BUNDLE_MISSING_FILES:\n' >&2
        for rel in "${missing[@]}"; do
            printf '  - %s\n' "$rel" >&2
        done
        return 1
    fi
    return 0
}

# ── Main ──────────────────────────────────────────────────────────

main() {
    local non_interactive="${ORVIX_NON_INTERACTIVE:-}"
    local domain="${ORVIX_DOMAIN:-}"
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
            --version|-V)
                printf 'Orvix Enterprise Mail — public installer v3.0.0\n'
                exit 0
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
            --version-string|--semver)
                [ $# -ge 2 ] || fail "--semver requires a value"
                ORVIX_VERSION="$2"
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
    trap 'rm -rf "$bundle_extract"' EXIT

    if [ -n "$ORVIX_GITHUB_REF" ] && [ "$use_github_archive" = "1" ]; then
        warn "installing from GitHub archive (ref=$ORVIX_GITHUB_REF); bundle sha256 verification is skipped"
        warn "this path is for dev/CI only; production installs must use --bundle-url or --version"
        local gh_url
        gh_url="$(resolve_github_archive_url "$ORVIX_GITHUB_REF")"
        info "downloading GitHub archive: $gh_url"
        local gh_tar
        gh_tar="$(download_to_tmp "$gh_url" "github archive ref=$ORVIX_GITHUB_REF")"
        info "extracting GitHub archive to $bundle_extract"
        if ! tar -xzf "$gh_tar" -C "$bundle_extract" --strip-components=1; then
            rm -f "$gh_tar"
            fail "could not extract GitHub archive; aborting"
        fi
        rm -f "$gh_tar"
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
        rm -f "$bundle_tar"
    fi

    # ── Validate bundle layout ──
    info "validating release tree..."
    if ! validate_bundle_layout "$bundle_path"; then
        fail "downloaded release is missing required files; refusing to install a half-complete bundle"
    fi
    info "release tree validated: install.sh + admin + webmail + systemd + sudoers + scripts present"

    # ── Print install plan ──
    local version_from_bundle commit_from_bundle channel_from_bundle
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
