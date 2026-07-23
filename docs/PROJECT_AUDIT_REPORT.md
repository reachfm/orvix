# ORVIX Enterprise Scorecard

**Date:** 2026-07-23
**Method:** direct source inspection (parallel codebase-audit agents + manual verification), evidence cited throughout this document set. Not a static-analysis tool report — every score below is backed by specific findings in `docs/CODEBASE_INDEX.md`, `docs/FEATURE_MATRIX.md`, and `docs/MASTER_TODO.md`.

---

## Scores (0-10 scale)

| Dimension | Score | Basis |
|---|---|---|
| Architecture | 6.5 | Clean protocol-layer separation (`internal/coremail/*` subpackages are well-factored, all 15 have test coverage). Undermined by handler-layer sprawl (`handlers.go` at 3657 lines, three coexisting "enterprise admin" generations) and three overlapping licensing packages with no clear single owner. |
| Security | 7.5 (up from 7) | RBAC + CSRF + tenant-isolation are real and increasingly well-tested. Confirmed IDOR classes found *and fixed* in this codebase's history: mailbox/domain read, alias/group, `requireTenantActive`, mailbox/domain **CSV export**, and now **`ListDomains`** (`GET /api/v1/domains`, 12 router-level regression tests this pass). Not raised further because `ListUsers` (`GET /api/v1/users`) — the same handler group, same suspected pattern — has not yet been traced or verified; it is neither confirmed safe nor confirmed vulnerable. Plus thin DKIM/SPF/DMARC test depth remains open. |
| Performance | Not scored | No load-testing evidence found in this pass beyond a `storage/loadtest` test-only stub; insufficient evidence to score responsibly. Flagged as a gap, not rated. |
| Maintainability | 6.5 (was 6) | 0.70 test-files-per-source-file is a reasonable breadth signal. The five confirmed schema/routing defects from the initial audit pass are now fixed and covered by 6 new regression tests, demonstrating the fix-and-test cycle works — but the underlying process gap that let them ship silently (four missing tables, one copy-paste route, and a `ListGroups` scan-count bug surfaced by the schema fix) is not itself addressed; a future defect of the same shape could ship the same way. |
| Documentation | 8.5 (was 8) | Was previously scattered across 60+ ad-hoc audit/report markdown files in `docs/` and repo root with no index. This pass adds the eight-document living-documentation set plus removal of 14 misleading legacy-architecture files, and now also documents five concrete architectural decisions from the stabilization fixes with full reasoning and evidence (`docs/DECISIONS.md`). |
| Developer Experience | 7.5 (was 6.5) | Dual SQLite/PostgreSQL support with a clean dialect-abstraction layer is a real strength for local dev. The specific endpoints that previously failed unpredictably (Groups, queue attempt detail, any `/enterprise` mutation) are now fixed and tested — a new contributor hitting these paths today gets correct behavior, not a silent 403/500. |
| Release Readiness | 7 (was 5.5) | Full Go test suite passes (80 packages, 0 failures) both before and after this session's stabilization fixes, including a dedicated re-run of `internal/api/handlers` after each change. All three frontends build clean. The four schema-missing tables and the broken tenant-gate middleware — previously real release blockers, empirically triggered during this session's earlier IDOR-fix work — are now fixed and covered by new tests. |
| Enterprise Readiness | 6 (was 5) | Strong foundation (RBAC, audit trail, billing, backup/restore, DNS ops, monitoring, and now Groups all COMPLETE per `docs/FEATURE_MATRIX.md`). Held back by: no SSO despite schema existing, no WebAuthn, reseller portal unimplemented, and the enterprise-parity audit's own engineering guardrails (`docs/ORVIX_ENTERPRISE_PARITY_AUDIT.md`) show a deliberate, honest "hide what's not real" policy — good practice, but it means several enterprise-tier surfaces are intentionally stubbed, not just incomplete by oversight.

---

## Top Technical Debt (ranked)

**Items 1-2 and the `/mailboxes` routing bug were fixed this session — see `docs/DECISIONS.md`. Kept here for historical visibility per this document set's "never delete completed work" convention; superseded items are marked.**

1. ~~`requireTenantActive` queries a nonexistent `organizations` table~~ — **FIXED**: repointed at `tenants`, plus a co-located GORM panic bug fixed.
2. ~~Four confirmed schema-missing tables~~ — **FIXED**: `organizations` resolved via item 1; `coremail_groups`/`coremail_group_members` migrated; `queue_attempts` resolved by repointing at the real `coremail_delivery_attempts` table.
3. **Three generations of enterprise-admin handlers coexisting** without a confirmed reason one supersedes the others. *(still open)*
4. **Handler-file sprawl**: `internal/api/handlers/handlers.go` grew further this session (new `ListMailboxes` handler added). *(still open — now a stronger case for the planned split)*
5. **Three overlapping licensing packages** with no single obvious owner. *(still open)*
6. ~~`ExportMailboxesCSV` has no tenant scoping~~ — **FIXED** this pass (mailbox + domain CSV export IDOR, regression-tested). Superseded by #7 below as the next open item of this class.
7. ~~`ListDomains` (`GET /api/v1/domains`) has no tenant scoping~~ — **FIXED** this pass (12 regression tests, `domain_list_isolation_test.go`). `ListUsers` remains untraced/unverified for the same class — see `docs/MASTER_TODO.md`.

## Top Risks

1. ~~Live 500/403 risk on customer-facing enterprise endpoints~~ — **FIXED and regression-tested** this session (`TestRequireTenantActive_*`, `TestEnterpriseGroups_FullRouterRoundTrip`).
2. **Thin test coverage on security-critical mail-auth subpackages** (DKIM/SPF/DMARC/antispam each 1 test file) relative to their compliance role. *(still open)*
3. **Orphaned packages of unclear intent** (`internal/pgbackup/` especially — a Postgres-specific encrypted backup helper with zero references is either abandoned or dangerously unwired; needs a decision, not silence). *(still open)*
4. ~~`ExportMailboxesCSV` tenant-scoping gap~~ — **FIXED and regression-tested** this pass (mailbox + domain CSV export).
5. ~~`ListDomains` tenant-scoping gap~~ — **FIXED and regression-tested** this pass. `ListUsers` (`GET /api/v1/users`) is the next candidate of this class but is unverified — not listed as a confirmed risk.

## Top Blockers (release-scoped)

**All three fixed this session:**

1. ~~`requireTenantActive` fix~~ — **DONE**.
2. ~~Groups feature schema~~ — **DONE**.
3. ~~`/mailboxes` routing bug~~ — **DONE**. (Corrected understanding: the live route is `/api/v1/mailboxes`, not `/api/v1/admin/mailboxes` — the "admin" router group has an empty URL prefix.)

**Next blocker in line:** none release-critical remain from this audit pass; see `docs/ROADMAP.md` "Next" section for near-term hardening items (none of which block a release on their own).

## Top Missing Enterprise Features

1. SSO (OIDC/SAML) — schema exists, zero endpoints.
2. WebAuthn/FIDO2.
3. Reseller portal — schema exists, zero endpoints.
4. Multi-tenant admin write UI (deliberately deferred, documented as such).
5. Multi-node clustering/replication (deliberately honest-stubbed, not faked).

## Recommended Implementation Order

**Items 1-4 below are done as of this session — kept for historical record.**

1. ~~Fix `requireTenantActive`~~ — **DONE**.
2. ~~Add missing table migrations~~ — **DONE** (`coremail_groups`/`coremail_group_members` migrated; `queue_attempts` resolved by repointing, not migrating).
3. ~~Fix the `/mailboxes` routing bug~~ — **DONE**.
4. ~~Add regression tests end-to-end through the real router~~ — **DONE** (`internal/api/handlers/enterprise_mutation_smoke_test.go`, 6 new tests, plus the pre-existing `TestOpsV2_MailboxFilters` regression caught and fixed in the same pass).
5. ~~Fix the `ExportMailboxesCSV` tenant-scoping gap~~ — **DONE** (mailbox + domain CSV export, `mailbox_export_isolation_test.go`).
5b. ~~Fix the `ListDomains` tenant-scoping gap~~ — **DONE** (`domain_list_isolation_test.go`). Next in this class: trace and, if warranted, fix `ListUsers` (`GET /api/v1/users`) — not yet verified either way.
6. Consolidate the enterprise-admin handler generations; split `handlers.go` and `webmail_user.go`.
7. Then proceed to net-new enterprise features (SSO, WebAuthn, reseller portal) per `docs/ROADMAP.md`.

## Estimated Completion Percentage

**~79% enterprise-ready (up from ~78%, small evidence-based increment).** Core mail engine, security primitives (RBAC/CSRF/tenant-isolation), billing, backup/restore, monitoring, DNS ops, the customer Groups feature, the `/enterprise` mutation gate, and — as of this pass — `ListDomains` are all COMPLETE and tested. This pass closed the `ListDomains` IDOR (12 regression tests, proven against pre-fix behavior) without surfacing a new confirmed-vulnerable sibling: `ListUsers` is flagged for a follow-up trace but is explicitly **unverified**, not counted as a known gap, so the increment reflects only what was actually confirmed and fixed. The remaining ~21% is concentrated in: unimplemented enterprise-tier features (SSO, WebAuthn, reseller portal), the still-unverified `ListUsers` sweep, and maintainability debt (handler sprawl, licensing-package overlap).

This estimate reflects evidence gathered by fixing and testing the previously-blocking issues, not optimism: every item marked DONE above has a corresponding passing regression test, and the full Go test suite (80+ packages) was re-run and confirmed green after each round of changes in this session, including a targeted re-run of `internal/api/handlers` specifically to catch the `TestOpsV2_MailboxFilters` regression before it could ship.

---

*Companion documents: `docs/PROJECT_MAP.md`, `docs/CODEBASE_INDEX.md`, `docs/FEATURE_MATRIX.md`, `docs/MASTER_TODO.md`, `docs/DECISIONS.md`, `docs/ROADMAP.md`, `docs/PROJECT_WORKFLOW.md`.*
