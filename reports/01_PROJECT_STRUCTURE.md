# PROJECT STRUCTURE REPORT

## Orvix Enterprise Mail (OrvixEM)

**Date:** 2026-06-05
**Status:** Greenfield — No existing codebase
**Source of Truth:** Orvix_Enterprise_Mail_COMPLETE_MVP.md

---

## 1. Current State

The project directory `D:\Orvix Enterprise Mail` contains a single file:

| File | Purpose |
|------|---------|
| `Orvix_Enterprise_Mail_COMPLETE_MVP.md` | Authoritative MVP specification (1630 lines) |

**Result:** Zero code exists. This is a greenfield start.

---

## 2. Required Structure (from MVP Section: Project Structure)

Per the MVP (lines 992–1093), the target project structure is:

```
orvix/
├── cmd/
│   └── orvix/
│       └── main.go
├── internal/
│   ├── license/                 # License validation, signed entitlements, tier limits, feature flags
│   │   ├── api.go
│   │   ├── validator.go
│   │   └── tier.go
│   ├── stalwart/                # Stalwart Core integration layer
│   │   ├── config.go            # Generate and validate Stalwart config
│   │   ├── service.go           # Start/stop/restart/reload orchestration
│   │   ├── api.go               # API/client adapters where supported
│   │   ├── events.go            # Normalize Stalwart events/logs into OrvixEM events
│   │   ├── provisioning.go      # Domains, mailboxes, aliases, quotas, policies
│   │   ├── compatibility.go     # Supported Stalwart versions and migration guards
│   │   └── diagnostics.go       # Safe support bundles without secrets
│   ├── mailops/                 # OrvixEM mail operations layer around Stalwart
│   │   ├── domains.go           # Domain operations
│   │   ├── mailboxes.go         # Mailbox operations
│   │   ├── aliases.go           # Alias and routing operations
│   │   ├── queue.go             # Queue visibility and controls
│   │   ├── policies.go          # Per-domain/per-user mail policies
│   │   └── delivery.go          # Delivery analytics and outbound guardrails
│   ├── security/                # DKIM, SPF, DMARC, ARC policy UX + platform security helpers
│   ├── firewall/                # Mail Firewall engine
│   │   ├── engine.go            # Main pipeline
│   │   ├── layers.go            # All filter layers
│   │   ├── rules.go             # Rules engine
│   │   ├── reputation.go        # IP/domain reputation
│   │   └── geo.go               # Geo-blocking
│   ├── autoheal/                # Auto-Heal system
│   │   ├── monitor.go           # Health checks
│   │   ├── fixers.go            # Auto-fix actions
│   │   └── history.go           # Heal history log
│   ├── guardian/                # Guardian AI Agent
│   │   ├── agent.go             # Main AI agent
│   │   ├── analyzer.go          # Threat analysis
│   │   ├── patterns.go          # Pattern learning
│   │   ├── reporter.go          # Intelligence reports
│   │   └── api.go               # Guardian REST API
│   ├── compose/                 # Smart Compose AI
│   │   ├── compose.go           # AI compose engine
│   │   └── stream.go            # SSE streaming
│   ├── provision/               # Instant Deploy API
│   │   ├── provisioner.go       # Domain provisioning
│   │   └── api.go               # Provision REST API
│   ├── migration/               # Smart Migration Tool
│   │   ├── wizard.go            # Migration wizard
│   │   ├── imap_sync.go         # IMAP sync engine
│   │   ├── sources/             # Source adapters
│   │   │   ├── axigen.go
│   │   │   ├── zimbra.go
│   │   │   ├── exchange.go
│   │   │   └── generic_imap.go
│   │   └── progress.go          # Real-time progress
│   ├── intelligence/            # Email Intelligence Dashboard
│   │   ├── analyzer.go
│   │   └── insights.go
│   ├── collaboration/           # Shared Inbox (Enterprise)
│   │   ├── shared_inbox.go
│   │   └── assignment.go
│   ├── compliance/              # Compliance Center
│   │   ├── gdpr.go
│   │   ├── legal_hold.go
│   │   └── ediscovery.go
│   ├── encryption/              # Zero-Knowledge Encryption
│   │   └── zke.go
│   ├── antispam/                # OrvixEM policy layer, quarantine, dashboards, integrations
│   ├── storage/                 # OrvixEM metadata, backup, retention and multi-cloud operations
│   │   ├── mailbox.go
│   │   ├── search.go
│   │   ├── quota.go
│   │   └── cloud/               # S3, GCS, Azure adapters
│   ├── dns/                     # DNS automation
│   ├── api/                     # REST API
│   ├── auth/                    # Auth system
│   ├── models/                  # Database models
│   ├── config/                  # Configuration
│   ├── updater/                 # Auto-update system, channels, rollback, manifests
│   ├── flags/                   # Feature flags and license-gated capabilities
│   ├── migrations/              # Additive-only DB migrations and migration safety checks
│   ├── changelog/               # Changelog parser/renderer for admin update UI
│   └── metrics/                 # Prometheus metrics
├── web/
│   ├── webmail/                 # Webmail React app
│   ├── admin/                   # Admin console React app
│   └── portal/                  # Customer/reseller portal React app
├── migrations/
├── templates/
├── docs/
├── packaging/
│   ├── systemd/
│   ├── docker/
│   └── checksums/
├── scripts/
│   ├── install.sh
│   ├── build.sh
│   ├── release.sh
│   └── verify-update.sh
├── go.mod
├── go.sum
└── Makefile
```

---

## 3. Missing Items

| Area | Status | Action Required |
|------|--------|----------------|
| `cmd/orvix/main.go` | MISSING | Create entry point |
| `internal/license/` | MISSING | Build license engine |
| `internal/stalwart/` | MISSING | Build Stalwart integration |
| `internal/mailops/` | MISSING | Build mail operations |
| `internal/security/` | MISSING | Build security layer |
| `internal/firewall/` | MISSING | Build firewall engine |
| `internal/autoheal/` | MISSING | Build auto-heal system |
| `internal/guardian/` | MISSING | Build Guardian AI |
| `internal/compose/` | MISSING | Build Smart Compose |
| `internal/provision/` | MISSING | Build Instant Deploy |
| `internal/migration/` | MISSING | Build migration tool |
| `internal/intelligence/` | MISSING | Build intelligence dashboard |
| `internal/collaboration/` | MISSING | Build shared inbox |
| `internal/compliance/` | MISSING | Build compliance center |
| `internal/encryption/` | MISSING | Build ZKE |
| `internal/antispam/` | MISSING | Build anti-spam layer |
| `internal/storage/` | MISSING | Build storage operations |
| `internal/dns/` | MISSING | Build DNS automation |
| `internal/api/` | MISSING | Build REST API |
| `internal/auth/` | MISSING | Build auth system |
| `internal/models/` | MISSING | Build database models |
| `internal/config/` | MISSING | Build config system |
| `internal/updater/` | MISSING | Build auto-update system |
| `internal/flags/` | MISSING | Build feature flags system |
| `internal/migrations/` | MISSING | Build migration engine |
| `internal/changelog/` | MISSING | Build changelog system |
| `internal/metrics/` | MISSING | Build metrics system |
| `web/webmail/` | MISSING | Build webmail React app |
| `web/admin/` | MISSING | Build admin React app |
| `web/portal/` | MISSING | Build portal React app |
| `migrations/` | MISSING | Create SQL migration files |
| `templates/` | MISSING | Create templates |
| `docs/` | MISSING | Create documentation |
| `packaging/` | MISSING | Create packaging scripts |
| `scripts/` | MISSING | Create build/install scripts |
| `go.mod` | MISSING | Initialize Go module |

---

## 4. Structure Compliance

**Source:** MVP lines 992–1093

- The MVP defines the exact file tree for the entire application.
- All 25+ internal packages, 3 frontend apps, deployment scripts, and build tooling are specified.
- **Current compliance: 0%** — no directories or files exist.
- **Target structure requires approximately 80+ files** for Go backend and React frontend applications.

---

## 5. Recommended Creation Order

Per MVP Phase 1 (lines 1428–1438):

1. `cmd/orvix/main.go` — Entry point
2. `go.mod` — Module initialization
3. `internal/config/` — Configuration system
4. `internal/models/` — GORM models
5. `internal/migrations/` — Migration engine
6. `internal/metrics/` — Prometheus metrics
7. `internal/license/` — License engine
8. `internal/flags/` — Feature flags
9. `internal/changelog/` — Changelog system

Then Phase 2 adds:
10. `internal/stalwart/` — Stalwart integration
11. `internal/mailops/` — Mail operations

Then Phase 3 adds:
12. `internal/firewall/` — Firewall engine
13. `internal/autoheal/` — Auto-heal
14. `internal/guardian/` — Guardian AI

Then Phase 4 adds:
15. `internal/auth/` — Auth system
16. `internal/api/` — REST API
17. `internal/provision/` — Instant Deploy API
18. `internal/security/` — Security layer

Then Phase 5 adds:
19. `web/` — All three frontend apps

Then Phase 6 adds:
20. Advanced features (calendar, migration, compliance, etc.)

Then Phase 7 adds:
21. `packaging/` — Packaging and documentation
22. `scripts/` — Build and install scripts
23. `docs/` — Documentation

---

## 6. Key Findings

1. **Module name:** `github.com/orvixemail/orvix` (per MVP line 1538)
2. **Binary name:** `orvix` (per MVP line 81)
3. **Config file:** `orvix.yaml` (per MVP line 81)
4. **Tech stack:** Go 1.23+, Fiber v3, GORM, React 19, Vite 6, Radix UI, Tailwind v4, SQLite/PostgreSQL, Redis 7, Stalwart Core Engine
5. **Frontend embedding:** All web assets embedded via Go `embed.FS`
6. **No mail protocol code:** SMTP/IMAP/POP3/JMAP handled by Stalwart, never built in OrvixEM

---

**Next Step:** Begin Phase 1 implementation — project structure setup, config system, database layer, license engine, feature flags, and versioning metadata.
