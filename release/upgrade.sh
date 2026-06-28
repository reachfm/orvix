#!/usr/bin/env bash
set -euo pipefail

# Orvix Upgrade Script (operator-initiated).
#
# Usage:
#   sudo bash upgrade.sh [binary-path]
#   sudo bash upgrade.sh --from-url <download-url> [--checksum-file <sha256-file>]
#   sudo bash upgrade.sh --dry-run [binary-path]
#
# If no argument is supplied, the script looks for a local file
# `orvix-linux-amd64` in the current working directory; this is
# the supported path on a hardened VPS where outbound HTTP is
# not allowed. Operators who DO want to fetch from a release
# server must pass --from-url explicitly — the script no longer
# hits https://releases.orvix.email by default because that
# domain does not exist in this build.
#
# Pre-flight:
#   1. Run `orvix admin backup create` (or the admin UI backup
#      flow) BEFORE invoking this script. upgrade.sh makes a
#      backup of /etc/orvix and the binary, but it cannot
#      snapshot a running SQLite database without a
#      coordinated `VACUUM INTO` — that's the runtime's job.
#   2. Read /etc/orvix/orvix.yaml after upgrade for any new
#      required fields.
#   3. Verify the SHA256 of the new binary against
#      release/checksums.txt when present.

BOLD=$'\033[1m'
RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
YELLOW=$'\033[1;33m'
NC=$'\033[0m'

ORVIX_BIN="${ORVIX_BIN:-/usr/local/bin/orvix}"
ORVIX_CONFIG="${ORVIX_CONFIG:-/etc/orvix/orvix.yaml}"
ORVIX_DATA_DIR="${ORVIX_DATA_DIR:-/var/lib/orvix}"
ORVIX_LOG_DIR="${ORVIX_LOG_DIR:-/var/log/orvix}"
BACKUP_PARENT="${BACKUP_PARENT:-/var/backups/orvix-upgrade}"
HEALTH_URL="${HEALTH_URL:-http://127.0.0.1:8080/api/v1/health}"
HEALTH_MAX_ATTEMPTS="${HEALTH_MAX_ATTEMPTS:-30}"
HEALTH_INTERVAL="${HEALTH_INTERVAL:-2}"

DRY_RUN=0
FROM_URL=""
CHECKSUM_FILE=""

usage() {
    cat <<USAGE
Usage:
  bash upgrade.sh [--dry-run] [binary-path]
  bash upgrade.sh --from-url <download-url> [--checksum-file <sha256-file>]
                   [--checksum <sha256-hex>] [--dry-run]

Environment overrides:
  ORVIX_BIN          target binary path          (default /usr/local/bin/orvix)
  ORVIX_CONFIG       config path                 (default /etc/orvix/orvix.yaml)
  ORVIX_DATA_DIR     data directory              (default /var/lib/orvix)
  BACKUP_PARENT      upgrade-backup parent dir   (default /var/backups/orvix-upgrade)
  HEALTH_URL         health endpoint URL         (default http://127.0.0.1:8080/api/v1/health)

Behaviour:
  --dry-run               print the plan + create the backup, but do not
                          restart the service and do not overwrite the binary.
  --from-url <url>        download the binary from <url> instead of using a
                          local file. The binary's SHA256 is verified when
                          --checksum / --checksum-file is supplied.
  --checksum <sha256>     expected SHA256 of the downloaded binary.
  --checksum-file <path>  path to a sha256sum-format file containing the
                          expected hash. The file's first matching line
                          is used; comments and blank lines are ignored.
USAGE
}

log() {
    printf '[%s] %s\n' "$(date -Is)" "$*" >&2
}

fail() {
    printf '%bERROR:%b %s\n' "$RED" "$NC" "$*" >&2
    exit 1
}

require_root() {
    [ "$(id -u)" -eq 0 ] || fail "must be run as root (or with sudo)"
}

# sha256_of_file prints the SHA256 of $1 in lowercase hex.
sha256_of_file() {
    sha256sum "$1" | awk '{print $1}'
}

# verify_checksum compares $1 against an expected SHA256, where
# the expected value is sourced from either --checksum=<hex>
# or a sha256sum-format file at $2. Returns 0 if matched, 1
# otherwise. Refuses to silently downgrade on mismatch.
verify_checksum() {
    local file="$1"
    local expected=""
    if [ -n "$EXPECTED_SHA" ]; then
        expected="$EXPECTED_SHA"
    elif [ -n "$CHECKSUM_FILE" ] && [ -f "$CHECKSUM_FILE" ]; then
        expected="$(awk -v target="$(basename "$file")" \
            '$2 == target { print $1; exit }' \
            "$CHECKSUM_FILE" 2>/dev/null || true)"
    fi
    if [ -z "$expected" ]; then
        log "WARN: no checksum supplied; skipping integrity verification of $file"
        return 0
    fi
    local actual
    actual="$(sha256_of_file "$file")"
    if [ "$actual" != "$expected" ]; then
        printf '%bFAIL%b sha256 mismatch for %s\n' "$RED" "$NC" "$file" >&2
        printf '  expected: %s\n' "$expected" >&2
        printf '  actual:   %s\n' "$actual" >&2
        return 1
    fi
    printf '%bOK%b sha256 %s verified for %s\n' "$GREEN" "$NC" "$actual" "$file" >&2
    return 0
}

# verify_health polls the health endpoint until it returns 200
# or the attempt budget is exhausted. Returns 0 on healthy, 1
# on timeout. Used after every restart so we never declare
# success on a half-up service.
verify_health() {
    local attempt
    for attempt in $(seq 1 "$HEALTH_MAX_ATTEMPTS"); do
        local code
        code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 "$HEALTH_URL" 2>/dev/null || echo '000')"
        if [ "$code" = "200" ]; then
            log "health OK after $attempt attempt(s)"
            return 0
        fi
        log "health attempt $attempt/$HEALTH_MAX_ATTEMPTS: HTTP $code"
        sleep "$HEALTH_INTERVAL"
    done
    return 1
}

# preflight_backup copies every file the upgrade path needs to
# be able to roll back from. Each target is logged with its
# SHA256 so the operator can later sanity-check the rollback.
preflight_backup() {
    local backup_dir="$BACKUP_PARENT/$(date -u +%Y%m%dT%H%M%SZ)"
    mkdir -p "$backup_dir"
    log "backup directory: $backup_dir"

    local file
    for file in \
        "$ORVIX_BIN" \
        "$ORVIX_CONFIG" \
        "$ORVIX_DATA_DIR/orvix.db" \
        "$ORVIX_DATA_DIR/jwt_key.pem" \
        "$ORVIX_DATA_DIR/license-cache.json" \
        /etc/orvix/vapid_private.key \
        /etc/orvix/vapid_public.key \
        /etc/orvix/license.json \
        /etc/orvix/bootstrap.env
    do
        if [ -e "$file" ]; then
            local dest="$backup_dir$(dirname "$file")"
            mkdir -p "$dest"
            cp -a "$file" "$dest/" || fail "could not back up $file"
            if [ -f "$file" ]; then
                log "  backed up $file (sha256 $(sha256_of_file "$file" 2>/dev/null || echo unknown))"
            else
                log "  backed up $file"
            fi
        else
            log "  skip $file (not present)"
        fi
    done

    if [ -d "$ORVIX_DATA_DIR/coremail" ]; then
        # The mailstore can be huge. We don't snapshot the
        # directory itself (that is the runtime backup's job);
        # we just record its size + file count for awareness.
        local count size
        count=$(find "$ORVIX_DATA_DIR/coremail" -type f 2>/dev/null | wc -l)
        size=$(du -sh "$ORVIX_DATA_DIR/coremail" 2>/dev/null | cut -f1 || echo unknown)
        log "  mailstore: $count files, $size (NOT snapshotted; run runtime backup before upgrade)"
        printf 'mailstore_files=%s\nmailstore_size=%s\n' "$count" "$size" >"$backup_dir/mailstore.summary"
    fi

    printf 'backup_dir=%s\ncreated_at=%s\n' "$backup_dir" "$(date -Is)" >"$BACKUP_PARENT/.latest"
    log "wrote $BACKUP_PARENT/.latest"
    BACKUP_DIR="$backup_dir"
}

resolve_input() {
    # Returns the local path of the binary to install, writing
    # it to NEW_BIN. Handles both --from-url and a positional
    # argument / default lookup.
    if [ -n "$FROM_URL" ]; then
        local tmp
        tmp="$(mktemp /tmp/orvix-upgrade.XXXXXX)"
        log "downloading $FROM_URL -> $tmp"
        if ! curl -fsSL --retry 3 --max-time 600 -o "$tmp" "$FROM_URL"; then
            rm -f "$tmp"
            fail "download failed: $FROM_URL"
        fi
        NEW_BIN="$tmp"
    else
        local candidate
        if [ $# -gt 0 ]; then
            candidate="$1"
        elif [ -f "./orvix-linux-amd64" ]; then
            candidate="./orvix-linux-amd64"
        elif [ -f "$ORVIX_SOURCE_DIR/release/orvix-linux-amd64" ]; then
            candidate="$ORVIX_SOURCE_DIR/release/orvix-linux-amd64"
        else
            fail "no binary supplied; pass a path or use --from-url <url>"
        fi
        if [ ! -f "$candidate" ]; then
            fail "binary not found: $candidate"
        fi
        NEW_BIN="$candidate"
    fi
}

install_and_restart() {
    log "verifying checksum"
    verify_checksum "$NEW_BIN" || fail "checksum verification failed"

    if [ "$DRY_RUN" = "1" ]; then
        log "DRY-RUN: would replace $ORVIX_BIN with $NEW_BIN"
        log "DRY-RUN: would restart orvix.service and probe $HEALTH_URL"
        return 0
    fi

    log "installing $NEW_BIN -> $ORVIX_BIN"
    install -m 0755 "$NEW_BIN" "$ORVIX_BIN"
    if [ -n "${FROM_URL:-}" ]; then
        rm -f "$NEW_BIN"
    fi

    log "restarting orvix.service"
    if ! systemctl restart orvix.service; then
        log "WARN: systemctl restart failed; rolling back to $BACKUP_DIR"
        if [ -f "$BACKUP_DIR$ORVIX_BIN" ]; then
            install -m 0755 "$BACKUP_DIR$ORVIX_BIN" "$ORVIX_BIN"
            systemctl restart orvix.service || true
        fi
        fail "service restart failed and rollback attempted; check journalctl -u orvix.service"
    fi

    if verify_health; then
        log "upgrade complete; backup preserved at $BACKUP_DIR"
    else
        log "WARN: health probe failed after $HEALTH_MAX_ATTEMPTS attempts"
        log "WARN: rolling back to $BACKUP_DIR"
        if [ -f "$BACKUP_DIR$ORVIX_BIN" ]; then
            install -m 0755 "$BACKUP_DIR$ORVIX_BIN" "$ORVIX_BIN"
            systemctl restart orvix.service || true
        fi
        fail "service unhealthy after upgrade; rolled back"
    fi
}

main() {
    # Parse args FIRST so --help works without root. Operators
    # running the script for the first time on a workstation
    # before SSHing into the VPS need to see the usage banner
    # without having to sudo first.
    POSITIONAL=()
    while [ $# -gt 0 ]; do
        case "$1" in
            -h|--help)
                usage
                exit 0
                ;;
        esac
        break
    done

    require_root
    BACKUP_DIR=""

    # Parse args.
    POSITIONAL=()
    while [ $# -gt 0 ]; do
        case "$1" in
            --dry-run)
                DRY_RUN=1
                shift
                ;;
            --from-url)
                [ $# -ge 2 ] || fail "--from-url requires a value"
                FROM_URL="$2"
                shift 2
                ;;
            --checksum)
                [ $# -ge 2 ] || fail "--checksum requires a value"
                EXPECTED_SHA="$2"
                shift 2
                ;;
            --checksum-file)
                [ $# -ge 2 ] || fail "--checksum-file requires a value"
                CHECKSUM_FILE="$2"
                shift 2
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            --)
                shift
                POSITIONAL+=("$@")
                break
                ;;
            -*)
                fail "unknown flag: $1 (try --help)"
                ;;
            *)
                POSITIONAL+=("$1")
                shift
                ;;
        esac
    done

    log "Orvix upgrade starting"
    log "binary target: $ORVIX_BIN"
    log "config: $ORVIX_CONFIG"
    log "data dir: $ORVIX_DATA_DIR"
    log "dry-run: $DRY_RUN"

    preflight_backup
    resolve_input "${POSITIONAL[0]:-}"
    install_and_restart
}

main "$@"