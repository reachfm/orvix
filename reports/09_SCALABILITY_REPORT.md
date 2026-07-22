# SCALABILITY REPORT

## Orvix Enterprise Mail (OrvixEM)

**Date:** 2026-06-05
**Reference:** MVP Sections: Architecture, Tech Stack, License Tiers

---

## 1. Scalability Targets

Per license tiers (MVP lines 108–196):

| Tier | Max Mailboxes | Max Domains | Clustering |
|------|--------------|-------------|------------|
| SMB ($500/yr) | 500 | 10 | ❌ |
| ISP ($1,200/yr) | 50,000 | Unlimited | Up to 3 nodes |
| Enterprise ($2,500/yr) | Unlimited | Unlimited | Unlimited nodes |

The architecture must scale from a single SQLite instance on a VPS to multi-node PostgreSQL clusters serving 50,000+ mailboxes.

---

## 2. Scalability Architecture

### 2.1 Single-Node (SMB Tier)

```
┌─────────────────────────────────┐
│         Single Server           │
├─────────────────────────────────┤
│ OrvixEM (Go binary)             │
│ Stalwart Core Engine            │
│ SQLite (via modernc)            │
│ Redis (embedded or local)       │
│ Bleve (embedded search)         │
└─────────────────────────────────┘
```

**Limits:** ~500 mailboxes, ~10 domains, ~50 emails/minute
**Storage:** Local disk or single S3 bucket
**Deployment:** One-line install script

### 2.2 Multi-Node (ISP Tier)

```
┌──────────┐  ┌──────────┐  ┌──────────┐
│ OrvixEM 1│  │ OrvixEM 2│  │ OrvixEM 3│
│ (App)    │  │ (App)    │  │ (App)    │
└────┬─────┘  └────┬─────┘  └────┬─────┘
     │              │              │
     └──────────────┼──────────────┘
                    │
          ┌─────────┴─────────┐
          │   PostgreSQL 16    │
          │   (Primary + HA)   │
          └───────────────────┘
          ┌───────────────────┐
          │   Redis Sentinel   │
          │   (Cluster Mode)   │
          └───────────────────┘
          ┌───────────────────┐
          │   S3 / Object      │
          │   Storage (shared) │
          └───────────────────┘
```

**Limits:** ~50,000 mailboxes, unlimited domains
**Storage:** S3-compatible object storage
**Load Balancing:** HTTP load balancer in front of OrvixEM nodes
**Stalwart:** Each node runs Stalwart; config synchronized

### 2.3 Enterprise (Unlimited)

```
┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐
│ OrvixEM 1│  │ OrvixEM 2│  │ OrvixEM N│  │ ...      │
└────┬─────┘  └────┬─────┘  └────┬─────┘  └──────────┘
     │              │              │
     └──────────────┼──────────────┘
                    │
          ┌─────────┴─────────┐
          │   PostgreSQL 16    │
          │   (Multi-AZ HA)    │
          └───────────────────┘
          ┌───────────────────┐
          │   Redis Cluster    │
          │   (Multi-AZ)       │
          └───────────────────┘
          ┌───────────────────┐
          │   Object Storage   │
          │   (Multi-region)   │
          └───────────────────┘
```

**Limits:** Unlimited mailboxes, horizontal scale
**Storage:** Multi-region S3/GCS/Azure
**Stalwart:** Distributed Stalwart nodes with shared storage

---

## 3. Performance Considerations

### 3.1 Database

| Factor | SQLite | PostgreSQL |
|--------|--------|------------|
| Max concurrent connections | 1 writer, unlimited readers | Hundreds |
| Max database size | ~100GB (practical) | Terabytes |
| Replication | None | Streaming + logical |
| Performance at scale | Degrades >10k records per query | Stable at millions |
| Full-text search | Bleve (external) | Built-in tsvector |

**Strategy:** SQLite for SMB; PostgreSQL for ISP/Enterprise. Both supported by GORM abstraction.

### 3.2 Caching

| Cache Layer | Technology | Purpose | TTL |
|-------------|-----------|---------|-----|
| Session cache | Redis | User sessions | 24 hours |
| Rate limit counters | Redis | Token bucket state | 15 minutes |
| Job queue | Redis (Asynq) | Background tasks | Until processed |
| API response cache | Redis | Frequent query results | 5 minutes |
| DNS lookups | In-memory | MX/SPF cache | 1 hour |
| License validation | In-memory | Cached until expiry | 24 hours |

**Redis failure mode:** Graceful degradation — sessions fall back to DB queries, rate limiting uses local counters, job queue pauses.

### 3.3 Search

| Scenario | Solution | Performance |
|----------|----------|-------------|
| SMB (500 mailboxes) | Bleve embedded | Sub-second |
| ISP (50k mailboxes) | PostgreSQL FTS or Dedicated Bleve index | 1-3 seconds |
| Enterprise (500k+) | Dedicated search (Elasticsearch post-MVP) | Sub-second |

### 3.4 File Storage

| Storage Type | Use Case | Scaling |
|-------------|----------|---------|
| Local disk | SMB, single-node | RAID, direct attached |
| S3-compatible | ISP, multi-node | Virtually unlimited |
| GCS / Azure | Enterprise, multi-cloud | Global scale |

---

## 4. Bottleneck Analysis

| Bottleneck | Impact | Mitigation |
|-----------|--------|------------|
| **Stalwart single-node limits** | Mail throughput capped per node | Stalwart clustering in ISP/Enterprise tiers |
| **Redis single-node** | Job queue and session SPOF | Redis Sentinel / Cluster |
| **SQLite write lock** | 1 concurrent write limits throughput | PostgreSQL for ISP+ |
| **Bleve memory usage** | Indexes stored in memory on writes | Configurable indexing schedule; PostgreSQL fallback |
| **Front-end API rate limits** | Customer-facing API throttling | Per-tenant rate limits; burst allowance |
| **Migration bandwidth** | Large mailbox migration | Throttle migration speed; schedule off-peak |

---

## 5. Load Testing Targets (MVP Phase 7)

| Test | Target | Method |
|------|--------|--------|
| Concurrent SMTP connections | 10,000 | Stalwart load test |
| Concurrent IMAP connections | 5,000 | Stalwart load test |
| API requests per second | 1,000 | k6 / Locust |
| Webmail concurrent users | 500 | Browser-based load test |
| Migration speed | 1 GB/hour per mailbox | Parallel mailbox migration |
| Mail delivery rate | 100,000/hour | Stalwart throughput test |
| Database query latency | <50ms p99 | PostgreSQL query profiling |

---

## 6. Scalability Score

| Dimension | Score (0-100) | Notes |
|-----------|--------------|-------|
| Database scaling | 85 | SQLite→PostgreSQL path is clean; GORM abstracts both |
| Application scaling | 75 | Stateless Go app; horizontal scale via load balancer |
| Storage scaling | 80 | S3 abstraction enables infinite scale |
| Cache scaling | 70 | Redis dependency is a concern; Sentinel mitigates |
| Search scaling | 65 | Bleve at scale is unproven; PostgreSQL FTS fallback |
| Migration scaling | 60 | IMAP sync is inherently single-threaded per mailbox |
| Frontend scaling | 85 | React + CDN-cached static assets |
| Network scaling | 80 | HTTP load balancer + protocol-level optimization |

**Overall Scalability Score: 75/100**

---

## 7. Recommendations for Scalability

1. **Benchmark Bleve early** — Test with 50k+ mailbox dataset; have PostgreSQL FTS as backup
2. **Implement Redis graceful degradation** — MVP defines Redis as dependency; document failure mode clearly
3. **Test Stalwart clustering** — Validate ISP/Enterprise clustering works before committing to architecture
4. **Plan PostgreSQL migration path** — Document exactly when customers should switch from SQLite
5. **Design for CDN** — Frontend assets served via CDN for global performance
6. **Database connection pooling** — Use PgBouncer for PostgreSQL at scale
7. **Rate limiting by tenant** — Prevent noisy tenants from affecting neighbors

---

**Conclusion:** The architecture supports the required scalability tiers. The primary concerns are Bleve performance at scale (mitigated by PostgreSQL fallback) and Redis single-point-of-failure (mitigated by Sentinel/Cluster). The SQLite→PostgreSQL migration path is well-defined. No fundamental architectural blockers to achieving ISP/Enterprise scale.
