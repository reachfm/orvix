# MASTER EXECUTION PLAN

## Orvix Enterprise Mail (OrvixEM)

**Date:** 2026-06-05
**Source of Truth:** Orvix_Enterprise_Mail_COMPLETE_MVP.md
**Phases:** 7 phases as defined in MVP (Lines 1427–1528)

---

## 1. Plan Overview

This Master Execution Plan converts the MVP's 7 defined phases into executable, prioritized tasks.

**Rule:** Only phases defined in the MVP are used. No new product phases are created. No roadmap replacements.

---

## 2. Phase Classification Key

| Priority | Definition | Revenue Impact | Security Impact |
|----------|-----------|---------------|-----------------|
| **P0 — Critical** | System cannot function without this | Blocks all revenue | Critical vuln |
| **P1 — High** | Core feature required for MVP launch | Direct revenue impact | High risk |
| **P2 — Medium** | Important but can defer to first update | Indirect revenue | Moderate risk |
| **P3 — Low** | Nice-to-have; post-MVP enhancement | Low/no revenue | Low risk |

---

## 3. Phase 1 — Foundation (MVP Lines 1428–1438)

**Duration:** Week 1–2 | **Total Effort:** 74 hours | **MVP Tasks:** 10

| ID | Task | File | P0/P1/P2/P3 | Business Impact | Revenue Impact | Security Impact | Complexity | Dependencies |
|----|------|------|-------------|----------------|---------------|----------------|------------|--------------|
| F01 | Set up project structure exactly as defined | All directories | P0 | Foundation for all development | None direct | None direct | Low | None |
| F02 | Initialize `go.mod` with `github.com/orvixemail/orvix` | `go.mod`, `cmd/orvix/main.go` | P0 | Entry point for binary | None direct | None direct | Low | F01 |
| F03 | Create config system reading `orvix.yaml` | `internal/config/` | P0 | All runtime configuration depends on this | None direct | Low — secrets config | Medium | F02 |
| F04 | Create database connection (SQLite/PostgreSQL) | `internal/models/` | P0 | All persistent storage | None direct | High — DB security | Medium | F03 |
| F05 | Create additive-only migration system | `internal/migrations/` | P0 | Safe schema evolution | Critical — prevents data loss | Medium | Medium | F04 |
| F06 | Set up structured logging (Zap) | `internal/metrics/` | P1 | Debugging, production operations | None direct | Low | Low | F02 |
| F07 | Set up Prometheus metrics | `internal/metrics/` | P1 | Observability, monitoring | None direct | Low | Low | F02 |
| F08 | Build license engine (JWT RS256) | `internal/license/` | P0 | **Revenue model** — tier enforcement | **Critical** — locks tiers | **Critical** — forgery prevention | High | F02 |
| F09 | Build feature flags engine | `internal/flags/` | P0 | License gating, channel control | **Critical** — feature unlock | Medium — bypass prevention | Medium | F08 |
| F10 | Add versioning + watermarking | `cmd/orvix/main.go`, metadata pkg | P1 | Piracy detection, version awareness | Medium | Medium — canary tokens | Low | F02 |

### Phase 1 Dependencies

```
F01 → F02 → F03 → F04 → F05
                  ↓
           F06, F07, F08, F09, F10
```

### Phase 1 Verification

```
orvix start                    → Binary runs, config loads
orvix doctor                   → DB connects, license validates
Feature flags respond to tier  → License → Flags mapping works
Migrations apply additive-only → Schema up to date
Prometheus /metrics endpoint   → Metrics exposed
```

---

## 4. Phase 2 — Stalwart Core Integration (MVP Lines 1440–1453)

**Duration:** Week 3–5 | **Total Effort:** 128 hours | **MVP Tasks:** 13

| ID | Task | File | P0/P1/P2/P3 | Business Impact | Revenue Impact | Security Impact | Complexity | Dependencies |
|----|------|------|-------------|----------------|---------------|----------------|------------|--------------|
| S01 | Stalwart install/detection flow | `internal/stalwart/service.go` | P0 | **Core mail engine** — nothing works without Stalwart | Critical — all mail ops | Medium — Stalwart version security | High | F03 |
| S02 | Stalwart config generator | `internal/stalwart/config.go` | P0 | Correct Stalwart configuration | Critical — mail delivery | **Critical** — misconfiguration causes vulns | High | S01 |
| S03 | Stalwart config validator | `internal/stalwart/config.go` | P0 | Prevent invalid config from applying | Critical — service reliability | **Critical** — prevents insecure config | Medium | S02 |
| S04 | Stalwart service lifecycle manager | `internal/stalwart/service.go` | P0 | Start/stop/restart/reload orchestration | Critical — service uptime | Medium | Medium | S01 |
| S05 | Domain provisioning | `internal/stalwart/provisioning.go` | P0 | Multi-tenant domain creation | **Critical** — core feature | Medium | High | S02 |
| S06 | Mailbox provisioning | `internal/stalwart/provisioning.go` | P0 | Mailbox creation and management | **Critical** — core feature | High — quota enforcement | High | S05 |
| S07 | Alias/routing provisioning | `internal/stalwart/provisioning.go` | P1 | Email routing | Medium | Low | Medium | S05 |
| S08 | Queue visibility and controls | `internal/mailops/queue.go` | P1 | Operational queue management | Medium | Low | Low | S01 |
| S09 | Stalwart log/event ingestion | `internal/stalwart/events.go` | P1 | Observability, troubleshooting | Medium | Low | Low | S01 |
| S10 | Health checks (SMTP/IMAP/POP3/JMAP) | `internal/stalwart/service.go` | P1 | Service monitoring | Medium | Low | Low | S04 |
| S11 | DKIM/SPF/DMARC policy management | `internal/security/` | P1 | Email authentication, deliverability | Medium | **High** — reputation impact | High | S05 |
| S12 | TLS/certificate management | `internal/stalwart/config.go` | P1 | Secure transport | Medium | Low | **Critical** — cert management | Medium | S02 |
| S13 | Compatibility matrix | `internal/stalwart/compatibility.go` | P1 | Version safety | **Critical** — prevents upgrade failures | Medium | Medium | S01 |

### Phase 2 Dependencies

```
Phase 1 complete
    ↓
S01 → S02 → S03 → S04 → S05 → S06 → S07
  ↓                    ↓      ↓
S13                  S10     S08
                      ↓       ↓
                     S09    S11, S12
```

### Phase 2 Verification

```
orvix stalwart status              → Stalwart detected and running
orvix stalwart validate-config     → Generated config is valid
Domain creation API                → Domain created in Stalwart
Mailbox creation API               → Mailbox created in Stalwart
Queue viewer                       → Queue visible via API
Health checks                      → SMTP/IMAP/POP3/JMAP all healthy
```

---

## 5. Phase 3 — Security Layer (MVP Lines 1455–1467)

**Duration:** Week 6–7 | **Total Effort:** 120 hours | **MVP Tasks:** 12

| ID | Task | File | P0/P1/P2/P3 | Business Impact | Revenue Impact | Security Impact | Complexity | Dependencies |
|----|------|------|-------------|----------------|---------------|----------------|------------|--------------|
| SEC01 | Mail Firewall — 5-layer pipeline | `internal/firewall/engine.go`, `layers.go` | P1 | **Core security** — threat protection | High — enterprise requirement | **Critical** — first defense layer | High | Phase 2 |
| SEC02 | Firewall rules engine | `internal/firewall/rules.go` | P1 | Customizable security policies | Medium | High — customer control | High | SEC01 |
| SEC03 | IP reputation integration (AbuseIPDB) | `internal/firewall/reputation.go` | P1 | Block known bad actors | Medium | **Critical** — threat intelligence | Medium | SEC01 |
| SEC04 | Geo-blocking engine | `internal/firewall/geo.go` | P2 | Regional access control | Low | Medium — geo restrictions | Medium | SEC01 |
| SEC05 | Rate limiting (token bucket) | `internal/auth/` | P0 | **Brute force prevention** | Critical — service protection | **Critical** — DoS prevention | Medium | Phase 1 |
| SEC06 | Brute force protection | `internal/auth/` | P0 | **Account security** | Critical — customer trust | **Critical** — account takeover prevention | Medium | SEC05 |
| SEC07 | Guardian Agent (AI threat analysis) | `internal/guardian/agent.go`, `analyzer.go` | P1 | **Competitive differentiator** — AI security | High — unique selling point | **Critical** — AI threat detection | High | SEC01 |
| SEC08 | Guardian REST API | `internal/guardian/api.go` | P2 | Enterprise API integration | Medium | High | Medium | SEC07 |
| SEC09 | Auto-Heal system | `internal/autoheal/monitor.go`, `fixers.go` | P1 | **Operational reliability** | High — reduces downtime | Medium — service availability | High | Phase 2 |
| SEC10 | Heal history log | `internal/autoheal/history.go` | P2 | Audit trail for auto-fixes | Low | Low | Low | SEC09 |
| SEC11 | Stalwart config drift detection | `internal/stalwart/diagnostics.go` | P2 | Configuration integrity | Medium | Medium | Medium | Phase 2 |
| SEC12 | Safe diagnostic bundle exporter | `internal/stalwart/diagnostics.go` | P2 | Support operations | Low | **Critical** — prevents secret exposure | Medium | Phase 2 |

### Phase 3 Dependencies

```
Phase 2 complete
    ↓
SEC05, SEC06 ──→ Phase 4 (Auth)
    ↓
SEC01 → SEC02 → SEC03 → SEC04
    ↓
SEC07 → SEC08
    ↓
SEC09 → SEC10
    ↓
SEC11, SEC12
```

### Phase 3 Verification

```
Firewall blocks test threat        → Rules engine working
Guardian analyzes test email       → AI response correct
Rate limiting triggers             → Token bucket enforces limits
Auto-Heal detects and fixes issue  → Health → Fix cycle working
Diagnostic bundle exports          → No secrets exposed
```

---

## 6. Phase 4 — API Layer (MVP Lines 1469–1479)

**Duration:** Week 8–9 | **Total Effort:** 92 hours | **MVP Tasks:** 10

| ID | Task | File | P0/P1/P2/P3 | Business Impact | Revenue Impact | Security Impact | Complexity | Dependencies |
|----|------|------|-------------|----------------|---------------|----------------|------------|--------------|
| API01 | Auth system (JWT + refresh + 2FA TOTP) | `internal/auth/` | P0 | **Foundation of all API security** | Critical — all features locked | **Critical** — authentication | High | SEC05, SEC06 |
| API02 | User management API | `internal/api/` | P0 | User CRUD operations | **Critical** — core feature | High — RBAC enforcement | Medium | API01 |
| API03 | Domain management API | `internal/api/` | P0 | Domain CRUD operations | **Critical** — core feature | Medium | Medium | API01 |
| API04 | Mailbox API | `internal/api/` | P0 | Mailbox operations | **Critical** — core feature | Medium | Medium | API01 |
| API05 | Admin API (full) | `internal/api/` | P1 | Provider operations | High | Medium | High | API01–04 |
| API06 | Stalwart operations API wrapper | `internal/api/` | P1 | Programmatic Stalwart control | Medium | Low | Medium | API01 |
| API07 | **Instant Deployment API** | `internal/provision/` | P1 | **⭐ Competitive differentiator** — <30s provisioning | High — data center feature | High — automated deployment | High | API02, API03 |
| API08 | Webhook system (HMAC-SHA256) | `internal/api/` | P2 | Third-party integrations | Medium | Medium | **Critical** — webhook security | Medium | API01 |
| API09 | API key management | `internal/api/` | P1 | Programmatic access | High | Medium | **Critical** — key security | Medium | API01 |
| API10 | License-gated endpoint enforcement | `internal/flags/` | P0 | Tier enforcement on APIs | **Critical** — revenue protection | **Critical** — bypass prevention | Medium | F08, F09, API01 |

### Phase 4 Dependencies

```
Phase 3 complete
   ↓
API01 → API09, API10
   ↓
API02 → API03 → API04 → API06
   ↓            ↓
API05         API07
                ↓
              API08
```

### Phase 4 Verification

```
POST /api/v1/auth/login              → JWT + refresh token returned
POST /api/v1/auth/2fa                → TOTP verified
POST /api/v1/domains                 → Domain created
POST /api/v1/mailboxes               → Mailbox created
POST /api/v1/provision/domain        → < 30 seconds
License-gated endpoint returns 403   → Tier enforcement works
```

---

## 7. Phase 5 — Frontend (MVP Lines 1481–1495)

**Duration:** Week 10–12 | **Total Effort:** 218 hours | **MVP Tasks:** 14

| ID | Task | Tech | P0/P1/P2/P3 | Business Impact | Revenue Impact | Security Impact | Complexity | Dependencies |
|----|------|------|-------------|----------------|---------------|----------------|------------|--------------|
| FE01 | Design system + component library | Radix UI + Tailwind v4 | P0 | Consistent UI across all apps | High — brand consistency | Low — UI only | Medium | None |
| FE02 | **⭐ Webmail UI** | React 19 | **P0** | **Customer-facing product** | **Critical** — what users see | **Critical** — XSS, CSP | **Very High** | FE01, API04 |
| FE03 | **⭐ Smart Compose AI** | React + SSE | P1 | **⭐ Competitive differentiator** | High — AI feature | Medium — data privacy | High | FE02 |
| FE04 | Admin console (all panels) | React 19 | P0 | **Provider operations** | **Critical** — admin functions | High — admin access | **Very High** | FE01, API05 |
| FE05 | License management UI | React 19 | P1 | License activation, usage view | **Critical** — upsell visibility | Medium | Medium | FE04 |
| FE06 | DNS wizard UI | React 19 | P1 | Easy DNS setup | Medium | Low | Medium | FE04 |
| FE07 | Stalwart status/config UI | React 19 | P2 | Operational visibility | Medium | Low | Low | FE04 |
| FE08 | Firewall rules UI (no-code builder) | React 19 | P1 | Security rule management | High — enterprise requirement | High | High | FE04 |
| FE09 | Auto-Heal dashboard | React 19 | P2 | Health/auto-fix visibility | Medium | Low | Medium | FE04 |
| FE10 | Guardian Agent dashboard | React 19 | P1 | Threat visibility | High — enterprise requirement | Medium | High | FE04 |
| FE11 | Email Intelligence dashboard | React 19 | P2 | Analytics/insights | Medium | Low | Low | High | FE04 |
| FE12 | Update manager UI | React 19 | P1 | Update/rollback control | High — operational | **Critical** — update integrity | Medium | FE04 |
| FE13 | Feature flags UI | React 19 | P2 | License-aware feature view | Medium | Low | Low | FE04 |
| FE14 | PWA manifest + service worker | Service Worker | P1 | Mobile installability | Medium | Low | Low | FE02 |

### Phase 5 Dependencies

```
Phase 4 complete
   ↓
FE01 → FE02 → FE03
   ↓      ↓
FE04 → FE05, FE06, FE07, FE08, FE09, FE10, FE11, FE12, FE13
   ↓
FE14
```

### Phase 5 Verification

```
Webmail: compose, send, receive    → Full email workflow works
Admin: create domain, add mailbox  → Provider operations complete
Smart Compose: AI writes reply     → SSE streaming works
Firewall rules UI: create rule     → Rule applied to engine
Dashboard: real-time stats         → Metrics displayed correctly
PWA: install on mobile             → Service worker registered
```

---

## 8. Phase 6 — Advanced Features (MVP Lines 1497–1511)

**Duration:** Week 13–15 | **Total Effort:** 192 hours | **MVP Tasks:** 14

| ID | Task | File | P0/P1/P2/P3 | Business Impact | Revenue Impact | Security Impact | Complexity | Dependencies |
|----|------|------|-------------|----------------|---------------|----------------|------------|--------------|
| AF01 | Calendar + Contacts + Tasks | `internal/`, React | P2 | **Core productivity** — CalDAV/CardDAV | High — feature parity | Low | High | FE02 |
| AF02 | ActiveSync (ISP+ tier) | `internal/` | P2 | Mobile device sync | High — ISP requirement | Medium | High | AF01 |
| AF03 | **⭐ Smart Migration Tool** | `internal/migration/` | **P1** | **⭐ Competitive differentiator** | **Critical** — customer acquisition | Medium | **Very High** | Phase 2 |
| AF04 | Zero-downtime migration engine | `internal/migration/` | P2 | Seamless customer migration | High | Low | High | AF03 |
| AF05 | Anti-spam policy engine + quarantine | `internal/antispam/` | P1 | Spam management | High | Low | Medium | Phase 3 |
| AF06 | Email Archiving + Legal Hold | `internal/compliance/` | P2 | **Regulatory compliance** | High — enterprise requirement | **Critical** — legal evidence | High | Phase 2 |
| AF07 | Compliance Center (GDPR/HIPAA/SOX) | `internal/compliance/` | P2 | **Enterprise compliance** | High — enterprise requirement | **Critical** — regulatory | High | AF06 |
| AF08 | Zero-Knowledge Encryption | `internal/encryption/` | P3 | **Enterprise security** | Medium — enterprise differentiator | **Critical** — encryption | **Very High** | FE02 |
| AF09 | Collaboration Layer / Shared Inbox | `internal/collaboration/` | P2 | **Enterprise feature** | High — enterprise requirement | Medium | High | FE02 |
| AF10 | Multi-Cloud Storage (S3/GCS/Azure) | `internal/storage/cloud/` | P2 | Storage flexibility | Medium | Low | Medium | Phase 2 |
| AF11 | Email Intelligence AI insights | `internal/intelligence/` | P2 | Analytics feature | Medium | Low | Medium | Phase 2 |
| AF12 | Backup & Restore | `internal/storage/` | P1 | **Disaster recovery** | **Critical** — data protection | **Critical** — backup security | Medium | Phase 2 |
| AF13 | LDAP/AD sync (Enterprise) | `internal/auth/` | P2 | **Enterprise directory integration** | High — enterprise requirement | **Critical** — identity security | High | API01 |
| AF14 | SSO — SAML 2.0 + OAuth2 (Enterprise) | `internal/auth/` | P2 | **Enterprise auth** | High — enterprise requirement | **Critical** — federation security | High | API01 |

### Phase 6 Dependencies

```
Phase 5 complete
    ↓
AF01 → AF02
AF03 → AF04
AF05 (from Phase 3)
AF06 → AF07
AF08 (standalone, needs FE02)
AF09 (needs FE02)
AF10 (needs Phase 2)
AF11 (needs Phase 2)
AF12 (standalone)
AF13 → AF14
```

### Phase 6 Verification

```
Migration: sync 1000 emails        → IMAP sync works
Calendar: create event, invite     → CalDAV flow works
Compliance: legal hold activated   → Email retention enforced
ZKE: encrypt/decrypt email         → Zero-knowledge flow works
Shared inbox: assign, reply, note  → Collaboration works
Backup: backup + restore           → Data recoverable
LDAP sync: users imported          → Directory integration works
SSO: SAML login                    → Federation works
```

---

## 9. Phase 7 — Hardening & Launch (MVP Lines 1513–1528)

**Duration:** Week 16–18 | **Total Effort:** 176 hours | **MVP Tasks:** 14

| ID | Task | P0/P1/P2/P3 | Business Impact | Revenue Impact | Security Impact | Complexity | Dependencies |
|----|------|-------------|----------------|---------------|----------------|------------|--------------|
| H01 | Full security audit | P0 | **Trust** — customer confidence | **Critical** — enterprise sales | **Critical** — all security | High | All phases |
| H02 | Penetration testing | P0 | **Trust** — vulnerability discovery | **Critical** — enterprise sales | **Critical** — all security | High | H01 |
| H03 | Load testing (10k concurrent) | P0 | **Performance validation** | **Critical** — ISP/Enterprise tiers | High — DoS resilience | Medium | All phases |
| H04 | Deliverability testing | P1 | **Email reputation** | **Critical** — customer trust | Medium | Low | Phase 2 |
| H05 | **Auto-update system** (safe pipeline) | **P0** | **Operational safety** | **Critical** — update reliability | **Critical** — update integrity | High | All phases |
| H06 | Update channels (Stable/Beta/EA/Nightly) | P1 | Release management | Medium | Low | Medium | H05 |
| H07 | One-line installer script | P0 | **Customer acquisition** | **Critical** — first experience | Medium | Medium | All phases |
| H08 | Full API documentation | P1 | **Developer adoption** | High | Low | Low | Phase 4 |
| H09 | Admin documentation | P1 | **Customer success** | High | Low | Low | All phases |
| H10 | Marketing website (orvix.email) | P1 | **Customer acquisition** | **Critical** — public face | Low | Low | None |
| H11 | License purchase flow + customer portal | P0 | **Revenue generation** | **Critical** — revenue | **Critical** — billing security | High | F08 |
| H12 | Update server setup | P0 | **Update infrastructure** | **Critical** — update reliability | **Critical** — update security | Medium | H05 |
| H13 | Status page (status.orvix.email) | P2 | **Transparency** | Medium | Low | Low | None |
| H14 | Stalwart compatibility certification | P1 | **Compatibility guarantee** | High | Medium | Medium | Phase 2 |

### Phase 7 Dependencies

```
All phases complete
    ↓
H05, H12 ──→ H06 (Update infrastructure)
H07 (Installer)
H08, H09 (Documentation)
H10 (Marketing site)
H11, F08 (License + purchase flow)
H13 (Status page)
H14, S13 (Stalwart compatibility)

H01 → H02 (Security hardening)
H03 (Performance hardening)
H04 (Deliverability)
```

### Phase 7 Verification

```
Security audit: zero critical findings
Penetration test: all findings resolved
Load test: 10k concurrent connections handled
Deliverability: Gmail/Outlook/Yahoo inbox rate >95%
Auto-update: install → verify → rollback cycle complete
Installer: clean install on Ubuntu 24.04, Debian 12, RHEL 9
API docs: all endpoints documented with examples
```

---

## 10. Phase Dependency Graph (Full)

```
Phase 1 ── Foundation ─────────────────────────────────────────────────┐
  ↓                                                                     │
Phase 2 ── Stalwart Integration ───────────────────────────────────────┤
  ↓                                                                     │
Phase 3 ── Security Layer ─────────────────────────────────────────────┤
  ↓                                                                     │
Phase 4 ── API Layer ──────────────────────────────────────────────────┤
  ↓                                                                     │
Phase 5 ── Frontend ───────────────────────────────────────────────────┤
  ↓                                                                     │
Phase 6 ── Advanced Features ──────────────────────────────────────────┤
  ↓                                                                     │
Phase 7 ── Hardening & Launch ◄────────────────────────────────────────┘
```

---

## 11. P0 Critical Items Summary

| ID | Item | Phase | Reason |
|----|------|-------|--------|
| F01 | Project structure | 1 | Foundation |
| F02 | Go module + entry point | 1 | Binary must compile |
| F03 | Config system | 1 | Everything needs config |
| F04 | Database layer | 1 | All persistence |
| F05 | Additive-only migrations | 1 | Safe schema evolution |
| F08 | License engine | 1 | **Revenue — tier enforcement** |
| F09 | Feature flags engine | 1 | **Revenue — feature gating** |
| S01 | Stalwart install/detection | 2 | **Core mail engine** |
| S02 | Stalwart config generator | 2 | Correct operation |
| S03 | Stalwart config validator | 2 | **Security — prevent misconfig** |
| S04 | Stalwart service lifecycle | 2 | Service management |
| S05 | Domain provisioning | 2 | **Core feature** |
| S06 | Mailbox provisioning | 2 | **Core feature** |
| SEC05 | Rate limiting | 3 | **Security — brute force prevention** |
| SEC06 | Brute force protection | 3 | **Security — account protection** |
| API01 | Auth system (JWT + 2FA) | 4 | **Security — all API auth depends** |
| API02 | User management API | 4 | **Core feature** |
| API03 | Domain management API | 4 | **Core feature** |
| API04 | Mailbox API | 4 | **Core feature** |
| API10 | License-gated endpoint enforcement | 4 | **Revenue — tier protection** |
| FE01 | Design system | 5 | All UI depends |
| FE02 | Webmail UI | 5 | **Customer-facing product** |
| FE04 | Admin console | 5 | **Provider operations** |
| H01 | Security audit | 7 | **Trust — enterprise requirement** |
| H02 | Penetration testing | 7 | **Trust — enterprise requirement** |
| H03 | Load testing | 7 | **Performance guarantee** |
| H05 | Auto-update system | 7 | **Operational safety** |
| H07 | One-line installer | 7 | **Customer acquisition** |
| H11 | License purchase flow | 7 | **Revenue generation** |
| H12 | Update server setup | 7 | **Update infrastructure** |

---

## 12. Risk Mitigation Summary

| Risk | Phase | Mitigation |
|------|-------|------------|
| Stalwart API breaking changes | 2 | Abstraction layer, compatibility matrix |
| Redis SPOF | 1 | Graceful degradation patterns |
| Frontend complexity | 5 | Shared component library; start early |
| Migration IMAP reliability | 6 | Robust error handling, validation |
| Compliance coverage | 6 | Built into Phase 6, not deferred |

---

**Conclusion:** This Master Execution Plan converts all MVP phases into 87 executable tasks classified by priority, with clear business impact, revenue impact, security impact, complexity, and dependency tracking. The plan enforces the MVP's phase ordering and does not introduce any new phases or deviate from the approved roadmap.
