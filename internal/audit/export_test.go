package audit

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newAuditTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "audit.db")+"?_txlock=immediate")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	s := NewStore(db)
	if err := s.EnsureTable(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if err := s.EnsureTenantColumn(ctx); err != nil {
		t.Fatalf("tenant col: %v", err)
	}
	return s, ctx
}

func TestAuditExportJSON(t *testing.T) {
	s, ctx := newAuditTestStore(t)
	_ = s.Record(ctx, &Entry{Actor: "user:1", Action: "test.action", Target: "res:1", Result: "success", TenantID: 1})
	_ = s.Record(ctx, &Entry{Actor: "user:2", Action: "test.other", Target: "res:2", Result: "success", TenantID: 1})
	var buf bytes.Buffer
	if err := s.ExportTo(ctx, &Query{TenantID: 1}, ExportJSON, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	if !strings.Contains(buf.String(), "test.action") {
		t.Fatalf("expected test.action in export, got: %s", buf.String())
	}
	if strings.Contains(buf.String(), "password") {
		t.Fatal("export should not contain password")
	}
}

func TestAuditExportCSV(t *testing.T) {
	s, ctx := newAuditTestStore(t)
	_ = s.Record(ctx, &Entry{Actor: "user:1", Action: "test.action", Target: "res:1", Result: "success", TenantID: 1})
	var buf bytes.Buffer
	if err := s.ExportTo(ctx, &Query{TenantID: 1}, ExportCSV, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	if !strings.Contains(buf.String(), "timestamp") {
		t.Fatal("CSV should have header")
	}
	if !strings.Contains(buf.String(), "test.action") {
		t.Fatalf("CSV should contain action, got: %s", buf.String())
	}
}

func TestAuditGetEntry(t *testing.T) {
	s, ctx := newAuditTestStore(t)
	s.Record(ctx, &Entry{Actor: "user:1", Action: "test", Target: "res", Result: "success", TenantID: 1})
	entries, _, _ := s.Search(ctx, &Query{TenantID: 1})
	if len(entries) == 0 {
		t.Fatal("expected entry")
	}
	entry, err := s.GetEntry(ctx, entries[0].ID)
	if err != nil {
		t.Fatalf("get entry: %v", err)
	}
	if entry.Action != "test" {
		t.Fatalf("expected action 'test', got %s", entry.Action)
	}
}

func TestAuditRetention(t *testing.T) {
	t0 := time.Date(2025, time.January, 2, 15, 4, 5, 123456789, time.UTC)
	fixedZone := time.FixedZone("UTC-07", -7*60*60)

	testCases := []struct {
		name   string
		cutoff time.Time
		want   int64
	}{
		{name: "utc before", cutoff: t0.Add(-time.Hour), want: 0},
		{name: "utc after", cutoff: t0.Add(time.Hour), want: 1},
		{name: "fixed zone before", cutoff: t0.Add(-time.Hour).In(fixedZone), want: 0},
		{name: "fixed zone after", cutoff: t0.Add(time.Hour).In(fixedZone), want: 1},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s, ctx := newAuditTestStore(t)
			if err := s.Record(ctx, &Entry{
				Actor:     "user:1",
				Action:    "fixed.timestamp",
				Result:    "success",
				TenantID:  1,
				Timestamp: t0,
			}); err != nil {
				t.Fatalf("record: %v", err)
			}

			var stored string
			var sqliteType string
			if err := s.db.QueryRowContext(ctx,
				"SELECT timestamp, typeof(timestamp) FROM coremail_audit WHERE action = ?",
				"fixed.timestamp").Scan(&stored, &sqliteType); err != nil {
				t.Fatalf("inspect stored timestamp: %v", err)
			}
			if sqliteType != "text" {
				t.Fatalf("expected SQLite timestamp storage type text, got %q (%q)", sqliteType, stored)
			}
			if stored == "" {
				t.Fatal("stored timestamp representation is empty")
			}
			t.Logf("SQLite timestamp representation=%q type=%q; cutoff=%s normalized=%s", stored, sqliteType, tc.cutoff, tc.cutoff.UTC())

			deleted, err := s.PurgeOlderThan(ctx, tc.cutoff)
			if err != nil {
				t.Fatalf("purge: %v", err)
			}
			if deleted != tc.want {
				t.Fatalf("expected %d deleted, got %d; stored timestamp=%q", tc.want, deleted, stored)
			}
		})
	}
}
