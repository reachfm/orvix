#!/usr/bin/env bash
set -euo pipefail

# Orvix Upgrade Script (operator-initiated).
#
# Usage:
#   sudo bash upgrade.sh [binary-path]
#   sudo bash upgrade.sh --from-url <download-url> [--checksum-file <sha256-file>]
#   sudo bash upgrade.sh --dry-run [binary-path]
#   sudo bash upgrade.sh --dev-unsafe [binary-path]
#
# If no argument is supplied, the script looks for a local file
# `orvix-linux-amd64` in the current working directory; this is
# the supported path on a hardened VPS where outbound HTTP is
# not allowed. Operators who DO want to fetch from a release
# server must pass --from-url explicitly G�� the script no longer
# hits https://releases.orvix.email by default because that
# domain does not exist in this build.
#
# Pre-flight:
#   1. Run `orvix admin backup create` (or the admin UI backup
#      flow) BEFORE invoking this script. upgrade.sh makes a
#      backup of /etc/orvix and the binary, but it cannot
#      snapshot a running SQLite database without a
#      coordinated `VACUUM INTO` G�� that's the runtime's job.
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
ORVIX_JWT_KEY="${ORVIX_JWT_KEY:-/var/lib/orvix/jwt_key.pem}"
ORVIX_VAPID_PRIVATE_KEY="${ORVIX_VAPID_PRIVATE_KEY:-/etc/orvix/vapid_private.key}"
ORVIX_VAPID_PUBLIC_KEY="${ORVIX_VAPID_PUBLIC_KEY:-/etc/orvix/vapid_public.key}"
ORVIX_BACKUP_ENCRYPTION_KEY="${ORVIX_BACKUP_ENCRYPTION_KEY:-/etc/orvix/backup_encryption.key}"
ORVIX_DKIM_DIR="${ORVIX_DKIM_DIR:-/var/lib/orvix/dkim}"
ORVIX_DOCTOR_SCRIPT="${ORVIX_DOCTOR_SCRIPT:-/usr/share/orvix/scripts/orvix-doctor.sh}"
ORVIX_SOURCE_DIR="${ORVIX_SOURCE_DIR:-$(pwd)}"
ORVIX_UPGRADE_LOCK="${ORVIX_UPGRADE_LOCK:-/run/lock/orvix-upgrade.lock}"
ORVIX_REQUIRE_RELEASE_SIGNATURE="${ORVIX_REQUIRE_RELEASE_SIGNATURE:-1}"
ORVIX_RELEASE_VERIFYING_KEY_FILE="${ORVIX_RELEASE_VERIFYING_KEY_FILE:-}"
ORVIX_CADDYFILE="${ORVIX_CADDYFILE:-/etc/caddy/Caddyfile}"
ORVIX_CADDY_BIN="${ORVIX_CADDY_BIN:-caddy}"
ORVIX_SYSTEMCTL="${ORVIX_SYSTEMCTL:-systemctl}"

# Admin + webmail UI deployment targets. The upgrade path MUST
# propagate both trees, not just the binary; otherwise a fresh
# backend can ship against a stale admin SPA. See
# release/scripts/lib-asset-propagate.sh for the contract and the
# smoke tests in release/scripts/tests/test-asset-propagation.sh
# for the assertions.
ORVIX_ADMIN_UI_DIR="${ORVIX_ADMIN_UI_DIR:-/usr/share/orvix/admin}"
ORVIX_WEBMAIL_UI_DIR="${ORVIX_WEBMAIL_UI_DIR:-/usr/share/orvix/webmail}"
ORVIX_MARKETING_UI_DIR="${ORVIX_MARKETING_UI_DIR:-/usr/share/orvix/marketing}"
ORVIX_RELEASE_ADMIN_SRC="${ORVIX_RELEASE_ADMIN_SRC:-$ORVIX_SOURCE_DIR/release/admin}"
ORVIX_RELEASE_WEBMAIL_SRC="${ORVIX_RELEASE_WEBMAIL_SRC:-$ORVIX_SOURCE_DIR/release/webmail}"
ORVIX_RELEASE_MARKETING_SRC="${ORVIX_RELEASE_MARKETING_SRC:-$ORVIX_SOURCE_DIR/release/marketing}"

log() {
    printf '[%s] %s\n' "$(date -Is)" "$*" >&2
}

fail() {
    printf '%bERROR:%b %s\n' "$RED" "$NC" "$*" >&2
    exit 1
}

# Source the asset-propagation library. BLOCKER 3 (fail-closed):
# the lib is REQUIRED G�� a backend upgrade MUST ship the matching
# admin + webmail static assets. If the lib is missing from the
# release tree we abort before any state is mutated, so the
# operator never sees a green upgrade report on a half-propagated
# host.
LIB_ASSET_PROPAGATE=""
for candidate in \
    "$ORVIX_SOURCE_DIR/release/scripts/lib-asset-propagate.sh" \
    "/usr/share/orvix/scripts/lib-asset-propagate.sh"; do
    if [ -f "$candidate" ]; then
        LIB_ASSET_PROPAGATE="$candidate"
        break
    fi
done
if [ -z "$LIB_ASSET_PROPAGATE" ]; then
    log "ERROR: lib-asset-propagate.sh not found in release tree (BLOCKER 3 fail-closed); refusing to upgrade."
    fail "asset propagation library missing; refusing to upgrade"
fi
if ! bash -n "$LIB_ASSET_PROPAGATE" 2>/dev/null; then
    log "ERROR: lib-asset-propagate.sh at $LIB_ASSET_PROPAGATE has a bash syntax error; refusing to upgrade."
    fail "asset propagation library has a syntax error; refusing to upgrade"
fi
# shellcheck disable=SC1090
. "$LIB_ASSET_PROPAGATE"
if ! command -v asset_propagate >/dev/null 2>&1; then
    log "ERROR: lib-asset-propagate.sh did not define asset_propagate(); refusing to upgrade."
    fail "asset propagation library is malformed; refusing to upgrade"
fi

DRY_RUN=0
FROM_URL=""
CHECKSUM_FILE=""
ALLOW_UNSIGNED_LOCAL=0
DEV_UNSAFE=0

REPORT_LINES=()

usage() {
    cat <<USAGE
Usage:
  bash upgrade.sh [--dry-run] [binary-path]
  bash upgrade.sh --from-url <download-url> --checksum <sha256-hex>
                   [--checksum-file <sha256-file>] [--dry-run]
  bash upgrade.sh --from-url <download-url> --checksum-file <sha256-file>
                   [--dry-run]
  bash upgrade.sh --dev-unsafe [binary-path]

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

  --dev-unsafe                      DANGEROUS: skip checksum verification AND the
                                    full health gate. Only for local, unsigned
                                    artifacts. Refused for --from-url upgrades.
                                    Prints a loud red banner before proceeding.

  --allow-unsigned-local-artifact   DEPRECATED: same as --dev-unsafe for local
                                    artifacts. Kept for backward compatibility.

Production-readiness gate BLOCKER 5: checksum verification is
FAIL-CLOSED. The script refuses to install a binary whose SHA256
does not match the operator-supplied expected hash, AND refuses to
install any binary (URL or local) whose expected hash is missing
unless --dev-unsafe is set.
USAGE
}

log() {
    printf '[%s] %s\n' "$(date -Is)" "$*" >&2
}

fail() {
    printf '%bERROR:%b %s\n' "$RED" "$NC" "$*" >&2
    exit 1
}

report() {
    local color="${1:-}"
    local msg="$2"
    REPORT_LINES+=("[$color] $msg")
    case "$color" in
        green)  printf '%bOK%b    %s\n' "$GREEN" "$NC" "$msg" ;;
        red)    printf '%bFAIL%b  %s\n' "$RED" "$NC" "$msg" ;;
        yellow) printf '%bWARN%b  %s\n' "$YELLOW" "$NC" "$msg" ;;
        *)      printf '       %s\n' "$msg" ;;
    esac
}
# Source the admin route migration library.
LIB_ADMIN_ROUTE_MIGRATION=""
for candidate in \
    "$ORVIX_SOURCE_DIR/release/scripts/lib-admin-route-migration.sh" \
    "/usr/share/orvix/scripts/lib-admin-route-migration.sh"
do
    if [ -f "$candidate" ]; then
        LIB_ADMIN_ROUTE_MIGRATION="$candidate"
        break
    fi
done

if [ -z "$LIB_ADMIN_ROUTE_MIGRATION" ]; then
    fail "lib-admin-route-migration.sh not found; refusing to run an incomplete upgrade"
fi

if ! bash -n "$LIB_ADMIN_ROUTE_MIGRATION"; then
    fail "lib-admin-route-migration.sh contains invalid Bash syntax"
fi

# shellcheck disable=SC1090
. "$LIB_ADMIN_ROUTE_MIGRATION"

if ! declare -F run_admin_route_migration >/dev/null 2>&1; then
    fail "lib-admin-route-migration.sh did not define run_admin_route_migration"
fi


require_root() {
    [ "$(id -u)" -eq 0 ] || fail "must be run as root (or with sudo)"
}

acquire_upgrade_lock() {
    command -v flock >/dev/null 2>&1 || fail "flock is required for process-safe upgrades"
    mkdir -p "$(dirname "$ORVIX_UPGRADE_LOCK")" || fail "cannot create upgrade lock directory"
    exec 9>"$ORVIX_UPGRADE_LOCK" || fail "cannot open upgrade lock"
    flock -n 9 || fail "another Orvix upgrade is already running"
}

# sha256_of_file prints the SHA256 of $1 in lowercase hex.
sha256_of_file() {
    sha256sum "$1" | awk '{print $1}'
}

trusted_release_key_file() {
    if [ -n "$ORVIX_RELEASE_VERIFYING_KEY_FILE" ]; then
        [ -f "$ORVIX_RELEASE_VERIFYING_KEY_FILE" ] || fail "release verifying key not found: $ORVIX_RELEASE_VERIFYING_KEY_FILE"
        printf '%s\n' "$ORVIX_RELEASE_VERIFYING_KEY_FILE"
        return 0
    fi
    local tmp
    tmp="$(mktemp /tmp/orvix-release-key.XXXXXX.pem)"
    cat >"$tmp" <<'KEY'
-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAtS/Uv9QvTrbhBziXhcbdnFHAKkwb2gNYUKNVNsRcKnI=
-----END PUBLIC KEY-----
KEY
    printf '%s\n' "$tmp"
}

verify_release_signature_fail_closed() {
    local artifact="$1"
    if [ "$ORVIX_REQUIRE_RELEASE_SIGNATURE" != "1" ]; then
        printf '%bWARN%b release signature verification disabled by ORVIX_REQUIRE_RELEASE_SIGNATURE=%s\n' "$YELLOW" "$NC" "$ORVIX_REQUIRE_RELEASE_SIGNATURE" >&2
        return 0
    fi
    command -v openssl >/dev/null 2>&1 || {
        printf '%bFAIL%b openssl is required for release signature verification\n' "$RED" "$NC" >&2
        return 1
    }
    local sig_file=""
    if [ -n "$FROM_URL" ]; then
        sig_file="$(mktemp /tmp/orvix-upgrade.XXXXXX.sig)"
        if ! curl -fsSL --retry 3 --max-time 60 -o "$sig_file" "${FROM_URL}.sig"; then
            rm -f "$sig_file"
            printf '%bFAIL%b missing release signature sidecar: %s.sig\n' "$RED" "$NC" "$FROM_URL" >&2
            return 1
        fi
    elif [ -f "${artifact}.sig" ]; then
        sig_file="${artifact}.sig"
    elif [ "$ALLOW_UNSIGNED_LOCAL" = "1" ] || [ "$DEV_UNSAFE" = "1" ]; then
        printf '%bWARN%b local artifact signature missing; allowed only because unsafe dev mode is enabled\n' "$YELLOW" "$NC" >&2
        return 0
    else
        printf '%bFAIL%b missing local release signature: %s.sig\n' "$RED" "$NC" "$artifact" >&2
        return 1
    fi
    local key_file
    key_file="$(trusted_release_key_file)"
    if ! openssl pkeyutl -verify -rawin -pubin -inkey "$key_file" -in "$artifact" -sigfile "$sig_file" >/dev/null 2>&1; then
        [ -n "$FROM_URL" ] && rm -f "$sig_file"
        [ -z "$ORVIX_RELEASE_VERIFYING_KEY_FILE" ] && rm -f "$key_file"
        printf '%bFAIL%b release signature verification failed for %s\n' "$RED" "$NC" "$artifact" >&2
        return 1
    fi
    [ -n "$FROM_URL" ] && rm -f "$sig_file"
    [ -z "$ORVIX_RELEASE_VERIFYING_KEY_FILE" ] && rm -f "$key_file"
    printf '%bOK%b release signature verified for %s\n' "$GREEN" "$NC" "$artifact" >&2
    return 0
}

# resolve_expected_sha returns the expected SHA256 hex string for
# the given file path, sourced from --checksum or --checksum-file.
# Echoes the empty string if no expected value is available.
resolve_expected_sha() {
    local file="$1"
    if [ -n "${EXPECTED_SHA:-}" ]; then
        printf '%s' "$EXPECTED_SHA"
        return 0
    fi
    if [ -n "${CHECKSUM_FILE:-}" ] && [ -f "$CHECKSUM_FILE" ]; then
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
#     file AND neither --dev-unsafe nor --allow-unsigned-local-artifact was set,
#   - the sha256 mismatches.
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

    if [ "$ALLOW_UNSIGNED_LOCAL" = "1" ] || [ "$DEV_UNSAFE" = "1" ]; then
        printf '%bWARN%b installing UNVERIFIED local artifact %s (sha256 %s)\n' "$YELLOW" "$NC" "$file" "$actual" >&2
        printf '  --dev-unsafe is DANGEROUS. Do not use on production.\n' >&2
        return 0
    fi

    printf '%bFAIL%b missing checksum for %s\n' "$RED" "$NC" "$file" >&2
    printf '  refusing to install a binary without integrity verification.\n' >&2
    printf '  Pass --checksum <sha256-hex> or --checksum-file <sha256-file>,\n' >&2
    printf '  or --dev-unsafe for air-gapped dev only.\n' >&2
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

# run_doctor calls the orvix-doctor.sh script if it exists
# and returns 0 if the doctor reports healthy, 1 otherwise.
# If the doctor script is not available, falls back to the
# simple health endpoint probe.
run_doctor() {
    local doctor_script
    doctor_script="$ORVIX_DOCTOR_SCRIPT"
    if [ ! -f "$doctor_script" ]; then
        doctor_script="$ORVIX_SOURCE_DIR/release/scripts/orvix-doctor.sh"
    fi
    if [ -x "$doctor_script" ]; then
        log "running orvix-doctor.sh"
        if bash "$doctor_script" --quiet 2>/dev/null; then
            return 0
        fi
        log "doctor reported unhealthy; checking individual checks..."
        bash "$doctor_script" --json 2>/dev/null || true
        return 1
    fi
    # Fall back to the simple health endpoint.
    if verify_health; then
        return 0
    fi
    return 1
}

# preflight_backup copies every file the upgrade path needs to
# be able to roll back from — binary, config, db, jwt, vapid keys,
# dkim keys, license, bootstrap env. Each regular file's metadata
# (UID, GID, numeric mode, SHA256) is recorded in a structured
# manifest so rollback can restore exact ownership and verify integrity.
preflight_backup() {
    local backup_dir="$BACKUP_PARENT/$(date -u +%Y%m%dT%H%M%SZ)"
    mkdir -p "$backup_dir"
    log "backup directory: $backup_dir"
    report "green" "backup directory: $backup_dir"

    local run_id
    run_id="$(date -u +%Y%m%dT%H%M%SZ)"
    local manifest="$backup_dir/manifest"
    : > "$manifest"

    record_file() {
        local src="$1" dest="$2"
        if [ -f "$src" ]; then
            local sha uid gid mode
            sha="$(sha256_of_file "$src" 2>/dev/null || echo unknown)"
            uid="$(stat -c '%u' "$src" 2>/dev/null || echo 0)"
            gid="$(stat -c '%g' "$src" 2>/dev/null || echo 0)"
            mode="$(stat -c '%a' "$src" 2>/dev/null || echo 644)"
            printf 'path=%s\nuid=%s\ngid=%s\nmode=%s\nsha256=%s\nbackup_path=%s\n---\n' \
                "$src" "$uid" "$gid" "$mode" "$sha" "$dest" >> "$manifest"
        else
            printf 'path=%s\nexisted=no\n---\n' "$src" >> "$manifest"
        fi
    }

    local file
    for file in \
        "$ORVIX_BIN" \
        "$ORVIX_CONFIG" \
        "$ORVIX_DATA_DIR/orvix.db" \
        "$ORVIX_JWT_KEY" \
        "$ORVIX_VAPID_PRIVATE_KEY" \
        "$ORVIX_VAPID_PUBLIC_KEY" \
        "$ORVIX_BACKUP_ENCRYPTION_KEY" \
        /etc/orvix/license.json \
        /etc/orvix/bootstrap.env \
        "$ORVIX_CADDYFILE" \
        "$ORVIX_DATA_DIR/license-cache.json"
    do
        if [ -e "$file" ]; then
            local dest="$backup_dir$(dirname "$file")"
            mkdir -p "$dest"
            cp -a "$file" "$dest/" || fail "could not back up $file"
            if [ -f "$file" ]; then
                record_file "$file" "$dest/$(basename "$file")"
                log "  backed up $file (sha256 $(sha256_of_file "$file" 2>/dev/null || echo unknown))"
            else
                log "  backed up $file"
            fi
        else
            log "  skip $file (not present)"
            printf 'path=%s\nexisted=no\n---\n' "$file" >> "$manifest"
        fi
    done

    # Backup DKIM keys.
    if [ -d "$ORVIX_DKIM_DIR" ]; then
        local dkim_dest="$backup_dir/$ORVIX_DKIM_DIR"
        mkdir -p "$dkim_dest"
        cp -a "$ORVIX_DKIM_DIR/." "$dkim_dest/" 2>/dev/null || true
        local dkim_count
        dkim_count="$(find "$ORVIX_DKIM_DIR" -type f 2>/dev/null | wc -l)"
        log "  backed up dkim keys: $dkim_count files from $ORVIX_DKIM_DIR"
    fi

    if [ -d "$ORVIX_DATA_DIR/coremail" ]; then
        local count size
        count=$(find "$ORVIX_DATA_DIR/coremail" -type f 2>/dev/null | wc -l)
        size=$(du -sh "$ORVIX_DATA_DIR/coremail" 2>/dev/null | cut -f1 || echo unknown)
        log "  mailstore: $count files, $size (NOT snapshotted; run runtime backup before upgrade)"
        printf 'mailstore_files=%s\nmailstore_size=%s\n' "$count" "$size" >"$backup_dir/mailstore.summary"
    fi

    printf 'run_id=%s\nbackup_dir=%s\ncreated_at=%s\n' "$run_id" "$backup_dir" "$(date -Is)" >"$BACKUP_PARENT/.latest"
    log "wrote $BACKUP_PARENT/.latest"
    echo "$run_id" >"$backup_dir/run_id"
    BACKUP_DIR="$backup_dir"
    ORVIX_UPGRADE_RUN_ID="$run_id"
    report "green" "pre-upgrade backup complete ($(find "$backup_dir" -type f 2>/dev/null | wc -l) files, run_id=$run_id)"
}

# full_rollback restores the binary, config, db, dkim keys, jwt key,
# and vapid keys from the backup directory. This is called when
# the service fails to restart or health verification fails after
# the new binary is installed.
#
# The function reads the pre-upgrade manifest to restore exact
# UID/GID/mode for every regular file, then verifies SHA256.
# Caddy validation and reload failures are fail-closed.
# Asset backup paths are tracked per-run, not guessed via ls|sort.
full_rollback() {
    local backup_dir="${1:-$BACKUP_DIR}"
    if [ -z "$backup_dir" ] || [ ! -d "$backup_dir" ]; then
        fail "cannot roll back: no backup directory available"
    fi
    log "ROLLBACK: restoring from $backup_dir"

    local manifest="$backup_dir/manifest"
    local rollback_failed=0
    local rolled=0

    restore_file_from_manifest() {
        local path="$1" uid="$2" gid="$3" mode="$4" sha="$5" backup="$6"
        local full_backup="$backup"
        if [ ! -f "$full_backup" ]; then
            log "  MISSING: backup file $full_backup not found"
            rollback_failed=1
            return
        fi
        cp -a "$full_backup" "$path"
        chown "$uid:$gid" "$path" 2>/dev/null || true
        chmod "$mode" "$path" 2>/dev/null || true
        local actual_sha
        actual_sha="$(sha256_of_file "$path" 2>/dev/null || echo '')"
        if [ -n "$actual_sha" ] && [ "$sha" != "unknown" ] && [ "$actual_sha" != "$sha" ]; then
            log "  SHA256 MISMATCH: $path expected=$sha actual=$actual_sha"
            rollback_failed=1
            return
        fi
        local actual_uid actual_gid actual_mode
        actual_uid="$(stat -c '%u' "$path" 2>/dev/null || echo '')"
        actual_gid="$(stat -c '%g' "$path" 2>/dev/null || echo '')"
        actual_mode="$(stat -c '%a' "$path" 2>/dev/null || echo '')"
        if [ -n "$actual_uid" ] && [ "$actual_uid" != "$uid" ]; then
            log "  UID MISMATCH: $path expected=$uid actual=$actual_uid"
            rollback_failed=1
            return
        fi
        if [ -n "$actual_gid" ] && [ "$actual_gid" != "$gid" ]; then
            log "  GID MISMATCH: $path expected=$gid actual=$actual_gid"
            rollback_failed=1
            return
        fi
        if [ -n "$actual_mode" ] && [ "$actual_mode" != "$mode" ]; then
            log "  MODE MISMATCH: $path expected=$mode actual=$actual_mode"
            rollback_failed=1
            return
        fi
        log "  rolled back $path (uid=$uid gid=$gid mode=$mode sha256=$sha)"
        rolled=$((rolled + 1))
    }

    if [ -f "$manifest" ]; then
        local current_path current_uid current_gid current_mode current_sha current_backup
        while IFS='=' read -r key value; do
            case "$key" in
                path)         current_path="$value" ;;
                uid)          current_uid="$value" ;;
                gid)          current_gid="$value" ;;
                mode)         current_mode="$value" ;;
                sha256)       current_sha="$value" ;;
                backup_path)  current_backup="$value" ;;
                ---)
                    if [ -n "$current_path" ] && [ -f "$current_backup" ]; then
                        restore_file_from_manifest "$current_path" "$current_uid" "$current_gid" "$current_mode" "$current_sha" "$current_backup"
                    elif [ -n "$current_path" ]; then
                        local existed_marker
                        existed_marker="$(grep -A1 "^path=$current_path$" "$manifest" 2>/dev/null | grep existed || true)"
                        if echo "$existed_marker" | grep -q 'existed=no'; then
                            log "  skip $current_path (not present in pre-upgrade snapshot)"
                        fi
                    fi
                    current_path=""
                    ;;
            esac
        done < "$manifest"
    else
        log "  WARNING: no manifest found; falling back to simple restore"
        local item dest
        for item in "$ORVIX_BIN" "$ORVIX_CONFIG"; do
            dest="$backup_dir$item"
            if [ -f "$dest" ]; then
                cp -a "$dest" "$item"
                log "  rolled back $item"
                rolled=$((rolled + 1))
            fi
        done
    fi

    # Caddyfile: validate and reload, fail-closed
    if [ -f "$backup_dir$ORVIX_CADDYFILE" ]; then
        if command -v "$ORVIX_CADDY_BIN" >/dev/null 2>&1; then
            if "$ORVIX_CADDY_BIN" validate --config "$ORVIX_CADDYFILE" 2>/dev/null; then
                log "  validated restored Caddyfile"
                if "$ORVIX_CADDY_BIN" reload --config "$ORVIX_CADDYFILE" 2>/dev/null; then
                    log "  reloaded Caddy with restored Caddyfile"
                else
                    log "  FAIL: Caddy reload failed after rollback"
                    rollback_failed=1
                fi
            else
                log "  FAIL: restored Caddyfile failed validation"
                rollback_failed=1
            fi
        else
            log "  FAIL: $ORVIX_CADDY_BIN not available; cannot validate/reload Caddy after rollback"
            rollback_failed=1
        fi
    fi

    # Restore DKIM keys.
    if [ -d "$backup_dir/$ORVIX_DKIM_DIR" ]; then
        mkdir -p "$ORVIX_DKIM_DIR"
        cp -a "$backup_dir/$ORVIX_DKIM_DIR/." "$ORVIX_DKIM_DIR/" 2>/dev/null || true
        log "  rolled back $ORVIX_DKIM_DIR"
        rolled=$((rolled + 1))
    fi

    # Roll back assets using exact per-run backup paths from manifest.
    local assets_manifest="$backup_dir/manifest.assets"
    if [ -f "$assets_manifest" ]; then
        while IFS='=' read -r key path_val; do
            case "$key" in
                asset_admin_backup)     dest_label="admin"     dest_dir="$ORVIX_ADMIN_UI_DIR" ;;
                asset_webmail_backup)   dest_label="webmail"   dest_dir="$ORVIX_WEBMAIL_UI_DIR" ;;
                asset_marketing_backup) dest_label="marketing" dest_dir="$ORVIX_MARKETING_UI_DIR" ;;
                *)                      dest_label="" dest_dir="" ;;
            esac
            if [ -n "$dest_label" ] && [ -d "$path_val" ] && [ -n "$dest_dir" ]; then
                local tmp_tree
                tmp_tree="$(mktemp -d "$(dirname "$dest_dir")/.rollback-${dest_label}-XXXXXX")"
                if cp -a "$path_val/." "$tmp_tree/" 2>/dev/null; then
                    local old_tree
                    old_tree="$(mktemp -d "$(dirname "$dest_dir")/.old-${dest_label}-XXXXXX")"
                    if [ -d "$dest_dir" ]; then
                        mv "$dest_dir" "$old_tree/" 2>/dev/null || true
                    fi
                    if mv "$tmp_tree" "$dest_dir" 2>/dev/null; then
                        log "  rolled back $dest_dir from $path_val"
                        rolled=$((rolled + 1))
                        rm -rf "$old_tree" 2>/dev/null || true
                    else
                        log "  FAIL: could not atomically replace $dest_dir"
                        if [ -d "$old_tree/$(basename "$dest_dir")" ]; then
                            mv "$old_tree/$(basename "$dest_dir")" "$dest_dir" 2>/dev/null || true
                        fi
                        rm -rf "$tmp_tree" "$old_tree" 2>/dev/null || true
                        rollback_failed=1
                    fi
                else
                    log "  FAIL: could not prepare restored $dest_label tree from $path_val"
                    rm -rf "$tmp_tree" 2>/dev/null || true
                    rollback_failed=1
                fi
            elif [ -n "$dest_label" ] && [ -n "$dest_dir" ]; then
                log "  no asset backup for $dest_label; skipping"
            fi
        done < "$assets_manifest"
    else
        log "  no asset backup manifest; skipping asset rollback"
    fi

    log "rollback restored $rolled item(s) from $backup_dir"

    if [ "$rollback_failed" = "1" ]; then
        report "red" "rollback FAILED: one or more restoration/validation steps failed; check logs"
        return 1
    fi

    if "$ORVIX_SYSTEMCTL" restart orvix.service 2>/dev/null; then
        log "  restarted orvix.service"
    else
        log "  FAIL: orvix.service restart failed after rollback"
        rollback_failed=1
        report "red" "rollback FAILED: service restart failed"
        return 1
    fi

    sleep 2
    if "$ORVIX_SYSTEMCTL" is-active --quiet orvix 2>/dev/null; then
        report "green" "rollback complete; service restarted with previous binary"
        return 0
    else
        report "red" "rollback complete but service is still not active; check journalctl"
        return 1
    fi
}

# propagate_assets copies admin + webmail static assets from the
# release tree into their installed paths. The function is a thin
# wrapper around asset_propagate from lib-asset-propagate.sh; the
# indirection exists so the smoke test can introspect the
# propagation step and so a future refactor (e.g. parallel copy) is
# local to one place.
#
# Fail-closed contract (BLOCKER 3): a missing propagation library or
# a propagation failure is a HARD upgrade failure. The previous
# "warn-but-continue" behaviour left the operator with a half-up
# service (new binary, stale admin SPA) and a green-ish upgrade
# report. We now refuse to call the new service healthy until BOTH
# asset trees have been propagated successfully; if propagation
# fails after the pre-copy backup has been taken, the asset lib

propagate_assets() {
	if [ -z "$LIB_ASSET_PROPAGATE" ] || ! command -v asset_propagate >/dev/null 2>&1; then
		log "ERROR: lib-asset-propagate.sh not sourced; refusing to upgrade with stale admin/webmail assets."
		report "red" "asset propagation library missing; refusing to upgrade (BLOCKER 3 fail-closed)"
		return 1
	fi
	local ok=1
	local run_id="${ORVIX_UPGRADE_RUN_ID:-}"
	local asset_backup_parent="$BACKUP_PARENT/assets"
	if [ -n "$run_id" ]; then
		asset_backup_parent="$BACKUP_PARENT/assets/$run_id"
	fi
	if [ -d "$ORVIX_RELEASE_ADMIN_SRC" ]; then
		log "propagating admin assets: $ORVIX_RELEASE_ADMIN_SRC -> $ORVIX_ADMIN_UI_DIR"
		if ! ASSET_BACKUP_PARENT="$asset_backup_parent" \
			ASSET_VERBOSE=1 \
			asset_propagate "$ORVIX_RELEASE_ADMIN_SRC" "$ORVIX_ADMIN_UI_DIR" admin; then
			log "ERROR: admin asset propagation failed; rolled back from backup"
			report "red" "admin asset propagation failed (rolled back from backup)"
			ok=0
		fi
	else
		log "ERROR: admin asset source $ORVIX_RELEASE_ADMIN_SRC not present; refusing to upgrade (BLOCKER 3 fail-closed)"
		report "red" "admin asset source missing; refusing to upgrade"
		return 1
	fi
	if [ -d "$ORVIX_RELEASE_WEBMAIL_SRC" ]; then
		log "propagating webmail assets: $ORVIX_RELEASE_WEBMAIL_SRC -> $ORVIX_WEBMAIL_UI_DIR"
		if ! ASSET_BACKUP_PARENT="$asset_backup_parent" \
			ASSET_VERBOSE=1 \
			asset_propagate "$ORVIX_RELEASE_WEBMAIL_SRC" "$ORVIX_WEBMAIL_UI_DIR" webmail; then
			log "ERROR: webmail asset propagation failed; rolled back from backup"
			report "red" "webmail asset propagation failed (rolled back from backup)"
			ok=0
		fi
	else
		log "ERROR: webmail asset source $ORVIX_RELEASE_WEBMAIL_SRC not present; refusing to upgrade (BLOCKER 3 fail-closed)"
		report "red" "webmail asset source missing; refusing to upgrade"
		return 1
	fi

	if [ -d "$ORVIX_RELEASE_MARKETING_SRC" ]; then
		log "propagating marketing assets: $ORVIX_RELEASE_MARKETING_SRC -> $ORVIX_MARKETING_UI_DIR"
		if ! ASSET_BACKUP_PARENT="$asset_backup_parent" \
			ASSET_VERBOSE=1 \
			asset_propagate "$ORVIX_RELEASE_MARKETING_SRC" "$ORVIX_MARKETING_UI_DIR" marketing; then
			log "ERROR: marketing asset propagation failed; rolled back from backup"
			report "red" "marketing asset propagation failed (rolled back from backup)"
			ok=0
		fi
	else
		log "ERROR: marketing release assets missing: $ORVIX_RELEASE_MARKETING_SRC"
		report "red" "marketing release assets missing; refusing to upgrade with a stale public site"
		ok=0
	fi

	if [ -n "$run_id" ] && [ -n "$BACKUP_DIR" ]; then
		for label in admin webmail marketing; do
			local found
			found="$(ls -1d "$asset_backup_parent"/*-"$label" 2>/dev/null | sort | tail -n 1 || true)"
			if [ -n "$found" ]; then
				printf 'asset_%s_backup=%s\n' "$label" "$found" >> "$BACKUP_DIR/manifest.assets"
			fi
		done
	fi

	if [ "$ok" = "1" ]; then
		report "green" "admin + webmail + marketing assets propagated (backups under $asset_backup_parent)"
		return 0
	fi
	return 1
}

resolve_input() {    if [ -n "$FROM_URL" ]; then
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
    report "" "--- Verification ---"

    log "verifying checksum (fail-closed)"
    if ! verify_checksum_fail_closed "$NEW_BIN"; then
        if [ "$DRY_RUN" = "1" ]; then
            log "DRY-RUN: checksum verification would fail; the upgrade would be aborted here"
        fi
        report "red" "checksum verification failed (fail-closed)"
        fail "checksum verification failed (fail-closed)"
    fi
    report "green" "checksum verification passed"

    log "verifying release signature (fail-closed)"
    if ! verify_release_signature_fail_closed "$NEW_BIN"; then
        report "red" "release signature verification failed (fail-closed)"
        fail "release signature verification failed (fail-closed)"
    fi
    report "green" "release signature verification passed"

    if [ "$DRY_RUN" = "1" ]; then
        report "" "--- Dry Run Summary ---"
        report "yellow" "DRY-RUN: would replace $ORVIX_BIN with $NEW_BIN"
        report "yellow" "DRY-RUN: would restart orvix.service and probe $HEALTH_URL"
        return 0
    fi

    report "" "--- Installation ---"
	log "installing $NEW_BIN -> $ORVIX_BIN"
	install -m 0755 "$NEW_BIN" "$ORVIX_BIN"
	if [ -n "${FROM_URL:-}" ]; then
		rm -f "$NEW_BIN"
	fi
	report "green" "new binary installed at $ORVIX_BIN"

	report "" "--- Asset Propagation ---"
	# Propagate admin + webmail static assets so a backend upgrade
	# ships the matching UI. Without this, an operator who upgrades
	# to a backend with a new admin endpoint would see a stale admin
	# SPA. The lib-asset-propagate.sh library handles per-file
	# backup, hash verification, ownership, perms, and rollback on
	# failure. A failure here is HARD: the new binary would ship
	# against a half-propagated UI and we refuse to start it.
	if ! propagate_assets; then
		report "red" "asset propagation failed (BLOCKER 3 fail-closed); rolling back binary to previous state"
		full_rollback "$BACKUP_DIR"
		fail "asset propagation failed; rolled back to previous binary"
	fi

	report "" "--- Caddy Admin Route Migration ---"
	if ! run_admin_route_migration; then
		report "red" "admin route migration failed"
		full_rollback "$BACKUP_DIR"
		fail "admin route migration failed; rolled back"
	fi

    report "" "--- Restart ---"
    log "restarting orvix.service"
    if ! "$ORVIX_SYSTEMCTL" restart orvix.service; then
        report "red" "orvix.service restart failed"
        full_rollback "$BACKUP_DIR"
        fail "service restart failed; rolled back to previous state"
    fi
    report "green" "orvix.service restarted"

    report "" "--- Health Verification ---"
    if [ "$DEV_UNSAFE" = "1" ]; then
        report "yellow" "DEV_UNSAFE: skipping health verification"
    elif verify_health; then
        report "green" "health endpoint reached (HTTP 200)"
    else
        report "red" "health probe failed after $HEALTH_MAX_ATTEMPTS attempts"
        full_rollback "$BACKUP_DIR"
        fail "service unhealthy after upgrade; rolled back to previous state"
    fi

    # Additional doctor check (non-fatal but reported).
    if [ "$DEV_UNSAFE" != "1" ]; then
        report "" "--- Doctor Check ---"
        if run_doctor; then
            report "green" "orvix-doctor reports healthy"
        else
            report "red" "orvix-doctor reports unhealthy; check doctor output above"
            full_rollback "$BACKUP_DIR"
            fail "doctor reported unhealthy state after upgrade; rolled back"
        fi
    fi

    report "" "--- Upgrade Complete ---"
    report "green" "upgrade succeeded; backup preserved at $BACKUP_DIR"
}

# generate_upgrade_report prints a structured summary of the
# upgrade operation. Called at the end of main() regardless of
# outcome (report is printed only on success; on failure the
# fail() function already printed the error).
generate_upgrade_report() {
    cat <<REPORT

${BOLD}========================================================
              ORVIX UPGRADE REPORT
========================================================${NC}

Timestamp:    $(date -Is)
Binary:       $ORVIX_BIN
Config:       $ORVIX_CONFIG
Backup dir:   ${BACKUP_DIR:-<none>}
Dry run:      $DRY_RUN
Dev unsafe:   $DEV_UNSAFE

${BOLD}--- Step Results ---${NC}
REPORT
    local line
    for line in "${REPORT_LINES[@]}"; do
        printf '%s\n' "$line"
    done

    cat <<REPORT

${BOLD}--- Rollback ---${NC}
To roll back this upgrade manually:
  sudo bash ${BASH_SOURCE[0]:-upgrade.sh} --rollback-from $BACKUP_DIR

Or restore the backed-up files by hand:
  cp -a $BACKUP_DIR/$ORVIX_BIN $ORVIX_BIN
  systemctl restart orvix.service

${BOLD}========================================================${NC}
REPORT
}

main() {
    # Parse args FIRST so --help works without root.
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
    acquire_upgrade_lock
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
            --dev-unsafe)
                DEV_UNSAFE=1
                ALLOW_UNSIGNED_LOCAL=1
                shift
                ;;
            --allow-unsigned-local-artifact)
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

    if [ "$DEV_UNSAFE" = "1" ]; then
        cat >&2 <<DEVUNSAFE
${RED}${BOLD}============================================================
  WARNING: --dev-unsafe MODE
============================================================
  Checksum verification: SKIPPED
  Health verification:   SKIPPED
  This is for air-gapped dev workstations ONLY.
  NEVER use this on a production host.
============================================================${NC}

DEVUNSAFE
        printf '%bContinue with --dev-unsafe? (yes/NO): %b' "$YELLOW" "$NC" >&2
        local confirm
        IFS= read -r confirm
        if [ "$confirm" != "yes" ]; then
            echo "aborted." >&2
            exit 1
        fi
        report "yellow" "proceeding in --dev-unsafe mode"
    fi

    # Production-readiness gate BLOCKER 5 enforcement:
    # --dev-unsafe is refused for URL upgrades.
    if [ -n "$FROM_URL" ] && [ "$DEV_UNSAFE" = "1" ]; then
        fail "--dev-unsafe is refused for --from-url upgrades (URL downloads are always fail-closed)"
    fi
    if [ -n "$FROM_URL" ] && [ "$ALLOW_UNSIGNED_LOCAL" = "1" ] && [ "$DEV_UNSAFE" != "1" ]; then
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
    log "dev-unsafe: $DEV_UNSAFE"
    if [ "$ALLOW_UNSIGNED_LOCAL" = "1" ] || [ "$DEV_UNSAFE" = "1" ]; then
        log "WARNING: unsigned/unsafe mode set; checksum verification will be SKIPPED for the local artifact. Production-readiness gate BLOCKER 5 disables this for downloaded artifacts."
    fi

    preflight_backup
    resolve_input "${POSITIONAL[0]:-}"
    install_and_restart
    generate_upgrade_report
}

[ "${BASH_SOURCE[0]}" != "${0}" ] && return 0
main "$@"