package mailbox

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/models"
)

// openMailboxPG opens a fresh PostgreSQL schema for the mailbox
// repository's ID-return + access-mode persistence path. Gated on
// PGHOST + ORVIX_RUN_POSTGRES_DML_TEST=1.
func openMailboxPG(t *testing.T) (*sql.DB, *Service, *domain.Service) {
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
	schema := fmt.Sprintf("mailbox_pg_%d", time.Now().UnixNano())
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

	eng := coremail.NewEngine(coremail.EngineConfig{DB: db, AuthCfg: coremail.DefaultAuthConfig()})
	domainSvc := domain.NewService(domain.NewDomainAdminRepo(db), nil, nil, nil)
	if _, err := domainSvc.ProvisionDomain(context.Background(), domain.CreateDomainRequest{Name: "mb.example.com", Status: "active"}, 1); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	svc := NewService(NewAdminMailboxRepo(db), eng.Auth, nil, nil)
	return db, svc, domainSvc
}

// TestMailboxCreate_PostgreSQLInsertIDReturnAndMode proves the mailbox
// insert returns the real generated id on PostgreSQL (RETURNING id),
// persists the explicit access mode, and hashes the password with the
// canonical Argon2id implementation.
func TestMailboxCreate_PostgreSQLInsertIDReturnAndMode(t *testing.T) {
	db, svc, _ := openMailboxPG(t)
	ctx := context.Background()

	mode := string(MailAccessInternalOnly)
	created, err := svc.CreateMailbox(ctx, CreateMailboxRequest{
		Email: "pguser@mb.example.com", Password: "PGSecret123!", MailAccessMode: &mode,
	}, 1)
	if err != nil {
		t.Fatalf("create mailbox: %v", err)
	}
	if created.Mailbox.ID == 0 {
		t.Fatal("postgres insert must return the generated mailbox id, got 0")
	}
	var (
		dbID    uint
		stored  string
		hash    string
		version int
	)
	if err := db.QueryRow(`SELECT id, mail_access_mode, password_hash, version FROM coremail_mailboxes WHERE email='pguser@mb.example.com'`).
		Scan(&dbID, &stored, &hash, &version); err != nil {
		t.Fatalf("read mailbox: %v", err)
	}
	if dbID != created.Mailbox.ID {
		t.Fatalf("returned id %d != stored id %d", created.Mailbox.ID, dbID)
	}
	if stored != string(MailAccessInternalOnly) {
		t.Fatalf("stored mode=%q want internal_only", stored)
	}
	if version != 1 {
		t.Fatalf("version=%d want 1", version)
	}
	// The stored hash is the canonical Argon2id format and never the
	// plaintext password.
	if hash == "" || hash == "PGSecret123!" || len(hash) < 40 {
		t.Fatalf("password must be stored as an Argon2id hash, got %q", hash)
	}

	// The guarded access-mode mutation works on PostgreSQL too.
	cfg, eff, newVersion, err := svc.SetMailAccessMode(ctx, created.Mailbox.ID, 1, string(MailAccessInternalExternal), 1)
	if err != nil {
		t.Fatalf("set mode: %v", err)
	}
	if cfg != string(MailAccessInternalExternal) || eff != string(MailAccessInternalExternal) || newVersion != 2 {
		t.Fatalf("cfg=%q eff=%q version=%d", cfg, eff, newVersion)
	}

	// Cross-tenant guarded write resolves to not-found (zero rows).
	if _, _, _, err := svc.SetMailAccessMode(ctx, created.Mailbox.ID, 999, string(MailAccessInternalOnly), 2); err != ErrMailboxNotFound {
		t.Fatalf("cross-tenant must be not-found, got %v", err)
	}
}
