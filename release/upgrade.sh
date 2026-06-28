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
ALLOW_UNSIGNED_LOCAL=0

usage() {
    cat <<USAGE
Usage:
  bash upgrade.sh [--dry-run] [binary-path]
  bash upgrade.sh --from-url <download-url> --checksum <sha256-hex>
                   [--checksum-file <sha256-file>] [--dry-run]
  bash upgrade.sh --from-url <download-url> --checksum-file <sha256-file>
                   [--dry-run]

Environment overrides:
  ORVIX_BIN          target binary path          (default /usr/local/bin/orvix)
  ORVIX_CONFIG       config path                 (default /etc/orvix/orvix.yaml)
  ORVIX_DATA_DIR     data directory              (default /var/lib/orvix)
  BACKUP_PARENT      upgrade-backup parent dir   (default /var/backups/orvix-upgrade)
  HEALTH_URL         health endpoint URL         (default http://127.0.0.1:8080/api/v1/health)

Behaviour:
  --dry-run                         print the plan + create the backup, but do
                                    not restart the service and do not overwrite
                                    the binary.

  --from-url <url>                  download the binary from <url>. REQUIRES a
                                    --checksum or --checksum-file flag. Without
                                    one of them, the upgrade fails closed before
                                    any state on the running host is mutated.

  --checksum <sha256-hex>           expected SHA256 of the new binary (64 lowercase
                                    hex chars). REQUIRED for --from-url upgrades.

  --checksum-file <sha256-file>     sha256sum-format file containing the expected
                                    hash. The file's first matching line is used;
                                    comments and blank lines are ignored.

  --allow-unsigned-local-artifact   DANGEROUS: skip checksum verification when
                                    using a LOCAL binary (no --from-url). This
                                    is for air-gapped dev workstations ONLY. The
                                    flag is refused for --from-url upgrades. The
                                    upgrade prints a loud red warning when this
                                    flag is set so production operators cannot
                                    accidentally use it.

Production-readiness gate BLOCKER 5: checksum verification is
FAIL-CLOSED. The script refuses to install a binary whose SHA256
does not match the operator-supplied expected hash, AND refuses to
install any binary (URL or local) whose expected hash is missing
unless --allow-unsigned-local-artifact is set.
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

# resolve_expected_sha returns the expected SHA256 hex string for
# the given file path, sourced from --checksum or --checksum-file.
# Echoes the empty string if no expected value is available.
resolve_expected_sha() {
    local file="$1"
    if [ -n "$EXPECTED_SHA" ]; then
        printf '%s' "$EXPECTED_SHA"
        return 0
    fi
    if [ -n "$CHECKSUM_FILE" ] && [ -f "$CHECKSUM_FILE" ]; then
        awk -v target="$(basename "$file")" \
            '$2 == target { print $1; exit }' \
            "$CHECKSUM_FILE" 2>/dev/null || true
        return 0
    fi
    return 0
}

# verify_checksum_fail_closed is the production-readiness gate
# BLOCKER 5 enforcement. Returns 0 only when:
#   - an expected sha256 was supplied (via --checksum / --checksum-file), AND
#   - the sha256 of $1 matches it byte-for-byte.
# Returns non-zero (and prints a loud failure) when:
#   - no expected sha256 is supplied AND the source is --from-url
#     (always fail closed for downloaded artifacts),
#   - no expected sha256 is supplied AND the source is a LOCAL
#     file AND --allow-unsigned-local-artifact was NOT set,
#   - the sha256 mismatches.
# Note: --allow-unsigned-local-artifact is ignored when --from-url
# is set. URL downloads are always fail-closed.
verify_checksum_fail_closed() {
    local file="$1"
    local expected
    expected="$(resolve_expected_sha "$file")"
    local actual
    actual="$(sha256_of_file "$file")"

    if [ -n "$expected" ]; then
        if [ "$actual" != "$expected" ]; then
            printf '%bFAIL%b sha256 mismatch for %s\n' "$RED" "$NC" "$file" >&2
            printf '  expected: %s\n' "$expected" >&2
            printf '  actual:   %s\n' "$actual" >&2
            return 1
        fi
        printf '%bOK%b sha256 %s verified for %s\n' "$GREEN" "$NC" "$actual" "$file" >&2
        return 0
    fi

    # No expected sha256 was supplied.
    if [ -n "$FROM_URL" ]; then
        printf '%bFAIL%b missing checksum for downloaded artifact %s\n' "$RED" "$NC" "$file" >&2
        printf '  --from-url upgrades REQUIRE --checksum or --checksum-file.\n' >&2
        printf '  refusing to install an unverified downloaded binary.\n' >&2
        return 1
    fi

    if [ "$ALLOW_UNSIGNED_LOCAL" = "1" ]; then
        printf '%bWARN%b installing UNVERIFIED local artifact %s (sha256 %s)\n' "$YELLOW" "$NC" "$file" "$actual" >&2
        printf '  --allow-unsigned-local-artifact is DANGEROUS. Do not use on production.\n' >&2
        return 0
    fi

    printf '%bFAIL%b missing checksum for %s\n' "$RED" "$NC" "$file" >&2
    printf '  refusing to install a binary without integrity verification.\n' >&2
    printf '  Pass --checksum <sha256-hex> or --checksum-file <sha256-file>,\n' >&2
    printf '  or --allow-unsigned-local-artifact for air-gapped dev only.\n' >&2
    return 1
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
    # Production-readiness gate BLOCKER 5: checksum is verified
    # BEFORE any production state is mutated. We do this AFTER
    # preflight_backup so a missing/bad checksum aborts with a
    # rollback dir already in place, and BEFORE the dry-run exit
    # so a --dry-run with no checksum reports what would have
    # been required (loud red message).
    log "verifying checksum (fail-closed)"
    if ! verify_checksum_fail_closed "$NEW_BIN"; then
        if [ "$DRY_RUN" = "1" ]; then
            log "DRY-RUN: checksum verification would fail; the upgrade would be aborted here"
        fi
        fail "checksum verification failed (fail-closed)"
    fi

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
            --allow-unsigned-local-artifact)
                # Production-readiness gate BLOCKER 5: this flag is
                # the ONLY way to skip checksum verification, and
                # it only works for LOCAL artifacts (no --from-url).
                # Refuse it loudly if combined with --from-url.
                ALLOW_UNSIGNED_LOCAL=1
                shift
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

    # Production-readiness gate BLOCKER 5 enforcement:
    # --allow-unsigned-local-artifact is refused for URL upgrades.
    if [ -n "$FROM_URL" ] && [ "$ALLOW_UNSIGNED_LOCAL" = "1" ]; then
        fail "--allow-unsigned-local-artifact is refused for --from-url upgrades (URL downloads are always fail-closed)"
    fi

    log "Orvix upgrade starting"
    log "binary target: $ORVIX_BIN"
    log "config: $ORVIX_CONFIG"
    log "data dir: $ORVIX_DATA_DIR"
    log "dry-run: $DRY_RUN"
    log "from-url: ${FROM_URL:-<none, local file>}"
    log "checksum: ${EXPECTED_SHA:-<none>}"
    log "checksum-file: ${CHECKSUM_FILE:-<none>}"
    if [ "$ALLOW_UNSIGNED_LOCAL" = "1" ]; then
        log "WARNING: --allow-unsigned-local-artifact set; checksum verification will be SKIPPED for the local artifact. Production-readiness gate BLOCKER 5 disables this for downloaded artifacts."
    fi

    preflight_backup
    resolve_input "${POSITIONAL[0]:-}"
    install_and_restart
}

main "$@"