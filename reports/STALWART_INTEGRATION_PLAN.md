# STALWART INTEGRATION PLAN

## Orvix Enterprise Mail (OrvixEM)

**Date:** 2026-06-05
**Reference:** MVP Sections: Architecture (Lines 33–105), Stalwart Integration (Lines 216–259), Project Structure (Lines 999–1006)

---

## 1. Integration Architecture

### 1.1 Responsibility Boundary

Per MVP lines 216–259, the boundary is:

```
Stalwart Core Engine                    Orvix Enterprise Layer
─────────────────────                    ─────────────────────
SMTP Server                              License validation
IMAP Server                              Multi-tenant admin
POP3 Server                              Webmail UI
JMAP Server                              REST API
Mail Storage                             DNS automation
Queue processing                         Mail Firewall policy
DKIM signing                             Guardian AI analysis
SPF validation                           Smart Compose AI
DMARC enforcement                        Migration tooling
TLS termination                          Auto-Heal system
Anti-spam (basic)                        Auto-update system
                                         Compliance center
                                         Monitoring/alerting
                                         Backup & restore
```

### 1.2 Integration Pattern

```
┌──────────────────────────────────────────────────────────────────┐
│                     OrvixEM Application                           │
│                                                                    │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │                  internal/stalwart/                          │  │
│  │  ┌─────────────┐  ┌─────────────┐  ┌────────────────────┐ │  │
│  │  │ config.go   │  │ service.go   │  │ provisioning.go    │ │  │
│  │  │ (generates  │  │ (manages    │  │ (creates domains,  │ │  │
│  │  │  orvix.yaml │  │  stalwart    │  │  mailboxes, etc.)  │ │  │
│  │  │  for        │  │  process)    │  └────────────────────┘ │  │
│  │  │  Stalwart)  │  └─────────────┘                          │  │
│  │  └─────────────┘                                           │  │
│  │  ┌─────────────┐  ┌─────────────┐  ┌────────────────────┐ │  │
│  │  │ events.go   │  │ api.go       │  │ compatibility.go   │ │  │
│  │  │ (ingests    │  │ (client     │  │ (version matrix,   │ │  │
│  │  │  Stalwart   │  │  adapter)    │  │  migration guards)  │ │  │
│  │  │  logs/events)│  │             │  └────────────────────┘ │  │
│  │  └─────────────┘  └─────────────┘                          │  │
│  └────────────────────────────────────────────────────────────┘  │
│                                                                    │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │                  Integration Methods                         │  │
│  │                                                              │  │
│  │  Method 1: Config File Generation                            │  │
│  │  ─────────────────────────────                                │  │
│  │  OrvixEM writes /etc/stalwart/config.yaml                    │  │
│  │  OrvixEM validates config before writing                     │  │
│  │  OrvixEM signals Stalwart to reload                          │  │
│  │                                                              │  │
│  │  Method 2: Stalwart Management API (JMAP)                    │  │
│  │  ─────────────────────────────────────────                    │  │
│  │  OrvixEM calls Stalwart JMAP admin endpoints                 │  │
│  │  Used for: domain/mailbox/alias CRUD operations              │  │
│  │                                                              │  │
│  │  Method 3: Stalwart Log/Metrics Collection                   │  │
│  │  ──────────────────────────────────────                       │  │
│  │  OrvixEM reads Stalwart log files via tail                    │  │
│  │  OrvixEM polls Stalwart status endpoints                     │  │
│  │                                                              │  │
│  │  Method 4: Direct Process Management                         │  │
│  │  ────────────────────────────────                             │  │
│  │  OrvixEM manages Stalwart systemd service                    │  │
│  │  OrvixEM validates Stalwart health via port checks           │  │
│  └────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────┘
```

---

## 2. Integration Components — Detailed

### 2.1 `internal/stalwart/config.go`

**Purpose:** Generate, validate, and apply Stalwart configuration.

| Responsibility | Implementation |
|---------------|----------------|
| Generate Stalwart config from OrvixEM models | Templates for `config.yaml` sections (server, tls, auth, storage, directory, queue, dkim, dmarc, spf) |
| Validate config before reload | Syntax check, port availability check, required field validation |
| Apply config | Write to Stalwart config directory, trigger reload |
| Config versioning | Track config versions, support rollback to previous valid config |
| Config drift detection | Compare running config vs expected config; alert on mismatch |

**MVP Reference:** Lines 246–247, File: `internal/stalwart/config.go` (line 1001)

### 2.2 `internal/stalwart/service.go`

**Purpose:** Manage Stalwart service lifecycle.

| Responsibility | Implementation |
|---------------|----------------|
| Stalwart installation detection | Check for Stalwart binary, version, supported version check |
| Service start | Start Stalwart via systemd or direct process |
| Service stop | Graceful stop with timeout, force kill if needed |
| Service restart | Config validation → graceful restart → health check → rollback on failure |
| Service reload | Signal reload for config changes (no downtime) |
| Service status | Running/stopped/crashed, uptime, version |
| Health check | TCP port checks for SMTP(25/587/465), IMAP(143/993), POP3(110/995), JMAP(80/443) |

**MVP Reference:** Lines 248, File: `internal/stalwart/service.go` (line 1002)

### 2.3 `internal/stalwart/api.go`

**Purpose:** Client adapters for Stalwart management interfaces.

| Responsibility | Implementation |
|---------------|----------------|
| JMAP admin API client | HTTP client for Stalwart JMAP admin endpoints |
| REST API client | HTTP client for Stalwart's REST management API (if available) |
| API authentication | Handle Stalwart admin auth |
| Error handling | Translate Stalwart API errors to OrvixEM error types |
| Rate limiting for API calls | Prevent overwhelming Stalwart with requests |

**MVP Reference:** File: `internal/stalwart/api.go` (line 1003)

### 2.4 `internal/stalwart/events.go`

**Purpose:** Normalize Stalwart events and logs into OrvixEM event system.

| Responsibility | Implementation |
|---------------|----------------|
| Log ingestion | Tail Stalwart log files, parse structured log entries |
| Event normalization | Map Stalwart event types to OrvixEM event types |
| Metric extraction | Extract SMTP metrics, queue depth, delivery stats from logs |
| Authentication events | Extract login successes/failures for auth monitoring |
| Security events | Extract spam reports, DMARC failures, auth failures |
| Log format support | JSON logs (Stalwart default), syslog, custom formats |

**MVP Reference:** File: `internal/stalwart/events.go` (line 1004)

### 2.5 `internal/stalwart/provisioning.go`

**Purpose:** Create and manage mail infrastructure objects in Stalwart.

| Responsibility | Implementation |
|---------------|----------------|
| Domain creation | Create domain in Stalwart directory (JMAP admin API) |
| Mailbox creation | Create mailbox/user in Stalwart with password, quota |
| Alias creation | Create email aliases for routing |
| Quota management | Set/get mailbox quotas |
| Policy management | Set per-domain spam/security policies |
| User deactivation | Suspend/delete users and mailboxes |
| Bulk operations | Batch create domains/mailboxes (for Instant Deploy API) |

**MVP Reference:** Lines 247, File: `internal/stalwart/provisioning.go` (line 1005)

### 2.6 `internal/stalwart/compatibility.go`

**Purpose:** Manage Stalwart version compatibility.

| Responsibility | Implementation |
|---------------|----------------|
| Version detection | Parse Stalwart version string |
| Compatibility matrix | Map OrvixEM version → supported Stalwart versions |
| Upgrade guards | Prevent upgrade if Stalwart version incompatible |
| Feature detection | Check Stalwart version for supported features |
| Migration validation | Validate that migration between versions is safe |
| Deprecation warnings | Warn admin when using Stalwart version nearing EOL |

**MVP Reference:** File: `internal/stalwart/compatibility.go` (line 1006)

### 2.7 `internal/stalwart/diagnostics.go`

**Purpose:** Produce support diagnostics without exposing secrets.

| Responsibility | Implementation |
|---------------|----------------|
| Bundle collection | Collect: config (redacted), logs (last 1000 lines), status, metrics |
| Secret redaction | Remove passwords, keys, tokens from diagnostic output |
| Health summary | Include current health check results |
| Config diff | Show last config change with diff |
| Stalwart diagnostics | Collect Stalwart's own diagnostic output |
| Bundle packaging | Produce `.tar.gz` for support handoff |

**MVP Reference:** File: `internal/stalwart/diagnostics.go` (line 1007)

---

## 3. Integration Methods

### 3.1 Method 1: Config File Generation (Primary)

**Use Case:** Initial setup, major configuration changes, periodic drift correction

```
OrvixEM Admin UI → Save Settings
    ↓
internal/stalwart/config.go generates Stalwart config.toml
    ↓
Config validated (syntax + port + schema checks)
    ↓
Current config backed up
    ↓
New config written to /etc/stalwart/config.d/100-orvix.toml
    ↓
Stalwart reload signaled (SIGHUP)
    ↓
Health check: SMTP/IMAP/JMAP responding?
    ├─ Yes → Mark config as active
    └─ No  → Restore backup, log failure, alert admin
```

### 3.2 Method 2: JMAP Admin API (Runtime Operations)

**Use Case:** Domain/mailbox/alias CRUD during normal operations

```
POST /api/v1/domains (OrvixEM API)
    ↓
internal/mailops/domains.go
    ↓
internal/stalwart/provisioning.go CreateDomain()
    ↓
Stalwart JMAP Admin API: /api/v1/session → Create directory entry
    ↓
Response: domain created + DNS records
    ↓
OrvixEM database updated with domain metadata
```

### 3.3 Method 3: Log/Metrics Collection (Observability)

**Use Case:** Dashboards, alerts, analytics

```
Stalwart Log File (/var/log/stalwart/*.json)
    ↓
internal/stalwart/events.go (tail + parse)
    ↓
Normalized events → OrvixEM event bus
    ↓
├─ Prometheus metrics updated
├─ Audit logs recorded
├─ Email Intelligence processed
└─ Auto-Heal system evaluated
```

### 3.4 Method 4: Direct Process Management (Operations)

**Use Case:** Service lifecycle, health monitoring

```
orvix stalwart status
    ↓
internal/stalwart/service.go Status()
    ↓
Check: systemctl is-active stalwart-server
Check: TCP port 25, 143, 993, 587 open
Check: Stalwart binary running
Check: Stalwart version compatible
    ↓
Return: status + details + recommended actions
```

---

## 4. Configuration Management

### 4.1 Config Sections Generated by OrvixEM

| Stalwart Config Section | OrvixEM Source | Purpose |
|------------------------|----------------|---------|
| `server.*` | Config + admin settings | Listen addresses, ports, TLS |
| `tls.*` | Config + ACME integration | Certificate paths, ACME provider |
| `auth.*` | User database | Authentication backends, OAuth2 |
| `storage.*` | OrvixEM models | Mail store paths, blob storage |
| `directory.*` | Provisioning API | User/domain directory backend |
| `queue.*` | Config | Queue paths, retry settings |
| `session.*` | Config | Session timeout, storage |
| `spam.*` | Admin settings | Spam filter configuration |
| `dmarc.*` | Admin settings | DMARC reporting |
| `dkim.*` | DNS automation | DKIM key management |
| `report.*` | Config | Reporting configuration |

### 4.2 Config Validation Pipeline

```
1. Schema validation     → All required fields present
2. Port validation       → No port conflicts detected
3. Path validation       → All referenced paths exist/writable
4. TLS validation        → Certificates exist and valid
5. Auth validation       → Auth backends configured correctly
6. Storage validation    → Storage paths have sufficient space
7. Stalwart syntax check → Stalwart itself validates config (--validate-config)
```

---

## 5. What Stalwart Handles (Must NOT Duplicate)

| Area | Stalwart Responsibility | OrvixEM Role |
|------|------------------------|--------------|
| SMTP protocol | Accept/relay/deliver SMTP messages | Generate SMTP config, monitor queue, show delivery stats |
| IMAP protocol | Serve IMAP connections | Generate IMAP config, monitor connections |
| POP3 protocol | Serve POP3 connections | Generate POP3 config, optionally disable |
| JMAP protocol | Serve JMAP connections | Generate JMAP config, use JMAP admin API |
| Mail storage | Store/retrieve messages on disk | Manage storage paths, quotas, backup orchestration |
| Message queue | Queue/retry/deliver messages | Visualize queue, force retry, delete messages via API |
| DKIM signing | Sign outbound messages | Manage DKIM keys, rotate keys, expose in DNS wizard |
| SPF validation | Validate inbound SPF | Display SPF results, provide policy UI |
| DMARC enforcement | Apply DMARC policy | Set DMARC policy per domain, display reports |
| TLS termination | Terminate TLS connections | Manage certificates, automate renewal via ACME |
| Anti-spam scoring | Score inbound messages | Set spam thresholds, manage quarantine, provide policy overrides |

---

## 6. What OrvixEM Handles (MUST Build)

| Area | OrvixEM Responsibility | Stalwart Role |
|------|-----------------------|--------------|
| License enforcement | Validate license, gate features | None — entirely OrvixEM |
| Multi-tenant admin | Create/manage domains and users | Provide underlying storage and auth |
| Webmail UI | React email client | None — JMAP API used by webmail |
| REST API | Full management API | None — OrvixEM-built |
| Mail Firewall | Multi-layer policy engine | Provide events for firewall to analyze |
| Guardian AI | AI threat analysis | Provide message data for analysis |
| Smart Compose AI | AI writing assistant | None — entirely OrvixEM |
| Migration tool | Import from other providers | Accept imported data |
| Auto-heal | Monitor and fix issues | Report status for monitoring |
| Auto-update | Update OrvixEM + coordinate Stalwart updates | Version compatibility validation |
| DNS automation | Manage DNS records | Provide DKIM keys for DNS |
| Compliance center | Legal hold, eDiscovery, DLP | Provide message storage access |
| Zero-knowledge encryption | Client-side encryption | Store encrypted blobs |
| Collaboration | Shared inbox features | Route messages to shared inboxes |

---

## 7. Integration Dependency Order

```
Phase 2 Build Order:
    ↓
1. config.go      ─── Config generation (needed first)
2. service.go     ─── Service management (needs running Stalwart)
3. compatibility.go ─ Version detection (needs installed Stalwart)
4. provisioning.go  ─ Domain/mailbox CRUD (needs Stalwart API)
5. api.go          ─ API client (needs Stalwart API)
6. events.go       ─ Event ingestion (needs running Stalwart)
7. diagnostics.go  ─ Support bundle (needs everything else)
```

---

## 8. Proprietary Considerations

### 8.1 What Should Remain Proprietary to OrvixEM

| Component | Reason |
|-----------|--------|
| License engine and validation | Revenue protection |
| Feature flag system | Tier enforcement |
| Admin UI and Portal UI | Customer experience — white-label |
| Webmail UI | Customer experience |
| Migration source adapters (Axigen, Zimbra, Exchange) | Competitive advantage |
| Guardian AI threat models | Proprietary AI |
| Smart Compose AI integration | Proprietary AI |
| Mail Firewall rules engine | Security — proprietary logic |
| Auto-Heal fixers | Operations — proprietary knowledge |

### 8.2 What Can Be Open Source

| Component | Reason |
|-----------|--------|
| Stalwart configuration templates | Standards-based config |
| Generic IMAP sync library | Industry standard protocol |
| REST API specifications | Industry standard API design |
| Database schema | Standard patterns |
| DNS automation providers (Cloudflare, Route53) | Standard API integrations |

---

## 9. Risk Assessment for Integration

| Risk | Probability | Impact | Mitigation |
|------|-----------|--------|------------|
| Stalwart JMAP API changes | Medium | High | Abstract API client; maintain adapter layer |
| Stalwart config format changes | Medium | Medium | Config templates versioned; validate before apply |
| Stalwart doesn't support needed admin operations | Low | High | Fall back to config generation for unsupported operations |
| Stalwart performance bottleneck | Low | Medium | Horizontal scaling with multiple Stalwart instances |
| Stalwart security vulnerability | Low | Critical | Monitor Stalwart security advisories; update promptly |
| Stalwart license changes | Low | Critical | Legal review before commercial bundling |
| OrvixEM code too tightly coupled to Stalwart version | Medium | High | Compatibility abstraction layer (`compatibility.go`) |

---

## 10. Integration Verification Checklist

| Check | Test | Pass Criteria |
|-------|------|--------------|
| Config generation | `orvix stalwart validate-config` | Config file valid and accepted by Stalwart |
| Service lifecycle | `orvix stalwart status` | Stalwart running, healthy |
| Domain provisioning | Create domain via API | Domain appears in Stalwart directory |
| Mailbox provisioning | Create mailbox via API | Mailbox can connect via IMAP |
| Queue visibility | Send email, check queue | Queue visible in OrvixEM admin |
| Health checks | All ports probed | SMTP(25/587/465), IMAP(143/993), JMAP healthy |
| Log ingestion | Send email, check logs | Event appears in OrvixEM audit log |
| DKIM management | Rotate DKIM key | New key valid, old key revoked |
| TLS management | Renew cert | New cert valid, no downtime |
| Config drift | Manually change Stalwart config | Detected and alerted within 60s |
| Diagnostic bundle | Run `orvix doctor` | Bundle created, no secrets exposed |

---

**Conclusion:** The Stalwart integration is the most critical technical component of OrvixEM. The architecture uses 4 integration methods (config generation, JMAP API, log collection, process management) to provide complete control without rebuilding mail protocols. The `internal/stalwart/` package provides a clean abstraction boundary, ensuring that OrvixEM never directly touches SMTP/IMAP/POP3/JMAP protocol code. All integration is through configuration, API calls, log observation, and process management — exactly as the MVP mandates.
