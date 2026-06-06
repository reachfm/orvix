package license

import (
	"testing"

	"go.uber.org/zap"
)

func testLogger(t *testing.T) *zap.Logger {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	return logger
}

func TestNewFeatureFlags(t *testing.T) {
	logger := testLogger(t)
	ff := NewFeatureFlags(logger)
	if ff == nil {
		t.Fatal("NewFeatureFlags returned nil")
	}
}

func TestFeatureFlagsSMBBasics(t *testing.T) {
	logger := testLogger(t)
	ff := NewFeatureFlags(logger)
	ff.SetTier(TierSMB)

	expectedEnabled := []string{
		"webmail", "firewall_basic", "antispam_basic",
		"ssl_auto", "two_factor_auth", "audit_logs", "rest_api",
	}
	for _, name := range expectedEnabled {
		if !ff.IsEnabled(name) {
			t.Errorf("feature %q should be enabled for SMB tier", name)
		}
	}

	expectedDisabled := []string{
		"white_label", "domains_unlimited", "advanced_antispam",
		"mailboxes_unlimited", "full_audit_logs", "priority_support",
	}
	for _, name := range expectedDisabled {
		if ff.IsEnabled(name) {
			t.Errorf("feature %q should NOT be enabled for SMB tier", name)
		}
	}
}

func TestFeatureFlagsISPIncludesSMBBasics(t *testing.T) {
	logger := testLogger(t)
	ff := NewFeatureFlags(logger)
	ff.SetTier(TierISP)

	smbFeatures := []string{"webmail", "firewall_basic", "antispam_basic", "audit_logs"}
	for _, name := range smbFeatures {
		if !ff.IsEnabled(name) {
			t.Errorf("SMB feature %q should be enabled for ISP tier", name)
		}
	}

	ispFeatures := []string{"white_label", "domains_unlimited", "advanced_antispam", "firewall_advanced", "webhooks"}
	for _, name := range ispFeatures {
		if !ff.IsEnabled(name) {
			t.Errorf("ISP feature %q should be enabled for ISP tier", name)
		}
	}

	shouldNotHave := []string{"full_audit_logs", "priority_support"}
	for _, name := range shouldNotHave {
		if ff.IsEnabled(name) {
			t.Errorf("feature %q should NOT be enabled for ISP tier", name)
		}
	}
}

func TestFeatureFlagsEnterpriseIncludesAll(t *testing.T) {
	logger := testLogger(t)
	ff := NewFeatureFlags(logger)
	ff.SetTier(TierEnterprise)

	enterpriseFeatures := []string{
		"webmail", "firewall_basic", "audit_logs",
		"white_label", "domains_unlimited", "advanced_antispam",
		"mailboxes_unlimited", "full_audit_logs", "priority_support",
	}
	for _, name := range enterpriseFeatures {
		if !ff.IsEnabled(name) {
			t.Errorf("Enterprise feature %q should be enabled for Enterprise tier", name)
		}
	}
}

func TestAllFeatures(t *testing.T) {
	logger := testLogger(t)
	ff := NewFeatureFlags(logger)
	ff.SetTier(TierEnterprise)

	all := ff.All()
	if len(all) == 0 {
		t.Fatal("All() should return non-empty map")
	}

	if len(all) < 10 {
		t.Errorf("expected at least 10 features, got %d", len(all))
	}
}

func TestTierParsing(t *testing.T) {
	tests := []struct {
		input string
		tier  Tier
		valid bool
	}{
		{"smb", TierSMB, true},
		{"isp", TierISP, true},
		{"enterprise", TierEnterprise, true},
		{"SMB", "", false},
		{"invalid", "", false},
	}

	for _, tt := range tests {
		tier := Tier(tt.input)
		switch tier {
		case TierSMB, TierISP, TierEnterprise:
			if !tt.valid {
				t.Errorf("Tier(%q) = %s, expected invalid", tt.input, tier)
			}
		default:
			if tt.valid {
				t.Errorf("Tier(%q) = %s, expected valid tier", tt.input, tier)
			}
		}
	}
}
