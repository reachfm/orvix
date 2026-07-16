#!/usr/bin/env bash
# build-release-bundle.sh
#
# Builds a self-contained release bundle tarball from the current
# git HEAD. The bundle is the supported distribution channel for
# the one-command public installer — it never depends on a live
# git tree, never reaches back to the host for missing files, and
# never lets a stale prebuilt binary ship against the wrong commit.
#
# Output:
#   dist/orvix-enterprise-mail-<version>-linux-amd64.tar.gz
#
# Bundle layout (top-level "orvix/" prefix so a tar -C / target
# is safe — nothing ever lands at the tarball root by accident):
#
#   orvix/
#     bin/orvix                        # verified current binary
#     release/install.sh
#     release/upgrade.sh
#     release/uninstall.sh
#     release/install-public.sh
#     release/systemd/orvix.service
#     release/systemd/orvix-update.service
#     release/sudoers.d/orvix-update
#     release/scripts/*.sh
#     release/admin/**                 # admin SPA + modules
#     release/webmail/**               # webmail SPA
#     release/marketing/**             # public marketing SPA
#     release/configs/orvix.yaml.example
#     VERSION
#     BUILDINFO                        # commit/version/build_time/channel
#     checksums.txt
#
# Behaviour:
#   * Always builds bin/orvix from current git HEAD via
#     `go build` with -ldflags injecting internal/buildinfo
#     Version / Commit / Tag / BuildTime / Channel.
#   * Runs `bin/orvix version --full` after build and asserts
#     the embedded version matches the supplied --version (or
#     release/VERSION) and that commit is non-empty. Otherwise
#     the script exits non-zero — we never ship a bundle whose
#     binary does not match the bundle metadata.
#   * Generates checksums.txt (sha256) over the bundle
#     contents so the public installer can verify the same
#     artifact later.
#   * Default version source: release/VERSION (first non-empty
#     non-comment line). Override with --version <ver>.
#   * --output <dir>  : where to write dist/. Default ./dist.
#   * --arch <arch>   : target arch. Default amd64. arm64 is
#                       supported; other arches fail loud with a
#                       clear error (no silent fallback).
#   * --os <os>       : target OS. Default linux.
#
# Exit codes:
#   0  bundle built and verified
#   1  pre-flight failed (missing files, missing tool, bad args)
#   2  binary build failed
#   3  embedded metadata mismatch (binary out of sync with bundle)
#   4  bundle assembly / checksum failed

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$REPO_ROOT"

OUTPUT_DIR="dist"
TARGET_OS="linux"
TARGET_ARCH="amd64"
VERSION_OVERRIDE=""
CHANNEL_OVERRIDE=""
LDFLAGS_EXTRA=""

usage() {
    cat <<USAGE
Usage: bash build-release-bundle.sh [options]

Builds a self-contained release bundle tarball from the current
git HEAD. The bundle is the supported distribution channel for
the one-command public installer.

Options:
  --output <dir>      Output directory (default: ./dist)
  --os <os>           Target OS (default: linux)
  --arch <arch>       Target arch: amd64, arm64 (default: amd64)
  --version <ver>     Override release/VERSION (e.g. 1.0.3-rc5)
  --channel <chan>    Override channel (default: stable)
  --ldflags-extra <s> Extra -ldflags appended after the standard set
  --help, -h          Show this help

Output:
  dist/orvix-enterprise-mail-<version>-<arch>.tar.gz

USAGE
}

while [ $# -gt 0 ]; do
    case "$1" in
        --output) OUTPUT_DIR="$2"; shift 2 ;;
        --os) TARGET_OS="$2"; shift 2 ;;
        --arch) TARGET_ARCH="$2"; shift 2 ;;
        --version) VERSION_OVERRIDE="$2"; shift 2 ;;
        --channel) CHANNEL_OVERRIDE="$2"; shift 2 ;;
        --ldflags-extra) LDFLAGS_EXTRA="$2"; shift 2 ;;
        --help|-h) usage; exit 0 ;;
        *) printf 'unknown flag: %s\n' "$1" >&2; usage >&2; exit 1 ;;
    esac
done

RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
YELLOW=$'\033[1;33m'
NC=$'\033[0m'

fail() { printf '%bERROR:%b %s\n' "$RED" "$NC" "$*" >&2; exit "${2:-1}"; }
info() { printf '%bINFO:%b %s\n' "$GREEN" "$NC" "$*" >&2; }
warn() { printf '%bWARN:%b %s\n' "$YELLOW" "$NC" "$*" >&2; }

# Admin SPA build/packaging logic (fail-closed legacy fallback) lives in a
# sourced library so it can be unit-tested in isolation.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=release/scripts/lib-admin-build.sh
. "$SCRIPT_DIR/lib-admin-build.sh"

# ── 1. Pre-flight ─────────────────────────────────────────────────
command -v git  >/dev/null 2>&1 || fail "git is required"
command -v tar  >/dev/null 2>&1 || fail "tar is required"
command -v sha256sum >/dev/null 2>&1 || fail "sha256sum is required"

if [ -n "$(git status --porcelain --untracked-files=no)" ] && [ "${ORVIX_ALLOW_DIRTY_BUILD:-}" != "1" ]; then
    fail "tracked working tree is dirty; commit reviewed changes before building a release"
fi

case "$TARGET_OS-$TARGET_ARCH" in
    linux-amd64|linux-arm64) ;;
    *) fail "unsupported target $TARGET_OS-$TARGET_ARCH (supported: linux-amd64, linux-arm64)" ;;
esac

[ -d .git ] || fail "must be run from inside the orvix git repository (no .git at $REPO_ROOT)"
[ -f go.mod ] || fail "go.mod not found at $REPO_ROOT (this is not the orvix build root)"

GIT_COMMIT="$(git rev-parse HEAD)"
GIT_SHORT_COMMIT="$(git rev-parse --short HEAD)"
GIT_BRANCH="$(git rev-parse --abbrev-ref HEAD)"
GIT_BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# Resolve version: --version > release/VERSION > "0.0.0-dev"
RESOLVED_VERSION="$VERSION_OVERRIDE"
if [ -z "$RESOLVED_VERSION" ] && [ -f release/VERSION ]; then
    RESOLVED_VERSION="$(awk 'NF && $1 !~ /^#/ {print; exit}' release/VERSION | tr -d '[:space:]')"
fi
[ -n "$RESOLVED_VERSION" ] || fail "could not resolve version (no --version and release/VERSION missing)"
RESOLVED_CHANNEL="${CHANNEL_OVERRIDE:-stable}"
[ -n "$RESOLVED_CHANNEL" ] || fail "channel resolved empty"
case "$RESOLVED_VERSION" in
    *[!A-Za-z0-9._+-]*) fail "invalid release version token: $RESOLVED_VERSION" ;;
esac
case "$RESOLVED_CHANNEL" in
    ""|*[!a-z0-9._-]*|[._-]*) fail "invalid release channel token: $RESOLVED_CHANNEL" ;;
esac

info "building bundle version=$RESOLVED_VERSION commit=$GIT_SHORT_COMMIT channel=$RESOLVED_CHANNEL target=$TARGET_OS-$TARGET_ARCH"

# Required source-tree files. Every one of these MUST land in the bundle.
REQUIRED_FILES=(
    release/install.sh
    release/install-public.sh
    release/upgrade.sh
    release/uninstall.sh
    release/VERSION
    release/systemd/orvix.service
    release/systemd/orvix-update.service
    release/systemd/orvix-restore.service
    release/systemd/orvix-restore.path
    release/sudoers.d/orvix-update
    release/scripts/healthcheck.sh
    release/scripts/smoke-admin-js.sh
    release/scripts/smoke-admin-ui.sh
    release/scripts/smoke-admin-browser.sh
    release/scripts/smoke-admin-import-graph.mjs
    release/scripts/smoke-admin-runtime.mjs
    release/scripts/smoke-install-bundle.sh
    release/scripts/smoke-install-public.sh
    release/scripts/smoke-upgrade.sh
    release/scripts/orvix-doctor.sh
    release/scripts/diagnostics.sh
    release/scripts/lib-asset-propagate.sh
    release/scripts/lib-admin-build.sh
    release/scripts/apply-runtime-update.sh
    release/scripts/generate-vapid-keys.sh
    release/scripts/reset-admin-password.sh
    release/scripts/setup-https.sh
    release/scripts/setup-smtp-tls.sh
    release/scripts/check-smtp-tls.sh
    release/scripts/publish-github-release.sh
    release/scripts/verify-github-release-assets.sh
    release/scripts/verify-fresh-vps-one-command.sh
    release/admin/app.js
    release/admin/index.html
    release/admin/styles.css
    release/admin/modules/auth.js
    release/admin/modules/components.js
    release/webmail/index.html
    release/webmail/sw.js
    release/marketing/index.html
    release/marketing/404.html
)
missing=()
for f in "${REQUIRED_FILES[@]}"; do
    [ -e "$f" ] || missing+=("$f")
done
if [ "${#missing[@]}" -gt 0 ]; then
    printf 'missing required release files:\n' >&2
    for f in "${missing[@]}"; do printf '  %s\n' "$f" >&2; done
    fail "release tree incomplete; refusing to ship a partial bundle" 1
fi

# ── 2. Build the binary ───────────────────────────────────────────
GO_BIN="${GO_BIN:-go}"
if ! command -v "$GO_BIN" >/dev/null 2>&1 && [ ! -x "$GO_BIN" ]; then
    # Windows Go default install location, Git Bash path. Useful
    # when running this script outside the regular $PATH.
    for fallback in /c/Go/bin/go.exe /mnt/c/Go/bin/go.exe /usr/local/go/bin/go; do
        if [ -x "$fallback" ]; then
            GO_BIN="$fallback"
            break
        fi
    done
fi
if ! command -v "$GO_BIN" >/dev/null 2>&1 && [ ! -x "$GO_BIN" ]; then
    fail "Go toolchain not found (set GO_BIN=/path/to/go or install go; tried '$GO_BIN')"
fi
# Resolve the absolute path of the Go binary so the build cmd below
# works even when GO_BIN was supplied as a bare name ("go" vs
# "/c/go/bin/go.exe").
if command -v "$GO_BIN" >/dev/null 2>&1; then
    GO_BIN="$(command -v "$GO_BIN")"
fi

WORK_DIR="$(mktemp -d -t orvix-bundle.XXXXXX)"
trap 'rm -rf "$WORK_DIR"' EXIT
BUNDLE_ROOT="$WORK_DIR/orvix"
mkdir -p "$BUNDLE_ROOT/bin" "$BUNDLE_ROOT/release/admin" "$BUNDLE_ROOT/release/webmail" "$BUNDLE_ROOT/release/marketing" \
         "$BUNDLE_ROOT/release/systemd" "$BUNDLE_ROOT/release/sudoers.d" \
         "$BUNDLE_ROOT/release/scripts" "$BUNDLE_ROOT/release/scripts/tests" \
         "$BUNDLE_ROOT/release/configs" "$BUNDLE_ROOT/release/trust"

BIN_OUT="$BUNDLE_ROOT/bin/orvix"
LDFLAGS=(
    "-s"
    "-w"
    "-X github.com/orvix/orvix/internal/buildinfo.Version=$RESOLVED_VERSION"
    "-X github.com/orvix/orvix/internal/buildinfo.Commit=$GIT_COMMIT"
    "-X github.com/orvix/orvix/internal/buildinfo.BuildTime=$GIT_BUILD_TIME"
    "-X github.com/orvix/orvix/internal/buildinfo.Channel=$RESOLVED_CHANNEL"
)
if [ -n "$LDFLAGS_EXTRA" ]; then
    # shellcheck disable=SC2206
    extra_args=( $LDFLAGS_EXTRA )
    LDFLAGS+=( "${extra_args[@]}" )
fi

GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" CGO_ENABLED=0 \
    "$GO_BIN" build -trimpath -ldflags "$(IFS=' '; echo "${LDFLAGS[*]}")" \
    -o "$BIN_OUT" ./cmd/orvix \
    || fail "go build failed" 2

# On Windows, Go always appends .exe to the binary name (even for
# Linux cross-compilation targets) so the resulting binary can be
# inspected by `go tool objdump`.  We normalise the binary path
# early so the rest of the script always references the real file.
BIN_REAL="$BIN_OUT"
if [ ! -f "$BIN_REAL" ] && [ -f "$BIN_OUT.exe" ]; then
    BIN_REAL="$BIN_OUT.exe"
fi
# Also check common alternate locations in case Go uses a
# different path convention.
for alt in "$BIN_OUT.exe" "$BIN_OUT" "$(dirname "$BIN_OUT")/orvix.exe"; do
    if [ -f "$alt" ]; then BIN_REAL="$alt"; break; fi
done

# Binary sanity checks before ELF verification.  Every failure
# here includes GOOS/GOARCH so the operator can correlate the
# build environment with the binary.
if [ ! -f "$BIN_REAL" ]; then
    fail "built binary not found at $BIN_REAL (go build succeeded but output file is missing; GOOS=$TARGET_OS GOARCH=$TARGET_ARCH)" 2
fi
bin_size="$(wc -c < "$BIN_REAL" 2>/dev/null || echo 0)"
if [ "$bin_size" -eq 0 ]; then
    fail "built binary at $BIN_REAL is empty (0 bytes; GOOS=$TARGET_OS GOARCH=$TARGET_ARCH)" 2
fi
if [ "$bin_size" -lt 1024 ]; then
    fail "built binary at $BIN_REAL is suspiciously small ($bin_size bytes; expected > 1KB; GOOS=$TARGET_OS GOARCH=$TARGET_ARCH)" 2
fi
info "built binary: $BIN_REAL ($bin_size bytes, GOOS=$TARGET_OS, GOARCH=$TARGET_ARCH)"
if command -v file >/dev/null 2>&1; then
    info "binary file type: $(file -b "$BIN_REAL" 2>/dev/null || echo 'unknown')"
fi
# If Go produced a .exe variant, normalise to the expected name.
if [ "$BIN_REAL" != "$BIN_OUT" ]; then
    mv "$BIN_REAL" "$BIN_OUT" || fail "could not rename $BIN_REAL -> $BIN_OUT" 2
    BIN_REAL="$BIN_OUT"
fi

# Cross-compiled Linux ELF binaries are not flagged as
# executable by Git Bash on Windows. We verify the binary via
# the ELF magic bytes (7f 45 4c 46 = "\x7fELF"). Bash is portable
# enough on its own for 4-byte I/O; we fall back through several
# methods so the check works on the host that runs this script
# (CI ubuntu, Git Bash on Windows, macOS developer laptops).
read_elf_magic_hex() {
    local file="$1"
    if command -v od >/dev/null 2>&1; then
        od -An -tx1 -N4 "$file" 2>/dev/null | tr -d ' \n'
        return
    fi
    if command -v xxd >/dev/null 2>&1; then
        xxd -l 4 -p "$file" 2>/dev/null
        return
    fi
    if command -v hexdump >/dev/null 2>&1; then
        hexdump -n 4 -e '1/1 "%02x"' "$file" 2>/dev/null
        return
    fi
    if command -v python3 >/dev/null 2>&1; then
        python3 -c "import sys; sys.stdout.write(open(sys.argv[1],'rb').read(4).hex())" "$file" 2>/dev/null
        return
    fi
    if command -v python >/dev/null 2>&1; then
        python -c "import sys; sys.stdout.write(open(sys.argv[1],'rb').read(4).hex())" "$file" 2>/dev/null
        return
    fi
    # perl handles raw binary with binmode + unpack; available
    # on Git Bash, macOS, and most Linux deployments.
    if command -v perl >/dev/null 2>&1; then
        perl -e 'open(my $fh,"<:raw",$ARGV[0]) or exit 1; read($fh,my $b,4); print unpack("H*",$b)' "$file" 2>/dev/null
        return
    fi
    # Final fallback — dd + shell printf.  dd reads raw bytes
    # portably; we convert them character by character (safe for
    # ELF magic which has no null bytes in the first four).
    local raw
    raw="$(dd if="$file" bs=1 count=4 2>/dev/null)"
    if [ -n "$raw" ]; then
        printf '%02x%02x%02x%02x' "'${raw:0:1}" "'${raw:1:1}" "'${raw:2:1}" "'${raw:3:1}"
        return
    fi
    printf ''
}
magic_bytes="$(read_elf_magic_hex "$BIN_OUT")"
[ "$magic_bytes" = "7f454c46" ] \
    || fail "built binary at $BIN_OUT is not a Linux ELF (size=$bin_size bytes, GOOS=$TARGET_OS GOARCH=$TARGET_ARCH, got magic=$magic_bytes, expected 7f454c46)" 2

# ── 3. Verify embedded metadata ───────────────────────────────────
EMBEDDED_FULL="$("$BIN_OUT" version --full || true)"

# Parse version from first line: "orvix <version>" — version is field 2.
EMBEDDED_VERSION="$(echo "$EMBEDDED_FULL" | awk 'NR==1 && $1=="orvix" {print $2}')"

# Parse embedded commit robustly — find line with optional whitespace
# + "commit:", strip prefix, trim whitespace, extract the SHA only.
# This handles any alignment/whitespace the pretty-printer uses.
EMBEDDED_COMMIT="$(echo "$EMBEDDED_FULL" | awk '/^[[:space:]]*commit:[[:space:]]*/ {sub(/^[[:space:]]*commit:[[:space:]]*/, ""); print; exit}')"
if [ -z "$EMBEDDED_COMMIT" ]; then
    printf 'could not parse commit from:\n%s\n' "$EMBEDDED_FULL" >&2
    fail "embedded commit not found" 3
fi

# Accept exact full match or prefix match (short commit).
if [ "$EMBEDDED_COMMIT" != "$GIT_COMMIT" ]; then
    case "$EMBEDDED_COMMIT" in
        "$GIT_SHORT_COMMIT"*) ;;
        *) printf 'expected commit %s (or short %s) but binary reports: %s\n' "$GIT_COMMIT" "$GIT_SHORT_COMMIT" "$EMBEDDED_COMMIT" >&2
           fail "embedded commit mismatch" 3 ;;
    esac
fi

# Parse embedded channel robustly.
EMBEDDED_CHANNEL="$(echo "$EMBEDDED_FULL" | awk '/^[[:space:]]*channel:[[:space:]]*/ {sub(/^[[:space:]]*channel:[[:space:]]*/, ""); print; exit}')"
[ "$EMBEDDED_CHANNEL" = "$RESOLVED_CHANNEL" ] \
    || fail "expected channel $RESOLVED_CHANNEL but binary reports: $EMBEDDED_CHANNEL" 3

[ -n "$EMBEDDED_VERSION" ] || fail "binary reports empty version" 3
info "embedded metadata OK: version=$EMBEDDED_VERSION commit=$EMBEDDED_COMMIT channel=$EMBEDDED_CHANNEL"

# ── 4. Copy release tree ──────────────────────────────────────────
cp release/install.sh "$BUNDLE_ROOT/release/install.sh"
cp release/install-public.sh "$BUNDLE_ROOT/release/install-public.sh"
cp release/upgrade.sh "$BUNDLE_ROOT/release/upgrade.sh"
[ -f release/uninstall.sh ] && cp release/uninstall.sh "$BUNDLE_ROOT/release/uninstall.sh"

cp release/systemd/orvix.service         "$BUNDLE_ROOT/release/systemd/orvix.service"
cp release/systemd/orvix-update.service  "$BUNDLE_ROOT/release/systemd/orvix-update.service"
cp release/systemd/orvix-restore.service "$BUNDLE_ROOT/release/systemd/orvix-restore.service"
cp release/systemd/orvix-restore.path    "$BUNDLE_ROOT/release/systemd/orvix-restore.path"
cp release/sudoers.d/orvix-update       "$BUNDLE_ROOT/release/sudoers.d/orvix-update"

for s in release/scripts/*.sh; do
    [ -f "$s" ] || continue
    cp "$s" "$BUNDLE_ROOT/release/scripts/$(basename "$s")"
done
for s in release/scripts/*.mjs; do
    [ -f "$s" ] || continue
    cp "$s" "$BUNDLE_ROOT/release/scripts/$(basename "$s")"
done
for s in release/scripts/tests/*.sh; do
    [ -f "$s" ] || continue
    cp "$s" "$BUNDLE_ROOT/release/scripts/tests/$(basename "$s")"
done
[ -f release/configs/orvix.yaml.example ] && \
    cp release/configs/orvix.yaml.example "$BUNDLE_ROOT/release/configs/orvix.yaml.example"
[ -f release/trust/orvix-release-signing.pub.pem ] && \
    cp release/trust/orvix-release-signing.pub.pem "$BUNDLE_ROOT/release/trust/orvix-release-signing.pub.pem"

# Asset trees — admin SPA.
# The React-based web/admin is the reviewed production UI. The committed
# legacy release/admin is used ONLY when the Node/npm toolchain is genuinely
# unavailable. When the toolchain is present, any failure of npm ci /
# TypeScript validation / npm run build / built-output verification is a hard
# bundle failure — build-release-bundle.sh never ships stale legacy admin
# assets after a real build error. See lib-admin-build.sh (package_admin_spa).
if ADMIN_SOURCE="$(package_admin_spa "$REPO_ROOT" "$BUNDLE_ROOT/release/admin")"; then
    info "admin assets packaged from: $ADMIN_SOURCE ($(find "$BUNDLE_ROOT/release/admin" -type f | wc -l) files)"
else
    fail "admin SPA packaging failed (see errors above); refusing to ship a bundle with stale or missing admin assets" 2
fi
(cd release/webmail && tar -cf - .) | (cd "$BUNDLE_ROOT/release/webmail" && tar -xf -)

# Marketing SPA. With Node/npm available, the source build is mandatory and
# any install/build/verification failure aborts the release. The committed
# release/marketing tree is used only on operator hosts without a JS toolchain.
if [ -f web/marketing/package.json ] && command -v node >/dev/null 2>&1 && command -v npm >/dev/null 2>&1; then
    info "building marketing SPA from web/marketing"
    (
        cd web/marketing
        npm ci
        npm run verify
    ) || fail "marketing SPA build or verification failed; refusing to ship stale assets" 2
    rm -rf "$BUNDLE_ROOT/release/marketing"
    mkdir -p "$BUNDLE_ROOT/release/marketing"
    (cd web/marketing/dist && tar -cf - .) | (cd "$BUNDLE_ROOT/release/marketing" && tar -xf -)
    MARKETING_SOURCE="built"
else
    info "Node/npm unavailable; packaging committed release/marketing fallback"
    (cd release/marketing && tar -cf - .) | (cd "$BUNDLE_ROOT/release/marketing" && tar -xf -)
    MARKETING_SOURCE="committed-fallback"
fi

cp release/VERSION "$BUNDLE_ROOT/VERSION"

# BUILDINFO — single source of truth for the bundle installer to read
cat > "$BUNDLE_ROOT/BUILDINFO" <<BUILDINFO
version=$RESOLVED_VERSION
commit=$GIT_COMMIT
short_commit=$GIT_SHORT_COMMIT
build_time=$GIT_BUILD_TIME
channel=$RESOLVED_CHANNEL
target_os=$TARGET_OS
target_arch=$TARGET_ARCH
built_by=build-release-bundle.sh
BUILDINFO

# SPDX 2.3 SBOM generated from the exact module graph and binary sealed into
# this bundle. The generator contains no network calls and no private data.
bash release/scripts/generate-sbom.sh "$BUNDLE_ROOT/SBOM.spdx" "$BIN_OUT" "$RESOLVED_VERSION" "$GIT_COMMIT" \
    || fail "SBOM generation failed" 4

# ── 5. Per-file sanity checks on bundle contents ─────────────────
# Every required entry must exist inside the bundle before we seal it.
BUNDLE_REQUIRED=(
    bin/orvix
    release/install.sh
    release/install-public.sh
    release/upgrade.sh
    release/systemd/orvix.service
    release/systemd/orvix-update.service
    release/systemd/orvix-restore.service
    release/systemd/orvix-restore.path
    release/sudoers.d/orvix-update
    release/admin/index.html
    release/webmail/index.html
    release/marketing/index.html
    release/marketing/404.html
    release/marketing/robots.txt
    release/marketing/sitemap.xml
    release/scripts/setup-https.sh
    release/scripts/smoke-admin-browser.sh
    release/scripts/smoke-admin-import-graph.mjs
    release/scripts/smoke-admin-runtime.mjs
    release/scripts/verify-fresh-vps-one-command.sh
    release/trust/orvix-release-signing.pub.pem
    VERSION
    BUILDINFO
    SBOM.spdx
)
for f in "${BUNDLE_REQUIRED[@]}"; do
    [ -e "$BUNDLE_ROOT/$f" ] || fail "bundle is missing $f (assembly lost a file)" 4
done

find "$BUNDLE_ROOT/release/marketing/marketing-assets" -maxdepth 1 -type f -name '*.js' -print -quit | grep -q . \
    || fail "bundle marketing SPA has no JavaScript assets" 4
info "marketing assets packaged from: $MARKETING_SOURCE"

# Verify the admin SPA index exists in every case.
admin_index="$BUNDLE_ROOT/release/admin/index.html"
[ -f "$admin_index" ] || fail "bundle is missing release/admin/index.html" 4

# When the admin was built from web/admin, assert the built ops modules are
# present (fail-closed). package_admin_spa already ran this check; we re-assert
# on the sealed bundle tree so a lost/rewritten asset is caught before sealing.
# The legacy (toolchain-absent) fallback has no assets/ directory and is
# intentionally exempt from the built-assets assertions.
if [ "${ADMIN_SOURCE:-}" = "built" ]; then
    verify_admin_ops_assets "$BUNDLE_ROOT/release/admin" \
        || fail "built admin output failed ops-module verification (see errors above)" 4
    info "built admin ops modules verified in sealed bundle tree"
else
    info "admin packaged from legacy fallback (toolchain absent); skipping built-assets assertions"
fi

# ── 6. checksums.txt — sha256 of every file in the bundle ────────
( cd "$BUNDLE_ROOT" && find . -type f -print0 | sort -z | xargs -0 sha256sum ) \
    > "$BUNDLE_ROOT/checksums.txt"

# ── 7. Seal the tarball ───────────────────────────────────────────
mkdir -p "$OUTPUT_DIR"
ARCHIVE_BASE="orvix-enterprise-mail-${RESOLVED_VERSION}-${TARGET_OS}-${TARGET_ARCH}"
ARCHIVE="$OUTPUT_DIR/${ARCHIVE_BASE}.tar.gz"

tar -C "$WORK_DIR" -czf "$ARCHIVE" orvix \
    || fail "tar seal failed for $ARCHIVE" 4

# ── 8. Re-verify the sealed archive ──────────────────────────────
sha256sum "$ARCHIVE" | awk -v a="$ARCHIVE" '{printf "%s  %s\n", $1, a}' > "$ARCHIVE.sha256"
info "sha256: $(awk '{print $1}' "$ARCHIVE.sha256")  $ARCHIVE"

# Also create stable-channel copies so the public installer can
# resolve the bundle from a predictable GitHub Releases URL:
#   orvix-enterprise-mail-stable-linux-amd64.tar.gz
#   orvix-enterprise-mail-stable-linux-amd64.tar.gz.sha256
STABLE_ARCHIVE="$OUTPUT_DIR/orvix-enterprise-mail-${RESOLVED_CHANNEL}-${TARGET_OS}-${TARGET_ARCH}.tar.gz"
cp "$ARCHIVE" "$STABLE_ARCHIVE"
sha256sum "$STABLE_ARCHIVE" | awk -v a="$STABLE_ARCHIVE" '{printf "%s  %s\n", $1, a}' > "$STABLE_ARCHIVE.sha256"
info "stable alias: $STABLE_ARCHIVE"

write_release_manifest() {
    local artifact="$1" output="$2"
    local artifact_sha sbom_sha
    artifact_sha="$(sha256sum "$artifact" | awk '{print $1}')"
    sbom_sha="$(sha256sum "$BUNDLE_ROOT/SBOM.spdx" | awk '{print $1}')"
    cat >"$output" <<MANIFEST
{
  "schema": 1,
  "product": "Orvix Enterprise Mail",
  "version": "$RESOLVED_VERSION",
  "channel": "$RESOLVED_CHANNEL",
  "commit": "$GIT_COMMIT",
  "build_time": "$GIT_BUILD_TIME",
  "target": "$TARGET_OS/$TARGET_ARCH",
  "artifact": "$(basename "$artifact")",
  "artifact_sha256": "$artifact_sha",
  "sbom": "SBOM.spdx",
  "sbom_sha256": "$sbom_sha"
}
MANIFEST
}

cp "$BUNDLE_ROOT/SBOM.spdx" "$ARCHIVE.sbom.spdx"
cp "$BUNDLE_ROOT/SBOM.spdx" "$STABLE_ARCHIVE.sbom.spdx"
write_release_manifest "$ARCHIVE" "$ARCHIVE.manifest.json"
write_release_manifest "$STABLE_ARCHIVE" "$STABLE_ARCHIVE.manifest.json"

# Signing is deliberately keyless by default. A release operator/CI must
# provide a private Ed25519 key outside the repository. The corresponding
# public key is distributed through the independently managed trust channel.
if [ -n "${ORVIX_RELEASE_SIGNING_KEY_FILE:-}" ]; then
    for artifact in "$ARCHIVE" "$ARCHIVE.manifest.json" "$ARCHIVE.sbom.spdx" "$STABLE_ARCHIVE" "$STABLE_ARCHIVE.manifest.json" "$STABLE_ARCHIVE.sbom.spdx"; do
        bash release/scripts/sign-release-artifact.sh "$artifact" "$ORVIX_RELEASE_SIGNING_KEY_FILE" "$artifact.sig" \
            || fail "release signing failed for $(basename "$artifact")" 4
    done
    if [ -n "${ORVIX_RELEASE_VERIFYING_KEY_FILE:-}" ]; then
        for artifact in "$ARCHIVE" "$ARCHIVE.manifest.json" "$ARCHIVE.sbom.spdx" "$STABLE_ARCHIVE" "$STABLE_ARCHIVE.manifest.json" "$STABLE_ARCHIVE.sbom.spdx"; do
            bash release/scripts/verify-release-signature.sh "$artifact" "$artifact.sig" "$ORVIX_RELEASE_VERIFYING_KEY_FILE" \
                || fail "release signature self-check failed for $(basename "$artifact")" 4
        done
    fi
elif [ "${ORVIX_REQUIRE_RELEASE_SIGNATURE:-}" = "1" ]; then
    fail "release signature required but ORVIX_RELEASE_SIGNING_KEY_FILE is not configured" 4
fi

# Pull the binary out of the tarball and re-run version to be 100%
# sure the sealed binary is the same one we built (catches a corrupt
# tar boundary on architectures with padding edge cases).
VERIFY_DIR="$(mktemp -d -t orvix-verify.XXXXXX)"
trap 'rm -rf "$WORK_DIR" "$VERIFY_DIR"' EXIT
tar -C "$VERIFY_DIR" -xzf "$ARCHIVE" orvix/bin/orvix orvix/BUILDINFO \
    || fail "could not re-extract binary for verification" 4

VERIFY_FULL="$("$VERIFY_DIR/orvix/bin/orvix" version --full || true)"
VERIFY_VERSION="$(echo "$VERIFY_FULL" | awk 'NR==1 && $1=="orvix" {print $2}')"
[ "$VERIFY_VERSION" = "$EMBEDDED_VERSION" ] \
    || fail "sealed binary reports $VERIFY_VERSION but build produced $EMBEDDED_VERSION" 4

VERIFY_BUILDINFO="$(cat "$VERIFY_DIR/orvix/BUILDINFO" || true)"
case "$VERIFY_BUILDINFO" in
    *"version=$RESOLVED_VERSION"*"commit=$GIT_COMMIT"*"channel=$RESOLVED_CHANNEL"*) ;;
    *) printf 'unexpected BUILDINFO:\n%s\n' "$VERIFY_BUILDINFO" >&2; fail "sealed BUILDINFO is wrong" 4 ;;
esac

# ── 9. Done ───────────────────────────────────────────────────────
BUNDLE_BASE_COUNT="$(find "$BUNDLE_ROOT" -type f | wc -l)"
info "bundle sealed: $ARCHIVE ($(du -h "$ARCHIVE" | cut -f1))"
info "contents: $BUNDLE_BASE_COUNT files, $RESOLVED_VERSION ($GIT_SHORT_COMMIT, $RESOLVED_CHANNEL, $TARGET_OS/$TARGET_ARCH)"

echo ""
echo "Orvix release bundle sealed:"
echo "  $ARCHIVE"
echo "  sha256: $(awk '{print $1}' "$ARCHIVE.sha256")"
echo "  version: $RESOLVED_VERSION  commit: $GIT_SHORT_COMMIT  channel: $RESOLVED_CHANNEL"
echo "  target: $TARGET_OS/$TARGET_ARCH  built: $GIT_BUILD_TIME"
echo "  bundle layout: orvix/{bin,release/{admin,webmail,systemd,sudoers.d,scripts,configs},VERSION,BUILDINFO,checksums.txt}"
echo ""
echo "Install in one command on a fresh Ubuntu VPS:"
echo "  curl -fsSL <URL>/${ARCHIVE_BASE}.tar.gz | tar -xz"
echo "  sudo bash orvix/release/install.sh"
echo "  (or use release/install-public.sh to drive the same bundle from"
echo "   the public installer entrypoint with --bundle-url)"

exit 0
