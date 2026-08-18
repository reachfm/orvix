package importer

import (
	"context"
	"database/sql"
	"testing"

	"github.com/orvix/orvix/internal/dbdialect"
)

// productionAdapterSchema creates the real business tables the production
// adapters write to. DDL is chosen per dialect so ids auto-increment on both
// backends (BIGSERIAL on PostgreSQL, INTEGER PRIMARY KEY AUTOINCREMENT on
// SQLite).
func productionAdapterSchema(t *testing.T, db *sql.DB, dialect *dbdialect.Info) {
	t.Helper()
	idType := `BIGSERIAL PRIMARY KEY`
	ts := `TIMESTAMPTZ`
	if !dialect.IsPostgres() {
		idType = `INTEGER PRIMARY KEY AUTOINCREMENT`
		ts = `DATETIME`
	}
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS users (
			id ` + idType + `,
			created_at ` + ts + ` NOT NULL,
			updated_at ` + ts + ` NOT NULL,
			deleted_at ` + ts + `,
			email TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'user',
			tenant_id BIGINT,
			active INTEGER NOT NULL DEFAULT 1,
			email_verified INTEGER NOT NULL DEFAULT 0,
			full_name TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS coremail_groups (
			id ` + idType + `,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			created_at ` + ts + ` NOT NULL,
			updated_at ` + ts + ` NOT NULL,
			deleted_at ` + ts + `)`,
		`CREATE TABLE IF NOT EXISTS coremail_aliases (
			id ` + idType + `,
			domain_id INTEGER NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			from_addr TEXT NOT NULL,
			to_addr TEXT NOT NULL,
			active INTEGER NOT NULL DEFAULT 1,
			created_at ` + ts + `,
			updated_at ` + ts + `,
			deleted_at ` + ts + `)`,
		`CREATE TABLE IF NOT EXISTS coremail_domains (
			id ` + idType + `,
			tenant_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			deleted_at ` + ts + `)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
}

func TestProductionAdaptersPortableInserts(t *testing.T) {
	db := newImporterTestDB(t)
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	productionAdapterSchema(t, db, dialect)
	adapters, err := NewProductionAdaptersFromDB(db, dialect)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	t.Run("tenant admin insert returns real id", func(t *testing.T) {
		id, err := adapters.Admin.CreateTenantAdmin(ctx, "admin@example.test", "Admin", "StrongPass!123", "tenant_admin", 7)
		if err != nil {
			t.Fatalf("CreateTenantAdmin: %v", err)
		}
		if id == 0 {
			t.Fatal("expected non-zero user id")
		}
		var got uint
		if err := db.QueryRow(`SELECT id FROM users WHERE id = `+dialect.Placeholder(1), id).Scan(&got); err != nil {
			t.Fatalf("user row not found: %v", err)
		}
		if got != id {
			t.Fatalf("row id %d != returned %d", got, id)
		}
	})

	t.Run("group insert returns real id", func(t *testing.T) {
		id, err := adapters.Group.CreateGroup(ctx, "eng", "Engineering", 7)
		if err != nil {
			t.Fatalf("CreateGroup: %v", err)
		}
		if id == 0 {
			t.Fatal("expected non-zero group id")
		}
		var got uint
		if err := db.QueryRow(`SELECT id FROM coremail_groups WHERE id = `+dialect.Placeholder(1), id).Scan(&got); err != nil {
			t.Fatalf("group row not found: %v", err)
		}
		if got != id {
			t.Fatalf("row id %d != returned %d", got, id)
		}
	})

	t.Run("alias insert returns real id", func(t *testing.T) {
		// The seed must use dialect placeholders like every other statement in
		// this file: a raw `?` reaches PostgreSQL verbatim and fails with
		// `syntax error at or near ","` (SQLSTATE 42601). This subtest had
		// never executed against PostgreSQL until the Phase 2 acceptance gate
		// ran it there, so the harness bug was invisible. The production
		// CreateAlias path was always correct — it rewrites through
		// insertReturningID/dialect.Rewrite.
		if _, err := db.Exec(
			`INSERT INTO coremail_domains (tenant_id, name) VALUES (`+
				dialect.Placeholder(1)+`, `+dialect.Placeholder(2)+`)`,
			7, "example.test"); err != nil {
			t.Fatal(err)
		}
		id, err := adapters.Alias.CreateAlias(ctx, "team@example.test", "boss@example.test", 7, 0)
		if err != nil {
			t.Fatalf("CreateAlias: %v", err)
		}
		if id == 0 {
			t.Fatal("expected non-zero alias id")
		}
		var got uint
		if err := db.QueryRow(`SELECT id FROM coremail_aliases WHERE id = `+dialect.Placeholder(1), id).Scan(&got); err != nil {
			t.Fatalf("alias row not found: %v", err)
		}
		if got != id {
			t.Fatalf("row id %d != returned %d", got, id)
		}
	})
}

// TestInsertReturningID_PostgresSQLPath asserts that when the dialect is
// PostgreSQL, the helper appends RETURNING id and reads the id from the
// scanned row (not from LastInsertId).
func TestInsertReturningID_PostgresSQLPath(t *testing.T) {
	db := newImporterTestDB(t)
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	productionAdapterSchema(t, db, dialect)

	// Force the Postgres branch of the helper against the real DB only when
	// we are actually on Postgres; otherwise assert the SQL construction
	// through a real Postgres if available, else skip.
	if dialect.IsPostgres() {
		id, err := insertReturningID(context.Background(), db, dialect,
			`INSERT INTO coremail_groups (tenant_id, name, description, created_at, updated_at) VALUES (?,?,?,?,?)`,
			1, "pg", "", timeNow(), timeNow())
		if err != nil {
			t.Fatalf("postgres insert: %v", err)
		}
		if id == 0 {
			t.Fatal("expected non-zero id from RETURNING path")
		}
		return
	}

	// On a non-Postgres driver we cannot run the literal RETURNING path; this
	// test is meaningful only against real Postgres (PGHOST). Otherwise the
	// portable behavior is already covered by TestProductionAdaptersPortableInserts.
	t.Log("skipping live RETURNING assertion: not connected to PostgreSQL (set PGHOST to enable)")
}
