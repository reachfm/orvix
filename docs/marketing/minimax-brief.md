# Orvix Website Implementation Brief for MiniMax

## Product Overview

**Orvix Enterprise Mail** is a managed multi-tenant email SaaS platform.
Customers sign up, create organizations, add domains, verify DNS,
create mailboxes, and use Webmail or any mail client.

No self-hosted installation. No server administration. No license files.

### Business Model
- SaaS subscription (Free / Starter / Business / Enterprise plans)
- Monthly or yearly billing
- Trial period available
- Managed infrastructure by Orvix team

### Target Customers
- Small to medium businesses (2-1000 mailboxes)
- Remote/distributed teams
- Companies migrating from Microsoft 365/Google Workspace/Zoho Mail

---

## Public Sitemap

- `/` — Home
- `/features` — Feature overview
- `/pricing` — Plans and pricing
- `/docs` — Documentation (links to docs.orvix.email)
- `/blog` — Company blog
- `/about` — About Orvix
- `/contact` — Contact/Support
- `/signup` — Create account
- `/login` — Redirect to app.orvix.email/login
- `/privacy` — Privacy policy
- `/terms` — Terms of service
- `/status` — Service status (status.orvix.email)
- `/docs/api` — API documentation

---

## Customer Journey

1. **Discovery** — Search/ads/referral → Orvix homepage
2. **Evaluation** — Feature page → Pricing page → Signup
3. **Account Creation** — Email verification → Organization setup
4. **Team Setup** — Invite team members → Assign roles
5. **Domain Setup** — Add domain → Verify DNS → Configure MX/SPF/DKIM/DMARC
6. **Productive Use** — Create mailboxes → Send/receive email → Use Webmail/clients
7. **Growth** — Add more domains → Upgrade plan → Add team

---

## Product Terminology

| Term | Definition |
|------|------------|
| Organization | Workspace for a company or team (formerly "tenant") |
| Domain | A domain name linked to the organization (e.g., example.com) |
| Mailbox | An email account on a domain |
| Alias | An alternative email address forwarding to a mailbox |
| Group | Shared email distribution list |
| Administrator | Organization user with full management rights |
| Operator | Support-level user with limited admin rights |
| Webmail | Browser-based email client (app.orvix.email/webmail) |
| Plan | Subscription tier (Free, Starter, Business, Enterprise) |
| Quota | Maximum resource allocation (mailboxes, domains, storage, sends) |

---

## Verified Feature Matrix

| Feature | Free | Starter | Business | Enterprise |
|---------|------|---------|----------|------------|
| Mailboxes | 5 | 25 | 100 | 1000 |
| Domains | 1 | 3 | 10 | 100 |
| Storage | 1 GB | 10 GB | 100 GB | 1 TB |
| DKIM | Yes | Yes | Yes | Yes |
| MTA-STS | No | Yes | Yes | Yes |
| API | No | Yes | Yes | Yes |
| Teams | No | Yes | Yes | Yes |
| Groups | No | No | Yes | Yes |
| Mail Forwarding | No | No | Yes | Yes |
| Backup | No | No | Yes | Yes |
| Audit Log | No | No | Yes | Yes |
| SSO | No | No | No | Yes |
| Priority Support | No | No | No | Yes |

---

## Screenshot Inventory

### Portal (app.orvix.email)

- Signup form
- Login form
- Dashboard
- Organization overview
- Members list
- Invitation modal
- Domain list
- Domain onboarding wizard (TXT record step)
- Domain onboarding wizard (DNS records step)
- Domain verification status
- Mailbox list
- Create mailbox form
- Alias list
- Group list
- Usage/Quota dashboard
- Plans comparison
- Subscription status
- API keys list
- Create API key modal
- Audit activity log
- Account settings
- Security settings

### Webmail (app.orvix.email/webmail)

- Inbox
- Message view
- Compose
- Drafts
- Sent
- Trash
- Settings
- Filters/Rules
- Vacation response
- Forwarding
- Signatures

---

## Demo Tenant Data

```json
{
  "organizations": [
    {"name": "Demo Corporation", "slug": "demo-corp", "domain": "demo.example.com"}
  ],
  "users": [
    {"email": "admin@demo-corp.test", "role": "admin", "password": "demo-admin-password-REPLACE"},
    {"email": "user@demo-corp.test", "role": "user", "password": "demo-user-password-REPLACE"}
  ],
  "mailboxes": [
    {"email": "john@demo.example.com", "name": "John Smith"},
    {"email": "jane@demo.example.com", "name": "Jane Doe"}
  ]
}
```

---

## Key CTA Paths

- Homepage → Signup
- Pricing → Signup (with plan selection)
- Features → Signup
- Navigation → Login
- Dashboard → Add Domain
- Dashboard → Create Mailbox
- Dashboard → Invite Team
- Dashboard → View Usage

---

## Documentation Map

- docs.orvix.email — All customer documentation
- docs.orvix.email/api — Public API reference
- docs.orvix.email/guides — Getting started guides
- status.orvix.email — Service status (separate site)

---

## Legal Pages (Required)

- Privacy Policy
- Terms of Service
- Data Processing Agreement
- Cookie Policy
- Acceptable Use Policy
- DMCA/Copyright

---

## Truthful Limitations (Disclose)

- No self-hosted option
- No POP3 support
- No calendar (in development)
- No video conferencing
- DNS changes propagate within 24-72 hours (outside Orvix control)
- Billing provider selection pending (currently: payment not activated)
- Maximum attachment size: 25 MB per message
- API rate limit: 100 requests per minute per IP

---

## Forbidden Claims (Do Not Make)

- Any specific customer count
- Any specific uptime percentage not verified
- Any specific throughput numbers
- "Unlimited" anything
- Comparison metrics with named competitors
- Claims about security certifications not held
- Claims about data center locations unless verified
- Claims about GDPR/HIPAA compliance unless legally verified
