# OrvixEM First VPS Deployment Guide

**Target:** Ubuntu 24.04 LTS
**Estimated Time:** 30-45 minutes
**Difficulty:** Intermediate

---

## 1. Minimum Hardware Requirements

| Component | Minimum | Recommended |
|---|---|---|
| CPU | 1 core @ 2.0 GHz | 2 cores @ 2.5 GHz |
| RAM | 512 MB | 2 GB |
| Disk | 10 GB SSD | 20 GB SSD |
| Network | 100 Mbps | 1 Gbps |
| OS | Ubuntu 24.04 LTS | Ubuntu 24.04 LTS |

## 2. Recommended Hardware Requirements

| Component | Production (w/ Stalwart) |
|---|---|
| CPU | 4 cores |
| RAM | 4 GB |
| Disk | 40 GB SSD |
| Network | 1 Gbps |

## 3. Required Open Ports

| Port | Service | Purpose |
|---|---|---|
| 22 | SSH | Server administration |
| 80 | HTTP | Let's Encrypt ACME challenge |
| 443 | HTTPS | Production web access |
| 8080 | OrvixEM | Internal API (behind reverse proxy) |
| 25 | Stalwart | SMTP inbound |
| 587 | Stalwart | SMTP submission |
| 465 | Stalwart | SMTP over TLS |
| 143 | Stalwart | IMAP |
| 993 | Stalwart | IMAP over TLS |
| 5432 | PostgreSQL | Database (internal only) |

## 4. Firewall Configuration

```bash
# Install UFW
sudo apt update
sudo apt install -y ufw

# Default deny
sudo ufw default deny incoming
sudo ufw default allow outgoing

# Allow essential services
sudo ufw allow 22/tcp comment 'SSH'
sudo ufw allow 80/tcp comment 'HTTP'
sudo ufw allow 443/tcp comment 'HTTPS'

# Allow mail ports (if using Stalwart directly, not behind proxy)
sudo ufw allow 25/tcp comment 'SMTP'
sudo ufw allow 587/tcp comment 'SMTP Submission'
sudo ufw allow 465/tcp comment 'SMTP over TLS'
sudo ufw allow 143/tcp comment 'IMAP'
sudo ufw allow 993/tcp comment 'IMAP over TLS'
sudo ufw allow 110/tcp comment 'POP3'
sudo ufw allow 995/tcp comment 'POP3 over TLS'

# Enable firewall
sudo ufw enable
sudo ufw status verbose
```

## 5. PostgreSQL Setup

```bash
# Install PostgreSQL
sudo apt install -y postgresql postgresql-client

# Start and enable
sudo systemctl start postgresql
sudo systemctl enable postgresql

# Create database user and database
sudo -u postgres psql -c "CREATE USER orvix WITH PASSWORD '$(openssl rand -base64 32)';"
sudo -u postgres psql -c "CREATE DATABASE orvix OWNER orvix;"
sudo -u postgres psql -c "GRANT ALL PRIVILEGES ON DATABASE orvix TO orvix;"

# Save the generated password
echo "PostgreSQL password: [saved in your password manager]"

# Configure pg_hba.conf for local password auth
# Edit /etc/postgresql/16/main/pg_hba.conf:
# local   all   orvix   md5
# host    all   orvix   127.0.0.1/32   md5

sudo systemctl restart postgresql
```

## 6. Stalwart Setup (Optional — Required for Email)

```bash
# Install Stalwart Mail Server
curl -sSL https://stalw.art/install | sudo bash

# Verify installation
/usr/local/bin/stalwart --version

# Start Stalwart
sudo systemctl start stalwart-server
sudo systemctl enable stalwart-server

# Verify OrvixEM detects Stalwart
/usr/local/bin/orvix stalwart status
```

## 7. TLS Setup (Let's Encrypt)

```bash
# Install Certbot
sudo apt install -y certbot python3-certbot-nginx

# Obtain certificate
sudo certbot certonly --nginx \
  -d mail.yourdomain.com \
  -d admin.yourdomain.com \
  -d portal.yourdomain.com \
  -d api.yourdomain.com

# Auto-renewal is configured by default
# Test renewal: sudo certbot renew --dry-run
```

## 8. Reverse Proxy Setup (Nginx)

```bash
# Install Nginx
sudo apt install -y nginx

# Remove default site
sudo rm -f /etc/nginx/sites-enabled/default

# Create OrvixEM proxy configuration
sudo tee /etc/nginx/sites-available/orvix > /dev/null << 'NGINX'
server {
    listen 80;
    server_name mail.yourdomain.com admin.yourdomain.com portal.yourdomain.com api.yourdomain.com;
    return 301 https://$server_name$request_uri;
}

server {
    listen 443 ssl http2;
    server_name mail.yourdomain.com;

    ssl_certificate /etc/letsencrypt/live/mail.yourdomain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/mail.yourdomain.com/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;

    client_max_body_size 50M;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}

server {
    listen 443 ssl http2;
    server_name admin.yourdomain.com;

    ssl_certificate /etc/letsencrypt/live/mail.yourdomain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/mail.yourdomain.com/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}

server {
    listen 443 ssl http2;
    server_name portal.yourdomain.com;

    ssl_certificate /etc/letsencrypt/live/mail.yourdomain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/mail.yourdomain.com/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
NGINX

# Enable site
sudo ln -s /etc/nginx/sites-available/orvix /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx
```

## 9. Installation Commands

```bash
# === STEP 1: Create Directories ===
sudo mkdir -p /etc/orvix
sudo mkdir -p /var/lib/orvix/{data,rollback,snapshots}
sudo mkdir -p /var/log/orvix

# === STEP 2: Install OrvixEM Binary ===
# Option A: Download pre-built binary
sudo curl -sSL https://updates.orvix.email/download/latest/orvix-linux-amd64 \
  -o /usr/local/bin/orvix
sudo chmod +x /usr/local/bin/orvix

# Option B: Build from source (requires Go 1.23+)
# git clone https://github.com/orvixemail/orvix.git
# cd orvix
# make build
# sudo cp orvix /usr/local/bin/orvix

# === STEP 3: Create orvix User ===
sudo useradd -r -s /bin/false -d /var/lib/orvix orvix
sudo chown -R orvix:orvix /var/lib/orvix /var/log/orvix

# === STEP 4: Configure ===
# Generate secure JWT secret
JWT_SECRET=$(openssl rand -base64 64)
echo "ORVIX_SECURITY_JWT_SECRET=$JWT_SECRET" | sudo tee /etc/orvix/env

# Copy config
sudo cp configs/orvix.yaml /etc/orvix/orvix.yaml
sudo chmod 600 /etc/orvix/orvix.yaml

# Edit config with production values
sudo nano /etc/orvix/orvix.yaml
# Set: database.driver, database.dsn, security.jwt_secret, updates.channel

# === STEP 5: Set Environment Variables ===
sudo tee /etc/profile.d/orvix.sh > /dev/null << 'ENV'
export ORVIX_SECURITY_JWT_SECRET="your-secure-jwt-secret"
export ORVIX_DATABASE_DRIVER="postgres"
export ORVIX_DATABASE_DSN="host=localhost user=orvix password=YOUR_PG_PASSWORD dbname=orvix port=5432 sslmode=disable"
ENV

# === STEP 6: Install systemd Service ===
sudo cp packaging/systemd/orvix.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable orvix
sudo systemctl start orvix

# === STEP 7: Verify ===
sudo systemctl status orvix
curl http://localhost:8080/healthz
```

## 10. First Login Process

```bash
# 1. Verify server is running
curl http://localhost:8080/healthz
# Expected: {"database":true,"frontend":{"admin":true,...},"status":"ok"}

# 2. Access admin console
# Open browser to: https://admin.yourdomain.com
# You will see the login page

# 3. Bootstrap admin (if first time)
curl -X POST http://localhost:8080/api/v1/admin/bootstrap \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@yourdomain.com","password":"YourSecurePassword123!"}'

# Expected: {"status":"admin_created","user_id":1,"email":"admin@yourdomain.com"}

# 4. Login
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@yourdomain.com","password":"YourSecurePassword123!"}'

# Save the access_token for subsequent requests
```

## 11. First Admin Bootstrap

```bash
# If bootstrap already ran, you'll get:
# {"error":"admin already exists"}

# To verify admin exists:
TOKEN="your-jwt-token-from-login"
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/admin/stats
# Expected: {"domains":0,"users":1,"product":"OrvixEM","version":"0.1.0"}
```

## 12. Domain Onboarding Process

```bash
# 1. Create a tenant
curl -X POST http://localhost:8080/api/v1/admin/tenants \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"name":"My Company","slug":"mycompany","tier":"smb"}'

# 2. Add a domain
curl -X POST http://localhost:8080/api/v1/admin/domains \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"name":"yourdomain.com","tenant_id":1}'

# 3. Check DNS records
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8080/api/v1/dns/check?domain=yourdomain.com"

# 4. Create a user
curl -X POST http://localhost:8080/api/v1/users \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"email":"user@yourdomain.com","password":"UserPassword123!","tenant_id":1,"domain_id":1}'
```

## 13. Backup Setup

```bash
# Manual backup via API
curl -X POST http://localhost:8080/api/v1/backups \
  -H "Authorization: Bearer $TOKEN"

# List backups
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/backups

# Add cron job for automatic daily backups
sudo crontab -e
# Add line:
# 0 2 * * * curl -X POST -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/backups

# Set backup encryption key
export ORVIX_ENCRYPTION_KEY="your-secure-encryption-key"
```

## 14. Update Setup

```bash
# Check for updates
orvix update-check

# Apply available update
orvix update-apply

# Rollback if needed
orvix update-rollback

# Configure auto-update channel in orvix.yaml:
# updates:
#   channel: stable
#   auto_check: true
#   auto_apply: false  # Set to true for automatic updates
```

## 15. Rollback Procedure

```bash
# If an update causes issues:

# 1. Check if rollback snapshot exists
ls -la /var/lib/orvix/rollback/

# 2. Perform rollback
orvix update-rollback

# 3. Restart the service
sudo systemctl restart orvix

# 4. Verify rollback was successful
orvix version
curl http://localhost:8080/healthz

# 5. Manual rollback from backup
# If update-rollback fails, restore from a manual backup
# Backups are stored in the backup directory
# Restore by extracting the latest tar.gz
```
