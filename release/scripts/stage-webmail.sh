#!/usr/bin/env bash
# stage-webmail.sh — CLI entry point around lib-webmail-stage.sh's
# stage_webmail(). Regenerates release/webmail from web/webmail-release.
#
# Usage: bash release/scripts/stage-webmail.sh [repo-root]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${1:-$(cd "$SCRIPT_DIR/../.." && pwd)}"

# shellcheck source=./lib-webmail-stage.sh
source "$SCRIPT_DIR/lib-webmail-stage.sh"

stage_webmail "$REPO_ROOT/web/webmail-release" "$REPO_ROOT/release/webmail"
echo "staged: $REPO_ROOT/web/webmail-release -> $REPO_ROOT/release/webmail"
