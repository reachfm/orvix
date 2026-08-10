package billing

// OverrideStore persists temporary operator overrides of a tenant's plan
// limits (Feature 3's "Temporary operator override with: reason, expiry,
// actor, audit"). This did not exist anywhere in internal/billing before
// this change — quota.go's CanCreateDomain/CanCreateMailbox/CanSendEmail
// only ever consulted the plan's static limits. OverrideStore is
// deliberately additive: it extends QuotaService's checks, it does not
// replace the plan-based path.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/dbdialect"
)

// OverrideDimension identifies which limit an override affects. Kept as a
// closed enum (not a free string) so a typo in a caller's dimension name
// fails at compile time rather than silently never matching in quota.go's
// lookup.
type OverrideDimension string

const (
	OverrideMaxDomains   OverrideDimension = "max_domains"
	OverrideMaxMailboxes OverrideDimension = "max_mailboxes"
	OverrideSendLimitDay OverrideDimension = "send_limit_day"
)

var ErrOverrideReasonRequired = errors.New("billing: an operator override requires a non-empty reason")
var ErrOverrideExpiryRequired = errors.New("billing: an operator override requires a future expiry")

type OperatorOverride struct {
	ID        int64
	TenantID  uint
	Dimension OverrideDimension
	Limit     int
	Reason    string
	ActorID   uint
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
}

type OverrideStore struct {
	db      *sql.DB
	dialect *dbdialect.Info
	audit   *audit.ExtendedStore
}

func NewOverrideStore(db *sql.DB, auditStore *audit.ExtendedStore) *OverrideStore {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &OverrideStore{db: db, dialect: dialect, audit: auditStore}
}

func (s *OverrideStore) EnsureSchema(ctx context.Context) error {
	ts := s.dialect.TimestampType()
	autoInc := s.dialect.AutoIncrement()
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS billing_operator_overrides (
		id `+autoInc+`,
		tenant_id INTEGER NOT NULL,
		dimension TEXT NOT NULL,
		limit_value INTEGER NOT NULL,
		reason TEXT NOT NULL,
		actor_id INTEGER NOT NULL,
		created_at `+ts+` NOT NULL,
		expires_at `+ts+` NOT NULL,
		revoked_at `+ts+`
	)`)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_billing_overrides_tenant_dim ON billing_operator_overrides(tenant_id, dimension)`)
	return err
}

// Set creates a new override and records it in the transactional audit
// log in the SAME database transaction — the override and its audit trail
// either both persist or neither does. reason must be non-empty and
// expiresAt must be in the future; both requirements are enforced here,
// not left to the caller, since a limit override with no accountability
// trail or no expiry defeats the entire point of it being "temporary".
func (s *OverrideStore) Set(ctx context.Context, tenantID uint, dim OverrideDimension, limit int, reason string, actorID uint, now, expiresAt time.Time) (*OperatorOverride, error) {
	if reason == "" {
		return nil, ErrOverrideReasonRequired
	}
	if !expiresAt.After(now) {
		return nil, ErrOverrideExpiryRequired
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("billing: begin override tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO billing_operator_overrides (tenant_id, dimension, limit_value, reason, actor_id, created_at, expires_at) VALUES (`+s.dialect.Placeholders(7)+`)`,
		tenantID, string(dim), limit, reason, actorID, now, expiresAt,
	)
	if err != nil {
		return nil, fmt.Errorf("billing: insert override: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("billing: read override id: %w", err)
	}

	if s.audit != nil {
		if err := s.audit.RecordTx(ctx, tx, &audit.ExtendedEntry{
			Actor:     fmt.Sprintf("user:%d", actorID),
			ActorID:   actorID,
			TenantID:  tenantID,
			Action:    "billing.override.set",
			Target:    string(dim),
			TargetID:  uint(id),
			Result:    "success",
			Reason:    reason,
			After:     fmt.Sprintf(`{"limit":%d,"expires_at":%q}`, limit, expiresAt.Format(time.RFC3339)),
			Timestamp: now,
		}); err != nil {
			return nil, fmt.Errorf("billing: record override audit: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("billing: commit override: %w", err)
	}
	return &OperatorOverride{
		ID: id, TenantID: tenantID, Dimension: dim, Limit: limit,
		Reason: reason, ActorID: actorID, CreatedAt: now, ExpiresAt: expiresAt,
	}, nil
}

// Revoke ends an override before its natural expiry, auditing the revoke
// in the same transaction as the row update.
func (s *OverrideStore) Revoke(ctx context.Context, id int64, actorID uint, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("billing: begin revoke tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`UPDATE billing_operator_overrides SET revoked_at = `+s.dialect.Placeholder(1)+` WHERE id = `+s.dialect.Placeholder(2)+` AND revoked_at IS NULL`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("billing: revoke override: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("billing: override %d not found or already revoked", id)
	}

	if s.audit != nil {
		if err := s.audit.RecordTx(ctx, tx, &audit.ExtendedEntry{
			Actor:     fmt.Sprintf("user:%d", actorID),
			ActorID:   actorID,
			Action:    "billing.override.revoke",
			TargetID:  uint(id),
			Result:    "success",
			Timestamp: now,
		}); err != nil {
			return fmt.Errorf("billing: record revoke audit: %w", err)
		}
	}

	return tx.Commit()
}

// ActiveOverride returns the tenant's currently-active (not expired, not
// revoked) override for dim, or nil if none exists. QuotaService consults
// this before falling back to the plan's static limit.
func (s *OverrideStore) ActiveOverride(ctx context.Context, tenantID uint, dim OverrideDimension, now time.Time) (*OperatorOverride, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, dimension, limit_value, reason, actor_id, created_at, expires_at
		 FROM billing_operator_overrides
		 WHERE tenant_id = `+s.dialect.Placeholder(1)+` AND dimension = `+s.dialect.Placeholder(2)+`
		   AND revoked_at IS NULL AND expires_at > `+s.dialect.Placeholder(3)+`
		 ORDER BY created_at DESC LIMIT 1`,
		tenantID, string(dim), now,
	)
	var o OperatorOverride
	var dimension string
	if err := row.Scan(&o.ID, &o.TenantID, &dimension, &o.Limit, &o.Reason, &o.ActorID, &o.CreatedAt, &o.ExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("billing: lookup active override: %w", err)
	}
	o.Dimension = OverrideDimension(dimension)
	return &o, nil
}
