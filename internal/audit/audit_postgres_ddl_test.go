package audit

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/orvix/orvix/internal/dbdialect"

	_ "modernc.org/sqlite"
)

func openSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("%s/audit_ddl_test.db", t.TempDir()))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestCoremailAuditDDL_PostgresHasNoSQLiteOnlySyntax guards the HIGH
// finding: audit.Store.EnsureTable used to emit `INTEGER PRIMARY KEY
// AUTOINCREMENT` and `DATETIME` to the primary DB. On the PostgreSQL
// control plane that is a parse-time syntax error near "AUTOINCREMENT",
// which was logged and swallowed so staging stayed green. The DDL must
// now be dialect-correct on both engines.
func TestCoremailAuditDDL_PostgresHasNoSQLiteOnlySyntax(t *testing.T) {
	pg := coremailAuditDDL(dbdialect.FromDriver("postgres"))
	if strings.Contains(strings.ToUpper(pg), "AUTOINCREMENT") {
		t.Fatalf("postgres audit DDL must not contain AUTOINCREMENT:\n%s", pg)
	}
	if !strings.Contains(pg, "BIGSERIAL PRIMARY KEY") {
		t.Fatalf("postgres audit DDL must use BIGSERIAL PRIMARY KEY:\n%s", pg)
	}
	if !strings.Contains(pg, "timestamp TIMESTAMP NOT NULL") {
		t.Fatalf("postgres audit DDL must use TIMESTAMP for timestamp column:\n%s", pg)
	}

	sq := coremailAuditDDL(dbdialect.FromDriver("sqlite"))
	if !strings.Contains(sq, "INTEGER PRIMARY KEY AUTOINCREMENT") {
		t.Fatalf("sqlite audit DDL must retain INTEGER PRIMARY KEY AUTOINCREMENT:\n%s", sq)
	}
}

// TestStoreEnsureTable_SQLiteRoundTrip proves the SQLite path still
// creates a working table and can record/search entries.
func TestStoreEnsureTable_SQLiteRoundTrip(t *testing.T) {
	db := openSQLite(t)
	s := NewStore(db)
	if s.dialect.IsPostgres() {
		t.Fatal("in-memory sqlite must detect as sqlite dialect")
	}
	ctx := context.Background()
	if err := s.EnsureTable(ctx); err != nil {
		t.Fatalf("EnsureTable(sqlite): %v", err)
	}
	// Idempotent.
	if err := s.EnsureTable(ctx); err != nil {
		t.Fatalf("EnsureTable second call: %v", err)
	}
	if err := s.Record(ctx, &Entry{Actor: "user:1", Action: "test.action", Result: "ok"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	entries, total, err := s.Search(ctx, &Query{Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if total != 1 || len(entries) != 1 || entries[0].Action != "test.action" {
		t.Fatalf("unexpected search result: total=%d entries=%+v", total, entries)
	}
}
