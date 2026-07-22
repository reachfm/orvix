package models

import (
	"time"

	"gorm.io/gorm"
)

// License represents an OrvixEM license key and its entitlements.
type License struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	KeyHash       string    `gorm:"uniqueIndex;size:128;not null" json:"key_hash"`
	Tier          string    `gorm:"size:20;not null;index" json:"tier"`
	IssuedAt      time.Time `gorm:"not null" json:"issued_at"`
	ExpiresAt     time.Time `gorm:"not null;index" json:"expires_at"`
	MaxDomains    int       `gorm:"default:10" json:"max_domains"`
	MaxMailboxes  int       `gorm:"default:500" json:"max_mailboxes"`
	HardwareID    string    `gorm:"size:256;index" json:"hardware_id"`
	Metadata      string    `gorm:"type:text" json:"metadata"`
	Active        bool      `gorm:"default:true;index" json:"active"`
	LastValidated time.Time `json:"last_validated"`
	OfflineUntil  time.Time `json:"offline_until"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Tenant represents a multi-tenant organization or reseller.
type Tenant struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Name         string    `gorm:"size:255;not null;index" json:"name"`
	Slug         string    `gorm:"uniqueIndex;size:128;not null" json:"slug"`
	Domain       string    `gorm:"size:255;index" json:"domain"`
	Tier         string    `gorm:"size:20;not null;default:'smb';index" json:"tier"`
	MaxDomains   int       `gorm:"default:10" json:"max_domains"`
	MaxMailboxes int       `gorm:"default:500" json:"max_mailboxes"`
	IsReseller   bool      `gorm:"default:false;index" json:"is_reseller"`
	ParentID     *uint     `json:"parent_id"`
	Active       bool      `gorm:"default:true;index" json:"active"`
	Settings     string    `gorm:"type:text" json:"settings"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Domain represents an email domain managed by OrvixEM.
type Domain struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	TenantID      uint      `gorm:"index:idx_domains_tenant_status;not null" json:"tenant_id"`
	Name          string    `gorm:"uniqueIndex;size:255;not null" json:"name"`
	Status        string    `gorm:"size:20;default:'pending';index:idx_domains_tenant_status" json:"status"`
	DKIMSelector  string    `gorm:"size:128;index" json:"dkim_selector"`
	DKIMPublicKey string    `gorm:"type:text" json:"dkim_public_key"`
	SPFRecord     string    `gorm:"type:text" json:"spf_record"`
	DMARCPolicy   string    `gorm:"size:20;default:'none'" json:"dmarc_policy"`
	CatchAll      string    `gorm:"size:255" json:"catch_all"`
	SSLEnabled    bool      `gorm:"default:true" json:"ssl_enabled"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// User represents a mail user or administrator.
type User struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	TenantID     uint       `gorm:"index:idx_users_tenant;not null" json:"tenant_id"`
	DomainID     uint       `gorm:"index:idx_users_domain" json:"domain_id"`
	Username     string     `gorm:"size:128;not null;index" json:"username"`
	Email        string     `gorm:"uniqueIndex;size:255;not null" json:"email"`
	PasswordHash string     `gorm:"size:255;not null" json:"-"`
	Role         string     `gorm:"size:20;default:'user';index" json:"role"`
	QuotaMB      int64      `gorm:"default:1024" json:"quota_mb"`
	UsedMB       int64      `gorm:"default:0" json:"used_mb"`
	IsActive     bool       `gorm:"default:true;index" json:"is_active"`
	IsAdmin      bool       `gorm:"default:false;index" json:"is_admin"`
	TOTPSecret   string     `gorm:"size:64" json:"-"`
	TOTPEnabled  bool       `gorm:"default:false" json:"totp_enabled"`
	BackupCodes  string     `gorm:"type:text" json:"-"`
	LastLoginAt  *time.Time `json:"last_login_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// UserSettings stores per-user preferences.
type UserSettings struct {
	ID             uint   `gorm:"primaryKey" json:"id"`
	UserID         uint   `gorm:"uniqueIndex;not null" json:"user_id"`
	Theme          string `gorm:"size:20;default:'dark'" json:"theme"`
	Density        string `gorm:"size:20;default:'comfortable'" json:"density"`
	Signature      string `gorm:"type:text" json:"signature"`
	VacationMsg    string `gorm:"type:text" json:"vacation_msg"`
	VacationActive bool   `gorm:"default:false;index" json:"vacation_active"`
	Language       string `gorm:"size:10;default:'en'" json:"language"`
	Timezone       string `gorm:"size:64;default:'UTC'" json:"timezone"`
}

// FeatureFlag represents a configurable feature flag.
type FeatureFlag struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Key          string    `gorm:"uniqueIndex;size:128;not null" json:"key"`
	Name         string    `gorm:"size:255;not null" json:"name"`
	Description  string    `gorm:"type:text" json:"description"`
	Enabled      bool      `gorm:"default:false;index" json:"enabled"`
	Tier         string    `gorm:"size:20;index" json:"tier"`
	IsGlobal     bool      `gorm:"default:true;index" json:"is_global"`
	TenantID     *uint     `json:"tenant_id"`
	IsKillSwitch bool      `gorm:"default:false;index" json:"is_kill_switch"`
	Source       string    `gorm:"size:20;default:'license'" json:"source"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// AuditLog records immutable audit events.
type AuditLog struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	UserID     *uint     `gorm:"index:idx_audit_user" json:"user_id"`
	TenantID   *uint     `gorm:"index:idx_audit_tenant" json:"tenant_id"`
	Action     string    `gorm:"size:64;not null;index:idx_audit_action" json:"action"`
	Resource   string    `gorm:"size:128;not null;index:idx_audit_resource" json:"resource"`
	ResourceID string    `gorm:"size:64;index" json:"resource_id"`
	IP         string    `gorm:"size:45;index" json:"ip"`
	Details    string    `gorm:"type:text" json:"details"`
	CreatedAt  time.Time `gorm:"index:idx_audit_created" json:"created_at"`
}

// APIKey stores hashed API keys for programmatic access.
type APIKey struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	UserID      uint       `gorm:"index:idx_apikeys_user;not null" json:"user_id"`
	KeyHash     string     `gorm:"uniqueIndex;size:128;not null" json:"-"`
	Name        string     `gorm:"size:128;not null" json:"name"`
	Permissions string     `gorm:"type:text" json:"permissions"`
	LastUsedAt  *time.Time `gorm:"index" json:"last_used_at"`
	ExpiresAt   *time.Time `gorm:"index" json:"expires_at"`
	Active      bool       `gorm:"default:true;index" json:"active"`
	CreatedAt   time.Time  `json:"created_at"`
}

// Session tracks active user sessions.
type Session struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	UserID      uint      `gorm:"index:idx_sessions_user;not null" json:"user_id"`
	TokenHash   string    `gorm:"uniqueIndex;size:128;not null" json:"-"`
	RefreshHash string    `gorm:"size:128;index" json:"-"`
	IP          string    `gorm:"size:45;index" json:"ip"`
	UserAgent   string    `gorm:"size:512" json:"user_agent"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `gorm:"index:idx_sessions_expires" json:"expires_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

// Migration tracks applied database migrations.
type Migration struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"uniqueIndex;size:255;not null" json:"name"`
	Batch     int       `gorm:"not null;index" json:"batch"`
	AppliedAt time.Time `gorm:"index" json:"applied_at"`
}

// Message represents an email message metadata (actual body stored in Stalwart).
type Message struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	MailboxID  uint      `gorm:"index:idx_messages_mailbox;not null" json:"mailbox_id"`
	UID        uint      `gorm:"index:idx_messages_uid" json:"uid"`
	Flags      string    `gorm:"size:64;index" json:"flags"`
	Size       int64     `gorm:"default:0" json:"size"`
	Subject    string    `gorm:"size:512;index" json:"subject"`
	FromAddr   string    `gorm:"size:255;index" json:"from_addr"`
	ToAddrs    string    `gorm:"type:text" json:"to_addrs"`
	Date       time.Time `gorm:"index:idx_messages_date" json:"date"`
	BodyText   string    `gorm:"type:text" json:"body_text"`
	BodyHTML   string    `gorm:"type:text" json:"body_html"`
	RawPath    string    `gorm:"size:512" json:"raw_path"`
	StalwartID string    `gorm:"size:128;index" json:"stalwart_id"`
	CreatedAt  time.Time `json:"created_at"`
}

// Attachment represents a file attached to an email message.
type Attachment struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	MessageID   uint   `gorm:"index:idx_attachments_message;not null" json:"message_id"`
	Filename    string `gorm:"size:255;not null" json:"filename"`
	ContentType string `gorm:"size:128;index" json:"content_type"`
	Size        int64  `gorm:"default:0;index" json:"size"`
	StoragePath string `gorm:"size:512" json:"storage_path"`
}

// Folder represents a mail folder/mailbox.
type Folder struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	UserID      uint   `gorm:"index:idx_folders_user;not null" json:"user_id"`
	Name        string `gorm:"size:128;not null;index" json:"name"`
	ParentID    *uint  `json:"parent_id"`
	FolderType  string `gorm:"size:20;default:'custom';index" json:"folder_type"`
	UnreadCount int    `gorm:"default:0" json:"unread_count"`
	TotalCount  int    `gorm:"default:0" json:"total_count"`
}

// MailQueue represents a queued outbound email tracked by OrvixEM.
type MailQueue struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	FromAddr   string     `gorm:"size:255;not null;index:idx_queue_from" json:"from_addr"`
	ToAddr     string     `gorm:"size:255;not null;index:idx_queue_to" json:"to_addr"`
	Domain     string     `gorm:"size:255;index:idx_queue_domain" json:"domain"`
	Status     string     `gorm:"size:20;default:'queued';index:idx_queue_status" json:"status"`
	Attempts   int        `gorm:"default:0" json:"attempts"`
	NextRetry  *time.Time `gorm:"index:idx_queue_retry" json:"next_retry"`
	LastError  string     `gorm:"type:text" json:"last_error"`
	StalwartID string     `gorm:"size:128" json:"stalwart_id"`
	CreatedAt  time.Time  `gorm:"index:idx_queue_created" json:"created_at"`
}

// Calendar represents a user's calendar.
type Calendar struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index:idx_calendars_user;not null" json:"user_id"`
	Name      string    `gorm:"size:128;not null" json:"name"`
	Color     string    `gorm:"size:7;default:'#4F7CFF'" json:"color"`
	IsShared  bool      `gorm:"default:false;index" json:"is_shared"`
	CreatedAt time.Time `json:"created_at"`
}

// Event represents a calendar event.
type Event struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	CalendarID  uint      `gorm:"index:idx_events_calendar;not null" json:"calendar_id"`
	Title       string    `gorm:"size:255;not null;index" json:"title"`
	Description string    `gorm:"type:text" json:"description"`
	StartAt     time.Time `gorm:"not null;index:idx_events_start" json:"start_at"`
	EndAt       time.Time `gorm:"not null;index:idx_events_end" json:"end_at"`
	IsRecurring bool      `gorm:"default:false;index" json:"is_recurring"`
	RRule       string    `gorm:"size:255" json:"rrule"`
	Location    string    `gorm:"size:255;index" json:"location"`
	CreatedAt   time.Time `json:"created_at"`
}

// Contact represents a contact entry.
type Contact struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index:idx_contacts_user;not null" json:"user_id"`
	Name      string    `gorm:"size:255;index" json:"name"`
	Email     string    `gorm:"size:255;index:idx_contacts_email" json:"email"`
	Phone     string    `gorm:"size:64;index" json:"phone"`
	Company   string    `gorm:"size:255;index" json:"company"`
	Notes     string    `gorm:"type:text" json:"notes"`
	VCard     string    `gorm:"type:text" json:"vcard"`
	CreatedAt time.Time `json:"created_at"`
}

// ContactGroup represents a group of contacts.
type ContactGroup struct {
	ID     uint   `gorm:"primaryKey" json:"id"`
	UserID uint   `gorm:"index:idx_contactgroups_user;not null" json:"user_id"`
	Name   string `gorm:"size:128;not null;index" json:"name"`
}

// ProvisioningJob tracks domain provisioning operations.
type ProvisioningJob struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	DomainID       uint       `gorm:"index:idx_prov_domain;not null" json:"domain_id"`
	DomainName     string     `gorm:"size:255;not null;index:idx_prov_domainname" json:"domain_name"`
	Type           string     `gorm:"size:20;not null;default:'provision';index" json:"type"`
	Status         string     `gorm:"size:20;default:'pending';index:idx_prov_status" json:"status"`
	StalwartResult string     `gorm:"size:20" json:"stalwart_result"`
	DNSSetupStatus string     `gorm:"size:20;default:'pending'" json:"dns_setup_status"`
	StartedAt      *time.Time `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	ErrorMessage   string     `gorm:"type:text" json:"error_message"`
	CreatedAt      time.Time  `gorm:"index" json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Webhook stores registered webhook endpoints for event notifications.
type Webhook struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index:idx_webhooks_user;not null" json:"user_id"`
	URL       string    `gorm:"size:512;not null" json:"url"`
	Secret    string    `gorm:"size:128;not null" json:"-"`
	Events    string    `gorm:"type:text;not null" json:"events"`
	Active    bool      `gorm:"default:true;index" json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// FirewallRule stores mail firewall rules in the database.
type FirewallRule struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"size:128;not null;uniqueIndex" json:"name"`
	Field     string    `gorm:"size:64;not null;index" json:"field"`
	Operator  string    `gorm:"size:32;not null;index" json:"operator"`
	Value     string    `gorm:"size:255;not null;index" json:"value"`
	Action    string    `gorm:"size:32;not null;index" json:"action"`
	Priority  int       `gorm:"default:0;index" json:"priority"`
	Enabled   bool      `gorm:"default:true;index" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// GeoBlock stores country-level geo-blocking rules.
type GeoBlock struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Country   string    `gorm:"size:2;not null;uniqueIndex" json:"country"`
	Blocked   bool      `gorm:"default:true;index" json:"blocked"`
	Reason    string    `gorm:"size:255" json:"reason"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DistributionList represents a mailing list / distribution list.
type DistributionList struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	DomainID    uint      `gorm:"index:idx_distlist_domain;not null" json:"domain_id"`
	Name        string    `gorm:"size:128;not null" json:"name"`
	Email       string    `gorm:"size:255;not null;uniqueIndex" json:"email"`
	Description string    `gorm:"type:text" json:"description"`
	IsPublic    bool      `gorm:"default:false;index" json:"is_public"`
	IsActive    bool      `gorm:"default:true;index" json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// DistributionListMember represents a member of a distribution list.
type DistributionListMember struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	DistributionListID uint      `gorm:"index:idx_distlistmem_list;not null" json:"distribution_list_id"`
	Email              string    `gorm:"size:255;not null;index" json:"email"`
	IsModerator        bool      `gorm:"default:false;index" json:"is_moderator"`
	CreatedAt          time.Time `json:"created_at"`
}

// Resource represents a bookable resource (room, equipment, etc.).
type Resource struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	DomainID  uint      `gorm:"index:idx_resource_domain;not null" json:"domain_id"`
	Name      string    `gorm:"size:128;not null;index" json:"name"`
	Email     string    `gorm:"size:255;not null;uniqueIndex" json:"email"`
	Type      string    `gorm:"size:32;not null;index" json:"type"`
	Capacity  int       `gorm:"default:0" json:"capacity"`
	Location  string    `gorm:"size:255" json:"location"`
	IsActive  bool      `gorm:"default:true;index" json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PublicFolder represents a shared folder accessible to multiple users.
type PublicFolder struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	DomainID    uint      `gorm:"index:idx_pubfolder_domain;not null" json:"domain_id"`
	Name        string    `gorm:"size:128;not null;index" json:"name"`
	Email       string    `gorm:"size:255;not null;uniqueIndex" json:"email"`
	Description string    `gorm:"type:text" json:"description"`
	ParentID    *uint     `json:"parent_id"`
	IsActive    bool      `gorm:"default:true;index" json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// PublicFolderAccess represents access permissions for a public folder.
type PublicFolderAccess struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	PublicFolderID uint      `gorm:"index:idx_pubfolderaccess_folder;not null" json:"public_folder_id"`
	UserID         *uint     `json:"user_id"`
	GroupID        *uint     `json:"group_id"`
	Permission     string    `gorm:"size:32;not null;index" json:"permission"`
	CreatedAt      time.Time `json:"created_at"`
}

// RoutingRule represents advanced email routing rules.
type RoutingRule struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	DomainID  uint      `gorm:"index:idx_routing_domain;not null" json:"domain_id"`
	Name      string    `gorm:"size:128;not null" json:"name"`
	Priority  int       `gorm:"default:0;index" json:"priority"`
	Condition string    `gorm:"type:text;not null" json:"condition"`
	Action    string    `gorm:"size:32;not null;index" json:"action"`
	Target    string    `gorm:"size:255;not null" json:"target"`
	IsEnabled bool      `gorm:"default:true;index" json:"is_enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DLPolicy represents a Data Loss Prevention policy.
type DLPolicy struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	DomainID    uint      `gorm:"index:idx_dlp_domain;not null" json:"domain_id"`
	Name        string    `gorm:"size:128;not null" json:"name"`
	Description string    `gorm:"type:text" json:"description"`
	Pattern     string    `gorm:"type:text;not null" json:"pattern"`
	Action      string    `gorm:"size:32;not null;index" json:"action"`
	Severity    string    `gorm:"size:32;default:'medium';index" json:"severity"`
	IsEnabled   bool      `gorm:"default:true;index" json:"is_enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// DLPViolation represents a detected DLP violation.
type DLPViolation struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	PolicyID    uint      `gorm:"index:idx_dlpviolation_policy;not null" json:"policy_id"`
	MessageID   uint      `gorm:"index" json:"message_id"`
	SenderEmail string    `gorm:"size:255;not null;index" json:"sender_email"`
	Recipient   string    `gorm:"size:255;not null;index" json:"recipient"`
	Action      string    `gorm:"size:32;not null;index" json:"action"`
	Details     string    `gorm:"type:text" json:"details"`
	CreatedAt   time.Time `gorm:"index" json:"created_at"`
}

// SLAMetric represents an SLA metric record.
type SLAMetric struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	DomainID   uint      `gorm:"index:idx_sla_domain;not null" json:"domain_id"`
	MetricType string    `gorm:"size:32;not null;index" json:"metric_type"`
	Value      float64   `gorm:"not null" json:"value"`
	Target     float64   `gorm:"not null" json:"target"`
	Period     string    `gorm:"size:32;not null;index" json:"period"`
	RecordedAt time.Time `gorm:"index:idx_sla_recorded;not null" json:"recorded_at"`
}

// LDAPConfig represents LDAP/Active Directory synchronization configuration.
type LDAPConfig struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	DomainID     uint       `gorm:"index:idx_ldap_domain;not null" json:"domain_id"`
	ServerURL    string     `gorm:"size:255;not null" json:"server_url"`
	BindDN       string     `gorm:"size:255;not null" json:"bind_dn"`
	BindPassword string     `gorm:"size:255;not null" json:"-"`
	BaseDN       string     `gorm:"size:255;not null" json:"base_dn"`
	UserFilter   string     `gorm:"size:255;not null" json:"user_filter"`
	GroupFilter  string     `gorm:"size:255" json:"group_filter"`
	SyncInterval int        `gorm:"default:3600" json:"sync_interval"`
	LastSyncAt   *time.Time `json:"last_sync_at"`
	IsActive     bool       `gorm:"default:true;index" json:"is_active"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// SSOConfig represents Single Sign-On configuration.
type SSOConfig struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	DomainID     uint      `gorm:"index:idx_sso_domain;not null" json:"domain_id"`
	Provider     string    `gorm:"size:32;not null;index" json:"provider"`
	ClientID     string    `gorm:"size:255;not null" json:"client_id"`
	ClientSecret string    `gorm:"size:255;not null" json:"-"`
	MetadataURL  string    `gorm:"size:512" json:"metadata_url"`
	EntityID     string    `gorm:"size:255" json:"entity_id"`
	ACSEndpoint  string    `gorm:"size:512" json:"acs_endpoint"`
	SLOEndpoint  string    `gorm:"size:512" json:"slo_endpoint"`
	IsActive     bool      `gorm:"default:true;index" json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// SpamListEntry represents a whitelist or blacklist entry.
type SpamListEntry struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	ListType  string    `gorm:"size:20;not null;index:idx_spam_list" json:"list_type"`
	EntryType string    `gorm:"size:20;not null" json:"type"`
	Value     string    `gorm:"size:255;not null;index" json:"value"`
	Reason    string    `gorm:"type:text" json:"reason"`
	DomainID  uint      `gorm:"default:0" json:"domain_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&License{},
		&Tenant{},
		&Domain{},
		&User{},
		&UserSettings{},
		&FeatureFlag{},
		&AuditLog{},
		&APIKey{},
		&Session{},
		&Migration{},
		&Message{},
		&Attachment{},
		&Folder{},
		&MailQueue{},
		&Calendar{},
		&Event{},
		&Contact{},
		&ContactGroup{},
		&ProvisioningJob{},
		&Webhook{},
		&FirewallRule{},
		&GeoBlock{},
		&DistributionList{},
		&DistributionListMember{},
		&Resource{},
		&PublicFolder{},
		&PublicFolderAccess{},
		&RoutingRule{},
		&DLPolicy{},
		&DLPViolation{},
		&SLAMetric{},
		&LDAPConfig{},
		&SSOConfig{},
		&SpamListEntry{},
	)
}
