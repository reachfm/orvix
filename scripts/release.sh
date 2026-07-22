#!/bin/bash
# OrvixEM Release Script
# Domain: orvix.email | Product: OrvixEM

set -euo pipefail

VERSION="${1:-}"
if [ -z "$VERSION" ]; then
    echo "Usage: $0 <version> [channel]"
    echo "Example: $0 1.0.0 stable"
    exit 1
fi

CHANNEL="${2:-stable}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo development)}"
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)

export VERSION CHANNEL COMMIT BUILD_DATE

echo "==> Releasing OrvixEM v$VERSION ($CHANNEL)"
echo "    Commit: $COMMIT"

# Build frontend
echo "    Building frontend apps..."
cd web
npm install --silent
for APP in admin webmail portal; do
    echo "      Building $APP..."
    cd $APP && npx vite build --config vite.config.ts --logLevel silent && cd ..
done
cd ..

# Build for multiple platforms
PLATFORMS=("linux/amd64" "linux/arm64")
mkdir -p dist
rm -f dist/SHA256SUMS

for PLATFORM in "${PLATFORMS[@]}"; do
    GOOS="${PLATFORM%/*}"
    GOARCH="${PLATFORM#*/}"
    OUTPUT="orvix-${GOOS}-${GOARCH}-v${VERSION}"

    echo "    Building for $GOOS/$GOARCH..."
    
    CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
        go build -ldflags="-s -w -X main.Version=$VERSION -X main.Product=OrvixEM -X main.Commit=$COMMIT -X main.Channel=$CHANNEL -X main.BuildDate=$BUILD_DATE" \
        -o "dist/$OUTPUT" ./cmd/orvix

    # Validate binary format
    if command -v file &>/dev/null; then
        BIN_TYPE=$(file "dist/$OUTPUT")
        echo "    Binary type: $BIN_TYPE"
        case "$BIN_TYPE" in
            *ELF*64-bit*)
                echo "    ✅ ELF 64-bit binary verified"
                ;;
            *PE32*Windows*)
                echo "    ❌ PE32/Windows binary detected! GOOS/GOARCH may be wrong."
                echo "    Expected: ELF 64-bit LSB executable"
                echo "    Got:      $BIN_TYPE"
                exit 1
                ;;
            *)
                echo "    ⚠️  Unknown binary format (not ELF, not PE)"
                ;;
        esac
    else
        # Check magic bytes as fallback
        MAGIC=$(xxd -p -l 4 "dist/$OUTPUT" 2>/dev/null || od -A n -t x1 -N 4 "dist/$OUTPUT" 2>/dev/null | tr -d ' ')
        if [ "$MAGIC" = "7f454c46" ]; then
            echo "    ✅ ELF binary (magic bytes verified)"
        elif [ "$MAGIC" = "4d5a" ]; then
            echo "    ❌ PE/Windows binary detected! Aborting."
            exit 1
        fi
    fi

    (cd dist && sha256sum "$OUTPUT" >> SHA256SUMS)
done

# Create release archive
echo ""
echo "    Creating release archive..."
(cd dist && tar -czf "orvixem-v${VERSION}-linux-amd64.tar.gz" \
    "orvix-linux-amd64-v${VERSION}" \
    "orvix-linux-arm64-v${VERSION}" \
    SHA256SUMS)

# Copy config and install script
cp configs/orvix.yaml dist/
cp scripts/install.sh dist/
chmod +x dist/install.sh
(cd dist && sha256sum install.sh orvix.yaml >> SHA256SUMS)

echo ""
echo "==> Release artifacts in dist/:"
ls -la dist/
echo ""
echo "    Release package: dist/orvixem-v${VERSION}-linux-amd64.tar.gz"
echo ""
echo "    To publish:"
echo "    Upload dist/* to https://updates.orvix.email/v1/download/$VERSION/"
echo "    Upload SHA256SUMS to https://updates.orvix.email/v1/manifest/$VERSION/"
echo "    Publish dist/install.sh at https://orvix.email/install.sh"
