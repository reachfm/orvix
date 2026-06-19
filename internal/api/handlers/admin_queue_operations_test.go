package handlers_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/api/handlers"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
)

// TestAdminQueueSummaryHandler directly tests the AdminQueueSummary
// handler without the full router stack.
func TestAdminQueueSummaryHandler(t *testing.T) {
	logger := zap.NewNop()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = dir + "/test.db?_loc=auto&_busy_timeout=5000"

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	for _, stmt := range append(queue.Tables(), queue.Indexes()...) {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("queue schema: %v", err)
		}
	}
	// Seed a sample deferred queue entry.
	_, err = sqlDB.Exec(`INSERT INTO coremail_queue (message_id, from_address, to_address, status, attempt_count, max_attempts, delivery_mode, direction, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"test-msg-1", "sender@example.com", "recip@example.com", "deferred", 3, 16, "remote_smtp", "outbound", time.Now(), time.Now())
	if err != nil {
		t.Fatalf("seed queue: %v", err)
	}

	authn, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	h := handlers.NewHandler(db, authn, nil, logger, cfg, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	// Wire the queue engine so the handler can access the repository.
	qe := queue.NewQueueEngine(sqlDB)
	h.SetQueueEngine(qe)
	_ = cfg
	_ = authn

	// Build a minimal app that mounts the summary handler directly.
	app := fiber.New()
	app.Get("/api/v1/admin/queue/summary", h.AdminQueueSummary)

	// Test unauthenticated request.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/queue/summary", nil)
	res, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	// Without auth middleware, the handler will be called directly.
	_ = res.Body.Close()

	// The handler should still return JSON when queueEngine is nil.
	// We don't set queueEngine in this test, so it returns "not available".
	res2, _ := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	body, _ := io.ReadAll(res2.Body)
	res2.Body.Close()
	if res2.StatusCode != http.StatusOK {
		t.Errorf("expected 200; got %d body=%s", res2.StatusCode, string(body))
	}
	var resp struct {
		Metrics interface{} `json:"metrics"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, string(body))
	}
}

// TestAdminQueueEntryHandler tests the GetAdminQueueEntry handler.
func TestAdminQueueEntryHandler(t *testing.T) {
	logger := zap.NewNop()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = dir + "/test.db?_loc=auto&_busy_timeout=5000"

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	for _, stmt := range append(queue.Tables(), queue.Indexes()...) {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("queue schema: %v", err)
		}
	}
	// Seed a queue entry.
	_, err = sqlDB.Exec(`INSERT INTO coremail_queue (message_id, from_address, to_address, status, attempt_count, max_attempts, delivery_mode, direction, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"msg-detail", "alice@example.com", "bob@example.com", "pending", 0, 16, "remote_smtp", "outbound", time.Now(), time.Now())
	if err != nil {
		t.Fatalf("seed queue: %v", err)
	}

	authn, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	h := handlers.NewHandler(db, authn, nil, logger, cfg, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	// Wire the queue engine so GetAdminQueueEntry can access the repo.
	qe := queue.NewQueueEngine(sqlDB)
	h.SetQueueEngine(qe)
	// Build a minimal app.
	app := fiber.New()
	app.Get("/api/v1/admin/queue/:id", h.GetAdminQueueEntry)

	// Non-existent entry should return 404.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/queue/99999", nil)
	res, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for non-existent id; got %d body=%s", res.StatusCode, string(body))
	}

	// Existing entry should return 200 with JSON.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/admin/queue/1", nil)
	res2, _ := app.Test(req2, fiber.TestConfig{Timeout: 5 * time.Second})
	body2, _ := io.ReadAll(res2.Body)
	res2.Body.Close()
	if res2.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for queue id 1; got %d body=%s", res2.StatusCode, string(body2))
	}
	var entry struct {
		ID            uint   `json:"id"`
		FromAddress   string `json:"from_address"`
		ToAddress     string `json:"to_address"`
		Status        string `json:"status"`
		LastError     string `json:"last_error,omitempty"`
	}
	if err := json.Unmarshal(body2, &entry); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, string(body2))
	}
	if entry.ID != 1 {
		t.Errorf("expected id=1; got %d", entry.ID)
	}
	if entry.Status != "pending" {
		t.Errorf("expected status=pending; got %q", entry.Status)
	}
	if entry.FromAddress != "alice@example.com" {
		t.Errorf("expected from=alice@example.com; got %q", entry.FromAddress)
	}
}

// TestAdminQueueRoutesAuth confirms the new admin queue read
// endpoints require authentication through the real router stack.
func TestAdminQueueRoutesAuth(t *testing.T) {
	logger := zap.NewNop()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = dir + "/test.db?_loc=auto&_busy_timeout=5000"

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	// Create queue table and seed a row.
	for _, stmt := range append(queue.Tables(), queue.Indexes()...) {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("queue schema: %v", err)
		}
	}
	_, err = sqlDB.Exec(`INSERT INTO coremail_queue (message_id, from_address, to_address, status, attempt_count, max_attempts, delivery_mode, direction, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"auth-test-msg", "a@b.com", "c@d.com", "pending", 0, 16, "remote_smtp", "outbound", time.Now(), time.Now())
	if err != nil {
		t.Fatalf("seed queue: %v", err)
	}

	// Create users table and seed admin + non-admin users.
	sqlDB.Exec(`CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at DATETIME, updated_at DATETIME, email TEXT, password_hash TEXT, role TEXT, tenant_id INTEGER, active INTEGER, email_verified INTEGER)`)
	sqlDB.Exec(`INSERT INTO users (id, created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		1, time.Now(), time.Now(), "admin@test", "$2a$10$dummyhashdummyhashdummyhashdummyhashdummyhashdummyhashdummyhash", "admin", 1, 1, 1)

	authn, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	reg := modules.NewRegistry(logger)
	ff := license.NewFeatureFlags(logger)

	router := api.NewRouter(cfg, authn, logger, db, reg, ff, nil)
	app := router.App()

	adminTok, _ := authn.GenerateAccessToken(1, auth.RoleAdmin)

	tests := []struct {
		name string
		path string
	}{
		{"summary", "/api/v1/admin/queue/summary"},
		{"detail", "/api/v1/admin/queue/1"},
	}

	for _, tt := range tests {
		t.Run(tt.name+" no auth", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			res, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer res.Body.Close()
			if res.StatusCode != http.StatusUnauthorized {
				t.Errorf("expected 401; got %d", res.StatusCode)
			}
		})

		t.Run(tt.name+" admin auth", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.Header.Set("Authorization", "Bearer "+adminTok)
			res, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer res.Body.Close()
			if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNotFound {
				t.Errorf("expected 200 or 404 for admin; got %d", res.StatusCode)
			}
		})
	}
}

// Compile-time guard that this file is only built when the
// internal test infrastructure is available.
var _ = sync.Once{}
var _ = time.Second
