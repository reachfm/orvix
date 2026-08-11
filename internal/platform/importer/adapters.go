package importer

import (
	"database/sql"
	"strings"
)

type OrgCreator interface {
	CreateOrg(name, domain string, tenantID uint) (uint, error)
}

type DomainCreator interface {
	CreateDomain(name string, tenantID uint) (uint, error)
}

type MailboxCreator interface {
	CreateMailbox(email, name, password, domainName string, tenantID uint) (uint, error)
}

type AliasCreator interface {
	CreateAlias(fromEmail, toEmail string, tenantID, domainID uint) (uint, error)
}

type GroupCreator interface {
	CreateGroup(name, description string, tenantID uint) (uint, error)
	AddGroupMember(groupID uint, email string) error
}

// ServiceAdapters wraps raw DB access for entity creation.
// In production these would wrap the actual admin services.
type ServiceAdapters struct {
	db      *sql.DB
	org     OrgCreator
	domain  DomainCreator
	mailbox MailboxCreator
	alias   AliasCreator
	group   GroupCreator
}

func NewServiceAdapters(db *sql.DB, org OrgCreator, domain DomainCreator, mailbox MailboxCreator, alias AliasCreator, group GroupCreator) *ServiceAdapters {
	return &ServiceAdapters{
		db:      db,
		org:     org,
		domain:  domain,
		mailbox: mailbox,
		alias:   alias,
		group:   group,
	}
}

func (a *ServiceAdapters) CreateOrganization(name, domain string, tenantID uint) (uint, error) {
	if a.org != nil {
		return a.org.CreateOrg(name, domain, tenantID)
	}
	var existing int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM tenants WHERE domain=? AND deleted_at IS NULL`, domain).Scan(&existing); err == nil && existing > 0 {
		return 0, nil
	}
	res, err := a.db.Exec(`INSERT INTO tenants (name, domain, plan, active, created_at, updated_at) VALUES (?,?,'free',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, name, domain)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return uint(id), nil
}

func (a *ServiceAdapters) CreateDomain(name string, tenantID uint) (uint, error) {
	if a.domain != nil {
		return a.domain.CreateDomain(name, tenantID)
	}
	var existing int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM coremail_domains WHERE name=? AND deleted_at IS NULL`, strings.ToLower(name)).Scan(&existing); err == nil && existing > 0 {
		return 0, nil
	}
	res, err := a.db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status, plan, created_at, updated_at) VALUES (?,?,'active','imported',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, tenantID, strings.ToLower(name))
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return uint(id), nil
}

func (a *ServiceAdapters) CreateMailbox(email, name, password, domainName string, tenantID uint) (uint, error) {
	if a.mailbox != nil {
		return a.mailbox.CreateMailbox(email, name, password, domainName, tenantID)
	}
	var existing int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM coremail_mailboxes WHERE email=? AND deleted_at IS NULL`, email).Scan(&existing); err == nil && existing > 0 {
		return 0, nil
	}
	var domainID int64
	if err := a.db.QueryRow(`SELECT id FROM coremail_domains WHERE name=? AND tenant_id=? AND deleted_at IS NULL`, strings.ToLower(domainName), tenantID).Scan(&domainID); err != nil {
		return 0, err
	}
	parts := strings.SplitN(email, "@", 2)
	localPart := parts[0]
	res, err := a.db.Exec(`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, status, quota_mb, is_admin, created_at, updated_at) VALUES (?,?,?,?,?,'active',1024,0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, domainID, tenantID, localPart, email, name)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return uint(id), nil
}

func (a *ServiceAdapters) CreateAlias(fromEmail, toEmail string, tenantID, domainID uint) (uint, error) {
	if a.alias != nil {
		return a.alias.CreateAlias(fromEmail, toEmail, tenantID, domainID)
	}
	var existing int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM coremail_aliases WHERE from_addr=? AND tenant_id=? AND deleted_at IS NULL`, fromEmail, tenantID).Scan(&existing); err == nil && existing > 0 {
		return 0, nil
	}
	res, err := a.db.Exec(`INSERT INTO coremail_aliases (domain_id, tenant_id, from_addr, to_addr, created_at, updated_at) VALUES (?,?,?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, domainID, tenantID, fromEmail, toEmail)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return uint(id), nil
}

func (a *ServiceAdapters) CreateGroup(name, description string, tenantID uint) (uint, error) {
	if a.group != nil {
		return a.group.CreateGroup(name, description, tenantID)
	}
	res, err := a.db.Exec(`INSERT INTO coremail_groups (tenant_id, name, description, created_at, updated_at) VALUES (?,?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, tenantID, name, description)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return uint(id), nil
}

func (a *ServiceAdapters) AddGroupMember(groupID uint, email string) error {
	if a.group != nil {
		return a.group.AddGroupMember(groupID, email)
	}
	_, err := a.db.Exec(`INSERT INTO coremail_group_members (group_id, email, created_at) VALUES (?,?,CURRENT_TIMESTAMP)`, groupID, email)
	return err
}
