# DEPENDENCY ANALYSIS REPORT

## Orvix Enterprise Mail (OrvixEM)

**Date:** 2026-06-05
**Reference:** MVP Section: Tech Stack (Lines 199–314)

---

## 1. External Dependencies

### 1.1 Go Backend Dependencies

| Dependency | Version | Type | License Risk | Criticality | Alternative |
|------------|---------|------|-------------|-------------|-------------|
| Fiber v3 | v3.x | Web framework | MIT | Core | Chi, Gin, Echo |
| GORM | v2.x | ORM | MIT | Core | sqlx, ent |
| SQLite (modernc) | latest | Embedded DB | Apache-2.0 | Core (small) | — |
| PostgreSQL driver | pgx v5 | DB driver | MIT | Core (scale) | — |
| Redis (go-redis) | v9 | Cache/queue | BSD-2 | Core | — |
| Bleve | latest | Full-text search | Apache-2.0 | Search | Zinc, Meilisearch |
| Asynq | latest | Job queue | MIT | Queue | Machinery, Bull |
| Viper | v2 | Config | MIT | Config | — |
| Zap | latest | Logging | MIT | Logging | zerolog, slog |
| Prometheus client | latest | Metrics | Apache-2.0 | Metrics | — |
| JWT (golang-jwt) | v5 | JWT | MIT | Licensing | — |
| Argon2id (stdlib) | — | Password hash | Go license | Auth | — |
| Bluemonday | latest | HTML sanitizer | BSD-3 | Security | — |
| go-acme/lego | latest | TLS automation | MIT | TLS | certmagic |

### 1.2 Frontend Dependencies

| Dependency | Version | License Risk | Criticality |
|------------|---------|-------------|-------------|
| React 19 | 19.x | MIT | Core |
| Vite 6 | 6.x | MIT | Build |
| Radix UI | latest | MIT | UI |
| Tailwind CSS v4 | 4.x | MIT | Styling |
| Zustand | 5.x | MIT | State |
| TanStack Query v5 | 5.x | MIT | Data |
| TipTap | latest | MIT | Editor |
| FullCalendar | latest | MIT (commercial) | Calendar |
| Lucide | latest | ISC | Icons |
| Motion | latest | MIT | Animations |
| Recharts | latest | MIT | Charts |
| TanStack Virtual | latest | MIT | Virtualization |
| i18next | latest | MIT | i18n |

### 1.3 Infrastructure Dependencies

| Dependency | Purpose | Self-Hosted Option |
|------------|---------|-------------------|
| Redis 7 | Session, queue, rate limiting | Yes |
| PostgreSQL 16 | Production database | Yes |
| Stalwart Core Engine | Mail protocol engine | Yes (managed) |
| DeepSeek API | AI analysis (Guardian + Compose) | No (Ollama local fallback) |
| AbuseIPDB | IP reputation | No (API key) |
| VirusTotal | Threat intelligence | No (API key) |
| Cloudflare API | DNS automation | Optional |
| Route53 API | DNS automation | Optional |
| Let's Encrypt (ACME) | TLS certificates | Yes |
| updates.orvix.email | Update distribution | Required (Orvix-operated) |
| license.orvix.email | License authority | Required (Orvix-operated) |

---

## 2. Dependency Criticality Assessment

### 2.1 Core (System cannot function without)

| Dependency | Why | Fallback |
|------------|-----|----------|
| Go runtime | Application runtime | None |
| Stalwart Core Engine | All mail protocols | None (architecture mandate) |
| SQLite or PostgreSQL | All persistent state | None |
| License system | Feature gate | 7-day offline grace |
| TLS certificates | Secure connections | Self-signed (not recommended) |

### 2.2 High (Major feature degradation without)

| Dependency | Why | Fallback |
|------------|-----|----------|
| Redis | Session, queue, rate limiting | DB-based session, degraded queue |
| DeepSeek API | Guardian AI, Smart Compose | Ollama local model |
| DNS provider API | Automated DNS setup | Manual DNS wizard |

### 2.3 Medium (Some features affected)

| Dependency | Why | Fallback |
|------------|-----|----------|
| AbuseIPDB | IP reputation | Static blacklist |
| VirusTotal | Threat intelligence | Pattern-only analysis |
| Prometheus | Metrics collection | Basic logging |
| FullCalendar | Calendar UI | Simpler calendar component |
| TanStack Virtual | Large list performance | Pagination |

### 2.4 Low (Minor impact)

| Dependency | Why | Fallback |
|------------|-----|----------|
| Animations (Motion) | UI polish | No animations |
| Charts (Recharts) | Admin dashboards | Tables |
| i18next | Multi-language | English only |

---

## 3. Dependency Graph

```
OrvixEM Application
│
├── Go 1.23+ Runtime
│   ├── Fiber v3 (HTTP)
│   ├── GORM (Database ORM)
│   │   ├── SQLite (modernc)
│   │   └── PostgreSQL (pgx v5)
│   ├── go-redis → Redis 7
│   ├── Asynq → Redis 7
│   ├── Bleve (Search Index)
│   ├── Viper (Config)
│   ├── Zap (Logging)
│   ├── Prometheus Client
│   ├── golang-jwt v5
│   ├── Bluemonday
│   └── go-acme/lego → Let's Encrypt
│
├── Stalwart Core Engine
│   ├── SMTP / IMAP / POP3 / JMAP
│   ├── Mail Storage
│   └── Mail Queue
│
├── AI Provider (configurable)
│   ├── DeepSeek API (default)
│   └── Ollama (Enterprise, offline)
│
├── External APIs
│   ├── AbuseIPDB
│   ├── VirusTotal
│   ├── Cloudflare API v4
│   └── AWS Route53 SDK
│
├── Orvix Infrastructure
│   ├── updates.orvix.email
│   └── license.orvix.email
│
└── Frontend (embedded via embed.FS)
    ├── React 19 + Vite 6
    ├── Radix UI + Tailwind v4
    ├── Zustand + TanStack Query
    ├── TipTap + FullCalendar
    └── i18next
```

---

## 4. Dependency Issues

| ID | Issue | Dependency | Impact | Resolution |
|----|-------|-----------|--------|------------|
| D01 | **Stalwart redistribution rights unclear** | Stalwart Core | Legal | Verify commercial redistribution license |
| D02 | **FullCalendar commercial license** for certain uses | FullCalendar | Legal | Verify if commercial use requires paid license |
| D03 | **DeepSeek API availability** — single provider risk | DeepSeek | Operational | Ollama fallback; support multiple AI providers |
| D04 | **Redis is SPOF** | Redis | Availability | Sentinel/cluster; graceful degradation |
| D05 | **Bleve performance at scale uncertain** | Bleve | Technical | Benchmark early; PostgreSQL fallback |
| D06 | **Modernc SQLite vs CGo SQLite performance** | modernc | Technical | Benchmark before deciding default |
| D07 | **Fiber v3 maturity** (version 3 vs 2) | Fiber | Technical | Evaluate stability at start of Phase 1 |

---

## 5. Dependency Version Policy

Per MVP update channel rules (lines 1228–1242):

| Channel | Dependency Update Policy |
|---------|------------------------|
| Stable | Pin all deps; only update for security patches; CI must pass |
| Beta | May update deps for feature testing |
| Early Access | Can test new dependency versions |
| Nightly | Always latest compatible versions |

---

## 6. Recommended Dependency Management

1. **Go modules** — Standard `go.mod` with version pinning
2. **npm** — `package.json` with lockfile; `npm audit` in CI
3. **Docker** — Pin base images by digest
4. **SBOM generation** — Include in every release (MVP line 1410)
5. **Automated dependency scanning** — Dependabot or Renovate
6. **License compliance scanning** — FOSSA or similar

---

**Conclusion:** The dependency profile is reasonable for a project of this scope. The three key risks are: (1) Stalwart redistribution rights, (2) Redis single point of failure, and (3) DeepSeek API single-provider dependency. All have mitigation paths defined.
