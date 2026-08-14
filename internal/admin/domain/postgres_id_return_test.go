package domain

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/coremail/dkim"
	"github.com/orvix/orvix/internal/models"
)

// openDomainPG opens a fresh PostgreSQL schema for the domain
// repository's ID-return path. Gated on PGHOST +
// ORVIX_RUN_POSTGRES_DML_TEST=1 like every other PostgreSQL suite; run
// against the disposable container with a dedicated database.
func openDomainPG(t *testing.T) *sql.DB {
	t.Helper()
	host := os.Getenv("PGHOST")
	if host == "" || os.Getenv("ORVIX_RUN_POSTGRES_DML_TEST") != "1" {
		t.Skip("postgres: set PGHOST and ORVIX_RUN_POSTGRES_DML_TEST=1 to run")
	}
	port := os.Getenv("PGPORT")
	if port == "" {
		port = "5432"
	}
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, os.Getenv("PGUSER"), os.Getenv("PGPASSWORD"), os.Getenv("PGDATABASE"))
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	schema := fmt.Sprintf("domain_pg_%d", time.Now().UnixNano())
	if _, err := db.Exec("CREATE SCHEMA IF NOT EXISTS " + schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := db.Exec("SET search_path TO " + schema); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
		_ = db.Close()
	})
	if err := models.MigrateAllPostgresRaw(db); err != nil {
		t.Fatalf("postgres migrate: %v", err)
	}
	now := time.Now().UTC()
	if _, err := db.Exec(
		`INSERT INTO tenants (name, slug, domain, plan, active, max_domains, max_mailboxes, created_at, updated_at)
		 VALUES ('PG Tenant', 'pg-tenant', 'pg.example', 'business', true, 10, 500, $1, $2)`, now, now); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return db
}

// TestDomainCreate_PostgreSQLInsertIDReturn proves the domain insert
// returns the real generated id on PostgreSQL (the RETURNING id path),
// which is what the provisioning transaction's audit/outbox/DKIM steps
// depend on.
func TestDomainCreate_PostgreSQLInsertIDReturn(t *testing.T) {
	db := openDomainPG(t)
	ctx := context.Background()

	svc := NewService(NewDomainAdminRepo(db), dkim.NewSQLRepo(db), audit.NewExtendedStore(db), nil)

	result, err := svc.ProvisionDomain(ctx, CreateDomainRequest{Name: "idreturn.example.com", Status: "active"}, 1)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if result.Domain.ID == 0 {
		t.Fatal("postgres insert must return the generated domain id, got 0")
	}
	// The returned id must be the row's actual id.
	var dbID uint
	if err := db.QueryRow(`SELECT id FROM coremail_domains WHERE name='idreturn.example.com'`).Scan(&dbID); err != nil {
		t.Fatalf("read domain: %v", err)
	}
	if dbID != result.Domain.ID {
		t.Fatalf("returned id %d != stored id %d", result.Domain.ID, dbID)
	}

	// With DKIM, the same id feeds the in-transaction DKIM + audit.
	dkimResult, err := svc.ProvisionDomain(ctx, CreateDomainRequest{
		Name: "dkimid.example.com", Status: "active", DKIM: &DKIMOptions{Generate: true, Selector: "mail"},
	}, 1)
	if err != nil {
		t.Fatalf("provision with dkim: %v", err)
	}
	if dkimResult.Domain.ID == 0 || dkimResult.DKIM == nil {
		t.Fatalf("dkim provisioning must return a real id + public dns: %+v", dkimResult)
	}
	var auditCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM orvix_audit WHERE target_id=$1 AND action='domain.provision'`, dkimResult.Domain.ID).Scan(&auditCount); err != nil {
		t.Fatalf("audit query: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("expected 1 domain.provision audit row for id %d, got %d", dkimResult.Domain.ID, auditCount)
	}
}
