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

Modes:
  --check    Check whether the admin root redirect exists (default).
  --apply    Insert the redirect rule if not already present.
  --dry-run  Show what would be changed without modifying files.

Environment:
  ORVIX_CADDYFILE      Path to Caddyfile (default: /etc/caddy/Caddyfile)
  ORVIX_CADDY_BIN      Path to caddy binary (default: caddy)
  ORVIX_SYSTEMCTL      Path to systemctl (default: systemctl)
  ORVIX_ADMIN_DOMAIN   Exact admin hostname to match (default: detect admin.*)
EOF
  exit 1
}

case "$MODE" in
  --check|--apply|--dry-run) ;;
  *) usage ;;
esac

if [ ! -f "$ORVIX_CADDYFILE" ]; then
  echo "Caddyfile not found at $ORVIX_CADDYFILE"
  exit 1
fi

# ── Parse all top-level hostname blocks ────────────────────────
# Returns: each line is "<block_name>"
all_blocks() {
  awk '
    /^[a-zA-Z0-9][a-zA-Z0-9.-]*[ \t]*\{/ {
      name=$1; gsub(/[\t ]*\{/,"",name); print name
    }
  ' "$ORVIX_CADDYFILE"
}

# ── Extract a single block body (including opening and closing) ─
extract_block() {
  local name="$1"
  awk -v name="$name" '
    BEGIN { in_block=0; depth=0 }
    /^[a-zA-Z0-9][a-zA-Z0-9.-]*[ \t]*\{/ {
      bname=$1; gsub(/[\t ]*\{/,"",bname)
      if(bname==name){ in_block=1; depth=0 }
    }
    in_block { print }
    in_block && /{/ { depth++ }
    in_block && /}/ { depth--; if(depth==0) in_block=0 }
  ' "$ORVIX_CADDYFILE"
}

# ── Check contract inside a specific block ─────────────────────
check_contract() {
  local name="$1"
  local body
  body="$(extract_block "$name")"

  local m mc rd
  m=$(printf '%s\n' "$body" | grep -cF "$MARKER" 2>/dev/null; printf x)
  m="${m%x}"
  m=$(printf '%s' "$m" | tr -cd '0-9')
  mc=$(printf '%s\n' "$body" | grep -cF "$MATCHER" 2>/dev/null; printf x)
  mc="${mc%x}"
  mc=$(printf '%s' "$mc" | tr -cd '0-9')
  rd=$(printf '%s\n' "$body" | grep -cF "$REDIRECT" 2>/dev/null; printf x)
  rd="${rd%x}"
  rd=$(printf '%s' "$rd" | tr -cd '0-9')
  [ -z "$m" ] && m=0
  [ -z "$mc" ] && mc=0
  [ -z "$rd" ] && rd=0

  if [ "$m" = "0" ] && [ "$mc" = "0" ] && [ "$rd" = "0" ]; then
    echo "absent"
    return 0
  fi

  if [ "$m" = "1" ] && [ "$mc" = "1" ] && [ "$rd" = "1" ]; then
    # Verify order: MARKER, then MATCHER, then REDIRECT
    local ordered om or
    ordered=$(printf '%s\n' "$body" | grep -nF "$MARKER" | head -1 | cut -d: -f1 | tr -d '[:space:]')
    om=$(printf '%s\n' "$body" | grep -nF "$MATCHER" | head -1 | cut -d: -f1 | tr -d '[:space:]')
    or=$(printf '%s\n' "$body" | grep -nF "$REDIRECT" | head -1 | cut -d: -f1 | tr -d '[:space:]')
    if [ -n "$ordered" ] && [ -n "$om" ] && [ -n "$or" ]; then
      if [ "$ordered" -lt "$om" ] && [ "$om" -lt "$or" ]; then
        echo "valid"
        return 0
      fi
    fi
    echo "malformed-order"
    return 0
  fi

  echo "malformed/$m/$mc/$rd"
}

# ── Select exactly one Admin block ──────────────────────────────
select_block() {
  local blocks
  blocks="$(all_blocks)"

  if [ -n "$ORVIX_ADMIN_DOMAIN" ]; then
    local found
    found="$(echo "$blocks" | while read -r name; do
      [ "$name" = "$ORVIX_ADMIN_DOMAIN" ] && echo "$name"
    done || true)"
    local count
    count=$(printf '%s\n' "$found" | grep -c . 2>/dev/null; printf x)
    count="${count%x}"
    count=$(printf '%s' "$count" | tr -cd '0-9')
    [ -z "$count" ] && count=0
    if [ "$count" = "0" ]; then
      echo "ERROR: no Caddy hostname block exactly matching \"$ORVIX_ADMIN_DOMAIN\"" >&2
      exit 1
    elif [ "$count" != "1" ]; then
      echo "ERROR: found $count blocks exactly matching \"$ORVIX_ADMIN_DOMAIN\" (expected exactly 1)" >&2
      exit 1
    fi
    printf '%s' "$found"
  else
    local admin_candidates
    admin_candidates="$(echo "$blocks" | grep '^admin\.' || true)"
    local count
    count=$(printf '%s\n' "$admin_candidates" | grep -c . 2>/dev/null; printf x)
    count="${count%x}"
    count=$(printf '%s' "$count" | tr -cd '0-9')
    [ -z "$count" ] && count=0
    if [ "$count" = "0" ]; then
      echo "ERROR: no admin.* hostname block found in $ORVIX_CADDYFILE" >&2
      exit 1
    elif [ "$count" != "1" ]; then
      echo "ERROR: found $count admin.* hostname blocks — set ORVIX_ADMIN_DOMAIN to disambiguate" >&2
      echo "$admin_candidates" >&2
      exit 1
    fi
    printf '%s' "$admin_candidates"
  fi
}

BLOCK_NAME="$(select_block)"

# ── Check for existing valid contract ───────────────────────────
CONTRACT="$(check_contract "$BLOCK_NAME")"

case "$CONTRACT" in
  absent) ;;
  valid)
    if [ "$MODE" = "--check" ]; then
      echo "Admin root redirect already present in $BLOCK_NAME block of $ORVIX_CADDYFILE"
      exit 0
    elif [ "$MODE" = "--dry-run" ]; then
      echo "Admin root redirect already present — no action needed"
      exit 0
    fi
    echo "Admin root redirect already present — idempotent, skipping"
    exit 0
    ;;
  *)
    echo "ERROR: malformed admin root redirect contract in $BLOCK_NAME block: $CONTRACT" >&2
    exit 1
    ;;
esac

# ── Generate candidate ─────────────────────────────────────────
tmp="$(mktemp "${ORVIX_CADDYFILE}.tmp.XXXXXX")"
cleanup_tmp() { rm -f "$tmp"; }
trap cleanup_tmp EXIT

# Resolve original ownership and mode.
orig_owner=""
orig_group=""
orig_mode=""
if command -v stat >/dev/null 2>&1; then
  if stat --version 2>/dev/null | grep -q GNU; then
    orig_owner="$(stat -c '%u' "$ORVIX_CADDYFILE" 2>/dev/null || echo "")"
    orig_group="$(stat -c '%g' "$ORVIX_CADDYFILE" 2>/dev/null || echo "")"
    orig_mode="$(stat -c '%a' "$ORVIX_CADDYFILE" 2>/dev/null || echo "")"
  else
    orig_owner="$(stat -f '%u' "$ORVIX_CADDYFILE" 2>/dev/null || echo "")"
    orig_group="$(stat -f '%g' "$ORVIX_CADDYFILE" 2>/dev/null || echo "")"
    orig_mode="$(stat -f '%OLp' "$ORVIX_CADDYFILE" 2>/dev/null || echo "")"
  fi
fi

# Create timestamped backup.
backup="${ORVIX_CADDYFILE}.backup.$(date +%Y%m%d%H%M%S)"
cp -p "$ORVIX_CADDYFILE" "$backup"

# Insert marker/redirect after block-opening line.
awk -v block="$BLOCK_NAME" -v marker="$MARKER" -v matcher="$MATCHER" -v redirect="$REDIRECT" '
  BEGIN { inserted=0; in_block=0; depth=0 }
  /^[a-zA-Z0-9][a-zA-Z0-9.-]*[ \t]*\{/ {
    bname=$1; gsub(/[\t ]*\{/,"",bname)
    if(bname==block) {
      print
      print marker
      print matcher
      print redirect
      inserted=1
      in_block=1
      depth=1
      next
    }
  }
  { print }
  { if($0 ~ /{/) depth++; if($0 ~ /}/) depth--; if(depth==0) in_block=0 }
  END { if(!inserted) { print "ERROR: could not locate block" > "/dev/stderr"; exit 1 } }
' "$ORVIX_CADDYFILE" > "$tmp"

# Apply ownership/mode to candidate.
if [ -n "$orig_owner" ] && [ -n "$orig_group" ]; then
  chown "${orig_owner}:${orig_group}" "$tmp" 2>/dev/null || true
fi
if [ -n "$orig_mode" ]; then
  chmod "$orig_mode" "$tmp" 2>/dev/null || true
fi

# Validate candidate.
if ! "$ORVIX_CADDY_BIN" validate --config "$tmp" >/dev/null 2>&1; then
  echo "ERROR: caddy validate failed for generated Caddyfile — original preserved at $backup" >&2
  rm -f "$tmp"
  exit 1
fi

# ── Dry-run ─────────────────────────────────────────────────────
if [ "$MODE" = "--dry-run" ]; then
  echo "Would insert admin root redirect into $BLOCK_NAME block in $ORVIX_CADDYFILE"
  echo "Backup would be: $backup"
  rm -f "$backup"
  rm -f "$tmp"
  trap - EXIT
  exit 0
fi

# ── Apply ───────────────────────────────────────────────────────
mv -f "$tmp" "$ORVIX_CADDYFILE"
trap - EXIT
rm -f "${ORVIX_CADDYFILE}.tmp."* 2>/dev/null || true

# Reload Caddy.
if ! "$ORVIX_SYSTEMCTL" reload caddy 2>/dev/null; then
  echo "ERROR: caddy reload failed — restoring backup $backup" >&2
  cp -p "$backup" "$ORVIX_CADDYFILE"
  if [ -n "$orig_owner" ] && [ -n "$orig_group" ]; then
    chown "${orig_owner}:${orig_group}" "$ORVIX_CADDYFILE" 2>/dev/null || true
  fi
  if [ -n "$orig_mode" ]; then
    chmod "$orig_mode" "$ORVIX_CADDYFILE" 2>/dev/null || true
  fi
  "$ORVIX_SYSTEMCTL" reload caddy 2>/dev/null || true
  exit 1
fi

echo "Admin root redirect inserted into $BLOCK_NAME block in $ORVIX_CADDYFILE (backup: $backup)"
exit 0
