#!/usr/bin/env bash
# external-backup-run.sh — orchestrates the daily ORVIX external backup:
# briefly quiesce orvix.service, stage a consistent tree, restart the service,
# hand the staging dir to Restic, run a fast metadata check, then run
# retention. Every gate is fail-closed:
#
#   free-space precheck fails => service is NOT touched, restic NEVER runs
#   quiesce fails             => stage.sh does NOT run; restart still attempted
#   staging failure           => restic NEVER runs (service already restarted)
#   restic backup failure     => restic check + retention NEVER run
#   restic check failure      => retention NEVER runs
#
# CONSISTENCY MODEL — read before changing the flow:
#   internal/coremail/storage/mailstore.go:PurgeMessage removes files from
#   disk BEFORE deleting the DB row (see os.RemoveAll at L329 and os.Remove
#   at L335 wrapping Messages.Purge at L331). If we snapshot the DB and then
#   the mail tree while orvix.service is live, a Purge that lands between
#   the two snapshots yields a DB row whose RFC822 file is missing from the
#   mail snapshot — a hard restore failure. VACUUM-INTO-first ordering in
#   stage.sh alone does NOT close this. The only correct fix is to stop
#   orvix.service for the duration of BOTH snapshots. That is what this
#   script does, wrapping ONLY the staging phase; restic upload runs after
#   the service is back up so the availability window is bounded to the
#   staging duration (seconds for a small install, scaling with mail tree
#   size).
#
#   FOLLOW-UP (not in this PR): internal/coremail/storage/mailstore.go
#   :WriteRFC822 (L414-L443) does a non-atomic os.WriteFile overwrite of
#   drafts; a snapshot mid-write could yield a truncated file. That needs
#   an application-side temp+rename fix; the service quiesce here narrows
#   the window but does not eliminate it if a client is actively saving a
#   draft at the exact moment stop is issued.
#
# The script loads credentials from /etc/orvix/external-backup.env, validates
# required env vars, and refuses to invoke restic with the credentials
# exported into its environment if the password file is not root-owned
# 0400/0600. `set -x` is intentionally never enabled — it would leak
# credentials from argv.

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

# Service quiesce knobs — overridable for tests.
ORVIX_SERVICE_NAME="${ORVIX_SERVICE_NAME:-orvix.service}"
SYSTEMCTL_BIN="${ORVIX_SYSTEMCTL_BIN:-systemctl}"
QUIESCE_STOP_TIMEOUT_SECS="${ORVIX_QUIESCE_STOP_TIMEOUT_SECS:-90}"

# Paths + free-space margin — overridable for tests.
ORVIX_MAIL_DIR="${ORVIX_MAIL_DIR:-/var/lib/orvix/coremail}"
ORVIX_DB_FILE="${ORVIX_DB_FILE:-/var/lib/orvix/orvix.db}"
CACHE_ROOT="${ORVIX_EXTERNAL_BACKUP_CACHE_ROOT:-/var/cache/orvix-external-backup}"

# Trap state — must live at global scope so the trap sees the latest values.
ORVIX_QUIESCED=0
ORVIX_RESUMED=0

log() { printf '[external-backup-run] %s\n' "$*"; }
die() { printf '[external-backup-run] ERROR: %s\n' "$*" >&2; exit 1; }

# Trap runs on EVERY exit path (success, error, signal). It must NEVER
# `set -e` — a failure inside the trap must not mask the original exit
# code, and must not prevent the remaining cleanup steps from running.
restore_service() {
    local rc=$?
    # 1. Service restore — unconditional if we quiesced but never resumed.
    #    `systemctl start` is idempotent (succeeds if already active).
    if [ "$ORVIX_QUIESCED" -eq 1 ] && [ "$ORVIX_RESUMED" -eq 0 ]; then
        log "TRAP: restoring ${ORVIX_SERVICE_NAME} (rc=$rc)"
        "$SYSTEMCTL_BIN" start "$ORVIX_SERVICE_NAME" || \
            printf '[external-backup-run] TRAP ERROR: systemctl start %s failed\n' \
                "$ORVIX_SERVICE_NAME" >&2 || true
    fi
    # 2. Staging cleanup — always attempt if we created one.
    if [ -n "$STAGING_DIR" ] && [ -d "$STAGING_DIR" ]; then
        rm -rf -- "$STAGING_DIR" || true
        log "cleaned staging $STAGING_DIR (exit=$rc)"
    fi
    exit "$rc"
}
trap restore_service EXIT HUP INT QUIT TERM ERR

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
# stage-only run cannot overlap with a scheduled full run.
exec 9>"$LOCK_FILE"
flock -n 9 || die "another external-backup run is in progress (lock: $LOCK_FILE)"

# --- 0. Free-space precheck --------------------------------------------
# Must run BEFORE quiesce: if the cache root can't hold ~2x the sources,
# there is no point taking the service down. We approximate required space
# as 2x(mail + db) — the VACUUM'd DB is typically smaller than the live
# DB, but doubling gives comfortable headroom.
log "free-space precheck"
install -d -m 0700 -o root -g root "$CACHE_ROOT"
required_bytes="$(du -sb "$ORVIX_MAIL_DIR" "$ORVIX_DB_FILE" 2>/dev/null | awk '{s+=$1} END {print s*2}')"
avail_bytes="$(df -B1 --output=avail "$CACHE_ROOT" | tail -n 1 | tr -dc '0-9')"
if [ -z "$required_bytes" ] || [ -z "$avail_bytes" ]; then
    die "free-space precheck could not compute sizes (required=$required_bytes avail=$avail_bytes)"
fi
if [ "$avail_bytes" -lt "$required_bytes" ]; then
    die "insufficient free space in $CACHE_ROOT: need ~${required_bytes}B, have ${avail_bytes}B — service was NOT touched"
fi
log "free-space ok: need=${required_bytes}B avail=${avail_bytes}B"

# --- 1. Quiesce orvix.service -----------------------------------------
# Set the flag BEFORE issuing stop so the trap will attempt restart even
# if stop errors partway through.
log "quiesce: stopping ${ORVIX_SERVICE_NAME} (timeout ${QUIESCE_STOP_TIMEOUT_SECS}s)"
ORVIX_QUIESCED=1
if ! timeout "$QUIESCE_STOP_TIMEOUT_SECS" "$SYSTEMCTL_BIN" stop "$ORVIX_SERVICE_NAME"; then
    die "systemctl stop ${ORVIX_SERVICE_NAME} failed or timed out — trap will attempt restart"
fi
# Verify stop actually took effect. `is-active` returns non-zero when the
# unit is not active; that is the success case here.
if "$SYSTEMCTL_BIN" is-active --quiet "$ORVIX_SERVICE_NAME"; then
    die "${ORVIX_SERVICE_NAME} still active after stop — refusing to snapshot mid-write"
fi
log "quiesce: ${ORVIX_SERVICE_NAME} stopped"

# --- 2. Staging (service is DOWN for this whole phase) -----------------
log "staging"
if ! STAGING_DIR="$("$STAGE_SCRIPT" | tail -n 1)"; then
    die "staging failed — restic will NOT run (service will be restored by trap)"
fi
[ -n "$STAGING_DIR" ] && [ -d "$STAGING_DIR" ] || die "staging did not produce a directory"

# --- 3. Resume orvix.service ------------------------------------------
# From here on, availability is restored. Anything below MUST NOT depend
# on the service being down. The trap disarms via ORVIX_RESUMED=1.
log "resume: starting ${ORVIX_SERVICE_NAME}"
if ! "$SYSTEMCTL_BIN" start "$ORVIX_SERVICE_NAME"; then
    die "systemctl start ${ORVIX_SERVICE_NAME} failed after staging — MANUAL INTERVENTION REQUIRED"
fi
if ! "$SYSTEMCTL_BIN" is-active --quiet "$ORVIX_SERVICE_NAME"; then
    die "${ORVIX_SERVICE_NAME} did not become active after start — MANUAL INTERVENTION REQUIRED"
fi
ORVIX_RESUMED=1
log "resume: ${ORVIX_SERVICE_NAME} is active"

# --- 4. Restic backup (service is UP) ---------------------------------
log "restic backup"
if ! restic backup --tag orvix-daily --host "$BACKUP_HOST" "$STAGING_DIR"; then
    die "restic backup failed — check + retention will NOT run"
fi

# --- 5. Metadata check (fast, every run) ------------------------------
log "restic check (metadata-only)"
if ! restic check; then
    die "restic check failed — retention will NOT run"
fi

# --- 6. Retention -----------------------------------------------------
log "restic forget --prune"
restic forget \
    --keep-hourly  "$RETENTION_KEEP_HOURLY" \
    --keep-daily   "$RETENTION_KEEP_DAILY" \
    --keep-weekly  "$RETENTION_KEEP_WEEKLY" \
    --keep-monthly "$RETENTION_KEEP_MONTHLY" \
    --prune \
    || die "restic forget failed"

log "external backup complete"
