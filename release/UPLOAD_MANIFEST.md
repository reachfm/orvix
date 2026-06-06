# Orvix v1.0.0 Upload Manifest

## Files to Upload

| File | Source Path | Expected URL | Size | SHA256 |
|------|-------------|-------------|------|--------|
| `orvix-v1.0.0-linux-amd64.tar.gz` | `release/orvix-v1.0.0-linux-amd64.tar.gz` | `https://releases.orvix.email/v1.0.0/orvix-v1.0.0-linux-amd64.tar.gz` | 9,445,131 bytes | `79226CBEABFF3F9DB0079B1B5EDFA0A4A3F949454324270DD6B72853E08EA18B` |
| `checksums.txt` | `release/checksums.txt` | `https://releases.orvix.email/v1.0.0/checksums.txt` | 1,024 bytes | See file |

## Install URL
```
https://orvix.email/install.sh
```
This URL should either:
- Serve `release/install.sh` directly
- Or redirect to: `https://releases.orvix.email/v1.0.0/install.sh`

## Release URL
```
https://releases.orvix.email/v1.0.0/
```

## Checksum URL
```
https://releases.orvix.email/v1.0.0/checksums.txt
```

## Server Installation Command
```bash
# Option 1: One-command install (requires install.sh hosted)
curl -fsSL https://orvix.email/install.sh | bash

# Option 2: Manual install from release tarball
curl -fsSL -o orvix-release.tar.gz https://releases.orvix.email/v1.0.0/orvix-v1.0.0-linux-amd64.tar.gz
tar -xzf orvix-release.tar.gz
cd release
bash install.sh

# Option 3: Direct binary download
curl -fsSL -o /usr/local/bin/orvix https://releases.orvix.email/v1.0.0/orvix-linux-amd64
chmod +x /usr/local/bin/orvix
```

## Directory Structure Required on Release Server
```
releases.orvix.email/
├── v1.0.0/
│   ├── orvix-v1.0.0-linux-amd64.tar.gz
│   ├── orvix-linux-amd64
│   ├── install.sh
│   ├── uninstall.sh
│   ├── upgrade.sh
│   ├── checksums.txt
│   ├── systemd/
│   │   └── orvix.service
│   ├── configs/
│   │   └── orvix.yaml.example
│   ├── scripts/
│   │   ├── healthcheck.sh
│   │   └── diagnostics.sh
│   ├── RELEASE_NOTES.md
│   └── VERSION
└── latest/  (symlink or copy of latest version)
```

## Verification After Upload
```bash
# Verify checksums
curl -fsSL https://releases.orvix.email/v1.0.0/checksums.txt | sha256sum -c

# Verify archive
curl -fsSL https://releases.orvix.email/v1.0.0/orvix-v1.0.0-linux-amd64.tar.gz | tar -tz
```
