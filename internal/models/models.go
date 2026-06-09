package models

import (
	"context"
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
			ip TEXT NOT NULL,
			user_agent TEXT,
			expires_at DATETIME NOT NULL
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
			UNIQUE(email, deleted_at)
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
			expires_at DATETIME,
			last_used_at DATETIME,
			active INTEGER NOT NULL DEFAULT 1
		)`,
		// CoreMail tables
		`CREATE TABLE IF NOT EXISTS coremail_domains (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			plan TEXT NOT NULL DEFAULT 'smb',
			active INTEGER NOT NULL DEFAULT 1,
			max_mailboxes INTEGER NOT NULL DEFAULT 0,
			max_quota INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_mailboxes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL,
			local_part TEXT NOT NULL,
			email TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			quota INTEGER NOT NULL DEFAULT 0,
			used_bytes INTEGER NOT NULL DEFAULT 0,
			active INTEGER NOT NULL DEFAULT 1,
			is_admin INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			deleted_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_aliases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL,
			from_addr TEXT NOT NULL,
			to_addr TEXT NOT NULL,
			active INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
	}

	// Execute table creation statements
	for _, sql := range sqls {
		_, err := sqlDB.ExecContext(ctx, sql)
		if err != nil {
			return fmt.Errorf("failed to execute table creation: %w", err)
		}
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
		`CREATE INDEX IF NOT EXISTS idx_guardian_logs_deleted_at ON guardian_logs(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_heal_histories_deleted_at ON heal_histories(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_provisioned_domains_deleted_at ON provisioned_domains(deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_coremail_audit_timestamp ON coremail_audit(timestamp)`,
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
