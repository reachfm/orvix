#!/usr/bin/env bash
# lib-admin-build.sh — admin SPA build + packaging logic for the release
# bundle. Sourced by build-release-bundle.sh and exercised directly by
# release/scripts/tests/test-admin-build.sh.
#
# Contract (BLOCKER 3):
#   The React web/admin is the reviewed production UI. The committed legacy
#   release/admin tree is an acceptable fallback ONLY when the Node/npm
#   toolchain is genuinely unavailable (node or npm not installed). When node
#   AND npm are present, ANY failure of `npm ci`, TypeScript validation
#   (`tsc --noEmit`), `npm run build`, or built-output verification is a HARD
#   failure. We never ship stale legacy admin assets after a real build error.
#
# This file defines functions only; it has no side effects when sourced.

# verify_admin_ops_assets <admin_dir>
# Asserts the built admin output contains the expected production ops modules.
# Uses stable API-path string literals that survive JS minification, so a
# build that dropped the ops pages fails instead of shipping silently.
verify_admin_ops_assets() {
    local admin_dir="$1"
    local assets_dir="$admin_dir/assets"
    if [ ! -f "$admin_dir/index.html" ]; then
        echo "verify_admin_ops_assets: missing index.html in $admin_dir" >&2
        return 1
    fi
    if [ ! -d "$assets_dir" ]; then
        echo "verify_admin_ops_assets: missing assets/ (no built assets) in $admin_dir" >&2
        return 1
    fi
    local js_count
    js_count=$(find "$assets_dir" -type f -name '*.js' 2>/dev/null | wc -l | tr -d ' ')
    if [ "${js_count:-0}" -lt 1 ]; then
        echo "verify_admin_ops_assets: no built JS assets in $assets_dir" >&2
        return 1
    fi
    local marker
    for marker in "/api/v1/admin/backups" "/api/v1/license" "/api/v1/monitoring/health"; do
        if ! grep -qF -- "$marker" "$assets_dir"/*.js 2>/dev/null; then
            echo "verify_admin_ops_assets: built admin missing expected ops module marker: $marker" >&2
            return 1
        fi
    done
    return 0
}

# _copy_admin_tree <src_dir> <dest_dir>
_copy_admin_tree() {
    local src="$1" dest="$2"
    rm -rf "$dest"
    mkdir -p "$dest"
    ( cd "$src" && tar -cf - . ) | ( cd "$dest" && tar -xf - )
}

# package_admin_spa <repo_root> <dest_admin_dir>
# On success prints exactly "built" or "legacy" on stdout and returns 0.
# On a hard failure prints a diagnostic to stderr and returns non-zero.
package_admin_spa() {
    local repo_root="$1" dest="$2"
    local web_admin="$repo_root/web/admin"
    local legacy="$repo_root/release/admin"

    # No React source present → documented legacy fallback.
    if [ ! -d "$web_admin" ] || [ ! -f "$web_admin/package.json" ]; then
        echo "package_admin_spa: web/admin source absent; packaging committed legacy release/admin (documented fallback)" >&2
        _copy_admin_tree "$legacy" "$dest" || { echo "package_admin_spa: legacy copy failed" >&2; return 1; }
        echo "legacy"
        return 0
    fi

    # Toolchain genuinely unavailable → documented legacy fallback.
    if ! command -v node >/dev/null 2>&1 || ! command -v npm >/dev/null 2>&1; then
        echo "package_admin_spa: Node/npm toolchain not installed; packaging committed legacy release/admin (documented fallback)" >&2
        _copy_admin_tree "$legacy" "$dest" || { echo "package_admin_spa: legacy copy failed" >&2; return 1; }
        echo "legacy"
        return 0
    fi

    # Toolchain present: build fresh. Every failure below is fatal.
    if ! ( cd "$web_admin" && npm ci ); then
        echo "package_admin_spa: 'npm ci' failed with Node/npm present; refusing to ship stale legacy admin assets" >&2
        return 2
    fi
    if [ -f "$web_admin/tsconfig.json" ]; then
        if ! ( cd "$web_admin" && npx --no-install tsc --noEmit -p tsconfig.json ); then
            echo "package_admin_spa: TypeScript validation ('tsc --noEmit') failed; refusing to ship stale legacy admin assets" >&2
            return 2
        fi
    fi
    if ! ( cd "$web_admin" && npm run build ); then
        echo "package_admin_spa: 'npm run build' failed; refusing to ship stale legacy admin assets" >&2
        return 2
    fi
    if [ ! -d "$web_admin/dist" ] || [ ! -f "$web_admin/dist/index.html" ]; then
        echo "package_admin_spa: build produced no dist/index.html; refusing to ship stale legacy admin assets" >&2
        return 2
    fi

    if ! _copy_admin_tree "$web_admin/dist" "$dest"; then
        echo "package_admin_spa: failed to copy built dist into bundle" >&2
        return 2
    fi
    if ! verify_admin_ops_assets "$dest"; then
        echo "package_admin_spa: built admin output failed ops-module verification; refusing to ship" >&2
        return 2
    fi
    echo "built"
    return 0
}
