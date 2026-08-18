package importer

// Phase 8 C2: domain operability enforcement for the importer's alias
// adapter (prodAliasAdapter.CreateAlias in adapters_production.go),
// which does a raw dialect-portable INSERT and previously had zero
// domain-status checking at all -- unlike prodMailboxAdapter, which
// already goes through the C1-guarded mailbox.Service.

import (
	"context"
	"sync"
	"testing"

	"github.com/orvix/orvix/internal/dbdialect"
)

func TestImporterCreateAlias_DisabledDomainRejected(t *testing.T) {
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

	if _, err := db.Exec(
		`INSERT INTO coremail_domains (tenant_id, name, status) VALUES (`+
			dialect.Placeholder(1)+`, `+dialect.Placeholder(2)+`, `+dialect.Placeholder(3)+`)`,
		9, "importer-disabled.test", "disabled"); err != nil {
		t.Fatal(err)
	}

	if _, err := adapters.Alias.CreateAlias(ctx, "team@importer-disabled.test", "boss@importer-disabled.test", 9, 0); err == nil {
		t.Fatal("expected CreateAlias to reject a disabled domain")
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_aliases WHERE from_addr = `+dialect.Placeholder(1), "team@importer-disabled.test").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected 0 alias rows for a rejected domain, got %d", count)
	}
}

func TestImporterCreateAlias_ActiveDomainSucceeds(t *testing.T) {
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

	if _, err := db.Exec(
		`INSERT INTO coremail_domains (tenant_id, name, status) VALUES (`+
			dialect.Placeholder(1)+`, `+dialect.Placeholder(2)+`, `+dialect.Placeholder(3)+`)`,
		9, "importer-active.test", "active"); err != nil {
		t.Fatal(err)
	}

	id, err := adapters.Alias.CreateAlias(ctx, "team@importer-active.test", "boss@importer-active.test", 9, 0)
	if err != nil {
		t.Fatalf("expected an active domain to succeed, got: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero alias id")
	}
}

// TestImporterCreateAlias_ConcurrentDisableCannotWinAfterCommit proves
// the C2-R1 atomicity fix: the domain-operability check and the alias
// insert now run inside ONE transaction (previously they were two
// separate operations with a TOCTOU window between them). A disable
// racing against CreateAlias must produce an internally consistent
// result — if CreateAlias reports success, the alias row exists AND
// the domain was active at commit time; it can never be the case that
// a disable "wins" after CreateAlias has already committed the insert.
func TestImporterCreateAlias_ConcurrentDisableCannotWinAfterCommit(t *testing.T) {
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

	if _, err := db.Exec(
		`INSERT INTO coremail_domains (tenant_id, name, status) VALUES (`+
			dialect.Placeholder(1)+`, `+dialect.Placeholder(2)+`, `+dialect.Placeholder(3)+`)`,
		9, "importer-concurrent.test", "active"); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var createErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, createErr = adapters.Alias.CreateAlias(ctx, "team@importer-concurrent.test", "boss@importer-concurrent.test", 9, 0)
	}()
	go func() {
		defer wg.Done()
		_, _ = db.Exec(dialect.Rewrite(`UPDATE coremail_domains SET status = 'disabled' WHERE name = ?`), "importer-concurrent.test")
	}()
	wg.Wait()

	var aliasCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_aliases WHERE from_addr = `+dialect.Placeholder(1), "team@importer-concurrent.test").Scan(&aliasCount); err != nil {
		t.Fatal(err)
	}
	if createErr == nil && aliasCount != 1 {
		t.Fatalf("CreateAlias reported success but alias row count = %d, want 1", aliasCount)
	}
	if createErr != nil && aliasCount != 0 {
		t.Fatalf("CreateAlias reported rejection (%v) but alias row count = %d, want 0 — the check and insert are not atomic", createErr, aliasCount)
	}
}
