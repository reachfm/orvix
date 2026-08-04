#!/usr/bin/env bash
# external-backup-check.sh — periodic Restic repository integrity check.
#
#   weekly  => restic check --read-data-subset=5%
#   monthly => restic check --read-data
#
# Read-only against the repository; a courtesy flock is used to serialize with
# other external-backup runs on the same host.

set -euo pipefail

LOCK_FILE="/var/lock/orvix-external-backup.lock"
ENV_FILE="${ORVIX_EXTERNAL_BACKUP_ENV:-/etc/orvix/external-backup.env}"

log() { printf '[external-backup-check] %s\n' "$*"; }
die() { printf '[external-backup-check] ERROR: %s\n' "$*" >&2; exit 1; }

MODE="${1:-}"
case "$MODE" in
    weekly|monthly) : ;;
    *) die "usage: external-backup-check.sh <weekly|monthly>" ;;
esac

[ "$(id -u)" -eq 0 ] || die "must run as root"
command -v restic >/dev/null 2>&1 || die "restic not found in PATH"
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

exec 9>"$LOCK_FILE"
flock -n 9 || die "another external-backup run is in progress (lock: $LOCK_FILE)"

case "$MODE" in
    weekly)
        log "restic check --read-data-subset=5%"
        restic check --read-data-subset=5%
        ;;
    monthly)
        log "restic check --read-data"
        restic check --read-data
        ;;
esac
