package mailcontrol

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/platform/kernel"
)

// Repository owns platform mail-control persistence that has no
// dedicated production service: platform-wide alias/group reads and
// bulk mailbox status transitions. Every query is tenant-scoped and
// uses dbdialect placeholders so SQLite and PostgreSQL behave
// identically.
type Repository struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func NewRepository(db *sql.DB) *Repository {
	d, err := dbdialect.Detect(db)
	if err != nil {
		d = dbdialect.FromDriver("sqlite")
	}
	return &Repository{db: db, dialect: d}
}

func (r *Repository) q(query string) string { return r.dialect.Rewrite(query) }

const maxPageSize = 200

func sanitizeLimit(limit int) int {
	if limit <= 0 {
		return 25
	}
	if limit > maxPageSize {
		return maxPageSize
	}
	return limit
}

// ── Tenant eligibility ─────────────────────────────────────────────

// TenantState is the lifecycle state the platform provisioning paths
// check before mutating a tenant's resources.
type TenantState struct {
	Exists  bool
	Active  bool
	Deleted bool
}

// TenantEligible resolves the lifecycle state of the explicit target
// tenant. Deleted tenants and absent tenants are indistinguishable to
// the caller except through Exists/Deleted (the platform operator is
// allowed to know the difference; the tenant admin surface never sees
// this read). A nil DB error leaves every field false — fail closed.
func (r *Repository) TenantEligible(ctx context.Context, tenantID uint) (TenantState, error) {
	if tenantID == 0 {
		return TenantState{}, nil
	}
	var active int
	var deletedAt *time.Time
	err := r.db.QueryRowContext(ctx, r.q(
		`SELECT COALESCE(active,0), deleted_at FROM tenants WHERE id = ?`),
		tenantID).Scan(&active, &deletedAt)
	if err == sql.ErrNoRows {
		return TenantState{}, nil
	}
	if err != nil {
		return TenantState{}, fmt.Errorf("resolve tenant lifecycle: %w", err)
	}
	return TenantState{Exists: true, Active: active != 0, Deleted: deletedAt != nil}, nil
}

// ── Aliases ────────────────────────────────────────────────────────

func (r *Repository) ListAliases(ctx context.Context, f PlatformAliasFilter) ([]PlatformAlias, int64, error) {
	f.Limit = sanitizeLimit(f.Limit)
	var where []string
	var args []any
	where = append(where, "deleted_at IS NULL")
	where = append(where, "tenant_id = "+r.dialect.Placeholder(len(args)+1))
	args = append(args, f.TenantID)
	if f.DomainID > 0 {
		where = append(where, "domain_id = "+r.dialect.Placeholder(len(args)+1))
		args = append(args, f.DomainID)
	}
	if f.Search != "" {
		where = append(where, "LOWER(from_addr) LIKE "+r.dialect.Placeholder(len(args)+1))
		args = append(args, "%"+strings.ToLower(f.Search)+"%")
	}
	if f.Destination != "" {
		where = append(where, "LOWER(to_addr) LIKE "+r.dialect.Placeholder(len(args)+1))
		args = append(args, "%"+strings.ToLower(f.Destination)+"%")
	}
	clause := " WHERE " + strings.Join(where, " AND ")

	var total int64
	if err := r.db.QueryRowContext(ctx, r.q(`SELECT COUNT(*) FROM coremail_aliases`+clause), args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count aliases: %w", err)
	}

	limitArgs := append(args, f.Limit, f.Offset)
	rows, err := r.db.QueryContext(ctx, r.q(`SELECT id, domain_id, tenant_id, from_addr, to_addr, active, created_at, updated_at
		FROM coremail_aliases`+clause+` ORDER BY id ASC LIMIT `+r.dialect.Placeholder(len(args)+1)+` OFFSET `+r.dialect.Placeholder(len(args)+2)), limitArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list aliases: %w", err)
	}
	defer rows.Close()

	var out []PlatformAlias
	for rows.Next() {
		var a PlatformAlias
		var active int
		if err := rows.Scan(&a.ID, &a.DomainID, &a.TenantID, &a.FromAddr, &a.ToAddr, &active, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, 0, err
		}
		a.Active = active != 0
		out = append(out, a)
	}
	return out, total, rows.Err()
}

func (r *Repository) GetAlias(ctx context.Context, id, tenantID uint) (*PlatformAlias, error) {
	var a PlatformAlias
	var active int
	err := r.db.QueryRowContext(ctx, r.q(`SELECT id, domain_id, tenant_id, from_addr, to_addr, active, created_at, updated_at
		FROM coremail_aliases WHERE id=`+r.dialect.Placeholder(1)+` AND tenant_id=`+r.dialect.Placeholder(2)+` AND deleted_at IS NULL`), id, tenantID).
		Scan(&a.ID, &a.DomainID, &a.TenantID, &a.FromAddr, &a.ToAddr, &active, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get alias: %w", err)
	}
	a.Active = active != 0
	return &a, nil
}

func (r *Repository) CreateAlias(ctx context.Context, tenantID, domainID uint, fromAddr, toAddr string) (*PlatformAlias, error) {
	now := time.Now().UTC()
	var id uint
	if r.dialect.IsPostgres() {
		if err := r.db.QueryRowContext(ctx, r.q(`INSERT INTO coremail_aliases (domain_id, tenant_id, from_addr, to_addr, active, created_at, updated_at)
			VALUES (?,?,?,?,1,?,?) RETURNING id`), domainID, tenantID, fromAddr, toAddr, now, now).Scan(&id); err != nil {
			return nil, fmt.Errorf("create alias: %w", err)
		}
	} else {
		res, err := r.db.ExecContext(ctx, r.q(`INSERT INTO coremail_aliases (domain_id, tenant_id, from_addr, to_addr, active, created_at, updated_at)
			VALUES (?,?,?,?,1,?,?)`), domainID, tenantID, fromAddr, toAddr, now, now)
		if err != nil {
			return nil, fmt.Errorf("create alias: %w", err)
		}
		lastID, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("create alias id: %w", err)
		}
		id = uint(lastID)
	}
	return &PlatformAlias{ID: id, TenantID: tenantID, DomainID: domainID, FromAddr: fromAddr, ToAddr: toAddr, Active: true, CreatedAt: now, UpdatedAt: now}, nil
}

// DeleteAlias soft-deletes an alias owned by tenantID.
func (r *Repository) DeleteAlias(ctx context.Context, id, tenantID uint) (bool, error) {
	now := time.Now().UTC()
	res, err := r.db.ExecContext(ctx, r.q(`UPDATE coremail_aliases SET deleted_at=?, updated_at=? WHERE id=? AND tenant_id=? AND deleted_at IS NULL`), now, now, id, tenantID)
	if err != nil {
		return false, fmt.Errorf("delete alias: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ── Groups and members ─────────────────────────────────────────────

func (r *Repository) ListGroups(ctx context.Context, f PlatformGroupFilter) ([]PlatformGroup, int64, error) {
	f.Limit = sanitizeLimit(f.Limit)
	var where []string
	var args []any
	where = append(where, "deleted_at IS NULL")
	where = append(where, "tenant_id = "+r.dialect.Placeholder(len(args)+1))
	args = append(args, f.TenantID)
	if f.Search != "" {
		where = append(where, "LOWER(name) LIKE "+r.dialect.Placeholder(len(args)+1))
		args = append(args, "%"+strings.ToLower(f.Search)+"%")
	}
	clause := " WHERE " + strings.Join(where, " AND ")

	var total int64
	if err := r.db.QueryRowContext(ctx, r.q(`SELECT COUNT(*) FROM coremail_groups`+clause), args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count groups: %w", err)
	}

	limitArgs := append(args, f.Limit, f.Offset)
	rows, err := r.db.QueryContext(ctx, r.q(`SELECT g.id, g.tenant_id, g.name, COALESCE(g.description,''), g.created_at, g.updated_at,
		(SELECT COUNT(*) FROM coremail_group_members m WHERE m.group_id = g.id) AS member_count
		FROM coremail_groups g`+clause+` ORDER BY g.id ASC LIMIT `+r.dialect.Placeholder(len(args)+1)+` OFFSET `+r.dialect.Placeholder(len(args)+2)), limitArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()

	var out []PlatformGroup
	for rows.Next() {
		var g PlatformGroup
		if err := rows.Scan(&g.ID, &g.TenantID, &g.Name, &g.Description, &g.CreatedAt, &g.UpdatedAt, &g.MemberCount); err != nil {
			return nil, 0, err
		}
		out = append(out, g)
	}
	return out, total, rows.Err()
}

func (r *Repository) GetGroup(ctx context.Context, id, tenantID uint) (*PlatformGroup, error) {
	var g PlatformGroup
	err := r.db.QueryRowContext(ctx, r.q(`SELECT g.id, g.tenant_id, g.name, COALESCE(g.description,''), g.created_at, g.updated_at,
		(SELECT COUNT(*) FROM coremail_group_members m WHERE m.group_id = g.id) AS member_count
		FROM coremail_groups g WHERE g.id=`+r.dialect.Placeholder(1)+` AND g.tenant_id=`+r.dialect.Placeholder(2)+` AND g.deleted_at IS NULL`), id, tenantID).
		Scan(&g.ID, &g.TenantID, &g.Name, &g.Description, &g.CreatedAt, &g.UpdatedAt, &g.MemberCount)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get group: %w", err)
	}
	return &g, nil
}

func (r *Repository) ListGroupMembers(ctx context.Context, groupID, tenantID uint) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, r.q(`SELECT m.email FROM coremail_group_members m
		JOIN coremail_groups g ON g.id = m.group_id
		WHERE m.group_id=`+r.dialect.Placeholder(1)+` AND g.tenant_id=`+r.dialect.Placeholder(2)+` AND g.deleted_at IS NULL
		ORDER BY m.email ASC`), groupID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list group members: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, err
		}
		out = append(out, email)
	}
	return out, rows.Err()
}

// CreateGroup inserts a new tenant group (coremail_groups, the SAME
// table the tenant self-service Groups page and the importer use).
// The UNIQUE(tenant_id, name) constraint is the real duplicate guard;
// a duplicate surfaces as ErrGroupExists so the service never
// fabricates a created group.
func (r *Repository) CreateGroup(ctx context.Context, tenantID uint, name, description string) (*PlatformGroup, error) {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, r.q(`INSERT INTO coremail_groups (tenant_id, name, description, created_at, updated_at)
		VALUES (`+r.dialect.Placeholders(5)+`)`), tenantID, name, description, now, now)
	if err != nil {
		if kernel.IsUniqueViolation(err) {
			return nil, ErrGroupExists
		}
		return nil, fmt.Errorf("create group: %w", err)
	}
	// Re-read by name (portable across sqlite/Postgres; avoids a
	// dialect-dependent RETURNING branch here).
	row := r.db.QueryRowContext(ctx, r.q(`SELECT g.id, g.tenant_id, g.name, COALESCE(g.description,''), g.created_at, g.updated_at,
		(SELECT COUNT(*) FROM coremail_group_members m WHERE m.group_id = g.id) AS member_count
		FROM coremail_groups g WHERE g.tenant_id=`+r.dialect.Placeholder(1)+` AND g.name=`+r.dialect.Placeholder(2)+` AND g.deleted_at IS NULL`), tenantID, name)
	var out PlatformGroup
	if err := row.Scan(&out.ID, &out.TenantID, &out.Name, &out.Description, &out.CreatedAt, &out.UpdatedAt, &out.MemberCount); err != nil {
		return nil, fmt.Errorf("read created group: %w", err)
	}
	return &out, nil
}

// SoftDeleteGroup tombstones a tenant group (deleted_at) without
// touching its member rows, matching the tenant self-service delete
// semantics. Returns found=false when the group does not exist in the
// tenant or is already deleted.
func (r *Repository) SoftDeleteGroup(ctx context.Context, id, tenantID uint) (found bool, err error) {
	now := time.Now().UTC()
	res, err := r.db.ExecContext(ctx, r.q(`UPDATE coremail_groups SET deleted_at=`+r.dialect.Placeholder(1)+`, updated_at=`+r.dialect.Placeholder(2)+`
		WHERE id=`+r.dialect.Placeholder(3)+` AND tenant_id=`+r.dialect.Placeholder(4)+` AND deleted_at IS NULL`), now, now, id, tenantID)
	if err != nil {
		return false, fmt.Errorf("soft delete group: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// AddGroupMember inserts a member email into a tenant-owned group.
// The UNIQUE(group_id, email) constraint is the real duplicate guard;
// a duplicate surfaces as ErrGroupMemberExists.
func (r *Repository) AddGroupMember(ctx context.Context, groupID, tenantID uint, email string) error {
	// The group must exist and be tenant-owned — the same predicate
	// ListGroupMembers uses. Never inserts a member row for a group the
	// caller cannot see.
	var exists int
	err := r.db.QueryRowContext(ctx, r.q(`SELECT COUNT(*) FROM coremail_groups WHERE id=`+r.dialect.Placeholder(1)+` AND tenant_id=`+r.dialect.Placeholder(2)+` AND deleted_at IS NULL`), groupID, tenantID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check group ownership: %w", err)
	}
	if exists == 0 {
		return ErrGroupNotFound
	}
	_, err = r.db.ExecContext(ctx, r.q(`INSERT INTO coremail_group_members (group_id, email, added_at) VALUES (`+r.dialect.Placeholders(3)+`)`), groupID, email, time.Now().UTC())
	if err != nil {
		if kernel.IsUniqueViolation(err) {
			return ErrGroupMemberExists
		}
		return fmt.Errorf("add group member: %w", err)
	}
	return nil
}

// RemoveGroupMember deletes one member row, scoped to the tenant
// through the group ownership subquery. Returns found=false when the
// member row does not exist in that tenant's group.
func (r *Repository) RemoveGroupMember(ctx context.Context, memberID, groupID, tenantID uint) (found bool, err error) {
	res, err := r.db.ExecContext(ctx, r.q(`DELETE FROM coremail_group_members WHERE id=`+r.dialect.Placeholder(1)+` AND group_id IN (
		SELECT id FROM coremail_groups WHERE id=`+r.dialect.Placeholder(2)+` AND tenant_id=`+r.dialect.Placeholder(3)+` AND deleted_at IS NULL)`), memberID, groupID, tenantID)
	if err != nil {
		return false, fmt.Errorf("remove group member: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ── Bulk mailbox status ────────────────────────────────────────────

// BulkSetMailboxStatus applies a status transition to many mailboxes
// within one tenant (and optionally one domain). RowsAffected is
// returned; per-row failures are reported by the caller via the admin
// mailbox service when available. This is the platform fallback that
// the service layer uses for cross-tenant batch operations.
func (r *Repository) BulkSetMailboxStatus(ctx context.Context, tenantID, domainID uint, ids []uint, status string, now time.Time) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(ids))
	idArgs := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = r.dialect.Placeholder(i + 1)
		idArgs[i] = id
	}
	inClause := strings.Join(placeholders, ",")
	args := []any{status, now, tenantID}
	args = append(args, idArgs...)
	query := `UPDATE coremail_mailboxes SET status=` + r.dialect.Placeholder(1) + `, updated_at=` + r.dialect.Placeholder(2) +
		` WHERE tenant_id=` + r.dialect.Placeholder(3) + ` AND deleted_at IS NULL AND id IN (` + inClause + `)`
	if domainID > 0 {
		query += ` AND domain_id=` + r.dialect.Placeholder(len(args)+1)
		args = append(args, domainID)
	}
	res, err := r.db.ExecContext(ctx, r.q(query), args...)
	if err != nil {
		return 0, fmt.Errorf("bulk mailbox status: %w", err)
	}
	return res.RowsAffected()
}
