# OrvixEM Prioritized Runtime Fixes

**Date:** 2026-06-05
**Priority:** P0 = breaks production | P1 = major customer-facing bug | P2 = functionality gap | P3 = polish

---

## P0 — Production Breaking (Fix Before Next Customer)

| # | Issue | System | Effort | Fix |
|---|---|---|---|---|
| F-1 | Mail never delivered — `isStalwartAvailable()` hardcoded to false | Mail Queue | 1h | Change to check if Stalwart binary is detected OR implement SMTP relay config |
| F-2 | Webmail shows users instead of email messages | Webmail | 2h | Add message list endpoint or change query to return messages based on user |
| F-3 | Backup directory `./backups` is relative — breaks on systemd | Backup | 0.5h | Use absolute default: `/var/backups/orvix` |
| F-4 | Auto-heal has checks but no fixers configured | Auto-Heal | 1h | Stub fixers with logging so admin can see what would happen |
| F-5 | `ProtectSystem=full` blocks SQLite writes on systemd | systemd | 0.5h | Change to `ProtectSystem=strict` + `ReadWritePaths=/var/lib/orvix` |
| F-6 | Prometheus metrics never registered — `/metrics` is empty | Monitoring | 0.25h | Add `metricsSvc.Register()` call in main.go |
| F-7 | No license activation API endpoint | Licensing | 1h | Add `POST /api/v1/license/activate` endpoint |
| F-8 | Backup ticker only runs when `auto_apply=true` | Backup | 0.25h | Start ticker unconditionally, respect `auto_apply` only for applying |
| F-9 | JWT access token stored in localStorage (XSS vulnerable) | Security | 2h | Move to memory-only storage, use httpOnly cookie for refresh |

**Total P0 effort:** ~8.5 hours

## P1 — Major Customer-Facing Bug

| # | Issue | System | Effort | Fix |
|---|---|---|---|---|
| F-10 | Dashboard shows all zeros on fresh install | Admin | 2h | Add setup wizard or guided first-run experience |
| F-11 | DNS records never published — no provider configured | DNS | 4h | Add manual DNS instruction display in domain view ("copy these records to your DNS provider") |
| F-12 | No session cleanup — expired sessions accumulate | Auth | 1h | Add periodic session cleanup job |
| F-13 | No disk/memory/CPU monitoring | Health | 2h | Add system metrics to health endpoint |
| F-14 | No per-account rate limiting on login | Auth | 1h | Add account-level rate limiting |
| F-15 | Database migrations not version-tracked | Migrations | 2h | Implement migration table with version checking |
| F-16 | docker-compose has port conflicts (orvix + stalwart share ports) | Docker | 0.5h | Document in docker-compose that ports should be mapped once |
| F-17 | No email templates (welcome, password reset) | Email | 3h | Add HTML email templates |

**Total P1 effort:** ~15.5 hours

## P2 — Functionality Gap

| # | Issue | System | Effort | Fix |
|---|---|---|---|---|
| F-18 | No password reset flow | Auth | 4h | Add forgot/reset password endpoints |
| F-19 | No storage usage visualization | Dashboard | 2h | Add DB queries for user storage usage |
| F-20 | Guardian AI has no training pipeline | Guardian | 8h | Add pattern learning from mail queue data |
| F-21 | Firewall rules never wired to mail flow | Firewall | 8h | Integrate with Stalwart events |
| F-22 | No CSV/Excel export for lists | Admin | 3h | Add export endpoints for users, domains |
| F-23 | No maintenance/offline page | Frontend | 1h | Add Nginx maintenance page template |
| F-24 | Calendars have no visual views | Webmail | 4h | Integrate FullCalendar library |
| F-25 | No browser push notifications | Webmail | 3h | Add Web Push API integration |
| F-26 | No favicon | Frontend | 0.25h | Add SVG favicon |
| F-27 | Admin tables have no pagination | Admin | 2h | Add limit/offset/page controls to list endpoints |

**Total P2 effort:** ~35 hours

## P3 — Polish

| # | Issue | System | Effort | Fix |
|---|---|---|---|---|
| F-28 | No dark/light theme toggle | Frontend | 1h | Add theme switcher to settings |
| F-29 | No keyboard shortcut reference sheet | Webmail | 1h | Add ? key to show shortcuts |
| F-30 | No loading skeletons | Frontend | 2h | Add Skeleton components |
| F-31 | No error boundary components | Frontend | 1h | Add React error boundaries |
| F-32 | No API request retry on network failure | Frontend | 2h | Add retry logic to API client |
| F-33 | No offline detection | Frontend | 0.5h | Add online/offline indicator |
| F-34 | No notification sounds | Webmail | 1h | Add audio feedback for new mail |
| F-35 | No rate limit remaining headers | API | 0.5h | Add X-RateLimit-Remaining headers |

**Total P3 effort:** ~9 hours

---

## Effort Summary

| Priority | Count | Estimated Effort |
|---|---|---|
| P0 | 9 | 8.5 hours |
| P1 | 8 | 15.5 hours |
| P2 | 10 | 35 hours |
| P3 | 7 | 9 hours |
| **Total** | **34** | **~68 hours (2 weeks)** |

## Recommended Immediate Actions

1. **Fix P0 items F-1 through F-9** — These will break every customer deployment
2. After P0, fix P1 items **F-10 through F-17** — Major UX gaps
3. After P1, address **F-18 through F-27** — Missing features customers expect
4. **F-28 through F-35** — Can be deferred to post-launch polish

## Critical Path for Next Customer

```
Hour 0-2:  F-1 (Mail delivery) + F-5 (systemd) + F-6 (Metrics) + F-8 (Backup ticker)
Hour 2-4:  F-2 (Webmail messages) + F-3 (Backup path) + F-7 (License activation)
Hour 4-6:  F-4 (Auto-heal logging) + F-9 (JWT storage)
Hour 6-8:  F-10 (Dashboard/setup wizard) + F-11 (DNS instructions)
Hour 8-12: F-12 (Session cleanup) + F-13 (System metrics) + F-14 (Login rate limit)
```

After 12 hours of focused fixes, the product would be ready for the next customer deployment.
