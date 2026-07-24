# ORVIX Project Map

**Purpose:** orientation document — what each major directory is for, how it's wired into the running binary, and how risky it is to touch. Evidence-based; built from direct source inspection, not assumption.

**Runtime entrypoint:** `cmd/orvix/main.go`. Startup order: logger → `config.Load` → `config.NewDatabase` → raw-SQL migrations (`migrateConfiguredDatabase`) → `seedFeatureFlags` → `modules.NewRegistry` → `orvixruntime.NewListenerRegistry` → `license.NewFeatureFlags` → `auth.NewAuthenticator` → `seedAdminUser` → `registerModules` (coremail runtime, firewall, autoheal, dns, migration, guardian, compose, updater, provision, calendar, collaboration, compliance, intelligence) → `reg.InitAll`/`StartAll` → `api.NewRouter` → `router.Start()`. Single Fiber app binds one port (`cfg.Server.Host:AdminPort`, default 8080) in one of three TLS modes (autocert / static cert / plain HTTP).

---

## Backend (`internal/`)

| Directory | Purpose | Wired in main/router | Risk if modified |
|---|---|---|---|
| `coremail/` (151 files) | The mail engine itself: SMTP, Submission, IMAP, POP3, JMAP, DKIM/SPF/DMARC, queue, delivery, storage, antispam, rules, runtime wiring. **The only mail engine in this repo.** | Yes — `main.go:277` via `coremailruntime.New`, `router.go:32-35` | **HIGH** — touches live mail delivery for every tenant |
| `api/` (114 files) | Fiber router (`router.go`) + all HTTP handlers (`handlers/`). Every REST endpoint lives here. | Yes — `api.NewRouter` at `main.go:207` | **HIGH** — auth/tenant-isolation bugs here are IDOR-class |
| `auth/` (20 files) | Authenticator, RBAC permission model, tenant context helpers, MFA, security monitor. | Yes — `main.go:185` | **HIGH** — security-critical |
| `models/` (7 files) | GORM structs + raw-SQL `CREATE TABLE` migrations (`postgres_migrations.go`, ~65 tables). | Yes — `main.go:315-320` | **HIGH** — schema drift breaks the app silently (see Codebase Index for confirmed gaps) |
| `admin/` (21 files, 5 subpackages: dashboard/domain/mailbox/organization/platform) | Admin-scoped service layer backing `/api/v1/admin/*` and `/api/v1/enterprise/*` reads. | Yes — `router.go:19-23` | MEDIUM |
| `billing/` (20 files) | Subscription/plan/invoice logic, Stripe-style webhook handling. | Yes — `router.go:30` | MEDIUM-HIGH — money-adjacent |
| `audit/` (5 files) | Audit-log writer used by nearly every mutation handler. | Yes — `router.go:27` | MEDIUM — silent failures here reduce forensic trail, not availability |
| `config/` (16 files) | Config loading, DB/Redis connection setup, logger construction. | Yes — `main.go:24` | HIGH — misconfiguration is a startup-blocking risk |
| `dnsops/`, `dnsverify/`, `domainregistry/` | DNS provider integrations, domain ownership verification. | Yes (dnsops via router.go:38-39); dnsverify/domainregistry used transitively | MEDIUM |
| `firewall/`, `guardian/`, `autoheal/` | Mail firewall rules, AI threat analysis, self-healing checks — each a `modules.Module`. | Yes — `main.go:280,284,281` | MEDIUM |
| `updater/` (12 files) | Self-update / release-channel logic for the binary. | Yes — `main.go:38,286` | HIGH — a bad update path can brick a production install |
| `backup/` (16 files) | Backup subsystem (unix-only build tag). | Indirect (28 refs elsewhere, not directly in main) | HIGH — data-loss risk if broken |
| `pgbackup/` | PostgreSQL-specific encrypted backup helper. | **Orphaned — zero external references found** | LOW to touch, but confirm it's truly unused before deleting (see Master TODO) |
| `adminapi/` | Legacy standalone admin HTTP server. | **Orphaned — `//go:build legacy_adminapi` gated, no references outside itself** | LOW to touch; dead code candidate |
| `ldap/`, `releasebundle/`, `storage/loadtest/` | LDAP sync (1 file), release-bundle test helper, load-test storage stub. | **Orphaned / test-only, zero external references** | LOW — dead-code candidates |
| `enterprise/rbac/` | Enterprise-tier RBAC extensions. | Yes — `router.go:40` | MEDIUM |
| `licensing/`, `licensingauthority/`, `license/` | Three related-but-distinct licensing packages (feature flags, tier authority, licensing service). | All actively referenced elsewhere (25-40 refs each) despite no direct main.go wiring for two of them | MEDIUM — naming overlap is a maintainability smell (see Codebase Index) |
| `monitoring/`, `observability/`, `metrics/` | Health snapshots, counters, alert dispatch. | observability/metrics wired via router.go:42,44; monitoring used transitively | LOW-MEDIUM |
| `queuemgmt/`, `messagetrace/`, `mailboxmgmt/` | Admin-facing management wrappers around coremail's queue/mailbox primitives. | Used transitively (27-39 refs each) | MEDIUM |
| `policy/`, `policymgmt/` | Policy engine (105 refs — heavily used) + a thinner policy-management wrapper. | Transitive | MEDIUM |
| `runtime/`, `runtimecontrol/` | Listener registry (shared telemetry state) + runtime control surface. | Yes — `main.go:37,180` | MEDIUM |
| `restorecoord/` | Boundary between the privileged `restore-run` CLI subcommand and the API. | Used by the `restore-run` path | HIGH — restore is inherently destructive if misused |

---

## Frontend (`web/`)

| Workspace | Framework | Size | Talks to |
|---|---|---|---|
| `web/admin` | React 19 + Vite + TanStack Query + Radix UI + Tailwind 4 | 38 source files | `/api/v1/*` via a shared `BASE` constant (`api.ts:1`) |
| `web/webmail` | React 19 + Vite + TanStack Query/Virtual + Tiptap + FullCalendar + i18next | 8 source files | Hard-coded literal paths, no shared base constant — **`EmailList.tsx` calls `/api/v1/queue`, not `/api/v1/webmail/messages`**, a likely stale-endpoint bug (see Codebase Index) |
| `web/marketing` | React 19 + react-router-dom 7 + Vite, with prerender/sitemap/SEO build pipeline | 52 source files | No live backend API dependency; a `ContactForm.tsx` doc comment references `/api/v1/contact`, which does not exist as a route — aspirational, not wired |

Legacy fallback: `release/admin/` (vanilla JS, `modules/` + `app.js`) is **not loaded in production** (the Vite SPA replaces it) but is intentionally preserved as a build-toolchain-unavailable fallback (`release/scripts/lib-admin-build.sh`).

---

## API Surface Map (`internal/api/router.go`, 1538 lines)

| Route group | Gate | Approx. endpoints | Notes |
|---|---|---|---|
| Public (no auth) | none, or login-rate-limited | ~15 | MTA-STS, Autodiscover, health, billing plans/webhook, auth login/signup/reset, admin/webmail login |
| `protected` (base) | API key or JWT + `TenantMiddleware` | ~46 | `/me`, `/account/*`, `/webmail/*` user endpoints, `/customer/domains/*` |
| `/api/v1/enterprise/*` | tenant context + CSRF + per-capability RBAC permission | ~45 | Most internally consistent group — fine-grained write guards |
| `/api/v1/admin/*` (+ unprefixed admin group, mounted with an empty URL prefix directly under `/api/v1`) | role-based (`Admin`/`SuperAdmin`/`PlatformSuperAdmin`) + CSRF at group level | ~180 | Largest, most heterogeneous group; the `GET /mailboxes` copy-paste routing bug is fixed this session (see Codebase Index) |
| `/api/v1/console/internal/*` | `SuperAdmin`/`PlatformSuperAdmin` only | 5 | Correctly tighter than parent admin group |
| `/api/v1/platform/*` | `SuperAdmin`/`PlatformSuperAdmin` only | 7 | Organization CRUD at platform level |

---

## Database Schema Map

- **GORM auto-migrated structs** (`internal/models/models.go`): Common, License, FeatureFlag, ModuleVersion, Tenant, Reseller, LDAPConfig, SSOConfig, AlertConfig, FirewallRule/Log, GuardianLog, HealHistory, ProvisionedDomain, Session, UpdateHistory, User, Domain, Mailbox, APIKey.
- **Raw-SQL migrations** (`internal/models/postgres_migrations.go`, ~65 tables): core account tables, coremail mail-store tables, RBAC/admin-group tables, enterprise v2/v3 feature tables, security tables, ops tables.
- **Self-managed schema** (own `EnsureSchema` inside their own package): `admin_settings`, `password_reset_tokens` — not gaps, just decentralized.
- **Previously schema-missing tables — fixed 2026-07-23**: `organizations` (resolved by repointing `requireTenantActive` at `tenants`, no new table), `coremail_groups` and `coremail_group_members` (migrations added to both SQLite and PostgreSQL paths), `queue_attempts` (resolved by repointing the one reader at the real `coremail_delivery_attempts` table instead of creating a speculative new one). See `docs/DECISIONS.md` for full reasoning and `docs/CODEBASE_INDEX.md` for verification evidence.

---

## Runtime Flow (request lifecycle, typical customer-facing mutation)

1. Request hits the single Fiber app on the bound port.
2. `apiRateLimitMiddleware` → auth middleware (JWT or API key) → `TenantMiddleware` (resolves `tenant_id` into `c.Locals`) → route-group-specific gate (RBAC permission or role) → (for enterprise mutations) `requireTenantActive` → CSRF middleware → handler.
3. Handler calls into a service/package under `internal/` (coremail, billing, admin, etc.), which talks to Postgres/SQLite via `*gorm.DB` or raw `database/sql`.
4. Mutations typically call `h.writeAuditLog(...)` before returning.

---

*Companion documents: `docs/CODEBASE_INDEX.md` (file-level detail), `docs/FEATURE_MATRIX.md` (feature-level status), `docs/MASTER_TODO.md` (actionable checklist), `docs/PROJECT_AUDIT_REPORT.md` (scorecard).*
