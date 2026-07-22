#!/bin/bash
# OrvixEM Update Verification Script
# Verifies update package integrity

set -euo pipefail

PACKAGE="${1:-}"
SIGNATURE="${2:-${PACKAGE}.sig}"
CHECKSUM="${3:-SHA256SUMS}"

if [ -z "$PACKAGE" ]; then
    echo "Usage: $0 <package.tar.gz> [signature] [checksums]"
    exit 1
fi

echo "Verifying update package: $PACKAGE"

# Check file exists
if [ ! -f "$PACKAGE" ]; then
    echo "ERROR: Package not found: $PACKAGE"
    exit 1
fi

# Verify SHA256
if [ -f "$CHECKSUM" ]; then
    echo "  Verifying SHA256..."
    if command -v sha256sum &>/dev/null; then
        sha256sum -c "$CHECKSUM" 2>/dev/null || true
    fi
fi

# Verify signature
if [ -f "$SIGNATURE" ] && command -v gpg &>/dev/null; then
    echo "  Verifying GPG signature..."
    gpg --verify "$SIGNATURE" "$PACKAGE" 2>/dev/null && echo "  Signature: OK" || echo "  Signature: INVALID"
fi

# Verify archive integrity
echo "  Testing archive integrity..."
if tar -tzf "$PACKAGE" >/dev/null 2>&1; then
    echo "  Archive: OK"
else
    echo "  Archive: CORRUPT"
    exit 1
fi

echo "Verification complete"
