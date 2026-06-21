package handlers_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
)

type backupTestEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	SizeBytes   int64  `json:"size_bytes"`
	CreatedAt   string `json:"created_at"`
	CompletedAt string `json:"completed_at"`
}

func buildBackupHarness(t *testing.T) (*api.Router, *sql.DB, string, string, string) {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	root := t.TempDir()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(root, "orvix.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.Backup.Dir = filepath.Join(root, "backups")
	cfg.CoreMail.DataPath = filepath.Join(root, "coremail")
	cfg.CoreMail.MailStorePath = filepath.Join(cfg.CoreMail.DataPath, "mailstore")
	if err := os.MkdirAll(cfg.CoreMail.MailStorePath, 0750); err != nil {
		t.Fatalf("mkdir mailstore: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.CoreMail.DataPath, "attachments"), 0750); err != nil {
		t.Fatalf("mkdir attachments: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfg.CoreMail.MailStorePath, "welcome.eml"), []byte("Subject: test\r\n\r\nbody"), 0640); err != nil {
		t.Fatalf("write mail fixture: %v", err)
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
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
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
	token := loginBackup(t, router)
	csrf := csrfBackup(t, router, token)
	return router, sqlDB, token, csrf, cfg.Backup.Dir
}

func loginBackup(t *testing.T, router *api.Router) string {
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

func csrfBackup(t *testing.T, router *api.Router, token string) string {
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

func backupRequest(t *testing.T, router *api.Router, method, path, body, token, csrf string) (*http.Response, []byte) {
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

func createBackupViaAPI(t *testing.T, router *api.Router, token, csrf string) backupTestEntry {
	t.Helper()
	resp, body := backupRequest(t, router, "POST", "/api/v1/admin/backups", `{}`, token, csrf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d: %s", resp.StatusCode, body)
	}
	var entry backupTestEntry
	if err := json.Unmarshal(body, &entry); err != nil {
		t.Fatalf("decode create: %v: %s", err, body)
	}
	if entry.ID == "" || strings.ContainsAny(entry.ID, `/\`) || strings.Contains(entry.ID, "..") {
		t.Fatalf("unsafe backup id: %#v", entry)
	}
	if entry.Status != "completed" {
		t.Fatalf("expected completed backup, got %#v", entry)
	}
	return entry
}

func TestBackupAPIListEmptyReturnsArray(t *testing.T) {
	router, sqlDB, token, _, _ := buildBackupHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	resp, body := backupRequest(t, router, "GET", "/api/v1/admin/backups", "", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status %d: %s", resp.StatusCode, body)
	}
	if string(body) == "null" {
		t.Fatal("list returned null, want []")
	}
	var entries []backupTestEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		t.Fatalf("decode list: %v: %s", err, body)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty list, got %d", len(entries))
	}
}

func TestBackupAPICreateListDownloadDelete(t *testing.T) {
	router, sqlDB, token, csrf, backupDir := buildBackupHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	created := createBackupViaAPI(t, router, token, csrf)
	if _, err := os.Stat(filepath.Join(backupDir, created.ID)); err != nil {
		t.Fatalf("backup dir missing: %v", err)
	}

	resp, body := backupRequest(t, router, "GET", "/api/v1/admin/backups", "", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status %d: %s", resp.StatusCode, body)
	}
	var list []backupTestEntry
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("unexpected list: %#v", list)
	}

	resp, archive := backupRequest(t, router, "GET", "/api/v1/admin/backups/"+created.ID+"/download", "", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download status %d: %s", resp.StatusCode, archive)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/gzip" {
		t.Fatalf("unexpected content type %q", got)
	}
	if got := resp.Header.Get("Content-Disposition"); !strings.Contains(got, "attachment;") || !strings.Contains(got, "orvix-backup-"+created.ID+".tar.gz") {
		t.Fatalf("unexpected content disposition %q", got)
	}
	names := readArchiveNames(t, archive)
	for _, name := range []string{"var/lib/orvix/orvix.db", "backup.json", "RESTORE_INSTRUCTIONS.txt", "checksums.txt"} {
		if !containsString(names, name) {
			t.Fatalf("archive missing %s, got %v", name, names)
		}
	}
	for _, name := range names {
		lower := strings.ToLower(name)
		for _, forbidden := range []string{".env", ".key", ".pem", ".crt", ".p12", ".pfx", "license", "token", "secret", "caddy", "tls", "headers"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("archive contained forbidden path %q", name)
			}
		}
	}

	// Verify backup.json manifest contents.
	if containsString(names, "backup.json") {
		entry := extractArchiveEntry(t, archive, "backup.json")
		if entry == "" {
			t.Fatal("backup.json entry empty")
		}
		var am struct {
			Product             string `json:"product"`
			BackupFormatVersion int    `json:"backup_format_version"`
		}
		if err := json.Unmarshal([]byte(entry), &am); err != nil {
			t.Fatalf("unmarshal backup.json: %v", err)
		}
		if am.Product != "Orvix Enterprise Mail" {
			t.Fatalf("expected product 'Orvix Enterprise Mail', got %q", am.Product)
		}
		if am.BackupFormatVersion != 1 {
			t.Fatalf("expected backup_format_version 1, got %d", am.BackupFormatVersion)
		}
	}

	resp, body = backupRequest(t, router, "DELETE", "/api/v1/admin/backups/"+created.ID, `{"confirm":"delete-orvix-backup"}`, token, csrf)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status %d: %s", resp.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(backupDir, created.ID)); !os.IsNotExist(err) {
		t.Fatalf("backup dir should be deleted, err=%v", err)
	}
}

func TestBackupDownloadStreamsArchiveWithoutFullBuffer(t *testing.T) {
	source, err := os.ReadFile("backups.go")
	if err != nil {
		t.Fatalf("read backups.go: %v", err)
	}
	content := string(source)
	start := strings.Index(content, "func (h *Handler) DownloadBackup")
	if start < 0 {
		t.Fatal("DownloadBackup function not found")
	}
	end := strings.Index(content[start:], "\n// DeleteBackup")
	if end < 0 {
		t.Fatal("DownloadBackup function end marker not found")
	}
	fn := content[start : start+end]
	for _, forbidden := range []string{
		"os.ReadFile",
		"io.ReadAll",
		"bytes.Buffer",
		"c.Send(buf.Bytes())",
	} {
		if strings.Contains(fn, forbidden) {
			t.Fatalf("DownloadBackup must stream archive without full buffering; found %s", forbidden)
		}
	}
	if !strings.Contains(fn, "SendStream(file") {
		t.Fatal("DownloadBackup must stream the contained archive file with SendStream")
	}
}

func TestBackupAPIWriteRequiresCSRF(t *testing.T) {
	router, sqlDB, token, csrf, _ := buildBackupHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	resp, _ := backupRequest(t, router, "POST", "/api/v1/admin/backups", `{}`, token, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected missing-CSRF create to be 403, got %d", resp.StatusCode)
	}

	created := createBackupViaAPI(t, router, token, csrf)
	resp, _ = backupRequest(t, router, "DELETE", "/api/v1/admin/backups/"+created.ID, `{"confirm":"delete-orvix-backup"}`, token, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected missing-CSRF delete to be 403, got %d", resp.StatusCode)
	}

	// POST /api/v1/admin/backups/schedule must require CSRF
	resp, _ = backupRequest(t, router, "POST", "/api/v1/admin/backups/schedule", `{}`, token, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected missing-CSRF schedule set to be 403, got %d", resp.StatusCode)
	}
	req := httptest.NewRequest("POST", "/api/v1/admin/backups/schedule", strings.NewReader(`{"enabled":true,"frequency":"daily","retentionCount":7}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Cookie", "csrf_token=invalid-cookie")
	req.Header.Set("X-CSRF-Token", "different-header")
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("invalid-CSRF schedule request: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected invalid-CSRF schedule set to be 403, got %d: %s", resp.StatusCode, body)
	}

	// POST /api/v1/admin/backups/retention must require CSRF
	resp, _ = backupRequest(t, router, "POST", "/api/v1/admin/backups/retention", "", token, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected missing-CSRF retention to be 403, got %d", resp.StatusCode)
	}
	req = httptest.NewRequest("POST", "/api/v1/admin/backups/retention", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Cookie", "csrf_token=invalid-cookie")
	req.Header.Set("X-CSRF-Token", "different-header")
	resp, err = router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("invalid-CSRF retention request: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected invalid-CSRF retention to be 403, got %d: %s", resp.StatusCode, body)
	}
}

func TestBackupAPIRejectsInvalidIDs(t *testing.T) {
	router, sqlDB, token, csrf, _ := buildBackupHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	for _, id := range []string{"..escape", "bad..name", "missing"} {
		resp, _ := backupRequest(t, router, "GET", "/api/v1/admin/backups/"+id+"/download", "", token, "")
		if id == "missing" {
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("missing backup should return 404, got %d", resp.StatusCode)
			}
			continue
		}
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid id %q should return 400, got %d", id, resp.StatusCode)
		}
		resp, _ = backupRequest(t, router, "DELETE", "/api/v1/admin/backups/"+id, `{"confirm":"delete-orvix-backup"}`, token, csrf)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid delete id %q should return 400, got %d", id, resp.StatusCode)
		}
	}
}

func readArchiveNames(t *testing.T, data []byte) []string {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	var names []string
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar reader: %v", err)
		}
		names = append(names, header.Name)
	}
	return names
}

func extractArchiveEntry(t *testing.T, data []byte, target string) string {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar reader: %v", err)
		}
		if header.Name == target {
			body, err := io.ReadAll(tr)
			if err != nil {
				t.Fatalf("read %s: %v", target, err)
			}
			return string(body)
		}
	}
	return ""
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

// ── Delete confirmation tests ────────────────────────────

func TestBackupDeleteNoBodyReturns400(t *testing.T) {
	router, sqlDB, token, csrf, _ := buildBackupHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()
	created := createBackupViaAPI(t, router, token, csrf)
	resp, body := backupRequest(t, router, "DELETE", "/api/v1/admin/backups/"+created.ID, "", token, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for no body, got %d: %s", resp.StatusCode, body)
	}
}

func TestBackupDeleteWrongConfirmReturns400(t *testing.T) {
	router, sqlDB, token, csrf, _ := buildBackupHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()
	created := createBackupViaAPI(t, router, token, csrf)
	resp, body := backupRequest(t, router, "DELETE", "/api/v1/admin/backups/"+created.ID, `{"confirm":"wrong-value"}`, token, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for wrong confirm, got %d: %s", resp.StatusCode, body)
	}
}

func TestBackupDeleteCorrectConfirmSucceeds(t *testing.T) {
	router, sqlDB, token, csrf, backupDir := buildBackupHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()
	created := createBackupViaAPI(t, router, token, csrf)
	resp, body := backupRequest(t, router, "DELETE", "/api/v1/admin/backups/"+created.ID, `{"confirm":"delete-orvix-backup"}`, token, csrf)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 for correct confirm, got %d: %s", resp.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(backupDir, created.ID)); !os.IsNotExist(err) {
		t.Fatalf("backup dir should be deleted, err=%v", err)
	}
}

// ── Legacy route tests ───────────────────────────────────

func TestBackupLegacyWriteRoutesReturn410(t *testing.T) {
	router, sqlDB, token, csrf, _ := buildBackupHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	for _, path := range []string{
		"/api/v1/backups",
		"/api/v1/backups/schedule",
		"/api/v1/backups/retention",
	} {
		resp, body := backupRequest(t, router, "POST", path, `{}`, token, csrf)
		if resp.StatusCode != http.StatusGone {
			t.Fatalf("POST %s expected 410, got %d: %s", path, resp.StatusCode, body)
		}
	}

	created := createBackupViaAPI(t, router, token, csrf)
	resp, body := backupRequest(t, router, "DELETE", "/api/v1/backups/"+created.ID, `{"confirm":"delete-orvix-backup"}`, token, csrf)
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("DELETE /api/v1/backups/:id expected 410, got %d: %s", resp.StatusCode, body)
	}

	for _, path := range []string{
		"/api/v1/backups",
		"/api/v1/backups/schedule",
		"/api/v1/backups/metrics",
		"/api/v1/backups/health",
	} {
		resp, body := backupRequest(t, router, "GET", path, "", token, "")
		if resp.StatusCode != http.StatusGone {
			t.Fatalf("GET %s expected 410, got %d: %s", path, resp.StatusCode, body)
		}
	}
	resp, body = backupRequest(t, router, "GET", "/api/v1/backups/"+created.ID+"/download", "", token, "")
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("GET /api/v1/backups/:id/download expected 410, got %d: %s", resp.StatusCode, body)
	}
}

// ── X-Orvix-Confirm header fallback tests ────────────────────────────
//
// DELETE intermediaries sometimes strip the request body. The admin UI
// and any external admin client can still authorize delete by sending
// X-Orvix-Confirm: delete-orvix-backup. CSRF + admin role are still
// enforced by router middleware.

// rawBackupRequest is a thin variant of backupRequest that allows extra
// headers (used to test X-Orvix-Confirm).
func rawBackupRequest(t *testing.T, router *api.Router, method, path, body, token, csrf string, extra map[string]string) (*http.Response, []byte) {
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
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, data
}

func TestBackupDeleteHeaderConfirmOnlySucceeds(t *testing.T) {
	router, sqlDB, token, csrf, backupDir := buildBackupHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()
	created := createBackupViaAPI(t, router, token, csrf)
	// No body. Confirm only via X-Orvix-Confirm header.
	resp, body := rawBackupRequest(t, router, "DELETE", "/api/v1/admin/backups/"+created.ID, "", token, csrf,
		map[string]string{"X-Orvix-Confirm": "delete-orvix-backup"})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 for X-Orvix-Confirm header delete, got %d: %s", resp.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(backupDir, created.ID)); !os.IsNotExist(err) {
		t.Fatalf("backup dir should be deleted via header path, err=%v", err)
	}
}

func TestBackupDeleteHeaderWrongConfirmReturns400(t *testing.T) {
	router, sqlDB, token, csrf, _ := buildBackupHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()
	created := createBackupViaAPI(t, router, token, csrf)
	resp, body := rawBackupRequest(t, router, "DELETE", "/api/v1/admin/backups/"+created.ID, "", token, csrf,
		map[string]string{"X-Orvix-Confirm": "nope"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for wrong X-Orvix-Confirm header, got %d: %s", resp.StatusCode, body)
	}
}

func TestBackupDeleteNoBodyNoHeaderReturns400(t *testing.T) {
	router, sqlDB, token, csrf, _ := buildBackupHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()
	created := createBackupViaAPI(t, router, token, csrf)
	// Neither body nor X-Orvix-Confirm header.
	resp, body := rawBackupRequest(t, router, "DELETE", "/api/v1/admin/backups/"+created.ID, "", token, csrf, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for no body and no confirm header, got %d: %s", resp.StatusCode, body)
	}
}

func TestBackupDeleteHeaderRequiresCSRF(t *testing.T) {
	router, sqlDB, token, csrf, _ := buildBackupHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()
	// Create with valid CSRF so a backup exists; then try to delete
	// with admin auth + header confirm but no CSRF.
	created := createBackupViaAPI(t, router, token, csrf)
	resp, body := rawBackupRequest(t, router, "DELETE", "/api/v1/admin/backups/"+created.ID, "", token, "",
		map[string]string{"X-Orvix-Confirm": "delete-orvix-backup"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 when CSRF missing on header-path delete, got %d: %s", resp.StatusCode, body)
	}
}
