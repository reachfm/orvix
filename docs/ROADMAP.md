# ORVIX Roadmap

Every item references the real source files it touches. See `docs/MASTER_TODO.md` for the full checklist this roadmap is prioritized from.

---

## Immediate (release-blocking or actively broken)

1. **Fix `requireTenantActive` querying nonexistent `organizations` table.**
   Files: `internal/api/router.go:890`. Blocks every `/api/v1/enterprise/*` mutation (aliases, groups, domains, mailboxes, invitations, etc.) — either silently 403s or panics 500 depending on downstream code.

2. **Create schema for `coremail_groups` / `coremail_group_members`.**
   Files: needs a new entry in `internal/models/postgres_migrations.go` (or equivalent SQLite path). Currently the entire customer Groups feature (`internal/api/handlers/customer_mail.go`: `ListGroups`, `CreateGroup`, `DeleteGroup`, `AddGroupMember`, `RemoveGroupMember`) is non-functional in every environment.

3. **Create schema for `queue_attempts`, or repoint the handler at `coremail_delivery_attempts`.**
   Files: `internal/api/handlers/admin_queue.go:205`.

4. **Fix `GET /admin/mailboxes` copy-paste routing bug.**
   Files: `internal/api/router.go:1011` — currently calls `r.h.ListUsers` instead of a mailbox-listing handler.

5. **Create schema for `organizations`, or repoint at `tenants`.**
   Same root cause as item 1 — bundled here as the actual schema decision to make (see `docs/DECISIONS.md` for the two options).

---

## Next (near-term hardening)

6. **Verify and, if needed, fix `enterpriseRead.Get("/organizations/:id")` tenant-scoping at the service layer.**
   Files: `internal/api/router.go:937`, `internal/admin/organization/repository.go`.

7. **Verify `web/webmail/EmailList.tsx`'s `/api/v1/queue` call is intentional vs. a stale reference to `/api/v1/webmail/messages`.**
   Files: `web/webmail/src/components/EmailList.tsx:28`, `internal/api/router.go:757`.

8. **Increase test depth for DKIM/SPF/DMARC/antispam/push/rules subpackages.**
   Files: `internal/coremail/{dkim,spf,dmarc,antispam,push,rules}` — each currently has only 1 test file.

9. **Replace `context.TODO()` in POP3 server with request-scoped context.**
   Files: `internal/coremail/pop3/server.go:244,256`.

10. **Confirm and remove the deprecated legacy vacation-reply path if truly unreachable.**
    Files: `internal/coremail/storage/rules.go:97,103`.

11. **Split the two largest handler files.**
    Files: `internal/api/handlers/handlers.go` (3657 lines), `internal/api/handlers/webmail_user.go` (2535 lines).

12. **Verify orphaned packages before deletion.**
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
