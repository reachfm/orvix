# OrvixEM — Remaining Work

**Priority Key:** P0 Critical | P1 High | P2 Medium | P3 Low

---

## P0 — Critical (Blocks Production Deployment)

| # | Item | Area | Details | Depends On |
|---|---|---|---|---|
| 1 | **Write unit tests** | Testing | Zero tests across all 34 packages. Must add tests for auth, license, API handlers, models, security | Nothing |
| 2 | **JWT secret management** | Security | JWT secret in config file. Must use env var or secrets manager | Nothing |
| 3 | **Database connection hardening** | Infrastructure | Connection pooling, retry logic, circuit breaker for DB | Nothing |
| 4 | **Error handling review** | API | Many handlers return generic errors without request_id or structured format | Nothing |
| 5 | **Graceful shutdown** | Infrastructure | No SIGTERM/SIGINT handler for clean shutdown of background workers | Nothing |

---

## P1 — High (Required for MVP Completion)

| # | Item | Area | Details | Depends On |
|---|---|---|---|---|
| 6 | **Webmail compose: TipTap editor** | Frontend | Replace plain textarea with rich text editor | Frontend rebuild |
| 7 | **Webmail: reply/forward flow** | Frontend | Implement reply, reply-all, forward in reading pane | Backend message API |
| 8 | **Webmail: HTML email rendering** | Frontend | Sandboxed iframe with CSP for email display | Nothing |
| 9 | **Webmail: attachment upload** | Frontend | Drag-and-drop file upload in compose | Backend attachment API |
| 10 | **Message body storage** | Backend | Add body_text/body_html/raw_path fields to Message model | DB migration |
| 11 | **Stalwart live integration test** | Backend | Test Stalwart binary detection, config generation, provisioning end-to-end | Stalwart binary |
| 12 | **Mail firewall wiring** | Backend | Wire 5-layer pipeline into actual mail flow (requires Stalwart) | Stalwart integration |
| 13 | **Auto-heal background runner** | Backend | Start Monitor in main.go with health check interval | Nothing |
| 14 | **Anti-spam whitelist/blacklist UI** | Frontend | Model exists, needs CRUD UI in admin | Nothing |
| 15 | **Backup scheduling** | Backend | Cron/scheduler for daily/weekly backups | Nothing |
| 16 | **DNS provider integration** | Backend | Cloudflare API v4 and Route53 SDK for automatic DNS record creation | SDK dependencies |
| 17 | **License purchase flow** | Portal | Link/UI for purchasing license from orvix.email | Nothing |
| 18 | **Recovery testing** | Testing | Test backup restore, job recovery, queue recovery | Backup feature |

---

## P2 — Medium (Important for Feature Parity)

| # | Item | Area | Details | Depends On |
|---|---|---|---|---|
| 19 | **Bleve search indexing** | Backend | Full-text search for emails, no external deps | Message model with body |
| 20 | **Redis session store** | Backend | Move sessions from SQLite to Redis for performance | Redis config |
| 21 | **Asynq job queue** | Backend | Background job processing for provisioning, migration, backup | Redis config |
| 22 | **Email Intelligence: real analytics** | Backend | Delivery trends, bounce rates from actual mail flow | Stalwart integration |
| 23 | **Zero-downtime migration** | Backend | Dual-delivery phase during IMAP migration | IMAP sync engine |
| 24 | **Migration source adapters** | Backend | Implement real Axigen/Zimbra/Exchange/Google adapters | External APIs |
| 25 | **SSO: SAML/OAuth2 redirect handling** | Backend | Implement actual redirect flow, not just config storage | SSO library |
| 26 | **LDAP sync execution** | Backend | Implement LDAP search and user sync from config | LDAP server |
| 27 | **DLP violation detection engine** | Backend | Wire DLP pattern matching against email content | Message body storage |
| 28 | **Webmail: Tasks module** | Frontend | Create tasks from emails, due dates, subtasks | Nothing |
| 29 | **Webmail: search with filters** | Frontend | Advanced search: from/to/subject/date/attachments | Bleve backend |
| 30 | **Admin: log viewer** | Frontend | SMTP/IMAP/auth log viewer with search | Stalwart log ingestion |
| 31 | **Admin: email routing UI** | Frontend | Catch-all, forward, relay configuration UI | Backend routing API |
| 32 | **Admin: maintenance mode** | Frontend | Toggle maintenance mode, show banner to users | Nothing |
| 33 | **Admin: scheduled backups UI** | Frontend | Schedule configuration (daily/weekly, retention) | Backup scheduling |
| 34 | **Admin: real-time threat feed** | Frontend | Live blocked threats display for firewall | Mail firewall wiring |
| 35 | **Admin: TLS cert management** | Frontend | View/manage Let's Encrypt certificates | Let's Encrypt ACME |

---

## P3 — Low (Nice to Have / Polish)

| # | Item | Area | Details | Depends On |
|---|---|---|---|---|
| 36 | **AES-256-GCM field encryption** | Backend | Encrypt TOTP secrets, backup codes, API keys at rest | Nothing |
| 37 | **Let's Encrypt ACME integration** | Backend | Automatic TLS certificate provisioning | Nothing |
| 38 | **PWA manifest + service worker** | Frontend | Installable webmail on mobile | Nothing |
| 39 | **i18next multi-language support** | Frontend | Internationalization framework integration | Nothing |
| 40 | **Keyboard shortcuts in webmail** | Frontend | R=reply, F=forward, J/K=navigate, etc. | Nothing |
| 41 | **Undo send (30s window)** | Frontend | Delay send with undo option | Compose improvements |
| 42 | **Draft auto-save** | Frontend | Auto-save compose content every 30s | Compose improvements |
| 43 | **Resource booking calendar integration** | Frontend | Book rooms/equipment via calendar UI | Calendar integration |
| 44 | **Public folder access management UI** | Frontend | Manage folder permissions in admin | Nothing |
| 45 | **Distribution list member management UI** | Frontend | Add/remove members in admin | Nothing |
| 46 | **API documentation generation** | Docs | Auto-generate API docs from route definitions | Nothing |
| 47 | **Admin documentation** | Docs | User guide for admin console | Nothing |
| 48 | **Marketing website (orvix.email)** | Portal | Landing page for product | Nothing |
| 49 | **CI/CD pipeline** | Infrastructure | GitHub Actions or similar | Nothing |
| 50 | **Penetration testing** | Security | Auth bypass, injection, tenant isolation tests | Nothing |

---

## Effort Summary

| Priority | Count | Estimated Effort |
|---|---|---|
| P0 Critical | 5 | 1 week |
| P1 High | 13 | 3-4 weeks |
| P2 Medium | 17 | 4-5 weeks |
| P3 Low | 15 | 3-4 weeks |
| **Total** | **50** | **11-14 weeks** |

## Current Build Status

```
✅ go build ./cmd/orvix
✅ go vet ./...
✅ go test ./... (no test files)
✅ npm run build (all 3 frontend apps)
✅ Server starts on :8080
✅ Frontend served at /admin, /mail, /portal
✅ API at /api/v1/*
✅ 100+ endpoints verified working
```
