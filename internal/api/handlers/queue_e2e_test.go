package handlers

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"go.uber.org/zap"
)

func setupQueueTest(t *testing.T) (*fiber.App, *Handler, uint) {
	t.Helper()
	logger := zap.NewNop()
	tmp := t.TempDir()

	cfg := &config.Config{
		Server: config.ServerConfig{ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, BodyLimit: 4 << 20},
		Auth:   config.AuthConfig{JWTKeyPath: tmp + "/jwt.pem"},
		Database: config.DatabaseConfig{
			Driver: "sqlite",
			DSN:    tmp + "/orvix_queue_test.db?_loc=auto&_busy_timeout=5000&_txlock=immediate",
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

	// Enable feature flags.
	db.Exec("INSERT INTO feature_flags (name, enabled, tier_required, module_version, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		"rest_api", 1, "smb", "1.0.0", time.Now(), time.Now())

	// Create tenant + user.
	now := time.Now()
	db.Exec("INSERT INTO tenants (name, slug, domain, active, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		"Queue Tenant", "queue-tenant", "queue.example.com", 1, now, now)
	var tenantID uint
	db.Raw("SELECT id FROM tenants WHERE slug = 'queue-tenant'").Scan(&tenantID)

	hash, _ := auth.HashPassword("pwd")
	db.Exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id) VALUES (?, ?, ?, ?, ?, ?)",
		now, now, "admin@queue.example.com", hash, "admin", tenantID)
	var userID uint
	db.Raw("SELECT id FROM users WHERE email = 'admin@queue.example.com'").Scan(&userID)

	// Insert sample queue entries with various directions and statuses.
	type qe struct {
		dir  string
		stat string
	}
	entries := []qe{
		{"inbound", "delivered"},
		{"outbound", "pending"},
		{"internal", "delivered"},
		{"outbound", "deferred"},
		{"outbound", "bounced"},
		{"inbound", "queued"},
		{"outbound", "processing"},
	}
	for i, e := range entries {
		status := e.stat
		completedAt := "NULL"
		if status == "delivered" || status == "bounced" || status == "dead_letter" {
			completedAt = "datetime('now')"
		}
		sql := "INSERT INTO coremail_queue (tenant_id, domain_id, message_id, from_address, to_address, recipient_domain, direction, status, delivery_mode, created_at, updated_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'), " + completedAt + ")"
		db.Exec(sql,
			tenantID, 1,
			fmt.Sprintf("msg-%d@queue.example.com", i),
			"sender@queue.example.com",
			fmt.Sprintf("rcpt%d@example.com", i),
			"example.com",
			e.dir, status, "remote_smtp",
		)
	}

	authenticator, _ := auth.NewAuthenticator(&cfg.Auth, db, logger)
	apikeyMgr := auth.NewAPIKeyManager(db, logger)
	ff := license.NewFeatureFlags(logger)
	ff.SetTier(license.TierSMB)
	h := NewHandler(db, authenticator, apikeyMgr, logger, cfg, nil, ff, nil)

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", userID)
		c.Locals("role", auth.RoleAdmin)
		c.Locals("tenant_id", tenantID)
		return c.Next()
	})

	// Mount admin queue endpoints (GET only for test — mutation endpoints
	// require queueEngine which is not available in unit tests).
	app.Get("/admin/queue/messages", h.AdminQueueList)
	app.Get("/admin/queue/messages/:id", h.AdminQueueDetail)

	return app, h, tenantID
}

func TestQueue_ListAll(t *testing.T) {
	app, _, _ := setupQueueTest(t)

	resp, err := app.Test(httptest.NewRequest("GET", "/admin/queue/messages", nil),
		fiber.TestConfig{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	msgs := result["messages"].([]interface{})
	if len(msgs) != 7 {
		t.Fatalf("expected 7 queue messages, got %d", len(msgs))
	}
	// Check direction field exists.
	for _, m := range msgs {
		msg := m.(map[string]interface{})
		if msg["direction"] == nil || msg["direction"] == "" {
			t.Fatal("expected non-empty direction field in every message")
		}
	}
}

func TestQueue_FilterByDirection(t *testing.T) {
	app, _, _ := setupQueueTest(t)

	resp, _ := app.Test(httptest.NewRequest("GET", "/admin/queue/messages?direction=inbound", nil),
		fiber.TestConfig{Timeout: 2 * time.Second})
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	msgs := result["messages"].([]interface{})
	if len(msgs) == 0 {
		t.Fatal("expected at least one inbound message")
	}
}

func TestQueue_FilterByStatus(t *testing.T) {
	app, _, _ := setupQueueTest(t)

	resp, _ := app.Test(httptest.NewRequest("GET", "/admin/queue/messages?status=delivered", nil),
		fiber.TestConfig{Timeout: 2 * time.Second})
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	msgs := result["messages"].([]interface{})
	for _, m := range msgs {
		msg := m.(map[string]interface{})
		if msg["status"] != "delivered" {
			t.Fatalf("expected all messages to be delivered, got %s", msg["status"])
		}
	}
}

func TestQueue_Detail(t *testing.T) {
	app, _, _ := setupQueueTest(t)

	resp, _ := app.Test(httptest.NewRequest("GET", "/admin/queue/messages/1", nil),
		fiber.TestConfig{Timeout: 2 * time.Second})
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	msg := result["message"].(map[string]interface{})
	if msg["id"] == nil {
		t.Fatal("expected message to have id")
	}
}

func TestQueue_DirectionValuesValid(t *testing.T) {
	// Verify that direction constants are correct for the frontend.
	if queue.DirectionInbound != "inbound" {
		t.Fatalf("expected DirectionInbound to be 'inbound', got %s", queue.DirectionInbound)
	}
	if queue.DirectionOutbound != "outbound" {
		t.Fatalf("expected DirectionOutbound to be 'outbound', got %s", queue.DirectionOutbound)
	}
	if queue.DirectionInternal != "internal" {
		t.Fatalf("expected DirectionInternal to be 'internal', got %s", queue.DirectionInternal)
	}
}

func TestQueue_StatusValuesValid(t *testing.T) {
	if queue.StatusPending != "pending" {
		t.Fatalf("expected StatusPending to be 'pending', got %s", queue.StatusPending)
	}
	if queue.StatusDelivered != "delivered" {
		t.Fatalf("expected StatusDelivered to be 'delivered', got %s", queue.StatusDelivered)
	}
	if queue.StatusBounced != "bounced" {
		t.Fatalf("expected StatusBounced to be 'bounced', got %s", queue.StatusBounced)
	}
	if queue.StatusDeferred != "deferred" {
		t.Fatalf("expected StatusDeferred to be 'deferred', got %s", queue.StatusDeferred)
	}
	if queue.StatusDeadLetter != "dead_letter" {
		t.Fatalf("expected StatusDeadLetter to be 'dead_letter', got %s", queue.StatusDeadLetter)
	}
}
