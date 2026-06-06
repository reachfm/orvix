# Orvix VPS Test Plan

## Prerequisites
- Clean Ubuntu 22.04 or 24.04 VPS (minimal install)
- Root SSH access
- Domain name with DNS access (optional but recommended)

## RC3 Release Information
- **Version:** v1.0.2-rc3
- **Git Commit:** 1849e4e
- **GitHub Release:** https://github.com/reachfm/orvix/releases/tag/v1.0.2
- **Archive SHA256:** `7a00f2fb67b86e741887fe836d0f20523618536df4473f1af0e1509b3261b4c1`
- **Binary SHA256:** `d348b1050322da89a61544f3023cf29ee2b462a24e4f8ab6a278182cca3814ee`

## Download Release
```bash
# Download from GitHub
curl -fsSL https://github.com/reachfm/orvix/releases/download/v1.0.2/orvix-v1.0.2-linux-amd64.tar.gz -o orvix.tar.gz

# Verify checksum
sha256sum orvix.tar.gz
# Expected: 7a00f2fb67b86e741887fe836d0f20523618536df4473f1af0e1509b3261b4c1

# Extract
tar -xzf orvix.tar.gz
```

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
# Run installer from extracted directory
sudo ./install.sh
```

Expected:
- Script prompts for domain, admin email, admin password
- **NO default credentials** - must provide admin email and password
- Directories created: /etc/orvix, /var/lib/orvix, /var/log/orvix
- User 'orvix' created
- Binary installed at /usr/local/bin/orvix
- systemd service installed and enabled with environment variables
- Service starts successfully

### 3. Verify Installation
```bash
# Check service
systemctl status orvix.service --no-pager -l

# Check health endpoint
curl -s http://127.0.0.1:8080/api/v1/health

# Check logs
journalctl -u orvix.service -n 20
```

Expected:
- Health endpoint returns `{"status":"ok"}`
- No hardcoded credentials in logs

### 4. Verify No Default Credentials
```bash
# Verify environment variables are set
cat /etc/systemd/system/orvix.service.d/override.conf

# Check for default credentials in binary
strings /usr/local/bin/orvix | grep -i "admin@orvix.local"
# Should return nothing
```

Expected:
- ORVIX_ADMIN_EMAIL and ORVIX_ADMIN_PASSWORD in override
- No default admin@orvix.local in binary

### 5. Login Test
```bash
# Login with provided credentials
curl -s -X POST http://127.0.0.1:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"YOUR_ADMIN_EMAIL","password":"YOUR_ADMIN_PASSWORD"}'
```

Expected:
- Returns access_token and refresh_token
- No "admin@orvix.local" or "admin123" works

### 6. /me Endpoint Test
```bash
# Get JWT from login response, then:
curl -s http://127.0.0.1:8080/api/v1/me \
  -H "Authorization: Bearer YOUR_JWT_TOKEN"
```

Expected:
- Returns user profile with id, email, role

### 7. Service Restart Test
```bash
systemctl restart orvix.service
sleep 5
curl -s http://127.0.0.1:8080/api/v1/health
```

Expected:
- Service restarts cleanly
- Data persists
- Login still works

### 8. Upgrade Test (optional)
```bash
# Download new version and upgrade
curl -fsSL https://github.com/reachfm/orvix/releases/download/v1.0.2/orvix-v1.0.2-linux-amd64 -o /tmp/orvix
sudo cp /tmp/orvix /usr/local/bin/orvix
sudo systemctl restart orvix.service
```

Expected:
- Service restarts with new binary
- Data preserved

## Validation Checklist
- [ ] 1. Fresh VPS baseline verified
- [ ] 2. Checksum verified (7a00f2fb...)
- [ ] 3. Installer runs without errors
- [ ] 4. No default credentials prompted
- [ ] 5. Health endpoint responds
- [ ] 6. Login works with provided credentials
- [ ] 7. /me endpoint returns profile
- [ ] 8. Service restart preserves data
- [ ] 9. No hardcoded admin@orvix.local in binary
- [ ] 10. Environment variables in systemd override

## Troubleshooting
```bash
# Check service logs
journalctl -u orvix.service -f

# Check environment variables
systemctl show orvix.service | grep ORVIX

# Verify binary checksum
sha256sum /usr/local/bin/orvix
# Expected: d348b1050322da89a61544f3023cf29ee2b462a24e4f8ab6a278182cca3814ee

# Run diagnostics
bash /usr/local/bin/orvix-diagnostics.sh 2>/dev/null || echo "Not installed"
```
