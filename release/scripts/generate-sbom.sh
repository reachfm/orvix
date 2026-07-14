#!/usr/bin/env bash
set -euo pipefail

OUTPUT="${1:?usage: generate-sbom.sh OUTPUT BINARY VERSION COMMIT}"
BINARY="${2:?binary path required}"
VERSION="${3:?version required}"
COMMIT="${4:?commit required}"
ROOT="$(git rev-parse --show-toplevel)"
GO_BIN="${GO_BIN:-go}"
if ! command -v "$GO_BIN" >/dev/null 2>&1 && [ ! -x "$GO_BIN" ]; then
  for candidate in /usr/local/go/bin/go /mnt/c/Go/bin/go.exe /c/Go/bin/go.exe; do
    if [ -x "$candidate" ]; then GO_BIN="$candidate"; break; fi
  done
fi
command -v "$GO_BIN" >/dev/null 2>&1 || [ -x "$GO_BIN" ] || { echo "Go toolchain not found" >&2; exit 1; }
CREATED="${SOURCE_DATE_EPOCH:+$(date -u -d "@$SOURCE_DATE_EPOCH" +%Y-%m-%dT%H:%M:%SZ)}"
CREATED="${CREATED:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
BIN_SHA="$(sha256sum "$BINARY" | awk '{print $1}')"
MODULES="$(mktemp -t orvix-sbom-modules.XXXXXX)"
trap 'rm -f "$MODULES"' EXIT
(cd "$ROOT" && "$GO_BIN" list -m -f '{{.Path}} {{if .Version}}{{.Version}}{{else}}devel{{end}}' all) >"$MODULES"

mkdir -p "$(dirname "$OUTPUT")"
{
  echo "SPDXVersion: SPDX-2.3"
  echo "DataLicense: CC0-1.0"
  echo "SPDXID: SPDXRef-DOCUMENT"
  echo "DocumentName: Orvix-Enterprise-Mail-$VERSION"
  echo "DocumentNamespace: https://orvix.email/spdx/$COMMIT"
  echo "Creator: Tool: orvix-generate-sbom"
  echo "Created: $CREATED"
  echo
  echo "PackageName: github.com/orvix/orvix"
  echo "SPDXID: SPDXRef-Package-Orvix"
  echo "PackageVersion: $VERSION"
  echo "PackageSupplier: Organization: Orvix"
  echo "PackageDownloadLocation: NOASSERTION"
  echo "FilesAnalyzed: false"
  echo "PackageChecksum: SHA256: $BIN_SHA"
  echo "PackageLicenseConcluded: NOASSERTION"
  echo "PackageLicenseDeclared: NOASSERTION"
  echo "PackageCopyrightText: NOASSERTION"
  echo "ExternalRef: PACKAGE-MANAGER purl pkg:golang/github.com/orvix/orvix@$VERSION"
  echo
  echo "Relationship: SPDXRef-DOCUMENT DESCRIBES SPDXRef-Package-Orvix"
  awk -f "$ROOT/release/scripts/sbom-modules.awk" "$MODULES"
} >"$OUTPUT"

grep -q '^SPDXVersion: SPDX-2.3$' "$OUTPUT"
grep -q '^Relationship: SPDXRef-DOCUMENT DESCRIBES SPDXRef-Package-Orvix$' "$OUTPUT"
echo "SBOM=$OUTPUT"
