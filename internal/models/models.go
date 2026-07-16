package models

import (
	"context"
	"database/sql"
	"fmt"
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
	KeyHash      string    `gorm:"uniqueIndex;not null" json:"key_hash"`
	Tier         string    `gorm:"not null;default:'smb'" json:"tier"`
	IssuedAt     time.Time `gorm:"not null" json:"issued_at"`
	ExpiresAt    time.Time `gorm:"not null" json:"expires_at"`
	MaxDomains   int       `gorm:"not null;default:10" json:"max_domains"`
	MaxMailboxes int       `gorm:"not null;default:500" json:"max_mailboxes"`
	HardwareID   string    `gorm:"not null" json:"hardware_id"`
	Metadata     string    `gorm:"type:text" json:"metadata"`
	Active       bool      `gorm:"not null;default:true" json:"active"`
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
	Description   string `gorm:"type:text" json:"description"`
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
	Name         string `gorm:"not null" json:"name"`
	Slug         string `gorm:"uniqueIndex;not null" json:"slug"`
	Domain       string `gorm:"uniqueIndex;not null" json:"domain"`
	Plan         string `gorm:"default:'smb'" json:"plan"`
	MaxDomains   int    `gorm:"default:10" json:"max_domains"`
	MaxMailboxes int    `gorm:"default:500" json:"max_mailboxes"`
	LogoURL      string `json:"logo_url"`
	PrimaryColor string `gorm:"default:'#4F7CFF'" json:"primary_color"`
	Active       bool   `gorm:"default:true" json:"active"`
	ResellerID   *uint  `gorm:"index" json:"reseller_id,omitempty"`
}

// Reseller represents a reseller who manages customer tenants.
type Reseller struct {
	Common
	Name         string  `gorm:"not null" json:"name"`
	Email        string  `gorm:"uniqueIndex;not null" json:"email"`
	PasswordHash string  `gorm:"not null" json:"-"`
	MaxTenants   int     `gorm:"default:50" json:"max_tenants"`
	MaxDomains   int     `gorm:"default:500" json:"max_domains"`
	MaxMailboxes int     `gorm:"default:10000" json:"max_mailboxes"`
	Commission   float64 `gorm:"default:0.0" json:"commission"`
	Active       bool    `gorm:"default:true" json:"active"`
}

// LDAPConfig stores LDAP directory synchronization settings.
type LDAPConfig struct {
	Common
	TenantID     uint       `gorm:"index;not null" json:"tenant_id"`
	Host         string     `gorm:"not null" json:"host"`
	Port         int        `gorm:"default:389" json:"port"`
	BaseDN       string     `gorm:"not null" json:"base_dn"`
	BindDN       string     `gorm:"not null" json:"bind_dn"`
	BindPassword string     `gorm:"not null" json:"-"`
	UserFilter   string     `gorm:"default:'(objectClass=person)'" json:"user_filter"`
	SyncEnabled  bool       `gorm:"default:false" json:"sync_enabled"`
	LastSync     *time.Time `json:"last_sync"`
}

// SSOConfig stores SSO/OAuth provider configuration.
type SSOConfig struct {
	Common
	TenantID     uint   `gorm:"index;not null" json:"tenant_id"`
	Provider     string `gorm:"not null" json:"provider"`
	ClientID     string `gorm:"not null" json:"client_id"`
	ClientSecret string `gorm:"not null" json:"-"`
	IssuerURL    string `json:"issuer_url"`
	Enabled      bool   `gorm:"default:false" json:"enabled"`
}

// AlertConfig stores security alert delivery settings.
type AlertConfig struct {
	Common
	TenantID             uint   `gorm:"index;not null" json:"tenant_id"`
	SMTPEnabled          bool   `gorm:"default:false" json:"smtp_enabled"`
	SMTPServer           string `json:"smtp_server"`
	SMTPPort             int    `gorm:"default:587" json:"smtp_port"`
	SMTPUsername         string `json:"smtp_username"`
	SMTPPassword         string `json:"-"`
	SMTPFrom             string `json:"smtp_from"`
	WebhookEnabled       bool   `gorm:"default:false" json:"webhook_enabled"`
	WebhookURL           string `json:"webhook_url"`
	AlertOnFailedLogin   bool   `gorm:"default:true" json:"alert_on_failed_login"`
	AlertOnSuspiciousKey bool   `gorm:"default:true" json:"alert_on_suspicious_key"`
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

// Session represents a user session.
//
// The Role and Email columns are server-persisted at login time and
// restored by the auth middleware on every request — the opaque
// HttpOnly cookie is the only thing the client holds. Storing the
// server-derived role on the session row removes the need to look up
// the users table on every request and, more importantly, lets the
// middleware restore the real role without trusting any
// client-supplied claim. Legacy rows written before this column
// existed are refused by ValidateOpaqueSession so a missing role
// never downgrades a real admin to a per-mailbox user.
type Session struct {
	Common
	UserID    uint      `gorm:"index;not null" json:"user_id"`
	TokenHash string    `gorm:"uniqueIndex;not null" json:"token_hash"`
	Role      string    `gorm:"not null;default:''" json:"role"`
	Email     string    `gorm:"not null;default:''" json:"email"`
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

// User represents a user in the system.
type User struct {
	Common
	Email         string     `gorm:"uniqueIndex;not null" json:"email"`
	PasswordHash  string     `gorm:"not null" json:"-"`
	Role          string     `gorm:"not null;default:'user'" json:"role"`
	TenantID      *uint      `gorm:"index" json:"tenant_id,omitempty"`
	Active        bool       `gorm:"not null;default:true" json:"active"`
	EmailVerified bool       `gorm:"not null;default:false" json:"email_verified"`
	LastLogin     *time.Time `json:"last_login"`
	DisplayName   string     `gorm:"column:full_name;default:''" json:"display_name,omitempty"`
	Locale        string     `gorm:"default:''" json:"locale,omitempty"`
	Timezone      string     `gorm:"default:''" json:"timezone,omitempty"`
	Theme         string     `gorm:"default:'dark'" json:"theme,omitempty"`
}

// Domain represents a mail domain.
type Domain struct {
	Common
	TenantID     uint   `gorm:"index;not null" json:"tenant_id"`
	Domain       string `gorm:"uniqueIndex;not null" json:"domain"`
	DKIMSelector string `gorm:"default:'mail'" json:"dkim_selector"`
	SPFRecord    string `gorm:"type:text" json:"spf_record"`
	DMARCRecord  string `gorm:"type:text" json:"dmarc_record"`
	DKIMRecord   string `gorm:"type:text" json:"dkim_record"`
	MXRecord     string `gorm:"type:text" json:"mx_record"`
	Status       string `gorm:"not null;default:'pending'" json:"status"`
	IsVerified   bool   `gorm:"not null;default:false" json:"is_verified"`
	IsPrimary    bool   `gorm:"not null;default:false" json:"is_primary"`
}

// Mailbox represents a mail account.
type Mailbox struct {
	Common
	TenantID     uint   `gorm:"index;not null" json:"tenant_id"`
	DomainID     uint   `gorm:"index;not null" json:"domain_id"`
	LocalPart    string `gorm:"not null" json:"local_part"`
	PasswordHash string `gorm:"not null" json:"-"`
	DisplayName  string `json:"display_name"`
	IsAlias      bool   `gorm:"not null;default:false" json:"is_alias"`
	IsCatchall   bool   `gorm:"not null;default:false" json:"is_catchall"`
	IsActive     bool   `gorm:"not null;default:true" json:"is_active"`
	QuotaMB      int    `gorm:"not null;default:1024" json:"quota_mb"`
	SendLimit    int    `gorm:"not null;default:500" json:"send_limit"`
}

// APIKey represents an API key for programmatic access.
type APIKey struct {
	Common
	UserID     uint       `gorm:"index;not null" json:"user_id"`
	KeyHash    string     `gorm:"uniqueIndex;not null" json:"-"`
	Name       string     `gorm:"not null" json:"name"`
	Scopes     string     `gorm:"type:text;not null;default:''" json:"scopes"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	Active     bool       `gorm:"not null;default:true" json:"active"`
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
		&Session{},
		&UpdateHistory{},
	)
}

// MigrateAllRaw uses raw SQL for SQLite compatibility (RC2 FIX)
// AutoMigrate uses postgres-specific queries that don't work with SQLite
// Uses raw database/sql directly to avoid GORM Transaction issues with modernc.org/sqlite
func MigrateAllRaw(db *gorm.DB) error {
	// Get underlying sql.DB from GORM
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create all tables using raw SQL compatible with SQLite
	sqls := []string{
		`CREATE TABLE IF NOT EXISTS licenses (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			key_hash TEXT NOT NULL UNIQUE,
			tier TEXT NOT NULL DEFAULT 'smb',
			issued_at DATETIME NOT NULL,
			expires_at DATETIME NOT NULL,
			max_domains INTEGER NOT NULL DEFAULT 10,
			max_mailboxes INTEGER NOT NULL DEFAULT 500,
			hardware_id TEXT NOT NULL,
			metadata TEXT,
			active INTEGER NOT NULL DEFAULT 1
		)`,
		`CREATE TABLE IF NOT EXISTS feature_flags (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			name TEXT NOT NULL UNIQUE,
			enabled INTEGER NOT NULL DEFAULT 0,
			tier_required TEXT NOT NULL,
			module_version TEXT NOT NULL DEFAULT '1.0.0',
			description TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS module_versions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			module_id TEXT NOT NULL UNIQUE,
			version TEXT NOT NULL,
			installed_at DATETIME NOT NULL,
			checksum TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			changelog TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS tenants (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			name TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE,
			domain TEXT NOT NULL UNIQUE,
			plan TEXT DEFAULT 'smb',
			max_domains INTEGER DEFAULT 10,
			max_mailboxes INTEGER DEFAULT 500,
			logo_url TEXT,
			primary_color TEXT DEFAULT '#4F7CFF',
			active INTEGER DEFAULT 1,
			reseller_id INTEGER,
			UNIQUE(slug, deleted_at)
		)`,
		`CREATE TABLE IF NOT EXISTS resellers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			name TEXT NOT NULL,
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			max_tenants INTEGER DEFAULT 50,
			max_domains INTEGER DEFAULT 500,
			max_mailboxes INTEGER DEFAULT 10000,
			commission REAL DEFAULT 0.0,
			active INTEGER DEFAULT 1
		)`,
		`CREATE TABLE IF NOT EXISTS l_dap_configs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			tenant_id INTEGER NOT NULL,
			host TEXT NOT NULL,
			port INTEGER DEFAULT 389,
			base_dn TEXT NOT NULL,
			bind_dn TEXT NOT NULL,
			bind_password TEXT NOT NULL,
			user_filter TEXT DEFAULT '(objectClass=person)',
			sync_enabled INTEGER DEFAULT 0,
			last_sync DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS s_s_o_configs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			tenant_id INTEGER NOT NULL,
			provider TEXT NOT NULL,
			client_id TEXT NOT NULL,
			client_secret TEXT NOT NULL,
			issuer_url TEXT,
			enabled INTEGER DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS alert_configs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			tenant_id INTEGER NOT NULL,
			smtp_enabled INTEGER DEFAULT 0,
			smtp_server TEXT,
			smtp_port INTEGER DEFAULT 587,
			smtp_username TEXT,
			smtp_password TEXT,
			smtp_from TEXT,
			webhook_enabled INTEGER DEFAULT 0,
			webhook_url TEXT,
			alert_on_failed_login INTEGER DEFAULT 1,
			alert_on_suspicious_key INTEGER DEFAULT 1
		)`,
		`CREATE TABLE IF NOT EXISTS firewall_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			name TEXT NOT NULL,
			condition TEXT NOT NULL,
			action TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1
		)`,
		`CREATE TABLE IF NOT EXISTS firewall_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			ip TEXT NOT NULL,
			domain TEXT NOT NULL,
			sender TEXT,
			recipient TEXT,
			action TEXT NOT NULL,
			reason TEXT,
			threat_score REAL NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS guardian_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			message_id TEXT NOT NULL,
			threat_score REAL NOT NULL,
			verdict TEXT NOT NULL,
			confidence REAL NOT NULL DEFAULT 0,
			reasons TEXT,
			action TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS heal_histories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			check_name TEXT NOT NULL,
			severity TEXT NOT NULL,
			issue TEXT NOT NULL,
			fix_applied TEXT,
			success INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS provisioned_domains (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			domain TEXT NOT NULL UNIQUE,
			plan TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			provisioned_by INTEGER NOT NULL,
			metadata TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			actor TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT '',
			target TEXT NOT NULL DEFAULT '',
			result TEXT NOT NULL DEFAULT '',
			ip TEXT NOT NULL DEFAULT '',
			user_agent TEXT NOT NULL DEFAULT '',
			timestamp DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			user_id INTEGER NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			role TEXT NOT NULL DEFAULT '',
			email TEXT NOT NULL DEFAULT '',
			ip TEXT NOT NULL,
			user_agent TEXT,
			expires_at DATETIME NOT NULL
		)`,
		// revoked_tokens backs H-9 access-token revocation on logout:
		// a logged-out access token's jti is stored here until its
		// own expiry so ValidateAccessToken rejects it immediately.
		`CREATE TABLE IF NOT EXISTS revoked_tokens (
			jti TEXT PRIMARY KEY,
			expires_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS update_histories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			module_id TEXT NOT NULL,
			from_version TEXT NOT NULL,
			to_version TEXT NOT NULL,
			status TEXT NOT NULL,
			backup_path TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'user',
			tenant_id INTEGER,
			active INTEGER NOT NULL DEFAULT 1,
			email_verified INTEGER NOT NULL DEFAULT 0,
			last_login DATETIME,
			full_name TEXT NOT NULL DEFAULT '',
			locale TEXT NOT NULL DEFAULT '',
			timezone TEXT NOT NULL DEFAULT '',
			theme TEXT NOT NULL DEFAULT 'dark',
			UNIQUE(email, deleted_at)
		)`,
		`CREATE TABLE IF NOT EXISTS user_notification_preferences (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			user_id INTEGER NOT NULL UNIQUE,
			domain_verification INTEGER NOT NULL DEFAULT 1,
			quota_warning INTEGER NOT NULL DEFAULT 1,
			quota_reached INTEGER NOT NULL DEFAULT 1,
			billing_status INTEGER NOT NULL DEFAULT 1,
			invitation INTEGER NOT NULL DEFAULT 1,
			session_activity INTEGER NOT NULL DEFAULT 1,
			channel_email INTEGER NOT NULL DEFAULT 1
		)`,
		`CREATE TABLE IF NOT EXISTS support_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			reference_id TEXT NOT NULL UNIQUE,
			tenant_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			user_email TEXT NOT NULL,
			category TEXT NOT NULL,
			subject TEXT NOT NULL,
			message TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'received',
			delivery_status TEXT NOT NULL DEFAULT 'pending',
			delivery_error TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS invoices (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			tenant_id INTEGER NOT NULL,
			subscription_id INTEGER,
			provider TEXT NOT NULL DEFAULT '',
			provider_invoice_id TEXT,
			invoice_number TEXT,
			currency TEXT NOT NULL DEFAULT 'usd',
			subtotal INTEGER NOT NULL DEFAULT 0,
			tax INTEGER NOT NULL DEFAULT 0,
			total INTEGER NOT NULL DEFAULT 0,
			amount_paid INTEGER NOT NULL DEFAULT 0,
			amount_due INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'draft',
			period_start DATETIME,
			period_end DATETIME,
			issued_at DATETIME,
			due_at DATETIME,
			paid_at DATETIME,
			hosted_invoice_url TEXT,
			pdf_url TEXT,
			provider_event_created_at DATETIME,
			provider_event_id TEXT,
			provider_updated_at DATETIME,
			UNIQUE(provider, provider_invoice_id)
		)`,
		`CREATE TABLE IF NOT EXISTS domains (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			tenant_id INTEGER NOT NULL,
			domain TEXT NOT NULL,
			dkim_selector TEXT DEFAULT 'mail',
			spf_record TEXT,
			dmarc_record TEXT,
			dkim_record TEXT,
			mx_record TEXT,
			status TEXT NOT NULL DEFAULT 'pending',
			is_verified INTEGER NOT NULL DEFAULT 0,
			is_primary INTEGER NOT NULL DEFAULT 0,
			UNIQUE(domain, deleted_at)
		)`,
		`CREATE TABLE IF NOT EXISTS mailboxes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			tenant_id INTEGER NOT NULL,
			domain_id INTEGER NOT NULL,
			local_part TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			display_name TEXT,
			is_alias INTEGER NOT NULL DEFAULT 0,
			is_catchall INTEGER NOT NULL DEFAULT 0,
			is_active INTEGER NOT NULL DEFAULT 1,
			quota_mb INTEGER NOT NULL DEFAULT 1024,
			send_limit INTEGER NOT NULL DEFAULT 500,
			UNIQUE(domain_id, local_part, deleted_at)
		)`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			user_id INTEGER NOT NULL,
			key_hash TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			scopes TEXT NOT NULL DEFAULT '',
			expires_at DATETIME,
			last_used_at DATETIME,
			active INTEGER NOT NULL DEFAULT 1
		)`,
		// CoreMail tables
		`CREATE TABLE IF NOT EXISTS coremail_domains (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			reseller_id INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'active',
			plan TEXT NOT NULL DEFAULT 'smb',
			description TEXT NOT NULL DEFAULT '',
			max_mailboxes INTEGER NOT NULL DEFAULT 0,
			max_aliases INTEGER NOT NULL DEFAULT 0,
			max_quota_mb INTEGER NOT NULL DEFAULT 0,
			dkim_enabled INTEGER NOT NULL DEFAULT 0,
			dkim_selector TEXT NOT NULL DEFAULT '',
			dmarc_enabled INTEGER NOT NULL DEFAULT 0,
			mtasts_enabled INTEGER NOT NULL DEFAULT 0,
			catchall_address TEXT NOT NULL DEFAULT '',
			abuse_contact TEXT NOT NULL DEFAULT '',
			labels TEXT NOT NULL DEFAULT '',
			mailbox_count INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_mailboxes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			local_part TEXT NOT NULL,
			email TEXT UNIQUE NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			password_hash TEXT NOT NULL,
			auth_scheme TEXT NOT NULL DEFAULT 'argon2id',
			mfa_enabled INTEGER NOT NULL DEFAULT 0,
			mfa_secret TEXT NOT NULL DEFAULT '',
			app_passwords TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			quota_mb INTEGER NOT NULL DEFAULT 0,
			used_bytes INTEGER NOT NULL DEFAULT 0,
			msg_count INTEGER NOT NULL DEFAULT 0,
			is_admin INTEGER NOT NULL DEFAULT 0,
			is_forwarder INTEGER NOT NULL DEFAULT 0,
			forward_to TEXT NOT NULL DEFAULT '',
			labels TEXT NOT NULL DEFAULT '',
			send_limit_per_hour INTEGER NOT NULL DEFAULT 0,
			recv_limit_per_hour INTEGER NOT NULL DEFAULT 0,
			last_login DATETIME,
			last_ip TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_aliases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			from_addr TEXT NOT NULL,
			to_addr TEXT NOT NULL,
			active INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			deleted_at DATETIME
		)`,
		// Admin enterprise v2 — Account Classes (mailbox
		// service classes: standard / shared / room /
		// equipment / distribution-list). Used by the
		// admin UI to assign a service class to a
		// mailbox and to derive quotas / send limits /
		// feature gates from the class.
		`CREATE TABLE IF NOT EXISTS coremail_account_classes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			default_quota_mb INTEGER NOT NULL DEFAULT 1024,
			max_quota_mb INTEGER NOT NULL DEFAULT 5120,
			max_send_per_hour INTEGER NOT NULL DEFAULT 500,
			max_recv_per_hour INTEGER NOT NULL DEFAULT 5000,
			allow_external_forwarding INTEGER NOT NULL DEFAULT 1,
			allow_imap INTEGER NOT NULL DEFAULT 1,
			allow_pop3 INTEGER NOT NULL DEFAULT 1,
			allow_jmap INTEGER NOT NULL DEFAULT 1,
			allow_webmail INTEGER NOT NULL DEFAULT 1,
			is_admin_class INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			UNIQUE(tenant_id, name)
		)`,
		// Admin enterprise v2 — Domain Groups (groups of
		// domains for bulk operations). The group holds a
		// label and a list of member domain ids. Admin
		// UI exposes the group for batch mailbox / DNS /
		// backup operations.
		`CREATE TABLE IF NOT EXISTS coremail_domain_groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			color TEXT NOT NULL DEFAULT '#1f6feb',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			UNIQUE(tenant_id, name)
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_domain_group_members (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			group_id INTEGER NOT NULL,
			domain_id INTEGER NOT NULL,
			created_at DATETIME NOT NULL,
			UNIQUE(group_id, domain_id)
		)`,
		// Admin enterprise v2 — Mailing Lists (a list is
		// an address that fans out to N subscribers).
		// Different from a static alias because the
		// subscriber list is editable through the admin
		// UI and supports moderation / archives /
		// subscription policy flags. The address lives
		// in coremail_aliases; the subscriber set lives
		// here.
		`CREATE TABLE IF NOT EXISTS coremail_mailing_lists (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			domain_id INTEGER NOT NULL,
			address TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			moderation_required INTEGER NOT NULL DEFAULT 0,
			archive_enabled INTEGER NOT NULL DEFAULT 0,
			subscription_policy TEXT NOT NULL DEFAULT 'closed',
			max_members INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'active',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			UNIQUE(domain_id, address)
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_mailing_list_members (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			list_id INTEGER NOT NULL,
			address TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT 'subscriber',
			created_at DATETIME NOT NULL,
			UNIQUE(list_id, address)
		)`,
		// Admin enterprise v2 — Public Folders (a folder
		// on a shared mailbox that other mailboxes can
		// subscribe to via IMAP). `owner_mailbox_id` is
		// the source mailbox; members are joined through
		// coremail_public_folder_members.
		`CREATE TABLE IF NOT EXISTS coremail_public_folders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			owner_mailbox_id INTEGER NOT NULL,
			folder_path TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			read_only INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			UNIQUE(owner_mailbox_id, folder_path)
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_public_folder_members (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			folder_id INTEGER NOT NULL,
			mailbox_id INTEGER NOT NULL,
			permission TEXT NOT NULL DEFAULT 'readonly',
			created_at DATETIME NOT NULL,
			UNIQUE(folder_id, mailbox_id)
		)`,
		// Admin enterprise v2 — Admin Groups (RBAC).
		// Distinct from coremail_domain_groups; admin
		// groups govern which admin actions an admin
		// user is allowed to perform. Membership is in
		// coremail_admin_group_members. The grant list
		// for a group is a comma-separated list of
		// permission tokens (e.g. "domains.read",
		// "domains.write", "backups.delete",
		// "admin.users.write"). The enforcement layer
		// in handlers reads the comma list from
		// coremail_admin_groups and rejects requests
		// whose action is not in the union of grants for
		// the caller's groups.
		`CREATE TABLE IF NOT EXISTS coremail_admin_groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			grants TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			UNIQUE(tenant_id, name)
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_admin_group_members (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			group_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			created_at DATETIME NOT NULL,
			UNIQUE(group_id, user_id)
		)`,
		// Admin enterprise v2 — ACL Rules. Per-mailbox
		// access control: which IPs / networks may reach
		// the mailbox for IMAP / POP3 / SMTP submission,
		// and which IPs are explicitly denied. The UI
		// exposes a list; enforcement happens in the
		// SMTP / IMAP / POP3 listener gates.
		`CREATE TABLE IF NOT EXISTS coremail_acl_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			scope TEXT NOT NULL DEFAULT 'global',
			scope_target TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT 'allow',
			protocol TEXT NOT NULL DEFAULT 'all',
			source TEXT NOT NULL DEFAULT '',
			priority INTEGER NOT NULL DEFAULT 100,
			note TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		// Admin enterprise v2 — Log Collection Rules.
		// Drives what gets shipped from the local syslog
		// / journald to the centralised log server (and
		// to which one). The run-time collector reads
		// this table on a 30s tick.
		`CREATE TABLE IF NOT EXISTS coremail_log_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT 'journald',
			severity TEXT NOT NULL DEFAULT 'info',
			match_pattern TEXT NOT NULL DEFAULT '',
			destination TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		// Admin enterprise v2 — Quarantine. Quarantined
		// messages are kept here for review by the admin.
		// The compliance package already defines
		// coremail_quarantine; we extend it via a
		// separate `coremail_quarantine_index` table
		// that stores the message-id, recipient,
		// reason, severity, and admin actions.
		`CREATE TABLE IF NOT EXISTS coremail_quarantine_index (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			message_id TEXT NOT NULL,
			recipient TEXT NOT NULL DEFAULT '',
			sender TEXT NOT NULL DEFAULT '',
			subject TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			severity TEXT NOT NULL DEFAULT 'low',
			status TEXT NOT NULL DEFAULT 'held',
			resolved_at DATETIME,
			resolved_by TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_dkim_config (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain TEXT UNIQUE NOT NULL,
			selector TEXT NOT NULL DEFAULT 'default',
			private_key_pem TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS mfa_recovery_codes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			code_hash TEXT NOT NULL,
			used_at DATETIME,
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		// Security events for login protection.
		`CREATE TABLE IF NOT EXISTS security_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ip TEXT NOT NULL DEFAULT '',
			email TEXT NOT NULL DEFAULT '',
			event_type TEXT NOT NULL DEFAULT '',
			count INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		// Admin Enterprise v2 — Acceptance & Routing
		// rules. Distinct from coremail_acl_rules (which is
		// the per-mailbox access list at the protocol layer);
		// these are admin-scoped "what should I do when this
		// sender sends to this recipient" decisions. Each
		// rule has a priority (lower number = applied first)
		// and an action (accept / reject / redirect /
		// hold). The runtime engine (when wired) walks the
		// rules in priority order for every incoming
		// message. Until that engine exists, the table is
		// the source of truth and the admin UI exposes
		// list/create/update/delete + an audit trail.
		`CREATE TABLE IF NOT EXISTS coremail_acceptance_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL DEFAULT '',
			priority INTEGER NOT NULL DEFAULT 100,
			enabled INTEGER NOT NULL DEFAULT 1,
			scope TEXT NOT NULL DEFAULT 'global',
			scope_target TEXT NOT NULL DEFAULT '',
			sender_pattern TEXT NOT NULL DEFAULT '',
			recipient_pattern TEXT NOT NULL DEFAULT '',
			source_ip_cidr TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT 'accept',
			redirect_to TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		// Admin Enterprise v2 — admin-scoped Incoming
		// Message Rules. Distinct from per-mailbox webmail
		// rules at /api/v1/webmail/rules: these are
		// tenant-level filter rules applied before
		// per-mailbox rules. Each rule moves / labels /
		// forwards / discards a message based on field
		// patterns. The runtime engine (when wired) walks
		// the table once per incoming message after
		// acceptance & routing. Until then, the table is
		// the source of truth and the admin UI exposes
		// list/create/update/delete + an audit trail.
		`CREATE TABLE IF NOT EXISTS coremail_incoming_msg_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL DEFAULT '',
			priority INTEGER NOT NULL DEFAULT 100,
			enabled INTEGER NOT NULL DEFAULT 1,
			field TEXT NOT NULL DEFAULT 'subject',
			operator TEXT NOT NULL DEFAULT 'contains',
			value TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT 'move',
			action_target TEXT NOT NULL DEFAULT '',
			apply_to TEXT NOT NULL DEFAULT 'all',
			stop_processing INTEGER NOT NULL DEFAULT 0,
			note TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		// Admin Enterprise v2 — Migration Sources.
		// Describes an external IMAP / POP3 / JMAP server
		// to migrate mailboxes from. The migration engine
		// (internal/migration) reads this table when
		// starting a new job and uses the credentials to
		// authenticate. Passwords live in
		// coremail_migration_source_passwords (separate
		// table) so the source listing never echoes the
		// secret.
		`CREATE TABLE IF NOT EXISTS coremail_migration_sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL,
			kind TEXT NOT NULL DEFAULT 'imap',
			host TEXT NOT NULL DEFAULT '',
			port INTEGER NOT NULL DEFAULT 993,
			username TEXT NOT NULL DEFAULT '',
			use_tls INTEGER NOT NULL DEFAULT 1,
			allow_insecure INTEGER NOT NULL DEFAULT 0,
			default_base_folder TEXT NOT NULL DEFAULT 'INBOX',
			verify_hostname TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT '',
			has_secret INTEGER NOT NULL DEFAULT 0,
			last_test_status TEXT NOT NULL DEFAULT '',
			last_test_at DATETIME,
			last_test_message TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			UNIQUE(tenant_id, name)
		)`,
		// Migration source credentials — stored separately
		// so the list/GET endpoint never returns them. The
		// secret is encrypted at rest using the same helper
		// (config.EncryptString) used for license keys and
		// other sensitive material. The migration engine
		// reads this table on job start.
		`CREATE TABLE IF NOT EXISTS coremail_migration_source_secrets (
			source_id INTEGER PRIMARY KEY,
			password_enc TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL
		)`,
		// Admin Enterprise v2 — FTP / SFTP backup target
		// configuration. Setting `enabled = 1` instructs the
		// backup post-processor to mirror finished archives
		// to the configured remote. Passwords are stored
		// separately in coremail_backup_target_secrets; the
		// listing endpoint never returns the password
		// itself, only a "configured" boolean. The runtime
		// transfer path is left honestly unwired until a
		// release that includes a tested FTP / SFTP client;
		// the table and the admin UI exist so operators can
		// store their target now and have it picked up when
		// the runtime lands.
		`CREATE TABLE IF NOT EXISTS coremail_backup_targets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL,
			kind TEXT NOT NULL DEFAULT 'ftp',
			host TEXT NOT NULL DEFAULT '',
			port INTEGER NOT NULL DEFAULT 21,
			username TEXT NOT NULL DEFAULT '',
			path TEXT NOT NULL DEFAULT '/',
			enabled INTEGER NOT NULL DEFAULT 0,
			verify_hostname INTEGER NOT NULL DEFAULT 1,
			has_secret INTEGER NOT NULL DEFAULT 0,
			last_test_status TEXT NOT NULL DEFAULT '',
			last_test_at DATETIME,
			last_test_message TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			UNIQUE(tenant_id, name)
		)`,
		// Encrypted password / key material for FTP / SFTP
		// backup targets. The secret column is the encrypted
		// blob; the listing endpoint never returns the
		// stored value.
		`CREATE TABLE IF NOT EXISTS coremail_backup_target_secrets (
			target_id INTEGER PRIMARY KEY,
			password_enc TEXT NOT NULL DEFAULT '',
			private_key_path TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL
		)`,
		// Admin Enterprise v2 — uploaded / imported TLS
		// certificates. The runtime loads its cert from
		// cfg.CoreMail.TLSCertFile (live config). The import
		// action writes the operator-supplied PEMs to disk
		// (path: a per-host dir under /etc/orvix/tls/admin),
		// inserts a row here, and surfaces it in the admin
		// UI. The reload action triggers a ListenerRegistry
		// re-bind if the active listener uses the same
		// file.
		`CREATE TABLE IF NOT EXISTS coremail_uploaded_certificates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL,
			cert_path TEXT NOT NULL DEFAULT '',
			key_path TEXT NOT NULL DEFAULT '',
			common_name TEXT NOT NULL DEFAULT '',
			sans TEXT NOT NULL DEFAULT '',
			issuer TEXT NOT NULL DEFAULT '',
			serial_number TEXT NOT NULL DEFAULT '',
			not_before DATETIME,
			not_after DATETIME,
			fingerprint_sha256 TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'unknown',
			created_by INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME,
			UNIQUE(tenant_id, name)
		)`,
	}

	// Execute table creation statements
	for _, sql := range sqls {
		_, err := sqlDB.ExecContext(ctx, sql)
		if err != nil {
			return fmt.Errorf("failed to execute table creation: %w", err)
		}
	}

	if err := migrateCoremailMailboxSchema(ctx, sqlDB); err != nil {
		return err
	}

	if err := migrateUsersMFASchema(ctx, sqlDB); err != nil {
		return err
	}
	if err := migrateUsersProfileSchema(ctx, sqlDB); err != nil {
		return err
	}
	if err := migrateInvoicesSchema(ctx, sqlDB); err != nil {
		return err
	}

	// Create indexes
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_licenses_deleted_at ON licenses(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_feature_flags_deleted_at ON feature_flags(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_module_versions_deleted_at ON module_versions(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_tenants_deleted_at ON tenants(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_resellers_deleted_at ON resellers(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_l_dap_configs_deleted_at ON l_dap_configs(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_s_s_o_configs_deleted_at ON s_s_o_configs(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_alert_configs_deleted_at ON alert_configs(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_firewall_rules_deleted_at ON firewall_rules(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_firewall_logs_deleted_at ON firewall_logs(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_invoices_tenant_issued ON invoices(tenant_id, issued_at)`,
		`CREATE INDEX IF NOT EXISTS idx_invoices_tenant_status ON invoices(tenant_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_invoices_subscription ON invoices(subscription_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_invoices_provider ON invoices(provider, provider_invoice_id)`,
		`CREATE INDEX IF NOT EXISTS idx_guardian_logs_deleted_at ON guardian_logs(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_heal_histories_deleted_at ON heal_histories(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_provisioned_domains_deleted_at ON provisioned_domains(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_coremail_audit_timestamp ON coremail_audit(timestamp)`,
		`CREATE INDEX IF NOT EXISTS idx_coremail_audit_actor ON coremail_audit(actor, timestamp)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_deleted_at ON sessions(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_update_histories_deleted_at ON update_histories(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_guardian_logs_message_id ON guardian_logs(message_id)`,
		`CREATE INDEX IF NOT EXISTS idx_l_dap_configs_tenant_id ON l_dap_configs(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_s_s_o_configs_tenant_id ON s_s_o_configs(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_alert_configs_tenant_id ON alert_configs(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tenants_reseller_id ON tenants(reseller_id)`,
		`CREATE INDEX IF NOT EXISTS idx_users_deleted_at ON users(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_users_email ON users(email)`,
		`CREATE INDEX IF NOT EXISTS idx_domains_deleted_at ON domains(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_domains_tenant_id ON domains(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_mailboxes_deleted_at ON mailboxes(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_mailboxes_tenant_id ON mailboxes(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_mailboxes_domain_id ON mailboxes(domain_id)`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_deleted_at ON api_keys(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_user_id ON api_keys(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_coremail_mailboxes_domain_id ON coremail_mailboxes(domain_id)`,
		`CREATE INDEX IF NOT EXISTS idx_coremail_mailboxes_email ON coremail_mailboxes(email)`,
		`CREATE INDEX IF NOT EXISTS idx_coremail_aliases_domain_id ON coremail_aliases(domain_id)`,
		`CREATE INDEX IF NOT EXISTS idx_coremail_aliases_from ON coremail_aliases(from_addr)`,
		// Admin enterprise v2 indexes
		`CREATE INDEX IF NOT EXISTS idx_account_classes_tenant ON coremail_account_classes(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_domain_groups_tenant ON coremail_domain_groups(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_domain_group_members_group ON coremail_domain_group_members(group_id)`,
		`CREATE INDEX IF NOT EXISTS idx_mailing_lists_domain ON coremail_mailing_lists(domain_id)`,
		`CREATE INDEX IF NOT EXISTS idx_mailing_list_members_list ON coremail_mailing_list_members(list_id)`,
		`CREATE INDEX IF NOT EXISTS idx_public_folders_owner ON coremail_public_folders(owner_mailbox_id)`,
		`CREATE INDEX IF NOT EXISTS idx_public_folder_members_folder ON coremail_public_folder_members(folder_id)`,
		`CREATE INDEX IF NOT EXISTS idx_admin_groups_tenant ON coremail_admin_groups(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_admin_group_members_group ON coremail_admin_group_members(group_id)`,
		`CREATE INDEX IF NOT EXISTS idx_acl_rules_tenant ON coremail_acl_rules(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_log_rules_tenant ON coremail_log_rules(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_quarantine_index_tenant ON coremail_quarantine_index(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_quarantine_index_status ON coremail_quarantine_index(status)`,
		`CREATE INDEX IF NOT EXISTS idx_dkim_domain ON coremail_dkim_config(domain)`,
		// Admin Enterprise v2 indexes — v2 follow-up.
		`CREATE INDEX IF NOT EXISTS idx_acceptance_rules_tenant ON coremail_acceptance_rules(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_acceptance_rules_priority ON coremail_acceptance_rules(priority)`,
		`CREATE INDEX IF NOT EXISTS idx_incoming_msg_rules_tenant ON coremail_incoming_msg_rules(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_incoming_msg_rules_priority ON coremail_incoming_msg_rules(priority)`,
		`CREATE INDEX IF NOT EXISTS idx_migration_sources_tenant ON coremail_migration_sources(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_backup_targets_tenant ON coremail_backup_targets(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_uploaded_certs_tenant ON coremail_uploaded_certificates(tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_security_events_email ON security_events(email)`,
		`CREATE INDEX IF NOT EXISTS idx_security_events_event_type ON security_events(event_type)`,
		`CREATE INDEX IF NOT EXISTS idx_security_events_created ON security_events(created_at)`,
	}

	// Execute index creation statements
	for _, idx := range indexes {
		_, err := sqlDB.ExecContext(ctx, idx)
		if err != nil {
			// Log but don't fail on index creation (indexes might already exist)
			fmt.Printf("WARN: index creation failed (may already exist): %v\n", err)
		}
	}

	// Verify critical tables exist
	criticalTables := []string{"licenses", "feature_flags", "sessions", "coremail_audit", "users", "domains", "mailboxes", "api_keys"}
	for _, table := range criticalTables {
		var count int
		err := sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count)
		if err != nil {
			return fmt.Errorf("failed to verify table %s: %w", table, err)
		}
		if count == 0 {
			return fmt.Errorf("critical table '%s' was not created", table)
		}
	}

	return nil
}

func migrateCoremailMailboxSchema(ctx context.Context, db *sql.DB) error {
	columns, err := sqliteColumns(ctx, db, "coremail_mailboxes")
	if err != nil {
		return fmt.Errorf("inspect coremail_mailboxes schema: %w", err)
	}

	additions := []struct {
		name string
		sql  string
	}{
		{"tenant_id", "ALTER TABLE coremail_mailboxes ADD COLUMN tenant_id INTEGER NOT NULL DEFAULT 0"},
		{"name", "ALTER TABLE coremail_mailboxes ADD COLUMN name TEXT NOT NULL DEFAULT ''"},
		{"auth_scheme", "ALTER TABLE coremail_mailboxes ADD COLUMN auth_scheme TEXT NOT NULL DEFAULT 'argon2id'"},
		{"mfa_enabled", "ALTER TABLE coremail_mailboxes ADD COLUMN mfa_enabled INTEGER NOT NULL DEFAULT 0"},
		{"mfa_secret", "ALTER TABLE coremail_mailboxes ADD COLUMN mfa_secret TEXT NOT NULL DEFAULT ''"},
		{"app_passwords", "ALTER TABLE coremail_mailboxes ADD COLUMN app_passwords TEXT NOT NULL DEFAULT ''"},
		{"status", "ALTER TABLE coremail_mailboxes ADD COLUMN status TEXT NOT NULL DEFAULT 'active'"},
		{"quota_mb", "ALTER TABLE coremail_mailboxes ADD COLUMN quota_mb INTEGER NOT NULL DEFAULT 0"},
		{"msg_count", "ALTER TABLE coremail_mailboxes ADD COLUMN msg_count INTEGER NOT NULL DEFAULT 0"},
		{"is_forwarder", "ALTER TABLE coremail_mailboxes ADD COLUMN is_forwarder INTEGER NOT NULL DEFAULT 0"},
		{"forward_to", "ALTER TABLE coremail_mailboxes ADD COLUMN forward_to TEXT NOT NULL DEFAULT ''"},
		{"labels", "ALTER TABLE coremail_mailboxes ADD COLUMN labels TEXT NOT NULL DEFAULT ''"},
		{"send_limit_per_hour", "ALTER TABLE coremail_mailboxes ADD COLUMN send_limit_per_hour INTEGER NOT NULL DEFAULT 0"},
		{"recv_limit_per_hour", "ALTER TABLE coremail_mailboxes ADD COLUMN recv_limit_per_hour INTEGER NOT NULL DEFAULT 0"},
		{"last_login", "ALTER TABLE coremail_mailboxes ADD COLUMN last_login DATETIME"},
		{"last_ip", "ALTER TABLE coremail_mailboxes ADD COLUMN last_ip TEXT NOT NULL DEFAULT ''"},
		{"deleted_at", "ALTER TABLE coremail_mailboxes ADD COLUMN deleted_at DATETIME"},
		{"class_id", "ALTER TABLE coremail_mailboxes ADD COLUMN class_id INTEGER NOT NULL DEFAULT 0"},
		{"allow_smtp", "ALTER TABLE coremail_mailboxes ADD COLUMN allow_smtp INTEGER NOT NULL DEFAULT 1"},
		{"allow_imap", "ALTER TABLE coremail_mailboxes ADD COLUMN allow_imap INTEGER NOT NULL DEFAULT 1"},
		{"allow_pop3", "ALTER TABLE coremail_mailboxes ADD COLUMN allow_pop3 INTEGER NOT NULL DEFAULT 1"},
		{"allow_jmap", "ALTER TABLE coremail_mailboxes ADD COLUMN allow_jmap INTEGER NOT NULL DEFAULT 1"},
		{"allow_webmail", "ALTER TABLE coremail_mailboxes ADD COLUMN allow_webmail INTEGER NOT NULL DEFAULT 1"},
	}

	for _, addition := range additions {
		if columns[addition.name] {
			continue
		}
		if _, err := db.ExecContext(ctx, addition.sql); err != nil {
			return fmt.Errorf("add coremail_mailboxes.%s: %w", addition.name, err)
		}
		columns[addition.name] = true
	}

	if columns["quota"] && columns["quota_mb"] {
		if _, err := db.ExecContext(ctx, "UPDATE coremail_mailboxes SET quota_mb = quota WHERE quota_mb = 0 AND quota IS NOT NULL"); err != nil {
			return fmt.Errorf("backfill coremail_mailboxes.quota_mb: %w", err)
		}
	}
	if columns["active"] && columns["status"] {
		if _, err := db.ExecContext(ctx, "UPDATE coremail_mailboxes SET status = CASE WHEN active = 0 THEN 'suspended' ELSE 'active' END WHERE status = ''"); err != nil {
			return fmt.Errorf("backfill coremail_mailboxes.status: %w", err)
		}
	}

	return nil
}

func migrateUsersMFASchema(ctx context.Context, db *sql.DB) error {
	columns, err := sqliteColumns(ctx, db, "users")
	if err != nil {
		return fmt.Errorf("inspect users schema: %w", err)
	}

	additions := []struct {
		name string
		sql  string
	}{
		{"mfa_enabled", "ALTER TABLE users ADD COLUMN mfa_enabled INTEGER NOT NULL DEFAULT 0"},
		{"mfa_secret", "ALTER TABLE users ADD COLUMN mfa_secret TEXT NOT NULL DEFAULT ''"},
		{"pending_mfa_secret", "ALTER TABLE users ADD COLUMN pending_mfa_secret TEXT NOT NULL DEFAULT ''"},
		{"pending_mfa_secret_raw", "ALTER TABLE users ADD COLUMN pending_mfa_secret_raw TEXT NOT NULL DEFAULT ''"},
		{"mfa_secret_raw", "ALTER TABLE users ADD COLUMN mfa_secret_raw TEXT NOT NULL DEFAULT ''"},
		{"mfa_label", "ALTER TABLE users ADD COLUMN mfa_label TEXT NOT NULL DEFAULT ''"},
	}

	for _, addition := range additions {
		if columns[addition.name] {
			continue
		}
		if _, err := db.ExecContext(ctx, addition.sql); err != nil {
			return fmt.Errorf("add users.%s: %w", addition.name, err)
		}
		columns[addition.name] = true
	}

	return nil
}

func migrateInvoicesSchema(ctx context.Context, db *sql.DB) error {
	// Check if table exists first.
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name='invoices'")
	if err != nil {
		return fmt.Errorf("check invoices table: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil // Table doesn't exist yet — CREATE TABLE in batch handles it.
	}

	columns, err := sqliteColumns(ctx, db, "invoices")
	if err != nil {
		return fmt.Errorf("inspect invoices schema: %w", err)
	}

	additions := []struct {
		name string
		sql  string
	}{
		{"provider_event_created_at", "ALTER TABLE invoices ADD COLUMN provider_event_created_at DATETIME"},
		{"provider_event_id", "ALTER TABLE invoices ADD COLUMN provider_event_id TEXT NOT NULL DEFAULT ''"},
		{"provider_updated_at", "ALTER TABLE invoices ADD COLUMN provider_updated_at DATETIME"},
	}

	for _, addition := range additions {
		if columns[addition.name] {
			continue
		}
		if _, err := db.ExecContext(ctx, addition.sql); err != nil {
			return fmt.Errorf("add invoices.%s: %w", addition.name, err)
		}
		columns[addition.name] = true
	}

	return nil
}

func migrateUsersProfileSchema(ctx context.Context, db *sql.DB) error {
	columns, err := sqliteColumns(ctx, db, "users")
	if err != nil {
		return fmt.Errorf("inspect users schema for profile columns: %w", err)
	}

	additions := []struct {
		name string
		sql  string
	}{
		{"full_name", "ALTER TABLE users ADD COLUMN full_name TEXT NOT NULL DEFAULT ''"},
		{"locale", "ALTER TABLE users ADD COLUMN locale TEXT NOT NULL DEFAULT ''"},
		{"timezone", "ALTER TABLE users ADD COLUMN timezone TEXT NOT NULL DEFAULT ''"},
		{"theme", "ALTER TABLE users ADD COLUMN theme TEXT NOT NULL DEFAULT 'dark'"},
	}

	for _, addition := range additions {
		if columns[addition.name] {
			continue
		}
		if _, err := db.ExecContext(ctx, addition.sql); err != nil {
			return fmt.Errorf("add users.%s: %w", addition.name, err)
		}
		columns[addition.name] = true
	}

	return nil
}

func sqliteColumns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}
