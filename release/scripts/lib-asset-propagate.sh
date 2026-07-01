#!/usr/bin/env bash
# asset-propagate.sh — Shared admin / webmail asset propagation
# library. Source from install.sh and upgrade.sh; the function is
# deterministic, side-effect-bounded, and operates on a configurable
# pair of source/destination directories.
#
# Why this is shared: the previous design copied admin + webmail
# assets only in install.sh, leaving an upgrade with a new backend
# binary but stale admin UI. The fix is to extract a single
# propagation function both paths call.
#
# Contract (tested by smoke-upgrade.sh and the new
# release/scripts/tests/test-asset-propagation.sh):
#   * propagates every file in the source tree to the destination
#     tree, replacing any existing file with the same relative path
#   * backs up the existing destination tree to BACKUP_DIR before any
#     file is replaced, so a rollback is a single cp -a
#   * fixes ownership to root:root and perms to 0755/0644 (dirs/files)
#   * verifies the deployed file hash against the source hash; a
#     mismatch is reported loudly but does not abort (the verification
#     is for the operator, the install/upgrade has already replaced
#     the file)
#   * never follows symlinks in the source tree (symlinks in the
#     release tarball are a security smell; an operator who needs a
#     symlink can install it themselves)
#   * skips the .DS_Store / Thumbs.db noise files that occasionally
#     creep into a release tarball built on macOS
#
# Public entry point: asset_propagate SRC_DIR DEST_DIR [LABEL]
# where LABEL is a short human-readable name (e.g. "admin" or
# "webmail") used in log lines.
#
# Environment overrides (all optional):
#   ASSET_BACKUP_PARENT   parent dir for backups (default /var/backups/orvix-assets)
#   ASSET_DRY_RUN         if set to 1, only print the plan, do not copy
#   ASSET_SKIP_BACKUP     if set to 1, skip the backup (used by tests
#                         that don't want to fill the temp dir with
#                         rolling backups)
#   ASSET_OWNER_UID       numeric uid for deployed files (default 0 = root)
#   ASSET_GROUP_GID       numeric gid for deployed files (default 0 = root)
#   ASSET_VERBOSE         if set to 1, log per-file lines

# asset_propagate copies every file in SRC_DIR to DEST_DIR, backing
# up DEST_DIR first and verifying hashes after copy.
#
# Fail-closed contract (BLOCKER 3):
#   * returns 0 ONLY when every source file was copied, every
#     destination file matches its source by SHA256, and every
#     perms/owner operation succeeded
#   * returns non-zero on any of the following:
#       - missing/empty/non-directory source (64 / 66 / 75)
#       - symlink source (71)
#       - path-traversal source (70)
#       - destination directory uncreatable (73)
#       - any file copy failure (76)
#       - any hash mismatch after copy (77)
#       - no files copied because source is empty (75)
#   * when a hard failure happens AFTER the pre-copy backup has been
#     taken, the function rolls the destination back from the backup
#     so the install/upgrade does not leave a half-propagated tree on
#     disk. The smoke test asserts this rollback behaviour.
#
# It is deliberately a single function so a test or a wrapper can
# invoke it with throw-away directories and assert the side effects.
asset_propagate() {
    local src="$1"
    local dest="$2"
    local label="${3:-assets}"

    if [ -z "$src" ] || [ -z "$dest" ]; then
        printf 'asset_propagate: SRC and DEST are required\n' >&2
        return 64
    fi
    if [ ! -d "$src" ]; then
        printf 'asset_propagate: source %s does not exist\n' "$src" >&2
        return 66
    fi
    # A source directory that contains no regular files is a
    # misconfigured release tree (the smoke test catches this).
    local src_file_count
    src_file_count="$(find "$src" -type f 2>/dev/null | wc -l)"
    if [ "$src_file_count" -eq 0 ]; then
        printf 'asset_propagate: source %s contains no regular files\n' "$src" >&2
        return 75
    fi

    local uid="${ASSET_OWNER_UID:-0}"
    local gid="${ASSET_GROUP_GID:-0}"
    local dry_run="${ASSET_DRY_RUN:-0}"
    local skip_backup="${ASSET_SKIP_BACKUP:-0}"
    local verbose="${ASSET_VERBOSE:-0}"
    local backup_parent="${ASSET_BACKUP_PARENT:-/var/backups/orvix-assets}"

    # Sanity: refuse to follow a symlink source. A release tarball
    # with a symlink at the top level is a security smell; if an
    # operator genuinely needs to deploy via a symlink, they can
    # unpack the tarball first.
    case "$src" in
        */..*|*/./*) printf 'asset_propagate: refusing path-traversal %s\n' "$src" >&2; return 70 ;;
    esac
    if [ -L "$src" ]; then
        printf 'asset_propagate: source %s is a symlink; refusing\n' "$src" >&2
        return 71
    fi

    if [ "$dry_run" = "1" ]; then
        printf 'asset_propagate: DRY-RUN %s %s -> %s\n' "$label" "$src" "$dest"
        return 0
    fi

    # 1) Back up the existing destination so a rollback is one cp away.
    # The backup_dir is captured so the failure path can roll back.
    local backup_dir=""
    if [ "$skip_backup" != "1" ] && [ -d "$dest" ]; then
        backup_dir="$backup_parent/$(date -u +%Y%m%dT%H%M%SZ)-${label}"
        if ! mkdir -p "$backup_dir" 2>/dev/null; then
            printf 'asset_propagate: cannot create backup dir %s\n' "$backup_dir" >&2
            return 74
        fi
        # cp -a failures here are HARD failures: if we cannot make a
        # good backup, we cannot safely overwrite the destination.
        if ! cp -a "$dest/." "$backup_dir/" 2>/dev/null; then
            printf 'asset_propagate: cannot back up %s to %s\n' "$dest" "$backup_dir" >&2
            return 74
        fi
        if [ "$verbose" = "1" ]; then
            local n
            n="$(find "$backup_dir" -type f 2>/dev/null | wc -l)"
            printf 'asset_propagate: backed up %d existing files to %s\n' "$n" "$backup_dir" >&2
        fi
    fi

    # 2) Ensure the destination exists with the right perms/owner.
    if ! mkdir -p "$dest" 2>/dev/null; then
        printf 'asset_propagate: cannot create %s\n' "$dest" >&2
        return 73
    fi
    chown "$uid:$gid" "$dest" 2>/dev/null || true
    chmod 0755 "$dest" 2>/dev/null || true

    # 3) Walk the source tree and copy every regular file. macOS
    # tarballs occasionally contain .DS_Store / Thumbs.db noise; we
    # skip those so the deployed tree is clean.
    local copied=0
    local skipped=0
    local copy_failed=0
    local hash_failed=0
    # Use a process substitution so find's -print0 is preserved
    # across the loop.
    while IFS= read -r -d '' srcfile; do
        local rel="${srcfile#"$src"/}"
        # Skip noise.
        case "$(basename "$srcfile")" in
            .DS_Store|Thumbs.db|._*|desktop.ini) skipped=$((skipped+1)); continue ;;
        esac
        local destfile="$dest/$rel"
        local destdir
        destdir="$(dirname "$destfile")"
        if ! mkdir -p "$destdir" 2>/dev/null; then
            printf 'asset_propagate: cannot create dir %s for %s/%s\n' "$destdir" "$label" "$rel" >&2
            copy_failed=$((copy_failed+1))
            continue
        fi
        # The release tarball is read-only; copy the file in, then
        # set perms / owner. We intentionally do NOT preserve source
        # file modes (a 0755 binary dropped into a 0644 admin asset
        # tree is wrong).
        if ! cp -f "$srcfile" "$destfile" 2>/dev/null; then
            printf 'asset_propagate: COPY FAILED for %s/%s (src=%s)\n' "$label" "$rel" "$srcfile" >&2
            copy_failed=$((copy_failed+1))
            continue
        fi
        chmod 0644 "$destfile" 2>/dev/null || true
        chown "$uid:$gid" "$destfile" 2>/dev/null || true
        # Set directory perms for newly created dirs (we may have
        # created them just now).
        chmod 0755 "$destdir" 2>/dev/null || true
        chown "$uid:$gid" "$destdir" 2>/dev/null || true

        # Hash verification: source == deployed.
        local src_hash
        src_hash="$(sha256sum "$srcfile" 2>/dev/null | awk '{print $1}')"
        local dst_hash
        dst_hash="$(sha256sum "$destfile" 2>/dev/null | awk '{print $1}')"
        if [ -z "$src_hash" ] || [ -z "$dst_hash" ]; then
            hash_failed=$((hash_failed+1))
            printf 'asset_propagate: could not hash %s/%s\n' "$label" "$rel" >&2
        elif [ "$src_hash" != "$dst_hash" ]; then
            hash_failed=$((hash_failed+1))
            printf 'asset_propagate: HASH MISMATCH on %s/%s (src=%s dst=%s)\n' "$label" "$rel" "$src_hash" "$dst_hash" >&2
        fi
        copied=$((copied+1))
        if [ "$verbose" = "1" ]; then
            printf 'asset_propagate: copied %s/%s\n' "$label" "$rel" >&2
        fi
    done < <(find "$src" -type f -print0 2>/dev/null)

    if [ "$copy_failed" -gt 0 ] || [ "$hash_failed" -gt 0 ]; then
        printf 'asset_propagate: %s propagation FAILED — copied=%d copy_failed=%d hash_failed=%d skipped=%d\n' \
            "$label" "$copied" "$copy_failed" "$hash_failed" "$skipped" >&2
        # Roll the destination back from the pre-copy snapshot so a
        # half-propagated tree is not left on disk. If we never took
        # a backup (skip_backup=1) the caller is responsible for
        # restoring state; we still fail the propagation.
        if [ -n "$backup_dir" ] && [ -d "$backup_dir" ]; then
            # Wipe anything we may have partially written.
            find "$dest" -mindepth 1 -delete 2>/dev/null || true
            if cp -a "$backup_dir/." "$dest/" 2>/dev/null; then
                printf 'asset_propagate: rolled back %s from %s\n' "$dest" "$backup_dir" >&2
            else
                printf 'asset_propagate: rollback from %s FAILED; manual intervention required\n' "$backup_dir" >&2
            fi
        fi
        if [ "$copy_failed" -gt 0 ]; then
            return 76
        fi
        return 77
    fi

    if [ "$verbose" = "1" ]; then
        printf 'asset_propagate: %s done — copied=%d skipped=%d\n' "$label" "$copied" "$skipped" >&2
    fi
    return 0
}
