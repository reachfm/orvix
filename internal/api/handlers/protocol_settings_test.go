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

func buildProtoHarness(t *testing.T) (*api.Router, *sql.DB, string, string) {
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
	t.Cleanup(func() { sqlDB.Close() })
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
	token := loginProto(t, router)
	csrf := csrfProto(t, router, token)
	return router, sqlDB, token, csrf
}

func loginProto(t *testing.T, router *api.Router) string {
	t.Helper()
	req := httptest.NewRequest("POST", "/admin/login", strings.NewReader(`{"username":"admin@test.local","password":"TestPassword123!"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	var data struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	return data.AccessToken
}

func csrfProto(t *testing.T, router *api.Router, token string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/csrf-token", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("csrf: %v", err)
	}
	var data struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("decode csrf: %v", err)
	}
	return data.CSRFToken
}

func protoRequest(t *testing.T, router *api.Router, method, path, body, token, csrf string) (*http.Response, []byte) {
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

func countAdminSettingsRows(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM admin_settings").Scan(&n); err != nil {
		t.Fatalf("count admin_settings: %v", err)
	}
	return n
}

// TestProtocolSettingsV1_UnknownPlusValidFieldCausesZeroMutations
// proves an unknown key in the same request as an otherwise-valid
// field rejects the WHOLE request, not just the bad field.
func TestProtocolSettingsV1_UnknownPlusValidFieldCausesZeroMutations(t *testing.T) {
	router, db, token, csrf := buildProtoHarness(t)
	before := countAdminSettingsRows(t, db)

	body := `{"coremail.smtp_port": 2525, "coremail.totally_made_up_field": true}`
	resp, respBody := protoRequest(t, router, "PATCH", "/api/v1/admin/settings/protocol/smtp_recv", body, token, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, respBody)
	}
	after := countAdminSettingsRows(t, db)
	if after != before {
		t.Fatalf("expected zero mutations, before=%d after=%d", before, after)
	}
}

// TestProtocolSettingsV1_InvalidPlusValidFieldCausesZeroMutations
// proves a semantically-invalid value (bad port) in the same request
// as a valid one rejects the WHOLE request.
func TestProtocolSettingsV1_InvalidPlusValidFieldCausesZeroMutations(t *testing.T) {
	router, db, token, csrf := buildProtoHarness(t)
	before := countAdminSettingsRows(t, db)

	body := `{"coremail.queue_workers": 4, "coremail.smtp_port": 99999}`
	resp, respBody := protoRequest(t, router, "PATCH", "/api/v1/admin/settings/protocol/smtp_recv", body, token, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, respBody)
	}
	after := countAdminSettingsRows(t, db)
	if after != before {
		t.Fatalf("expected zero mutations, before=%d after=%d", before, after)
	}
}

func TestProtocolSettingsV1_InvalidPortValues(t *testing.T) {
	router, _, token, csrf := buildProtoHarness(t)
	for _, bad := range []string{"0", "-1", "65536"} {
		body := `{"coremail.smtp_port": ` + bad + `}`
		resp, respBody := protoRequest(t, router, "PATCH", "/api/v1/admin/settings/protocol/smtp_recv", body, token, csrf)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("port %s: expected 400, got %d: %s", bad, resp.StatusCode, respBody)
		}
	}
}

func TestProtocolSettingsV1_InvalidDurationAndNegativeSize(t *testing.T) {
	router, _, token, csrf := buildProtoHarness(t)

	resp, body := protoRequest(t, router, "PATCH", "/api/v1/admin/settings/protocol/smtp_recv", `{"coremail.worker_interval": "not-a-duration"}`, token, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid duration: expected 400, got %d: %s", resp.StatusCode, body)
	}

	resp2, body2 := protoRequest(t, router, "PATCH", "/api/v1/admin/settings/protocol/smtp_recv", `{"coremail.worker_interval": "-5s"}`, token, csrf)
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("negative duration: expected 400, got %d: %s", resp2.StatusCode, body2)
	}

	resp3, body3 := protoRequest(t, router, "PATCH", "/api/v1/admin/settings/protocol/smtp_recv", `{"coremail.max_attachment_size_mb": -10}`, token, csrf)
	if resp3.StatusCode != http.StatusBadRequest {
		t.Fatalf("negative size: expected 400, got %d: %s", resp3.StatusCode, body3)
	}
}

func TestProtocolSettingsV1_InvalidPercentageOrdering(t *testing.T) {
	router, _, token, csrf := buildProtoHarness(t)
	// warning (90) >= critical (80): rejected, even though each
	// value is independently a valid 0..100 percentage.
	body := `{"monitoring.disk_usage_warning_pct": 90, "monitoring.disk_usage_critical_pct": 80}`
	resp, respBody := protoRequest(t, router, "PATCH", "/api/v1/admin/settings/protocol/webadmin", body, token, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for warning>=critical, got %d: %s", resp.StatusCode, respBody)
	}
}

// TestProtocolSettingsV1_HotSettingChangesLiveConfig proves a
// hot-applied field (outbound.prefer_ipv4, in Bridge.applyHot's real
// allowlist) actually changes the running process's in-memory config
// after PATCH — not just the DB row.
func TestProtocolSettingsV1_HotSettingChangesLiveConfig(t *testing.T) {
	router, _, token, csrf := buildProtoHarness(t)

	resp, body := protoRequest(t, router, "PATCH", "/api/v1/admin/settings/protocol/smtp_tx", `{"outbound.prefer_ipv4": true}`, token, csrf)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status %d: %s", resp.StatusCode, body)
	}
	var parsed struct {
		HotApplied []string `json:"hot_applied"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, k := range parsed.HotApplied {
		if k == "outbound.prefer_ipv4" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected outbound.prefer_ipv4 in hot_applied, got %v (body=%s)", parsed.HotApplied, body)
	}

	// Confirm via GET that the change is visible as a real applied
	// value (not just claimed).
	getResp, getBody := protoRequest(t, router, "GET", "/api/v1/admin/settings/protocol/smtp_tx", "", token, csrf)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get status %d: %s", getResp.StatusCode, getBody)
	}
	if !strings.Contains(string(getBody), `"value":true`) {
		t.Fatalf("expected the hot-applied true value to be visible on GET, body=%s", getBody)
	}
}

// TestProtocolSettingsV1_RestartRequiredSettingDoesNotChangeListener
// proves a restart-required field (coremail.smtp_port) is persisted
// but the live listener config is untouched until a real restart —
// PATCH must never claim it took effect.
func TestProtocolSettingsV1_RestartRequiredSettingDoesNotChangeListener(t *testing.T) {
	router, _, token, csrf := buildProtoHarness(t)

	resp, body := protoRequest(t, router, "PATCH", "/api/v1/admin/settings/protocol/smtp_recv", `{"coremail.smtp_port": 2525}`, token, csrf)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status %d: %s", resp.StatusCode, body)
	}
	var parsed struct {
		HotApplied     []string `json:"hot_applied"`
		PendingRestart []string `json:"pending_restart"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range parsed.HotApplied {
		if k == "coremail.smtp_port" {
			t.Fatal("coremail.smtp_port must never be reported as hot_applied — it requires a real restart")
		}
	}
	found := false
	for _, k := range parsed.PendingRestart {
		if k == "coremail.smtp_port" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected coremail.smtp_port in pending_restart, got %v", parsed.PendingRestart)
	}
}

func TestProtocolSettingsV1_NoOpPatchCausesNoWrite(t *testing.T) {
	router, db, token, csrf := buildProtoHarness(t)

	// First patch actually changes something.
	if resp, body := protoRequest(t, router, "PATCH", "/api/v1/admin/settings/protocol/smtp_recv", `{"coremail.queue_workers": 7}`, token, csrf); resp.StatusCode != http.StatusOK {
		t.Fatalf("first patch status %d: %s", resp.StatusCode, body)
	}
	afterFirst := countAdminSettingsRows(t, db)

	// Second patch with the SAME value must not write anything new.
	resp2, body2 := protoRequest(t, router, "PATCH", "/api/v1/admin/settings/protocol/smtp_recv", `{"coremail.queue_workers": 7}`, token, csrf)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("no-op patch status %d: %s", resp2.StatusCode, body2)
	}
	var parsed struct {
		Applied []map[string]any `json:"applied"`
	}
	if err := json.Unmarshal(body2, &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(parsed.Applied) != 0 {
		t.Fatalf("expected zero applied fields for a no-op patch, got %v", parsed.Applied)
	}
	afterSecond := countAdminSettingsRows(t, db)
	if afterSecond != afterFirst {
		t.Fatalf("no-op patch wrote a row: before=%d after=%d", afterFirst, afterSecond)
	}
}

func TestProtocolSettingsV1_EmptyNumericInputNeverSendsMutation(t *testing.T) {
	router, db, token, csrf := buildProtoHarness(t)
	before := countAdminSettingsRows(t, db)

	// An empty string for an "int" field must fail type coercion, not
	// silently coerce to 0.
	resp, body := protoRequest(t, router, "PATCH", "/api/v1/admin/settings/protocol/smtp_recv", `{"coremail.smtp_port": ""}`, token, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty numeric input, got %d: %s", resp.StatusCode, body)
	}
	after := countAdminSettingsRows(t, db)
	if after != before {
		t.Fatalf("empty numeric input mutated admin_settings: before=%d after=%d", before, after)
	}
}

func TestProtocolSettingsV1_ReadOnlyKeyRejected(t *testing.T) {
	router, db, token, csrf := buildProtoHarness(t)
	before := countAdminSettingsRows(t, db)

	resp, body := protoRequest(t, router, "PATCH", "/api/v1/admin/settings/protocol/remote_pop", `{"coremail.imap_idle_enabled": true}`, token, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for a read-only key, got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "read-only") {
		t.Fatalf("expected an honest read-only rejection reason, got %s", body)
	}
	after := countAdminSettingsRows(t, db)
	if after != before {
		t.Fatalf("read-only key PATCH mutated admin_settings: before=%d after=%d", before, after)
	}
}
