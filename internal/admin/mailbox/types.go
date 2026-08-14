package mailbox

import (
	"strings"
	"time"
)

// MailAccessMode is the canonical per-mailbox mail-access policy
// (MAILBOX-ACCESS-MODE-PHASE1). The three persisted values are:
//
//	MailAccessInherit          — resolve through the domain's policy
//	MailAccessInternalOnly     — local-only send and receive
//	MailAccessInternalExternal — local and external send and receive
//
// "inherit" is the default for every existing mailbox, which is what
// keeps pre-existing installations behaving exactly as before: their
// effective mode continues to come from the domain column.
type MailAccessMode string

const (
	MailAccessInherit          MailAccessMode = "inherit"
	MailAccessInternalOnly     MailAccessMode = "internal_only"
	MailAccessInternalExternal MailAccessMode = "internal_external"
)

// ParseMailAccessMode normalizes and validates a requested mailbox
// access mode. Unlike the domain mode parser, "inherit" is a legal
// persisted value here. Unknown/free-text values are rejected so no
// write path can persist an invalid state.
func ParseMailAccessMode(s string) (MailAccessMode, bool) {
	v := MailAccessMode(strings.ToLower(strings.TrimSpace(s)))
	switch v {
	case MailAccessInherit, MailAccessInternalOnly, MailAccessInternalExternal:
		return v, true
	default:
		return "", false
	}
}

// NormalizeMailAccessMode maps an empty value (a request that omitted
// the field, or a legacy row) to the canonical "inherit" value. It is
// applied at every service/repository write boundary.
func NormalizeMailAccessMode(s string) MailAccessMode {
	if strings.TrimSpace(s) == "" {
		return MailAccessInherit
	}
	return MailAccessMode(strings.ToLower(strings.TrimSpace(s)))
}

type AdminMailboxStatus string

const (
	AdminMailboxActive    AdminMailboxStatus = "active"
	AdminMailboxDisabled  AdminMailboxStatus = "disabled"
	AdminMailboxSuspended AdminMailboxStatus = "suspended"
	AdminMailboxDeleted   AdminMailboxStatus = "deleted"
)

// MailboxAuthSchemeArgon2id is the auth_scheme stored for mailboxes created
// or reset by this service. It matches coremail's default argon2id scheme so
// SMTP/IMAP/POP3/JMAP authentication verifies the stored hash.
const MailboxAuthSchemeArgon2id = "argon2id"

type AdminMailbox struct {
	ID         uint               `json:"id"`
	DomainID   uint               `json:"domain_id"`
	TenantID   uint               `json:"tenant_id"`
	Email      string             `json:"email"`
	LocalPart  string             `json:"local_part"`
	Name       string             `json:"name"`
	Status     AdminMailboxStatus `json:"status"`
	QuotaMB    int64              `json:"quota_mb"`
	UsedBytes  int64              `json:"used_bytes"`
	MsgCount   int                `json:"msg_count"`
	IsAdmin    bool               `json:"is_admin"`
	AllowSMTP  bool               `json:"allow_smtp"`
	AllowIMAP  bool               `json:"allow_imap"`
	AllowPOP3  bool               `json:"allow_pop3"`
	AllowJMAP  bool               `json:"allow_jmap"`
	MFAEnabled bool               `json:"mfa_enabled"`
	SendLimit  int                `json:"send_limit_per_hour"`
	// MailAccessMode is the CONFIGURED per-mailbox policy ("inherit",
	// "internal_only", "internal_external"). EffectiveMailAccessMode is
	// the RESOLVED policy after inherit falls back to the domain —
	// the value the delivery paths actually enforce. The two are
	// deliberately distinct fields so they can never be confused.
	MailAccessMode          string     `json:"mail_access_mode"`
	EffectiveMailAccessMode string     `json:"effective_mail_access_mode"`
	Version                 int        `json:"version"`
	LastLogin               *time.Time `json:"last_login"`
	LastIP                  string     `json:"last_ip"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

type AdminMailboxResponse struct {
	Mailbox    AdminMailbox `json:"mailbox"`
	AliasCount int          `json:"alias_count,omitempty"`
}

// CreateMailboxRequest is the mailbox creation contract. MailAccessMode
// is OPTIONAL and defaults to "inherit" when omitted — the tenant-admin
// and Public API callers that predate per-mailbox policy are unchanged,
// and their mailboxes keep resolving through the domain. The platform
// create-mailbox route requires an explicit mode at the handler layer.
type CreateMailboxRequest struct {
	Email               string `json:"email"`
	Password            string `json:"password"`
	Name                string `json:"name,omitempty"`
	QuotaMB             int64  `json:"quota_mb,omitempty"`
	SendLimit           int    `json:"send_limit_per_hour,omitempty"`
	ForcePasswordChange bool   `json:"force_password_change,omitempty"`
	// MailAccessMode is a pointer so "omitted" (nil -> inherit) is
	// distinguishable from an explicit "inherit".
	MailAccessMode *string `json:"mail_access_mode,omitempty"`
}

type CreateMailboxResponse struct {
	Mailbox  AdminMailbox `json:"mailbox"`
	Password string       `json:"password,omitempty"`
}

type UpdateMailboxRequest struct {
	Name      *string `json:"name,omitempty"`
	QuotaMB   *int64  `json:"quota_mb,omitempty"`
	SendLimit *int    `json:"send_limit_per_hour,omitempty"`
	IsAdmin   *bool   `json:"is_admin,omitempty"`
	AllowSMTP *bool   `json:"allow_smtp,omitempty"`
	AllowIMAP *bool   `json:"allow_imap,omitempty"`
	AllowPOP3 *bool   `json:"allow_pop3,omitempty"`
	AllowJMAP *bool   `json:"allow_jmap,omitempty"`
	// MailAccessMode is validated at the service boundary; nil means
	// "unchanged".
	MailAccessMode *string `json:"mail_access_mode,omitempty"`
}

// SetMailboxAccessModeRequest is the guarded access-mode mutation
// contract. expected_version is the mailbox's current version (read
// from a prior GET); a stale value is rejected with a precondition
// failure so concurrent policy changes cannot silently overwrite each
// other.
type SetMailboxAccessModeRequest struct {
	MailAccessMode  string `json:"mail_access_mode"`
	ExpectedVersion int    `json:"expected_version"`
}

type MailboxFilter struct {
	DomainID *uint
	TenantID *uint
	Status   *AdminMailboxStatus
	Search   string
	Limit    int
	Offset   int
}

type BulkActionRequest struct {
	MailboxIDs []uint `json:"mailbox_ids"`
	Reason     string `json:"reason,omitempty"`
}
