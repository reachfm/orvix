#!/usr/bin/env bash
# external-backup-stage.sh — build a consistent staging tree of everything the
# operator needs to restore an ORVIX host from bare metal into a fresh, private,
# root-only directory. This script does NOT talk to Hetzner or Restic — that is
# external-backup-run.sh's job. Keeping the two separate lets us test the
# staging producer without a network dependency.
#
# CONSISTENCY MODEL (read before changing the order below):
#   internal/coremail/storage/mailstore.go:StoreMessage writes the RFC822 file
#   to disk via os.WriteFile BEFORE the DB row is inserted (no atomic
#   temp+rename, no fsync). On INSERT failure the file is os.Remove'd. There is
#   no application quiesce endpoint we can call from here.
#
#   Therefore we snapshot the DATABASE FIRST (via SQLite VACUUM INTO, which is
#   the SQLite-native atomic snapshot that is safe against concurrent writers),
#   then the mail directory. A message that arrives between the two snapshots
#   will exist on disk in the mail snapshot but be absent from the DB snapshot
#   — these are benign "orphan files" that a restore procedure can quarantine.
#
#   Do NOT reverse this order. Snapshotting the mail dir before the DB would
#   create DB rows referencing files that the earlier disk snapshot missed,
#   which is a hard restore failure.
#
# The script fails closed on missing required sources, refuses to run as
# non-root, holds a flock so it never overlaps with itself or with the Restic
# wrapper, and prints only path names + byte counts (never secret contents).
# `set -x` is intentionally NEVER enabled: it would print the argv of `install`
# and `cp`, some of which reference secret files.

set -euo pipefail

LOCK_FILE="/var/lock/orvix-external-backup.lock"
CACHE_ROOT="/var/cache/orvix-external-backup"
STAGING_DIR=""

log() { printf '[external-backup-stage] %s\n' "$*"; }
die() { printf '[external-backup-stage] ERROR: %s\n' "$*" >&2; exit 1; }

cleanup() {
    local rc=$?
    if [ -n "$STAGING_DIR" ] && [ -d "$STAGING_DIR" ]; then
        # Run on EVERY exit path (success, error, signal). This is the only
        # thing we clean up because we never touched the live ORVIX service.
        rm -rf -- "$STAGING_DIR" || true
        log "cleaned staging $STAGING_DIR (exit=$rc)"
    fi
    exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT HUP QUIT TERM

[ "$(id -u)" -eq 0 ] || die "must run as root"

# Single-instance flock. FD 9 stays open for the life of the process; the lock
# is released implicitly when this process exits (including on trap cleanup).
exec 9>"$LOCK_FILE"
flock -n 9 || die "another external-backup run is in progress (lock: $LOCK_FILE)"

# Required sources — fail closed if any are missing.
REQUIRED_PATHS=(
    "/var/lib/orvix/orvix.db"
    "/var/lib/orvix/coremail"
    "/var/lib/orvix/jwt_key.pem"
    "/var/lib/orvix/encryption_key"
    "/etc/orvix/orvix.yaml"
    "/etc/orvix/backup_encryption.key"
    "/etc/orvix/vapid_private.key"
)
for p in "${REQUIRED_PATHS[@]}"; do
    [ -e "$p" ] || die "required source missing: $p"
done

command -v sqlite3 >/dev/null 2>&1 || die "sqlite3 not found in PATH"

# Ensure cache root exists at 0700 root:root.
install -d -m 0700 -o root -g root "$CACHE_ROOT"

STAGING_DIR="$(mktemp -d -p "$CACHE_ROOT" staging.XXXXXXXX)"
chmod 0700 "$STAGING_DIR"

install -d -m 0700 -o root -g root \
    "$STAGING_DIR/database" \
    "$STAGING_DIR/mail" \
    "$STAGING_DIR/config" \
    "$STAGING_DIR/caddy"

# (1) Database snapshot FIRST — VACUUM INTO is SQLite's atomic snapshot,
# safe against concurrent writers. See consistency note at top of file.
log "snapshotting database (VACUUM INTO)"
sqlite3 /var/lib/orvix/orvix.db \
    "VACUUM INTO '$STAGING_DIR/database/orvix.db'" \
    || die "sqlite VACUUM INTO failed"
chmod 0600 "$STAGING_DIR/database/orvix.db"

# (2) Mail directory SECOND. Any message that lands after step (1) will be
# tolerated at restore as an orphan file.
log "copying mail directory"
cp -a /var/lib/orvix/coremail/. "$STAGING_DIR/mail/"

# (3) Secrets + config at 0600.
log "copying secrets and config"
install -m 0600 -o root -g root /var/lib/orvix/jwt_key.pem       "$STAGING_DIR/config/jwt_key.pem"
install -m 0600 -o root -g root /var/lib/orvix/encryption_key    "$STAGING_DIR/config/encryption_key"
install -m 0600 -o root -g root /etc/orvix/orvix.yaml            "$STAGING_DIR/config/orvix.yaml"
install -m 0600 -o root -g root /etc/orvix/backup_encryption.key "$STAGING_DIR/config/backup_encryption.key"
install -m 0600 -o root -g root /etc/orvix/vapid_private.key     "$STAGING_DIR/config/vapid_private.key"

# (4) Caddy certs — OPTIONAL. Skip cleanly if absent (fresh HTTPS not yet
# configured, or Caddy running out-of-tree).
CADDY_SRC="/var/lib/caddy/.local/share/caddy/certificates"
if [ -d "$CADDY_SRC" ]; then
    log "copying caddy certificates"
    cp -a "$CADDY_SRC/." "$STAGING_DIR/caddy/"
else
    log "skipping caddy certs (not present at $CADDY_SRC)"
fi

# Report — sizes only, never contents.
DB_BYTES=$(stat -c '%s' "$STAGING_DIR/database/orvix.db")
MAIL_BYTES=$(du -sb "$STAGING_DIR/mail" | awk '{print $1}')
log "staging ready: db=${DB_BYTES}B mail=${MAIL_BYTES}B path=$STAGING_DIR"

# Suppress the EXIT trap: we want the staging dir to survive so the caller
# (external-backup-run.sh) can hand it to restic. The caller re-installs its
# own trap that removes this directory when restic finishes.
trap - EXIT INT HUP QUIT TERM

# Emit staging path on the LAST stdout line for the caller to consume.
printf '%s\n' "$STAGING_DIR"
