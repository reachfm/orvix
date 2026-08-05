#!/usr/bin/env bash
#
# test-external-backup-manifests.sh
#
# Cross-manifest synchronization guard for the external-backup file set.
#
# There are FIVE places in the tree that must all agree on the same ten
# external-backup filenames. When any one of them drifts, the release-bundle
# pipeline either ships a half-complete tarball (`build-release-bundle.sh`
# lists) or the end-consumer validator rejects a complete one
# (`install-public.sh:validate_bundle_layout`) or the smoke test rejects
# itself (`smoke-install-public.sh` synthetic fixture). PR #55 shipped with
# the fifth manifest (smoke fixture) out of sync, cratering the post-merge
# Release Bundle workflow. This test blocks that class of drift.
#
# Design principle: DO NOT introduce a sixth hard-coded list. Instead, this
# test extracts the external-backup entries directly from each of the five
# maintained manifests and cross-compares them. The authoritative anchor is
# `release/systemd/` and `release/scripts/` on disk — the ten files that
# actually exist define the required set; every manifest must reference the
# same set with no missing and no stale entries.

set -euo pipefail

# ── Resolve REPO_ROOT relative to this script, without cd'ing away. ─────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

FAIL_COUNT=0
PASS_COUNT=0

pass() { PASS_COUNT=$((PASS_COUNT + 1)); printf '  PASS  %s\n' "$*"; }
fail() { FAIL_COUNT=$((FAIL_COUNT + 1)); printf '  FAIL  %s\n' "$*" >&2; }
info() { printf '  INFO  %s\n' "$*"; }

# ── Compute the authoritative external-backup file set from disk. ───────────
# This is the ONLY hard reference: whatever external-backup-* files exist in
# release/systemd/ and release/scripts/ are what all five manifests must
# match. Anything else is a manifest drift.
authoritative_set() {
    (
        cd "$REPO_ROOT"
        # Systemd units: services + timers, both root and check variants.
        # ls returns names only; sort for stable comparison.
        ls release/systemd/orvix-external-backup*.service \
           release/systemd/orvix-external-backup*.timer 2>/dev/null | sort
        # Scripts: everything matching the external-backup-*.sh pattern.
        ls release/scripts/external-backup-*.sh 2>/dev/null | sort
    ) | sort -u
}

AUTHORITATIVE="$(authoritative_set)"
AUTHORITATIVE_COUNT="$(printf '%s\n' "$AUTHORITATIVE" | grep -c . || true)"

info "authoritative external-backup file set (from disk): $AUTHORITATIVE_COUNT files"
printf '%s\n' "$AUTHORITATIVE" | sed 's/^/         /'

if [ "$AUTHORITATIVE_COUNT" -lt 10 ]; then
    fail "authoritative set has fewer than 10 files — something is missing from release/systemd/ or release/scripts/"
fi

# ── Extract the external-backup entries each manifest actually references. ──
# Each extractor greps the specific manifest for external-backup path lines,
# normalizes them to `release/…/name` form, sorts, dedupes, and returns them.

extract_build_bundle_required_files() {
    # REQUIRED_FILES array in build-release-bundle.sh: array of bare
    # release/... paths, comment-free lines that reference external-backup.
    awk '/^REQUIRED_FILES=\(/,/^\)/' "$REPO_ROOT/release/scripts/build-release-bundle.sh" \
        | grep -oE 'release/(systemd|scripts)/(orvix-)?external-backup[A-Za-z0-9._-]*' \
        | sort -u
}

extract_build_bundle_cp_block() {
    # The plain `cp release/... "$BUNDLE_ROOT/release/..."` block for the
    # systemd/scripts external-backup files.
    grep -oE '^cp release/(systemd|scripts)/(orvix-)?external-backup[A-Za-z0-9._-]*' \
        "$REPO_ROOT/release/scripts/build-release-bundle.sh" \
        | awk '{print $2}' \
        | sort -u
}

extract_build_bundle_required_bundle() {
    # BUNDLE_REQUIRED array — same shape as REQUIRED_FILES, different array.
    awk '/^BUNDLE_REQUIRED=\(/,/^\)/' "$REPO_ROOT/release/scripts/build-release-bundle.sh" \
        | grep -oE 'release/(systemd|scripts)/(orvix-)?external-backup[A-Za-z0-9._-]*' \
        | sort -u
}

extract_install_public_validator() {
    # validate_bundle_layout's here-doc REQUIRED list.
    awk '/<<REQUIRED$/,/^REQUIRED$/' "$REPO_ROOT/release/install-public.sh" \
        | grep -oE 'release/(systemd|scripts)/(orvix-)?external-backup[A-Za-z0-9._-]*' \
        | sort -u
}

extract_smoke_fixture() {
    # The `for rel in \ ... release/... release/... ; do` block in
    # smoke-install-public.sh that stubs each required file.
    grep -oE 'release/(systemd|scripts)/(orvix-)?external-backup[A-Za-z0-9._-]*' \
        "$REPO_ROOT/release/scripts/smoke-install-public.sh" \
        | sort -u
}

compare_manifest() {
    local label="$1"
    local extracted="$2"
    if [ "$extracted" = "$AUTHORITATIVE" ]; then
        pass "$label matches the authoritative set ($AUTHORITATIVE_COUNT files)"
        return 0
    fi
    fail "$label is out of sync with the authoritative set"
    local missing="$(comm -23 <(printf '%s\n' "$AUTHORITATIVE") <(printf '%s\n' "$extracted"))"
    local stale="$(comm -13 <(printf '%s\n' "$AUTHORITATIVE") <(printf '%s\n' "$extracted"))"
    if [ -n "$missing" ]; then
        printf '         missing from %s:\n' "$label" >&2
        printf '%s\n' "$missing" | sed 's/^/           - /' >&2
    fi
    if [ -n "$stale" ]; then
        printf '         stale/extra in %s (file no longer exists on disk):\n' "$label" >&2
        printf '%s\n' "$stale" | sed 's/^/           - /' >&2
    fi
    return 1
}

echo "── Cross-manifest external-backup synchronization ──"

compare_manifest "build-release-bundle.sh REQUIRED_FILES" \
    "$(extract_build_bundle_required_files)" || true
compare_manifest "build-release-bundle.sh cp block" \
    "$(extract_build_bundle_cp_block)" || true
compare_manifest "build-release-bundle.sh BUNDLE_REQUIRED" \
    "$(extract_build_bundle_required_bundle)" || true
compare_manifest "install-public.sh validate_bundle_layout" \
    "$(extract_install_public_validator)" || true
compare_manifest "smoke-install-public.sh synthetic fixture" \
    "$(extract_smoke_fixture)" || true

echo
echo "── Summary ──"
printf '  PASS=%d  FAIL=%d\n' "$PASS_COUNT" "$FAIL_COUNT"

if [ "$FAIL_COUNT" -gt 0 ]; then
    echo
    echo "REMEDIATION: whenever an external-backup file is added, renamed, or"
    echo "removed, update ALL FIVE manifests above so their references match"
    echo "the actual files in release/systemd/ and release/scripts/."
    exit 1
fi

exit 0
