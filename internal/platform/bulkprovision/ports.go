package bulkprovision

import (
	"context"

	"github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/admin/mailbox"
)

// MailboxCreator is the narrow slice of internal/admin/mailbox.Service
// this package depends on. Defined here (consumer side) so
// bulkprovision depends on a port, not the concrete type — and so a
// test can substitute a fake without spinning up the full mailbox
// package's SQLite schema.
type MailboxCreator interface {
	CreateMailbox(ctx context.Context, req mailbox.CreateMailboxRequest, tenantID uint) (*mailbox.CreateMailboxResponse, error)
	ExistsByEmail(ctx context.Context, email string) (bool, error)
	ResolveDomainAllocation(ctx context.Context, domainName string, tenantID uint) (*mailbox.DomainAllocation, error)
	SoftDeleteMailbox(ctx context.Context, id, tenantID uint, reason string) error
	CountActiveByDomain(ctx context.Context, domainID, tenantID uint) (int, error)
}

// DomainAccessMode is the narrow slice of internal/admin/domain.Service
// needed to check mail_access_mode compatibility for imported rows.
type DomainAccessMode interface {
	GetMailAccessMode(ctx context.Context, id, tenantID uint) (domain.MailAccessMode, error)
}
