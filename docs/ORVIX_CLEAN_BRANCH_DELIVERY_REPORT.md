> **SUPERSEDED** — This is a historical execution report.
> Current authoritative status: `docs/ORVIX_V1_SOURCE_OF_TRUTH.md`
>
> PR #27 has been squash-merged into `main` at
> `e7f5441c25732b7583f3e57053f1e4d79f417fc5`.

# Security Sprint Clean Branch Delivery Report

## 1. Branch Pushed

`security/day1-hardening-review`

## 2. Commit SHA

`3fee790` — `security: harden admin auth and SMTP boundaries`

## 3. Files Staged (26)

### New files (8)
| File | Description |
|------|-------------|
| `internal/auth/password.go` | Centralized Argon2id + bcrypt password hashing |
| `internal/auth/password_test.go` | Password hashing tests |
| `internal/auth/smtp_tls.go` | Shared SMTP-TLS dial helper |
| `internal/auth/smtp_tls_test.go` | SMTP TLS regression tests (10 tests) |
| `internal/api/handlers/rehash_on_login_test.go` | Rehash-on-login tests (6 tests) |
| `internal/api/handlers/tenant_isolation_matrix_test.go` | Tenant isolation matrix tests (12 tests) |
| `internal/coremail/jmap/rc1_admin_test_harness_test.go` | JMAP admin test-only harness (replaces adminapi) |
| `docs/ORVIX_MANDATORY_TRUTHFUL_REPORT.md` | Mandatory truthful execution report |

### Modified files (18)
| File | Change |
|------|--------|
| `internal/auth/auth.go` | VerifyPasswordWithRehash, bcrypt→Argon2id delegation, NormalizeRole, canonical role constants, token_version |
| `internal/auth/rbac/rbac.go` | Canonical role permission maps (PlatformSuperAdmin, TenantAdmin, etc.) |
| `internal/auth/alerts.go` | Replaced unprotected smtp.SendMail with DialSMTPWithTLS |
| `internal/api/mail_sender.go` | Refactored dialAuthenticated to delegate to auth.DialSMTPWithTLS |
| `internal/api/handlers/handlers.go` | Rehash-on-login in Handler.Login |
| `internal/api/handlers/webmail_auth.go` | Rehash-on-login in WebmailLogin; verifyMailboxPassword returns needsRehash |
| `internal/api/handlers/saas_admin.go` | isSuperRole recognizes RolePlatformSuperAdmin |
| `internal/api/router.go` | Admin/internalOps/platform groups include RolePlatformSuperAdmin |
| `internal/enterprise/rbac/evaluator.go` | IsPlatformRole, IsTenantRole, CanManageTenant, CanAccessDomain updated |
| `cmd/orvix/main.go` | auth.HashPassword, removed dead hashPasswordArgon2id |
| `cmd/orvix/password_chain_test.go` | Updated for Argon2id hash format |
| `cmd/orvix/freshinstall_test.go` | Updated bcrypt assertion to accept Argon2id |
| `internal/coremail/jmap/rc1_integrated_test.go` | Removed adminapi dependency, removed build tag |
| `internal/adminapi/server.go` | Added `//go:build legacy_adminapi` |
| `internal/adminapi/auth.go` | Added `//go:build legacy_adminapi` |
| `internal/adminapi/middleware.go` | Added `//go:build legacy_adminapi` |
| `internal/adminapi/types.go` | Added `//go:build legacy_adminapi` |
| `internal/adminapi/admin_test.go` | Added `//go:build legacy_adminapi` |

## 4. Files Intentionally Excluded (4)

These files contained pre-existing unrelated changes from prior development:

| File | Reason |
|------|--------|
| `internal/coremail/runtime/module.go` | Pre-existing runtime changes, unrelated to security |
| `release/install.sh` | Pre-existing installer changes, unrelated |
| `release/scripts/build-release-bundle.sh` | Pre-existing build script changes, unrelated |
| `release/scripts/setup-https.sh` | Pre-existing HTTPS setup changes, unrelated |

## 5. Verification Commands and Results

| Command | Result |
|---------|--------|
| `gofmt -l <staged Go files>` | No unformatted files |
| `git diff --check` | No whitespace errors |
| `go vet ./...` | PASS |
| `go build ./...` | PASS |
| `go build -tags legacy_adminapi ./...` | PASS |
| `go test ./internal/auth/... -count=1` | PASS (21.745s + 2.991s) |
| `go test ./internal/enterprise/rbac/... -count=1` | PASS (2.788s) |
| `go test ./internal/admin/... -count=1` | PASS (all 5 sub-packages) |
| `go test ./internal/api/... -count=1` | PASS (38.981s + 535.845s + 1.789s) |
| `go test ./internal/coremail/jmap/... -count=1` | PASS (88.097s, default build) |
| `go test -tags legacy_adminapi ./internal/adminapi/... -count=1` | PASS (26.765s) |
| `go test -tags legacy_adminapi ./internal/coremail/jmap/... -count=1` | PASS (86.240s) |

## 6. Known Pre-Existing Failures

`TestMetadataArgs_FastExit` in `cmd/orvix` — times out at ~65s vs expected <10s on Windows filesystem. This test is timing-sensitive and unrelated to the security changes. It was failing before this sprint and remains failing after.

## 7. GitHub Push Result

```
remote: Create a pull request for 'security/day1-hardening-review' on GitHub by visiting:
remote:      https://github.com/reachfm/orvix/pull/new/security/day1-hardening-review
To https://github.com/reachfm/orvix
 * [new branch]      security/day1-hardening-review -> security/day1-hardening-review
```

Branch pushed successfully. No PR was created — awaiting manual PR creation for Claude/CI review.

## 8. Confirmation

- **Branch:** `security/day1-hardening-review` pushed to `origin`
- **Commit:** `3fee790`
- **No merge to `main` occurred.**
- **No deployment occurred.**
- **No production modification occurred.**
