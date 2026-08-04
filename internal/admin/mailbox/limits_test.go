package mailbox

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"

	"github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/audit"
)

// seedDomainWithLimits inserts a domain carrying the sentinel-encoded limits
// under test, plus the organization plan row the inherit path resolves against.
func seedDomainWithLimits(t *testing.T, db *sql.DB, name string, tenantID uint, maxMailboxes int, maxQuotaMB, defaultQuotaMB int64, orgMaxMailboxes int) uint {
	t.Helper()
	// The enforcement paths run inside the audit transaction, so the audit
	// table must exist for these tests to exercise the real code path.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS orvix_audit (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		actor TEXT NOT NULL DEFAULT '', actor_id INTEGER NOT NULL DEFAULT 0,
		actor_role TEXT NOT NULL DEFAULT '', tenant_id INTEGER NOT NULL DEFAULT 0,
		action TEXT NOT NULL DEFAULT '', target TEXT NOT NULL DEFAULT '',
		target_id INTEGER NOT NULL DEFAULT 0, result TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL DEFAULT '', before TEXT NOT NULL DEFAULT '',
		after TEXT NOT NULL DEFAULT '', request_id TEXT NOT NULL DEFAULT '',
		ip TEXT NOT NULL DEFAULT '', user_agent TEXT NOT NULL DEFAULT '',
		timestamp DATETIME NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		t.Fatal(err)
	}
	res, err := db.Exec(`INSERT INTO coremail_domains
		(tenant_id, name, status, max_mailboxes, max_aliases, max_quota_mb, default_mailbox_quota_mb, created_at, updated_at)
		VALUES (?, ?, 'active', ?, 0, ?, ?, '2026-01-01', '2026-01-01')`,
		tenantID, name, maxMailboxes, maxQuotaMB, defaultQuotaMB)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if _, err := db.Exec(`INSERT INTO tenants (id, plan, max_domains, max_mailboxes) VALUES (?, 'business', 100, ?)`,
		tenantID, orgMaxMailboxes); err != nil {
		t.Fatal(err)
	}
	return uint(id)
}

func newLimitsService(db *sql.DB) *Service {
	return NewService(NewAdminMailboxRepo(db), newTestHasher(), audit.NewExtendedStore(db), nil)
}

// TestCreateMailboxEnforcesDomainCap proves the cap is real enforcement, not a
// stored number: creation is refused once the domain's finite cap is reached.
func TestCreateMailboxEnforcesDomainCap(t *testing.T) {
	db := newMailboxTestDB(t)
	seedDomainWithLimits(t, db, "capped.test", 5, 2, 0, 0, 500)
	svc := newLimitsService(db)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if _, err := svc.CreateMailbox(ctx, CreateMailboxRequest{
			Email:    fmt.Sprintf("u%d@capped.test", i),
			Password: "Sup3rSecret!x",
		}, 5); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	_, err := svc.CreateMailbox(ctx, CreateMailboxRequest{
		Email:    "overflow@capped.test",
		Password: "Sup3rSecret!x",
	}, 5)
	if err != domain.ErrMailboxLimitReached {
		t.Fatalf("err = %v, want ErrMailboxLimitReached", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_mailboxes WHERE deleted_at IS NULL`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("mailbox count = %d, want 2 (the cap was overshot)", count)
	}
}

// TestCreateMailboxInheritsOrgCapWhenDomainInherits proves an omitted domain
// cap means INHERIT the organization ceiling, not unlimited.
func TestCreateMailboxInheritsOrgCapWhenDomainInherits(t *testing.T) {
	db := newMailboxTestDB(t)
	// Domain cap = LimitInherit(0); the org plan allows only 1 mailbox.
	seedDomainWithLimits(t, db, "inherit.test", 5, domain.LimitInherit, 0, 0, 1)
	svc := newLimitsService(db)
	ctx := context.Background()

	if _, err := svc.CreateMailbox(ctx, CreateMailboxRequest{Email: "a@inherit.test", Password: "Sup3rSecret!x"}, 5); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := svc.CreateMailbox(ctx, CreateMailboxRequest{Email: "b@inherit.test", Password: "Sup3rSecret!x"}, 5); err != domain.ErrMailboxLimitReached {
		t.Fatalf("err = %v, want the INHERITED org ceiling to be enforced", err)
	}
}

// TestCreateMailboxUnlimitedDomainSkipsCap proves the explicit unlimited
// sentinel really does bypass the check.
func TestCreateMailboxUnlimitedDomainSkipsCap(t *testing.T) {
	db := newMailboxTestDB(t)
	seedDomainWithLimits(t, db, "boundless.test", 5, domain.LimitUnlimited, 0, 0, 1)
	svc := newLimitsService(db)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := svc.CreateMailbox(ctx, CreateMailboxRequest{
			Email:    fmt.Sprintf("u%d@boundless.test", i),
			Password: "Sup3rSecret!x",
		}, 5); err != nil {
			t.Fatalf("unlimited domain create %d: %v", i, err)
		}
	}
}

// TestConcurrentMailboxCreationCannotExceedCap is the concurrency regression
// test. Ten simultaneous creations against a domain capped at 3 must produce
// exactly 3 mailboxes; the rest receive the typed limit error. A check-then-act
// implementation without the in-transaction count would overshoot here.
func TestConcurrentMailboxCreationCannotExceedCap(t *testing.T) {
	db := newMailboxTestDB(t)
	seedDomainWithLimits(t, db, "race.test", 5, 3, 0, 0, 500)
	db.SetMaxOpenConns(1)
	svc := newLimitsService(db)

	const attempts = 10
	var wg sync.WaitGroup
	errs := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.CreateMailbox(context.Background(), CreateMailboxRequest{
				Email:    fmt.Sprintf("r%d@race.test", i),
				Password: "Sup3rSecret!x",
			}, 5)
		}(i)
	}
	wg.Wait()

	created, rejected := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			created++
		case err == domain.ErrMailboxLimitReached:
			rejected++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if created != 3 || rejected != attempts-3 {
		t.Errorf("created=%d rejected=%d, want 3/%d", created, rejected, attempts-3)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_mailboxes WHERE deleted_at IS NULL`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("committed mailboxes = %d, want exactly the cap of 3", count)
	}
}

// --- quota bounds ----------------------------------------------------------

func TestCreateMailboxRejectsQuotaAboveDomainMaximum(t *testing.T) {
	db := newMailboxTestDB(t)
	seedDomainWithLimits(t, db, "quota.test", 5, 0, 2048, 1024, 500)
	svc := newLimitsService(db)

	if _, err := svc.CreateMailbox(context.Background(), CreateMailboxRequest{
		Email:    "big@quota.test",
		Password: "Sup3rSecret!x",
		QuotaMB:  4096,
	}, 5); err != domain.ErrQuotaExceedsDomain {
		t.Fatalf("err = %v, want ErrQuotaExceedsDomain", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_mailboxes`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("a mailbox was created despite an over-limit quota")
	}
}

func TestCreateMailboxStampsDomainDefaultQuota(t *testing.T) {
	db := newMailboxTestDB(t)
	seedDomainWithLimits(t, db, "default.test", 5, 0, 8192, 3072, 500)
	svc := newLimitsService(db)

	res, err := svc.CreateMailbox(context.Background(), CreateMailboxRequest{
		Email:    "u@default.test",
		Password: "Sup3rSecret!x",
	}, 5)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.Mailbox.QuotaMB != 3072 {
		t.Errorf("quota = %d, want the domain default 3072", res.Mailbox.QuotaMB)
	}
}

func TestCreateMailboxFallsBackToHistoricDefaultWhenInheriting(t *testing.T) {
	db := newMailboxTestDB(t)
	seedDomainWithLimits(t, db, "legacy.test", 5, 0, domain.LimitInherit, domain.LimitInherit, 500)
	svc := newLimitsService(db)

	res, err := svc.CreateMailbox(context.Background(), CreateMailboxRequest{
		Email:    "u@legacy.test",
		Password: "Sup3rSecret!x",
	}, 5)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.Mailbox.QuotaMB != domain.DefaultMailboxQuotaMB {
		t.Errorf("quota = %d, want the unchanged historic default %d", res.Mailbox.QuotaMB, domain.DefaultMailboxQuotaMB)
	}
}

func TestCreateMailboxRejectsNegativeQuota(t *testing.T) {
	db := newMailboxTestDB(t)
	seedDomainWithLimits(t, db, "neg.test", 5, 0, 0, 0, 500)
	svc := newLimitsService(db)

	if _, err := svc.CreateMailbox(context.Background(), CreateMailboxRequest{
		Email:    "u@neg.test",
		Password: "Sup3rSecret!x",
		QuotaMB:  -5,
	}, 5); err != ErrInvalidQuota {
		t.Fatalf("err = %v, want ErrInvalidQuota", err)
	}
}

// TestUpdateMailboxEnforcesQuotaCeiling proves the CHANGE path is guarded too:
// storing a ceiling is not enforcement if a later update can exceed it.
func TestUpdateMailboxEnforcesQuotaCeiling(t *testing.T) {
	db := newMailboxTestDB(t)
	seedDomainWithLimits(t, db, "change.test", 5, 0, 2048, 1024, 500)
	svc := newLimitsService(db)
	ctx := context.Background()

	created, err := svc.CreateMailbox(ctx, CreateMailboxRequest{
		Email:    "u@change.test",
		Password: "Sup3rSecret!x",
		QuotaMB:  1024,
	}, 5)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	over := int64(9999)
	if _, err := svc.UpdateMailbox(ctx, created.Mailbox.ID, 5, UpdateMailboxRequest{QuotaMB: &over}); err != domain.ErrQuotaExceedsDomain {
		t.Fatalf("update err = %v, want ErrQuotaExceedsDomain", err)
	}

	var stored int64
	if err := db.QueryRow(`SELECT quota_mb FROM coremail_mailboxes WHERE id=?`, created.Mailbox.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 1024 {
		t.Fatalf("quota was changed to %d despite the rejection", stored)
	}

	within := int64(2048)
	if _, err := svc.UpdateMailbox(ctx, created.Mailbox.ID, 5, UpdateMailboxRequest{QuotaMB: &within}); err != nil {
		t.Fatalf("in-bounds update must succeed: %v", err)
	}
}

func TestUpdateMailboxRejectsNegativeQuota(t *testing.T) {
	db := newMailboxTestDB(t)
	seedDomainWithLimits(t, db, "negupd.test", 5, 0, 0, 0, 500)
	svc := newLimitsService(db)
	ctx := context.Background()

	created, err := svc.CreateMailbox(ctx, CreateMailboxRequest{Email: "u@negupd.test", Password: "Sup3rSecret!x"}, 5)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	neg := int64(-1)
	if _, err := svc.UpdateMailbox(ctx, created.Mailbox.ID, 5, UpdateMailboxRequest{QuotaMB: &neg}); err != ErrInvalidQuota {
		t.Fatalf("err = %v, want ErrInvalidQuota", err)
	}
}

// TestResolveDomainAllocationIsTenantScoped keeps the cross-tenant contract:
// another tenant's domain must be indistinguishable from an absent one.
func TestResolveDomainAllocationIsTenantScoped(t *testing.T) {
	db := newMailboxTestDB(t)
	seedDomainWithLimits(t, db, "owned.test", 5, 10, 0, 0, 500)
	repo := NewAdminMailboxRepo(db)

	if _, err := repo.ResolveDomainAllocation(context.Background(), "owned.test", 6, false); err != sql.ErrNoRows {
		t.Fatalf("cross-tenant err = %v, want sql.ErrNoRows (no existence leak)", err)
	}
	if _, err := repo.ResolveDomainAllocation(context.Background(), "owned.test", 5, false); err != nil {
		t.Fatalf("owner lookup: %v", err)
	}
}
