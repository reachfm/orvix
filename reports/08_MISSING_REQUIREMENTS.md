# MISSING REQUIREMENTS ANALYSIS

## Orvix Enterprise Mail (OrvixEM)

**Date:** 2026-06-05
**Reference:** MVP Full Document

---

## 1. Explicit Requirements (From MVP)

The MVP is a comprehensive specification. This report identifies areas where the MVP explicitly specifies requirements vs areas that are implicit or left to implementer discretion.

---

## 2. Fully Specified Areas

| Area | MVP Coverage | Lines |
|------|-------------|-------|
| Architecture | Complete — Stalwart + Orvix layer diagram | 33–68 |
| License Tiers | Complete — all 3 tiers with per-feature tables | 108–196 |
| Tech Stack | Complete — every component specified | 199–314 |
| Security Stack | Complete — auth, encryption, headers | 556–616 |
| Firewall Architecture | Complete — 5-layer pipeline | 620–685 |
| Auto-Heal System | Complete — health checks, decision tree | 688–723 |
| Guardian Agent | Complete — API, features, dashboard | 726–792 |
| Smart Compose AI | Complete — features, API, privacy | 795–829 |
| Instant Deploy API | Complete — request/response format | 832–878 |
| Migration Tool | Complete — supported sources, process | 882–924 |
| Project Structure | Complete — entire file tree | 990–1093 |
| Database Schema | Complete — core tables defined | 1101–1134 |
| Auto-Update System | Complete — pipeline, channels, safety | 1138–1358 |
| Build & Release | Complete — commands, artifacts, gates | 1361–1423 |
| Build Order | Complete — 7 phases with tasks | 1427–1528 |

---

## 3. Implicit / Unspecified Areas

These areas are referenced but not fully detailed in the MVP. Implementation decisions are needed.

### 3.1 Infrastructure & Operations

| Missing Item | MVP Reference | Recommendation |
|-------------|---------------|----------------|
| **Monitoring stack details** | Line 59 — "Monitoring / Telemetry / Alerts" specified but no specific alerting rules, dashboard layouts, or integration details | Implement Prometheus + Grafana stack; define alerting rules per MVP feature (queue depth, bounce rate, health checks) |
| **Log aggregation** | Lines 500–505 — Logs mentioned but no log retention, rotation, or shipping strategy | Default: Zap files with rotation; optional: Loki/Elasticsearch integration |
| **Backup strategy** | Lines 519–523 — Backup mentioned but no RPO/RTO, backup verification, or disaster recovery plan | Define: hourly incremental, daily full; RPO=1hr, RTO=30min; backup verification script |
| **Deployment topology** | Lines 305–313 — "`./orvix start`" but no HA topology, load balancing, or network architecture | Define single-server mode (MVP default) and cluster mode (Enterprise) |
| **Database backup/restore testing** | Line 522 — "One-click restore" but no restore testing requirements | Monthly automated restore test |
| **Stalwart installation details** | Line 311 — "Installed/configured/managed by OrvixEM" but no specifics on Stalwart download, version detection, or package management | Define Stalwart package source, version pinning strategy, automated install flow |

### 3.2 Security & Compliance

| Missing Item | MVP Reference | Recommendation |
|-------------|---------------|----------------|
| **Data retention policy** | Lines 183–184 — "Compliance Center" but no default data retention periods | Define: email retention by tier (SMB: 1yr, ISP: 3yr, Enterprise: configurable); trash purge after 30 days |
| **GDPR data subject rights** | Line 1053 — "gdpr.go" but no specific data export/deletion flows | Implement: export user data, right to erasure, data portability APIs |
| **HIPAA BA agreement** | Line 184 — "HIPAA" listed but no BAA process | HIPAA compliance requires Business Associate Agreement; define process |
| **Rate limit default values** | Lines 288, 664 — Token bucket concept is defined but no default rate limits | Define defaults: 5 login attempts/15min per IP, 500 msgs/hr per sender |
| **Password policy** | Line 287 — Argon2id specified but no password complexity or rotation requirements | Define: min 8 chars, no rotation requirement (NIST 2024 guidance) |
| **Session timeout** | Line 293 — "short TTL" but no specific values | Define: access token 15 min, refresh token 30 days, idle session 24hr |

### 3.3 Frontend & UX

| Missing Item | MVP Reference | Recommendation |
|-------------|---------------|----------------|
| **Mobile app strategy** | Line 127 — "PWA (install on mobile)" specified but no Android/iOS native apps | PWA is sufficient for MVP; native apps post-MVP |
| **Offline webmail support** | Line 127 — PWA mentioned but no offline email caching | Service worker caching for recent emails; full offline mode post-MVP |
| **Responsive breakpoints** | Line 319 — "Dark-first with optional light mode" but no responsive design breakpoints defined | Standard breakpoints: sm(640), md(768), lg(1024), xl(1280) |
| **Loading states / skeletons** | Line 323 — "Zero loading spinners" but skeleton screen states not defined | Define skeleton component per page layout |
| **Empty states** | — No mention of empty state design | Define empty states for: inbox, search results, dashboard, admin panels |
| **Error pages** | Line 363 — "explain what went wrong AND how to fix it" but no error page designs | Define 404, 403, 500 page templates |

### 3.4 Commercial & Business

| Missing Item | MVP Reference | Recommendation |
|-------------|---------------|----------------|
| **Payment processing** | Line 314 — "license activation" but no payment processor integration (Stripe, etc.) | Integrate Stripe for recurring payments and license fulfillment |
| **Trial/demo flow** | — No trial mechanism defined | 14-day free trial; automated license creation |
| **Refund policy** | — Not mentioned | Define 30-day money-back guarantee for annual plans |
| **Billing portal** | — Not mentioned | Customer portal for invoice history, payment method updates |
| **Reseller pricing** | Lines 538–543 — Reseller panel defined but no reseller commission/pricing model | Define: reseller markup, volume discounts, minimum commitments |
| **Partner program** | — Not mentioned but implied by "Partners / Strategic Partners" channel | Define partner tiers, benefits, certification process |

### 3.5 Operations & Support

| Missing Item | MVP Reference | Recommendation |
|-------------|---------------|----------------|
| **Support ticket system** | Line 190 — "Priority support SLA" but no ticketing system integration | Define support portal or integrate with existing systems |
| **Monitoring runbook** | Lines 688–723 — Auto-Heal defined but no runbook for manual interventions | Create runbook for scenarios Auto-Heal cannot resolve |
| **Customer onboarding** | Line 319 — "Setup wizard" mentioned but no onboarding flow defined | Define: welcome email sequence, setup wizard steps, getting-started guide |
| **Migration cutover window** | Lines 916–923 — Zero-downtime migration but no specific cutover window | Define: maximum 5 minutes DNS propagation delay |
| **Backup verification** | Line 522 — "One-click restore" but no automated backup testing | Monthly automated restore test to sandbox environment |

---

## 4. Deferred Features (Intentionally Not in MVP)

The following are referenced but explicitly deferred from MVP scope:

| Feature | Status | Rationale |
|---------|--------|-----------|
| Native iOS/Android apps | Post-MVP | PWA covers MVP needs |
| Advanced email marketing | Post-MVP | Focus on core email first |
| Chat/instant messaging | Post-MVP | Collaboration layer is shared inbox only |
| Video conferencing | Post-MVP | Calendar integration only |
| Advanced CRM | Post-MVP | Contact management only |
| Email list management | Post-MVP | Distribution lists are basic only |

---

## 5. Critical Missing Items (Must Add)

Based on analysis, these items are implicitly required by the MVP but not explicitly specified:

| Priority | Missing Item | Justification | MVP Connection |
|----------|-------------|---------------|----------------|
| P0 | Payment processor integration | Revenue model requires billing | License purchase flow (line 1524) |
| P0 | Customer portal UI | License management, billing, support | portal.orvix.email (line 89) |
| P0 | Graceful degradation without Redis | Redis is SPOF for core operations | Redis dependency (line 209) |
| P1 | Database backup verification | RTO/RPO guarantees for enterprise | Backup & Restore (lines 519-523) |
| P1 | Monitoring runbook | Auto-Heal escalation path | Auto-Heal system (lines 688-723) |
| P1 | Data retention policy | Required for GDPR/HIPAA compliance | Compliance Center (line 184) |
| P2 | Trial license generation | Customer acquisition requires trials | License flow (line 314) |
| P2 | Empty states / error pages | Production UX quality | UI principles (lines 357-364) |

---

**Conclusion:** The MVP is remarkably complete for a specification of its scope. The identified "missing" items are primarily operational details that are typically defined during implementation. No critical architectural or feature requirements are missing. The most important gaps are in the commercial infrastructure (payment processing, customer portal, trial flow) which are referenced but not detailed.
