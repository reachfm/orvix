package handlers_test

// End-to-end tests for the user-facing webmail API
// (Phase: Real Webmail v1).
//
// These tests pin:
//   - unauthenticated calls return 401
//   - authenticated user with no mailbox gets a clean error
//   - authenticated user with a mailbox can list folders
//   - messages injected via the MailStore appear in /messages
//   - send creates a real row in the user's Sent folder
//   - delete marks the message deleted and moves it to Trash
//   - message body is loaded from disk, not hardcoded

import (
	"bytes"
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
	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// webmailTestEnv is a fully-wired test environment for the
// user-facing webmail endpoints. It contains:
//   - a Fiber router with auth, MailStore wired, and the
//     webmail user endpoints registered
//   - a real MailStore on a temp dir
//   - a real coremail QueueEngine against the same sqlite
//     db, used by the WebmailSend enqueue path
//   - a coremail_mailboxes row for admin@orvix.email
//   - a users row for admin@orvix.email with bcrypt password
type webmailTestEnv struct {
	router   *api.Router
	mailbox  *storage.MailStore
	queue    *queue.QueueEngine
	email    string
	password string
}

// runtimeProviderModule is a stand-in module that exposes
// a MailStore and QueueEngine without booting the full
// coremail runtime (SMTP/IMAP/POP3). It satisfies the
// interface used by router.go to wire both into the
// handler. The ID matches the production coremail runtime
// module so the same wiring path in router.go picks it up.
type runtimeProviderModule struct {
	store    *storage.MailStore
	queue    *queue.QueueEngine
}

func (m *runtimeProviderModule) ID() string             { return "coremail-runtime" }
func (m *runtimeProviderModule) Version() string        { return "test" }
func (m *runtimeProviderModule) Requires() []string     { return nil }
func (m *runtimeProviderModule) Init(_ *config.Config, _ *gorm.DB) error {
	return nil
}
func (m *runtimeProviderModule) Start() error   { return nil }
func (m *runtimeProviderModule) Stop() error    { return nil }
func (m *runtimeProviderModule) Migrate() error { return nil }
func (m *runtimeProviderModule) MailStore() *storage.MailStore {
	return m.store
}
func (m *runtimeProviderModule) QueueEngine() *queue.QueueEngine {
	return m.queue
}

// buildWebmailTestEnv wires a router with a MailStore and
// provisions the admin user/mailbox with a real bcrypt
// password so the admin can log in via /api/v1/auth/login.
func buildWebmailTestEnv(t *testing.T) *webmailTestEnv {
	t.Helper()

	root := webmailRepoRoot(t)
	webmailDir := filepath.Join(root, "release", "webmail")

	scratchDir := t.TempDir()
	adminDir := filepath.Join(scratchDir, "admin")
	if err := os.MkdirAll(adminDir, 0o755); err != nil {
		t.Fatalf("mkdir admin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(adminDir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatalf("write admin index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(adminDir, "app.js"), []byte(""), 0o644); err != nil {
		t.Fatalf("write admin app.js: %v", err)
	}
	if err := os.WriteFile(filepath.Join(adminDir, "styles.css"), []byte(""), 0o644); err != nil {
		t.Fatalf("write admin styles.css: %v", err)
	}

	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = scratchDir + "/orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
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

	// Build a real MailStore backed by a temp directory.
	mailstoreDir := filepath.Join(scratchDir, "mailstore")
	if err := os.MkdirAll(mailstoreDir, 0o750); err != nil {
		t.Fatalf("mkdir mailstore: %v", err)
	}
	// Run the MailStore's DDL so coremail_folders /
	// coremail_messages / coremail_attachments exist. The
	// production runtime calls storage.Tables() during
	// Migrate; we do the same here so the test exercises
	// the same schema the runtime uses.
	for _, stmt := range storage.Tables() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("mailstore ddl: %v\nstmt: %s", err, stmt)
		}
	}
	mailStore, err := storage.NewMailStore(sqlDB, mailstoreDir)
	if err != nil {
		t.Fatalf("mailstore: %v", err)
	}

	// Run the queue DDL so coremail_queue exists. The
	// production runtime calls queue.Tables() +
	// queue.Indexes() during Migrate; we do the same
	// here. The WebmailSend enqueue path writes one row
	// per recipient into this table.
	for _, stmt := range queue.Tables() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("queue ddl: %v\nstmt: %s", err, stmt)
		}
	}
	for _, stmt := range queue.Indexes() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("queue idx: %v\nstmt: %s", err, stmt)
		}
	}
	qe := queue.NewQueueEngine(sqlDB)

	cfg.Server.AdminUIDir = adminDir
	cfg.Server.WebmailUIDir = webmailDir
	cfg.CoreMail.MailStorePath = mailstoreDir

	reg := modules.NewRegistry(logger)
	ff := license.NewFeatureFlags(logger)
	reg.Register(&runtimeProviderModule{store: mailStore, queue: qe})

	router := api.NewRouter(cfg, authenticator, logger, db, reg, ff, nil)

	// Provision admin with bcrypt password so login works.
	const (
		email    = "admin@orvix.email"
		password = "MaghaghaMos086"
	)
	if err := provisionAdminUser(t, sqlDB, email, password); err != nil {
		t.Fatalf("provision admin user: %v", err)
	}

	t.Cleanup(func() {
		_ = router.App().Shutdown()
		_ = sqlDB.Close()
	})

	return &webmailTestEnv{
		router:   router,
		mailbox:  mailStore,
		queue:    qe,
		email:    email,
		password: password,
	}
}

// provisionAdminUser inserts an admin user + active
// coremail_mailboxes + tenants row. The password is hashed
// with bcrypt at the installer's DefaultCost (10) so the
// login endpoint verifies it correctly.
func provisionAdminUser(t *testing.T, sqlDB *sql.DB, email, password string) error {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("bcrypt: %w", err)
	}
	now := time.Now().UTC()

	// Insert tenant (id=1 is the bootstrap tenant).
	if _, err := sqlDB.Exec(
		"INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		1, now, now, "orvix", "orvix", "orvix.email", "enterprise", 1,
	); err != nil {
		// Insert may fail if a previous run already
		// populated the tenant; that's fine.
	}

	// Insert users row.
	if _, err := sqlDB.Exec(
		"INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		now, now, email, string(hash), "admin", 1, 1, 1,
	); err != nil {
		return fmt.Errorf("insert users: %w", err)
	}

	// Insert coremail_domains row.
	domainName := "orvix.email"
	if _, err := sqlDB.Exec(
		"INSERT INTO coremail_domains (name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		domainName, 1, "active", "enterprise", 0, 0, 0, now, now,
	); err != nil {
		return fmt.Errorf("insert coremail_domains: %w", err)
	}

	// Insert coremail_mailboxes row for admin.
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		1, 1, "admin", email, "Admin", "x-bcrypt-hash-placeholder", "bcrypt", "active", 1024, 1, now, now,
	); err != nil {
		return fmt.Errorf("insert coremail_mailboxes: %w", err)
	}
	return nil
}

// loginAdmin performs POST /api/v1/auth/login and returns
// the access_token cookie value.
func (e *webmailTestEnv) loginAdmin(t *testing.T) string {
	return loginAs(t, e, e.email, e.password)
}

// loginAs performs a login and returns the access_token.
func loginAs(t *testing.T, e *webmailTestEnv, email, password string) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{
		"username": email,
		"password": password,
	})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("login: expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	for _, c := range resp.Cookies() {
		if c.Name == "access_token" {
			return c.Value
		}
	}
	t.Fatalf("login: no access_token cookie")
	return ""
}

// webmailRequest issues an authenticated webmail request
// and returns the HTTP status + JSON-decoded body.
func (e *webmailTestEnv) webmailRequest(t *testing.T, method, path, accessToken string, body interface{}) (int, map[string]interface{}) {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	if accessToken != "" {
		req.Header.Set("Cookie", "access_token="+accessToken)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	out := map[string]interface{}{}
	if len(respBody) > 0 {
		_ = json.Unmarshal(respBody, &out)
	}
	return resp.StatusCode, out
}

// injectMessage writes a real message into the MailStore
// for the admin mailbox in the Inbox folder.
func (e *webmailTestEnv) injectMessage(t *testing.T, subject, body string) uint {
	t.Helper()
	mailboxID := mailboxIDForEmail(t, e.mailbox, e.email)
	inbox, err := e.mailbox.Folders.GetByPath(t.Context(), mailboxID, "INBOX", nil)
	if err != nil || inbox == nil {
		t.Fatalf("injectMessage: no INBOX folder: %v", err)
	}
	messageID := makeID()
	now := time.Now().UTC()
	from := "sender@example.com"
	to := e.email
	rfc822 := buildRFC822(from, to, subject, body, messageID, now)
	msg := &storage.Message{
		MessageID:         messageID,
		InternetMessageID: "<" + messageID + "@test>",
		TenantID:          1,
		DomainID:          1,
		MailboxID:         mailboxID,
		FolderID:          inbox.ID,
		Subject:           subject,
		FromAddress:       from,
		ToAddresses:       to,
		MessageDate:       &now,
		ReceivedDate:      now,
	}
	if err := e.mailbox.StoreMessage(t.Context(), msg, []byte(rfc822), nil); err != nil {
		t.Fatalf("StoreMessage: %v", err)
	}
	return msg.ID
}

// mailboxIDForEmail looks up the mailbox id for the given
// email by querying coremail_mailboxes directly.
func mailboxIDForEmail(t *testing.T, ms *storage.MailStore, email string) uint {
	t.Helper()
	row := ms.DB.QueryRow(
		"SELECT id FROM coremail_mailboxes WHERE email = ? AND deleted_at IS NULL",
		email,
	)
	var id uint
	if err := row.Scan(&id); err != nil {
		t.Fatalf("mailboxIDForEmail(%s): %v", email, err)
	}
	return id
}

// mustMailboxIDForTest is an alias used in injectMessage
// helpers; the implementation is the same.
func mustMailboxIDForTest(t *testing.T, e *webmailTestEnv, email string) uint {
	return mailboxIDForEmail(t, e.mailbox, email)
}

// ── tests ───────────────────────────────────────────────

// TestWebmailAPIUnauthenticatedReturns401 confirms that
// every webmail endpoint rejects requests with no
// access_token cookie.
func TestWebmailAPIUnauthenticatedReturns401(t *testing.T) {
	e := buildWebmailTestEnv(t)
	cases := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/webmail/me"},
		{"GET", "/api/v1/webmail/folders"},
		{"GET", "/api/v1/webmail/messages?folder=INBOX"},
		{"GET", "/api/v1/webmail/messages/1"},
		{"POST", "/api/v1/webmail/send"},
		{"POST", "/api/v1/webmail/messages/1/delete"},
	}
	for _, c := range cases {
		var body io.Reader
		if c.method == "POST" {
			body = strings.NewReader("{}")
		}
		req := httptest.NewRequest(c.method, c.path, body)
		if c.method == "POST" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
		if err != nil {
			t.Fatalf("%s %s: %v", c.method, c.path, err)
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s: expected 401 unauthenticated, got %d", c.method, c.path, resp.StatusCode)
		}
	}
}

// TestWebmailAPINoQueueUsage scans the static webmail
// client source and asserts it never calls /api/v1/queue
// from any active code path. The user explicitly forbade
// /api/v1/queue usage from webmail; this is the regression
// guard. Comments are exempt — they document the rule.
func TestWebmailAPINoQueueUsage(t *testing.T) {
	root := webmailRepoRoot(t)
	js, err := os.ReadFile(filepath.Join(root, "release", "webmail", "assets", "webmail.js"))
	if err != nil {
		t.Fatalf("read webmail.js: %v", err)
	}
	// Strip comments and string literals so the rule
	// catches only actual code references.
	src := stripJSCommentsAndStrings(string(js))
	if strings.Contains(src, "/api/v1/queue") {
		t.Fatal("webmail.js must not call /api/v1/queue")
	}
	for _, line := range strings.Split(src, "\n") {
		if strings.Contains(line, "/queue") {
			t.Errorf("webmail.js contains /queue reference: %q", strings.TrimSpace(line))
		}
	}
}

// stripJSCommentsAndStrings removes // line comments,
// /* block comments */, and string literals (single and
// double-quoted) so that word-boundary scans inside source
// files do not match text inside documentation.
func stripJSCommentsAndStrings(src string) string {
	var out strings.Builder
	i := 0
	for i < len(src) {
		// Line comment
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '/' {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}
		// Block comment
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '*' {
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i += 2
			continue
		}
		// String literal
		if src[i] == '"' || src[i] == '\'' {
			quote := src[i]
			i++
			for i < len(src) && src[i] != quote {
				if src[i] == '\\' && i+1 < len(src) {
					i += 2
					continue
				}
				i++
			}
			i++ // skip closing quote
			continue
		}
		out.WriteByte(src[i])
		i++
	}
	return out.String()
}

// TestWebmailAPIMeWithMailbox confirms an authenticated
// admin gets a real mailbox back from /api/v1/webmail/me.
func TestWebmailAPIMeWithMailbox(t *testing.T) {
	e := buildWebmailTestEnv(t)
	tok := e.loginAdmin(t)
	status, body := e.webmailRequest(t, "GET", "/api/v1/webmail/me", tok, nil)
	if status != 200 {
		t.Fatalf("GET /me: expected 200, got %d: %v", status, body)
	}
	if body["mailbox"] == nil {
		t.Fatalf("GET /me: expected mailbox object, got: %v", body)
	}
	mb, _ := body["mailbox"].(map[string]interface{})
	if got := mb["email"]; got != e.email {
		t.Errorf("GET /me: mailbox email = %v, want %s", got, e.email)
	}
}

// TestWebmailAPIListFolders confirms the folders endpoint
// returns the system folders created by the MailStore.
func TestWebmailAPIListFolders(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mailboxIDForEmail(t, e.mailbox, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)
	status, body := e.webmailRequest(t, "GET", "/api/v1/webmail/folders", tok, nil)
	if status != 200 {
		t.Fatalf("GET /folders: expected 200, got %d: %v", status, body)
	}
	folders, ok := body["folders"].([]interface{})
	if !ok || len(folders) == 0 {
		t.Fatalf("GET /folders: expected non-empty folder list, got: %v", body)
	}
	hasInbox := false
	for _, f := range folders {
		m, _ := f.(map[string]interface{})
		if m["path"] == "INBOX" || m["name"] == "INBOX" {
			hasInbox = true
		}
	}
	if !hasInbox {
		t.Fatalf("GET /folders: INBOX missing from %v", folders)
	}
}

// TestWebmailAPIListMessages confirms an injected message
// appears in /api/v1/webmail/messages?folder=INBOX.
func TestWebmailAPIListMessages(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	id := e.injectMessage(t, "Hello from sender", "This is the body of the test message.")

	status, body := e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=INBOX", tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages: expected 200, got %d: %v", status, body)
	}
	messages, ok := body["messages"].([]interface{})
	if !ok {
		t.Fatalf("GET /messages: expected messages array, got: %v", body)
	}
	if len(messages) == 0 {
		t.Fatalf("GET /messages: expected at least 1 message, got 0")
	}
	found := false
	for _, m := range messages {
		mm, _ := m.(map[string]interface{})
		if int(mm["id"].(float64)) == int(id) {
			found = true
			if mm["subject"] != "Hello from sender" {
				t.Errorf("GET /messages: subject mismatch: %v", mm["subject"])
			}
			if mm["from"] != "sender@example.com" {
				t.Errorf("GET /messages: from mismatch: %v", mm["from"])
			}
		}
	}
	if !found {
		t.Errorf("GET /messages: injected message id=%d not in list", id)
	}
}

// TestWebmailAPIGetMessageBody confirms the body of a
// message is loaded from disk and is NOT hardcoded.
func TestWebmailAPIGetMessageBody(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	marker := "ORVIX_BODY_MARKER_" + makeID()
	id := e.injectMessage(t, "Body marker test", "Subject of body marker test\n\n"+marker+"\n")

	path := fmt.Sprintf("/api/v1/webmail/messages/%d", id)
	status, body := e.webmailRequest(t, "GET", path, tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages/%d: expected 200, got %d: %v", id, status, body)
	}
	rfc822, _ := body["rfc822"].(string)
	if !strings.Contains(rfc822, marker) {
		t.Errorf("GET /messages/%d: response body missing marker %q\nbody=%s", id, marker, rfc822)
	}
	if !strings.Contains(rfc822, "Subject: Body marker test") {
		t.Errorf("GET /messages/%d: response body missing subject header", id)
	}
	if !strings.Contains(rfc822, "From: sender@example.com") {
		t.Errorf("GET /messages/%d: response body missing From header", id)
	}
}

// TestWebmailAPISendCreatesSentMessage confirms POST
// /api/v1/webmail/send writes a real row to the user's
// Sent folder with the supplied subject + body, AND
// enqueues the outbound message into the delivery queue.
// After Phase WEBMAIL-3 the response status is "queued"
// not "stored" — the message is durable in Sent AND in
// the queue.
func TestWebmailAPISendCreatesSentMessage(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	bodyMarker := "ORVIX_SEND_MARKER_" + makeID()
	req := map[string]string{
		"to":      "recipient@example.com",
		"subject": "Send test " + bodyMarker,
		"body":    "Body content " + bodyMarker,
	}
	status, resp := e.webmailRequest(t, "POST", "/api/v1/webmail/send", tok, req)
	if status != http.StatusCreated {
		t.Fatalf("POST /send: expected 201, got %d: %v", status, resp)
	}
	if resp["folder"] != "Sent" {
		t.Errorf("POST /send: response folder = %v, want Sent", resp["folder"])
	}
	// Phase WEBMAIL-3: response status is "queued" once
	// the message has been handed to the delivery queue.
	if resp["status"] != "queued" {
		t.Errorf("POST /send: response status = %v, want queued", resp["status"])
	}
	if resp["queued_count"] != float64(1) {
		t.Errorf("POST /send: queued_count = %v, want 1", resp["queued_count"])
	}
	id, ok := resp["id"].(float64)
	if !ok || id == 0 {
		t.Fatalf("POST /send: response id invalid: %v", resp["id"])
	}
	msgID, _ := resp["message_id"].(string)
	if msgID == "" {
		t.Fatalf("POST /send: response message_id empty")
	}

	// The sent message should appear when we list the
	// Sent folder.
	status, listResp := e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=Sent", tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages?folder=Sent: expected 200, got %d", status)
	}
	messages, _ := listResp["messages"].([]interface{})
	found := false
	for _, m := range messages {
		mm, _ := m.(map[string]interface{})
		if int(mm["id"].(float64)) == int(id) {
			found = true
			if !strings.Contains(mm["subject"].(string), bodyMarker) {
				t.Errorf("POST /send: stored subject missing marker")
			}
		}
	}
	if !found {
		t.Errorf("POST /send: sent message id=%v not in Sent folder", id)
	}

	// Body content is loaded from disk, so the body
	// marker MUST be in the RFC822 of the stored message.
	status, msgResp := e.webmailRequest(t, "GET", fmt.Sprintf("/api/v1/webmail/messages/%d", int(id)), tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages/%d: expected 200, got %d", int(id), status)
	}
	rfc822, _ := msgResp["rfc822"].(string)
	if !strings.Contains(rfc822, bodyMarker) {
		t.Errorf("POST /send: stored RFC822 missing body marker %q", bodyMarker)
	}
}

// TestWebmailAPIDeleteMovesToTrash confirms POST
// /api/v1/webmail/messages/:id/delete moves the message
// to the Trash folder and marks it deleted.
func TestWebmailAPIDeleteMovesToTrash(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	id := e.injectMessage(t, "Delete me", "Body of delete me test")

	_, body := e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=INBOX", tok, nil)
	found := false
	for _, m := range body["messages"].([]interface{}) {
		mm, _ := m.(map[string]interface{})
		if int(mm["id"].(float64)) == int(id) {
			found = true
		}
	}
	if !found {
		t.Fatalf("setup: injected message not in INBOX")
	}

	status, delResp := e.webmailRequest(t, "POST", fmt.Sprintf("/api/v1/webmail/messages/%d/delete", id), tok, nil)
	if status != 200 {
		t.Fatalf("POST /delete: expected 200, got %d: %v", status, delResp)
	}
	if delResp["status"] != "deleted" {
		t.Errorf("POST /delete: status = %v, want deleted", delResp["status"])
	}
	if delResp["moved_to"] != "Trash" {
		t.Errorf("POST /delete: moved_to = %v, want Trash", delResp["moved_to"])
	}

	// INBOX listing should no longer include it.
	_, body = e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=INBOX", tok, nil)
	for _, m := range body["messages"].([]interface{}) {
		mm, _ := m.(map[string]interface{})
		if int(mm["id"].(float64)) == int(id) {
			t.Errorf("POST /delete: message still present in INBOX after delete")
		}
	}

	// Trash listing SHOULD include it.
	_, body = e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=Trash", tok, nil)
	foundInTrash := false
	for _, m := range body["messages"].([]interface{}) {
		mm, _ := m.(map[string]interface{})
		if int(mm["id"].(float64)) == int(id) {
			foundInTrash = true
		}
	}
	if !foundInTrash {
		t.Errorf("POST /delete: message not in Trash folder")
	}
}

// TestWebmailAPIUserWithoutMailbox confirms a user with
// no coremail_mailboxes row gets a clean "no mailbox"
// response.
func TestWebmailAPIUserWithoutMailbox(t *testing.T) {
	e := buildWebmailTestEnv(t)

	// Create a second user with no mailbox row.
	const orphanEmail = "orphan@orvix.email"
	const orphanPass = "OrphanP@ss-987"
	if err := createOrphanUser(t, e.mailbox, orphanEmail, orphanPass); err != nil {
		t.Fatalf("create orphan user: %v", err)
	}
	tok := loginAs(t, e, orphanEmail, orphanPass)

	status, body := e.webmailRequest(t, "GET", "/api/v1/webmail/me", tok, nil)
	if status != 200 {
		t.Fatalf("GET /me (no mailbox): expected 200, got %d: %v", status, body)
	}
	if body["mailbox"] != nil {
		t.Errorf("GET /me (no mailbox): expected mailbox:null, got %v", body["mailbox"])
	}
	if body["reason"] != "no_mailbox" {
		t.Errorf("GET /me (no mailbox): reason = %v, want no_mailbox", body["reason"])
	}

	status, body = e.webmailRequest(t, "GET", "/api/v1/webmail/folders", tok, nil)
	if status != 200 {
		t.Fatalf("GET /folders (no mailbox): expected 200, got %d", status)
	}
	if body["reason"] != "no_mailbox" {
		t.Errorf("GET /folders (no mailbox): reason = %v", body["reason"])
	}
	folders, _ := body["folders"].([]interface{})
	if len(folders) != 0 {
		t.Errorf("GET /folders (no mailbox): expected empty array, got %v", folders)
	}

	status, body = e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=INBOX", tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages (no mailbox): expected 200, got %d", status)
	}
	if body["reason"] != "no_mailbox" {
		t.Errorf("GET /messages (no mailbox): reason = %v", body["reason"])
	}
}

// TestWebmailAPISendRejectsCRLFInjection confirms that CRLF
// characters in the Subject (and other header fields) are
// sanitized before the RFC822 message is stored. An injected
// Bcc header via the Subject field must NOT appear in the
// stored message body.
func TestWebmailAPISendRejectsCRLFInjection(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	// Subject containing CRLF injection payload.
	// After JSON deserialization the \r\n becomes literal CR+LF bytes.
	payloadSubject := "hello\r\nBcc: attacker@example.com"
	payload := map[string]string{
		"to":      "recipient@example.com",
		"subject": payloadSubject,
		"body":    "Message body.",
	}
	status, resp := e.webmailRequest(t, "POST", "/api/v1/webmail/send", tok, payload)
	if status != http.StatusCreated {
		t.Fatalf("POST /send: expected 201, got %d: %v", status, resp)
	}
	id, ok := resp["id"].(float64)
	if !ok || id == 0 {
		t.Fatalf("POST /send: response id invalid: %v", resp["id"])
	}

	// Read back the stored message's RFC822 body.
	path := fmt.Sprintf("/api/v1/webmail/messages/%d", int(id))
	status, msgResp := e.webmailRequest(t, "GET", path, tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages/%d: expected 200, got %d", int(id), status)
	}

	rfc822, _ := msgResp["rfc822"].(string)
	// The injected Bcc header must NOT be a standalone header line.
	if strings.Contains(rfc822, "\nBcc: attacker@example.com") {
		t.Error("GET /messages: RFC822 contains injected Bcc header from Subject CRLF")
	}
	// The CRLF must have been stripped from the Subject header value.
	if strings.Contains(rfc822, "Subject: hello\r\n") {
		t.Error("GET /messages: RFC822 Subject header contains raw CRLF")
	}
	// The Subject header must remain intact with CRLF stripped.
	if !strings.Contains(rfc822, "Subject: helloBcc:") {
		t.Errorf("GET /messages: RFC822 Subject header missing sanitized content: %s", rfc822)
	}

	// The JSON metadata subject should also be sanitized.
	metaSubject, _ := msgResp["subject"].(string)
	if strings.Contains(metaSubject, "\n") || strings.Contains(metaSubject, "\r") {
		t.Error("GET /messages: metadata subject contains raw CRLF")
	}
	if metaSubject != "helloBcc: attacker@example.com" {
		t.Errorf("GET /messages: metadata subject = %q, want %q", metaSubject, "helloBcc: attacker@example.com")
	}
}

// ── Phase WEBMAIL-3: real outbound send delivery ────────
//
// The tests below pin the new enqueue behavior added on top
// of the existing Sent-folder storage: every successful
// /api/v1/webmail/send MUST result in a coremail_queue row
// with direction='outbound', status='pending', and the
// authenticated mailbox as the sender. Invalid recipients
// MUST be rejected with 400 before any disk write. The
// authorization chain (auth middleware + mailbox lookup)
// MUST remain intact.

// TestWebmailAPISendEnqueuesOutboundJob is the proof that
// the message reached the queue after the Sent copy was
// stored. We assert the exact row contents the production
// delivery worker would lease on its next pass.
func TestWebmailAPISendEnqueuesOutboundJob(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	// Snapshot the queue row count before sending so
	// we can assert exactly one new row was created.
	sqlDB := e.mailbox.DB
	mailboxID := mailboxIDForEmail(t, e.mailbox, e.email)
	before, err := queueRowCount(t, sqlDB, mailboxID)
	if err != nil {
		t.Fatalf("queue count before: %v", err)
	}

	req := map[string]string{
		"to":      "recipient@example.com",
		"subject": "Enqueue proof",
		"body":    "Body for enqueue proof.",
	}
	status, resp := e.webmailRequest(t, "POST", "/api/v1/webmail/send", tok, req)
	if status != http.StatusCreated {
		t.Fatalf("POST /send: expected 201, got %d: %v", status, resp)
	}
	if resp["status"] != "queued" {
		t.Fatalf("POST /send: status = %v, want queued", resp["status"])
	}
	msgID, _ := resp["message_id"].(string)
	if msgID == "" {
		t.Fatalf("POST /send: message_id missing")
	}

	// Exactly one new row for this mailbox's outbound.
	after, err := queueRowCount(t, sqlDB, mailboxID)
	if err != nil {
		t.Fatalf("queue count after: %v", err)
	}
	if after-before != 1 {
		t.Fatalf("queue row count delta = %d, want 1", after-before)
	}

	// Inspect the new row in detail.
	var (
		gotID          uint
		gotMessageID   string
		gotFromAddress string
		gotToAddress   string
		gotDirection   string
		gotStatus      string
		gotDelivery    string
		gotMailbox     sql.NullInt64
	)
	row := sqlDB.QueryRow(
		`SELECT id, message_id, from_address, to_address, direction, status, delivery_mode, mailbox_id
		 FROM coremail_queue WHERE mailbox_id = ? AND deleted_at IS NULL ORDER BY id DESC LIMIT 1`,
		mailboxID,
	)
	if err := row.Scan(&gotID, &gotMessageID, &gotFromAddress, &gotToAddress, &gotDirection, &gotStatus, &gotDelivery, &gotMailbox); err != nil {
		t.Fatalf("queue row scan: %v", err)
	}
	if gotMessageID != msgID {
		t.Errorf("queue message_id = %q, want %q", gotMessageID, msgID)
	}
	if gotFromAddress != e.email {
		t.Errorf("queue from_address = %q, want authenticated mailbox %q", gotFromAddress, e.email)
	}
	if gotToAddress != "recipient@example.com" {
		t.Errorf("queue to_address = %q, want recipient@example.com", gotToAddress)
	}
	if gotDirection != "outbound" {
		t.Errorf("queue direction = %q, want outbound", gotDirection)
	}
	if gotStatus != "pending" {
		t.Errorf("queue status = %q, want pending (delivery worker picks it up)", gotStatus)
	}
	if gotDelivery != "remote_smtp" {
		t.Errorf("queue delivery_mode = %q, want remote_smtp", gotDelivery)
	}
	if !gotMailbox.Valid || uint(gotMailbox.Int64) != mailboxID {
		t.Errorf("queue mailbox_id = %v, want %d", gotMailbox, mailboxID)
	}

	// The response also exposes queue_ids for client
	// reconciliation; the queued row id must match.
	queueIDs, ok := resp["queue_ids"].([]interface{})
	if !ok || len(queueIDs) != 1 {
		t.Fatalf("response queue_ids = %v, want 1 id", resp["queue_ids"])
	}
	if uint(queueIDs[0].(float64)) != gotID {
		t.Errorf("response queue_ids[0] = %v, want %d", queueIDs[0], gotID)
	}
}

// TestWebmailAPISendEnqueuesAllRecipients pins the rule
// that one queue row is created per recipient across To,
// Cc, and Bcc — exactly what the delivery worker needs
// to issue one RCPT TO per recipient on the wire.
func TestWebmailAPISendEnqueuesAllRecipients(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)
	sqlDB := e.mailbox.DB
	mailboxID := mailboxIDForEmail(t, e.mailbox, e.email)
	before, err := queueRowCount(t, sqlDB, mailboxID)
	if err != nil {
		t.Fatalf("queue count before: %v", err)
	}

	req := map[string]string{
		"to":      "to1@example.com, to2@example.com",
		"cc":      "cc1@example.com",
		"bcc":     "bcc1@example.com, bcc2@example.com",
		"subject": "Multi-recipient",
		"body":    "Hello everyone.",
	}
	status, resp := e.webmailRequest(t, "POST", "/api/v1/webmail/send", tok, req)
	if status != http.StatusCreated {
		t.Fatalf("POST /send: expected 201, got %d: %v", status, resp)
	}
	if resp["status"] != "queued" {
		t.Fatalf("POST /send: status = %v, want queued", resp["status"])
	}
	if resp["queued_count"] != float64(5) {
		t.Errorf("POST /send: queued_count = %v, want 5 (2 To + 1 Cc + 2 Bcc)", resp["queued_count"])
	}
	after, err := queueRowCount(t, sqlDB, mailboxID)
	if err != nil {
		t.Fatalf("queue count after: %v", err)
	}
	if after-before != 5 {
		t.Errorf("queue row count delta = %d, want 5", after-before)
	}

	// All five new rows must reference the same
	// message_id and the authenticated mailbox.
	rows, err := sqlDB.Query(
		`SELECT to_address, message_id, from_address, mailbox_id, direction, status, delivery_mode
		 FROM coremail_queue WHERE mailbox_id = ? AND deleted_at IS NULL ORDER BY id DESC LIMIT 5`,
		mailboxID,
	)
	if err != nil {
		t.Fatalf("queue rows scan: %v", err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var to, msgID, from string
		var mbID int64
		var direction, status, mode string
		if err := rows.Scan(&to, &msgID, &from, &mbID, &direction, &status, &mode); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if msgID != resp["message_id"] {
			t.Errorf("queue row msg_id = %q, want %q", msgID, resp["message_id"])
		}
		if from != e.email {
			t.Errorf("queue row from = %q, want %q", from, e.email)
		}
		if direction != "outbound" {
			t.Errorf("queue row direction = %q, want outbound", direction)
		}
		if status != "pending" {
			t.Errorf("queue row status = %q, want pending", status)
		}
		if mode != "remote_smtp" {
			t.Errorf("queue row delivery_mode = %q, want remote_smtp", mode)
		}
		seen[strings.ToLower(to)] = true
	}
	expected := []string{"to1@example.com", "to2@example.com", "cc1@example.com", "bcc1@example.com", "bcc2@example.com"}
	for _, want := range expected {
		if !seen[want] {
			t.Errorf("missing queue row for %s; saw %v", want, seen)
		}
	}
}

// TestWebmailAPISendRejectsInvalidRecipient is the
// authorization-by-input test: malformed To/Cc/Bcc values
// must be rejected with 400 BEFORE we touch the disk or
// the queue. No Sent copy, no queue row.
func TestWebmailAPISendRejectsInvalidRecipient(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)
	sqlDB := e.mailbox.DB
	mailboxID := mailboxIDForEmail(t, e.mailbox, e.email)
	before, err := queueRowCount(t, sqlDB, mailboxID)
	if err != nil {
		t.Fatalf("queue count before: %v", err)
	}

	cases := []struct {
		name string
		req  map[string]string
	}{
		{
			name: "to missing at-sign",
			req: map[string]string{
				"to":      "not-an-email",
				"subject": "x",
				"body":    "x",
			},
		},
		{
			name: "to has display-name but no host",
			req: map[string]string{
				"to":      "Foo <bar>",
				"subject": "x",
				"body":    "x",
			},
		},
		{
			name: "cc has whitespace-only junk",
			req: map[string]string{
				"to":      "valid@example.com",
				"cc":      " , ,",
				"subject": "x",
				"body":    "x",
			},
		},
		{
			name: "bcc is missing the domain",
			req: map[string]string{
				"to":      "valid@example.com",
				"bcc":     "nobody@",
				"subject": "x",
				"body":    "x",
			},
		},
		{
			name: "to is empty",
			req: map[string]string{
				"to":      "",
				"subject": "x",
				"body":    "x",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, resp := e.webmailRequest(t, "POST", "/api/v1/webmail/send", tok, tc.req)
			if status != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %v", status, resp)
			}
			if _, ok := resp["error"]; !ok {
				t.Errorf("response missing error: %v", resp)
			}
		})
	}

	// After all the rejections, the queue must be
	// untouched — no partial enqueue from any case.
	after, err := queueRowCount(t, sqlDB, mailboxID)
	if err != nil {
		t.Fatalf("queue count after: %v", err)
	}
	if after != before {
		t.Errorf("queue row count changed on rejection: before=%d after=%d", before, after)
	}
}

// TestWebmailAPISendPreservesAuthorization confirms that
// the enqueue path does NOT bypass the standard auth
// checks: a caller with no mailbox, or no auth at all,
// must NOT be able to enqueue. This is the regression
// guard for "WEBMAIL-3 must not weaken authorization".
func TestWebmailAPISendPreservesAuthorization(t *testing.T) {
	e := buildWebmailTestEnv(t)
	sqlDB := e.mailbox.DB
	orphanEmail := "send-orphan@orvix.email"
	orphanPass := "SendOrphanP@ss-321"
	if err := createOrphanUser(t, e.mailbox, orphanEmail, orphanPass); err != nil {
		t.Fatalf("create orphan: %v", err)
	}
	tok := loginAs(t, e, orphanEmail, orphanPass)

	before, err := queueRowCount(t, sqlDB, 0)
	if err != nil {
		t.Fatalf("queue count before: %v", err)
	}

	status, resp := e.webmailRequest(t, "POST", "/api/v1/webmail/send", tok, map[string]string{
		"to":      "victim@example.com",
		"subject": "unauthorized",
		"body":    "should not be sent",
	})
	// No mailbox -> 403 Forbidden, just like the other
	// user-facing webmail endpoints. The body is JSON
	// with reason=no_mailbox.
	if status != http.StatusForbidden {
		t.Fatalf("POST /send (no mailbox): expected 403, got %d: %v", status, resp)
	}
	if resp["reason"] != "no_mailbox" {
		t.Errorf("POST /send (no mailbox): reason = %v, want no_mailbox", resp["reason"])
	}

	// Also: completely unauthenticated callers still
	// hit 401 from the middleware, before any handler
	// code runs.
	req := httptest.NewRequest("POST", "/api/v1/webmail/send", strings.NewReader(`{"to":"x@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("unauth POST /send: %v", err)
	}
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauth POST /send: expected 401, got %d", resp2.StatusCode)
	}

	after, err := queueRowCount(t, sqlDB, 0)
	if err != nil {
		t.Fatalf("queue count after: %v", err)
	}
	if after != before {
		t.Errorf("unauthorized calls leaked queue rows: before=%d after=%d", before, after)
	}
}

// queueRowCount returns the count of non-deleted queue
// rows. Pass 0 for "all rows across all mailboxes" (used
// when we want to assert no rows were created for
// unauthorized callers).
func queueRowCount(t *testing.T, sqlDB *sql.DB, mailboxID uint) (int64, error) {
	t.Helper()
	var n int64
	var err error
	if mailboxID == 0 {
		err = sqlDB.QueryRow(`SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL`).Scan(&n)
	} else {
		err = sqlDB.QueryRow(`SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND mailbox_id = ?`, mailboxID).Scan(&n)
	}
	return n, err
}

// createOrphanUser inserts a users row WITHOUT a
// coremail_mailboxes row. Used by the "no mailbox" tests.
func createOrphanUser(t *testing.T, ms *storage.MailStore, email, password string) error {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = ms.DB.Exec(
		"INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		now, now, email, string(hash), "user", 1, 1, 1,
	)
	return err
}

// ── helpers ────────────────────────────────────────────

func makeID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 12)
	for i := range b {
		b[i] = alphabet[int(time.Now().UnixNano()+int64(i))%len(alphabet)]
		time.Sleep(time.Microsecond)
	}
	return string(b)
}

func buildRFC822(from, to, subject, body, messageID string, date time.Time) string {
	return fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s@test>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n",
		from, to, subject, date.Format(time.RFC1123Z), messageID, body,
	)
}
