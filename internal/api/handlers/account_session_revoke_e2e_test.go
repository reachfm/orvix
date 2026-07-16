package handlers_test

// End-to-end proof, through the real HTTP account route, that revoking a
// session immediately invalidates that session's bearer JWT while leaving an
// unrelated session valid, that cross-user revoke is denied, and that a
// duplicate revoke is safe.

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api/handlers"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
	_ "modernc.org/sqlite"
)

func TestAccountSessionRevokeE2E(t *testing.T) {
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "sess-e2e.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.Auth.JWTKeyPath = filepath.Join(t.TempDir(), "jwt_key.pem")

	gdb, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sqlDB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	authr, err := auth.NewAuthenticator(&cfg.Auth, gdb, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	h := handlers.NewHandler(gdb, authr, nil, logger, cfg, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)

	// Two independent sessions for the same user (5): each mints an access
	// token whose jti is persisted on its session row.
	const uid = uint(5)
	tokenA, jtiA, _, err := authr.GenerateAccessTokenWithJTI(uid, auth.RoleAdmin)
	if err != nil {
		t.Fatalf("issue A: %v", err)
	}
	if _, _, err := authr.GenerateRefreshToken(uid, jtiA); err != nil {
		t.Fatalf("refresh A: %v", err)
	}
	tokenB, jtiB, _, err := authr.GenerateAccessTokenWithJTI(uid, auth.RoleAdmin)
	if err != nil {
		t.Fatalf("issue B: %v", err)
	}
	if _, _, err := authr.GenerateRefreshToken(uid, jtiB); err != nil {
		t.Fatalf("refresh B: %v", err)
	}

	sessionID := func(jti string) uint {
		var id uint
		if err := sqlDB.QueryRow("SELECT id FROM sessions WHERE jti = ?", jti).Scan(&id); err != nil {
			t.Fatalf("session id for jti: %v", err)
		}
		return id
	}
	idA := sessionID(jtiA)

	// App with a middleware that authenticates the caller as the configured
	// user id (the auth middleware itself is proven separately; here we drive
	// the real RevokeAccountSession handler).
	var caller uint
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", caller)
		c.Locals("tenant_id", uint(1))
		c.Locals("email", "u@orvix.email")
		return c.Next()
	})
	app.Post("/account/sessions/:id/revoke", h.RevokeAccountSession)

	revoke := func(t *testing.T, id, asUser uint) int {
		caller = asUser
		req := httptest.NewRequest(http.MethodPost, "/account/sessions/"+strconv.FormatUint(uint64(id), 10)+"/revoke", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		return resp.StatusCode
	}

	// Both bearer tokens authenticate before revocation.
	if _, _, err := authr.ValidateAccessToken(tokenA); err != nil {
		t.Fatalf("A should authenticate before revoke: %v", err)
	}
	if _, _, err := authr.ValidateAccessToken(tokenB); err != nil {
		t.Fatalf("B should authenticate before revoke: %v", err)
	}

	// Cross-user revoke of A (as user 6) must be a safe not-found, and must NOT
	// revoke A.
	if code := revoke(t, idA, 6); code != http.StatusNotFound {
		t.Fatalf("cross-user revoke: got %d, want 404", code)
	}
	if _, _, err := authr.ValidateAccessToken(tokenA); err != nil {
		t.Fatalf("A must still be valid after a denied cross-user revoke: %v", err)
	}

	// User 5 revokes their own session A through the HTTP route.
	if code := revoke(t, idA, uid); code != http.StatusOK {
		t.Fatalf("revoke A: got %d, want 200", code)
	}

	// A's bearer JWT fails immediately; B remains valid.
	if _, _, err := authr.ValidateAccessToken(tokenA); err == nil {
		t.Fatal("session A bearer JWT must fail immediately after revoke")
	}
	if _, _, err := authr.ValidateAccessToken(tokenB); err != nil {
		t.Fatalf("session B bearer JWT must remain valid: %v", err)
	}

	// Duplicate revoke of the now-deleted session A is safe (404, no panic).
	if code := revoke(t, idA, uid); code != http.StatusNotFound {
		t.Fatalf("duplicate revoke: got %d, want 404", code)
	}

	// The revoked jti is recorded until at least the JWT's natural expiry.
	var expUnix int64
	if err := sqlDB.QueryRow("SELECT expires_at FROM revoked_tokens WHERE jti = ?", jtiA).Scan(&expUnix); err != nil {
		t.Fatalf("revoked_tokens must contain jti A: %v", err)
	}
	if time.Unix(expUnix, 0).Before(time.Now()) {
		t.Fatal("revoked token expiry must be in the future (>= JWT natural expiry)")
	}

}
