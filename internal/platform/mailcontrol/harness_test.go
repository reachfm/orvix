package mailcontrol

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/admin/mailbox"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/dkim"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/models"
	"go.uber.org/zap"
)

// newMailControlHarness builds the real production schema (via
// models.MigrateAllRaw) and real admin services, exactly as the router
// wires them, so the tests exercise production initialization — never a
// hand-rolled test schema.
func newMailControlHarness(t *testing.T) (*Service, *Repository, *sql.DB) {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "mailcontrol.db") + "?_pragma=busy_timeout(5000)&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s, err := db.DB(); err == nil {
			s.Close()
		}
	})
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	// The coremail storage schema (folders/messages/attachments) is
	// owned by the coremail storage package; the runtime provisions it
	// at boot, and the harness mirrors that for folder provisioning.
	for _, stmt := range storage.Tables() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("storage schema: %v", err)
		}
	}
	for _, stmt := range storage.Indexes() {
		_, _ = sqlDB.Exec(stmt)
	}
	eng := coremail.NewEngine(coremail.EngineConfig{DB: sqlDB, AuthCfg: coremail.DefaultAuthConfig()})
	auditStore := audit.NewExtendedStore(sqlDB)
	_ = auditStore.EnsureTable(context.Background())

	domainSvc := domain.NewService(domain.NewDomainAdminRepo(sqlDB), dkim.NewSQLRepo(sqlDB), auditStore, nil)
	mailboxSvc := mailbox.NewService(mailbox.NewAdminMailboxRepo(sqlDB), eng.Auth, auditStore, nil)
	repo := NewRepository(sqlDB)
	svc := NewService(repo, Ports{Domains: domainSvc, Mailboxes: mailboxSvc, Audit: auditStore})
	return svc, repo, sqlDB
}

func mustSeedTenant(t *testing.T, db *sql.DB, id uint) {
	t.Helper()
	now := "2026-01-01T00:00:00Z"
	if _, err := db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, active, created_at, updated_at)
		VALUES (?, 'Tenant', ?, ?, 'business', 1, ?, ?)`, id, "tenant-"+itoa(int(id)), "t"+itoa(int(id))+".example", now, now); err != nil {
		t.Fatal(err)
	}
}
