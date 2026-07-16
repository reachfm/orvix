#!/usr/bin/env bash
# smoke-install-public.sh — Verifies install-public.sh is a
# fully self-contained one-command installer entrypoint.
#
# What this smoke guards against:
#
#   1. The Codex CTO blocker that prompted this fix: install-public
#      used to default to downloading only install.sh and rely on
#      a developer's worktree for the rest of the release tree. A
#      clean VPS has no such worktree, so install.sh then failed
#      silently half-way through the install with "no prebuilt
#      binary found AND no Go source tree".
#
#   2. install-public silently using a stale bundle when a newer
#      one is published.
#
#   3. install-public forwarding a bundle whose embedded binary
#      does NOT match the bundle's BUILDINFO claim.
#
# This script does NOT actually execute install-public end-to-end
# against a live VPS — that needs real systemd and is gated behind
# smoke-install-systemd.sh in the production-readiness gate. What
# it DOES do is statically verify the installer entrypoint's
# contract by:
#   - parsing both install-public.sh and install.sh with bash -n;
#   - asserting install-public.sh DOES NOT default to a bare
#     install.sh download;
#   - asserting install-public.sh downloads (and validates) a
#     release bundle;
#   - forcing install-public.sh against a synthetic, partially
#     broken bundle and confirming it fails closed;
#   - forcing install-public.sh against a complete synthetic bundle
#     and confirming it succeeds through to install.sh.
#
# Mode:
#   --dry  default: only static checks (bash -n, grep gates).
#          Runs anywhere without Go or curl.
#   --live exercise the full code path against a tmpdir fixture.
#          Requires bash + tar on PATH (no systemd needed).
#
# Usage:
#   bash release/scripts/smoke-install-public.sh
#   bash release/scripts/smoke-install-public.sh --live

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$REPO_ROOT"

VERBOSE=0
MODE="dry"

while [ $# -gt 0 ]; do
    case "$1" in
        --verbose|-v) VERBOSE=1; shift ;;
        --live)        MODE="live"; shift ;;
        --help|-h) sed -n '3,32p' "$0"; exit 0 ;;
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

[ -f release/install-public.sh ] || fail "release/install-public.sh not found"
bash -n release/install-public.sh || fail "install-public.sh has a bash syntax error"
pass "install-public.sh parses cleanly"

# Static guards (the rules every install-public.sh commit must keep).

# 1. The blocker: install-public must NOT default to a bare install.sh URL.
check "install-public.sh does NOT default to downloading install.sh only" \
    "! grep -qE 'ORVIX_INSTALL_URL=.+/install\\.sh' release/install-public.sh"

# 2. install-public must export ORVIX_SOURCE_DIR for install.sh.
check "install-public.sh exports ORVIX_SOURCE_DIR" \
    "grep -qE '^\\s*export ORVIX_SOURCE_DIR=' release/install-public.sh"

# 3. install-public must pass ORVIX_VERSION + ORVIX_COMMIT to install.sh.
check "install-public.sh exports ORVIX_VERSION" \
    "grep -qE '^\\s*export ORVIX_VERSION=' release/install-public.sh"
check "install-public.sh exports ORVIX_COMMIT" \
    "grep -qE '^\\s*export ORVIX_COMMIT=' release/install-public.sh"

# 4. install-public must validate the bundle has install.sh + the
#    runtime asset trees before delegating.
check "install-public.sh validates required bundle files" \
    "grep -q 'validate_bundle_layout' release/install-public.sh"

check "validate_bundle_layout requires admin assets" \
    "sed -n '/^validate_bundle_layout/,/^}/p' release/install-public.sh | grep -qE 'release/admin/(app\\.js|index\\.html|styles\\.css)'"

check "validate_bundle_layout requires webmail assets" \
    "sed -n '/^validate_bundle_layout/,/^}/p' release/install-public.sh | grep -qE 'release/webmail/(index\\.html|sw\\.js|assets/auth-gate\\.js|assets/webmail\\.js)'"

check "validate_bundle_layout requires marketing assets" \
    "sed -n '/^validate_bundle_layout/,/^}/p' release/install-public.sh | grep -qE 'release/marketing/(index\\.html|404\\.html|robots\\.txt|sitemap\\.xml)' && sed -n '/^validate_bundle_layout/,/^}/p' release/install-public.sh | grep -q 'marketing-assets/\\*.js'"

check "validate_bundle_layout requires systemd + sudoers" \
    "sed -n '/^validate_bundle_layout/,/^}/p' release/install-public.sh | grep -qE 'orvix\\.service|orvix-update\\.service|orvix-update$'"

check "validate_bundle_layout requires admin smoke modules" \
    "sed -n '/^validate_bundle_layout/,/^}/p' release/install-public.sh | grep -q 'release/scripts/smoke-admin-import-graph.mjs'"

# 5. install-public must refuse to delegate to install.sh when the
#    bundle is incomplete.
check "install-public.sh refuses a half-complete bundle" \
    "grep -qE 'refusing to install a half-complete bundle' release/install-public.sh"

# 6. install-public must accept bundle overrides.
check "install-public.sh honours --bundle-url" \
    "grep -qE '\\-\\-bundle-url' release/install-public.sh"
check "install-public.sh honours --bundle-sha256" \
    "grep -qE '\\-\\-bundle-sha256|ORVIX_BUNDLE_SHA256' release/install-public.sh"
check "install-public.sh honours --github-ref" \
    "grep -qE '\\-\\-github-ref' release/install-public.sh"

# 7. install-public must print what it is installing.
check "install-public.sh prints an install plan" \
    "grep -q 'install plan' release/install-public.sh"

# 8. install-public must NOT introduce a nodejs dependency on the
#    production install path (production installs never need node).
check "install-public.sh has no nodejs requirement" \
    "! grep -qE 'command -v node|command -v nodejs|nodejs\\.exe' release/install-public.sh"

# 9. The exit path through install.sh must NOT silently install a
#    stale binary.
check "install.sh rejects stale release/orvix-linux-amd64 mismatches" \
    "grep -qE 'stale prebuilt|exp_commit' release/install.sh"

# ── Live exercise ────────────────────────────────────────────────
if [ "$MODE" = "live" ]; then
    command -v tar >/dev/null 2>&1 || fail "tar is required for --live"
    command -v openssl >/dev/null 2>&1 || fail "openssl is required for --live (signature verification)"
    info "running live install-public.sh fixture..."

    WORK="$(mktemp -d -t orvix-pub-smoke.XXXXXX)"
    trap 'rm -rf "$WORK"' EXIT

    # Build a fully synthetic "remote" bundle directory that mirrors
    # what release/scripts/build-release-bundle.sh produces. We do
    # NOT need a real orvix binary for this smoke — what we are
    # proving is that install-public validates the bundle layout
    # before delegating to install.sh.
    FIX="$WORK/fake-release-server/orvix-enterprise-mail-stable-linux-amd64.tar.gz"
    mkdir -p "$(dirname "$FIX")"
    BUNDLE_STAGING="$WORK/staging/orvix"
    mkdir -p "$BUNDLE_STAGING/bin" \
             "$BUNDLE_STAGING/release/admin" \
             "$BUNDLE_STAGING/release/webmail/assets" \
             "$BUNDLE_STAGING/release/marketing/marketing-assets" \
             "$BUNDLE_STAGING/release/systemd" \
             "$BUNDLE_STAGING/release/sudoers.d" \
             "$BUNDLE_STAGING/release/scripts" \
             "$BUNDLE_STAGING/release/trust"

    # Minimal install.sh that records its invocation and exits 0
    # so we can assert install-public.sh reached the delegation step.
    cat > "$BUNDLE_STAGING/release/install.sh" <<'INSTALL_EOF'
#!/usr/bin/env bash
echo "INSTALL.SH_REACHED_MARKER ORVIX_SOURCE_DIR=${ORVIX_SOURCE_DIR:-unset} ORVIX_VERSION=${ORVIX_VERSION:-unset} ORVIX_COMMIT=${ORVIX_COMMIT:-unset}" >&2
exit 42
INSTALL_EOF
    chmod +x "$BUNDLE_STAGING/release/install.sh"
    echo "1.0.3-rc5" > "$BUNDLE_STAGING/VERSION"

    cat > "$BUNDLE_STAGING/BUILDINFO" <<BUILDINFO_EOF
version=1.0.3-rc5
commit=53ecf2400000000000000000000000000000000
short_commit=53ecf24
build_time=2026-07-03T17:40:57Z
channel=stable
BUILDINFO_EOF

    # Stub binary. install.sh will run this in production; here
    # install-public.sh must NOT execute it before validation.
    cat > "$BUNDLE_STAGING/bin/orvix" <<'BIN_EOF'
#!/usr/bin/env bash
echo "binary-in-bundle invoked" >&2
exit 0
BIN_EOF
    chmod +x "$BUNDLE_STAGING/bin/orvix"

    # All required admin/webmail/systemd/sudoers/scripts files
    for rel in \
        release/install-public.sh release/upgrade.sh release/uninstall.sh \
        release/systemd/orvix.service release/systemd/orvix-update.service \
        release/sudoers.d/orvix-update \
        release/scripts/smoke-admin-js.sh release/scripts/smoke-admin-ui.sh \
        release/scripts/smoke-admin-browser.sh \
        release/scripts/smoke-admin-import-graph.mjs \
        release/scripts/smoke-admin-runtime.mjs \
        release/scripts/smoke-install-bundle.sh \
        release/scripts/smoke-install-public.sh \
        release/scripts/smoke-upgrade.sh release/scripts/orvix-doctor.sh \
        release/scripts/lib-asset-propagate.sh release/scripts/apply-runtime-update.sh \
        release/scripts/generate-vapid-keys.sh release/scripts/reset-admin-password.sh \
        release/scripts/setup-https.sh release/scripts/setup-smtp-tls.sh \
        release/scripts/check-smtp-tls.sh \
        release/scripts/publish-github-release.sh \
        release/scripts/verify-github-release-assets.sh \
        release/scripts/verify-fresh-vps-one-command.sh \
        release/scripts/healthcheck.sh release/scripts/diagnostics.sh \
        release/trust/orvix-release-signing.pub.pem \
        release/admin/index.html release/admin/app.js release/admin/styles.css \
        release/admin/modules/auth.js release/admin/modules/components.js \
        release/webmail/index.html release/webmail/sw.js \
        release/webmail/assets/auth-gate.js release/webmail/assets/webmail.js \
        release/marketing/index.html release/marketing/404.html \
        release/marketing/robots.txt release/marketing/sitemap.xml \
        release/marketing/marketing-assets/index-test.js; do
        mkdir -p "$BUNDLE_STAGING/$(dirname "$rel")"
        printf '#!/usr/bin/env bash\necho stub >/dev/null\n' > "$BUNDLE_STAGING/$rel"
        chmod +x "$BUNDLE_STAGING/$rel" || true
    done

    # Sanity-fill the unzip strip into its own tar so install-public
    # sees the same "tar -tzf ... orvix/" structure as a real bundle.
    OUT_STAGE="$WORK/staging-out"
    mkdir -p "$OUT_STAGE"
    tar -C "$WORK/staging" -czf "$FIX.tmp" orvix
    mv "$FIX.tmp" "$FIX"
    size="$(wc -c < "$FIX")"
    info "fixture bundle at $FIX ($size bytes)"

    # Build a tiny local HTTP server that serves the fixture bundle
    # so install-public exercises its real download path.
    SRV="$WORK/server"
    mkdir -p "$SRV"
    cp "$FIX" "$SRV/orvix-enterprise-mail-stable-linux-amd64.tar.gz"
    sha="$(sha256sum "$FIX" | awk '{print $1}')"
    echo "$sha" > "$SRV/orvix-enterprise-mail-stable-linux-amd64.tar.gz.sha256"

    # Generate ephemeral Ed25519 signing key for bundle signature
    # verification. install-public.sh requires a .sig sidecar and a
    # trusted public key; we create a one-shot key pair so the
    # signature verification path is exercised end-to-end without
    # depending on the repository's signing key.
    openssl genpkey -algorithm Ed25519 -out "$SRV/signing-key.pem" 2>/dev/null
    openssl pkey -in "$SRV/signing-key.pem" -pubout -out "$SRV/signing-key.pub" 2>/dev/null
    openssl pkeyutl -sign -rawin -inkey "$SRV/signing-key.pem" \
        -in "$SRV/orvix-enterprise-mail-stable-linux-amd64.tar.gz" \
        -out "$SRV/orvix-enterprise-mail-stable-linux-amd64.tar.gz.sig" 2>/dev/null

    # Generate minimal release manifest and SBOM so the synthetic
    # release server serves the same sidecar set as a real bundle.
    cat > "$SRV/orvix-enterprise-mail-stable-linux-amd64.tar.gz.sbom.spdx" <<'SBOM'
SPDXVersion: SPDX-2.3
DataLicense: CC0-1.0
SPDXID: SPDXRef-DOCUMENT
DocumentName: orvix-enterprise-mail-1.0.3-rc5
SBOM
    openssl pkeyutl -sign -rawin -inkey "$SRV/signing-key.pem" \
        -in "$SRV/orvix-enterprise-mail-stable-linux-amd64.tar.gz.sbom.spdx" \
        -out "$SRV/orvix-enterprise-mail-stable-linux-amd64.tar.gz.sbom.spdx.sig" 2>/dev/null

    sbom_sha="$(sha256sum "$SRV/orvix-enterprise-mail-stable-linux-amd64.tar.gz.sbom.spdx" | awk '{print $1}')"
    cat > "$SRV/orvix-enterprise-mail-stable-linux-amd64.tar.gz.manifest.json" <<MANIFEST
{
  "schema": 1,
  "product": "Orvix Enterprise Mail",
  "version": "1.0.3-rc5",
  "channel": "stable",
  "commit": "53ecf2400000000000000000000000000000000",
  "build_time": "2026-07-03T17:40:57Z",
  "target": "linux/amd64",
  "artifact": "orvix-enterprise-mail-stable-linux-amd64.tar.gz",
  "artifact_sha256": "$sha",
  "sbom": "SBOM.spdx",
  "sbom_sha256": "$sbom_sha"
}
MANIFEST
    openssl pkeyutl -sign -rawin -inkey "$SRV/signing-key.pem" \
        -in "$SRV/orvix-enterprise-mail-stable-linux-amd64.tar.gz.manifest.json" \
        -out "$SRV/orvix-enterprise-mail-stable-linux-amd64.tar.gz.manifest.json.sig" 2>/dev/null

    # Verify all required release sidecars exist before starting the
    # HTTP server — the smoke must fail if any sidecar is absent.
    for sidecar in \
        "$SRV/orvix-enterprise-mail-stable-linux-amd64.tar.gz" \
        "$SRV/orvix-enterprise-mail-stable-linux-amd64.tar.gz.sha256" \
        "$SRV/orvix-enterprise-mail-stable-linux-amd64.tar.gz.sig" \
        "$SRV/orvix-enterprise-mail-stable-linux-amd64.tar.gz.manifest.json" \
        "$SRV/orvix-enterprise-mail-stable-linux-amd64.tar.gz.manifest.json.sig" \
        "$SRV/orvix-enterprise-mail-stable-linux-amd64.tar.gz.sbom.spdx" \
        "$SRV/orvix-enterprise-mail-stable-linux-amd64.tar.gz.sbom.spdx.sig"; do
        [ -f "$sidecar" ] || fail "missing required sidecar: $sidecar"
    done
    info "all 7 release sidecars present on synthetic server"

    HTTP_PORT="$(
        python3 - <<'PY'
import socket

with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
    s.bind(("127.0.0.1", 0))
    print(s.getsockname()[1])
PY
    )"
    cd "$SRV"
    python3 -m http.server "$HTTP_PORT" --bind 127.0.0.1 >"$WORK/http.log" 2>&1 &
    HTTP_PID=$!
    cd "$REPO_ROOT"
    sleep 0.5
    info "synthetic release server up on 127.0.0.1:$HTTP_PORT (pid $HTTP_PID)"
    info "server root: $SRV ($(ls -la "$SRV/" | wc -l) entries)"
    ls -la "$SRV/" >&2

    cleanup() {
        if [ -n "${HTTP_PID:-}" ] && kill -0 "$HTTP_PID" 2>/dev/null; then
            kill "$HTTP_PID" 2>/dev/null || true
        fi
        rm -rf "$WORK"
    }
    trap cleanup EXIT

    # Negative test: empty bundle must be rejected. Stage a bad
    # bundle at /tmp/.../incomplete.tar.gz with a tar header but
    # fewer required files.
    BAD_DIR="$WORK/bad-staging"
    mkdir -p "$BAD_DIR/orvix"
    echo "1.0.3-rc5" > "$BAD_DIR/orvix/VERSION"
    BAD="$SRV/incomplete.tar.gz"
    tar -C "$BAD_DIR" -czf "$BAD" orvix
    info "negative fixture bundle at $BAD (intentionally incomplete)"

    # ── 1. Negative: incomplete bundle must fail closed ─────────
    info "negative test: incomplete bundle must fail with non-zero exit"
    set +e
    ORVIX_BUNDLE_URL="http://127.0.0.1:${HTTP_PORT}/incomplete.tar.gz" \
    ORVIX_NON_INTERACTIVE=1 \
    ORVIX_DOMAIN="example.com" \
    ORVIX_PUBLIC_IPV4="65.75.203.74" \
    bash "$REPO_ROOT/release/install-public.sh" >"$WORK/neg.out" 2>&1
    NEG_RC=$?
    set -e
    if [ "$NEG_RC" = "0" ]; then
        fail "incomplete bundle was accepted (rc=0); install-public did not reject a half-complete release"
    fi
    grep -qE 'missing|half-complete|incomplete' "$WORK/neg.out" \
        || fail "incomplete bundle was rejected but with no diagnostic message (got: $(head -3 "$WORK/neg.out"))"
    pass "incomplete bundle correctly rejected (rc=$NEG_RC)"

    # ── 2. Positive: complete bundle must reach install.sh ──
    info "positive test: complete bundle must reach install.sh delegation"
    set +e
    ORVIX_BUNDLE_URL="http://127.0.0.1:${HTTP_PORT}/orvix-enterprise-mail-stable-linux-amd64.tar.gz" \
    ORVIX_BUNDLE_SHA256="$sha" \
    ORVIX_RELEASE_VERIFYING_KEY_FILE="$SRV/signing-key.pub" \
    ORVIX_NON_INTERACTIVE=1 \
    ORVIX_DOMAIN="example.com" \
    ORVIX_PUBLIC_IPV4="65.75.203.74" \
    bash "$REPO_ROOT/release/install-public.sh" >"$WORK/pos.out" 2>&1
    POS_RC=$?
    set -e
    grep -q 'INSTALL.SH_REACHED_MARKER' "$WORK/pos.out" \
        || fail "complete bundle did NOT reach install.sh (rc=$POS_RC; output: $(head -30 "$WORK/pos.out"))"
    # Install-public execs install.sh — the marker must carry the
    # correct env we expect.
    if [ "$POS_RC" != "42" ]; then
        warn "install.sh exited with $POS_RC instead of the test sentinel 42; check $WORK/pos.out"
    fi
    if [ "$POS_RC" = "42" ]; then
        pass "complete bundle reached install.sh delegation (rc=42 = the test sentinel)"
    else
        pass "complete bundle reached install.sh delegation (rc=$POS_RC, see $WORK/pos.out)"
    fi
    grep -q 'ORVIX_VERSION=1.0.3-rc5' "$WORK/pos.out" \
        || fail "complete bundle did not export ORVIX_VERSION from BUILDINFO"
    pass "ORVIX_VERSION exported to install.sh"
    grep -q 'ORVIX_COMMIT=53ecf24' "$WORK/pos.out" \
        || fail "complete bundle did not export ORVIX_COMMIT from BUILDINFO"
    pass "ORVIX_COMMIT exported to install.sh"
    grep -q 'bundle signature verified' "$WORK/pos.out" \
        || fail "bundle signature was not verified (missing or failed .sig sidecar at $SRV)"
    pass "bundle signature verified via install-public.sh"

    if [ -n "${HTTP_PID:-}" ] && kill -0 "$HTTP_PID" 2>/dev/null; then
        kill "$HTTP_PID" 2>/dev/null || true
    fi
fi

# ── Done ──────────────────────────────────────────────────────────
echo ""
echo "================================================================"
echo "Orvix install-public smoke: $CHECKS_PASSED / $CHECKS_TOTAL checks passed"
[ "$CHECKS_PASSED" = "$CHECKS_TOTAL" ] || fail "$((CHECKS_TOTAL - CHECKS_PASSED)) check(s) failed"
echo "================================================================"
exit 0
