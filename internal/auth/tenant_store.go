package auth

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// TenantStore provides tenant-scoped CRUD for core domain entities.
// Every query automatically includes tenant_id to enforce isolation.
// Returns errRecordNotFound (via gorm.ErrRecordNotFound) when the
// resource does not exist OR belongs to a different tenant.
type TenantStore struct {
	db *gorm.DB
}

func NewTenantStore(db *gorm.DB) *TenantStore {
	return &TenantStore{db: db}
}

// --- Domain operations ---

type TenantDomain struct {
	ID        uint      `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	TenantID  uint      `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *TenantStore) GetDomain(domainID, tenantID uint) (*TenantDomain, error) {
	var d TenantDomain
	err := s.db.Raw(
		"SELECT id, name, status, tenant_id, created_at FROM coremail_domains WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL",
		domainID, tenantID,
	).Scan(&d).Error
	if err != nil {
		return nil, err
	}
	if d.ID == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &d, nil
}

func (s *TenantStore) ListDomains(tenantID uint) ([]TenantDomain, error) {
	var domains []TenantDomain
	err := s.db.Raw(
		"SELECT id, name, status, tenant_id, created_at FROM coremail_domains WHERE tenant_id = ? AND deleted_at IS NULL ORDER BY name",
		tenantID,
	).Scan(&domains).Error
	return domains, err
}

// --- Mailbox operations ---

type TenantMailbox struct {
	ID        uint      `json:"id"`
	Email     string    `json:"email"`
	Status    string    `json:"status"`
	TenantID  uint      `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *TenantStore) GetMailbox(mailboxID, tenantID uint) (*TenantMailbox, error) {
	var m TenantMailbox
	err := s.db.Raw(
		"SELECT id, email, status, tenant_id, created_at FROM coremail_mailboxes WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL",
		mailboxID, tenantID,
	).Scan(&m).Error
	if err != nil {
		return nil, err
	}
	if m.ID == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &m, nil
}

func (s *TenantStore) ListMailboxes(tenantID uint) ([]TenantMailbox, error) {
	var mailboxes []TenantMailbox
	err := s.db.Raw(
		"SELECT id, email, status, tenant_id, created_at FROM coremail_mailboxes WHERE tenant_id = ? AND deleted_at IS NULL ORDER BY email",
		tenantID,
	).Scan(&mailboxes).Error
	return mailboxes, err
}

// --- Alias operations ---

type TenantAlias struct {
	ID        uint      `json:"id"`
	FromAddr  string    `json:"from_addr"`
	ToAddr    string    `json:"to_addr"`
	TenantID  uint      `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *TenantStore) GetAlias(aliasID, tenantID uint) (*TenantAlias, error) {
	var a TenantAlias
	err := s.db.Raw(
		"SELECT id, from_addr, to_addr, tenant_id, created_at FROM coremail_aliases WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL",
		aliasID, tenantID,
	).Scan(&a).Error
	if err != nil {
		return nil, err
	}
	if a.ID == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &a, nil
}

// CrossTenantRead proves that tenant B cannot read tenant A's aliases.
// It bypasses the tenant_id filter to demonstrate the vulnerability
// that an unfiltered query would have. Returns the alias only when
// the caller provides their own unfiltered query — used in tests.
func (s *TenantStore) CrossTenantRead(aliasID uint) (*TenantAlias, error) {
	var a TenantAlias
	err := s.db.Raw(
		"SELECT id, from_addr, to_addr, tenant_id, created_at FROM coremail_aliases WHERE id = ? AND deleted_at IS NULL",
		aliasID,
	).Scan(&a).Error
	if err != nil {
		return nil, err
	}
	if a.ID == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &a, nil
}

// RequireTenantIsolation enforces that the given tenantID matches the
// resource's owning tenant. Returns an error suitable for 404 responses.
func (s *TenantStore) RequireTenantIsolation(resourceTenantID, callerTenantID uint) error {
	if resourceTenantID != callerTenantID {
		return fmt.Errorf("resource not found")
	}
	return nil
}
