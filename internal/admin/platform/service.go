package platform

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/dbdialect"
	entrbac "github.com/orvix/orvix/internal/enterprise/rbac"
)

type PlatformService struct {
	db         *sql.DB
	auditStore *audit.ExtendedStore
	rbac       *entrbac.Evaluator
	dialect    *dbdialect.Info
}

func NewPlatformService(db *sql.DB, auditStore *audit.ExtendedStore, rbac *entrbac.Evaluator) *PlatformService {
	d, _ := dbdialect.Detect(db)
	if d == nil {
		d = dbdialect.FromDriver("sqlite")
	}
	return &PlatformService{db: db, auditStore: auditStore, rbac: rbac, dialect: d}
}

type OrganizationSummary struct {
	ID           uint   `json:"id"`
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	Domain       string `json:"domain"`
	Plan         string `json:"plan"`
	Active       bool   `json:"active"`
	MailboxCount int64  `json:"mailbox_count"`
	DomainCount  int64  `json:"domain_count"`
	CreatedAt    string `json:"created_at"`
}

func (s *PlatformService) ListOrganizationSummaries(ctx context.Context, search string, limit, offset int) ([]OrganizationSummary, int64, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	where := "t.deleted_at IS NULL"
	args := []interface{}{}
	if search != "" {
		where += " AND (t.name LIKE ? OR t.slug LIKE ? OR t.domain LIKE ?)"
		s := "%" + search + "%"
		args = append(args, s, s, s)
	}

	var total int64
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tenants t WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	query := fmt.Sprintf(`SELECT t.id, t.name, t.slug, t.domain, t.plan, t.active,
		COALESCE((SELECT COUNT(*) FROM coremail_mailboxes m WHERE m.tenant_id=t.id AND m.deleted_at IS NULL),0),
		COALESCE((SELECT COUNT(*) FROM coremail_domains d WHERE d.tenant_id=t.id AND d.deleted_at IS NULL),0),
		t.created_at
		FROM tenants t WHERE %s ORDER BY t.created_at DESC LIMIT ? OFFSET ?`, where)
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var summaries []OrganizationSummary
	for rows.Next() {
		var s OrganizationSummary
		var created time.Time
		var active int
		if err := rows.Scan(&s.ID, &s.Name, &s.Slug, &s.Domain, &s.Plan, &active, &s.MailboxCount, &s.DomainCount, &created); err != nil {
			return nil, 0, err
		}
		s.Active = active != 0
		s.CreatedAt = created.Format(time.RFC3339)
		summaries = append(summaries, s)
	}
	return summaries, total, rows.Err()
}

func (s *PlatformService) GetOrganizationDetail(ctx context.Context, id uint) (map[string]interface{}, error) {
	summary, err := s.getOrgSummary(ctx, id)
	if err != nil {
		return nil, err
	}
	if summary == nil {
		return nil, fmt.Errorf("organization not found")
	}

	adminRows, err := s.db.QueryContext(ctx,
		"SELECT id, email, role FROM users WHERE tenant_id=? AND role IN ('admin','superadmin','operator','readonly') AND deleted_at IS NULL", id)
	if err != nil {
		return nil, err
	}
	defer adminRows.Close()

	var admins []map[string]interface{}
	for adminRows.Next() {
		var uid uint
		var email, role string
		adminRows.Scan(&uid, &email, &role)
		admins = append(admins, map[string]interface{}{
			"id": uid, "email": email, "role": role,
		})
	}

	return map[string]interface{}{
		"organization": summary,
		"administrators": admins,
	}, nil
}

func (s *PlatformService) getOrgSummary(ctx context.Context, id uint) (*OrganizationSummary, error) {
	var srv OrganizationSummary
	var created time.Time
	var active int
	err := s.db.QueryRowContext(ctx, `SELECT t.id, t.name, t.slug, t.domain, t.plan, t.active,
		COALESCE((SELECT COUNT(*) FROM coremail_mailboxes m WHERE m.tenant_id=t.id AND m.deleted_at IS NULL),0),
		COALESCE((SELECT COUNT(*) FROM coremail_domains d WHERE d.tenant_id=t.id AND d.deleted_at IS NULL),0),
		t.created_at FROM tenants t WHERE t.id=? AND t.deleted_at IS NULL`, id).Scan(
		&srv.ID, &srv.Name, &srv.Slug, &srv.Domain, &srv.Plan, &active, &srv.MailboxCount, &srv.DomainCount, &created)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	srv.Active = active != 0
	srv.CreatedAt = created.Format(time.RFC3339)
	return &srv, nil
}
