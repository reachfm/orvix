package policy

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/orvix/orvix/internal/dbdialect"
	_ "modernc.org/sqlite"
)

func testPolicyRepo(t *testing.T, dbPath string) (*sql.DB, *Repository) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	for _, stmt := range Tables(dbdialect.FromDriver("sqlite")) {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	for _, stmt := range Indexes() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create index: %v", err)
		}
	}
	return db, NewRepository(db)
}

func TestPolicyPersistenceAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "policy.db")

	db1, repo1 := testPolicyRepo(t, dbPath)
	eng1 := NewEngine()
	eng1.SetRepository(repo1)
	eng1.SetDefaultMode(InternalOnly)
	eng1.SetTenantPolicy(42, Disabled)
	eng1.SetDomainPolicy("persist.example", SendOnly)
	eng1.SetMailboxPolicy(99, ReceiveOnly)
	eng1.SetDomainPolicy("delete.example", Disabled)
	eng1.DeleteDomainPolicy("delete.example")
	eng1.SetTenantPolicy(77, Disabled)
	eng1.DeleteTenantPolicy(77)
	eng1.SetMailboxPolicy(100, Disabled)
	eng1.DeleteMailboxPolicy(100)
	if err := db1.Close(); err != nil {
		t.Fatalf("close db1: %v", err)
	}

	db2, repo2 := testPolicyRepo(t, dbPath)
	defer db2.Close()
	eng2 := NewEngine()
	eng2.SetRepository(repo2)
	if err := eng2.LoadFromDB(ctx); err != nil {
		t.Fatalf("load from db: %v", err)
	}

	if got := eng2.Resolve(1, "none.example", nil).Mode; got != InternalOnly {
		t.Fatalf("default mode not restored: %v", got)
	}
	if p, ok := eng2.GetTenantPolicy(42); !ok || p.Mode != Disabled {
		t.Fatalf("tenant policy not restored: %#v ok=%v", p, ok)
	}
	if p, ok := eng2.GetDomainPolicy("persist.example"); !ok || p.Mode != SendOnly {
		t.Fatalf("domain policy not restored: %#v ok=%v", p, ok)
	}
	if p, ok := eng2.GetMailboxPolicy(99); !ok || p.Mode != ReceiveOnly {
		t.Fatalf("mailbox policy not restored: %#v ok=%v", p, ok)
	}
	if _, ok := eng2.GetDomainPolicy("delete.example"); ok {
		t.Fatal("deleted domain policy restored")
	}
	if _, ok := eng2.GetTenantPolicy(77); ok {
		t.Fatal("deleted tenant policy restored")
	}
	if _, ok := eng2.GetMailboxPolicy(100); ok {
		t.Fatal("deleted mailbox policy restored")
	}
}
