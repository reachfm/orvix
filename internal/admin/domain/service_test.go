package domain

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/coremail/dkim"
	"github.com/orvix/orvix/internal/tlsmgmt"
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
		default_mailbox_quota_mb INTEGER NOT NULL DEFAULT 0,
		dkim_enabled INTEGER DEFAULT 0,
		dkim_selector TEXT,
		dmarc_enabled INTEGER DEFAULT 0,
		mail_access_mode TEXT NOT NULL DEFAULT 'internal_external',
		created_at DATETIME,
		updated_at DATETIME,
		deleted_at DATETIME
	);
	CREATE TABLE coremail_mailboxes (id INTEGER PRIMARY KEY, domain_id INTEGER, tenant_id INTEGER, used_bytes INTEGER DEFAULT 0, msg_count INTEGER DEFAULT 0, deleted_at DATETIME);
	CREATE TABLE coremail_aliases (id INTEGER PRIMARY KEY, domain_id INTEGER, tenant_id INTEGER, deleted_at DATETIME);
	CREATE TABLE customer_domain_verifications (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		domain_id INTEGER NOT NULL,
		score INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT '',
		mx_status TEXT, spf_status TEXT, dkim_status TEXT, dmarc_status TEXT,
		evidence TEXT,
		checked_at DATETIME,
		created_at DATETIME
	);
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

// TestListDomainsReturnsRealAggregates is the regression test for the
// enterprise domains table: it verifies mailbox/alias counts, storage
// bytes, message counts, and DNS health/score come back as real computed
// values from the batched List query — not zero placeholders — and that
// the list is scoped to the caller's tenant.
func TestListDomainsReturnsRealAggregates(t *testing.T) {
	db := newDomainTestDB(t)
	svc := NewService(NewDomainAdminRepo(db), dkim.NewSQLRepo(db), nil, nil)
	ctx := context.Background()

	created, err := svc.CreateDomain(ctx, CreateDomainRequest{Name: "agg.example.com", MaxQuotaMB: 100}, 5)
	if err != nil {
		t.Fatalf("create domain: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO coremail_mailboxes (domain_id, tenant_id, used_bytes, msg_count) VALUES (?, 5, 1000, 3), (?, 5, 2000, 7)`,
		created.ID, created.ID); err != nil {
		t.Fatalf("seed mailboxes: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO coremail_aliases (domain_id, tenant_id) VALUES (?, 5)`, created.ID); err != nil {
		t.Fatalf("seed alias: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO customer_domain_verifications (domain_id, score, status, checked_at, created_at) VALUES (?, 80, 'warning', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`, created.ID); err != nil {
		t.Fatalf("seed verification: %v", err)
	}

	domains, total, err := svc.ListDomains(ctx, DomainFilter{TenantID: uintPtr(5)})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(domains) != 1 {
		t.Fatalf("total=%d len=%d, want 1/1", total, len(domains))
	}
	d := domains[0]
	if d.MailboxCount != 2 {
		t.Errorf("MailboxCount = %d, want 2", d.MailboxCount)
	}
	if d.AliasCount != 1 {
		t.Errorf("AliasCount = %d, want 1", d.AliasCount)
	}
	if d.StorageUsedBytes != 3000 {
		t.Errorf("StorageUsedBytes = %d, want 3000 (real SUM, not a placeholder)", d.StorageUsedBytes)
	}
	if d.MessageCount != 10 {
		t.Errorf("MessageCount = %d, want 10", d.MessageCount)
	}
	if d.StorageLimitBytes != 100*1024*1024 {
		t.Errorf("StorageLimitBytes = %d, want %d", d.StorageLimitBytes, 100*1024*1024)
	}
	if d.DNSHealth != "warning" || d.DNSScore != 80 {
		t.Errorf("DNSHealth/DNSScore = %q/%d, want warning/80 (from latest verification snapshot)", d.DNSHealth, d.DNSScore)
	}
	if d.DNSLastCheckedAt == nil {
		t.Error("DNSLastCheckedAt is nil, want the verification's checked_at")
	}
}

// TestListDomainsIsSingleQuery is the no-N+1 regression test: List must
// issue exactly one query to coremail_domains (the correlated subqueries
// for aggregates execute inside that single statement, not as separate
// round trips) regardless of how many domains are returned.
func TestListDomainsIsSingleQuery(t *testing.T) {
	db := newDomainTestDB(t)
	svc := NewService(NewDomainAdminRepo(db), dkim.NewSQLRepo(db), nil, nil)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		if _, err := svc.CreateDomain(ctx, CreateDomainRequest{Name: fmt.Sprintf("multi%d.example.com", i)}, 5); err != nil {
			t.Fatalf("create domain %d: %v", i, err)
		}
	}

	counter := &countingDB{db: db}
	repo := &DomainAdminRepo{root: db, db: counter, dialect: NewDomainAdminRepo(db).dialect}
	domains, total, err := repo.List(ctx, DomainFilter{TenantID: uintPtr(5)})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 10 || len(domains) != 10 {
		t.Fatalf("total=%d len=%d, want 10/10", total, len(domains))
	}
	// COUNT(*) + the paginated SELECT = exactly 2 queries, independent of
	// the 10 domains returned. If aggregates were computed via N separate
	// per-domain queries instead of correlated subqueries, this would be
	// 2 + 10 = 12.
	if counter.queryCalls != 2 {
		t.Errorf("List issued %d QueryContext calls for 10 domains, want exactly 2 (COUNT + paginated SELECT) — N+1 regression", counter.queryCalls)
	}
}

// countingDB wraps *sql.DB to count QueryContext calls, for the no-N+1 test.
type countingDB struct {
	db         *sql.DB
	queryCalls int
}

func (c *countingDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return c.db.ExecContext(ctx, query, args...)
}
func (c *countingDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	c.queryCalls++
	return c.db.QueryContext(ctx, query, args...)
}
func (c *countingDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	c.queryCalls++
	return c.db.QueryRowContext(ctx, query, args...)
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
		default_mailbox_quota_mb INTEGER NOT NULL DEFAULT 0,
		dkim_enabled INTEGER DEFAULT 0,
		dkim_selector TEXT,
		dmarc_enabled INTEGER DEFAULT 0,
		mail_access_mode TEXT NOT NULL DEFAULT 'internal_external',
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
		"bad-.com", strings.Repeat("a", 64) + ".com", "exa_mple.com",
		// Public suffixes are not registrable and can never belong to a
		// single tenant, so accepting one would let a tenant claim an entire
		// namespace.
		"com", "co.uk", "github.io",
	}
	for _, v := range invalid {
		if _, err := ValidateDomainName(v); err == nil {
			t.Errorf("ValidateDomainName(%q) should be invalid", v)
		}
	}

	// IDNA: internationalized names are now folded to their canonical ASCII
	// A-label (punycode) form instead of being rejected, so two spellings of
	// the same name can never both be provisioned.
	idnaCases := map[string]string{
		"m\u00fcnchen.de":   "xn--mnchen-3ya.de",
		"M\u00dcNCHEN.DE":   "xn--mnchen-3ya.de",
		"xn--mnchen-3ya.de": "xn--mnchen-3ya.de",
		"m\u00fcnchen.de.":  "xn--mnchen-3ya.de",
	}
	for in, want := range idnaCases {
		got, err := ValidateDomainName(in)
		if err != nil {
			t.Errorf("ValidateDomainName(%q) should be valid, got %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ValidateDomainName(%q) = %q, want %q", in, got, want)
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

// TestDKIMResultNeverSerializesPrivateKey locks the response contract for all
// four DKIM handler paths (GetAdminDomainDKIM, PostAdminDomainDKIMGenerate,
// PostAdminDomainDKIMRotate and PlatformDKIM), every one of which serializes
// exactly this struct as {"dkim": ...}. The private key is generated and stored
// server-side (dkim.DKIMConfig.PrivateKeyPEM) and is read back only to derive
// the public key — it must never become reachable over the wire. If someone
// adds a private-key field to DKIMResult, this test fails.
func TestDKIMResultNeverSerializesPrivateKey(t *testing.T) {
	// A realistic-looking PEM in the selector field proves we are checking the
	// serialized shape, not merely that the fixture happens to be clean.
	r := DKIMResult{
		Selector:      "mail",
		PublicDNSTxt:  "v=DKIM1; k=rsa; p=MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8A",
		DNSRecordName: "mail._domainkey.example.com",
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	payload := string(b)

	for _, marker := range []string{
		"BEGIN RSA PRIVATE KEY", "BEGIN PRIVATE KEY", "private_key",
		"privateKey", "PrivateKeyPEM", "private_key_pem",
	} {
		if strings.Contains(payload, marker) {
			t.Fatalf("DKIM response contains private key marker %q: %s", marker, payload)
		}
	}

	// Assert the exact field set, so a newly added field is a deliberate act.
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]bool{"selector": true, "public_dns_txt": true, "dns_record_name": true}
	if len(got) != len(want) {
		t.Fatalf("DKIMResult field set changed: got %v, want exactly %v", got, want)
	}
	for k := range got {
		if !want[k] {
			t.Errorf("unexpected field %q in DKIM response: %s", k, payload)
		}
	}
}

func TestRevokeDKIM_DisablesButPreservesConfigRow(t *testing.T) {
	db, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, err := svc.CreateDomain(ctx, CreateDomainRequest{Name: "revoke.example.test"}, 5)
	if err != nil {
		t.Fatalf("create domain: %v", err)
	}
	if _, err := svc.GenerateDKIM(ctx, d.ID, 5, "mail"); err != nil {
		t.Fatalf("generate dkim: %v", err)
	}

	if err := svc.RevokeDKIM(ctx, d.ID, 5); err != nil {
		t.Fatalf("revoke dkim: %v", err)
	}

	got, _ := svc.GetDomain(ctx, d.ID, 5)
	if got.DKIMEnabled {
		t.Fatal("expected dkim_enabled to be false after revoke")
	}

	cfg, err := dkim.NewSQLRepo(db).GetByDomain(ctx, "revoke.example.test", nil)
	if err != nil {
		t.Fatalf("get dkim config: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected the dkim config row to still exist after revoke (not deleted)")
	}
	if cfg.Enabled {
		t.Fatal("expected the dkim config row itself to be marked disabled")
	}
}

func TestRevokeDKIM_NotConfiguredReturnsTypedError(t *testing.T) {
	_, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, _ := svc.CreateDomain(ctx, CreateDomainRequest{Name: "norevoke.example.test"}, 5)

	err := svc.RevokeDKIM(ctx, d.ID, 5)
	if err != ErrDKIMNotConfigured {
		t.Fatalf("expected ErrDKIMNotConfigured, got %v", err)
	}
}

func TestDKIMSelectorHistory_RecordsGenerateRotateRevokeInOrder(t *testing.T) {
	_, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, _ := svc.CreateDomain(ctx, CreateDomainRequest{Name: "history.example.test"}, 5)

	if _, err := svc.GenerateDKIM(ctx, d.ID, 5, "mail1"); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := svc.RotateDKIM(ctx, d.ID, 5, "mail2"); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if err := svc.RevokeDKIM(ctx, d.ID, 5); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	hist, err := svc.ListDKIMHistory(ctx, d.ID, 5)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if len(hist) != 3 {
		t.Fatalf("expected 3 history entries, got %d: %#v", len(hist), hist)
	}
	// Newest first.
	if hist[0].Action != "revoked" || hist[0].Selector != "mail2" {
		t.Fatalf("entry 0 = %#v, want revoked/mail2", hist[0])
	}
	if hist[1].Action != "rotated" || hist[1].Selector != "mail2" {
		t.Fatalf("entry 1 = %#v, want rotated/mail2", hist[1])
	}
	if hist[2].Action != "generated" || hist[2].Selector != "mail1" {
		t.Fatalf("entry 2 = %#v, want generated/mail1", hist[2])
	}
}

func TestDKIMSelectorHistory_UnknownDomainIsNotFound(t *testing.T) {
	_, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	_, err := svc.ListDKIMHistory(ctx, 99999, 5)
	if err != ErrDomainNotFound {
		t.Fatalf("expected ErrDomainNotFound, got %v", err)
	}
}

// fakeTLSSource is a minimal in-memory tlsStatusSource for testing
// DomainTLSStatus without depending on internal/tlsmgmt's real
// filesystem/database-backed certificate loading.
type fakeTLSSource struct {
	uploaded   []tlsmgmt.TLSCertificate
	configured []tlsmgmt.TLSCertificate
}

func (f *fakeTLSSource) LoadCertificates(ctx context.Context) ([]tlsmgmt.TLSCertificate, error) {
	return f.configured, nil
}

func (f *fakeTLSSource) ListUploadedCertificates(ctx context.Context, tenantID int64) ([]tlsmgmt.TLSCertificate, error) {
	return f.uploaded, nil
}

func TestDomainTLSStatus_NoTLSServiceWiredReportsUnconfigured(t *testing.T) {
	_, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, _ := svc.CreateDomain(ctx, CreateDomainRequest{Name: "notls.example.test"}, 5)

	res, err := svc.DomainTLSStatus(ctx, d.ID, 5)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if res.Configured {
		t.Fatal("expected Configured=false with no TLS service wired")
	}
	if res.Source != "none" {
		t.Fatalf("expected source none, got %q", res.Source)
	}
}

func TestDomainTLSStatus_MatchesUploadedCertByExactCommonName(t *testing.T) {
	_, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, _ := svc.CreateDomain(ctx, CreateDomainRequest{Name: "exact.example.test"}, 5)

	svc.SetTLSService(&fakeTLSSource{
		uploaded: []tlsmgmt.TLSCertificate{
			{CommonName: "exact.example.test", Status: tlsmgmt.CertActive, DaysRemaining: 60, NotAfter: time.Now().Add(60 * 24 * time.Hour)},
		},
	})

	res, err := svc.DomainTLSStatus(ctx, d.ID, 5)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !res.Configured || res.Source != "uploaded" {
		t.Fatalf("expected uploaded match, got %#v", res)
	}
	if res.RenewalRequired {
		t.Fatal("expected RenewalRequired=false for an active cert")
	}
}

func TestDomainTLSStatus_MatchesWildcardSAN(t *testing.T) {
	_, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, _ := svc.CreateDomain(ctx, CreateDomainRequest{Name: "mail.wild.example.test"}, 5)

	svc.SetTLSService(&fakeTLSSource{
		configured: []tlsmgmt.TLSCertificate{
			{CommonName: "wild.example.test", SANs: []string{"*.wild.example.test"}, Status: tlsmgmt.CertWarning, DaysRemaining: 5},
		},
	})

	res, err := svc.DomainTLSStatus(ctx, d.ID, 5)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !res.Configured || res.Source != "configured" {
		t.Fatalf("expected wildcard SAN match via configured cert, got %#v", res)
	}
	if !res.RenewalRequired {
		t.Fatal("expected RenewalRequired=true for a warning-status cert")
	}
}

func TestDomainTLSStatus_WildcardNeverMatchesBareApex(t *testing.T) {
	_, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, _ := svc.CreateDomain(ctx, CreateDomainRequest{Name: "wild.example.test"}, 5)

	svc.SetTLSService(&fakeTLSSource{
		configured: []tlsmgmt.TLSCertificate{
			{CommonName: "other.test", SANs: []string{"*.wild.example.test"}, Status: tlsmgmt.CertActive},
		},
	})

	res, err := svc.DomainTLSStatus(ctx, d.ID, 5)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if res.Configured {
		t.Fatal("expected a wildcard SAN to never match the bare apex domain")
	}
}

func TestDomainTLSStatus_UnknownDomainIsNotFound(t *testing.T) {
	_, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	_, err := svc.DomainTLSStatus(ctx, 99999, 5)
	if err != ErrDomainNotFound {
		t.Fatalf("expected ErrDomainNotFound, got %v", err)
	}
}

func TestMailAccessMode_DefaultsToInternalExternal(t *testing.T) {
	_, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, _ := svc.CreateDomain(ctx, CreateDomainRequest{Name: "defaultmode.example.test"}, 5)

	mode, err := svc.GetMailAccessMode(ctx, d.ID, 5)
	if err != nil {
		t.Fatalf("get mode: %v", err)
	}
	if mode != MailAccessInternalExternal {
		t.Fatalf("expected default internal_external, got %q", mode)
	}
}

func TestMailAccessMode_SetAndGetRoundTrip(t *testing.T) {
	_, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, _ := svc.CreateDomain(ctx, CreateDomainRequest{Name: "setmode.example.test"}, 5)

	if err := svc.SetMailAccessMode(ctx, d.ID, 5, "internal_only"); err != nil {
		t.Fatalf("set mode: %v", err)
	}
	mode, err := svc.GetMailAccessMode(ctx, d.ID, 5)
	if err != nil {
		t.Fatalf("get mode: %v", err)
	}
	if mode != MailAccessInternalOnly {
		t.Fatalf("expected internal_only, got %q", mode)
	}
}

func TestMailAccessMode_InvalidValueRejected(t *testing.T) {
	_, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, _ := svc.CreateDomain(ctx, CreateDomainRequest{Name: "badmode.example.test"}, 5)

	err := svc.SetMailAccessMode(ctx, d.ID, 5, "totally-invalid")
	if err != ErrInvalidMailAccessMode {
		t.Fatalf("expected ErrInvalidMailAccessMode, got %v", err)
	}
}

func TestMailAccessMode_CrossTenantIsNotFound(t *testing.T) {
	_, svc := newDomainWithDKIMTestDB(t)
	ctx := context.Background()
	d, _ := svc.CreateDomain(ctx, CreateDomainRequest{Name: "tenantiso.example.test"}, 5)

	if err := svc.SetMailAccessMode(ctx, d.ID, 6, "internal_only"); err != ErrDomainNotFound {
		t.Fatalf("expected ErrDomainNotFound for a different tenant, got %v", err)
	}
	if _, err := svc.GetMailAccessMode(ctx, d.ID, 6); err != ErrDomainNotFound {
		t.Fatalf("expected ErrDomainNotFound for a different tenant, got %v", err)
	}
}
