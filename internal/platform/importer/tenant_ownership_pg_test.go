package importer

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/platform/kernel"
)

// H-7 tenant-ownership invariant, executed against REAL PostgreSQL.
//
// The SQLite coverage lives in tenant_ownership_test.go. That suite runs on
// the legacy in-package test doubles, which are SQLite-only (raw `?`
// placeholders and LastInsertId), so it cannot simply be pointed at
// PostgreSQL. This file re-proves the invariant on PostgreSQL using the
// dialect-aware repository and portable SQL only.
//
// It is gated on PGHOST + ORVIX_RUN_POSTGRES_DML_TEST=1 (the repository
// convention), so a plain SQLite `go test ./...` skips it and the PostgreSQL
// Runtime DML workflow executes it against a real server.

func newImporterPGService(t *testing.T) (*Service, *dbdialect.Info) {
	t.Helper()
	if os.Getenv("PGHOST") == "" || os.Getenv("ORVIX_RUN_POSTGRES_DML_TEST") != "1" {
		t.Skip("postgres importer: set PGHOST and ORVIX_RUN_POSTGRES_DML_TEST=1 to run")
	}
	// newImporterTestDB creates an isolated PostgreSQL schema and drops it on
	// cleanup, so this never touches another suite's data.
	db := newImporterTestDB(t)
	d, err := dbdialect.Detect(db)
	if err != nil {
		t.Fatalf("detect dialect: %v", err)
	}
	if !d.IsPostgres() {
		t.Fatalf("expected a PostgreSQL handle, got %v", d.Dialect)
	}

	// The importer's own schema is created by the dialect-aware repository —
	// this exercises PostgreSQL schema creation and additive migration.
	repo := NewRepository(db)
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure importer schema on postgres: %v", err)
	}
	// Idempotent re-run: additive migration must be safe to repeat.
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure importer schema twice on postgres: %v", err)
	}

	// Ambient tenant-owned tables the orphan diagnostic scans, created with
	// PostgreSQL-native types.
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS tenants (id BIGSERIAL PRIMARY KEY, name TEXT, domain TEXT, plan TEXT, active INTEGER, created_at TIMESTAMP, updated_at TIMESTAMP, deleted_at TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS users (id BIGSERIAL PRIMARY KEY, tenant_id INTEGER, email TEXT, full_name TEXT, password_hash TEXT, role TEXT, created_at TIMESTAMP, updated_at TIMESTAMP, deleted_at TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS coremail_domains (id BIGSERIAL PRIMARY KEY, tenant_id INTEGER, name TEXT, status TEXT, created_at TIMESTAMP, updated_at TIMESTAMP, deleted_at TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS coremail_mailboxes (id BIGSERIAL PRIMARY KEY, domain_id INTEGER, tenant_id INTEGER, email TEXT, created_at TIMESTAMP, updated_at TIMESTAMP, deleted_at TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS coremail_aliases (id BIGSERIAL PRIMARY KEY, domain_id INTEGER, tenant_id INTEGER, from_addr TEXT, to_addr TEXT, created_at TIMESTAMP, updated_at TIMESTAMP, deleted_at TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS coremail_groups (id BIGSERIAL PRIMARY KEY, tenant_id INTEGER, name TEXT, description TEXT, created_at TIMESTAMP, updated_at TIMESTAMP, deleted_at TIMESTAMP)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("create ambient table on postgres: %v", err)
		}
	}

	staging, err := NewStagingService(t.TempDir())
	if err != nil {
		t.Fatalf("staging: %v", err)
	}
	// Adapters are not invoked by these tests (nothing executes); the service
	// requires them to be present, and the production adapters are the ones
	// whose PostgreSQL behaviour TestProductionAdaptersPortableInserts covers.
	adapters, err := NewProductionAdaptersFromDB(db, d)
	if err != nil {
		t.Fatalf("production adapters on postgres: %v", err)
	}
	return NewService(repo, adapters, staging, nil, nil), d
}

const pgImportCSV = "entity,name,domain\norganization,Acme,acme.test\n"

// TestPostgresImport_RejectsZeroTenantBeforeStaging proves the H-7 entry-point
// guard holds on PostgreSQL and leaves no job row behind.
func TestPostgresImport_RejectsZeroTenantBeforeStaging(t *testing.T) {
	svc, _ := newImporterPGService(t)
	ctx := context.Background()

	_, err := svc.Create(ctx, CreateImportParams{
		TenantID: 0, Scope: "platform", Actor: "psa", SourceType: SourceCSV, SourceName: "x.csv",
	}, []byte(pgImportCSV))
	if err == nil {
		t.Fatal("platform import without a target tenant must be rejected on postgres")
	}
	var kerr *kernel.Error
	if !errors.As(err, &kerr) {
		t.Fatalf("expected a typed kernel error, got %T: %v", err, err)
	}
	if _, ok := kerr.Fields["target_tenant_id"]; !ok {
		t.Fatalf("validation error must name target_tenant_id, got %v", kerr.Fields)
	}
}

// TestPostgresImport_AcceptsExplicitTargetTenant proves creation with an
// explicit target persists that tenant on PostgreSQL (RETURNING-id insert
// path, no LastInsertId).
func TestPostgresImport_AcceptsExplicitTargetTenant(t *testing.T) {
	svc, _ := newImporterPGService(t)
	ctx := context.Background()

	job, err := svc.Create(ctx, CreateImportParams{
		TenantID: 7, Scope: "platform", Actor: "psa", SourceType: SourceCSV, SourceName: "x.csv",
	}, []byte(pgImportCSV))
	if err != nil {
		t.Fatalf("create with explicit target tenant on postgres: %v", err)
	}
	if job.TenantID != 7 {
		t.Fatalf("job must record the explicit target tenant, got %d", job.TenantID)
	}
	if job.ID == 0 {
		t.Fatal("postgres insert must return a real generated id (RETURNING id), got 0")
	}
}

// TestPostgresImport_TargetTenantImmutableAndCrossTenantDenied proves the
// stored owner is authoritative on PostgreSQL: a foreign tenant cannot reach
// the job, and the owning tenant reads back the same target.
func TestPostgresImport_TargetTenantImmutableAndCrossTenantDenied(t *testing.T) {
	svc, _ := newImporterPGService(t)
	ctx := context.Background()

	job, err := svc.Create(ctx, CreateImportParams{
		TenantID: 9, Scope: "platform", Actor: "psa", SourceType: SourceCSV, SourceName: "x.csv",
	}, []byte(pgImportCSV))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := svc.Get(ctx, job.ID, 9999, "platform"); err == nil {
		t.Fatal("a foreign tenant must not read another tenant's import job on postgres")
	}
	reloaded, err := svc.Get(ctx, job.ID, 9, "platform")
	if err != nil {
		t.Fatalf("get with the owning tenant on postgres: %v", err)
	}
	if reloaded.TenantID != 9 {
		t.Fatalf("stored target tenant changed on postgres: %d", reloaded.TenantID)
	}
	// Tenant-scoped access to a platform job must also be refused.
	if _, err := svc.Get(ctx, job.ID, 9, "tenant"); err == nil {
		t.Fatal("a platform job must not be readable through tenant scope")
	}
}

// TestPostgresImport_AdaptersRejectTenantZero re-proves the adapter choke
// point with REAL PostgreSQL-backed production adapters, so the guard is
// confirmed on the dialect that actually ships.
func TestPostgresImport_AdaptersRejectTenantZero(t *testing.T) {
	svc, d := newImporterPGService(t)
	_ = svc
	db := newImporterTestDB(t)
	adapters, err := NewProductionAdaptersFromDB(db, d)
	if err != nil {
		t.Fatalf("adapters: %v", err)
	}
	ctx := context.Background()

	if _, err := adapters.Admin.CreateTenantAdmin(ctx, "a@b.test", "n", "StrongPass!123", "tenant_admin", 0); err == nil {
		t.Fatal("CreateTenantAdmin must reject tenant 0 on postgres")
	} else {
		var ie *ImportError
		if !errors.As(err, &ie) || ie.Code != CodeTenantRequired {
			t.Fatalf("expected %s, got %v", CodeTenantRequired, err)
		}
	}
	if _, err := adapters.Domain.CreateDomain(ctx, "d.test", 0); err == nil {
		t.Fatal("CreateDomain must reject tenant 0 on postgres")
	}
	if _, err := adapters.Group.CreateGroup(ctx, "g", "desc", 0); err == nil {
		t.Fatal("CreateGroup must reject tenant 0 on postgres")
	}
	if err := adapters.Group.AddGroupMember(ctx, "g", "m@d.test", 0); err == nil {
		t.Fatal("AddGroupMember must reject tenant 0 on postgres")
	}
}

// TestPostgresImport_OrphanDiagnosticReportsWithoutMutating proves the
// read-only diagnostic works on PostgreSQL and changes nothing.
func TestPostgresImport_OrphanDiagnosticReportsWithoutMutating(t *testing.T) {
	svc, d := newImporterPGService(t)
	_ = svc
	db := newImporterTestDB(t)
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS coremail_domains (id BIGSERIAL PRIMARY KEY, tenant_id INTEGER, name TEXT, status TEXT, created_at TIMESTAMP, updated_at TIMESTAMP, deleted_at TIMESTAMP)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("ddl: %v", err)
		}
	}
	if _, err := db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status) VALUES (5,'ok.test','active')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO coremail_domains (tenant_id, name, status) VALUES (0,'orphan.test','active')`); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	var before int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_domains`).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}

	report, err := ScanTenantZeroOrphans(context.Background(), db, d)
	if err != nil {
		t.Fatalf("scan on postgres: %v", err)
	}
	if report.Total < 1 {
		t.Fatalf("expected the seeded orphan to be reported, got %+v", report)
	}

	var after, stillOrphan int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_domains`).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_domains WHERE tenant_id = 0`).Scan(&stillOrphan); err != nil {
		t.Fatalf("orphan recount: %v", err)
	}
	if after != before || stillOrphan != 1 {
		t.Fatalf("diagnostic mutated data on postgres: before=%d after=%d orphans=%d", before, after, stillOrphan)
	}
}
