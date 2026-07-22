package license

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/orvixemail/orvix/internal/config"
	"github.com/orvixemail/orvix/internal/models"
	"gorm.io/gorm"
)

func setupLicenseDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	if err := db.AutoMigrate(&models.License{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	return db
}

func TestParseTier(t *testing.T) {
	tests := []struct {
		input string
		want  Tier
	}{
		{"smb", TierSMB},
		{"isp", TierISP},
		{"enterprise", TierEnterprise},
		{"unknown", TierUnknown},
		{"", TierUnknown},
		{"SMB", TierUnknown},
	}

	for _, tt := range tests {
		got := ParseTier(tt.input)
		if got != tt.want {
			t.Errorf("ParseTier(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestTierString(t *testing.T) {
	tests := []struct {
		tier Tier
		want string
	}{
		{TierSMB, "smb"},
		{TierISP, "isp"},
		{TierEnterprise, "enterprise"},
		{TierUnknown, "unknown"},
	}

	for _, tt := range tests {
		got := tt.tier.String()
		if got != tt.want {
			t.Errorf("Tier(%d).String() = %q, want %q", tt.tier, got, tt.want)
		}
	}
}

func TestActivateAndGetLicense(t *testing.T) {
	db := setupLicenseDB(t)
	svc := NewService(db, config.LicenseConfig{
		OfflineGraceDays: 7,
	})

	expiresAt := time.Now().Add(365 * 24 * time.Hour)
	lic, err := svc.ActivateLicense("test-key-hash", "smb", 10, 500, expiresAt)
	if err != nil {
		t.Fatalf("ActivateLicense failed: %v", err)
	}
	if lic.Tier != "smb" {
		t.Errorf("expected tier=smb, got %s", lic.Tier)
	}
	if lic.MaxDomains != 10 {
		t.Errorf("expected max_domains=10, got %d", lic.MaxDomains)
	}
	if lic.MaxMailboxes != 500 {
		t.Errorf("expected max_mailboxes=500, got %d", lic.MaxMailboxes)
	}
	if !lic.Active {
		t.Error("license should be active")
	}

	active, err := svc.GetActiveLicense()
	if err != nil {
		t.Fatalf("GetActiveLicense failed: %v", err)
	}
	if active.ID != lic.ID {
		t.Errorf("expected license ID %d, got %d", lic.ID, active.ID)
	}

	tier := svc.GetTier()
	if tier != TierSMB {
		t.Errorf("expected TierSMB, got %v", tier)
	}

	domains := svc.GetMaxDomains()
	if domains != 10 {
		t.Errorf("expected 10 max domains, got %d", domains)
	}

	mailboxes := svc.GetMaxMailboxes()
	if mailboxes != 500 {
		t.Errorf("expected 500 max mailboxes, got %d", mailboxes)
	}
}

func TestNoLicenseReturnsUnknown(t *testing.T) {
	db := setupLicenseDB(t)
	svc := NewService(db, config.LicenseConfig{})

	_, err := svc.GetActiveLicense()
	if err == nil {
		t.Error("GetActiveLicense should fail when no license exists")
	}

	tier := svc.GetTier()
	if tier != TierUnknown {
		t.Errorf("expected TierUnknown, got %v", tier)
	}

	domains := svc.GetMaxDomains()
	if domains != 0 {
		t.Errorf("expected 0 max domains, got %d", domains)
	}
}

func TestExpiredLicenseDeactivated(t *testing.T) {
	db := setupLicenseDB(t)
	svc := NewService(db, config.LicenseConfig{
		OfflineGraceDays: 0,
	})

	past := time.Now().Add(-1 * time.Hour)
	lic, err := svc.ActivateLicense("expired-key", "isp", 100, 5000, past)
	if err != nil {
		t.Fatalf("ActivateLicense failed: %v", err)
	}

	// Should be deactivated on startup validation
	svc2 := NewService(db, config.LicenseConfig{
		OfflineGraceDays: 0,
	})

	_ = lic
	_ = svc2

	result := db.Where("active = ?", true).First(&models.License{})
	if result.Error == nil {
		t.Error("expired license should be deactivated")
	}
}
