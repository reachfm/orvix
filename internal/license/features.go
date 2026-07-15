package license

import (
	"sync"

	"go.uber.org/zap"
)

// Tier represents a license tier.
type Tier string

const (
	TierSMB        Tier = "smb"
	TierISP        Tier = "isp"
	TierEnterprise Tier = "enterprise"
)

// FeatureFlags provides feature enablement based on license tier.
type FeatureFlags struct {
	mu     sync.RWMutex
	tier   Tier
	flags  map[string]bool
	logger *zap.Logger
}

// NewFeatureFlags creates a new feature flag system.
func NewFeatureFlags(logger *zap.Logger) *FeatureFlags {
	return &FeatureFlags{
		flags:  make(map[string]bool),
		logger: logger,
	}
}

// SetTier updates the current license tier and re-evaluates features.
func (ff *FeatureFlags) SetTier(tier Tier) {
	ff.mu.Lock()
	defer ff.mu.Unlock()

	ff.tier = tier
	ff.evaluate()
	ff.logger.Info("feature flags updated", zap.String("tier", string(tier)))
}

// IsEnabled returns whether a feature is enabled.
func (ff *FeatureFlags) IsEnabled(name string) bool {
	ff.mu.RLock()
	defer ff.mu.RUnlock()
	return ff.flags[name]
}

// All returns all feature flags.
func (ff *FeatureFlags) All() map[string]bool {
	ff.mu.RLock()
	defer ff.mu.RUnlock()

	result := make(map[string]bool, len(ff.flags))
	for k, v := range ff.flags {
		result[k] = v
	}
	return result
}

func (ff *FeatureFlags) evaluate() {
	ff.flags["webmail"] = true
	ff.flags["firewall_basic"] = true
	ff.flags["antispam_basic"] = true
	ff.flags["ssl_auto"] = true
	ff.flags["two_factor_auth"] = true
	ff.flags["audit_logs"] = true
	ff.flags["rest_api"] = true

	if ff.tier == TierISP || ff.tier == TierEnterprise {
		ff.flags["domains_unlimited"] = true
		ff.flags["white_label"] = true
		ff.flags["advanced_antispam"] = true
		ff.flags["firewall_advanced"] = true
		ff.flags["webhooks"] = true
	}

	if ff.tier == TierEnterprise {
		ff.flags["mailboxes_unlimited"] = true
		ff.flags["full_audit_logs"] = true
		ff.flags["priority_support"] = true
	}
}
