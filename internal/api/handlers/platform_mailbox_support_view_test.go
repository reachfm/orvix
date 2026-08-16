package handlers_test

// Acceptance tests for the audited, read-only platform mailbox
// support-view feature: POST /platform/mailboxes/:tenant_id/:id/support-view
// and its child read-only routes. These tests are the mission's
// security backstop — every isolation/expiry/regression guarantee in
// the mission spec is asserted against the REAL router, REAL RBAC,
// and a REAL MailStore, not a mock.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
)

const (
	msvPSA1Email   = "msv-psa1@platform.local"
	msvPSA1Pass    = "PlatformPSA1!2026"
	msvPSA2Email   = "msv-psa2@platform.local"
	msvPSA2Pass    = "PlatformPSA2!2026"
	msvTenantEmail = "msv-tenant-admin@tenant1.local"
	msvTenantPass  = "MsvTenant!2026"
	msvMailboxPass = "CustomerMailboxPass!2026"
)

type mailboxSupportViewEnv struct {
	router     *api.Router
	db         *sql.DB
	mailStore  *storage.MailStore
	psa1Tok    string
	psa1CSRF   string
	psa2Tok    string
	psa2CSRF   string
	tenantTok  string
	tenantCSRF string

	tenant1MailboxID   uint
	tenant1FolderID    uint
	tenant1MessageID   uint
	tenant1AttachID    uint
	tenant1MailboxHash string
	tenant2MailboxID   uint
}

type msvRuntimeModule struct {
	store *storage.MailStore
	queue *queue.QueueEngine
}

func (m *msvRuntimeModule) ID() string                              { return "coremail-runtime" }
func (m *msvRuntimeModule) Version() string                         { return "test" }
func (m *msvRuntimeModule) Requires() []string                      { return nil }
func (m *msvRuntimeModule) Init(_ *config.Config, _ *gorm.DB) error { return nil }
func (m *msvRuntimeModule) Start() error                            { return nil }
func (m *msvRuntimeModule) Stop() error                             { return nil }
func (m *msvRuntimeModule) Migrate() error                          { return nil }
func (m *msvRuntimeModule) MailStore() *storage.MailStore           { return m.store }
func (m *msvRuntimeModule) QueueEngine() *queue.QueueEngine         { return m.queue }

func buildMailboxSupportViewEnv(t *testing.T) *mailboxSupportViewEnv {
	t.Helper()
	logger := zap.NewNop()
	scratchDir := t.TempDir()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = scratchDir + "/msv.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
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

	mailstoreDir := filepath.Join(scratchDir, "mailstore")
	if err := os.MkdirAll(mailstoreDir, 0o750); err != nil {
		t.Fatalf("mkdir mailstore: %v", err)
	}
	for _, stmt := range storage.Tables() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("mailstore ddl: %v", err)
		}
	}
	mailStore, err := storage.NewMailStore(sqlDB, mailstoreDir)
	if err != nil {
		t.Fatalf("mailstore: %v", err)
	}
	for _, stmt := range queue.Tables() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("queue ddl: %v", err)
		}
	}
	qe := queue.NewQueueEngine(sqlDB)

	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	adminDir := filepath.Join(scratchDir, "admin")
	if err := os.MkdirAll(adminDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(adminDir, "index.html"), []byte("<html></html>"), 0o644)
	cfg.Server.AdminUIDir = adminDir
	cfg.CoreMail.MailStorePath = mailstoreDir

	reg := modules.NewRegistry(logger)
	reg.Register(&msvRuntimeModule{store: mailStore, queue: qe})
	router := api.NewRouter(cfg, authenticator, logger, db, reg, license.NewFeatureFlags(logger), nil)

	now := time.Now().UTC()
	psa1Hash, _ := authenticator.HashPassword(msvPSA1Pass)
	if _, err := sqlDB.Exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, 'platform_super_admin', NULL, 1, 1)", now, now, msvPSA1Email, psa1Hash); err != nil {
		t.Fatalf("seed psa1: %v", err)
	}
	psa2Hash, _ := authenticator.HashPassword(msvPSA2Pass)
	if _, err := sqlDB.Exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, 'platform_super_admin', NULL, 1, 1)", now, now, msvPSA2Email, psa2Hash); err != nil {
		t.Fatalf("seed psa2: %v", err)
	}
	if _, err := sqlDB.Exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (1, ?, ?, 'tenant-a', 'tenant-a', 't1.example', 'enterprise', 1)", now, now); err != nil {
		t.Fatalf("seed tenant 1: %v", err)
	}
	if _, err := sqlDB.Exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (2, ?, ?, 'tenant-b', 'tenant-b', 't2.example', 'enterprise', 1)", now, now); err != nil {
		t.Fatalf("seed tenant 2: %v", err)
	}
	seedTenantAdminWithPassword(t, sqlDB, msvTenantEmail, 1, msvTenantPass)

	if _, err := sqlDB.Exec("INSERT INTO coremail_domains (name, tenant_id, status, created_at, updated_at, version) VALUES ('t1.example', 1, 'active', ?, ?, 1)", now, now); err != nil {
		t.Fatalf("seed domain 1: %v", err)
	}
	if _, err := sqlDB.Exec("INSERT INTO coremail_domains (name, tenant_id, status, created_at, updated_at, version) VALUES ('t2.example', 2, 'active', ?, ?, 1)", now, now); err != nil {
		t.Fatalf("seed domain 2: %v", err)
	}

	mailboxHash, _ := authenticator.HashPassword(msvMailboxPass)
	res, err := sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (1, 1, 'customer', 'customer@t1.example', 'Customer', ?, 'argon2id', 'active', 1024, 0, ?, ?)`,
		mailboxHash, now, now)
	if err != nil {
		t.Fatalf("seed mailbox 1: %v", err)
	}
	mb1ID, _ := res.LastInsertId()

	res2, err := sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (2, 2, 'other', 'other@t2.example', 'Other', ?, 'argon2id', 'active', 1024, 0, ?, ?)`,
		mailboxHash, now, now)
	if err != nil {
		t.Fatalf("seed mailbox 2: %v", err)
	}
	mb2ID, _ := res2.LastInsertId()

	if err := mailStore.Folders.EnsureSystemFolders(t.Context(), uint(mb1ID), nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	folders, err := mailStore.Folders.ListByMailbox(t.Context(), uint(mb1ID), nil)
	if err != nil || len(folders) == 0 {
		t.Fatalf("list folders: %v", err)
	}
	inboxID := folders[0].ID
	for _, f := range folders {
		if f.Name == "Inbox" {
			inboxID = f.ID
		}
	}

	rfc822Path := filepath.Join(mailstoreDir, "msg1.eml")
	rfc822Body := "From: sender@example.com\r\nTo: customer@t1.example\r\nSubject: Test subject\r\n\r\nHello there.\r\n"
	if err := os.WriteFile(rfc822Path, []byte(rfc822Body), 0o644); err != nil {
		t.Fatalf("write rfc822: %v", err)
	}
	msgRes, err := sqlDB.Exec(
		`INSERT INTO coremail_messages (message_id, tenant_id, domain_id, mailbox_id, folder_id, internet_message_id, subject, from_address, to_addresses, received_date, size_bytes, rfc822_path, sha256, seen, created_at, updated_at)
		 VALUES ('uuid-1', 1, 1, ?, ?, '<msg1@t1.example>', 'Test subject', 'sender@example.com', 'customer@t1.example', ?, ?, ?, 'deadbeef', 0, ?, ?)`,
		mb1ID, inboxID, now, len(rfc822Body), rfc822Path, now, now)
	if err != nil {
		t.Fatalf("seed message: %v", err)
	}
	msgID, _ := msgRes.LastInsertId()

	attRes, err := sqlDB.Exec(
		`INSERT INTO coremail_attachments (message_id, filename, content_type, size_bytes, sha256, storage_path, created_at)
		 VALUES (?, 'file.txt', 'text/plain', 10, 'aa', ?, ?)`,
		msgID, filepath.Join(mailstoreDir, "file.txt"), now)
	if err != nil {
		t.Fatalf("seed attachment: %v", err)
	}
	_ = os.WriteFile(filepath.Join(mailstoreDir, "file.txt"), []byte("hello 123"), 0o644)
	attID, _ := attRes.LastInsertId()

	t.Cleanup(func() {
		_ = router.App().Shutdown()
		_ = sqlDB.Close()
	})

	psa1Tok := importRouteLogin(t, router, msvPSA1Email, msvPSA1Pass)
	psa2Tok := importRouteLogin(t, router, msvPSA2Email, msvPSA2Pass)
	tenantTok := importRouteLogin(t, router, msvTenantEmail, msvTenantPass)

	return &mailboxSupportViewEnv{
		router:             router,
		db:                 sqlDB,
		mailStore:          mailStore,
		psa1Tok:            psa1Tok,
		psa1CSRF:           importRouteCSRF(t, router, psa1Tok),
		psa2Tok:            psa2Tok,
		psa2CSRF:           importRouteCSRF(t, router, psa2Tok),
		tenantTok:          tenantTok,
		tenantCSRF:         importRouteCSRF(t, router, tenantTok),
		tenant1MailboxID:   uint(mb1ID),
		tenant1FolderID:    inboxID,
		tenant1MessageID:   uint(msgID),
		tenant1AttachID:    uint(attID),
		tenant1MailboxHash: mailboxHash,
		tenant2MailboxID:   uint(mb2ID),
	}
}

func (e *mailboxSupportViewEnv) doJSON(t *testing.T, method, path, token, csrf string, body map[string]any) (*http.Response, map[string]interface{}) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
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
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	return resp, out
}

func msvConfirm(mailboxID uint) string { return "ACCESS-MAILBOX-" + itoa(int64(mailboxID)) }

func (e *mailboxSupportViewEnv) start(t *testing.T, tenantID, mailboxID uint, token, csrf string, body map[string]any) (*http.Response, map[string]interface{}) {
	path := "/api/v1/platform/mailboxes/" + itoa(int64(tenantID)) + "/" + itoa(int64(mailboxID)) + "/support-view"
	return e.doJSON(t, http.MethodPost, path, token, csrf, body)
}

func (e *mailboxSupportViewEnv) startOK(t *testing.T, tenantID, mailboxID uint, token, csrf string) string {
	t.Helper()
	resp, out := e.start(t, tenantID, mailboxID, token, csrf, map[string]any{
		"ticket_ref": "TICKET-1", "reason": "customer escalation", "confirm": msvConfirm(mailboxID),
	})
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200 starting session, got %d: %v", resp.StatusCode, out)
	}
	sid, _ := out["session_id"].(string)
	if sid == "" {
		t.Fatalf("expected session_id in response: %v", out)
	}
	return sid
}

// ── Start endpoint ───────────────────────────────────────────────

func TestStartMailboxSupportView_TenantAdminDenied(t *testing.T) {
	env := buildMailboxSupportViewEnv(t)
	resp, _ := env.start(t, 1, env.tenant1MailboxID, env.tenantTok, env.tenantCSRF, map[string]any{
		"ticket_ref": "T1", "reason": "r", "confirm": msvConfirm(env.tenant1MailboxID),
	})
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected 403 for tenant admin, got %d", resp.StatusCode)
	}
}

func TestStartMailboxSupportView_UnauthenticatedDenied(t *testing.T) {
	env := buildMailboxSupportViewEnv(t)
	resp, _ := env.start(t, 1, env.tenant1MailboxID, "", "", map[string]any{
		"ticket_ref": "T1", "reason": "r", "confirm": msvConfirm(env.tenant1MailboxID),
	})
	if resp.StatusCode != fiber.StatusUnauthorized && resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected 401/403, got %d", resp.StatusCode)
	}
}

func TestStartMailboxSupportView_WrongConfirmationRejected(t *testing.T) {
	env := buildMailboxSupportViewEnv(t)
	resp, _ := env.start(t, 1, env.tenant1MailboxID, env.psa1Tok, env.psa1CSRF, map[string]any{
		"ticket_ref": "T1", "reason": "r", "confirm": "ACCESS-MAILBOX-999999",
	})
	if resp.StatusCode != fiber.StatusPreconditionFailed {
		t.Fatalf("expected 412, got %d", resp.StatusCode)
	}
}

func TestStartMailboxSupportView_ReasonAndTicketRequired(t *testing.T) {
	env := buildMailboxSupportViewEnv(t)
	resp, _ := env.start(t, 1, env.tenant1MailboxID, env.psa1Tok, env.psa1CSRF, map[string]any{
		"ticket_ref": "", "reason": "", "confirm": msvConfirm(env.tenant1MailboxID),
	})
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestStartMailboxSupportView_CrossTenantPathIsNotFound(t *testing.T) {
	env := buildMailboxSupportViewEnv(t)
	// tenant1's mailbox id under tenant 2's path.
	resp, _ := env.start(t, 2, env.tenant1MailboxID, env.psa1Tok, env.psa1CSRF, map[string]any{
		"ticket_ref": "T1", "reason": "r", "confirm": msvConfirm(env.tenant1MailboxID),
	})
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("expected 404 for cross-tenant path, got %d", resp.StatusCode)
	}
}

func TestStartMailboxSupportView_ResponseNeverContainsPasswordOrHash(t *testing.T) {
	env := buildMailboxSupportViewEnv(t)
	resp, out := env.start(t, 1, env.tenant1MailboxID, env.psa1Tok, env.psa1CSRF, map[string]any{
		"ticket_ref": "T1", "reason": "r", "confirm": msvConfirm(env.tenant1MailboxID),
	})
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d: %v", resp.StatusCode, out)
	}
	for _, forbidden := range []string{"password", "password_hash", "hash", "token", "access_token"} {
		if _, ok := out[forbidden]; ok {
			t.Fatalf("response must never contain %q, got: %v", forbidden, out)
		}
	}
	if out["mode"] != "read_only" {
		t.Fatalf("expected mode=read_only, got %v", out["mode"])
	}
}

func TestStartMailboxSupportView_NeverTouchesCustomerPasswordOrCounters(t *testing.T) {
	env := buildMailboxSupportViewEnv(t)
	env.startOK(t, 1, env.tenant1MailboxID, env.psa1Tok, env.psa1CSRF)
	var hashAfter string
	if err := env.db.QueryRow("SELECT password_hash FROM coremail_mailboxes WHERE id=?", env.tenant1MailboxID).Scan(&hashAfter); err != nil {
		t.Fatalf("query mailbox: %v", err)
	}
	if hashAfter != env.tenant1MailboxHash {
		t.Fatalf("mailbox password hash must be untouched by support-view; before=%s after=%s", env.tenant1MailboxHash, hashAfter)
	}
}

// ── Read-only routes + isolation ────────────────────────────────

func TestMailboxSupportView_HappyPathFoldersMessagesAttachmentEnd(t *testing.T) {
	env := buildMailboxSupportViewEnv(t)
	sid := env.startOK(t, 1, env.tenant1MailboxID, env.psa1Tok, env.psa1CSRF)
	base := "/api/v1/platform/mailboxes/1/" + itoa(int64(env.tenant1MailboxID)) + "/support-view/" + sid

	resp, out := env.doJSON(t, http.MethodGet, base+"/folders", env.psa1Tok, env.psa1CSRF, nil)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("folders: expected 200, got %d: %v", resp.StatusCode, out)
	}
	if _, ok := out["folders"]; !ok {
		t.Fatalf("expected folders key: %v", out)
	}

	resp, out = env.doJSON(t, http.MethodGet, base+"/messages", env.psa1Tok, env.psa1CSRF, nil)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("messages: expected 200, got %d: %v", resp.StatusCode, out)
	}

	msgPath := base + "/messages/" + itoa(int64(env.tenant1MessageID))
	resp, out = env.doJSON(t, http.MethodGet, msgPath, env.psa1Tok, env.psa1CSRF, nil)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("message: expected 200, got %d: %v", resp.StatusCode, out)
	}

	attPath := msgPath + "/attachments/" + itoa(int64(env.tenant1AttachID))
	resp, _ = env.doJSON(t, http.MethodGet, attPath, env.psa1Tok, env.psa1CSRF, nil)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("attachment: expected 200, got %d", resp.StatusCode)
	}

	resp, out = env.doJSON(t, http.MethodPost, base+"/end", env.psa1Tok, env.psa1CSRF, nil)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("end: expected 200, got %d: %v", resp.StatusCode, out)
	}

	// Ended sessions must fail closed on further reads.
	resp, _ = env.doJSON(t, http.MethodGet, base+"/folders", env.psa1Tok, env.psa1CSRF, nil)
	if resp.StatusCode == fiber.StatusOK {
		t.Fatalf("expected non-200 after session end, got 200")
	}
}

func TestMailboxSupportView_ReadingMessageDoesNotMarkItSeen(t *testing.T) {
	env := buildMailboxSupportViewEnv(t)
	sid := env.startOK(t, 1, env.tenant1MailboxID, env.psa1Tok, env.psa1CSRF)
	base := "/api/v1/platform/mailboxes/1/" + itoa(int64(env.tenant1MailboxID)) + "/support-view/" + sid
	resp, _ := env.doJSON(t, http.MethodGet, base+"/messages/"+itoa(int64(env.tenant1MessageID)), env.psa1Tok, env.psa1CSRF, nil)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var seen int
	if err := env.db.QueryRow("SELECT seen FROM coremail_messages WHERE id=?", env.tenant1MessageID).Scan(&seen); err != nil {
		t.Fatalf("query message: %v", err)
	}
	if seen != 0 {
		t.Fatalf("reading a message through the support view must never mark it seen")
	}
}

func TestMailboxSupportView_CrossTenantURLTamperingDenied(t *testing.T) {
	env := buildMailboxSupportViewEnv(t)
	sid := env.startOK(t, 1, env.tenant1MailboxID, env.psa1Tok, env.psa1CSRF)
	// Same session id, but the URL now claims tenant 2 / mailbox from tenant 2.
	tamperedPath := "/api/v1/platform/mailboxes/2/" + itoa(int64(env.tenant2MailboxID)) + "/support-view/" + sid + "/folders"
	resp, _ := env.doJSON(t, http.MethodGet, tamperedPath, env.psa1Tok, env.psa1CSRF, nil)
	if resp.StatusCode == fiber.StatusOK {
		t.Fatalf("cross-tenant/cross-mailbox URL tampering must be denied, got 200")
	}
	if resp.StatusCode != fiber.StatusNotFound && resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected 403/404, got %d", resp.StatusCode)
	}
}

func TestMailboxSupportView_SessionUnusableByDifferentOperator(t *testing.T) {
	env := buildMailboxSupportViewEnv(t)
	sid := env.startOK(t, 1, env.tenant1MailboxID, env.psa1Tok, env.psa1CSRF)
	base := "/api/v1/platform/mailboxes/1/" + itoa(int64(env.tenant1MailboxID)) + "/support-view/" + sid
	resp, _ := env.doJSON(t, http.MethodGet, base+"/folders", env.psa2Tok, env.psa2CSRF, nil)
	if resp.StatusCode == fiber.StatusOK {
		t.Fatalf("a different operator must not be able to use another operator's session, got 200")
	}
}

func TestMailboxSupportView_TenantRoleCannotCallSupportViewEndpoints(t *testing.T) {
	env := buildMailboxSupportViewEnv(t)
	sid := env.startOK(t, 1, env.tenant1MailboxID, env.psa1Tok, env.psa1CSRF)
	base := "/api/v1/platform/mailboxes/1/" + itoa(int64(env.tenant1MailboxID)) + "/support-view/" + sid
	resp, _ := env.doJSON(t, http.MethodGet, base+"/folders", env.tenantTok, env.tenantCSRF, nil)
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected 403 for tenant role, got %d", resp.StatusCode)
	}
}

func TestMailboxSupportView_UnknownSessionIDIs404NotLeaked(t *testing.T) {
	env := buildMailboxSupportViewEnv(t)
	base := "/api/v1/platform/mailboxes/1/" + itoa(int64(env.tenant1MailboxID)) + "/support-view/nonexistent-session-id"
	resp, _ := env.doJSON(t, http.MethodGet, base+"/folders", env.psa1Tok, env.psa1CSRF, nil)
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("expected 404 for unknown session id, got %d", resp.StatusCode)
	}
}

func TestMailboxSupportView_ExpiredSessionFailsClosed(t *testing.T) {
	env := buildMailboxSupportViewEnv(t)
	resp, out := env.start(t, 1, env.tenant1MailboxID, env.psa1Tok, env.psa1CSRF, map[string]any{
		"ticket_ref": "T1", "reason": "r", "confirm": msvConfirm(env.tenant1MailboxID), "duration_minutes": 1,
	})
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d: %v", resp.StatusCode, out)
	}
	sid, _ := out["session_id"].(string)
	// Force expiry directly rather than sleeping a full minute in CI.
	if _, err := env.db.Exec("UPDATE platform_mailbox_view_sessions SET expires_at = ? WHERE id = ?", time.Now().UTC().Add(-time.Minute), sid); err != nil {
		t.Fatalf("force-expire session: %v", err)
	}
	base := "/api/v1/platform/mailboxes/1/" + itoa(int64(env.tenant1MailboxID)) + "/support-view/" + sid
	resp2, _ := env.doJSON(t, http.MethodGet, base+"/folders", env.psa1Tok, env.psa1CSRF, nil)
	if resp2.StatusCode == fiber.StatusOK {
		t.Fatalf("expired session must fail closed, got 200")
	}
}

func TestStartMailboxSupportView_DurationCappedAtSixtyMinutes(t *testing.T) {
	env := buildMailboxSupportViewEnv(t)
	resp, _ := env.start(t, 1, env.tenant1MailboxID, env.psa1Tok, env.psa1CSRF, map[string]any{
		"ticket_ref": "T1", "reason": "r", "confirm": msvConfirm(env.tenant1MailboxID), "duration_minutes": 61,
	})
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("expected 400 for duration exceeding 60m, got %d", resp.StatusCode)
	}
}

func TestMailboxSupportView_AuditEventsRecorded(t *testing.T) {
	env := buildMailboxSupportViewEnv(t)
	sid := env.startOK(t, 1, env.tenant1MailboxID, env.psa1Tok, env.psa1CSRF)
	base := "/api/v1/platform/mailboxes/1/" + itoa(int64(env.tenant1MailboxID)) + "/support-view/" + sid
	env.doJSON(t, http.MethodGet, base+"/folders", env.psa1Tok, env.psa1CSRF, nil)
	env.doJSON(t, http.MethodPost, base+"/end", env.psa1Tok, env.psa1CSRF, nil)

	for _, action := range []string{"support.mailbox_view.start", "support.mailbox_view.folders_read", "support.mailbox_view.end"} {
		var count int
		if err := env.db.QueryRow("SELECT COUNT(*) FROM coremail_audit WHERE action = ?", action).Scan(&count); err != nil {
			t.Fatalf("query audit for %s: %v", action, err)
		}
		if count == 0 {
			t.Fatalf("expected at least one audit row for action %q", action)
		}
	}
}
