#!/usr/bin/env bash
set -euo pipefail

# Orvix RC2 Build Script
# Usage: ./scripts/build.sh

set -e

VERSION="1.0.1"
BUILD_DIR="release"
ARCHIVE_NAME="orvix-v${VERSION}-linux-amd64"

echo "========================================"
echo "Orvix v${VERSION} RC2 Build Script"
echo "========================================"

# Clean previous builds
echo "[1/6] Cleaning previous builds..."
rm -rf "${BUILD_DIR:?}/"*
rm -f "../${ARCHIVE_NAME}.tar.gz"

# Build Go binary (CGO disabled for pure Go SQLite)
echo "[2/6] Building Go binary (CGO_ENABLED=0)..."
cd ..
CGO_ENABLED=0 go build -ldflags="-s -w" -o "${BUILD_DIR}/orvix" ./cmd/orvix/

# Build frontends
echo "[3/6] Building webmail frontend..."
cd web/webmail && npm install --silent && npm run build --silent && cd ../..

echo "[4/6] Building admin frontend..."
cd web/admin && npm install --silent && npm run build --silent && cd ../..

# Copy build artifacts
echo "[5/6] Copying build artifacts..."
cd ..

# Rename binary for release
mv "${BUILD_DIR}/orvix" "${BUILD_DIR}/orvix-v${VERSION}-linux-amd64"

# Create release package
echo "[6/6] Creating release package..."
cd "${BUILD_DIR}"

# Create package structure
mkdir -p "systemd" "configs" "scripts"

# Copy systemd service
cp ../release/systemd/orvix.service systemd/

# Copy configs
cp ../orvix.yaml configs/orvix.yaml.example

# Copy scripts
cp ../release/scripts/*.sh scripts/ 2>/dev/null || true

# Create checksums
echo "# Orvix v${VERSION} RC2 Checksums" > checksums.txt
echo "# Generated: $(date -Iseconds)" >> checksums.txt
echo "" >> checksums.txt
sha256sum "orvix-v${VERSION}-linux-amd64" >> checksums.txt

# Create archive
tar -czf "../${ARCHIVE_NAME}.tar.gz" *

# Display results
echo ""
echo "========================================"
echo "Build Complete!"
echo "========================================"
echo ""
echo "Release files:"
ls -lh *
echo ""
echo "Archive: ../${ARCHIVE_NAME}.tar.gz"
echo ""
echo "Checksums:"
cat checksums.txt
echo ""

# Verify the binary works (basic test)
echo "Verifying binary..."
if file orvix-v${VERSION}-linux-amd64 | grep -q "ELF"; then
    echo "✓ Binary is valid ELF executable"
else
    echo "✗ Binary validation failed"
    exit 1
fi

if strings orvix-v${VERSION}-linux-amd64 | grep -q "modernc.org/sqlite"; then
    echo "✓ Binary uses modernc.org/sqlite (no CGO)"
else
    echo "✗ SQLite driver check failed"
    exit 1
fi

echo ""
echo "✅ Build verification passed!"
echo ""
echo "Next steps:"
echo "  1. Upload ${ARCHIVE_NAME}.tar.gz to GitHub releases"
echo "  2. Update VERSION file to ${VERSION}"
echo "  3. Update RELEASE_NOTES.md"
echo "  4. Update checksums.txt with actual SHA256"
echo "  5. Test on clean VPS"