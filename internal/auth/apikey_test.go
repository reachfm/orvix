package auth

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/models"
	"go.uber.org/zap"
)

func newAPIKeyTestManager(t *testing.T) *APIKeyManager {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	dir := t.TempDir()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(dir, "apikey.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	m := NewAPIKeyManager(db, logger)

	// H-2: minting a key now requires a real, currently-authorized owner, so
	// the fixture provisions the tenant/user these tests mint keys for
	// (tenant 1, user 1, active tenant_admin). Previously keys could be
	// minted for a user id that did not exist at all.
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	now := time.Now().UTC()
	if _, err := sqlDB.Exec(
		"INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (1, ?, ?, 'fixture', 'fixture', 'fixture.test', 'enterprise', 1)",
		now, now); err != nil {
		t.Fatalf("insert fixture tenant: %v", err)
	}
	if _, err := sqlDB.Exec(
		"INSERT INTO users (id, created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, token_version) VALUES (1, ?, ?, 'fixture@example.test', 'x', 'tenant_admin', 1, 1, 1, 0)",
		now, now); err != nil {
		t.Fatalf("insert fixture user: %v", err)
	}
	return m
}

func TestParseAllowedIPs_ValidEntries(t *testing.T) {
	cidrs, err := ParseAllowedIPs("203.0.113.0/24, 198.51.100.7")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cidrs) != 2 {
		t.Fatalf("expected 2 entries, got %v", cidrs)
	}
	if cidrs[1] != "198.51.100.7/32" {
		t.Fatalf("expected a bare IP to become /32, got %q", cidrs[1])
	}
}

func TestParseAllowedIPs_InvalidEntryRejected(t *testing.T) {
	if _, err := ParseAllowedIPs("not-an-ip"); err == nil {
		t.Fatal("expected an error for an invalid CIDR/IP entry")
	}
}

func TestParseAllowedIPs_EmptyMeansUnrestricted(t *testing.T) {
	cidrs, err := ParseAllowedIPs("")
	if err != nil || cidrs != nil {
		t.Fatalf("expected (nil, nil) for empty input, got (%v, %v)", cidrs, err)
	}
}

func TestIPAllowed_EmptyRestrictionAllowsAny(t *testing.T) {
	if !ipAllowed("", "203.0.113.99") {
		t.Fatal("expected an empty restriction to allow any IP")
	}
}

func TestIPAllowed_MatchingCIDR(t *testing.T) {
	if !ipAllowed("203.0.113.0/24", "203.0.113.42") {
		t.Fatal("expected an IP inside the allowed CIDR to be allowed")
	}
}

func TestIPAllowed_NonMatchingCIDRDenied(t *testing.T) {
	if ipAllowed("203.0.113.0/24", "198.51.100.1") {
		t.Fatal("expected an IP outside the allowed CIDR to be denied")
	}
}

func TestIPAllowed_UnparseableIPFailsClosed(t *testing.T) {
	if ipAllowed("203.0.113.0/24", "") {
		t.Fatal("expected an unparseable remote IP to fail closed when a restriction is configured")
	}
}

func TestAPIKey_ValidateForIP_UnrestrictedKeyAllowsAnyIP(t *testing.T) {
	m := newAPIKeyTestManager(t)
	fullKey, record, err := m.Generate("test key", 1, 1, "tenant_admin", nil, 0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := m.ValidateForIP(fullKey, "198.51.100.1"); err != nil {
		t.Fatalf("expected an unrestricted key to validate from any IP, got %v", err)
	}
	_ = record
}

func TestAPIKey_SetAllowedIPs_ThenValidateForIP(t *testing.T) {
	m := newAPIKeyTestManager(t)
	fullKey, record, err := m.Generate("scoped key", 1, 1, "tenant_admin", nil, 0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if err := m.SetAllowedIPs(record.ID, 1, "203.0.113.0/24"); err != nil {
		t.Fatalf("set allowed ips: %v", err)
	}

	if _, err := m.ValidateForIP(fullKey, "203.0.113.42"); err != nil {
		t.Fatalf("expected the key to validate from an address inside the restriction, got %v", err)
	}
	if _, err := m.ValidateForIP(fullKey, "198.51.100.1"); err == nil {
		t.Fatal("expected the key to be rejected from an address outside the restriction")
	}
}

func TestAPIKey_SetAllowedIPs_InvalidCIDRRejected(t *testing.T) {
	m := newAPIKeyTestManager(t)
	_, record, err := m.Generate("key", 1, 1, "tenant_admin", nil, 0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if err := m.SetAllowedIPs(record.ID, 1, "not-a-cidr"); err == nil {
		t.Fatal("expected an invalid CIDR to be rejected, not silently stored")
	}
}

func TestAPIKey_SetAllowedIPs_ClearRestriction(t *testing.T) {
	m := newAPIKeyTestManager(t)
	fullKey, record, err := m.Generate("key", 1, 1, "tenant_admin", nil, 0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if err := m.SetAllowedIPs(record.ID, 1, "203.0.113.0/24"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ValidateForIP(fullKey, "198.51.100.1"); err == nil {
		t.Fatal("expected the key to be denied before clearing the restriction")
	}
	if err := m.SetAllowedIPs(record.ID, 1, ""); err != nil {
		t.Fatalf("clear restriction: %v", err)
	}
	if _, err := m.ValidateForIP(fullKey, "198.51.100.1"); err != nil {
		t.Fatalf("expected the key to validate from any IP after clearing the restriction, got %v", err)
	}
}

func TestAPIKey_ValidateWithoutIPStillWorksUnrestricted(t *testing.T) {
	m := newAPIKeyTestManager(t)
	fullKey, _, err := m.Generate("key", 1, 1, "tenant_admin", nil, 0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := m.Validate(fullKey); err != nil {
		t.Fatalf("expected the IP-agnostic Validate to still work for an unrestricted key, got %v", err)
	}
}
