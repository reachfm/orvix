package auth

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/models"
	"go.uber.org/zap"
)

func TestReauthActionScope_Mapping(t *testing.T) {
	tests := []struct {
		action string
		want   ReauthScope
	}{
		{"tenant_create", ScopeTenantManagement},
		{"admin_create", ScopeIdentityManagement},
		{"domain_create", ScopeDomainManagement},
		{"mailbox_create", ScopeMailboxManagement},
		{"billing_plan", ScopeBillingManagement},
		{"firewall_create", ScopeFirewallManagement},
		{"apikey_create", ScopeAPIKeyManagement},
		{"queue_retry", ScopeQueueDestructive},
		{"backup_create", ScopeBackupRestore},
		{"security_settings", ScopeSecuritySettings},
		{"system_settings", ScopeSystemSettings},
		{"system_update", ScopeSystemUpdate},
		{"unknown_action", ""},
	}
	for _, tt := range tests {
		got := ReauthActionScope(tt.action)
		if got != tt.want {
			t.Errorf("ReauthActionScope(%q) = %q, want %q", tt.action, got, tt.want)
		}
	}
}

func TestRouteSecurity_Build_WithReauth(t *testing.T) {
	logger := zap.NewNop()
	tmp := t.TempDir()
	cfg := config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    tmp + "/orvix_sec.db?_loc=auto&_busy_timeout=5000&_txlock=immediate",
	}
	db, err := config.NewDatabase(&cfg, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}()
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	hash, _ := HashPassword("pwd")
	db.Exec("INSERT INTO users (created_at, updated_at, email, password_hash, role) VALUES (?, ?, ?, ?, ?)",
		time.Now(), time.Now(), "admin@test.com", hash, "platform_super_admin")

	authCfg := &config.AuthConfig{JWTKeyPath: tmp + "/key.pem"}
	authInst, err := NewAuthenticator(authCfg, db, logger)
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	rm := NewReauthManager(db, logger, authInst)

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", uint(1))
		c.Locals("session_id", "session-1")
		c.Locals("tenant_id", uint(1))
		return c.Next()
	})

	rs := RouteSecurity{
		RequiredScope: ScopeBackupRestore,
		ReauthManager: rm,
	}
	app.Post("/backup/restore", rs.Build(), func(c fiber.Ctx) error {
		return c.SendStatus(200)
	})

	// Missing grant should fail.
	req := httptest.NewRequest("POST", "/backup/restore", nil)
	resp, _ := app.Test(req, fiber.TestConfig{Timeout: time.Second})
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected 403 for missing grant, got %d", resp.StatusCode)
	}

	// Create grant.
	rm.CreateGrant(1, "session-1", 1, ScopeBackupRestore)

	req2 := httptest.NewRequest("POST", "/backup/restore", nil)
	resp2, _ := app.Test(req2, fiber.TestConfig{Timeout: time.Second})
	if resp2.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200 with valid grant, got %d", resp2.StatusCode)
	}
}

func TestRouteSecurity_Build_NoReauth(t *testing.T) {
	rs := RouteSecurity{}
	handler := rs.Build()
	// Should just call next with no check.
	if handler == nil {
		t.Fatal("expected non-nil handler")
	}
}
