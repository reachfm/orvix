package features

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/orvixemail/orvix/internal/license"
	"github.com/orvixemail/orvix/internal/models"
	"gorm.io/gorm"
)

type Manager struct {
	db          *gorm.DB
	licSvc      *license.Service
	mu          sync.RWMutex
	cache       map[string]bool
	cacheTTL    time.Duration
	lastRefresh time.Time
}

func NewManager(db *gorm.DB, licSvc *license.Service) *Manager {
	return &Manager{
		db:       db,
		licSvc:   licSvc,
		cache:    make(map[string]bool),
		cacheTTL: 5 * time.Minute,
	}
}

var defaultFeatures = []models.FeatureFlag{
	{Key: "webmail", Name: "Webmail Access", Description: "Access to the webmail interface", Enabled: true, Tier: "smb", IsGlobal: true, Source: "builtin"},
	{Key: "admin_console", Name: "Admin Console", Description: "Access to the admin console", Enabled: true, Tier: "smb", IsGlobal: true, Source: "builtin"},
	{Key: "smtp_imap_pop3", Name: "SMTP / IMAP / POP3", Description: "Standard mail protocols", Enabled: true, Tier: "smb", IsGlobal: true, Source: "builtin"},
	{Key: "mail_firewall_basic", Name: "Mail Firewall (Basic)", Description: "Basic mail firewall rules", Enabled: true, Tier: "smb", IsGlobal: true, Source: "builtin"},
	{Key: "auto_heal", Name: "Auto-Heal System", Description: "Automatic health checks and fixes", Enabled: true, Tier: "smb", IsGlobal: true, Source: "builtin"},
	{Key: "anti_spam_basic", Name: "Anti-Spam (Basic)", Description: "Basic spam filtering", Enabled: true, Tier: "smb", IsGlobal: true, Source: "builtin"},
	{Key: "ssl_tls_auto", Name: "SSL/TLS Auto", Description: "Automatic SSL/TLS certificate management", Enabled: true, Tier: "smb", IsGlobal: true, Source: "builtin"},
	{Key: "dns_wizard", Name: "DNS Wizard", Description: "Guided DNS setup", Enabled: true, Tier: "smb", IsGlobal: true, Source: "builtin"},
	{Key: "two_factor_auth", Name: "2FA", Description: "Two-factor authentication", Enabled: true, Tier: "smb", IsGlobal: true, Source: "builtin"},
	{Key: "calendar_contacts", Name: "Calendar & Contacts", Description: "CalDAV/CardDAV sync", Enabled: true, Tier: "smb", IsGlobal: true, Source: "builtin"},
	{Key: "smart_compose_basic", Name: "Smart Compose AI (Basic)", Description: "Basic AI writing assistant", Enabled: true, Tier: "smb", IsGlobal: true, Source: "builtin"},
	{Key: "pwa", Name: "PWA", Description: "Installable progressive web app", Enabled: true, Tier: "smb", IsGlobal: true, Source: "builtin"},

	{Key: "white_label", Name: "White Label", Description: "Full white-label branding", Enabled: false, Tier: "isp", IsGlobal: true, Source: "builtin"},
	{Key: "rest_api", Name: "REST API", Description: "Programmatic API access", Enabled: false, Tier: "isp", IsGlobal: true, Source: "builtin"},
	{Key: "instant_deploy_api", Name: "Instant Deploy API", Description: "Provision domains in <30s via API", Enabled: false, Tier: "isp", IsGlobal: true, Source: "builtin"},
	{Key: "clustering_basic", Name: "Clustering (3 nodes)", Description: "Up to 3 node cluster", Enabled: false, Tier: "isp", IsGlobal: true, Source: "builtin"},
	{Key: "anti_spam_advanced", Name: "Advanced Anti-Spam", Description: "Advanced spam filtering and AV", Enabled: false, Tier: "isp", IsGlobal: true, Source: "builtin"},
	{Key: "mail_firewall_advanced", Name: "Mail Firewall (Advanced)", Description: "Advanced firewall with custom rules", Enabled: false, Tier: "isp", IsGlobal: true, Source: "builtin"},
	{Key: "guardian_ai", Name: "Guardian Agent (Full AI)", Description: "Full AI security agent", Enabled: false, Tier: "isp", IsGlobal: true, Source: "builtin"},
	{Key: "email_archiving", Name: "Email Archiving", Description: "Email archiving and retention", Enabled: false, Tier: "isp", IsGlobal: true, Source: "builtin"},
	{Key: "reseller_panel", Name: "Reseller Panel", Description: "Multi-tenant reseller management", Enabled: false, Tier: "isp", IsGlobal: true, Source: "builtin"},
	{Key: "migration_tool", Name: "Smart Migration Tool", Description: "IMAP and provider migration", Enabled: false, Tier: "isp", IsGlobal: true, Source: "builtin"},
	{Key: "multi_cloud_storage", Name: "Multi-Cloud Storage", Description: "S3/GCS/Azure storage", Enabled: false, Tier: "isp", IsGlobal: true, Source: "builtin"},
	{Key: "email_intelligence", Name: "Email Intelligence", Description: "AI-powered email analytics", Enabled: false, Tier: "isp", IsGlobal: true, Source: "builtin"},
	{Key: "active_sync", Name: "ActiveSync", Description: "Exchange ActiveSync mobile sync", Enabled: false, Tier: "isp", IsGlobal: true, Source: "builtin"},
	{Key: "distribution_lists", Name: "Distribution Lists", Description: "Mailing lists and distribution lists", Enabled: false, Tier: "isp", IsGlobal: true, Source: "builtin"},
	{Key: "resource_booking", Name: "Resource Booking", Description: "Bookable resources (rooms, equipment)", Enabled: false, Tier: "isp", IsGlobal: true, Source: "builtin"},
	{Key: "public_folders", Name: "Public Folders", Description: "Shared folders accessible to multiple users", Enabled: false, Tier: "isp", IsGlobal: true, Source: "builtin"},
	{Key: "sla_monitoring", Name: "SLA Monitoring", Description: "SLA monitoring dashboard", Enabled: false, Tier: "isp", IsGlobal: true, Source: "builtin"},

	{Key: "clustering_unlimited", Name: "Clustering (Unlimited)", Description: "Unlimited cluster nodes", Enabled: false, Tier: "enterprise", IsGlobal: true, Source: "builtin"},
	{Key: "ldap_sync", Name: "LDAP/AD Sync", Description: "Directory service synchronization", Enabled: false, Tier: "enterprise", IsGlobal: true, Source: "builtin"},
	{Key: "sso", Name: "SSO (SAML/OAuth2)", Description: "Single sign-on federation", Enabled: false, Tier: "enterprise", IsGlobal: true, Source: "builtin"},
	{Key: "advanced_routing", Name: "Advanced Email Routing", Description: "Advanced routing rules engine", Enabled: false, Tier: "enterprise", IsGlobal: true, Source: "builtin"},
	{Key: "legal_hold", Name: "Legal Hold & eDiscovery", Description: "Legal hold and discovery tools", Enabled: false, Tier: "enterprise", IsGlobal: true, Source: "builtin"},
	{Key: "dlp", Name: "DLP", Description: "Data loss prevention", Enabled: false, Tier: "enterprise", IsGlobal: true, Source: "builtin"},
	{Key: "compliance_center", Name: "Compliance Center", Description: "GDPR/HIPAA/SOX compliance", Enabled: false, Tier: "enterprise", IsGlobal: true, Source: "builtin"},
	{Key: "zero_knowledge_encryption", Name: "Zero-Knowledge Encryption", Description: "Client-side email encryption", Enabled: false, Tier: "enterprise", IsGlobal: true, Source: "builtin"},
	{Key: "guardian_api", Name: "Guardian API + Custom Training", Description: "AI agent API with custom training", Enabled: false, Tier: "enterprise", IsGlobal: true, Source: "builtin"},
	{Key: "collaboration_layer", Name: "Collaboration Layer", Description: "Shared inbox and team collaboration", Enabled: false, Tier: "enterprise", IsGlobal: true, Source: "builtin"},
	{Key: "smart_compose_advanced", Name: "Smart Compose AI (Advanced)", Description: "Advanced AI with custom training", Enabled: false, Tier: "enterprise", IsGlobal: true, Source: "builtin"},
	{Key: "full_audit_logs", Name: "Full Audit Logs", Description: "Comprehensive audit trail", Enabled: false, Tier: "enterprise", IsGlobal: true, Source: "builtin"},
	{Key: "backup_restore", Name: "Backup & Restore", Description: "Built-in backup and restore", Enabled: false, Tier: "enterprise", IsGlobal: true, Source: "builtin"},
	{Key: "s3_storage", Name: "S3/External Storage", Description: "External object storage", Enabled: false, Tier: "enterprise", IsGlobal: true, Source: "builtin"},
	{Key: "auto_update_admin", Name: "Auto-Update from Admin", Description: "One-click updates from admin panel", Enabled: false, Tier: "enterprise", IsGlobal: true, Source: "builtin"},

	{Key: "kill_all", Name: "Emergency Kill Switch", Description: "Disable all non-essential features", Enabled: false, IsGlobal: true, IsKillSwitch: true, Source: "builtin"},
	{Key: "kill_ai", Name: "Kill AI Features", Description: "Disable all AI-powered features", Enabled: false, IsGlobal: true, IsKillSwitch: true, Source: "builtin"},
	{Key: "kill_migration", Name: "Kill Migration", Description: "Disable migration tools", Enabled: false, IsGlobal: true, IsKillSwitch: true, Source: "builtin"},
}

func (m *Manager) Initialize() error {
	var count int64
	m.db.Model(&models.FeatureFlag{}).Count(&count)
	if count > 0 {
		return nil
	}

	for _, f := range defaultFeatures {
		if err := m.db.Create(&f).Error; err != nil {
			return fmt.Errorf("failed to create feature flag %s: %w", f.Key, err)
		}
	}

	return nil
}

func (m *Manager) IsEnabled(key string, tenantID *uint) bool {
	m.mu.RLock()
	cached, ok := m.cache[key]
	m.mu.RUnlock()

	if ok && time.Since(m.lastRefresh) < m.cacheTTL {
		return cached
	}

	m.refreshCache()

	m.mu.RLock()
	enabled, ok := m.cache[key]
	m.mu.RUnlock()

	if ok {
		return enabled
	}

	return m.evaluateFeature(key, tenantID)
}

func (m *Manager) evaluateFeature(key string, tenantID *uint) bool {
	licTier := m.licSvc.GetTier()

	var ff models.FeatureFlag
	if err := m.db.Where("key = ?", key).First(&ff).Error; err != nil {
		return false
	}

	if ff.IsKillSwitch && ff.Enabled {
		return false
	}

	killAll := models.FeatureFlag{}
	m.db.Where("key = ?", "kill_all").First(&killAll)
	if killAll.Enabled && !ff.IsKillSwitch {
		return false
	}

	if ff.Source == "license" {
		requiredTier := license.ParseTier(ff.Tier)
		return licTier >= requiredTier
	}

	return ff.Enabled
}

func (m *Manager) refreshCache() {
	m.mu.Lock()
	defer m.mu.Unlock()

	var flags []models.FeatureFlag
	m.db.Where("is_global = ? OR is_kill_switch = ?", true, true).Find(&flags)

	for _, f := range flags {
		m.cache[f.Key] = f.Enabled
	}

	for _, ff := range defaultFeatures {
		enabled := m.evaluateFeature(ff.Key, nil)
		m.cache[ff.Key] = enabled
	}

	killAll, ok := m.cache["kill_all"]
	if ok && killAll {
		for k := range m.cache {
			if k != "kill_all" {
				m.cache[k] = false
			}
		}
	}

	m.lastRefresh = time.Now()
}

func (m *Manager) SetFlag(key string, enabled bool) error {
	result := m.db.Model(&models.FeatureFlag{}).Where("key = ?", key).Update("enabled", enabled)
	if result.Error != nil {
		return fmt.Errorf("failed to update feature flag %s: %w", key, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("feature flag %s not found", key)
	}

	m.mu.Lock()
	m.cache[key] = enabled
	m.mu.Unlock()

	return nil
}

func (m *Manager) GetAllFlags() []models.FeatureFlag {
	m.refreshCache()

	var flags []models.FeatureFlag
	m.db.Where("is_kill_switch = ?", false).Find(&flags)

	for i := range flags {
		m.mu.RLock()
		enabled, ok := m.cache[flags[i].Key]
		m.mu.RUnlock()
		if ok {
			flags[i].Enabled = enabled
		}
	}

	return flags
}

func (m *Manager) CheckRemoteKillSwitch() error {
	cfg := struct {
		URL string `mapstructure:"emergency_disable_url"`
	}{}
	if cfg.URL == "" {
		return nil
	}

	resp, err := http.Get(cfg.URL)
	if err != nil {
		return fmt.Errorf("failed to check kill switch: %w", err)
	}
	defer resp.Body.Close()

	var killFlags map[string]bool
	if err := json.NewDecoder(resp.Body).Decode(&killFlags); err != nil {
		return fmt.Errorf("failed to decode kill switch response: %w", err)
	}

	for key, enabled := range killFlags {
		var ff models.FeatureFlag
		if err := m.db.Where("key = ?", key).First(&ff).Error; err == nil && ff.IsKillSwitch {
			m.db.Model(&ff).Update("enabled", enabled)
			m.mu.Lock()
			m.cache[key] = enabled
			m.mu.Unlock()
		}
	}

	return nil
}
