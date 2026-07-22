# ARCHITECTURE ANALYSIS REPORT

## Orvix Enterprise Mail (OrvixEM)

**Date:** 2026-06-05
**Reference:** MVP Sections: Architecture Overview, Tech Stack, Security Architecture
**MVP Lines:** 33–105, 199–314, 556–616

---

## 1. Architecture Overview

The MVP defines a two-layer architecture (lines 33–68):

```
┌────────────────────────────────────────────────────────────────┐
│                    Orvix Enterprise Mail Platform               │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │                Stalwart Core Engine                        │  │
│  │  SMTP │ IMAP │ POP3 │ JMAP │ Mail Store │ Queue │ Auth   │  │
│  └──────────────────────────────────────────────────────────┘  │
│                            │                                     │
│                            ▼                                     │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │                 Orvix Enterprise Layer                     │  │
│  │  Web Server │ Admin │ License │ Updates │ Security        │  │
│  │  Migration  │ AI    │ DNS     │ Monitoring │ Deploy API   │  │
│  └──────────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────────┘
```

**Key architectural principle (line 99):** "Use Stalwart as the proven mail protocol and storage core instead of rebuilding the mail engine from zero."

---

## 2. Component Architecture Breakdown

### 2.1 Stalwart Core Engine (Responsibility)

| Component | MVP Reference | Responsibility |
|-----------|--------------|----------------|
| SMTP Server | Line 222 | Mail transport protocol |
| IMAP Server | Line 223 | Mail retrieval protocol |
| POP3 Server | Line 224 | Mail retrieval protocol |
| JMAP | Line 225 | Modern mail sync protocol |
| Mail Storage | Line 226 | Core message storage |
| Queue | Line 227 | Message queue management |
| DKIM Signing | Line 228 | Outbound signing |
| SPF Validation | Line 229 | Inbound validation |
| DMARC | Line 230 | Policy enforcement |
| TLS | Line 231 | Transport encryption |
| Anti-spam | Line 232 | Basic filtering |

### 2.2 Orvix Enterprise Layer (Responsibility)

| Component | MVP Reference | Responsibility |
|-----------|--------------|----------------|
| License Manager | Line 52 | License validation, tier limits, feature flags |
| Admin Console | Line 52 | Provider operations, reseller UI |
| Webmail UI | Line 52 | End-user email client |
| REST API | Line 53 | Public and internal API |
| Auto Updater | Line 52 | Update channels, rollback |
| Security | Line 57 | Guardian AI, Firewall UI |
| Storage Ops | Line 58 | Backup, S3, Retention |
| DNS Manager | Line 58 | Auto-config, DKIM/DMARC |
| Monitoring | Line 59 | Telemetry, Alerts |
| Migration Wizard | Line 63 | IMAP sync, source adapters |
| Auto-Heal | Line 63 | Health checks, fixers |
| Smart Compose AI | Line 63 | DeepSeek/Ollama integration |
| Deploy API | Line 64 | Provisioning, Data Center ops |

---

## 3. Technology Stack Analysis

### 3.1 Backend Stack

| Component | Technology | Status | Risk |
|-----------|-----------|--------|------|
| Language | Go 1.23+ | Not started | Low — mature ecosystem |
| Web Framework | Fiber v3 | Not started | Low — well-documented |
| ORM | GORM | Not started | Medium — learn patterns |
| Database (small) | SQLite (modernc) | Not started | Low — well-known |
| Database (scale) | PostgreSQL 16 | Not started | Low — industry standard |
| Cache/Queue | Redis 7 | Not started | Low — mature |
| Search | Bleve | Not started | Medium — embedded search |
| Job Queue | Asynq | Not started | Medium — Redis dependency |
| Config | Viper | Not started | Low — widely used |
| Logging | Zap | Not started | Low — performance proven |
| Metrics | Prometheus | Not started | Low — standard |

### 3.2 Frontend Stack

| Component | Technology | Status | Risk |
|-----------|-----------|--------|------|
| Framework | React 19 | Not started | Low — mature |
| Build | Vite 6 | Not started | Low — fast builds |
| UI Components | Radix UI | Not started | Medium — learn patterns |
| Styling | Tailwind CSS v4 | Not started | Low — well-known |
| State | Zustand | Not started | Low — simple API |
| Data Fetching | TanStack Query v5 | Not started | Low — well-documented |
| Editor | TipTap | Not started | Medium — complex integration |
| Calendar | FullCalendar | Not started | Medium — license check |
| Charts | Recharts | Not started | Low — straightforward |

### 3.3 Integration Architecture

The architecture uses a **control plane / data plane separation**:

- **Data Plane:** Stalwart Core Engine handles all mail protocol operations, storage, and transport
- **Control Plane:** OrvixEM handles configuration, management, API, UI, licensing, monitoring

**Integration points (MVP lines 246–253):**

1. **Configuration:** OrvixEM generates, validates, and applies Stalwart config
2. **Provisioning:** OrvixEM creates domains, mailboxes, aliases, policies through Stalwart APIs
3. **Observability:** OrvixEM collects logs, metrics, queue state from Stalwart
4. **Safety:** OrvixEM validates config before reload, auto-rollback on failure
5. **Licensing:** OrvixEM enables/disables capabilities without corrupting Stalwart data
6. **Updates:** OrvixEM coordinates version compatibility with Stalwart
7. **Migration:** OrvixEM imports data without bypassing integrity rules
8. **Supportability:** OrvixEM produces diagnostics bundles

---

## 4. Architecture Quality Assessment

### 4.1 Strengths

| Strength | Description |
|----------|-------------|
| **Separation of concerns** | Clear boundary between mail engine and enterprise layer |
| **Proven mail core** | Avoids years of SMTP/IMAP protocol risk |
| **Single binary deployment** | Go produces a single binary with embedded frontend |
| **License-driven features** | Same package, different capabilities per tier |
| **Additive-only migrations** | Prevents destructive upgrades |
| **Safe update pipeline** | Signed manifests, health checks, automatic rollback |

### 4.2 Risks

| Risk | Severity | Mitigation |
|------|----------|------------|
| Stalwart API maturity | Medium | Build abstraction layer, support multiple API versions |
| Stalwart compatibility | Medium | Maintain compatibility matrix, version-pin |
| Redis dependency for core ops | Medium | Graceful degradation when Redis unavailable |
| Bleve search performance at scale | Medium | Plan PostgreSQL fallback for search at scale |
| Frontend embedding increases binary size | Low | Tree-shake, lazy-load routes |
| DeepSeek API availability for AI | Medium | Support Ollama fallback for offline operation |
| Single binary upgrade risk | Low | Blue-green deployment pattern through update system |

### 4.3 Architecture Scores

| Dimension | Score (0-100) | Notes |
|-----------|--------------|-------|
| Separation of Concerns | 95 | Clear Stalwart/Orvix boundary |
| Modularity | 90 | Well-structured internal packages |
| Scalability | 85 | SQLite→PostgreSQL path, clustering |
| Maintainability | 88 | Go single binary, embedded frontend |
| Testability | 75 | No test infrastructure defined yet |
| Security | 90 | Multi-layer security architecture |
| Deployability | 92 | One-line install, auto-update |
| Observability | 85 | Prometheus, structured logging, audit logs |

---

## 5. Critical Architecture Decisions

| Decision | MVP Reference | Rationale |
|----------|--------------|-----------|
| Stalwart as mail core | Lines 99, 256–259 | Avoid rebuilding SMTP/IMAP/POP3/JMAP |
| Go + single binary | Lines 201, 1365 | Performance, easy deployment |
| SQLite default, PostgreSQL for scale | Lines 101, 1565–1566 | Zero-dependency entry point |
| License key unlocks tiers | Lines 102, 109–196 | Same package, different features |
| Additive-only migrations | Lines 104, 1244–1277 | Prevent destructive customer upgrades |
| Frontend embedded via embed.FS | Lines 281, 1367–1369 | Single binary deployment |
| Memory-only access tokens | Lines 571, 1570 | XSS protection |
| Argon2id password hashing | Lines 287, 1571 | Best current standard |
| JWT RS256 license keys | Lines 592–593, 1568 | Offline validation, tamper-proof |

---

## 6. Boundary Validation

### 6.1 Stalwart Responsibilities (Must NOT build in OrvixEM)

- SMTP protocol handling
- IMAP protocol handling
- POP3 protocol handling
- JMAP protocol handling
- Mail storage engine
- Message queue processing
- DKIM signing engine
- SPF validation engine
- DMARC enforcement engine
- TLS termination

### 6.2 OrvixEM Responsibilities (MUST build)

- License validation and feature flag control
- Configuration generation and management for Stalwart
- Multi-tenant domain/mailbox provisioning APIs
- Admin console (full provider operations)
- Webmail UI (end-user email client)
- Reseller panel and white-label management
- Migration tooling (IMAP sync, source adapters)
- Auto-update system with rollback
- Guardian AI security agent
- Mail Firewall (policy engine around Stalwart events)
- Auto-Heal system
- DNS automation
- Billing/license portal
- Monitoring and alerting
- Compliance center
- Smart Compose AI
- Collaboration layer (shared inbox)
- Backup and restore

---

**Conclusion:** The architecture is well-designed with clear boundaries. The primary execution risk is Stalwart integration depth and API maturity, which is mitigated by abstracting all Stalwart interactions behind the `internal/stalwart/` package layer.
