# TECHNICAL DEBT REPORT

## Orvix Enterprise Mail (OrvixEM)

**Date:** 2026-06-05
**Status:** Greenfield — zero code exists
**Reference:** MVP Full Document

---

## 1. Current State

There is zero technical debt in the codebase because there is zero code.

This report identifies potential future technical debt that must be proactively avoided during implementation.

---

## 2. Proactive Technical Debt Prevention

### 2.1 Architecture-Level Prevention

| Potential Debt | Prevention Strategy | MVP Reference |
|---------------|---------------------|---------------|
| Stalwart coupling too tight | Abstract all Stalwart interactions behind `internal/stalwart/` interfaces; never import Stalwart concepts directly in other packages | Lines 999–1006 |
| License logic spread across codebase | Centralize all license/feature checks in `internal/flags/`; never inline tier checks | Lines 1070–1071 |
| Config scattered across packages | Single config system via `internal/config/` reading `orvix.yaml`; no hardcoded values | Line 1068 |
| Database schema directly accessed from UI | Always go through `internal/api/` — no SQL/ORM in frontend code | Line 1065 |
| Frontend logic duplication across 3 apps | Shared component library in `web/` for common components | Lines 1074–1077 |

### 2.2 Code-Level Prevention

| Practice | Enforcement |
|----------|-------------|
| No global state | All state explicitly passed through dependency injection |
| Interface-based design | All major systems (Stalwart, storage, DNS, AI) behind interfaces |
| Error wrapping | All errors wrapped with context (Go 1.20+ `%w` / `fmt.Errorf`) |
| No `any`/`interface{}` | Strict typing everywhere |
| Test coverage floor | 80% minimum coverage for business logic |
| Linting | `golangci-lint` in CI with strict rules |
| No TODO comments in committed code | CI blocks PRs with unlinked TODO comments |
| Documentation alongside code | Go doc comments on all exported symbols |
| No hardcoded secrets | All secrets in environment variables or encrypted config |
| Frontend TypeScript strict mode | No `any` types in React code |

### 2.3 Database-Level Prevention

| Potential Debt | Prevention |
|---------------|------------|
| Migration bloat | Additive-only policy; prune old migrations only in major releases |
| Schema drift | GORM auto-migrate for development; explicit migration files for production |
| Slow queries | All queries reviewed; indexes added with migrations; EXPLAIN ANALYZE in CI |
| Missing foreign keys | All relationships defined in GORM models |
| No audit trail | Append-only audit_logs table; never UPDATE or DELETE audit entries |

### 2.4 Frontend-Level Prevention

| Potential Debt | Prevention |
|---------------|------------|
| Component duplication | Share via `web/` common components library |
| State management spaghetti | Zustand stores per domain; no global store |
| Missing error states | Every data-fetching query has loading/error/empty states |
| CSS bloat | Tailwind utility classes; no custom CSS except design tokens |
| Bundle size | Route-level code splitting; lazy load heavy components (calendar, charts) |
| No accessibility | Radix UI provides WCAG compliance; audit with axe-core |

---

## 3. MVP-Required Quality Standards

The MVP defines quality standards that prevent debt:

| Standard | MVP Reference |
|----------|---------------|
| Additive-only migrations | Lines 1244–1277 |
| Zero-downtime updates | Line 533 |
| Snapshot before mutation | Lines 1154–1161 |
| Auto-rollback on failure | Lines 1186–1188 |
| All errors explain cause AND fix | Line 363 |
| Keyboard-first design | Line 322 |
| WCAG 2.1 AA minimum | Line 324 |
| Optimistic UI (no loading spinners) | Line 323 |
| Immutable audit log | Line 294 |

---

## 4. Technical Debt Tracking

### 4.1 Known Upcoming Decisions That Could Create Debt

| Decision Point | Risk | Recommended Action |
|---------------|------|-------------------|
| SQLite vs PostgreSQL feature parity | Some SQL features differ | Test both; limit SQLite to compatible subset |
| Bleve vs PostgreSQL search | Maintaining two search backends | Benchmark early; consider PostgreSQL-only for scale |
| Redis dependency for job queue | Production Redis cluster complexity | Consider embedded queue for single-server deployments |
| Stalwart version compatibility | Supporting multiple Stalwart versions | Maintain compatibility matrix; CI tests per version |
| Frontend React apps shared vs separate | Mono-repo complexity | Single repo with shared package; Vite workspace |

### 4.2 Technical Debt Budget

| Category | Acceptable | Unacceptable |
|----------|------------|--------------|
| Test coverage | >80% business logic | <50% overall |
| Lint warnings | 0 | Any |
| TypeScript strict errors | 0 | Any |
| Security vulnerabilities | 0 critical/high | Any critical |
| Dead code | 0 | Any |
| Manual testing required | <10% of features | >30% of features |
| Documentation coverage | 100% exported Go symbols | Missing public docs |

---

## 5. Measurement

Technical debt will be measured monthly via:

1. **SonarQube** or **CodeClimate** — overall quality gate
2. **Go vet** / **staticcheck** — zero warnings target
3. **npm audit** — zero critical/high vulnerabilities
4. **Go test -cover** — coverage report
5. **Lighthouse** — frontend performance/accessibility

---

## 6. Current Debt Score

| Metric | Current | Target |
|--------|---------|--------|
| Codebase size | 0 lines | ~80k lines (estimated) |
| Test coverage | N/A | >80% |
| Lint violations | 0 | 0 |
| Security vulns | 0 | 0 |
| TODO/FIXME count | 0 | 0 |
| Documentation | N/A | 100% |

---

**Conclusion:** At project start, there is zero technical debt. The patterns defined in the MVP — additive-only migrations, interface-based integration, safe updates, and quality gates — are strong preventive measures. The primary risk is accumulating debt during the aggressive 18-week schedule, which must be mitigated by enforcing code review, test coverage, and linting from day one.
