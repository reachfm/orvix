# Portal Separation — Phase 1 Deployment Runbook

## Product decisions (fixed)

1. `admin@orvix.email` is the **Platform Super Admin** — no customer tenant.
2. The Company Admin for the `orvix.email` tenant is a **separate identity**,
   provisioned in a later deployment. Phase 1 does not create it or set a
   password.
3. Platform privilege is **never** inferred from email domain — only from the
   `role` and `tenant_id` columns on `users`.
4. No permanent dual-role account.
5. Impersonation is deferred to Phase 2+.
6. An empty Company Admin sees the full tenant console with an onboarding-first
   empty state.
7. Break-glass is a future root-only CLI (see `break-glass-recovery.md`) — no
   live CLI in Phase 1.
8. `/admin/platform/*` and `/admin/organization/*` are two shells on one
   hostname.
9. `/admin` is the role-derived landing route.
10. Separate hostnames / bundles / cookies / JWT audiences are deferred to
    Phase 2.
11. External-backup components stay installed but disabled and untouched.

## What Phase 1 changes

- `internal/auth/rbac/rbac.go` — the deprecated `RoleAdmin` permission map is
  emptied. Any un-normalized "admin" row has zero permissions.
- `internal/api/router.go` — the top-level `/admin/*` group gate no longer
  admits the deprecated `RoleAdmin`. It now admits
  `RolePlatformSuperAdmin`, `RoleSuperAdmin`, and `RoleTenantAdmin`.
- `cmd/orvix/main.go` — bootstrap inserts the platform admin with
  `role = 'platform_super_admin'` and `tenant_id = NULL`.
- `internal/models/models.go` — `NormalizeAdminRoles(ctx, db, dialect)` runs
  at startup on both SQLite and Postgres paths (idempotent).
- `internal/api/handlers/handlers.go` — `/me` returns a server-derived
  `portal` field (`"platform"` | `"organization"` | `""`) plus an optional
  `organization` object when the user is tenant-scoped.
- `internal/api/handlers/admin_queue.go` — the mail-queue admin gate is
  restricted to platform super admins (was too permissive).
- `web/admin/src/App.tsx` — the `isPlatformRole` shell gate now reads
  `me.portal` instead of guessing from the role string.

## Post-install ops checklist (per install)

1. **Boot the service.** `NormalizeAdminRoles` runs automatically. Watch the
   startup log for:
   - `normalizeAdminRoles: promoted N bootstrap row(s) to platform_super_admin`
   - `normalizeAdminRoles: promoted N 'admin' row(s) with tenant_id to tenant_admin`
   - `ERROR AMBIGUOUS_ADMIN_ROLE …` — an operator must decide the fate of
     these rows. They have no permissions until fixed.
2. **Confirm the platform admin logs in** and sees the platform shell only.
3. **Provision a separate Company Admin** for the `orvix.email` tenant:
   ```sql
   INSERT INTO users (created_at, updated_at, email, password_hash, role,
                      tenant_id, active, email_verified)
   VALUES (now(), now(), '<company-admin@orvix.email>',
           '<argon2id-hash>', 'tenant_admin', <orvix-tenant-id>, true, true);
   ```
   This account is what internal staff use for tenant-facing operations.
4. **Do NOT keep any account with the deprecated `admin` string** in the
   users table. If normalization skipped rows (AMBIGUOUS_ADMIN_ROLE), decide:
   - Platform account → `UPDATE users SET role='platform_super_admin', tenant_id=NULL, token_version=token_version+1 WHERE id=<id>;`
   - Tenant account → `UPDATE users SET role='tenant_admin', tenant_id=<t>, token_version=token_version+1 WHERE id=<id>;`

## Rollback

The migration only rewrites the `role` and `tenant_id` columns and bumps
`token_version`. To roll back the router / RBAC / bootstrap changes:

1. `git revert` the six Phase-1 commits (see PR).
2. Restart the service — no schema changes to undo.
3. Live `role` values remain in their canonical form (`platform_super_admin`,
   `tenant_admin`, …). The old RBAC map still accepted these because
   `NormalizeRole` had already been added in an earlier release.
