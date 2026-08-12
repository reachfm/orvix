package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/billing"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// buildSendEnforcementHarness builds a real Handler wired with a MailStore,
// QueueEngine and webmail service, provisioned with an admin user + mailbox,
// and returns it plus the DB handles. The caller decides whether to wire a
// send enforcer so both the fail-closed and normal paths can be exercised
// through the real WebmailSend HTTP handler.
func buildSendEnforcementHarness(t *testing.T, wireEnforcer bool) (*Handler, *gorm.DB, *sql.DB) {
	t.Helper()
	logger := zap.NewNop()
	scratchDir := t.TempDir()
	adminDir := filepath.Join(scratchDir, "admin")
	if err := os.MkdirAll(adminDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adminDir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = scratchDir + "/orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.Server.AdminUIDir = adminDir
	cfg.CoreMail.MailStorePath = filepath.Join(scratchDir, "mailstore")
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}

	mailstoreDir := filepath.Join(scratchDir, "mailstore")
	if err := os.MkdirAll(mailstoreDir, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range storage.Tables() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("mailstore ddl: %v", err)
		}
	}
	mailStore, err := storage.NewMailStore(sqlDB, mailstoreDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range queue.Tables() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("queue ddl: %v", err)
		}
	}
	for _, stmt := range queue.Indexes() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("queue idx: %v", err)
		}
	}
	qe := queue.NewQueueEngine(sqlDB)

	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatal(err)
	}
	reg := modules.NewRegistry(logger)
	ff := license.NewFeatureFlags(logger)

	h := NewHandler(db, authenticator, nil, logger, cfg, reg, ff, nil)
	h.SetMailStore(mailStore)
	h.SetQueueEngine(qe)
	h.SetWebmailService(nil)

	if wireEnforcer {
		billingSvc, _, quotaSvc, _, _, enforcer, err := billing.Initialize(sqlDB)
		if err != nil {
			t.Fatalf("billing.Initialize: %v", err)
		}
		_ = billingSvc
		_ = quotaSvc
		h.SetSendEnforcer(enforcer)
	}

	provisionSendAdmin(t, sqlDB)

	// Ensure system folders (Sent/Inbox/etc.) exist for the admin mailbox so
	// the send path can store the sent copy.
	var mailboxID uint
	if err := sqlDB.QueryRow("SELECT id FROM coremail_mailboxes WHERE email='admin@orvix.email'").Scan(&mailboxID); err != nil {
		t.Fatal(err)
	}
	if err := mailStore.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatal(err)
	}

	return h, db, sqlDB
}

func provisionSendAdmin(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	now := currentTimeForTest()
	if _, err := sqlDB.Exec(
		"INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		1, now, now, "orvix", "orvix", "orvix.email", "enterprise", 1,
	); err != nil {
		t.Fatalf("tenant: %v", err)
	}
	sqlDB.Exec(`INSERT OR IGNORE INTO subscriptions (tenant_id, plan_id, status, billing_interval,
		current_period_start, current_period_end, send_limit_day, storage_mb, created_at, updated_at)
		VALUES (1, 'free', 'active', 'monthly', ?, ?, 500, 1024, ?, ?)`, now, now.AddDate(0, 1, 0), now, now)
	if _, err := sqlDB.Exec(
		"INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		now, now, "admin@orvix.email", "x-bcrypt-hash-placeholder", "tenant_admin", 1, 1, 1,
	); err != nil {
		t.Fatalf("users: %v", err)
	}
	if _, err := sqlDB.Exec(
		"INSERT INTO coremail_domains (name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		"orvix.email", 1, "active", "enterprise", 0, 0, 0, now, now,
	); err != nil {
		t.Fatalf("domains: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		1, 1, "admin", "admin@orvix.email", "Admin", "x-bcrypt-hash-placeholder", "bcrypt", "active", 1024, 1, now, now,
	); err != nil {
		t.Fatalf("mailboxes: %v", err)
	}
}

func currentTimeForTest() time.Time { return time.Now().UTC() }

// TestWebmailSendFailsClosedWhenEnforcerUnavailable proves that when the
// send enforcer is not wired, the real WebmailSend handler returns 503
// SEND_ENFORCEMENT_UNAVAILABLE and does NOT enqueue anything. This is the
// fail-closed guarantee: outbound send must never bypass quota enforcement.
func TestWebmailSendFailsClosedWhenEnforcerUnavailable(t *testing.T) {
	h, db, sqlDB := buildSendEnforcementHarness(t, false)
	t.Cleanup(func() {
		sqlDB.Close()
		if s, err := db.DB(); err == nil {
			s.Close()
		}
	})

	app := fiber.New()
	app.Post("/api/v1/webmail/send", func(c fiber.Ctx) error {
		c.Locals("user_id", uint(1))
		return h.WebmailSend(c)
	})

	req := httptest.NewRequest("POST", "/api/v1/webmail/send", bytes.NewBufferString(`{"to":"alice@example.com","subject":"Hi","body":"body"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusServiceUnavailable {
		t.Fatalf("expected 503 when enforcer unavailable, got %d", resp.StatusCode)
	}
	var body map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["code"] != "SEND_ENFORCEMENT_UNAVAILABLE" {
		t.Fatalf("expected SEND_ENFORCEMENT_UNAVAILABLE code, got %v", body["code"])
	}

	// Nothing must be enqueued.
	var count int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_queue").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected no queue entries, got %d", count)
	}
}

// TestWebmailSendSucceedsWhenEnforcerWired proves that with a real wired
// enforcer the same request succeeds (201 queued), so the fail-closed path
// is not blocking the normal path.
func TestWebmailSendSucceedsWhenEnforcerWired(t *testing.T) {
	h, db, sqlDB := buildSendEnforcementHarness(t, true)
	t.Cleanup(func() {
		sqlDB.Close()
		if s, err := db.DB(); err == nil {
			s.Close()
		}
	})

	app := fiber.New()
	app.Post("/api/v1/webmail/send", func(c fiber.Ctx) error {
		c.Locals("user_id", uint(1))
		return h.WebmailSend(c)
	})

	req := httptest.NewRequest("POST", "/api/v1/webmail/send", bytes.NewBufferString(`{"to":"alice@example.com","subject":"Hi","body":"body"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusCreated {
		t.Fatalf("expected 201 with enforcer wired, got %d", resp.StatusCode)
	}
}
