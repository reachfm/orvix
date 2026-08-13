// Package retention implements Feature 17 (Milestone 14):
// hierarchical retention policy resolution, legal hold, and a
// dry-run/typed-confirmation purge planner. internal/compliance's
// existing LegalHold/RetentionPolicy GORM types are bare struct
// declarations with no service logic (no hierarchy resolution, no
// enforcement, no purge planner) — this package is the real
// implementation, deliberately named differently
// (internal/platform/retention, not internal/compliance) to avoid a
// package-name collision while that legacy package still exists.
package retention

import "time"

// PolicyLevel is the hierarchy tier a policy applies at. Resolution
// picks the MOST SPECIFIC level that has an applicable policy:
// mailbox beats domain beats tenant beats platform default.
type PolicyLevel string

const (
	LevelPlatform PolicyLevel = "platform"
	LevelTenant   PolicyLevel = "tenant"
	LevelDomain   PolicyLevel = "domain"
	LevelMailbox  PolicyLevel = "mailbox"
)

// levelRank gives each level a specificity rank — higher wins.
var levelRank = map[PolicyLevel]int{
	LevelPlatform: 0,
	LevelTenant:   1,
	LevelDomain:   2,
	LevelMailbox:  3,
}

// Policy is one retention rule at one hierarchy level, optionally
// scoped further to a message category (e.g. "sent", "spam",
// "trash") — an empty Category means "applies to every category not
// covered by a more specific category-scoped policy at the same
// level".
type Policy struct {
	ID              uint        `json:"id"`
	Level           PolicyLevel `json:"level"`
	TenantID        uint        `json:"tenant_id,omitempty"`
	DomainID        uint        `json:"domain_id,omitempty"`
	MailboxID       uint        `json:"mailbox_id,omitempty"`
	Category        string      `json:"category,omitempty"`
	RetentionDays   int         `json:"retention_days"` // 0 = retain indefinitely
	RecoveryDays    int         `json:"recovery_days"`  // window after deletion before permanent purge
	ArchiveEligible bool        `json:"archive_eligible"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
}

// LegalHold prevents purge for its Scope while active. Scope is a
// simple (kind, value) pair — "mailbox"/<id>, "tenant"/<id>,
// "domain"/<id> — kept as strings rather than several nullable
// columns so new scope kinds don't require a migration.
type LegalHold struct {
	ID        uint       `json:"id"`
	ScopeKind string     `json:"scope_kind"`
	ScopeID   uint       `json:"scope_id"`
	CaseRef   string     `json:"case_ref"`
	Reason    string     `json:"reason"`
	ActorID   uint       `json:"actor_id"`
	StartedAt time.Time  `json:"started_at"`
	EndsAt    *time.Time `json:"ends_at,omitempty"` // nil = indefinite, until explicitly released
	Released  bool       `json:"released"`
	CreatedAt time.Time  `json:"created_at"`
}

// IsActive reports whether the hold currently blocks purge.
func (h LegalHold) IsActive(now time.Time) bool {
	if h.Released {
		return false
	}
	if h.EndsAt != nil && !h.EndsAt.After(now) {
		return false
	}
	return true
}

// PurgePlan is the dry-run output: what WOULD be purged, never what
// WAS — Execute is a separate, explicitly confirmed call.
type PurgePlan struct {
	ScopeKind     string    `json:"scope_kind"`
	ScopeID       uint      `json:"scope_id"`
	EligibleCount int       `json:"eligible_count"`
	HeldCount     int       `json:"held_count"` // excluded from eligible because of an active legal hold
	GeneratedAt   time.Time `json:"generated_at"`
}

// ChainOfCustodyEvent is an append-only evidentiary record for an
// export/recovery operation — who, what, when, and a content hash, so
// the operation can later be proven not to have been tampered with.
type ChainOfCustodyEvent struct {
	ID          uint      `json:"id"`
	Operation   string    `json:"operation"` // "export", "recover", "purge"
	ScopeKind   string    `json:"scope_kind"`
	ScopeID     uint      `json:"scope_id"`
	ActorID     uint      `json:"actor_id"`
	ContentHash string    `json:"content_hash,omitempty"`
	RecordCount int       `json:"record_count"`
	CreatedAt   time.Time `json:"created_at"`
}
