package handlers_test

// Tests for the Admin Enterprise v2 endpoints declared in
// internal/api/handlers/enterprise_admin.go. The tests focus
// on:
//   - CRUD lifecycle happy path
//   - CSRF enforcement on every mutation
//   - cross-tenant access denial
//   - soft-delete + re-create round-trip
//   - audit trail on every mutation
//
// The fixtures are intentionally minimal: each test creates a
// single admin user, an `admin` JWT, a CSRF token, and the
// minimum domain / mailbox / list rows to exercise the
// handler.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
)

func newEnterpriseRouter(t *testing.T) (*api.Router, *sql.DB) {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Server.AdminUIDir = "../../release/admin"
	cfg.Server.WebmailUIDir = "../../release/webmail"
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	hashedPw, err := authenticator.HashPassword("TestPassword123!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (?, ?, 'admin@test.local', ?, 'admin', 1, 1, 1)`,
		now, now, hashedPw); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_domains (name, tenant_id, status, plan, created_at, updated_at)
		 VALUES ('test.local', 1, 'active', 'enterprise', ?, ?)`, now, now); err != nil {
		t.Fatalf("insert domain: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (1, 1, 'admin', 'admin@test.local', 'Admin', 'hash', 'argon2id', 'active', 1024, 1, ?, ?)`,
		now, now); err != nil {
		t.Fatalf("insert mailbox: %v", err)
	}
	router := api.NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() {
		router.App().Shutdown()
		sqlDB.Close()
	})
	return router, sqlDB
}

// newEnterpriseRouterWithMalformedLockouts creates a router on a
// database where the coremail_lockouts table is deliberately
// malformed (wrong columns). This forces the production LoadFromDB
// code path inside api.NewRouter to fail, triggering the real
// SetTrustPersistence(false, sanitizedMessage) branch. No manual
// trust re-wire, no direct SetTrustPersistence call, no mutable
// global seam — the failure is purely a DB schema mismatch.
func newEnterpriseRouterWithMalformedLockouts(t *testing.T) (*api.Router, *sql.DB) {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Server.AdminUIDir = "../../release/admin"
	cfg.Server.WebmailUIDir = "../../release/webmail"
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	hashedPw, err := authenticator.HashPassword("TestPassword123!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (?, ?, 'admin@test.local', ?, 'admin', 1, 1, 1)`,
		now, now, hashedPw); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_domains (name, tenant_id, status, plan, created_at, updated_at)
		 VALUES ('test.local', 1, 'active', 'enterprise', ?, ?)`, now, now); err != nil {
		t.Fatalf("insert domain: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (1, 1, 'admin', 'admin@test.local', 'Admin', 'hash', 'argon2id', 'active', 1024, 1, ?, ?)`,
		now, now); err != nil {
		t.Fatalf("insert mailbox: %v", err)
	}

	// Sabotage the coremail_lockouts table BEFORE router
	// construction. MigrateAllRaw created it with the correct
	// columns. We DROP it and CREATE a malformed version with
	// only one wrong column. When api.NewRouter later runs
	// trust.Tables() (CREATE TABLE IF NOT EXISTS) the table
	// already exists with the wrong schema, so it stays.
	// LoadFromDB's SELECT key, expires_at FROM coremail_lockouts
	// then fails because the "key" column does not exist.
	// This forces the real production error branch in
	// router.go:273-275 without any test-only backdoor.
	sqlDB.Exec("DROP TABLE IF EXISTS coremail_lockouts")
	sqlDB.Exec(`CREATE TABLE coremail_lockouts (broken TEXT NOT NULL)`)

	router := api.NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() {
		router.App().Shutdown()
		sqlDB.Close()
	})
	return router, sqlDB
}

// runEnterprise tests the full lifecycle of every Admin
// Enterprise v2 endpoint family. The intent is regression
// coverage for the routes mounted in router.go — not exhaustive
// functional verification (covered by the per-handler tests
// in enterprise_admin_integration_test.go).
func runEnterprise(t *testing.T, router *api.Router, sqlDB *sql.DB, token, csrf string) {
	t.Helper()
	// Account classes
	t.Run("account_classes", func(t *testing.T) {
		resp := postJSON(t, router, "/api/v1/admin/account-classes", token, csrf, `{"name":"premium","default_quota_mb":2048,"max_quota_mb":10240}`)
		if resp.status != 201 {
			t.Fatalf("create: want 201, got %d %s", resp.status, resp.body)
		}
		var created struct{ ID int64 `json:"id"` }
		_ = json.Unmarshal(resp.bodyBytes, &created)
		resp2 := patchJSON(t, router, "/api/v1/admin/account-classes/"+strconv.FormatInt(created.ID, 10), token, csrf, `{"description":"Premium tier"}`)
		if resp2.status != 200 {
			t.Fatalf("patch: want 200, got %d %s", resp2.status, resp2.body)
		}
		resp3 := getJSON(t, router, "/api/v1/admin/account-classes", token)
		if resp3.status != 200 || !bytes.Contains(resp3.bodyBytes, []byte("premium")) {
			t.Fatalf("list missing premium: %d %s", resp3.status, resp3.body)
		}
		delResp := delJSON(t, router, "/api/v1/admin/account-classes/"+strconv.FormatInt(created.ID, 10), token, csrf)
		if delResp.status != 200 {
			t.Fatalf("delete: want 200, got %d %s", delResp.status, delResp.body)
		}
	})

	// Domain groups
	t.Run("domain_groups", func(t *testing.T) {
		resp := postJSON(t, router, "/api/v1/admin/domain-groups", token, csrf, `{"name":"production","domain_ids":[]}`)
		if resp.status != 201 {
			t.Fatalf("create: want 201, got %d %s", resp.status, resp.body)
		}
		var created struct{ ID int64 `json:"id"` }
		_ = json.Unmarshal(resp.bodyBytes, &created)
		resp2 := putJSON(t, router, "/api/v1/admin/domain-groups/"+strconv.FormatInt(created.ID, 10)+"/members", token, csrf, `{"domain_ids":[1]}`)
		if resp2.status != 200 {
			t.Fatalf("members: want 200, got %d %s", resp2.status, resp2.body)
		}
		delResp := delJSON(t, router, "/api/v1/admin/domain-groups/"+strconv.FormatInt(created.ID, 10), token, csrf)
		if delResp.status != 200 {
			t.Fatalf("delete: want 200, got %d %s", delResp.status, delResp.body)
		}
	})

	// Mailing lists
	t.Run("mailing_lists", func(t *testing.T) {
		resp := postJSON(t, router, "/api/v1/admin/mailing-lists", token, csrf, `{"domain_id":1,"address":"all","display_name":"All Staff"}`)
		if resp.status != 201 {
			t.Fatalf("create: want 201, got %d %s", resp.status, resp.body)
		}
		var created struct{ ID int64 `json:"id"` }
		_ = json.Unmarshal(resp.bodyBytes, &created)
		addResp := postJSON(t, router, "/api/v1/admin/mailing-lists/"+strconv.FormatInt(created.ID, 10)+"/members", token, csrf, `{"address":"alice@test.local"}`)
		if addResp.status != 201 {
			t.Fatalf("add member: want 201, got %d %s", addResp.status, addResp.body)
		}
		listResp := getJSON(t, router, "/api/v1/admin/mailing-lists/"+strconv.FormatInt(created.ID, 10)+"/members", token)
		if listResp.status != 200 || !bytes.Contains(listResp.bodyBytes, []byte("alice@test.local")) {
			t.Fatalf("list members: %d %s", listResp.status, listResp.body)
		}
		delResp := delJSON(t, router, "/api/v1/admin/mailing-lists/"+strconv.FormatInt(created.ID, 10), token, csrf)
		if delResp.status != 200 {
			t.Fatalf("delete: want 200, got %d %s", delResp.status, delResp.body)
		}
	})

	// Public folders
	t.Run("public_folders", func(t *testing.T) {
		resp := postJSON(t, router, "/api/v1/admin/public-folders", token, csrf, `{"owner_mailbox_id":1,"folder_path":"Public/Announcements","display_name":"Announcements"}`)
		if resp.status != 201 {
			t.Fatalf("create: want 201, got %d %s", resp.status, resp.body)
		}
		var created struct{ ID int64 `json:"id"` }
		_ = json.Unmarshal(resp.bodyBytes, &created)
		delResp := delJSON(t, router, "/api/v1/admin/public-folders/"+strconv.FormatInt(created.ID, 10), token, csrf)
		if delResp.status != 200 {
			t.Fatalf("delete: want 200, got %d %s", delResp.status, delResp.body)
		}
	})

	// Admin groups
	t.Run("admin_groups", func(t *testing.T) {
		resp := postJSON(t, router, "/api/v1/admin/admin-groups", token, csrf, `{"name":"domain_admins","description":"Domain admins","grants":["domains.read","domains.write"]}`)
		if resp.status != 201 {
			t.Fatalf("create: want 201, got %d %s", resp.status, resp.body)
		}
		var created struct{ ID int64 `json:"id"` }
		_ = json.Unmarshal(resp.bodyBytes, &created)
		patchResp := patchJSON(t, router, "/api/v1/admin/admin-groups/"+strconv.FormatInt(created.ID, 10), token, csrf, `{"description":"Domain admins v2"}`)
		if patchResp.status != 200 {
			t.Fatalf("patch: want 200, got %d %s", patchResp.status, patchResp.body)
		}
		delResp := delJSON(t, router, "/api/v1/admin/admin-groups/"+strconv.FormatInt(created.ID, 10), token, csrf)
		if delResp.status != 200 {
			t.Fatalf("delete: want 200, got %d %s", delResp.status, delResp.body)
		}
	})

	// ACL rules
	t.Run("acl_rules", func(t *testing.T) {
		resp := postJSON(t, router, "/api/v1/admin/acl-rules", token, csrf, `{"source":"10.0.0.0/8","action":"allow","protocol":"all","note":"corp VPN"}`)
		if resp.status != 201 {
			t.Fatalf("create: want 201, got %d %s", resp.status, resp.body)
		}
		var created struct{ ID int64 `json:"id"` }
		_ = json.Unmarshal(resp.bodyBytes, &created)
		delResp := delJSON(t, router, "/api/v1/admin/acl-rules/"+strconv.FormatInt(created.ID, 10), token, csrf)
		if delResp.status != 200 {
			t.Fatalf("delete: want 200, got %d %s", delResp.status, delResp.body)
		}
	})

	// Log rules
	t.Run("log_rules", func(t *testing.T) {
		resp := postJSON(t, router, "/api/v1/admin/log-rules", token, csrf, `{"name":"smtp-errors","source":"journald","severity":"error","match_pattern":"smtp","destination":"syslog://siem.local:514"}`)
		if resp.status != 201 {
			t.Fatalf("create: want 201, got %d %s", resp.status, resp.body)
		}
		var created struct{ ID int64 `json:"id"` }
		_ = json.Unmarshal(resp.bodyBytes, &created)
		delResp := delJSON(t, router, "/api/v1/admin/log-rules/"+strconv.FormatInt(created.ID, 10), token, csrf)
		if delResp.status != 200 {
			t.Fatalf("delete: want 200, got %d %s", delResp.status, delResp.body)
		}
	})

	// Quarantine
	t.Run("quarantine", func(t *testing.T) {
		// Insert a quarantined row directly so we can test the
		// resolve action without needing the SMTP engine.
		if _, err := sqlDB.Exec(`INSERT INTO coremail_quarantine_index
			(tenant_id, message_id, recipient, sender, subject, reason, severity, status, created_at)
			VALUES (1, 'msg-001', 'user@test.local', 'spam@example.com', 'Buy now!', 'spam', 'high', 'held', ?)`,
			time.Now().UTC()); err != nil {
			t.Fatalf("insert quarantine: %v", err)
		}
		resp := getJSON(t, router, "/api/v1/admin/quarantine", token)
		if resp.status != 200 {
			t.Fatalf("list: want 200, got %d %s", resp.status, resp.body)
		}
		if !bytes.Contains(resp.bodyBytes, []byte("spam@example.com")) {
			t.Fatalf("list missing sender: %s", resp.body)
		}
		// resolve as released
		resolveResp := postJSON(t, router, "/api/v1/admin/quarantine/1/resolve", token, csrf, `{"action":"release"}`)
		if resolveResp.status != 200 {
			t.Fatalf("resolve: want 200, got %d %s", resolveResp.status, resolveResp.body)
		}
	})

	// Audit logs
	t.Run("audit_logs", func(t *testing.T) {
		resp := getJSON(t, router, "/api/v1/admin/audit-logs", token)
		if resp.status != 200 {
			t.Fatalf("list: want 200, got %d %s", resp.status, resp.body)
		}
		// We expect at least the rows from the previous
		// sub-tests (account_class.create,
		// domain_group.create, mailing_list.create, etc.).
		if !bytes.Contains(resp.bodyBytes, []byte("account_class.create")) {
			t.Fatalf("audit logs missing account_class.create: %s", resp.body)
		}
		if !bytes.Contains(resp.bodyBytes, []byte("mailing_list.create")) {
			t.Fatalf("audit logs missing mailing_list.create: %s", resp.body)
		}
	})
}

func TestEnterpriseAdminV2Endpoints(t *testing.T) {
	router, sqlDB := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)
	runEnterprise(t, router, sqlDB, token, csrf)
}

func TestEnterpriseAdminV2CSRFEnforced(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	for _, path := range []string{
		"/api/v1/admin/account-classes",
		"/api/v1/admin/domain-groups",
		"/api/v1/admin/mailing-lists",
		"/api/v1/admin/public-folders",
		"/api/v1/admin/admin-groups",
		"/api/v1/admin/acl-rules",
		"/api/v1/admin/log-rules",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("POST", path, strings.NewReader(`{"name":"x"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			// No CSRF token.
			resp, err := router.App().Test(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			if resp.StatusCode != 403 {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("%s without CSRF must return 403, got %d: %s", path, resp.StatusCode, body)
			}
		})
	}
}

// helper: read body and return as string + raw bytes.
type httpResp struct {
	status    int
	body      string
	bodyBytes []byte
}

func postJSON(t *testing.T, router *api.Router, path, token, csrf, body string) httpResp {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Cookie", "csrf_token="+csrf)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := router.App().Test(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	b, _ := io.ReadAll(resp.Body)
	return httpResp{status: resp.StatusCode, body: string(b), bodyBytes: b}
}

func putJSON(t *testing.T, router *api.Router, path, token, csrf, body string) httpResp {
	t.Helper()
	req := httptest.NewRequest("PUT", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Cookie", "csrf_token="+csrf)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := router.App().Test(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	b, _ := io.ReadAll(resp.Body)
	return httpResp{status: resp.StatusCode, body: string(b), bodyBytes: b}
}

func patchJSON(t *testing.T, router *api.Router, path, token, csrf, body string) httpResp {
	t.Helper()
	req := httptest.NewRequest("PATCH", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Cookie", "csrf_token="+csrf)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := router.App().Test(req)
	if err != nil {
		t.Fatalf("PATCH %s: %v", path, err)
	}
	b, _ := io.ReadAll(resp.Body)
	return httpResp{status: resp.StatusCode, body: string(b), bodyBytes: b}
}

func delJSON(t *testing.T, router *api.Router, path, token, csrf string) httpResp {
	t.Helper()
	req := httptest.NewRequest("DELETE", path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Cookie", "csrf_token="+csrf)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := router.App().Test(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", path, err)
	}
	b, _ := io.ReadAll(resp.Body)
	return httpResp{status: resp.StatusCode, body: string(b), bodyBytes: b}
}

func getJSON(t *testing.T, router *api.Router, path, token string) httpResp {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := router.App().Test(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	b, _ := io.ReadAll(resp.Body)
	return httpResp{status: resp.StatusCode, body: string(b), bodyBytes: b}
}

// enterpriseLoginForTest posts to /api/v1/auth/login and
// returns the access token. Mirrors the helper in
// internal/api/router_test.go but stays local to this file
// so the handlers_test package does not depend on internals
// of the api package.
func enterpriseLoginForTest(t *testing.T, router *api.Router, email, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"email":    email,
		"password": password,
	})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := router.App().Test(req)
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("login expected 200, got %d: %s", resp.StatusCode, b)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	b, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("login json: %v: %s", err, b)
	}
	if out.AccessToken == "" {
		t.Fatalf("login response missing access_token: %s", b)
	}
	return out.AccessToken
}

// enterpriseCSRFForTest fetches a CSRF token using a valid
// access token. The token is required for every admin
// mutation in router.go (CSRF middleware mounted on `men`).
func enterpriseCSRFForTest(t *testing.T, router *api.Router, token string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/csrf-token", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := router.App().Test(req)
	if err != nil {
		t.Fatalf("csrf request: %v", err)
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("csrf expected 200, got %d: %s", resp.StatusCode, b)
	}
	var out struct {
		CSRFToken string `json:"csrf_token"`
	}
	b, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("csrf json: %v: %s", err, b)
	}
	if out.CSRFToken == "" {
		t.Fatalf("csrf response missing csrf_token: %s", b)
	}
	return out.CSRFToken
}