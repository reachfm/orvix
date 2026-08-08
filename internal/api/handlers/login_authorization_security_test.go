package handlers_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"

	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
)

type loginAuthHarness struct {
	router  *api.Router
	sqlDB   *sql.DB
	tenantA uint
	authn   *auth.Authenticator
}

func newLoginAuthHarness(t *testing.T) *loginAuthHarness {
	t.Helper()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "loginauth.db") + "?_loc=auto&_busy_timeout=5000&_journal_mode=WAL"
	cfg.Auth.JWTSecret = "test-secret-64-bytes-min-login-auth-matrix-fixture-XXX"
	cfg.Auth.JWTKeyPath = filepath.Join(t.TempDir(), "jwt.pem")
	cfg.Auth.LoginRateLimit = 10000

	logger := zap.NewNop()
	gdb, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	if err := models.MigrateAllRaw(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, _ := gdb.DB()
	t.Cleanup(func() { sqlDB.Close() })

	authn, err := auth.NewAuthenticator(&cfg.Auth, gdb, logger)
	if err != nil {
		t.Fatalf("authn: %v", err)
	}

	adminUI := filepath.Join(t.TempDir(), "admin")
	webmailUI := filepath.Join(t.TempDir(), "webmail")
	os.MkdirAll(adminUI, 0755)
	os.MkdirAll(webmailUI, 0755)
	cfg.Server.AdminUIDir = adminUI
	cfg.Server.WebmailUIDir = webmailUI

	router := api.NewRouter(cfg, authn, logger, gdb, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() { router.App().Shutdown() })

	now := time.Now().UTC()
	res, _ := sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active, created_at, updated_at) VALUES ('a', 'a', 'a.example', 'enterprise', 1, ?, ?)`, now, now)
	tid, _ := res.LastInsertId()

	return &loginAuthHarness{router: router, sqlDB: sqlDB, tenantA: uint(tid), authn: authn}
}

func (h *loginAuthHarness) seedUser(t *testing.T, email, role string, tenantID *uint, active bool, deleted bool) uint {
	t.Helper()
	now := time.Now().UTC()
	hash, _ := auth.HashPassword("TestPass!2026")
	var tid interface{}
	if tenantID == nil {
		tid = nil
	} else {
		tid = *tenantID
	}
	var del interface{}
	if deleted {
		del = now
	} else {
		del = nil
	}
	res, err := h.sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, deleted_at) VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		now, now, email, hash, role, tid, active, del,
	)
	if err != nil {
		t.Fatalf("seed %s %s: %v", email, role, err)
	}
	id, _ := res.LastInsertId()
	return uint(id)
}

func (h *loginAuthHarness) doLogin(t *testing.T, email, password string) (int, []byte, []*http.Cookie) {
	t.Helper()
	body := `{"email":"` + email + `","password":"` + password + `"}`
	req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// Use a unique client IP per login to avoid the login rate limiter
	// (5 / 15 min per IP) from tripping during the matrix.
	req.RemoteAddr = uniqueLoginIP()
	req.Header.Set("X-Forwarded-For", uniqueLoginIP())
	resp, err := h.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login %s: %v", email, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, resp.Cookies()
}

var loginIPCounter int

func uniqueLoginIP() string {
	loginIPCounter++
	return fmt.Sprintf("10.0.%d.%d:%d", (loginIPCounter/250)%250, loginIPCounter%250, 40000+loginIPCounter%5000)
}

func TestLoginCanonicalAuthorizationMatrix(t *testing.T) {
	// Each subtest uses a fresh router so the hardcoded login rate limiter
	// (Max:5 per 15 min) cannot trip across the whole matrix.
	allowed := []struct {
		role string
	}{
		{"platform_super_admin"},
		{"tenant_admin"},
		{"tenant_operator"},
		{"tenant_support"},
		{"tenant_readonly"},
		{"user"},
		{"billing"},
	}
	for _, tc := range allowed {
		t.Run("allow_"+tc.role, func(t *testing.T) {
			h := newLoginAuthHarness(t)
			var tid *uint
			if tc.role != "platform_super_admin" {
				tid = &h.tenantA
			}
			email := tc.role + "@test.local"
			uid := h.seedUser(t, email, tc.role, tid, true, false)
			status, raw, _ := h.doLogin(t, email, "TestPass!2026")
			if status != 200 {
				t.Fatalf("login %s: want 200, got %d body=%s", tc.role, status, raw)
			}
			var out struct {
				AccessToken string `json:"access_token"`
			}
			json.Unmarshal(raw, &out)
			if out.AccessToken == "" {
				t.Fatalf("login %s: no access token", tc.role)
			}
			_, role, err := h.authn.ValidateAccessToken(out.AccessToken)
			if err != nil {
				t.Fatalf("login %s: token invalid: %v", tc.role, err)
			}
			if string(role) != tc.role {
				t.Fatalf("login %s: JWT role=%s want %s", tc.role, role, tc.role)
			}
			_ = uid
		})
	}

	denied := []struct {
		name string
		role string
		null bool
	}{
		{"psa_with_tenant", "platform_super_admin", false},
		{"ta_null", "tenant_admin", true},
		{"to_null", "tenant_operator", true},
		{"ts_null", "tenant_support", true},
		{"tro_null", "tenant_readonly", true},
		{"user_null", "user", true},
		{"billing_null", "billing", true},
		{"admin", "admin", false},
		{"superadmin", "superadmin", false},
		{"super_admin", "super_admin", false},
		{"super_admin_hyphen", "super-admin", false},
		{"operator", "operator", false},
		{"readonly", "readonly", false},
		{"unknown", "unknown_role_zzz", false},
		{"empty", "", false},
	}
	for _, tc := range denied {
		t.Run("deny_"+tc.name, func(t *testing.T) {
			h := newLoginAuthHarness(t)
			email := "deny" + tc.name + "@test.local"
			var tid *uint
			if !tc.null {
				tid = &h.tenantA
			}
			if tc.role != "" {
				h.seedUser(t, email, tc.role, tid, true, false)
			}
			status, raw, cookies := h.doLogin(t, email, "TestPass!2026")
			if status >= 500 {
				t.Fatalf("%s: 5xx %d", tc.name, status)
			}
			if status != 401 {
				t.Fatalf("%s: want 401, got %d body=%s", tc.name, status, raw)
			}
			if !strings.Contains(string(raw), `"error":"invalid credentials"`) {
				t.Fatalf("%s: wrong body: %s", tc.name, raw)
			}
			if len(cookies) > 0 {
				t.Fatalf("%s: got cookies on denied login", tc.name)
			}
		})
	}

	// Status-based denials.
	for _, tc := range []struct {
		name     string
		inactive bool
		deleted  bool
	}{
		{"inactive", true, false},
		{"deleted", false, true},
	} {
		t.Run("deny_"+tc.name, func(t *testing.T) {
			h := newLoginAuthHarness(t)
			email := "status" + tc.name + "@test.local"
			h.seedUser(t, email, "tenant_admin", &h.tenantA, !tc.inactive, tc.deleted)
			status, raw, _ := h.doLogin(t, email, "TestPass!2026")
			if status != 401 || !strings.Contains(string(raw), `"error":"invalid credentials"`) {
				t.Fatalf("%s: want 401 invalid-credentials, got %d body=%s", tc.name, status, raw)
			}
		})
	}

	// Missing user.
	t.Run("deny_missing_user", func(t *testing.T) {
		h := newLoginAuthHarness(t)
		status, raw, _ := h.doLogin(t, "missing@test.local", "TestPass!2026")
		if status != 401 || !strings.Contains(string(raw), `"error":"invalid credentials"`) {
			t.Fatalf("missing user: want 401, got %d body=%s", status, raw)
		}
	})

	// Wrong password.
	t.Run("deny_wrong_password", func(t *testing.T) {
		h := newLoginAuthHarness(t)
		h.seedUser(t, "wrongpass@test.local", "tenant_admin", &h.tenantA, true, false)
		status, raw, _ := h.doLogin(t, "wrongpass@test.local", "WrongPass!2026")
		if status != 401 || !strings.Contains(string(raw), `"error":"invalid credentials"`) {
			t.Fatalf("wrong password: want 401, got %d body=%s", status, raw)
		}
	})
}
