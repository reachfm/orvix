package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/models"
	"go.uber.org/zap"
	_ "modernc.org/sqlite"
)

func TestMigrateDryRunSQLite(t *testing.T) {
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "orvix.db")
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = srcPath + "?_loc=auto&_busy_timeout=5000"
	db, err := config.NewDatabase(&cfg.Database, zap.NewNop())
	if err != nil {
		t.Fatalf("open source db: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate source: %v", err)
	}
	sqlDB, _ := db.DB()
	if _, err := sqlDB.Exec("INSERT INTO tenants (created_at, updated_at, name, slug, domain, plan, active) VALUES (datetime('now'), datetime('now'), 'Test', 'test', 'test.example.com', 'smb', 1)"); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	sqlDB.Close()

	code := migrateCommand([]string{
		"--from", "sqlite",
		"--to", "postgres",
		"--sqlite-path", srcPath,
		"--postgres-dsn", "",
		"--dry-run", "true",
	})
	if code != 2 {
		t.Fatalf("expected exit code 2 for empty DSN, got %d", code)
	}
}

func TestMigrateEmptyDSNRejected(t *testing.T) {
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "orvix.db")
	code := migrateCommand([]string{
		"--from", "sqlite",
		"--to", "postgres",
		"--sqlite-path", srcPath,
		"--dry-run", "true",
	})
	if code != 2 {
		t.Fatalf("expected exit code 2 for missing DSN, got %d", code)
	}
}

func TestMigrateDryRunListsRows(t *testing.T) {
	if os.Getenv("ORVIX_RUN_POSTGRES_MIGRATE_TEST") != "1" {
		t.Skip("set ORVIX_RUN_POSTGRES_MIGRATE_TEST=1 to run postgres migration tests")
	}
	if strings.ToLower(strings.TrimSpace(os.Getenv("ORVIX_DB_DRIVER"))) != "postgres" {
		t.Skip("ORVIX_DB_DRIVER must be postgres")
	}
	dsn := strings.TrimSpace(os.Getenv("ORVIX_DB_DSN"))
	if dsn == "" {
		t.Skip("ORVIX_DB_DSN is empty")
	}

	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "orvix.db")
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = srcPath + "?_loc=auto&_busy_timeout=5000"
	db, err := config.NewDatabase(&cfg.Database, zap.NewNop())
	if err != nil {
		t.Fatalf("open source db: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate source: %v", err)
	}
	sqlDB, _ := db.DB()
	if _, err := sqlDB.Exec("INSERT INTO tenants (created_at, updated_at, name, slug, domain, plan, active) VALUES (datetime('now'), datetime('now'), 'Test', 'test', 'test.example.com', 'smb', 1)"); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	sqlDB.Close()

	schema := fmt.Sprintf("orvix_migrate_test_%d", time.Now().UnixNano())
	code := migrateCommand([]string{
		"--from", "sqlite",
		"--to", "postgres",
		"--sqlite-path", srcPath,
		"--postgres-dsn", dsn,
		"--target-schema", schema,
		"--dry-run", "true",
	})
	if code != 0 {
		t.Fatalf("expected dry-run exit 0, got %d", code)
	}

	cleanupCfg := config.Defaults()
	cleanupCfg.Database.Driver = "postgres"
	cleanupCfg.Database.DSN = dsn
	gormDB, err := config.NewDatabase(&cleanupCfg.Database, zap.NewNop())
	if err != nil {
		t.Fatalf("connect for cleanup: %v", err)
	}
	cleanupDB, _ := gormDB.DB()
	cleanupDB.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
	cleanupDB.Close()
}

// seed10Tables populates the SQLite source database with realistic data
// across all 10 migration tables, respecting FK relationships.
func seed10Tables(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	// 1. tenants
	_, err := sqlDB.Exec(`INSERT INTO tenants (created_at, updated_at, name, slug, domain, plan, max_domains, max_mailboxes, logo_url, primary_color, active, reseller_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, "Acme Corp", "acme-corp", "acme.com", "enterprise", 50, 1000, "https://acme.com/logo.png", "#FF0000", 1, nil)
	if err != nil {
		t.Fatalf("seed tenant 1: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO tenants (created_at, updated_at, name, slug, domain, plan, max_domains, max_mailboxes, active)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, "Beta Inc", "beta-inc", "beta.com", "smb", 10, 100, 1)
	if err != nil {
		t.Fatalf("seed tenant 2: %v", err)
	}

	// 2. users (FK → tenants)
	_, err = sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, last_login)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, "admin@acme.com", "$2a$10$abc", "admin", 1, 1, 1, now)
	if err != nil {
		t.Fatalf("seed user 1: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, "user@acme.com", "$2a$10$def", "user", 1, 1, 0)
	if err != nil {
		t.Fatalf("seed user 2: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, "admin@beta.com", "$2a$10$ghi", "admin", 2, 1, 1)
	if err != nil {
		t.Fatalf("seed user 3: %v", err)
	}

	// 3. domains (FK → tenants)
	_, err = sqlDB.Exec(`INSERT INTO domains (created_at, updated_at, tenant_id, domain, dkim_selector, status, is_verified, is_primary)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, 1, "acme.com", "orvix", "active", 1, 1)
	if err != nil {
		t.Fatalf("seed domain 1: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO domains (created_at, updated_at, tenant_id, domain, dkim_selector, status, is_verified, is_primary)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, 1, "sub.acme.com", "default", "pending", 0, 0)
	if err != nil {
		t.Fatalf("seed domain 2: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO domains (created_at, updated_at, tenant_id, domain, dkim_selector, status, is_verified, is_primary)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, 2, "beta.com", "mail", "active", 1, 1)
	if err != nil {
		t.Fatalf("seed domain 3: %v", err)
	}

	// 4. mailboxes (FK → domains). local_part is the part before '@';
	// the migration joins domains to build the target email address.
	_, err = sqlDB.Exec(`INSERT INTO mailboxes (created_at, updated_at, tenant_id, domain_id, local_part, password_hash, quota_mb, is_alias, is_catchall, is_active, display_name, send_limit)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, 1, 1, "admin", "$2a$10$abc", 2048, 0, 0, 1, "Admin User", 500)
	if err != nil {
		t.Fatalf("seed mailbox 1: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO mailboxes (created_at, updated_at, tenant_id, domain_id, local_part, password_hash, quota_mb, is_alias, is_catchall, is_active, display_name, send_limit)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, 1, 1, "user", "$2a$10$def", 1024, 0, 0, 1, "Regular User", 200)
	if err != nil {
		t.Fatalf("seed mailbox 2: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO mailboxes (created_at, updated_at, tenant_id, domain_id, local_part, password_hash, quota_mb, is_alias, is_catchall, is_active)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, 1, 1, "catchall", "$2a$10$xyz", 512, 0, 1, 1)
	if err != nil {
		t.Fatalf("seed mailbox 3: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO mailboxes (created_at, updated_at, tenant_id, domain_id, local_part, password_hash, quota_mb, is_alias, is_catchall, is_active, display_name)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, 2, 3, "admin", "$2a$10$ghi", 1024, 0, 0, 1, "Beta Admin")
	if err != nil {
		t.Fatalf("seed mailbox 4: %v", err)
	}

	// 5. api_keys (FK → users)
	_, err = sqlDB.Exec(`INSERT INTO api_keys (created_at, updated_at, user_id, key_hash, name, expires_at, last_used, enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, 1, "sk_live_abc123", "Production API Key", now, now, 1)
	if err != nil {
		t.Fatalf("seed api_key 1: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO api_keys (created_at, updated_at, user_id, key_hash, name, enabled)
		VALUES (?, ?, ?, ?, ?, ?)`,
		now, now, 1, "sk_test_def456", "Test API Key", 1)
	if err != nil {
		t.Fatalf("seed api_key 2: %v", err)
	}

	// 6. sessions (FK → users)
	_, err = sqlDB.Exec(`INSERT INTO sessions (created_at, updated_at, user_id, token_hash, role, email, ip, user_agent, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, 1, "tok_abc", "admin", "admin@acme.com", "192.168.1.1", "Mozilla/5.0", now)
	if err != nil {
		t.Fatalf("seed session 1: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO sessions (created_at, updated_at, user_id, token_hash, role, email, ip, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, 2, "tok_def", "user", "user@acme.com", "10.0.0.1", now)
	if err != nil {
		t.Fatalf("seed session 2: %v", err)
	}

	// 7. coremail_audit
	_, err = sqlDB.Exec(`INSERT INTO coremail_audit (actor, role, action, target, result, ip, user_agent, timestamp)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"admin@acme.com", "admin", "tenant.create", "acme.com", "success", "192.168.1.1", "admin-ui", now)
	if err != nil {
		t.Fatalf("seed audit 1: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO coremail_audit (actor, role, action, target, result, ip, timestamp)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"admin@beta.com", "admin", "user.delete", "user@beta.com", "success", "10.0.0.2", now)
	if err != nil {
		t.Fatalf("seed audit 2: %v", err)
	}

	// 8. security_events
	_, err = sqlDB.Exec(`INSERT INTO security_events (ip, email, event_type, count, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		"203.0.113.1", "admin@acme.com", "failed_login", 5, now)
	if err != nil {
		t.Fatalf("seed security event 1: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO security_events (ip, email, event_type, created_at)
		VALUES (?, ?, ?, ?)`,
		"198.51.100.1", "user@acme.com", "password_reset", now)
	if err != nil {
		t.Fatalf("seed security event 2: %v", err)
	}

	// 9. feature_flags
	_, err = sqlDB.Exec(`INSERT INTO feature_flags (created_at, updated_at, name, enabled, tier_required, module_version, description)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		now, now, "webmail", 1, "smb", "1.0.0", "Webmail UI")
	if err != nil {
		t.Fatalf("seed feature_flag 1: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO feature_flags (created_at, updated_at, name, enabled, tier_required, module_version, description)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		now, now, "audit_logs", 1, "smb", "2.0.0", "Audit log access")
	if err != nil {
		t.Fatalf("seed feature_flag 2: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO feature_flags (created_at, updated_at, name, enabled, tier_required, module_version, description)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		now, now, "intelligence", 0, "enterprise", "1.5.0", "Email intelligence")
	if err != nil {
		t.Fatalf("seed feature_flag 3: %v", err)
	}

	// 10. licenses
	_, err = sqlDB.Exec(`INSERT INTO licenses (created_at, updated_at, key_hash, tier, issued_at, expires_at, max_domains, max_mailboxes, hardware_id, metadata, active)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, "lic_abc123", "enterprise", now, now, 100, 5000, "hw-001", `{"region":"us-east"}`, 1)
	if err != nil {
		t.Fatalf("seed license 1: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO licenses (created_at, updated_at, key_hash, tier, issued_at, expires_at, max_domains, max_mailboxes, hardware_id, active)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, "lic_def456", "smb", now, now, 10, 500, "hw-002", 1)
	if err != nil {
		t.Fatalf("seed license 2: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO licenses (created_at, updated_at, key_hash, tier, issued_at, expires_at, max_domains, max_mailboxes, hardware_id, active)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, "lic_inactive", "smb", now, now, 5, 100, "hw-003", 0)
	if err != nil {
		t.Fatalf("seed license 3: %v", err)
	}
}

// expectedRowCounts returns the expected row counts after seed10Tables.
func expectedRowCounts() map[string]int64 {
	return map[string]int64{
		"tenants":         2,
		"users":           3,
		"domains":         3,
		"mailboxes":       4,
		"api_keys":        2,
		"sessions":        2,
		"coremail_audit":  2,
		"security_events": 2,
		"feature_flags":   3,
		"licenses":        3,
	}
}

func TestMigrateAll10TablesWithRowVerification(t *testing.T) {
	if os.Getenv("ORVIX_RUN_POSTGRES_MIGRATE_TEST") != "1" {
		t.Skip("set ORVIX_RUN_POSTGRES_MIGRATE_TEST=1 to run postgres migration tests")
	}
	if strings.ToLower(strings.TrimSpace(os.Getenv("ORVIX_DB_DRIVER"))) != "postgres" {
		t.Skip("ORVIX_DB_DRIVER must be postgres")
	}
	dsn := strings.TrimSpace(os.Getenv("ORVIX_DB_DSN"))
	if dsn == "" {
		t.Skip("ORVIX_DB_DSN is empty")
	}

	// Create and seed SQLite source.
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "orvix.db")
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = srcPath + "?_loc=auto&_busy_timeout=5000"
	db, err := config.NewDatabase(&cfg.Database, zap.NewNop())
	if err != nil {
		t.Fatalf("open source db: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate source: %v", err)
	}
	sqlDB, _ := db.DB()

	seed10Tables(t, sqlDB)

	// Capture source row counts.
	plan := defaultMigrationPlan()
	srcCounts, err := plan.rowCounts(context.Background(), sqlDB)
	if err != nil {
		t.Fatalf("source row counts: %v", err)
	}
	sqlDB.Close()

	expected := expectedRowCounts()
	for table, want := range expected {
		got := srcCounts[table]
		if got != want {
			t.Fatalf("source table %s: expected %d rows, got %d", table, want, got)
		}
	}

	// Run migration to isolated PG schema.
	schema := fmt.Sprintf("orvix_migrate_test_%d", time.Now().UnixNano())
	code := migrateCommand([]string{
		"--from", "sqlite",
		"--to", "postgres",
		"--sqlite-path", srcPath,
		"--postgres-dsn", dsn,
		"--target-schema", schema,
		"--dry-run=false",
		"--skip-confirm",
	})
	if code != 0 {
		t.Fatalf("expected migration exit 0, got %d", code)
	}

	// Verify PG target row counts.
	tgtCfg := config.Defaults()
	tgtCfg.Database.Driver = "postgres"
	tgtCfg.Database.DSN = dsn
	tgtCfg.Database.MaxOpen = 1
	tgtGorm, err := config.NewDatabase(&tgtCfg.Database, zap.NewNop())
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}
	tgtDB, _ := tgtGorm.DB()
	defer func() {
		tgtDB.Exec("SET search_path TO public")
		tgtDB.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
		tgtDB.Close()
	}()

	if _, err := tgtDB.Exec("SET search_path TO " + schema); err != nil {
		t.Fatalf("set search_path: %v", err)
	}

	tgtCounts, err := plan.rowCounts(context.Background(), tgtDB)
	if err != nil {
		t.Fatalf("target row counts: %v", err)
	}

	for table, want := range expected {
		got := tgtCounts[table]
		if got != want {
			t.Errorf("target table %s: expected %d rows, got %d", table, want, got)
		}
	}

	// Verify boolean columns were properly converted.
	verifyBooleanConversions(t, tgtDB)

	// Verify mailbox local_part/domain/email semantics.
	verifyMailboxSemantics(t, tgtDB)

	// Verify non-empty target guard blocks second migration.
	code2 := migrateCommand([]string{
		"--from", "sqlite",
		"--to", "postgres",
		"--sqlite-path", srcPath,
		"--postgres-dsn", dsn,
		"--target-schema", schema,
		"--dry-run=false",
	})
	if code2 == 0 {
		t.Error("expected non-empty target guard to refuse migration")
	}
	t.Logf("non-empty target guard exit code: %d", code2)
}

func verifyBooleanConversions(t *testing.T, db *sql.DB) {
	t.Helper()

	// tenants.active
	var tenantActive bool
	if err := db.QueryRow("SELECT active FROM tenants WHERE slug = 'acme-corp'").Scan(&tenantActive); err != nil {
		t.Errorf("query tenants.active: %v", err)
	} else if !tenantActive {
		t.Error("tenants.active should be true")
	}

	// users.active and email_verified
	var userActive, emailVerified bool
	err := db.QueryRow("SELECT active, email_verified FROM users WHERE email = 'admin@acme.com'").Scan(&userActive, &emailVerified)
	if err != nil {
		t.Errorf("query users booleans: %v", err)
	} else {
		if !userActive {
			t.Error("users.active should be true for admin@acme.com")
		}
		if !emailVerified {
			t.Error("users.email_verified should be true for admin@acme.com")
		}
	}

	// Verify email_verified=false for user@acme.com
	err = db.QueryRow("SELECT active, email_verified FROM users WHERE email = 'user@acme.com'").Scan(&userActive, &emailVerified)
	if err != nil {
		t.Errorf("query users booleans: %v", err)
	} else {
		if emailVerified {
			t.Error("users.email_verified should be false for user@acme.com")
		}
	}

	// domains.is_verified, is_primary
	var isVerified, isPrimary bool
	err = db.QueryRow("SELECT is_verified, is_primary FROM domains WHERE domain = 'acme.com'").Scan(&isVerified, &isPrimary)
	if err != nil {
		t.Errorf("query domains booleans: %v", err)
	} else {
		if !isVerified {
			t.Error("domains.is_verified should be true for acme.com")
		}
		if !isPrimary {
			t.Error("domains.is_primary should be true for acme.com")
		}
	}

	// domains.is_verified=false for sub.acme.com
	var subVerified bool
	if err := db.QueryRow("SELECT is_verified FROM domains WHERE domain = 'sub.acme.com'").Scan(&subVerified); err != nil {
		t.Errorf("query sub.acme.com is_verified: %v", err)
	} else if subVerified {
		t.Error("domains.is_verified should be false for sub.acme.com")
	}

	// mailboxes.is_active, is_alias, is_catchall
	var isActive, isAlias, isCatchall bool
	err = db.QueryRow("SELECT is_active, is_alias, is_catchall FROM mailboxes WHERE email = 'admin@acme.com'").Scan(&isActive, &isAlias, &isCatchall)
	if err != nil {
		t.Errorf("query mailboxes booleans: %v", err)
	} else {
		if !isActive {
			t.Error("mailboxes.is_active should be true")
		}
		if isAlias {
			t.Error("mailboxes.is_alias should be false")
		}
		if isCatchall {
			t.Error("mailboxes.is_catchall should be false")
		}
	}

	// catchall mailbox
	err = db.QueryRow("SELECT is_active, is_catchall FROM mailboxes WHERE email = 'catchall@acme.com'").Scan(&isActive, &isCatchall)
	if err != nil {
		t.Errorf("query catchall booleans: %v", err)
	} else {
		if !isCatchall {
			t.Error("catchall mailbox is_catchall should be true")
		}
	}

	// api_keys.enabled
	var keyEnabled bool
	if err := db.QueryRow("SELECT enabled FROM api_keys WHERE key_hash = 'sk_live_abc123'").Scan(&keyEnabled); err != nil {
		t.Errorf("query api_keys.enabled: %v", err)
	} else if !keyEnabled {
		t.Error("api_keys.enabled should be true")
	}

	// feature_flags.enabled
	var ffEnabled bool
	if err := db.QueryRow("SELECT enabled FROM feature_flags WHERE name = 'webmail'").Scan(&ffEnabled); err != nil {
		t.Errorf("query feature_flags.enabled: %v", err)
	} else if !ffEnabled {
		t.Error("webmail feature flag should be enabled")
	}
	err = db.QueryRow("SELECT enabled FROM feature_flags WHERE name = 'intelligence'").Scan(&ffEnabled)
	if err != nil {
		t.Errorf("query feature_flags.enabled: %v", err)
	} else if ffEnabled {
		t.Error("intelligence feature flag should be disabled")
	}

	// licenses.active
	var licActive bool
	if err := db.QueryRow("SELECT active FROM licenses WHERE key_hash = 'lic_abc123'").Scan(&licActive); err != nil {
		t.Errorf("query licenses.active: %v", err)
	} else if !licActive {
		t.Error("licenses.active should be true for lic_abc123")
	}
	if err := db.QueryRow("SELECT active FROM licenses WHERE key_hash = 'lic_inactive'").Scan(&licActive); err != nil {
		t.Errorf("query licenses.active: %v", err)
	} else if licActive {
		t.Error("licenses.active should be false for lic_inactive")
	}
}

// verifyMailboxSemantics asserts that a mailbox row was reconstructed
// from SQLite local_part + domains.domain instead of copying local_part
// directly into the email column.
func verifyMailboxSemantics(t *testing.T, db *sql.DB) {
	t.Helper()

	var email, localPart, displayName, domain string
	var domainID int64
	var passwordHash string
	var quotaMB, sendLimit int
	err := db.QueryRow(`SELECT m.email, m.local_part, m.password_hash, m.quota_mb, m.display_name, m.send_limit, m.domain_id, d.domain
		FROM mailboxes m JOIN domains d ON m.domain_id = d.id
		WHERE m.local_part = 'admin' AND d.domain = 'acme.com'`).Scan(
		&email, &localPart, &passwordHash, &quotaMB, &displayName, &sendLimit, &domainID, &domain)
	if err != nil {
		t.Fatalf("query admin mailbox semantics: %v", err)
	}
	if email != "admin@acme.com" {
		t.Errorf("mailbox email should be admin@acme.com, got %q", email)
	}
	if localPart != "admin" {
		t.Errorf("mailbox local_part should be preserved as 'admin', got %q", localPart)
	}
	if passwordHash != "$2a$10$abc" {
		t.Errorf("mailbox password_hash not preserved: got %q", passwordHash)
	}
	if quotaMB != 2048 {
		t.Errorf("mailbox quota_mb not preserved: expected 2048, got %d", quotaMB)
	}
	if displayName != "Admin User" {
		t.Errorf("mailbox display_name not preserved: got %q", displayName)
	}
	if sendLimit != 500 {
		t.Errorf("mailbox send_limit not preserved: expected 500, got %d", sendLimit)
	}
	if domain != "acme.com" {
		t.Errorf("mailbox domain relationship not preserved: expected acme.com, got %q", domain)
	}
}

// TestMigrateRealPostgresWithRowCounts tests the basic 2-table migration
// (tenants + users) that existed in the original test suite.
func TestMigrateRealPostgresWithRowCounts(t *testing.T) {
	if os.Getenv("ORVIX_RUN_POSTGRES_MIGRATE_TEST") != "1" {
		t.Skip("set ORVIX_RUN_POSTGRES_MIGRATE_TEST=1 to run postgres migration tests")
	}
	if strings.ToLower(strings.TrimSpace(os.Getenv("ORVIX_DB_DRIVER"))) != "postgres" {
		t.Skip("ORVIX_DB_DRIVER must be postgres")
	}
	dsn := strings.TrimSpace(os.Getenv("ORVIX_DB_DSN"))
	if dsn == "" {
		t.Skip("ORVIX_DB_DSN is empty")
	}

	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "orvix.db")
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = srcPath + "?_loc=auto&_busy_timeout=5000"
	db, err := config.NewDatabase(&cfg.Database, zap.NewNop())
	if err != nil {
		t.Fatalf("open source db: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate source: %v", err)
	}
	sqlDB, _ := db.DB()

	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	sqlDB.Exec("INSERT INTO tenants (created_at, updated_at, name, slug, domain, plan, active) VALUES (?, ?, 'Test', 'test', 'test.example.com', 'smb', 1)", now, now)
	sqlDB.Exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'admin@test.example.com', '$2a$04$placeholder', 'admin', 1, 1, 1)", now, now)

	var srcTenants, srcUsers int
	sqlDB.QueryRow("SELECT COUNT(*) FROM tenants").Scan(&srcTenants)
	sqlDB.QueryRow("SELECT COUNT(*) FROM users").Scan(&srcUsers)
	t.Logf("source: tenants=%d users=%d", srcTenants, srcUsers)
	sqlDB.Close()

	schema := fmt.Sprintf("orvix_migrate_test_%d", time.Now().UnixNano())
	code := migrateCommand([]string{
		"--from", "sqlite",
		"--to", "postgres",
		"--sqlite-path", srcPath,
		"--postgres-dsn", dsn,
		"--target-schema", schema,
		"--dry-run=false",
		"--skip-confirm",
	})
	if code != 0 {
		t.Fatalf("expected migration exit 0, got %d", code)
	}

	cleanupCfg := config.Defaults()
	cleanupCfg.Database.Driver = "postgres"
	cleanupCfg.Database.DSN = dsn
	gormDB, err := config.NewDatabase(&cleanupCfg.Database, zap.NewNop())
	if err != nil {
		t.Fatalf("connect for cleanup: %v", err)
	}
	cleanupDB, _ := gormDB.DB()
	defer cleanupDB.Close()

	cleanupDB.Exec("SET search_path TO " + schema)
	var tgtTenants, tgtUsers int
	cleanupDB.QueryRow("SELECT COUNT(*) FROM tenants").Scan(&tgtTenants)
	cleanupDB.QueryRow("SELECT COUNT(*) FROM users").Scan(&tgtUsers)
	t.Logf("target: tenants=%d users=%d", tgtTenants, tgtUsers)

	if tgtTenants != srcTenants {
		t.Errorf("tenant count mismatch: src=%d tgt=%d", srcTenants, tgtTenants)
	}
	if tgtUsers != srcUsers {
		t.Errorf("user count mismatch: src=%d tgt=%d", srcUsers, tgtUsers)
	}

	code2 := migrateCommand([]string{
		"--from", "sqlite",
		"--to", "postgres",
		"--sqlite-path", srcPath,
		"--postgres-dsn", dsn,
		"--target-schema", schema,
		"--dry-run=false",
	})
	if code2 == 0 {
		t.Error("expected non-empty target guard to refuse migration")
	}
	t.Logf("non-empty target guard exit code: %d", code2)

	cleanupDB.Exec("SET search_path TO public")
	cleanupDB.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
}
