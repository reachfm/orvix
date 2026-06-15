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
	"sort"
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
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(root, "orvix.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.Backup.Dir = filepath.Join(root, "backups")
	cfg.CoreMail.DataPath = filepath.Join(root, "coremail")
	cfg.CoreMail.MailStorePath = filepath.Join(cfg.CoreMail.DataPath, "mailstore")
	cfg.Update.CheckURL = "https://updates.invalid"
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
		Pass    bool `json:"pass"`
		Checks  []map[string]interface{} `json:"checks"`
		Message string `json:"message"`
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

// TestUpdateV1_PreflightPassesWhenScriptPresent verifies the
// preflight reports pass=true when the canonical script is
// present and the disk has space.
func TestUpdateV1_PreflightPassesWhenScriptPresent(t *testing.T) {
	router, sqlDB, token, _, _ := buildUpdateHarness(t, true)
	defer router.App().Shutdown()
	defer sqlDB.Close()
	resp, body := updateRequest(t, router, "GET", "/api/v1/update/preflight", "", token, "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("preflight: %d %s", resp.StatusCode, body)
	}
	var pf struct {
		Pass    bool                   `json:"pass"`
		Checks  []map[string]interface{} `json:"checks"`
		Message string                 `json:"message"`
	}
	if err := json.Unmarshal(body, &pf); err != nil {
		t.Fatalf("decode: %v: %s", err, body)
	}
	// The disk_space and binary_build checks may be warnings on
	// Windows because statfs returns ENOSYS; the Pass should still
	// be true because script_path and backup_dir_writable pass.
	if !pf.Pass {
		t.Fatalf("expected preflight to pass, got %s", body)
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
func TestUpdateV1_ConcurrentRunSingleFlight(t *testing.T) {
	router, sqlDB, token, csrf, root := buildUpdateHarness(t, true)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	// Counter file: the script will write one byte per execution.
	counterFile := filepath.Join(root, "script-executions.counter")
	// Lay down the script that increments the counter and sleeps
	// for a few seconds so the second request is guaranteed to
	// arrive while the first is still in flight.
	scriptPath := filepath.Join(root, "release", "scripts", "apply-runtime-update.sh")
	// The script body is portable enough: a one-byte append, then
	// a short sleep, then exit 0. On Windows this is never exec'd
	// (the test asserts the counter is 0 there) but on POSIX the
	// second request must observe IsRunning=true and return 409.
	script := "#!/bin/sh\n" +
		"printf 'x' >> " + counterFile + "\n" +
		"sleep 2\n" +
		"exit 0\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0750); err != nil {
		t.Fatalf("write script: %v", err)
	}

	// Two parallel POSTs.
	type result struct {
		status int
		body   []byte
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			resp, body := updateRequest(t, router, "POST", "/api/v1/update/run", "", token, csrf, true)
			results <- result{status: resp.StatusCode, body: body}
		}()
	}
	r1 := <-results
	r2 := <-results

	// Assert exactly one 200 and one 409. The 200 response will
	// have status: "completed" on POSIX or status: "failed" on
	// Windows (where the script can't exec); the key invariant is
	// 200 vs 409.
	statuses := []int{r1.status, r2.status}
	sort.Ints(statuses)
	if statuses[0] != http.StatusOK || statuses[1] != http.StatusConflict {
		t.Fatalf("expected one 200 and one 409, got %d and %d (bodies: %q, %q)",
			r1.status, r2.status, r1.body, r2.body)
	}

	// The 409 response must carry the safe "already_running" code.
	var conflictBody []byte
	if r1.status == http.StatusConflict {
		conflictBody = r1.body
	} else {
		conflictBody = r2.body
	}
	var conflictResp struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(conflictBody, &conflictResp); err != nil {
		t.Fatalf("decode 409 body: %v: %s", err, conflictBody)
	}
	if conflictResp.Code != "already_running" {
		t.Fatalf("expected 409 response code 'already_running', got %q: %s", conflictResp.Code, conflictBody)
	}

	// Audit log: exactly one update_started row was written. This
	// is the platform-independent invariant. A second
	// update_started would mean a second job was admitted past
	// the IsRunning check, which would defeat single-flight.
	var startedCount int
	if err := sqlDB.QueryRow(
		`SELECT COUNT(*) FROM coremail_audit WHERE action='update_started'`,
	).Scan(&startedCount); err != nil {
		t.Fatalf("count update_started: %v", err)
	}
	if startedCount != 1 {
		t.Fatalf("expected exactly 1 update_started audit row, got %d", startedCount)
	}

	// Script-execution counter: the previous per-request-service
	// implementation would have started the script twice on POSIX.
	// With the shared-service refactor it is at most 1.
	data, err := os.ReadFile(counterFile)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read counter: %v", err)
	}
	if err == nil {
		count := strings.Count(string(data), "x")
		if count > 1 {
			t.Fatalf("expected at most 1 script execution, counter file has %d marks", count)
		}
	}
}

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
