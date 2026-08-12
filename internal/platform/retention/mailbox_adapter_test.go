package retention

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/admin/mailbox"
	_ "modernc.org/sqlite"
)

func newMailboxAdapterTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE coremail_mailboxes (
		id INTEGER PRIMARY KEY AUTOINCREMENT, domain_id INTEGER NOT NULL DEFAULT 1, tenant_id INTEGER NOT NULL,
		local_part TEXT NOT NULL DEFAULT 'x', email TEXT NOT NULL, name TEXT, password_hash TEXT NOT NULL DEFAULT 'h',
		auth_scheme TEXT, status TEXT NOT NULL, quota_mb INTEGER NOT NULL DEFAULT 0, used_bytes INTEGER NOT NULL DEFAULT 0,
		msg_count INTEGER NOT NULL DEFAULT 0, is_admin INTEGER NOT NULL DEFAULT 0, allow_smtp INTEGER NOT NULL DEFAULT 1,
		allow_imap INTEGER NOT NULL DEFAULT 1, allow_pop3 INTEGER NOT NULL DEFAULT 1, allow_jmap INTEGER NOT NULL DEFAULT 1,
		mfa_enabled INTEGER NOT NULL DEFAULT 0, send_limit_per_hour INTEGER NOT NULL DEFAULT 0,
		last_login DATETIME, last_ip TEXT, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME
	)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedMailboxRow(t *testing.T, db *sql.DB, tenantID uint, email, status string, deletedAt *time.Time) uint {
	t.Helper()
	now := time.Now().UTC()
	res, err := db.Exec(`INSERT INTO coremail_mailboxes (tenant_id, email, status, created_at, updated_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?)`,
		tenantID, email, status, now, now, deletedAt)
	if err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}
	id, _ := res.LastInsertId()
	return uint(id)
}

func TestMailboxPurgeAdapter_OnlyCountsAndPurgesSoftDeletedPastCutoff(t *testing.T) {
	db := newMailboxAdapterTestDB(t)
	repo := mailbox.NewAdminMailboxRepo(db)
	adapter := NewMailboxPurgeAdapter(repo)
	ctx := context.Background()

	oldDeleted := time.Now().UTC().Add(-100 * 24 * time.Hour)
	recentDeleted := time.Now().UTC().Add(-time.Hour)
	seedMailboxRow(t, db, 1, "old-deleted@test.com", "deleted", &oldDeleted)
	seedMailboxRow(t, db, 1, "recent-deleted@test.com", "deleted", &recentDeleted)
	liveID := seedMailboxRow(t, db, 1, "live@test.com", "active", nil)

	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)
	n, err := adapter.CountEligible(ctx, "tenant", 1, cutoff)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 eligible (only the old-deleted row), got %d", n)
	}

	purged, err := adapter.PurgeBatch(ctx, "tenant", 1, cutoff, 100)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if purged != 1 {
		t.Fatalf("expected 1 purged, got %d", purged)
	}

	var remaining int
	db.QueryRow(`SELECT COUNT(*) FROM coremail_mailboxes`).Scan(&remaining)
	if remaining != 2 {
		t.Fatalf("expected 2 rows remaining (recent-deleted + live), got %d", remaining)
	}
	var liveStillExists int
	db.QueryRow(`SELECT COUNT(*) FROM coremail_mailboxes WHERE id=? AND deleted_at IS NULL`, liveID).Scan(&liveStillExists)
	if liveStillExists != 1 {
		t.Fatal("the live mailbox must never be touched by purge")
	}
}

func TestMailboxPurgeAdapter_TenantScoped(t *testing.T) {
	db := newMailboxAdapterTestDB(t)
	repo := mailbox.NewAdminMailboxRepo(db)
	adapter := NewMailboxPurgeAdapter(repo)
	ctx := context.Background()

	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
	seedMailboxRow(t, db, 1, "t1@test.com", "deleted", &old)
	seedMailboxRow(t, db, 2, "t2@test.com", "deleted", &old)

	cutoff := time.Now().UTC().Add(-time.Hour)
	n, err := adapter.CountEligible(ctx, "tenant", 1, cutoff)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 eligible for tenant 1, got %d", n)
	}

	purged, err := adapter.PurgeBatch(ctx, "tenant", 1, cutoff, 100)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if purged != 1 {
		t.Fatalf("expected 1 purged for tenant 1, got %d", purged)
	}
	var t2Remaining int
	db.QueryRow(`SELECT COUNT(*) FROM coremail_mailboxes WHERE tenant_id=2`).Scan(&t2Remaining)
	if t2Remaining != 1 {
		t.Fatal("tenant 2's row must be untouched by a tenant-1-scoped purge")
	}
}

// TestRetentionService_WithRealMailboxAdapter_EndToEnd proves the full
// stack — Service.ExecutePurge driving the REAL MailboxPurgeAdapter,
// not a fake — including legal hold enforcement.
func TestRetentionService_WithRealMailboxAdapter_EndToEnd(t *testing.T) {
	mailboxDB := newMailboxAdapterTestDB(t)
	repo := mailbox.NewAdminMailboxRepo(mailboxDB)
	adapter := NewMailboxPurgeAdapter(repo)

	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
	seedMailboxRow(t, mailboxDB, 1, "gone@test.com", "deleted", &old)

	retentionDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer retentionDB.Close()
	retentionDB.SetMaxOpenConns(1)
	rrepo := NewRepository(retentionDB)
	if err := rrepo.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	svc := NewService(rrepo, adapter, nil, nil, nil)
	ctx := context.Background()

	cutoff := time.Now().UTC().Add(-time.Hour)
	plan, err := svc.PlanPurge(ctx, "tenant", 1, cutoff)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.EligibleCount != 1 {
		t.Fatalf("expected 1 eligible in the real end-to-end plan, got %d", plan.EligibleCount)
	}

	svc.PlaceLegalHold(ctx, "tenant", 1, "case-1", "litigation hold", 1, nil)
	if _, err := svc.ExecutePurge(ctx, "tenant", 1, cutoff, PurgeConfirmationPhrase, "", 1); err != ErrLegalHoldActive {
		t.Fatalf("expected ErrLegalHoldActive, got %v", err)
	}
	var stillThere int
	mailboxDB.QueryRow(`SELECT COUNT(*) FROM coremail_mailboxes`).Scan(&stillThere)
	if stillThere != 1 {
		t.Fatal("the held mailbox must not have been purged")
	}
}
