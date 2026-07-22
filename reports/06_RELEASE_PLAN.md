# RELEASE PLAN

## Orvix Enterprise Mail (OrvixEM)

**Date:** 2026-06-05
**Reference:** MVP Sections: Build & Release (Lines 1361–1423), Update Channels (Lines 1228–1242), Versioning (Lines 1206–1226)

---

## 1. Versioning Strategy

Per MVP lines 1206–1222:

```
MAJOR.MINOR.PATCH
```

| Component | Current | Strategy |
|-----------|---------|----------|
| OrvixEM Version | 0.0.0 (pre-release) | Start at 0.1.0 for first development build |
| First Production Release | 1.0.0 | After Phase 7 completion |
| API Version | v1 | Breaking changes require new version prefix |

---

## 2. Release Cadence

| Channel | Frequency | Audience | Quality Gate |
|---------|-----------|----------|--------------|
| Nightly | Daily (automated) | Internal dev team | Build succeeds, unit tests pass |
| Early Access | Bi-weekly | Strategic partners, data centers | Integration tests + smoke tests pass |
| Beta | Monthly | Selected customers, QA | All tests pass, staging verified |
| Stable | Every 6-8 weeks | All production customers | Full release verification (MVP lines 1414–1423) |

---

## 3. Release Milestones

### Pre-Release (Development)

| Version | Target Date | Phase Complete | Key Features |
|---------|-------------|---------------|--------------|
| 0.1.0 | Week 2 | Phase 1 | Foundation: config, DB, license, flags |
| 0.2.0 | Week 5 | Phase 2 | Stalwart integration complete |
| 0.3.0 | Week 7 | Phase 3 | Security layer, Guardian, Auto-Heal |
| 0.4.0 | Week 9 | Phase 4 | API layer, Instant Deploy |
| 0.5.0 | Week 12 | Phase 5 | Webmail, Admin Console, Portal |
| 0.6.0 | Week 15 | Phase 6 | Migration, Compliance, Encryption |
| 1.0.0-rc.1 | Week 16 | Phase 7 | Release candidate |
| 1.0.0-rc.2 | Week 17 | Phase 7 | Post-audit RC |
| 1.0.0 | Week 18 | Phase 7 | Production release |

---

## 4. Release Process

### 4.1 Build Process (MVP lines 1363–1370)

```bash
# Backend build
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -ldflags="-s -w \
    -X main.Version=1.0.0 \
    -X main.Product=OrvixEM \
    -X main.Commit=$(git rev-parse --short HEAD) \
    -X main.Channel=stable" \
  -o orvix ./cmd/orvix

# Frontend build (separate)
cd web
npm run build  # Produces web/dist/
cd ..
# Frontend is embedded via //go:embed web/dist
```

### 4.2 Release Artifacts (MVP lines 1399–1410)

Each release must include:

| Artifact | Description | Format |
|----------|-------------|--------|
| OrvixEM binary | Compiled Go binary | `orvix-linux-amd64-v1.0.0` |
| SHA256SUMS | Checksums file | `SHA256SUMS` |
| Signature | GPG signature | `SHA256SUMS.sig` |
| Release manifest | Machine-readable metadata | `release-v1.0.0.json` |
| Changelog | Human/machine readable changes | `CHANGELOG.md` |
| Migration manifest | DB migration files | `migrations/v1.0.0/` |
| Compatibility matrix | Stalwart versions | `compatibility.json` |
| Rollback metadata | Previous version info | `rollback-v1.0.0.json` |
| SBOM | Software bill of materials | `sbom-v1.0.0.json` (CycloneDX) |

### 4.3 Release Manifest Format

```json
{
  "version": "1.0.0",
  "channel": "stable",
  "product": "OrvixEM",
  "commit": "a1b2c3d",
  "build_date": "2026-10-15T10:00:00Z",
  "checksum": "sha256:abc123...",
  "signature": "base64:...",
  "stalwart_compatible": [">=0.5.0", "<1.0.0"],
  "migration_required": false,
  "migration_type": "additive",
  "min_upgradable_from": "0.5.0",
  "rollback_supported": true,
  "breaking_changes": [],
  "security_fixes": ["SEC-2026-001", "SEC-2026-002"],
  "sbom_url": "https://updates.orvix.email/v1.0.0/sbom.json"
}
```

---

## 5. Quality Gates (MVP lines 1414–1423)

No release enters Stable unless ALL checks pass:

```
Gate 1: Unit Tests
  - All Go tests pass (go test ./...)
  - Frontend tests pass (npm test)

Gate 2: Integration Tests
  - API integration tests pass
  - Stalwart compatibility tests pass
  - Migration tests pass

Gate 3: Security Tests
  - No critical/high CVEs in dependencies
  - License enforcement tests pass
  - Auth bypass tests pass

Gate 4: Update Tests
  - Update from previous version succeeds
  - Rollback to previous version succeeds
  - Snapshot/restore works

Gate 5: Deployment Tests
  - Clean install on fresh OS succeeds
  - systemd service starts correctly
  - Health checks all pass

Gate 6: Performance Tests
  - API response times within threshold
  - DB query performance validated
  - No memory leaks detected
```

---

## 6. Update Channels Configuration

Per MVP lines 1228–1242:

```yaml
# orvix.yaml — update configuration
update:
  channel: stable           # stable | beta | early-access | nightly
  auto_check: true          # Check for updates automatically
  auto_apply: false         # Never auto-apply (admin must confirm)
  update_server: https://updates.orvix.email
  public_key: /etc/orvix/update.pub  # GPG public key for manifest verification
```

---

## 7. Rollback Strategy

Per MVP lines 1154–1161 (snapshot creation) and 1186–1188 (auto-rollback):

```
Rollback Scenarios:
  1. Post-update health check fails → Auto-rollback within 60 seconds
  2. Admin manually triggers rollback → Previous version restored
  3. Critical bug found in release → Emergency channel disable via license server

Rollback Process:
  1. Stop current services
  2. Restore previous binary
  3. Restore previous config
  4. Restore DB from snapshot (if migration applied)
  5. Restart services
  6. Verify health checks
  7. Notify admin of rollback
```

---

## 8. Infrastructure for Releases

### 8.1 Update Server (`updates.orvix.email`)

Must provide (MVP lines 1348–1357):

| Endpoint | Purpose |
|----------|---------|
| `GET /v1/manifest/latest` | Latest version metadata |
| `GET /v1/manifest/{version}` | Specific version metadata |
| `GET /v1/download/{version}/{file}` | Binary/download file |
| `GET /v1/changelog/{version}` | Changelog |
| `GET /v1/compatibility/{version}` | Compatibility matrix |
| `GET /v1/channels` | Channel definitions |
| `GET /v1/rollback/{version}` | Rollback metadata |
| `GET /v1/security/bulletins` | Security bulletins |

### 8.2 License Server (`license.orvix.email`)

Must provide:

| Endpoint | Purpose |
|----------|---------|
| `POST /v1/activate` | License activation |
| `POST /v1/validate` | Online license validation |
| `GET /v1/public-key` | RS256 public key |
| `POST /v1/deactivate` | License deactivation |

---

## 9. Post-Release Maintenance

| Activity | Frequency | Owner |
|----------|-----------|-------|
| Security dependency scan | Daily | Automated |
| Stalwart compatibility check | Weekly | DevOps |
| Performance benchmark | Weekly | Engineering |
| Customer update notifications | Per release | Product |
| Emergency security release | As needed | Security |

---

## 10. Launch Checklist (Phase 7)

| Item | Status |
|------|--------|
| orvix.email marketing website deployed | ❌ |
| updates.orvix.email update server operational | ❌ |
| license.orvix.email license server operational | ❌ |
| status.orvix.email status page operational | ❌ |
| One-line installer script tested | ❌ |
| API documentation published | ❌ |
| Admin documentation published | ❌ |
| Security audit completed | ❌ |
| Penetration test completed | ❌ |
| Load test completed (10k concurrent) | ❌ |
| Deliverability test passed | ❌ |
| Stalwart compatibility certified | ❌ |
| License purchase flow operational | ❌ |
| All 3 tiers tested and verified | ❌ |

---

**Conclusion:** The release plan follows the MVP's defined versioning, channel, and quality gate requirements. First production release (1.0.0) is targeted for Week 18. The update server and license server infrastructure must be operational before launch.
