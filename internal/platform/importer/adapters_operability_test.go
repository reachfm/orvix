package importer

// Phase 8 C2: domain operability enforcement for the importer's alias
// adapter (prodAliasAdapter.CreateAlias in adapters_production.go),
// which does a raw dialect-portable INSERT and previously had zero
// domain-status checking at all -- unlike prodMailboxAdapter, which
// already goes through the C1-guarded mailbox.Service.

import (
	"context"
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
