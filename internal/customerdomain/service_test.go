package customerdomain

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/dkim"
	"github.com/orvix/orvix/internal/dnsops"
	_ "modernc.org/sqlite"
)

// newServiceTestEnv builds a Service backed by an on-disk SQLite database
// with the coremail_domains and customer_domain_verifications tables, plus
// a fake DNS resolver (all lookups NXDOMAIN → deterministic "fail" result,
// which still persists a snapshot). It returns the service and the raw DB
// for direct assertions.
func newServiceTestEnv(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cds.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// Mirror production's single-writer SQLite pool.
	db.SetMaxOpenConns(1)
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
		)`)
	if err != nil {
		t.Fatalf("create coremail_domains: %v", err)
	}

	domainRepo := coremail.NewDomainSQLRepo(db)
	verifRepo := NewVerificationRepo(db)
	if err := verifRepo.EnsureTable(context.Background()); err != nil {
		t.Fatalf("ensure verifications table: %v", err)
	}
	inspector := NewDNSInspector(dnsops.NewFakeResolver())
	svc := NewService(db, domainRepo, inspector, verifRepo)
	return svc, db
}

func seedDomain(t *testing.T, svc *Service, name string, tenantID uint) uint {
	t.Helper()
	d := &coremail.Domain{Name: name, TenantID: tenantID, Status: coremail.DomainActive}
	if err := svc.domains.Create(context.Background(), d, nil); err != nil {
		t.Fatalf("create domain %s: %v", name, err)
	}
	return d.ID
}

func countSnapshots(t *testing.T, db *sql.DB, domainID uint) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM customer_domain_verifications WHERE domain_id = ?`, domainID).Scan(&n); err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	return n
}

// TestServiceTenantIsolation verifies every domain-scoped read/write refuses
// a domain owned by another tenant, returning ErrDomainNotFound (not a
// forbidden/leak), across all four entry points.
func TestServiceTenantIsolation(t *testing.T) {
	svc, _ := newServiceTestEnv(t)
	ctx := context.Background()

	// Domain owned by tenant 1; caller claims tenant 2.
	domID := seedDomain(t, svc, "owned.example.com", 1)
	const attacker = uint(2)

	if _, err := svc.GetDomain(ctx, attacker, domID); err != ErrDomainNotFound {
		t.Errorf("GetDomain cross-tenant err = %v, want ErrDomainNotFound", err)
	}
	if _, err := svc.GetDNS(ctx, attacker, domID); err != ErrDomainNotFound {
		t.Errorf("GetDNS cross-tenant err = %v, want ErrDomainNotFound", err)
	}
	if err := svc.VerifyDomain(ctx, attacker, domID); err != ErrDomainNotFound {
		t.Errorf("VerifyDomain cross-tenant err = %v, want ErrDomainNotFound", err)
	}
	if _, err := svc.GetLatestSnapshot(ctx, attacker, domID); err != ErrDomainNotFound {
		t.Errorf("GetLatestSnapshot cross-tenant err = %v, want ErrDomainNotFound", err)
	}

	// The legitimate owner still succeeds.
	if _, err := svc.GetDomain(ctx, 1, domID); err != nil {
		t.Errorf("GetDomain owner err = %v, want nil", err)
	}

	// A cross-tenant verify must not have persisted anything.
	if n := countSnapshots(t, svc.db, domID); n != 0 {
		t.Errorf("snapshots after cross-tenant verify = %d, want 0", n)
	}
}

// TestServiceListScopedToTenant verifies ListDomains only returns the
// caller's own domains.
func TestServiceListScopedToTenant(t *testing.T) {
	svc, _ := newServiceTestEnv(t)
	ctx := context.Background()

	seedDomain(t, svc, "t1-a.example.com", 1)
	seedDomain(t, svc, "t1-b.example.com", 1)
	seedDomain(t, svc, "t2-a.example.com", 2)

	resp, err := svc.ListDomains(ctx, 1, DomainListRequest{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if resp.Total != 2 || len(resp.Domains) != 2 {
		t.Fatalf("tenant 1 sees total=%d len=%d, want 2/2", resp.Total, len(resp.Domains))
	}
	for _, d := range resp.Domains {
		if d.Name == "t2-a.example.com" {
			t.Fatalf("tenant 1 list leaked tenant 2 domain %q", d.Name)
		}
	}
}

// TestServiceCooldownEnforced verifies a second verify inside the cooldown
// window is rejected and does not persist a second snapshot.
func TestServiceCooldownEnforced(t *testing.T) {
	svc, db := newServiceTestEnv(t)
	ctx := context.Background()
	domID := seedDomain(t, svc, "cooldown.example.com", 1)

	if err := svc.VerifyDomain(ctx, 1, domID); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	if err := svc.VerifyDomain(ctx, 1, domID); err != ErrVerificationCooldown {
		t.Fatalf("second verify err = %v, want ErrVerificationCooldown", err)
	}
	if n := countSnapshots(t, db, domID); n != 1 {
		t.Fatalf("snapshots after cooldown-blocked second verify = %d, want 1", n)
	}
}

// TestServiceConcurrentVerifySingleSnapshot is the regression test for the
// cooldown race: many concurrent verify calls for the same domain must
// persist exactly one snapshot (the rest hit the cooldown), never
// duplicates.
func TestServiceConcurrentVerifySingleSnapshot(t *testing.T) {
	svc, db := newServiceTestEnv(t)
	ctx := context.Background()
	domID := seedDomain(t, svc, "race.example.com", 1)

	const n = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			errs[idx] = svc.VerifyDomain(ctx, 1, domID)
		}(i)
	}
	close(start)
	wg.Wait()

	success, cooldown := 0, 0
	for _, err := range errs {
		switch err {
		case nil:
			success++
		case ErrVerificationCooldown:
			cooldown++
		default:
			t.Fatalf("unexpected verify error: %v", err)
		}
	}
	if success != 1 {
		t.Errorf("successful verifies = %d, want exactly 1", success)
	}
	if cooldown != n-1 {
		t.Errorf("cooldown rejections = %d, want %d", cooldown, n-1)
	}
	if got := countSnapshots(t, db, domID); got != 1 {
		t.Fatalf("persisted snapshots = %d, want exactly 1 (no duplicates)", got)
	}
}

// TestServiceVerifyPersistsAndReadsBack verifies a snapshot survives a
// round-trip: after VerifyDomain, GetLatestSnapshot returns a non-nil
// snapshot for the domain.
func TestServiceVerifyPersistsAndReadsBack(t *testing.T) {
	svc, _ := newServiceTestEnv(t)
	ctx := context.Background()
	domID := seedDomain(t, svc, "persist.example.com", 1)

	if err := svc.VerifyDomain(ctx, 1, domID); err != nil {
		t.Fatalf("verify: %v", err)
	}
	snap, err := svc.GetLatestSnapshot(ctx, 1, domID)
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if snap == nil {
		t.Fatal("GetLatestSnapshot returned nil after a successful verify")
	}
	if snap.DomainID != domID {
		t.Errorf("snapshot domain_id = %d, want %d", snap.DomainID, domID)
	}
	// All lookups are NXDOMAIN → overall status "fail", score 0.
	if snap.Status != "fail" {
		t.Errorf("snapshot status = %q, want fail", snap.Status)
	}
}

// newServiceTestEnvWithResolver is newServiceTestEnv but also returns the
// FakeResolver, so tests can seed specific MX/TXT records instead of only
// exercising the universal-NXDOMAIN default.
func newServiceTestEnvWithResolver(t *testing.T) (*Service, *sql.DB, *dnsops.FakeResolver) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cds.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
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
		)`)
	if err != nil {
		t.Fatalf("create coremail_domains: %v", err)
	}

	domainRepo := coremail.NewDomainSQLRepo(db)
	verifRepo := NewVerificationRepo(db)
	if err := verifRepo.EnsureTable(context.Background()); err != nil {
		t.Fatalf("ensure verifications table: %v", err)
	}
	resolver := dnsops.NewFakeResolver()
	inspector := NewDNSInspector(resolver)
	svc := NewService(db, domainRepo, inspector, verifRepo)
	return svc, db, resolver
}

// TestGetEnterpriseDNSTenantIsolation and TestVerifyEnterpriseDNSTenantIsolation
// are the regression tests for the two new enterprise DNS entry points this
// package adds. TestServiceTenantIsolation above covers the pre-existing
// GetDNS/VerifyDomain/GetDomain/GetLatestSnapshot methods, but GetEnterpriseDNS
// and VerifyEnterpriseDNS are separate methods with their own tenant check —
// they had zero test coverage before this change, despite being the exact
// methods the new /enterprise/domains/:id/dns and .../dns/verify routes call.
func TestGetEnterpriseDNSTenantIsolation(t *testing.T) {
	svc, _, _ := newServiceTestEnvWithResolver(t)
	ctx := context.Background()
	domID := seedDomain(t, svc, "eowned.example.com", 1)
	const attacker = uint(2)

	if _, err := svc.GetEnterpriseDNS(ctx, attacker, domID, nil); err != ErrDomainNotFound {
		t.Errorf("GetEnterpriseDNS cross-tenant err = %v, want ErrDomainNotFound", err)
	}
	if _, err := svc.GetEnterpriseDNS(ctx, attacker, domID+999, nil); err != ErrDomainNotFound {
		t.Errorf("GetEnterpriseDNS nonexistent id err = %v, want ErrDomainNotFound (must be indistinguishable from cross-tenant)", err)
	}
	if _, err := svc.GetEnterpriseDNS(ctx, 1, domID, nil); err != nil {
		t.Errorf("GetEnterpriseDNS owner err = %v, want nil", err)
	}
}

func TestVerifyEnterpriseDNSTenantIsolation(t *testing.T) {
	svc, db, _ := newServiceTestEnvWithResolver(t)
	ctx := context.Background()
	domID := seedDomain(t, svc, "eowned2.example.com", 1)
	const attacker = uint(2)

	if _, err := svc.VerifyEnterpriseDNS(ctx, attacker, domID, nil); err != ErrDomainNotFound {
		t.Errorf("VerifyEnterpriseDNS cross-tenant err = %v, want ErrDomainNotFound", err)
	}
	if n := countSnapshots(t, db, domID); n != 0 {
		t.Errorf("snapshots after cross-tenant verify attempt = %d, want 0", n)
	}
	if _, err := svc.VerifyEnterpriseDNS(ctx, 1, domID, nil); err != nil {
		t.Errorf("VerifyEnterpriseDNS owner err = %v, want nil", err)
	}
}

// TestResolveExpectedMXUsesDomainNameNotID is the regression test for the
// fix in this change: the handler previously built the ExpectedMX fallback
// from the URL's numeric :id path parameter ("mail.42") instead of the
// resolved domain name ("mail.example.com"), because the fallback was
// computed in the handler before the domain name was known. It is now
// computed inside the service, after GetByID resolves the real name.
func TestResolveExpectedMXUsesDomainNameNotID(t *testing.T) {
	if got := resolveExpectedMX(nil, "example.com"); len(got) != 1 || got[0] != "mail.example.com" {
		t.Errorf("resolveExpectedMX(nil, %q) = %v, want [mail.example.com]", "example.com", got)
	}
	if got := resolveExpectedMX([]string{}, "42"); len(got) != 1 || got[0] != "mail.42" {
		// This is the literal defect shape: if a caller ever passes the
		// numeric id string as domainName by mistake, the bug reappears.
		// This assertion documents the contract precisely: resolveExpectedMX
		// trusts its domainName argument completely — callers must pass a
		// real domain name, never a path parameter.
		t.Errorf("resolveExpectedMX(nil, %q) = %v, want [mail.42] (documents caller contract)", "42", got)
	}
	configured := []string{"mx1.example.com", "mx2.example.com"}
	if got := resolveExpectedMX(configured, "example.com"); len(got) != 2 || got[0] != configured[0] || got[1] != configured[1] {
		t.Errorf("resolveExpectedMX with configured value = %v, want unchanged %v", got, configured)
	}
}

func TestGetEnterpriseDNSUsesRealDomainNameForMXFallback(t *testing.T) {
	svc, _, resolver := newServiceTestEnvWithResolver(t)
	ctx := context.Background()
	domID := seedDomain(t, svc, "mxfallback.example.com", 1)

	// Seed the MX record that the CORRECT fallback ("mail.mxfallback.example.com")
	// would look for. If the old bug were present, the fallback would be
	// "mail.<numeric id>" and this lookup would never match, always failing MX.
	resolver.Set("mxfallback.example.com", dnsops.FakeEntry{
		MX: []net.MX{{Host: "mail.mxfallback.example.com.", Pref: 10}},
	})

	health, err := svc.GetEnterpriseDNS(ctx, 1, domID, nil)
	if err != nil {
		t.Fatalf("GetEnterpriseDNS: %v", err)
	}
	if health.MX == nil {
		t.Fatal("expected MX result")
	}
	if health.MX.Status != "pass" {
		t.Errorf("MX status = %q, want pass (fallback must resolve to the real domain name, not the numeric id)", health.MX.Status)
	}
}

// TestVerifyEnterpriseDNSDoesNotFalselyClaimDKIMMatch is the regression test
// for the removed false-positive: the previous code set DKIM.MatchesDNS=true
// whenever any DKIM TXT record was published, without ever comparing it
// against the tenant's actual stored key (VerifyEnterpriseDNS always called
// the inspector with an empty expectedDKIMRecord). A stale or wrong
// published key would have been reported as "matches".
func TestVerifyEnterpriseDNSDoesNotFalselyClaimDKIMMatch(t *testing.T) {
	svc, _, resolver := newServiceTestEnvWithResolver(t)
	ctx := context.Background()
	domID := seedDomain(t, svc, "dkimclaim.example.com", 1)
	if _, err := svc.db.Exec(`UPDATE coremail_domains SET dkim_enabled = 1, dkim_selector = 'mail' WHERE id = ?`, domID); err != nil {
		t.Fatalf("enable dkim: %v", err)
	}
	// Publish SOME DKIM TXT record — status will read "pass" (a record
	// exists), but the service never told the inspector what the real
	// expected value is, so this must not be reported as a verified match.
	resolver.Set("mail._domainkey.dkimclaim.example.com", dnsops.FakeEntry{
		TXT: []string{"v=DKIM1; k=rsa; p=anything-could-be-here"},
	})

	health, err := svc.VerifyEnterpriseDNS(ctx, 1, domID, nil)
	if err != nil {
		t.Fatalf("VerifyEnterpriseDNS: %v", err)
	}
	if health.DKIM == nil {
		t.Fatal("expected DKIM result")
	}
	if health.DKIM.MatchesDNS {
		t.Error("DKIM.MatchesDNS = true, but no real comparison against the stored key was ever performed — this is a false positive")
	}
}

// ── DKIM key matching tests (Part 1) ──────────────────────────

func newServiceTestEnvWithDKIM(t *testing.T) (*Service, *sql.DB, *dnsops.FakeResolver, *dkim.SQLRepo) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cds_dkim.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
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
		CREATE TABLE IF NOT EXISTS coremail_dkim_config (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain TEXT UNIQUE NOT NULL,
			selector TEXT NOT NULL DEFAULT 'default',
			private_key_pem TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`)
	if err != nil {
		t.Fatalf("create tables: %v", err)
	}

	domainRepo := coremail.NewDomainSQLRepo(db)
	verifRepo := NewVerificationRepo(db)
	if err := verifRepo.EnsureTable(context.Background()); err != nil {
		t.Fatalf("ensure verifications table: %v", err)
	}
	resolver := dnsops.NewFakeResolver()
	inspector := NewDNSInspector(resolver)
	svc := NewService(db, domainRepo, inspector, verifRepo)
	dkimRepo := dkim.NewSQLRepo(db)
	svc.SetDKIMRepo(dkimRepo)
	return svc, db, resolver, dkimRepo
}

func generateTestKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func generateTestKeyPubRecord(t *testing.T, privPEM, selector, domain string) string {
	t.Helper()
	_, record, err := dkim.GenerateKeyPair(selector, domain)
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	return record
}

func TestDKIMKeyMatch_ExactMatch(t *testing.T) {
	svc, _, resolver, dkimRepo := newServiceTestEnvWithDKIM(t)
	ctx := context.Background()
	domID := seedDomain(t, svc, "exactmatch.example.com", 1)
	if _, err := svc.db.Exec(`UPDATE coremail_domains SET dkim_enabled = 1, dkim_selector = 'mail' WHERE id = ?`, domID); err != nil {
		t.Fatalf("enable dkim: %v", err)
	}

	sel := "mail"
	dom := "exactmatch.example.com"
	privPEM, pubDNS, err := dkim.GenerateKeyPair(sel, dom)
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	if err := dkimRepo.Create(ctx, &dkim.DKIMConfig{
		Domain:        dom,
		Selector:      sel,
		PrivateKeyPEM: privPEM,
		Enabled:       true,
	}, nil); err != nil {
		t.Fatalf("create dkim config: %v", err)
	}

	resolver.Set(sel+"._domainkey."+dom, dnsops.FakeEntry{
		TXT: []string{pubDNS},
	})

	health, err := svc.VerifyEnterpriseDNS(ctx, 1, domID, nil)
	if err != nil {
		t.Fatalf("VerifyEnterpriseDNS: %v", err)
	}
	if health.DKIM == nil {
		t.Fatal("expected DKIM result")
	}
	if !health.DKIM.MatchesDNS {
		t.Error("DKIM.MatchesDNS = false, want true (key should match)")
	}
	if health.DKIM.Status != "pass" {
		t.Errorf("DKIM status = %q, want pass", health.DKIM.Status)
	}
}

func TestDKIMKeyMatch_DNSMissing(t *testing.T) {
	svc, _, _, dkimRepo := newServiceTestEnvWithDKIM(t)
	ctx := context.Background()
	domID := seedDomain(t, svc, "dnsmissing.example.com", 1)
	if _, err := svc.db.Exec(`UPDATE coremail_domains SET dkim_enabled = 1, dkim_selector = 'mail' WHERE id = ?`, domID); err != nil {
		t.Fatalf("enable dkim: %v", err)
	}

	sel := "mail"
	dom := "dnsmissing.example.com"
	privPEM, _, err := dkim.GenerateKeyPair(sel, dom)
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	if err := dkimRepo.Create(ctx, &dkim.DKIMConfig{
		Domain:        dom,
		Selector:      sel,
		PrivateKeyPEM: privPEM,
		Enabled:       true,
	}, nil); err != nil {
		t.Fatalf("create dkim config: %v", err)
	}

	health, err := svc.VerifyEnterpriseDNS(ctx, 1, domID, nil)
	if err != nil {
		t.Fatalf("VerifyEnterpriseDNS: %v", err)
	}
	if health.DKIM == nil {
		t.Fatal("expected DKIM result")
	}
	if health.DKIM.MatchesDNS {
		t.Error("DKIM.MatchesDNS = true, want false (no DNS published)")
	}
	if !strings.Contains(strings.ToLower(health.DKIM.Reason), "dkim") {
		t.Logf("DKIM reason = %q", health.DKIM.Reason)
	}
}

func TestDKIMKeyMatch_NotConfigured(t *testing.T) {
	svc, _, _, _ := newServiceTestEnvWithDKIM(t)
	ctx := context.Background()
	domID := seedDomain(t, svc, "notconfig.example.com", 1)
	if _, err := svc.db.Exec(`UPDATE coremail_domains SET dkim_enabled = 1, dkim_selector = 'mail' WHERE id = ?`, domID); err != nil {
		t.Fatalf("enable dkim: %v", err)
	}

	health, err := svc.VerifyEnterpriseDNS(ctx, 1, domID, nil)
	if err != nil {
		t.Fatalf("VerifyEnterpriseDNS: %v", err)
	}
	if health.DKIM == nil {
		t.Fatal("expected DKIM result")
	}
	if health.DKIM.Configured == false {
		t.Log("configured=false is expected when no DKIM config exists")
	}
	if health.DKIM.MatchesDNS {
		t.Error("DKIM.MatchesDNS = true, want false (no config in DB)")
	}
}

func TestDKIMKeyMatch_StaleMismatch(t *testing.T) {
	svc, _, resolver, dkimRepo := newServiceTestEnvWithDKIM(t)
	ctx := context.Background()
	domID := seedDomain(t, svc, "stale.example.com", 1)
	if _, err := svc.db.Exec(`UPDATE coremail_domains SET dkim_enabled = 1, dkim_selector = 'mail' WHERE id = ?`, domID); err != nil {
		t.Fatalf("enable dkim: %v", err)
	}

	sel := "mail"
	dom := "stale.example.com"
	privPEM, _, err := dkim.GenerateKeyPair(sel, dom)
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	if err := dkimRepo.Create(ctx, &dkim.DKIMConfig{
		Domain:        dom,
		Selector:      sel,
		PrivateKeyPEM: privPEM,
		Enabled:       true,
	}, nil); err != nil {
		t.Fatalf("create dkim config: %v", err)
	}

	_, oldPubDNS, err := dkim.GenerateKeyPair(sel, dom)
	if err != nil {
		t.Fatalf("old GenerateKeyPair: %v", err)
	}
	resolver.Set(sel+"._domainkey."+dom, dnsops.FakeEntry{
		TXT: []string{oldPubDNS},
	})

	health, err := svc.VerifyEnterpriseDNS(ctx, 1, domID, nil)
	if err != nil {
		t.Fatalf("VerifyEnterpriseDNS: %v", err)
	}
	if health.DKIM == nil {
		t.Fatal("expected DKIM result")
	}
	if health.DKIM.MatchesDNS {
		t.Error("DKIM.MatchesDNS = true, want false (DNS has old rotated key)")
	}
	if !strings.Contains(strings.ToLower(health.DKIM.Reason), "mismatch") {
		t.Logf("DKIM reason = %q (expected 'key mismatch')", health.DKIM.Reason)
	}
}

func TestDKIMKeyMatch_SplitTXT(t *testing.T) {
	svc, _, resolver, dkimRepo := newServiceTestEnvWithDKIM(t)
	ctx := context.Background()
	domID := seedDomain(t, svc, "splittxt.example.com", 1)
	if _, err := svc.db.Exec(`UPDATE coremail_domains SET dkim_enabled = 1, dkim_selector = 'mail' WHERE id = ?`, domID); err != nil {
		t.Fatalf("enable dkim: %v", err)
	}

	sel := "mail"
	dom := "splittxt.example.com"
	privPEM, pubDNS, err := dkim.GenerateKeyPair(sel, dom)
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	if err := dkimRepo.Create(ctx, &dkim.DKIMConfig{
		Domain:        dom,
		Selector:      sel,
		PrivateKeyPEM: privPEM,
		Enabled:       true,
	}, nil); err != nil {
		t.Fatalf("create dkim config: %v", err)
	}

	mid := len(pubDNS) / 3
	resolver.Set(sel+"._domainkey."+dom, dnsops.FakeEntry{
		TXT: []string{pubDNS[:mid], pubDNS[mid : 2*mid], pubDNS[2*mid:]},
	})

	health, err := svc.VerifyEnterpriseDNS(ctx, 1, domID, nil)
	if err != nil {
		t.Fatalf("VerifyEnterpriseDNS: %v", err)
	}
	if health.DKIM == nil {
		t.Fatal("expected DKIM result")
	}
	if !health.DKIM.MatchesDNS {
		t.Error("DKIM.MatchesDNS = false, want true (split TXT should join and match)")
	}
}

func TestDKIMKeyMatch_NoPrivateKeyExposure(t *testing.T) {
	svc, _, resolver, dkimRepo := newServiceTestEnvWithDKIM(t)
	ctx := context.Background()
	domID := seedDomain(t, svc, "noexp.example.com", 1)
	if _, err := svc.db.Exec(`UPDATE coremail_domains SET dkim_enabled = 1, dkim_selector = 'mail' WHERE id = ?`, domID); err != nil {
		t.Fatalf("enable dkim: %v", err)
	}

	sel := "mail"
	dom := "noexp.example.com"
	privPEM, pubDNS, err := dkim.GenerateKeyPair(sel, dom)
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	if err := dkimRepo.Create(ctx, &dkim.DKIMConfig{
		Domain:        dom,
		Selector:      sel,
		PrivateKeyPEM: privPEM,
		Enabled:       true,
	}, nil); err != nil {
		t.Fatalf("create dkim config: %v", err)
	}

	resolver.Set(sel+"._domainkey."+dom, dnsops.FakeEntry{
		TXT: []string{pubDNS},
	})

	health, err := svc.VerifyEnterpriseDNS(ctx, 1, domID, nil)
	if err != nil {
		t.Fatalf("VerifyEnterpriseDNS: %v", err)
	}
	if health.DKIM == nil {
		t.Fatal("expected DKIM result")
	}

	fields := []string{
		health.DKIM.Selector,
		health.DKIM.Status,
		health.DKIM.Expected,
		health.DKIM.Observed,
		health.DKIM.Reason,
		health.DKIM.RecordName,
	}
	for _, f := range fields {
		lower := strings.ToLower(f)
		if strings.Contains(lower, "private") || strings.Contains(lower, "secret") {
			t.Errorf("DKIMHealthCheck response field contains sensitive word: %q", f)
		}
	}
	if strings.Contains(health.DKIM.Expected, "PRIVATE") {
		t.Error("Expected field must not contain private key")
	}
}

func TestDeriveExpectedDKIMRecord_Valid(t *testing.T) {
	sel := "mail"
	dom := "example.com"
	privPEM, expectedRecord, err := dkim.GenerateKeyPair(sel, dom)
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	got, ok := deriveExpectedDKIMRecord(privPEM, sel, dom)
	if !ok {
		t.Fatal("deriveExpectedDKIMRecord should succeed for valid PEM")
	}
	if got != expectedRecord {
		t.Errorf("derived record:\n  got:  %s\n  want: %s", got, expectedRecord)
	}
}

func TestDeriveExpectedDKIMRecord_InvalidPEM(t *testing.T) {
	got, ok := deriveExpectedDKIMRecord("not a valid pem", "mail", "example.com")
	if ok {
		t.Error("deriveExpectedDKIMRecord should return false for invalid PEM")
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestDeriveExpectedDKIMRecord_WrongSelector(t *testing.T) {
	sel := "mail"
	dom := "example.com"
	privPEM, _, err := dkim.GenerateKeyPair(sel, dom)
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	got, ok := deriveExpectedDKIMRecord(privPEM, "other", dom)
	if !ok {
		t.Fatal("deriveExpectedDKIMRecord should succeed")
	}
	if strings.Contains(got, "BEGIN") {
		t.Errorf("record must not contain private key: %q", got)
	}
	t.Logf("record with other selector: %s", got)
}

func TestDKIMKeyMatch_MalformedTXT(t *testing.T) {
	svc, _, resolver, dkimRepo := newServiceTestEnvWithDKIM(t)
	ctx := context.Background()
	domID := seedDomain(t, svc, "malformed.example.com", 1)
	if _, err := svc.db.Exec(`UPDATE coremail_domains SET dkim_enabled = 1, dkim_selector = 'mail' WHERE id = ?`, domID); err != nil {
		t.Fatalf("enable dkim: %v", err)
	}

	sel := "mail"
	dom := "malformed.example.com"
	privPEM, _, err := dkim.GenerateKeyPair(sel, dom)
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	if err := dkimRepo.Create(ctx, &dkim.DKIMConfig{
		Domain:        dom,
		Selector:      sel,
		PrivateKeyPEM: privPEM,
		Enabled:       true,
	}, nil); err != nil {
		t.Fatalf("create dkim config: %v", err)
	}

	resolver.Set(sel+"._domainkey."+dom, dnsops.FakeEntry{
		TXT: []string{"some-random-txt-no-dkim-signature"},
	})

	health, err := svc.VerifyEnterpriseDNS(ctx, 1, domID, nil)
	if err != nil {
		t.Fatalf("VerifyEnterpriseDNS: %v", err)
	}
	if health.DKIM == nil {
		t.Fatal("expected DKIM result")
	}
	if health.DKIM.MatchesDNS {
		t.Error("DKIM.MatchesDNS = true, want false (malformed TXT, no p= tag in normalized expected)")
	}
	t.Logf("DKIM status = %q, reason = %q", health.DKIM.Status, health.DKIM.Reason)
}

func TestNormalizeDKIMTXT_WhitespaceAndQuotes(t *testing.T) {
	input := `"v=DKIM1; k=rsa; p=TEST KEY"`
	got := normalizeDKIMTXT(input)
	if got != "v=DKIM1;k=rsa;p=TESTKEY" {
		t.Errorf("normalizeDKIMTXT = %q, want whitespace-free and quote-free", got)
	}
}
