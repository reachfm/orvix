package audit

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSanitizeJSONRedactsNestedSecretsCaseInsensitively(t *testing.T) {
	input := `{"outer":{"Password":"hunter2","items":[{"SESSION_TOKEN":"secret-token"}]},"safe":"visible"}`
	got := sanitizeJSON(input, sensitiveFields)
	for _, secret := range []string{"hunter2", "secret-token"} {
		if strings.Contains(got, secret) {
			t.Fatalf("sanitized audit metadata leaked %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, `"safe":"visible"`) {
		t.Fatalf("sanitizer removed safe metadata: %s", got)
	}
}

func TestSanitizeJSONMalformedInputFailsClosed(t *testing.T) {
	input := `{"password":"secret"`
	got := sanitizeJSON(input, sensitiveFields)
	if strings.Contains(got, "secret") || strings.Contains(got, input) {
		t.Fatalf("malformed audit metadata leaked input: %s", got)
	}
	if got != `"[REDACTED: invalid audit metadata]"` {
		t.Fatalf("unexpected malformed metadata marker: %s", got)
	}
}

func TestExtendedStoreEnsureTableSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	store := NewExtendedStore(db)
	ctx := context.Background()

	// Must not return a syntax error on SQLite.
	if err := store.EnsureTable(ctx); err != nil {
		t.Fatalf("EnsureTable on sqlite: %v", err)
	}

	// Verify the table and expected columns exist.
	for _, col := range []string{
		"id", "actor", "actor_id", "actor_role", "tenant_id",
		"action", "target", "target_id", "result", "reason",
		"before", "after", "request_id", "ip", "user_agent", "timestamp",
	} {
		var name string
		if err := db.QueryRow(
			"SELECT name FROM pragma_table_info('orvix_audit') WHERE name=?",
			col,
		).Scan(&name); err != nil {
			t.Fatalf("column %q not found: %v", col, err)
		}
	}

	// Insert a record and read it back.
	e := &ExtendedEntry{
		Actor:     "user:1",
		ActorID:   1,
		ActorRole: "admin",
		TenantID:  1,
		Action:    "test.create",
		Target:    "domain:example.com",
		Result:    "success",
		IP:        "127.0.0.1",
		Timestamp: time.Now().UTC(),
	}
	if err := store.Record(ctx, e); err != nil {
		t.Fatalf("record: %v", err)
	}

	// Read it back via Search (Record does not populate e.ID).
	entries, total, err := store.Search(ctx, &ExtendedQuery{Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected 1 entry, got %d", total)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].ID == 0 {
		t.Fatal("expected auto-generated id")
	}
	if entries[0].Action != "test.create" {
		t.Fatalf("unexpected action: %s", entries[0].Action)
	}
}
