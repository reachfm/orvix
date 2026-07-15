package domain

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/dbdialect"
	entrbac "github.com/orvix/orvix/internal/enterprise/rbac"
)

type DomainAdminRepo struct {
	root    *sql.DB
	db      domainDB
	dialect *dbdialect.Info
}

type domainDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func NewDomainAdminRepo(db *sql.DB) *DomainAdminRepo {
	d, err := dbdialect.Detect(db)
	if err != nil {
		d = dbdialect.FromDriver("sqlite")
	}
	return &DomainAdminRepo{root: db, db: db, dialect: d}
}

func (r *DomainAdminRepo) BeginTx(ctx context.Context) (*sql.Tx, error) {
	return r.root.BeginTx(ctx, nil)
}

func (r *DomainAdminRepo) WithTx(tx *sql.Tx) *DomainAdminRepo {
	return &DomainAdminRepo{root: r.root, db: tx, dialect: r.dialect}
}

func (r *DomainAdminRepo) List(ctx context.Context, filter DomainFilter) ([]AdminDomain, int64, error) {
	var where []string
	var args []interface{}
	where = append(where, "d.deleted_at IS NULL")

	if filter.TenantID != nil {
		where = append(where, "d.tenant_id = "+r.dialect.Placeholder(len(args)+1))
		args = append(args, *filter.TenantID)
	}
	if filter.Status != nil && *filter.Status != "" {
		where = append(where, "d.status = "+r.dialect.Placeholder(len(args)+1))
		args = append(args, *filter.Status)
	}
	if filter.Search != "" {
		where = append(where, "d.name LIKE "+r.dialect.Placeholder(len(args)+1))
		args = append(args, "%"+filter.Search+"%")
	}

	clause := strings.Join(where, " AND ")

	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_domains d WHERE "+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Limit > 500 {
		filter.Limit = 500
	}

	query := `SELECT d.id, d.tenant_id, d.name, d.status, COALESCE(d.plan,''), COALESCE(d.description,''),
		d.max_mailboxes, d.max_aliases, d.max_quota_mb,
		d.dkim_enabled, COALESCE(d.dkim_selector,'mail'), d.dmarc_enabled,
		COALESCE((SELECT COUNT(*) FROM coremail_mailboxes m WHERE m.domain_id=d.id AND m.deleted_at IS NULL),0),
		COALESCE((SELECT COUNT(*) FROM coremail_aliases a WHERE a.domain_id=d.id AND a.deleted_at IS NULL),0),
		d.created_at, d.updated_at
		FROM coremail_domains d WHERE ` + clause + ` ORDER BY d.name ASC LIMIT ` + r.dialect.Placeholder(len(args)+1) + ` OFFSET ` + r.dialect.Placeholder(len(args)+2)
	args = append(args, filter.Limit, filter.Offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var domains []AdminDomain
	for rows.Next() {
		var d AdminDomain
		var dkimEnabled, dmarcEnabled int
		if err := rows.Scan(&d.ID, &d.TenantID, &d.Name, &d.Status, &d.Plan, &d.Description,
			&d.MaxMailboxes, &d.MaxAliases, &d.MaxQuotaMB,
			&dkimEnabled, &d.DKIMSelector, &dmarcEnabled,
			&d.MailboxCount, &d.AliasCount, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, 0, err
		}
		d.DKIMEnabled = dkimEnabled != 0
		d.DMARCEnabled = dmarcEnabled != 0
		domains = append(domains, d)
	}
	return domains, total, rows.Err()
}

func (r *DomainAdminRepo) GetByID(ctx context.Context, id, tenantID uint) (*AdminDomain, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT d.id, d.tenant_id, d.name, d.status, COALESCE(d.plan,''), COALESCE(d.description,''),
			d.max_mailboxes, d.max_aliases, d.max_quota_mb,
			d.dkim_enabled, COALESCE(d.dkim_selector,'mail'), d.dmarc_enabled,
			COALESCE((SELECT COUNT(*) FROM coremail_mailboxes m WHERE m.domain_id=d.id AND m.deleted_at IS NULL),0),
			COALESCE((SELECT COUNT(*) FROM coremail_aliases a WHERE a.domain_id=d.id AND a.deleted_at IS NULL),0),
			d.created_at, d.updated_at
		FROM coremail_domains d WHERE d.id = `+r.dialect.Placeholder(1)+` AND d.tenant_id = `+r.dialect.Placeholder(2)+` AND d.deleted_at IS NULL`, id, tenantID)
	return scanAdminDomain(row)
}

func (r *DomainAdminRepo) Create(ctx context.Context, d *AdminDomain) (*AdminDomain, error) {
	now := time.Now().UTC()
	d.CreatedAt = now
	d.UpdatedAt = now
	if d.Status == "" {
		d.Status = "active"
	}
	if d.Plan == "" {
		d.Plan = "smb"
	}
	if d.DKIMSelector == "" {
		d.DKIMSelector = "mail"
	}
	if d.MaxMailboxes == 0 {
		d.MaxMailboxes = 500
	}
	if d.MaxAliases == 0 {
		d.MaxAliases = 50
	}
	if d.MaxQuotaMB == 0 {
		d.MaxQuotaMB = 10240
	}

	res, err := r.db.ExecContext(ctx,
		"INSERT INTO coremail_domains (tenant_id, name, status, plan, description, max_mailboxes, max_aliases, max_quota_mb, dkim_enabled, dkim_selector, dmarc_enabled, created_at, updated_at) VALUES ("+r.dialect.Placeholders(13)+")",
		d.TenantID, d.Name, d.Status, d.Plan, d.Description, d.MaxMailboxes, d.MaxAliases, d.MaxQuotaMB,
		boolToInt(d.DKIMEnabled), d.DKIMSelector, boolToInt(d.DMARCEnabled), d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create domain: %w", err)
	}
	id, _ := res.LastInsertId()
	d.ID = uint(id)
	return d, nil
}

func (r *DomainAdminRepo) Update(ctx context.Context, d *AdminDomain) error {
	d.UpdatedAt = time.Now().UTC()
	_, err := r.db.ExecContext(ctx,
		"UPDATE coremail_domains SET description="+r.dialect.Placeholder(1)+", max_mailboxes="+r.dialect.Placeholder(2)+", max_aliases="+r.dialect.Placeholder(3)+", max_quota_mb="+r.dialect.Placeholder(4)+", dkim_enabled="+r.dialect.Placeholder(5)+", dmarc_enabled="+r.dialect.Placeholder(6)+", updated_at="+r.dialect.Placeholder(7)+" WHERE id="+r.dialect.Placeholder(8)+" AND tenant_id="+r.dialect.Placeholder(9)+" AND deleted_at IS NULL",
		d.Description, d.MaxMailboxes, d.MaxAliases, d.MaxQuotaMB, boolToInt(d.DKIMEnabled), boolToInt(d.DMARCEnabled), d.UpdatedAt, d.ID, d.TenantID)
	return err
}

func (r *DomainAdminRepo) UpdateStatus(ctx context.Context, id, tenantID uint, status string) error {
	_, err := r.db.ExecContext(ctx,
		"UPDATE coremail_domains SET status="+r.dialect.Placeholder(1)+", updated_at="+r.dialect.Placeholder(2)+" WHERE id="+r.dialect.Placeholder(3)+" AND tenant_id="+r.dialect.Placeholder(4)+" AND deleted_at IS NULL",
		status, time.Now().UTC(), id, tenantID)
	return err
}

func (r *DomainAdminRepo) CountByTenant(ctx context.Context, tenantID uint) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_domains WHERE tenant_id="+r.dialect.Placeholder(1)+" AND deleted_at IS NULL", tenantID).Scan(&count)
	return count, err
}

func (r *DomainAdminRepo) AssignDomainAdmin(ctx context.Context, domainID, userID, tenantID uint) error {
	_, err := r.db.ExecContext(ctx,
		"INSERT INTO coremail_admin_group_members (group_id, user_id) SELECT g.id, "+r.dialect.Placeholder(1)+" FROM coremail_admin_groups g WHERE g.tenant_id="+r.dialect.Placeholder(2)+" AND g.name='domain_admin' AND g.deleted_at IS NULL",
		userID, tenantID)
	if err != nil {
		return fmt.Errorf("assign domain admin: %w", err)
	}
	return nil
}

type Service struct {
	repo       *DomainAdminRepo
	auditStore *audit.ExtendedStore
	rbac       *entrbac.Evaluator
}

func NewService(repo *DomainAdminRepo, auditStore *audit.ExtendedStore, rbac *entrbac.Evaluator) *Service {
	return &Service{repo: repo, auditStore: auditStore, rbac: rbac}
}

func (s *Service) ListDomains(ctx context.Context, filter DomainFilter) ([]AdminDomain, int64, error) {
	return s.repo.List(ctx, filter)
}

func (s *Service) GetDomain(ctx context.Context, id, tenantID uint) (*AdminDomain, error) {
	d, err := s.repo.GetByID(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, ErrDomainNotFound
	}
	return d, nil
}

func (s *Service) CountByTenant(ctx context.Context, tenantID uint) (int64, error) {
	return s.repo.CountByTenant(ctx, tenantID)
}

func (s *Service) CreateDomain(ctx context.Context, req CreateDomainRequest, tenantID uint) (*AdminDomain, error) {
	d := &AdminDomain{
		TenantID:     tenantID,
		Name:         req.Name,
		MaxMailboxes: req.MaxMailboxes,
		MaxAliases:   req.MaxAliases,
		MaxQuotaMB:   req.MaxQuotaMB,
	}

	var created *AdminDomain
	entry := &audit.ExtendedEntry{Action: "domain.create", TenantID: tenantID, Result: "success"}
	if err := s.mutateWithAudit(ctx, entry, func(repo *DomainAdminRepo) error {
		var createErr error
		created, createErr = repo.Create(ctx, d)
		if createErr == nil {
			entry.Target, entry.TargetID = fmt.Sprintf("domain:%d", created.ID), created.ID
		}
		return createErr
	}); err != nil {
		return nil, err
	}
	return created, nil
}

func (s *Service) UpdateDomain(ctx context.Context, id, tenantID uint, req UpdateDomainRequest) (*AdminDomain, error) {
	d, err := s.repo.GetByID(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, ErrDomainNotFound
	}

	if req.Description != nil {
		d.Description = *req.Description
	}
	if req.MaxMailboxes != nil {
		d.MaxMailboxes = *req.MaxMailboxes
	}
	if req.MaxAliases != nil {
		d.MaxAliases = *req.MaxAliases
	}
	if req.MaxQuotaMB != nil {
		d.MaxQuotaMB = *req.MaxQuotaMB
	}
	if req.DKIMEnabled != nil {
		d.DKIMEnabled = *req.DKIMEnabled
	}
	if req.DMARCEnabled != nil {
		d.DMARCEnabled = *req.DMARCEnabled
	}

	entry := &audit.ExtendedEntry{Action: "domain.update", Target: fmt.Sprintf("domain:%d", d.ID), TargetID: d.ID, TenantID: tenantID, Result: "success"}
	if err := s.mutateWithAudit(ctx, entry, func(repo *DomainAdminRepo) error { return repo.Update(ctx, d) }); err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Service) SetDomainStatus(ctx context.Context, id, tenantID uint, status string, reason string) error {
	entry := &audit.ExtendedEntry{Action: "domain." + status, Target: fmt.Sprintf("domain:%d", id), TargetID: id, TenantID: tenantID, Result: "success", Reason: reason}
	return s.mutateWithAudit(ctx, entry, func(repo *DomainAdminRepo) error {
		return repo.UpdateStatus(ctx, id, tenantID, status)
	})
}

func (s *Service) mutateWithAudit(ctx context.Context, entry *audit.ExtendedEntry, mutate func(*DomainAdminRepo) error) error {
	if s.auditStore == nil {
		return mutate(s.repo)
	}
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("begin domain mutation: %w", err)
	}
	defer tx.Rollback()
	if err := mutate(s.repo.WithTx(tx)); err != nil {
		return err
	}
	if err := s.auditStore.RecordTx(ctx, tx, entry); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit domain mutation: %w", err)
	}
	return nil
}

func scanAdminDomain(row interface {
	Scan(dest ...interface{}) error
}) (*AdminDomain, error) {
	var d AdminDomain
	var dkimEnabled, dmarcEnabled int
	err := row.Scan(&d.ID, &d.TenantID, &d.Name, &d.Status, &d.Plan, &d.Description,
		&d.MaxMailboxes, &d.MaxAliases, &d.MaxQuotaMB,
		&dkimEnabled, &d.DKIMSelector, &dmarcEnabled,
		&d.MailboxCount, &d.AliasCount, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	d.DKIMEnabled = dkimEnabled != 0
	d.DMARCEnabled = dmarcEnabled != 0
	return &d, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

var ErrDomainNotFound = fmt.Errorf("domain not found")
