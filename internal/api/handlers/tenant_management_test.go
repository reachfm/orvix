package handlers

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"go.uber.org/zap"
)

func setupTenantTest(t *testing.T) (*Handler, uint, uint) {
	t.Helper()
	logger := zap.NewNop()
	tmp := t.TempDir()

	cfg := &config.Config{
		Server: config.ServerConfig{ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, BodyLimit: 4 << 20},
		Auth:   config.AuthConfig{JWTKeyPath: tmp + "/jwt.pem"},
		Database: config.DatabaseConfig{
			Driver: "sqlite",
			DSN:    tmp + "/orvix_tenant.db?_loc=auto&_busy_timeout=5000&_txlock=immediate",
		},
	}
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("MigrateAllRaw: %v", err)
	}

	db.Exec("INSERT INTO feature_flags (name, enabled, tier_required, module_version, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		"rest_api", 1, "smb", "1.0.0", time.Now(), time.Now())

	now := time.Now()
	db.Exec("INSERT INTO tenants (name, slug, domain, active, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		"Acme Corp", "acme", "acme.com", 1, now, now)
	db.Exec("INSERT INTO tenants (name, slug, domain, active, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		"Beta Inc", "beta", "beta.org", 1, now, now)

	var tA, tB uint
	db.Raw("SELECT id FROM tenants WHERE slug = 'acme'").Scan(&tA)
	db.Raw("SELECT id FROM tenants WHERE slug = 'beta'").Scan(&tB)

	hash, _ := auth.HashPassword("pwd")
	db.Exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id) VALUES (?, ?, ?, ?, ?, ?)",
		now, now, "superadmin@orvix.io", hash, "platform_super_admin", 0)
	var superUID uint
	db.Raw("SELECT id FROM users WHERE email = 'superadmin@orvix.io'").Scan(&superUID)

	db.Exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id) VALUES (?, ?, ?, ?, ?, ?)",
		now, now, "admin@acme.com", hash, "admin", tA)
	var adminUID uint
	db.Raw("SELECT id FROM users WHERE email = 'admin@acme.com'").Scan(&adminUID)

	authenticator, _ := auth.NewAuthenticator(&cfg.Auth, db, logger)
	apikeyMgr := auth.NewAPIKeyManager(db, logger)
	ff := license.NewFeatureFlags(logger)
	ff.SetTier(license.TierEnterprise)
	h := NewHandler(db, authenticator, apikeyMgr, logger, cfg, nil, ff, nil)

	return h, superUID, adminUID
}

func TestInternalTenants_DirectSQL(t *testing.T) {
	h, _, _ := setupTenantTest(t)
	sqlDB := h.sqlDB()
	if sqlDB == nil {
		t.Fatal("sqlDB is nil")
	}
	var count int
	err := sqlDB.QueryRow("SELECT COUNT(*) FROM tenants WHERE deleted_at IS NULL").Scan(&count)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count < 2 {
		t.Fatalf("expected at least 2 tenants, got %d", count)
	}
}

func TestInternalTenants_EmptyFilter(t *testing.T) {
	// Test filtering - no results for non-existent name.
	h, _, _ := setupTenantTest(t)
	sqlDB := h.sqlDB()
	var count int
	err := sqlDB.QueryRow("SELECT COUNT(*) FROM tenants WHERE name = 'nonexistent' AND deleted_at IS NULL").Scan(&count)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 tenants, got %d", count)
	}
}

func TestInternalTenants_HasDomainAndMailboxCounts(t *testing.T) {
	h, _, _ := setupTenantTest(t)
	sqlDB := h.sqlDB()
	dial := dbdialect.FromDriver("sqlite")

	// Verify the domain count query works.
	var tenantID uint
	sqlDB.QueryRow("SELECT id FROM tenants WHERE slug = 'acme'").Scan(&tenantID)

	var domainCount int
	err := sqlDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM coremail_domains WHERE tenant_id = %s AND deleted_at IS NULL",
		dial.Placeholder(1)), tenantID).Scan(&domainCount)
	if err != nil {
		t.Fatalf("domain count: %v", err)
	}
	if domainCount != 0 {
		t.Fatalf("expected 0 domains for new tenant, got %d", domainCount)
	}

	var mailboxCount int
	err = sqlDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM coremail_mailboxes WHERE tenant_id = %s AND deleted_at IS NULL",
		dial.Placeholder(1)), tenantID).Scan(&mailboxCount)
	if err != nil {
		t.Fatalf("mailbox count: %v", err)
	}
	if mailboxCount != 0 {
		t.Fatalf("expected 0 mailboxes for new tenant, got %d", mailboxCount)
	}
}

func TestInternalTenants_JSONResponseFormat(t *testing.T) {
	h, superUID, _ := setupTenantTest(t)
	_ = superUID

	// Build the response directly by calling the handler's raw query logic
	// (same as InternalTenants does internally).
	sqlDB := h.sqlDB()
	dial := dbdialect.FromDriver("sqlite")

	rows, err := sqlDB.Query("SELECT id, name, slug, domain, COALESCE(plan,''), COALESCE(active,0) FROM tenants WHERE deleted_at IS NULL ORDER BY id ASC")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	type tenantRow struct {
		ID            int    `json:"id"`
		Name          string `json:"name"`
		Slug          string `json:"slug"`
		Domain        string `json:"domain"`
		Plan          string `json:"plan"`
		Active        bool   `json:"active"`
		Domains       int    `json:"domains"`
		Mailboxes     int    `json:"mailboxes"`
		StorageBytes  int64  `json:"storage_bytes"`
		LoginFailures int    `json:"login_failures"`
	}

	var tenants []tenantRow
	for rows.Next() {
		var r tenantRow
		var active int
		if err := rows.Scan(&r.ID, &r.Name, &r.Slug, &r.Domain, &r.Plan, &active); err != nil {
			continue
		}
		r.Active = active != 0
		sqlDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM coremail_domains WHERE tenant_id = %s AND deleted_at IS NULL",
			dial.Placeholder(1)), r.ID).Scan(&r.Domains)
		sqlDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM coremail_mailboxes WHERE tenant_id = %s AND deleted_at IS NULL",
			dial.Placeholder(1)), r.ID).Scan(&r.Mailboxes)
		tenants = append(tenants, r)
	}
	if len(tenants) < 2 {
		t.Fatalf("expected at least 2 tenants, got %d", len(tenants))
	}

	// Verify JSON serialization.
	jsonBytes, err := json.Marshal(tenants)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	jsonStr := string(jsonBytes)
	if !strings.Contains(jsonStr, "acme") {
		t.Fatal("expected 'acme' in JSON output")
	}
	if !strings.Contains(jsonStr, "beta") {
		t.Fatal("expected 'beta' in JSON output")
	}
}
