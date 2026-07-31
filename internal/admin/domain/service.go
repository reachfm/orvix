package domain

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/coremail/dkim"
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

func (r *DomainAdminRepo) GetByName(ctx context.Context, name string, tenantID uint) (*AdminDomain, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT d.id, d.tenant_id, d.name, d.status, COALESCE(d.plan,''), COALESCE(d.description,''),
			d.max_mailboxes, d.max_aliases, d.max_quota_mb,
			d.dkim_enabled, COALESCE(d.dkim_selector,'mail'), d.dmarc_enabled,
			COALESCE((SELECT COUNT(*) FROM coremail_mailboxes m WHERE m.domain_id=d.id AND m.deleted_at IS NULL),0),
			COALESCE((SELECT COUNT(*) FROM coremail_aliases a WHERE a.domain_id=d.id AND a.deleted_at IS NULL),0),
			d.created_at, d.updated_at
		FROM coremail_domains d WHERE d.name = `+r.dialect.Placeholder(1)+` AND d.tenant_id = `+r.dialect.Placeholder(2)+` AND d.deleted_at IS NULL`, name, tenantID)
	return scanAdminDomain(row)
}

func (r *DomainAdminRepo) DeleteByID(ctx context.Context, id, tenantID uint) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx,
		"UPDATE coremail_domains SET deleted_at="+r.dialect.Placeholder(1)+", updated_at="+r.dialect.Placeholder(2)+" WHERE id="+r.dialect.Placeholder(3)+" AND tenant_id="+r.dialect.Placeholder(4)+" AND deleted_at IS NULL",
		now, now, id, tenantID)
	return err
}

func (r *DomainAdminRepo) CountMailboxesByDomain(ctx context.Context, domainID, tenantID uint) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_mailboxes WHERE domain_id="+r.dialect.Placeholder(1)+" AND tenant_id="+r.dialect.Placeholder(2)+" AND deleted_at IS NULL", domainID, tenantID).Scan(&count)
	return count, err
}

func (r *DomainAdminRepo) CountAliasesByDomain(ctx context.Context, domainID, tenantID uint) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_aliases WHERE domain_id="+r.dialect.Placeholder(1)+" AND tenant_id="+r.dialect.Placeholder(2)+" AND deleted_at IS NULL", domainID, tenantID).Scan(&count)
	return count, err
}

func (r *DomainAdminRepo) GetDomainNameByID(ctx context.Context, domainID, tenantID uint) (string, error) {
	var name string
	err := r.db.QueryRowContext(ctx,
		"SELECT name FROM coremail_domains WHERE id="+r.dialect.Placeholder(1)+" AND tenant_id="+r.dialect.Placeholder(2)+" AND deleted_at IS NULL", domainID, tenantID).Scan(&name)
	if err != nil {
		return "", err
	}
	return name, nil
}

func (r *DomainAdminRepo) GetDomainForVerification(ctx context.Context, domainID, tenantID uint) (*AdminDomain, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT d.id, d.tenant_id, d.name, d.status, COALESCE(d.plan,''), COALESCE(d.description,''),
			d.max_mailboxes, d.max_aliases, d.max_quota_mb,
			d.dkim_enabled, COALESCE(d.dkim_selector,'mail'), d.dmarc_enabled,
			COALESCE((SELECT COUNT(*) FROM coremail_mailboxes m WHERE m.domain_id=d.id AND m.deleted_at IS NULL),0),
			COALESCE((SELECT COUNT(*) FROM coremail_aliases a WHERE a.domain_id=d.id AND a.deleted_at IS NULL),0),
			d.created_at, d.updated_at
		FROM coremail_domains d WHERE d.id = `+r.dialect.Placeholder(1)+` AND d.tenant_id = `+r.dialect.Placeholder(2)+` AND d.deleted_at IS NULL`, domainID, tenantID)
	return scanAdminDomain(row)
}

// UpdateDomainDKIMState marks a domain's DKIM configuration state inside the
// caller's transaction (or directly when no transaction is supplied).
func (r *DomainAdminRepo) UpdateDomainDKIMState(ctx context.Context, domainID, tenantID uint, enabled bool, selector string) error {
	_, err := r.db.ExecContext(ctx,
		"UPDATE coremail_domains SET dkim_enabled="+r.dialect.Placeholder(1)+", dkim_selector="+r.dialect.Placeholder(2)+", updated_at="+r.dialect.Placeholder(3)+" WHERE id="+r.dialect.Placeholder(4)+" AND tenant_id="+r.dialect.Placeholder(5)+" AND deleted_at IS NULL",
		boolToInt(enabled), selector, time.Now().UTC(), domainID, tenantID)
	return err
}

// DeleteDKIMConfigByDomain removes a domain's DKIM configuration row. It is
// called inside the caller's transaction where a transaction is active so the
// domain deletion and its DKIM cleanup commit or roll back as one unit.
func (r *DomainAdminRepo) DeleteDKIMConfigByDomain(ctx context.Context, domain string) error {
	_, err := r.db.ExecContext(ctx,
		"DELETE FROM coremail_dkim_config WHERE domain="+r.dialect.Placeholder(1), domain)
	return err
}

// GetByNameGlobal reports whether a normalized domain name already exists
// anywhere in the platform, regardless of tenant. Domain names are globally
// unique DNS names, so cross-tenant duplicates are conflicts. The result is a
// bare existence flag so a caller never learns which tenant owns the name.
func (r *DomainAdminRepo) GetByNameGlobal(ctx context.Context, name string) (bool, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_domains WHERE name="+r.dialect.Placeholder(1)+" AND deleted_at IS NULL", name).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetByNameGlobalDomain resolves a domain by its canonical name across the
// whole platform, ignoring tenant ownership. It is used only by platform-admin
// paths (no tenant context). The caller must not expose ownership details.
func (r *DomainAdminRepo) GetByNameGlobalDomain(ctx context.Context, name string) (*AdminDomain, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT d.id, d.tenant_id, d.name, d.status, COALESCE(d.plan,''), COALESCE(d.description,''),
			d.max_mailboxes, d.max_aliases, d.max_quota_mb,
			d.dkim_enabled, COALESCE(d.dkim_selector,'mail'), d.dmarc_enabled,
			COALESCE((SELECT COUNT(*) FROM coremail_mailboxes m WHERE m.domain_id=d.id AND m.deleted_at IS NULL),0),
			COALESCE((SELECT COUNT(*) FROM coremail_aliases a WHERE a.domain_id=d.id AND a.deleted_at IS NULL),0),
			d.created_at, d.updated_at
		FROM coremail_domains d WHERE d.name = `+r.dialect.Placeholder(1)+` AND d.deleted_at IS NULL`, name)
	return scanAdminDomain(row)
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
	dkimRepo   dkim.Repository
	auditStore *audit.ExtendedStore
	rbac       *entrbac.Evaluator
}

func NewService(repo *DomainAdminRepo, dkimRepo dkim.Repository, auditStore *audit.ExtendedStore, rbac *entrbac.Evaluator) *Service {
	return &Service{repo: repo, dkimRepo: dkimRepo, auditStore: auditStore, rbac: rbac}
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

func (s *Service) GetByName(ctx context.Context, name string, tenantID uint) (*AdminDomain, error) {
	d, err := s.repo.GetByName(ctx, name, tenantID)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Service) CountMailboxesByDomain(ctx context.Context, domainID, tenantID uint) (int64, error) {
	return s.repo.CountMailboxesByDomain(ctx, domainID, tenantID)
}

func (s *Service) CountAliasesByDomain(ctx context.Context, domainID, tenantID uint) (int64, error) {
	return s.repo.CountAliasesByDomain(ctx, domainID, tenantID)
}

func (s *Service) GetDomainNameByID(ctx context.Context, domainID, tenantID uint) (string, error) {
	return s.repo.GetDomainNameByID(ctx, domainID, tenantID)
}

// DomainExistsGlobal reports whether a normalized domain name already exists
// anywhere in the platform. Used for deterministic duplicate detection before
// the database-level unique constraint is hit.
func (s *Service) DomainExistsGlobal(ctx context.Context, name string) (bool, error) {
	return s.repo.GetByNameGlobal(ctx, name)
}

func (s *Service) DeleteDomain(ctx context.Context, id, tenantID uint) error {
	d, err := s.repo.GetByID(ctx, id, tenantID)
	if err != nil {
		return err
	}
	if d == nil {
		return ErrDomainNotFound
	}

	// Perform soft delete, dependency checks, DKIM cleanup and the audit
	// record inside a single transaction so they commit or roll back as one
	// unit. The mailbox/alias dependency checks are re-run inside the
	// transaction to close the check-then-act race with concurrent mailbox
	// creation.
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return ErrDomainDeleteFailed
	}
	defer tx.Rollback()

	repo := s.repo.WithTx(tx)

	// Check mailbox dependencies (inside the transaction).
	mbCount, err := repo.CountMailboxesByDomain(ctx, id, tenantID)
	if err != nil {
		return err
	}
	if mbCount > 0 {
		return ErrDomainHasMailboxes
	}

	// Check alias dependencies (inside the transaction).
	alCount, err := repo.CountAliasesByDomain(ctx, id, tenantID)
	if err != nil {
		return err
	}
	if alCount > 0 {
		return ErrDomainHasDependencies
	}

	if err := repo.DeleteByID(ctx, id, tenantID); err != nil {
		return ErrDomainDeleteFailed
	}
	// Clean up the associated DKIM configuration in the same transaction.
	if err := repo.DeleteDKIMConfigByDomain(ctx, d.Name); err != nil {
		return ErrDomainDeleteFailed
	}

	if s.auditStore != nil {
		entry := &audit.ExtendedEntry{Action: "domain.delete", Target: fmt.Sprintf("domain:%d", id), TargetID: id, TenantID: tenantID, Result: "success", After: d.Name}
		if err := s.auditStore.RecordTx(ctx, tx, entry); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return ErrDomainDeleteFailed
	}
	return nil
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
			return nil
		}
		// A concurrent duplicate create surfaces as a unique-constraint
		// violation at the database layer. Map it to the deterministic
		// duplicate contract instead of leaking a raw SQL error.
		if isUniqueViolation(createErr) {
			return ErrDomainAlreadyExists
		}
		return createErr
	}); err != nil {
		return nil, err
	}
	return created, nil
}

// isUniqueViolation reports whether an error is a database unique-constraint
// violation across the supported dialects.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "unique") && strings.Contains(msg, "violation") ||
		strings.Contains(msg, "constraint failed") ||
		strings.Contains(msg, "duplicate key")
}

type DKIMResult struct {
	Selector      string `json:"selector"`
	PublicDNSTxt  string `json:"public_dns_txt"`
	DNSRecordName string `json:"dns_record_name"`
}

// GenerateDKIM atomically provisions a DKIM key pair for a domain:
// it resolves the domain inside the tenant, checks (inside the same
// transaction) that no configuration already exists, inserts the new
// configuration, marks the domain's DKIM state, and writes the audit record
// — committing or rolling back as one unit. Concurrent generate calls are
// settled deterministically by the transaction: the losing caller receives
// ErrDKIMAlreadyConfigured instead of a generic failure.
func (s *Service) GenerateDKIM(ctx context.Context, id, tenantID uint, selector string) (*DKIMResult, error) {
	if s.dkimRepo == nil {
		return nil, fmt.Errorf("dkim repository unavailable")
	}
	d, err := s.repo.GetByID(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, ErrDomainNotFound
	}

	if selector == "" {
		selector = "mail"
	}

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin dkim generate: %w", err)
	}
	defer tx.Rollback()

	repo := s.repo.WithTx(tx)

	// Deterministic duplicate handling inside the transaction.
	existing, err := s.dkimRepo.GetByDomain(ctx, d.Name, tx)
	if err != nil {
		return nil, fmt.Errorf("check dkim config: %w", err)
	}
	if existing != nil {
		return nil, ErrDKIMAlreadyConfigured
	}

	privPEM, dnsValue, err := dkim.GenerateKeyPair(selector, d.Name)
	if err != nil {
		return nil, fmt.Errorf("dkim keygen: %w", err)
	}

	cfg := &dkim.DKIMConfig{
		Domain:        d.Name,
		Selector:      selector,
		PrivateKeyPEM: privPEM,
		Enabled:       true,
	}
	if err := s.dkimRepo.Create(ctx, cfg, tx); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDKIMAlreadyConfigured
		}
		return nil, fmt.Errorf("save dkim config: %w", err)
	}

	if err := repo.UpdateDomainDKIMState(ctx, id, tenantID, true, selector); err != nil {
		return nil, fmt.Errorf("mark domain dkim state: %w", err)
	}

	if s.auditStore != nil {
		entry := &audit.ExtendedEntry{
			Action:   "domain.dkim.generate",
			Target:   fmt.Sprintf("domain:%d", id),
			TargetID: id,
			TenantID: tenantID,
			Result:   "success",
			After:    fmt.Sprintf(`{"domain":"%s","selector":"%s"}`, d.Name, selector),
		}
		if err := s.auditStore.RecordTx(ctx, tx, entry); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit dkim generate: %w", err)
	}

	return &DKIMResult{
		Selector:      selector,
		PublicDNSTxt:  dnsValue,
		DNSRecordName: fmt.Sprintf("%s._domainkey.%s", selector, d.Name),
	}, nil
}

// RotateDKIM atomically replaces an existing DKIM configuration with a new
// key pair, updating the record and writing the audit event in one
// transaction. A domain without an existing configuration returns
// ErrDKIMNotConfigured; nothing is changed on failure.
func (s *Service) RotateDKIM(ctx context.Context, id, tenantID uint, selector string) (*DKIMResult, error) {
	if s.dkimRepo == nil {
		return nil, fmt.Errorf("dkim repository unavailable")
	}
	d, err := s.repo.GetByID(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, ErrDomainNotFound
	}

	if selector == "" {
		selector = "mail"
	}

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin dkim rotate: %w", err)
	}
	defer tx.Rollback()

	repo := s.repo.WithTx(tx)

	existing, err := s.dkimRepo.GetByDomain(ctx, d.Name, tx)
	if err != nil {
		return nil, fmt.Errorf("check dkim config: %w", err)
	}
	if existing == nil {
		return nil, ErrDKIMNotConfigured
	}

	privPEM, dnsValue, err := dkim.GenerateKeyPair(selector, d.Name)
	if err != nil {
		return nil, fmt.Errorf("dkim keygen: %w", err)
	}

	existing.Selector = selector
	existing.PrivateKeyPEM = privPEM
	existing.Enabled = true
	if err := s.dkimRepo.Update(ctx, existing, tx); err != nil {
		return nil, fmt.Errorf("update dkim config: %w", err)
	}

	if err := repo.UpdateDomainDKIMState(ctx, id, tenantID, true, selector); err != nil {
		return nil, fmt.Errorf("mark domain dkim state: %w", err)
	}

	if s.auditStore != nil {
		entry := &audit.ExtendedEntry{
			Action:   "domain.dkim.rotate",
			Target:   fmt.Sprintf("domain:%d", id),
			TargetID: id,
			TenantID: tenantID,
			Result:   "success",
			After:    fmt.Sprintf(`{"domain":"%s","selector":"%s"}`, d.Name, selector),
		}
		if err := s.auditStore.RecordTx(ctx, tx, entry); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit dkim rotate: %w", err)
	}

	return &DKIMResult{
		Selector:      selector,
		PublicDNSTxt:  dnsValue,
		DNSRecordName: fmt.Sprintf("%s._domainkey.%s", selector, d.Name),
	}, nil
}

func (s *Service) GetDKIM(ctx context.Context, id, tenantID uint) (*DKIMResult, error) {
	if s.dkimRepo == nil {
		return nil, fmt.Errorf("dkim repository unavailable")
	}
	d, err := s.repo.GetByID(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, ErrDomainNotFound
	}

	cfg, err := s.dkimRepo.GetByDomain(ctx, d.Name, nil)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil
	}

	pubKey, ok := deriveDKIMPublicKey(cfg.PrivateKeyPEM)
	if !ok {
		return nil, fmt.Errorf("public key derivation failed")
	}

	dnsName, dnsValue := dkim.GenerateDNSRecord(cfg.Selector, d.Name, pubKey)

	return &DKIMResult{
		Selector:      cfg.Selector,
		PublicDNSTxt:  dnsValue,
		DNSRecordName: dnsName,
	}, nil
}

// PlatformDKIM generates or rotates a DKIM key pair for a domain resolved by
// its canonical name (platform-admin context, no tenant). It is the single
// shared DKIM transaction used by the platform DNS-ops route and shares the
// exact repository, key generation, domain-state update, and audit logic of
// the enterprise flow.
//
// Behavior:
//   - Missing domain -> ErrDomainNotFound.
//   - No existing config -> create (audit "domain.dkim.generate").
//   - Existing config without confirmRotation -> ErrDKIMAlreadyConfigured
//     (typed 409, deterministic under concurrency).
//   - Existing config with confirmRotation=="rotate-dkim-key" -> rotate
//     (audit "domain.dkim.rotate").
//
// All writes and the audit record commit or roll back as one unit. The private
// key is never returned or logged.
func (s *Service) PlatformDKIM(ctx context.Context, domainName, selector, confirmRotation string) (*DKIMResult, error) {
	if s.dkimRepo == nil {
		return nil, fmt.Errorf("dkim repository unavailable")
	}
	if selector == "" {
		selector = "orvix"
	}
	d, err := s.repo.GetByNameGlobalDomain(ctx, domainName)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, ErrDomainNotFound
	}

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin dkim provision: %w", err)
	}
	defer tx.Rollback()

	repo := s.repo.WithTx(tx)

	existing, err := s.dkimRepo.GetByDomain(ctx, d.Name, tx)
	if err != nil {
		return nil, fmt.Errorf("check dkim config: %w", err)
	}
	if existing != nil && confirmRotation != "rotate-dkim-key" {
		return nil, ErrDKIMAlreadyConfigured
	}

	privPEM, dnsValue, err := dkim.GenerateKeyPair(selector, d.Name)
	if err != nil {
		return nil, fmt.Errorf("dkim keygen: %w", err)
	}

	action := "domain.dkim.generate"
	if existing != nil {
		action = "domain.dkim.rotate"
		existing.Selector = selector
		existing.PrivateKeyPEM = privPEM
		existing.Enabled = true
		if err := s.dkimRepo.Update(ctx, existing, tx); err != nil {
			if isUniqueViolation(err) {
				return nil, ErrDKIMAlreadyConfigured
			}
			return nil, fmt.Errorf("update dkim config: %w", err)
		}
	} else {
		cfg := &dkim.DKIMConfig{
			Domain:        d.Name,
			Selector:      selector,
			PrivateKeyPEM: privPEM,
			Enabled:       true,
		}
		if err := s.dkimRepo.Create(ctx, cfg, tx); err != nil {
			if isUniqueViolation(err) {
				return nil, ErrDKIMAlreadyConfigured
			}
			return nil, fmt.Errorf("save dkim config: %w", err)
		}
	}

	if err := repo.UpdateDomainDKIMState(ctx, d.ID, d.TenantID, true, selector); err != nil {
		return nil, fmt.Errorf("mark domain dkim state: %w", err)
	}

	if s.auditStore != nil {
		entry := &audit.ExtendedEntry{
			Action:   action,
			Target:   fmt.Sprintf("domain:%d", d.ID),
			TargetID: d.ID,
			TenantID: d.TenantID,
			Result:   "success",
			After:    fmt.Sprintf(`{"domain":"%s","selector":"%s"}`, d.Name, selector),
		}
		if err := s.auditStore.RecordTx(ctx, tx, entry); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit dkim provision: %w", err)
	}

	return &DKIMResult{
		Selector:      selector,
		PublicDNSTxt:  dnsValue,
		DNSRecordName: fmt.Sprintf("%s._domainkey.%s", selector, d.Name),
	}, nil
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

// SetDomainStatus validates and normalizes the requested status against the
// explicit domain-status model, then persists it transactionally with an audit
// record. Unsupported or unknown values are rejected (ErrInvalidDomainStatus)
// so no free-text status is ever persisted.
func (s *Service) SetDomainStatus(ctx context.Context, id, tenantID uint, status string, reason string) error {
	normalized, ok := ParseDomainStatus(status)
	if !ok {
		return ErrInvalidDomainStatus
	}
	entry := &audit.ExtendedEntry{Action: "domain." + string(normalized), Target: fmt.Sprintf("domain:%d", id), TargetID: id, TenantID: tenantID, Result: "success", Reason: reason}
	return s.mutateWithAudit(ctx, entry, func(repo *DomainAdminRepo) error {
		return repo.UpdateStatus(ctx, id, tenantID, string(normalized))
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

func deriveDKIMPublicKey(privPEM string) (string, bool) {
	block, _ := pem.Decode([]byte(privPEM))
	if block == nil {
		return "", false
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		if k1, err1 := x509.ParsePKCS1PrivateKey(block.Bytes); err1 == nil {
			keyAny = k1
		} else {
			return "", false
		}
	}
	rsaKey, ok := keyAny.(*rsa.PrivateKey)
	if !ok {
		return "", false
	}
	pubBytes, err := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	if err != nil {
		return "", false
	}
	recordName, recordValue := dkim.GenerateDNSRecord("ignored", "ignored", string(pubBytes))
	_ = recordName
	if i := strings.Index(recordValue, "p="); i >= 0 {
		return recordValue[i+2:], true
	}
	return "", false
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
