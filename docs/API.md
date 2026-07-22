# OrvixEM API Documentation

## Base URL
All API endpoints are under `/api/v1/`.

## Authentication
- JWT Bearer token in `Authorization` header
- TOTP 2FA optional
- CSRF token via `X-CSRF-Token` header for frontend routes

## Public Endpoints

### GET /health
System health check.
Returns: `{ status, product, version, database, stalwart }`

### GET /version
Version information.
Returns: `{ product, version, commit, channel, build_date, go_version }`

### GET /api/v1/features
List all feature flags.
Returns: `{ features: [...] }`

### GET /api/v1/license/status
License status.
Returns: `{ active, tier, expires_at, max_domains, max_mailboxes, used_domains, used_mailboxes }`

## Auth Endpoints

### POST /api/v1/auth/login
Body: `{ email, password }`
Returns: `{ access_token, refresh_token, expires_in, user_id, email, role }`
If 2FA enabled: `{ totp_required: true, user_id }`

### POST /api/v1/auth/verify-totp
Body: `{ user_id, code }`
Returns: `{ access_token, refresh_token, ... }`

### POST /api/v1/auth/refresh
Body: `{ refresh_token }`
Returns: `{ access_token, refresh_token }`

## Admin Endpoints (Auth Required)

### GET/POST /api/v1/admin/tenants
Tenant management.

### GET/POST /api/v1/admin/domains
Domain management. Auto-generates DKIM/SPF/DMARC.

### GET/POST /api/v1/users
User management.

### GET /api/v1/admin/provisioning-jobs
List provisioning jobs.

### GET /api/v1/admin/audit-logs
List audit logs.

### GET/POST /api/v1/calendars
Calendar management.

### GET/POST /api/v1/contacts
Contact management.

### GET/POST /api/v1/firewall/rules
Firewall rules management.

### GET/POST /api/v1/webhooks
Webhook management.

### GET/POST /api/v1/api-keys
API key management.

### POST /api/v1/compose/send
Send email through mail queue.
Body: `{ to, subject, body }`

### GET /api/v1/mail-queue/stats
Mail queue statistics.

### GET/POST /api/v1/backups
Backup management. Creates tar.gz archives.

### POST /api/v1/migration/start
Start IMAP migration.
