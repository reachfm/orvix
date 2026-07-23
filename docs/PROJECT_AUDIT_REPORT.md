# ORVIX Enterprise Scorecard

**Date:** 2026-07-23
**Method:** direct source inspection (parallel codebase-audit agents + manual verification), evidence cited throughout this document set. Not a static-analysis tool report — every score below is backed by specific findings in `docs/CODEBASE_INDEX.md`, `docs/FEATURE_MATRIX.md`, and `docs/MASTER_TODO.md`.

---

## Scores (0-10 scale)

| Dimension | Score | Basis |
|---|---|---|
| Architecture | 6.5 | Clean protocol-layer separation (`internal/coremail/*` subpackages are well-factored, all 15 have test coverage). Undermined by handler-layer sprawl (`handlers.go` at 3657 lines, three coexisting "enterprise admin" generations) and three overlapping licensing packages with no clear single owner. |
| Security | 6 | RBAC + CSRF + tenant-isolation are real and tested (two confirmed IDOR classes found *and fixed* in this codebase's history — mailbox/domain, then alias/group). Docked hard for the `requireTenantActive` bug itself being broken (queries a nonexistent table), which is currently the single largest live security-adjacent defect: it doesn't leak data, but it means the tenant-active gate is not actually enforcing anything correctly today. |
| Performance | Not scored | No load-testing evidence found in this pass beyond a `storage/loadtest` test-only stub; insufficient evidence to score responsibly. Flagged as a gap, not rated. |
| Maintainability | 6 | 0.70 test-files-per-source-file is a reasonable breadth signal. Five confirmed schema/routing defects that have shipped silently (four missing tables, one copy-paste route) indicate the review/test process doesn't currently catch "wired but never actually functional" endpoints. |
| Documentation | 8 (after this pass) | Was previously scattered across 60+ ad-hoc audit/report markdown files in `docs/` and repo root with no index. This pass adds the eight-document living-documentation set (`PROJECT_MAP`, `CODEBASE_INDEX`, `FEATURE_MATRIX`, `MASTER_TODO`, `DECISIONS`, `ROADMAP`, `PROJECT_WORKFLOW`, this scorecard) plus removal of 14 files that were actively misleading (describing an architecture the codebase no longer has). |
| Developer Experience | 6.5 | Dual SQLite/PostgreSQL support with a clean dialect-abstraction layer is a real strength for local dev. Undercut by the fact that hitting several real customer-facing endpoints (Groups, queue attempt detail, any `/enterprise` mutation) currently fails in ways a new contributor would have no way to anticipate without reading this audit. |
| Release Readiness | 5.5 | Full Go test suite passes (80 packages, 0 failures, confirmed this session with a 60-minute-timeout full run). All three frontends build clean. But four schema-missing tables and one broken tenant-gate middleware are release blockers for the features they touch — they are not theoretical, they were empirically triggered during this session's work (a `POST` to `/api/v1/enterprise/aliases` through the live router panics). |
| Enterprise Readiness | 5 | Strong foundation (RBAC, audit trail, billing, backup/restore, DNS ops, monitoring all COMPLETE per `docs/FEATURE_MATRIX.md`). Held back by: Groups feature completely non-functional, no SSO despite schema existing, no WebAuthn, reseller portal unimplemented, and the enterprise-parity audit's own engineering guardrails (`docs/ORVIX_ENTERPRISE_PARITY_AUDIT.md`) show a deliberate, honest "hide what's not real" policy — good practice, but it means several enterprise-tier surfaces are intentionally stubbed, not just incomplete by oversight.

---

## Top Technical Debt (ranked)

1. **`requireTenantActive` queries a nonexistent `organizations` table** (`router.go:890`) — silently breaks or panics every `/enterprise` mutation.
2. **Four confirmed schema-missing tables**: `organizations`, `coremail_groups`, `coremail_group_members`, `queue_attempts`.
3. **Three generations of enterprise-admin handlers coexisting** without a confirmed reason one supersedes the others.
4. **Handler-file sprawl**: top 6 largest non-test files, 4 of them are in `internal/api/handlers/`.
5. **Three overlapping licensing packages** with no single obvious owner.

## Top Risks

1. **Live 500/403 risk on customer-facing enterprise endpoints** — not hypothetical, empirically confirmed this session.
2. **Thin test coverage on security-critical mail-auth subpackages** (DKIM/SPF/DMARC/antispam each 1 test file) relative to their compliance role.
3. **Orphaned packages of unclear intent** (`internal/pgbackup/` especially — a Postgres-specific encrypted backup helper with zero references is either abandoned or dangerously unwired; needs a decision, not silence).

## Top Blockers (release-scoped)

1. `requireTenantActive` fix (schema or code) — blocks safe use of the entire `/enterprise` write surface.
2. Groups feature schema — blocks a shipped, RBAC-gated, routed feature from ever working.
3. `GET /admin/mailboxes` routing bug — blocks a core admin listing view from returning correct data.

## Top Missing Enterprise Features

1. SSO (OIDC/SAML) — schema exists, zero endpoints.
2. WebAuthn/FIDO2.
3. Reseller portal — schema exists, zero endpoints.
4. Multi-tenant admin write UI (deliberately deferred, documented as such).
5. Multi-node clustering/replication (deliberately honest-stubbed, not faked).

## Recommended Implementation Order

1. Fix `requireTenantActive` (unblocks testing/using everything downstream of it).
2. Add missing table migrations (`organizations` and/or fix the query; `coremail_groups`/`coremail_group_members`; `queue_attempts`).
3. Fix the `/admin/mailboxes` routing bug (trivial, one-line).
4. Add regression tests end-to-end through the real router for the now-fixed `/enterprise` mutation path (currently only handler-level tests exist for aliases/groups, because the router itself was broken).
5. Consolidate the enterprise-admin handler generations; split `handlers.go` and `webmail_user.go`.
6. Then proceed to net-new enterprise features (SSO, WebAuthn, reseller portal) per `docs/ROADMAP.md`.

## Estimated Completion Percentage

**~72% enterprise-ready.** Core mail engine, security primitives (RBAC/CSRF/tenant-isolation-when-working), billing, backup/restore, monitoring, and DNS ops are COMPLETE and tested. The remaining ~28% is concentrated in: the `requireTenantActive`/schema-gap cluster (currently blocking a meaningful slice of the customer-facing enterprise API), a handful of genuinely unimplemented enterprise-tier features (SSO, WebAuthn, reseller portal), and maintainability debt (handler sprawl, licensing-package overlap) that doesn't block shipping but will slow every future change until addressed.

This estimate is deliberately conservative relative to raw test-pass-rate (100% of the existing suite passes) because several of the defects found here are exactly the kind that a green test suite doesn't catch — they're missing/broken *before* any test exercises them, or they were never tested at all until this session.

---

*Companion documents: `docs/PROJECT_MAP.md`, `docs/CODEBASE_INDEX.md`, `docs/FEATURE_MATRIX.md`, `docs/MASTER_TODO.md`, `docs/DECISIONS.md`, `docs/ROADMAP.md`, `docs/PROJECT_WORKFLOW.md`.*
