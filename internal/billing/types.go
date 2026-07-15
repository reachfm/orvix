package billing

import "time"

type PlanID string

const (
	PlanFree       PlanID = "free"
	PlanStarter    PlanID = "starter"
	PlanBusiness   PlanID = "business"
	PlanEnterprise PlanID = "enterprise"
)

type BillingInterval string

const (
	IntervalMonthly BillingInterval = "monthly"
	IntervalYearly  BillingInterval = "yearly"
)

type SubscriptionStatus string

const (
	SubTrialing    SubscriptionStatus = "trialing"
	SubActive      SubscriptionStatus = "active"
	SubPastDue     SubscriptionStatus = "past_due"
	SubGracePeriod SubscriptionStatus = "grace_period"
	SubSuspended   SubscriptionStatus = "suspended"
	SubCancelled   SubscriptionStatus = "cancelled"
	SubExpired     SubscriptionStatus = "expired"
)

type Plan struct {
	ID           PlanID    `gorm:"primaryKey;size:32" json:"id"`
	Name         string    `gorm:"not null" json:"name"`
	Description  string    `gorm:"type:text" json:"description"`
	PriceMonthly int64     `gorm:"not null;default:0" json:"price_monthly"`
	PriceYearly  int64     `gorm:"not null;default:0" json:"price_yearly"`
	MaxDomains   int       `gorm:"not null;default:1" json:"max_domains"`
	MaxMailboxes int       `gorm:"not null;default:5" json:"max_mailboxes"`
	StorageMB    int64     `gorm:"not null;default:1024" json:"storage_mb"`
	SendLimitDay int       `gorm:"not null;default:500" json:"send_limit_day"`
	Features     string    `gorm:"type:text" json:"features"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type PlanFeature string

const (
	FeatureCustomDomain    PlanFeature = "custom_domain"
	FeatureDKIM            PlanFeature = "dkim"
	FeatureMTASTS          PlanFeature = "mta_sts"
	FeatureAPI             PlanFeature = "api"
	FeatureTeam            PlanFeature = "team"
	FeatureGroups          PlanFeature = "groups"
	FeatureCatchAll        PlanFeature = "catch_all"
	FeatureMailForwarding  PlanFeature = "mail_forwarding"
	FeatureBackup          PlanFeature = "backup"
	FeatureAuditLog        PlanFeature = "audit_log"
	FeatureSSO             PlanFeature = "sso"
	FeatureMFA             PlanFeature = "mfa"
	FeatureSLA             PlanFeature = "sla"
	FeaturePrioritySupport PlanFeature = "priority_support"
)

type Subscription struct {
	ID                 uint               `gorm:"primaryKey" json:"id"`
	TenantID           uint               `gorm:"uniqueIndex;not null" json:"tenant_id"`
	PlanID             PlanID             `gorm:"not null;size:32" json:"plan_id"`
	Status             SubscriptionStatus `gorm:"not null;default:'trialing'" json:"status"`
	BillingInterval    BillingInterval    `gorm:"not null;default:'monthly'" json:"billing_interval"`
	TrialEndsAt        *time.Time         `json:"trial_ends_at"`
	CurrentPeriodStart time.Time          `gorm:"not null" json:"current_period_start"`
	CurrentPeriodEnd   time.Time          `gorm:"not null" json:"current_period_end"`
	CancelledAt        *time.Time         `json:"cancelled_at"`
	PastDueSince       *time.Time         `json:"past_due_since"`
	GracePeriodEndsAt  *time.Time         `json:"grace_period_ends_at"`
	SuspendedAt        *time.Time         `json:"suspended_at"`
	StorageMB          int64              `gorm:"not null;default:1024" json:"storage_mb"`
	SendLimitDay       int                `gorm:"not null;default:500" json:"send_limit_day"`
	Provider           string             `gorm:"size:32" json:"provider"`
	ProviderSubID      string             `gorm:"size:255" json:"provider_sub_id"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

type UsageRecord struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	TenantID       uint      `gorm:"index;not null" json:"tenant_id"`
	PeriodStart    time.Time `gorm:"not null" json:"period_start"`
	PeriodEnd      time.Time `gorm:"not null" json:"period_end"`
	MailboxesUsed  int       `gorm:"not null;default:0" json:"mailboxes_used"`
	DomainsUsed    int       `gorm:"not null;default:0" json:"domains_used"`
	StorageUsedMB  int64     `gorm:"not null;default:0" json:"storage_used_mb"`
	EmailsSent     int64     `gorm:"not null;default:0" json:"emails_sent"`
	EmailsReceived int64     `gorm:"not null;default:0" json:"emails_received"`
	APICalls       int64     `gorm:"not null;default:0" json:"api_calls"`
	CreatedAt      time.Time `json:"created_at"`
}

type QuotaCheckResult struct {
	Allowed   bool   `json:"allowed"`
	Limit     int    `json:"limit"`
	Used      int    `json:"used"`
	Remaining int    `json:"remaining"`
	Reason    string `json:"reason,omitempty"`
}
