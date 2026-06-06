# Orvix VPS Test Plan

## Prerequisites
- Clean Ubuntu 22.04 or 24.04 VPS (minimal install)
- Root SSH access
- Domain name with DNS access (optional but recommended)

## Test Steps

### 1. Fresh VPS Setup
```bash
# Connect to VPS
ssh root@YOUR_VPS_IP

# Verify clean state
uname -a
cat /etc/os-release
df -h
free -m
```

### 2. Install Orvix
```bash
# From release directory:
bash release/install.sh
```

Expected:
- Script prompts for domain, admin email, admin password
- Directories created: /etc/orvix, /var/lib/orvix, /var/log/orvix
- User 'orvix' created
- Binary installed at /usr/local/bin/orvix
- systemd service installed and enabled
- Service starts successfully

### 3. Verify Installation
```bash
# Check service
systemctl status orvix.service

# Check health endpoint
curl -s http://localhost:8080/api/v1/health

# Check diagnostics
bash /usr/local/bin/orvix-diagnostics.sh
```

Expected:
- Health endpoint returns `{"status":"ok"}`
- Diagnostics report shows all systems green

### 4. Activate License
```bash
# Login as admin
curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@YOUR_DOMAIN","password":"YOUR_PASSWORD"}' \
  -c cookies.txt
```

Expected:
- Login returns access_expires_in and refresh_expires_in
- Cookie file created

### 5. Add Domain
```bash
# Add domain (requires CSRF token first)
CSRF=$(curl -s -b cookies.txt http://localhost:8080/api/v1/csrf-token | grep -o '"csrf_token":"[^"]*"' | cut -d'"' -f4)
curl -s -X POST http://localhost:8080/api/v1/domains \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: $CSRF" \
  -b cookies.txt \
  -d '{"name":"YOUR_DOMAIN"}'
```

Expected:
- Domain created successfully
- Domain appears in GET /api/v1/domains

### 6. Add Mailbox
```bash
CSRF=$(curl -s -b cookies.txt http://localhost:8080/api/v1/csrf-token | grep -o '"csrf_token":"[^"]*"' | cut -d'"' -f4)
curl -s -X POST http://localhost:8080/api/v1/users \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: $CSRF" \
  -b cookies.txt \
  -d '{"name":"Test User","email":"test@YOUR_DOMAIN","password":"TestPass123!"}'
```

Expected:
- User created successfully
- User appears in GET /api/v1/users

### 7. Login to Webmail
```bash
# Access webmail URL
curl -s http://localhost:3000 | head -5
```

Expected:
- Webmail page loads (HTML)

### 8. Service Restart
```bash
systemctl restart orvix.service
sleep 3
systemctl status orvix.service
curl -s http://localhost:8080/api/v1/health
```

Expected:
- Service restarts cleanly
- Health check passes

### 9. Upgrade Test
```bash
# Simulate upgrade
bash release/upgrade.sh
```

Expected:
- Backup created
- Service restarts cleanly

### 10. Uninstall Test (optional)
```bash
# On test VPS only:
bash release/uninstall.sh
```

Expected:
- Services stopped and disabled
- Binaries removed
- Backup saved

## Validation Checklist
- [ ] 1. Fresh VPS baseline verified
- [ ] 2. Installer runs without errors
- [ ] 3. Health endpoint responds
- [ ] 4. Login works
- [ ] 5. Domain creation works
- [ ] 6. Mailbox creation works
- [ ] 7. Webmail loads
- [ ] 8. Service restart survives
- [ ] 9. Upgrade preserves data
- [ ] 10. Uninstall cleans up (optional)

## Troubleshooting
```bash
# Check service logs
journalctl -u orvix.service -f

# Run diagnostics
bash release/scripts/diagnostics.sh

# Check health
bash release/scripts/healthcheck.sh --verbose
```
