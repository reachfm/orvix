#!/usr/bin/env bash
# external-backup-restore-drill.sh — restore the latest snapshot into an
# ISOLATED directory (never into the running install), then verify the
# database with PRAGMA integrity_check. Drill artifacts are left in place
# for operator inspection.

set -euo pipefail

ENV_FILE="${ORVIX_EXTERNAL_BACKUP_ENV:-/etc/orvix/external-backup.env}"
TARGET=""

log() { printf '[external-backup-restore-drill] %s\n' "$*"; }
die() { printf '[external-backup-restore-drill] ERROR: %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
    case "$1" in
        --target)
            [ $# -ge 2 ] || die "--target requires an argument"
            TARGET="$2"; shift 2 ;;
        *) die "unknown arg: $1 (usage: --target <dir>)" ;;
    esac
done

[ -n "$TARGET" ] || die "--target <dir> is required"

# Reject any target that could overwrite the live install.
case "$TARGET" in
    "/"|"") die "refusing target: $TARGET" ;;
    /var/lib/orvix|/var/lib/orvix/*) die "refusing target under /var/lib/orvix: $TARGET" ;;
    /etc/orvix|/etc/orvix/*) die "refusing target under /etc/orvix: $TARGET" ;;
    /var/lib/caddy|/var/lib/caddy/*) die "refusing target under /var/lib/caddy: $TARGET" ;;
    /usr/local/bin|/usr/local/bin/*) die "refusing target under /usr/local/bin: $TARGET" ;;
    /usr/share/orvix|/usr/share/orvix/*) die "refusing target under /usr/share/orvix: $TARGET" ;;
esac

# Also refuse if the target is a parent of any live install path.
for live in /var/lib/orvix /etc/orvix /usr/local/bin/orvix /usr/share/orvix; do
    case "$live" in
        "$TARGET"|"$TARGET"/*) die "refusing target $TARGET: parent of live path $live" ;;
    esac
done

[ "$(id -u)" -eq 0 ] || die "must run as root"
command -v restic >/dev/null 2>&1 || die "restic not found in PATH"
command -v sqlite3 >/dev/null 2>&1 || die "sqlite3 not found in PATH"
[ -f "$ENV_FILE" ] || die "missing env file $ENV_FILE"

set -a
# shellcheck disable=SC1090
. "$ENV_FILE"
set +a

for v in RESTIC_REPOSITORY RESTIC_PASSWORD_FILE AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_DEFAULT_REGION; do
    if [ -z "${!v:-}" ]; then
        die "$v not set in $ENV_FILE"
    fi
done

install -d -m 0700 -o root -g root "$TARGET"

log "restic restore latest --target $TARGET"
restic restore latest --target "$TARGET" || die "restic restore failed"

# The restored tree preserves the staging layout; find the DB inside it.
DB_FILE="$(find "$TARGET" -type f -name orvix.db -path '*/database/*' -print -quit || true)"
[ -n "$DB_FILE" ] || die "restored tree missing database/orvix.db"

log "sqlite3 PRAGMA integrity_check on $DB_FILE"
RESULT="$(sqlite3 "$DB_FILE" 'PRAGMA integrity_check;')"
if [ "$RESULT" != "ok" ]; then
    die "integrity_check failed: $RESULT"
fi
log "integrity_check: ok"

# Summary — never print secret contents.
FILE_COUNT="$(find "$TARGET" -type f | wc -l | tr -d ' ')"
TOTAL_BYTES="$(du -sb "$TARGET" | awk '{print $1}')"
log "drill complete: files=$FILE_COUNT bytes=$TOTAL_BYTES target=$TARGET"
