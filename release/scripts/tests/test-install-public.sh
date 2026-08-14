#!/usr/bin/env bash
# test-install-public.sh — regression suite for the public installer contract.
#
# Covers the confirmed installer defects that made the v1.0.4-rc2 release
# unreachable:
#   1. ORVIX_GITHUB_REF through the environment must behave exactly like
#      --github-ref <ref> (GitHub source-archive mode).
#   2. --github-ref downloads a source archive; a valid source tree must
#      pass SOURCE-tree validation (not the bundle-only validator).
#   3. A half-complete source archive fails closed.
#   4. Release bundles fail closed when bin/orvix / VERSION / BUILDINFO /
#      signatures / manifest / SBOM / frontend assets / systemd / scripts
#      are missing — bundle validation is NOT weakened to make tests pass.
#   5. --version <semver> selects a release version; --installer-version
#      and -V print the installer program version.
#   6. Explicit versions (including prereleases) resolve to the exact
#      immutable release tag URL — never releases/latest/download.
#   7. Stable/default resolution still uses the stable alias mechanism.
#   8. SHA-256, bundle signature, manifest signature, SBOM signature,
#      BUILDINFO and embedded binary commit are all verified before
#      anything is installed.
#   9. The complete admin release tree includes theme-init.js and every
#      web/admin/public runtime asset; no stale frontend asset hash is
#      served.
#  10. The exact operator installation command (script saved to a file,
#      then executed with the pinned env) reaches the installer's
#      interactive administrator-password prompt reading from /dev/tty,
#      NOT from a curl pipe / stdin.
#
# Requirements: bash, tar, curl, openssl, python3, sha256sum.
# The pty test additionally requires util-linux `script` (present on
# Ubuntu runners; skipped with a reason when missing).
#
# Run from anywhere:
#   bash release/scripts/tests/test-install-public.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
INSTALLER="$REPO_ROOT/release/install-public.sh"
TRUST_KEY="$REPO_ROOT/release/trust/orvix-release-signing.pub.pem"

PASS=0
FAIL=0
SKIP=0
WORK="$(mktemp -d -t orvix-installer-test.XXXXXX)"
SRV_PID=""
HTTP_PORT=""

cleanup() {
    if [ -n "${SRV_PID:-}" ] && kill -0 "$SRV_PID" 2>/dev/null; then
        kill "$SRV_PID" 2>/dev/null || true
    fi
    [ -n "${WORK:-}" ] && rm -rf "$WORK"
}
trap cleanup EXIT

pass() { printf '  PASS  %s\n' "$*"; PASS=$((PASS + 1)); }
fail() { printf '  FAIL  %s\n' "$*" >&2; FAIL=$((FAIL + 1)); }
skip() { printf '  SKIP  %s\n' "$*"; SKIP=$((SKIP + 1)); }
info() { printf '  INFO  %s\n' "$*"; }

expect_rc_eq() {
    local want="$1" got="$2" label="$3"
    if [ "$want" = "$got" ]; then pass "$label"; else fail "$label (rc=$got, want $want)"; fi
}

expect_output_has() {
    local file="$1" needle="$2" label="$3"
    if grep -qF -- "$needle" "$file"; then pass "$label"; else fail "$label (output missing: $needle)"; fi
}

expect_output_lacks() {
    local file="$1" needle="$2" label="$3"
    if grep -qF -- "$needle" "$file"; then fail "$label (output unexpectedly contains: $needle)"; else pass "$label"; fi
}

# run_installer <outfile> [VAR=value...] -- [installer args...]
# Runs install-public.sh in a subshell with the standard non-interactive
# fixture env plus any extra VAR=value assignments, then prints the exit
# code. The "--" separator splits env assignments from CLI args (env(1)
# would otherwise eat installer flags like --version).
run_installer() {
    local out="$1"
    shift
    local args=() envs=()
    local passthrough=0 a
    for a in "$@"; do
        if [ "$passthrough" = "1" ]; then
            args+=("$a")
        elif [ "$a" = "--" ]; then
            passthrough=1
        else
            envs+=("$a")
        fi
    done
    set +e
    (
        export ORVIX_NON_INTERACTIVE=1
        export ORVIX_DOMAIN=example.com
        export ORVIX_PUBLIC_IPV4=65.75.203.74
        export ORVIX_RELEASE_VERIFYING_KEY_FILE="$WORK/signing-key.pub"
        local e
        for e in "${envs[@]}"; do
            export "$e"
        done
        bash "$INSTALLER" "${args[@]}"
    ) >"$out" 2>&1
    local rc=$?
    set -e
    printf '%s' "$rc"
}

# ── Fixture builders ──────────────────────────────────────────────

# extract_validator_list <here-doc-marker> — pulls the validator's
# required-file here-doc straight out of install-public.sh so the
# fixture is ALWAYS in sync with the validator (no sixth manifest).
extract_validator_list() {
    local marker="$1"
    awk -v start="<<$marker\$" -v end="^$marker\$" '
        $0 ~ start { inlist = 1; next }
        inlist && $0 ~ end { inlist = 0; next }
        inlist { print }
    ' "$INSTALLER"
}

# make_bundle <dest> [missing_rel] [bin_commit]
# Builds a synthetic complete release bundle at <dest>/orvix with every
# file the bundle validator requires. Optionally omits one file and/or
# overrides the embedded commit reported by the stub binary.
make_bundle() {
    local dest="$1" missing="${2:-}" bin_commit="${3:-}"
    local root="$dest/orvix"
    rm -rf "$dest"
    mkdir -p "$root"
    local rel
    while IFS= read -r rel; do
        [ -n "$rel" ] || continue
        [ "$rel" = "$missing" ] && continue
        mkdir -p "$root/$(dirname "$rel")"
        case "$rel" in
            bin/orvix)
                cat > "$root/bin/orvix" <<BINEOF
#!/usr/bin/env bash
if [ "\${1:-}" = "version" ]; then
    if [ "\${2:-}" = "--full" ]; then
        printf 'orvix 1.0.4-rc2\n  commit:     ${bin_commit:-53ecf2400000000000000000000000000000000}\n  channel:    rc\n  build_time: 2026-08-14T00:00:00Z\n'
        exit 0
    fi
    printf 'orvix 1.0.4-rc2\n'
    exit 0
fi
exit 0
BINEOF
                chmod +x "$root/bin/orvix"
                ;;
            VERSION)
                printf '1.0.4-rc2\n' > "$root/VERSION"
                ;;
            BUILDINFO)
                cat > "$root/BUILDINFO" <<'BIEOF'
version=1.0.4-rc2
commit=53ecf2400000000000000000000000000000000
short_commit=53ecf24
build_time=2026-08-14T00:00:00Z
channel=rc
target_os=linux
target_arch=amd64
built_by=test-install-public.sh
BIEOF
                ;;
            checksums.txt)
                printf 'aa  bin/orvix\n' > "$root/checksums.txt"
                ;;
            SBOM.spdx)
                cat > "$root/SBOM.spdx" <<'SBOMEOF'
SPDXVersion: SPDX-2.3
DataLicense: CC0-1.0
SPDXID: SPDXRef-DOCUMENT
DocumentName: orvix-enterprise-mail-1.0.4-rc2
DocumentNamespace: https://orvix.email/sbom/orvix-enterprise-mail-1.0.4-rc2
Creator: Tool: orvix-generate-sbom
Created: 2026-08-14T00:00:00Z

PackageName: orvix
SPDXID: SPDXRef-Package-orvix
PackageVersion: 1.0.4-rc2
PackageDownloadLocation: https://github.com/reachfm/orvix/releases/download/v1.0.4-rc2/orvix-enterprise-mail-1.0.4-rc2-linux-amd64.tar.gz
FilesAnalyzed: true
SBOMEOF
                ;;
            release/install.sh)
                cat > "$root/release/install.sh" <<'INSTALLEOF'
#!/usr/bin/env bash
echo "INSTALL.SH_REACHED_MARKER ORVIX_SOURCE_DIR=${ORVIX_SOURCE_DIR:-unset} ORVIX_VERSION=${ORVIX_VERSION:-unset} ORVIX_COMMIT=${ORVIX_COMMIT:-unset}" >&2
exit 42
INSTALLEOF
                chmod +x "$root/release/install.sh"
                ;;
            release/admin/index.html)
                printf '<!doctype html><script src="/admin/theme-init.js"></script><script type="module" crossorigin src="/admin/assets/index-testhash.js"></script>' \
                    > "$root/release/admin/index.html"
                ;;
            release/admin/theme-init.js)
                printf '(function(){try{if(window.localStorage.getItem("orvix-admin-theme")==="dark"){document.documentElement.classList.add("dark")}}catch(e){}})();\n' \
                    > "$root/release/admin/theme-init.js"
                ;;
            release/marketing/index.html)
                printf '<!doctype html><title>Orvix</title></html>\n' > "$root/release/marketing/index.html"
                ;;
            *)
                printf 'stub\n' > "$root/$rel"
                ;;
        esac
    done <<REQUIRED
$(extract_validator_list REQUIRED)
REQUIRED

    # Validator-required-but-globbed assets (not fixed names).
    mkdir -p "$root/release/admin/assets"
    printf 'console.log("stub admin bundle");\n' > "$root/release/admin/assets/index-testhash.js"
    mkdir -p "$root/release/marketing/marketing-assets"
    printf 'stub marketing chunk\n' > "$root/release/marketing/marketing-assets/index-test.js"

    # Pad so the tar comfortably exceeds install-public's 1KB sanity gate.
    dd if=/dev/zero of="$root/.padding" bs=1024 count=4 2>/dev/null
}

# publish_bundle <serve_dir> <name> [--omit-sig] [--omit-manifest-sig]
#                 [--omit-sbom-sig] [--omit-sha] [--wrong-sha]
#                 [--wrong-key] [--skip-manifest-sbom]
# Builds a complete bundle, seals it as <name>, computes the sha256
# sidecar, and signs the bundle + manifest + SBOM with the ephemeral
# test key. Options mutate the published sidecar set.
publish_bundle() {
    local serve_dir="$1" name="$2"
    shift 2
    local omit_sig=0 omit_manifest_sig=0 omit_sbom_sig=0 omit_sha=0 wrong_sha=0 wrong_key=0 skip_manifest_sbom=0
    while [ $# -gt 0 ]; do
        case "$1" in
            --omit-sig) omit_sig=1 ;;
            --omit-manifest-sig) omit_manifest_sig=1 ;;
            --omit-sbom-sig) omit_sbom_sig=1 ;;
            --omit-sha) omit_sha=1 ;;
            --wrong-sha) wrong_sha=1 ;;
            --wrong-key) wrong_key=1 ;;
            --skip-manifest-sbom) skip_manifest_sbom=1 ;;
            *) fail "publish_bundle: unknown option $1" ;;
        esac
        shift
    done
    local staging="$serve_dir/.stage-$name"
    make_bundle "$staging"
    mkdir -p "$serve_dir"
    local tar_path="$serve_dir/$name"
    tar -C "$staging" -czf "$tar_path" orvix
    local sha
    sha="$(sha256sum "$tar_path" | awk '{print $1}')"
    if [ "$wrong_sha" = "1" ]; then
        printf '%s  %s\n' "0000000000000000000000000000000000000000000000000000000000000000" "$name" > "$tar_path.sha256"
    elif [ "$omit_sha" = "0" ]; then
        printf '%s  %s\n' "$sha" "$name" > "$tar_path.sha256"
    fi
    local sig_key="$WORK/signing-key.pem"
    [ "$wrong_key" = "1" ] && sig_key="$WORK/other-key.pem"
    if [ "$omit_sig" = "0" ]; then
        openssl pkeyutl -sign -rawin -inkey "$sig_key" -in "$tar_path" -out "$tar_path.sig" 2>/dev/null
    fi
    if [ "$skip_manifest_sbom" = "0" ]; then
        local sbom_sha
        sbom_sha="$(sha256sum "$staging/orvix/SBOM.spdx" | awk '{print $1}')"
        cat > "$tar_path.manifest.json" <<MANIFEST
{
  "schema": 1,
  "product": "Orvix Enterprise Mail",
  "version": "1.0.4-rc2",
  "channel": "rc",
  "commit": "53ecf2400000000000000000000000000000000",
  "build_time": "2026-08-14T00:00:00Z",
  "target": "linux/amd64",
  "artifact": "$name",
  "artifact_sha256": "$sha",
  "sbom": "SBOM.spdx",
  "sbom_sha256": "$sbom_sha"
}
MANIFEST
        if [ "$omit_manifest_sig" = "0" ]; then
            openssl pkeyutl -sign -rawin -inkey "$sig_key" -in "$tar_path.manifest.json" -out "$tar_path.manifest.json.sig" 2>/dev/null
        fi
        cp "$staging/orvix/SBOM.spdx" "$tar_path.sbom.spdx"
        if [ "$omit_sbom_sig" = "0" ]; then
            openssl pkeyutl -sign -rawin -inkey "$sig_key" -in "$tar_path.sbom.spdx" -out "$tar_path.sbom.spdx.sig" 2>/dev/null
        fi
    fi
    rm -rf "$staging"
}

# make_source_tree <dest> [missing_rel]
# Builds a synthetic GitHub source archive (the whole repo shape) with
# every file the source-tree validator requires.
make_source_tree() {
    local dest="$1" missing="${2:-}"
    local root="$dest/orvix-src"
    rm -rf "$dest"
    mkdir -p "$root"
    local rel
    while IFS= read -r rel; do
        [ -n "$rel" ] || continue
        [ "$rel" = "$missing" ] && continue
        mkdir -p "$root/$(dirname "$rel")"
        case "$rel" in
            go.mod)
                printf 'module github.com/orvix/orvix\n\ngo 1.26\n' > "$root/go.mod"
                ;;
            cmd/orvix/main.go)
                printf 'package main\nfunc main() {}\n' > "$root/cmd/orvix/main.go"
                ;;
            release/install.sh)
                cat > "$root/release/install.sh" <<'INSTALLEOF'
#!/usr/bin/env bash
echo "INSTALL.SH_REACHED_MARKER ORVIX_SOURCE_DIR=${ORVIX_SOURCE_DIR:-unset} ORVIX_VERSION=${ORVIX_VERSION:-unset} ORVIX_COMMIT=${ORVIX_COMMIT:-unset}" >&2
exit 42
INSTALLEOF
                chmod +x "$root/release/install.sh"
                ;;
            release/admin/index.html)
                printf '<!doctype html><script src="/admin/theme-init.js"></script><script type="module" crossorigin src="/admin/assets/index-testhash.js"></script>' \
                    > "$root/release/admin/index.html"
                ;;
            internal)
                mkdir -p "$root/internal"
                ;;
            *)
                printf 'stub\n' > "$root/$rel"
                ;;
        esac
    done <<SOURCE_REQUIRED
$(extract_validator_list SOURCE_REQUIRED)
SOURCE_REQUIRED

    mkdir -p "$root/release/admin/assets" "$root/release/marketing/marketing-assets"
    printf 'console.log("stub admin bundle");\n' > "$root/release/admin/assets/index-testhash.js"
    printf 'stub marketing chunk\n' > "$root/release/marketing/marketing-assets/index-test.js"
    dd if=/dev/zero of="$root/.padding" bs=1024 count=4 2>/dev/null
}

# publish_source_archive <serve_dir> <name> [missing_rel]
publish_source_archive() {
    local serve_dir="$1" name="$2" missing="${3:-}"
    local staging="$serve_dir/.stage-src-$name"
    make_source_tree "$staging" "$missing"
    mkdir -p "$(dirname "$serve_dir/$name")"
    tar -C "$staging" -czf "$serve_dir/$name" orvix-src
    rm -rf "$staging"
}

start_server() {
    mkdir -p "$WORK/srv"
    HTTP_PORT="$(
        python3 - <<'PY'
import socket
with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
    s.bind(("127.0.0.1", 0))
    print(s.getsockname()[1])
PY
    )"
    (cd "$WORK/srv" && python3 -m http.server "$HTTP_PORT" --bind 127.0.0.1 >"$WORK/http.log" 2>&1) &
    SRV_PID=$!
    sleep 0.5
    kill -0 "$SRV_PID" 2>/dev/null || fail "local HTTP server failed to start (see $WORK/http.log)"
}

echo "=== Installer regression suite (install-public.sh) ==="

[ -f "$INSTALLER" ] || { echo "FATAL: installer not found at $INSTALLER" >&2; exit 1; }
[ -f "$TRUST_KEY" ] || { echo "FATAL: trust key not found at $TRUST_KEY" >&2; exit 1; }
command -v openssl >/dev/null 2>&1 || { echo "FATAL: openssl required" >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "FATAL: python3 required" >&2; exit 1; }
command -v curl >/dev/null 2>&1 || { echo "FATAL: curl required" >&2; exit 1; }
command -v tar >/dev/null 2>&1 || { echo "FATAL: tar required" >&2; exit 1; }
command -v sha256sum >/dev/null 2>&1 || { echo "FATAL: sha256sum required" >&2; exit 1; }

# ── 1. Static contract checks (no network) ────────────────────────
echo ""
echo "--- 1. Static installer contract ---"

# Trust key parity: the key embedded in install-public.sh must be the
# exact key in release/trust/orvix-release-signing.pub.pem. A stale
# embedded key makes every default-verification install fail.
EMBEDDED_KEY="$(awk '/BEGIN PUBLIC KEY/,/END PUBLIC KEY/' "$INSTALLER")"
FILE_KEY="$(cat "$TRUST_KEY")"
if [ "$EMBEDDED_KEY" = "$FILE_KEY" ]; then
    pass "installer's embedded release signing key matches release/trust/orvix-release-signing.pub.pem"
else
    fail "installer's embedded release signing key does NOT match release/trust/orvix-release-signing.pub.pem"
fi

IV_OUT="$WORK/iv.out"
set +e
bash "$INSTALLER" --installer-version >"$IV_OUT" 2>&1
IV_RC=$?
set -e
expect_rc_eq 0 "$IV_RC" "--installer-version prints the installer version and exits 0"
expect_output_has "$IV_OUT" "public installer v" "--installer-version output names the installer program version"

set +e
bash "$INSTALLER" -V >"$IV_OUT" 2>&1
IV_RC=$?
set -e
expect_rc_eq 0 "$IV_RC" "-V prints the installer version and exits 0"
expect_output_has "$IV_OUT" "public installer v" "-V output names the installer program version"

# Bare --version (no value) must fail with guidance, never silently
# print-and-exit (the historical ambiguity defect).
set +e
bash "$INSTALLER" --version >"$IV_OUT" 2>&1
IV_RC=$?
set -e
if [ "$IV_RC" -ne 0 ]; then pass "bare --version without a value fails closed"; else fail "bare --version without a value must not succeed"; fi
expect_output_has "$IV_OUT" "--installer-version" "bare --version error points at --installer-version"

# Complete admin release tree includes theme-init.js.
if [ -f "$REPO_ROOT/release/admin/theme-init.js" ]; then
    pass "release/admin/theme-init.js exists in the release tree"
else
    fail "release/admin/theme-init.js is missing from the release tree"
fi
if grep -q 'theme-init.js' "$REPO_ROOT/release/admin/index.html"; then
    pass "release/admin/index.html references theme-init.js"
else
    fail "release/admin/index.html does not reference theme-init.js"
fi
if [ -f "$REPO_ROOT/web/admin/public/theme-init.js" ]; then
    pass "web/admin/public/theme-init.js exists (source of the built asset)"
else
    fail "web/admin/public/theme-init.js is missing"
fi

# Every web/admin/public runtime asset must exist in release/admin.
ADMIN_PUBLIC_MISSING=0
while IFS= read -r f; do
    rel="${f#"$REPO_ROOT/web/admin/public/"}"
    if [ ! -f "$REPO_ROOT/release/admin/$rel" ]; then
        fail "web/admin/public asset $rel is missing from release/admin"
        ADMIN_PUBLIC_MISSING=1
    fi
done < <(find "$REPO_ROOT/web/admin/public" -type f)
[ "$ADMIN_PUBLIC_MISSING" = "0" ] && pass "every web/admin/public runtime asset exists in release/admin"

# No stale frontend asset hash: the legacy demo-bundle hashes that
# install.sh / apply-runtime-update.sh forbid must not be shipped in
# the release trees, and index.html's module reference must resolve.
STALE=0
for forbidden in "index-CmhA8wNq.js" "vendor-xxE1au3H.js" "index-BiLI_Nmd.css"; do
    if find "$REPO_ROOT/release/admin" "$REPO_ROOT/release/webmail" -type f -name "$forbidden" 2>/dev/null | grep -q .; then
        fail "stale legacy frontend asset $forbidden present in release/admin or release/webmail"
        STALE=1
    fi
done
[ "$STALE" = "0" ] && pass "no stale legacy frontend asset hashes present in release trees"
ADMIN_MODULE_SRC="$(grep -oE 'type="module"[^>]*src="[^"]*"' "$REPO_ROOT/release/admin/index.html" | head -n1 | sed -E 's/.*src="([^"]*)".*/\1/')"
if [ -n "$ADMIN_MODULE_SRC" ] && [ -f "$REPO_ROOT/release/admin/${ADMIN_MODULE_SRC#/admin/}" ]; then
    pass "release/admin/index.html module reference resolves to a real asset"
else
    fail "release/admin/index.html module reference does not resolve ($ADMIN_MODULE_SRC)"
fi

# ── 2. Server-backed live installer tests ─────────────────────────
echo ""
echo "--- 2. Live installer tests (synthetic release server) ---"

start_server

# Ephemeral signing key pair for the synthetic release server.
openssl genpkey -algorithm Ed25519 -out "$WORK/signing-key.pem" 2>/dev/null
openssl pkey -in "$WORK/signing-key.pem" -pubout -out "$WORK/signing-key.pub" 2>/dev/null
openssl genpkey -algorithm Ed25519 -out "$WORK/other-key.pem" 2>/dev/null

BASE="http://127.0.0.1:$HTTP_PORT"

# ── 2a. GitHub source-archive mode (dev/test) ─────────────────────
echo ""
echo "--- 2a. GitHub source-archive mode ---"

publish_source_archive "$WORK/srv" "repo/tar.gz/v1.0.4-rc2"

OUT="$WORK/src-env.out"
RC="$(run_installer "$OUT" "ORVIX_GITHUB_REF=v1.0.4-rc2" "ORVIX_GITHUB_BASE=$BASE" "ORVIX_GITHUB_REPO=repo" --)"
expect_rc_eq 42 "$RC" "ORVIX_GITHUB_REF env activates GitHub archive mode and reaches install.sh"
expect_output_has "$OUT" "GitHub SOURCE ARCHIVE" "env archive mode is explicitly marked dev/test only"
expect_output_has "$OUT" "source tree validated" "env archive mode passes source-tree validation"

OUT="$WORK/src-flag.out"
RC="$(run_installer "$OUT" "ORVIX_GITHUB_BASE=$BASE" "ORVIX_GITHUB_REPO=repo" -- "--github-ref" "v1.0.4-rc2")"
expect_rc_eq 42 "$RC" "--github-ref flag activates GitHub archive mode and reaches install.sh"
expect_output_has "$OUT" "source tree validated" "--github-ref flag passes source-tree validation"

publish_source_archive "$WORK/srv" "repo-half/tar.gz/v1.0.4-rc2" "cmd/orvix/main.go"
OUT="$WORK/src-half.out"
RC="$(run_installer "$OUT" "ORVIX_GITHUB_REF=v1.0.4-rc2" "ORVIX_GITHUB_BASE=$BASE" "ORVIX_GITHUB_REPO=repo-half" --)"
if [ "$RC" -ne 0 ]; then pass "half-complete source archive fails closed (rc=$RC)"; else fail "half-complete source archive must fail closed"; fi
expect_output_has "$OUT" "SOURCE_TREE_MISSING_FILES" "half-complete source archive names the missing files"

# ── 2b. Signed release bundle flow + exact-tag resolution ─────────
echo ""
echo "--- 2b. Signed release bundle flow ---"

BUNDLE_NAME="orvix-enterprise-mail-1.0.4-rc2-linux-amd64.tar.gz"
mkdir -p "$WORK/srv/tag/v1.0.4-rc2" "$WORK/srv/stable"
publish_bundle "$WORK/srv/tag/v1.0.4-rc2" "$BUNDLE_NAME"

OUT="$WORK/version-flag.out"
RC="$(run_installer "$OUT" "ORVIX_RELEASE_DOWNLOAD_BASE=$BASE/tag" -- "--version" "1.0.4-rc2")"
expect_rc_eq 42 "$RC" "--version 1.0.4-rc2 installs the exact rc2 release and reaches install.sh"
expect_output_has "$OUT" "$BASE/tag/v1.0.4-rc2/$BUNDLE_NAME" "--version 1.0.4-rc2 resolves to the exact v1.0.4-rc2 tag URL"
expect_output_lacks "$OUT" "releases/latest" "--version 1.0.4-rc2 never uses releases/latest/download"

OUT="$WORK/version-env.out"
RC="$(run_installer "$OUT" "ORVIX_VERSION=1.0.4-rc2" "ORVIX_RELEASE_DOWNLOAD_BASE=$BASE/tag" --)"
expect_rc_eq 42 "$RC" "ORVIX_VERSION=1.0.4-rc2 env resolves the exact tag and reaches install.sh"
expect_output_has "$OUT" "$BASE/tag/v1.0.4-rc2/$BUNDLE_NAME" "ORVIX_VERSION env resolves to the exact v1.0.4-rc2 tag URL"

# Channel-alias release: same bytes, but its manifest names the alias
# artifact (exactly what build-release-bundle.sh's write_release_manifest
# does for the <channel> copy).
STABLE_NAME="orvix-enterprise-mail-stable-linux-amd64.tar.gz"
cp "$WORK/srv/tag/v1.0.4-rc2/$BUNDLE_NAME" "$WORK/srv/stable/$STABLE_NAME"
cp "$WORK/srv/tag/v1.0.4-rc2/$BUNDLE_NAME.sha256" "$WORK/srv/stable/$STABLE_NAME.sha256"
cp "$WORK/srv/tag/v1.0.4-rc2/$BUNDLE_NAME.sig" "$WORK/srv/stable/$STABLE_NAME.sig"
STABLE_SHA="$(sha256sum "$WORK/srv/stable/$STABLE_NAME" | awk '{print $1}')"
cat > "$WORK/srv/stable/$STABLE_NAME.manifest.json" <<MANIFEST
{"schema":1,"product":"Orvix Enterprise Mail","version":"1.0.4-rc2","channel":"rc","commit":"53ecf2400000000000000000000000000000000","build_time":"2026-08-14T00:00:00Z","target":"linux/amd64","artifact":"$STABLE_NAME","artifact_sha256":"$STABLE_SHA","sbom":"SBOM.spdx"}
MANIFEST
openssl pkeyutl -sign -rawin -inkey "$WORK/signing-key.pem" \
    -in "$WORK/srv/stable/$STABLE_NAME.manifest.json" -out "$WORK/srv/stable/$STABLE_NAME.manifest.json.sig" 2>/dev/null
cp "$WORK/srv/tag/v1.0.4-rc2/$BUNDLE_NAME.sbom.spdx" "$WORK/srv/stable/$STABLE_NAME.sbom.spdx"
cp "$WORK/srv/tag/v1.0.4-rc2/$BUNDLE_NAME.sbom.spdx.sig" "$WORK/srv/stable/$STABLE_NAME.sbom.spdx.sig"

OUT="$WORK/stable-default.out"
RC="$(run_installer "$OUT" "ORVIX_RELEASES_BASE=$BASE/stable" --)"
expect_rc_eq 42 "$RC" "default (no version) installs the stable channel alias and reaches install.sh"
expect_output_has "$OUT" "$BASE/stable/$STABLE_NAME" "default resolution uses the stable alias URL"
expect_output_has "$OUT" "release manifest verified" "default resolution verifies the signed manifest"

OUT="$WORK/stable-version.out"
RC="$(run_installer "$OUT" "ORVIX_VERSION=stable" "ORVIX_RELEASES_BASE=$BASE/stable" --)"
expect_rc_eq 42 "$RC" "--version stable (backward-compatible alias) reaches install.sh"
expect_output_has "$OUT" "$BASE/stable/$STABLE_NAME" "--version stable uses the stable alias URL"

# ── 2c. Fail-closed bundle validation ─────────────────────────────
echo ""
echo "--- 2c. Fail-closed bundle validation ---"

# Missing bin/orvix — cryptographically valid bundle, incomplete layout.
mkdir -p "$WORK/srv/neg-nobin"
publish_bundle "$WORK/srv/neg-nobin" "$BUNDLE_NAME"
NEG_STAGE="$WORK/srv/neg-nobin/.stage"
make_bundle "$NEG_STAGE" "bin/orvix"
tar -C "$NEG_STAGE" -czf "$WORK/srv/neg-nobin/$BUNDLE_NAME" orvix
NEG_SHA="$(sha256sum "$WORK/srv/neg-nobin/$BUNDLE_NAME" | awk '{print $1}')"
printf '%s  %s\n' "$NEG_SHA" "$BUNDLE_NAME" > "$WORK/srv/neg-nobin/$BUNDLE_NAME.sha256"
openssl pkeyutl -sign -rawin -inkey "$WORK/signing-key.pem" \
    -in "$WORK/srv/neg-nobin/$BUNDLE_NAME" -out "$WORK/srv/neg-nobin/$BUNDLE_NAME.sig" 2>/dev/null
cat > "$WORK/srv/neg-nobin/$BUNDLE_NAME.manifest.json" <<MANIFEST
{"schema":1,"product":"Orvix Enterprise Mail","version":"1.0.4-rc2","channel":"rc","commit":"53ecf2400000000000000000000000000000000","build_time":"2026-08-14T00:00:00Z","target":"linux/amd64","artifact":"$BUNDLE_NAME","artifact_sha256":"$NEG_SHA","sbom":"SBOM.spdx"}
MANIFEST
openssl pkeyutl -sign -rawin -inkey "$WORK/signing-key.pem" \
    -in "$WORK/srv/neg-nobin/$BUNDLE_NAME.manifest.json" -out "$WORK/srv/neg-nobin/$BUNDLE_NAME.manifest.json.sig" 2>/dev/null
cp "$NEG_STAGE/orvix/SBOM.spdx" "$WORK/srv/neg-nobin/$BUNDLE_NAME.sbom.spdx"
openssl pkeyutl -sign -rawin -inkey "$WORK/signing-key.pem" \
    -in "$WORK/srv/neg-nobin/$BUNDLE_NAME.sbom.spdx" -out "$WORK/srv/neg-nobin/$BUNDLE_NAME.sbom.spdx.sig" 2>/dev/null
rm -rf "$NEG_STAGE"
OUT="$WORK/neg-nobin.out"
RC="$(run_installer "$OUT" "ORVIX_BUNDLE_URL=$BASE/neg-nobin/$BUNDLE_NAME" "ORVIX_BUNDLE_SHA256=$NEG_SHA" --)"
if [ "$RC" -ne 0 ]; then pass "bundle missing bin/orvix fails closed (rc=$RC)"; else fail "bundle missing bin/orvix must fail closed"; fi
expect_output_has "$OUT" "bin/orvix" "missing bin/orvix is named in the failure"

# Missing VERSION.
mkdir -p "$WORK/srv/neg-noversion"
publish_bundle "$WORK/srv/neg-noversion" "$BUNDLE_NAME"
NEG_STAGE="$WORK/srv/neg-noversion/.stage"
make_bundle "$NEG_STAGE" "VERSION"
tar -C "$NEG_STAGE" -czf "$WORK/srv/neg-noversion/$BUNDLE_NAME" orvix
NEG_SHA="$(sha256sum "$WORK/srv/neg-noversion/$BUNDLE_NAME" | awk '{print $1}')"
printf '%s  %s\n' "$NEG_SHA" "$BUNDLE_NAME" > "$WORK/srv/neg-noversion/$BUNDLE_NAME.sha256"
openssl pkeyutl -sign -rawin -inkey "$WORK/signing-key.pem" \
    -in "$WORK/srv/neg-noversion/$BUNDLE_NAME" -out "$WORK/srv/neg-noversion/$BUNDLE_NAME.sig" 2>/dev/null
cat > "$WORK/srv/neg-noversion/$BUNDLE_NAME.manifest.json" <<MANIFEST
{"schema":1,"product":"Orvix Enterprise Mail","version":"1.0.4-rc2","channel":"rc","commit":"53ecf2400000000000000000000000000000000","build_time":"2026-08-14T00:00:00Z","target":"linux/amd64","artifact":"$BUNDLE_NAME","artifact_sha256":"$NEG_SHA","sbom":"SBOM.spdx"}
MANIFEST
openssl pkeyutl -sign -rawin -inkey "$WORK/signing-key.pem" \
    -in "$WORK/srv/neg-noversion/$BUNDLE_NAME.manifest.json" -out "$WORK/srv/neg-noversion/$BUNDLE_NAME.manifest.json.sig" 2>/dev/null
cp "$NEG_STAGE/orvix/SBOM.spdx" "$WORK/srv/neg-noversion/$BUNDLE_NAME.sbom.spdx"
openssl pkeyutl -sign -rawin -inkey "$WORK/signing-key.pem" \
    -in "$WORK/srv/neg-noversion/$BUNDLE_NAME.sbom.spdx" -out "$WORK/srv/neg-noversion/$BUNDLE_NAME.sbom.spdx.sig" 2>/dev/null
rm -rf "$NEG_STAGE"
OUT="$WORK/neg-noversion.out"
RC="$(run_installer "$OUT" "ORVIX_BUNDLE_URL=$BASE/neg-noversion/$BUNDLE_NAME" "ORVIX_BUNDLE_SHA256=$NEG_SHA" --)"
if [ "$RC" -ne 0 ]; then pass "bundle missing VERSION fails closed (rc=$RC)"; else fail "bundle missing VERSION must fail closed"; fi
expect_output_has "$OUT" "VERSION" "missing VERSION is named in the failure"

# Missing BUILDINFO.
mkdir -p "$WORK/srv/neg-nobuildinfo"
publish_bundle "$WORK/srv/neg-nobuildinfo" "$BUNDLE_NAME"
NEG_STAGE="$WORK/srv/neg-nobuildinfo/.stage"
make_bundle "$NEG_STAGE" "BUILDINFO"
tar -C "$NEG_STAGE" -czf "$WORK/srv/neg-nobuildinfo/$BUNDLE_NAME" orvix
NEG_SHA="$(sha256sum "$WORK/srv/neg-nobuildinfo/$BUNDLE_NAME" | awk '{print $1}')"
printf '%s  %s\n' "$NEG_SHA" "$BUNDLE_NAME" > "$WORK/srv/neg-nobuildinfo/$BUNDLE_NAME.sha256"
openssl pkeyutl -sign -rawin -inkey "$WORK/signing-key.pem" \
    -in "$WORK/srv/neg-nobuildinfo/$BUNDLE_NAME" -out "$WORK/srv/neg-nobuildinfo/$BUNDLE_NAME.sig" 2>/dev/null
cat > "$WORK/srv/neg-nobuildinfo/$BUNDLE_NAME.manifest.json" <<MANIFEST
{"schema":1,"product":"Orvix Enterprise Mail","version":"1.0.4-rc2","channel":"rc","commit":"53ecf2400000000000000000000000000000000","build_time":"2026-08-14T00:00:00Z","target":"linux/amd64","artifact":"$BUNDLE_NAME","artifact_sha256":"$NEG_SHA","sbom":"SBOM.spdx"}
MANIFEST
openssl pkeyutl -sign -rawin -inkey "$WORK/signing-key.pem" \
    -in "$WORK/srv/neg-nobuildinfo/$BUNDLE_NAME.manifest.json" -out "$WORK/srv/neg-nobuildinfo/$BUNDLE_NAME.manifest.json.sig" 2>/dev/null
cp "$NEG_STAGE/orvix/SBOM.spdx" "$WORK/srv/neg-nobuildinfo/$BUNDLE_NAME.sbom.spdx"
openssl pkeyutl -sign -rawin -inkey "$WORK/signing-key.pem" \
    -in "$WORK/srv/neg-nobuildinfo/$BUNDLE_NAME.sbom.spdx" -out "$WORK/srv/neg-nobuildinfo/$BUNDLE_NAME.sbom.spdx.sig" 2>/dev/null
rm -rf "$NEG_STAGE"
OUT="$WORK/neg-nobuildinfo.out"
RC="$(run_installer "$OUT" "ORVIX_BUNDLE_URL=$BASE/neg-nobuildinfo/$BUNDLE_NAME" "ORVIX_BUNDLE_SHA256=$NEG_SHA" --)"
if [ "$RC" -ne 0 ]; then pass "bundle missing BUILDINFO fails closed (rc=$RC)"; else fail "bundle missing BUILDINFO must fail closed"; fi
expect_output_has "$OUT" "BUILDINFO" "missing BUILDINFO is named in the failure"

# Missing bundle signature (.sig 404).
mkdir -p "$WORK/srv/neg-nosig"
publish_bundle "$WORK/srv/neg-nosig" "$BUNDLE_NAME" --omit-sig
OUT="$WORK/neg-nosig.out"
RC="$(run_installer "$OUT" "ORVIX_BUNDLE_URL=$BASE/neg-nosig/$BUNDLE_NAME" "ORVIX_BUNDLE_SHA256=$(awk '{print $1}' "$WORK/srv/neg-nosig/$BUNDLE_NAME.sha256")" --)"
if [ "$RC" -ne 0 ]; then pass "bundle without .sig fails closed (rc=$RC)"; else fail "bundle without .sig must fail closed"; fi
expect_output_has "$OUT" "signature sidecar not found" "missing .sig produces the signature-sidecar diagnostic"

# Missing manifest signature.
mkdir -p "$WORK/srv/neg-nomanifestsig"
publish_bundle "$WORK/srv/neg-nomanifestsig" "$BUNDLE_NAME" --omit-manifest-sig
OUT="$WORK/neg-nomanifestsig.out"
RC="$(run_installer "$OUT" "ORVIX_BUNDLE_URL=$BASE/neg-nomanifestsig/$BUNDLE_NAME" "ORVIX_BUNDLE_SHA256=$(awk '{print $1}' "$WORK/srv/neg-nomanifestsig/$BUNDLE_NAME.sha256")" --)"
if [ "$RC" -ne 0 ]; then pass "bundle without manifest .sig fails closed (rc=$RC)"; else fail "bundle without manifest .sig must fail closed"; fi
expect_output_has "$OUT" "manifest.json.sig" "missing manifest signature is named in the failure"

# Missing SBOM signature.
mkdir -p "$WORK/srv/neg-nosbomsig"
publish_bundle "$WORK/srv/neg-nosbomsig" "$BUNDLE_NAME" --omit-sbom-sig
OUT="$WORK/neg-nosbomsig.out"
RC="$(run_installer "$OUT" "ORVIX_BUNDLE_URL=$BASE/neg-nosbomsig/$BUNDLE_NAME" "ORVIX_BUNDLE_SHA256=$(awk '{print $1}' "$WORK/srv/neg-nosbomsig/$BUNDLE_NAME.sha256")" --)"
if [ "$RC" -ne 0 ]; then pass "bundle without SBOM .sig fails closed (rc=$RC)"; else fail "bundle without SBOM .sig must fail closed"; fi
expect_output_has "$OUT" "sbom.spdx.sig" "missing SBOM signature is named in the failure"

# Wrong SHA-256 sidecar.
mkdir -p "$WORK/srv/neg-wrongsha"
publish_bundle "$WORK/srv/neg-wrongsha" "$BUNDLE_NAME" --wrong-sha
OUT="$WORK/neg-wrongsha.out"
RC="$(run_installer "$OUT" "ORVIX_BUNDLE_URL=$BASE/neg-wrongsha/$BUNDLE_NAME" "ORVIX_BUNDLE_SHA256=$(awk '{print $1}' "$WORK/srv/neg-wrongsha/$BUNDLE_NAME.sha256")" --)"
if [ "$RC" -ne 0 ]; then pass "wrong bundle sha256 fails closed (rc=$RC)"; else fail "wrong bundle sha256 must fail closed"; fi
expect_output_has "$OUT" "sha256 mismatch" "wrong sha256 produces the mismatch diagnostic"

# Wrong signature (signed with a different key).
mkdir -p "$WORK/srv/neg-wrongkey"
publish_bundle "$WORK/srv/neg-wrongkey" "$BUNDLE_NAME" --wrong-key
OUT="$WORK/neg-wrongkey.out"
RC="$(run_installer "$OUT" "ORVIX_BUNDLE_URL=$BASE/neg-wrongkey/$BUNDLE_NAME" "ORVIX_BUNDLE_SHA256=$(awk '{print $1}' "$WORK/srv/neg-wrongkey/$BUNDLE_NAME.sha256")" --)"
if [ "$RC" -ne 0 ]; then pass "bundle signed by an untrusted key fails closed (rc=$RC)"; else fail "bundle signed by an untrusted key must fail closed"; fi
expect_output_has "$OUT" "signature verification failed" "wrong signature produces the signature diagnostic"

# Wrong embedded commit: bundle crypto is valid, but the binary
# reports a different commit than BUILDINFO/manifest.
mkdir -p "$WORK/srv/neg-commit"
publish_bundle "$WORK/srv/neg-commit" "$BUNDLE_NAME"
NEG_STAGE="$WORK/srv/neg-commit/.stage"
make_bundle "$NEG_STAGE" "" "deadbeef00000000000000000000000000000000"
tar -C "$NEG_STAGE" -czf "$WORK/srv/neg-commit/$BUNDLE_NAME" orvix
NEG_SHA="$(sha256sum "$WORK/srv/neg-commit/$BUNDLE_NAME" | awk '{print $1}')"
printf '%s  %s\n' "$NEG_SHA" "$BUNDLE_NAME" > "$WORK/srv/neg-commit/$BUNDLE_NAME.sha256"
openssl pkeyutl -sign -rawin -inkey "$WORK/signing-key.pem" \
    -in "$WORK/srv/neg-commit/$BUNDLE_NAME" -out "$WORK/srv/neg-commit/$BUNDLE_NAME.sig" 2>/dev/null
cat > "$WORK/srv/neg-commit/$BUNDLE_NAME.manifest.json" <<MANIFEST
{"schema":1,"product":"Orvix Enterprise Mail","version":"1.0.4-rc2","channel":"rc","commit":"53ecf2400000000000000000000000000000000","build_time":"2026-08-14T00:00:00Z","target":"linux/amd64","artifact":"$BUNDLE_NAME","artifact_sha256":"$NEG_SHA","sbom":"SBOM.spdx"}
MANIFEST
openssl pkeyutl -sign -rawin -inkey "$WORK/signing-key.pem" \
    -in "$WORK/srv/neg-commit/$BUNDLE_NAME.manifest.json" -out "$WORK/srv/neg-commit/$BUNDLE_NAME.manifest.json.sig" 2>/dev/null
cp "$NEG_STAGE/orvix/SBOM.spdx" "$WORK/srv/neg-commit/$BUNDLE_NAME.sbom.spdx"
openssl pkeyutl -sign -rawin -inkey "$WORK/signing-key.pem" \
    -in "$WORK/srv/neg-commit/$BUNDLE_NAME.sbom.spdx" -out "$WORK/srv/neg-commit/$BUNDLE_NAME.sbom.spdx.sig" 2>/dev/null
rm -rf "$NEG_STAGE"
OUT="$WORK/neg-commit.out"
RC="$(run_installer "$OUT" "ORVIX_BUNDLE_URL=$BASE/neg-commit/$BUNDLE_NAME" "ORVIX_BUNDLE_SHA256=$NEG_SHA" --)"
if [ "$RC" -ne 0 ]; then pass "bundle with a mismatched embedded commit fails closed (rc=$RC)"; else fail "bundle with a mismatched embedded commit must fail closed"; fi
expect_output_has "$OUT" "embeds commit" "embedded-commit mismatch is named in the failure"

# Stale bundle VERSION file: VERSION says 1.0.3-rc4 while BUILDINFO /
# manifest say 1.0.4-rc2 (the exact defect found when the first
# v1.0.4-rc2 bundle shipped the stale committed release/VERSION).
mkdir -p "$WORK/srv/neg-version-mismatch"
publish_bundle "$WORK/srv/neg-version-mismatch" "$BUNDLE_NAME"
NEG_STAGE="$WORK/srv/neg-version-mismatch/.stage"
make_bundle "$NEG_STAGE" ""
printf '1.0.3-rc4\n' > "$NEG_STAGE/orvix/VERSION"
tar -C "$NEG_STAGE" -czf "$WORK/srv/neg-version-mismatch/$BUNDLE_NAME" orvix
NEG_SHA="$(sha256sum "$WORK/srv/neg-version-mismatch/$BUNDLE_NAME" | awk '{print $1}')"
printf '%s  %s\n' "$NEG_SHA" "$BUNDLE_NAME" > "$WORK/srv/neg-version-mismatch/$BUNDLE_NAME.sha256"
openssl pkeyutl -sign -rawin -inkey "$WORK/signing-key.pem" \
    -in "$WORK/srv/neg-version-mismatch/$BUNDLE_NAME" -out "$WORK/srv/neg-version-mismatch/$BUNDLE_NAME.sig" 2>/dev/null
cat > "$WORK/srv/neg-version-mismatch/$BUNDLE_NAME.manifest.json" <<MANIFEST
{"schema":1,"product":"Orvix Enterprise Mail","version":"1.0.4-rc2","channel":"rc","commit":"53ecf2400000000000000000000000000000000","build_time":"2026-08-14T00:00:00Z","target":"linux/amd64","artifact":"$BUNDLE_NAME","artifact_sha256":"$NEG_SHA","sbom":"SBOM.spdx"}
MANIFEST
openssl pkeyutl -sign -rawin -inkey "$WORK/signing-key.pem" \
    -in "$WORK/srv/neg-version-mismatch/$BUNDLE_NAME.manifest.json" -out "$WORK/srv/neg-version-mismatch/$BUNDLE_NAME.manifest.json.sig" 2>/dev/null
cp "$NEG_STAGE/orvix/SBOM.spdx" "$WORK/srv/neg-version-mismatch/$BUNDLE_NAME.sbom.spdx"
openssl pkeyutl -sign -rawin -inkey "$WORK/signing-key.pem" \
    -in "$WORK/srv/neg-version-mismatch/$BUNDLE_NAME.sbom.spdx" -out "$WORK/srv/neg-version-mismatch/$BUNDLE_NAME.sbom.spdx.sig" 2>/dev/null
rm -rf "$NEG_STAGE"
OUT="$WORK/neg-version-mismatch.out"
RC="$(run_installer "$OUT" "ORVIX_BUNDLE_URL=$BASE/neg-version-mismatch/$BUNDLE_NAME" "ORVIX_BUNDLE_SHA256=$NEG_SHA" --)"
if [ "$RC" -ne 0 ]; then pass "bundle with a stale VERSION file fails closed (rc=$RC)"; else fail "bundle with a stale VERSION file must fail closed"; fi
expect_output_has "$OUT" "disagrees with BUILDINFO" "stale VERSION vs BUILDINFO mismatch is named in the failure"

# ── 2d. Operator command + interactive password prompt (pty) ──────
echo ""
echo "--- 2d. Operator installation command (tty password prompt) ---"

if command -v script >/dev/null 2>&1 && { [ -r /dev/tty ] || [ -c /dev/tty ]; }; then
    # Exact operator flow: save the installer to a file (curl), chmod,
    # then run the file with pinned env — password prompts must come
    # from the terminal, not from stdin.
    cp "$INSTALLER" "$WORK/srv/install-public.sh"
    curl -fsSLo "$WORK/install-orvix.sh" "$BASE/install-public.sh"
    chmod 700 "$WORK/install-orvix.sh"

    # A bundle whose install.sh probes the tty exactly like the real
    # installer's prompt_password contract. Served at the exact-tag
    # path the installer resolves for ORVIX_VERSION=1.0.4-rc2.
    PTY_DIR="$WORK/srv/pty/v1.0.4-rc2"
    mkdir -p "$PTY_DIR"
    PTY_STAGE="$WORK/srv/pty/.stage"
    make_bundle "$PTY_STAGE"
    cat > "$PTY_STAGE/orvix/release/install.sh" <<'TTYEOF'
#!/usr/bin/env bash
printf 'Admin password (8-72 bytes, hidden): ' >&2
if [ -r /dev/tty ]; then
    exec 3<>/dev/tty
    IFS= read -r -s password <&3
    printf '\n' >&2
    printf 'PROMPT_PROBE_TTY len=%d\n' "${#password}" >&2
else
    printf 'PROMPT_PROBE_NO_TTY\n' >&2
fi
exit 0
TTYEOF
    chmod +x "$PTY_STAGE/orvix/release/install.sh"
    tar -C "$PTY_STAGE" -czf "$PTY_DIR/$BUNDLE_NAME" orvix
    PTY_SHA="$(sha256sum "$PTY_DIR/$BUNDLE_NAME" | awk '{print $1}')"
    printf '%s  %s\n' "$PTY_SHA" "$BUNDLE_NAME" > "$PTY_DIR/$BUNDLE_NAME.sha256"
    openssl pkeyutl -sign -rawin -inkey "$WORK/signing-key.pem" \
        -in "$PTY_DIR/$BUNDLE_NAME" -out "$PTY_DIR/$BUNDLE_NAME.sig" 2>/dev/null
    cat > "$PTY_DIR/$BUNDLE_NAME.manifest.json" <<MANIFEST
{"schema":1,"product":"Orvix Enterprise Mail","version":"1.0.4-rc2","channel":"rc","commit":"53ecf2400000000000000000000000000000000","build_time":"2026-08-14T00:00:00Z","target":"linux/amd64","artifact":"$BUNDLE_NAME","artifact_sha256":"$PTY_SHA","sbom":"SBOM.spdx"}
MANIFEST
    openssl pkeyutl -sign -rawin -inkey "$WORK/signing-key.pem" \
        -in "$PTY_DIR/$BUNDLE_NAME.manifest.json" -out "$PTY_DIR/$BUNDLE_NAME.manifest.json.sig" 2>/dev/null
    cp "$PTY_STAGE/orvix/SBOM.spdx" "$PTY_DIR/$BUNDLE_NAME.sbom.spdx"
    openssl pkeyutl -sign -rawin -inkey "$WORK/signing-key.pem" \
        -in "$PTY_DIR/$BUNDLE_NAME.sbom.spdx" -out "$PTY_DIR/$BUNDLE_NAME.sbom.spdx.sig" 2>/dev/null
    rm -rf "$PTY_STAGE"

    set +e
    printf 'SuperSecret123!\n' | script -qec "env \
        ORVIX_NON_INTERACTIVE=1 \
        ORVIX_PRIMARY_DOMAIN=orvix.email \
        ORVIX_PUBLIC_IPV4=51.75.240.231 \
        ORVIX_ADMIN_EMAIL=admin@orvix.email \
        ORVIX_RELEASE_VERIFYING_KEY_FILE=$WORK/signing-key.pub \
        ORVIX_VERSION=1.0.4-rc2 \
        ORVIX_RELEASE_DOWNLOAD_BASE=$BASE/pty \
        bash $WORK/install-orvix.sh" /dev/null >"$WORK/pty.out" 2>&1
    PTY_RC=$?
    set -e
    expect_output_has "$WORK/pty.out" "Admin password (8-72 bytes, hidden)" "operator command reaches the interactive administrator-password prompt"
    expect_output_has "$WORK/pty.out" "PROMPT_PROBE_TTY len=" "password prompt reads from /dev/tty, not from stdin"
    if grep -qE 'PROMPT_PROBE_TTY len=0' "$WORK/pty.out"; then
        fail "password prompt read an EMPTY line from /dev/tty (password not fed through the pty)"
    else
        pass "password prompt received non-empty input through /dev/tty"
    fi
    if grep -q 'PROMPT_PROBE_NO_TTY' "$WORK/pty.out"; then
        fail "installer probe had no /dev/tty (script-pipe test rig broken)"
    else
        pass "password prompt resolved /dev/tty"
    fi
    if [ "$PTY_RC" = "0" ]; then pass "operator command completed via the saved installer file"; else fail "operator command exited rc=$PTY_RC"; fi
else
    skip "util-linux 'script' or /dev/tty unavailable — interactive prompt test skipped"
fi

# ── 3. Summary ────────────────────────────────────────────────────
echo ""
echo "=== Installer regression suite summary ==="
echo "passed=$PASS failed=$FAIL skipped=$SKIP"
[ "$FAIL" -eq 0 ] || exit 1
exit 0
