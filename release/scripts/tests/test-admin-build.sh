#!/usr/bin/env bash
# test-admin-build.sh — regression tests for the BLOCKER 3 contract in
# lib-admin-build.sh: legacy release/admin is used ONLY when the Node/npm
# toolchain is genuinely absent; with the toolchain present, npm ci / build
# failures and missing/invalid built output are HARD failures (no stale
# legacy fallback).
#
# The tests build a throwaway repo layout and put stub `node`/`npm` binaries
# on PATH so no real network/build is needed. They never touch the real repo.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=release/scripts/lib-admin-build.sh
. "$SCRIPT_DIR/lib-admin-build.sh"

PASSED=0
FAILED=0
SKIPPED=0
pass() { echo "  PASS $*"; PASSED=$((PASSED + 1)); }
failt() { echo "  FAIL $*"; FAILED=$((FAILED + 1)); }
skip() { echo "  SKIP $*"; SKIPPED=$((SKIPPED + 1)); }

# make_repo <root> <with_web_admin:0|1>
make_repo() {
    local root="$1" with_web="$2"
    mkdir -p "$root/release/admin/modules"
    printf '<!doctype html><div id="root"></div>' >"$root/release/admin/index.html"
    printf 'console.log("legacy");' >"$root/release/admin/app.js"
    if [ "$with_web" = "1" ]; then
        mkdir -p "$root/web/admin"
        printf '{"name":"orvix-admin","scripts":{"build":"node build.js"}}' >"$root/web/admin/package.json"
        printf '{}' >"$root/web/admin/tsconfig.json"
    fi
}

# install_stub_toolchain <bindir> <npm_ci_rc> <build_rc> <tsc_rc> <emit_ops:0|1>
# Writes stub `node`, `npm`, and `npx` that honour the requested exit codes and
# (optionally) emit a dist/ tree containing the ops-module markers.
install_stub_toolchain() {
    local bindir="$1" npm_ci_rc="$2" build_rc="$3" tsc_rc="$4" emit_ops="$5"
    mkdir -p "$bindir"
    cat >"$bindir/node" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
    cat >"$bindir/npm" <<EOF
#!/usr/bin/env bash
cmd="\${1:-}"
if [ "\$cmd" = "ci" ]; then exit $npm_ci_rc; fi
if [ "\$cmd" = "run" ] && [ "\${2:-}" = "build" ]; then
    if [ "$build_rc" -ne 0 ]; then exit $build_rc; fi
    mkdir -p dist/assets
    printf '<!doctype html><script src="/assets/index-abc.js"></script>' > dist/index.html
    if [ "$emit_ops" = "1" ]; then
         printf 'fetch("/api/v1/admin/backups");fetch("/api/v1/monitoring/health");' > dist/assets/index-abc.js
    else
        printf 'console.log("no ops here");' > dist/assets/index-abc.js
    fi
    exit 0
fi
exit 0
EOF
    cat >"$bindir/npx" <<EOF
#!/usr/bin/env bash
# Only 'tsc' is expected.
exit $tsc_rc
EOF
    chmod +x "$bindir/node" "$bindir/npm" "$bindir/npx"
}

run_case() {
    local name="$1"; shift
    local tmp; tmp="$(mktemp -d)"
    ( eval "$@" ) # runs the case body in a subshell
    local rc=$?
    rm -rf "$tmp"
    return $rc
}

# ── 0. No React source present → documented legacy fallback ───────
# Exercises the legacy fallback + copy under a normal (working) PATH, so it
# runs everywhere.
t_no_web_admin_source() {
    local root; root="$(mktemp -d)"; make_repo "$root" 0 # no web/admin
    local out
    out="$(package_admin_spa "$root" "$root/out-admin" 2>/dev/null)"
    local rc=$?
    local ok=1
    [ -f "$root/out-admin/index.html" ] || ok=0
    [ -f "$root/out-admin/app.js" ] || ok=0 # legacy tree copied
    rm -rf "$root"
    if [ $rc -eq 0 ] && [ "$out" = "legacy" ] && [ $ok -eq 1 ]; then
        pass "no web/admin source → legacy fallback"
    else
        failt "no-web-admin expected legacy/rc0/copied, got rc=$rc out='$out' ok=$ok"
    fi
}

# ── 1. Toolchain absent → documented legacy fallback ──────────────
t_toolchain_absent() {
    local root; root="$(mktemp -d)"; make_repo "$root" 1
    # A PATH containing the coreutils the legacy copy needs (tar/rm/mkdir)
    # but deliberately NOT node/npm, so command -v node fails.
    local toolbin; toolbin="$(mktemp -d)"
    local u src
    for u in tar rm mkdir cp find grep wc tr cat dirname basename; do
        src="$(command -v "$u" 2>/dev/null)" || continue
        ln -s "$src" "$toolbin/$u" 2>/dev/null || cp "$src" "$toolbin/$u" 2>/dev/null || true
    done
    # Some environments (e.g. msys/Git Bash) cannot run coreutils from an
    # isolated PATH because of DLL colocation. If tar cannot run here, skip:
    # the Linux CI runner exercises this case for real.
    if ! PATH="$toolbin" tar --version >/dev/null 2>&1; then
        rm -rf "$root" "$toolbin"
        skip "toolchain absent (isolated PATH cannot run coreutils in this env)"
        return
    fi
    local out
    out="$(PATH="$toolbin" package_admin_spa "$root" "$root/out-admin" 2>/dev/null)"
    local rc=$?
    local ok=1
    [ -f "$root/out-admin/index.html" ] || ok=0 # legacy index copied
    rm -rf "$root" "$toolbin"
    if [ $rc -eq 0 ] && [ "$out" = "legacy" ] && [ $ok -eq 1 ]; then
        pass "toolchain absent → legacy fallback"
    else
        failt "toolchain absent expected legacy/rc0/copied, got rc=$rc out='$out' ok=$ok"
    fi
}

# ── 2. Toolchain present + build succeeds → built admin packaged ──
t_build_success() {
    local root; root="$(mktemp -d)"; make_repo "$root" 1
    local bin; bin="$(mktemp -d)"; install_stub_toolchain "$bin" 0 0 0 1
    local out
    out="$(PATH="$bin:$PATH" package_admin_spa "$root" "$root/out-admin" 2>/dev/null)"
    local rc=$?
    local ok=1
    [ -f "$root/out-admin/index.html" ] || ok=0
    grep -qF "/api/v1/admin/backups" "$root/out-admin/assets/index-abc.js" 2>/dev/null || ok=0
    rm -rf "$root" "$bin"
    if [ $rc -eq 0 ] && [ "$out" = "built" ] && [ $ok -eq 1 ]; then
        pass "toolchain present + build ok → built admin packaged"
    else
        failt "build-success expected built/rc0/ok, got rc=$rc out='$out' ok=$ok"
    fi
}

# ── 3. Toolchain present + npm ci fails → bundle fails ────────────
t_npm_ci_fails() {
    local root; root="$(mktemp -d)"; make_repo "$root" 1
    local bin; bin="$(mktemp -d)"; install_stub_toolchain "$bin" 7 0 0 1
    local out
    out="$(PATH="$bin:$PATH" package_admin_spa "$root" "$root/out-admin" 2>/dev/null)"
    local rc=$?
    local legacy_leak=0
    # A stale legacy fallback would have copied app.js; assert it did NOT.
    [ -f "$root/out-admin/app.js" ] && legacy_leak=1
    rm -rf "$root" "$bin"
    if [ $rc -ne 0 ] && [ "$out" != "legacy" ] && [ $legacy_leak -eq 0 ]; then
        pass "npm ci fails → hard failure, no legacy fallback"
    else
        failt "npm-ci-fail expected non-zero + no legacy, got rc=$rc out='$out' legacy_leak=$legacy_leak"
    fi
}

# ── 4. Toolchain present + build fails → bundle fails ─────────────
t_build_fails() {
    local root; root="$(mktemp -d)"; make_repo "$root" 1
    local bin; bin="$(mktemp -d)"; install_stub_toolchain "$bin" 0 5 0 1
    local out
    out="$(PATH="$bin:$PATH" package_admin_spa "$root" "$root/out-admin" 2>/dev/null)"
    local rc=$?
    rm -rf "$root" "$bin"
    if [ $rc -ne 0 ] && [ "$out" != "legacy" ]; then
        pass "npm run build fails → hard failure, no legacy fallback"
    else
        failt "build-fail expected non-zero + no legacy, got rc=$rc out='$out'"
    fi
}

# ── 4b. Toolchain present + TypeScript validation fails → fails ───
t_tsc_fails() {
    local root; root="$(mktemp -d)"; make_repo "$root" 1
    local bin; bin="$(mktemp -d)"; install_stub_toolchain "$bin" 0 0 3 1
    local out
    out="$(PATH="$bin:$PATH" package_admin_spa "$root" "$root/out-admin" 2>/dev/null)"
    local rc=$?
    rm -rf "$root" "$bin"
    if [ $rc -ne 0 ] && [ "$out" != "legacy" ]; then
        pass "tsc --noEmit fails → hard failure, no legacy fallback"
    else
        failt "tsc-fail expected non-zero + no legacy, got rc=$rc out='$out'"
    fi
}

# ── 5. Output missing expected ops assets → bundle fails ──────────
t_output_missing_assets() {
    local root; root="$(mktemp -d)"; make_repo "$root" 1
    local bin; bin="$(mktemp -d)"; install_stub_toolchain "$bin" 0 0 0 0 # emit_ops=0
    local out
    out="$(PATH="$bin:$PATH" package_admin_spa "$root" "$root/out-admin" 2>/dev/null)"
    local rc=$?
    rm -rf "$root" "$bin"
    if [ $rc -ne 0 ] && [ "$out" != "built" ]; then
        pass "built output missing ops markers → hard failure"
    else
        failt "missing-assets expected non-zero, got rc=$rc out='$out'"
    fi
}

echo "=== lib-admin-build.sh regression tests ==="
t_no_web_admin_source
t_toolchain_absent
t_build_success
t_npm_ci_fails
t_build_fails
t_tsc_fails
t_output_missing_assets

echo ""
echo "passed=$PASSED failed=$FAILED skipped=$SKIPPED"
[ "$FAILED" -eq 0 ] || exit 1
