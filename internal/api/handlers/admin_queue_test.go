package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"

	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
)

type queueTestEnv struct {
	router     *api.Router
	sqlDB      *sql.DB
	adminToken string
	csrfToken  string
	userToken  string
}

func buildQueueTestEnv(t *testing.T) *queueTestEnv {
	t.Helper()

	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "queue_test.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	// Create tenant + domain.
	now := time.Now().UTC()
	if _, err := sqlDB.Exec(
		"INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (1, ?, ?, 'orvix', 'orvix', 'orvix.email', 'enterprise', 1)",
		now, now,
	); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if _, err := sqlDB.Exec(
		"INSERT INTO coremail_domains (name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES ('orvix.email', 1, 'active', 'enterprise', 0, 0, 0, ?, ?)",
		now, now,
	); err != nil {
		t.Fatalf("insert domain: %v", err)
	}

	// Create admin and non-admin users.
	hash, err := authenticator.HashPassword("AdminPass!2026")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := sqlDB.Exec(
		"INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'admin@orvix.email', ?, 'admin', 1, 1, 1)",
		now, now, hash,
	); err != nil {
		t.Fatalf("insert admin: %v", err)
	}
	userHash, err := authenticator.HashPassword("UserPass!2026")
	if err != nil {
		t.Fatalf("hash user: %v", err)
	}
	if _, err := sqlDB.Exec(
		"INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, 'user@orvix.email', ?, 'user', 1, 1, 1)",
		now, now, userHash,
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	// Build queue table.
	for _, stmt := range queue.Tables() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("queue ddl: %v", err)
		}
	}

	// Setup scratch dirs for admin / webmail SPA.
	scratchDir := t.TempDir()
	adminDir := filepath.Join(scratchDir, "admin")
	os.MkdirAll(adminDir, 0755)
	os.WriteFile(filepath.Join(adminDir, "index.html"), []byte("<html></html>"), 0644)
	os.WriteFile(filepath.Join(adminDir, "app.js"), []byte(""), 0644)
	os.WriteFile(filepath.Join(adminDir, "styles.css"), []byte(""), 0644)
	webmailDir := filepath.Join(scratchDir, "webmail")
	os.MkdirAll(webmailDir, 0755)
	os.WriteFile(filepath.Join(webmailDir, "index.html"), []byte("<html></html>"), 0644)
	os.WriteFile(filepath.Join(webmailDir, "auth-gate.css"), []byte(""), 0644)
	os.WriteFile(filepath.Join(webmailDir, "auth-gate.js"), []byte(""), 0644)
	os.WriteFile(filepath.Join(webmailDir, "webmail.css"), []byte(""), 0644)
	os.WriteFile(filepath.Join(webmailDir, "webmail.js"), []byte(""), 0644)

	cfg.Server.AdminUIDir = adminDir
	cfg.Server.WebmailUIDir = webmailDir
	reg := modules.NewRegistry(logger)
	ff := license.NewFeatureFlags(logger)
	router := api.NewRouter(cfg, authenticator, logger, db, reg, ff, nil)
	router.SetQueueEngine(queue.NewQueueEngine(sqlDB))

	// Login admin and user via HTTP login endpoint.
	adminToken := loginQueueTestUser(t, router, "admin@orvix.email", "AdminPass!2026")
	userToken := loginQueueTestUser(t, router, "user@orvix.email", "UserPass!2026")
	csrf := getQueueCSRFCookie(t, router, adminToken)

	t.Cleanup(func() {
		_ = router.App().Shutdown()
		_ = sqlDB.Close()
	})

	return &queueTestEnv{
		router:     router,
		sqlDB:      sqlDB,
		adminToken: adminToken,
		csrfToken:  csrf,
		userToken:  userToken,
	}
}

func loginQueueTestUser(t *testing.T, router *api.Router, email, password string) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login %s: %v", email, err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("login %s: expected 200, got %d: %s", email, resp.StatusCode, string(body))
	}
	for _, c := range resp.Cookies() {
		if c.Name == "access_token" {
			return c.Value
		}
	}
	t.Fatalf("login %s: no access_token cookie", email)
	return ""
}

func getQueueCSRFCookie(t *testing.T, router *api.Router, bearer string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/csrf-token", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("csrf: %v", err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("csrf: expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	for _, c := range resp.Cookies() {
		if c.Name == "csrf_token" {
			return c.Value
		}
	}
	t.Fatal("no csrf_token cookie")
	return ""
}

func queueRequest(t *testing.T, e *queueTestEnv, method, path string, body string, token, csrf string) (*http.Response, []byte) {
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
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, data
}

func seedQueueEntry(t *testing.T, e *queueTestEnv, status, from, to string) uint {
	t.Helper()
	now := time.Now().UTC()
	res, err := e.sqlDB.Exec(
		`INSERT INTO coremail_queue (tenant_id, domain_id, message_id, from_address, to_address,
			recipient_domain, direction, status, priority, attempt_count, max_attempts,
			delivery_mode, created_at, updated_at)
		VALUES (1, 1, ?, ?, ?, 'remote.test', 'outbound', ?, 0, 0, 16, 'remote_smtp', ?, ?)`,
		"msg-"+from+"-"+to, from, to, status, now, now,
	)
	if err != nil {
		t.Fatalf("seed queue: %v", err)
	}
	qid, _ := res.LastInsertId()
	return uint(qid)
}

func TestAdminQueueList(t *testing.T) {
	e := buildQueueTestEnv(t)

	// Seed entries.
	seedQueueEntry(t, e, "pending", "a@test.com", "b@test.com")
	seedQueueEntry(t, e, "deferred", "c@test.com", "d@test.com")
	seedQueueEntry(t, e, "delivered", "e@test.com", "f@test.com")

	resp, body := queueRequest(t, e, "GET", "/api/v1/admin/queue/messages", "", e.adminToken, "")
	if resp.StatusCode != 200 {
		t.Fatalf("list: expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	var result struct {
		Messages []map[string]interface{} `json:"messages"`
		Total    int64                    `json:"total"`
		Limit    int                      `json:"limit"`
		Offset   int                      `json:"offset"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, string(body))
	}
	if result.Total != 3 {
		t.Errorf("total = %d, want 3", result.Total)
	}
	if len(result.Messages) != 3 {
		t.Errorf("messages len = %d, want 3", len(result.Messages))
	}
	if result.Limit != 50 {
		t.Errorf("default limit = %d, want 50", result.Limit)
	}
}

func TestAdminQueueListFilters(t *testing.T) {
	e := buildQueueTestEnv(t)

	seedQueueEntry(t, e, "pending", "senderA@test.com", "rcpt@example.com")
	seedQueueEntry(t, e, "deferred", "senderB@test.com", "rcpt@example.com")
	seedQueueEntry(t, e, "pending", "senderA@other.com", "rcpt@other.net")

	// Filter by status.
	resp, body := queueRequest(t, e, "GET", "/api/v1/admin/queue/messages?status=pending", "", e.adminToken, "")
	if resp.StatusCode != 200 {
		t.Fatalf("filter status: %d: %s", resp.StatusCode, string(body))
	}
	var result struct {
		Messages []map[string]interface{} `json:"messages"`
		Total    int64                    `json:"total"`
	}
	json.Unmarshal(body, &result)
	if result.Total != 2 {
		t.Errorf("status=pending total = %d, want 2: %s", result.Total, string(body))
	}

	// Filter by domain.
	resp2, body2 := queueRequest(t, e, "GET", "/api/v1/admin/queue/messages?domain=remote.test", "", e.adminToken, "")
	if resp2.StatusCode != 200 {
		t.Fatalf("filter domain: %d: %s", resp2.StatusCode, string(body2))
	}
	var result2 struct {
		Messages []map[string]interface{} `json:"messages"`
		Total    int64                    `json:"total"`
	}
	json.Unmarshal(body2, &result2)
	if result2.Total != 3 {
		t.Errorf("domain=remote.test total = %d, want 3: %s", result2.Total, string(body2))
	}

	// Filter by sender.
	resp3, body3 := queueRequest(t, e, "GET", "/api/v1/admin/queue/messages?from=senderA", "", e.adminToken, "")
	if resp3.StatusCode != 200 {
		t.Fatalf("filter sender: %d: %s", resp3.StatusCode, string(body3))
	}
	var result3 struct {
		Messages []map[string]interface{} `json:"messages"`
		Total    int64                    `json:"total"`
	}
	json.Unmarshal(body3, &result3)
	if result3.Total != 2 {
		t.Errorf("from=senderA total = %d, want 2: %s", result3.Total, string(body3))
	}

	// Filter by to.
	resp4, body4 := queueRequest(t, e, "GET", "/api/v1/admin/queue/messages?to=rcpt@example", "", e.adminToken, "")
	if resp4.StatusCode != 200 {
		t.Fatalf("filter to: %d: %s", resp4.StatusCode, string(body4))
	}
	var result4 struct {
		Messages []map[string]interface{} `json:"messages"`
		Total    int64                    `json:"total"`
	}
	json.Unmarshal(body4, &result4)
	if result4.Total != 2 {
		t.Errorf("to=rcpt@example total = %d, want 2: %s", result4.Total, string(body4))
	}

	// Pagination.
	resp5, body5 := queueRequest(t, e, "GET", "/api/v1/admin/queue/messages?limit=1&offset=0", "", e.adminToken, "")
	if resp5.StatusCode != 200 {
		t.Fatalf("pagination: %d: %s", resp5.StatusCode, string(body5))
	}
	var result5 struct {
		Messages []map[string]interface{} `json:"messages"`
		Total    int64                    `json:"total"`
		Limit    int                      `json:"limit"`
	}
	json.Unmarshal(body5, &result5)
	if result5.Limit != 1 || len(result5.Messages) != 1 {
		t.Errorf("pagination limit=1: got limit=%d len=%d", result5.Limit, len(result5.Messages))
	}
}

func TestAdminQueueDetail(t *testing.T) {
	e := buildQueueTestEnv(t)

	id := seedQueueEntry(t, e, "deferred", "detail@from.test", "detail@to.test")

	resp, body := queueRequest(t, e, "GET", "/api/v1/admin/queue/messages/"+strconv.FormatUint(uint64(id), 10), "", e.adminToken, "")
	if resp.StatusCode != 200 {
		t.Fatalf("detail: expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	var result struct {
		Message  map[string]interface{} `json:"message"`
		Attempts []map[string]interface{} `json:"attempts"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, string(body))
	}
	if result.Message["id"] == nil {
		t.Fatal("message missing id")
	}
	if result.Message["status"] != "deferred" {
		t.Errorf("status = %v, want deferred", result.Message["status"])
	}
	if result.Message["from_address"] != "detail@from.test" {
		t.Errorf("from = %v, want detail@from.test", result.Message["from_address"])
	}
	if result.Message["to_address"] != "detail@to.test" {
		t.Errorf("to = %v, want detail@to.test", result.Message["to_address"])
	}

	// Non-existent ID.
	resp2, _ := queueRequest(t, e, "GET", "/api/v1/admin/queue/messages/99999", "", e.adminToken, "")
	if resp2.StatusCode != 404 {
		t.Errorf("nonexistent: expected 404, got %d", resp2.StatusCode)
	}

	// Invalid ID.
	resp3, _ := queueRequest(t, e, "GET", "/api/v1/admin/queue/messages/abc", "", e.adminToken, "")
	if resp3.StatusCode != 400 {
		t.Errorf("invalid id: expected 400, got %d", resp3.StatusCode)
	}
}

func TestAdminQueueRetryNow(t *testing.T) {
	e := buildQueueTestEnv(t)

	id := seedQueueEntry(t, e, "deferred", "retry@test.com", "retry-to@test.com")

	resp, body := queueRequest(t, e, "POST",
		"/api/v1/admin/queue/messages/"+strconv.FormatUint(uint64(id), 10)+"/retry",
		"", e.adminToken, e.csrfToken)
	if resp.StatusCode != 200 {
		t.Fatalf("retry: expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	var result map[string]interface{}
	json.Unmarshal(body, &result)
	if result["status"] != "retrying" {
		t.Errorf("status = %v, want retrying", result["status"])
	}

	// Verify the row was updated.
	var dbStatus string
	e.sqlDB.QueryRow("SELECT status FROM coremail_queue WHERE id = ?", id).Scan(&dbStatus)
	if dbStatus != "pending" {
		t.Errorf("db status = %s, want pending", dbStatus)
	}

	// Non-existent ID.
	resp2, _ := queueRequest(t, e, "POST", "/api/v1/admin/queue/messages/99999/retry", "", e.adminToken, e.csrfToken)
	if resp2.StatusCode != 404 {
		t.Errorf("nonexistent retry: expected 404, got %d", resp2.StatusCode)
	}

	// Missing CSRF.
	resp3, _ := queueRequest(t, e, "POST", "/api/v1/admin/queue/messages/"+strconv.FormatUint(uint64(id), 10)+"/retry", "", e.adminToken, "")
	if resp3.StatusCode != 403 {
		t.Errorf("no-csrf retry: expected 403, got %d", resp3.StatusCode)
	}
}

func TestAdminQueueBounce(t *testing.T) {
	e := buildQueueTestEnv(t)

	id := seedQueueEntry(t, e, "pending", "bounce@test.com", "bounce-to@test.com")

	resp, body := queueRequest(t, e, "POST",
		"/api/v1/admin/queue/messages/"+strconv.FormatUint(uint64(id), 10)+"/bounce",
		`{"reason":"test bounce"}`, e.adminToken, e.csrfToken)
	if resp.StatusCode != 200 {
		t.Fatalf("bounce: expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	var result map[string]interface{}
	json.Unmarshal(body, &result)
	if result["status"] != "bounced" {
		t.Errorf("status = %v, want bounced", result["status"])
	}

	var dbStatus, dbError string
	e.sqlDB.QueryRow("SELECT status, last_error FROM coremail_queue WHERE id = ?", id).Scan(&dbStatus, &dbError)
	if dbStatus != "dead_letter" {
		t.Errorf("db status = %s, want dead_letter", dbStatus)
	}
	if dbError != "test bounce" {
		t.Errorf("db last_error = %s, want 'test bounce'", dbError)
	}

	// Bounce with no reason (default).
	id2 := seedQueueEntry(t, e, "pending", "bounce2@test.com", "bounce2-to@test.com")
	resp2, _ := queueRequest(t, e, "POST",
		"/api/v1/admin/queue/messages/"+strconv.FormatUint(uint64(id2), 10)+"/bounce",
		`{}`, e.adminToken, e.csrfToken)
	if resp2.StatusCode != 200 {
		t.Fatalf("bounce default: expected 200, got %d", resp2.StatusCode)
	}
	var dbError2 string
	e.sqlDB.QueryRow("SELECT last_error FROM coremail_queue WHERE id = ?", id2).Scan(&dbError2)
	if dbError2 != "manually bounced" {
		t.Errorf("db last_error = %s, want 'manually bounced'", dbError2)
	}

	// Missing CSRF.
	resp3, _ := queueRequest(t, e, "POST", "/api/v1/admin/queue/messages/"+strconv.FormatUint(uint64(id), 10)+"/bounce", `{}`, e.adminToken, "")
	if resp3.StatusCode != 403 {
		t.Errorf("no-csrf bounce: expected 403, got %d", resp3.StatusCode)
	}
}

func TestAdminQueueCancel(t *testing.T) {
	e := buildQueueTestEnv(t)

	id := seedQueueEntry(t, e, "pending", "cancel@test.com", "cancel-to@test.com")

	resp, body := queueRequest(t, e, "POST",
		"/api/v1/admin/queue/messages/"+strconv.FormatUint(uint64(id), 10)+"/cancel",
		"", e.adminToken, e.csrfToken)
	if resp.StatusCode != 200 {
		t.Fatalf("cancel: expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	var result map[string]interface{}
	json.Unmarshal(body, &result)
	if result["status"] != "cancelled" {
		t.Errorf("status = %v, want cancelled", result["status"])
	}

	var dbStatus string
	var completedAt *time.Time
	e.sqlDB.QueryRow("SELECT status, completed_at FROM coremail_queue WHERE id = ?", id).Scan(&dbStatus, &completedAt)
	if dbStatus != "cancelled" {
		t.Errorf("db status = %s, want cancelled", dbStatus)
	}
	if completedAt == nil {
		t.Error("completed_at should be set after cancel")
	}

	// Missing CSRF.
	id2 := seedQueueEntry(t, e, "pending", "cancel2@test.com", "cancel2-to@test.com")
	resp2, _ := queueRequest(t, e, "POST", "/api/v1/admin/queue/messages/"+strconv.FormatUint(uint64(id2), 10)+"/cancel", "", e.adminToken, "")
	if resp2.StatusCode != 403 {
		t.Errorf("no-csrf cancel: expected 403, got %d", resp2.StatusCode)
	}
}

func TestAdminQueueRBAC(t *testing.T) {
	e := buildQueueTestEnv(t)

	id := seedQueueEntry(t, e, "pending", "rbac@test.com", "rbac-to@test.com")

	// Unauthenticated requests must be 401.
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"list", "GET", "/api/v1/admin/queue/messages"},
		{"detail", "GET", "/api/v1/admin/queue/messages/" + strconv.FormatUint(uint64(id), 10)},
		{"retry", "POST", "/api/v1/admin/queue/messages/" + strconv.FormatUint(uint64(id), 10) + "/retry"},
		{"bounce", "POST", "/api/v1/admin/queue/messages/" + strconv.FormatUint(uint64(id), 10) + "/bounce"},
		{"cancel", "POST", "/api/v1/admin/queue/messages/" + strconv.FormatUint(uint64(id), 10) + "/cancel"},
	}

	for _, tt := range tests {
		t.Run(tt.name+"_no_auth", func(t *testing.T) {
			resp, _ := queueRequest(t, e, tt.method, tt.path, "", "", "")
			if resp.StatusCode != 401 {
				t.Errorf("no-auth: expected 401, got %d", resp.StatusCode)
			}
		})

		t.Run(tt.name+"_non_admin", func(t *testing.T) {
			resp, _ := queueRequest(t, e, tt.method, tt.path, "", e.userToken, e.csrfToken)
			if resp.StatusCode != 403 {
				t.Errorf("non-admin: expected 403, got %d", resp.StatusCode)
			}
		})
	}
}

func TestAdminQueueAudit(t *testing.T) {
	e := buildQueueTestEnv(t)

	// Create queue entries and perform actions that generate audit events.
	id1 := seedQueueEntry(t, e, "pending", "audit1@test.com", "audit1-to@test.com")
	id2 := seedQueueEntry(t, e, "deferred", "audit2@test.com", "audit2-to@test.com")
	id3 := seedQueueEntry(t, e, "pending", "audit3@test.com", "audit3-to@test.com")

	// Retry.
	resp1, _ := queueRequest(t, e, "POST",
		"/api/v1/admin/queue/messages/"+strconv.FormatUint(uint64(id2), 10)+"/retry",
		"", e.adminToken, e.csrfToken)
	if resp1.StatusCode != 200 {
		t.Fatalf("retry for audit: %d", resp1.StatusCode)
	}

	// Bounce.
	resp2, _ := queueRequest(t, e, "POST",
		"/api/v1/admin/queue/messages/"+strconv.FormatUint(uint64(id1), 10)+"/bounce",
		`{"reason":"test audit"}`, e.adminToken, e.csrfToken)
	if resp2.StatusCode != 200 {
		t.Fatalf("bounce for audit: %d", resp2.StatusCode)
	}

	// Cancel.
	resp3, _ := queueRequest(t, e, "POST",
		"/api/v1/admin/queue/messages/"+strconv.FormatUint(uint64(id3), 10)+"/cancel",
		"", e.adminToken, e.csrfToken)
	if resp3.StatusCode != 200 {
		t.Fatalf("cancel for audit: %d", resp3.StatusCode)
	}

	// Read back audit logs via the admin audit endpoint.
	resp4, body4 := queueRequest(t, e, "GET", "/api/v1/audit/logs", "", e.adminToken, "")
	if resp4.StatusCode != 200 {
		t.Fatalf("audit logs: expected 200, got %d: %s", resp4.StatusCode, string(body4))
	}

	bodyStr := string(body4)
	// Check that audit entries for queue actions are present.
	expectedActions := []string{"queue.retry", "queue.bounce", "queue.cancel"}
	for _, action := range expectedActions {
		if !strings.Contains(bodyStr, `"`+action+`"`) {
			t.Errorf("audit log missing action %q: %s", action, bodyStr)
		}
	}
}
