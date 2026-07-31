package domain

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/coremail/dkim"
	_ "modernc.org/sqlite"
)

func newDomainTestDB(t *testing.T) *sql.DB {
	t.Helper()
	// Shared-cache in-memory DB so concurrent goroutines (race tests) share
	// one schema. Each test uses a unique database name to prevent
	// cross-test pollution.
	name := "domain_test_" + strings.ReplaceAll(t.Name(), "/", "_")
	db, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared&_busy_timeout=5000&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE coremail_domains (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		status TEXT NOT NULL,
		plan TEXT,
		description TEXT,
		max_mailboxes INTEGER,
		max_aliases INTEGER,
		max_quota_mb INTEGER,
		dkim_enabled INTEGER DEFAULT 0,
		dkim_selector TEXT,
		dmarc_enabled INTEGER DEFAULT 0,
		created_at DATETIME,
		updated_at DATETIME,
		deleted_at DATETIME
	);
	CREATE TABLE coremail_mailboxes (id INTEGER PRIMARY KEY, domain_id INTEGER, tenant_id INTEGER, deleted_at DATETIME);
	CREATE TABLE coremail_aliases (id INTEGER PRIMARY KEY, domain_id INTEGER, tenant_id INTEGER, deleted_at DATETIME);
	CREATE TABLE coremail_admin_groups (id INTEGER PRIMARY KEY, tenant_id INTEGER, name TEXT, deleted_at DATETIME);
	CREATE TABLE coremail_admin_group_members (group_id INTEGER, user_id INTEGER);
	CREATE TABLE coremail_dkim_config (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		domain TEXT UNIQUE NOT NULL,
		selector TEXT NOT NULL DEFAULT 'default',
		private_key_pem TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);
	CREATE TABLE orvix_audit (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		actor TEXT NOT NULL DEFAULT '',
		actor_id INTEGER NOT NULL DEFAULT 0,
		actor_role TEXT NOT NULL DEFAULT '',
		tenant_id INTEGER NOT NULL DEFAULT 0,
		action TEXT NOT NULL DEFAULT '',
		target TEXT NOT NULL DEFAULT '',
		target_id INTEGER NOT NULL DEFAULT 0,
		result TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL DEFAULT '',
		before TEXT NOT NULL DEFAULT '',
		after TEXT NOT NULL DEFAULT '',
		request_id TEXT NOT NULL DEFAULT '',
		ip TEXT NOT NULL DEFAULT '',
		user_agent TEXT NOT NULL DEFAULT '',
		timestamp DATETIME NOT NULL DEFAULT (datetime('now'))
	);`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestDomainServiceTenantScopedLifecycle(t *testing.T) {
	db := newDomainTestDB(t)
	svc := NewService(NewDomainAdminRepo(db), dkim.NewSQLRepo(db), nil, nil)
	ctx := context.Background()

	created, err := svc.CreateDomain(ctx, CreateDomainRequest{Name: "example.test"}, 5)
	if err != nil {
		t.Fatalf("create domain: %v", err)
	}
	if created.TenantID != 5 || created.Status != "active" {
		t.Fatalf("unexpected created domain: %#v", created)
	}
	if _, err := svc.GetDomain(ctx, created.ID, 6); err != ErrDomainNotFound {
		t.Fatalf("cross-tenant domain read must fail closed, got %v", err)
	}
	if err := svc.SetDomainStatus(ctx, created.ID, 5, "suspended", "billing"); err != nil {
		t.Fatalf("set status: %v", err)
	}
	got, err := svc.GetDomain(ctx, created.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "suspended" {
		t.Fatalf("status not persisted: %#v", got)
	}
}

func TestDomainMutationRollsBackWhenAuditWriteFails(t *testing.T) {
	// Build a schema WITHOUT orvix_audit so the transactional audit write
	// fails, proving the domain mutation rolls back with it.
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE coremail_domains (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		status TEXT NOT NULL,
		plan TEXT,
		description TEXT,
		max_mailboxes INTEGER,
		max_aliases INTEGER,
		max_quota_mb INTEGER,
		dkim_enabled INTEGER DEFAULT 0,
		dkim_selector TEXT,
		dmarc_enabled INTEGER DEFAULT 0,
		created_at DATETIME,
		updated_at DATETIME,
		deleted_at DATETIME
	);`); err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	svc := NewService(NewDomainAdminRepo(db), dkim.NewSQLRepo(db), audit.NewExtendedStore(db), nil)
	if _, err := svc.CreateDomain(context.Background(), CreateDomainRequest{Name: "audit-failure.test"}, 5); err == nil {
		t.Fatal("audit failure must fail the domain mutation")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_domains WHERE name = ?`, "audit-failure.test").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("domain mutation committed without its audit record")
	}
}

func newDomainWithDKIMTestDB(t *testing.T) (*sql.DB, *Service) {
	t.Helper()
	db := newDomainTestDB(t)
	svc := NewService(NewDomainAdminRepo(db), dkim.NewSQLRepo(db), audit.NewExtendedStore(db), nil)
	return db, svc
}

func TestGenerateDKIMSuccessAndAtomicState(t *testing.T) {
	db, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, err := svc.CreateDomain(ctx, CreateDomainRequest{Name: "dkim.example.test"}, 5)
	if err != nil {
		t.Fatalf("create domain: %v", err)
	}

	res, err := svc.GenerateDKIM(ctx, d.ID, 5, "mail")
	if err != nil {
		t.Fatalf("generate dkim: %v", err)
	}
	if res.Selector != "mail" {
		t.Fatalf("selector = %q, want mail", res.Selector)
	}
	if !strings.Contains(res.PublicDNSTxt, "v=DKIM1; k=rsa; p=") {
		t.Fatalf("public dns txt malformed: %q", res.PublicDNSTxt)
	}
	if res.DNSRecordName != "mail._domainkey.dkim.example.test" {
		t.Fatalf("dns record name = %q", res.DNSRecordName)
	}

	// Domain DKIM state persisted.
	got, _ := svc.GetDomain(ctx, d.ID, 5)
	if !got.DKIMEnabled || got.DKIMSelector != "mail" {
		t.Fatalf("domain dkim state not persisted: %#v", got)
	}

	// Private key is never returned by the service.
	if strings.Contains(res.PublicDNSTxt, "BEGIN") {
		t.Fatal("public dns txt must not contain private key material")
	}

	// Audit record written.
	entries, _, err := audit.NewExtendedStore(db).Search(ctx, &audit.ExtendedQuery{Action: "domain.dkim.generate", TenantID: uintPtr(5)})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("domain.dkim.generate audit entries = %d, want 1", len(entries))
	}
}

func TestGenerateDKIMDuplicateReturnsTypedError(t *testing.T) {
	_, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, _ := svc.CreateDomain(ctx, CreateDomainRequest{Name: "dupdkim.example.test"}, 5)
	if _, err := svc.GenerateDKIM(ctx, d.ID, 5, "mail"); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if _, err := svc.GenerateDKIM(ctx, d.ID, 5, "mail"); err != ErrDKIMAlreadyConfigured {
		t.Fatalf("second generate err = %v, want ErrDKIMAlreadyConfigured", err)
	}
}

func TestGenerateDKIMCrossTenantFailsClosed(t *testing.T) {
	_, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, _ := svc.CreateDomain(ctx, CreateDomainRequest{Name: "crossdkim.example.test"}, 5)
	if _, err := svc.GenerateDKIM(ctx, d.ID, 99, "mail"); err != ErrDomainNotFound {
		t.Fatalf("cross-tenant generate err = %v, want ErrDomainNotFound", err)
	}
	var count int
	_ = svc.repo.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_dkim_config").Scan(&count)
	if count != 0 {
		t.Fatalf("cross-tenant generate must not write dkim config, got %d rows", count)
	}
}

func TestGenerateDKIMConcurrentRaceIsDeterministic(t *testing.T) {
	db := newDomainTestDB(t)
	// Serialize transactions on one connection (the repo's established
	// SQLite concurrency pattern). With _txlock=immediate in production,
	// concurrent generate calls serialize at the storage layer the same way:
	// exactly one winner, every loser gets the typed conflict error.
	db.SetMaxOpenConns(1)
	svc := NewService(NewDomainAdminRepo(db), dkim.NewSQLRepo(db), audit.NewExtendedStore(db), nil)
	ctx := context.Background()
	d, _ := svc.CreateDomain(ctx, CreateDomainRequest{Name: "race.example.test"}, 5)

	var wg sync.WaitGroup
	results := make([]error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := svc.GenerateDKIM(ctx, d.ID, 5, "mail")
			results[idx] = err
		}(i)
	}
	wg.Wait()

	var okCount, dupCount int
	for _, r := range results {
		if r == nil {
			okCount++
		} else if r == ErrDKIMAlreadyConfigured {
			dupCount++
		} else {
			t.Fatalf("unexpected generate error: %v", r)
		}
	}
	if okCount != 1 {
		t.Fatalf("exactly one generate must win, got ok=%d dup=%d", okCount, dupCount)
	}
	var cfgCount int
	_ = svc.repo.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_dkim_config").Scan(&cfgCount)
	if cfgCount != 1 {
		t.Fatalf("exactly one dkim config row must exist, got %d", cfgCount)
	}
}

func TestRotateDKIMReplacesKeyAndAudits(t *testing.T) {
	db, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, _ := svc.CreateDomain(ctx, CreateDomainRequest{Name: "rotate.example.test"}, 5)
	first, err := svc.GenerateDKIM(ctx, d.ID, 5, "mail")
	if err != nil {
		t.Fatal(err)
	}
	rot, err := svc.RotateDKIM(ctx, d.ID, 5, "mail")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if rot.PublicDNSTxt == first.PublicDNSTxt {
		t.Fatal("rotate must produce a different key")
	}
	entries, _, _ := audit.NewExtendedStore(db).Search(ctx, &audit.ExtendedQuery{Action: "domain.dkim.rotate", TenantID: uintPtr(5)})
	if len(entries) != 1 {
		t.Fatalf("domain.dkim.rotate audit entries = %d, want 1", len(entries))
	}
}

func TestRotateDKIMWithoutConfigReturnsTypedError(t *testing.T) {
	_, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, _ := svc.CreateDomain(ctx, CreateDomainRequest{Name: "norotate.example.test"}, 5)
	if _, err := svc.RotateDKIM(ctx, d.ID, 5, "mail"); err != ErrDKIMNotConfigured {
		t.Fatalf("rotate without config err = %v, want ErrDKIMNotConfigured", err)
	}
}

func TestGenerateDKIMRollsBackWhenAuditFails(t *testing.T) {
	db := newDomainTestDB(t)
	svc := NewService(NewDomainAdminRepo(db), dkim.NewSQLRepo(db), audit.NewExtendedStore(db), nil)
	ctx := context.Background()
	d, err := svc.CreateDomain(ctx, CreateDomainRequest{Name: "rollback.example.test"}, 5)
	if err != nil {
		t.Fatalf("create domain: %v", err)
	}
	// Break the audit write path after the domain exists so the DKIM
	// mutation's transactional audit fails and everything rolls back.
	if _, err := db.Exec(`DROP TABLE orvix_audit`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GenerateDKIM(ctx, d.ID, 5, "mail"); err == nil {
		t.Fatal("generate must fail when audit write fails")
	}
	var cfgCount, dkimState int
	_ = svc.repo.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_dkim_config").Scan(&cfgCount)
	if cfgCount != 0 {
		t.Fatalf("dkim config must roll back, got %d rows", cfgCount)
	}
	_ = svc.repo.db.QueryRowContext(ctx, "SELECT dkim_enabled FROM coremail_domains WHERE id=?", d.ID).Scan(&dkimState)
	if dkimState != 0 {
		t.Fatalf("domain dkim state must roll back, got %d", dkimState)
	}
}

func TestDeleteDomainCleansUpDKIMInSameTransaction(t *testing.T) {
	db, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, _ := svc.CreateDomain(ctx, CreateDomainRequest{Name: "cleanupdkim.example.test"}, 5)
	if _, err := svc.GenerateDKIM(ctx, d.ID, 5, "mail"); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteDomain(ctx, d.ID, 5); err != nil {
		t.Fatalf("delete domain: %v", err)
	}
	var cfgCount int
	_ = svc.repo.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_dkim_config").Scan(&cfgCount)
	if cfgCount != 0 {
		t.Fatalf("dkim config must be removed on domain delete, got %d rows", cfgCount)
	}
	// Repeated delete is safe (not-found).
	if err := svc.DeleteDomain(ctx, d.ID, 5); err != ErrDomainNotFound {
		t.Fatalf("repeated delete err = %v, want ErrDomainNotFound", err)
	}
	// Audit event exists.
	entries, _, _ := audit.NewExtendedStore(db).Search(ctx, &audit.ExtendedQuery{Action: "domain.delete", TenantID: uintPtr(5)})
	if len(entries) != 1 {
		t.Fatalf("domain.delete audit entries = %d, want 1", len(entries))
	}
}

func TestDeleteDomainBlockedByMailboxes(t *testing.T) {
	db, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, _ := svc.CreateDomain(ctx, CreateDomainRequest{Name: "hasmb.example.test"}, 5)
	if _, err := db.Exec(`INSERT INTO coremail_mailboxes (domain_id, tenant_id, deleted_at) VALUES (?, ?, NULL)`, d.ID, 5); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteDomain(ctx, d.ID, 5); err != ErrDomainHasMailboxes {
		t.Fatalf("delete with mailboxes err = %v, want ErrDomainHasMailboxes", err)
	}
}

func TestValidateDomainNameCanonical(t *testing.T) {
	valid := []string{"example.com", "EXAMPLE.COM", "a.b.example.co.uk", "my-domain.example.com", "example.com."}
	for _, v := range valid {
		if _, err := ValidateDomainName(v); err != nil {
			t.Errorf("ValidateDomainName(%q) should be valid, got %v", v, err)
		}
	}
	invalid := []string{
		"", " ", "example", "https://example.com", "http://example.com/path",
		"example.com/path", "user@example.com", "exa mple.com", "*.example.com",
		"example.com:8080", "example.com#frag", "example.com?q=1", "-bad.com",
		"bad-.com", strings.Repeat("a", 64) + ".com", "exa_mple.com", "m\u00fcnchen.de",
	}
	for _, v := range invalid {
		if _, err := ValidateDomainName(v); err == nil {
			t.Errorf("ValidateDomainName(%q) should be invalid", v)
		}
	}
}

func uintPtr(v uint) *uint { return &v }

func TestParseDomainStatusSupportedSet(t *testing.T) {
	// Supported, WRITABLE values, including case/whitespace normalization.
	for _, in := range []string{"active", "disabled", "suspended", " Active ", "SUSPENDED", "  disabled  "} {
		if _, ok := ParseDomainStatus(in); !ok {
			t.Errorf("ParseDomainStatus(%q) should be supported after normalization", in)
		}
	}
	// Unsupported / unknown legacy values must fail closed. "locked" is
	// deliberately included here (not in the supported list above): it has
	// no evidenced production writer, so SetDomainStatus must not be able
	// to introduce it as a new state. See DomainStatusLocked's doc comment.
	for _, in := range []string{"", " ", "pending", "verified", "deleted", "unknown", "garbage", "frozen", "abuse-blocked", "locked", "LOCKED"} {
		if _, ok := ParseDomainStatus(in); ok {
			t.Errorf("ParseDomainStatus(%q) should be rejected", in)
		}
	}
}

func TestSetDomainStatusNormalizesAndRejects(t *testing.T) {
	db := newDomainTestDB(t)
	svc := NewService(NewDomainAdminRepo(db), dkim.NewSQLRepo(db), nil, nil)
	ctx := context.Background()
	d, err := svc.CreateDomain(ctx, CreateDomainRequest{Name: "status.test"}, 5)
	if err != nil {
		t.Fatal(err)
	}

	// Reject arbitrary free-text status values.
	if err := svc.SetDomainStatus(ctx, d.ID, 5, "pending", ""); err != ErrInvalidDomainStatus {
		t.Fatalf("SetDomainStatus(pending) err = %v, want ErrInvalidDomainStatus", err)
	}
	if err := svc.SetDomainStatus(ctx, d.ID, 5, "garbage", ""); err != ErrInvalidDomainStatus {
		t.Fatalf("SetDomainStatus(garbage) err = %v, want ErrInvalidDomainStatus", err)
	}

	// Normalize (trim + lowercase) and persist canonical form.
	if err := svc.SetDomainStatus(ctx, d.ID, 5, "  SUSPENDED ", "billing"); err != nil {
		t.Fatalf("SetDomainStatus normalized: %v", err)
	}
	got, err := svc.GetDomain(ctx, d.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "suspended" {
		t.Fatalf("status after normalization = %q, want suspended", got.Status)
	}

	// Cross-tenant cannot change status.
	if err := svc.SetDomainStatus(ctx, d.ID, 99, "active", ""); err != nil {
		t.Fatalf("cross-tenant set status err = %v (must fail closed, not leak)", err)
	}
}

func TestPlatformDKIMCreateRotateAndConflicts(t *testing.T) {
	db, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	if _, err := svc.CreateDomain(ctx, CreateDomainRequest{Name: "platform.example.test"}, 5); err != nil {
		t.Fatal(err)
	}

	// Missing domain -> not found.
	if _, err := svc.PlatformDKIM(ctx, "missing.example.test", "orvix", ""); err != ErrDomainNotFound {
		t.Fatalf("PlatformDKIM missing domain err = %v, want ErrDomainNotFound", err)
	}

	// Create.
	res, err := svc.PlatformDKIM(ctx, "platform.example.test", "orvix", "")
	if err != nil {
		t.Fatalf("PlatformDKIM create: %v", err)
	}
	if res.Selector != "orvix" || res.DNSRecordName != "orvix._domainkey.platform.example.test" {
		t.Fatalf("unexpected dkim result: %#v", res)
	}

	// Already configured without confirmation -> typed conflict.
	if _, err := svc.PlatformDKIM(ctx, "platform.example.test", "orvix", ""); err != ErrDKIMAlreadyConfigured {
		t.Fatalf("PlatformDKIM duplicate err = %v, want ErrDKIMAlreadyConfigured", err)
	}

	// Rotate with confirmation produces a new key.
	first, _ := svc.PlatformDKIM(ctx, "platform.example.test", "mail", "rotate-dkim-key")
	if first.Selector != "mail" {
		t.Fatalf("rotate selector = %q, want mail", first.Selector)
	}

	// Exactly two canonical audit events (generate + rotate).
	entries, _, _ := audit.NewExtendedStore(db).Search(ctx, &audit.ExtendedQuery{TenantID: uintPtr(5)})
	var generate, rotate int
	for _, e := range entries {
		switch e.Action {
		case "domain.dkim.generate":
			generate++
		case "domain.dkim.rotate":
			rotate++
		}
	}
	if generate != 1 || rotate != 1 {
		t.Fatalf("audit counts generate=%d rotate=%d, want 1/1", generate, rotate)
	}
}

// TestPlatformDKIMConcurrentCreateIsDeterministic is the PlatformDKIM
// counterpart to TestGenerateDKIMConcurrentRaceIsDeterministic. PostAdminDNSDKIM
// (the platform-admin DNS route) now delegates entirely to PlatformDKIM, so
// its concurrency safety must be proven independently of the tenant-scoped
// GenerateDKIM path it used to duplicate.
func TestPlatformDKIMConcurrentCreateIsDeterministic(t *testing.T) {
	db := newDomainTestDB(t)
	db.SetMaxOpenConns(1)
	svc := NewService(NewDomainAdminRepo(db), dkim.NewSQLRepo(db), audit.NewExtendedStore(db), nil)
	ctx := context.Background()
	if _, err := svc.CreateDomain(ctx, CreateDomainRequest{Name: "platformrace.example.test"}, 5); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	results := make([]error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := svc.PlatformDKIM(ctx, "platformrace.example.test", "orvix", "")
			results[idx] = err
		}(i)
	}
	wg.Wait()

	var okCount, dupCount int
	for _, r := range results {
		if r == nil {
			okCount++
		} else if r == ErrDKIMAlreadyConfigured {
			dupCount++
		} else {
			t.Fatalf("unexpected PlatformDKIM error: %v", r)
		}
	}
	if okCount != 1 {
		t.Fatalf("exactly one PlatformDKIM create must win, got ok=%d dup=%d", okCount, dupCount)
	}
	if dupCount != 7 {
		t.Fatalf("the other 7 calls must all get the typed conflict, got dup=%d", dupCount)
	}
	var cfgCount int
	_ = svc.repo.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_dkim_config").Scan(&cfgCount)
	if cfgCount != 1 {
		t.Fatalf("exactly one dkim config row must exist, got %d", cfgCount)
	}
}

func TestPlatformDKIMRollsBackWhenAuditFails(t *testing.T) {
	db, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	if _, err := svc.CreateDomain(ctx, CreateDomainRequest{Name: "prollback.example.test"}, 5); err != nil {
		t.Fatal(err)
	}
	// Break the audit write path so the transactional audit fails and the
	// DKIM row + domain state roll back.
	if _, err := db.Exec(`DROP TABLE orvix_audit`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PlatformDKIM(ctx, "prollback.example.test", "orvix", ""); err == nil {
		t.Fatal("PlatformDKIM must fail when audit write fails")
	}
	var cfgCount int
	_ = svc.repo.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_dkim_config").Scan(&cfgCount)
	if cfgCount != 0 {
		t.Fatalf("dkim config must roll back on audit failure, got %d rows", cfgCount)
	}
}
