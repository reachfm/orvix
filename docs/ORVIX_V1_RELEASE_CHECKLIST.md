# Orvix V1 Release Checklist

## A. Completed and Merged

- [x] Centralized Argon2id password hashing (`internal/auth/password.go`)
- [x] bcrypt compatibility and rehash-on-login
- [x] SMTP authentication only after verified TLS (`internal/auth/smtp_tls.go`)
- [x] Alert SMTP TLS hardening
- [x] Platform/Tenant RBAC role corrections
- [x] Tenant-isolation test matrix (12 tests)
- [x] Legacy adminapi quarantine (`//go:build legacy_adminapi`)
- [x] JMAP integration test restoration (decoupled from adminapi)
- [x] PostgreSQL installer-test environment isolation
- [x] PR #27 squash-merged into `main` (`e7f5441`)

## B. Security Follow-up

- [ ] Verify TenantAdmin Fiber Admin route authorization
  - Issue: **TO BE CREATED**
  - Acceptance: every admin endpoint correctly enforces TenantAdmin role
  - Tests: `internal/api/handlers/tenant_isolation_matrix_test.go` extended
- [ ] Verify Argon2id auth_scheme consistency (`$argon2id$` vs `argon2id`)
  - Issue: **TO BE CREATED**
  - Acceptance: single canonical format used throughout codebase
  - Tests: grep-based lint check or unit test
- [ ] Triage and document gosec informational findings
  - Issue: **TO BE CREATED**
  - Acceptance: each finding is fixed, suppressed with justification, or accepted
  - No new security scan failures

## C. Known Product Defects

- [ ] Fix duplicate messages during self-send
  - Issue: **TO BE CREATED**
  - Acceptance: zero duplicate deliveries for self-addressed messages
  - Tests: `internal/coremail/delivery`
- [ ] Correct inbound/local-delivery classification in Outbound Queue UI
  - Issue: **TO BE CREATED**
  - Acceptance: Queue UI correctly distinguishes inbound from outbound
  - Tests: UI acceptance or API response validation

## D. Push Notifications Completion

- [ ] Complete Web Push subscription flow end-to-end
  - Issue: **TO BE CREATED**
  - Acceptance: subscriber receives push notification for new mail
  - Tests: `internal/coremail/push`
- [ ] Verify VAPID key provisioning and delivery
  - Issue: **TO BE CREATED**
  - Acceptance: VAPID keys provisioned by installer, used by push
  - Tests: `internal/config` (installer VAPID), `internal/coremail/push`

## E. Installer and Upgrade Validation

- [ ] Run final installer gate (`release/install.sh`)
  - Fresh install on clean Linux VM
  - Verify admin account creation and first login
- [ ] Run upgrade gate (`release/install.sh` or `orvix update`)
  - Upgrade from previous RC to current
  - Verify all data preserved
- [ ] Verify operator-edited config preserved on re-run

## F. Backup and Restore Validation

- [ ] Run backup gate (`orvix backup`)
  - Full backup completes without error
  - Backup file verifiable
- [ ] Run restore gate (`orvix restore`)
  - Restore completes without error
  - All data intact after restore
- [ ] Verify backup encryption key stable across restarts

## G. Doctor and Monitoring Validation

- [ ] Run doctor gate (`orvix doctor`)
  - All health checks pass
  - No false positives or false negatives
- [ ] Verify monitoring alerts fire correctly
- [ ] Verify webhook delivery

## H. Linux and PostgreSQL Verification

- [ ] Run full Linux `go test ./...` with PostgreSQL
  - All tests pass
  - PostgreSQL Readiness workflow green
- [ ] Run `go test -race ./...` on Linux (CGO enabled)
  - No race conditions detected in security-critical packages
  - `internal/auth/...`, `internal/api/handlers/...`

## I. Staging

- [ ] Deploy commit `e7f5441` to staging
  - Issue: **TO BE CREATED**
  - Acceptance: staging runs same artifact as main HEAD
- [ ] Run staging acceptance tests
- [ ] Verify admin, webmail, and JMAP on staging

## J. Closed Beta

- [ ] Recruit closed beta participants
- [ ] Provision beta tenant(s)
- [ ] Collect beta feedback
- [ ] Address blocking beta issues

## K. Production Release

- [ ] Obtain production deployment approval
- [ ] Create release tag (not yet v1.0.0)
- [ ] Deploy to production with backup
- [ ] Run post-deployment smoke tests
- [ ] Verify DNS propagation

## L. Post-Release Monitoring

- [ ] Monitor error rates for 48 hours
- [ ] Monitor SMTP delivery success rate
- [ ] Monitor admin panel accessibility
- [ ] Verify backup scheduled and running
