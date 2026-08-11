package importer

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/admin/mailbox"
	"github.com/orvix/orvix/internal/admin/organization"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/dkim"
	"github.com/orvix/orvix/internal/dbdialect"
)

// ProductionAdapterDeps carries the real business services the production
// adapters wrap. nil service fields are programming errors — the importer
// must never silently fall back to a stub.
type ProductionAdapterDeps struct {
	OrgService     *organization.Service
	DomainService  *domain.Service
	MailboxService *mailbox.Service
	DB             *sql.DB
	Dialect        *dbdialect.Info
}

// NewProductionAdapters builds the six importer ports around the real
// admin services. It fails closed if any required service is missing.
func NewProductionAdapters(deps ProductionAdapterDeps) (*Adapters, error) {
	if deps.OrgService == nil || deps.DomainService == nil || deps.MailboxService == nil {
		return nil, fmt.Errorf("import production adapters: org, domain and mailbox services are required")
	}
	if deps.DB == nil || deps.Dialect == nil {
		return nil, fmt.Errorf("import production adapters: db and dialect are required")
	}
	return NewAdapters(
		&prodOrgAdapter{svc: deps.OrgService},
		&prodAdminAdapter{db: deps.DB, dialect: deps.Dialect},
		&prodDomainAdapter{svc: deps.DomainService},
		&prodMailboxAdapter{svc: deps.MailboxService},
		&prodAliasAdapter{db: deps.DB, dialect: deps.Dialect},
		&prodGroupAdapter{db: deps.DB, dialect: deps.Dialect},
	), nil
}

// NewProductionAdaptersFromDB builds the production adapters directly from
// a database handle, constructing the real admin services the same way the
// router does (nil audit/rbac is tolerated by the services' mutateWithAudit
// paths). Used by the CLI so an import run performs real mutations.
func NewProductionAdaptersFromDB(db *sql.DB, dialect *dbdialect.Info) (*Adapters, error) {
	eng := coremail.NewEngine(coremail.EngineConfig{DB: db, AuthCfg: coremail.DefaultAuthConfig()})
	return NewProductionAdapters(ProductionAdapterDeps{
		OrgService:     organization.NewService(organization.NewOrganizationRepo(db), nil, nil),
		DomainService:  domain.NewService(domain.NewDomainAdminRepo(db), dkim.NewSQLRepo(db), nil, nil),
		MailboxService: mailbox.NewService(mailbox.NewAdminMailboxRepo(db), eng.Auth, nil, nil),
		DB:             db,
		Dialect:        dialect,
	})
}

// ── Organization ──────────────────────────────────────────────────────

type prodOrgAdapter struct{ svc *organization.Service }

func (a *prodOrgAdapter) CreateOrganization(ctx context.Context, name, domainName string, tenantID uint) (uint, error) {
	org, err := a.svc.CreateOrganization(ctx, organization.CreateOrganizationRequest{
		Name:   name,
		Slug:   slugify(name),
		Domain: domainName,
	}, tenantID)
	if err != nil {
		return 0, err
	}
	return org.ID, nil
}

func (a *prodOrgAdapter) SoftDeleteOrganization(ctx context.Context, id, tenantID uint) error {
	return a.svc.SetOrganizationActive(ctx, id, false, "import compensation")
}

// ── Tenant admin / user ───────────────────────────────────────────────

// prodAdminAdapter persists tenant-admin user rows through the real users
// table using the same hashing the rest of the platform uses. This is a
// real, portable persistence adapter — not a raw business SQL in the CLI.
type prodAdminAdapter struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func (a *prodAdminAdapter) CreateTenantAdmin(ctx context.Context, email, name, password, role string, tenantID uint) (uint, error) {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return 0, err
	}
	now := timeNow()
	return insertReturningID(ctx, a.db, a.dialect,
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, full_name) VALUES (?,?,?,?,?,?,1,1,?)`,
		now, now, strings.TrimSpace(email), hash, role, tenantID, name)
}

func (a *prodAdminAdapter) SoftDeleteUser(ctx context.Context, id, tenantID uint) error {
	_, err := a.db.ExecContext(ctx, a.dialect.Rewrite(`UPDATE users SET deleted_at=? WHERE id=? AND tenant_id=?`), timeNow(), id, tenantID)
	return err
}

// ── Domain ────────────────────────────────────────────────────────────

type prodDomainAdapter struct{ svc *domain.Service }

func (a *prodDomainAdapter) CreateDomain(ctx context.Context, name string, tenantID uint) (uint, error) {
	d, err := a.svc.CreateDomain(ctx, domain.CreateDomainRequest{Name: name}, tenantID)
	if err != nil {
		return 0, err
	}
	return d.ID, nil
}

func (a *prodDomainAdapter) SoftDeleteDomain(ctx context.Context, id, tenantID uint) error {
	return a.svc.DeleteDomain(ctx, id, tenantID)
}

// ── Mailbox ───────────────────────────────────────────────────────────

type prodMailboxAdapter struct{ svc *mailbox.Service }

func (a *prodMailboxAdapter) CreateMailbox(ctx context.Context, email, name, password, domainName string, tenantID uint) (uint, error) {
	resp, err := a.svc.CreateMailbox(ctx, mailbox.CreateMailboxRequest{Email: email, Name: name, Password: password}, tenantID)
	if err != nil {
		return 0, err
	}
	return resp.Mailbox.ID, nil
}

func (a *prodMailboxAdapter) SoftDeleteMailbox(ctx context.Context, id, tenantID uint) error {
	return a.svc.SoftDeleteMailbox(ctx, id, tenantID, "import compensation")
}

// ── Alias ─────────────────────────────────────────────────────────────

// prodAliasAdapter persists aliases directly through a dialect-portable
// insert. The coremail AliasSQLRepo uses LastInsertId which is not
// PostgreSQL-portable, so the importer adapter owns its own portable insert.
type prodAliasAdapter struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func (a *prodAliasAdapter) CreateAlias(ctx context.Context, fromEmail, toEmail string, tenantID, domainID uint) (uint, error) {
	if domainID == 0 {
		var err error
		domainID, err = resolveDomainID(ctx, a.db, a.dialect, toEmail, tenantID)
		if err != nil {
			return 0, err
		}
	}
	now := timeNow()
	return insertReturningID(ctx, a.db, a.dialect,
		`INSERT INTO coremail_aliases (domain_id, tenant_id, from_addr, to_addr, active, created_at, updated_at) VALUES (?,?,?,?,1,?,?)`,
		domainID, tenantID, fromEmail, toEmail, now, now)
}

func (a *prodAliasAdapter) SoftDeleteAlias(ctx context.Context, id, tenantID uint) error {
	_, err := a.db.ExecContext(ctx, a.dialect.Rewrite(`UPDATE coremail_aliases SET deleted_at=?, updated_at=? WHERE id=? AND tenant_id=?`), timeNow(), timeNow(), id, tenantID)
	return err
}

// ── Group ─────────────────────────────────────────────────────────────

// prodGroupAdapter persists customer-facing distribution groups through the
// real coremail_groups / coremail_group_members tables.
type prodGroupAdapter struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func (a *prodGroupAdapter) CreateGroup(ctx context.Context, name, description string, tenantID uint) (uint, error) {
	now := timeNow()
	return insertReturningID(ctx, a.db, a.dialect,
		`INSERT INTO coremail_groups (tenant_id, name, description, created_at, updated_at) VALUES (?,?,?,?,?)`,
		tenantID, name, description, now, now)
}

func (a *prodGroupAdapter) AddGroupMember(ctx context.Context, groupName, email string, tenantID uint) error {
	var groupID uint
	if err := a.db.QueryRowContext(ctx, a.dialect.Rewrite(`SELECT id FROM coremail_groups WHERE name=? AND tenant_id=? AND deleted_at IS NULL`), groupName, tenantID).Scan(&groupID); err != nil {
		return err
	}
	_, err := a.db.ExecContext(ctx, a.dialect.Rewrite(`INSERT INTO coremail_group_members (group_id, email, added_at) VALUES (?,?,?)`), groupID, email, timeNow())
	return err
}

func (a *prodGroupAdapter) SoftDeleteGroup(ctx context.Context, id, tenantID uint) error {
	_, err := a.db.ExecContext(ctx, a.dialect.Rewrite(`UPDATE coremail_groups SET deleted_at=?, updated_at=? WHERE id=? AND tenant_id=?`), timeNow(), timeNow(), id, tenantID)
	return err
}

func (a *prodGroupAdapter) RemoveGroupMember(ctx context.Context, memberID, tenantID uint) error {
	_, err := a.db.ExecContext(ctx, a.dialect.Rewrite(`DELETE FROM coremail_group_members WHERE id=? AND group_id IN (SELECT id FROM coremail_groups WHERE tenant_id=?)`), memberID, tenantID)
	return err
}

// ── helpers ───────────────────────────────────────────────────────────

func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '.' || r == '_':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "org"
	}
	return out
}

func resolveDomainID(ctx context.Context, db *sql.DB, dialect *dbdialect.Info, email string, tenantID uint) (uint, error) {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid email address: %s", email)
	}
	var id uint
	err := db.QueryRowContext(ctx, dialect.Rewrite(`SELECT id FROM coremail_domains WHERE name=? AND tenant_id=? AND deleted_at IS NULL`), parts[1], tenantID).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func timeNow() time.Time { return time.Now().UTC() }
