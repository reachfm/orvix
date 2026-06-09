package audit

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("%s/audit_test.db", t.TempDir()))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	s := NewStore(db)
	if err := s.EnsureTable(context.Background()); err != nil {
		t.Fatalf("ensure table: %v", err)
	}
	return s
}

func TestRecordAndSearch(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	s.Record(ctx, &Entry{Actor: "admin", Role: "admin", Action: "login", Result: "success", IP: "127.0.0.1"})
	s.Record(ctx, &Entry{Actor: "admin", Role: "admin", Action: "logout", Result: "success", IP: "127.0.0.1"})

	entries, total, err := s.Search(ctx, &Query{Limit: 100})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected 2, got %d", total)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestFilterByAction(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	s.Record(ctx, &Entry{Actor: "admin", Action: "login", Result: "success"})
	s.Record(ctx, &Entry{Actor: "admin", Action: "logout", Result: "success"})

	entries, total, err := s.Search(ctx, &Query{Action: "login", Limit: 100})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected 1, got %d", total)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestFilterByActor(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	s.Record(ctx, &Entry{Actor: "admin@test.com", Action: "login"})
	s.Record(ctx, &Entry{Actor: "user@test.com", Action: "login"})

	res, total, err := s.Search(ctx, &Query{Actor: "admin", Limit: 100})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected 1, got %d", total)
	}
	_ = res
}

func TestPagination(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		s.Record(ctx, &Entry{Actor: "user", Action: fmt.Sprintf("action_%d", i)})
	}

	page, total, err := s.Search(ctx, &Query{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if total != 5 {
		t.Fatalf("expected total 5, got %d", total)
	}
	if len(page) != 2 {
		t.Fatalf("expected 2 on page 1, got %d", len(page))
	}
}

func TestPurgeOlderThan(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	s.Record(ctx, &Entry{Actor: "old", Action: "test", Timestamp: time.Now().Add(-48 * time.Hour)})
	s.Record(ctx, &Entry{Actor: "new", Action: "test", Timestamp: time.Now()})

	deleted, err := s.PurgeOlderThan(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}
}

func TestAuditPersistenceAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := fmt.Sprintf("%s/audit_restart.db", t.TempDir())

	db1, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db1: %v", err)
	}
	store1 := NewStore(db1)
	if err := store1.EnsureTable(ctx); err != nil {
		t.Fatalf("ensure table: %v", err)
	}
	if err := store1.Record(ctx, &Entry{Actor: "admin", Role: "admin", Action: "domain_create", Target: "example.com", Result: "success", IP: "127.0.0.1"}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close db1: %v", err)
	}

	db2, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db2: %v", err)
	}
	defer db2.Close()
	store2 := NewStore(db2)
	entries, total, err := store2.Search(ctx, &Query{Action: "domain_create", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if total != 1 || len(entries) != 1 {
		t.Fatalf("expected one restored audit entry, total=%d len=%d", total, len(entries))
	}
	if entries[0].Target != "example.com" || entries[0].Result != "success" {
		t.Fatalf("wrong restored audit entry: %#v", entries[0])
	}
}
