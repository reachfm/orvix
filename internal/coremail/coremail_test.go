package coremail

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"
)

// Compile-time interface checks.
var _ DomainRepository = (*DomainSQLRepo)(nil)
var _ MailboxRepository = (*MailboxSQLRepo)(nil)
var _ AliasRepository = (*AliasSQLRepo)(nil)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS coremail_domains (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			reseller_id INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'active',
			plan TEXT NOT NULL DEFAULT 'smb',
			description TEXT NOT NULL DEFAULT '',
			max_mailboxes INTEGER NOT NULL DEFAULT 0,
			max_aliases INTEGER NOT NULL DEFAULT 0,
			max_quota_mb INTEGER NOT NULL DEFAULT 0,
			dkim_enabled INTEGER NOT NULL DEFAULT 0,
			dkim_selector TEXT NOT NULL DEFAULT '',
			dmarc_enabled INTEGER NOT NULL DEFAULT 0,
			mtasts_enabled INTEGER NOT NULL DEFAULT 0,
			catchall_address TEXT NOT NULL DEFAULT '',
			abuse_contact TEXT NOT NULL DEFAULT '',
			labels TEXT NOT NULL DEFAULT '',
			mailbox_count INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		);
		CREATE TABLE IF NOT EXISTS coremail_mailboxes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			local_part TEXT NOT NULL,
			email TEXT UNIQUE NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			password_hash TEXT NOT NULL,
			auth_scheme TEXT NOT NULL DEFAULT 'argon2id',
			mfa_enabled INTEGER NOT NULL DEFAULT 0,
			mfa_secret TEXT NOT NULL DEFAULT '',
			app_passwords TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			quota_mb INTEGER NOT NULL DEFAULT 0,
			used_bytes INTEGER NOT NULL DEFAULT 0,
			msg_count INTEGER NOT NULL DEFAULT 0,
			is_admin INTEGER NOT NULL DEFAULT 0,
			is_forwarder INTEGER NOT NULL DEFAULT 0,
			forward_to TEXT NOT NULL DEFAULT '',
			labels TEXT NOT NULL DEFAULT '',
			send_limit_per_hour INTEGER NOT NULL DEFAULT 0,
			recv_limit_per_hour INTEGER NOT NULL DEFAULT 0,
			last_login DATETIME,
			last_ip TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		);
		CREATE TABLE IF NOT EXISTS coremail_aliases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			from_addr TEXT NOT NULL,
			to_addr TEXT NOT NULL,
			active INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		);
	`)
	if err != nil {
		t.Fatalf("create tables: %v", err)
	}
	return db
}

func TestDomainRepositoryCreateAndGet(t *testing.T) {
	db := testDB(t)
	repo := NewDomainSQLRepo(db)
	ctx := context.Background()

	d := &Domain{
		Name:       "test-enterprise.example.com",
		TenantID:   1,
		Plan:       "enterprise",
		Status:     DomainActive,
		MaxMailboxes: 1000,
		MaxAliases:   500,
		DKIMEnabled: true,
		DMARCEnabled: true,
	}

	if err := repo.Create(ctx, d, nil); err != nil {
		t.Fatalf("create domain: %v", err)
	}
	if d.ID == 0 {
		t.Fatal("expected non-zero ID after create")
	}

	got, err := repo.GetByID(ctx, d.ID, nil)
	if err != nil {
		t.Fatalf("get domain: %v", err)
	}
	if got == nil {
		t.Fatal("expected domain, got nil")
	}
	if got.Name != "test-enterprise.example.com" {
		t.Fatalf("expected name 'test-enterprise.example.com', got %q", got.Name)
	}
	if got.TenantID != 1 {
		t.Fatalf("expected tenant 1, got %d", got.TenantID)
	}
	if !got.DKIMEnabled {
		t.Fatal("expected DKIM enabled")
	}
	if !got.DMARCEnabled {
		t.Fatal("expected DMARC enabled")
	}
	if got.Status != DomainActive {
		t.Fatalf("expected active, got %s", got.Status)
	}
	if got.MaxMailboxes != 1000 {
		t.Fatalf("expected 1000 max mailboxes, got %d", got.MaxMailboxes)
	}
}

func TestDomainRepositoryGetByName(t *testing.T) {
	db := testDB(t)
	repo := NewDomainSQLRepo(db)
	ctx := context.Background()

	d := &Domain{Name: "by-name.example.com", TenantID: 1, Status: DomainActive}
	if err := repo.Create(ctx, d, nil); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.GetByName(ctx, "by-name.example.com", nil)
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if got == nil {
		t.Fatal("expected domain")
	}
	if got.Name != "by-name.example.com" {
		t.Fatalf("expected by-name.example.com, got %q", got.Name)
	}
}

func TestDomainRepositoryList(t *testing.T) {
	db := testDB(t)
	repo := NewDomainSQLRepo(db)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		d := &Domain{
			Name:     fmt.Sprintf("list-%d.example.com", i),
			TenantID: uint(1 + i%2),
			Plan:     "smb",
			Status:   DomainActive,
		}
		if err := repo.Create(ctx, d, nil); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	tid := uint(1)
	domains, total, err := repo.List(ctx, DomainFilter{TenantID: &tid, Pagination: Pagination{Limit: 10}}, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 {
		t.Fatalf("expected 3 domains for tenant 1, got %d", total)
	}
	if len(domains) != 3 {
		t.Fatalf("expected 3 domains, got %d", len(domains))
	}
}

func TestDomainRepositorySoftDelete(t *testing.T) {
	db := testDB(t)
	repo := NewDomainSQLRepo(db)
	ctx := context.Background()

	d := &Domain{Name: "delete-me.example.com", TenantID: 1, Status: DomainActive}
	if err := repo.Create(ctx, d, nil); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := repo.Delete(ctx, d.ID, nil); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, err := repo.GetByID(ctx, d.ID, nil)
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil after soft delete")
	}

	exists, err := repo.Exists(ctx, "delete-me.example.com", nil)
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if exists {
		t.Fatal("expected domain to not exist after delete")
	}
}

func TestDomainRepositoryUpdate(t *testing.T) {
	db := testDB(t)
	repo := NewDomainSQLRepo(db)
	ctx := context.Background()

	d := &Domain{Name: "update-test.example.com", TenantID: 1, Plan: "smb", Status: DomainActive}
	if err := repo.Create(ctx, d, nil); err != nil {
		t.Fatalf("create: %v", err)
	}

	d.Plan = "enterprise"
	d.MaxMailboxes = 5000
	d.DKIMEnabled = true
	if err := repo.Update(ctx, d, nil); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := repo.GetByID(ctx, d.ID, nil)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Plan != "enterprise" {
		t.Fatalf("expected enterprise plan, got %q", got.Plan)
	}
	if got.MaxMailboxes != 5000 {
		t.Fatalf("expected 5000 mailboxes, got %d", got.MaxMailboxes)
	}
	if !got.DKIMEnabled {
		t.Fatal("expected DKIM enabled")
	}
}

func TestMailboxRepositoryCreateAndGet(t *testing.T) {
	db := testDB(t)
	repo := NewMailboxSQLRepo(db)
	domRepo := NewDomainSQLRepo(db)
	ctx := context.Background()

	dom := &Domain{Name: "mb-test.example.com", TenantID: 1, Status: DomainActive}
	if err := domRepo.Create(ctx, dom, nil); err != nil {
		t.Fatalf("create domain: %v", err)
	}

	m := &Mailbox{
		DomainID:     dom.ID,
		TenantID:     1,
		LocalPart:    "user",
		Email:        "user@mb-test.example.com",
		Name:         "Test User",
		PasswordHash: "argon2hash...",
		Status:       MailboxActive,
		QuotaMB:      1024,
		IsAdmin:      false,
	}
	if err := repo.Create(ctx, m, nil); err != nil {
		t.Fatalf("create mailbox: %v", err)
	}
	if m.ID == 0 {
		t.Fatal("expected non-zero ID")
	}

	got, err := repo.GetByID(ctx, m.ID, nil)
	if err != nil {
		t.Fatalf("get mailbox: %v", err)
	}
	if got == nil {
		t.Fatal("expected mailbox, got nil")
	}
	if got.Email != "user@mb-test.example.com" {
		t.Fatalf("expected user@mb-test.example.com, got %q", got.Email)
	}
	if got.LocalPart != "user" {
		t.Fatalf("expected local-part 'user', got %q", got.LocalPart)
	}
	if got.QuotaMB != 1024 {
		t.Fatalf("expected 1024 MB quota, got %d", got.QuotaMB)
	}
}

func TestMailboxRepositoryGetByEmail(t *testing.T) {
	db := testDB(t)
	repo := NewMailboxSQLRepo(db)
	domRepo := NewDomainSQLRepo(db)
	ctx := context.Background()

	dom := &Domain{Name: "getbyemail.example.com", TenantID: 1, Status: DomainActive}
	domRepo.Create(ctx, dom, nil)

	repo.Create(ctx, &Mailbox{DomainID: dom.ID, TenantID: 1, LocalPart: "findme", Email: "findme@getbyemail.example.com", PasswordHash: "h", Status: MailboxActive}, nil)

	got, err := repo.GetByEmail(ctx, "findme@getbyemail.example.com", nil)
	if err != nil {
		t.Fatalf("get by email: %v", err)
	}
	if got == nil || got.Email != "findme@getbyemail.example.com" {
		t.Fatal("mailbox not found by email")
	}
}

func TestMailboxRepositoryList(t *testing.T) {
	db := testDB(t)
	repo := NewMailboxSQLRepo(db)
	domRepo := NewDomainSQLRepo(db)
	ctx := context.Background()

	dom := &Domain{Name: "mbox-list.example.com", TenantID: 1, Status: DomainActive}
	domRepo.Create(ctx, dom, nil)

	for i := 0; i < 3; i++ {
		repo.Create(ctx, &Mailbox{
			DomainID: dom.ID, TenantID: 1,
			LocalPart:    fmt.Sprintf("u%d", i),
			Email:        fmt.Sprintf("u%d@mbox-list.example.com", i),
			PasswordHash: "h",
			Status:       MailboxActive,
		}, nil)
	}

	did := dom.ID
	mailboxes, total, err := repo.List(ctx, MailboxFilter{DomainID: &did, Pagination: Pagination{Limit: 10}}, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 {
		t.Fatalf("expected 3, got %d", total)
	}
	if len(mailboxes) != 3 {
		t.Fatalf("expected 3 results, got %d", len(mailboxes))
	}
}

func TestMailboxRepositoryUpdateQuota(t *testing.T) {
	db := testDB(t)
	repo := NewMailboxSQLRepo(db)
	domRepo := NewDomainSQLRepo(db)
	ctx := context.Background()

	dom := &Domain{Name: "quota-test.example.com", TenantID: 1, Status: DomainActive}
	domRepo.Create(ctx, dom, nil)

	m := &Mailbox{DomainID: dom.ID, TenantID: 1, LocalPart: "q", Email: "q@quota-test.example.com", PasswordHash: "h", Status: MailboxActive}
	repo.Create(ctx, m, nil)

	if err := repo.UpdateQuota(ctx, m.ID, 1024, 1, nil); err != nil {
		t.Fatalf("update quota: %v", err)
	}

	got, _ := repo.GetByID(ctx, m.ID, nil)
	if got.UsedBytes != 1024 {
		t.Fatalf("expected 1024 used bytes, got %d", got.UsedBytes)
	}
	if got.MsgCount != 1 {
		t.Fatalf("expected 1 message, got %d", got.MsgCount)
	}
}

func TestAliasRepositoryCreateAndResolve(t *testing.T) {
	db := testDB(t)
	repo := NewAliasSQLRepo(db)
	domRepo := NewDomainSQLRepo(db)
	ctx := context.Background()

	dom := &Domain{Name: "alias-test.example.com", TenantID: 1, Status: DomainActive}
	domRepo.Create(ctx, dom, nil)

	a := &Alias{
		DomainID: dom.ID,
		TenantID: 1,
		FromAddr: "sales@alias-test.example.com",
		ToAddr:   "alice@alias-test.example.com,bob@alias-test.example.com",
		Active:   true,
	}
	if err := repo.Create(ctx, a, nil); err != nil {
		t.Fatalf("create alias: %v", err)
	}

	got, err := repo.GetByFromAddr(ctx, "sales@alias-test.example.com", nil)
	if err != nil {
		t.Fatalf("get alias: %v", err)
	}
	if got == nil {
		t.Fatal("expected alias")
	}
	if got.ToAddr != "alice@alias-test.example.com,bob@alias-test.example.com" {
		t.Fatalf("unexpected destinations: %q", got.ToAddr)
	}
}

func TestAliasRepositoryListByDomain(t *testing.T) {
	db := testDB(t)
	repo := NewAliasSQLRepo(db)
	domRepo := NewDomainSQLRepo(db)
	ctx := context.Background()

	dom := &Domain{Name: "alias-list.example.com", TenantID: 1, Status: DomainActive}
	domRepo.Create(ctx, dom, nil)

	for i := 0; i < 3; i++ {
		repo.Create(ctx, &Alias{
			DomainID: dom.ID, TenantID: 1,
			FromAddr: fmt.Sprintf("a%d@alias-list.example.com", i),
			ToAddr:   fmt.Sprintf("t%d@alias-list.example.com", i),
			Active:   true,
		}, nil)
	}

	aliases, err := repo.ListByDomain(ctx, dom.ID, nil)
	if err != nil {
		t.Fatalf("list by domain: %v", err)
	}
	if len(aliases) != 3 {
		t.Fatalf("expected 3 aliases, got %d", len(aliases))
	}
}

func TestAuthServiceHashAndVerify(t *testing.T) {
	cfg := DefaultAuthConfig()
	s := NewAuthService(nil, nil, nil, cfg)

	hash, err := s.HashPassword("MySecureP@ss123!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}

	if !s.VerifyPassword("MySecureP@ss123!", hash) {
		t.Fatal("expected valid password to verify")
	}
	if s.VerifyPassword("wrong", hash) {
		t.Fatal("expected wrong password to fail")
	}
	if s.VerifyPassword("MySecureP@ss123!", "invalid-format") {
		t.Fatal("expected invalid hash format to fail")
	}
}

func TestAuthServiceResolveAddress(t *testing.T) {
	db := testDB(t)
	domRepo := NewDomainSQLRepo(db)
	mboxRepo := NewMailboxSQLRepo(db)
	aliasRepo := NewAliasSQLRepo(db)
	ctx := context.Background()
	cfg := DefaultAuthConfig()
	svc := NewAuthService(mboxRepo, domRepo, aliasRepo, cfg)

	dom := &Domain{Name: "resolve.example.com", TenantID: 1, Status: DomainActive}
	domRepo.Create(ctx, dom, nil)

	mboxRepo.Create(ctx, &Mailbox{
		DomainID: dom.ID, TenantID: 1, LocalPart: "alice",
		Email: "alice@resolve.example.com", PasswordHash: "h", Status: MailboxActive,
	}, nil)

	targets, err := svc.ResolveAddress(ctx, "alice@resolve.example.com")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(targets) != 1 || targets[0] != "alice@resolve.example.com" {
		t.Fatalf("expected direct delivery, got %v", targets)
	}
}

func TestAuthServiceResolveAlias(t *testing.T) {
	db := testDB(t)
	domRepo := NewDomainSQLRepo(db)
	mboxRepo := NewMailboxSQLRepo(db)
	aliasRepo := NewAliasSQLRepo(db)
	ctx := context.Background()
	cfg := DefaultAuthConfig()
	svc := NewAuthService(mboxRepo, domRepo, aliasRepo, cfg)

	dom := &Domain{Name: "alias-resolve.example.com", TenantID: 1, Status: DomainActive}
	domRepo.Create(ctx, dom, nil)

	aliasRepo.Create(ctx, &Alias{
		DomainID: dom.ID, TenantID: 1,
		FromAddr: "team@alias-resolve.example.com",
		ToAddr:   "alice@alias-resolve.example.com,bob@alias-resolve.example.com",
		Active:   true,
	}, nil)

	targets, err := svc.ResolveAddress(ctx, "team@alias-resolve.example.com")
	if err != nil {
		t.Fatalf("resolve alias: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets via alias, got %d", len(targets))
	}
}

func TestEngineProvisionDomain(t *testing.T) {
	db := testDB(t)
	cfg := EngineConfig{DB: db, AuthCfg: DefaultAuthConfig()}
	eng := NewEngine(cfg)
	ctx := context.Background()

	domain, mailbox, err := eng.ProvisionDomain(ctx, "provision-test.example.com", "enterprise", "admin@provision-test.example.com", "AdminP@ss1", "Admin User", 1)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if domain == nil {
		t.Fatal("expected domain")
	}
	if mailbox == nil {
		t.Fatal("expected mailbox")
	}
	if mailbox.Email != "admin@provision-test.example.com" {
		t.Fatalf("expected admin email, got %q", mailbox.Email)
	}
	if !mailbox.IsAdmin {
		t.Fatal("expected admin mailbox")
	}
	if domain.Name != "provision-test.example.com" {
		t.Fatalf("expected domain name, got %q", domain.Name)
	}

	// Verify domain is in DB.
	gotDomain, _ := eng.Domains.GetByName(ctx, "provision-test.example.com", nil)
	if gotDomain == nil {
		t.Fatal("domain should exist")
	}

	// Verify mailbox is in DB.
	gotMbox, _ := eng.Mailboxes.GetByEmail(ctx, "admin@provision-test.example.com", nil)
	if gotMbox == nil {
		t.Fatal("mailbox should exist")
	}

	// Verify password works.
	authMbox, err := eng.Auth.AuthenticateMailbox(ctx, "admin@provision-test.example.com", "AdminP@ss1")
	if err != nil {
		t.Fatalf("authenticate after provision: %v", err)
	}
	if authMbox == nil {
		t.Fatal("expected authenticated mailbox")
	}
}

func TestEngineProvisionDuplicateDomain(t *testing.T) {
	db := testDB(t)
	cfg := EngineConfig{DB: db, AuthCfg: DefaultAuthConfig()}
	eng := NewEngine(cfg)
	ctx := context.Background()

	eng.ProvisionDomain(ctx, "dup.example.com", "smb", "admin@dup.example.com", "pass", "Admin", 1)
	_, _, err := eng.ProvisionDomain(ctx, "dup.example.com", "smb", "admin2@dup.example.com", "pass2", "Admin2", 1)
	if err == nil {
		t.Fatal("expected error on duplicate domain provision")
	}
}

func TestDomainRepositoryCountByTenant(t *testing.T) {
	db := testDB(t)
	repo := NewDomainSQLRepo(db)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		repo.Create(ctx, &Domain{Name: fmt.Sprintf("cnt-%d.example.com", i), TenantID: 10, Status: DomainActive}, nil)
	}
	count, err := repo.CountByTenant(ctx, 10, nil)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3, got %d", count)
	}
}
