#!/bin/bash
set -euo pipefail

VERSION="${VERSION:-0.1.0}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo 'development')}"
CHANNEL="${CHANNEL:-stable}"
BUILD_DATE="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
OUTPUT="${OUTPUT:-orvix}"

LDFLAGS="-s -w -X main.Version=$VERSION -X main.Product=OrvixEM -X main.Commit=$COMMIT -X main.Channel=$CHANNEL -X main.BuildDate=$BUILD_DATE"

echo "==> Building OrvixEM v$VERSION ($CHANNEL)"

# Build frontend apps
cd web && npm install --silent 2>/dev/null
for APP in admin webmail portal; do (cd $APP && npx vite build --config vite.config.ts --logLevel silent) 2>/dev/null || true; done
cd ..

# Build backend (Linux AMD64)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$LDFLAGS" -o "$OUTPUT" ./cmd/orvix
echo "    Binary: $OUTPUT ($(ls -lh "$OUTPUT" | awk '{print $5}'))"

# Validate ELF format
if command -v file &>/dev/null; then file "$OUTPUT" | grep -q ELF && echo "    ELF verified" || echo "    WARNING: not ELF"; fi
echo "==> Build complete"
