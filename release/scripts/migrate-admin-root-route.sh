#!/usr/bin/env bash
set -euo pipefail

ORVIX_CADDYFILE="${ORVIX_CADDYFILE:-/etc/caddy/Caddyfile}"
ORVIX_CADDY_BIN="${ORVIX_CADDY_BIN:-caddy}"
ORVIX_SYSTEMCTL="${ORVIX_SYSTEMCTL:-systemctl}"
ORVIX_ADMIN_DOMAIN="${ORVIX_ADMIN_DOMAIN:-}"
MARKER="# ORVIX_ADMIN_ROOT_REDIRECT_V1"
MATCHER="@orvix_admin_root path /"
REDIRECT="redir @orvix_admin_root /admin 308"
MODE="${1:---check}"

usage() {
  cat <<EOF
Usage: $0 [--check|--apply|--dry-run]
Environment:
  ORVIX_CADDYFILE, ORVIX_CADDY_BIN, ORVIX_SYSTEMCTL, ORVIX_ADMIN_DOMAIN
EOF
  exit 1
}
case "$MODE" in --check|--apply|--dry-run) ;; *) usage ;; esac
[ -f "$ORVIX_CADDYFILE" ] || { echo "Caddyfile not found at $ORVIX_CADDYFILE"; exit 1; }

_caddy_base="$(dirname "$ORVIX_CADDYFILE")"

all_blocks() {
  awk '/^[a-zA-Z0-9][a-zA-Z0-9.-]*[ \t]*\{/{name=$1;gsub(/[\t ]*\{/,"",name);print name}' "$ORVIX_CADDYFILE"
}

_extract() {
  local n="$1"
  awk -v n="$n" 'BEGIN{b=0;d=0}/^[a-zA-Z0-9][a-zA-Z0-9.-]*[ \t]*\{/{x=$1;gsub(/[\t ]*\{/,"",x);if(x==n){b=1;d=0}}b{print}b&&/{/{d++}b&&/}/{d--;if(d==0)b=0}' "$ORVIX_CADDYFILE"
}

_check() {
  local n="$1" b; b="$(_extract "$n")"
  local m mc rd
  m=$(printf '%s\n' "$b" | grep -cF "$MARKER" 2>/dev/null; printf x); m="${m%x}"; m=$(printf '%s' "$m" | tr -cd '0-9'); [ -z "$m" ] && m=0
  mc=$(printf '%s\n' "$b" | grep -cF "$MATCHER" 2>/dev/null; printf x); mc="${mc%x}"; mc=$(printf '%s' "$mc" | tr -cd '0-9'); [ -z "$mc" ] && mc=0
  rd=$(printf '%s\n' "$b" | grep -cF "$REDIRECT" 2>/dev/null; printf x); rd="${rd%x}"; rd=$(printf '%s' "$rd" | tr -cd '0-9'); [ -z "$rd" ] && rd=0
  if [ "$m" = "0" ] && [ "$mc" = "0" ] && [ "$rd" = "0" ]; then echo "absent"; return 0; fi
  if [ "$m" = "1" ] && [ "$mc" = "1" ] && [ "$rd" = "1" ]; then
    local lo lm lr
    lo=$(printf '%s\n' "$b" | grep -nF "$MARKER" | head -1 | cut -d: -f1 | tr -cd '0-9')
    lm=$(printf '%s\n' "$b" | grep -nF "$MATCHER" | head -1 | cut -d: -f1 | tr -cd '0-9')
    lr=$(printf '%s\n' "$b" | grep -nF "$REDIRECT" | head -1 | cut -d: -f1 | tr -cd '0-9')
    if [ -n "$lo" ] && [ -n "$lm" ] && [ -n "$lr" ] && [ "$lo" -lt "$lm" ] && [ "$lm" -lt "$lr" ]; then echo "valid"; return 0; fi
    echo "malformed-order"; return 0
  fi
  echo "malformed/$m/$mc/$rd"
}

_select() {
  local blocks; blocks="$(all_blocks)"
  if [ -n "$ORVIX_ADMIN_DOMAIN" ]; then
    local found; found="$(echo "$blocks" | while read -r nm; do [ "$nm" = "$ORVIX_ADMIN_DOMAIN" ] && echo "$nm"; done || true)"
    local cnt; cnt=$(printf '%s\n' "$found" | grep -c . 2>/dev/null; printf x); cnt="${cnt%x}"; cnt=$(printf '%s' "$cnt" | tr -cd '0-9'); [ -z "$cnt" ] && cnt=0
    [ "$cnt" = "0" ] && { echo "ERROR: no block matching \"$ORVIX_ADMIN_DOMAIN\"" >&2; exit 1; }
    [ "$cnt" != "1" ] && { echo "ERROR: found $cnt blocks matching \"$ORVIX_ADMIN_DOMAIN\" (expected 1)" >&2; exit 1; }
    printf '%s' "$found"
  else
    local ac; ac="$(echo "$blocks" | grep '^admin\.' || true)"
    local cnt; cnt=$(printf '%s\n' "$ac" | grep -c . 2>/dev/null; printf x); cnt="${cnt%x}"; cnt=$(printf '%s' "$cnt" | tr -cd '0-9'); [ -z "$cnt" ] && cnt=0
    [ "$cnt" = "0" ] && { echo "ERROR: no admin.* block found" >&2; exit 1; }
    [ "$cnt" != "1" ] && { echo "ERROR: found $cnt admin.* blocks — set ORVIX_ADMIN_DOMAIN" >&2; echo "$ac" >&2; exit 1; }
    printf '%s' "$ac"
  fi
}

BLOCK_NAME="$(_select)"

_meta() {
  _orig_mode=""; _orig_owner=""; _orig_group=""
  if command -v stat >/dev/null 2>&1; then
    if stat --version 2>/dev/null | grep -q GNU; then
      _orig_owner="$(stat -c '%u' "$ORVIX_CADDYFILE" 2>/dev/null || echo "")"
      _orig_group="$(stat -c '%g' "$ORVIX_CADDYFILE" 2>/dev/null || echo "")"
      _orig_mode="$(stat -c '%a' "$ORVIX_CADDYFILE" 2>/dev/null || echo "")"
    else
      _orig_owner="$(stat -f '%u' "$ORVIX_CADDYFILE" 2>/dev/null || echo "")"
      _orig_group="$(stat -f '%g' "$ORVIX_CADDYFILE" 2>/dev/null || echo "")"
      _orig_mode="$(stat -f '%OLp' "$ORVIX_CADDYFILE" 2>/dev/null || echo "")"
    fi
  fi
}
_meta

_apply_meta() {
  local f="$1"
  if [ -n "$_orig_owner" ] && [ -n "$_orig_group" ]; then
    local c_owner c_group
    if command -v stat >/dev/null 2>&1; then
      if stat --version 2>/dev/null | grep -q GNU; then
        c_owner="$(stat -c '%u' "$f" 2>/dev/null || echo "")"
        c_group="$(stat -c '%g' "$f" 2>/dev/null || echo "")"
      else
        c_owner="$(stat -f '%u' "$f" 2>/dev/null || echo "")"
        c_group="$(stat -f '%g' "$f" 2>/dev/null || echo "")"
      fi
    fi
    if [ "$c_owner" != "$_orig_owner" ] || [ "$c_group" != "$_orig_group" ]; then
      chown "${_orig_owner}:${_orig_group}" "$f" || { echo "ERROR: chown failed" >&2; return 1; }
    fi
  fi
  if [ -n "$_orig_mode" ]; then
    chmod "$_orig_mode" "$f" || { echo "ERROR: chmod failed" >&2; return 1; }
  fi
}

CONTRACT="$(_check "$BLOCK_NAME")"

case "$CONTRACT" in
  absent)
    if [ "$MODE" = "--check" ]; then
      echo "Migration required in $BLOCK_NAME" >&2
      exit 1
    fi
    ;;
  valid)
    if [ "$MODE" = "--check" ]; then echo "Already present in $BLOCK_NAME"; exit 0
    elif [ "$MODE" = "--dry-run" ]; then echo "Already present — no action"; exit 0
    else echo "Already present — idempotent, skipping"; exit 0; fi ;;
  *) echo "ERROR: malformed contract in $BLOCK_NAME: $CONTRACT" >&2; exit 1 ;;
esac

TMP="$(mktemp "${ORVIX_CADDYFILE}.tmp.XXXXXX")"
_cleanup() { rm -f "$TMP"; }
trap _cleanup EXIT

awk -v b="$BLOCK_NAME" -v m="$MARKER" -v a="$MATCHER" -v r="$REDIRECT" '
  BEGIN { ins=0 }
  /^[a-zA-Z0-9][a-zA-Z0-9.-]*[ \t]*\{/ { x=$1; gsub(/[\t ]*\{/,"",x); if(x==b){ print; print m; print a; print r; ins=1; next } }
  { print }
  END { if(!ins) { print "ERROR: block not found" > "/dev/stderr"; exit 1 } }
' "$ORVIX_CADDYFILE" > "$TMP"

_apply_meta "$TMP"

if ! "$ORVIX_CADDY_BIN" validate --config "$TMP" >/dev/null 2>&1; then
  echo "ERROR: caddy validate failed — original preserved" >&2
  exit 1
fi

if [ "$MODE" = "--dry-run" ]; then
  echo "Selected Admin hostname: $BLOCK_NAME"
  echo "Candidate validation: success"
  diff -u "$ORVIX_CADDYFILE" "$TMP" || true
  exit 0
fi

BACKUP="$(mktemp "${ORVIX_CADDYFILE}.backup.$(date +%Y%m%dT%H%M%S).XXXXXX")"
cp -p "$ORVIX_CADDYFILE" "$BACKUP"
cmp -s "$ORVIX_CADDYFILE" "$BACKUP" || { echo "ERROR: backup comparison failed" >&2; rm -f "$BACKUP"; exit 1; }

mv -f "$TMP" "$ORVIX_CADDYFILE"
trap - EXIT
rm -f "$TMP"

echo "Selected Admin hostname: $BLOCK_NAME"
echo "Backup path: $BACKUP"
echo "Validation result: success"

if "$ORVIX_SYSTEMCTL" reload caddy 2>/dev/null; then
  echo "Reload result: success"
  exit 0
fi

echo "Reload result: FAILED" >&2
echo "Rolling back…" >&2

restore_tmp="$(mktemp "${ORVIX_CADDYFILE}.restore.XXXXXX")"
cp -p "$BACKUP" "$restore_tmp"
_apply_meta "$restore_tmp"
cmp -s "$BACKUP" "$restore_tmp" || { echo "ERROR: restore candidate mismatch" >&2; rm -f "$restore_tmp"; exit 1; }
mv -f "$restore_tmp" "$ORVIX_CADDYFILE"

if "$ORVIX_SYSTEMCTL" reload caddy 2>/dev/null; then
  echo "Recovery reload: succeeded"
else
  echo "Recovery reload: FAILED" >&2
fi
exit 1
