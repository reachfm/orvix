package trust

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func testTrustRepo(t *testing.T, dbPath string) (*sql.DB, *Repository) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	for _, stmt := range Tables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	return db, NewRepository(db)
}

func TestTrustPersistenceAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "trust.db")

	db1, repo1 := testTrustRepo(t, dbPath)
	eng1 := NewEngineWithRepo(repo1)
	eng1.SetUserTrust("user@example.com", TrustHigh, "known good")
	eng1.SetMailboxTrust(7, TrustMedium, "steady")
	eng1.SetDomainTrust("example.com", TrustLow, "complaints")
	eng1.SetIPTrust("192.0.2.7", TrustLow, "bad auth")
	if err := db1.Close(); err != nil {
		t.Fatalf("close db1: %v", err)
	}

	db2, repo2 := testTrustRepo(t, dbPath)
	defer db2.Close()
	eng2 := NewEngineWithRepo(repo2)
	if err := eng2.LoadFromDB(ctx); err != nil {
		t.Fatalf("load from db: %v", err)
	}

	if got := eng2.GetUserTrust("user@example.com"); got == nil || got.Score != TrustHigh || got.Reason != "known good" {
		t.Fatalf("user trust not restored: %#v", got)
	}
	if got := eng2.GetMailboxTrust(7); got == nil || got.Score != TrustMedium || got.Reason != "steady" {
		t.Fatalf("mailbox trust not restored: %#v", got)
	}
	if got := eng2.GetDomainTrust("example.com"); got == nil || got.Score != TrustLow || got.Reason != "complaints" {
		t.Fatalf("domain trust not restored: %#v", got)
	}
	if got := eng2.GetIPTrust("192.0.2.7"); got == nil || got.Score != TrustLow || got.Reason != "bad auth" {
		t.Fatalf("ip trust not restored: %#v", got)
	}
}

func TestLockoutPersistenceAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "lockout.db")

	db1, repo1 := testTrustRepo(t, dbPath)
	eng1 := NewEngineWithRepo(repo1)
	for i := 0; i < eng1.policy.MaxAttempts; i++ {
		eng1.RecordAuthFailure("user@example.com")
	}
	if !eng1.IsLockedOut("user@example.com") {
		t.Fatal("expected lockout before restart")
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close db1: %v", err)
	}

	db2, repo2 := testTrustRepo(t, dbPath)
	eng2 := NewEngineWithRepo(repo2)
	if err := eng2.LoadFromDB(ctx); err != nil {
		t.Fatalf("load from db: %v", err)
	}
	if !eng2.IsLockedOut("user@example.com") {
		t.Fatal("lockout not restored")
	}
	if !eng2.ClearLockout("user@example.com") {
		t.Fatal("clear lockout failed")
	}
	if err := db2.Close(); err != nil {
		t.Fatalf("close db2: %v", err)
	}

	db3, repo3 := testTrustRepo(t, dbPath)
	defer db3.Close()
	eng3 := NewEngineWithRepo(repo3)
	if err := eng3.LoadFromDB(ctx); err != nil {
		t.Fatalf("load after clear: %v", err)
	}
	if eng3.IsLockedOut("user@example.com") {
		t.Fatal("cleared lockout restored after restart")
	}

	if err := repo3.SaveLockout(ctx, "expired@example.com", time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("save expired lockout: %v", err)
	}
	eng4 := NewEngineWithRepo(repo3)
	if err := eng4.LoadFromDB(ctx); err != nil {
		t.Fatalf("load expired: %v", err)
	}
	if eng4.IsLockedOut("expired@example.com") {
		t.Fatal("expired lockout restored")
	}
}
