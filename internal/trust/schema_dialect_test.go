package trust

import (
	"strings"
	"testing"

	"github.com/orvix/orvix/internal/dbdialect"
)

// TestTablesForDialect_PostgresHasNoSQLiteOnlySyntax guards the HIGH finding:
// the trust schema used to emit `INTEGER PRIMARY KEY AUTOINCREMENT` and
// `DATETIME` to the primary DB. On the PostgreSQL control plane that is a
// parse-time syntax error near "AUTOINCREMENT" that was logged and swallowed
// ("trust schema migration failed, falling back to in-memory").
func TestTablesForDialect_PostgresHasNoSQLiteOnlySyntax(t *testing.T) {
	for _, ddl := range TablesForDialect(dbdialect.FromDriver("postgres")) {
		up := strings.ToUpper(ddl)
		if strings.Contains(up, "AUTOINCREMENT") {
			t.Fatalf("postgres trust DDL must not contain AUTOINCREMENT:\n%s", ddl)
		}
		if strings.Contains(up, "DATETIME") {
			t.Fatalf("postgres trust DDL must not contain DATETIME:\n%s", ddl)
		}
	}
	joined := strings.Join(TablesForDialect(dbdialect.FromDriver("postgres")), "\n")
	if !strings.Contains(joined, "BIGSERIAL PRIMARY KEY") {
		t.Fatalf("postgres trust DDL must use BIGSERIAL PRIMARY KEY:\n%s", joined)
	}

	// SQLite retains its native shapes.
	sq := strings.Join(TablesForDialect(dbdialect.FromDriver("sqlite")), "\n")
	if !strings.Contains(sq, "INTEGER PRIMARY KEY AUTOINCREMENT") {
		t.Fatalf("sqlite trust DDL must retain INTEGER PRIMARY KEY AUTOINCREMENT:\n%s", sq)
	}
	if !strings.Contains(strings.ToUpper(sq), "DATETIME") {
		t.Fatalf("sqlite trust DDL must retain DATETIME:\n%s", sq)
	}
}
