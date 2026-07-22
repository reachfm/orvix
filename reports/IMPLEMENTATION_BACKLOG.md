# IMPLEMENTATION BACKLOG

## Orvix Enterprise Mail (OrvixEM)

**Source of Truth:** Orvix_Enterprise_Mail_COMPLETE_MVP.md
**Rule:** Every feature maps to MVP. No invented features. No missing MVP features.

---

## Phase 1 — Foundation (MVP Lines 1428–1438)

### Task 1.1: Project Structure Setup
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 990–1093 (Project Structure), Line 1537 |
| **Description** | Set up OrvixEM project structure exactly as defined in the Project Structure section |
| **Files to Create** | All directories: `cmd/orvix/`, `internal/` (all packages), `web/webmail/`, `web/admin/`, `web/portal/`, `migrations/`, `templates/`, `docs/`, `packaging/`, `scripts/` |
| **Dependencies** | None |
| **Effort** | 4 hours |
| **Acceptance Criteria** | Directory tree matches MVP Project Structure section exactly |

### Task 1.2: Go Module Initialization
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1538 |
| **Description** | Initialize `go.mod` with module name `github.com/orvixemail/orvix`. Binary name: `orvix`. Config file: `orvix.yaml`. |
| **Files to Create** | `go.mod`, `cmd/orvix/main.go` |
| **Dependencies** | 1.1 |
| **Effort** | 2 hours |
| **Acceptance Criteria** | `go build ./cmd/orvix` produces `orvix` binary |

### Task 1.3: Config System
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 81 (orvix.yaml), Lines 199–214 (Tech Stack), Line 1068 |
| **Description** | Set up Fiber v3, GORM, Viper, Zap dependencies. Create config system reading from `orvix.yaml`. |
| **Files to Create** | `internal/config/` package |
| **Dependencies** | 1.2 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | Config loads from `orvix.yaml` with defaults; all tech stack dependencies initialized |

### Task 1.4: Database Layer
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 101–102 (SQLite default, PostgreSQL for scale), Lines 207–208, Line 1067 |
| **Description** | Create database connection supporting SQLite (via modernc) as default and PostgreSQL when configured. Create GORM models. |
| **Files to Create** | `internal/models/` (all models), database connection code |
| **Dependencies** | 1.3 |
| **Effort** | 12 hours |
| **Acceptance Criteria** | DB connects with SQLite and PostgreSQL; GORM auto-migrates models |

### Task 1.5: Additive-Only Migration System
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 1244–1277 (Migration Rules — Additive Only), Line 1071 |
| **Description** | Create additive-only migration system with safety checks. ALLOWED: ADD TABLE, ADD COLUMN, ADD INDEX, CREATE VIEW, CREATE TRIGGER. FORBIDDEN: DROP TABLE, DROP COLUMN, RENAME COLUMN, RENAME TABLE, DESTRUCTIVE TYPE CHANGE. |
| **Files to Create** | `internal/migrations/` |
| **Dependencies** | 1.4 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | Additive migrations apply; destructive migrations rejected with error |

### Task 1.6: Logging System
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 212 (Zap — fast structured logging) |
| **Description** | Set up Zap for structured, leveled logging |
| **Files to Create** | Logging initialization in `internal/metrics/` or root |
| **Dependencies** | 1.2 |
| **Effort** | 4 hours |
| **Acceptance Criteria** | Logs output in structured JSON format at configurable levels |

### Task 1.7: Metrics Endpoints
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 214 (Prometheus — built-in observability), Line 1073 |
| **Description** | Set up Prometheus metrics endpoints for built-in observability |
| **Files to Create** | `internal/metrics/` |
| **Dependencies** | 1.2 |
| **Effort** | 4 hours |
| **Acceptance Criteria** | `/metrics` endpoint returns Prometheus-formatted metrics |

### Task 1.8: License Engine
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 108–196 (License Tiers), Lines 591–597 (License Security), Line 998 |
| **Description** | Build license engine: JWT RS256 validation, embedded public key verification, tier extraction, feature entitlements. License key = signed JWT (RS256) from license server. Offline validation via embedded public key. Hardware fingerprint binding (optional). Tamper detection. 7-day grace period. |
| **Files to Create** | `internal/license/` |
| **Dependencies** | 1.2 |
| **Effort** | 16 hours |
| **Acceptance Criteria** | License validates with embedded public key; tier/extracted; grace period works; tampered license rejected |

### Task 1.9: Feature Flags Engine
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 1308–1344 (Feature Flags System), Line 1070 |
| **Description** | Build feature flags engine controlled by license entitlements, update channel, and admin configuration. Feature flag sources: license tier, signed entitlement payload, admin configuration, update channel, emergency remote disable list. |
| **Files to Create** | `internal/flags/` |
| **Dependencies** | 1.8 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | Feature flags respond to license tier; admin can override; channel controls work |

### Task 1.10: Versioning Metadata
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 1206–1222 (Versioning Strategy — MAJOR.MINOR.PATCH) |
| **Description** | Create version metadata package with semantic versioning, build commit, update channel. |
| **Files to Create** | Version metadata package or embedded in `cmd/orvix/main.go` |
| **Dependencies** | 1.2 |
| **Effort** | 4 hours |
| **Acceptance Criteria** | `orvix version` returns MAJOR.MINOR.PATCH + commit + channel |

### Task 1.11: Code Watermarking
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1438 (Code watermarking — embedded copyright strings + canary tokens) |
| **Description** | Embed copyright strings and canary tokens for piracy detection |
| **Files to Create** | Watermarking in binary build |
| **Dependencies** | 1.2 |
| **Effort** | 4 hours |
| **Acceptance Criteria** | Canary tokens present in binary; copyright strings embedded |

---

## Phase 2 — Stalwart Core Integration (MVP Lines 1440–1453)

### Task 2.1: Stalwart Install/Detection Flow
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1440, Line 311 |
| **Description** | Detect Stalwart installation, verify version, install if missing |
| **Files to Create** | `internal/stalwart/service.go` |
| **Dependencies** | Phase 1 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | Stalwart detected or installed automatically |

### Task 2.2: Stalwart Config Generator
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1441, Line 1001 |
| **Description** | Generate Stalwart configuration from OrvixEM's `orvix.yaml` and admin settings |
| **Files to Create** | `internal/stalwart/config.go` |
| **Dependencies** | 2.1 |
| **Effort** | 16 hours |
| **Acceptance Criteria** | Valid Stalwart config generated; all sections (server, tls, auth, storage, directory, queue) populated |

### Task 2.3: Stalwart Config Validator
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1442 |
| **Description** | Validate Stalwart config before applying: syntax, port conflicts, path validity, TLS certs |
| **Files to Create** | `internal/stalwart/config.go` |
| **Dependencies** | 2.2 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | Invalid config rejected; validation errors returned to admin |

### Task 2.4: Stalwart Service Lifecycle Manager
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1443, Line 1002 |
| **Description** | Start/stop/restart/reload Stalwart service with graceful operations |
| **Files to Create** | `internal/stalwart/service.go` |
| **Dependencies** | 2.1 |
| **Effort** | 12 hours |
| **Acceptance Criteria** | `orvix stalwart start|stop|restart|status|reload` all work correctly |

### Task 2.5: Domain Provisioning
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1444, Line 1005 |
| **Description** | Create domains in Stalwart via OrvixEM API/config |
| **Files to Create** | `internal/stalwart/provisioning.go`, `internal/mailops/domains.go` |
| **Dependencies** | 2.2 |
| **Effort** | 12 hours |
| **Acceptance Criteria** | Domain created in Stalwart; appears in admin UI |

### Task 2.6: Mailbox Provisioning
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1445, Line 1005 |
| **Description** | Create mailboxes/users in Stalwart with passwords, quotas |
| **Files to Create** | `internal/stalwart/provisioning.go`, `internal/mailops/mailboxes.go` |
| **Dependencies** | 2.5 |
| **Effort** | 12 hours |
| **Acceptance Criteria** | Mailbox created; can connect via IMAP; quota enforced |

### Task 2.7: Alias/Routing Provisioning
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1446, Line 1009 |
| **Description** | Create email aliases and routing rules in Stalwart |
| **Files to Create** | `internal/stalwart/provisioning.go`, `internal/mailops/aliases.go` |
| **Dependencies** | 2.5 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | Aliases route mail correctly |

### Task 2.8: Queue Visibility and Controls
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1447, Line 1011 |
| **Description** | Read Stalwart queue state; provide controls (retry, delete, inspect) |
| **Files to Create** | `internal/mailops/queue.go` |
| **Dependencies** | 2.1 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | Queue visible in admin; retry/delete operations succeed |

### Task 2.9: Stalwart Log/Event Ingestion
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1448, Line 1004 |
| **Description** | Tail Stalwart logs, normalize events into OrvixEM event system |
| **Files to Create** | `internal/stalwart/events.go` |
| **Dependencies** | 2.1 |
| **Effort** | 12 hours |
| **Acceptance Criteria** | SMTP/IMAP/auth events appear in OrvixEM audit log |

### Task 2.10: Stalwart Health Checks
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1449 |
| **Description** | TCP port checks for SMTP (25/587/465), IMAP (143/993), POP3 (110/995), JMAP |
| **Files to Create** | `internal/stalwart/service.go` |
| **Dependencies** | 2.4 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | All ports probed; health status correctly reported |

### Task 2.11: DKIM/SPF/DMARC Policy Management
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1450, Lines 228–230 |
| **Description** | UI + API for managing DKIM keys, SPF records, DMARC policies around Stalwart |
| **Files to Create** | `internal/security/`, `internal/dns/` |
| **Dependencies** | 2.5 |
| **Effort** | 12 hours |
| **Acceptance Criteria** | DKIM keys generated/rotated; SPF record generated; DMARC policy set |

### Task 2.12: TLS/Certificate Management
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1451, Line 231 |
| **Description** | Automate TLS certificates via ACME (Let's Encrypt); manage in admin UI |
| **Files to Create** | `internal/stalwart/config.go` |
| **Dependencies** | 2.2 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | Certificate auto-renewed; admin notified of expiry |

### Task 2.13: Compatibility Matrix
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1452, Line 1006 |
| **Description** | Maintain compatibility matrix for supported Stalwart versions; guard upgrades |
| **Files to Create** | `internal/stalwart/compatibility.go` |
| **Dependencies** | 2.1 |
| **Effort** | 4 hours |
| **Acceptance Criteria** | Incompatible Stalwart version detected; upgrade blocked with explanation |

---

## Phase 3 — Security Layer (MVP Lines 1455–1467)

### Task 3.1: Mail Firewall — 5-Layer Pipeline
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 620–655 (Processing Pipeline — 5 layers: Connection/Protocol/Auth/Content/Behavioral) |
| **Description** | Build multi-layer mail firewall pipeline processing inbound connections through all 5 filter layers |
| **Files to Create** | `internal/firewall/engine.go`, `internal/firewall/layers.go` |
| **Dependencies** | Phase 2 |
| **Effort** | 24 hours |
| **Acceptance Criteria** | Email passes through all 5 layers; PASS/QUARANTINE/BLOCK verdicts enforced |

### Task 3.2: Firewall Rules Engine
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 657–676 (Rules Engine — no-code rule builder) |
| **Description** | Build rules engine with IF/THEN syntax; admin can create rules via UI without code |
| **Files to Create** | `internal/firewall/rules.go` |
| **Dependencies** | 3.1 |
| **Effort** | 12 hours |
| **Acceptance Criteria** | Rules defined in admin panel → enforced in firewall pipeline |

### Task 3.3: IP Reputation Integration
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1457 (AbuseIPDB integration) |
| **Description** | Integrate AbuseIPDB for real-time IP reputation checking |
| **Files to Create** | `internal/firewall/reputation.go` |
| **Dependencies** | 3.1 |
| **Effort** | 6 hours |
| **Acceptance Criteria** | Known bad IPs blocked; reputation score used in firewall decision |

### Task 3.4: Geo-Blocking Engine
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1458, Lines 627–628 |
| **Description** | Geo-block connections by country; admin map UI to select blocked countries |
| **Files to Create** | `internal/firewall/geo.go` |
| **Dependencies** | 3.1 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | Connections from blocked countries rejected; GeoIP lookup works |

### Task 3.5: Rate Limiting
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 288 (Token bucket per IP + per account), Line 1459 |
| **Description** | Token bucket rate limiting per IP address, per account, and global |
| **Files to Create** | `internal/auth/` or `internal/firewall/` |
| **Dependencies** | Phase 1 (Redis) |
| **Effort** | 8 hours |
| **Acceptance Criteria** | Rate limit exceeded → 429 response; token bucket refills correctly |

### Task 3.6: Brute Force Protection
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1460, Lines 561–562 (5 attempts/15 min per IP) |
| **Description** | Block IP after failed login attempts; gradual lockout with increasing delays |
| **Files to Create** | `internal/auth/` |
| **Dependencies** | 3.5 |
| **Effort** | 6 hours |
| **Acceptance Criteria** | 5 failed logins → locked for 15 min; admin can unlock |

### Task 3.7: Guardian Agent
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 726–792 (Guardian AI Agent architecture) |
| **Description** | Build AI threat analysis agent: feature vector construction, DeepSeek API integration, verdict engine |
| **Files to Create** | `internal/guardian/agent.go`, `internal/guardian/analyzer.go`, `internal/guardian/patterns.go` |
| **Dependencies** | 3.1 |
| **Effort** | 16 hours |
| **Acceptance Criteria** | Email analyzed; threat_score + verdict + explanation returned |

### Task 3.8: Guardian REST API
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 749–783 (Guardian API — POST /api/v1/guardian/analyze) |
| **Description** | Build Guardian REST API for Enterprise-tier API access |
| **Files to Create** | `internal/guardian/api.go` |
| **Dependencies** | 3.7 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | POST /api/v1/guardian/analyze returns threat analysis |

### Task 3.9: Auto-Heal System
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 688–723 (Auto-Heal System) |
| **Description** | Build health check monitor (every 60s) with 12 check types and auto-fix actions. Health checks: smtp_port, imap_port, queue_depth, disk_usage, memory_usage, db_connection, redis_connection, ssl_expiry, dkim_rotation, blacklist_status, spam_rate, bounce_rate. |
| **Files to Create** | `internal/autoheal/monitor.go`, `internal/autoheal/fixers.go` |
| **Dependencies** | Phase 2 |
| **Effort** | 16 hours |
| **Acceptance Criteria** | Health checks run every 60s; auto-fix actions execute; severity levels enforced |

### Task 3.10: Heal History Log
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 718–722 (Heal History Log) |
| **Description** | Log all heal actions with before/after state, success/failure rate |
| **Files to Create** | `internal/autoheal/history.go` |
| **Dependencies** | 3.9 |
| **Effort** | 4 hours |
| **Acceptance Criteria** | Heal actions logged; success rate visible in admin panel |

### Task 3.11: Stalwart Config Drift Detection
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1465 |
| **Description** | Detect when Stalwart config differs from what OrvixEM expects |
| **Files to Create** | `internal/stalwart/diagnostics.go` |
| **Dependencies** | Phase 2 |
| **Effort** | 6 hours |
| **Acceptance Criteria** | Config drift detected within 60s; admin alerted |

### Task 3.12: Safe Diagnostic Bundle Exporter
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1466, Line 1007 |
| **Description** | Produce .tar.gz support bundle with config (redacted), logs (last 1000 lines), status, metrics — no secrets exposed |
| **Files to Create** | `internal/stalwart/diagnostics.go` |
| **Dependencies** | Phase 2 |
| **Effort** | 6 hours |
| **Acceptance Criteria** | Bundle created; passwords/keys/tokens redacted |

---

## Phase 4 — API Layer (MVP Lines 1469–1479)

### Task 4.1: Auth System
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 557–572 (Authentication Flow) |
| **Description** | Build full auth system: rate limited login, Argon2id password verify, 2FA TOTP with backup codes, JWT access tokens (15 min), refresh tokens (30 days, rotated), HttpOnly + Secure + SameSite=Strict cookies, memory-only access tokens |
| **Files to Create** | `internal/auth/` |
| **Dependencies** | 3.5, 3.6 |
| **Effort** | 16 hours |
| **Acceptance Criteria** | Login flow matches exact MVP auth flow diagram |

### Task 4.2: User Management API
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 483–491 (User Management in Admin Console) |
| **Description** | CRUD API for users: create/edit/delete, quota management, force password reset, impersonate, export CSV, bulk import CSV, role assignment, active sessions viewer |
| **Files to Create** | `internal/api/` |
| **Dependencies** | 4.1 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | All user management operations work via API; RBAC enforced |

### Task 4.3: Domain Management API
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 476–481 (Domain Management in Admin Console) |
| **Description** | CRUD API for domains: add/remove, DNS status checker (SPF/DKIM/DMARC/MX), one-click DNS wizard, DKIM key rotation, per-domain settings, domain aliases |
| **Files to Create** | `internal/api/` |
| **Dependencies** | 4.1 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | All domain management operations work via API |

### Task 4.4: Mailbox API
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 483–491 (User/Mailbox management) |
| **Description** | CRUD API for mailboxes with quota enforcement |
| **Files to Create** | `internal/api/` |
| **Dependencies** | 4.1 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | Mailbox CRUD + quota enforcement works via API |

### Task 4.5: Admin API (Full)
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 467–552 (Admin Console full spec) |
| **Description** | Build all admin API endpoints covering: dashboard data, domains, users, mail queue, logs, anti-spam, routing, backup, license, updates, resellers, system settings |
| **Files to Create** | `internal/api/` |
| **Dependencies** | 4.2, 4.3, 4.4 |
| **Effort** | 12 hours |
| **Acceptance Criteria** | All admin console features accessible via REST API |

### Task 4.6: Stalwart Operations API Wrapper
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1474 |
| **Description** | Expose Stalwart operations through OrvixEM API (status, config validate, restart, etc.) |
| **Files to Create** | `internal/api/` |
| **Dependencies** | 4.1 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | `orvix stalwart` commands available via API |

### Task 4.7: Instant Deployment API
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 832–878 (Instant Deployment API — provisions domain in < 30 seconds) |
| **Description** | Build provisioning API that creates domain, DNS, DKIM, mailboxes, quotas, firewall, SSL, and sends welcome emails in < 30 seconds |
| **Files to Create** | `internal/provision/provisioner.go`, `internal/provision/api.go` |
| **Dependencies** | 4.3, 4.4 |
| **Effort** | 12 hours |
| **Acceptance Criteria** | POST /api/v1/provision/domain returns provisioned resources in < 30 seconds |

### Task 4.8: Webhook System
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1477, Line 587 (HMAC-SHA256 signed) |
| **Description** | Build webhook system: register webhooks, dispatch events, HMAC-SHA256 signing |
| **Files to Create** | `internal/api/` |
| **Dependencies** | 4.1 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | Webhooks dispatched with HMAC signature; retry on failure |

### Task 4.9: API Key Management
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1478, Lines 586–588 (API key system, hashed never stored plain) |
| **Description** | Generate, hash, and manage API keys; key rotation; permission scoping |
| **Files to Create** | `internal/api/` |
| **Dependencies** | 4.1 |
| **Effort** | 6 hours |
| **Acceptance Criteria** | API keys generated; hashed at rest; scoped to permissions |

### Task 4.10: License-Gated Endpoint Enforcement
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1479, Lines 1308–1328 (Feature flags controlled by license) |
| **Description** | Enforce license tier on every API endpoint; return 403 with upgrade prompt |
| **Files to Create** | Middleware in `internal/flags/` |
| **Dependencies** | 1.8, 1.9, 4.1 |
| **Effort** | 6 hours |
| **Acceptance Criteria** | Unlicensed feature returns 403; upgrade prompt displayed |

---

## Phase 5 — Frontend (MVP Lines 1481–1495)

### Task 5.1: Design System + Component Library
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 317–364 (UI/UX Design System) |
| **Description** | Build design system with Radix UI + Tailwind CSS v4. Implement: dark-first theme with optional light, density control, keyboard-first navigation, WCAG 2.1 AA, color system, typography (Geist, JetBrains Mono), spacing scale |
| **Files to Create** | `web/` shared component library |
| **Dependencies** | None |
| **Effort** | 24 hours |
| **Acceptance Criteria** | All components match design system spec; keyboard navigable; accessible |

### Task 5.2: Webmail UI
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 367–463 (Webmail UI — Full Feature Spec) |
| **Description** | Build full webmail: sidebar/folders, virtualized email list (100k+), reading pane with sandboxed iframe, compose with TipTap editor, search, calendar, contacts, tasks, settings |
| **Files to Create** | `web/webmail/` |
| **Dependencies** | 5.1 |
| **Effort** | 60 hours |
| **Acceptance Criteria** | All webmail features work: compose/send/receive, search 100k emails, calendar/contacts/tasks, PWA installable |

### Task 5.3: Smart Compose AI
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 795–829 (Smart Compose AI) |
| **Description** | Build AI compose engine: autocomplete sentences, write full reply, tone adjustment, translate, summarize, subject suggestions, grammar check. SSE streaming responses. DeepSeek API integration. |
| **Files to Create** | `internal/compose/compose.go`, `internal/compose/stream.go` |
| **Dependencies** | 5.2 |
| **Effort** | 16 hours |
| **Acceptance Criteria** | AI compose streamed in webmail; all features work via SSE |

### Task 5.4: Admin Console
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 467–552 (Admin Console — Full Feature Spec) |
| **Description** | Build all admin panels: dashboard, domains, users, queue, logs, anti-spam, routing, backup, license, updates, resellers, system settings |
| **Files to Create** | `web/admin/` |
| **Dependencies** | 5.1 |
| **Effort** | 40 hours |
| **Acceptance Criteria** | All admin panels functional; real-time stats display; CRUD operations work |

### Task 5.5: License Management UI
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 525–529 (License Management in Admin Console) |
| **Description** | UI for license info, usage vs limits, activation/deactivation, offline support |
| **Files to Create** | `web/admin/` |
| **Dependencies** | 5.4 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | License status displayed; activation works; usage tracking shown |

### Task 5.6: DNS Wizard UI
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 296–303 (DNS Automation), Lines 476–481 |
| **Description** | One-click DNS setup wizard: MX, SPF, DKIM, DMARC, rDNS. Cloudflare API, Route53, and manual modes. |
| **Files to Create** | `web/admin/`, `internal/dns/` |
| **Dependencies** | 5.4 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | DNS records created via wizard; provider API integration works |

### Task 5.7: Stalwart Status/Config UI
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1488 |
| **Description** | Show Stalwart status, version, running config; validate config button |
| **Files to Create** | `web/admin/` |
| **Dependencies** | 5.4 |
| **Effort** | 6 hours |
| **Acceptance Criteria** | Stalwart status displayed; config validation triggers |

### Task 5.8: Firewall Rules UI
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 678–685 (Firewall Admin Panel Features) |
| **Description** | No-code rule builder, real-time block log, IP whitelist/blacklist, geo-block map, attack timeline |
| **Files to Create** | `web/admin/` |
| **Dependencies** | 5.4 |
| **Effort** | 12 hours |
| **Acceptance Criteria** | Rules created via UI enforced; block log displayed; geo-map interactive |

### Task 5.9: Auto-Heal Dashboard
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 718–723 (Heal History Log) |
| **Description** | Display heal history, success/failure rates, disable specific auto-heals, custom thresholds |
| **Files to Create** | `web/admin/` |
| **Dependencies** | 5.4 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | Heal history displayed; auto-heals configurable from UI |

### Task 5.10: Guardian Agent Dashboard
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 785–792 (Guardian Dashboard) |
| **Description** | Threat feed, threat categories breakdown, top attacking IPs/domains, weekly intelligence report, false positive reporting, custom AI instructions |
| **Files to Create** | `web/admin/` |
| **Dependencies** | 5.4 |
| **Effort** | 12 hours |
| **Acceptance Criteria** | Threat feed live; breakdown charts displayed; reports exportable |

### Task 5.11: Email Intelligence Dashboard
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 927–944 (Email Intelligence Dashboard) |
| **Description** | Per-domain and per-user insights: delivery rate, bounce rate, best send times, geographic distribution, anomaly alerts |
| **Files to Create** | `web/admin/`, `internal/intelligence/` |
| **Dependencies** | 5.4 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | Dashboard displays AI-powered insights; anomaly alerts generated |

### Task 5.12: Update Manager UI
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 531–536 (Auto-Update System in Admin Console) |
| **Description** | Current version + changelog, check for updates, one-click update, rollback button, update channel selector |
| **Files to Create** | `web/admin/` |
| **Dependencies** | 5.4 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | Update check/download/apply/rollback all work from UI |

### Task 5.13: Feature Flags UI
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1494 |
| **Description** | License-aware feature flags view; see which features are enabled/disabled and why |
| **Files to Create** | `web/admin/` |
| **Dependencies** | 5.4 |
| **Effort** | 4 hours |
| **Acceptance Criteria** | Feature flags displayed; license tier limitations shown |

### Task 5.14: PWA Manifest + Service Worker
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1495, Line 127 (PWA — install on mobile) |
| **Description** | PWA manifest, service worker for offline cache, install prompt |
| **Files to Create** | `web/webmail/` |
| **Dependencies** | 5.2 |
| **Effort** | 4 hours |
| **Acceptance Criteria** | Webmail installable on mobile via PWA; service worker registered |

---

## Phase 6 — Advanced Features (MVP Lines 1497–1511)

### Task 6.1: Calendar + Contacts + Tasks
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 429–448 (Calendar, Contacts), Line 126 (CalDAV/CardDAV sync) |
| **Description** | FullCalendar integration, CalDAV/CardDAV sync, event creation/management, contact groups, tasks with reminders |
| **Files to Create** | Calendar/contacts/tasks backend + frontend |
| **Dependencies** | 5.2 |
| **Effort** | 24 hours |
| **Acceptance Criteria** | Calendar events created/edited/deleted; CalDAV sync works; contacts sync via CardDAV |

### Task 6.2: ActiveSync (ISP+ Tier)
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 160 (ActiveSync mobile sync) |
| **Description** | Exchange ActiveSync protocol support for mobile device sync |
| **Files to Create** | ActiveSync integration |
| **Dependencies** | 6.1 |
| **Effort** | 16 hours |
| **Acceptance Criteria** | Mobile devices sync email/calendar/contacts via ActiveSync |

### Task 6.3: Smart Migration Tool
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 882–924 (Smart Migration Tool) |
| **Description** | Migration wizard with source adapters: Axigen, Zimbra, Exchange, cPanel, Google Workspace, generic IMAP. Phased: copy historical, sync new, DNS cutover, final sync. |
| **Files to Create** | `internal/migration/wizard.go`, `internal/migration/imap_sync.go`, `internal/migration/sources/` (5 adapters) |
| **Dependencies** | Phase 2 |
| **Effort** | 24 hours |
| **Acceptance Criteria** | Migration from all 5 sources works; validation passes |

### Task 6.4: Zero-Downtime Migration Engine
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 916–923 (Zero Downtime Migration) |
| **Description** | 4-phase migration: copy historical, real-time sync (dual delivery), DNS cutover (< 5 min), final sync + validation |
| **Files to Create** | `internal/migration/` |
| **Dependencies** | 6.3 |
| **Effort** | 12 hours |
| **Acceptance Criteria** | Migration completes with < 5 min downtime; validation matches source |

### Task 6.5: Anti-Spam Policy Engine + Quarantine
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 232 (Anti-spam), Lines 507–512 (Anti-spam in Admin Console) |
| **Description** | Spam score threshold config, whitelist/blacklist, per-domain settings, quarantine management UI |
| **Files to Create** | `internal/antispam/` |
| **Dependencies** | Phase 3 |
| **Effort** | 12 hours |
| **Acceptance Criteria** | Spam policies enforced; quarantine holds/releases messages |

### Task 6.6: Email Archiving + Legal Hold
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 132 (Archiving), Line 182 (Legal hold & eDiscovery) |
| **Description** | Email archiving with configurable retention; legal hold prevents deletion |
| **Files to Create** | `internal/compliance/` |
| **Dependencies** | Phase 2 |
| **Effort** | 12 hours |
| **Acceptance Criteria** | Archived emails retained per policy; legal hold prevents deletion |

### Task 6.7: Compliance Center
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 183–184 (Compliance Center — GDPR/HIPAA/SOX) |
| **Description** | GDPR data export/erasure, HIPAA controls, SOX compliance reporting, audit export |
| **Files to Create** | `internal/compliance/gdpr.go`, `internal/compliance/legal_hold.go`, `internal/compliance/ediscovery.go` |
| **Dependencies** | 6.6 |
| **Effort** | 16 hours |
| **Acceptance Criteria** | GDPR data export works; audit logs exportable; compliance reports generated |

### Task 6.8: Zero-Knowledge Encryption
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 948–964 (Zero-Knowledge Encryption) |
| **Description** | Client-side encryption: master password → PBKDF2 key derivation → AES-256-GCM encrypt in browser → store ciphertext only |
| **Files to Create** | `internal/encryption/zke.go`, frontend crypto |
| **Dependencies** | 5.2 |
| **Effort** | 12 hours |
| **Acceptance Criteria** | Emails encrypted client-side; server stores only ciphertext; admin cannot read |

### Task 6.9: Collaboration Layer / Shared Inbox
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 969–988 (Collaboration Layer — Shared Inbox) |
| **Description** | Shared inbox: assign emails, internal notes, collision detection, status tracking, SLA timer, templates |
| **Files to Create** | `internal/collaboration/shared_inbox.go`, `internal/collaboration/assignment.go` |
| **Dependencies** | 5.2 |
| **Effort** | 12 hours |
| **Acceptance Criteria** | Shared inbox functional; assignment/notes/collision detection work |

### Task 6.10: Multi-Cloud Storage
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 162–163 (Multi-Cloud Storage), Line 193 (S3/external storage) |
| **Description** | S3, GCS, Azure storage adapters for mail data |
| **Files to Create** | `internal/storage/cloud/` |
| **Dependencies** | Phase 2 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | Mail stored in S3/GCS/Azure; retrieval works |

### Task 6.11: Email Intelligence AI Insights
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 927–944 (Email Intelligence Dashboard) |
| **Description** | AI-powered insights: delivery trends, bounce alerts, best send times, geographic distribution, anomaly detection |
| **Files to Create** | `internal/intelligence/analyzer.go`, `internal/intelligence/insights.go` |
| **Dependencies** | Phase 2 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | Intelligence dashboard shows AI-generated insights |

### Task 6.12: Backup & Restore
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 519–523 (Backup & Restore in Admin Console) |
| **Description** | Scheduled backups (daily/weekly), local or S3 storage, one-click restore, backup encryption |
| **Files to Create** | `internal/storage/` |
| **Dependencies** | Phase 2 |
| **Effort** | 12 hours |
| **Acceptance Criteria** | Backup scheduled and stored; restore returns data to original state |

### Task 6.13: LDAP/AD Sync (Enterprise)
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 179 (LDAP/Active Directory sync) |
| **Description** | Sync users from LDAP/AD; map groups to mailboxes; periodic sync |
| **Files to Create** | `internal/auth/` |
| **Dependencies** | 4.1 |
| **Effort** | 12 hours |
| **Acceptance Criteria** | Users synced from LDAP/AD; groups mapped to mailboxes |

### Task 6.14: SSO — SAML 2.0 + OAuth2 (Enterprise)
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 180 (SSO — SAML 2.0 / OAuth2) |
| **Description** | SAML 2.0 and OAuth2/OIDC federation for Enterprise SSO |
| **Files to Create** | `internal/auth/` |
| **Dependencies** | 4.1 |
| **Effort** | 12 hours |
| **Acceptance Criteria** | SAML 2.0 login works; OAuth2/OIDC login works |

---

## Phase 7 — Hardening & Launch (MVP Lines 1513–1528)

### Task 7.1: Full Security Audit
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1514 (Full security audit — all attack vectors) |
| **Description** | Comprehensive security audit covering all attack vectors |
| **Dependencies** | All phases |
| **Effort** | 16 hours |
| **Acceptance Criteria** | All findings documented and addressed |

### Task 7.2: Penetration Testing
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1515 (Penetration testing — auth bypass, injection, tenant isolation) |
| **Description** | External pen test covering auth bypass, injection, tenant isolation |
| **Dependencies** | 7.1 |
| **Effort** | 16 hours |
| **Acceptance Criteria** | All critical/high findings resolved |

### Task 7.3: Load Testing
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1516 (Load testing — 10k concurrent connections) |
| **Description** | Load test with 10k concurrent connections; measure throughput and latency |
| **Dependencies** | All phases |
| **Effort** | 16 hours |
| **Acceptance Criteria** | 10k concurrent connections handled; p95 latency within SLA |

### Task 7.4: Deliverability Testing
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1517 (Deliverability testing — Gmail, Outlook, Yahoo) |
| **Description** | Test email deliverability to major providers |
| **Dependencies** | Phase 2 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | >95% inbox placement at Gmail/Outlook/Yahoo |

### Task 7.5: Auto-Update System
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 1138–1204 (Auto-Update System — Safe Pipeline) |
| **Description** | Build complete auto-update pipeline: manifest verification, SHA256 checksum, GPG signature, snapshot creation, prechecks, health checks, auto-rollback |
| **Files to Create** | `internal/updater/` |
| **Dependencies** | All phases |
| **Effort** | 16 hours |
| **Acceptance Criteria** | Full update → verify → rollback cycle passes |

### Task 7.6: Update Channels
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 1228–1242 (Update Channels — Stable/Beta/Early Access/Nightly) |
| **Description** | Implement channel definitions: Stable, Beta, Early Access, Nightly |
| **Files to Create** | `internal/updater/` |
| **Dependencies** | 7.5 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | Channel switching works; each channel serves correct releases |

### Task 7.7: One-Line Installer Script
| Field | Value |
|-------|-------|
| **MVP Reference** | Lines 1372–1383 (Installer Script) |
| **Description** | `curl -sSL https://get.orvix.email | bash` — detects OS/arch, downloads package, installs Stalwart, creates services, generates config, creates admin, activates license |
| **Files to Create** | `scripts/install.sh` |
| **Dependencies** | All phases |
| **Effort** | 8 hours |
| **Acceptance Criteria** | Clean install on Ubuntu 24.04, Debian 12, RHEL 9 works |

### Task 7.8: Full API Documentation
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1521 (Full API documentation) |
| **Description** | Document all REST API endpoints with examples |
| **Dependencies** | Phase 4 |
| **Effort** | 16 hours |
| **Acceptance Criteria** | All endpoints documented; examples for each |

### Task 7.9: Admin Documentation
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1522 (Admin documentation) |
| **Description** | Write admin guide covering installation, configuration, operations |
| **Dependencies** | All phases |
| **Effort** | 12 hours |
| **Acceptance Criteria** | Admin can install and operate from docs alone |

### Task 7.10: Marketing Website
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1523 (Marketing website — Next.js — orvix.email) |
| **Description** | Build marketing site at orvix.email |
| **Files to Create** | Marketing website project |
| **Dependencies** | None |
| **Effort** | 24 hours |
| **Acceptance Criteria** | Marketing site live at orvix.email |

### Task 7.11: License Purchase Flow + Customer Portal
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1524 (License purchase flow + customer portal) |
| **Description** | Build purchase flow: Stripe integration, license activation, customer portal (portal.orvix.email) |
| **Files to Create** | `web/portal/` |
| **Dependencies** | 1.8 |
| **Effort** | 16 hours |
| **Acceptance Criteria** | Customer purchases license → key activated → using product |

### Task 7.12: Update Server Setup
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1525 (Update server setup — updates.orvix.email) |
| **Description** | Set up and deploy the update server at updates.orvix.email |
| **Dependencies** | 7.5 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | Update server serves manifests, packages, checksums |

### Task 7.13: Status Page Setup
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1526 (Status page setup — status.orvix.email) |
| **Description** | Deploy public status page at status.orvix.email |
| **Dependencies** | None |
| **Effort** | 4 hours |
| **Acceptance Criteria** | Status page live; shows service health |

### Task 7.14: Stalwart Compatibility Certification
| Field | Value |
|-------|-------|
| **MVP Reference** | Line 1527 (Stalwart compatibility certification for launch release) |
| **Description** | Certify OrvixEM 1.0.0 against supported Stalwart versions |
| **Dependencies** | Phase 2 |
| **Effort** | 8 hours |
| **Acceptance Criteria** | Compatibility matrix verified; certified versions documented |

---

## Backlog Summary

| Phase | Tasks | Hours | P0 | P1 | P2 | P3 |
|-------|-------|-------|----|----|----|----|
| Phase 1: Foundation | 11 | 74 | 7 | 4 | 0 | 0 |
| Phase 2: Stalwart Integration | 13 | 128 | 6 | 7 | 0 | 0 |
| Phase 3: Security Layer | 12 | 120 | 2 | 6 | 4 | 0 |
| Phase 4: API Layer | 10 | 92 | 5 | 4 | 1 | 0 |
| Phase 5: Frontend | 14 | 218 | 3 | 7 | 4 | 0 |
| Phase 6: Advanced Features | 14 | 192 | 0 | 3 | 10 | 1 |
| Phase 7: Hardening & Launch | 14 | 176 | 6 | 5 | 2 | 1 |
| **Total** | **88** | **1000** | **29** | **36** | **21** | **2** |

---

**Note:** Every task in this backlog maps directly to a specific section, line, or feature in the Orvix_Enterprise_Mail_COMPLETE_MVP.md. No features are invented. No MVP features are missing.
