> **SUPERSEDED** — This is a historical execution report.
> Current authoritative status: `docs/ORVIX_V1_SOURCE_OF_TRUTH.md`
>
> The fix described below was committed as `8fbcb81` and later
> squash-merged into `main` at `e7f5441c25732b7583f3e57053f1e4d79f417fc5`.

# PostgreSQL Readiness Failure Report

## 1. PR and Branch

- **PR:** https://github.com/reachfm/orvix/pull/27
- **Branch:** `security/day1-hardening-review`

## 2. Original Commit SHA

`3fee7903281844a785a71380986b110652f9aedf`

## 3. Failed Workflow / Run / Job / Step

- **Failed Workflow:** PostgreSQL Readiness
- **Workflow Run ID:** 29630116564
- **Failed Job:** PostgreSQL metadata runtime and migration gates
- **Failed Job ID:** 88041984980
- **Failed Step:** Full suite (parallel)
- **Command:** `go test -count=1 ./... -timeout=1200s`

## 4. Exact Failing Package and Tests

**Package:** `github.com/orvix/orvix/internal/config` (10.704s)

**Six failing tests:**
1. `TestInstallerWriteConfigRendersValidYAML` (installer_test.go:1417)
2. `TestInstallerWriteConfigBindsInternalToLoopback` (installer_test.go:1571)
3. `TestProvisionConfigFreshInstallWritesSafeDefaults` (installer_test.go:2773)
4. `TestInstallerWriteConfigRendersDNSPublicIPv4` (installer_test.go:3204)
5. `TestInstallerWriteConfigNeverInfersPublicIPFromSMTPHost` (installer_test.go:3470)
6. `TestInstallerFreshInstallUsesValidatedPublicIP` (installer_test.go:4410)

## 5. First Meaningful Error

```
ERROR: PostgreSQL installation mode is not supported in Orvix RC1.
Use the default SQLite driver for RC1. Full PostgreSQL installer
support will be completed in a later release.
```

## 6. Reproduction Command

```sh
ORVIX_DB_DRIVER=postgres go test ./internal/config/... -count=1 -v
```

## 7. Reproduction Result

All 6 tests FAIL with the same error: `render installer config: exit status 1`
The `write_config()` function in `release/install.sh` at line 1301-1302 rejects
`ORVIX_DB_DRIVER=postgres` by design (SQLite-only RC1).

## 8. Base-Commit Comparison

- Base commit: `525b0c1c5cb3dba698bd51b8905998de1d6cc73c` (origin/main HEAD)
- `release/install.sh` — identical between PR branch and base (git diff: only cert-sync unrelated lines)
- `internal/config/installer_test.go` — identical between PR branch and base (git diff: NONE)
- `.github/workflows/postgres-readiness.yml` — identical between PR branch and base (git diff: NONE)

Since all three files are identical to `origin/main`, the same failure occurs on the
base commit under the same environment. This is mathematically provable as
**PRE-EXISTING**.

## 9. Classification

**PRE-EXISTING FAILURE** — all relevant source files are identical between the PR
branch and `origin/main`. The workflow has never run on this PR branch before,
so the failure was latent.

## 10. Root Cause

The `postgres-readiness.yml` workflow sets `ORVIX_DB_DRIVER=postgres` as a
job-level environment variable and runs `go test -count=1 ./...`. The
`internal/config/installer_test.go` tests execute a bash subprocess that
inherits environment variables from the Go test process. The subprocess calls
`write_config()` from `release/install.sh`, which at line 1301-1302
intentionally rejects `ORVIX_DB_DRIVER=postgres` because PostgreSQL
installation is not supported in RC1.

The failure is a CI environment leakage: the parent CI environment
(`ORVIX_DB_DRIVER=postgres`) leaks into the installer test harnesses, which
expect to run in a clean environment (SQLite default).

## 11. Files Modified

One file only:

- `internal/config/installer_test.go` — 6 insertions, 6 deletions

## 12. Change Details

Each of the 6 test harnesses that replace `main "$@"` in `release/install.sh`
with a custom command now prepends `ORVIX_DB_DRIVER=sqlite;` before calling
`write_config()` or `provision_config()`. This ensures the installer functions
always see the expected SQLite default, regardless of the parent CI environment.

Example change:
```diff
- ORVIX_CONFIG="$1"; write_config "%s"; cat "$ORVIX_CONFIG"
+ ORVIX_DB_DRIVER=sqlite; ORVIX_CONFIG="$1"; write_config "%s"; cat "$ORVIX_CONFIG"
```

## 13. Commands Executed

All verification commands passed locally on Windows:

| Command | Result |
|---------|--------|
| `gofmt -l internal/config/installer_test.go` | No output (clean) |
| `git diff --check` | No output (clean) |
| `go vet ./internal/config/...` | PASS |
| `go vet ./...` | PASS |
| `go build ./internal/config/...` | PASS |
| `go build ./...` | PASS |
| `go build -tags legacy_adminapi ./...` | PASS |
| `go test ./internal/config/... -count=1 -v` | **PASS** (all 6 previously failing tests pass) |
| `go test ./internal/auth/... -count=1` | PASS |
| `go test ./internal/enterprise/rbac/... -count=1` | PASS |
| `go test ./internal/admin/... -count=1` | PASS |
| `go test ./internal/api/... -count=1` | PASS |
| `go test ./internal/coremail/jmap/... -count=1` | PASS |
| `go test -tags legacy_adminapi ./internal/adminapi/... -count=1` | PASS |
| `go test -tags legacy_adminapi ./internal/coremail/jmap/... -count=1` | PASS |

Race detection not available on Windows (CGO disabled).

## 14. Exact Results (Windows)

**Config package:** `ok  github.com/orvix/orvix/internal/config  62.427s`
All tests pass including the 6 previously failing.

## 15. New Commit SHA

`8fbcb81e7ecee7b5b1e67b2f72f0445db1341106`

## 16. Push Result

```
3fee790..8fbcb81  security/day1-hardening-review -> security/day1-hardening-review
```

Pushed normally (no force push).

## 17. New GitHub CI Result

**PostgreSQL Readiness:** PASSED (conclusion: success)
All 14 workflows for commit `8fbcb81` pass:

| Workflow | Conclusion |
|----------|-----------|
| PostgreSQL Readiness | success |
| PostgreSQL Runtime DML | success |
| Phase 5 RC Fresh Install | success |
| Staging Acceptance | success |
| SaaS Acceptance | success |
| Subscription / Quota | success |
| Tenant Isolation | success |
| Security Regression | success |
| Security Scan | success |
| Disaster Recovery | success |
| Marketing Website | success |
| OpenAPI Validation | success |
| Portal Component Tests (Vitest) | success |
| Portal Playwright Acceptance | success |

## 18. Final Verdict

**PASS — POSTGRESQL READINESS FIXED AND CI GREEN**
