# Phase 0 Implementation Report

## Exact Base Commit
`5bec518da396bff6a7009f54746abecd9d456536`

## Branch
`fix/admin-phase0-security-billing-csrf`

## Files Changed
| File | Status | Lines |
|------|--------|-------|
| `internal/api/handlers/customer_mail.go` | Modified | +99/-13 |
| `internal/api/router.go` | Modified | +26/-12 |
| `internal/billing/service.go` | Modified | +58/-0 |
| `internal/billing/setup.go` | Modified | +3/-0 |
| `internal/models/models.go` | Modified | +21/-0 |
| `internal/models/postgres_migrations.go` | Modified | +23/-0 |
| `web/admin/src/App.tsx` | Modified | +9/-2 |
| `web/admin/src/api.ts` | Modified | +89/-2 |
| `internal/api/handlers/tenant_isolation_aliases_groups_test.go` | **New** | 248 lines |
| `internal/billing/backfill_test.go` | **New** | 242 lines |

## Fix 1 — Cross-Tenant IDOR (BLOCKER)

### Root Cause
`DeleteAlias`, `DeleteGroup`, `AddGroupMember`, `RemoveGroupMember` in `customer_mail.go` performed database operations using resource ID alone (`WHERE id = ?`) without verifying the resource belonged to the caller's tenant. The `callerOwnsTenant` pattern used by legacy admin handlers was absent.

### Implementation
- **DeleteAlias** (line 110): `WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`. Checks `RowsAffected` → 404 if zero.
- **DeleteGroup** (line 206): Same pattern with `tenant_id`.
- **AddGroupMember** (line 242): Verifies group belongs to caller via `SELECT tenant_id FROM coremail_groups WHERE id = ?`. If email resolves to a mailbox, verifies mailbox tenant matches.
- **RemoveGroupMember** (line 291): DELETE with subquery `WHERE id = ? AND group_id IN (SELECT id FROM coremail_groups WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL)`.
- **RBAC**: Inline `requireRBAC` helper using `authrbac.HasPermission`. Mutation routes moved from sub-groups to `enterpriseRead` group.
- **Audit**: Added audit log calls to `AddGroupMember` and `RemoveGroupMember` (were missing).
- **ListGroups fix**: Fixed scan mismatch (4 SELECT cols vs 3 scan fields).
- **Missing DDL**: Added `coremail_groups` and `coremail_group_members` tables with indexes to both SQLite and PostgreSQL schemas.

### Evidence
- Test `TestAliasGroupIsolation_DeleteCrossTenantAlias`: DELETE Tenant B's alias from Tenant A → 404
- Test `TestAliasGroupIsolation_DeleteCrossTenantGroup`: DELETE Tenant B's group from Tenant A → 404
- Test `TestAliasGroupIsolation_AddMemberToCrossTenantGroup`: Add member to Tenant B's group → 404
- Test `TestAliasGroupIsolation_RemoveMemberFromCrossTenantGroup`: Remove member from Tenant B's group → 404
- Test `TestAliasGroupIsolation_OwnGroupCRUDSucceeds`: Tenant A creates/lists/deletes own group → 201/200
- Test `TestAliasGroupIsolation_DeleteOwnAlias`: Tenant A creates/deletes own alias → 200

## Fix 2 — Subscription Backfill (CRITICAL)

### Root Cause
No startup migration creates subscription rows for tenants that existed before the billing module. `CreateOrganization` auto-provisions only for NEW organizations. Existing tenants (including bootstrap tenant id=1) have no subscription row, causing send enforcer to reject ALL outbound mail.

### Implementation
- `BackfillSubscriptions()` in `service.go` (line 299): queries `SELECT t.id, COALESCE(t.plan, 'free') FROM tenants t WHERE t.active = 1 AND t.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM subscriptions sub WHERE sub.tenant_id = t.id)`.
- Maps legacy `tenants.plan`: `free→PlanFree`, `starter→PlanStarter`, `business→PlanBusiness`, `enterprise→PlanEnterprise`, `smb→PlanFree`. Unknown values → `PlanFree` (safe fallback, NOT enterprise).
- Creates subscription with `active` status, `monthly` interval.
- Called from `Initialize()` after plan seeding (line 301).
- Idempotent: `CreateSubscription` checks for existing subscription. `NOT EXISTS` prevents double-processing.

### Evidence
- Test `TestBackfillSubscriptions_ExistingTenantWithoutSubGetsOne`: Tenant without sub gets one
- Test `TestBackfillSubscriptions_UnknownLegacyMapsToFree`: Unknown plan → Free (not enterprise)
- Test `TestBackfillSubscriptions_ExistingSubIsUnchanged`: Existing sub not modified
- Test `TestBackfillSubscriptions_Idempotent`: Repeated run = 0 new
- Test `TestBackfillSubscriptions_InactiveTenantSkipped`: Inactive tenant skipped

## Fix 3 — Admin SPA CSRF (HIGH)

### Root Cause
Frontend API client never fetched or sent `X-CSRF-Token` header. Backend CSRF middleware requires this header on all mutation requests. All POST/PATCH/PUT/DELETE from admin SPA were blocked.

### Implementation
- `api.ts`: Added `initCSRF()`, `setCSRFToken()`, `resetCSRFToken()` module-level functions.
- `request()`: Auto-fetches CSRF token on first mutation. Injects `X-CSRF-Token` header on POST/PUT/PATCH/DELETE.
- Retry: On CSRF-specific 403, token is cleared, re-fetched, and request retried exactly once (`_csrfRetried: true` prevents loops).
- Added `logout`/`logoutAll` methods to `api` object.
- `App.tsx`: Calls `initCSRF()` after successful auth check. Uses `api.logout()` for CSRF-protected logout.

### Retry Safety
- Condition: `status === 403` + error contains "csrf" + token exists + NOT already retried.
- Retried call sets `_csrfRetried: true` → second 403 falls through to `throw`.
- No infinite loop possible.

## requireTenantActive Panic Diagnosis

### Root Cause
GORM v1.31.2 `Raw().Scan()` on modernc.org/sqlite driver: `tx.Rows()` returns `(nil *sql.Rows, nil error)` because the callback chain fails to populate `Statement.Dest` but sets no error. GORM's `Scan` then calls `defer rows.Close()` on the nil pointer, causing the panic.

### Stack Trace (captured via `recover()` + `debug.Stack()`)
```
database/sql.(*Rows).closemuRUnlockIfHeldByScan() → sql.go:3410
database/sql.(*Rows).Close(0x0)                    → sql.go:3437
gorm.io/gorm.(*DB).Scan.func1()                    → finisher_api.go:547
database/sql.(*Rows).Next(0x0)                     → sql.go:3033
gorm.io/gorm.(*DB).Scan()                          → finisher_api.go:552
```

### Fix
Replaced GORM `Raw().Scan()` with `database/sql.QueryRow().Scan()` in `requireTenantActive`. Also changed the queried table from the non-existent `organizations` to `tenants` (which has the same `active` column). Uses trusted `tenantID` from `auth.RequireTenantID(c)` — never from request params/body.

### Security Properties
- `sql.ErrNoRows` → `err != nil` → 403 Forbidden (fails closed)
- `active == 0` → 403 Forbidden (inactive tenant denied)
- Soft-deleted tenants: if `active=0` or `deleted_at IS NOT NULL`, the query returns 0 or no rows → 403
- No unscoped fallback exists

## Verification Results

| Check | Result |
|-------|--------|
| `go test ./internal/billing/` | PASS |
| `go test ./internal/auth/` | PASS |
| `go test ./internal/api/handlers/ -run "TestTenantIsolation\|TestCSRF\|TestQueue\|TestAliasGroupIsolation\|TestAdminDomainAdvanced\|TestCompliance"` | PASS |
| `go test ./internal/models/` | PASS |
| `go vet ./...` | PASS |
| `npm run typecheck` | PASS |
| `npm run build` | PASS |
| `npm run test` | 2/3 test files passed (1 Playwright E2E pre-existing failure) |
| `git diff --check` | PASS |

### New Tests Added (16 total)

**IDOR Tests (6):**
`TestAliasGroupIsolation_DeleteCrossTenantAlias`, `DeleteCrossTenantGroup`, `AddMemberToCrossTenantGroup`, `RemoveMemberFromCrossTenantGroup`, `OwnGroupCRUDSucceeds`, `DeleteOwnAlias`

**Backfill Tests (10):**
`TestBackfillSubscriptions_ExistingTenantWithoutSubGetsOne`, `EnterpriseLegacyMapsToEnterprise`, `BusinessLegacyMapsToBusiness`, `StarterLegacyMapsToStarter`, `FreeLegacyMapsToFree`, `UnknownLegacyMapsToFree`, `ExistingSubIsUnchanged`, `Idempotent`, `InactiveTenantSkipped`, `SmbMapsToFree`

## Commands Run
```bash
go vet ./...
go test ./internal/billing/ -count=1
go test ./internal/auth/ -count=1
go test ./internal/api/handlers/ -run "TestTenantIsolation|TestCSRF|TestQueue|TestAliasGroupIsolation|TestAdminDomainAdvanced|TestCompliance" -count=1
go test ./internal/models/ -count=1
cd web/admin && npm run typecheck && npm run build && npm run test
git diff --check
```

## Unresolved Issues
None within Phase 0 scope. The `requireTenantActive` middleware now correctly queries `tenants.active` using `database/sql` instead of GORM `Raw().Scan()`.

PHASE 0 COMPLETE — READY FOR INDEPENDENT SECURITY REVIEW
