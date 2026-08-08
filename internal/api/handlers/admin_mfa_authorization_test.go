package handlers_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
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

type mfaAuthEnv struct {
	router *api.Router
	sqlDB  *sql.DB
	authn  *auth.Authenticator
}

func newMFAAuthEnv(t *testing.T) *mfaAuthEnv {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "mfauth.db") + "?_loc=auto&_busy_timeout=5000&_journal_mode=WAL"
	cfg.Auth.JWTKeyPath = filepath.Join(t.TempDir(), "jwt.pem")
	cfg.Auth.LoginRateLimit = 10000

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { sqlDB.Close() })

	authn, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authn: %v", err)
	}

	router := api.NewRouter(cfg, authn, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() { router.App().Shutdown() })

	return &mfaAuthEnv{router: router, sqlDB: sqlDB, authn: authn}
}

func (e *mfaAuthEnv) seedUser(t *testing.T, email, role string, tenantID *uint, active, deleted bool) {
	t.Helper()
	now := time.Now().UTC()
	hash, _ := auth.HashPassword("Pass!2026")
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
	_, err := e.sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified, deleted_at, mfa_enabled) VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, 0)`,
		now, now, email, hash, role, tid, active, del,
	)
	if err != nil {
		t.Fatalf("seed %s: %v", email, err)
	}
}

func (e *mfaAuthEnv) doLogin(t *testing.T, email, password string) (int, []byte) {
	t.Helper()
	body := fmt.Sprintf(`{"email":"%s","password":"%s"}`, email, password)
	req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// TestMFAPreChallengeAuthorizationMatrix proves that invalid roles/tenant
// bindings are denied at login with the exact wrong-password contract. A
// valid canonical role succeeds (with or without MFA). Each denied case
// uses a fresh harness to avoid the login rate limiter.
func TestMFAPreChallengeAuthorizationMatrix(t *testing.T) {
	tenant := uint(1)

	// Valid user that CAN log in — proves the harness works and that the
	// canonical role succeeds.
	validBase := newMFAAuthEnv(t)
	validBase.sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active) VALUES ('a','a','a.ex','enterprise',1)`)
	validBase.seedUser(t, "valid@mfa.test", "tenant_admin", &tenant, true, false)
	status, body := validBase.doLogin(t, "valid@mfa.test", "Pass!2026")
	if status != 200 {
		t.Fatalf("valid user: want 200, got %d body=%s", status, body)
	}
	var validResp struct {
		AccessToken string `json:"access_token"`
	}
	json.Unmarshal(body, &validResp)
	if validResp.AccessToken == "" {
		t.Fatalf("valid user: no access token: %s", body)
	}

	denied := []struct {
		name    string
		role    string
		tid     *uint
		active  bool
		deleted bool
	}{
		{"admin", "admin", &tenant, true, false},
		{"superadmin", "superadmin", &tenant, true, false},
		{"super_admin", "super_admin", &tenant, true, false},
		{"super_admin_hyphen", "super-admin", &tenant, true, false},
		{"operator", "operator", &tenant, true, false},
		{"readonly", "readonly", &tenant, true, false},
		{"unknown", "unknown_role", &tenant, true, false},
		{"empty", "", &tenant, true, false},
		{"psa_with_tenant", "platform_super_admin", &tenant, true, false},
		{"ta_null", "tenant_admin", nil, true, false},
		{"inactive", "tenant_admin", &tenant, false, false},
		{"deleted", "tenant_admin", &tenant, true, true},
	}
	for _, tc := range denied {
		t.Run(tc.name, func(t *testing.T) {
			e := newMFAAuthEnv(t)
			e.sqlDB.Exec(`INSERT INTO tenants (name, slug, domain, plan, active) VALUES ('a','a','a.ex','enterprise',1)`)
			email := tc.name + "@mfa.test"
			e.seedUser(t, email, tc.role, tc.tid, tc.active, tc.deleted)
			status, body := e.doLogin(t, email, "Pass!2026")
			if status >= 500 {
				t.Fatalf("%s: 5xx %d", tc.name, status)
			}
			if status != 401 {
				t.Fatalf("%s: want 401, got %d body=%s", tc.name, status, body)
			}
			if !strings.Contains(string(body), `"error":"invalid credentials"`) {
				t.Fatalf("%s: wrong body: %s", tc.name, body)
			}
		})
	}
}
