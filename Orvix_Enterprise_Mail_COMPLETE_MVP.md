# Orvix Enterprise Mail (OrvixEM) — Enterprise Email Platform MVP
> Powered by Stalwart Core · Orvix Enterprise Layer · License-Driven Features · Enterprise Grade

---

## 🧭 Vision

Orvix Enterprise Mail (OrvixEM) is a self-hosted, white-label enterprise email platform built around a proven mail core: **Stalwart Core Engine**.

Instead of wasting years rebuilding SMTP, IMAP, POP3, JMAP, storage, queues and protocol correctness through Stalwart Core integration, OrvixEM focuses on the commercial layer that customers actually buy:

- Enterprise administration
- Licensing and feature control
- Multi-tenant hosting/provider management
- Reseller and data center operations
- Migration automation
- Safe updates and rollback
- Guardian AI security
- Deliverability visibility
- Backup, monitoring and compliance
- White-label customer experience

Competitors charge $3,000–$10,000/year. OrvixEM targets **$500–$2,500/year** across three tiers, sold to:
- Hosting companies
- Data centers
- ISPs
- Enterprises running private mail infrastructure

**Stalwart Core. Orvix Enterprise Layer. One license key. Everything unlocks.**

---

## 🏗️ Architecture Overview

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                    Orvix Enterprise Mail Platform                             │
│                         Official Domain: orvix.email                         │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐  │
│  │                         Stalwart Core Engine                            │  │
│  │                                                                        │  │
│  │  SMTP  │  IMAP  │  POP3  │  JMAP  │  Mail Store  │ Queue │ TLS │ Auth │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
│                                      │                                       │
│                                      ▼                                       │
│  ┌────────────────────────────────────────────────────────────────────────┐  │
│  │                         Orvix Enterprise Layer                          │  │
│  │                                                                        │  │
│  │  ┌──────────────┐ ┌──────────────┐ ┌────────────────┐ ┌─────────────┐ │  │
│  │  │ Web Server   │ │ Admin Console│ │ License Manager│ │ Auto Updater│ │  │
│  │  │ Webmail UI   │ │ Provider Ops │ │ Feature Flags  │ │ Rollback    │ │  │
│  │  │ REST API     │ │ Reseller UI  │ │ Entitlements   │ │ Channels    │ │  │
│  │  └──────────────┘ └──────────────┘ └────────────────┘ └─────────────┘ │  │
│  │                                                                        │  │
│  │  ┌──────────────┐ ┌──────────────┐ ┌────────────────┐ ┌─────────────┐ │  │
│  │  │ Security     │ │ Storage Ops  │ │ DNS Manager    │ │ Monitoring  │ │  │
│  │  │ Guardian AI  │ │ Backup/S3    │ │ Auto Config    │ │ Telemetry   │ │  │
│  │  │ Firewall UI  │ │ Retention    │ │ DKIM/DMARC     │ │ Alerts      │ │  │
│  │  └──────────────┘ └──────────────┘ └────────────────┘ └─────────────┘ │  │
│  │                                                                        │  │
│  │  ┌──────────────┐ ┌──────────────┐ ┌────────────────┐ ┌─────────────┐ │  │
│  │  │ Migration    │ │ Auto-Heal    │ │ Smart Compose  │ │ Deploy API  │ │  │
│  │  │ Wizard       │ │ System       │ │ AI             │ │ Provisioning│ │  │
│  │  │ IMAP Sync    │ │ Fixers       │ │ DeepSeek/Ollama│ │ Data Center │ │  │
│  │  └──────────────┘ └──────────────┘ └────────────────┘ └─────────────┘ │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Official Branding

| Item | Value |
|------|-------|
| Brand | **Orvix** |
| Full Product Name | **Orvix Enterprise Mail** |
| Short Name | **OrvixEM** |
| Official Domain | **orvix.email** |
| Repository | **github.com/orvixemail/orvix** |
| Binary | **orvix** |
| Config File | **orvix.yaml** |
| Installer | **https://get.orvix.email** |

### Official Service Map

| Hostname | Purpose |
|----------|---------|
| `orvix.email` | Marketing website |
| `portal.orvix.email` | Customer portal |
| `admin.orvix.email` | Company/admin control panel |
| `license.orvix.email` | License authority API/admin |
| `mail.orvix.email` | Webmail |
| `api.orvix.email` | Public API |
| `docs.orvix.email` | Documentation |
| `updates.orvix.email` | Update metadata, packages and release channels |
| `status.orvix.email` | Public service status page |

### Core Principle
- **Use Stalwart as the proven mail protocol and storage core** instead of rebuilding the mail engine from zero.
- **Single commercial OrvixEM package** ships Stalwart integration, admin panels, webmail, migrations, licensing and update system.
- **SQLite** for small installs, **PostgreSQL** for scale.
- **License key** unlocks tiers — same package, different features.
- **Enterprise value lives in OrvixEM Layer**: management, automation, migration, monitoring, supportability, licensing and safe operations.
- **No destructive updates by default**: migrations are additive-only unless a special major-version migration plan exists.

---

## 📦 License Tiers

### Tier 1 — SMB ($500/year)
For hosting companies and small businesses.

| Feature | Included |
|---------|----------|
| Domains | Up to 10 |
| Mailboxes | Up to 500 |
| SMTP / IMAP / POP3 | ✅ |
| Webmail | ✅ |
| Mail Firewall (basic rules) | ✅ |
| Auto-Heal System | ✅ |
| Anti-spam (basic) | ✅ |
| SSL/TLS auto | ✅ |
| DNS wizard | ✅ |
| 2FA | ✅ |
| Mobile sync (CalDAV/CardDAV) | ✅ |
| Calendar & Contacts | ✅ |
| PWA (install on mobile) | ✅ |
| Smart Compose AI (basic) | ✅ |
| White Label | ❌ |
| Clustering | ❌ |
| API access | ❌ |
| Archiving | ❌ |
| Guardian Agent | ❌ |
| Instant Deploy API | ❌ |

---

### Tier 2 — ISP ($1,200/year)
For ISPs and regional data centers.

Includes everything in SMB, plus:

| Feature | Included |
|---------|----------|
| Domains | Unlimited |
| Mailboxes | Up to 50,000 |
| White Label (full) | ✅ |
| REST API | ✅ |
| Instant Deployment API | ✅ |
| Clustering (up to 3 nodes) | ✅ |
| Advanced anti-spam/AV | ✅ |
| Mail Firewall (advanced + custom rules) | ✅ |
| Guardian Agent (full AI) | ✅ |
| Auto-Heal (advanced) | ✅ |
| Email Archiving | ✅ |
| Distribution Lists | ✅ |
| Shared Calendars | ✅ |
| Resource Booking | ✅ |
| Public Folders | ✅ |
| ActiveSync (mobile) | ✅ |
| Reseller Panel | ✅ |
| Smart Migration Tool | ✅ |
| Multi-Cloud Storage | ✅ |
| SLA monitoring dashboard | ✅ |
| Webhooks + Zapier Integration | ✅ |
| Email Intelligence Dashboard | ✅ |

---

### Tier 3 — Enterprise ($2,500/year)
For enterprises running private mail infrastructure.

Includes everything in ISP, plus:

| Feature | Included |
|---------|----------|
| Mailboxes | Unlimited |
| Clustering (unlimited nodes) | ✅ |
| LDAP / Active Directory sync | ✅ |
| SSO (SAML 2.0 / OAuth2) | ✅ |
| Advanced Email Routing Rules | ✅ |
| Legal Hold & eDiscovery | ✅ |
| DLP (Data Loss Prevention) | ✅ |
| Compliance Center (GDPR/HIPAA/SOX) | ✅ |
| Zero-Knowledge Encryption | ✅ |
| Guardian Agent + API + Custom Training | ✅ |
| Collaboration Layer (Shared Inbox) | ✅ |
| Smart Compose AI (advanced) | ✅ |
| Audit Logs (full) | ✅ |
| Priority support SLA | ✅ |
| Custom branding (deep) | ✅ |
| Backup & Restore (built-in) | ✅ |
| S3 / external storage | ✅ |
| Webhook system | ✅ |
| Auto-update from admin panel | ✅ |

---

## ⚙️ Tech Stack

### Backend
| Component | Technology | Why |
|-----------|-----------|-----|
| Language | **Go 1.23+** | Single binary, fast, low memory |
| Web Framework | **Fiber v3** | Fastest Go HTTP framework |
| ORM | **GORM** | Clean models, migration support |
| Database (small) | **SQLite (via modernc)** | Zero dependency |
| Database (scale) | **PostgreSQL 16** | Power and scale |
| Cache / Queue | **Redis 7** | Session, queue, rate limiting |
| Search | **Bleve** (embedded) | Full-text search, no external deps |
| Job Queue | **Asynq** | Redis-backed, reliable |
| Config | **Viper** | Flexible config management |
| Logging | **Zap** | Fast structured logging |
| Metrics | **Prometheus** | Built-in observability |

### Stalwart Core Engine + Orvix Enterprise Layer

OrvixEM does **not** build SMTP, IMAP, POP3, JMAP and mail storage through Stalwart Core integration. The production architecture uses **Stalwart Core Engine** for the low-level mail server foundation, while OrvixEM adds the commercial enterprise layer around it.

| Component | Technology / Ownership |
|-----------|------------------------|
| SMTP Server | **Stalwart Core Engine** |
| IMAP Server | **Stalwart Core Engine** |
| POP3 Server | **Stalwart Core Engine** |
| JMAP | **Stalwart Core Engine** |
| Mail Storage | **Stalwart Core Engine**, controlled and surfaced through OrvixEM admin APIs |
| Queue | **Stalwart Core Engine** + OrvixEM queue visibility, policy controls and dashboard |
| DKIM Signing | **Stalwart Core Engine** + OrvixEM DNS wizard and rotation workflows |
| SPF Validation | **Stalwart Core Engine** + OrvixEM policy UI and reporting |
| DMARC | **Stalwart Core Engine** + OrvixEM enforcement UI and reporting |
| TLS | Stalwart TLS + OrvixEM certificate automation and admin UX |
| Anti-spam | Stalwart filtering + OrvixEM policy, quarantine, reporting and Guardian AI layer |
| Anti-virus | Optional ClamAV integration managed by OrvixEM |
| Bounce Handling | Stalwart delivery signals + OrvixEM analytics, alerts and outbound safeguards |
| Mail Firewall | OrvixEM multi-layer policy engine around Stalwart events and message flow |
| Auto-Heal | OrvixEM health monitor, service repair and admin alerting |
| Guardian Agent | OrvixEM DeepSeek/Ollama AI + AbuseIPDB + VirusTotal integrations |
| Smart Compose | OrvixEM DeepSeek/Ollama AI, streamed in webmail per-user context |
| Migration Engine | OrvixEM IMAP sync + provider-specific migration workflows |
| Instant Deploy API | OrvixEM provisioning engine controlling domains, users, DNS, policies and Stalwart configuration |

### Stalwart Integration Responsibilities

| Area | OrvixEM Responsibility |
|------|------------------------|
| Configuration | Generate, validate, version and safely apply Stalwart configuration |
| Provisioning | Create domains, mailboxes, aliases, policies and quotas through supported APIs/config flows |
| Observability | Collect logs, metrics, queue state, authentication events and delivery signals |
| Safety | Validate configuration before reload/restart and auto-rollback on failed health checks |
| Licensing | Enable/disable OrvixEM enterprise capabilities without corrupting Stalwart data |
| Updates | Coordinate OrvixEM updates and Stalwart version compatibility through safe channels |
| Migration | Import data into the platform without bypassing integrity, ownership or quota rules |
| Supportability | Produce diagnostics bundles for support without exposing secrets |

### Non-Negotiable Rule

OrvixEM must never fork itself into an unmaintainable custom mail protocol server unless there is a formal commercial reason. The default strategy is:

**Stalwart Core Engine + Orvix Enterprise Layer + Licensing + Automation + Migration + Safe Updates.**

---

### Frontend (Webmail + Admin)
| Component | Technology | Why |
|-----------|-----------|-----|
| Framework | **React 19** | Modern, fast |
| Build | **Vite 6** | Instant HMR, fast builds |
| UI Components | **Radix UI** | Accessible, unstyled primitives |
| Styling | **Tailwind CSS v4** | Utility-first, no runtime |
| State | **Zustand** | Lightweight, no boilerplate |
| Data Fetching | **TanStack Query v5** | Smart caching |
| Editor (email compose) | **TipTap** | Rich text, extensible |
| Calendar | **FullCalendar** | Feature-complete |
| Icons | **Lucide** | Clean, consistent |
| Animations | **Motion** | Smooth micro-interactions |
| Charts (admin) | **Recharts** | Clean data visualization |
| Virtualization | **TanStack Virtual** | Handle 100k+ email lists |
| i18n | **i18next** | Multi-language from day one |

### Frontend is **embedded** into the Go binary via `embed.FS`

### Security Stack
| Layer | Technology |
|-------|-----------|
| Auth | JWT (short-lived) + Refresh tokens (rotated) |
| 2FA | TOTP (RFC 6238) + Backup codes |
| Password | Argon2id hashing |
| Rate Limiting | Token bucket per IP + per account |
| CSRF | Double-submit cookie pattern |
| Headers | Strict CSP, HSTS, X-Frame-Options |
| Encryption at rest | AES-256-GCM for sensitive fields |
| TLS | Auto via Let's Encrypt (ACME) |
| Session | Redis-backed, short TTL |
| Audit Log | Immutable append-only log |

### DNS Automation
| Provider | Method |
|----------|--------|
| Cloudflare | API v4 |
| Route53 | AWS SDK |
| Generic | Manual wizard with copy-paste |

Auto-creates: MX, SPF, DKIM, DMARC, rDNS instructions

### Deployment
| Method | Details |
|--------|---------|
| OrvixEM package | `./orvix start` |
| Installer script | `curl -sSL https://get.orvix.email | bash` |
| Systemd service | Auto-created on install |
| Stalwart service | Installed/configured/managed by OrvixEM deployment flow |
| Auto-update | Pull from `updates.orvix.email`, verify, snapshot, apply, health-check and rollback |
| License activation | `license.orvix.email` online activation with offline grace support |

---

## 🎨 UI/UX Design System

### Design Philosophy
- **Refined Dark-first** with optional light mode
- **Density control** — comfortable / compact / spacious
- **Keyboard-first** — every action has a shortcut
- **Zero loading spinners** — optimistic UI everywhere
- **Accessible** — WCAG 2.1 AA minimum

### Color System
```css
/* Dark Theme (default) */
--bg-base:        #0C0E12;   /* deepest background */
--bg-surface:     #13161C;   /* cards, panels */
--bg-elevated:    #1A1E26;   /* dropdowns, modals */
--bg-subtle:      #222736;   /* hover states */
--border:         #2A2F3E;   /* borders */
--text-primary:   #E8EAF0;   /* main text */
--text-secondary: #8B92A8;   /* secondary text */
--text-muted:     #555D73;   /* placeholders */
--accent:         #4F7CFF;   /* primary actions */
--accent-hover:   #6B93FF;
--success:        #34D399;
--warning:        #FBBF24;
--danger:         #F87171;
--info:           #60A5FA;
```

### Typography
```
Display / Headings : Geist (clean, modern, technical)
Body / UI          : Geist Mono for data, Geist for prose
Code / Monospace   : JetBrains Mono
```

### Spacing Scale
```
4px base unit → 4, 8, 12, 16, 24, 32, 48, 64, 96
```

### Component Principles
- All inputs have visible focus rings (3px accent color)
- All destructive actions require confirmation
- All async actions show inline feedback, never page reload
- All tables support keyboard navigation
- All modals trap focus correctly
- All errors explain what went wrong AND how to fix it

---

## 📱 Webmail UI — Full Feature Spec

### Layout
```
┌──────────┬────────────────────┬──────────────────┐
│ Sidebar  │   Email List       │  Reading Pane    │
│ 240px    │   380px            │  flex-1          │
│          │                    │                  │
│ Folders  │ Virtualized list   │ Full email view  │
│ Labels   │ 100k+ emails fast  │ Reply / Forward  │
│ Accounts │ Smart preview      │ Attachments      │
│ Search   │ Multi-select       │ Actions          │
└──────────┴────────────────────┴──────────────────┘
```

### Email List Item
- Sender avatar (auto-generated from initials + color)
- Sender name + subject + preview
- Timestamp (relative: "2 min ago" → absolute on hover)
- Read/unread indicator
- Star / Flag
- Attachment indicator
- Labels/tags

### Reading Pane
- Full HTML email rendering in sandboxed iframe
- External image blocking with "Load images" prompt
- Reply / Reply All / Forward inline
- Print view
- Raw source viewer
- Show full headers toggle
- Block sender / Report spam
- Move to folder (drag or menu)
- Keyboard: R=reply, F=forward, E=archive, #=delete, J/K=navigate

### Compose Window
- Full TipTap rich text editor
- Inline image paste
- Attachment drag & drop (with progress)
- CC / BCC toggle
- Delivery receipts
- Read receipts
- Priority flag
- Scheduled send (date/time picker)
- Save draft (auto every 30 seconds)
- Templates
- Signatures (per-account)
- Undo send (30 second window)

### Search
- Instant full-text search
- Advanced filters: from / to / subject / date range / has attachment / folder
- Saved searches
- Search history

### Folders & Labels
- System folders: Inbox / Sent / Drafts / Spam / Trash / Archive
- Custom folders (nested, drag to reorder)
- Color labels
- Smart folders (saved searches as folders)
- Folder rules (auto-sort incoming)

### Calendar (integrated)
- Month / Week / Day / Agenda views
- Create events from email
- Invite attendees (sends email)
- Recurring events
- Resource booking
- Shared calendars
- CalDAV sync for mobile

### Contacts
- Auto-complete from sent history
- Contact groups / distribution lists
- CardDAV sync for mobile
- Import / Export (vCard)

### Tasks
- Create tasks from emails
- Due dates + reminders
- Subtasks
- Linked to calendar

### Settings (per user)
- Account info + password change
- 2FA setup (QR code + backup codes)
- Signatures (rich text, per-identity)
- Vacation auto-reply
- Forwarding rules
- Filters / sorting rules
- Notifications (browser push)
- Theme (dark / light / system)
- Density (comfortable / compact)
- Keyboard shortcuts reference
- Connected devices (active sessions)
- Storage usage visualization

---

## 🛡️ Admin Console — Full Feature Spec

### Dashboard
- Real-time stats: emails/min, queue depth, active connections
- Delivery rate, bounce rate, spam rate charts (last 24h / 7d / 30d)
- Top senders, top recipients
- Server health (CPU, RAM, disk, network)
- Alert feed

### Domain Management
- Add / remove domains
- DNS status checker (SPF ✅ / DKIM ✅ / DMARC ✅ / MX ✅)
- One-click DNS setup wizard
- DKIM key rotation
- Per-domain settings
- Domain aliases

### User Management
- Create / edit / delete users
- Quota management
- Force password reset
- Impersonate user (for support)
- Export users (CSV)
- Bulk import (CSV)
- Role assignment
- Active sessions viewer

### Mail Queue
- Live queue viewer
- Filter by status: queued / deferred / failed
- Force retry
- Delete from queue
- Inspect raw message

### Logs
- SMTP logs (searchable)
- IMAP logs
- Auth logs
- Admin audit log
- Export logs

### Anti-spam
- Spam score threshold config
- Whitelist / Blacklist (IP, domain, email)
- Per-domain spam settings
- Quarantine management

### Email Routing
- Rules engine (if/then)
- Forward all domain mail
- Catch-all address
- Mail relay configuration

### Backup & Restore
- Schedule backups (daily / weekly)
- Store locally or S3-compatible
- One-click restore
- Backup encryption

### License Management
- Current license info (tier, expiry, limits)
- Usage vs limits (mailboxes, domains)
- License activation / deactivation
- Offline activation support

### Auto-Update System
- Current version + changelog
- Check for updates button
- One-click update (hot-swap binary, zero downtime)
- Rollback to previous version
- Update channel (stable / beta)

### Reseller Panel (ISP+ tier)
- Create sub-accounts (resellers)
- Set limits per reseller
- White-label config (logo, colors, domain)
- Usage reports per reseller

### System Settings
- SMTP relay configuration
- TLS certificate management
- Backup MX config
- Rate limiting rules
- IP reputation settings
- Notification emails (alerts to admin)
- Maintenance mode toggle

---

## 🔒 Security Architecture

### Authentication Flow
```
User Login
    ↓
Rate limit check (5 attempts / 15 min per IP)
    ↓
Argon2id password verify
    ↓
If 2FA enabled → TOTP verify
    ↓
Issue: Access Token (JWT, 15 min) + Refresh Token (30 days, rotated)
    ↓
Refresh token stored: HttpOnly + Secure + SameSite=Strict cookie
    ↓
Access token stored: memory only (never localStorage)
```

### Email Security
- Mandatory TLS for all connections (opportunistic + forced modes)
- DKIM signing on all outbound mail
- SPF hard fail on inbound
- DMARC policy enforcement
- ARC (Authenticated Received Chain) support
- DANE (TLSA DNS records) support
- MTA-STS policy support

### API Security
- All endpoints require valid JWT
- Role-based access control (RBAC)
- Per-endpoint rate limiting
- Request signing for webhooks (HMAC-SHA256)
- API key system (hashed, never stored plain)
- IP allowlist for admin API (optional)

### License Security
- License key = signed JWT (RS256) from license server
- Offline validation (public key embedded in binary)
- Hardware fingerprint binding (optional)
- Tamper detection
- Grace period (7 days) if license server unreachable

### Input Security
- All user input sanitized via bluemonday (HTML)
- Email HTML rendered in sandboxed iframe (CSP: sandbox)
- File uploads validated (magic bytes, not just extension)
- Max attachment size enforced
- Path traversal prevention on all file operations
- SQL injection: parameterized queries only (GORM)
- XSS: React's default escaping + strict CSP headers

### Infrastructure Security
```
Security Headers (all responses):
  Strict-Transport-Security: max-age=31536000; includeSubDomains
  Content-Security-Policy: [strict policy]
  X-Frame-Options: DENY
  X-Content-Type-Options: nosniff
  Referrer-Policy: no-referrer
  Permissions-Policy: camera=(), microphone=(), geolocation=()
```

---

## 🔥 Mail Firewall — Architecture

### Processing Pipeline
```
Inbound Connection
        ↓
Layer 1: Connection Filter
  → IP reputation check (AbuseIPDB)
  → Geo-block check
  → Rate limit per IP
  → Known blacklist check
        ↓
Layer 2: Protocol Filter
  → SMTP commands valid?
  → EHLO hostname legit?
  → AUTH brute force check
        ↓
Layer 3: Authentication Filter
  → SPF hard fail → block
  → DKIM invalid → quarantine
  → DMARC policy enforce
        ↓
Layer 4: Content Filter
  → Spam score (custom engine)
  → Attachment type check
  → URL reputation (real-time)
  → Phishing pattern detection
  → Spoofed display name check
        ↓
Layer 5: Behavioral Filter
  → Sending rate normal?
  → Recipient pattern normal?
  → Guardian AI final verdict
        ↓
PASS → Deliver | QUARANTINE → Hold | BLOCK → Drop + Log
```

### Firewall Rules Engine
```go
// Rules defined in Admin Panel — no code needed
Rule: "Block countries"
  IF connection.country IN ["XX", "YY"]
  THEN block

Rule: "Rate limit per sender"
  IF sender.messages_per_hour > 500
  THEN throttle(rate=50/hour)

Rule: "Block dangerous attachments"
  IF attachment.extension IN [".exe",".bat",".ps1",".vbs"]
  THEN block + alert_admin

Rule: "Phishing domain detection"
  IF sender.domain.age_days < 7
  AND email.has_links = true
  THEN quarantine + guardian_review
```

### Firewall Admin Panel Features
- Real-time block log with reasons
- IP whitelist / blacklist management
- Custom rule builder (no-code UI)
- Geo-block map (click countries to block)
- Attack timeline visualization
- Auto-block threshold config

---

## 🔄 Auto-Heal System — Architecture

### Health Checks (every 60 seconds)
```go
checks := []HealthCheck{
    {Name: "smtp_port",       Fix: RestartSMTP},
    {Name: "imap_port",       Fix: RestartIMAP},
    {Name: "queue_depth",     Fix: ScaleWorkers},
    {Name: "disk_usage",      Fix: PurgeOldTrash},
    {Name: "memory_usage",    Fix: GracefulRestart},
    {Name: "db_connection",   Fix: ReconnectDB},
    {Name: "redis_connection",Fix: ReconnectRedis},
    {Name: "ssl_expiry",      Fix: RenewCert},
    {Name: "dkim_rotation",   Fix: RotateDKIM},
    {Name: "blacklist_status",Fix: AlertAdmin},
    {Name: "spam_rate",       Fix: ThrottleSending},
    {Name: "bounce_rate",     Fix: PauseHighBounce},
}
```

### Auto-Heal Decision Tree
```
Issue detected
      ↓
Severity: LOW → auto-fix silently + log
Severity: MEDIUM → auto-fix + notify admin
Severity: HIGH → attempt fix + alert + escalate
Severity: CRITICAL → alert + pause service + wait for admin
```

### Heal History Log (in Admin Panel)
- Every heal action logged with before/after state
- Success/failure rate per check type
- Admin can disable specific auto-heals
- Custom thresholds per check

---

## 🤖 Guardian Agent — Architecture

### How It Works
```
Every email passes through Guardian
        ↓
Guardian builds feature vector:
  - IP reputation score
  - Domain age
  - SPF/DKIM/DMARC results
  - Content signals (links, attachments)
  - Behavioral signals (volume, timing)
  - Historical patterns for this sender
        ↓
AI model (DeepSeek API) analyzes vector
        ↓
Returns: threat_score + verdict + explanation
        ↓
Action taken automatically
        ↓
Admin sees full explanation in dashboard
```

### Guardian API (Enterprise tier)
```http
POST /api/v1/guardian/analyze
Authorization: Bearer {api_key}
Content-Type: application/json

{
  "email_id": "msg_abc123",
  "raw_headers": "Received: from...",
  "sender_ip": "185.220.101.1",
  "sender_domain": "suspicious.xyz",
  "recipient": "ceo@company.com",
  "subject": "Urgent wire transfer needed",
  "has_attachments": true,
  "attachment_types": [".pdf"]
}

Response:
{
  "threat_score": 94,
  "verdict": "phishing",
  "confidence": 0.97,
  "category": "CEO_fraud",
  "reasons": [
    "Domain registered 3 days ago",
    "Sender IP on 12 blacklists",
    "Subject matches CEO fraud pattern",
    "Urgency language detected"
  ],
  "action": "block",
  "explanation": "This email exhibits classic CEO fraud / BEC attack patterns. The sending domain was registered 3 days ago and the IP has been flagged for phishing campaigns. Recommend blocking and alerting the recipient.",
  "similar_attacks": 847,
  "processed_ms": 43
}
```

### Guardian Dashboard
- Threat feed (real-time blocked threats)
- Threat categories breakdown chart
- Top attacking IPs / domains
- Weekly intelligence report (PDF export)
- False positive reporting
- Custom AI instructions per domain

---

## 🧠 Smart Compose AI — Architecture

### Features
```
While composing email:
  ✅ Autocomplete sentences (Tab to accept)
  ✅ "Write full reply" from selected text
  ✅ Tone adjustment (formal / friendly / assertive)
  ✅ Translate email to any language
  ✅ Summarize long email thread
  ✅ Subject line suggestions
  ✅ Grammar & clarity check
  ✅ Response templates from past emails
```

### Implementation
```go
// Streamed response — no waiting
POST /api/v1/ai/compose
{
  "action": "complete" | "reply" | "translate" | "summarize",
  "context": "email thread text...",
  "instruction": "Write a polite decline",
  "language": "en",
  "tone": "professional"
}
// Returns: Server-Sent Events stream
```

### Privacy
- Email content never stored by AI provider
- Option: use local Ollama model (Enterprise)
- Per-domain enable/disable by admin
- User can opt-out individually

---

## ⚡ Instant Deployment API — Architecture

### What It Does
```
Data Center calls API once
        ↓
OrvixEM provisions everything in < 30 seconds:
  ✅ Creates domain record
  ✅ Configures DNS (if Cloudflare/Route53)
  ✅ Generates DKIM keys
  ✅ Creates mailboxes
  ✅ Sets quotas
  ✅ Configures firewall rules
  ✅ Issues SSL cert
  ✅ Sends welcome emails
  ✅ Returns credentials
```

### API Reference
```http
POST /api/v1/provision/domain
{
  "domain": "newclient.com",
  "plan": "business",
  "mailboxes": [
    {"username": "john", "quota_gb": 10},
    {"username": "sales", "quota_gb": 25}
  ],
  "dns_provider": "cloudflare",
  "dns_api_key": "...",
  "white_label": {
    "brand_name": "AcmeMail",
    "logo_url": "https://..."
  }
}

Response:
{
  "domain_id": "dom_xyz",
  "status": "active",
  "provisioned_in_ms": 18400,
  "dns_records": [...],
  "webmail_url": "https://mail.newclient.com",
  "admin_url": "https://admin.newclient.com",
  "credentials": [...]
}
```

---

## 🔁 Smart Migration Tool — Architecture

### Supported Sources
| Source | Method |
|--------|--------|
| Axigen | IMAP sync + config export |
| Zimbra | IMAP sync + ZCS API |
| Exchange | EWS API + IMAP |
| cPanel/WHM | IMAP sync |
| Google Workspace | Gmail API + IMAP |
| Any IMAP server | Standard IMAP sync |

### Migration Process
```
Admin starts Migration Wizard
        ↓
Enter source server credentials
        ↓
OrvixEM scans: domains, mailboxes, folders, size
        ↓
Shows migration plan + estimated time
        ↓
Admin confirms → migration starts background
        ↓
Real-time progress per mailbox
        ↓
Emails → Contacts → Calendar → Rules → Settings
        ↓
Validation: counts match source
        ↓
DNS cutover wizard (when ready)
        ↓
Old server stays as backup for 7 days
```

### Key Feature: Zero Downtime Migration
```
Phase 1: Copy historical emails (background)
Phase 2: Sync new emails in real-time (dual delivery)
Phase 3: DNS cutover (< 5 min downtime)
Phase 4: Final sync + validation
```

---

## 📊 Email Intelligence Dashboard

### Insights (AI-powered)
```
Per Domain:
  📈 Delivery rate trend
  📉 Bounce rate alerts
  🕐 Best send times for your recipients
  🌍 Geographic distribution of senders
  ⚠️  "Domain X has unusually high outbound volume"
  🔍 "User Y sent sensitive keywords externally"

Per User:
  📊 Email volume over time
  ⏱️  Average response time
  📂 Most contacted domains
  🚨 Anomaly alerts
```

---

## 🔐 Zero-Knowledge Encryption (Enterprise)

### How It Works
```
User sets master password (never sent to server)
        ↓
Client derives encryption key (PBKDF2)
        ↓
All email content encrypted in browser
        ↓
Only ciphertext stored on server
        ↓
Even admin cannot read emails
        ↓
Key never leaves user's device
```

---

## 🤝 Collaboration Layer (Enterprise)

### Shared Inbox Features
```
team@company.com → Shared inbox
        ↓
Any team member can see + reply
        ↓
Assign email to specific person
        ↓
Internal notes (not sent to customer)
        ↓
Collision detection: "Sarah is replying..."
        ↓
Status: Open / Pending / Resolved
        ↓
Shared email templates
        ↓
SLA timer per email
```

---

## 📁 Project Structure

```
orvix/
├── cmd/
│   └── orvix/
│       └── main.go
├── internal/
│   ├── license/                 # License validation, signed entitlements, tier limits, feature flags
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

### Project Structure Rule

All low-level mail protocol behavior belongs to the Stalwart Core integration layer. OrvixEM source code must focus on enterprise control, licensing, automation, dashboards, support tooling, safe updates, migration and commercial workflows.

---

## 🗄️ Database Schema (Core Tables)

```sql
-- License
licenses (id, key_hash, tier, issued_at, expires_at, max_domains, max_mailboxes, hardware_id, metadata)

-- Domains
domains (id, name, status, dkim_selector, dkim_private_key, spf_record, dmarc_policy, catch_all, created_at)

-- Users / Mailboxes
users (id, domain_id, username, email, password_hash, quota_mb, used_mb, role, is_active, created_at)
user_settings (user_id, theme, density, signature, vacation_msg, vacation_active, language, timezone)

-- Mail Storage
messages (id, mailbox_id, uid, flags, size, subject, from_addr, to_addrs, date, body_text, body_html, raw_path)
attachments (id, message_id, filename, content_type, size, storage_path)
folders (id, mailbox_id, name, parent_id, type, unread_count, total_count)

-- Queue
mail_queue (id, from_addr, to_addr, domain, status, attempts, next_retry, last_error, created_at, raw_path)

-- Security
sessions (id, user_id, token_hash, ip, user_agent, created_at, expires_at, last_seen)
audit_logs (id, user_id, action, resource, ip, details, created_at)
api_keys (id, user_id, key_hash, name, permissions, last_used, expires_at)

-- Calendar
calendars (id, user_id, name, color, is_shared)
events (id, calendar_id, title, description, start_at, end_at, is_recurring, rrule, location)

-- Contacts
contacts (id, user_id, name, email, phone, company, notes, vcard)
contact_groups (id, user_id, name)
```

---

## 🔄 Auto-Update System

```
Admin clicks "Update"
        ↓
OrvixEM checks selected update channel from updates.orvix.email
        ↓
Downloads signed release manifest
        ↓
Verifies manifest signature
        ↓
Downloads OrvixEM package and compatible Stalwart metadata
        ↓
Verifies SHA256 checksum
        ↓
Verifies GPG/signing signature
        ↓
Creates snapshot:
  - OrvixEM binary
  - OrvixEM config
  - Database backup/checkpoint
  - Stalwart config
  - Update manifest
        ↓
Runs prechecks:
  - Disk space
  - Database connectivity
  - License validity
  - Stalwart version compatibility
  - Migration safety
  - Port availability
        ↓
Writes new package to temporary path
        ↓
Applies additive-only migrations
        ↓
Restarts/reloads services gracefully
        ↓
Runs health checks:
  - API alive
  - Admin alive
  - Webmail alive
  - Stalwart SMTP alive
  - Stalwart IMAP alive
  - Stalwart JMAP alive
  - Queue healthy
  - DB healthy
        ↓
Health check passes → update complete
        ↓
Health check fails → auto-rollback to previous snapshot
```

### Safe Update Pipeline

The update pipeline must never trust a downloaded package until all integrity checks pass.

Required checks:
- Signed release manifest
- SHA256 checksum validation
- Package signature validation
- License entitlement validation
- Stalwart compatibility validation
- Additive-only migration validation
- Snapshot creation before mutation
- Post-update health checks
- Automatic rollback on failure

### Versioning Strategy

OrvixEM uses semantic versioning:

```
MAJOR.MINOR.PATCH
```

Examples:

| Version | Meaning |
|---------|---------|
| `1.0.0` | Initial production release |
| `1.1.0` | Backward-compatible feature release |
| `1.1.1` | Bug fix or security patch |
| `2.0.0` | Breaking platform change requiring explicit upgrade planning |

Rules:
- `PATCH` releases may include bug fixes, security fixes, safe compatibility fixes and documentation updates.
- `MINOR` releases may include new features, new feature flags, new APIs and additive schema changes.
- `MAJOR` releases may include breaking changes, but must include an explicit migration plan, rollback plan and compatibility notice.

### Update Channels

| Channel | Purpose | Customers |
|---------|---------|-----------|
| Stable | Production-safe releases | Normal production customers |
| Beta | Feature validation before stable | Selected customers and internal QA |
| Early Access | Partner/data center preview | Strategic partners, resellers, data centers |
| Nightly | Automated internal builds | Internal testing only |

Channel rules:
- Stable must never receive untested database migrations.
- Beta can include new feature flags disabled by default.
- Early Access can expose partner-only workflows.
- Nightly must not be used by production customers.

### Migration Rules — Additive Only

Default policy:

```
Additive only.
```

Allowed:

```sql
ADD TABLE
ADD COLUMN
ADD INDEX
CREATE VIEW
CREATE TRIGGER
```

Forbidden in normal updates:

```sql
DROP TABLE
DROP COLUMN
RENAME COLUMN
RENAME TABLE
DESTRUCTIVE TYPE CHANGE
```

Destructive migrations are only allowed in a major release with:
- Written migration plan
- Preflight compatibility check
- Customer-visible warning
- Backup requirement
- Rollback strategy
- Staged migration path

### Changelog Format

Every release must ship a machine-readable and human-readable changelog.

```markdown
## Version 1.2.0

### Added
- New Migration Wizard source adapter.

### Improved
- Faster IMAP sync progress reporting.

### Fixed
- DKIM validation edge case.

### Security
- Fixed privilege escalation issue in reseller role checks.

### Compatibility
- Compatible with Stalwart version range X.Y.Z to X.Y.Z.

### Migration
- Additive migration only; no destructive changes.

### Rollback Notes
- Automatic rollback supported from this release to the previous stable release.
```

### Feature Flags System

Feature flags are controlled by license entitlements, update channel and admin configuration.

Example:

```yaml
features:
  webmail: true
  reseller_panel: true
  guardian_ai: true
  guardian_api: false
  clustering: false
  active_sync: true
  migration_suite: true
  smart_compose: true
  legal_hold: false
  zero_knowledge_encryption: false
  collaboration_layer: false
  update_channel_beta: false
```

Feature flag sources:
- License tier
- Signed entitlement payload
- Admin configuration
- Update channel
- Emergency remote disable list

Feature flags enable:
- License unlocks
- Beta rollouts
- A/B testing
- Emergency disable
- Reseller-specific capabilities
- Datacenter-specific capabilities
- Safe feature delivery without shipping separate binaries

### Update Server Responsibilities

`updates.orvix.email` must provide:
- Release manifests
- Version metadata
- Changelog files
- Package checksums
- Package signatures
- Stalwart compatibility matrix
- Channel definitions
- Rollback metadata
- Security bulletin metadata

---

## 🚀 Build & Release

```bash
# Build OrvixEM single binary (Linux x64)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build   -ldflags="-s -w -X main.Version=1.0.0 -X main.Product=OrvixEM"   -o orvix ./cmd/orvix

# Frontend is embedded via go:embed
//go:embed web/dist
var webFS embed.FS
```

### Installer Script
```bash
curl -sSL https://get.orvix.email | bash
# → Detects OS/arch
# → Downloads correct OrvixEM package
# → Installs or validates Stalwart Core Engine compatibility
# → Creates systemd services
# → Generates initial orvix.yaml config
# → Creates admin account
# → Activates license
# → Opens setup wizard
```

### Runtime Commands

```bash
orvix start
orvix status
orvix doctor
orvix license activate
orvix update check
orvix update apply
orvix update rollback
orvix stalwart status
orvix stalwart validate-config
```

### Release Artifacts

Each release must include:
- OrvixEM binary/package
- SHA256SUMS
- Signature file
- Release manifest
- Changelog
- Migration manifest
- Stalwart compatibility matrix
- Rollback metadata
- SBOM where possible

### Release Rule

No release is allowed to enter Stable unless:
- Tests pass
- Migrations are additive-only
- Package signatures validate
- Update rollback test passes
- Stalwart compatibility test passes
- License enforcement test passes
- Admin/Webmail smoke tests pass
- Installer test passes on a clean server

---

## 📋 Build Order (for OpenCode)

### Phase 1 — Foundation (Week 1-2)
- [ ] Project structure setup exactly as defined in the OrvixEM Project Structure section
- [ ] Config system using `orvix.yaml`
- [ ] Database layer (GORM + SQLite + PostgreSQL)
- [ ] Additive-only migration system with safety checks
- [ ] Logging (Zap — structured, leveled)
- [ ] Metrics (Prometheus endpoints)
- [ ] License engine (JWT RS256 validate + feature flags per tier)
- [ ] Feature flags engine (license + admin + channel controlled)
- [ ] Versioning metadata (`MAJOR.MINOR.PATCH`, build commit, channel)
- [ ] Code watermarking (embedded copyright strings + canary tokens)

### Phase 2 — Stalwart Core Integration (Week 3-5)
- [ ] Stalwart install/detection flow
- [ ] Stalwart config generator
- [ ] Stalwart config validator
- [ ] Stalwart service lifecycle manager (start/stop/restart/reload/status)
- [ ] Domain provisioning through OrvixEM layer
- [ ] Mailbox provisioning through OrvixEM layer
- [ ] Alias/routing provisioning through OrvixEM layer
- [ ] Queue visibility and operational controls
- [ ] Stalwart log/event ingestion
- [ ] Stalwart health checks (SMTP/IMAP/POP3/JMAP)
- [ ] DKIM/SPF/DMARC policy management UI/API around Stalwart
- [ ] TLS/certificate management integration
- [ ] Compatibility matrix for supported Stalwart versions

### Phase 3 — Security Layer (Week 6-7)
- [ ] Mail Firewall (all 5 layers: connection/protocol/auth/content/behavioral)
- [ ] Firewall rules engine (no-code rule builder)
- [ ] IP reputation integration (AbuseIPDB)
- [ ] Geo-blocking engine
- [ ] Rate limiting (token bucket per IP + account)
- [ ] Brute force protection
- [ ] Guardian Agent (AI threat analysis via DeepSeek API)
- [ ] Guardian REST API (Enterprise tier)
- [ ] Auto-Heal system (all health checks + fixers)
- [ ] Heal history log
- [ ] Stalwart configuration drift detection
- [ ] Safe diagnostic bundle exporter without secrets

### Phase 4 — API Layer (Week 8-9)
- [ ] Auth system (JWT + refresh tokens + 2FA TOTP)
- [ ] User management API
- [ ] Domain management API
- [ ] Mailbox API
- [ ] Admin API (full)
- [ ] Stalwart operations API wrapper
- [ ] **⭐ Instant Deployment API** (provision domain in < 30 seconds)
- [ ] Webhook system (HMAC-SHA256 signed)
- [ ] API key management
- [ ] License-gated endpoint enforcement

### Phase 5 — Frontend (Week 10-12)
- [ ] Design system + component library (Radix UI + Tailwind v4)
- [ ] **⭐ Webmail UI** (full Outlook-level: compose, read, search, folders)
- [ ] **⭐ Smart Compose AI** (autocomplete, reply, translate, summarize — streamed)
- [ ] Admin console (all panels)
- [ ] License management UI
- [ ] DNS wizard UI
- [ ] Stalwart status/config UI
- [ ] Firewall rules UI (no-code rule builder)
- [ ] Auto-Heal dashboard
- [ ] Guardian Agent dashboard
- [ ] Email Intelligence dashboard
- [ ] Update manager UI (version, channel, changelog, rollback)
- [ ] Feature flags UI (safe view, license-aware)
- [ ] PWA manifest + service worker (installable on mobile)

### Phase 6 — Advanced Features (Week 13-15)
- [ ] Calendar + Contacts + Tasks (CalDAV + CardDAV)
- [ ] ActiveSync (ISP+ tier)
- [ ] **⭐ Smart Migration Tool** (Axigen, Zimbra, Exchange, cPanel, Google)
- [ ] Zero-downtime migration engine
- [ ] Anti-spam policy engine and quarantine UI
- [ ] Email Archiving + Legal Hold
- [ ] Compliance Center (GDPR / HIPAA / SOX)
- [ ] Zero-Knowledge Encryption (Enterprise)
- [ ] Collaboration Layer / Shared Inbox (Enterprise)
- [ ] Multi-Cloud Storage (S3 / GCS / Azure)
- [ ] Email Intelligence AI insights
- [ ] Backup & Restore (local + S3)
- [ ] LDAP / Active Directory sync (Enterprise)
- [ ] SSO — SAML 2.0 + OAuth2 (Enterprise)

### Phase 7 — Hardening & Launch (Week 16-18)
- [ ] Full security audit (all attack vectors)
- [ ] Penetration testing (auth bypass, injection, tenant isolation)
- [ ] Load testing (10k concurrent connections)
- [ ] Deliverability testing (Gmail, Outlook, Yahoo)
- [ ] Auto-update system (safe pipeline + rollback)
- [ ] Update channels (Stable/Beta/Early Access/Nightly)
- [ ] One-line installer script
- [ ] Full API documentation
- [ ] Admin documentation
- [ ] Marketing website (Next.js — orvix.email)
- [ ] License purchase flow + customer portal
- [ ] Update server setup (`updates.orvix.email`)
- [ ] Status page setup (`status.orvix.email`)
- [ ] Stalwart compatibility certification for launch release

---

## 🎯 Current Task

**START HERE:**

```
Phase 1 — Task 1:
Set up OrvixEM project structure exactly as defined in the Project Structure section.
Initialize go.mod with module name "github.com/orvixemail/orvix".
Website: orvix.email
Binary name: orvix
Config file: orvix.yaml
Set up Fiber v3, GORM, Viper, Zap dependencies.
Create config system that reads from orvix.yaml.
Create database connection (SQLite default, PostgreSQL if configured).
Run initial additive-only migrations for core OrvixEM tables.
Create placeholder Stalwart integration package with config validation interfaces.
Create version metadata package with semantic versioning and update channel support.
Create feature flags package tied to license entitlements.
All code must be production-quality with proper error handling.
Do not build SMTP/IMAP/POP3/JMAP through Stalwart Core integration; those belong to Stalwart Core Engine.
```

---

## 📌 Key Decisions Log

| Decision | Choice | Reason |
|----------|--------|--------|
| Brand | Orvix Enterprise Mail (OrvixEM) | Global enterprise brand suitable for email infrastructure |
| Domain | orvix.email | Strong product-domain fit for an email platform |
| Core mail engine | Stalwart Core Engine | Avoid years of protocol risk; use proven SMTP/IMAP/POP3/JMAP foundation |
| Enterprise layer | OrvixEM | Licensing, admin, reseller, migration, automation, monitoring and commercial workflows |
| Language | Go | Single binary, performance, low memory |
| HTTP | Fiber v3 | Fast Go HTTP framework |
| DB small | SQLite | Zero dependency install |
| DB large | PostgreSQL | Scale and reliability |
| Frontend | React + Vite | Modern, fast, great DX |
| License | JWT RS256 | Offline validation, tamper-proof entitlements |
| Feature flags | License + channel + admin controlled | Safe rollout and entitlement enforcement |
| Auth tokens | Memory only | Never localStorage — XSS protection |
| Password hash | Argon2id | Best current standard |
| Mail storage | Stalwart-managed storage + OrvixEM metadata | Avoid corrupting core mail data while enabling enterprise dashboards |
| Search | Stalwart/Bleve/metadata hybrid where appropriate | Search without unnecessary external dependencies |
| Updates | Signed manifest + safe pipeline + rollback | Production-safe updates |
| Update channels | Stable / Beta / Early Access / Nightly | Controlled release flow |
| Migrations | Additive-only by default | Prevent destructive customer upgrades |
| CSS | Tailwind v4 | No runtime, fast builds |
| State | Zustand | Simple, no boilerplate |
| AI provider | DeepSeek API | Best price/performance for Guardian + Compose |
| Local AI option | Ollama | Enterprise offline AI option |
| Deployment | OrvixEM package + Stalwart compatibility management | Fast installation with proven mail core |
| Watermarking | Embedded + Canary tokens | Detect piracy automatically |

---

## 🏆 Competitive Advantages Summary

### Why customers choose OrvixEM over Axigen / Zimbra / MDaemon

| Feature | Axigen | Zimbra | OrvixEM |
|---------|--------|--------|---------|
| **Price** | $3,000–10,000/yr | $3,000+/yr | $500–2,500/yr |
| **Install time** | Minutes | Hours | **Seconds** |
| **Guardian AI Agent** | ❌ | ❌ | ✅ |
| **Smart Compose AI** | ❌ | ❌ | ✅ |
| **Auto-Heal System** | ❌ | Partial | ✅ |
| **Mail Firewall (multi-layer)** | Basic | Basic | ✅ |
| **Instant Deploy API** | ❌ | ❌ | ✅ |
| **Smart Migration Tool** | ❌ | Basic | ✅ |
| **Zero-Knowledge Encryption** | ❌ | ❌ | ✅ (Enterprise) |
| **PWA (mobile app)** | ❌ | ❌ | ✅ |
| **Email Intelligence** | ❌ | ❌ | ✅ |
| **Collaboration / Shared Inbox** | ❌ | ❌ | ✅ (Enterprise) |
| **Open Source friendly** | ❌ | Partial | ✅ |

---

## ⭐ The 3 Features That Win Deals

### 1. Smart Migration Tool
> Breaks the #1 barrier to switching — "it's too hard to migrate"
> Supports: Axigen, Zimbra, Exchange, cPanel, Google Workspace
> Zero-downtime migration with real-time progress
> **Talking point:** "Migrate your entire server in one afternoon, not one month"

### 2. Instant Deployment API
> Data Centers can provision a new client domain in < 30 seconds via API
> Full automation: DNS + SSL + mailboxes + firewall + welcome email
> **Talking point:** "Your ops team provisions 100 clients a day without touching a keyboard"

### 3. Smart Compose AI
> AI writing assistant built directly into the Webmail
> No external app needed — works offline with Ollama (Enterprise)
> **Talking point:** "The only self-hosted mail server with a built-in AI assistant"

---

*OrvixEM MVP.md — Single source of truth for OpenCode*
*Version: 3.0 | Stack: Go + React + PostgreSQL + Stalwart Core | Target: Global market*
*Domain: orvix.email | Updated for Stalwart Core + Orvix Enterprise Layer + Safe Updates + Feature Flags*
