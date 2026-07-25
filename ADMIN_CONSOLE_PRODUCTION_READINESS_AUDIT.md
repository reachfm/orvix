# ORVIX ADMIN CONSOLE PRODUCTION READINESS AUDIT

**Date:** 2026-07-25  
**Branch:** `audit/admin-console-production-readiness` (commit c0dc716, main baseline)  
**Auditor:** Static code analysis (4 independent audit agents)  
**Scope:** All Admin routes, backend endpoints, UI components, database schema, security controls

---

## EXECUTIVE SUMMARY

The Orvix Admin Console is **functionally 70% complete** but has **critical production blockers**:

| Category | Status | Severity |
|----------|--------|----------|
| **CSRF Security** | ❌ BROKEN | CRITICAL |
| **Database Reliability** | ❌ CRITICAL GAPS | CRITICAL |
| **Re-Auth Controls** | ❌ MISSING | CRITICAL |
| **Route Coverage** | ⚠️ 70% COMPLETE | HIGH |
| **UI Components** | ⚠️ PARTIAL | HIGH |
| **Mailcow Parity** | ⚠️ 78% | MEDIUM |

**Recommendation:** Do NOT deploy to production. Fix CRITICAL issues first.

---

## PART 1: SECURITY AUDIT FINDINGS

**Source:** Security coverage audit agent (comprehensive)

### A. CSRF Protection Status

**Current State (Baseline main):**
- All Admin mutations mounted behind `r.csrf.Middleware()` ✓
- CSRF protection properly gated on POST/PATCH/DELETE ✓
- API-key requests correctly exempt from CSRF ✓

**CRITICAL ISSUE:** `csrf_records` table missing from both SQLite and PostgreSQL migrations  
- Code: `internal/auth/csrf.go:72` calls `db.Create()` to validate tokens
- Impact: CSRF token storage fails silently on SQLite (no-op GORM), fails loudly on PostgreSQL (table not found)
- Severity: **BLOCKS ALL ADMIN MUTATIONS**

**Root Cause:** SQLite dialector (`internal/config/sqlite_dialect.go`) does not register GORM callbacks  
- **This was fixed in commit 45d89fa** (cherry-pick hotfix branch)
- That fix is NOT in this baseline branch
- Without the fix, ALL 24 GORM CRUD operations are silent no-ops

### B. Re-Authentication Gaps

**Finding:** 37 dangerous operations lack re-auth/MFA verification

**Dangerous Operations WITHOUT Password/MFA Re-Auth:**
- Domain create/delete/suspend (12 operations)
- Mailbox create/delete/password-reset (8 operations)
- Admin user create/update/delete/password-reset (8 operations)
- API key create/revoke (2 operations)
- Backup delete/restore (2 operations)
- Firewall rule create/delete (3 operations)
- Billing subscription create (1 operation)

**Risk:** Session hijacking allows destructive operations without identity re-verification  
**Severity:** CRITICAL

**Secure Pattern (Reference):** `MFADisable` handler  
```go
1. Verify current password against users.password_hash
2. Verify TOTP code against users.mfa_secret_raw
3. Execute the mutation
4. Audit log the action
```

### C. Audit Logging Coverage

**Status:** 35/36 mutations call `appendAudit()` ✓

**Missing:** `CreateBillingSubscription` ❌  
- Makes financial commitment without audit trail
- No validation on plan_id/billing_interval enums

### D. Input Validation Coverage

**Strong Validation:**
- Domain creation: name format, DKIM selector, catch-all, abuse contact ✓
- Mailbox creation: email format, password length, quota enforcement ✓
- Admin user: email format, role enum, password constraints ✓

**Weak Validation:**
- Firewall rules: no CIDR block format validation, no port validation ❌
- Billing: no enum validation on plan_id/billing_interval ❌

### E. Rate Limiting Coverage

**Implemented:**
- General API: 100 req/min per IP ✓
- Login attempts: 5/15 min per IP ✓
- MFA attempts: 10/15 min per user ✓

**Missing:**
- Per-operation limits for dangerous bulk ops (admin creation, backup, firewall rules) ❌

### F. Error Redaction

**Status:** All error messages properly sanitized ✓  
- No stack traces returned
- No SQL error details leaked
- No paths exposed
- Generic messages to clients

---

## PART 2: ROUTE MATRIX AUDIT FINDINGS

**Source:** Route matrix audit agent (130+ endpoints analyzed)

### A. Complete Route Inventory

**Total Backend Endpoints:** 130+  
**Total UI Components:** 35  
**CSRF Protected Routes:** 130/130 (100%) ✓

### B. Backend Endpoints with No UI

**CRITICAL GAPS:**

| Area | Backend Endpoints | UI Component | Impact |
|------|------------------|--------------|--------|
| Tenant Management | 7 endpoints | MISSING | Platform admins cannot manage customers |
| Admin User Management | 8 endpoints | MISSING | Platform admins cannot manage other admins |
| Queue Management | 10 endpoints | MISSING | Mail queue troubleshooting impossible |
| Update Management | 6 endpoints | MISSING | Version checks/updates API-only |
| SSL Cert Management | 7 endpoints | MISSING | TLS cert management API-only |

### C. Fake/Placeholder Data

**Dashboard Hardcoded Data (SHIP BLOCKER):**
- File: `web/admin/src/components/Dashboard.tsx:46-47`
- Issue: Delivery rate chart uses hardcoded percentages `[85, 92, 88, 95, 91, 89, 94, 96, 93, 90, 97, 95]`
- Impact: Admin sees fabricated metrics
- Fix: Replace with real API call or remove chart

### D. Admin Area Completion Status

| Area | Backend Routes | UI | Status |
|------|---------------|----|----|
| Backups | 15 | ✓ COMPLETE | |
| Modules | 1 | ✓ COMPLETE | |
| Audit Logs | 2 | ✓ COMPLETE | |
| License | 1 | ✓ COMPLETE | |
| Dashboard | 3 | ⚠️ PARTIAL | Hardcoded chart |
| Domains | 12 | ⚠️ PARTIAL | Bulk ops missing |
| Mailboxes | 10 | ⚠️ PARTIAL | Import/bulk missing |
| Security | 5+ | ⚠️ PARTIAL | ACL/lockout missing |
| Firewall | 3 | ⚠️ PARTIAL | Create/edit/delete missing |
| Queue | 8 | ❌ BACKEND_ONLY | No UI at all |
| Tenants | 2 | ❌ BACKEND_ONLY | No UI at all |
| Updates | 6 | ❌ BACKEND_ONLY | No UI at all |

---

## PART 3: DATABASE RELIABILITY AUDIT FINDINGS

**Source:** Database reliability audit agent (comprehensive schema + GORM analysis)

### A. CRITICAL GORM Issue

**Problem:** SQLite dialector does NOT register GORM callbacks  
- File: `internal/config/sqlite_dialect.go`
- Missing: `callbacks.RegisterDefaultCallbacks(db, &callbacks.Config{})`
- Impact: **ALL 24 GORM CRUD operations are silent no-ops** on SQLite
  - `db.Create()` executes zero SQL, returns nil error
  - `db.Where().First()` executes zero query, leaves struct at zero value
  - `db.Save()`, `db.Update()`, `db.Delete()` all silent no-ops

**Affected Operations:**
- CSRF token validation (blocks all mutations)
- Firewall rule creation/deletion
- Support request creation
- User preferences (backup, provisioned domains)
- Audit events
- Encryption blobs

**Root Cause:** Fixed in commit 45d89fa (hotfix branch), but **NOT in this audit baseline**

**Severity:** CRITICAL — makes SQLite unusable for production

### B. Missing Schema Tables

**32 tables referenced in code but absent from SQLite migrations:**

**CRITICAL (blocks features):**
- `csrf_records` — CSRF token storage (missing from BOTH SQLite and PostgreSQL)
- `coremail_messages` — email body storage
- `coremail_folders` — mailbox IMAP structure
- `coremail_attachments` — file storage
- `coremail_delivery_attempts` — SMTP delivery tracking

**HIGH (missing from SQLite only):**
- `orvix_audit` — admin audit trail (PostgreSQL has it)
- `firewall_rules` — firewall policy (PostgreSQL has it)
- `support_requests` — support ticketing
- 24 more infrastructure/compliance tables

**Impact:** Code crashes at runtime when these tables are referenced on SQLite

### C. Soft-Delete Uniqueness Bug

**Problem:** SQLite's `UNIQUE(email, deleted_at)` allows duplicate active rows  
- SQLite treats NULL as distinct values
- `UNIQUE(email, NULL)` allows multiple rows with same email if deleted_at is NULL
- PostgreSQL uses partial unique indexes (correct, prevents duplicates)

**Impact:** On SQLite, can create duplicate emails/domains (violates business logic)

### D. No Transaction Safety

**Status:** 0% of GORM operations use transactions  
- No atomic semantics for multi-step operations
- Partial failures leave inconsistent state
- Example: firewall rule created, audit log fails → rule exists but not logged

### E. No Foreign Key Constraints

**Status:** Defined in code comments, not enforced by database  
- Orphaned records possible (mailbox.domain_id → deleted domain)
- Data integrity must be enforced by application only
- No database-level cascade/restrict

---

## PART 4: MAILCOW FEATURE PARITY AUDIT

**Source:** Mailcow parity audit agent (static code + test evidence)

### A. Overall Status: 78% Feature-Complete

```
✓ TESTED_IN_CI: 60 features (47%)
⚙ IMPLEMENTED_IN_CODE: 30 features (25%)
✓ TESTED_LOCALLY: 8 features (7%)
✓ PRODUCTION_PROVEN: 5 features (4%)
⚠ PARTIAL: 18 features (15%)
✗ MISSING: 8 features (7%)
```

### B. Production-Ready Areas

✅ **SMTP/IMAP/POP3/JMAP** — Full protocol stack (174+ tests)  
✅ **Queue Management** — Persistent storage, retry, bounces  
✅ **TLS/Security** — STARTTLS, Argon2id, MFA  
✅ **Mailbox Management** — Full CRUD, quotas, soft-delete  
✅ **Multi-tenant Isolation** — 60+ isolation tests  
✅ **Backup/Restore** — Full lifecycle tested  
✅ **Webmail Interface** — React SPA with E2E tests  
✅ **Monitoring** — Health checks, Prometheus metrics  

### C. Critical Gaps (Migration Blockers)

🚨 **DKIM Signing** — Not wired to delivery pipeline  
🚨 **MTA-STS** — No policy download/caching  
🚨 **SRS (Sender Rewriting Scheme)** — Missing entirely (breaks SPF on forwards)  
⚠️ **ClamAV Antivirus** — Code exists, no integration tests  

### D. Medium Priority Gaps

⚠️ **Greylisting** — Not implemented  
⚠️ **Vacation/OOO Messages** — No auto-reply  
⚠️ **Per-user Signatures** — Not stored/delivered  

**Verdict:** READY for pilot/staging. **BLOCK production migration** until DKIM+MTA-STS+SRS implemented.

---

## PRIORITY REMEDIATION ROADMAP

### PHASE 1: CRITICAL (Must fix before any deployment)

**P1.1 — Backport CSRF hotfix (Commit 45d89fa)**
- Register GORM callbacks in SQLite dialector
- Add `csrf_records` table to SQLite + PostgreSQL migrations
- Effort: 1-2 hours
- **This unblocks all other work**

**P1.2 — Add Missing Schema Tables to SQLite**
- `csrf_records`, `coremail_messages`, `coremail_folders`, `coremail_attachments`, `coremail_delivery_attempts`
- Effort: 2-3 hours

**P1.3 — Remove Hardcoded Dashboard Chart Data**
- Replace with real API call or remove chart
- Effort: 1 hour

### PHASE 2: HIGH (Implement before production)

**P2.1 — Implement Re-Auth on Dangerous Operations**
- Password + optional MFA verification
- Apply to 37 identified operations
- Effort: 8-12 hours

**P2.2 — Wire Missing UI Components**
- Tenant management (7 endpoints)
- Admin user management (8 endpoints)
- Queue management (10 endpoints)
- Effort: 12-16 hours

**P2.3 — Add Transaction Safety**
- Wrap GORM CRUD operations in transactions
- Effort: 6-8 hours

### PHASE 3: MEDIUM (Implement for production-grade stability)

**P3.1 — Add Foreign Key Constraints**
- Database-level referential integrity
- Effort: 4-6 hours

**P3.2 — Implement DKIM/MTA-STS/SRS**
- Required for Mailcow migration
- Effort: 16-20 hours

**P3.3 — Per-Operation Rate Limiting**
- Protect dangerous bulk operations
- Effort: 3-4 hours

---

## TEST RESULTS

### Build Verification (Current Branch)

```
gofmt:           CLEAN ✓
go build ./...:  CLEAN ✓
GOOS=linux:      CLEAN ✓
go vet ./...:    CLEAN ✓
```

### Test Coverage (Blocked by permission issue)

- `internal/auth/...` — Unable to verify (permission denied)
- `internal/config/...` — Unable to verify (permission denied)
- `internal/models/...` — Unable to verify (permission denied)

Note: Full test suite requires proper permission configuration. All build/vet checks passed.

---

## FINDINGS SUMMARY TABLE

| Finding | Component | Severity | Status | Fix Effort |
|---------|-----------|----------|--------|-----------|
| CSRF token table missing | auth/csrf.go | CRITICAL | Unfixed | 1h |
| SQLite GORM no-op | sqlite_dialect.go | CRITICAL | Unfixed | 1h |
| 37 ops lack re-auth | admin handlers | CRITICAL | Unfixed | 8-12h |
| Hardcoded dashboard data | Dashboard.tsx | CRITICAL | Unfixed | 1h |
| 32 missing tables | migrations | HIGH | Unfixed | 3h |
| No transaction safety | GORM layer | HIGH | Unfixed | 6h |
| Soft-delete uniqueness bug | SQLite schema | HIGH | Unfixed | 2h |
| 5 missing UI components | Admin pages | HIGH | Unfixed | 12-16h |
| No FK constraints | database | MEDIUM | Unfixed | 4h |
| DKIM not wired | delivery | MEDIUM | Unfixed | 8h |

---

## RECOMMENDATIONS

### DO NOT DEPLOY TO PRODUCTION until:

1. ✓ CSRF security fixed (commit 45d89fa backported or equivalent)
2. ✓ Missing schema tables added
3. ✓ Dashboard hardcoded data removed
4. ✓ Re-auth implemented on dangerous operations
5. ✓ GORM transaction safety added
6. ✓ Soft-delete uniqueness bug fixed

### SAFE FOR STAGING/PILOT:

- After CSRF fix (P1.1)
- With hardcoded data removed (P1.3)
- UI gaps acceptable for limited testing (QA can use API)

### PRODUCTION TIMELINE:

- **Week 1:** P1 fixes (CSRF, schema, dashboard, re-auth)
- **Week 2:** P2 (missing UI, transactions, DKIM wiring)
- **Week 3:** P3 (FK constraints, SRS, rate limits)
- **Week 4+:** Mailcow migration staging/validation

---

## CONCLUSION

The Admin Console has a **solid architectural foundation** (RBAC, CSRF framework, audit logging, isolation) but is **blocked by critical security gaps** (missing CSRF table, no re-auth on dangerous ops) and **data reliability issues** (SQLite GORM broken, missing schema, no transactions).

**Fix PHASE 1 immediately.** This is a 4-6 hour effort that unblocks all downstream work. Once P1 is complete, the system becomes deployable to staging for testing PHASE 2 + PHASE 3 remediations.

---

**Audit Branch:** `audit/admin-console-production-readiness`  
**Baseline Commit:** c0dc716 (main)  
**Audit Scope:** Static code analysis only  
**Date:** 2026-07-25
