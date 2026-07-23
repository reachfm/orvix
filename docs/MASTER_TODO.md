# ORVIX Master TODO

Completed items stay visible forever — never delete them, only check them off.

---

# Security

- [x] CSRF protection (group-level middleware on all mutation routes)
- [x] IDOR — mailbox/domain cross-tenant isolation (`internal/api/handlers/tenant_isolation_test.go`)
- [x] IDOR — customer alias/group cross-tenant isolation (`customer_mail.go`, commit `9bee80e`, this session)
- [x] RBAC permission model (role → permission map, `internal/auth/rbac`)
- [x] Stalwart zero-tolerance removal — zero runtime/config/doc/comment references remain outside protective tests (commit `d5a48cb`, this session)
- [ ] Fix `requireTenantActive` querying nonexistent `organizations` table (`router.go:890`) — currently causes every `/enterprise` mutation to misbehave (403 or panic-500)
- [ ] Real RFC 6238 MFA hardening review (currently TOTP/SHA1 — flagged as gosec false-positive/by-design in a prior audit pass, revisit if compliance requires SHA256+)
- [ ] WebAuthn / FIDO2 support
- [ ] SSO (OIDC/SAML) admin API — `s_s_o_configs` table exists, no endpoints wired
- [ ] Verify `enterpriseRead.Get("/organizations/:id")` (`router.go:937`) enforces `:id == caller tenant` at the service layer, not just via tenant-scoped middleware (flagged, not confirmed safe)
- [ ] Confirm the legacy vacation-reply path (`internal/coremail/storage/rules.go:97,103`) is truly unreachable, or remove it

# Database / Schema

- [ ] Create migration for `organizations` table, or repoint `requireTenantActive` at `tenants` (four options exist; pick one, document in `docs/DECISIONS.md`)
- [ ] Create migration for `coremail_groups` (customer Groups feature is non-functional without it)
- [ ] Create migration for `coremail_group_members`
- [ ] Create migration for `queue_attempts`, or repoint `admin_queue.go:205` at the existing `coremail_delivery_attempts` table if that was the intended target
- [x] Dual SQLite/PostgreSQL dialect abstraction (`internal/dbdialect`)

# Backend Routing

- [ ] Fix `GET /admin/mailboxes` wired to `ListUsers` instead of a mailbox handler (`router.go:1011`)
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
- [ ] Groups feature — blocked on missing schema (see Database/Schema above)
- [ ] Multi-tenant write UI (backend model exists, no admin write API — documented as intentionally hidden in `docs/ORVIX_ENTERPRISE_PARITY_AUDIT.md`)

# Testing

- [x] Tenant-isolation regression tests for mailboxes/domains
- [x] Tenant-isolation regression tests for aliases/groups (this session)
- [ ] Regression tests for DKIM/SPF/DMARC beyond the single existing test file each
- [ ] End-to-end test for the `requireTenantActive` bug once fixed (currently untestable through the full router)

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
