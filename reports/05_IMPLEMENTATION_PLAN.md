# IMPLEMENTATION PLAN

## Orvix Enterprise Mail (OrvixEM)

**Date:** 2026-06-05
**Source:** MVP Phase Definitions (Lines 1427–1528)
**Duration Estimate:** 18 weeks (Phases 1–7)

---

## 1. Implementation Overview

The MVP defines exactly 7 implementation phases (lines 1427–1528):

| Phase | Focus | Duration | Dependencies |
|-------|-------|----------|--------------|
| Phase 1 | Foundation | Week 1–2 | None |
| Phase 2 | Stalwart Core Integration | Week 3–5 | Phase 1 |
| Phase 3 | Security Layer | Week 6–7 | Phase 2 |
| Phase 4 | API Layer | Week 8–9 | Phase 3 |
| Phase 5 | Frontend | Week 10–12 | Phase 4 |
| Phase 6 | Advanced Features | Week 13–15 | Phase 5 |
| Phase 7 | Hardening & Launch | Week 16–18 | Phase 6 |

---

## 2. Detailed Phase Breakdown

### Phase 1: Foundation (Week 1–2)

**MVP Reference:** Lines 1428–1438

| Task | Description | Files | Estimated Effort |
|------|-------------|-------|-----------------|
| 1.1 | Project structure setup | All directories per MVP structure | 4 hours |
| 1.2 | Go module initialization | `go.mod`, `cmd/orvix/main.go` | 2 hours |
| 1.3 | Config system | `internal/config/`, `orvix.yaml` | 8 hours |
| 1.4 | Database layer | `internal/models/`, GORM setup, SQLite + PostgreSQL | 12 hours |
| 1.5 | Migration system | `internal/migrations/`, additive-only engine | 8 hours |
| 1.6 | Logging system | `internal/metrics/`, Zap integration | 4 hours |
| 1.7 | Metrics endpoints | Prometheus metrics | 4 hours |
| 1.8 | License engine | `internal/license/`, JWT RS256 validation | 16 hours |
| 1.9 | Feature flags engine | `internal/flags/`, license + channel + admin | 8 hours |
| 1.10 | Versioning metadata | Semver, build commit, channel | 4 hours |
| 1.11 | Code watermarking | Embedded copyright + canary tokens | 4 hours |

**Phase 1 Total: 74 hours (~2 weeks)**

### Phase 2: Stalwart Core Integration (Week 3–5)

**MVP Reference:** Lines 1440–1453

| Task | Description | Effort |
|------|-------------|--------|
| 2.1 | Stalwart install/detection flow | 8 hours |
| 2.2 | Stalwart config generator | 16 hours |
| 2.3 | Stalwart config validator | 8 hours |
| 2.4 | Stalwart service lifecycle manager | 12 hours |
| 2.5 | Domain provisioning through OrvixEM | 12 hours |
| 2.6 | Mailbox provisioning through OrvixEM | 12 hours |
| 2.7 | Alias/routing provisioning | 8 hours |
| 2.8 | Queue visibility and controls | 8 hours |
| 2.9 | Stalwart log/event ingestion | 12 hours |
| 2.10 | Stalwart health checks | 8 hours |
| 2.11 | DKIM/SPF/DMARC policy management | 12 hours |
| 2.12 | TLS/certificate management | 8 hours |
| 2.13 | Compatibility matrix | 4 hours |

**Phase 2 Total: 128 hours (~3 weeks)**

### Phase 3: Security Layer (Week 6–7)

**MVP Reference:** Lines 1455–1467

| Task | Description | Effort |
|------|-------------|--------|
| 3.1 | Mail Firewall engine (5 layers) | 24 hours |
| 3.2 | Firewall rules engine (no-code builder) | 12 hours |
| 3.3 | IP reputation integration (AbuseIPDB) | 6 hours |
| 3.4 | Geo-blocking engine | 8 hours |
| 3.5 | Rate limiting (token bucket) | 8 hours |
| 3.6 | Brute force protection | 6 hours |
| 3.7 | Guardian Agent (AI threat analysis) | 16 hours |
| 3.8 | Guardian REST API | 8 hours |
| 3.9 | Auto-Heal system | 16 hours |
| 3.10 | Heal history log | 4 hours |
| 3.11 | Stalwart config drift detection | 6 hours |
| 3.12 | Safe diagnostic bundle exporter | 6 hours |

**Phase 3 Total: 120 hours (~2 weeks)**

### Phase 4: API Layer (Week 8–9)

**MVP Reference:** Lines 1469–1479

| Task | Description | Effort |
|------|-------------|--------|
| 4.1 | Auth system (JWT + refresh + 2FA TOTP) | 16 hours |
| 4.2 | User management API | 8 hours |
| 4.3 | Domain management API | 8 hours |
| 4.4 | Mailbox API | 8 hours |
| 4.5 | Admin API (full) | 12 hours |
| 4.6 | Stalwart operations API wrapper | 8 hours |
| 4.7 | Instant Deployment API | 12 hours |
| 4.8 | Webhook system | 8 hours |
| 4.9 | API key management | 6 hours |
| 4.10 | License-gated endpoint enforcement | 6 hours |

**Phase 4 Total: 92 hours (~2 weeks)**

### Phase 5: Frontend (Week 10–12)

**MVP Reference:** Lines 1481–1495

| Task | Description | Effort |
|------|-------------|--------|
| 5.1 | Design system + component library | 24 hours |
| 5.2 | Webmail UI (full Outlook-level) | 60 hours |
| 5.3 | Smart Compose AI (streamed) | 16 hours |
| 5.4 | Admin console (all panels) | 40 hours |
| 5.5 | License management UI | 8 hours |
| 5.6 | DNS wizard UI | 8 hours |
| 5.7 | Stalwart status/config UI | 6 hours |
| 5.8 | Firewall rules UI (no-code builder) | 12 hours |
| 5.9 | Auto-Heal dashboard | 8 hours |
| 5.10 | Guardian Agent dashboard | 12 hours |
| 5.11 | Email Intelligence dashboard | 8 hours |
| 5.12 | Update manager UI | 8 hours |
| 5.13 | Feature flags UI | 4 hours |
| 5.14 | PWA manifest + service worker | 4 hours |

**Phase 5 Total: 218 hours (~3 weeks)**

### Phase 6: Advanced Features (Week 13–15)

**MVP Reference:** Lines 1497–1511

| Task | Description | Effort |
|------|-------------|--------|
| 6.1 | Calendar + Contacts + Tasks | 24 hours |
| 6.2 | ActiveSync (ISP+ tier) | 16 hours |
| 6.3 | Smart Migration Tool | 24 hours |
| 6.4 | Zero-downtime migration engine | 12 hours |
| 6.5 | Anti-spam policy engine + quarantine | 12 hours |
| 6.6 | Email Archiving + Legal Hold | 12 hours |
| 6.7 | Compliance Center | 16 hours |
| 6.8 | Zero-Knowledge Encryption | 12 hours |
| 6.9 | Collaboration Layer / Shared Inbox | 12 hours |
| 6.10 | Multi-Cloud Storage | 8 hours |
| 6.11 | Email Intelligence AI insights | 8 hours |
| 6.12 | Backup & Restore | 12 hours |
| 6.13 | LDAP/AD sync | 12 hours |
| 6.14 | SSO (SAML 2.0 + OAuth2) | 12 hours |

**Phase 6 Total: 192 hours (~3 weeks)**

### Phase 7: Hardening & Launch (Week 16–18)

**MVP Reference:** Lines 1513–1528

| Task | Description | Effort |
|------|-------------|--------|
| 7.1 | Full security audit | 16 hours |
| 7.2 | Penetration testing | 16 hours |
| 7.3 | Load testing (10k concurrent) | 16 hours |
| 7.4 | Deliverability testing | 8 hours |
| 7.5 | Auto-update system (safe pipeline) | 16 hours |
| 7.6 | Update channels | 8 hours |
| 7.7 | One-line installer script | 8 hours |
| 7.8 | Full API documentation | 16 hours |
| 7.9 | Admin documentation | 12 hours |
| 7.10 | Marketing website | 24 hours |
| 7.11 | License purchase flow + portal | 16 hours |
| 7.12 | Update server setup | 8 hours |
| 7.13 | Status page setup | 4 hours |
| 7.14 | Stalwart compatibility certification | 8 hours |

**Phase 7 Total: 176 hours (~3 weeks)**

---

## 3. Overall Effort Summary

| Phase | Hours | Weeks | Dependencies Met |
|-------|-------|-------|-----------------|
| Phase 1: Foundation | 74 | 2 | ✅ None |
| Phase 2: Stalwart Integration | 128 | 3 | ✅ Phase 1 |
| Phase 3: Security Layer | 120 | 2 | ✅ Phase 2 |
| Phase 4: API Layer | 92 | 2 | ✅ Phase 3 |
| Phase 5: Frontend | 218 | 3 | ✅ Phase 4 |
| Phase 6: Advanced Features | 192 | 3 | ✅ Phase 5 |
| Phase 7: Hardening & Launch | 176 | 3 | ✅ Phase 6 |
| **Total** | **1000** | **18** | |

---

## 4. Critical Path

```
Phase 1 → Phase 2 → Phase 3 → Phase 4 → Phase 5 → Phase 6 → Phase 7
   ↑          ↑          ↑          ↑          ↑          ↑          ↑
  2 wks      3 wks      2 wks      2 wks      3 wks      3 wks      3 wks
  74 hrs     128 hrs    120 hrs    92 hrs     218 hrs    192 hrs    176 hrs
```

The critical path is strict — each phase depends on the previous. Any delay in Phase 2 (Stalwart integration) cascades to all subsequent phases.

---

## 5. Resource Recommendations

| Phase | Recommended Team | Skills Required |
|-------|-----------------|-----------------|
| Phase 1 | 2 Backend Engineers | Go, GORM, SQLite, PostgreSQL, security |
| Phase 2 | 1 Backend Engineer + 1 DevOps | Stalwart, SMTP/IMAP protocols, Go |
| Phase 3 | 1 Security Engineer + 1 Backend | Security, AI/ML, Go |
| Phase 4 | 2 Backend Engineers | REST API design, Go, Fiber |
| Phase 5 | 2 Frontend Engineers + 1 Designer | React, Radix UI, Tailwind, TypeScript |
| Phase 6 | 2 Full-Stack Engineers | Various (migration, compliance, encryption) |
| Phase 7 | Full Team | DevOps, security, documentation, marketing |

---

## 6. Key Milestones

| Milestone | Phase | Deliverable |
|-----------|-------|-------------|
| M1 | Phase 1 | Foundation complete: config, DB, license, flags, versioning |
| M2 | Phase 2 | Stalwart integrated: config, lifecycle, provisioning, health |
| M3 | Phase 3 | Security layer: firewall, Guardian, Auto-Heal |
| M4 | Phase 4 | API layer: auth, endpoints, Instant Deploy, webhooks |
| M5 | Phase 5 | Frontend: webmail, admin, portal complete |
| M6 | Phase 6 | Advanced features: migration, compliance, encryption |
| M7 | Phase 7 | Launch-ready: tested, documented, packaged |

---

## 7. Verification

- **Phase 1:** `orvix start` runs, DB connects, license validates, feature flags work
- **Phase 2:** Stalwart starts/stops via OrvixEM, domains provisioned, config validated
- **Phase 3:** Firewall blocks threats, Guardian analyzes emails, Auto-Heal fixes issues
- **Phase 4:** All API endpoints respond, auth works, Instant Deploy provisions in < 30s
- **Phase 5:** Full webmail functional, admin panels complete, mobile PWA installable
- **Phase 6:** Migration works end-to-end, compliance center functional, encryption operable
- **Phase 7:** Security audit passes, load test passes, installer works on clean server

---

**Conclusion:** The implementation plan maps exactly to the 7-phase MVP build order. Total estimated effort is 1000 hours (18 weeks) for a full team. The Stalwart integration phase (Phase 2) is the highest risk and should receive the most careful planning and resource allocation.
