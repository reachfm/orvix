#!/usr/bin/env bash
set -euo pipefail

# Workflow-contract test for the "Archive signed bundle as a workflow-run
# artifact" step added to .github/workflows/release-bundle.yml. Asserts
# the properties of the step from the YAML source text (no live GitHub
# Actions run required), so a regression here is caught before any
# workflow is ever dispatched.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
WORKFLOW="$REPO_ROOT/.github/workflows/release-bundle.yml"
PASS=0 FAIL=0

fail_msg() { echo "FAIL: $1"; FAIL=$((FAIL + 1)); }
pass()     { echo "PASS: $1"; PASS=$((PASS + 1)); }

echo "=== Release Artifact Upload Workflow-Contract Test ==="

[ -f "$WORKFLOW" ] || { echo "FAIL: workflow file not found: $WORKFLOW"; exit 1; }

# --- Locate the three anchor lines by number ---
SIG_LINE="$(grep -n 'SIGNATURE_VERIFIED_LOCALLY=YES" >> "\$GITHUB_ENV"' "$WORKFLOW" | head -n1 | cut -d: -f1)"
UPLOAD_STEP_LINE="$(grep -n '^      - name: Archive signed bundle as a workflow-run artifact' "$WORKFLOW" | head -n1 | cut -d: -f1)"
NEXT_STEP_LINE="$(grep -n '^      - name: Dry-run verdict' "$WORKFLOW" | head -n1 | cut -d: -f1)"

if [ -z "$SIG_LINE" ]; then
  fail_msg "could not find local signature-verification success marker"
else
  pass "found local signature-verification success marker at line $SIG_LINE"
fi

if [ -z "$UPLOAD_STEP_LINE" ]; then
  fail_msg "could not find the 'Archive signed bundle as a workflow-run artifact' step"
  exit 1
else
  pass "found upload-artifact step at line $UPLOAD_STEP_LINE"
fi

# 1. Ordering: upload step must appear strictly after signature verification.
if [ -n "$SIG_LINE" ] && [ "$UPLOAD_STEP_LINE" -gt "$SIG_LINE" ]; then
  pass "upload step is positioned after local signature verification"
else
  fail_msg "upload step is NOT positioned after local signature verification"
fi

# Extract just this step's YAML block (from its "- name:" line up to the
# next top-level step or EOF) for the remaining assertions.
if [ -n "$NEXT_STEP_LINE" ]; then
  STEP_BLOCK="$(sed -n "${UPLOAD_STEP_LINE},$((NEXT_STEP_LINE - 1))p" "$WORKFLOW")"
else
  STEP_BLOCK="$(tail -n +"$UPLOAD_STEP_LINE" "$WORKFLOW")"
fi

# 2. Restricted to workflow_dispatch: the step's own condition must be
#    exactly env.PUBLISH == 'true' AND env.PUBLISH is only ever set to
#    'true' inside the workflow_dispatch branch of the preflight step
#    (never on pull_request or push).
if echo "$STEP_BLOCK" | grep -qE "^\s*if: env\.PUBLISH == 'true'\s*$"; then
  pass "upload step is gated on env.PUBLISH == 'true'"
else
  fail_msg "upload step is missing the exact 'if: env.PUBLISH == '\''true'\''' gate"
fi

PREFLIGHT_BLOCK="$(awk '/name: Release trigger preflight/,/name: Install Go test deps/' "$WORKFLOW")"
if echo "$PREFLIGHT_BLOCK" | grep -q 'github.event_name.*!=.*workflow_dispatch' \
  && echo "$PREFLIGHT_BLOCK" | grep -q 'PUBLISH=true'; then
  pass "PUBLISH is only ever set to true on the workflow_dispatch branch of preflight"
else
  fail_msg "could not confirm PUBLISH=true is gated on workflow_dispatch in preflight"
fi

# 3. Correct action + version.
if echo "$STEP_BLOCK" | grep -q 'uses: actions/upload-artifact@v7'; then
  pass "uses actions/upload-artifact@v7"
else
  fail_msg "does not use actions/upload-artifact@v7"
fi

# 4. Unique name containing commit SHA and run ID.
if echo "$STEP_BLOCK" | grep -qE 'name: orvix-release-bundle-\$\{\{ github\.sha \}\}-run\$\{\{ github\.run_id \}\}'; then
  pass "artifact name contains github.sha and github.run_id"
else
  fail_msg "artifact name does not contain both github.sha and github.run_id"
fi

# 5. Expected files, and only the already-verified release outputs.
for pattern in 'dist/\*\.tar\.gz' 'dist/\*\.sha256' 'dist/\*\.sig' 'dist/\*\.manifest\.json' 'dist/\*\.sbom\.spdx'; do
  if echo "$STEP_BLOCK" | grep -qE "$pattern"; then
    pass "path includes $pattern"
  else
    fail_msg "path is missing $pattern"
  fi
done

# 6. Missing files fail the job.
if echo "$STEP_BLOCK" | grep -qE '^\s*if-no-files-found: error\s*$'; then
  pass "if-no-files-found is set to error"
else
  fail_msg "if-no-files-found is not set to error"
fi

# 7. Retention exactly 3 days.
if echo "$STEP_BLOCK" | grep -qE '^\s*retention-days: 3\s*$'; then
  pass "retention-days is exactly 3"
else
  fail_msg "retention-days is not exactly 3"
fi

# 8. The signing secret is never referenced inside this step's block.
if echo "$STEP_BLOCK" | grep -q 'ORVIX_RELEASE_SIGNING_KEY_PEM_B64'; then
  fail_msg "upload step references the signing secret — it must never touch it"
else
  pass "upload step never references ORVIX_RELEASE_SIGNING_KEY_PEM_B64"
fi

# 9. Never touches tag creation, release publication, or the trusted key /
#    signing algorithm — this step's block must not reference any of the
#    mutating release/signing primitives.
for forbidden in 'gh release create' 'gh release edit' 'gh release upload' 'pkeyutl -sign' 'orvix-release-signing.pub.pem' 'git tag'; do
  if echo "$STEP_BLOCK" | grep -qF "$forbidden"; then
    fail_msg "upload step unexpectedly references '$forbidden'"
  else
    pass "upload step does not reference '$forbidden'"
  fi
done

echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ]
