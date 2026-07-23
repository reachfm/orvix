# ORVIX Roadmap

Every item references the real source files it touches. See `docs/MASTER_TODO.md` for the full checklist this roadmap is prioritized from.

---

## Immediate (release-blocking or actively broken)

**All five items below were fixed 2026-07-23 — see `docs/DECISIONS.md` for full detail. Kept here, checked off, per this roadmap's own convention of never deleting completed work.**

1. [x] **Fix `requireTenantActive` querying nonexistent `organizations` table.**
   Files: `internal/api/router.go`. Repointed at `tenants`; also fixed a co-located GORM `.Raw().Scan()` nil-pointer panic. Verified via `TestRequireTenantActive_ActiveTenantSucceeds`/`InactiveTenantRejected`/`MissingTenantRejectedSafely`.

2. [x] **Create schema for `coremail_groups` / `coremail_group_members`.**
   Files: `internal/models/models.go`, `internal/models/postgres_migrations.go`. Also fixed a latent `ListGroups` column/scan-count mismatch bug surfaced by the schema now existing. Verified via `TestEnterpriseGroups_FullRouterRoundTrip`.

3. [x] **`queue_attempts` — repointed at `coremail_delivery_attempts` instead of creating a new table.**
   Files: `internal/api/handlers/admin_queue.go`, `cmd/orvix/main.go`. Confirmed via grep that nothing writes to `queue_attempts`; the real table already existed with unused DDL, now wired into the SQLite bootstrap path too.

4. [x] **Fix `/mailboxes` copy-paste routing bug.**
   Files: `internal/api/router.go`, `internal/api/handlers/handlers.go`. New `ListMailboxes` handler added, tenant-scoped, wired in place of `ListUsers`. Verified via `TestAdminMailboxesRoute_ReturnsMailboxesNotUsers`.

5. [x] **`organizations` — resolved as part of item 1** (same root cause, same fix).

---

## Newly Discovered (surfaced while fixing the items above)

6. **`ExportMailboxesCSV` has no tenant scoping.** A tenant admin can export every tenant's mailboxes via CSV — discovered while building the correctly-scoped `ListMailboxes`. Files: `internal/api/handlers/handlers.go`.

---

## Next (near-term hardening)

7. [x] **Fix `ExportMailboxesCSV` missing tenant scoping.** — **DONE**: `ExportMailboxesCSV` and the adjacent `ExportDomainsCSV` now scope by tenant via `isSuperRole`/`scopedTenantID`, regression-tested in `mailbox_export_isolation_test.go`. Files: `internal/api/handlers/handlers.go`. See `docs/DECISIONS.md`.

7b. [x] **Fix `ListDomains` missing tenant scoping.** — **DONE**: `GET /api/v1/domains` now scopes non-super callers via `isSuperRole`/`scopedTenantID`, applied ahead of the existing `q`/`status` filters; regression-tested in `domain_list_isolation_test.go` (12 tests). See `docs/DECISIONS.md`.

7c. **Sweep sibling admin-group list handlers for the same class** (e.g. `ListUsers` at `GET /api/v1/users`) — not yet traced or verified; the pattern has now been confirmed twice (exports, `ListDomains`) in this same handler group, so a dedicated sweep is warranted.

8. **Verify and, if needed, fix `enterpriseRead.Get("/organizations/:id")` tenant-scoping at the service layer.**
   Files: `internal/api/router.go:937`, `internal/admin/organization/repository.go`.

9. **Verify `web/webmail/EmailList.tsx`'s `/api/v1/queue` call is intentional vs. a stale reference to `/api/v1/webmail/messages`.**
   Files: `web/webmail/src/components/EmailList.tsx:28`, `internal/api/router.go:757`.

10. **Increase test depth for DKIM/SPF/DMARC/antispam/push/rules subpackages.**
    Files: `internal/coremail/{dkim,spf,dmarc,antispam,push,rules}` — each currently has only 1 test file.

11. **Replace `context.TODO()` in POP3 server with request-scoped context.**
    Files: `internal/coremail/pop3/server.go:244,256`.

12. **Confirm and remove the deprecated legacy vacation-reply path if truly unreachable.**
    Files: `internal/coremail/storage/rules.go:97,103`.

13. **Split the two largest handler files.**
    Files: `internal/api/handlers/handlers.go` (now larger still after this session's additions), `internal/api/handlers/webmail_user.go` (2535 lines).

14. **Verify orphaned packages before deletion.**
    Files: `internal/adminapi/`, `internal/ldap/`, `internal/pgbackup/` — each has zero references outside itself; confirm truly dead (not just unwired-but-planned) before removing.

---

## Future (feature completeness)

13. **WebAuthn / FIDO2 support** for admin and customer authentication, alongside existing TOTP MFA.

14. **SSO (OIDC/SAML) admin API.** The `s_s_o_configs` table and `SSOConfig` model already exist; no endpoints are wired.

15. **Customer-facing Contacts API.** Only admin-scoped contacts exist today; no `/api/v1/webmail/contacts`-style route.

16. **Calendar completion.** Backend module and FullCalendar frontend integration are partial.

17. **Consolidate the three-generation enterprise-admin handler cluster** (`enterprise_admin.go`, `enterprise_admin_v3.go`, `enterprise_admin_ssl.go`, `enterprise_parity.go`) into a single coherent set, once confirmed no dead code remains among them.

18. **Consolidate the three overlapping licensing packages** (`internal/license/`, `internal/licensing/`, `internal/licensingauthority/`) or document why three are genuinely needed.

---

## Enterprise (larger, longer-horizon)

19. **Multi-tenant write UI** — the `tenants` table and reseller model already exist; there is deliberately no admin write API yet (documented as out-of-scope in `docs/ORVIX_ENTERPRISE_PARITY_AUDIT.md` §3.1).

20. **Reseller hierarchy / reseller portal.** `resellers` table exists, no admin API.

21. **Multi-node clustering / read replicas / sharded stores.** Currently honest single-node-only UI (`release/admin/modules/pages/storage-topology.js`, `clustering.js`) — explicitly not faked, per the engineering guardrails in `docs/ORVIX_ENTERPRISE_PARITY_AUDIT.md` §4.

22. **OpenTelemetry export.** Counters exist (`internal/observability/metrics.go`); no OTLP exporter wired.

23. **SMTPS (port 465) listener implementation.** Config plumbing exists (`internal/coremail/runtime/module.go:372-378`); listener itself was never implemented, only a startup warning.

---

*Companion documents: `docs/MASTER_TODO.md` (checklist form), `docs/DECISIONS.md` (why), `docs/FEATURE_MATRIX.md` (current status), `docs/PROJECT_AUDIT_REPORT.md` (scorecard).*
