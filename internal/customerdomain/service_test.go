package customerdomain

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	"github.com/orvix/orvix/internal/coremail"
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
