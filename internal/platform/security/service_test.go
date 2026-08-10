package security

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestService(t *testing.T) (*sql.DB, *Service) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	repo := NewRepository(db)
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return db, NewService(repo, nil)
}

func TestRecordEvent_RejectsInvalidCategory(t *testing.T) {
	_, svc := newTestService(t)
	_, err := svc.RecordEvent(context.Background(), 1, "not-a-real-category", SeverityInfo, "test", "", "detail")
	if err != ErrInvalidCategory {
		t.Fatalf("expected ErrInvalidCategory, got %v", err)
	}
}

func TestRecordEvent_DefaultsSeverityToInfo(t *testing.T) {
	_, svc := newTestService(t)
	e, err := svc.RecordEvent(context.Background(), 1, CategoryMalware, "", "antivirus", "", "eicar detected")
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if e.Severity != SeverityInfo {
		t.Fatalf("expected default severity info, got %s", e.Severity)
	}
}

func TestRecordEvent_RedactsCredentialLikeDetail(t *testing.T) {
	_, svc := newTestService(t)
	e, err := svc.RecordEvent(context.Background(), 1, CategoryAuthAbuse, SeverityWarning, "auth", "203.0.113.5", "login attempt password=hunter2 from suspicious IP")
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if strings.Contains(e.Detail, "hunter2") {
		t.Fatalf("expected the credential-like fragment to be redacted, got %q", e.Detail)
	}
	if !strings.Contains(e.Detail, "[redacted]") {
		t.Fatalf("expected a [redacted] marker, got %q", e.Detail)
	}
}

func TestRecordEvent_TruncatesOverlongDetail(t *testing.T) {
	_, svc := newTestService(t)
	huge := strings.Repeat("a", MaxDetailLength*2)
	e, err := svc.RecordEvent(context.Background(), 1, CategoryPolicyViolation, SeverityInfo, "acl", "", huge)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if len(e.Detail) > MaxDetailLength+3 { // "…" is a 3-byte UTF-8 rune, appended after truncation
		t.Fatalf("expected detail truncated to ~%d chars, got %d", MaxDetailLength, len(e.Detail))
	}
}

func TestListEvents_CursorPaginationNoOverlap(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		svc.RecordEvent(ctx, 1, CategorySpam, SeverityInfo, "antispam", "", "spam scored high")
	}
	page1, err := svc.ListEvents(ctx, ListFilter{Limit: 2})
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("expected 2 results, got %d", len(page1))
	}
	page2, err := svc.ListEvents(ctx, ListFilter{AfterID: page1[1].ID, Limit: 2})
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if len(page2) != 2 || page2[0].ID <= page1[1].ID {
		t.Fatalf("expected page 2 to continue strictly after page 1, got %+v", page2)
	}
}

func TestListEvents_TenantIsolation(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	svc.RecordEvent(ctx, 1, CategoryMalware, SeverityCritical, "antivirus", "", "eicar")
	svc.RecordEvent(ctx, 2, CategoryMalware, SeverityCritical, "antivirus", "", "eicar")

	t1Events, err := svc.ListEvents(ctx, ListFilter{TenantID: 1})
	if err != nil {
		t.Fatalf("list tenant 1: %v", err)
	}
	if len(t1Events) != 1 {
		t.Fatalf("expected tenant 1 to see only its own event, got %d", len(t1Events))
	}
}

func TestListEvents_CategoryAndSeverityFilters(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	svc.RecordEvent(ctx, 1, CategoryMalware, SeverityCritical, "antivirus", "", "a")
	svc.RecordEvent(ctx, 1, CategorySpam, SeverityInfo, "antispam", "", "b")
	svc.RecordEvent(ctx, 1, CategoryMalware, SeverityWarning, "antivirus", "", "c")

	byCategory, _ := svc.ListEvents(ctx, ListFilter{Category: CategoryMalware})
	if len(byCategory) != 2 {
		t.Fatalf("expected 2 malware events, got %d", len(byCategory))
	}
	bySeverity, _ := svc.ListEvents(ctx, ListFilter{Severity: SeverityCritical})
	if len(bySeverity) != 1 {
		t.Fatalf("expected 1 critical event, got %d", len(bySeverity))
	}
}

func TestPurgeRetention_DeletesOnlyOlderThanCutoffInBoundedBatches(t *testing.T) {
	db, svc := newTestService(t)
	ctx := context.Background()

	old := time.Now().UTC().Add(-48 * time.Hour)
	for i := 0; i < 3; i++ {
		if _, err := db.Exec(`INSERT INTO platform_security_events (tenant_id, category, severity, source_system, actor, detail, created_at) VALUES (1, 'malware', 'info', 'x', '', 'old', ?)`, old); err != nil {
			t.Fatal(err)
		}
	}
	svc.RecordEvent(ctx, 1, CategoryMalware, SeverityInfo, "x", "", "recent")

	deleted, err := svc.PurgeRetention(ctx, 24*time.Hour, 10)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("expected exactly 3 old events purged, got %d", deleted)
	}
	remaining, _ := svc.ListEvents(ctx, ListFilter{})
	if len(remaining) != 1 || remaining[0].Detail != "recent" {
		t.Fatalf("expected only the recent event to remain, got %+v", remaining)
	}
}

func TestPurgeRetention_NoStaleEventsIsNoOp(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	svc.RecordEvent(ctx, 1, CategoryMalware, SeverityInfo, "x", "", "recent")
	deleted, err := svc.PurgeRetention(ctx, 24*time.Hour, 10)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("expected 0 deleted when nothing is stale, got %d", deleted)
	}
}
