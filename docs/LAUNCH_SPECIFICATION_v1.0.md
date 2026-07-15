# Orvix Launch Specification â€” v1.0

**Status:** Phase 3 complete. Backend production-ready. This document is the
master blueprint for the customer-facing experience. Codex will implement
against this spec.

**Date:** 2026-07-15

**Audience:** Product Designer, SaaS UX Architect, Implementation Engineering.

**Scope:** All customer-facing surfaces. Marketing site, customer portal,
onboarding, email lifecycle, pricing UX, visual language, accessibility.

**Out of scope:** Backend redesign, billing logic, SMTP, IMAP, storage,
database, server, infrastructure, Caddy, MTA-STS, DNS, TLS, systemd.

**Repository:** `reachfm/orvix`, branch `phase3/saas-ga-readiness`.

---

## 0. Backend Surface (authoritative truth for this spec)

The spec is grounded in what the backend already exposes. Every page, every
flow, every field maps to a real endpoint, a real DB column, or a real
email. Where the spec describes a capability the backend does not yet
expose, the spec says so explicitly and marks it as
**"Requires backend implementation"**.

### 0.1 Public surface (no auth)

| Endpoint | Purpose | Spec reference |
| -------- | ------- | -------------- |
| `GET  /api/v1/health` | Liveness + version | Status, login, footer |
| `GET  /api/v1/billing/plans` | Plan catalog for marketing/pricing | Pricing, upgrade flow |
| `GET  /autodiscover/autodiscover.xml` | Outlook autodiscover | Webmail client setup |
| `GET  /.well-known/autoconfig/mail/config-v1.1.xml` | Thunderbird autoconfig | Webmail client setup |
| `GET  /.well-known/mta-sts.txt` | MTA-STS policy | Enterprise page |
| `POST /api/v1/auth/signup` | Create tenant + user + free subscription | Signup, onboarding |
| `POST /api/v1/auth/login` | Email + password (+ MFA challenge) | Login |
| `POST /api/v1/auth/refresh` | Rotate JWT | Login |
| `POST /api/v1/auth/mfa/verify` | TOTP / recovery code | Login, MFA |
| `POST /api/v1/auth/forgot-password` | Email reset link (rate-limited) | Password reset |
| `POST /api/v1/auth/reset-password` | Consume reset token, set new password | Password reset |
| `POST /api/v1/billing/webhook` | Payment provider webhook | Billing events |
| `POST /api/v1/billing/complaint` | Bounce complaint webhook | Compliance |
| `POST /api/v1/webmail/login` | Webmail form login | Webmail setup |

### 0.2 Authenticated surface (customer portal)

All mounted under the `protected` group, which enforces
**HttpOnly cookie session + CSRF on writes + tenant middleware**.

| Endpoint | Purpose | Spec reference |
| -------- | ------- | -------------- |
| `GET    /api/v1/me` | Current user + role + tenant scope | Profile, topbar |
| `POST   /api/v1/auth/change-password` | Change own password | Profile, Security |
| `POST   /api/v1/auth/logout` | End current session | Sign out |
| `POST   /api/v1/auth/logout-all` | End all sessions for the user | Security, Sessions |
| `GET    /api/v1/csrf-token` | Get token for write requests | (any write form) |

Enterprise tenant admin (admin / operator / superadmin):

| Endpoint | Purpose | Spec reference |
| -------- | ------- | -------------- |
| `GET    /api/v1/enterprise/dashboard` | Tenant KPIs | Dashboard |
| `GET    /api/v1/enterprise/domains` | List tenant domains | Domains |
| `GET    /api/v1/enterprise/domains/:id` | Single domain | Domains |
| `POST   /api/v1/enterprise/domains` | Add domain | Onboarding, DNS Wizard |
| `PATCH  /api/v1/enterprise/domains/:id` | Update domain | Domains |
| `POST   /api/v1/enterprise/domains/:id/status` | Activate / suspend | Domains |
| `GET    /api/v1/enterprise/mailboxes` | List mailboxes | Mailboxes |
| `GET    /api/v1/enterprise/mailboxes/:id` | Single mailbox | Mailboxes |
| `POST   /api/v1/enterprise/mailboxes` | Create mailbox | Mailboxes, Onboarding |
| `PATCH  /api/v1/enterprise/mailboxes/:id` | Update mailbox | Mailboxes |
| `POST   /api/v1/enterprise/mailboxes/:id/status` | Suspend / enable | Mailboxes |
| `POST   /api/v1/enterprise/mailboxes/bulk/status` | Bulk suspend / enable | Mailboxes |
| `POST   /api/v1/enterprise/mailboxes/:id/reset-password` | Reset password | Mailboxes, Profile |
| `GET    /api/v1/enterprise/organizations/current` | Org row | Organization |
| `GET    /api/v1/enterprise/organizations/:id` | Org row by id | Organization |
| `GET    /api/v1/enterprise/invitations` | List invitations | Members |
| `POST   /api/v1/enterprise/invitations` | Invite member | Members |
| `POST   /api/v1/enterprise/invitations/:id/revoke` | Cancel pending invite | Members |
| `GET    /api/v1/enterprise/members` | List org members | Members |
| `PATCH  /api/v1/enterprise/members/:id/role` | Change role | Members, Roles |
| `DELETE /api/v1/enterprise/members/:id` | Remove member | Members |
| `POST   /api/v1/enterprise/ownership/request` | Request transfer | Ownership |
| `POST   /api/v1/enterprise/ownership/accept` | Accept transfer | Ownership |
| `POST   /api/v1/enterprise/ownership/cancel` | Cancel transfer | Ownership |
| `GET    /api/v1/enterprise/aliases` | List aliases | Aliases |
| `POST   /api/v1/enterprise/aliases` | Create alias | Aliases |
| `DELETE /api/v1/enterprise/aliases/:id` | Delete alias | Aliases |
| `GET    /api/v1/enterprise/groups` | List groups | Groups |
| `POST   /api/v1/enterprise/groups` | Create group | Groups |
| `POST   /api/v1/enterprise/groups/:id/members` | Add member to group | Groups |
| `DELETE /api/v1/enterprise/groups/:id/members/:memberId` | Remove from group | Groups |
| `DELETE /api/v1/enterprise/groups/:id` | Delete group | Groups |
| `GET    /api/v1/enterprise/abuse/send-limit` | Current send-rate posture | Usage, Quota |
| `GET    /api/v1/enterprise/abuse/signals` | Active abuse signals | Security |
| `POST   /api/v1/enterprise/abuse/signals/:id/acknowledge` | Acknowledge | Security |
| `POST   /api/v1/enterprise/abuse/signals/:id/resolve` | Resolve | Security |
| `GET    /api/v1/enterprise/status` | Suspension state | Billing, suspension |
| `POST   /api/v1/enterprise/deletion` | Request account deletion | Cancel flow |
| `POST   /api/v1/enterprise/deletion/cancel` | Cancel pending deletion | Cancel flow |
| `GET    /api/v1/enterprise/billing/subscription` | Current sub | Billing |
| `POST   /api/v1/enterprise/billing/subscription` | Create / change sub | Pricing, upgrade/downgrade |
| `GET    /api/v1/enterprise/billing/usage` | Current period usage | Usage |
| `GET    /api/v1/enterprise/billing/quota` | Quota headroom | Usage, Quota warning |
| `GET    /api/v1/customer/domains` | Customer-facing domain list | Domains |
| `GET    /api/v1/customer/domains/:id` | Domain detail | Domains |
| `GET    /api/v1/customer/domains/:id/dns` | DNS records to publish | DNS Wizard |
| `POST   /api/v1/customer/domains/:id/verify` | Trigger DNS verify | DNS Wizard |

Note: API keys, audit-log view, MFA setup/verify, etc. are listed in the
`Authenticator` / `admin_mfa.go` / enterprise handler set. They are scoped
to admin roles, not customer roles; the customer portal therefore
**does not** expose them. The relevant customer capabilities (password
change, MFA challenge at sign-in, list of active sessions) are handled
through the public/authenticated endpoints above.

### 0.3 Roles

| Role | Customer portal | Admin console |
| ---- | --------------- | ------------- |
| `user` (webmail / self-serve mailbox owner) | Full access to **own** mailbox + own profile, sessions, MFA. | No admin console access. |
| `readonly` (org auditor) | Read everything in the org. | Read everything in the org via admin console. **Requires backend implementation** for invite flow. |
| `operator` (helpdesk) | Read all org data + reset passwords + suspend/enable mailboxes. | Same. |
| `admin` (org admin) | Full tenant admin: domains, mailboxes, members, billing. | Same. |
| `superadmin` (Orvix staff) | No customer portal access (staff are not customers). | Full cross-tenant. |

The signup handler currently hard-codes the first user as `role: "user"`.
Promoting a user to admin is a one-time act that the spec does **not**
expose in v1.0 self-serve UI; it is done by the Orvix team through
support on request. **Promote-self to admin in UI is not in scope** for
v1.0.

### 0.4 Plan catalog (the only real plans)

Source of truth: internal/billing/service.go SeedDefaultPlans().

| Plan ID | Name | USD/mo | USD/yr | Domains | Mailboxes | Storage | Sends/day | Features |
| ------- | ---- | ------ | ------ | ------- | --------- | ------- | --------- | -------- |
| ree | Free | 0 | 0 | 1 | 5 | 1 GB | 500 | custom_domain, dkim |
| starter | Starter | 9.99 | 99.90 | 3 | 25 | 10 GB | 2 000 | + mta_sts, api, team |
| usiness | Business | 29.99 | 299.90 | 10 | 100 | 100 GB | 10 000 | + groups, catch_all, mail_forwarding, backup, audit_log, mfa |
| enterprise | Enterprise | 99.99 | 999.90 | 100 | 1 000 | 1 TB | 100 000 | + sso, sla, priority_support |

Annual prices are 10x monthly (16% discount) — consistent with the
PriceYearly: PriceMonthly * 10 pattern in the seed.

Pricing page must mirror these numbers exactly. **No invented tiers.
No invented features.** If marketing wants a "Pro" tier, that is a
backend change and is **Requires backend implementation**.

### 0.5 Subscription states

internal/billing/types.go defines:

| State | Meaning | UX |
| ----- | ------- | -- |
| 	rialing | In trial, no payment method yet. | Banner: "X days left in trial." |
| ctive | Paid, in good standing. | (default) |
| past_due | Payment failed, retry pending. | Banner: "Payment failed. Update payment method." |
| grace_period | After past_due; sending still allowed for N days. | Banner: "Service in grace period. Update payment method to avoid suspension." |
| suspended | Service cut, mail rejected at SMTP layer. | Full-screen block, webmail login blocked. |
| cancelled | Will not renew. | Banner: "Subscription ends YYYY-MM-DD." |
| expired | End of term reached, no renewal. | Same as cancelled. |

### 0.6 Localisation

elease/admin/modules/i18n.js ships en and r. Marketing site and
customer portal inherit the same i18n surface so Arabic right-to-left
is consistent across admin, webmail, and marketing.

**v1.0 supported locales:** English (default), Arabic. RTL is
opt-in via ?rtl=1 query param; the portal stores the choice in
orvix_locale (already used by the admin app) and applies it on every
page. RTL flips the document direction and renders mirrored icons
(arrow, caret). LTR remains the default for English and Arabic users
who do not pass ?rtl=1.

The Arabic translation table is shipped out-of-the-box. **Partial
Arabic is acceptable** for v1.0 if a key has no Arabic string — the
page falls back to English for that key. A missing key never breaks
the page; it falls back to the key itself, which is the existing
admin-app behaviour. **No placeholder strings** in either locale.

---

## 1. Public Sitemap

Static HTML (or SSR) pages served from orvix.com (or whatever domain
the deploy uses). Marketing site is independent from the portal; the
portal is pp.orvix.com.

| Path | Page | Audience |
| ---- | ---- | -------- |
| / | Home | Anonymous |
| /features | Features | Anonymous |
| /enterprise | Enterprise | Anonymous |
| /security | Security & compliance | Anonymous |
| /pricing | Pricing | Anonymous |
| /docs | Documentation index (links to docs.orvix.email) | Anonymous |
| /docs/api | API reference (redirects to docs) | Anonymous |
| /api | API one-pager for developers (public surface) | Anonymous |
| /status | Status (links to status.orvix.email) | Anonymous |
| /about | About | Anonymous |
| /contact | Contact form | Anonymous |
| /blog | Blog index (placeholder list, **Requires backend implementation** for full CMS) | Anonymous |
| /blog/:slug | Blog post (placeholder) | Anonymous |
| /changelog | Changelog | Anonymous |
| /legal/terms | Terms of service | Anonymous |
| /legal/privacy | Privacy policy | Anonymous |
| /legal/aup | Acceptable use policy | Anonymous |
| /legal/gdpr | GDPR statement | Anonymous |
| /legal/cookies | Cookie policy | Anonymous |
| /signup | Account creation (Step 1) | Anonymous |
| /login | Redirects to pp.orvix.com/login | Anonymous |
| /forgot | Forgot password (Step 1) | Anonymous |
| /reset?token=… | Reset password (Step 2) | Anonymous |

**Cannot serve from the marketing site** because the marketing site is
public/anonymous:

- The marketing site MUST NOT log in users, MUST NOT store session
  cookies, MUST NOT see CSRF tokens. The portal host
  (pp.orvix.com) is the only place that holds the
  __Host-orvix_session cookie.
- Marketing pages load statically; no PHP / no SSR runtime. The
  portal host serves the SPA at /admin and /webmail.


---

## 2. Marketing Site (Public, Anonymous)

### 2.1 Shared chrome (every marketing page)

- **Top nav** (left to right): Logo (links to /).
  Links: Features, Enterprise, Security, Pricing, Docs, Status.
  Right side: Sign in (links to /login), Start free (primary
  button, links to /signup).
- **Footer** (four columns):
  1. Product — Features, Enterprise, Security, Pricing, Status, Changelog.
  2. Resources — Docs, API, Blog.
  3. Company — About, Contact, Careers (mailto:careers@orvix.com).
  4. Legal — Terms, Privacy, AUP, GDPR, Cookies.
  Bottom row: small Orvix mark, copyright © Orvix, Inc., language
  switcher (English | ???????).
- **Cookies / consent banner:** Bottom-anchored strip with a "Reject
  all" and "Accept all" button. Loaded on every page. Preference
  stored in orvix_cookie_consent localStorage. No third-party
  tracking cookies; only first-party orvix_* keys are used.
  **Do not include analytics SDKs in v1.0.** Adding analytics
  is **Requires backend implementation**.
- **Locale switch:** switches between English and Arabic; persists
  via orvix_locale localStorage; sets document.documentElement.lang
  and dir attributes. RTL flips the entire layout.

### 2.2 Home (/)

Above the fold (the operator's first 1.5 seconds):

- **Headline:** One sentence. "Mail operations, end to end." Sub-headline:
  "Multi-tenant business email on a single managed platform — domains,
  mailboxes, audit, billing, in one console."
- **Primary CTA:** Start free (links to /signup).
- **Secondary CTA:** See pricing (links to /pricing).
- **Hero visual:** A still illustration of the customer dashboard,
  rendered as a static raster (PNG / WebP). No animated illustrations.
  No carousel hero. The illustration must come from the design system
  **as a static export** — it is **not** generated from the live SPA.

Below the fold (the operator scrolls):

1. **Trusted by** — three to five customer logos in greyscale. **No
   logos until customers sign off on the partnership.** The slot is
   there; the row is omitted until approved.
2. **What you get** — three columns:
   - **Custom domains & DNS** — Verified MX/SPF/DKIM/DMARC. (Maps to
     /api/v1/customer/domains/:id/dns.)
   - **Multi-tenant by design** — Each tenant isolated. (Maps to
     coremail_domains scoped by 	enant_id.)
   - **Audit & compliance** — Every admin action logged. (Maps to
     coremail_audit table; **requires backend implementation** for
     a customer-facing audit view. The admin console exposes it.)
3. **Live in 5 minutes** — A four-step "how it works" strip:
   1. Create your account.
   2. Add a domain.
   3. Verify DNS.
   4. Invite your team.

   Each step links into the relevant onboarding section of the
   customer portal.
4. **Security & privacy** — Three icons + one-line labels: CSRF
   cookies, MFA, audit log. (True — backend enforces all three.)
5. **Pricing summary** — A condensed four-column plan table that
   mirrors the pricing page. The "Most popular" badge is on
   Business. (The badge is design-only; the plans are real.)
6. **Customer quote** (when available) or "Built by mail engineers"
   tagline with the small Orvix mark.
7. **Final CTA** — Start free button.

Footer follows the global pattern.

### 2.3 Features (/features)

A long scroll that explains each capability. Each capability block is
**only included if the backend actually supports it**; otherwise the
section says so explicitly.

| Section | Maps to | Real? |
| ------- | ------- | ----- |
| Custom domains & DNS verification | /api/v1/customer/domains/:id/dns + /verify | Yes |
| DKIM key management | admin /api/v1/admin/dns/:domain/dkim | Yes (admin only — customer read via customer/domains/:id/dns) |
| MTA-STS & TLS-RPT | /.well-known/mta-sts.txt | Yes |
| Mailbox provisioning | /api/v1/enterprise/mailboxes | Yes |
| Aliases & groups | /api/v1/enterprise/aliases + /groups | Yes |
| Catch-all (org-wide address) | **Requires backend implementation** | No |
| Audit log | admin /api/v1/admin/audit-logs | Yes (admin only) |
| API access | /api/v1/me + admin endpoints; **Requires backend implementation** for a published v1.0 customer-facing API token surface | Partial |
| Webmail | /webmail SPA | Yes |
| Push notifications | /api/v1/webmail/push/* | Yes |
| Vacation / forwarding | /api/v1/webmail/vacation + /forwarding | Yes |
| Mobile (IMAP / SMTP) | Outlook/Thunderbird autodiscover endpoints | Yes |

For each capability the section shows a representative screenshot of
the real surface (not a mock). All screenshots are static exports.

**Do not include** capabilities that are not in the backend. Specifically
**do not promise** end-to-end encryption, AI email writing, or "advanced
threat detection" — those are not in the v1.0 backend.

### 2.4 Enterprise (/enterprise)

Long-form sales page. Three sections:

1. **Built for scale** — hard numbers from the real backend
   (1 000 mailboxes per tenant, 100 000 sends/day, 1 TB storage). The
   numbers must match the Enterprise plan row in §0.4 exactly.
2. **Compliance** — table of compliance posture. **Only include
   rows where the actual implementation supports the claim.**

   | Standard | Posture |
   | -------- | ------- |
   | GDPR (data export / delete) | POST /enterprise/deletion exists; "Request data export" endpoint **Requires backend implementation** |
   | SOC 2 (Type II) | **Requires backend implementation** — page says "In progress" with a contact link |
   | ISO 27001 | **Requires backend implementation** — page says "In progress" with a contact link |
   | HIPAA BAA | **Requires backend implementation** — page says "On request, contact sales" |
   | TLS 1.3 in transit | Yes (Caddy default) |
   | At-rest encryption | Yes (database-level; **Requires backend implementation** for KMS-managed keys) |

3. **Talk to sales** — mailto sales@orvix.com and a small form
   that POSTs to a webhook (**Requires backend implementation** for
   the form action; v1.0 fallback is the mailto link).

### 2.5 Security (/security)

The honesty page. Every claim maps to a real, demonstrable property.
Where a claim is aspirational, the page says "in progress" with a
contact link rather than stating a false capability.

Sections, in order:

1. **Transport** — TLS 1.3 only on pp.orvix.com, webmail.*,
   dmin.*. HSTS preload. Cipher list shown verbatim. (Source:
   Caddyfile + internal/api/router.go securityHeaders.)
2. **Authentication** — HttpOnly __Host- cookies, CSRF on writes,
   password hashed with bcrypt (uth/auth.go),
   opaque session token is stored as SHA-256 hash only
   (uth.go GenerateOpaqueSession / ValidateOpaqueSession).
   Show the uth.go flow diagram (login ? bcrypt ? SHA-256 session
   row ? cookie).
3. **Authorization** — 5 roles, server-side role check, no client-side
   role hiding. (Source: §0.3 + internal/auth/auth.go RequireRole.)
4. **MFA** — TOTP and one-time recovery codes (internal/api/handlers/admin_mfa.go).
   **Customer-portal MFA enrollment is **Requires backend
   implementation**; v1.0 surfaces the MFA CHALLENGE at sign-in only
   (the user must configure MFA in the admin console for now).**
5. **Login protection** — rate limit + per-account lockout
   (internal/trustmgmt/service.go + uth.go LoginProtectionStatus).
6. **Audit** — every admin action written to coremail_audit
   (uth.go writeAuditLog + enterprise handlers). Customer portal
   audit view is **Requires backend implementation**; the page says so.
7. **Data residency** — current deploy is in one region; multi-region
   failover is **Requires backend implementation**.
8. **Responsible disclosure** — security@orvix.com mailto and a
   short PGP block.

### 2.6 Pricing (/pricing)

Four-column plan table. **Numbers come straight from §0.4.**

Layout:

`
+-------------------------------------------------------+
¦ Free        ¦ Starter     ¦ Business    ¦ Enterprise  ¦
¦           ¦ .99 /mo   ¦ .99 /mo  ¦ .99 /mo  ¦
¦ (or /yr)  ¦ (or /yr) ¦ (or /yr) ¦ (or /yr) ¦
+-------------+-------------+-------------+-------------¦
¦ 1 domain    ¦ 3 domains   ¦ 10 domains  ¦ 100 domains ¦
¦ 5 mailboxes ¦ 25 mailboxes¦ 100 mboxes  ¦ 1 000 mboxes¦
¦ 1 GB        ¦ 10 GB       ¦ 100 GB      ¦ 1 TB        ¦
¦ 500 / day   ¦ 2 000 / day ¦ 10 000 / day¦ 100 000/day ¦
¦             ¦             ¦             ¦             ¦
¦ Custom domain¦+ DKIM      ¦+ DKIM       ¦+ DKIM       ¦
¦ DKIM        ¦+ MTA-STS    ¦+ MTA-STS    ¦+ MTA-STS    ¦
¦             ¦+ API access ¦+ API access ¦+ API access ¦
¦             ¦+ Team mgmt  ¦+ Groups     ¦+ Groups     ¦
¦             ¦             ¦+ Catch-all  ¦+ Catch-all  ¦
¦             ¦             ¦+ Forwarding ¦+ Forwarding ¦
¦             ¦             ¦+ Backups    ¦+ Backups    ¦
¦             ¦             ¦+ Audit log  ¦+ Audit log  ¦
¦             ¦             ¦+ MFA        ¦+ MFA        ¦
¦             ¦             ¦             ¦+ SSO        ¦
¦             ¦             ¦             ¦+ SLA        ¦
¦             ¦             ¦             ¦+ Priority   ¦
¦             ¦             ¦             ¦  support    ¦
+-------------+-------------+-------------+-------------¦
¦ [Sign up]   ¦ [Start free]¦[Start free] ¦[Contact]    ¦
¦             ¦             ¦[Most popular]¦ sales       ¦
+-------------------------------------------------------+
`

Above the table:
- Headline: "Simple pricing. Per-tenant, no per-mailbox fees."
- Toggle: [ Monthly | Annual ] (Annual saves ~16%).
  State persists in orvix_pricing_cycle localStorage.
- Below the table: feature comparison table (the same as in §2.7).

Below the table:
- "All plans include" row:
  - 99.9% uptime SLA (Starter / Business), 99.99% (Enterprise).
    **Honest claim**: SLA is contractual, not enforced by the runtime
    in v1.0 — but it is in the contract.
  - Email support (Starter / Business), priority support (Enterprise).
  - TLS 1.3, DNSSEC-aware DNS verification.
- "Frequently asked" section:
  - **Can I change plans later?** Yes, from Organization ? Billing.
  - **How does billing work?** Monthly or annual. The current period
    is charged at signup. The next period is charged on the period
    end. Proration on upgrade: **Requires backend implementation**;
    v1.0 charges the new plan in full at the next renewal and lets
    the operator use the new plan immediately.
  - **Do you offer refunds?** See §2.12 Refund flow.
  - **Is there a free trial?** The Free plan IS the free tier; no
    credit card required. (Trials for v1.0: **Requires backend
    implementation**.)

### 2.7 Comparison table (under the pricing table)

A long horizontal table with one row per feature. Columns are plans.
Cells show check / dash / "Available on X" copy. Features are taken
**only** from the Features field of the plans table in §0.4.
**No invented features.**

### 2.8 Docs (/docs)

Landing page that links to docs.orvix.email (which is the repo's
docs/customer/index.md). Each link is a card with a one-line
description:

- Getting started
- Manage your organization
- Add a domain & verify DNS
- Create mailboxes
- Manage aliases & groups
- Email client setup
- Security (MFA, sessions)
- Quotas & abuse
- API reference
- Troubleshooting
- FAQ

The docs site itself is the existing docs/customer/*.md files
served as Markdown. A future "v1.0" docs site is **Requires backend
implementation** (or a static site generator). v1.0 ships the
Markdown and serves it from a docs.* subdomain. (Requires
docs.* DNS configuration — **Requires backend / infra implementation**;
for v1.0 the docs site lives at the same host as the marketing site
under /docs.)

### 2.9 API (/api)

A one-pager for developers. Lists the public surface (§0.1) and the
customer-portal surface (§0.2) as bullet groups, with sample curl
calls. Links to the per-endpoint reference at /docs/api. The
per-endpoint reference is **Requires backend implementation**; for
v1.0, the docs site carries a hand-written OpenAPI snippet.

**Honest claim:** the customer portal does not yet ship a v1.0
"create API token" flow. The existing /api/v1/api-keys endpoints
are admin-only. **A documented v1.0 customer-facing API token
surface is Requires backend implementation**; the API page lists
this gap explicitly with a contact link.

### 2.10 Status (/status)

Static page. The current v1.0 status check is the /api/v1/health
endpoint. The page itself is the same design as the docs index but
links to status.orvix.email (which is **Requires backend / infra
implementation**; v1.0 ships a static page that says "Subscribe for
uptime alerts — contact us" with a mailto).

The page shows:
- The last 7 days of "All systems operational" (manually updated for
  v1.0; future **Requires backend implementation** for live
  status).
- Three service tiles: Mail delivery, Webmail, Billing.
- A "Subscribe" mailto: status-subscribe@orvix.com.

### 2.11 About / Contact / Blog

- **About** (/about): Two paragraphs about Orvix. **No fake
  founder names, no fake HQ address** — the company is real, but
  the v1.0 spec uses generic copy: "Orvix is a managed email
  platform built by mail engineers. We run the mail server, you
  run your business." Contact link to hello@orvix.com.
- **Contact** (/contact): A small form. **Requires backend
  implementation** for the form submission (no backend endpoint
  exists). v1.0 fallback: the form POSTs to a mailto, the page
  shows "Send us an email at hello@orvix.com."
- **Blog** (/blog, /blog/:slug): Two seeded blog posts:
  "Welcome to Orvix" and "Why we built Orvix on a single
  managed runtime." Both are plain Markdown rendered to static
  HTML at build time. **CMS for additional posts is Requires
  backend implementation**; for v1.0 the marketing site has a
  hard-coded list of two posts.

### 2.12 Refund flow (cross-page)

The pricing page links to a /legal/refunds page. v1.0 supports
**manual refunds only**, by the Orvix team through support. The
flow:

1. User emails illing@orvix.com with their tenant ID and reason.
2. Orvix team confirms the refund amount.
3. Refund processed via the payment provider. **Requires
   backend implementation** for in-portal self-serve refund;
   the portal will surface "Contact billing@orvix.com for
   refund" for v1.0.

The pricing page shows the policy:

> "Refunds within 14 days of a new or renewal charge are available
> on request. Email illing@orvix.com with your tenant ID.
> Mid-cycle downgrades are pro-rated at the next renewal — you keep
> the higher tier features until then."

The 14-day rule is **Requires backend implementation** to
enforce automatically in v1.0; it is a documented policy the
team follows manually.

### 2.13 Legal pages (cross-page)

Each is plain Markdown, served as static HTML. The copy is real
and not invented — see docs/legal/ for the actual text
(**Requires backend implementation**; for v1.0 the legal team
supplies the text and it is checked in).

The pages are:

- **Terms of service** — Standard SaaS terms, governing law,
  acceptable use link. **Requires backend implementation** for
  the "last updated" date to be auto-set; for v1.0 the date is
  set at deploy time.
- **Privacy policy** — What data is collected, retention, GDPR
  rights, contact DPO dpo@orvix.com.
- **Acceptable use policy** — No spam, no phishing, no crypto
  abuse, no illegal content. Backed by the existing
  buse.RateLimitService and buse.SignalService.
- **GDPR statement** — Lawful basis, data subject rights (export,
  delete), DPO contact, supervisory authority. The "export" link
  in the portal is **Requires backend implementation**; the
  portal shows the email-to-DPO path for v1.0.
- **Cookie policy** — A list of every orvix_* key. No third-party
  trackers. (Matches §2.1.)
---

## 3. Customer Portal (Post-login)

The portal lives at `app.orvix.com`. It is the same SPA host as the
admin app but is gated on role `user`, `readonly`, `operator`, or
`admin` (not `superadmin` â€” staff use the admin console, not the
portal). The portal SPA loads `release/portal/app.js` (a
**Requires backend implementation** deliverable; v1.0 ships
the same admin SPA at `/portal` with a customer-mode flag).

### 3.1 Shared portal chrome (every portal page)

- **Top bar (left to right):**
  Logo (links to `/#/dashboard`). Org name (links to
  `/#/organization`). Search field (placeholder "Search mailboxes,
  domains, members..."; submits `?q=...`). Right side: Help link
  (mailto `support@orvix.com`), Bell (Notifications panel â€” see
  Â§3.16), Profile menu (Profile, Sessions, Sign Out).
- **Left sidebar (collapse to icons):**
  Section "Manage":
  - Dashboard (`#/dashboard`)
  - Organization (`#/organization`)
  - Members (`#/members`)
  - Invitations (`#/invitations`) â€” shows pending count badge

  Section "Domain":
  - Domains (`#/domains`)
  - DNS Wizard (`#/domains/:id/dns`)
  - Mailboxes (`#/mailboxes`)
  - Aliases (`#/aliases`)
  - Groups (`#/groups`)

  Section "Account":
  - Billing (`#/billing`)
  - Invoices (**Requires backend implementation**; placeholder)
  - Usage (`#/usage`)
  - API Keys (**Requires backend implementation**; placeholder)

  Section "You":
  - Profile (`#/profile`)
  - Notifications (`#/notifications`)
  - Security (`#/security`) â€” password, sessions, MFA
  - Audit (`#/audit`) â€” **Requires backend implementation**;
    v1.0 shows "Audit log is available in the Admin console"
  - Support (`#/support`)

- **Footer (in-app):** Version string (`v1.0.0`), language switcher
  (English / Ø§Ù„Ø¹Ø±Ø¨ÙŠØ©), copyright.

- **Top banner** (when applicable):
  - **Trial banner** (`trialing`): "You have N days left in your
    free tier." with a `Upgrade` button â†’ `/#/billing`.
  - **Past-due banner** (`past_due`): "Your last payment failed.
    Update your payment method to keep your service active." with
    `Update payment` â†’ `/#/billing`.
  - **Grace period banner** (`grace_period`): "Your service is in
    grace period. Mail is still being delivered but will be
    suspended on YYYY-MM-DD." with `Update payment` â†’ `/#/billing`.
  - **Suspended banner** (`suspended`): Full-page block on every
    route. "Your service is suspended. Update your payment method
    to restore." with `Update payment` â†’ `/#/billing`.
  - **Deletion pending banner**: "Account deletion requested for
    YYYY-MM-DD. Cancel deletion" â†’ `POST /api/v1/enterprise/deletion/cancel`.

### 3.2 Dashboard (`/#/dashboard`)

**Source endpoint:** `GET /api/v1/enterprise/dashboard`
returns `CustomerDashboard` (`internal/admin/dashboard/types.go`):

```
{
  total_domains, healthy_domains, domains_needing_attention,
  total_mailboxes, active_mailboxes, suspended_mailboxes,
  disabled_mailboxes, quota_used_bytes, recent_actions[]
}
```

Layout (top to bottom):

1. **Welcome / status strip** (1 row):
   - Left: "Welcome back, {first_name}."
   - Right: "Org: {org.name} Â· Plan: {plan.name}" + a
     `Change plan` button â†’ `/#/billing`.

2. **KPI strip** (4 cards):
   - **Domains** â€” `{total_domains}` total, `{healthy_domains}`
     healthy (green if equal). Subtext: click to `/#/domains`.
   - **Mailboxes** â€” `{active_mailboxes} active` /
     `{total_mailboxes} total`, suspended as grey secondary.
   - **Storage used** â€” `fmtBytes(quota_used_bytes)` of plan limit.
     Bar fills based on the ratio. Colour: green if < 60 %,
     amber at 60â€“80 %, red at 80 %+.
   - **Recent actions** â€” last 5 from `recent_actions[]`; each row
     shows `{action} {target} {timestamp}`. Click to `/#/audit`
     (**Requires backend implementation** for the page, but the
     card itself works).

3. **Health strip** (one row, four badges):
   - DNS verified badge: green if all domains verified, amber if
     some pending. Pulled from the domain rows (`/customer/domains`).
   - Trial / billing badge: shows the subscription state and the
     days remaining or the next renewal date.
   - Abuse signals badge: green if no active abuse signals; amber
     if any. Pulled from `/api/v1/enterprise/abuse/signals` (count
     of `status=open`).
   - Send limit badge: shows today's sends out of
     `plan.send_limit_day`. Pulled from
     `/api/v1/enterprise/abuse/send-limit` (or the usage endpoint;
     the data is the same).

4. **Onboarding checklist** (shown only when incomplete):
   - [ ] Add your first domain â†’ `/#/domains` (`Add domain`).
   - [ ] Verify DNS â†’ `/#/domains/:id/dns` for the unverified
     domain.
   - [ ] Create your first mailbox â†’ `/#/mailboxes`
     (`Add mailbox`).
   - [ ] Invite a teammate â†’ `/#/invitations`
     (`Invite member`).
   - [ ] Turn on MFA â†’ `/#/security` (`Set up MFA`).
   - Each item shows a check icon when complete. The list is
     hidden once all items are done.

5. **Recent activity feed** â€” last 10 entries from
   `recent_actions[]`. Each row: actor, action, target, time
   (relative: "2 minutes ago", "yesterday"). Click to `/#/audit`
   (**Requires backend implementation**; the card itself works).

6. **Quick links** (footer of dashboard): `+ Add domain`,
   `+ Add mailbox`, `Invite member`, `View billing`, `Open
   Webmail` (links to `webmail.orvix.com/inbox`).

Empty states:

- If `total_domains == 0` and `total_mailboxes == 0`:
  "Welcome! Let's get you set up." + `Start setup` button â†’
  Onboarding Wizard (Â§4).
- If `total_domains > 0` but no mailboxes: "No mailboxes yet.
  Create your first mailbox to start sending and receiving."
- If `total_mailboxes > 0` but `domains_needing_attention > 0`:
  "N domain(s) need attention." link to `/#/domains`.

### 3.3 Organization (`/#/organization`)

Read-only org profile. **Source:** `GET /api/v1/enterprise/organizations/current`.

Fields:
- Org name (read-only, **Requires backend implementation** to
  rename in v1.0).
- Slug (read-only).
- Primary domain (read-only).
- Plan (link to `/#/billing`).
- Tenant ID (for support requests; copy-to-clipboard).
- Created at, Last updated at.

Sections (cards):
- **Plan & billing** â€” current plan, last invoice date
  (**Requires backend implementation** for the list), next
  renewal date. Button: `Change plan` â†’ `/#/billing`.
- **Contact** â€” billing email (read-only, **Requires backend
  implementation** to change). **Requires backend implementation**
  for the "Billing contact" form to update the row.
- **Danger zone** â€” `Request account deletion` button. Confirms via
  typed "delete my account" prompt. Calls
  `POST /api/v1/enterprise/deletion`. The row's
  `cancelled_at`-equivalent is set, and the deletion sweep is
  handled by the billing scheduler (**Requires backend
  implementation** for the actual data removal; v1.0 marks the
  org as deleted and stops new logins after 7 days).

### 3.4 Members (`/#/members`)

**Source:** `GET /api/v1/enterprise/members`.

Layout:
- Toolbar: `+ Invite member` button â†’ opens the Invite modal
  (Â§3.5).
- Table columns: Avatar+Name, Email, Role, Last active
  (**Requires backend implementation**; shows "Never" for v1.0),
  Actions.
- **Role chips** (read-only display, with the current colour code):
  - `Owner` â€” single user; the only one who can transfer ownership.
  - `Admin` â€” full tenant admin except ownership transfer.
  - `Operator` â€” read + suspend/enable + password reset.
  - `User` â€” default mailbox-user role.
  - `Readonly` â€” read-only auditor.
- **Actions per row:**
  - `Change role` â€” opens a small select. Calls
    `PATCH /api/v1/enterprise/members/:id/role`.
  - `Reset password` (**Requires backend implementation** â€” the
    portal does not yet ship a customer-facing "reset another
    member's password" action; admin console has it). v1.0 hides
    this action for non-admin roles and shows a tooltip "Ask the
    member to reset their password from `/forgot`."
  - `Sign out all sessions` (**Requires backend implementation**;
    v1.0 hides this action).
  - `Remove from org` â€” for everyone except yourself and the owner.
    Calls `DELETE /api/v1/enterprise/members/:id`. Asks the
    operator to type the member's email to confirm.

**Empty state:** "No members yet. Invite your first teammate to get
started." with `Invite member` button.

**Filters:** by role (chips: All, Owner, Admin, Operator, User,
Readonly), by status (Active, Invited), by name (search).

**Pagination:** 25 / 50 / 100. Server-driven via `?page=` and
`?limit=`. **Requires backend implementation** if not already
present; v1.0 ships with the existing handler and adds the
client-side pagination UI.

### 3.5 Invitations (`/#/invitations`)

**Source:** `GET /api/v1/enterprise/invitations`,
`POST /api/v1/enterprise/invitations`,
`POST /api/v1/enterprise/invitations/:id/revoke`.

Layout:
- Toolbar: `+ Invite member` button.
- Table columns: Email, Role, Status, Invited by, Expires,
  Actions.
- **Status chips:**
  - `Pending` â€” neutral. Row shows `Revoke` button.
  - `Accepted` â€” green. Row is read-only.
  - `Revoked` â€” grey. Row is read-only.
  - `Expired` â€” red. Row is read-only.
- **Invite modal** (opened by `+ Invite member`):
  - Field: Email (required, validated as RFC 5322).
  - Field: Role (select: Admin, Operator, User, Readonly).
    Default: User. **Owner cannot be invited;** only transferred
    via Â§3.6.
  - Field: Personal message (textarea, optional, max 500 chars).
  - Validation errors:
    - Empty email â†’ "Email is required."
    - Invalid email format â†’ "Enter a valid email address."
    - Email already in members â†’ "This person is already a member."
    - Email already has a pending invite â†’ "This person already
      has a pending invitation."
  - Submit: `Send invitation`. Shows success toast
    "Invitation sent to {email}."
- **Empty state:** "No invitations. Click `Invite member` to add
  your first teammate."

### 3.6 Ownership transfer (`/#/organization/ownership`)

**Source:** `POST /api/v1/enterprise/ownership/{request,accept,cancel}`.

Two-step:
1. Owner clicks `Transfer ownership`. Selects the new owner from
   the current members list (only `admin` role can receive
   ownership). Confirms via typed "transfer ownership" prompt.
   Calls `POST /api/v1/enterprise/ownership/request`.
2. The new owner receives an in-app banner (and an email â€” see
   Â§5). They accept via a banner button "Accept ownership" â†’
   `POST /api/v1/enterprise/ownership/accept`. The current owner
   becomes `admin`. The new owner becomes `owner`.

Cancel: from either side via `POST /api/v1/enterprise/ownership/cancel`.

**This page is only visible to the current owner.** The route
`/#/organization/ownership` is in the "Owner tools" sub-menu of
Organization.

### 3.7 Billing (`/#/billing`)

This is the long one. It owns the pricing UX (Â§5) and the
subscription-state UX (Â§0.5).

**Source endpoints:**
- `GET /api/v1/enterprise/billing/subscription` â€” current sub row.
- `GET /api/v1/enterprise/billing/usage` â€” current period usage.
- `GET /api/v1/billing/plans` â€” plan catalog (cached client-side).
- `POST /api/v1/enterprise/billing/subscription` â€” change plan.

Layout:

1. **Top card: current plan**
   - "You are on the **{Plan name}** plan." with `Change plan` and
     `Cancel subscription` buttons.
   - Next renewal date (or "Renews on the 1st of every month" for
     monthly).
   - Monthly / annual toggle.
   - If `cancelled_at` is set: "Your subscription is set to cancel
     on YYYY-MM-DD. You can re-activate any time before then." with
     `Re-activate` button.

2. **Payment method** â€” **Requires backend implementation** for
   the card-update flow. v1.0 surface: "Card on file: VISA ending
   in 4242 Â· expires 12/27" (placeholder; real data **Requires
   backend implementation**). `Update card` button links to
   `/#/billing/payment-method` (**Requires backend implementation**;
   v1.0 shows "Contact `billing@orvix.com` to update your card").

3. **Invoices** â€” table of past invoices. **Requires backend
   implementation** for the list. v1.0 placeholder: "Invoice list
   is available on request from `billing@orvix.com`."

4. **Current usage** â€” `GET /api/v1/enterprise/billing/usage`:
   - Mailboxes used / allowed (`max_mailboxes`).
   - Storage used / allowed (`StorageMB`).
   - Domains used / allowed (`max_domains`).
   - Sends this period / daily limit.
   - A small "X days left in billing period" label.

5. **Plan comparison** â€” Same 4-column table as the marketing
   pricing page (Â§2.6). The current plan column is highlighted.

6. **Danger zone** â€” `Cancel subscription` button. Confirms via
   typed "cancel my subscription" prompt. Sets
   `subscription.cancelled_at` to now; the subscription remains
   active until `current_period_end`.

7. **Refund link** â€” "Need a refund? Email
   `billing@orvix.com` within 14 days of any charge."

### 3.8 Invoices (`/#/billing/invoices`)

**Requires backend implementation.** v1.0 placeholder page that
says "Invoice history is available on request from
`billing@orvix.com`."

### 3.9 Usage (`/#/usage`)

**Source:** `GET /api/v1/enterprise/billing/usage`.

Cards (each a quota progress widget):

- **Mailboxes**: `used / max`. Bar fills; amber at 80 %,
  red at 100 %. (Backed by `coremail_mailboxes` count + the
  `Subscription` plan row.)
- **Storage**: `fmtBytes(used_bytes) / fmtBytes(StorageMB)`. Same
  bar logic.
- **Domains**: `used / max`. Same.
- **Daily send rate**: live count from
  `abuse.RateLimitService` against the plan's `send_limit_day`.
  Shows today's window (rolling 24 h). Bar with the same
  thresholds.

A line graph (**Requires backend implementation** for
time-series; v1.0 shows the current day's bar) below the cards.

Empty state: "No usage yet. Once you start sending and receiving,
your usage will appear here."

### 3.10 Domains (`/#/domains`)

**Source:** `GET /api/v1/customer/domains` (or
`/api/v1/enterprise/domains` for write).

Layout:
- Toolbar: `+ Add domain` button.
- Table columns: Domain, Status, Mailboxes, MX, SPF, DKIM, DMARC,
  Last verified, Actions.
- **Status chips:** `Verified` (green), `Pending verification`
  (amber), `Failed` (red), `Suspended` (grey).
- **DNS posture columns:** each cell shows a tiny badge
  (`verified` green, `missing` red, `not_checked` grey). These
  come from the customer's DNS verify result.
- **Per-row actions:**
  - `Verify now` â€” calls `POST /api/v1/customer/domains/:id/verify`.
    Shows a spinner. On completion, the row updates.
  - `Open DNS wizard` â†’ `/#/domains/:id/dns`.
  - `Suspend` (only if status = active) â€” confirms via typed
    domain name. Calls
    `POST /api/v1/enterprise/domains/:id/status` with
    `status: suspended`. Suspended domains stop accepting mail
    but the row stays for re-activation.
  - `Activate` (only if status = suspended) â€” same endpoint,
    `status: active`.
  - **Domain removal is admin-only.** v1.0 portal does not
    expose a removal action; the row is marked suspended and
    the admin console handles permanent removal.

**Empty state:** "You haven't added a domain yet. Click
`+ Add domain` to start." with the `Add domain` CTA.

**Add domain modal:**
- Field: Domain (e.g. `example.com`). Validation: must be a
  valid DNS name, must not already exist in any tenant.
- Error states:
  - Empty / malformed â†’ "Enter a valid domain (e.g. example.com)."
  - Already taken â†’ "This domain is already configured. Contact
    support@orvix.com to migrate."
- Submit: `Add`. The page navigates to
  `/#/domains/:id/dns` (the wizard) immediately. Calls
  `POST /api/v1/enterprise/domains`.

### 3.11 DNS Wizard (`/#/domains/:id/dns`)

**Source:** `GET /api/v1/customer/domains/:id/dns`.

This is the most important onboarding screen. It is **wizard-style**
with 4 steps (record types):

1. **MX** â€” "Add an MX record pointing to
   `mx.orvix.com` (priority 10)." With a copy-to-clipboard
   button. Side panel shows the current MX posture
   (`verified` / `missing` / `not_checked`).
2. **SPF** â€” "Add a TXT record at `@` with value
   `v=spf1 include:_spf.orvix.com -all`." Same side panel.
3. **DKIM** â€” "Add a TXT record at
   `<selector>._domainkey.example.com` with value
   `<public-dns-txt>`." The selector and the public DNS TXT
   are pulled from the domain row (**Requires backend
   implementation** to expose the per-tenant DKIM selector; v1.0
   uses a single default selector `orvix1`). Side panel.
4. **DMARC** â€” "Add a TXT record at `_dmarc.example.com` with
   value `v=DMARC1; p=quarantine; rua=mailto:dmarc-reports@orvix.com`."
   (The rua address is a **Requires backend implementation** in
   the sense of "configurable per-tenant"; v1.0 ships a single
   shared address.) Side panel.

Each step has:
- A "I have added this record" button that triggers a
  `POST /api/v1/customer/domains/:id/verify`.
- A "Copy value" button (clipboard).
- A "Skip for now" link (logged as `pending` in the side panel;
  **verification still runs**).
- A "Back" / "Next" pair.

The wizard **runs verification in the background** even while
the user moves between steps. Each step's side panel updates as
verification completes.

Final step: "All records detected. Your domain is verified."
+ `Continue to mailboxes` button â†’ `/#/mailboxes`.

If verification fails, the wizard **does not block**; it shows
"Verification pending â€” records not yet visible from public DNS"
with a `Verify again` button. The domain stays in
`pending verification` until the operator resolves the issue or
7 days pass.

Empty state: "Select a domain to configure its DNS records."

### 3.12 Mailboxes (`/#/mailboxes`)

**Source:** `GET /api/v1/enterprise/mailboxes`.

Layout:
- Toolbar: `+ Add mailbox` button.
- Filter row: by status (Active, Suspended, Disabled), by
  domain (multi-select from the domain list), by storage usage
  (Above 80 %, Above 50 %, All).
- Table columns: Avatar+Name, Email, Domain, Status, Storage used,
  Last login (**Requires backend implementation**; v1.0 shows
  "Never"), Actions.
- **Status chips:** `Active` (green), `Suspended` (amber),
  `Disabled` (red), `Pending` (grey â€” for mailboxes created via
  invite that haven't accepted yet).
- **Per-row actions:**
  - `Reset password` (admin only) â†’ opens password-reset modal
    (Â§3.12.1).
  - `Suspend` / `Enable` (admin only) â†’ calls
    `POST /api/v1/enterprise/mailboxes/:id/status`.
  - `Edit quota` (admin only) â†’ opens quota modal. **Requires
    backend implementation**; v1.0 shows "Contact
    support@orvix.com to change the quota."
  - `Delete` (admin only) â€” confirm via typed email address.
    Calls `DELETE /api/v1/enterprise/mailboxes/:id`.
- **Bulk actions:** when rows are selected, a sticky action bar
  appears: `Suspend selected`, `Enable selected`. Bulk removal
  is admin-only and **Requires backend implementation**; v1.0
  exposes per-row actions only.

**Empty state:** "No mailboxes yet. Click `+ Add mailbox` to add
your first."

**Add mailbox modal:**
- Field: Display name (required).
- Field: Local part (auto-suggested from the user's email, editable).
- Field: Domain (select from tenant's domains).
- Field: Quota (default 1 GB for Free / Starter, 5 GB for
  Business, 50 GB for Enterprise â€” **Requires backend implementation**
  to make the default plan-aware; v1.0 uses a static 1 GB).
- Field: Initial password (auto-generated, copyable, can be
  regenerated).
- Checkbox: `Send welcome email with sign-in instructions` (on
  by default).
- Validation errors:
  - Empty name â†’ "Display name is required."
  - Empty local part â†’ "Local part is required."
  - Local part contains `@` / spaces â†’ "Local part must not
    contain `@` or spaces."
  - Email already exists â†’ "A mailbox with that email already
    exists in this domain."
  - Domain at quota â†’ "This domain has reached its mailbox
    limit. Remove a mailbox or upgrade your plan."
- Submit: `Create mailbox`. Calls
  `POST /api/v1/enterprise/mailboxes`. On success, the modal
  closes, the table refreshes, and a success toast shows "Welcome
  email sent to {email}." If the welcome email is unchecked,
  the modal stays open showing the initial password in a
  copyable box.

#### 3.12.1 Password reset modal

- Confirm via typed email address.
- New password is auto-generated, copyable, can be regenerated.
- Submit: `Reset password`. Calls
  `POST /api/v1/enterprise/mailboxes/:id/reset-password`.
- Success toast: "Password reset. The new password is shown above
  â€” copy it now, it is not stored in plain text." (The handler
  actually does store a hash; the modal copy is the only plaintext
  view.)

### 3.13 Aliases (`/#/aliases`)

**Source:** `GET /api/v1/enterprise/aliases`,
`POST /api/v1/enterprise/aliases`,
`DELETE /api/v1/enterprise/aliases/:id`.

Layout:
- Toolbar: `+ Add alias` button.
- Table columns: Alias, Forwards to, Status, Created, Actions.
- Aliases are scoped to a domain. Filter by domain.

**Add alias modal:**
- Field: Alias (local part, e.g. `hello`).
- Field: Domain (select from tenant's domains).
- Field: Forwards to (chips of mailbox emails; multi-select).
  The list of mailboxes comes from `GET /enterprise/mailboxes`
  (filtered to active).
- Validation:
  - Empty local part â†’ "Local part is required."
  - Empty forwards â†’ "Select at least one target."
  - Self-forward loop (alias forwards to itself) â†’
    "An alias cannot forward to itself."
- Submit: `Create alias`.

### 3.14 Groups (`/#/groups`)

**Source:** `GET /api/v1/enterprise/groups`,
`POST /api/v1/enterprise/groups`,
`POST /api/v1/enterprise/groups/:id/members`,
`DELETE /api/v1/enterprise/groups/:id/members/:memberId`,
`DELETE /api/v1/enterprise/groups/:id`.

Layout:
- Toolbar: `+ Add group` button.
- Table columns: Group name, Members, Created, Actions.
- **Per-row expand:** shows the member list (name + email +
  Remove).
- **Add group modal:** name (required, unique), description
  (optional). Submit creates the group, then opens the
  "Add members" multi-select picker.

### 3.15 API Keys (`/#/api-keys`)

**Requires backend implementation.** v1.0 placeholder page:
"Customer API keys are not yet available. Contact
support@orvix.com for early access."

### 3.16 Notifications (`/#/notifications`)

**Source:** the in-app notification stream is fed by backend
events. **Requires backend implementation** for a customer-facing
notification stream; v1.0 ships an empty-state page that says
"Notifications will appear here when important things happen on
your account (e.g. a new member joins, a domain fails DNS
verification)."

The webhook for in-app notifications is the existing
`/api/v1/billing/webhook` for billing events plus
`coremail_audit` for admin events; surfacing them in a customer
notification center is the missing piece.

Empty state: "No notifications yet."

### 3.17 Profile (`/#/profile`)

**Source:** `GET /api/v1/me`.

Layout:
- Avatar (initials in a circle â€” **Requires backend
  implementation** for image upload; v1.0 shows initials only).
- Display name (read-only; **Requires backend implementation**
  for the rename; v1.0 shows "Contact support to change your
  name.").
- Email (read-only; **Requires backend implementation** for
  the change; v1.0 shows the same).
- Role (chip).
- Org name (link to `/#/organization`).
- Member since (date).

### 3.18 Security (`/#/security`)

Three sub-pages reachable from a tab strip on the security page:

- **Password** (`/#/security/password`):
  - Field: Current password.
  - Field: New password.
  - Field: Confirm new password.
  - Strength meter (same rules as signup, Â§4.1).
  - Submit: `Change password`. Calls
    `POST /api/v1/auth/change-password`.
  - "Forgot your password?" link to `/forgot`.

- **Sessions** (`/#/security/sessions`):
  - Table: Device, IP, Location (**Requires backend
    implementation** for the geolocation; v1.0 shows "â€”"),
    Last active, Current.
  - Each row has `Sign out` button
    (`POST /api/v1/auth/logout`) except the current session.
  - Footer action: `Sign out of all sessions` â†’
    `POST /api/v1/auth/logout-all`. Confirms via typed "sign
    out everywhere".
  - Empty state when only one session: "You are signed in from
    one device."

- **MFA** (`/#/security/mfa`):
  - **Honest state:** "TOTP MFA is supported on the admin
    console for tenant admins. The customer portal will support
    end-user MFA in a future release."
  - **Requires backend implementation** for
    customer-side TOTP enrollment. v1.0 shows the status block
    (configured / not configured) but the `Set up MFA` button
    is disabled with the tooltip "Contact your admin to set up
    MFA."

### 3.19 Support (`/#/support`)

Static page. Sections:
- **Help center** â€” links to `/docs`.
- **Email** â€” `support@orvix.com` mailto.
- **Status** â€” link to `/status`.
- **Report a bug** â€” `bugs@orvix.com` mailto.
- **Request a feature** â€” `feedback@orvix.com` mailto.

No chat widget, no ticket form. v1.0 is email-driven support.

### 3.20 Sign out

- Click `Sign Out` in the profile menu.
- Confirm via modal "Sign out? You will need to sign in again
  to access the portal."
- `Sign out` calls `POST /api/v1/auth/logout`. On success,
  redirect to `/#/login` (or the marketing `/login` which
  redirects to `app.orvix.com/login`).
- Banner on login page: "You have been signed out."

---

## 4. Signup, Login, Forgot, Reset

### 4.1 Signup (`/signup`)

**Source:** `POST /api/v1/auth/signup` (handler in
`internal/api/handlers/customer_auth.go`).

**Important backend behavior to mirror:**

The signup handler:

- Validates email format (RFC 5322).
- Enforces `passwordStrength`: 8+ characters, at least one
  uppercase, one lowercase, one digit.
- Looks up the existing user by email; returns 409 Conflict if
  the email already exists.
- Begins a transaction; inserts the `tenants` row, the `users`
  row (with `role: user`, `active: true`, `email_verified: true`),
  and the `subscriptions` row (`plan_id: free`, interval monthly,
  no trial).
- Sets the HttpOnly session cookie on success.

**Frontend flow** (`/signup`):

1. **Page** (1 column, no top nav beyond logo + "Sign in" link).
   - Headline: "Create your Orvix account."
   - Subhead: "Free tier, no credit card required."
2. **Form fields:**
   - **Email** (required, type=email, autocomplete=email).
   - **Password** (required, type=password,
     autocomplete=new-password, with show/hide eye toggle).
     Strength meter below the input (5-segment bar, no colour
     until 8+ chars; turns green at "Strong").
   - **Confirm password** (required, type=password).
   - **Organization name** (required, free text, placeholder
     "Acme Inc."). Used to derive the tenant slug.
   - **Terms of service** checkbox (required, "I agree to the
     [Terms of service] and [Privacy policy].").
3. **Validation errors** (all shown inline, with red 1px border on
   the offending field and a one-line message above the field):
   - Empty email / invalid email format.
   - Password shorter than 8 characters / missing uppercase /
     missing lowercase / missing digit.
   - Passwords don't match.
   - Empty organization name.
   - Terms not accepted.
   - **Email already taken** â†’ shows the error inline and a
     secondary link: "Sign in to your existing account." â†’
     `/login?email={email}`.
4. **Submit:** `Create account`. Disabled until valid. Shows
   spinner on submit.
5. **Success:** redirect to `/#/dashboard` (the portal). The first
   thing the operator sees is the Onboarding Wizard (Â§4.2)
   pinned to the top of the dashboard, plus a one-time
   success toast: "Welcome to Orvix. Let's set up your first
   domain."
6. **Errors:**
   - 5xx â†’ "We couldn't create your account. Please try again.
     If the problem persists, contact support@orvix.com."
   - Network error â†’ "Connection problem. Check your network and
     try again."

**Email verification:** the current backend auto-verifies
(`email_verified: true` on signup). The v1.0 frontend does **not**
show a "verify your email" page because the backend does not
require it. The signup-completion email (Â§5) is the single
welcome touchpoint; it is informational, not a verification gate.

**SSO signup:** **Requires backend implementation.** v1.0 does
not surface a "Sign up with SSO" button on `/signup`.

**Email enumeration prevention:** the 409 Conflict from the
backend is intentionally surfaced (otherwise the operator
cannot recover their account), but only after a
**minimum-friction delay** (200â€“400 ms server-side) so timing
attacks cannot enumerate accounts. The backend currently returns
409 synchronously; the frontend just displays the result
honestly with the recovery link.

### 4.2 Onboarding Wizard (post-signup, optional re-entry)

A 4-step wizard that lives at `/#/onboarding` (link from
dashboard "Onboarding checklist" or `Start setup` button).

**Source endpoints:** the wizard is **purely a flow** over the
existing endpoints â€” it does not introduce new ones.

#### Step 1 / 4 â€” Welcome

- Headline: "Welcome to Orvix."
- Subhead: "Let's get your mail working. This takes about 5
  minutes."
- "We will help you: 1) Add a domain 2) Verify DNS 3) Create
  your first mailbox 4) Invite a teammate."
- Buttons: `Get started` (primary), `Skip for now` (link).

#### Step 2 / 4 â€” Add a domain

This is a thinner version of the "Add domain" modal in Â§3.10.
- Field: Domain.
- Same validation.
- Submit: `Add domain` (calls
  `POST /api/v1/enterprise/domains`). On success, the wizard
  advances to step 3 and the new domain is the active row.

#### Step 3 / 4 â€” Verify DNS

A thinner version of the DNS Wizard in Â§3.11. All four record
types (MX, SPF, DKIM, DMARC) on one page with side panels.
Wizard "Next" only enables when MX is verified. The DKIM, SPF,
DMARC cards show the value + copy button + the same
"I have added this record" / "Verify again" pattern as Â§3.11.

If the operator cannot verify right now, they click `Verify
later`. The wizard still progresses to step 4; the domain
status stays `pending verification` and a banner on the
dashboard reminds them.

#### Step 4 / 4 â€” Create your first mailbox

A thinner version of Â§3.12. Display name, local part (pre-fills
`admin`), domain (pre-fills the just-added domain), initial
password (auto-generated), welcome email checkbox (on by
default). Submit calls `POST /api/v1/enterprise/mailboxes`.

On success:
- "Your mailbox is ready." with the new email and the initial
  password in a copyable box.
- "Open webmail" button â†’ `webmail.orvix.com/inbox`.
- "Invite a teammate" button â†’ `/#/invitations`.
- `Finish` button â†’ `/#/dashboard` (the dashboard, with the
  onboarding checklist hidden because it's done).

The wizard state is **client-side only** (no DB persistence). If
the operator closes the tab mid-wizard, the dashboard checklist
takes over and they can resume individual steps from the
relevant portal pages.

### 4.3 Login (`/login`)

**Source:** `POST /api/v1/auth/login` (handlers.go) â†’
`POST /api/v1/webmail/login` if the form is the webmail form.

**Two tabs on the login page** (toggle between them):

- **Admin / Portal login** (default): The form below. Logs the
  user into `app.orvix.com` (the portal + admin app).
- **Webmail login** (link: "Sign in to Webmail only"). Logs into
  `webmail.orvix.com` only. The form is identical but the submit
  target is `/api/v1/webmail/login`.

**Form fields:**
- **Email** (required, type=email, autocomplete=email).
- **Password** (required, type=password,
  autocomplete=current-password, with show/hide eye toggle).
- **Remember me** (checkbox, **Requires backend implementation**;
  v1.0 defaults to off, the session always lasts the same 30
  min idle TTL). Shown but disabled with a tooltip
  "Sessions are 30 minutes; sign in again to extend."
- **Submit:** `Sign in` (primary, full width).
- Below: `Forgot your password?` link â†’ `/forgot`.

**Errors:**
- Wrong credentials â†’ "Invalid email or password." (Same generic
  message regardless of whether the email exists â€” prevents
  enumeration.)
- Rate-limited (5 attempts in 15 min per IP) â†’ "Too many
  attempts. Please wait a few minutes and try again." (The
  backend enforces; the frontend displays the message verbatim.)
- 5xx â†’ "We couldn't sign you in right now. Please try again."

**MFA challenge:**
- If the user has MFA enabled, the backend returns an
  `mfa_challenge` field. The frontend then renders the MFA step:
  - "Enter your 6-digit verification code."
  - Field: 6-digit input (auto-advance between digits, paste
    support, autocomplete=one-time-code).
  - "Use a recovery code instead" link â†’ switches the input to
    an 8-char text field.
  - `Verify` button (disabled until 6 digits).
  - Errors: wrong code â†’ "Invalid code." locked out (after N
    attempts) â†’ "Too many attempts. Sign in again to retry."
- On success the backend sets the session cookie and returns
  the user to the previous page or the dashboard.

**SSO login button** (below the password form): "Sign in with
SSO." **Requires backend implementation** for the actual SSO
flow; v1.0 hides this button or shows it disabled with a tooltip
"Contact your admin to enable SSO for your organization."

### 4.4 Forgot password (`/forgot`)

**Source:** `POST /api/v1/auth/forgot-password` (rate-limited:
5 in 15 min per IP, same as login).

1. Field: Email.
2. Submit: `Send reset link`.
3. **Always** show the same success message regardless of
   whether the email exists â€” prevents enumeration:
   "If an account exists for {email}, we sent a reset link.
   Check your inbox."
4. Errors:
   - 429 â†’ "Too many requests. Please wait a few minutes."
   - 5xx â†’ "We couldn't process that right now. Please try
     again."

**Email content** (Â§5.4).

### 4.5 Reset password (`/reset?token=â€¦`)

**Source:** `POST /api/v1/auth/reset-password`.

1. The token is in the URL. The page reads it from
   `location.search`. If no token, show "Invalid or expired
   link" with `Request a new link` â†’ `/forgot`.
2. Form fields:
   - New password (with strength meter, same rules as signup).
   - Confirm new password.
3. Submit: `Reset password`.
4. **Success:** "Your password is reset. You can now sign in."
   `Sign in` button â†’ `/login` (pre-fill email if we know it
   from the token-decoded claim, **Requires backend
   implementation** to expose the email from the token; v1.0
   just redirects to `/login` without pre-fill).
5. **Errors:**
   - Invalid or expired token â†’ "This link is invalid or has
     expired. Request a new link."
   - Password policy violation â†’ inline error.

---

## 5. Email Lifecycle

Every email below has a real template. The template engine is
the existing transactional mail sender wired in
`internal/api/router.go` ("Wire transactional mail sender for
password resets"). The sender is provider-agnostic. For v1.0,
emails are queued through the same mail pipeline and delivered
from `noreply@orvix.com`.

Every email:

- Plain-text alternative + HTML body.
- Subject in the locale of the recipient (`Accept-Language`,
  falling back to `orvix_locale`).
- Reply-to: `support@orvix.com` for support emails,
  `billing@orvix.com` for billing, `security@orvix.com` for
  security.
- Footer with: company address, unsubscribe link (transactional
  emails are exempt from the unsubscribe requirement per
  common anti-spam law, but the link is included anyway for
  transparency), privacy and terms links.

### 5.1 Welcome (post-signup)

**Trigger:** successful `POST /api/v1/auth/signup`.
**To:** the new user's email.
**From:** `welcome@orvix.com`. **Reply-to:** `support@orvix.com`.

**Subject (en):** "Welcome to Orvix"
**Subject (ar):** "Ù…Ø±Ø­Ø¨Ø§Ù‹ Ø¨Ùƒ ÙÙŠ Orvix"

**Plain-text body:**

```
Hi {first_name},

Welcome to Orvix. Your account is ready.

What you can do right now:
- Add a domain and verify DNS â€” https://app.orvix.com/#/domains
- Create your first mailbox â€” https://app.orvix.com/#/mailboxes
- Invite a teammate â€” https://app.orvix.com/#/invitations

The free tier includes:
- 1 custom domain
- 5 mailboxes
- 1 GB of storage
- 500 messages per day
- DKIM signing
- Webmail

If you have any questions, reply to this email or contact
support@orvix.com.

â€” The Orvix team
https://orvix.com
```

**HTML body** mirrors the plain-text body with:
- Orvix logo.
- A single primary CTA "Go to your dashboard" â†’
  `https://app.orvix.com/#/dashboard`.
- A "What's next" three-icon strip (Domains, Mailboxes, Team).
- Footer with the support contact.

### 5.2 Verify email

**Not in v1.0.** The current backend auto-verifies the email at
signup. A verify-email flow is **Requires backend implementation**.

### 5.3 Forgot-password / reset link

**Trigger:** `POST /api/v1/auth/forgot-password`.
**To:** the requester's email (always, regardless of whether
the email exists â€” see Â§4.4).
**From:** `security@orvix.com`. **Reply-to:** `support@orvix.com`.

**Subject (en):** "Reset your Orvix password"
**Subject (ar):** "Ø¥Ø¹Ø§Ø¯Ø© ØªØ¹ÙŠÙŠÙ† ÙƒÙ„Ù…Ø© Ù…Ø±ÙˆØ± Orvix"

**Plain-text body:**

```
Hi,

We received a request to reset the password for the Orvix
account associated with this email address.

If you made this request, click the link below to set a new
password. The link expires in 1 hour.

https://app.orvix.com/reset?token={reset_token}

If you did not make this request, you can safely ignore this
email. Your password will not change.

â€” The Orvix team
https://orvix.com
```

**HTML body** mirrors with a primary CTA button "Reset your
password" â†’ the link above.

### 5.4 Reset password confirmation

**Trigger:** successful `POST /api/v1/auth/reset-password`.
**To:** the user's email.

**Subject (en):** "Your Orvix password was reset"
**Subject (ar):** "ØªÙ… Ø¥Ø¹Ø§Ø¯Ø© ØªØ¹ÙŠÙŠÙ† ÙƒÙ„Ù…Ø© Ù…Ø±ÙˆØ± Orvix Ø§Ù„Ø®Ø§ØµØ© Ø¨Ùƒ"

**Body:**

```
Hi,

Your Orvix password was just reset. If this was not you, please
contact security@orvix.com immediately.

When you sign in next, you will be asked to set a new password
(if you have not already done so on the reset page).

â€” The Orvix team
https://orvix.com
```

### 5.5 Invite user (organization invite)

**Trigger:** `POST /api/v1/enterprise/invitations`.
**To:** the invitee's email.
**From:** `invites@orvix.com`. **Reply-to:** the inviter's email
(falls back to `support@orvix.com`).

**Subject (en):** "{inviter_name} invited you to {org_name} on Orvix"
**Subject (ar):** "{inviter_name} Ø¯Ø¹Ø§Ùƒ Ù„Ù„Ø§Ù†Ø¶Ù…Ø§Ù… Ø¥Ù„Ù‰ {org_name} ÙÙŠ Orvix"

**Plain-text body:**

```
Hi,

{inviter_name} ({inviter_email}) has invited you to join
{org_name} on Orvix as a {role}.

Click below to accept. This invitation expires in 7 days.

https://app.orvix.com/invite?token={invite_token}

If you don't already have an Orvix account, you will be asked
to create one before accepting.

â€” {org_name} via Orvix
https://orvix.com
```

### 5.6 Organization invite (full org invite by owner)

**Trigger:** same as Â§5.5, but the inviter is the org owner and
the email content is identical. v1.0 ships a single template
that handles both. The "Role" field is always shown.

### 5.7 Payment success

**Trigger:** `POST /api/v1/billing/webhook` with `type:
invoice.paid` (or equivalent from the payment provider).
**To:** the org owner's email.
**From:** `billing@orvix.com`. **Reply-to:** `billing@orvix.com`.

**Subject (en):** "Payment received â€” thank you"
**Subject (ar):** "ØªÙ… Ø§Ø³ØªÙ„Ø§Ù… Ø§Ù„Ø¯ÙØ¹ â€” Ø´ÙƒØ±Ø§Ù‹ Ù„Ùƒ"

**Plain-text body:**

```
Hi,

We received your payment of ${amount} {currency} for the
{plan_name} plan on {org_name}.

A receipt is available on request from billing@orvix.com.
The next billing date is {next_billing_date}.

If you have any questions, reply to this email.

â€” Orvix Billing
https://orvix.com
```

**v1.0 limitation:** "A receipt is available on request" because
invoices are **Requires backend implementation** for
self-serve. v1.0 invoice emails include the plan and amount
only.

### 5.8 Payment failed

**Trigger:** `POST /api/v1/billing/webhook` with `type:
invoice.payment_failed` (or equivalent).
**To:** the org owner's email.

**Subject (en):** "Payment failed for {org_name}"
**Subject (ar):** "ÙØ´Ù„ Ø§Ù„Ø¯ÙØ¹ Ù„Ù€ {org_name}"

**Plain-text body:**

```
Hi,

We were unable to charge your card on file for the
{plan_name} plan on {org_name}.

To keep your service active, please update your payment
method within {grace_period_days} days.

Update your payment method:
  https://app.orvix.com/#/billing

If you need help, reply to this email.

â€” Orvix Billing
https://orvix.com
```

### 5.9 Trial ending (not in v1.0)

The v1.0 backend does not have a "trial" state distinct from
`trialing`. The free plan IS the free tier; the trial concept
is **Requires backend implementation**. v1.0 does not send a
trial-ending email.

### 5.10 Subscription renewed

**Trigger:** payment webhook on successful renewal of a paid
plan.
**To:** the org owner's email.

**Subject (en):** "Your {plan_name} plan was renewed"
**Subject (ar):** "ØªÙ… ØªØ¬Ø¯ÙŠØ¯ Ø®Ø·Ø© {plan_name} Ø§Ù„Ø®Ø§ØµØ© Ø¨Ùƒ"

**Plain-text body:**

```
Hi,

Your {plan_name} subscription on {org_name} was renewed
for another billing period. ${amount} {currency} was charged
to your card on file. The next renewal date is
{next_billing_date}.

Reply to this email if you have any questions.

â€” Orvix Billing
https://orvix.com
```

### 5.11 Subscription cancelled

**Trigger:** `POST /api/v1/enterprise/deletion` (operator
cancels their subscription) OR `subscription.cancelled_at` is
set via `POST /api/v1/enterprise/billing/subscription` with
`status: cancelled` (operator cancels in the portal).
**To:** the org owner's email.

**Subject (en):** "Your Orvix subscription is set to cancel"
**Subject (ar):** "ØªÙ… ØªØ¹ÙŠÙŠÙ† Ø§Ø´ØªØ±Ø§Ùƒ Orvix Ø§Ù„Ø®Ø§Øµ Ø¨Ùƒ Ù„Ù„Ø¥Ù„ØºØ§Ø¡"

**Plain-text body:**

```
Hi,

Your Orvix subscription on {org_name} is set to cancel on
{current_period_end}. You will keep full access to your
mailboxes, aliases, and groups until then.

To re-activate before that date:
  https://app.orvix.com/#/billing

If you have any questions, reply to this email.

â€” Orvix Billing
https://orvix.com
```

### 5.12 Quota warning

**Trigger:** when `usage.storage_used_mb / storage_mb >= 0.8`
or `usage.mailboxes_used / max_mailboxes >= 0.8` (the
`billing/billing.Scheduler` computes this; **Requires backend
implementation** to wire the scheduler to send the email).
**To:** the org owner's email.

**Subject (en):** "You're approaching your {plan_name} storage limit"
**Subject (ar):** "Ø£Ù†Øª ØªÙ‚ØªØ±Ø¨ Ù…Ù† Ø­Ø¯ Ø§Ù„ØªØ®Ø²ÙŠÙ† ÙÙŠ Ø®Ø·Ø© {plan_name}"

**Plain-text body:**

```
Hi,

You're at {pct}% of your {plan_name} storage limit
({fmtBytes(used)} of {fmtBytes(limit)}). Once you hit 100 %,
new mail will be rejected by the receiving server.

You can:
- Free up space by removing old messages or mailboxes.
- Upgrade your plan: https://app.orvix.com/#/billing

â€” The Orvix team
https://orvix.com
```

### 5.13 Quota reached

**Trigger:** `usage.storage_used_mb / storage_mb >= 1.0` (same
scheduler).
**To:** the org owner's email.

**Subject (en):** "Your {plan_name} storage is full"
**Subject (ar):** "ØªØ®Ø²ÙŠÙ† Ø®Ø·Ø© {plan_name} Ù…Ù…ØªÙ„Ø¦"

**Plain-text body:**

```
Hi,

Your {plan_name} storage on {org_name} is full. New mail sent
to your mailboxes is being rejected by the receiving server.

You can:
- Free up space immediately by removing old messages.
- Upgrade your plan: https://app.orvix.com/#/billing

â€” The Orvix team
https://orvix.com
```

### 5.14 Domain verified

**Trigger:** `POST /api/v1/customer/domains/:id/verify` returns
all four records (MX, SPF, DKIM, DMARC) as `verified`.
**To:** the org owner's email.
**From:** `domains@orvix.com`. **Reply-to:** `support@orvix.com`.

**Subject (en):** "Your domain {domain} is verified"
**Subject (ar):** "ØªÙ… Ø§Ù„ØªØ­Ù‚Ù‚ Ù…Ù† Ù†Ø·Ø§Ù‚Ùƒ {domain}"

**Plain-text body:**

```
Hi,

Your domain {domain} on {org_name} is now fully verified. Mail
sent to addresses on this domain will be delivered and will
pass SPF, DKIM, and DMARC checks.

If you have any questions, reply to this email.

â€” The Orvix team
https://orvix.com
```

### 5.15 Domain failed

**Trigger:** `POST /api/v1/customer/domains/:id/verify` returns
one or more records as `missing` or `mismatch`.
**To:** the org owner's email.

**Subject (en):** "Domain verification needs attention: {domain}"
**Subject (ar):** "Ø§Ù„ØªØ­Ù‚Ù‚ Ù…Ù† Ø§Ù„Ù†Ø·Ø§Ù‚ ÙŠØ­ØªØ§Ø¬ Ø¥Ù„Ù‰ Ø§Ù‡ØªÙ…Ø§Ù…: {domain}"

**Plain-text body:**

```
Hi,

We tried to verify the DNS records for {domain} on
{org_name} and found the following issue(s):

- {record_type}: {expected_value} (got: {actual_value or "not found"})

Open the DNS wizard to fix this:
  https://app.orvix.com/#/domains/{id}/dns

Until verification passes, mail sent to {domain} may be
rejected by other providers.

â€” The Orvix team
https://orvix.com
```

---

## 6. Pricing UX (flows)

The pricing UX lives in two places: the **marketing `/pricing`
page** (Â§2.6) and the **portal Billing `Change plan` flow** (Â§6.1).
The numbers and feature lists are the same in both places;
they are pulled from the same `GET /api/v1/billing/plans`
response.

### 6.1 Change plan (from the portal)

**Entry points:** `/#/billing` (`Change plan`), or
`/#/dashboard` (`Upgrade` button in the trial banner).

1. The portal renders the 4-column plan table with the current
   plan highlighted. `Monthly | Annual` toggle is preserved from
   the marketing site (`orvix_pricing_cycle` localStorage).
2. The operator clicks `Choose {Plan name}` on any other plan
   card.
3. A modal appears:
   - **Header:** "Switch to {Plan name} ({cycle})?"
   - **Body:**
     - "You are switching from {current_plan} to {new_plan} on a
       {monthly | annual} cycle."
     - "Prorated charge today: ${amount}." **Requires backend
       implementation** to compute proration; v1.0 charges the new
       full price immediately and credites the unused portion of
       the old plan at the end of the period.
     - "Effective immediately: your plan upgrades and the new
       limits take effect now."
     - **For downgrades:** "Effective on YYYY-MM-DD. You keep the
       higher limits until then." (computed from
       `current_period_end`).
   - Footer: `Cancel` and `Confirm switch`.
4. `Confirm switch` calls
   `POST /api/v1/enterprise/billing/subscription` with the new
   `plan_id` and `billing_interval`. The handler is responsible
   for the actual billing web hook; the v1.0 page does not assume
   payment success.
5. **Success:** "You're now on the {Plan name} plan." with a
   link to `/#/billing`. Banner is dismissed.
6. **Errors:**
   - 402 (payment required) â†’ "Your payment method was
     declined. Update your card and try again." with a link
     to `/#/billing`.
   - 409 (conflict) â†’ "Your plan was already changed by
     another tab. Refresh the page."
   - 5xx â†’ "We couldn't change your plan. Please try again."

### 6.2 Cancel flow

1. From `/#/billing`, click `Cancel subscription` (in the
   "Danger zone" card).
2. Confirm via modal:
   - "Are you sure you want to cancel?"
   - Body:
     - "Your subscription will end on {current_period_end}."
     - "After that date, your domain will be suspended and your
       mailboxes will stop receiving mail. You can re-activate
       any time before then."
     - "If you change your mind, click `Re-activate` from the
       Billing page before the end of your billing period."
   - Footer: `Keep my subscription` and `Cancel subscription`.
3. `Cancel subscription` calls
   `POST /api/v1/enterprise/billing/subscription` (or a dedicated
   cancel endpoint â€” **Requires backend implementation** to
   decide; the v1.0 portal reuses the subscription endpoint
   with a `status: cancelled` body which the existing handler
   already supports).
4. The Billing page updates to the "Subscription ends on
   {date}" state. The `Re-activate` button is shown.
5. **Re-activate** calls the same endpoint with `status: active`
   and the same plan. Confirmation modal: "Reactivate
   subscription? Your plan and limits will resume immediately."

### 6.3 Downgrade flow

A downgrade is a `Change plan` to a lower plan. The flow
follows Â§6.1 with the same proration caveat. The portal does
**not** block a downgrade if the tenant is over the new plan's
limits â€” instead, the dashboard surfaces a warning banner
"Mailboxes / domains / storage exceed your new plan's limits.
Remove some before {end_of_current_period} or upgrade again."

The actual enforcement (refusing to over-quota) is server-side
in `coremail`; the portal shows a clear, honest warning rather
than a block.

### 6.4 Refund flow

1. The pricing page and the Billing page both link to
   `/legal/refunds` (markdown, see Â§2.12).
2. **In v1.0** the only action is to email
   `billing@orvix.com` with the tenant ID and reason. The
   marketing page and the portal both surface this email
   address prominently.
3. **Requires backend implementation** for a self-serve refund
   request flow inside the portal. v1.0 placeholder page
   `/#/billing/refund` reads "Refund requests are processed
   by the Orvix team. Email `billing@orvix.com` with your
   tenant ID and the charge you want refunded."

### 6.5 Comparison table (refresher)

The four-column table on `/pricing` and on the Billing page is
generated from `GET /api/v1/billing/plans`. The `Features` field
is a JSON array of feature keys; the table renderer turns each
key into a check mark under the appropriate plan column. The
mapping is the same one used in Â§0.4.

For each plan, the table shows:

- Price (monthly / annual, with the Annual savings %).
- Domains / mailboxes / storage / sends per day.
- Features (check or dash).
- CTA button (`Start free`, `Start free`, `Start free` (with
  "Most popular" badge), `Contact sales`).

The table never invents a feature. If a feature is in the
JSON but the backend does not actually implement it, the row
is omitted from the table (not shown as "coming soon").

---

## 7. Visual Language

### 7.1 Reference aesthetic

Microsoft 365 for information density and the gravitas of
enterprise software. Google Workspace for clarity and the
breathing room around each surface. Linear for the polish of
controls, hover states, and shadows. Notion for the gentle
typography hierarchy and the way secondary information
de-emphasises without disappearing.

**Not** Slack (too playful), **not** Notion at 100 % saturation
(their primary blue is too saturated for serious admin work),
**not** Tailwind UI templates (too generic). The brand sits
between Google Workspace and Linear on the personality
spectrum.

### 7.2 Color

The current admin app already defines a working palette in
`release/webmail/assets/webmail.css`. v1.0 extends it; it does
**not** replace it. Hex values from the existing `:root`:

- `--bg-app: #0a0d14` (dark mode canvas)
- `--bg-canvas: #0d1118`
- `--bg-surface: #131822`
- `--bg-elevated: #1a2030`
- `--bg-raised: #232a3c`
- `--bg-hover: #1e2536`
- `--bg-active: #243049`
- `--bg-selected: #1d3258`
- `--accent: #5b9eff` (Orvix primary blue)
- `--accent-2: #7ab1ff`
- `--accent-3: #3d7be0`
- `--accent-soft: #1c2f55`
- `--accent-glow: rgba(91, 158, 255, 0.18)`
- `--success: #3ecf8e` (green)
- `--warning: #f0b429` (amber)
- `--danger: #ef4444` (red)
- `--muted: #8a93a6` (grey)

Light mode flips the surfaces to off-white. v1.0 ships a
`:root.theme-light` class that the marketing site and the
portal use when the user opts in (the toggle is in the
profile menu, persisted in `orvix_theme` localStorage).

**Primary CTA** uses `--accent` with white text.
**Secondary CTA** uses `--bg-elevated` with `--accent` text.
**Destructive CTA** uses `--danger` with white text, **and
only inside a `confirmDanger` modal** with typed confirmation.

**Honest colour rules** (must be enforced in design system):

- Green = confirmed, verified, healthy, active, success.
- Amber = pending, warning, awaiting action.
- Red = failed, suspended, blocked, danger.
- Grey / muted = disabled, archived, read-only.
- **Never** colour something red to grab attention.
  **Never** colour a destructive button unless it is the final
  confirmation step.

### 7.3 Typography

The current admin app uses:

- `--font-sans: "Segoe UI Variable", "Segoe UI", "SF Pro Text", â€¦`
- `--font-mono: "JetBrains Mono", "SF Mono", Menlo, â€¦`

v1.0 keeps both. The portal and the marketing site use the
same font stack. The marketing site may also set `font-feature-settings: 'ss01'` for
cleaner tabular numbers; the in-app portal does not need it.

**Hierarchy** (per page):
- H1: 32 px, weight 600. Used once per page (page title).
- H2: 24 px, weight 600. Section headers.
- H3: 18 px, weight 600. Sub-section headers.
- H4: 16 px, weight 600. Card titles.
- Body: 14 px, weight 400. Default.
- Body small: 13 px, weight 400. Captions, helper text.
- Mono: 13 px, JetBrains Mono. DNS records, IDs, code blocks.

**Line height** 1.5 for body, 1.25 for headings.

**No all-caps headlines** for v1.0. Sentence case only.

### 7.4 Spacing, radius, shadows

- **Spacing scale:** 4, 8, 12, 16, 20, 24, 32, 40, 56, 80 px.
- **Border radius:** 6 px for buttons, 8 px for inputs, 12 px
  for cards and modals, 999 px for avatars.
- **Shadows:**
  - `--shadow-sm`: `0 1px 2px rgba(0,0,0,0.10)`.
  - `--shadow-md`: `0 4px 12px rgba(0,0,0,0.10)`.
  - `--shadow-lg`: `0 12px 32px rgba(0,0,0,0.16)`.
  - `--shadow-focus`: `0 0 0 3px var(--accent-glow)`.

Light mode drops the shadow opacity to half.

### 7.5 Density

- **Marketing site** is roomy. 80 px between sections. 24 px
  inside cards.
- **Portal** is dense. 24 px between sections. 16 px inside
  cards. 12 px between rows in a table.
- **Wizard** (onboarding, change-plan) is roomier than the
  portal because the operator is doing one thing at a time.

### 7.6 Iconography

- **Stroke icons only.** No filled icons. 1.5 px stroke. 16 px
  in body text, 20 px in cards, 24 px in headers, 32 px in
  hero areas.
- Use a single icon set (e.g. Lucide, **Requires backend
  implementation** to wire the icon font for the marketing
  site; v1.0 ships with inline SVG for hero icons and the
  existing `release/webmail/assets/icons.js` for the portal).
- **No brand mascot** in v1.0. The "O" logomark is enough.

### 7.7 Motion

- **No animated illustrations.** No carousel hero. No
  marquee. No bouncing elements. Movement only on state
  changes (a button press, a modal open, a hover state).
- **Transitions:** 120 ms ease-out for hover / focus,
  180 ms ease-out for modals and drawers, 0 ms for
  anything stateful (the new state replaces the old
  immediately, no transition).
- **`prefers-reduced-motion: reduce`** disables all
  transitions. The portal must respect this OS setting.

### 7.8 Empty states

Every list, every table, every dashboard card has a designed
empty state. The default is:

> Big illustration (or icon if no illustration) +
> One-line headline ("No domains yet.") +
> One-line subhead ("Add your first domain to start
> receiving mail.") +
> Primary CTA button.

Empty states are **not** hidden. They are the first thing
the operator sees in a fresh tenant, and they are the most
common thing a small-business operator sees for the first
month.

### 7.9 Error states

Errors are honest, never apologetic ("Something went wrong"
is a cop-out; "We couldn't save your changes â€” your network
connection dropped. Try again" is honest). Error messages are
written by humans, in plain English, with a path forward.

Errors are **not** a free-for-all. The design system has
three error states:

- **Inline field error** â€” red 1px border, one-line message
  above the field. For validation.
- **Toast error** â€” bottom-right, 4 s dismissable. For
  transient failures.
- **Banner error** â€” top of the page, persistent. For
  state-changing failures that block progress.

### 7.10 What is **NOT** in the visual language

- **No carousels** on the marketing site.
- **No popups, modals on entry, exit-intent, or "subscribe to
  our newsletter"** on the marketing site. The only modal
  triggers are: cookie consent, demo video (opt-in), and the
  language switcher.
- **No live chat widget** in v1.0.
- **No animated stats counter** ("10,000+ customers served").
  The marketing site does not show a customer count in v1.0
  because we have not yet signed customers.
- **No "Powered by [framework]"** in the footer. Footer credits
  go to the Orvix team only.
- **No gradients** as backgrounds. The Orvix mark uses
  `linear-gradient(135deg, #5b9eff 0%, #3d7be0 100%)` but
  only the mark; surfaces stay flat.
- **No emoji** in product UI. The marketing site can use them
  sparingly in blog post prose, never in buttons.

---

## 8. Accessibility

The portal and the marketing site must meet **WCAG 2.1 AA**.
This section pins the specific behaviours. Anything not pinned
here follows the standard AA rules; nothing here relaxes them.

### 8.1 Keyboard

- **Every** interactive element is reachable with `Tab`,
  `Shift+Tab`, and activatable with `Enter` or `Space`. Custom
  widgets (combobox, listbox, table sort, drag-and-drop)
  implement the WAI-ARIA Authoring Practices.
- **Visible focus ring** on every focusable element:
  `outline: 2px solid var(--accent); outline-offset: 2px;`.
  Never `outline: none` without a replacement.
- **Skip link** at the top of every page: "Skip to main content."
  Activating it moves focus to `<main>`.
- **Keyboard shortcuts** (the `g + letter` pattern is already
  in the admin app, Â§2.6 of the admin app). v1.0 portal
  extends with:
  - `g d` â†’ Dashboard
  - `g o` â†’ Organization
  - `g m` â†’ Members
  - `g i` â†’ Invitations
  - `g x` â†’ Domains
  - `g b` â†’ Mailboxes
  - `g a` â†’ Aliases
  - `g r` â†’ Groups
  - `g $` â†’ Billing
  - `g u` â†’ Usage
  - `g s` â†’ Security
  - `?` â†’ Keyboard shortcuts overlay
  - `Esc` â†’ Close any open modal / drawer
  - `/` â†’ Focus the search field (top bar)
- **Shortcuts overlay** (`?`): shows every shortcut, hides on
  click or `Esc`. Backed by the existing `app.js` keyboard
  overlay. Marketing site does **not** expose shortcuts.

### 8.2 Screen reader

- **Semantic HTML** throughout. `<main>`, `<nav>`, `<header>`,
  `<footer>`, `<section>`, `<article>`, `<aside>`, `<table>`,
  `<thead>`, `<tbody>`, `<tr>`, `<th>`, `<td>`. Never a
  `<div>` for what should be a heading or button.
- **Headings** are sequential. h1 â†’ h2 â†’ h3, no skips.
- **aria-label** on every icon-only button. The current admin
  app has "Close" and "Reload current section" already
  pinned by static analysis (Â§2 of the Codex final review);
  the portal extends with: "Sign Out", "Search",
  "Notifications", "Profile menu", "Switch language",
  "Toggle theme".
- **aria-describedby** on every input that has help text below
  it. Validation errors use `aria-invalid="true"` and
  `aria-errormessage` pointing to the error text.
- **aria-live="polite"** on the top banner area (so a new
  banner is announced) and on the toast stack
  (`role="status"`).
- **aria-label** on the data tables: the `caption` element
  gives the table its accessible name; column headers are
  `<th scope="col">`; row headers are `<th scope="row">`
  where the column is the row's identity.
- **Forms** wrap every input in a `<label>` (the existing
  `el('label', ...)` in components.js already does this).
  No placeholder-as-label.

### 8.3 Visual

- **Colour contrast** â‰¥ 4.5:1 for body text, â‰¥ 3:1 for
  large text and icon-only buttons. The accent blue
  `#5b9eff` on `--bg-canvas #0d1118` is 8.4:1; on
  `--bg-elevated #1a2030` is 6.7:1. Both pass.
- **Focus ring** is always visible â€” not removed by the
  component author.
- **Text size** minimum 14 px in the portal, 16 px in
  marketing prose. Body line-height 1.5.
- **No colour-only signalling.** A red badge for "Failed"
  must also have a status icon (X) and a label.
- **`prefers-reduced-motion`** disables every transition
  and every animation.
- **Dark / light mode** both meet the same contrast rules.

### 8.4 Mobile and tablet

The portal and the marketing site are responsive to
**320 px wide** (iPhone SE) up. The layout adapts:

- **Marketing site (â‰¥ 1024 px):** Two-column hero, four-column
  pricing, three-column feature blocks.
- **Marketing site (768â€“1023 px):** Two-column hero,
  two-column pricing (collapses to 2-up from 4-up at this
  width), two-column feature blocks.
- **Marketing site (< 768 px):** Single-column. Hamburger
  nav. Hero stacks vertically. Pricing cards stack.
- **Portal (â‰¥ 1024 px):** Sidebar visible, top bar visible,
  content right-anchored. Density as in Â§7.5.
- **Portal (768â€“1023 px):** Sidebar collapses to icons
  (icons-only rail, 56 px wide). Top bar shows org name.
- **Portal (< 768 px):** Sidebar hides behind a hamburger
  that opens a drawer. Tables become stacked cards (one
  card per row, with field labels). The dashboard KPI strip
  is two-column.

The marketing site **must not** show a horizontal scroll
at any width â‰¥ 320 px. The portal's tables may show a
horizontal scroll inside a constrained container if the
operator chooses to view on a small screen; an honest
"Use a wider screen for the full table" banner is shown
on those.

### 8.5 Touch targets

Every interactive element has a minimum touch target of
**44 Ã— 44 px** on touch devices. The portal's compact
desktop density (`32 Ã— 32` icon buttons) is allowed on
mouse-only devices but not on touch. Touch detection: CSS
`@media (pointer: coarse)` wraps the icon-only buttons in a
44 Ã— 44 hit area.

### 8.6 Forms

- **Every input** has a visible label, not just a placeholder.
- **Required** fields are marked with a red asterisk after
  the label AND `aria-required="true"`.
- **Errors** are inline, near the field, with `aria-invalid`
  and `aria-errormessage`.
- **Autocomplete** attributes are set on every input:
  `email`, `current-password`, `new-password`,
  `one-time-code`, `organization`, `name`, `given-name`.
- **Submit** buttons are `type="submit"` inside a `<form>`.
  Pressing `Enter` in any input submits the form.

### 8.7 Errors and status

- All error toasts use `role="status"` and `aria-live="polite"`.
- The top banner (trial, past-due, etc.) uses
  `role="region"` and `aria-label="{Banner text}"`.
- Form errors do not rely on colour alone. The asterisk,
  the text, and the focus ring are all visible.

### 8.8 Language and direction

- `lang` attribute on `<html>` reflects the active locale.
  Arabic triggers `dir="rtl"`.
- `dir` is set per-section when mixing (e.g. a billing
  summary table in English on an Arabic page); the
  `applyAutoDir` helper from the admin app handles this.
- Numbers and dates are formatted in the active locale.
- The marketing site does not right-align for the LTR
  version.

---

## 9. Appendices

### 9.1 Status banners (canonical copy)

| Banner | Text | When |
| ------ | ---- | ---- |
| Trial | "You have N days left in your free tier." + `Upgrade` | `subscription.status = trialing` and `trial_ends_at` set |
| Past due | "Your last payment failed. Update your payment method to keep your service active." + `Update payment` | `subscription.status = past_due` |
| Grace period | "Your service is in grace period. Mail is still being delivered but will be suspended on {grace_period_ends_at}." + `Update payment` | `subscription.status = grace_period` |
| Suspended (full-page block) | "Your service is suspended. Mail is no longer being delivered. Update your payment method to restore." + `Update payment` | `subscription.status = suspended` |
| Deletion pending | "Account deletion requested for {date}. Your data will be removed on that date." + `Cancel deletion` | `subscription.cancelled_at` set in the last 7 days |
| Quota warning | "You're at {pct} % of your {plan} {resource} limit." + `View usage` | `usage.used / limit >= 0.8` (storage or mailboxes) |
| Quota reached | "Your {plan} {resource} is full. New mail is being rejected." + `Upgrade` | `usage.used / limit >= 1.0` |
| Verification pending | "Domain verification is in progress. We'll email you when it completes." | `domain.status = pending_verification` for > 24 h |
| Verification failed | "Domain {domain} failed verification. Open the DNS wizard to fix." + `Open wizard` | `domain.status = failed` |
| MFA available | "Set up MFA to add a second factor to your sign-in." + `Set up MFA` | `user.mfa_enabled = false` (**Requires backend implementation** for the field; v1.0 always shows the banner) |

### 9.2 Empty states (canonical copy)

| Surface | Headline | Subhead | CTA |
| ------- | -------- | ------- | --- |
| No domains | "No domains yet" | "Add your first domain to start receiving mail." | `+ Add domain` |
| Domain pending | "Verification in progress" | "DNS records usually propagate within 24 hours. We'll email you when verification completes." | `Verify again` |
| Domain failed | "Verification needs attention" | "Some DNS records are missing or incorrect. Open the wizard to fix." | `Open DNS wizard` |
| No mailboxes | "No mailboxes yet" | "Create your first mailbox to start sending and receiving." | `+ Add mailbox` |
| No members | "No teammates yet" | "Invite your first teammate to collaborate." | `+ Invite member` |
| No invitations | "No invitations" | "Click Invite member to add your first teammate." | `+ Invite member` |
| No aliases | "No aliases yet" | "Aliases forward mail from one address to another." | `+ Add alias` |
| No groups | "No groups yet" | "Groups let you manage permissions for many mailboxes at once." | `+ Add group` |
| No usage | "No usage yet" | "Once you start sending and receiving, your usage will appear here." | (no CTA) |
| No notifications | "No notifications" | "Important account events will appear here." | (no CTA) |
| No active sessions | "Only this session is active" | "Sign in from another device to manage it here." | (no CTA) |
| No audit log | "No audit entries" | "Every admin action will be recorded here." | (no CTA) |
| No API keys | "No API keys yet" | "API keys are not yet available. Contact support for early access." | (no CTA) |
| No invoices | "No invoices yet" | "Your first invoice will appear here after your first billing cycle." | (no CTA) |

### 9.3 Error messages (canonical copy)

| Code | Where | Text |
| ---- | ----- | ---- |
| 400 (validation) | All forms | Inline next to the field: "{Field} is required." / "Enter a valid {field}." / "Passwords do not match." / "{Field} must be at least {N} characters." |
| 400 (invalid format) | Domain add | "Enter a valid domain (e.g. example.com)." |
| 401 (login) | Login | "Invalid email or password." |
| 401 (session expired) | Anywhere | Banner: "Your session has expired. Sign in again to continue." + `Sign in` |
| 403 (permission) | Restricted actions | "You do not have permission to do this. Contact your admin." |
| 404 (route) | Router | "This page does not exist." + `Back to dashboard` |
| 409 (conflict) | Signup, alias, group | "A {resource} with that name already exists." |
| 409 (email taken) | Signup | "An account with this email already exists. Sign in to your existing account." |
| 409 (domain taken) | Domain add | "This domain is already configured. Contact support@orvix.com to migrate." |
| 429 (rate limit) | Login, forgot | "Too many attempts. Please wait a few minutes and try again." |
| 402 (payment declined) | Change plan | "Your payment method was declined. Update your card and try again." + `Update card` |
| 500 (server) | Anywhere | "We couldn't {action} right now. Please try again. If the problem persists, contact support@orvix.com." |
| 502 / 503 / 504 | Anywhere | "The service is temporarily unavailable. Please try again in a moment." |
| Network error | Anywhere | "Connection problem. Check your network and try again." |

### 9.4 Toast rules

- **Success toast** â€” 3 s, green accent, top-right, dismissable.
  Copy: "Saved." / "Invitation sent to {email}." / "Mailbox
  created."
- **Error toast** â€” 6 s, red accent, top-right, dismissable.
  Copy: the canonical error message from Â§9.3. If the error
  is recoverable (e.g. payment declined), include the action
  button inline: "Update card" link inside the toast.
- **Info toast** â€” 4 s, neutral accent, top-right. Copy: "We
  sent a reset link to your email." / "MFA challenge issued."
- **Limit:** at most 3 toasts visible at once. Stacks at the
  top-right corner. The 4th toast pushes the 1st out.

### 9.5 Loading & pending states

- **Initial page load:** top-of-page skeleton (1 row of 4
  skeleton cards, 2 rows of skeleton text). The skeleton uses
  the same spacing as the eventual content, so the layout
  does not jump when the real data arrives.
- **Background refresh (polling, re-fetching):** the page
  shows the previous data with a small "Refreshingâ€¦" chip in
  the top bar. No skeleton; no spinner. The data is the
  source of truth until the next response arrives.
- **Action in flight:** the button that triggered the action
  shows a spinner, becomes `disabled`, and its label changes
  to the past tense ("Save" â†’ "Savingâ€¦", "Send" â†’ "Sendingâ€¦").
  The form's other fields are disabled too. The Cancel
  button (where present) stays enabled.
- **Slow request (> 5 s):** the spinner gets a label
  "Taking longer than usualâ€¦" beneath the button. If the
  request is for a destructive action, the user can cancel.

### 9.6 Plan matrix (operational reference)

For the implementation team, here is the entire capability
matrix, derived directly from `internal/billing/service.go
SeedDefaultPlans()` and the `Features` array.

```
                  Free   Starter   Business   Enterprise
                  $0     $9.99/mo  $29.99/mo  $99.99/mo
Domains           1      3         10         100
Mailboxes         5      25        100        1 000
Storage           1 GB   10 GB     100 GB     1 TB
Sends/day         500    2 000     10 000     100 000
custom_domain     yes    yes       yes        yes
dkim              yes    yes       yes        yes
mta_sts                  yes       yes        yes
api                       yes       yes        yes
team                       yes       yes        yes
groups                              yes        yes
catch_all                          yes        yes
mail_forwarding                    yes        yes
backup                             yes        yes
audit_log                          yes        yes
mfa                                yes        yes
sso                                          yes
sla                                          yes
priority_support                              yes
```

A feature is either **yes** or it is not in the row. The spec
never invents a feature. The implementation must not either.

### 9.7 What this spec does **not** cover (out of scope for v1.0)

Listed here so a future Codex pass can scope the next launch
cycle. Every item below is **Requires backend implementation**.

- **Trial period distinct from the free tier.** v1.0 uses
  the Free plan as the trial; the billing handler does not
  implement a time-boxed trial.
- **SAML / OIDC SSO.** The Enterprise plan claims `sso` as
  a feature; the customer-facing SSO login button is not
  surfaced in v1.0. SSO is a future-quadrant effort.
- **API tokens for customer-portal use.** The admin
  `/api/v1/api-keys` endpoint exists; a customer-portal
  v1.0 surface is **Requires backend implementation**.
- **In-portal invoice PDF download.**
- **In-portal payment-method update (card form).**
- **End-user MFA enrollment from the customer portal.**
- **Audit-log view from the customer portal.** Visible in
  the admin console; the customer-portal mirror is
  **Requires backend implementation**.
- **Per-tenant DKIM selector.** v1.0 uses a single
  default selector.
- **Per-tenant DMARC rua address.** v1.0 uses a single
  shared address.
- **Proration on plan changes.** v1.0 charges the new
  full price at the next renewal and switches the plan
  immediately. Proration is **Requires backend
  implementation**.
- **Refunds self-serve.** v1.0 is email-driven (see Â§2.12).
- **Help center / knowledge base.** v1.0 ships the docs site
  as static Markdown; a full CMS is **Requires backend
  implementation**.
- **In-app chat support.** v1.0 is email-only.
- **Live status page.** v1.0 is a static page; the
  operational status feed is **Requires backend
  implementation**.
- **Custom webhook subscriptions per tenant.**
- **Server-side email rules, vacation, forwarding per
  mailbox** are real (`/api/v1/webmail/rules`,
  `/api/v1/webmail/vacation`, `/api/v1/webmail/forwarding`)
  but the customer-portal mirror is **Requires backend
  implementation** in v1.0. The webmail app uses these
  endpoints directly.
- **Catch-all addresses.** Plan `features` includes
  `catch_all` for Business and Enterprise, but the customer
  portal UI is **Requires backend implementation** in v1.0.

### 9.8 Cross-reference (every spec page â†’ real endpoint)

| Spec page | Endpoint |
| --------- | -------- |
| Home, Pricing, Login | `GET /api/v1/billing/plans` |
| Signup | `POST /api/v1/auth/signup` |
| Login (portal) | `POST /api/v1/auth/login` |
| Login (webmail only) | `POST /api/v1/webmail/login` |
| Forgot password | `POST /api/v1/auth/forgot-password` |
| Reset password | `POST /api/v1/auth/reset-password` |
| MFA challenge at sign-in | `POST /api/v1/auth/mfa/verify` |
| Dashboard | `GET /api/v1/enterprise/dashboard` |
| Organization | `GET /api/v1/enterprise/organizations/current` |
| Members | `GET /api/v1/enterprise/members` |
| Member role change | `PATCH /api/v1/enterprise/members/:id/role` |
| Member removal | `DELETE /api/v1/enterprise/members/:id` |
| Invitations list | `GET /api/v1/enterprise/invitations` |
| Invitation send | `POST /api/v1/enterprise/invitations` |
| Invitation revoke | `POST /api/v1/enterprise/invitations/:id/revoke` |
| Ownership request | `POST /api/v1/enterprise/ownership/request` |
| Ownership accept | `POST /api/v1/enterprise/ownership/accept` |
| Ownership cancel | `POST /api/v1/enterprise/ownership/cancel` |
| Domains list | `GET /api/v1/customer/domains` (or `/api/v1/enterprise/domains`) |
| Add domain | `POST /api/v1/enterprise/domains` |
| Domain status change | `POST /api/v1/enterprise/domains/:id/status` |
| DNS records | `GET /api/v1/customer/domains/:id/dns` |
| DNS verify | `POST /api/v1/customer/domains/:id/verify` |
| Mailboxes list | `GET /api/v1/enterprise/mailboxes` |
| Add mailbox | `POST /api/v1/enterprise/mailboxes` |
| Mailbox status change | `POST /api/v1/enterprise/mailboxes/:id/status` |
| Mailbox reset password | `POST /api/v1/enterprise/mailboxes/:id/reset-password` |
| Aliases list | `GET /api/v1/enterprise/aliases` |
| Add alias | `POST /api/v1/enterprise/aliases` |
| Delete alias | `DELETE /api/v1/enterprise/aliases/:id` |
| Groups list | `GET /api/v1/enterprise/groups` |
| Add group | `POST /api/v1/enterprise/groups` |
| Add group member | `POST /api/v1/enterprise/groups/:id/members` |
| Delete group member | `DELETE /api/v1/enterprise/groups/:id/members/:memberId` |
| Delete group | `DELETE /api/v1/enterprise/groups/:id` |
| Billing subscription | `GET /api/v1/enterprise/billing/subscription` |
| Change plan | `POST /api/v1/enterprise/billing/subscription` |
| Usage | `GET /api/v1/enterprise/billing/usage` |
| Quota check | `GET /api/v1/enterprise/billing/quota` |
| Plan catalog | `GET /api/v1/billing/plans` |
| Account deletion | `POST /api/v1/enterprise/deletion` |
| Cancel deletion | `POST /api/v1/enterprise/deletion/cancel` |
| Account status | `GET /api/v1/enterprise/status` |
| Send limit | `GET /api/v1/enterprise/abuse/send-limit` |
| Abuse signals | `GET /api/v1/enterprise/abuse/signals` |
| Acknowledge signal | `POST /api/v1/enterprise/abuse/signals/:id/acknowledge` |
| Resolve signal | `POST /api/v1/enterprise/abuse/signals/:id/resolve` |
| Profile | `GET /api/v1/me` |
| Change password | `POST /api/v1/auth/change-password` |
| Sign out | `POST /api/v1/auth/logout` |
| Sign out all | `POST /api/v1/auth/logout-all` |
| CSRF token | `GET /api/v1/csrf-token` |
| Webmail UI (page) | `/webmail/index.html` (existing) |
| Webmail API | `/api/v1/webmail/*` (existing, per docs) |
| Outlook autodiscover | `GET /autodiscover/autodiscover.xml` |
| Thunderbird autoconfig | `GET /.well-known/autoconfig/mail/config-v1.1.xml` |
| MTA-STS policy | `GET /.well-known/mta-sts.txt` |
| Webhook | `POST /api/v1/billing/webhook` |
| Complaint webhook | `POST /api/v1/billing/complaint` |
| Liveness | `GET /api/v1/health` |
| Static marketing assets | `release/portal/` (**Requires backend implementation** for the build pipeline) |
| Static webmail assets | `release/webmail/` (existing) |
| Static admin assets | `release/admin/` (existing) |
| Static docs | `docs/customer/*.md` (existing) |
