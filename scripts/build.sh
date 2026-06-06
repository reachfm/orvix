#!/usr/bin/env bash
set -euo pipefail

VERSION="${1:-1.0.0}"
BUILD_DIR="build"

echo "==> Building Orvix v${VERSION}..."

mkdir -p "${BUILD_DIR}"

CGO_ENABLED=0 go build \
    -ldflags "-X github.com/orvix/orvix/internal/config.buildVersion=${VERSION} -X github.com/orvix/orvix/internal/config.buildTime=$(date -u '+%Y-%m-%d_%H:%M:%S')" \
    -o "${BUILD_DIR}/orvix" \
    ./cmd/orvix/

echo "==> Build complete: ${BUILD_DIR}/orvix"

cd web/webmail && npm install && npm run build && cd ../..
cd web/admin && npm install && npm run build && cd ../..

echo "==> All builds complete"
