package incident

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newSQLiteTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "test.db")+"?_txlock=immediate")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func newTestService(t *testing.T) (*Service, context.Context) {
	t.Helper()
	db := newSQLiteTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return NewService(repo), ctx
}

func TestIncident_CreateAndGet(t *testing.T) {
	svc, ctx := newTestService(t)
	inc, err := svc.Create(ctx, "Test Incident", "something broke", SevMajor, []string{"mailbox"}, []string{"us-east"}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if inc.ID == 0 {
		t.Fatal("expected incident ID")
	}
	if inc.Status != StatusInvestigating {
		t.Fatalf("expected investigating, got %s", inc.Status)
	}
	got, err := svc.Get(ctx, inc.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "Test Incident" {
		t.Fatalf("title mismatch: %s", got.Title)
	}
}

func TestIncident_Transitions(t *testing.T) {
	svc, ctx := newTestService(t)
	inc, _ := svc.Create(ctx, "Test", "desc", SevMinor, nil, nil, nil)
	valid := []Status{StatusIdentified, StatusMonitoring, StatusResolved}
	prev := inc.Status
	for _, to := range valid {
		updated, err := svc.Update(ctx, inc.ID, to, "progress", "operator:1")
		if err != nil {
			t.Fatalf("transition %s->%s: %v", prev, to, err)
		}
		if updated.Status != to {
			t.Fatalf("expected %s, got %s", to, updated.Status)
		}
		prev = to
	}
	if _, err := svc.Update(ctx, inc.ID, StatusIdentified, "reopen?", "op:1"); err == nil {
		t.Fatal("expected error transitioning from resolved")
	}
}

func TestIncident_Timeline(t *testing.T) {
	svc, ctx := newTestService(t)
	inc, _ := svc.Create(ctx, "Test", "desc", SevMinor, nil, nil, nil)
	_, _ = svc.Update(ctx, inc.ID, StatusIdentified, "found it", "op:1")
	_, _ = svc.Update(ctx, inc.ID, StatusResolved, "fixed", "op:1")
	tl, err := svc.Timeline(ctx, inc.ID)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if len(tl) < 2 {
		t.Fatalf("expected >=2 timeline events, got %d", len(tl))
	}
}

func TestIncident_PublicStatus_Redaction(t *testing.T) {
	svc, ctx := newTestService(t)
	_, _ = svc.Create(ctx, "Outage", "internal note with secret token=abc123", SevCritical, []string{"mailbox"}, []string{"us-east"}, nil)
	st, err := svc.PublicStatus(ctx)
	if err != nil {
		t.Fatalf("public status: %v", err)
	}
	if st.Overall != "outage" {
		t.Fatalf("expected outage, got %s", st.Overall)
	}
	for _, inc := range st.Incidents {
		if inc.LastUpdate != "" {
			t.Fatalf("public incident leaked internal detail: %+v", inc)
		}
	}
}

func TestIncident_ConcurrentUpdate_Conflict(t *testing.T) {
	ctx := context.Background()
	db := newSQLiteTestDB(t)
	repo := NewRepository(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	inc := &Incident{Title: "Test", Severity: SevMinor}
	if err := repo.Insert(ctx, inc); err != nil {
		t.Fatalf("insert: %v", err)
	}
	copy1, _ := repo.Get(ctx, inc.ID)
	copy2, _ := repo.Get(ctx, inc.ID)
	copy1.Status = StatusIdentified
	if err := repo.Update(ctx, copy1); err != nil {
		t.Fatalf("first update: %v", err)
	}
	copy2.Status = StatusMonitoring
	if err := repo.Update(ctx, copy2); err == nil {
		t.Fatal("expected stale version error when updating from stale base")
	}
}

func TestIncident_List_FilterAndPagination(t *testing.T) {
	svc, ctx := newTestService(t)
	for i := 0; i < 5; i++ {
		_, _ = svc.Create(ctx, "Active", "desc", SevMinor, nil, nil, nil)
	}
	resolved, _ := svc.Create(ctx, "Done", "desc", SevMinor, nil, nil, nil)
	_, _ = svc.Update(ctx, resolved.ID, StatusResolved, "fixed", "op:1")
	active, err := svc.List(ctx, "investigating", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(active) != 5 {
		t.Fatalf("expected 5 investigating, got %d", len(active))
	}
	all, err := svc.List(ctx, "", 100)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 6 {
		t.Fatalf("expected 6 total, got %d", len(all))
	}
}
