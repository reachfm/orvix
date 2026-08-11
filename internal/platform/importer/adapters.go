package importer

import (
	"context"
	"fmt"
)

// Domain-level ports that the importer uses. Each port is a single-purpose
// interface consumed only by the executor/compensation paths. The importer
// itself never issues raw SQL against business tables. Soft-delete methods
// carry the tenantID so every mutation is tenant-scoped end to end.
type OrganizationPort interface {
	CreateOrganization(ctx context.Context, name, domain string, tenantID uint) (uint, error)
	SoftDeleteOrganization(ctx context.Context, id, tenantID uint) error
}

type TenantAdminPort interface {
	CreateTenantAdmin(ctx context.Context, email, name, password, role string, tenantID uint) (uint, error)
	SoftDeleteUser(ctx context.Context, id, tenantID uint) error
}

type DomainPort interface {
	CreateDomain(ctx context.Context, name string, tenantID uint) (uint, error)
	SoftDeleteDomain(ctx context.Context, id, tenantID uint) error
}

type MailboxPort interface {
	CreateMailbox(ctx context.Context, email, name, password, domainName string, tenantID uint) (uint, error)
	SoftDeleteMailbox(ctx context.Context, id, tenantID uint) error
}

type AliasPort interface {
	CreateAlias(ctx context.Context, fromEmail, toEmail string, tenantID, domainID uint) (uint, error)
	SoftDeleteAlias(ctx context.Context, id, tenantID uint) error
}

type GroupPort interface {
	CreateGroup(ctx context.Context, name, description string, tenantID uint) (uint, error)
	AddGroupMember(ctx context.Context, groupName, email string, tenantID uint) error
	SoftDeleteGroup(ctx context.Context, id, tenantID uint) error
	RemoveGroupMember(ctx context.Context, memberID, tenantID uint) error
}

type Compensator interface {
	SoftDeleteOrg(ctx context.Context, id, tenantID uint) error
	SoftDeleteUser(ctx context.Context, id, tenantID uint) error
	SoftDeleteDomain(ctx context.Context, id, tenantID uint) error
	SoftDeleteMailbox(ctx context.Context, id, tenantID uint) error
	SoftDeleteAlias(ctx context.Context, id, tenantID uint) error
	SoftDeleteGroup(ctx context.Context, id, tenantID uint) error
	RemoveGroupMember(ctx context.Context, memberID, tenantID uint) error
}

// Adapters is a pure port registry. Every port is required at construction
// time — there is no direct SQL fallback. Tests supply lightweight in-
// memory implementations; production supplies real admin services.
type Adapters struct {
	Org     OrganizationPort
	Admin   TenantAdminPort
	Domain  DomainPort
	Mailbox MailboxPort
	Alias   AliasPort
	Group   GroupPort
}

func NewAdapters(org OrganizationPort, admin TenantAdminPort, domain DomainPort, mailbox MailboxPort, alias AliasPort, group GroupPort) *Adapters {
	return &Adapters{
		Org:     org,
		Admin:   admin,
		Domain:  domain,
		Mailbox: mailbox,
		Alias:   alias,
		Group:   group,
	}
}

func (a *Adapters) Validate() error {
	if a.Org == nil || a.Admin == nil || a.Domain == nil || a.Mailbox == nil || a.Alias == nil || a.Group == nil {
		return fmt.Errorf("import adapters: all ports must be non-nil")
	}
	return nil
}
