#!/usr/bin/env bash
# external-backup-run.sh — orchestrates the hourly ORVIX external backup:
# stage a consistent tree, hand it to Restic, run a fast metadata check, then
# run retention. Every gate is fail-closed:
#
#   staging failure  => restic NEVER runs
#   restic backup fx => restic check + retention NEVER run
#   restic check fx  => retention NEVER runs
#
# The script loads credentials from /etc/orvix/external-backup.env, validates
# required env vars, and refuses to invoke a restic subprocess with the
# credentials exported into its environment if the password file is not
# root-owned 0400/0600. `set -x` is intentionally never enabled.

set -euo pipefail

LOCK_FILE="/var/lock/orvix-external-backup.lock"
ENV_FILE="${ORVIX_EXTERNAL_BACKUP_ENV:-/etc/orvix/external-backup.env}"
STAGING_DIR=""
BACKUP_HOST="${ORVIX_BACKUP_HOST:-mail}"

# Retention defaults — see docs/backup/hetzner-object-storage-restic.md.
RETENTION_KEEP_HOURLY="${RETENTION_KEEP_HOURLY:-24}"
RETENTION_KEEP_DAILY="${RETENTION_KEEP_DAILY:-14}"
RETENTION_KEEP_WEEKLY="${RETENTION_KEEP_WEEKLY:-8}"
RETENTION_KEEP_MONTHLY="${RETENTION_KEEP_MONTHLY:-12}"

# Stage script path — overridable for tests.
STAGE_SCRIPT="${ORVIX_EXTERNAL_BACKUP_STAGE_SCRIPT:-/usr/share/orvix/scripts/external-backup-stage.sh}"

log() { printf '[external-backup-run] %s\n' "$*"; }
die() { printf '[external-backup-run] ERROR: %s\n' "$*" >&2; exit 1; }

cleanup() {
    local rc=$?
    if [ -n "$STAGING_DIR" ] && [ -d "$STAGING_DIR" ]; then
        rm -rf -- "$STAGING_DIR" || true
        log "cleaned staging $STAGING_DIR (exit=$rc)"
    fi
    exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT HUP QUIT TERM

[ "$(id -u)" -eq 0 ] || die "must run as root"

command -v restic >/dev/null 2>&1 || die "restic not found in PATH — install it (apt-get install restic) before enabling the timer"

[ -f "$ENV_FILE" ] || die "missing env file $ENV_FILE — see docs/backup/hetzner-object-storage-restic.md"

# Validate env file perms (defense-in-depth).
env_mode="$(stat -c '%a' "$ENV_FILE" 2>/dev/null || echo '')"
case "$env_mode" in
    600|400) : ;;
    *) die "$ENV_FILE has mode $env_mode; require 0600 or 0400 root:root" ;;
esac

# Load env, then verify every required variable is present.
set -a
# shellcheck disable=SC1090
. "$ENV_FILE"
set +a

for v in RESTIC_REPOSITORY RESTIC_PASSWORD_FILE AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_DEFAULT_REGION; do
    if [ -z "${!v:-}" ]; then
        die "$v not set in $ENV_FILE"
    fi
done

[ -f "$RESTIC_PASSWORD_FILE" ] || die "RESTIC_PASSWORD_FILE=$RESTIC_PASSWORD_FILE does not exist"
pw_mode="$(stat -c '%a' "$RESTIC_PASSWORD_FILE" 2>/dev/null || echo '')"
pw_owner="$(stat -c '%U' "$RESTIC_PASSWORD_FILE" 2>/dev/null || echo '')"
case "$pw_mode" in
    400|600) : ;;
    *) die "$RESTIC_PASSWORD_FILE has mode $pw_mode; require 0400 or 0600" ;;
esac
[ "$pw_owner" = "root" ] || die "$RESTIC_PASSWORD_FILE not owned by root"

# Single-instance flock, shared with the staging script so a manual
# stage-only run cannot overlap with an hourly full run.
exec 9>"$LOCK_FILE"
flock -n 9 || die "another external-backup run is in progress (lock: $LOCK_FILE)"

# --- 1. Staging ---------------------------------------------------------
log "staging"
if ! STAGING_DIR="$("$STAGE_SCRIPT" | tail -n 1)"; then
    die "staging failed — restic will NOT run"
fi
[ -n "$STAGING_DIR" ] && [ -d "$STAGING_DIR" ] || die "staging did not produce a directory"

# --- 2. Restic backup ---------------------------------------------------
log "restic backup"
if ! restic backup --tag orvix-hourly --host "$BACKUP_HOST" "$STAGING_DIR"; then
    die "restic backup failed — check + retention will NOT run"
fi

# --- 3. Metadata check (fast, every run) --------------------------------
log "restic check (metadata-only)"
if ! restic check; then
    die "restic check failed — retention will NOT run"
fi

# --- 4. Retention -------------------------------------------------------
log "restic forget --prune"
restic forget \
    --keep-hourly  "$RETENTION_KEEP_HOURLY" \
    --keep-daily   "$RETENTION_KEEP_DAILY" \
    --keep-weekly  "$RETENTION_KEEP_WEEKLY" \
    --keep-monthly "$RETENTION_KEEP_MONTHLY" \
    --prune \
    || die "restic forget failed"

log "external backup complete"
