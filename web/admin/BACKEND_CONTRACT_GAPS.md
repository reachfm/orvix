# BACKEND_CONTRACT_GAPS — Platform Super Admin Mail Control

Integration target: PR #65 head `f1d2954` (merged into the frontend
branch at `de49a8e`). Every entry below was verified against the real
router (`internal/api/router.go`), handler
(`internal/api/handlers/platform_mail_control.go`,
`platform_relay_admin.go`), and service contracts
(`internal/platform/mailcontrol`, `deliverability`, `relay`). No
workaround was fabricated: the UI surfaces exactly what the platform
routes provide and never invents endpoints, controls, or data.

## 1. Platform Domains — surface smaller than the frontend wish-list

- Present routes: list, detail, lifecycle status
  (`POST /platform/domains/:tenant_id/:id/status`), mail-access mode
  (`POST .../mail-access-mode`).
- NOT present in the platform route family (verified in router.go):
  - create domain for an explicit tenant
  - update of safe domain fields (plan, description, limits)
  - DNS health / DNS record detail / DNS verification trigger
  - DKIM generate / rotate / revoke (selector + history exist only in
    the tenant admin service, not behind a platform route)
  - TLS / ACME state + expiry, or any TLS/ACME action
  - soft-delete / restore / purge
  - lock / unlock (the backend treats `locked` as a read-only defensive
    value; `ParseDomainStatus` accepts only active/disabled/suspended)
- Frontend consequence: the Domains page shows lifecycle, counts,
  DKIM-enabled/selector, DMARC, mail-access mode and timestamps — the
  real contract — and does not render DNS/TLS/DKIM-rotate controls.

## 2. Platform Mailboxes — no restore route

- Present: list, detail, status, quota, one-time password reset,
  typed-confirmation soft delete, bulk status.
- NOT present: create mailbox, restore (deleted mailboxes can only be
  restored through the tenant-admin `RestoreMailbox` service path,
  which has no platform route), irreversible purge, protocol updates.
- The DELETE route is soft-delete with `X-Confirm: PURGE-MAILBOX-<id>`;
  the UI explains it as soft-delete (status → deleted) and offers no
  restore because no platform route exists.

## 3. Platform Aliases — no update/enable/disable/restore

- Present: list, detail, create (`domain_id`, `from_addr`, `to_addr`),
  delete.
- NOT present: update of alias fields, enable/disable, restore. The
  list returns the real `active` flag; there is no route to change it.

## 4. Platform Groups — read-only inventory

- Present: list, detail, members.
- NOT present (verified): create group, update, delete, add member,
  remove member, sender-permission mutations. The Groups page is
  explicitly inventory + membership read-only and states this.
  Tenant Admin continues to manage groups via tenant-owned routes.

## 5. Deliverability — no dimension "value" filter UI on events

- Present: metrics (requires RFC3339 `start`/`end`), events
  (domain/type/provider/start/end filters), event detail.
- The metrics endpoint accepts `dimension`/`value` but the UI uses the
  tenant dimension (the canonical view); other dimension views are
  backend-capable but not wired (BACKEND_ONLY in the capability
  inventory).
- `BreakdownRow` has no json tags in the backend, so breakdown rows
  serialize as `{"Key": ..., "Count": ...}`; the frontend contract
  matches that exactly.

## 6. Queue — attempt history now real

- PR #65 fixed the attempt-history source to
  `coremail_delivery_attempts` (previously a never-written table).
  No gap; noted for completeness.

## 7. Relay — none

- The relay route family is complete (create/update/enable/disable/
  rotate/test/delete with idempotency + typed confirmations). The only
  constraints are intentional: credentials are never returned and the
  generated credential appears exactly once.

## 8. Suppressions — none

- Full lifecycle present (add/list/get/history/release/reactivate/
  delete-with-confirmation/remove-by-address). DELETE is
  release-with-history, which the UI states explicitly.
