// Package mailcontrol is the Platform Super Admin mail-control bounded
// context. It orchestrates the existing production admin services
// (internal/admin/domain, internal/admin/mailbox) and owns the
// platform-side persistence for aliases, groups, and bulk operations
// that no dedicated production service exists for.
//
// Ownership model:
//   - A Platform Super Admin (role platform_super_admin, tenant_id NULL)
//     calls ONLY the /platform/* routes, all platformMW-gated.
//   - Every platform mail-control request MUST name an explicit target
//     tenant_id; the platform service never derives, infers, or defaults
//     a tenant.
//   - Every repository query and mutation is scoped by tenant_id and
//     verifies resource ownership inside the same operation.
//   - Support Access is a separate, temporary, scoped mechanism and is
//     NOT the PSA authorization model.
package mailcontrol

import "time"

// ── Platform domain views ──────────────────────────────────────────

// PlatformDomain is the platform-owned projection of a domain across
// all tenants. It wraps the admin domain detail plus platform-relevant
// counters and policy state. Field names match the admin service wire
// shape so the platform handler never invents a contract.
type PlatformDomain struct {
	ID             uint      `json:"id"`
	TenantID       uint      `json:"tenant_id"`
	Name           string    `json:"name"`
	Status         string    `json:"status"`
	Plan           string    `json:"plan"`
	Description    string    `json:"description,omitempty"`
	MailboxCount   int       `json:"mailbox_count"`
	AliasCount     int       `json:"alias_count"`
	DKIMEnabled    bool      `json:"dkim_enabled"`
	DKIMSelector   string    `json:"dkim_selector,omitempty"`
	DMARCEnabled   bool      `json:"dmarc_enabled"`
	MailAccessMode string    `json:"mail_access_mode"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	// Version is the real optimistic-concurrency counter from
	// coremail_domains.version. Callers pass this value back as
	// expected_version on guarded mutations (e.g. .../deactivate); a
	// stale value is rejected with the existing typed conflict.
	Version int `json:"version"`
}

// PlatformDomainList is the stable list envelope.
type PlatformDomainList struct {
	Domains []PlatformDomain `json:"domains"`
	Total   int64            `json:"total"`
	Limit   int              `json:"limit"`
	Offset  int              `json:"offset"`
}

// PlatformDomainFilter controls platform-wide domain listing.
type PlatformDomainFilter struct {
	TenantID uint
	Search   string
	Status   string
	Limit    int
	Offset   int
}

// ── Platform mailbox views ─────────────────────────────────────────

type PlatformMailbox struct {
	ID         uint   `json:"id"`
	TenantID   uint   `json:"tenant_id"`
	DomainID   uint   `json:"domain_id"`
	DomainName string `json:"domain"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	IsAdmin    bool   `json:"is_admin"`
	QuotaMB    int64  `json:"quota_mb"`
	UsedBytes  int64  `json:"used_bytes"`
	// MailAccessMode is the CONFIGURED per-mailbox policy;
	// EffectiveMailAccessMode is the RESOLVED policy after inherit
	// falls back to the domain. The two are distinct fields so they
	// can never be confused.
	MailAccessMode          string `json:"mail_access_mode"`
	EffectiveMailAccessMode string `json:"effective_mail_access_mode"`
	// Version is the mailbox's real optimistic-concurrency counter (the
	// same value the canonical admin/mailbox service already tracks and
	// SetMailboxAccessMode already requires as expected_version). It
	// must be read here — never fabricated or defaulted — so a caller
	// can perform a genuine guarded mutation after only a list/get.
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type PlatformMailboxList struct {
	Mailboxes []PlatformMailbox `json:"mailboxes"`
	Total     int64             `json:"total"`
	Limit     int               `json:"limit"`
	Offset    int               `json:"offset"`
}

type PlatformMailboxFilter struct {
	TenantID uint
	DomainID uint
	Search   string
	Status   string
	Limit    int
	Offset   int
}

// ── Platform mailbox creation ──────────────────────────────────────

// PlatformCreateMailboxRequest is the Platform Super Admin mailbox
// creation contract (POST /api/v1/platform/mailboxes/:tenant_id).
// mail_access_mode is REQUIRED on this route and accepts only
// internal_only or internal_external — "inherit" is deliberately not
// accepted here because the whole point of the platform route is an
// explicit per-mailbox decision. Tenant-admin and Public API create
// paths keep omitting the field (persisting inherit) unchanged.
type PlatformCreateMailboxRequest struct {
	Email               string `json:"email"`
	Name                string `json:"name,omitempty"`
	Password            string `json:"password"`
	QuotaMB             int64  `json:"quota_mb,omitempty"`
	SendLimitPerHour    int    `json:"send_limit_per_hour,omitempty"`
	ForcePasswordChange bool   `json:"force_password_change,omitempty"`
	MailAccessMode      string `json:"mail_access_mode"`
}

// PlatformCreateMailboxResult is the safe post-create contract. It
// NEVER carries the password (or any hash): the caller-supplied
// password is used once to derive the Argon2id hash and is never
// stored, logged, or returned.
type PlatformCreateMailboxResult struct {
	Mailbox PlatformMailbox `json:"mailbox"`
}

// PlatformSetMailboxAccessModeRequest is the guarded access-mode
// mutation body. expected_version is the mailbox's current version
// from a prior read; a stale value is a precondition failure.
type PlatformSetMailboxAccessModeRequest struct {
	MailAccessMode  string `json:"mail_access_mode"`
	ExpectedVersion int    `json:"expected_version"`
}

// PlatformMailboxAccessModeResult reports the post-mutation state:
// the configured value and the resolved effective value, which must
// never be confused.
type PlatformMailboxAccessModeResult struct {
	ID                      uint   `json:"id"`
	MailAccessMode          string `json:"mail_access_mode"`
	EffectiveMailAccessMode string `json:"effective_mail_access_mode"`
	Version                 int    `json:"version"`
}

// ── Platform alias views ───────────────────────────────────────────

type PlatformAlias struct {
	ID        uint      `json:"id"`
	TenantID  uint      `json:"tenant_id"`
	DomainID  uint      `json:"domain_id"`
	FromAddr  string    `json:"from_addr"`
	ToAddr    string    `json:"to_addr"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type PlatformAliasList struct {
	Aliases []PlatformAlias `json:"aliases"`
	Total   int64           `json:"total"`
	Limit   int             `json:"limit"`
	Offset  int             `json:"offset"`
}

type PlatformAliasFilter struct {
	TenantID    uint
	DomainID    uint
	Search      string
	Destination string
	Limit       int
	Offset      int
}

// ── Platform group views ───────────────────────────────────────────

type PlatformGroup struct {
	ID          uint      `json:"id"`
	TenantID    uint      `json:"tenant_id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	MemberCount int       `json:"member_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type PlatformGroupList struct {
	Groups []PlatformGroup `json:"groups"`
	Total  int64           `json:"total"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

type PlatformGroupFilter struct {
	TenantID uint
	Search   string
	Limit    int
	Offset   int
}

// ── Bulk mailbox operations ────────────────────────────────────────

type BulkMailboxAction string

const (
	BulkMailboxSuspend    BulkMailboxAction = "suspend"
	BulkMailboxReactivate BulkMailboxAction = "reactivate"
	BulkMailboxDelete     BulkMailboxAction = "delete"
)

type BulkMailboxRequest struct {
	TenantID uint
	DomainID uint
	IDs      []uint
	Action   BulkMailboxAction
	Reason   string
}

type BulkMailboxResult struct {
	Total     int                  `json:"total"`
	Succeeded int                  `json:"succeeded"`
	Failed    []BulkMailboxFailure `json:"failed,omitempty"`
}

type BulkMailboxFailure struct {
	ID    uint   `json:"id"`
	Error string `json:"error"`
}

// Confirmation phrases for destructive platform actions.
const (
	ConfirmMailboxPurge = "PURGE-MAILBOX-"
)

var ErrInvalidConfirmation = &mailControlError{"invalid or missing confirmation"}
var ErrTenantRequired = &mailControlError{"an explicit target tenant_id is required"}

type mailControlError struct{ msg string }

func (e *mailControlError) Error() string { return e.msg }

// ── Platform domain creation ───────────────────────────────────────

// PlatformCreateDomainRequest is the Platform Super Admin domain
// creation contract (POST /api/v1/platform/domains/:tenant_id).
// mail_access_mode is deliberately ABSENT: mail-access policy is a
// per-mailbox concern in this release, so a domain create cannot
// pin it. The legacy domain mail-access APIs remain operational for
// compatibility (deprecated).
type PlatformCreateDomainRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status,omitempty"`
	// Limits are optional typed allocation controls (see
	// admin/domain.DomainLimits sentinel semantics).
	Limits *PlatformDomainLimits `json:"limits,omitempty"`
	// DKIM optionally requests in-transaction DKIM provisioning. The
	// generated private key is never returned, logged, or audited.
	DKIM *PlatformDKIMOptions `json:"dkim,omitempty"`
}

type PlatformDomainLimits struct {
	MaxMailboxes          *int   `json:"max_mailboxes,omitempty"`
	MaxAliases            *int   `json:"max_aliases,omitempty"`
	DefaultMailboxQuotaMB *int64 `json:"default_mailbox_quota_mb,omitempty"`
	MaxMailboxQuotaMB     *int64 `json:"max_mailbox_quota_mb,omitempty"`
}

type PlatformDKIMOptions struct {
	Generate bool   `json:"generate"`
	Selector string `json:"selector,omitempty"`
}

// PlatformDNSRequirement is one publishable DNS record the tenant
// must create. It carries only public values (name/type/value/ttl/
// priority) — never DKIM private material or provider credentials.
type PlatformDNSRequirement struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Value    string `json:"value"`
	TTL      int    `json:"ttl"`
	Priority int    `json:"priority,omitempty"`
	Required bool   `json:"required"`
	Purpose  string `json:"purpose,omitempty"`
}

// PlatformCreateDomainResult is the safe post-create contract. It
// contains only publishable data: the created domain, the resolved
// effective limits, the PUBLIC DKIM DNS record, and the DNS records
// the tenant must publish. No private key, password, or token is ever
// placed on this struct.
type PlatformCreateDomainResult struct {
	Domain           PlatformDomain           `json:"domain"`
	EffectiveLimits  PlatformEffectiveLimits  `json:"effective_limits"`
	DKIM             *PlatformDKIMResult      `json:"dkim,omitempty"`
	DNSRequirements  []PlatformDNSRequirement `json:"dns_requirements,omitempty"`
	DNSNextStep      string                   `json:"dns_next_step"`
	PublicDNSChanged bool                     `json:"public_dns_changed"`
	Plan             *PlatformPlanSummary     `json:"plan,omitempty"`
	Idempotent       bool                     `json:"idempotent"`
}

// PlatformEffectiveLimits is the resolved allocation view (mirrors
// admin/domain.EffectiveLimits without importing its Go type into the
// JSON contract — see PlatformDomain for the same reasoning).
type PlatformEffectiveLimits struct {
	MaxMailboxes                   int   `json:"max_mailboxes"`
	MaxMailboxesUnlimited          bool  `json:"max_mailboxes_unlimited"`
	MaxMailboxesInherited          bool  `json:"max_mailboxes_inherited"`
	MaxAliases                     int   `json:"max_aliases"`
	MaxAliasesUnlimited            bool  `json:"max_aliases_unlimited"`
	MaxAliasesInherited            bool  `json:"max_aliases_inherited"`
	DefaultMailboxQuotaMB          int64 `json:"default_mailbox_quota_mb"`
	MaxMailboxQuotaMB              int64 `json:"max_mailbox_quota_mb"`
	MaxMailboxQuotaUnlimited       bool  `json:"max_mailbox_quota_mb_unlimited"`
	MaxMailboxQuotaInherited       bool  `json:"max_mailbox_quota_mb_inherited"`
	DefaultMailboxQuotaMBInherited bool  `json:"default_mailbox_quota_mb_inherited"`
}

// PlatformDKIMResult carries only the PUBLIC DKIM DNS record.
type PlatformDKIMResult struct {
	Selector      string `json:"selector"`
	PublicDNSTxt  string `json:"public_dns_txt"`
	DNSRecordName string `json:"dns_record_name"`
	// Version is set only by the version-guarded generate/rotate
	// route; zero elsewhere.
	Version int `json:"version,omitempty"`
}

// PlatformDomainDNSResult is the response for
// GET /platform/domains/:tenant_id/:id/dns — a read-only snapshot of
// an EXISTING domain's public DNS/DKIM configuration. Every field is
// either read directly from the domain row or built by the same
// canonical DNS-requirements generator CreatePlatformDomain uses;
// nothing here is fabricated, and no key material — public or
// private — is ever generated by this route. DKIMConfigured=false
// means DKIMSelector/DKIMDNSRecordName/DKIMPublicDNSTxt are all empty
// (an honest not-configured state, never a placeholder value).
type PlatformDomainDNSResult struct {
	TenantID          uint                     `json:"tenant_id"`
	DomainID          uint                     `json:"domain_id"`
	Domain            string                   `json:"domain"`
	Version           int                      `json:"version"`
	Status            string                   `json:"status"`
	DKIMConfigured    bool                     `json:"dkim_configured"`
	DKIMSelector      string                   `json:"dkim_selector,omitempty"`
	DKIMDNSRecordName string                   `json:"dkim_dns_record_name,omitempty"`
	DKIMPublicDNSTxt  string                   `json:"dkim_public_dns_txt,omitempty"`
	DNSRequirements   []PlatformDNSRequirement `json:"dns_requirements,omitempty"`
	DNSNextStep       string                   `json:"dns_next_step,omitempty"`
}

// PlatformPlanSummary is the post-create plan/usage view.
type PlatformPlanSummary struct {
	Plan                  string `json:"plan"`
	MaxDomains            int    `json:"max_domains"`
	MaxDomainsUnlimited   bool   `json:"max_domains_unlimited"`
	DomainsUsed           int    `json:"domains_used"`
	RemainingDomains      *int   `json:"remaining_domains"`
	MaxMailboxes          int    `json:"max_mailboxes"`
	MaxMailboxesUnlimited bool   `json:"max_mailboxes_unlimited"`
	MailboxesUsed         int    `json:"mailboxes_used"`
	RemainingMailboxes    *int   `json:"remaining_mailboxes"`
}

// Platform errors for provisioning.
var (
	ErrTenantNotFound  = &mailControlError{"tenant not found"}
	ErrTenantSuspended = &mailControlError{"tenant is suspended or inactive"}
	ErrTenantDeleted   = &mailControlError{"tenant is deleted"}
	ErrProvisionFailed = &mailControlError{"provisioning failed"}
)
