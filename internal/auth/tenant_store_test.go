package auth

import (
	"testing"
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// tenantIsolationEnv sets up two independent tenants with their own
// domain, mailbox, and alias for adversarial cross-tenant tests.
type tenantIsolationEnv struct {
	db         *gorm.DB
	store      *TenantStore
	tenantA    uint
	tenantB    uint
	domainA    uint
	domainB    uint
	mailboxA   uint
	mailboxB   uint
	aliasA     uint
	aliasB     uint
}

func buildTenantIsolationEnv(t *testing.T) *tenantIsolationEnv {
	t.Helper()
	logger := zap.NewNop()
	tmp := t.TempDir()
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    tmp + "/orvix_isolation.db?_loc=auto&_busy_timeout=5000&_txlock=immediate",
	}
	db, err := config.NewDatabase(cfg, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})

	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("MigrateAllRaw: %v", err)
	}

	now := time.Now()

	// Tenant A
	if err := db.Exec("INSERT INTO tenants (name, slug, domain, active, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		"Tenant A", "tenant-a", "a.example.com", 1, now, now).Error; err != nil {
		t.Fatalf("insert tenant A: %v", err)
	}
	var tenantA uint
	db.Raw("SELECT id FROM tenants WHERE slug = ?", "tenant-a").Scan(&tenantA)

	// Tenant B
	if err := db.Exec("INSERT INTO tenants (name, slug, domain, active, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		"Tenant B", "tenant-b", "b.example.com", 1, now, now).Error; err != nil {
		t.Fatalf("insert tenant B: %v", err)
	}
	var tenantB uint
	db.Raw("SELECT id FROM tenants WHERE slug = ?", "tenant-b").Scan(&tenantB)

	// Domain A
	if err := db.Exec("INSERT INTO coremail_domains (name, tenant_id, created_at, updated_at) VALUES (?, ?, ?, ?)",
		"domain-a.com", tenantA, now, now).Error; err != nil {
		t.Fatalf("insert domain A: %v", err)
	}
	var domainA uint
	db.Raw("SELECT id FROM coremail_domains WHERE name = ?", "domain-a.com").Scan(&domainA)

	// Domain B
	if err := db.Exec("INSERT INTO coremail_domains (name, tenant_id, created_at, updated_at) VALUES (?, ?, ?, ?)",
		"domain-b.com", tenantB, now, now).Error; err != nil {
		t.Fatalf("insert domain B: %v", err)
	}
	var domainB uint
	db.Raw("SELECT id FROM coremail_domains WHERE name = ?", "domain-b.com").Scan(&domainB)

	// Mailbox A
	if err := db.Exec("INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, password_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		domainA, tenantA, "user-a", "user-a@domain-a.com", "hash", now, now).Error; err != nil {
		t.Fatalf("insert mailbox A: %v", err)
	}
	var mailboxA uint
	db.Raw("SELECT id FROM coremail_mailboxes WHERE email = ?", "user-a@domain-a.com").Scan(&mailboxA)

	// Mailbox B
	if err := db.Exec("INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, password_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		domainB, tenantB, "user-b", "user-b@domain-b.com", "hash", now, now).Error; err != nil {
		t.Fatalf("insert mailbox B: %v", err)
	}
	var mailboxB uint
	db.Raw("SELECT id FROM coremail_mailboxes WHERE email = ?", "user-b@domain-b.com").Scan(&mailboxB)

	// Alias A
	if err := db.Exec("INSERT INTO coremail_aliases (domain_id, tenant_id, from_addr, to_addr, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		domainA, tenantA, "alias-a@domain-a.com", "user-a@domain-a.com", now, now).Error; err != nil {
		t.Fatalf("insert alias A: %v", err)
	}
	var aliasA uint
	db.Raw("SELECT id FROM coremail_aliases WHERE from_addr = ?", "alias-a@domain-a.com").Scan(&aliasA)

	// Alias B
	if err := db.Exec("INSERT INTO coremail_aliases (domain_id, tenant_id, from_addr, to_addr, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		domainB, tenantB, "alias-b@domain-b.com", "user-b@domain-b.com", now, now).Error; err != nil {
		t.Fatalf("insert alias B: %v", err)
	}
	var aliasB uint
	if err := db.Raw("SELECT id FROM coremail_aliases WHERE from_addr = ?", "alias-b@domain-b.com").Scan(&aliasB).Error; err != nil {
		t.Fatalf("get alias B: %v", err)
	}

	store := NewTenantStore(db)
	return &tenantIsolationEnv{
		db: db, store: store,
		tenantA: tenantA, tenantB: tenantB,
		domainA: domainA, domainB: domainB,
		mailboxA: mailboxA, mailboxB: mailboxB,
		aliasA: aliasA, aliasB: aliasB,
	}
}

func TestTenantStore_OwnDomainAccessible(t *testing.T) {
	env := buildTenantIsolationEnv(t)
	d, err := env.store.GetDomain(env.domainA, env.tenantA)
	if err != nil {
		t.Fatalf("tenant A could not read own domain: %v", err)
	}
	if d.Name != "domain-a.com" {
		t.Fatalf("expected domain-a.com, got %s", d.Name)
	}
}

func TestTenantStore_CrossTenantDomainInaccessible(t *testing.T) {
	env := buildTenantIsolationEnv(t)
	_, err := env.store.GetDomain(env.domainB, env.tenantA)
	if err == nil {
		t.Fatal("tenant A should NOT be able to read tenant B's domain; expected error")
	}
}

func TestTenantStore_OwnMailboxAccessible(t *testing.T) {
	env := buildTenantIsolationEnv(t)
	m, err := env.store.GetMailbox(env.mailboxA, env.tenantA)
	if err != nil {
		t.Fatalf("tenant A could not read own mailbox: %v", err)
	}
	if m.Email != "user-a@domain-a.com" {
		t.Fatalf("expected user-a@domain-a.com, got %s", m.Email)
	}
}

func TestTenantStore_CrossTenantMailboxInaccessible(t *testing.T) {
	env := buildTenantIsolationEnv(t)
	_, err := env.store.GetMailbox(env.mailboxB, env.tenantA)
	if err == nil {
		t.Fatal("tenant A should NOT be able to read tenant B's mailbox; expected error")
	}
}

func TestTenantStore_OwnAliasAccessible(t *testing.T) {
	env := buildTenantIsolationEnv(t)
	a, err := env.store.GetAlias(env.aliasA, env.tenantA)
	if err != nil {
		t.Fatalf("tenant A could not read own alias: %v", err)
	}
	if a.FromAddr != "alias-a@domain-a.com" {
		t.Fatalf("expected alias-a@domain-a.com, got %s", a.FromAddr)
	}
}

func TestTenantStore_CrossTenantAliasInaccessible(t *testing.T) {
	env := buildTenantIsolationEnv(t)
	_, err := env.store.GetAlias(env.aliasB, env.tenantA)
	if err == nil {
		t.Fatal("tenant A should NOT be able to read tenant B's alias; expected error")
	}
}

func TestTenantStore_CrossTenantReadBypassDetected(t *testing.T) {
	// This test proves that an unfiltered query (no tenant_id) would
	// expose data across tenants — confirming the TenantStore's
	// tenant-scoped query is necessary.
	env := buildTenantIsolationEnv(t)

	// CrossTenantRead deliberately omits tenant_id filter.
	alias, err := env.store.CrossTenantRead(env.aliasB)
	if err != nil {
		t.Fatalf("CrossTenantRead (deliberately unfiltered) should find alias B: %v", err)
	}
	if alias.TenantID == env.tenantA {
		t.Fatal("CrossTenantRead returned alias B but with tenant A ownership — data corrupted")
	}
	if alias.TenantID != env.tenantB {
		t.Fatalf("alias B tenant_id should be %d, got %d", env.tenantB, alias.TenantID)
	}

	// Now verify that TenantStore's tenant-scoped GetAlias blocks it.
	_, err = env.store.GetAlias(env.aliasB, env.tenantA)
	if err == nil {
		t.Fatal("tenant A should NOT read alias B via tenant-scoped GetAlias")
	}
}

func TestTenantStore_ListDomainsScoped(t *testing.T) {
	env := buildTenantIsolationEnv(t)
	domains, err := env.store.ListDomains(env.tenantA)
	if err != nil {
		t.Fatalf("list domains for tenant A: %v", err)
	}
	if len(domains) != 1 {
		t.Fatalf("tenant A should have 1 domain, got %d", len(domains))
	}
	if domains[0].Name != "domain-a.com" {
		t.Fatalf("expected domain-a.com, got %s", domains[0].Name)
	}
}

func TestTenantStore_ListMailboxesScoped(t *testing.T) {
	env := buildTenantIsolationEnv(t)
	mailboxes, err := env.store.ListMailboxes(env.tenantA)
	if err != nil {
		t.Fatalf("list mailboxes for tenant A: %v", err)
	}
	if len(mailboxes) != 1 {
		t.Fatalf("tenant A should have 1 mailbox, got %d", len(mailboxes))
	}
	if mailboxes[0].Email != "user-a@domain-a.com" {
		t.Fatalf("expected user-a@domain-a.com, got %s", mailboxes[0].Email)
	}
}

func TestTenantStore_RequireTenantIsolation(t *testing.T) {
	env := buildTenantIsolationEnv(t)

	// Same tenant — passes.
	if err := env.store.RequireTenantIsolation(env.tenantA, env.tenantA); err != nil {
		t.Fatalf("same tenant should pass: %v", err)
	}

	// Different tenant — fails.
	if err := env.store.RequireTenantIsolation(env.tenantB, env.tenantA); err == nil {
		t.Fatal("cross-tenant should fail")
	}
}
