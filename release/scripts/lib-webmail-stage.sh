#!/usr/bin/env bash
# lib-webmail-stage.sh — the ONE deterministic source-to-runtime
# staging routine for the webmail SPA.
#
# web/webmail-release is the canonical, hand-edited source. release/
# must never itself be hand-edited — it is always the staged output
# of this script, committed to git the same way release/admin is
# (a committed fallback/default tree, not merely a build artifact),
# because internal/config's WebmailUIDir test default and several
# router tests read release/webmail directly off disk at a fixed
# relative path and must not depend on a build step having run first.
#
# Usage:
#   source lib-webmail-stage.sh
#   stage_webmail <src_dir> <dest_dir>
#
# stage_webmail:
#   - fails closed if src_dir is missing index.html or assets/webmail.js;
#   - removes dest_dir's contents first (a stale prior run's now-
#     deleted-from-source file must not survive staging);
#   - copies deterministically (plain recursive copy over deleted
#     dest_dir — no separate exclude list is needed since the source
#     tree itself contains only shippable files: no node_modules, no
#     test reports, no secrets — it's a static HTML/CSS/JS bundle);
#   - preserves executable bits on anything that had them.
stage_webmail() {
    local src_dir="$1"
    local dest_dir="$2"

    if [ -z "$src_dir" ] || [ -z "$dest_dir" ]; then
        echo "stage_webmail: usage: stage_webmail <src_dir> <dest_dir>" >&2
        return 1
    fi
    if [ ! -f "$src_dir/index.html" ]; then
        echo "stage_webmail: missing $src_dir/index.html — refusing to stage from an incomplete source" >&2
        return 1
    fi
    if [ ! -f "$src_dir/assets/webmail.js" ]; then
        echo "stage_webmail: missing $src_dir/assets/webmail.js — refusing to stage from an incomplete source" >&2
        return 1
    fi

    rm -rf "$dest_dir"
    mkdir -p "$dest_dir"
    (cd "$src_dir" && tar -cf - .) | (cd "$dest_dir" && tar -xf -)

    if [ ! -f "$dest_dir/index.html" ] || [ ! -f "$dest_dir/assets/webmail.js" ]; then
        echo "stage_webmail: staged output at $dest_dir is missing required files after copy" >&2
        return 1
    fi
    return 0
}
