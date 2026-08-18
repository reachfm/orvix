# Organization Product Parity Matrix — Organization/Customer Admin Portal

> Author: Backend Architect (Enterprise Product Completion Pass, Phase 1)
> Branch: `work/enterprise-parity-ff78209` (worktree `D:\orvix-parity`)
> Starting SHA: `ff78209f54d08397e6995455920aca3779948711`
> Scope: Organization Admin portal (`portal="organization"`), every visible page and
> meaningful feature traced Frontend → API client → HTTP route → middleware/RBAC →
> handler → service/domain → repository/storage → real persisted/runtime behavior.
> Executed after the recent RBAC repair (canonical tenant role family:
> `tenant_admin`/`tenant_operator`/`tenant_support`/`tenant_readonly`; `RoleUser`
> and `RoleBilling` denied the entire console).
> This document is the **execution plan** for Phase 1 and the audit record handed to
> the API Platform Engineer and Frontend Developer.

## Disposition taxonomy (final, this pass)

Same taxonomy as `docs/PLATFORM_PRODUCT_PARITY_MATRIX.md` (COMPLETE, UI_GAP,
BACKEND_GAP, CONTRACT_DRIFT, RBAC_BUG, TENANT_SCOPE_BUG, FAKE_UI, STUB_BACKEND,
DEAD_UI, DIRECT_FETCH_BYPASS, MISSING_TEST, INTENTIONAL_READ_ONLY,
INTENTIONAL_MACHINE_ONLY, DEPRECATED, DUPLICATE_SUPERSEDED).

## Portal-wide route gate (verified once, applies to every row)

- All `/enterprise/*` routes are mounted on `enterpriseRead`:
  `auth.RequireAnyRole(tenant_admin, tenant_operator, tenant_support, tenant_readonly)`
  + `requireTenantContext` (tenant from JWT row, never from the request) + CSRF
  middleware + `requireTenantActive` (non-GET blocked when tenant `active=0` or
  `deleted_at` set). Writes additionally require the exact per-capability
  permission (`authrbac.Require(...)`): `domains.write`, `mailboxes.write`,
  `organizations.write`, `users.write`, `invitations.write`, `ownership.transfer`,
  `apikeys.write`, `billing.write`, `aliases.write`, `groups.write`,
  `imports.write`, `imports.execute`.
- CSRF: required on every non-GET (csrf.Middleware no-ops on GET/HEAD/OPTIONS and
  API-key-authenticated requests).
- Tenant isolation: every handler resolves `tenant_id` from the session and every
  repository query carries a `tenant_id=` predicate. Verified by
  `organization_admin_tenant_isolation_test.go`, `tenant_isolation_test.go`,
  `tenant_isolation_aliases_groups_test.go`, `customer_mail_tenant_isolation_test.go`.

---

## 1. Dashboard

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 1.1 | Customer dashboard: real plan/usage/domains/mailboxes/quota/recent activity/service state — **no platform/global metrics** | `components/Dashboard.tsx` → `api.getDashboard()` → `GET /enterprise/dashboard` → `CustomerDashboard` → `dashboardsvc.NewDashboardService(sqlDB)` (`internal/admin/dashboard`) — tenant-scoped aggregates (domains, mailboxes, quota usage, recent activity, subscription status). RBAC: tenant-role family. CSRF n/a (GET). Test: org portal page + service tests. | COMPLETE |

## 2. Domains

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 2.1 | List own-tenant domains (search/filter) | `components/Domains.tsx` → `api.listDomainsEnterprise()` → `GET /enterprise/domains` → `ListAdminDomains` → `domainAdminSvc` → `internal/admin/domain` service + repo (`coremail_domains`, tenant predicate). `PermDomainsRead`-family gate via enterpriseRead role gate; tenant from session. | COMPLETE |
| 2.2 | Domain detail (+ DKIM, TLS status, mail-access mode) | `GET /enterprise/domains/:id`, `GET .../dkim`, `GET .../dkim/history`, `GET .../tls`, `GET .../mail-access-mode`. | COMPLETE |
| 2.3 | Create domain (plan/limit enforcement, stable typed codes: `DOMAIN_LIMIT_REACHED`, `PLAN_UNAVAILABLE`, `INVALID_LIMIT`, `LIMIT_EXCEEDS_PLAN`, `DOMAIN_ALREADY_EXISTS`, …) | `POST /enterprise/domains` (`canWriteDomains`, CSRF) → `CreateAdminDomain` → `domainAdminSvc.CreateDomain` (transactional provisioning + DKIM + dnsops requirements; `domain_provisioning_api_test.go`, `domain_contract_test.go`). | COMPLETE |
| 2.4 | Update / status transition / delete (soft-delete; dependency-safe) | `PATCH /enterprise/domains/:id`, `POST .../status`, `DELETE .../:id` (CSRF + `domains.write`). | COMPLETE |
| 2.5 | DKIM generate / rotate / revoke + history | `POST .../dkim/generate`, `POST .../dkim/rotate`, `POST .../dkim/revoke`, `GET .../dkim/history` (CSRF on writes). Private keys never exposed. | COMPLETE |
| 2.6 | Verify (record verification) + DNS snapshot + live DNS verify | `POST /enterprise/domains/:id/verify`, `GET .../dns`, `POST .../dns/verify` (429 cooldown carries last-successful snapshot; shared client preserves body). | COMPLETE |
| 2.7 | Plan/limit UX (capacity summary for wizard) | `GET /enterprise/organizations/current/capacity` → `GetOrganizationCapacity` → `domainAdminSvc.GetPlanSummary` (real plan ceilings + live usage; `*_unlimited` flags, null remaining — never misleading 0; fails closed 409 `PLAN_UNAVAILABLE`). | COMPLETE |

## 3. Organization

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 3.1 | Organization profile (name/domain/plan/limits/logo/color) | `components/OrganizationOverviewPage.tsx` → `api.getCurrentOrganization()` → `GET /enterprise/organizations/current` → `GetCurrentOrganization` (tenant-scoped; support-grant middleware admits platform actors with `read_only` grant). | COMPLETE |
| 3.2 | Owner/admin info | Same page reads `/enterprise/members` (see 6.2) — members list includes roles; owner is the `tenant_admin` row. | COMPLETE |
| 3.3 | Plan summary + usage | `api.getSubscription()` / `api.getUsage()` (see 12.1) rendered on Organization page. | COMPLETE |

## 4. Mailboxes

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 4.1 | List own-tenant mailboxes | `components/CustomerMailboxesPage.tsx` → `api.listMailboxes()` → `GET /enterprise/mailboxes` → `ListAdminMailboxes` → `mailboxAdminSvc` (tenant predicate; `customer_mail_operability_test.go`). | COMPLETE |
| 4.2 | Create mailbox (plan-limit UX; quota/status; Argon2id; password never returned) | `POST /enterprise/mailboxes` (`mailboxes.write`, CSRF) → `CreateAdminMailbox` → `mailboxAdminSvc.Create` (folder provisioning in-transaction; `domain_provisioning_api_test.go`). | COMPLETE |
| 4.3 | Update / status / reset-password / delete / restore / purge | `PATCH /enterprise/mailboxes/:id`, `POST .../status`, `POST .../reset-password`, `DELETE .../:id`, `POST .../restore`, `DELETE .../purge` (all `mailboxes.write`, CSRF; purge destructive — typed confirm in UI). | COMPLETE |
| 4.4 | Bulk status | `POST /enterprise/mailboxes/bulk/status`. | COMPLETE |
| 4.5 | Mailbox detail/audit | `GET /enterprise/mailboxes/:id`; audit via `GET /mailboxes/:id/audit` (tenantCompatMW) | COMPLETE |

## 5. Aliases & Groups

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 5.1 | Aliases list/create/delete | `components/AliasesPage.tsx` → `GET /enterprise/aliases`, `POST /enterprise/aliases`, `DELETE /enterprise/aliases/:id` (`aliases.write`; CSRF; loop rejection + conflict detection + tenant predicate). | COMPLETE |
| 5.2 | Groups list/create/delete | `components/GroupsPage.tsx` → `GET /enterprise/groups`, `POST /enterprise/groups`, `DELETE /enterprise/groups/:id` (`groups.write`; CSRF; `coremail_groups` soft-delete; `customer_mail_tenant_isolation_test.go`). | COMPLETE |
| 5.3 | Group membership add/remove | `POST /enterprise/groups/:id/members`, `DELETE /enterprise/groups/:id/members/:memberId` (member rows keyed to group in SQL; tenant predicate through subquery). | COMPLETE |

## 6. Usage, Invitations, Members, Ownership, Status

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 6.1 | Usage (real billing/quota usage only — no fake charts) | `components/UsageQuotasPage.tsx` → `api.getUsage()` → `GET /enterprise/billing/usage` → `GetBillingUsage` → `usageSvc.GetCurrentUsage` (real usage counters per period) + `GET /enterprise/billing/quota` → `CheckBillingQuota` (`domains`/`mailboxes` resource checks) + `GET /billing/plans` (real plan rows). | COMPLETE |
| 6.2 | Members list (roles, names) | `components/MembersRolesPage.tsx` → `GET /enterprise/members` → `ListMembers` → `orgAdminSvc.ListMembers` (tenant users; role allowlist). | COMPLETE |
| 6.3 | Member role change (server assigns/validates roles; **no platform roles accepted** — `isValidOrgMemberRole` rejects `platform_super_admin`/legacy/unknown) | `PATCH /enterprise/members/:id/role` (`users.write`, CSRF) → `UpdateMemberRole` (last-active-admin protection `ErrLastActiveAdmin`; token_version bump). | COMPLETE |
| 6.4 | Remove member (owner protection: last active tenant_admin cannot be removed) | `DELETE /enterprise/members/:id` (`users.write`, CSRF) → `RemoveMember`. | COMPLETE |
| 6.5 | Invitations list/create (role allowlist, 7-day expiry, one-time token reveal)/revoke | `components/InvitationsPage.tsx` → `GET /enterprise/invitations`, `POST /enterprise/invitations` (CSRF, `invitations.write`), `POST /enterprise/invitations/:id/revoke`. Token returned once at creation (`org_invitations` table; token stored hashed; expiry enforced at accept). | COMPLETE |
| 6.6 | **Invitation token re-issue (resend)** | Service `RotateInvitationToken` exists; **no HTTP route** (`POST /enterprise/invitations/:id/resend` missing). **Fixed in this pass** — see Remediation R-6. | BACKEND_GAP → **FIXED (R-6)**; UI_GAP for frontend agent |
| 6.7 | Ownership transfer lifecycle (request → token → accept → cancel) | `components/OwnershipTransferPage.tsx` → `POST /enterprise/ownership/request` (target member), `POST /enterprise/ownership/accept` (token), `POST /enterprise/ownership/cancel` (`ownership.transfer`, CSRF) → `orgAdminSvc.RequestOwnershipTransfer/AcceptOwnershipTransfer/CancelOwnershipTransfer` (`ownership_transfers` table; `ErrLastOwnerCannotTransfer` guard). | COMPLETE |
| 6.8 | Status (suspension status + self-service deletion request/cancel) | `components/SuspensionDeletionPage.tsx` → `GET /enterprise/status` → `SuspensionStatus` (real `org_suspensions` row), `POST /enterprise/deletion` / `POST /enterprise/deletion/cancel` (`organizations.write`, CSRF) → `orgAdminSvc.RequestDeletion/CancelDeletion` (deletion lifecycle with retention period). | COMPLETE |

## 7. Invoices & Billing

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 7.1 | Subscription (plan, status, billing period, trial) | `components/BillingPage.tsx` → `api.getSubscription()` → `GET /enterprise/billing/subscription` → `GetBillingSubscription` → `billingSvc.GetSubscription` (real `subscriptions` row: plan_id, status trialing/active/past_due/grace/suspended/cancelled/expired, current_period_start/end, trial_ends_at). | COMPLETE |
| 7.2 | Plan change (create/update subscription) | `POST /enterprise/billing/subscription` (`billing.write`, CSRF) → `CreateBillingSubscription` → `billingSvc.CreateSubscription(plan_id, interval, trial_days)` — conflict on invalid plan. | COMPLETE |
| 7.3 | Invoices list/detail (real rows from webhook/provider events; status open/paid/…) | `components/InvoicesPage.tsx` → `GET /enterprise/billing/invoices`, `GET /enterprise/billing/invoices/:id` → `invoiceSvc.ListTenantInvoices` (tenant-scoped; `invoice_service_test.go`). No fabricated paid invoices — rows only exist from provider events. | COMPLETE |
| 7.4 | Payment/provider status | **BACKEND_GAP before this pass**: no tenant-facing endpoint reports payment provider configuration/status honestly. **Fixed in this pass** — see Remediation R-7 (billing state endpoint surfaces "provider not configured" when no provider is wired; never claims payment success). | BACKEND_GAP → **FIXED (R-7)**; UI_GAP for frontend agent |
| 7.5 | Balance/credit ledger (tenant-visible portion) | Platform billing ledger exists (`platform_billing_*`); tenant-facing balance endpoint added in R-7 as read-only surface (no raw ledger internals). | COMPLETE after R-7 |

## 8. API Keys

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 8.1 | List/create (tenant-bound scopes only; scope validation rejects platform-only scopes for tenant roles and tenant scopes for PSA) | `components/ApiKeysPage.tsx` → `GET /enterprise/api-keys` → `ListEnterpriseAPIKeys` → `ListAPIKeys` (tenant-bound keys), `POST /enterprise/api-keys` (`apikeys.write`, CSRF) → `CreateEnterpriseAPIKey` → `validateAPIKeyScopes` (public scopes map to tenant resources; `apikey_scope_test.go`). | COMPLETE |
| 8.2 | Rotate (atomic; new key returned once; old disabled; warning "will not be shown again") | `POST /enterprise/api-keys/:id/rotate` (`apikeys.write`, CSRF) → `RotateEnterpriseAPIKey` → `apikeys.RotateByID`. | COMPLETE |
| 8.3 | Delete | `DELETE /enterprise/api-keys/:id` (`apikeys.write`, CSRF). | COMPLETE |
| 8.4 | **API key creation one-time secret reveal** | `CreateAPIKey` returns `api_key` full secret only on creation; list endpoints return prefix only. Verify list contract does not include the secret — covered by `apikey_scope_test.go`. | COMPLETE |

## 9. Account, Security, Preferences

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 9.1 | Profile get/update | `components/AccountSettingsPage.tsx` → `GET/PATCH /account/profile` (self-scoped). | COMPLETE |
| 9.2 | Change password | `POST /auth/change-password` (CSRF; current password verified; token_version bump). | COMPLETE |
| 9.3 | MFA (status/setup/verify/disable/recovery codes) | `components/SecurityPage.tsx` → `/account/mfa/*` (dedicated 10/15-min rate limit; recovery-code regenerate). | COMPLETE |
| 9.4 | Sessions list + revoke | `GET /account/sessions`, `POST /account/sessions/:id/revoke` (CSRF; self-scoped). | COMPLETE |
| 9.5 | Preferences + notification preferences | `components/PreferencesPage.tsx` → `GET/PATCH /account/preferences`, `/account/notification-preferences`. | COMPLETE |

## 10. Support

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 10.1 | Submit support request (category/subject/message; rate-limited 5/10min per user; confirmation) | `components/SupportPage.tsx` → `POST /account/support-requests` (`supportLimiter` 5/10min) → `SubmitSupportRequest` — sends real mail via transactional sender; fails honestly on send failure (no false "sent"). | COMPLETE |
| 10.2 | Ticket history | **No ticket/history backend exists** (no `support_tickets` table, no list endpoint). Not invented: frontend must NOT render a history concept; if a history surface is desired it requires a new bounded context (backend-first). Documented disposition — the Support page currently submits only. | INTENTIONAL_READ_ONLY (submission only; history is NOT implemented — do not fabricate) |

## 11. Org-side routes outside visible pages (dispositioned)

| Route | Disposition |
|---|---|
| `/enterprise/organizations/:id` (own tenant only; IDOR-guarded 404) | COMPLETE (used by no page — `GetCurrentOrganization` is the page's route; kept for contract completeness) |
| `/enterprise/audit/logs` (tenant-scoped audit) | COMPLETE — page uses it via `api.listAuditLogs()` → `ListEnterpriseAuditLogs` (tenant-scoped) |
| `/enterprise/abuse/*` (send-limit, signals list/acknowledge/resolve) | COMPLETE (Abuse panel) |
| `/enterprise/cluster/nodes*` (cordon/uncordon/drain/resume) | COMPLETE (tenant cluster control; real `cluster.Service`) |
| `/enterprise/relay/*` (pools/providers/routing-rules/emergency-override; provider test SSRF-safe) | COMPLETE (Relay control plane) |
| `/enterprise/imports*` (tenant import lifecycle, same contracts as platform) | COMPLETE |
| `/enterprise/billing/...` — see section 7 | COMPLETE (after R-7) |
| `/automation/jobs*` (tenant jobs) | COMPLETE |
| `/webhooks/subscriptions*` (CRUD, rotate-secret, deliveries, replay/retry) | COMPLETE |
| `/enterprise/mailboxes/bulk/*` + `/enterprise/domains/:id/mailboxes/bulk/*` (tenant bulk provisioning) | COMPLETE |
| `/admin/tenants/current` (tenant row read for branding) + `PATCH /admin/tenants/:id/branding` (logo_url/primary_color validated) | COMPLETE |
| Legacy `/domains*`, `/mailboxes*`, `/users*`, `/queue*` tenantCompatMW routes | DUPLICATE_SUPERSEDED by `/enterprise/*` (kept for the webmail/legacy SPA consumers where load-bearing) |

## 12. Remediation log (backend fixes in this pass)

| ID | Area | Fix | Files |
|---|---|---|---|
| R-6 | Invitations | `POST /enterprise/invitations/:id/resend` — re-issue (rotate) the one-time invitation token via existing `RotateInvitationToken` service (status must be `pending`; audited) | `internal/api/handlers/customer_org.go`, `internal/api/router.go`, tests |
| R-7 | Billing | Tenant-facing payment/provider state: extend `GET /enterprise/billing/usage` response OR add `GET /enterprise/billing/state` — real subscription+usage+invoice summary + honest provider configuration state (from `cfg.Payment`: provider, enabled; "not configured" when absent). Never fabricates card/MRR/payment success | `internal/api/handlers/billing.go`, `internal/api/router.go`, tests |

## 13. Frontend handoff notes (for Frontend Developer)

1. **Invitations**: wire "Resend invitation" button → `POST /enterprise/invitations/:id/resend` (R-6); token shown once.
2. **Billing**: render provider state honestly from R-7 ("Payment provider not configured" when absent); keep existing subscription/usage/invoices surfaces.
3. **Support**: no ticket history exists — do not render a history concept; keep submission + confirmation only (or request a new backend bounded context first).
4. **Dashboard**: keep tenant-scoped metrics only; never render platform/global totals.
5. **API keys**: keep one-time secret reveal at creation; never display secret again after creation.
6. Do NOT wire platform roles into any member/invitation form (`tenant_admin`/`tenant_operator`/`tenant_support`/`tenant_readonly` only — server rejects the rest).

## 14. Phase 2 addendum (API Platform Engineer) — contract completion

Executed after Phase 1 (backend) on the same branch. Starting SHA:
`0c9ad30ff801bb9d762036445338aafebf310d84`.

### 14.1 Final CONTRACT_DRIFT disposition (Phase 2, org surface)

| ID | Drift found (proved from source) | Fix (Phase 2) | Status |
|---|---|---|---|
| D-3 (invitation resend, R-6) | `POST /enterprise/invitations/:id/resend` returned the raw service error string with no stable code for non-pending invitations, and the one-time token response had no `Cache-Control: no-store` | Stable codes: `VALIDATION_FAILED` (bad id) / `NOT_FOUND` (missing invitation) / `INVALID_STATE_TRANSITION` (accepted/revoked/expired); no-store on the token response; pinned by `TestInvitationResend_ErrorShapeAndNoStore` | FIXED (Phase 2) |
| R-7 (billing state) | `GET /enterprise/billing/state` — new route; verified in source: real subscription/plan/usage/invoice rows, honest `payment_provider` (`configured:false` + note when no provider), `invoices` never null | Verified consistent; documented in OpenAPI with `BillingStateResponse`; pinned by `TestBillingState_RealDataAndHonestProvider` + the platform-overview envelope test | RESOLVED (no fix needed) |

### 14.2 Deliverables completed (Phase 2)

- OpenAPI spec (`docs/api/openapi.yaml`) documents
  `POST /enterprise/invitations/{id}/resend` and
  `GET /enterprise/billing/state` (plus the full platform-side surface);
  local OpenAPI gate green (both specs lint clean,
  `TestPublicRouterMatchesOpenAPI` passes).
- Contract tests added: `TestInvitationResend_ErrorShapeAndNoStore`,
  `TestPlatformBillingOverview_EnvelopeShapePins` (platform side),
  `TestAuditLogList_ResultFilterLimitClampAndDetailShape`, and the
  platform create/groups/DKIM shape tests.

### 14.3 Blocker reviews (Phase 2 API Platform, second pass)

Starting SHA: `29d7877e0e5dd1986576d215862cd2c12ba8f4ec`.

| ID | Blocker finding (proved from source) | Fix (this pass) | Status |
|---|---|---|---|
| B-A1 (owner lifecycle) | PSA-created organizations (`POST /platform/organizations`) were created `tenants.active=1` with only a pending owner invitation — an ownerless ACTIVE org. The org-creation lifecycle test even pinned the wrong expectation (`Active==true`) | PSA orgs now start `pending_activation` (`active=0`) + REQUIRED owner invitation; activated ONLY by the new public `POST /auth/invitations/accept`; detail reports `status_label: pending_activation`; existing tests updated to pin the correct lifecycle | FIXED |
| B-A2 (invitation accept) | No HTTP route could redeem an invitation token — `AcceptInvitation` was dead code, so the owner could never activate | New public `POST /auth/invitations/accept` (token + password): creates the member (role/tenant from invitation row), atomically claims the invitation, activates the org (respecting open suspensions), audits. Stable codes: 404 `NOT_FOUND` / 409 `INVALID_STATE_TRANSITION` / 409 `CONFLICT` | FIXED |
| B-A4 (duplicate invites) | Two pending invitations for the same email were possible (no guard), leaving two live tokens where one redemption silently orphaned the other | `POST /enterprise/invitations` now rejects a duplicate pending email with 409 `CONFLICT` "a pending invitation already exists for this email"; resend covers the fresh-share use case by rotating the token | FIXED |
| B-B2 (tenant audit page) | `GET /enterprise/audit/logs` read the legacy `coremail_audit` store and returned a bare array — the tenant page saw different actions than the platform page under a second incompatible contract | Reads the canonical `orvix_audit`, tenant-scoped, with the same `{entries, total, limit, offset}` envelope + rich fields as `GET /audit/logs`; filters action/actor/target/result/since/until + pagination. Frontend `api.listAuditLogs` unwraps `.entries` (compat wrapper) | FIXED |
| B-C1 (invitation delivery honesty) | Invitation delivery is inviter-mediated (the one-time token is returned to the inviter, who shares it) — no mail submission is claimed, so there is no false-success class bug; resend rotates the token and returns it once | Verified honest; OpenAPI + matrices document the delivery model explicitly (inviter shares the token; UI must render it with a copy action — API_READY_FRONTEND_PENDING) | VERIFIED (no fix) |

Verified consistent (no drift): billing state remains real-data-only with
honest provider state; invitation revoke/expiry semantics unchanged;
`requireTenantActive` continues to gate the console so a
pending_activation org cannot be used before its owner activates.
