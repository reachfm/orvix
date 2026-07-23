# ORVIX Master TODO

Completed items stay visible forever — never delete them, only check them off.

---

# Security

- [x] CSRF protection (group-level middleware on all mutation routes)
- [x] IDOR — mailbox/domain cross-tenant isolation (`internal/api/handlers/tenant_isolation_test.go`)
- [x] IDOR — customer alias/group cross-tenant isolation (`customer_mail.go`, commit `9bee80e`, this session)
- [x] RBAC permission model (role → permission map, `internal/auth/rbac`)
- [x] Stalwart zero-tolerance removal — zero runtime/config/doc/comment references remain outside protective tests (commit `d5a48cb`, this session)
- [x] Fix `requireTenantActive` querying nonexistent `organizations` table (`router.go:890`) — repointed at `tenants`, also fixed a GORM `.Raw().Scan()` nil-pointer panic found during the fix (see `docs/DECISIONS.md`)
- [ ] Real RFC 6238 MFA hardening review (currently TOTP/SHA1 — flagged as gosec false-positive/by-design in a prior audit pass, revisit if compliance requires SHA256+)
- [ ] WebAuthn / FIDO2 support
- [ ] SSO (OIDC/SAML) admin API — `s_s_o_configs` table exists, no endpoints wired
- [ ] Verify `enterpriseRead.Get("/organizations/:id")` (`router.go:937`) enforces `:id == caller tenant` at the service layer, not just via tenant-scoped middleware (flagged, not confirmed safe)
- [ ] Confirm the legacy vacation-reply path (`internal/coremail/storage/rules.go:97,103`) is truly unreachable, or remove it
- [x] IDOR — `ExportMailboxesCSV` tenant isolation: a tenant admin could export every tenant's mailboxes via `GET /api/v1/mailboxes/export`. **CONFIRMED and FIXED** — added `isSuperRole`/`scopedTenantID` scoping; regression-tested in `mailbox_export_isolation_test.go` (proven to fail against pre-fix code). See `docs/DECISIONS.md`.
- [x] IDOR — `ExportDomainsCSV` tenant isolation (adjacent handler, same defect): **CONFIRMED and FIXED** in the same pass, same helper, same test file.
- [x] IDOR — `ListDomains` (`internal/api/handlers/handlers.go:1038`, backs `GET /api/v1/domains`) had the same missing-tenant-scope defect as the export handlers. **CONFIRMED and FIXED** — added `isSuperRole`/`scopedTenantID` scoping ahead of the existing `q`/`status` filters; regression-tested in `domain_list_isolation_test.go` (12 tests, proven to fail against pre-fix code — 6 isolation assertions failed, 6 filter/auth assertions correctly still passed). See `docs/DECISIONS.md`.
- [ ] Audit remaining `admin`-group *list* handlers for the same class (e.g. `ListUsers`) — the export-security pass proved the pattern exists in this group; a focused list-handler sweep is warranted but was out of scope here.

# Database / Schema

- [x] `organizations` table issue resolved — `requireTenantActive` repointed at `tenants` (see `docs/DECISIONS.md`), no new table created
- [x] Migration added for `coremail_groups` (both SQLite `models.go` and PostgreSQL `postgres_migrations.go`)
- [x] Migration added for `coremail_group_members`
- [x] `queue_attempts` resolved — confirmed dead (zero writers anywhere), repointed `admin_queue.go` at the real `coremail_delivery_attempts` table instead, and wired its previously-uncalled DDL into the SQLite bootstrap path
- [x] Dual SQLite/PostgreSQL dialect abstraction (`internal/dbdialect`)
- [x] `ExportMailboxesCSV` (`internal/api/handlers/handlers.go`) had no tenant scoping — **FIXED** this pass (see the two IDOR entries in the Security section above and `docs/DECISIONS.md`).

# Backend Routing

- [x] Fix `GET /mailboxes` (the "admin" group has an empty URL prefix — not literally `/admin/mailboxes`, corrected understanding this pass) wired to `ListUsers` instead of a mailbox handler — new `ListMailboxes` handler added and wired, tenant-scoped, tested
- [ ] Verify `web/webmail/EmailList.tsx` calling `/api/v1/queue` instead of `/api/v1/webmail/messages` is intentional, not a stale reference

# Mail Protocols

- [x] SMTP inbound (port 25)
- [x] Submission (port 587)
- [ ] SMTPS (port 465) — config exists, listener never implemented (`internal/coremail/runtime/module.go:372-378`)
- [x] IMAP / IMAPS
- [x] POP3 / POP3S
- [x] JMAP
- [x] DKIM signing/verification
- [x] SPF enforcement
- [x] DMARC enforcement
- [ ] Increase test depth for DKIM/SPF/DMARC/antispam/push/rules (each has only 1 test file — thin relative to security/compliance role)
- [ ] Replace `context.TODO()` with request-scoped context in `internal/coremail/pop3/server.go:244,256`

# Webmail

- [x] Attachments
- [x] Drafts
- [x] Forwarding
- [x] Push notifications (VAPID)
- [ ] Calendar (partial — FullCalendar wired, backend module partial)
- [ ] Contacts (no customer-facing API found; only admin-scoped contacts exist)
- [ ] Vacation responder — remove deprecated legacy path once confirmed unreachable

# Admin Panel

- [x] Vite-based React SPA (replaces legacy vanilla-JS console)
- [x] Legacy vanilla-JS fallback preserved intentionally for minimal-toolchain builds
- [ ] Consolidate three generations of enterprise-admin handler files (`enterprise_admin.go`, `enterprise_admin_v3.go`, `enterprise_admin_ssl.go`, `enterprise_parity.go`) — confirm no dead code among them
- [ ] Split `internal/api/handlers/handlers.go` (3657 lines) into cohesive sub-files
- [ ] Split `internal/api/handlers/webmail_user.go` (2535 lines)

# Customer Panel

- [x] Self-service domains, mailboxes (read), aliases (full CRUD, tenant-isolated)
- [x] Groups feature — schema added, `ListGroups` scan-count bug fixed, full router round-trip tested (`TestEnterpriseGroups_FullRouterRoundTrip`)
- [ ] Multi-tenant write UI (backend model exists, no admin write API — documented as intentionally hidden in `docs/ORVIX_ENTERPRISE_PARITY_AUDIT.md`)

# Testing

- [x] Tenant-isolation regression tests for mailboxes/domains
- [x] Tenant-isolation regression tests for aliases/groups (this session)
- [x] End-to-end tests for `requireTenantActive` through the full router (active/inactive/missing-tenant, `enterprise_mutation_smoke_test.go`)
- [x] Full-router regression test for the Groups feature (create/list/duplicate-rejection)
- [x] Regression test proving `/mailboxes` returns mailbox-shaped data, not user-shaped, with tenant scoping
- [ ] Regression tests for DKIM/SPF/DMARC beyond the single existing test file each

# Dead Code / Cleanup (needs verification before deletion, per non-negotiable rule)

- [ ] Confirm `internal/adminapi/` is safe to delete (build-tag gated, zero references outside itself)
- [ ] Confirm `internal/ldap/` is safe to delete (zero references)
- [ ] Confirm `internal/pgbackup/` is safe to delete or determine if it should be wired in (zero references currently — unclear if abandoned or unfinished)
- [ ] Confirm `internal/releasebundle/` and `internal/storage/loadtest/` are test-only by design (likely fine to keep as-is, not true dead code)

# Documentation

- [x] `docs/PROJECT_MAP.md`
- [x] `docs/CODEBASE_INDEX.md`
- [x] `docs/FEATURE_MATRIX.md`
- [x] `docs/MASTER_TODO.md` (this file)
- [x] `docs/DECISIONS.md`
- [x] `docs/ROADMAP.md`
- [x] `docs/PROJECT_WORKFLOW.md`
- [x] `docs/PROJECT_AUDIT_REPORT.md`
- [x] Removed 14 obsolete Stalwart-era documentation files (`d5a48cb`)
