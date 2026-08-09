package handlers_test

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
)

func buildFirewallHarness(t *testing.T) (*api.Router, *sql.DB, string, string) {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	root := t.TempDir()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(root, "orvix.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	if _, err := authenticator.HashPassword("TestPassword123!"); err != nil {
		t.Fatalf("hash: %v", err)
	}
	seedPlatformSuperAdminWithPassword(t, sqlDB, "admin@test.local", "TestPassword123!")

	router := api.NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	token := loginFirewall(t, router)
	csrf := csrfFirewall(t, router, token)
	return router, sqlDB, token, csrf
}

func loginFirewall(t *testing.T, router *api.Router) string {
	t.Helper()
	req := httptest.NewRequest("POST", "/admin/login", strings.NewReader(`{"username":"admin@test.local","password":"TestPassword123!"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("login status %d: %s", resp.StatusCode, body)
	}
	var data struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if data.AccessToken == "" {
		t.Fatal("missing access token")
	}
	return data.AccessToken
}

func csrfFirewall(t *testing.T, router *api.Router, token string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/csrf-token", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("csrf: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("csrf status %d: %s", resp.StatusCode, body)
	}
	var data struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("decode csrf: %v", err)
	}
	return data.CSRFToken
}

func firewallRequest(t *testing.T, router *api.Router, method, path, body, token, csrf string) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if csrf != "" {
		req.Header.Set("Cookie", "csrf_token="+csrf)
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, data
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestFirewallV1_CreateRuleFailsClosed proves POST /api/v1/firewall/rules
// no longer writes to a table nothing in the mail path consults.
// internal/firewall.Module.Start never calls LoadRules and no
// SMTP/CoreMail code holds a reference to RuleEngine — CoreMail
// enforces policy via internal/ruler instead. The endpoint must fail
// closed (410, a stable error code) rather than let an operator
// believe a created rule is protecting live traffic.
func TestFirewallV1_CreateRuleFailsClosed(t *testing.T) {
	router, sqlDB, token, csrf := buildFirewallHarness(t)
	defer sqlDB.Close()

	before := countRows(t, sqlDB, "firewall_rules")
	auditBefore := countRows(t, sqlDB, "orvix_audit")

	resp, body := firewallRequest(t, router, "POST", "/api/v1/firewall/rules",
		`{"name":"block-bad-ip","condition":"sender_ip = 1.2.3.4","action":"block","priority":5,"enabled":true}`,
		token, csrf)

	if resp.StatusCode != http.StatusGone {
		t.Fatalf("expected 410 Gone, got %d: %s", resp.StatusCode, body)
	}
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode response: %v, body=%s", err, body)
	}
	if payload.Code != "FIREWALL_RULE_ENGINE_NOT_OPERATIONAL" {
		t.Fatalf("expected stable code FIREWALL_RULE_ENGINE_NOT_OPERATIONAL, got %q", payload.Code)
	}

	after := countRows(t, sqlDB, "firewall_rules")
	if after != before {
		t.Fatalf("firewall_rules row count changed: before=%d after=%d — a fail-closed response must insert nothing", before, after)
	}

	auditAfter := countRows(t, sqlDB, "orvix_audit")
	if auditAfter != auditBefore {
		t.Fatalf("an audit row was written for a request that mutated nothing: before=%d after=%d", auditBefore, auditAfter)
	}
}

// TestFirewallV1_ListRulesStillReadable proves the read path (existing
// legacy rule records, if any) remains available — only the write
// path was retired.
func TestFirewallV1_ListRulesStillReadable(t *testing.T) {
	router, sqlDB, token, csrf := buildFirewallHarness(t)
	defer sqlDB.Close()
	resp, body := firewallRequest(t, router, "GET", "/api/v1/firewall/rules", "", token, csrf)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status %d: %s", resp.StatusCode, body)
	}
}
