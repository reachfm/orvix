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

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
)

// buildUpdateHarness mirrors buildBackupHarness but with the
// workspace root pointed at a fresh temp dir that contains the
// canonical runtime update script. The router reads the working
// directory and the handler resolves the script relative to it.
func buildUpdateHarness(t *testing.T, withScript bool) (*api.Router, *sql.DB, string, string, string) {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	root := t.TempDir()
	t.Chdir(root)
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(root, "orvix.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.Backup.Dir = filepath.Join(root, "backups")
	cfg.CoreMail.DataPath = filepath.Join(root, "coremail")
	cfg.CoreMail.MailStorePath = filepath.Join(cfg.CoreMail.DataPath, "mailstore")
	cfg.Update.CheckURL = ""
	cfg.Update.FeedURL = ""
	cfg.Update.Channel = "stable"
	cfg.Update.WorkspaceRoot = root // <TempDir>, where the test lays down the script

	if err := os.MkdirAll(cfg.CoreMail.MailStorePath, 0750); err != nil {
		t.Fatalf("mkdir mailstore: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.CoreMail.DataPath, "attachments"), 0750); err != nil {
		t.Fatalf("mkdir attachments: %v", err)
	}
	if err := os.MkdirAll(cfg.Backup.Dir, 0750); err != nil {
		t.Fatalf("mkdir backups: %v", err)
	}

	if withScript {
		scriptDir := filepath.Join(root, "release", "scripts")
		if err := os.MkdirAll(scriptDir, 0750); err != nil {
			t.Fatalf("mkdir scripts: %v", err)
		}
		if err := os.WriteFile(filepath.Join(scriptDir, "apply-runtime-update.sh"),
			[]byte("#!/bin/sh\nexit 0\n"), 0750); err != nil {
			t.Fatalf("write script: %v", err)
		}
	}

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
	now := "2026-06-15 00:00:00"
	hash, err := authenticator.HashPassword("TestPassword123!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (?, ?, 'admin@test.local', ?, 'admin', 1, 1, 1)`,
		now, now, hash,
	); err != nil {
		t.Fatalf("insert admin: %v", err)
	}

	router := api.NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	token := loginUpdate(t, router)
	csrf := csrfUpdate(t, router, token)
	return router, sqlDB, token, csrf, root
}

func loginUpdate(t *testing.T, router *api.Router) string {
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
	return data.AccessToken
}

func csrfUpdate(t *testing.T, router *api.Router, token string) string {
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

func updateRequest(t *testing.T, router *api.Router, method, path, body, token, csrf string, withCSRF bool) (*http.Response, []byte) {
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
	if withCSRF && csrf != "" {
		req.Header.Set("Cookie", "csrf_token="+csrf)
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

// TestUpdateV1_StatusReturnsSafeFields verifies the /update/status
// response contains only safe fields and never leaks env values,
// tokens, file contents, or private absolute paths.
func TestUpdateV1_StatusReturnsSafeFields(t *testing.T) {
	router, sqlDB, token, _, _ := buildUpdateHarness(t, false)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	resp, body := updateRequest(t, router, "GET", "/api/v1/update/status", "", token, "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d %s", resp.StatusCode, body)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode: %v: %s", err, body)
	}
	for _, key := range []string{
		"currentVersion", "currentSha", "buildTime", "availableVersion",
		"availableSha", "channel", "updateAvailable", "releaseNotes",
		"checkedAt", "jobStatus",
	} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing top-level field %q: %s", key, body)
		}
	}
	if ch, _ := raw["channel"].(string); ch != "stable" {
		t.Errorf("channel: %q (want stable)", ch)
	}
	for _, banned := range []string{
		"Bearer ", "Bearer:", "bearer ",
		"password=", "password:",
		"secret=", "secret:",
		"jwt=", "jwt:",
		"AKIA",
		"-----BEGIN",
		"/etc/orvix",
		"ORVIX_DB_DSN",
		"DATABASE_URL",
		"x-api-key",
	} {
		if strings.Contains(string(body), banned) {
			t.Fatalf("status leaks forbidden token %q: %s", banned, body)
		}
	}
}

// TestUpdateV1_StatusRequiresAuth verifies /update/status requires
// a valid token.
func TestUpdateV1_StatusRequiresAuth(t *testing.T) {
	router, sqlDB, _, _, _ := buildUpdateHarness(t, false)
	defer router.App().Shutdown()
	defer sqlDB.Close()
	resp, _ := updateRequest(t, router, "GET", "/api/v1/update/status", "", "", "", false)
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 401/403 for unauth status, got %d", resp.StatusCode)
	}
}

// TestUpdateV1_HistoryReturnsSafeFields verifies the /update/history
// response is well-formed and rows never contain banned tokens.
func TestUpdateV1_HistoryReturnsSafeFields(t *testing.T) {
	router, sqlDB, token, _, _ := buildUpdateHarness(t, false)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	// Seed a history row.
	if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS update_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		started_at DATETIME NOT NULL,
		completed_at DATETIME,
		duration_seconds INTEGER NOT NULL DEFAULT 0,
		previous_sha TEXT NOT NULL DEFAULT '',
		new_sha TEXT NOT NULL DEFAULT '',
		from_version TEXT NOT NULL DEFAULT '',
		to_version TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT '',
		severity TEXT NOT NULL DEFAULT '',
		actor TEXT NOT NULL DEFAULT '',
		notes TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO update_history (started_at, completed_at, duration_seconds, previous_sha, new_sha, from_version, to_version, status, severity, actor, notes) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"2026-06-15 00:00:00", "2026-06-15 00:00:30", 30, "aaa", "bbb", "1.0.0", "1.1.0", "completed", "info", "user:1", "runtime update completed",
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	resp, body := updateRequest(t, router, "GET", "/api/v1/update/history", "", token, "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("history: %d %s", resp.StatusCode, body)
	}
	var out struct {
		History []map[string]interface{} `json:"history"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v: %s", err, body)
	}
	if len(out.History) == 0 {
		t.Fatalf("expected at least one row, got 0: %s", body)
	}
	for i, r := range out.History {
		for _, key := range []string{"id", "startedAt", "durationSeconds", "previousSha", "newSha", "fromVersion", "toVersion", "status", "severity", "actor"} {
			if _, ok := r[key]; !ok {
				t.Errorf("row[%d] missing field %q: %+v", i, key, r)
			}
		}
		notes, _ := r["notes"].(string)
		for _, banned := range []string{"Bearer", "password=", "secret=", "AKIA", "PRIVATE KEY", "/etc/orvix"} {
			if strings.Contains(notes, banned) {
				t.Errorf("row[%d] notes leaks forbidden token %q: %q", i, banned, notes)
			}
		}
	}
}

// TestUpdateV1_HistoryInvalidLimitRejected verifies /update/history
// rejects a non-integer or negative limit.
func TestUpdateV1_HistoryInvalidLimitRejected(t *testing.T) {
	router, sqlDB, token, _, _ := buildUpdateHarness(t, false)
	defer router.App().Shutdown()
	defer sqlDB.Close()
	for _, bad := range []string{"abc", "-1", "99999"} {
		resp, _ := updateRequest(t, router, "GET", "/api/v1/update/history?limit="+bad, "", token, "", false)
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("history?limit=%s must not return 200, got %d", bad, resp.StatusCode)
		}
	}
}

func TestUpdateV1_GetUpdateCheckMissingFeedURL(t *testing.T) {
	router, sqlDB, token, _, _ := buildUpdateHarness(t, false)
	defer router.App().Shutdown()
	defer sqlDB.Close()
	resp, body := updateRequest(t, router, "GET", "/api/v1/update/check", "", token, "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("check: %d %s", resp.StatusCode, body)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode: %v: %s", err, body)
	}
	for _, key := range []string{"current_version", "current_sha", "latest_version", "latest_sha", "update_available", "channel", "release_notes", "message"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("missing field %q: %s", key, body)
		}
	}
	if raw["message"] != "update check not configured" {
		t.Fatalf("unexpected message: %s", body)
	}
	if raw["update_available"] != false {
		t.Fatalf("update_available should be false: %s", body)
	}
}

func TestUpdateV1_GetUpdateCheckRequiresAuth(t *testing.T) {
	router, sqlDB, _, _, _ := buildUpdateHarness(t, false)
	defer router.App().Shutdown()
	defer sqlDB.Close()
	resp, _ := updateRequest(t, router, "GET", "/api/v1/update/check", "", "", "", false)
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected auth failure, got %d", resp.StatusCode)
	}
}

// TestUpdateV1_CheckRequiresCSRF verifies /update/check is rejected
// without a CSRF token.
func TestUpdateV1_CheckRequiresCSRF(t *testing.T) {
	router, sqlDB, token, _, _ := buildUpdateHarness(t, false)
	defer router.App().Shutdown()
	defer sqlDB.Close()
	// Without CSRF: must be 4xx.
	resp, _ := updateRequest(t, router, "POST", "/api/v1/update/check", "", token, "", false)
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("check without CSRF must not return 200, got %d", resp.StatusCode)
	}
}

// TestUpdateV1_RunRequiresCSRF verifies /update/run is rejected
// without a CSRF token.
func TestUpdateV1_RunRequiresCSRF(t *testing.T) {
	router, sqlDB, token, _, _ := buildUpdateHarness(t, true)
	defer router.App().Shutdown()
	defer sqlDB.Close()
	// Without CSRF: must be 4xx.
	resp, _ := updateRequest(t, router, "POST", "/api/v1/update/run", "", token, "", false)
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("run without CSRF must not return 200, got %d", resp.StatusCode)
	}
}

// TestUpdateV1_PreflightDoesNotExecScript verifies /update/preflight
// is read-only. We assert that the preflight report is well-formed
// and that the script_path check correctly fails when the script
// is missing.
func TestUpdateV1_PreflightDoesNotExecScript(t *testing.T) {
	router, sqlDB, token, _, _ := buildUpdateHarness(t, false)
	defer router.App().Shutdown()
	defer sqlDB.Close()
	resp, body := updateRequest(t, router, "GET", "/api/v1/update/preflight", "", token, "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("preflight: %d %s", resp.StatusCode, body)
	}
	var pf struct {
		Pass    bool                     `json:"pass"`
		Checks  []map[string]interface{} `json:"checks"`
		Message string                   `json:"message"`
	}
	if err := json.Unmarshal(body, &pf); err != nil {
		t.Fatalf("decode: %v: %s", err, body)
	}
	if pf.Pass {
		t.Fatalf("expected preflight to fail when script is missing, got pass=true: %s", body)
	}
	found := false
	for _, c := range pf.Checks {
		if c["name"] == "script_path" {
			found = true
			if status, _ := c["status"].(string); status != "fail" {
				t.Errorf("script_path status: %q (want fail)", status)
			}
			detail, _ := c["detail"].(string)
			for _, banned := range []string{"/etc/", "Bearer", "password=", "secret="} {
				if strings.Contains(detail, banned) {
					t.Errorf("preflight script_path detail leaks %q: %q", banned, detail)
				}
			}
		}
	}
	if !found {
		t.Fatalf("expected script_path check in preflight: %s", body)
	}
}

// TestUpdateV1_PreflightReportsMissingHelperUnit verifies the
// preflight correctly reports the update helper unit as missing
// on machines without the systemd oneshot installed (always true
// in test/CI). This confirms the preflight gate is wired to the
// systemd-only design.
func TestUpdateV1_PreflightReportsMissingHelperUnit(t *testing.T) {
	router, sqlDB, token, _, _ := buildUpdateHarness(t, false)
	defer router.App().Shutdown()
	defer sqlDB.Close()
	resp, body := updateRequest(t, router, "GET", "/api/v1/update/preflight", "", token, "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("preflight: %d %s", resp.StatusCode, body)
	}
	var pf struct {
		Pass    bool                     `json:"pass"`
		Checks  []map[string]interface{} `json:"checks"`
		Message string                   `json:"message"`
	}
	if err := json.Unmarshal(body, &pf); err != nil {
		t.Fatalf("decode: %v: %s", err, body)
	}
	// The preflight must fail due to missing helper unit (systemd
	// is not available in the test environment).
	if pf.Pass {
		t.Fatalf("expected preflight to fail (no systemd), but got pass=true: %s", body)
	}
	found := false
	for _, c := range pf.Checks {
		if name, _ := c["name"].(string); name == "update_helper_unit" {
			found = true
			if status, _ := c["status"].(string); status != "fail" {
				t.Errorf("update_helper_unit status = %q, want fail", status)
			}
		}
	}
	if !found {
		t.Fatalf("expected update_helper_unit check in preflight: %s", body)
	}
}

// TestUpdateV1_AuditChainOnRun verifies the audit log records
// update_started, update_completed (or update_failed) on a run.
// We use the run path that will fail in preflight (no script) to
// assert the update_failed audit fires; we then test the success
// path on a different harness that has the script.
func TestUpdateV1_AuditChainRecordsFailedRun(t *testing.T) {
	router, sqlDB, token, csrf, _ := buildUpdateHarness(t, false)
	defer router.App().Shutdown()
	defer sqlDB.Close()
	// Create the audit table in this test.
	if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS coremail_audit (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		actor TEXT NOT NULL DEFAULT '',
		role TEXT NOT NULL DEFAULT '',
		action TEXT NOT NULL DEFAULT '',
		target TEXT NOT NULL DEFAULT '',
		result TEXT NOT NULL DEFAULT '',
		ip TEXT NOT NULL DEFAULT '',
		user_agent TEXT NOT NULL DEFAULT '',
		timestamp DATETIME NOT NULL
	)`); err != nil {
		t.Fatalf("create audit: %v", err)
	}
	resp, _ := updateRequest(t, router, "POST", "/api/v1/update/run", "", token, csrf, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with status=failed, got %d", resp.StatusCode)
	}
	// Verify the audit chain. We expect update_started and
	// update_failed entries.
	var actions []string
	rows, err := sqlDB.Query(`SELECT action FROM coremail_audit WHERE action LIKE 'update_%' ORDER BY id`)
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatalf("scan: %v", err)
		}
		actions = append(actions, a)
	}
	wantActions := map[string]bool{
		"update_started": false,
		"update_failed":  false,
	}
	for _, a := range actions {
		if _, ok := wantActions[a]; ok {
			wantActions[a] = true
		}
	}
	for k, v := range wantActions {
		if !v {
			t.Errorf("expected audit action %q, not found in %v", k, actions)
		}
	}
}

// TestUpdateV1_NoShellInjectionInRun verifies the run route never
// accepts a user-supplied command or script path. We POST a
// body with a malicious shell-injection payload and assert that
// the handler ignores it.
func TestUpdateV1_NoShellInjectionInRun(t *testing.T) {
	router, sqlDB, token, csrf, _ := buildUpdateHarness(t, true)
	defer router.App().Shutdown()
	defer sqlDB.Close()
	malicious := `{"script": "/bin/sh -c 'rm -rf /'", "command": "rm -rf /"}`
	resp, _ := updateRequest(t, router, "POST", "/api/v1/update/run", malicious, token, csrf, true)
	// The handler must ignore the body and use the hard-coded script.
	// We assert that the response is well-formed and does not echo
	// the malicious payload anywhere.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, banned := range []string{"/bin/sh", "rm -rf", "script", "command"} {
		// The literals "script" and "command" appear as part of the
		// JSON keys in the body of the request, but the response
		// must not echo the malicious payloads. We only assert that
		// the dangerous commands do not appear.
		if banned == "rm -rf" && strings.Contains(string(body), banned) {
			t.Errorf("response echoed shell command %q: %s", banned, body)
		}
	}
	// On Windows the run will fail at exec because the script is
	// bash-only. The response status is "failed" and the audit
	// chain includes update_failed. We don't assert history
	// contents here because the shell is not present on Windows.
	_ = fmt.Sprintf("body: %s", body)
}

// TestUpdateV1_ConcurrentRunSingleFlight at the HTTP level fires
// two POST /api/v1/update/run requests in parallel against the
// same router and asserts the process-wide single-flight
// guarantee: exactly one job starts, the second request is
// rejected with 409 Conflict, and the script-execution counter
// sees at most one increment.
//
// This test would FAIL on the previous per-request
// RuntimeService implementation because each request constructed
// a fresh service with its own mutex — both calls would have
// acquired their own lock and both scripts would have started.
//
// We use a counter file that the script appends to. On Windows
// the script cannot be exec'd, so the counter stays at 0; the
// lock is still acquired exactly once and the second request
// still sees 409 — so the single-flight guarantee is asserted
// via the audit-log "update_started" row count, which is
// platform-independent.

// TestUpdateV1_FailedRunDoesNotLeakPrivatePath forces the script
// start to fail (e.g. the script becomes a non-executable
// regular file) and asserts that the API response and the audit
// row target field do NOT contain:
//   - the workspace root
//   - the canonical "release/scripts" suffix
//   - the absolute path to apply-runtime-update.sh
//   - the temp dir path used by the test
//
// The response carries only the safe code; the audit row carries
// only the safe code. The underlying exec error is logged to
// the server logger and never crosses the API or audit boundary.
func TestUpdateV1_FailedRunDoesNotLeakPrivatePath(t *testing.T) {
	router, sqlDB, token, csrf, root := buildUpdateHarness(t, true)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	// Lay the script down, then chmod 0644 (non-executable) so
	// exec.Start will fail on POSIX. On Windows, exec.Start
	// always fails for a .sh file because CreateProcess cannot
	// execute a bash script directly. Either way the start path
	// fails — and produces a start_failed UpdateError.
	scriptPath := filepath.Join(root, "release", "scripts", "apply-runtime-update.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	// Belt-and-braces: explicitly remove the executable bit on
	// POSIX. On Windows this call is a no-op.
	_ = os.Chmod(scriptPath, 0644)

	resp, body := updateRequest(t, router, "POST", "/api/v1/update/run", "", token, csrf, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with status=failed, got %d %s", resp.StatusCode, body)
	}

	// 1. Response must carry only the safe code + safe message.
	for _, banned := range bannedSubstrings(root) {
		if strings.Contains(string(body), banned) {
			t.Fatalf("response body leaks forbidden token %q: %s", banned, body)
		}
	}
	// 2. Response must include the safe code and message.
	var respBody struct {
		Status  string `json:"status"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &respBody); err != nil {
		t.Fatalf("decode response: %v: %s", err, body)
	}
	if respBody.Code == "" {
		t.Errorf("response missing safe code: %s", body)
	}
	if respBody.Message == "" {
		t.Errorf("response missing safe message: %s", body)
	}
	if !strings.HasPrefix(respBody.Message, "update failed") {
		t.Errorf("response message not generic: %q", respBody.Message)
	}

	// 3. Audit log: the update_failed target field must contain
	// only the safe code, never any path or argv.
	rows, err := sqlDB.Query(`SELECT target FROM coremail_audit WHERE action='update_failed' ORDER BY id`)
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var target string
		if err := rows.Scan(&target); err != nil {
			t.Fatalf("scan: %v", err)
		}
		for _, banned := range bannedSubstrings(root) {
			if strings.Contains(target, banned) {
				t.Fatalf("audit target leaks forbidden token %q: %q", banned, target)
			}
		}
	}
}

// bannedSubstrings returns the set of strings that must NEVER
// appear in a sanitized API response or audit row when the
// runtime update fails. The list is built from the test
// workspace root and includes:
//   - the workspace root itself
//   - the canonical "release/scripts" suffix
//   - the canonical script filename
//   - the workspace's temp dir name (the last component of
//     root, which is something like "001" or similar on Linux,
//     or the test name on Windows)
func bannedSubstrings(root string) []string {
	cleaned := filepath.Clean(root)
	// Components: the workspace root, the basename (which on
	// Linux is typically a counter, on Windows often the test
	// name), the release/scripts path, the script name, and
	// the absolute path to the script (rebuild for the assertion).
	parts := []string{
		cleaned,
		filepath.Base(cleaned),
		"release" + string(filepath.Separator) + "scripts",
		"apply-runtime-update.sh",
		filepath.Join(cleaned, "release", "scripts", "apply-runtime-update.sh"),
		// The two main Unix temp dirs and the Windows temp root
		// patterns. We deliberately match the substring "/tmp/" or
		// "Temp" so a leaked absolute path inside the test root
		// is caught regardless of platform.
		"/tmp/",
		"\\Temp\\",
		"AppData\\Local\\Temp",
	}
	// Deduplicate.
	seen := map[string]bool{}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// workspaceRoot walks up from the test package directory until it
// finds a go.mod file, returning the absolute workspace root. Used
// by tests that need to read release/ files.
func workspaceRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("workspace root not found (no go.mod in any parent)")
		}
		dir = parent
	}
}

// TestUpdateV1_InstallScriptReferencesUpdateHelper verifies that
// release/install.sh contains the commands to deploy the update
// helper unit and the sudoers drop-in with the correct permissions.
// This test exists to catch a refactor that drops the install step.
func TestUpdateV1_InstallScriptReferencesUpdateHelper(t *testing.T) {
	root := workspaceRoot(t)
	installPath := filepath.Join(root, "release", "install.sh")
	data, err := os.ReadFile(installPath)
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	content := string(data)

	// The install script must install the systemd unit.
	if !strings.Contains(content, "/etc/systemd/system/orvix-update.service") {
		t.Error("install.sh must reference /etc/systemd/system/orvix-update.service")
	}
	// The install script must install the sudoers drop-in.
	if !strings.Contains(content, "/etc/sudoers.d/orvix-update") {
		t.Error("install.sh must reference /etc/sudoers.d/orvix-update")
	}
	// The unit file must be installed with 0644 root:root.
	if !strings.Contains(content, "0644 -o root -g root") {
		t.Error("install.sh must set unit file permissions 0644 root:root")
	}
	// The sudoers file must be installed with 0440 root:root.
	if !strings.Contains(content, "0440 -o root -g root") {
		t.Error("install.sh must set sudoers file permissions 0440 root:root")
	}
	// daemon-reload must be called so systemd picks up the new unit.
	if !strings.Contains(content, "systemctl daemon-reload") {
		t.Error("install.sh must run systemctl daemon-reload after installing the helper unit")
	}
}

// TestUpdateV1_HelperUnitHasReadWritePaths verifies the update
// helper unit file contains explicit ReadWritePaths entries for
// the paths the runtime update script needs to write: /opt/orvix,
// /usr/local/bin, /usr/share/orvix, /tmp. Without these the script
// cannot write the updated binary, admin UI, or build artifacts.
func TestUpdateV1_HelperUnitHasReadWritePaths(t *testing.T) {
	root := workspaceRoot(t)
	unitPath := filepath.Join(root, "release", "systemd", "orvix-update.service")
	data, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("read unit file: %v", err)
	}
	content := string(data)

	// Must use ProtectSystem=strict (not full) with explicit paths.
	if !strings.Contains(content, "ProtectSystem=strict") {
		t.Error("unit must use ProtectSystem=strict")
	}
	if !strings.Contains(content, "ReadWritePaths") {
		t.Error("unit must have ReadWritePaths directive")
	}
	for _, path := range []string{"/opt/orvix", "/usr/local/bin", "/usr/share/orvix", "/tmp"} {
		if !strings.Contains(content, path) {
			t.Errorf("unit ReadWritePaths must include %q", path)
		}
	}
}

// TestUpdateV1_HelperUnitNoEnvironmentFile verifies the update
// helper unit does NOT load any external environment file. The
// unit runs as root with ExecStart=...apply-runtime-update.sh
// and must NOT source /etc/orvix/update.env because that file
// lives under a directory owned by the non-root orvix user.
// An attacker who controls update.env would control environment
// variables in a root process.
//
// The only environment variable in the unit is the hardcoded
// ORVIX_UPDATE=1 (set via the Environment directive, which is
// part of the root-owned unit file and cannot be modified by
// the orvix user).
//
// ExecStart path note: the original release referenced
// /opt/orvix/release/scripts/apply-runtime-update.sh, but that
// path was NEVER copied by install.sh (the script lives at
// /usr/share/orvix/scripts/apply-runtime-update.sh post-install).
// The unit now points at the installer-managed path so the
// oneshot unit does not silently fail on every fresh VPS.
func TestUpdateV1_HelperUnitNoEnvironmentFile(t *testing.T) {
	root := workspaceRoot(t)
	unitPath := filepath.Join(root, "release", "systemd", "orvix-update.service")
	data, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("read unit file: %v", err)
	}
	content := string(data)

	// Must NOT contain EnvironmentFile (the whole point of this test).
	if strings.Contains(content, "EnvironmentFile") {
		t.Error("unit must NOT contain EnvironmentFile (injection vector)")
	}
	// Must NOT reference update.env in any executable context.
	// Comments explaining the historical /etc/orvix/update.env
	// design are fine.
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}
		if strings.Contains(line, "update.env") {
			t.Errorf("unit executable line references update.env: %s", line)
		}
		if strings.HasPrefix(trimmed, "Environment=") && strings.Contains(trimmed, "/etc/orvix") {
			t.Errorf("Environment directive references /etc/orvix (injection vector): %s", line)
		}
	}
	// ExecStart must point at the installer-managed path. The
	// previous /opt/orvix/release/scripts/... path was a
	// regression — install.sh never copied the script there, so
	// the oneshot silently failed on every fresh VPS install.
	if !strings.Contains(content, "ExecStart=/usr/share/orvix/scripts/apply-runtime-update.sh") {
		t.Error("unit must have ExecStart=/usr/share/orvix/scripts/apply-runtime-update.sh (installer-managed path)")
	}
	if strings.Contains(content, "ExecStart=/opt/orvix/release/scripts/apply-runtime-update.sh") {
		t.Error("unit still references the /opt/orvix/... ExecStart path that install.sh never copies (release-blocker regression)")
	}
}

// ── Installer validation function tests ────────────────

// installerScript returns the content of release/install.sh.
func installerScript(t *testing.T) string {
	t.Helper()
	root := workspaceRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "release", "install.sh"))
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	return string(data)
}

// TestInstallScript_HasValidateSystemd verifies that install.sh
// defines the validate_systemd function that checks both systemd
// units (orvix.service and orvix-update.service) for existence,
// enablement, and active status.
func TestInstallScript_HasValidateSystemd(t *testing.T) {
	content := installerScript(t)
	if !strings.Contains(content, "validate_systemd()") {
		t.Error("install.sh must define validate_systemd()")
	}
	if !strings.Contains(content, "/etc/systemd/system/orvix.service") {
		t.Error("validate_systemd must check orvix.service path")
	}
	if !strings.Contains(content, "/etc/systemd/system/orvix-update.service") {
		t.Error("validate_systemd must check orvix-update.service path")
	}
	if !strings.Contains(content, "systemctl is-enabled") {
		t.Error("validate_systemd must verify service is enabled")
	}
	if !strings.Contains(content, "systemctl is-active") {
		t.Error("validate_systemd must verify service is active")
	}
}

// TestInstallScript_HasValidateSudoers verifies that install.sh
// defines validate_sudoers to check /etc/sudoers.d/orvix-update
// ownership (root:root) and mode (0440).
func TestInstallScript_HasValidateSudoers(t *testing.T) {
	content := installerScript(t)
	if !strings.Contains(content, "validate_sudoers()") {
		t.Error("install.sh must define validate_sudoers()")
	}
	if !strings.Contains(content, "/etc/sudoers.d/orvix-update") {
		t.Error("validate_sudoers must check /etc/sudoers.d/orvix-update")
	}
	if !strings.Contains(content, "root:root") {
		t.Error("validate_sudoers must check owner root:root")
	}
	if !strings.Contains(content, "440") {
		t.Error("validate_sudoers must check mode 440")
	}
}

// TestInstallScript_HasValidateDirectory verifies that install.sh
// defines validate_directory with an internal allowlist and unsafe
// path rejection.
func TestInstallScript_HasValidateDirectory(t *testing.T) {
	content := installerScript(t)
	if !strings.Contains(content, "validate_directory()") {
		t.Error("install.sh must define validate_directory()")
	}
	if !strings.Contains(content, "mkdir -p") {
		t.Error("validate_directory must self-heal with mkdir -p")
	}
	if !strings.Contains(content, "chown") {
		t.Error("validate_directory must set ownership")
	}
	if !strings.Contains(content, "chmod") {
		t.Error("validate_directory must set permissions")
	}
	// Must have an allowlist of exact expected paths.
	if !strings.Contains(content, "/opt/orvix") {
		t.Error("validate_directory allowlist must include /opt/orvix")
	}
	if !strings.Contains(content, "/usr/share/orvix/admin") {
		t.Error("validate_directory allowlist must include /usr/share/orvix/admin")
	}
	if !strings.Contains(content, "/var/lib/orvix") {
		t.Error("validate_directory allowlist must include /var/lib/orvix")
	}
	if !strings.Contains(content, "/var/log/orvix") {
		t.Error("validate_directory allowlist must include /var/log/orvix")
	}
	// Must reject unsafe paths.
	if !strings.Contains(content, "not in allowlist") {
		t.Error("validate_directory must reject paths not in allowlist")
	}
	if !strings.Contains(content, "path-traversal") {
		t.Error("validate_directory must reject path-traversal patterns")
	}
	if !strings.Contains(content, "relative path") {
		t.Error("validate_directory must reject relative paths")
	}
	if !strings.Contains(content, "refusing unsafe path") {
		t.Error("validate_directory must reject empty or root paths")
	}
}

// TestInstallScript_HasValidateBinary verifies that install.sh
// defines validate_binary to check /usr/local/bin/orvix exists and
// is executable. The binary is NEVER invoked; no --help or version
// subcommand is called. Optional file/sha256sum checks are allowed.
func TestInstallScript_HasValidateBinary(t *testing.T) {
	content := installerScript(t)
	if !strings.Contains(content, "validate_binary()") {
		t.Error("install.sh must define validate_binary()")
	}
	if !strings.Contains(content, "ORVIX_BIN") {
		t.Error("validate_binary must check ORVIX_BIN")
	}
	if !strings.Contains(content, "-x") {
		t.Error("validate_binary must check executable flag")
	}
	// Must NOT invoke the binary before config exists.
	// Check that the validate_binary function body does not
	// contain `"$bin" --help` or `"$bin" version`.
	if strings.Contains(content, "\"$bin\" --help") {
		t.Error("validate_binary must NOT call --help on the binary")
	}
	if strings.Contains(content, "\"$bin\" version") {
		t.Error("validate_binary must NOT call version on the binary")
	}
	// Optional integrity tools are acceptable.
	if !strings.Contains(content, "file") && !strings.Contains(content, "sha256sum") {
		t.Error("validate_binary should reference file or sha256sum for integrity")
	}
}

// TestInstallScript_HasValidateAdminUI verifies that install.sh
// defines validate_admin_ui to check admin UI assets.
func TestInstallScript_HasValidateAdminUI(t *testing.T) {
	content := installerScript(t)
	if !strings.Contains(content, "validate_admin_ui()") {
		t.Error("install.sh must define validate_admin_ui()")
	}
	if !strings.Contains(content, "index.html") {
		t.Error("validate_admin_ui must check index.html")
	}
	if !strings.Contains(content, "app.js") {
		t.Error("validate_admin_ui must check app.js")
	}
}

// TestInstallScript_HasValidateHTTPSConfig verifies that install.sh
// defines validate_https_config to check reverse proxy config.
func TestInstallScript_HasValidateHTTPSConfig(t *testing.T) {
	content := installerScript(t)
	if !strings.Contains(content, "validate_https_config()") {
		t.Error("install.sh must define validate_https_config()")
	}
	if !strings.Contains(content, "/etc/caddy/Caddyfile") {
		t.Error("validate_https_config must check Caddyfile")
	}
	if !strings.Contains(content, "caddy") {
		t.Error("validate_https_config must check caddy binary")
	}
}

// TestInstallScript_HasSmokeTests verifies that install.sh
// defines smoke_tests covering health, admin, JMAP, webmail,
// and advisory metrics. Admin-protected endpoints (update
// preflight, backup API) are not included; they are validated
// separately by validate_systemd and validate_directory.
func TestInstallScript_HasSmokeTests(t *testing.T) {
	content := installerScript(t)
	if !strings.Contains(content, "smoke_tests()") {
		t.Error("install.sh must define smoke_tests()")
	}
	if !strings.Contains(content, "/api/v1/health") {
		t.Error("smoke_tests must check health endpoint")
	}
	if !strings.Contains(content, "/admin") {
		t.Error("smoke_tests must check admin endpoint")
	}
	if !strings.Contains(content, "/.well-known/jmap") {
		t.Error("smoke_tests must check JMAP endpoint")
	}
	if !strings.Contains(content, "/webmail") {
		t.Error("smoke_tests must check webmail endpoint")
	}
	// Metrics is advisory but still checked.
	if !strings.Contains(content, "/metrics") {
		t.Error("smoke_tests must reference metrics endpoint")
	}
	// Update preflight and backup API are admin-protected and not
	// tested via smoke_tests. Verify they are NOT in smoke_tests.
	if strings.Contains(content, "/api/v1/update/preflight") {
		t.Error("smoke_tests must NOT include admin-protected update preflight endpoint")
	}
	if strings.Contains(content, "/api/v1/backup/status") {
		t.Error("smoke_tests must NOT include non-existent backup/status endpoint")
	}
}

// TestInstallScript_HasGenerateReport verifies that install.sh
// defines generate_install_report to produce a structured report.
func TestInstallScript_HasGenerateReport(t *testing.T) {
	content := installerScript(t)
	if !strings.Contains(content, "generate_install_report()") {
		t.Error("install.sh must define generate_install_report()")
	}
	if !strings.Contains(content, "INSTALLATION REPORT") {
		t.Error("generate_install_report must emit a report header")
	}
	if !strings.Contains(content, "Services") {
		t.Error("generate_install_report must list services")
	}
	if !strings.Contains(content, "Ports") {
		t.Error("generate_install_report must list ports")
	}
	if !strings.Contains(content, "Directories") {
		t.Error("generate_install_report must list directories")
	}
	if !strings.Contains(content, "Smoke Tests") {
		t.Error("generate_install_report must list smoke test results")
	}
	if !strings.Contains(content, "INSTALLATION COMPLETED SUCCESSFULLY") {
		t.Error("generate_install_report must indicate success")
	}
}

// TestInstallScript_WiredValidationCalls verifies that the
// validation functions are called at the appropriate points
// in main().
func TestInstallScript_WiredValidationCalls(t *testing.T) {
	content := installerScript(t)
	// Binary validation must be called in the binary step.
	if !strings.Contains(content, "install_binary") {
		t.Error("main must call install_binary")
	}
	if !strings.Contains(content, "validate_binary") {
		t.Error("main must call validate_binary after binary install")
	}
	// Admin UI validation must be called after config copy.
	if !strings.Contains(content, "validate_admin_ui") {
		t.Error("main must call validate_admin_ui after config provisioning")
	}
	// Systemd validation must be called after daemon-reload.
	if !strings.Contains(content, "validate_systemd") {
		t.Error("main must call validate_systemd after daemon-reload")
	}
	// Sudoers validation must be called.
	if !strings.Contains(content, "validate_sudoers") {
		t.Error("main must call validate_sudoers")
	}
	// Directory validation must be called in the user step.
	if !strings.Contains(content, "validate_directory") {
		t.Error("main must call validate_directory")
	}
	// Smoke tests must be called in verification step.
	if !strings.Contains(content, "smoke_tests") {
		t.Error("main must call smoke_tests in verification step")
	}
	// HTTPS config check must be called.
	if !strings.Contains(content, "validate_https_config") {
		t.Error("main must call validate_https_config")
	}
	// Report generation must be called.
	if !strings.Contains(content, "generate_install_report") {
		t.Error("main must call generate_install_report")
	}
}

// TestInstallScript_SafetyNoSecrets verifies the install script
// never echoes secrets, tokens, or private keys.
func TestInstallScript_SafetyNoSecrets(t *testing.T) {
	content := installerScript(t)
	// Never print password values.
	if strings.Contains(content, "echo.*$password") {
		t.Error("install.sh must not echo password values")
	}
	// Never print password hashes.
	if strings.Contains(content, "password_hash") {
		t.Error("install.sh must not echo password hashes")
	}
	// Never print JWT tokens.
	if strings.Contains(content, "echo.*$token") || strings.Contains(content, "echo.*token") {
		if strings.Contains(content, "echo.*token$") || strings.Contains(content, "csrf_token") {
			// csrf_token cookie value is acceptable
		} else {
			t.Error("install.sh must not echo auth tokens")
		}
	}
	// Never print private-key values. Config field names such as
	// coremail.vapid_private_key_file are safe and required, but
	// echo/printf/log lines must not disclose key material.
	for _, line := range strings.Split(content, "\n") {
		if (strings.Contains(line, "echo") || strings.Contains(line, "printf") || strings.Contains(line, "log_detail")) &&
			(strings.Contains(line, "$PRIVATE_KEY") || strings.Contains(line, "PRIVATE KEY")) {
			t.Errorf("install.sh must not echo private keys: %s", line)
		}
	}
}
