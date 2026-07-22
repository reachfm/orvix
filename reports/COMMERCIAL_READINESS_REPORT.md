# COMMERCIAL READINESS REPORT

## Orvix Enterprise Mail (OrvixEM)

**Date:** 2026-06-05
**Reference:** MVP Full Document
**Scoring:** 0–100 scale in each dimension

---

## 1. Overall Commercial Readiness

| Dimension | Score | Status |
|-----------|-------|--------|
| Architecture | 75 | Well-defined architecture, not implemented |
| Security | 70 | Comprehensive security plan, not implemented |
| Scalability | 65 | Good multi-tier plan, unproven |
| Licensing | 30 | License engine designed, no infrastructure |
| Maintainability | 75 | Clean Go structure, no code yet |
| Enterprise Readiness | 25 | Enterprise features designed, not built |
| Datacenter Readiness | 20 | Datacenter features planned, not built |
| Reseller Readiness | 15 | Reseller framework defined, not built |
| Revenue Readiness | 10 | No payment processing, no purchase flow |
| **Overall** | **43** | Well-planned but pre-implementation |

---

## 2. Dimension Scoring

### 2.1 Architecture — Score: 75/100

| Criteria | Score | Reason |
|----------|-------|--------|
| Separation of concerns | 90 | Clear Stalwart/Orvix boundary |
| Component modularity | 85 | Well-structured package hierarchy |
| Technology choices | 85 | Go + React + Postgres = strong stack |
| Integration design | 70 | Stalwart integration well-planned but untested |
| Documentation | 60 | Good architecture doc; implementation details TBD |
| **Average** | **75** | |

**What's missing:** Implementation. The architecture is well-designed on paper but has zero code.

### 2.2 Security — Score: 70/100

| Criteria | Score | Reason |
|----------|-------|--------|
| Authentication design | 80 | JWT + 2FA + Argon2id well-specified |
| Authorization design | 70 | RBAC defined but permission matrix incomplete |
| Input validation | 80 | bluemonday + CSP + sandboxed iframes specified |
| Transport security | 85 | TLS + HSTS + DANE + MTA-STS specified |
| API security | 75 | Rate limiting + webhook signing + key hashing |
| Email security | 80 | DKIM + SPF + DMARC + ARC all specified |
| License security | 75 | RS256 + hardware binding + tamper detection |
| Incident response | 30 | No incident response plan defined |
| **Average** | **70** | |

**What's missing:** Implementation, incident response plan, SIEM integration.

### 2.3 Scalability — Score: 65/100

| Criteria | Score | Reason |
|----------|-------|--------|
| Single-node (SMB) | 80 | SQLite + embedded Bleve works for 500 mailboxes |
| Multi-node (ISP) | 60 | PostgreSQL + Redis cluster design unproven |
| Enterprise (unlimited) | 40 | Clustering design defined but not validated |
| Storage scaling | 70 | S3 abstraction enables scale |
| Performance testing | 20 | No benchmarks exist; planned for Phase 7 |
| **Average** | **65** | |

**What's missing:** Load test results, Stalwart clustering validation, Redis cluster configuration.

### 2.4 Licensing — Score: 30/100

| Criteria | Score | Reason |
|----------|-------|--------|
| License key design | 80 | JWT RS256 with offline validation |
| Tier enforcement | 50 | Feature flags defined but not mapped to tiers |
| License server | 10 | API designed but not built |
| Offline activation | 30 | Grace period defined, no implementation |
| Hardware binding | 20 | Concept exists, no implementation |
| Tamper detection | 20 | Concept exists, no implementation |
| Canary tokens | 10 | Mentioned in Phase 1, not designed |
| **Average** | **30** | |

**What's missing:** License server infrastructure, activation flow, purchase flow, hardware binding, tamper detection.

### 2.5 Maintainability — Score: 75/100

| Criteria | Score | Reason |
|----------|-------|--------|
| Code organization | 85 | Clear package structure defined |
| Go conventions | 80 | Standard Go project layout |
| Migration strategy | 90 | Additive-only policy is excellent |
| Update system | 70 | Safe pipeline designed, not built |
| Documentation | 60 | API docs planned but not started |
| Test coverage | 30 | No test plan defined |
| **Average** | **75** | |

**What's missing:** Test infrastructure, CI pipeline, developer documentation.

### 2.6 Enterprise Readiness — Score: 25/100

| Criteria | Score | Reason |
|----------|-------|--------|
| SSO (SAML/OAuth2) | 10 | Specified for Enterprise tier, not built |
| LDAP/AD sync | 10 | Specified for Enterprise tier, not built |
| Compliance (GDPR/HIPAA/SOX) | 15 | Compliance Center designed, not built |
| Legal hold / eDiscovery | 10 | Specified, not built |
| DLP | 10 | Specified, not built |
| Zero-Knowledge Encryption | 15 | Designed, not built |
| Collaboration (shared inbox) | 15 | Designed, not built |
| Audit logging | 40 | Schema defined, implementation needed |
| Backup & restore | 30 | Designed, not built |
| **Average** | **25** | |

**What's missing:** All Enterprise-tier features are in Phase 6. None built yet.

### 2.7 Datacenter Readiness — Score: 20/100

| Criteria | Score | Reason |
|----------|-------|--------|
| Instant Deploy API | 30 | API designed, not built |
| Multi-tenant isolation | 40 | RBAC designed, tenant isolation defined |
| White-label support | 20 | Concept defined, not built |
| Clustering | 15 | Designed (ISP: 3 nodes, Enterprise: unlimited), not built |
| Resource management (quotas) | 30 | DB schema includes quotas, not enforced |
| Provisioning automation | 20 | Provisioning API designed, not built |
| **Average** | **20** | |

**What's missing:** All datacenter features defined in MVP but in Phase 4/5/6.

### 2.8 Reseller Readiness — Score: 15/100

| Criteria | Score | Reason |
|----------|-------|--------|
| Reseller account management | 20 | Concept defined (ISP+ tier), not built |
| Sub-account limits | 15 | Concept defined, not built |
| White-label config | 20 | Concept defined, not built |
| Usage reports per reseller | 15 | Concept defined, not built |
| Reseller pricing/commission | 10 | Not defined in MVP |
| Tiered reseller program | 10 | Not defined |
| **Average** | **15** | |

**What's missing:** The entire reseller panel is in Phase 5 (admin console). Reseller pricing model needs definition.

### 2.9 Revenue Readiness — Score: 10/100

| Criteria | Score | Reason |
|----------|-------|--------|
| Pricing defined | 80 | 3 tiers clearly defined ($500/$1200/$2500) |
| Purchase flow | 5 | Not designed or built |
| Payment processing | 0 | No payment processor integration |
| License activation | 10 | Key mechanism designed, activation flow not built |
| Customer portal | 5 | `portal.orvix.email` defined, not built |
| Trial mechanism | 0 | Not defined |
| Billing integration | 0 | Not defined |
| Invoicing | 0 | Not defined |
| Revenue operations | 0 | No metrics/tracking |
| **Average** | **10** | |

**What's missing:** Payment processor (Stripe), purchase flow, customer portal, trial generation, billing system. These are critical for revenue but only lightly referenced in the MVP.

---

## 3. Readiness by Customer Segment

| Customer Segment | Readiness Score | Reason |
|-----------------|----------------|--------|
| **SMB / Small Business** | 30 | Core features designed; webmail and admin not built |
| **ISP / Regional Hosting** | 20 | SMB features + reseller/clustering not built |
| **Enterprise** | 15 | All enterprise features in Phase 6 |
| **Data Centers** | 10 | Instant Deploy, clustering, white-label not built |
| **MSPs / Resellers** | 10 | Reseller panel, billing, white-label not built |

---

## 4. Revenue Potential Assessment

### 4.1 TAM/SAM/SOM

| Segment | TAM (Est.) | OrvixEM Target |
|---------|-----------|---------------|
| SMB email hosting (self-hosted) | $500M | $2M (0.4%) |
| ISP email infrastructure | $800M | $3M (0.38%) |
| Enterprise private mail | $1.2B | $2M (0.17%) |
| Data center operations | $400M | $1M (0.25%) |
| **Total Addressable** | **$2.9B** | **$8M** |

### 4.2 Revenue Model Validation

| Factor | Assessment |
|--------|-----------|
| Pricing vs competitors ($500–2500 vs $3000–10000) | ✅ Competitive advantage |
| Tier differentiation (SMB → ISP → Enterprise) | ✅ Clear upsell path |
| Feature gating by license | ✅ Designed but not implemented |
| Customer segments targeted | ✅ Well-defined |
| Payment processing | ❌ Not implemented |
| Purchase flow | ❌ Not implemented |
| Trial/demo mechanism | ❌ Not defined |

---

## 5. Launch Readiness Checklist

| Item | Status | Required for Revenue |
|------|--------|---------------------|
| Product features per MVP | 🔴 0% complete | ✅ Yes |
| Purchase flow | 🔴 Not started | ✅ Yes |
| Payment processing | 🔴 Not started | ✅ Yes |
| License activation | 🔴 Not started | ✅ Yes |
| Customer portal | 🔴 Not started | ✅ Yes |
| Marketing website | 🔴 Not started | ✅ Yes |
| Documentation | 🔴 Not started | ✅ Yes |
| Installer | 🔴 Not started | ✅ Yes |
| Trial mechanism | 🔴 Not defined | ✅ Yes |
| Billing integration | 🔴 Not started | ✅ Yes |

---

## 6. Recommendations to Improve Readiness

### 6.1 Immediate (Pre-Phase 1)

| Action | Impact on Score | Effort |
|--------|----------------|--------|
| Define payment processor integration (Stripe) | +20 Revenue Readiness | Low |
| Define trial license generation flow | +15 Revenue Readiness | Low |
| Design customer portal mockups | +10 Revenue Readiness | Low |
| Define reseller pricing model | +20 Reseller Readiness | Low |

### 6.2 During Implementation

| Phase | Action | Score Improvement |
|-------|--------|------------------|
| Phase 1 | Build license engine with activation flow | +20 Licensing |
| Phase 1 | Build feature flag → tier mapping | +10 Licensing |
| Phase 4 | Build Instant Deploy API (datacenter feature) | +20 Datacenter |
| Phase 5 | Build reseller panel in admin console | +40 Reseller |
| Phase 6 | Build all Enterprise features | +50 Enterprise |
| Phase 7 | Build purchase flow + payment integration | +40 Revenue |

### 6.3 Post-MVP (Critical for Commercial Success)

| Priority | Item | Target |
|----------|------|--------|
| P0 | Stripe payment integration | Before 1.0.0 launch |
| P0 | Customer portal (portal.orvix.email) | Before 1.0.0 launch |
| P1 | 14-day free trial | Before 1.0.0 launch |
| P1 | Automated invoicing | Before 1.0.0 launch |
| P1 | Email onboarding sequence | Before 1.0.0 launch |
| P2 | Partner/reseller portal | Within 3 months of launch |
| P2 | Usage-based billing option | Within 6 months of launch |

---

## 7. Score Improvement Plan

| Dimension | Current | After Phase 4 | After Phase 7 | Target |
|-----------|---------|--------------|--------------|--------|
| Architecture | 75 | 80 | 85 | 90 |
| Security | 70 | 75 | 85 | 90 |
| Scalability | 65 | 70 | 80 | 85 |
| Licensing | 30 | 50 | 70 | 85 |
| Maintainability | 75 | 78 | 82 | 85 |
| Enterprise Readiness | 25 | 35 | 75 | 85 |
| Datacenter Readiness | 20 | 50 | 70 | 80 |
| Reseller Readiness | 15 | 40 | 60 | 75 |
| Revenue Readiness | 10 | 10 | 60 | 80 |
| **Overall** | **43** | **54** | **74** | **84** |

---

## 8. Key Commercial Risks

| Risk | Impact | Probability | Mitigation |
|------|--------|------------|------------|
| Cannot process payments at launch | **Revenue = $0** | Medium | Prioritize Stripe integration in Phase 7 |
| License bypass/piracy | Revenue loss | Medium | RS256 + hardware binding + telemetry |
| Pricing too low for enterprise | Left money on table | Medium | Annual price review; enterprise add-on SKUs |
| No trial mechanism | Lost customer acquisition | Medium | 14-day auto-expiring trial license generation |
| Reseller model undefined | Missed MSP channel | Medium | Define before Phase 7 launch |
| Competitor price drop | Margin pressure | Low | Focus on feature differentiation |
| Stalwart licensing costs | Margin erosion | Low | Verify redistribution terms early |

---

**Conclusion:** The project is in a planning-complete, pre-implementation state. Commercial readiness score is 43/100, which is expected at this stage. The architecture, pricing, and feature plans are solid. The critical gaps are in revenue infrastructure (payment processing, purchase flow, customer portal) which must be prioritized in Phase 7 to ensure revenue generation at launch. The most important commercial action is to define and implement the Stripe integration and license purchase flow before 1.0.0 release.
