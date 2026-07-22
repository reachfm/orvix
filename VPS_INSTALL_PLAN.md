# OrvixEM VPS Installation Plan

**Target:** Ubuntu 24.04 LTS
**Domain:** orvix.email (example — replace with your domain)

---

## 1. System Requirements

| Component | Minimum | Recommended |
|---|---|---|
| CPU | 1 core | 2 cores |
| RAM | 512 MB | 2 GB |
| Disk | 10 GB | 20 GB |
| OS | Ubuntu 24.04 LTS | Ubuntu 24.04 LTS |
| Architecture | x86_64 | x86_64 |

## 2. Prerequisites

```bash
# Update system
sudo apt update && sudo apt upgrade -y

# Install required packages
sudo apt install -y curl wget git

# Install Go (for building from source — skip if downloading binary)
wget https://go.dev/dl/go1.23.0.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.23.0.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
source ~/.bashrc

# Install Node.js (for building frontend — skip if downloading binary)
curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
sudo apt install -y nodejs
```

## 3. Install OrvixEM

### Option A: Download Binary (Recommended)

```bash
# Download latest release
curl -sSL https://updates.orvix.email/download/latest/orvix-linux-amd64 -o /tmp/orvix
sudo mv /tmp/orvix /usr/local/bin/orvix
sudo chmod +x /usr/local/bin/orvix
```

### Option B: Build from Source

```bash
git clone https://github.com/orvixemail/orvix.git
cd orvix
make build
sudo cp orvix /usr/local/bin/orvix
```

## 4. Directory Setup

```bash
# Create directories
sudo mkdir -p /etc/orvix
sudo mkdir -p /var/lib/orvix/{data,rollback,snapshots}
sudo mkdir -p /var/log/orvix

# Create orvix user (optional but recommended)
sudo useradd -r -s /bin/false -m -d /var/lib/orvix orvix
sudo chown -R orvix:orvix /var/lib/orvix /var/log/orvix

# Copy config
sudo cp configs/orvix.yaml /etc/orvix/orvix.yaml
sudo chmod 600 /etc/orvix/orvix.yaml
```

## 5. Configuration

Edit `/etc/orvix/orvix.yaml`:

```yaml
server:
  listen: ":8080"
  external_url: "https://mail.yourdomain.com"

database:
  driver: "postgres"
  dsn: "host=localhost user=orvix password=YOUR_DB_PASSWORD dbname=orvix port=5432 sslmode=disable"

security:
  jwt_secret: "GENERATE_A_SECURE_RANDOM_STRING"
  allowed_origins: ["https://mail.yourdomain.com", "https://admin.yourdomain.com"]
```

Set environment variables for sensitive values:

```bash
export ORVIX_SECURITY_JWT_SECRET="your-secure-random-string"
export ORVIX_DATABASE_DSN="host=localhost user=orvix password=YOUR_DB_PASSWORD dbname=orvix port=5432 sslmode=disable"
```

## 6. PostgreSQL Setup

```bash
sudo apt install -y postgresql postgresql-client
sudo systemctl start postgresql

sudo -u postgres psql -c "CREATE USER orvix WITH PASSWORD 'YOUR_DB_PASSWORD';"
sudo -u postgres psql -c "CREATE DATABASE orvix OWNER orvix;"
sudo -u postgres psql -c "GRANT ALL PRIVILEGES ON DATABASE orvix TO orvix;"
```

## 7. Stalwart Mail Server Setup (Optional)

```bash
# Download Stalwart
curl -sSL https://stalw.art/install | bash

# Verify installation
orvix stalwart status
orvix stalwart validate
```

## 8. systemd Service

```bash
# Install systemd service
sudo cp packaging/systemd/orvix.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable orvix
sudo systemctl start orvix
```

## 9. Firewall Rules

```bash
sudo ufw allow 22/tcp      # SSH
sudo ufw allow 80/tcp      # HTTP (for Let's Encrypt)
sudo ufw allow 443/tcp     # HTTPS
sudo ufw allow 8080/tcp    # OrvixEM API (internal)
sudo ufw allow 25/tcp      # SMTP (Stalwart)
sudo ufw allow 587/tcp     # SMTP submission (Stalwart)
sudo ufw allow 465/tcp     # SMTP over TLS (Stalwart)
sudo ufw allow 143/tcp     # IMAP (Stalwart)
sudo ufw allow 993/tcp     # IMAP over TLS (Stalwart)
sudo ufw allow 110/tcp     # POP3 (Stalwart)
sudo ufw allow 995/tcp     # POP3 over TLS (Stalwart)
```

## 10. DNS Records

```
A      mail.yourdomain.com   → YOUR_VPS_IP
A      admin.yourdomain.com  → YOUR_VPS_IP
CNAME  api.yourdomain.com    → mail.yourdomain.com
CNAME  portal.yourdomain.com → mail.yourdomain.com
MX     yourdomain.com        → mail.yourdomain.com (priority 10)
TXT    yourdomain.com        → "v=spf1 include:_spf.orvix.email ~all"
TXT    _dmarc.yourdomain.com → "v=DMARC1; p=none; fo=1; pct=100"
```

## 11. Nginx Reverse Proxy (Optional)

```nginx
server {
    listen 80;
    server_name mail.yourdomain.com admin.yourdomain.com portal.yourdomain.com;
    return 301 https://$server_name$request_uri;
}

server {
    listen 443 ssl;
    server_name mail.yourdomain.com;

    ssl_certificate /etc/letsencrypt/live/mail.yourdomain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/mail.yourdomain.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

## 12. Health Check

```bash
# Verify server is running
curl http://localhost:8080/healthz

# Expected response:
# {"database":true,"frontend":{"admin":true,"portal":true,"webmail":true},"license":false,"status":"ok"}

# Bootstrap admin
curl -X POST http://localhost:8080/api/v1/admin/bootstrap \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@yourdomain.com","password":"YourAdminPassword!"}'
```

## 13. Deployment Order

1. Provision VPS (Ubuntu 24.04)
2. Configure DNS records
3. Open firewall ports
4. Install prerequisites (Go, Node.js)
5. Build or download OrvixEM binary
6. Set up PostgreSQL
7. Create directories and copy config
8. Set environment variables
9. Install Stalwart (optional)
10. Install systemd service
11. Start OrvixEM
12. Bootstrap admin account
13. Verify health endpoint
14. Configure Nginx reverse proxy (optional)
15. Set up Let's Encrypt TLS (optional)

## 14. Post-Install Checklist

- [ ] Server responds to `/healthz` with HTTP 200
- [ ] Admin console accessible at `/admin`
- [ ] Webmail accessible at `/mail`
- [ ] Portal accessible at `/portal`
- [ ] API responds at `/api/v1/features`
- [ ] Bootstrap admin account created
- [ ] Login works with JWT token
- [ ] PostgreSQL is connected (if using)
- [ ] Backup directory is writable
- [ ] Logs are being written to `/var/log/orvix`
