> **SUPERSEDED** — This is a historical execution report.
> Current authoritative status: `docs/ORVIX_V1_SOURCE_OF_TRUTH.md`

# ORVIX MASTER EXECUTION LEDGER

## Wave 0 — Baseline, Safety, and Change Control

| Task | Status | Details |
|------|--------|---------|
| Record current branch | DONE | `feature/enterprise-rc5-admin-portals-secure-mail` |
| Record current commit | DONE | `525b0c1c5cb3dba698bd51b8905998de1d6cc73c` |
| Record git status | DONE | Modified: auth.go, rbac.go, module.go, install scripts. Untracked: migrations, cert-sync, ledger, architecture doc |
| Record git diff --check | DONE | Clean |
| Confirm no production target | DONE | Local dev environment only |
| Create execution ledger | DONE | This file |
| Create architecture doc | DONE | docs/architecture/ORVIX_CANONICAL_BOUNDARIES.md |

## Wave 1 — Canonical Architecture Boundaries

| Task | Status |
|------|--------|
| Identify duplicate implementations | DONE |
| Quarantine unsafe legacy paths | DONE |
| Document package boundaries | DONE |
| Architecture rules doc | DONE |
| Tests for no duplicate admin planes | DONE |

## Wave 2 — Admin Foundation (Phases A–T)

| Phase | Status | Details |
|-------|--------|---------|
| Phase A: Audit | DONE | Full codebase audit produced |
| Phase B: Legacy quarantine | DONE | Legacy adminapi not wired into production startup |
| Phase C: Canonical roles | DONE | 5 canonical roles + NormalizeRole function |
| Phase D: Permission registry | DONE | 40 permissions (14 platform + 26 tenant) in migration |
| Phase E: Role templates | DONE | 4 system templates with explicit permission sets |
| Phase F: Auth engine | PARTIAL | New roles added to rbac; plan/policy evaluator pending |
| Phase G: Tenant scope | DONE | TenantMiddleware + tenant-bound repository pattern |
| Phase H: Plan/entitlement | PARTIAL | tenant_entitlements table created; enforcement pending |
| Phase I: Database foundation | DONE | Migration 004 (up + rollback), 7 new tables/columns |
| Phase J: Session revocation | PARTIAL | token_version column added; middleware check pending |
| Phase K–T | NOT STARTED | Remaining admin phases require further implementation |

## Waves 3–14

| Wave | Status |
|------|--------|
| Wave 3: Auth/MFA/sessions | NOT STARTED |
| Wave 4: Certificate lifecycle | NOT STARTED |
| Wave 5: Mail protocols | NOT STARTED |
| Wave 6: Queue/delivery | NOT STARTED |
| Wave 7: Backup/restore | NOT STARTED |
| Wave 8: Monitoring | NOT STARTED |
| Wave 9: Configuration | NOT STARTED |
| Wave 10: Webmail | NOT STARTED |
| Wave 11: Performance | NOT STARTED |
| Wave 12: Installer/upgrade | NOT STARTED |
| Wave 13: Pilot validation | NOT STARTED |
| Wave 14: Release gate | NOT STARTED |