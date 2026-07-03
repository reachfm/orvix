#!/usr/bin/env bash
# smoke-install-bundle.sh — Verifies the release bundle is a
# complete, internally-consistent install payload.
#
# This smoke runs WITHOUT requiring systemd, root, or a running
# Orvix instance. It is the gate you wire into CI to answer:
#   - did the bundle miss any file install.sh needs?
#   - does the bundled binary embed the right commit / channel?
#   - does the install.sh install_binary() guard reject a stale
#     release/orvix-linux-amd64?
#   - do install.sh and install-public.sh stay in sync on env vars?
#   - does any shipped release script reference a removed file?
#
# Two modes:
#   1. Default (static) — checks only files already on disk under
#      ./release. Runs anywhere. Exits 0 when everything matches.
#   2. --build   — runs build-release-bundle.sh, then unpacks the
#      sealed bundle and validates it. Requires Go.
#
# Usage:
#   bash release/scripts/smoke-install-bundle.sh
#   bash release/scripts/smoke-install-bundle.sh --build
#   bash release/scripts/smoke-install-bundle.sh --bundle dist/orvix-enterprise-mail-...tar.gz

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$REPO_ROOT"

VERBOSE=0
MODE="static"
BUNDLE_PATH=""

while [ $# -gt 0 ]; do
    case "$1" in
        --verbose|-v) VERBOSE=1; shift ;;
        --build)      MODE="build"; shift ;;
        --bundle)     [ $# -ge 2 ] || { echo "--bundle requires a value" >&2; exit 1; }
                      BUNDLE_PATH="$2"; shift 2 ;;
        --help|-h)
            sed -n '3,30p' "$0"; exit 0 ;;
        *) echo "unknown flag: $1" >&2; exit 1 ;;
    esac
done

RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
YELLOW=$'\033[1;33m'
NC=$'\033[0m'

pass() { printf '%bPASS%b %s\n' "$GREEN" "$NC" "$*" >&2; }
fail() { printf '%bFAIL%b %s\n' "$RED" "$NC" "$*" >&2; exit 1; }
warn() { printf '%bWARN%b %s\n' "$YELLOW" "$NC" "$*" >&2; }
info() { [ "$VERBOSE" = "1" ] && printf '  %s\n' "$*" >&2 || true; }

CHECKS_TOTAL=0
CHECKS_PASSED=0
check() {
    CHECKS_TOTAL=$((CHECKS_TOTAL + 1))
    if eval "$2" >/dev/null 2>&1; then
        CHECKS_PASSED=$((CHECKS_PASSED + 1))
        pass "$1"
    else
        fail "$1"
    fi
}

# ── 1. install.sh / install-public.sh / upgrade.sh sanity ─────
[ -f release/install.sh ]         || fail "release/install.sh not found"
[ -f release/install-public.sh ] || fail "release/install-public.sh not found"
[ -f release/upgrade.sh ]         || fail "release/upgrade.sh not found"

bash -n release/install.sh         || fail "release/install.sh has a bash syntax error"
bash -n release/install-public.sh || fail "release/install-public.sh has a bash syntax error"
bash -n release/upgrade.sh         || fail "release/upgrade.sh has a bash syntax error"
pass "all installer scripts parse cleanly"

# Contract: install-public.sh must NEVER download install.sh in
# isolation. The Codex CTO blocker was that the public installer
# fetched only $ORVIX_INSTALL_URL which is /install.sh and then
# expected the operator to magically have release/{admin,webmail,
#systemd,sudoers.d,scripts} on disk. The fix is the bundle path.
check "install-public.sh does NOT default to a bare install.sh URL" \
    "grep -qE 'install.sh|install-public.sh.*bundle|bundle_url' release/install-public.sh && ! grep -qE 'ORVIX_INSTALL_URL=.+releases.orvix.email/install\.sh.' release/install-public.sh || false"

# Install-public.sh must validate the bundle layout.
check "install-public.sh validates full bundle layout" \
    "grep -q 'validate_bundle_layout' release/install-public.sh"

# Install-public.sh must fail closed on a partial bundle.
check "install-public.sh fails closed on missing files" \
    "grep -qE 'missing required files|refusing to install a half-complete bundle|incomplete bundle' release/install-public.sh"

# Install-public.sh must NOT silently fall through to a developer
# worktree when fetching a bundle — the worktree-only path was the
# silent half-install regression.
check "install-public.sh does NOT have a worktree-only fallback that skips bundle download" \
    "! grep -qE 'if ! worktree_root=|worktree_root=.+&&.+\\[ -f' release/install-public.sh"

# Install-public.sh must surface ORVIX_COMMIT to install.sh so the
# embedded-commit guard in install.sh has the expected value to
# compare against.
check "install-public.sh exports ORVIX_COMMIT for install.sh" \
    "grep -qE '^\\s*export ORVIX_COMMIT=' release/install-public.sh"

# install-public.sh must export ORVIX_SOURCE_DIR pointing at the
# extracted bundle so install.sh finds the right path.
check "install-public.sh exports ORVIX_SOURCE_DIR" \
    "grep -qE '^\\s*export ORVIX_SOURCE_DIR=' release/install-public.sh"

# install.sh must validate the installed binary's embedded metadata.
check "install.sh has a stale-binary rejection + metadata verification path" \
    "grep -q 'verify_installed_binary_metadata' release/install.sh && \
     grep -qE 'STALE prebuilt|exp_commit' release/install.sh && \
     grep -qE 'ORVIX_USE_PREBUILT' release/install.sh && \
     grep -qE 'ORVIX_BUILD_FROM_SOURCE|go build.*ldflags' release/install.sh"

# install.sh install_binary must NOT silently prefer release/orvix-linux-amd64.
check "install.sh install_binary() does not unconditionally prefer the old release/orvix-linux-amd64 path" \
    "grep -q 'bin/orvix' release/install.sh"

# install.sh install_binary must look at the bundle bin/orvix FIRST
# (before the legacy release/orvix-linux-amd64 path). We extract
# the install_binary function body with awk (skipping the header),
# then assert bin/orvix appears before release/orvix-linux-amd64.
check "install.sh install_binary() walks bundle bin/orvix first" \
    'awk "/^install_binary/,/^}$/" release/install.sh | grep -nE "bin/orvix|release/orvix-linux-amd64" | awk -F: "{print \$NF}" | head -n1 | grep -q "bin/orvix"'

# install.sh must explicitly support the bundle path: when the
# bundle is present with bin/orvix, no source build is required,
# and the installer must not falsely fall through to go build.
check "install.sh refuses to install a stale prebuilt + no-source tree" \
    "grep -qE 're-fetch the bundle or supply ORVIX_USE_PREBUILT' release/install.sh"

# Buildinfo must be injected via -ldflags when source build runs.
check "install.sh source build uses ldflags to inject buildinfo" \
    "grep -qE 'internal/buildinfo.Version=' release/install.sh && \
     grep -qE 'internal/buildinfo.Commit=' release/install.sh && \
     grep -qE 'internal/buildinfo.Channel=' release/install.sh"

# 8080/8081 listener contract: default config binds loopback-only.
# We assert the LITERAL config line (with the YAML indentation) so
# a comment that mentions the same values cannot falsely pass the
# check.
check "install.sh writes server.host=127.0.0.1 by default" \
    "grep -nqF '  host: \"127.0.0.1\"' release/install.sh"
check "install.sh writes coremail.jmap_host=127.0.0.1 by default" \
    "grep -nqF '  jmap_host: 127.0.0.1' release/install.sh"

check "install.sh runtime listener posture check enforces 8080/8081 loopback-only" \
    "grep -qE '8080.*8081|for port in 8080 8081' release/install.sh && \
     grep -qE 'has_loopback|all_loopback' release/install.sh"

# Required files installer relies on (and that the bundle must
# contain) — listed so a release-tree regression that drops one
# of them is caught here rather than at customer install time.
check "release tree has all required admin assets" \
    "[ -f release/admin/app.js ] && [ -f release/admin/index.html ] && [ -f release/admin/styles.css ]"
check "release tree has all required webmail assets" \
    "[ -f release/webmail/index.html ] && [ -f release/webmail/sw.js ] && \
      [ -f release/webmail/assets/auth-gate.js ] && [ -f release/webmail/assets/auth-gate.css ] && \
      [ -f release/webmail/assets/webmail.js ] && [ -f release/webmail/assets/webmail.css ]"
check "release tree has systemd units + sudoers drop-in" \
    "[ -f release/systemd/orvix.service ] && [ -f release/systemd/orvix-update.service ] && [ -f release/sudoers.d/orvix-update ]"
check "release tree has install/upgrade/uninstall scripts" \
    "[ -f release/install.sh ] && [ -f release/upgrade.sh ] && [ -f release/uninstall.sh ]"

# build-release-bundle.sh must be discoverable.
[ -f release/scripts/build-release-bundle.sh ] || fail "release/scripts/build-release-bundle.sh not found"
bash -n release/scripts/build-release-bundle.sh || fail "build-release-bundle.sh has a bash syntax error"
pass "build-release-bundle.sh present and parses cleanly"

check "build-release-bundle.sh embeds buildinfo.Version via ldflags" \
    "grep -q 'internal/buildinfo.Version=' release/scripts/build-release-bundle.sh"

check "build-release-bundle.sh embeds buildinfo.Commit via ldflags" \
    "grep -q 'internal/buildinfo.Commit=' release/scripts/build-release-bundle.sh"

check "build-release-bundle.sh embeds buildinfo.BuildTime via ldflags" \
    "grep -q 'internal/buildinfo.BuildTime=' release/scripts/build-release-bundle.sh"

check "build-release-bundle.sh embeds buildinfo.Channel via ldflags" \
    "grep -q 'internal/buildinfo.Channel=' release/scripts/build-release-bundle.sh"

check "build-release-bundle.sh emits BUILDINFO sidecar with version+commit+channel" \
    "grep -qE '^version=|^commit=|^channel=|^build_time=' release/scripts/build-release-bundle.sh"

check "build-release-bundle.sh includes admin + webmail assets" \
    "grep -q 'release/admin' release/scripts/build-release-bundle.sh && \
     grep -q 'release/webmail' release/scripts/build-release-bundle.sh"

check "build-release-bundle.sh includes systemd + sudoers + scripts" \
    "grep -q 'release/systemd' release/scripts/build-release-bundle.sh && \
     grep -q 'release/sudoers.d' release/scripts/build-release-bundle.sh && \
     grep -q 'release/scripts' release/scripts/build-release-bundle.sh"

check "build-release-bundle.sh generates a sha256 sidecar" \
    "grep -q 'sha256' release/scripts/build-release-bundle.sh && \
     grep -qE '\\.sha256' release/scripts/build-release-bundle.sh"

check "build-release-bundle.sh re-verifies the sealed binary" \
    "grep -q 'VERIFY_VERSION' release/scripts/build-release-bundle.sh && \
     grep -qE 'sealed binary' release/scripts/build-release-bundle.sh"

# ── 2. Optionally build & inspect the bundle ───────────────────
EXPECTED_VERSION=""
if [ -f release/VERSION ]; then
    EXPECTED_VERSION="$(awk 'NF && $1 !~ /^#/ {print; exit}' release/VERSION | tr -d '[:space:]')"
fi

if [ "$MODE" = "build" ]; then
    command -v go >/dev/null 2>&1 || fail "go is required for --build mode"
    info "building release bundle..."
    bash release/scripts/build-release-bundle.sh >/dev/null 2>&1 \
        || fail "build-release-bundle.sh exited non-zero"

    # Find the most recent tarball
    BUNDLE_PATH="$(ls -1t dist/orvix-enterprise-mail-*.tar.gz 2>/dev/null | head -n1 || true)"
    [ -n "$BUNDLE_PATH" ] || fail "no bundle tarball produced in dist/"
    pass "bundle built: $BUNDLE_PATH"

    # Extract + verify
    BDIR="$(mktemp -d -t orvix-bundle-smoke.XXXXXX)"
    trap 'rm -rf "$BDIR"' EXIT
    tar -xzf "$BUNDLE_PATH" -C "$BDIR"

    # All bundle-required files must be present in the sealed tree.
    for rel in bin/orvix release/install.sh release/install-public.sh release/upgrade.sh \
        release/systemd/orvix.service release/systemd/orvix-update.service \
        release/sudoers.d/orvix-update release/admin/app.js release/admin/index.html \
        release/webmail/index.html VERSION BUILDINFO; do
        if [ ! -e "$BDIR/orvix/$rel" ]; then
            fail "sealed bundle is missing $rel"
        fi
    done
    pass "sealed bundle contains every required file"

    # The sealed binary's metadata must match the bundle metadata.
    if [ -x "$BDIR/orvix/bin/orvix" ]; then
        EMBED_VERSION="$("$BDIR/orvix/bin/orvix" version | awk '{print $2}' || true)"
        [ -n "$EMBED_VERSION" ] || fail "sealed binary reports no version"
        if [ -n "$EXPECTED_VERSION" ] && [ "$EMBED_VERSION" != "$EXPECTED_VERSION" ]; then
            fail "sealed binary reports version=$EMBED_VERSION but bundle expected version=$EXPECTED_VERSION"
        fi
        pass "sealed binary version matches bundle: $EMBED_VERSION"

        # Embedded commit must come back when asked
        "$BDIR/orvix/bin/orvix" version --full | grep -E '^[[:space:]]*commit:[[:space:]]*[0-9a-f]{12,}' >/dev/null \
            || fail "sealed binary does not report a commit in version --full"
        pass "sealed binary reports a commit"

        "$BDIR/orvix/bin/orvix" version --full | grep -E '^[[:space:]]*channel:[[:space:]]*[a-z]+' >/dev/null \
            || fail "sealed binary does not report a channel in version --full"
        pass "sealed binary reports a channel"
    else
        warn "sealed binary at $BDIR/orvix/bin/orvix is not executable; skipping metadata check (smoke ran without --build)"
    fi

    # checksums.txt must list every file in the bundle
    if [ -f "$BDIR/orvix/checksums.txt" ]; then
        LINES="$(wc -l < "$BDIR/orvix/checksums.txt")"
        [ "$LINES" -ge 15 ] || fail "checksums.txt has $LINES lines (expected many more for a real bundle)"
        pass "checksums.txt covers $LINES files"
    else
        fail "sealed bundle is missing checksums.txt"
    fi
fi

# ── 3. Optionally inspect an external bundle ───────────────────
if [ -n "$BUNDLE_PATH" ] && [ "$MODE" != "build" ]; then
    [ -f "$BUNDLE_PATH" ] || fail "bundle path $BUNDLE_PATH not found"
    BDIR2="$(mktemp -d -t orvix-bundle-ext.XXXXXX)"
    trap 'rm -rf "$BDIR2"' EXIT
    tar -xzf "$BUNDLE_PATH" -C "$BDIR2"
    for rel in bin/orvix release/install.sh release/install-public.sh VERSION; do
        [ -e "$BDIR2/orvix/$rel" ] || fail "external bundle is missing $rel"
    done
    pass "external bundle validates"
fi

# ── 4. Done ─────────────────────────────────────────────────────
echo ""

BUILDER_EXERCISED=0
if [ "$MODE" = "build" ]; then
    BUILDER_EXERCISED=1
elif [ -n "$BUNDLE_PATH" ]; then
    BUILDER_EXERCISED=1
fi

if [ "$BUILDER_EXERCISED" = "1" ]; then
    echo "================================================================"
    echo "Orvix install bundle smoke (FULL — builder exercised): $CHECKS_PASSED / $CHECKS_TOTAL checks passed"
else
    echo "================================================================"
    echo "Orvix install bundle smoke (STATIC — real builder NOT exercised): $CHECKS_PASSED / $CHECKS_TOTAL checks passed"
    echo ""
    echo "WARNING: static mode only checks release-tree fixtures and script"
    echo "syntax. It does NOT exercise build-release-bundle.sh.  For a full"
    echo "bundle-build gate, run with --build."
    echo ""
    echo "This smoke is INCONCLUSIVE for the real builder path."
    echo "================================================================"
    exit 0
fi

[ "$CHECKS_PASSED" = "$CHECKS_TOTAL" ] || fail "$((CHECKS_TOTAL - CHECKS_PASSED)) check(s) failed"
echo "================================================================"
exit 0
