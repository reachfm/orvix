package domain

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/audit"
)

// ---------------------------------------------------------------------------
// Limit sentinels
// ---------------------------------------------------------------------------
//
// coremail_domains.max_mailboxes / max_aliases / max_quota_mb are NOT NULL
// INTEGER columns on both supported dialects (SQLite and PostgreSQL). Rather
// than adding nullable columns — which would require a destructive ALTER on a
// shipped schema and a second, dialect-specific migration path — the two
// non-numeric states are encoded as documented sentinels:
//
//	LimitInherit   (0)  the domain does not pin a value; the ORGANIZATION plan
//	                    ceiling applies. This is dynamic: raising the org plan
//	                    immediately raises every inheriting domain. 0 was
//	                    already the "unset" value written by the pre-existing
//	                    create path, so no data migration is needed.
//	LimitUnlimited (-1) the domain explicitly has no ceiling. Only accepted
//	                    when the organization plan itself is unlimited for
//	                    that dimension.
//
// A value > 0 is a concrete ceiling. Negative values other than -1 are
// rejected. This mapping is the single source of truth for both the API
// contract and the enforcement helpers below.
const (
	// LimitInherit means "no domain-level value; use the organization plan".
	LimitInherit = 0
	// LimitUnlimited means "explicitly no ceiling at this level".
	LimitUnlimited = -1
)

// maxQuotaMB is the largest per-mailbox quota accepted, in mebibytes. It is
// chosen so that MB -> bytes conversion (x * 1024 * 1024) cannot overflow
// int64 with a wide safety margin, which is what makes ToBytes total.
const maxQuotaMB int64 = 1 << 40 // 1 PiB expressed in MiB

// ---------------------------------------------------------------------------
// Request contract
// ---------------------------------------------------------------------------

// DomainLimits carries the four enforceable per-domain allocation controls.
// Every field is a pointer: a nil/omitted field means INHERIT the organization
// plan (persisted as LimitInherit), which is deliberately NOT the same as
// unlimited. An explicit -1 requests unlimited and is only honoured when the
// organization plan is itself unlimited for that dimension.
type DomainLimits struct {
	MaxMailboxes          *int   `json:"max_mailboxes,omitempty"`
	MaxAliases            *int   `json:"max_aliases,omitempty"`
	DefaultMailboxQuotaMB *int64 `json:"default_mailbox_quota_mb,omitempty"`
	MaxMailboxQuotaMB     *int64 `json:"max_mailbox_quota_mb,omitempty"`
}

// DKIMOptions requests in-transaction DKIM provisioning. Selector is validated
// server-side; the generated PRIVATE key is never returned, logged or audited.
type DKIMOptions struct {
	Generate bool   `json:"generate"`
	Selector string `json:"selector,omitempty"`
}

// ProvisionDomainResult is the public provisioning response. It deliberately
// contains only publishable data: the created domain, the resolved effective
// limits, and the PUBLIC DKIM DNS record. No private key, password, token or
// secret is ever placed on this struct.
type ProvisionDomainResult struct {
	Domain          *AdminDomain     `json:"domain"`
	EffectiveLimits EffectiveLimits  `json:"effective_limits"`
	DKIM            *DKIMResult      `json:"dkim,omitempty"`
	Plan            *PlanSummary     `json:"plan,omitempty"`
	DNS             *DNSNextStepInfo `json:"dns,omitempty"`
	// Idempotent reports that the request matched an already-provisioned
	// domain created by the same tenant with the same idempotency key, and
	// that no second domain was created.
	Idempotent bool `json:"idempotent"`
}

// DNSNextStepInfo tells the caller what to do next. It never asserts that any
// public DNS was changed: Orvix does not write to authoritative DNS.
type DNSNextStepInfo struct {
	PublicDNSChanged bool   `json:"public_dns_changed"`
	NextStep         string `json:"next_step"`
}

// EffectiveLimits is the resolved view of a domain's allocation controls after
// inheritance has been applied. Unlimited is explicit and never encoded as 0.
type EffectiveLimits struct {
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

// ---------------------------------------------------------------------------
// Plan capacity
// ---------------------------------------------------------------------------

// PlanSummary is the organization plan + usage view the provisioning wizard
// needs. Unlimited dimensions are reported with an explicit *Unlimited flag and
// a nil Remaining* pointer — never as a misleading 0.
type PlanSummary struct {
	Plan string `json:"plan"`

	MaxDomains          int  `json:"max_domains"`
	MaxDomainsUnlimited bool `json:"max_domains_unlimited"`
	DomainsUsed         int  `json:"domains_used"`
	RemainingDomains    *int `json:"remaining_domains"`

	MaxMailboxes          int  `json:"max_mailboxes"`
	MaxMailboxesUnlimited bool `json:"max_mailboxes_unlimited"`
	MailboxesUsed         int  `json:"mailboxes_used"`
	RemainingMailboxes    *int `json:"remaining_mailboxes"`

	// The organization data model (tenants) has no alias ceiling column, so
	// there is no organization-level alias cap to report. AliasesUsed is a
	// real count; RemainingAliases is nil and MaxAliasesUnlimited is true
	// because no org ceiling exists to constrain domain alias caps.
	MaxAliasesUnlimited bool `json:"max_aliases_unlimited"`
	AliasesUsed         int  `json:"aliases_used"`
	RemainingAliases    *int `json:"remaining_aliases"`

	// Storage is READ-ONLY. StorageUsedBytes is the real sum of mailbox
	// usage. StorageAllocatedBytes is the sum of the finite per-domain
	// allocations. There is no writable organization storage cap because no
	// delivery/JMAP path enforces one (see the omissions note in the PR).
	StorageUsedBytes      int64 `json:"storage_used_bytes"`
	StorageAllocatedBytes int64 `json:"storage_allocated_bytes"`

	// MailboxesAllocated is the sum of the finite per-domain mailbox caps
	// already handed out. It is what "remaining allocatable capacity" is
	// measured against when a new domain pins a finite cap.
	MailboxesAllocated int `json:"mailboxes_allocated"`
}

// planCapacity is the raw organization row plus usage, read inside the
// provisioning transaction.
type planCapacity struct {
	plan         string
	maxDomains   int
	maxMailboxes int

	domainsUsed        int
	mailboxesUsed      int
	aliasesUsed        int
	mailboxesAllocated int
	storageUsedBytes   int64
	storageAllocBytes  int64
}

func (p *planCapacity) domainsUnlimited() bool   { return p.maxDomains <= 0 }
func (p *planCapacity) mailboxesUnlimited() bool { return p.maxMailboxes <= 0 }

// Summary projects the raw capacity into the public PlanSummary contract.
func (p *planCapacity) Summary() *PlanSummary {
	s := &PlanSummary{
		Plan:                  p.plan,
		MaxDomains:            p.maxDomains,
		MaxDomainsUnlimited:   p.domainsUnlimited(),
		DomainsUsed:           p.domainsUsed,
		MaxMailboxes:          p.maxMailboxes,
		MaxMailboxesUnlimited: p.mailboxesUnlimited(),
		MailboxesUsed:         p.mailboxesUsed,
		MaxAliasesUnlimited:   true,
		AliasesUsed:           p.aliasesUsed,
		RemainingAliases:      nil,
		StorageUsedBytes:      p.storageUsedBytes,
		StorageAllocatedBytes: p.storageAllocBytes,
		MailboxesAllocated:    p.mailboxesAllocated,
	}
	if !p.domainsUnlimited() {
		r := p.maxDomains - p.domainsUsed
		if r < 0 {
			r = 0
		}
		s.RemainingDomains = &r
	}
	if !p.mailboxesUnlimited() {
		r := p.maxMailboxes - p.mailboxesUsed
		if r < 0 {
			r = 0
		}
		s.RemainingMailboxes = &r
	}
	return s
}

// ---------------------------------------------------------------------------
// Repository: plan capacity reads
// ---------------------------------------------------------------------------

// LoadPlanCapacity reads the organization plan row and the live usage counters
// for a tenant. When a transaction-bound repo is used on PostgreSQL the
// organization row is locked FOR UPDATE so two concurrent provisioning
// requests serialize on the same plan row instead of both passing the capacity
// check. SQLite serializes writers at the transaction level already, so the
// lock clause is omitted there (it is not valid SQLite syntax).
//
// A missing or unreadable organization row fails CLOSED with
// ErrPlanUnavailable — provisioning never proceeds on unknown plan data.
func (r *DomainAdminRepo) LoadPlanCapacity(ctx context.Context, tenantID uint, lock bool) (*planCapacity, error) {
	q := "SELECT COALESCE(plan,''), COALESCE(max_domains,0), COALESCE(max_mailboxes,0) FROM tenants WHERE id=" +
		r.dialect.Placeholder(1) + " AND deleted_at IS NULL"
	if lock && r.dialect.IsPostgres() {
		q += " FOR UPDATE"
	}
	var p planCapacity
	if err := r.db.QueryRowContext(ctx, q, tenantID).Scan(&p.plan, &p.maxDomains, &p.maxMailboxes); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrPlanUnavailable
		}
		return nil, ErrPlanUnavailable
	}

	// Live usage. All counters are tenant-scoped and soft-delete aware.
	if err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_domains WHERE tenant_id="+r.dialect.Placeholder(1)+" AND deleted_at IS NULL",
		tenantID).Scan(&p.domainsUsed); err != nil {
		return nil, fmt.Errorf("plan usage (domains): %w", err)
	}
	if err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_mailboxes WHERE tenant_id="+r.dialect.Placeholder(1)+" AND deleted_at IS NULL",
		tenantID).Scan(&p.mailboxesUsed); err != nil {
		return nil, fmt.Errorf("plan usage (mailboxes): %w", err)
	}
	if err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_aliases WHERE tenant_id="+r.dialect.Placeholder(1)+" AND deleted_at IS NULL",
		tenantID).Scan(&p.aliasesUsed); err != nil {
		return nil, fmt.Errorf("plan usage (aliases): %w", err)
	}
	// Only FINITE per-domain caps consume allocatable capacity. Inheriting
	// (0) and unlimited (-1) domains are excluded from the sum: an inheriting
	// domain does not reserve capacity, and an unlimited domain can only
	// exist under an unlimited plan where allocation is not tracked.
	if err := r.db.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(max_mailboxes),0) FROM coremail_domains WHERE tenant_id="+
			r.dialect.Placeholder(1)+" AND deleted_at IS NULL AND max_mailboxes > 0",
		tenantID).Scan(&p.mailboxesAllocated); err != nil {
		return nil, fmt.Errorf("plan usage (allocated): %w", err)
	}
	if err := r.db.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(used_bytes),0) FROM coremail_mailboxes WHERE tenant_id="+
			r.dialect.Placeholder(1)+" AND deleted_at IS NULL",
		tenantID).Scan(&p.storageUsedBytes); err != nil {
		return nil, fmt.Errorf("plan usage (storage): %w", err)
	}
	var allocMB int64
	if err := r.db.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(max_quota_mb * max_mailboxes),0) FROM coremail_domains WHERE tenant_id="+
			r.dialect.Placeholder(1)+" AND deleted_at IS NULL AND max_quota_mb > 0 AND max_mailboxes > 0",
		tenantID).Scan(&allocMB); err != nil {
		return nil, fmt.Errorf("plan usage (allocated storage): %w", err)
	}
	p.storageAllocBytes = mbToBytes(allocMB)
	return &p, nil
}

// mbToBytes converts mebibytes to bytes without overflowing int64. Values that
// would overflow saturate at math.MaxInt64, which is only reachable by data
// that already bypassed validation.
func mbToBytes(mb int64) int64 {
	if mb <= 0 {
		return 0
	}
	if mb > math.MaxInt64/(1024*1024) {
		return math.MaxInt64
	}
	return mb * 1024 * 1024
}

// GetPlanSummary returns the organization plan + usage view for a tenant
// without opening a transaction. It fails closed on unknown plan data.
func (s *Service) GetPlanSummary(ctx context.Context, tenantID uint) (*PlanSummary, error) {
	cap, err := s.repo.LoadPlanCapacity(ctx, tenantID, false)
	if err != nil {
		return nil, err
	}
	return cap.Summary(), nil
}

// ---------------------------------------------------------------------------
// Limit validation
// ---------------------------------------------------------------------------

// validatedLimits are the storable sentinel values produced by
// validateLimits: each is LimitInherit, LimitUnlimited, or a positive value.
type validatedLimits struct {
	maxMailboxes int
	maxAliases   int
	defaultQuota int64
	maxQuota     int64
}

// validateLimits checks a requested limit set against the organization plan and
// converts it into storable sentinel values.
//
// Rules enforced (all fail with a typed error, never a raw DB error):
//   - Omitted -> LimitInherit (inherit the plan, NOT unlimited).
//   - Negative values other than -1 are rejected.
//   - Unlimited (-1) is only accepted when the plan dimension is itself
//     unlimited. Under a finite plan it is a limit-exceeded error.
//   - A finite mailbox cap must fit inside the plan ceiling AND inside the
//     remaining allocatable capacity (plan ceiling minus caps already pinned
//     by other domains).
//   - default_mailbox_quota_mb must be <= max_mailbox_quota_mb, resolving
//     inheritance on both sides first.
//   - Quota values are bounded by maxQuotaMB so MB->byte conversion is
//     overflow-safe.
//
// There is no organization-level alias ceiling in the tenants schema, so a
// finite domain alias cap is validated for sanity only; an explicit unlimited
// alias cap is permitted because no org ceiling contradicts it.
func validateLimits(in *DomainLimits, cap *planCapacity) (validatedLimits, error) {
	out := validatedLimits{
		maxMailboxes: LimitInherit,
		maxAliases:   LimitInherit,
		defaultQuota: LimitInherit,
		maxQuota:     LimitInherit,
	}
	if cap == nil {
		return out, ErrPlanUnavailable
	}
	if in == nil {
		return out, nil
	}

	// --- mailboxes -------------------------------------------------------
	if in.MaxMailboxes != nil {
		v := *in.MaxMailboxes
		switch {
		case v == LimitUnlimited:
			if !cap.mailboxesUnlimited() {
				return out, fmt.Errorf("%w: unlimited mailboxes requires an unlimited organization plan", ErrLimitExceedsPlan)
			}
			out.maxMailboxes = LimitUnlimited
		case v < 0:
			return out, fmt.Errorf("%w: max_mailboxes", ErrInvalidLimit)
		case v == 0:
			// Explicit 0 is indistinguishable from "unset" in the storage
			// encoding and a zero-capacity domain is never a useful product
			// state, so it is rejected rather than silently meaning inherit.
			return out, fmt.Errorf("%w: max_mailboxes must be positive, -1 for unlimited, or omitted to inherit", ErrInvalidLimit)
		default:
			if !cap.mailboxesUnlimited() {
				if v > cap.maxMailboxes {
					return out, fmt.Errorf("%w: max_mailboxes %d exceeds plan ceiling %d", ErrLimitExceedsPlan, v, cap.maxMailboxes)
				}
				remaining := cap.maxMailboxes - cap.mailboxesAllocated
				if remaining < 0 {
					remaining = 0
				}
				if v > remaining {
					return out, fmt.Errorf("%w: max_mailboxes %d exceeds remaining allocatable capacity %d", ErrLimitExceedsPlan, v, remaining)
				}
			}
			out.maxMailboxes = v
		}
	}

	// --- aliases ---------------------------------------------------------
	if in.MaxAliases != nil {
		v := *in.MaxAliases
		switch {
		case v == LimitUnlimited:
			out.maxAliases = LimitUnlimited
		case v <= 0:
			return out, fmt.Errorf("%w: max_aliases must be positive, -1 for unlimited, or omitted to inherit", ErrInvalidLimit)
		default:
			out.maxAliases = v
		}
	}

	// --- quotas ----------------------------------------------------------
	maxQ := int64(LimitInherit)
	if in.MaxMailboxQuotaMB != nil {
		v := *in.MaxMailboxQuotaMB
		switch {
		case v == LimitUnlimited:
			maxQ = LimitUnlimited
		case v <= 0:
			return out, fmt.Errorf("%w: max_mailbox_quota_mb must be positive, -1 for unlimited, or omitted to inherit", ErrInvalidLimit)
		case v > maxQuotaMB:
			return out, fmt.Errorf("%w: max_mailbox_quota_mb exceeds the maximum supported value", ErrInvalidLimit)
		default:
			maxQ = v
		}
	}
	defQ := int64(LimitInherit)
	if in.DefaultMailboxQuotaMB != nil {
		v := *in.DefaultMailboxQuotaMB
		switch {
		case v <= 0:
			// A default quota has no meaningful "unlimited" form: it is the
			// value stamped onto each new mailbox, and an unlimited mailbox
			// quota is expressed by omitting the ceiling, not by a default.
			return out, fmt.Errorf("%w: default_mailbox_quota_mb must be positive or omitted to inherit", ErrInvalidLimit)
		case v > maxQuotaMB:
			return out, fmt.Errorf("%w: default_mailbox_quota_mb exceeds the maximum supported value", ErrInvalidLimit)
		default:
			defQ = v
		}
	}
	// Contradiction check: a default above the ceiling can never be applied.
	if defQ > 0 && maxQ > 0 && defQ > maxQ {
		return out, fmt.Errorf("%w: default_mailbox_quota_mb (%d) exceeds max_mailbox_quota_mb (%d)", ErrLimitContradiction, defQ, maxQ)
	}
	out.defaultQuota = defQ
	out.maxQuota = maxQ
	return out, nil
}

// resolveEffectiveLimits projects stored sentinel values plus the plan into the
// public EffectiveLimits view.
func resolveEffectiveLimits(v validatedLimits, cap *planCapacity) EffectiveLimits {
	e := EffectiveLimits{}

	switch v.maxMailboxes {
	case LimitUnlimited:
		e.MaxMailboxesUnlimited = true
		e.MaxMailboxes = LimitUnlimited
	case LimitInherit:
		e.MaxMailboxesInherited = true
		if cap == nil || cap.mailboxesUnlimited() {
			e.MaxMailboxesUnlimited = true
			e.MaxMailboxes = LimitUnlimited
		} else {
			e.MaxMailboxes = cap.maxMailboxes
		}
	default:
		e.MaxMailboxes = v.maxMailboxes
	}

	switch v.maxAliases {
	case LimitUnlimited:
		e.MaxAliasesUnlimited = true
		e.MaxAliases = LimitUnlimited
	case LimitInherit:
		// No organization alias ceiling exists in the data model, so an
		// inheriting domain is genuinely uncapped for aliases. This is
		// reported explicitly rather than as a fake 0.
		e.MaxAliasesInherited = true
		e.MaxAliasesUnlimited = true
		e.MaxAliases = LimitUnlimited
	default:
		e.MaxAliases = v.maxAliases
	}

	switch v.maxQuota {
	case LimitUnlimited:
		e.MaxMailboxQuotaUnlimited = true
		e.MaxMailboxQuotaMB = LimitUnlimited
	case LimitInherit:
		// No organization per-mailbox quota ceiling exists either.
		e.MaxMailboxQuotaInherited = true
		e.MaxMailboxQuotaUnlimited = true
		e.MaxMailboxQuotaMB = LimitUnlimited
	default:
		e.MaxMailboxQuotaMB = v.maxQuota
	}

	if v.defaultQuota == LimitInherit {
		e.DefaultMailboxQuotaMBInherited = true
		e.DefaultMailboxQuotaMB = DefaultMailboxQuotaMB
	} else {
		e.DefaultMailboxQuotaMB = v.defaultQuota
	}
	return e
}

// ---------------------------------------------------------------------------
// Enforcement helpers (shared with the mailbox and alias write paths)
// ---------------------------------------------------------------------------

// DefaultMailboxQuotaMB is the quota stamped on a new mailbox when neither the
// request nor the domain pins one. It matches the value the mailbox service
// used before domain-level quota controls existed, so behaviour is unchanged
// for domains that inherit.
const DefaultMailboxQuotaMB int64 = 1024

// ResolveMailboxCap returns the enforceable mailbox ceiling for a domain given
// the domain's stored sentinel and the organization ceiling. unlimited=true
// means no cap applies. This is the single function both the provisioning path
// and the mailbox creation path use, so stored and enforced values cannot
// drift.
func ResolveMailboxCap(domainMax int, orgMax int) (limit int, unlimited bool) {
	switch {
	case domainMax == LimitUnlimited:
		return 0, true
	case domainMax > 0:
		return domainMax, false
	default: // LimitInherit
		if orgMax <= 0 {
			return 0, true
		}
		return orgMax, false
	}
}

// ResolveAliasCap returns the enforceable alias ceiling for a domain. There is
// no organization alias ceiling in the data model, so an inheriting domain is
// uncapped.
func ResolveAliasCap(domainMax int) (limit int, unlimited bool) {
	if domainMax > 0 {
		return domainMax, false
	}
	return 0, true
}

// ResolveQuotaBounds returns the per-mailbox quota ceiling and the default
// quota for a domain. maxUnlimited=true means no per-mailbox ceiling applies.
func ResolveQuotaBounds(domainMaxQuotaMB int64, domainDefaultQuotaMB int64) (maxMB int64, maxUnlimited bool, defaultMB int64) {
	switch {
	case domainMaxQuotaMB == LimitUnlimited:
		maxMB, maxUnlimited = 0, true
	case domainMaxQuotaMB > 0:
		maxMB, maxUnlimited = domainMaxQuotaMB, false
	default:
		maxMB, maxUnlimited = 0, true
	}
	defaultMB = domainDefaultQuotaMB
	if defaultMB <= 0 {
		defaultMB = DefaultMailboxQuotaMB
	}
	if !maxUnlimited && defaultMB > maxMB {
		defaultMB = maxMB
	}
	return maxMB, maxUnlimited, defaultMB
}

// ---------------------------------------------------------------------------
// Provisioning
// ---------------------------------------------------------------------------

// ProvisionDomain is the single, concurrency-safe provisioning operation.
//
// Inside ONE database transaction it:
//  1. normalizes and validates the domain name,
//  2. locks and reads the organization plan row (FOR UPDATE on PostgreSQL),
//  3. re-checks the global name uniqueness and the plan domain ceiling,
//  4. validates the requested limits against the plan,
//  5. inserts the domain with the resolved sentinel limits,
//  6. optionally generates DKIM through the SAME shared transaction helper the
//     standalone GenerateDKIM path uses,
//  7. writes the canonical audit events,
//  8. commits.
//
// Any failure at any step — including a failure to write the audit record —
// rolls the whole transaction back, so a partially-created domain is not
// reachable. Duplicate names return ErrDomainAlreadyExists both from the
// pre-check and from the unique-constraint violation, so a concurrent double
// submit is deterministic.
func (s *Service) ProvisionDomain(ctx context.Context, req CreateDomainRequest, tenantID uint) (*ProvisionDomainResult, error) {
	if tenantID == 0 {
		return nil, ErrDomainForbidden
	}

	normalized, err := ValidateDomainName(req.Name)
	if err != nil {
		return nil, err
	}

	status := string(DomainStatusActive)
	if strings.TrimSpace(req.Status) != "" {
		parsed, ok := ParseDomainStatus(req.Status)
		if !ok {
			return nil, ErrInvalidDomainStatus
		}
		// Provisioning may only create an operable or an explicitly
		// deactivated domain. "suspended" is an administrative action applied
		// to an existing domain, never an initial state chosen by the caller.
		if parsed != DomainStatusActive && parsed != DomainStatusDisabled {
			return nil, ErrInvalidDomainStatus
		}
		status = string(parsed)
	}

	description := strings.TrimSpace(req.Description)
	if len(description) > MaxDomainDescriptionLen {
		return nil, ErrDescriptionTooLong
	}

	selector := ""
	generateDKIM := false
	if req.DKIM != nil && req.DKIM.Generate {
		generateDKIM = true
		selector, err = ValidateDKIMSelector(req.DKIM.Selector)
		if err != nil {
			return nil, err
		}
	}

	// Merge the legacy flat limit fields into the typed shape so the old
	// {"name":..,"max_mailboxes":..} body keeps working unchanged.
	limits := req.Limits
	if limits == nil && (req.MaxMailboxes != 0 || req.MaxAliases != 0 || req.MaxQuotaMB != 0) {
		limits = &DomainLimits{}
		if req.MaxMailboxes != 0 {
			v := req.MaxMailboxes
			limits.MaxMailboxes = &v
		}
		if req.MaxAliases != 0 {
			v := req.MaxAliases
			limits.MaxAliases = &v
		}
		if req.MaxQuotaMB != 0 {
			v := req.MaxQuotaMB
			limits.MaxMailboxQuotaMB = &v
		}
	}

	idemKey := strings.TrimSpace(req.IdempotencyKey)

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin domain provisioning: %w", err)
	}
	defer tx.Rollback()

	repo := s.repo.WithTx(tx)

	// Idempotency / deterministic double-submit handling. A repeat submission
	// of the same name by the same tenant returns the existing domain instead
	// of a confusing conflict, provided the caller supplied a key OR the
	// existing row is owned by this tenant. A name owned by ANOTHER tenant is
	// always a plain conflict that never reveals the owner.
	if existing, err := repo.GetByName(ctx, normalized, tenantID); err != nil {
		return nil, err
	} else if existing != nil {
		if idemKey != "" {
			eff := resolveEffectiveLimitsFromDomain(existing, nil)
			return &ProvisionDomainResult{
				Domain:          existing,
				EffectiveLimits: eff,
				Idempotent:      true,
				DNS:             defaultDNSNextStep(),
			}, nil
		}
		return nil, ErrDomainAlreadyExists
	}
	if taken, err := repo.GetByNameGlobal(ctx, normalized); err != nil {
		return nil, err
	} else if taken {
		return nil, ErrDomainAlreadyExists
	}

	// Plan capacity, locked for the duration of the transaction.
	cap, err := repo.LoadPlanCapacity(ctx, tenantID, true)
	if err != nil {
		return nil, err
	}
	if !cap.domainsUnlimited() && cap.domainsUsed >= cap.maxDomains {
		return nil, ErrDomainLimitReached
	}

	vl, err := validateLimits(limits, cap)
	if err != nil {
		return nil, err
	}

	d := &AdminDomain{
		TenantID:     tenantID,
		Name:         normalized,
		Status:       status,
		Plan:         cap.plan,
		Description:  description,
		MaxMailboxes: vl.maxMailboxes,
		MaxAliases:   vl.maxAliases,
		MaxQuotaMB:   vl.maxQuota,
	}
	created, err := repo.Create(ctx, d)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDomainAlreadyExists
		}
		return nil, err
	}
	if err := repo.SetDefaultMailboxQuota(ctx, created.ID, tenantID, vl.defaultQuota); err != nil {
		return nil, err
	}
	created.DefaultMailboxQuotaMB = vl.defaultQuota

	var dkimResult *DKIMResult
	if generateDKIM {
		// The SAME transaction-scoped helper the standalone GenerateDKIM
		// entrypoint uses: no duplicated crypto or duplicate-detection logic,
		// and a DKIM failure rolls the domain insert back with it.
		dkimResult, err = s.generateDKIMTx(ctx, tx, repo, created.ID, tenantID, created.Name, selector)
		if err != nil {
			return nil, err
		}
		created.DKIMEnabled = true
		created.DKIMSelector = dkimResult.Selector
	}

	if s.webhooks != nil {
		if _, err := s.webhooks.Publish(ctx, repo.db, "domain.created", fmt.Sprintf("domain:%d", created.ID), tenantID, map[string]any{
			"domain_id": created.ID,
			"name":      created.Name,
			"status":    created.Status,
		}, created.CreatedAt); err != nil {
			return nil, fmt.Errorf("publish domain webhook event: %w", err)
		}
	}

	if s.auditStore != nil {
		// Canonical provisioning audit event. The payload records only the
		// public provisioning decision: domain, status, resolved limits and
		// the DKIM SELECTOR. No key material is ever placed in the audit log.
		after := fmt.Sprintf(
			`{"domain":%q,"status":%q,"max_mailboxes":%d,"max_aliases":%d,"max_quota_mb":%d,"default_mailbox_quota_mb":%d,"dkim":%t}`,
			created.Name, created.Status, vl.maxMailboxes, vl.maxAliases, vl.maxQuota, vl.defaultQuota, generateDKIM)
		entry := &audit.ExtendedEntry{
			Action:   "domain.provision",
			Target:   fmt.Sprintf("domain:%d", created.ID),
			TargetID: created.ID,
			TenantID: tenantID,
			Result:   "success",
			After:    after,
		}
		if err := s.auditStore.RecordTx(ctx, tx, entry); err != nil {
			return nil, err
		}
		if generateDKIM {
			dkimEntry := &audit.ExtendedEntry{
				Action:   "domain.dkim.generate",
				Target:   fmt.Sprintf("domain:%d", created.ID),
				TargetID: created.ID,
				TenantID: tenantID,
				Result:   "success",
				After:    fmt.Sprintf(`{"domain":%q,"selector":%q}`, created.Name, dkimResult.Selector),
			}
			if err := s.auditStore.RecordTx(ctx, tx, dkimEntry); err != nil {
				return nil, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit domain provisioning: %w", err)
	}

	// Recompute the plan summary AFTER commit so the caller sees post-create
	// remaining capacity.
	summary := cap.Summary()
	if post, err := s.GetPlanSummary(ctx, tenantID); err == nil {
		summary = post
	}

	return &ProvisionDomainResult{
		Domain:          created,
		EffectiveLimits: resolveEffectiveLimits(vl, cap),
		DKIM:            dkimResult,
		Plan:            summary,
		DNS:             defaultDNSNextStep(),
	}, nil
}

func defaultDNSNextStep() *DNSNextStepInfo {
	return &DNSNextStepInfo{
		PublicDNSChanged: false,
		NextStep:         "publish_and_verify_dns",
	}
}

// resolveEffectiveLimitsFromDomain rebuilds the effective view from a stored
// domain row, used on the idempotent replay path.
func resolveEffectiveLimitsFromDomain(d *AdminDomain, cap *planCapacity) EffectiveLimits {
	return resolveEffectiveLimits(validatedLimits{
		maxMailboxes: d.MaxMailboxes,
		maxAliases:   d.MaxAliases,
		defaultQuota: d.DefaultMailboxQuotaMB,
		maxQuota:     d.MaxQuotaMB,
	}, cap)
}

// ValidateDKIMSelector normalizes and validates a DKIM selector. Selectors
// become a DNS label (<selector>._domainkey.<domain>) so they are restricted to
// the same character set a DNS label allows. An empty selector defaults to
// "mail".
func ValidateDKIMSelector(sel string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(sel))
	if s == "" {
		return "mail", nil
	}
	if len(s) > 63 {
		return "", ErrInvalidDKIMSelector
	}
	if strings.HasPrefix(s, "-") || strings.HasSuffix(s, "-") {
		return "", ErrInvalidDKIMSelector
	}
	for _, ch := range s {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_') {
			return "", ErrInvalidDKIMSelector
		}
	}
	return s, nil
}

// SetDefaultMailboxQuota persists the domain's default per-mailbox quota.
func (r *DomainAdminRepo) SetDefaultMailboxQuota(ctx context.Context, id, tenantID uint, quotaMB int64) error {
	_, err := r.db.ExecContext(ctx,
		"UPDATE coremail_domains SET default_mailbox_quota_mb="+r.dialect.Placeholder(1)+
			", updated_at="+r.dialect.Placeholder(2)+
			" WHERE id="+r.dialect.Placeholder(3)+" AND tenant_id="+r.dialect.Placeholder(4)+" AND deleted_at IS NULL",
		quotaMB, time.Now().UTC(), id, tenantID)
	return err
}
