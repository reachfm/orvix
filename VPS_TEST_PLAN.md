# Orvix VPS Test Plan

## Prerequisites
- Clean Ubuntu 22.04 or 24.04 VPS (minimal install)
- Root SSH access
- Domain name with DNS access (optional but recommended)

## RC4 Release Information
- **Version:** v1.0.3-rc4
- **Git Commit:** (pending push)
- **GitHub Release:** https://github.com/reachfm/orvix/releases/tag/v1.0.3
- **Archive SHA256:** `aed4f97924b3e9315afbe9185600e6d3b8a3cecdff8698314090e768499099bb`
- **Binary SHA256:** `1cc564f2183ee9ad4d07e3fa4515eb2e22e8caecdfb8a6215fb817f78b7287f5`

## Download Release
```bash
# Download from GitHub
curl -fsSL https://github.com/reachfm/orvix/releases/download/v1.0.3/orvix-v1.0.3-linux-amd64.tar.gz -o orvix.tar.gz

# Verify checksum
sha256sum orvix.tar.gz
# Expected: aed4f97924b3e9315afbe9185600e6d3b8a3cecdff8698314090e768499099bb

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
curl -fsSL https://github.com/reachfm/orvix/releases/download/v1.0.3/orvix-v1.0.3-linux-amd64 -o /tmp/orvix
sudo cp /tmp/orvix /usr/local/bin/orvix
sudo systemctl restart orvix.service
```

Expected:
- Service restarts with new binary
- Data preserved

## Validation Checklist
- [ ] 1. Fresh VPS baseline verified
- [ ] 2. Checksum verified (aed4f979...)
- [ ] 3. Installer runs without errors
- [ ] 4. Stalwart download succeeds
- [ ] 5. No default credentials prompted
- [ ] 6. Health endpoint responds
- [ ] 7. Login works with provided credentials
- [ ] 8. /me endpoint returns profile
- [ ] 9. Service restart preserves data
- [ ] 10. No hardcoded admin@orvix.local in binary
- [ ] 11. Environment variables in systemd override
- [ ] 12. Password confirmation works

## Troubleshooting
```bash
# Check service logs
journalctl -u orvix.service -f

# Check environment variables
systemctl show orvix.service | grep ORVIX

# Verify binary checksum
sha256sum /usr/local/bin/orvix
# Expected: 1cc564f2183ee9ad4d07e3fa4515eb2e22e8caecdfb8a6215fb817f78b7287f5

# Run diagnostics
bash /usr/local/bin/orvix-diagnostics.sh 2>/dev/null || echo "Not installed"
```
