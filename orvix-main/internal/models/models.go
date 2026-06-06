package models

import (
	"time"

	"github.com/orvix/orvix/internal/config"
	"gorm.io/gorm"
)

// Common fields embedded in every model.
type Common struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}

// License represents a license key.
type License struct {
	Common
	KeyHash       string `gorm:"uniqueIndex;not null" json:"key_hash"`
	Tier          string `gorm:"not null;default:'smb'" json:"tier"`
	IssuedAt      time.Time `gorm:"not null" json:"issued_at"`
	ExpiresAt     time.Time `gorm:"not null" json:"expires_at"`
	MaxDomains    int    `gorm:"not null;default:10" json:"max_domains"`
	MaxMailboxes  int    `gorm:"not null;default:500" json:"max_mailboxes"`
	HardwareID    string `gorm:"not null" json:"hardware_id"`
	Metadata     string `gorm:"type:text" json:"metadata"`
	Active       bool   `gorm:"not null;default:true" json:"active"`
}

// BeforeCreate encrypts sensitive fields before storing in the database.
func (l *License) BeforeCreate(tx *gorm.DB) error {
	if l.KeyHash != "" {
		encrypted, err := config.EncryptString(l.KeyHash)
		if err != nil {
			return err
		}
		l.KeyHash = encrypted
	}
	if l.Metadata != "" {
		encrypted, err := config.EncryptString(l.Metadata)
		if err != nil {
			return err
		}
		l.Metadata = encrypted
	}
	return nil
}

// AfterFind decrypts sensitive fields after reading from the database.
func (l *License) AfterFind(tx *gorm.DB) error {
	if l.KeyHash != "" {
		if decrypted, err := config.DecryptString(l.KeyHash); err == nil {
			l.KeyHash = decrypted
		}
	}
	if l.Metadata != "" {
		if decrypted, err := config.DecryptString(l.Metadata); err == nil {
			l.Metadata = decrypted
		}
	}
	return nil
}

// FeatureFlag represents a feature toggle controlled by license tier.
type FeatureFlag struct {
	Common
	Name          string `gorm:"uniqueIndex;not null" json:"name"`
	Enabled       bool   `gorm:"not null;default:false" json:"enabled"`
	TierRequired  string `gorm:"not null" json:"tier_required"`
	ModuleVersion string `gorm:"not null;default:'1.0.0'" json:"module_version"`
	Description    string `gorm:"type:text" json:"description"`
}

// ModuleVersion tracks installed module versions.
type ModuleVersion struct {
	Common
	ModuleID    string    `gorm:"uniqueIndex;not null" json:"module_id"`
	Version     string    `gorm:"not null" json:"version"`
	InstalledAt time.Time `gorm:"not null" json:"installed_at"`
	Checksum    string    `gorm:"not null" json:"checksum"`
	Status      string    `gorm:"not null;default:'active'" json:"status"`
	Changelog   string    `gorm:"type:text" json:"changelog"`
}

// Tenant represents an organization in the multi-tenant system.
type Tenant struct {
	Common
	Name       string `gorm:"not null" json:"name"`
	Slug       string `gorm:"uniqueIndex;not null" json:"slug"`
	Domain     string `gorm:"uniqueIndex;not null" json:"domain"`
	Plan       string `gorm:"default:'smb'" json:"plan"`
	MaxDomains   int  `gorm:"default:10" json:"max_domains"`
	MaxMailboxes int  `gorm:"default:500" json:"max_mailboxes"`
	LogoURL    string `json:"logo_url"`
	PrimaryColor string `gorm:"default:'#4F7CFF'" json:"primary_color"`
	Active     bool   `gorm:"default:true" json:"active"`
	ResellerID *uint  `gorm:"index" json:"reseller_id,omitempty"`
}

// Reseller represents a reseller who manages customer tenants.
type Reseller struct {
	Common
	Name         string `gorm:"not null" json:"name"`
	Email        string `gorm:"uniqueIndex;not null" json:"email"`
	PasswordHash string `gorm:"not null" json:"-"`
	MaxTenants   int    `gorm:"default:50" json:"max_tenants"`
	MaxDomains   int    `gorm:"default:500" json:"max_domains"`
	MaxMailboxes int    `gorm:"default:10000" json:"max_mailboxes"`
	Commission   float64 `gorm:"default:0.0" json:"commission"`
	Active       bool    `gorm:"default:true" json:"active"`
}

// LDAPConfig stores LDAP directory synchronization settings.
type LDAPConfig struct {
	Common
	TenantID    uint   `gorm:"index;not null" json:"tenant_id"`
	Host        string `gorm:"not null" json:"host"`
	Port        int    `gorm:"default:389" json:"port"`
	BaseDN      string `gorm:"not null" json:"base_dn"`
	BindDN      string `gorm:"not null" json:"bind_dn"`
	BindPassword string `gorm:"not null" json:"-"`
	UserFilter  string `gorm:"default:'(objectClass=person)'" json:"user_filter"`
	SyncEnabled bool   `gorm:"default:false" json:"sync_enabled"`
	LastSync    *time.Time `json:"last_sync"`
}

// SSOConfig stores SSO/OAuth provider configuration.
type SSOConfig struct {
	Common
	TenantID      uint   `gorm:"index;not null" json:"tenant_id"`
	Provider      string `gorm:"not null" json:"provider"`
	ClientID      string `gorm:"not null" json:"client_id"`
	ClientSecret  string `gorm:"not null" json:"-"`
	IssuerURL     string `json:"issuer_url"`
	Enabled       bool   `gorm:"default:false" json:"enabled"`
}

// AlertConfig stores security alert delivery settings.
type AlertConfig struct {
	Common
	TenantID     uint   `gorm:"index;not null" json:"tenant_id"`
	SMTPEnabled  bool   `gorm:"default:false" json:"smtp_enabled"`
	SMTPServer   string `json:"smtp_server"`
	SMTPPort     int    `gorm:"default:587" json:"smtp_port"`
	SMTPUsername string `json:"smtp_username"`
	SMTPPassword string `json:"-"`
	SMTPFrom     string `json:"smtp_from"`
	WebhookEnabled bool `gorm:"default:false" json:"webhook_enabled"`
	WebhookURL   string `json:"webhook_url"`
	AlertOnFailedLogin bool `gorm:"default:true" json:"alert_on_failed_login"`
	AlertOnSuspiciousKey bool `gorm:"default:true" json:"alert_on_suspicious_key"`
}
type FirewallRule struct {
	Common
	Name      string `gorm:"not null" json:"name"`
	Condition string `gorm:"type:text;not null" json:"condition"`
	Action    string `gorm:"not null" json:"action"`
	Priority  int    `gorm:"not null;default:0" json:"priority"`
	Enabled   bool   `gorm:"not null;default:true" json:"enabled"`
}

// FirewallLog represents a firewall action log entry.
type FirewallLog struct {
	Common
	IP          string  `gorm:"not null" json:"ip"`
	Domain      string  `gorm:"not null" json:"domain"`
	Sender      string  `gorm:"" json:"sender"`
	Recipient   string  `gorm:"" json:"recipient"`
	Action      string  `gorm:"not null" json:"action"`
	Reason      string  `gorm:"type:text" json:"reason"`
	ThreatScore float64 `gorm:"not null;default:0" json:"threat_score"`
}

// GuardianLog represents an AI threat analysis log.
type GuardianLog struct {
	Common
	MessageID   string  `gorm:"index;not null" json:"message_id"`
	ThreatScore float64 `gorm:"not null" json:"threat_score"`
	Verdict     string  `gorm:"not null" json:"verdict"`
	Confidence  float64 `gorm:"not null;default:0" json:"confidence"`
	Reasons     string  `gorm:"type:text" json:"reasons"`
	Action      string  `gorm:"not null" json:"action"`
}

// HealHistory represents an auto-heal action.
type HealHistory struct {
	Common
	CheckName  string `gorm:"not null" json:"check_name"`
	Severity   string `gorm:"not null" json:"severity"`
	Issue      string `gorm:"type:text;not null" json:"issue"`
	FixApplied string `gorm:"type:text" json:"fix_applied"`
	Success    bool   `gorm:"not null" json:"success"`
}

// ProvisionedDomain tracks domains created via the deployment API.
type ProvisionedDomain struct {
	Common
	Domain        string `gorm:"uniqueIndex;not null" json:"domain"`
	Plan          string `gorm:"not null" json:"plan"`
	Status        string `gorm:"not null;default:'pending'" json:"status"`
	ProvisionedBy uint   `gorm:"not null" json:"provisioned_by"`
	Metadata      string `gorm:"type:text" json:"metadata"`
}

// AuditLog represents an immutable audit log entry.
type AuditLog struct {
	Common
	UserID   uint   `gorm:"index;not null" json:"user_id"`
	Action   string `gorm:"not null" json:"action"`
	Resource string `gorm:"not null" json:"resource"`
	IP       string `gorm:"not null" json:"ip"`
	Details  string `gorm:"type:text" json:"details"`
}

// Session represents a user session.
type Session struct {
	Common
	UserID    uint      `gorm:"index;not null" json:"user_id"`
	TokenHash string    `gorm:"uniqueIndex;not null" json:"token_hash"`
	IP        string    `gorm:"not null" json:"ip"`
	UserAgent string    `gorm:"type:text" json:"user_agent"`
	ExpiresAt time.Time `gorm:"not null" json:"expires_at"`
}

// UpdateHistory tracks module update history.
type UpdateHistory struct {
	Common
	ModuleID    string `gorm:"not null" json:"module_id"`
	FromVersion string `gorm:"not null" json:"from_version"`
	ToVersion   string `gorm:"not null" json:"to_version"`
	Status      string `gorm:"not null" json:"status"`
	BackupPath  string `gorm:"type:text" json:"backup_path"`
}

// MigrateAll auto-migrates all models.
func MigrateAll(db *gorm.DB) error {
	return db.AutoMigrate(
		&License{},
		&FeatureFlag{},
		&ModuleVersion{},
		&Tenant{},
		&Reseller{},
		&LDAPConfig{},
		&SSOConfig{},
		&AlertConfig{},
		&FirewallRule{},
		&FirewallLog{},
		&GuardianLog{},
		&HealHistory{},
		&ProvisionedDomain{},
		&AuditLog{},
		&Session{},
		&UpdateHistory{},
	)
}

