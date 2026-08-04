#!/usr/bin/env bash
# test-updater-cache-hardening.sh — regression tests for the systemd-managed
# Go build cache in release/systemd/orvix-update.service +
# release/scripts/apply-runtime-update.sh.
#
# Defect being guarded against:
#   ProtectHome=true + Go's default $HOME/go/pkg/mod = /root/go/pkg/mod
#   causes "could not create module cache: permission denied" at step 1/7 of
#   the runtime updater. Fix: systemd-managed CacheDirectory= plus explicit
#   GOMODCACHE/GOCACHE/GOPATH under /var/cache/orvix-update, prepared by the
#   script before `go build` runs.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
UNIT="$REPO_ROOT/release/systemd/orvix-update.service"
SCRIPT="$REPO_ROOT/release/scripts/apply-runtime-update.sh"

PASSED=0; FAILED=0; SKIPPED=0
pass() { echo "  PASS $*"; PASSED=$((PASSED+1)); }
fail() { echo "  FAIL $*"; FAILED=$((FAILED+1)); }
skip() { echo "  SKIP $*"; SKIPPED=$((SKIPPED+1)); }

echo "== 1. ProtectHome preserved =="
if grep -qE '^ProtectHome=true$' "$UNIT"; then pass "ProtectHome=true"; else fail "ProtectHome=true missing"; fi

echo "== 2. CacheDirectory declared =="
if grep -qE '^CacheDirectory=orvix-update$' "$UNIT"; then pass "CacheDirectory"; else fail "CacheDirectory=orvix-update missing"; fi
if grep -qE '^CacheDirectoryMode=0750$' "$UNIT"; then pass "CacheDirectoryMode"; else fail "CacheDirectoryMode=0750 missing"; fi

echo "== 3. Cache env vars are absolute, not /root, not repo =="
for var in GOMODCACHE GOCACHE GOPATH; do
  line="$(grep -E "^Environment=${var}=" "$UNIT" || true)"
  if [ -z "$line" ]; then fail "$var missing"; continue; fi
  val="${line#Environment=${var}=}"
  case "$val" in
    /*) : ;;
    *) fail "$var not absolute: $val"; continue ;;
  esac
  case "$val" in
    /root|/root/*) fail "$var under /root: $val"; continue ;;
  esac
  case "$val" in
    "$REPO_ROOT"|"$REPO_ROOT"/*) fail "$var inside repo: $val"; continue ;;
  esac
  pass "$var = $val"
done

echo "== 4. go build succeeds with ProtectHome-like inaccessible HOME =="
if ! command -v go >/dev/null 2>&1; then
  skip "go not in PATH"
else
  TMP="$(mktemp -d)"
  trap 'rm -rf "$TMP"' EXIT
  mkdir -p "$TMP/src" "$TMP/cache/mod" "$TMP/cache/build" "$TMP/cache/path"
  cat >"$TMP/src/go.mod" <<'EOF'
module tinyprobe

go 1.21
EOF
  cat >"$TMP/src/main.go" <<'EOF'
package main
func main() {}
EOF
  if ( cd "$TMP/src" && HOME=/nonexistent/does-not-exist \
        GOMODCACHE="$TMP/cache/mod" GOCACHE="$TMP/cache/build" GOPATH="$TMP/cache/path" \
        go build -o "$TMP/out" ./... ) >"$TMP/log" 2>&1; then
    pass "build succeeded with HOME=/nonexistent + dedicated caches"
  else
    fail "build failed: $(tail -c 400 "$TMP/log")"
  fi
fi

echo "== 5. Script rejects cache paths under /root or inside repo =="
# Extract the prep_go_cache function into a temp file with a stub REPO_ROOT and run it.
FN_TMP="$(mktemp)"
trap 'rm -f "$FN_TMP"' EXIT
awk '/^prep_go_cache\(\) \{/,/^\}$/' "$SCRIPT" >"$FN_TMP"
if ! grep -q 'prep_go_cache' "$FN_TMP"; then
  fail "could not extract prep_go_cache from script"
else
  # /root rejection
  if ( REPO_ROOT=/tmp/notrepo; set +e; . "$FN_TMP"; GOMODCACHE=/root/x GOCACHE=/tmp/a/b GOPATH=/tmp/a/c prep_go_cache ) 2>/dev/null; then
    fail "expected rejection of /root path"
  else
    pass "rejects /root path"
  fi
  # inside-repo rejection
  if ( REPO_ROOT=/tmp/therepo; set +e; . "$FN_TMP"; GOMODCACHE=/tmp/therepo/cache GOCACHE=/tmp/a/b GOPATH=/tmp/a/c prep_go_cache ) 2>/dev/null; then
    fail "expected rejection of in-repo path"
  else
    pass "rejects in-repo path"
  fi
  # relative rejection
  if ( REPO_ROOT=/tmp/therepo; set +e; . "$FN_TMP"; GOMODCACHE=relative/path GOCACHE=/tmp/a/b GOPATH=/tmp/a/c prep_go_cache ) 2>/dev/null; then
    fail "expected rejection of relative path"
  else
    pass "rejects relative path"
  fi
fi

echo "== 6. install step gated on build success (set -e + guard) =="
if grep -qE '^set -euo pipefail' "$SCRIPT"; then
  build_line="$(grep -nF 'build -o /tmp/orvix-next' "$SCRIPT" | head -1 | cut -d: -f1)"
  install_line="$(grep -nF 'install -m 0755 /tmp/orvix-next' "$SCRIPT" | head -1 | cut -d: -f1)"
  if [ -n "$build_line" ] && [ -n "$install_line" ] && [ "$install_line" -gt "$build_line" ]; then
    if sed -n "${build_line},${install_line}p" "$SCRIPT" | grep -q '! -f /tmp/orvix-next'; then
      pass "build-failure guard present between build and install"
    else
      fail "missing existence guard between build and install"
    fi
  else
    fail "build/install ordering incorrect (build=$build_line install=$install_line)"
  fi
else
  fail "set -euo pipefail missing"
fi

echo "== 7. Operator-provided cache paths are honored, not overridden =="
TMP2="$(mktemp -d)"
MOD="$TMP2/opmod"; BUILD="$TMP2/opbuild"; GP="$TMP2/opgo"
# Stub `install` so the test does not require running as root (real script
# runs as root via systemd; the -o/-g root flags succeed there).
mkdir -p "$TMP2/bin"
cat >"$TMP2/bin/install" <<'EOF'
#!/usr/bin/env bash
# Stub: extract the target dir from `install -d [-m MODE] [-o U] [-g G] DIR`
dir=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -d) shift ;;
    -m|-o|-g) shift 2 ;;
    *) dir="$1"; shift ;;
  esac
done
[ -n "$dir" ] || exit 1
mkdir -p "$dir"
EOF
chmod +x "$TMP2/bin/install"
awk '/^prep_go_cache\(\) \{/,/^\}$/' "$SCRIPT" >"$TMP2/fn.sh"
if ( PATH="$TMP2/bin:$PATH"; REPO_ROOT="$TMP2/repo"; mkdir -p "$REPO_ROOT"; set -e; . "$TMP2/fn.sh"; \
     export GOMODCACHE="$MOD" GOCACHE="$BUILD" GOPATH="$GP"; \
     prep_go_cache >/dev/null; \
     [ "$GOMODCACHE" = "$MOD" ] && [ "$GOCACHE" = "$BUILD" ] && [ "$GOPATH" = "$GP" ] \
     && [ -d "$MOD" ] && [ -d "$BUILD" ] && [ -d "$GP" ] ) 2>/dev/null; then
  pass "operator-supplied paths honored"
else
  fail "operator-supplied paths were overridden"
fi
rm -rf "$TMP2"

echo "== 8. install.sh installs unit byte-for-byte from canonical source =="
if grep -qE 'install -m 0644.*/etc/systemd/system/orvix-update\.service' "$REPO_ROOT/release/install.sh"; then
  pass "install.sh copies unit from source tree"
else
  fail "install.sh does not install unit from canonical source"
fi
if grep -qE 'systemctl daemon-reload' "$REPO_ROOT/release/install.sh"; then
  pass "install.sh runs daemon-reload"
else
  fail "install.sh missing daemon-reload"
fi

echo "== 9. Hardening additions-only (no removals) =="
missing=0
for directive in \
  'NoNewPrivileges=true' \
  'ProtectSystem=strict' \
  'ProtectHome=true' \
  'PrivateTmp=true' \
  'CapabilityBoundingSet=CAP_SETFCAP' \
  'AmbientCapabilities=CAP_SETFCAP' \
  'ProtectClock=true' \
  'ProtectKernelTunables=true' \
  'ProtectKernelLogs=true' \
  'ProtectControlGroups=true' \
  'RestrictRealtime=true' \
  'RestrictSUIDSGID=true' \
  'SystemCallArchitectures=native' \
  'RemoveIPC=true' \
  'LockPersonality=true' \
  'MemoryDenyWriteExecute=true' \
  'RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX'; do
  if ! grep -qF "$directive" "$UNIT"; then
    fail "hardening directive removed: $directive"
    missing=1
  fi
done
[ "$missing" = "0" ] && pass "all preserved hardening directives present"

echo ""
echo "== SUMMARY == PASS=$PASSED FAIL=$FAILED SKIP=$SKIPPED"
[ "$FAILED" -eq 0 ]
