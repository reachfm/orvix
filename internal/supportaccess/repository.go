package supportaccess

import (
	"context"
	"database/sql"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

type Repository struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func NewRepository(db *sql.DB) *Repository {
	d, _ := dbdialect.Detect(db)
	if d == nil {
		d = dbdialect.FromDriver("sqlite")
	}
	return &Repository{db: db, dialect: d}
}

func (r *Repository) EnsureSchema(ctx context.Context) error {
	ts := r.dialect.TimestampType()
	autoInc := r.dialect.AutoIncrement()
	_, err := r.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS platform_support_access_grants (
		id `+autoInc+`,
		ticket_ref TEXT NOT NULL,
		reason TEXT NOT NULL,
		target_tenant_id INTEGER NOT NULL,
		granted_by_id INTEGER NOT NULL,
		permission_scope TEXT NOT NULL DEFAULT 'read_only',
		status TEXT NOT NULL DEFAULT 'requested',
		activated_at `+ts+`,
		expires_at `+ts+` NOT NULL,
		revoked_at `+ts+`,
		revoke_reason TEXT NOT NULL DEFAULT '',
		emergency_break_glass INTEGER NOT NULL DEFAULT 0,
		version INTEGER NOT NULL DEFAULT 1,
		created_at `+ts+` NOT NULL,
		updated_at `+ts+` NOT NULL
	)`)
	return err
}

func (r *Repository) Insert(ctx context.Context, g *AccessGrant) error {
	now := time.Now().UTC()
	g.CreatedAt = now
	g.UpdatedAt = now
	glass := 0
	if g.EmergencyBreakGlass {
		glass = 1
	}
	query := r.dialect.Rewrite(
		`INSERT INTO platform_support_access_grants (ticket_ref, reason, target_tenant_id, granted_by_id, permission_scope, status, expires_at, emergency_break_glass, version, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,1,?,?)`)
	if r.dialect.IsPostgres() {
		var id uint
		err := r.db.QueryRowContext(ctx, query+" RETURNING id",
			g.TicketRef, g.Reason, g.TargetTenantID, g.GrantedByID, g.PermissionScope, g.Status, g.ExpiresAt, glass, g.CreatedAt, g.UpdatedAt).Scan(&id)
		if err != nil {
			return err
		}
		g.ID = id
		g.version = 1
		return nil
	}
	res, err := r.db.ExecContext(ctx, query,
		g.TicketRef, g.Reason, g.TargetTenantID, g.GrantedByID, g.PermissionScope, g.Status, g.ExpiresAt, glass, g.CreatedAt, g.UpdatedAt)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	g.ID = uint(id)
	g.version = 1
	return nil
}

func (r *Repository) Update(ctx context.Context, g *AccessGrant) error {
	g.UpdatedAt = time.Now().UTC()
	res, err := r.db.ExecContext(ctx, r.dialect.Rewrite(
		`UPDATE platform_support_access_grants SET status=?, activated_at=?, revoked_at=?, revoke_reason=?, version=version+1, updated_at=? WHERE id=? AND version=?`,
	), g.Status, g.ActivatedAt, g.RevokedAt, g.RevokeReason, g.UpdatedAt, g.ID, g.version)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return &saError{"concurrent modification: grant may have been updated"}
	}
	g.version++
	return nil
}

func (r *Repository) Get(ctx context.Context, id uint) (*AccessGrant, error) {
	var g AccessGrant
	var activatedAt, revokedAt sql.NullTime
	var glass int
	err := r.db.QueryRowContext(ctx, r.dialect.Rewrite(
		`SELECT id, ticket_ref, reason, target_tenant_id, granted_by_id, permission_scope, status, activated_at, expires_at, revoked_at, revoke_reason, emergency_break_glass, version, created_at, updated_at
		FROM platform_support_access_grants WHERE id=?`), id).
		Scan(&g.ID, &g.TicketRef, &g.Reason, &g.TargetTenantID, &g.GrantedByID, &g.PermissionScope, &g.Status, &activatedAt, &g.ExpiresAt, &revokedAt, &g.RevokeReason, &glass, &g.version, &g.CreatedAt, &g.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if activatedAt.Valid {
		g.ActivatedAt = &activatedAt.Time
	}
	if revokedAt.Valid {
		g.RevokedAt = &revokedAt.Time
	}
	g.EmergencyBreakGlass = glass == 1
	return &g, nil
}

func (r *Repository) FindActiveForTenant(ctx context.Context, tenantID uint) (*AccessGrant, error) {
	var g AccessGrant
	var activatedAt, revokedAt sql.NullTime
	var glass int
	err := r.db.QueryRowContext(ctx, r.dialect.Rewrite(
		`SELECT id, ticket_ref, reason, target_tenant_id, granted_by_id, permission_scope, status, activated_at, expires_at, revoked_at, revoke_reason, emergency_break_glass, version, created_at, updated_at
		FROM platform_support_access_grants WHERE target_tenant_id=? AND status='active' AND expires_at > ? ORDER BY created_at DESC LIMIT 1`,
	), tenantID, time.Now().UTC()).
		Scan(&g.ID, &g.TicketRef, &g.Reason, &g.TargetTenantID, &g.GrantedByID, &g.PermissionScope, &g.Status, &activatedAt, &g.ExpiresAt, &revokedAt, &g.RevokeReason, &glass, &g.version, &g.CreatedAt, &g.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if activatedAt.Valid {
		g.ActivatedAt = &activatedAt.Time
	}
	if revokedAt.Valid {
		g.RevokedAt = &revokedAt.Time
	}
	g.EmergencyBreakGlass = glass == 1
	return &g, nil
}

func (r *Repository) FindGrantByOperator(ctx context.Context, operatorID, tenantID uint) (*AccessGrant, error) {
	var g AccessGrant
	var activatedAt, revokedAt sql.NullTime
	var glass int
	err := r.db.QueryRowContext(ctx, r.dialect.Rewrite(
		`SELECT id, ticket_ref, reason, target_tenant_id, granted_by_id, permission_scope, status, activated_at, expires_at, revoked_at, revoke_reason, emergency_break_glass, version, created_at, updated_at
		FROM platform_support_access_grants WHERE granted_by_id=? AND target_tenant_id=? AND status='active' AND expires_at > ? ORDER BY created_at DESC LIMIT 1`,
	), operatorID, tenantID, time.Now().UTC()).
		Scan(&g.ID, &g.TicketRef, &g.Reason, &g.TargetTenantID, &g.GrantedByID, &g.PermissionScope, &g.Status, &activatedAt, &g.ExpiresAt, &revokedAt, &g.RevokeReason, &glass, &g.version, &g.CreatedAt, &g.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if activatedAt.Valid {
		g.ActivatedAt = &activatedAt.Time
	}
	if revokedAt.Valid {
		g.RevokedAt = &revokedAt.Time
	}
	g.EmergencyBreakGlass = glass == 1
	return &g, nil
}

func (r *Repository) List(ctx context.Context, tenantID uint, limit int) ([]AccessGrant, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, r.dialect.Rewrite(
		`SELECT id, ticket_ref, reason, target_tenant_id, granted_by_id, permission_scope, status, activated_at, expires_at, revoked_at, revoke_reason, emergency_break_glass, version, created_at, updated_at
		FROM platform_support_access_grants WHERE target_tenant_id=? ORDER BY created_at DESC LIMIT ?`), tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AccessGrant
	for rows.Next() {
		var g AccessGrant
		var activatedAt, revokedAt sql.NullTime
		var glass int
		if err := rows.Scan(&g.ID, &g.TicketRef, &g.Reason, &g.TargetTenantID, &g.GrantedByID, &g.PermissionScope, &g.Status, &activatedAt, &g.ExpiresAt, &revokedAt, &g.RevokeReason, &glass, &g.version, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		if activatedAt.Valid {
			g.ActivatedAt = &activatedAt.Time
		}
		if revokedAt.Valid {
			g.RevokedAt = &revokedAt.Time
		}
		g.EmergencyBreakGlass = glass == 1
		out = append(out, g)
	}
	return out, rows.Err()
}
