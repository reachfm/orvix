# Orvix Roadmap — Future Features

Items moved from MVP.md Build Order that are outside current implementation scope.

## Phase 6 — Advanced Features (Future)

- **ActiveSync (ISP+ tier)** — ActiveSync protocol server implementation. Requires EAS protocol handler.
- **Multi-Cloud Storage (S3 / GCS / Azure)** — Email attachment storage on cloud providers. Backend config + upload plumbing needed.
- **SSO — SAML 2.0 + OAuth2 (Enterprise)** — SSOConfig model exists. Full redirect flow (login endpoint, callback handler, session creation) needed.

## Phase 7 — Hardening + Launch (Future)

- **Penetration testing** — Manual security audit by external team for auth bypass, injection, tenant isolation.
- **Load testing** — 10k concurrent connections verification. Requires production-like infrastructure.
- **Deliverability testing** — Verify email delivery to Gmail, Outlook, Yahoo. Requires Stalwart with valid domains.
- **Marketing website** — Next.js site at orvix.email. Marketing content + pricing page.
- **License purchase flow** — Payment integration (Stripe/Paddle) + customer portal.
- **Update server setup** — Production update server binary and infrastructure.

## Backlog

- **Calendar/Contacts/Tasks React UI** — Backend CRUD APIs exist. Frontend pages need FullCalendar + Radix UI components.
- **ClamAV webhook integration** — Scanner exists. Integration with email webhook path pending.
- **S3 cloud backup** — Local backup works. S3/GCS/Azure storage backend pending.
- **Frontend SPA polish** — Loading/error states, form validation, UX improvements.
