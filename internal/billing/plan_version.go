package billing

// PlanVersion is an immutable, versioned snapshot of a plan's full term
// set (Feature 3's "Immutable plan versions" + the full limit-dimension
// list). The existing mutable Plan row (types.go) remains the live,
// editable catalog entry an operator updates; PlanVersion is a permanent,
// never-mutated record of exactly what a plan's terms were at a point in
// time, so a subscription created against version 3 keeps version 3's
// terms even after an operator edits the live Plan to version 4 — this is
// what "immutable" means here: publishing a new version never rewrites an
// old one, it only inserts a new row.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// SupportTier is a closed enum so a typo doesn't silently create a new,
// unrecognized tier that every UI/entitlement check then fails to match.
type SupportTier string

const (
	SupportTierCommunity  SupportTier = "community"
	SupportTierStandard   SupportTier = "standard"
	SupportTierPriority   SupportTier = "priority"
	SupportTierEnterprise SupportTier = "enterprise"
)

// PlanLimits is the full dimension set Feature 3 specifies. Countable
// dimensions (the *Max*/*Limit* int fields) are enforced via
// UsageCounterStore's atomic reserve-on-write path; the boolean/enum
// fields (RelayAccess, ArchiveAccess, APIAccess, SupportTier,
// DataResidency) are static entitlements an EntitlementResolver reads
// directly — there's nothing to "reserve" for a yes/no capability.
type PlanLimits struct {
	MaxDomains              int         `json:"max_domains"`
	MaxMailboxes            int         `json:"max_mailboxes"`
	MaxTenantAdmins         int         `json:"max_tenant_admins"`
	MailboxStorageMB        int64       `json:"mailbox_storage_mb"`      // per-account
	OrganizationStorageMB   int64       `json:"organization_storage_mb"` // total
	MaxAliases              int         `json:"max_aliases"`
	MaxGroups               int         `json:"max_groups"`
	SendLimitDay            int         `json:"send_limit_day"`
	SendLimitHour           int         `json:"send_limit_hour"`
	MaxRecipientsPerMessage int         `json:"max_recipients_per_message"`
	MaxAttachmentSizeMB     int         `json:"max_attachment_size_mb"`
	RelayAccess             bool        `json:"relay_access"`
	ArchiveAccess           bool        `json:"archive_access"`
	RetentionDays           int         `json:"retention_days"`
	APIAccess               bool        `json:"api_access"`
	SupportTier             SupportTier `json:"support_tier"`
	DataResidency           string      `json:"data_residency,omitempty"` // e.g. "eu", "us"; empty = no constraint
}

type PlanVersion struct {
	ID        int64
	PlanID    PlanID
	Version   int
	Limits    PlanLimits
	CreatedAt time.Time
	CreatedBy uint
}

type PlanVersionStore struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func NewPlanVersionStore(db *sql.DB) *PlanVersionStore {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &PlanVersionStore{db: db, dialect: dialect}
}

func (s *PlanVersionStore) EnsureSchema(ctx context.Context) error {
	ts := s.dialect.TimestampType()
	autoInc := s.dialect.AutoIncrement()
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS billing_plan_versions (
		id `+autoInc+`,
		plan_id TEXT NOT NULL,
		version INTEGER NOT NULL,
		limits TEXT NOT NULL,
		created_at `+ts+` NOT NULL,
		created_by INTEGER NOT NULL DEFAULT 0,
		UNIQUE(plan_id, version)
	)`)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_billing_plan_versions_plan ON billing_plan_versions(plan_id)`)
	return err
}

// Publish inserts a new immutable version for planID. version must be
// exactly one greater than the current latest version (or 1 if none
// exists) — publishing is append-only and gap-free, so "version N" always
// unambiguously means "the Nth published revision", never a
// caller-chosen arbitrary number that could collide or leave a gap.
func (s *PlanVersionStore) Publish(ctx context.Context, planID PlanID, limits PlanLimits, createdBy uint, now time.Time) (*PlanVersion, error) {
	latest, err := s.Latest(ctx, planID)
	if err != nil && err != ErrPlanVersionNotFound {
		return nil, err
	}
	nextVersion := 1
	if latest != nil {
		nextVersion = latest.Version + 1
	}
	body, err := json.Marshal(limits)
	if err != nil {
		return nil, fmt.Errorf("billing: encode plan limits: %w", err)
	}
	insertQuery := `INSERT INTO billing_plan_versions (plan_id, version, limits, created_at, created_by) VALUES (` + s.dialect.Placeholders(5) + `)`
	args := []any{string(planID), nextVersion, string(body), now, createdBy}
	var id int64
	if s.dialect.IsPostgres() {
		if err := s.db.QueryRowContext(ctx, insertQuery+` RETURNING id`, args...).Scan(&id); err != nil {
			return nil, fmt.Errorf("billing: publish plan version: %w", err)
		}
	} else {
		res, err := s.db.ExecContext(ctx, insertQuery, args...)
		if err != nil {
			return nil, fmt.Errorf("billing: publish plan version: %w", err)
		}
		id, err = res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("billing: read plan version id: %w", err)
		}
	}
	return &PlanVersion{ID: id, PlanID: planID, Version: nextVersion, Limits: limits, CreatedAt: now, CreatedBy: createdBy}, nil
}

var ErrPlanVersionNotFound = fmt.Errorf("billing: no published version for this plan")

func (s *PlanVersionStore) Latest(ctx context.Context, planID PlanID) (*PlanVersion, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, plan_id, version, limits, created_at, created_by FROM billing_plan_versions WHERE plan_id = `+s.dialect.Placeholder(1)+` ORDER BY version DESC LIMIT 1`,
		string(planID),
	)
	return scanPlanVersion(row)
}

func (s *PlanVersionStore) Get(ctx context.Context, planID PlanID, version int) (*PlanVersion, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, plan_id, version, limits, created_at, created_by FROM billing_plan_versions WHERE plan_id = `+s.dialect.Placeholder(1)+` AND version = `+s.dialect.Placeholder(2),
		string(planID), version,
	)
	return scanPlanVersion(row)
}

func scanPlanVersion(row *sql.Row) (*PlanVersion, error) {
	var pv PlanVersion
	var planID, limitsJSON string
	if err := row.Scan(&pv.ID, &planID, &pv.Version, &limitsJSON, &pv.CreatedAt, &pv.CreatedBy); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrPlanVersionNotFound
		}
		return nil, fmt.Errorf("billing: scan plan version: %w", err)
	}
	pv.PlanID = PlanID(planID)
	if err := json.Unmarshal([]byte(limitsJSON), &pv.Limits); err != nil {
		return nil, fmt.Errorf("billing: decode plan limits: %w", err)
	}
	return &pv, nil
}
