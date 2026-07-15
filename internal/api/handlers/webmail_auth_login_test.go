package handlers_test

// End-to-end tests for the new webmail login flow
// (fix/real-webmail-auth). These tests pin the contract
// that webmail is a real user mail client — not a
// session-gated admin-side page.
//
// The flow we pin:
//   - GET  /api/v1/webmail/session  with no cookie
//                                  returns 401 (not 200).
//   - POST /api/v1/webmail/login   with no body returns
//                                  400 (bad request), not 404
//                                  (not wired).
//   - POST /api/v1/webmail/login   with valid mailbox
//                                  email+password returns 200,
//                                  sets the HttpOnly
//                                  access_token cookie, and
//                                  the subsequent /me call
//                                  returns 200 with
//                                  authenticated:true.
//   - POST /api/v1/webmail/login   with wrong password
//                                  returns 401 (no cookie set).
//   - POST /api/v1/webmail/login   with an email that has
//                                  no coremail_mailboxes row
//                                  returns 401 (not 200).
//   - The webmail login does NOT require the admin
//                                  login first: a mailbox
//                                  user with no row in the
//                                  `users` table can still
//                                  log in via webmail.
//   - The webmail login is independent of the
//                                  /api/v1/auth/login
//                                  admin endpoint.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
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
	"go.uber.org/zap"
	"golang.org/x/crypto/argon2"
	"gorm.io/gorm"

	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
)

// webmailLoginEnv wires a real router + mailstore for
// the webmail login flow. The runtime provider module
// pattern matches the one in webmail_user_test.go but is
// kept local to this file so the two test files do not
// depend on each other's helpers.
type webmailLoginEnv struct {
	router   *api.Router
	mailbox  *storage.MailStore
	queue    *queue.QueueEngine
	db       *gorm.DB
	email    string
	password string
}

type webmailLoginRuntimeModule struct {
	store *storage.MailStore
	queue *queue.QueueEngine
}

func (m *webmailLoginRuntimeModule) ID() string         { return "coremail-runtime" }
func (m *webmailLoginRuntimeModule) Version() string    { return "test" }
func (m *webmailLoginRuntimeModule) Requires() []string { return nil }
func (m *webmailLoginRuntimeModule) Init(_ *config.Config, _ *gorm.DB) error {
	return nil
}
func (m *webmailLoginRuntimeModule) Start() error   { return nil }
func (m *webmailLoginRuntimeModule) Stop() error    { return nil }
func (m *webmailLoginRuntimeModule) Migrate() error { return nil }
func (m *webmailLoginRuntimeModule) MailStore() *storage.MailStore {
	return m.store
}
func (m *webmailLoginRuntimeModule) QueueEngine() *queue.QueueEngine {
	return m.queue
}

// buildWebmailLoginEnv wires a router with a MailStore
// + QueueEngine and provisions a single mailbox with a
// real Argon2id password so the webmail login flow can
// run end-to-end. The provision uses the public
// coremail.EnsureMailboxSystemFolders helper so the
// test exercises the same code path the installer and
// the CreateMailbox handler do.
func buildWebmailLoginEnv(t *testing.T) *webmailLoginEnv {
	t.Helper()

	root := webmailRepoRoot(t)
	webmailDir := filepath.Join(root, "release", "webmail")

	scratchDir := t.TempDir()
	adminDir := filepath.Join(scratchDir, "admin")
	if err := os.MkdirAll(adminDir, 0o755); err != nil {
		t.Fatalf("mkdir admin: %v", err)
	}
	_ = os.WriteFile(filepath.Join(adminDir, "index.html"), []byte("<html></html>"), 0o644)
	_ = os.WriteFile(filepath.Join(adminDir, "app.js"), []byte(""), 0o644)
	_ = os.WriteFile(filepath.Join(adminDir, "styles.css"), []byte(""), 0o644)

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

	mailstoreDir := filepath.Join(scratchDir, "mailstore")
	if err := os.MkdirAll(mailstoreDir, 0o750); err != nil {
		t.Fatalf("mkdir mailstore: %v", err)
	}
	for _, stmt := range storage.Tables() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("mailstore ddl: %v\nstmt: %s", err, stmt)
		}
	}
	mailStore, err := storage.NewMailStore(sqlDB, mailstoreDir)
	if err != nil {
		t.Fatalf("mailstore: %v", err)
	}
	for _, stmt := range queue.Tables() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("queue ddl: %v\nstmt: %s", err, stmt)
		}
	}
	qe := queue.NewQueueEngine(sqlDB)

	cfg.Server.AdminUIDir = adminDir
	cfg.Server.WebmailUIDir = webmailDir
	cfg.CoreMail.MailStorePath = mailstoreDir

	reg := modules.NewRegistry(logger)
	ff := license.NewFeatureFlags(logger)
	reg.Register(&webmailLoginRuntimeModule{store: mailStore, queue: qe})
	router := api.NewRouter(cfg, authenticator, logger, db, reg, ff, nil)

	// Provision a tenant + domain + mailbox directly,
	// using the standalone coremail helper so the test
	// exercises the same provisioner the install
	// bootstrap path uses.
	const (
		email    = "alice@orvix.email"
		password = "Orvix!Passw0rd-2026"
	)
	now := time.Now().UTC()
	if _, err := sqlDB.Exec(
		`INSERT INTO tenants (created_at, updated_at, name, slug, domain, plan, active) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		now, now, "orvix", "orvix", "orvix.email", "enterprise", 1,
	); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_domains (name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"orvix.email", 1, "active", "enterprise", 0, 0, 0, now, now,
	); err != nil {
		t.Fatalf("insert domain: %v", err)
	}
	hash, err := hashArgon2idTest(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	res, err := sqlDB.ExecContext(context.Background(), `
		INSERT INTO coremail_mailboxes
			(domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 'argon2id', 'active', 1024, 0, ?, ?)`,
		1, 1, "alice", email, "Alice", hash, now, now)
	if err != nil {
		t.Fatalf("insert mailbox: %v", err)
	}
	mailboxID, _ := res.LastInsertId()
	if err := coremail.EnsureMailboxSystemFolders(context.Background(), sqlDB, uint(mailboxID)); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}

	periodEnd := now.AddDate(0, 1, 0)
	sqlDB.Exec(`INSERT OR IGNORE INTO subscriptions (tenant_id, plan_id, status, billing_interval,
		current_period_start, current_period_end, send_limit_day, storage_mb, created_at, updated_at)
		VALUES (1, 'free', 'active', 'monthly', ?, ?, 500, 1024, ?, ?)`,
		now, periodEnd, now, now)

	t.Cleanup(func() {
		_ = router.App().Shutdown()
		_ = sqlDB.Close()
	})

	return &webmailLoginEnv{
		router:   router,
		mailbox:  mailStore,
		queue:    qe,
		db:       db,
		email:    email,
		password: password,
	}
}

// hashArgon2idTest produces a $argon2id$ hash with the
// same format hashPasswordArgon2id in handlers.go uses.
// We reproduce the format here to keep the test
// independent of the production helper's location.
func hashArgon2idTest(password string) (string, error) {
	const (
		mem     uint32 = 65536
		timeP   uint32 = 3
		threads uint8  = 4
		keyLen  uint32 = 32
	)
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, timeP, mem, threads, keyLen)
	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Key := base64.RawStdEncoding.EncodeToString(key)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", mem, timeP, threads, b64Salt, b64Key), nil
}

// ── tests ──────────────────────────────────────────────────

// TestWebmailSessionUnauthReturns401 pins the no-regression
// contract: an unauthenticated GET /api/v1/webmail/session
// MUST return 401, not 200. The auth gate's "show login
// form" branch keys on this status code.
func TestWebmailSessionUnauthReturns401(t *testing.T) {
	env := buildWebmailLoginEnv(t)
	resp := doWebmailRequest(t, env, "GET", "/api/v1/webmail/session", nil, "")
	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unauth GET /api/v1/webmail/session: expected 401, got %d, body=%s",
			resp.StatusCode, string(body))
	}
}

// TestWebmailLoginEmptyBodyReturns400 pins the contract
// that the login endpoint is registered. Without it,
// the request would return 404 (route not found).
func TestWebmailLoginEmptyBodyReturns400(t *testing.T) {
	env := buildWebmailLoginEnv(t)
	req := httptest.NewRequest("POST", "/api/v1/webmail/login", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := env.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		t.Fatal("POST /api/v1/webmail/login returned 404; the endpoint is not registered")
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty body: expected 400, got %d", resp.StatusCode)
	}
}

// TestWebmailLoginWithMailboxCredentialsSucceeds pins the
// primary contract: a mailbox user can log in via
// /api/v1/webmail/login with their own email+password,
// get a HttpOnly access_token cookie back, and the
// subsequent /session probe reports authenticated:true.
func TestWebmailLoginWithMailboxCredentialsSucceeds(t *testing.T) {
	env := buildWebmailLoginEnv(t)
	loginResp := postWebmailJSON(t, env, "/api/v1/webmail/login", map[string]string{
		"email":    env.email,
		"password": env.password,
	}, "")
	if loginResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(loginResp.Body)
		t.Fatalf("login: expected 200, got %d, body=%s", loginResp.StatusCode, string(body))
	}
	// The login response body must include the
	// authenticated:true flag and a mailbox descriptor.
	var loginBody map[string]interface{}
	body, _ := io.ReadAll(loginResp.Body)
	if err := json.Unmarshal(body, &loginBody); err != nil {
		t.Fatalf("login body decode: %v\nbody: %s", err, string(body))
	}
	if auth, ok := loginBody["authenticated"].(bool); !ok || !auth {
		t.Fatalf("login body: expected authenticated:true, got %v", loginBody)
	}
	mb, ok := loginBody["mailbox"].(map[string]interface{})
	if !ok {
		t.Fatalf("login body: missing mailbox block: %v", loginBody)
	}
	if got := mb["email"]; got != env.email {
		t.Fatalf("login body mailbox.email: expected %q, got %v", env.email, got)
	}

	// The access_token cookie must be set, HttpOnly,
	// Secure, with Path=/ so it covers /api/v1/webmail/*
	// and /admin/* on the same cross-subdomain domain.
	var accessCookie *http.Cookie
	for _, c := range loginResp.Cookies() {
		if c.Name == "access_token" {
			accessCookie = c
			break
		}
	}
	if accessCookie == nil {
		t.Fatal("login did not set access_token cookie")
	}
	if !accessCookie.HttpOnly {
		t.Error("access_token cookie must be HttpOnly")
	}
	if !accessCookie.Secure {
		t.Error("access_token cookie must be Secure")
	}
	if accessCookie.Path != "/" {
		t.Errorf("access_token cookie Path: expected /, got %q", accessCookie.Path)
	}

	// Now probe /api/v1/webmail/session with the
	// cookie. The endpoint must return 200 and
	// authenticated:true.
	sessionResp := doWebmailRequest(t, env, "GET", "/api/v1/webmail/session", nil, accessCookie.Value)
	if sessionResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(sessionResp.Body)
		t.Fatalf("/session with cookie: expected 200, got %d, body=%s",
			sessionResp.StatusCode, string(body))
	}
	body, _ = io.ReadAll(sessionResp.Body)
	var sessionBody map[string]interface{}
	if err := json.Unmarshal(body, &sessionBody); err != nil {
		t.Fatalf("/session body decode: %v", err)
	}
	if auth, ok := sessionBody["authenticated"].(bool); !ok || !auth {
		t.Fatalf("/session: expected authenticated:true, got %v", sessionBody)
	}
}

// TestWebmailLoginWithWrongPasswordReturns401 pins the
// no-credential-leak contract: a wrong password returns
// 401 and does not set a cookie. The rate limiter /
// security monitor should also record the failure —
// this test does not assert that (it is exercised
// elsewhere) but the 401 is the user-visible contract.
func TestWebmailLoginWithWrongPasswordReturns401(t *testing.T) {
	env := buildWebmailLoginEnv(t)
	loginResp := postWebmailJSON(t, env, "/api/v1/webmail/login", map[string]string{
		"email":    env.email,
		"password": "definitely-not-the-right-password",
	}, "")
	if loginResp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(loginResp.Body)
		t.Fatalf("wrong-password login: expected 401, got %d, body=%s",
			loginResp.StatusCode, string(body))
	}
	for _, c := range loginResp.Cookies() {
		if c.Name == "access_token" && c.Value != "" {
			t.Errorf("wrong-password login set access_token cookie (must not): %q", c.Value)
		}
	}
}

// TestWebmailLoginWithUnknownEmailReturns401 pins the
// no-user-enumeration contract: an unknown email
// returns the same 401 as a wrong password.
func TestWebmailLoginWithUnknownEmailReturns401(t *testing.T) {
	env := buildWebmailLoginEnv(t)
	loginResp := postWebmailJSON(t, env, "/api/v1/webmail/login", map[string]string{
		"email":    "ghost@orvix.email",
		"password": "anything",
	}, "")
	if loginResp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(loginResp.Body)
		t.Fatalf("unknown-email login: expected 401, got %d, body=%s",
			loginResp.StatusCode, string(body))
	}
}

// TestWebmailLoginDoesNotRequireAdminSession pins the
// core "webmail is its own login" contract: the mailbox
// user has NO row in the `users` table at the moment
// the login is attempted. The login must still succeed
// (the handler auto-provisions the user row).
func TestWebmailLoginDoesNotRequireAdminSession(t *testing.T) {
	env := buildWebmailLoginEnv(t)
	sqlDB, err := env.db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	// Pre-condition: the mailbox exists, but there is
	// no matching users row.
	var userCount int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM users WHERE email = ?", env.email).Scan(&userCount); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if userCount != 0 {
		t.Fatalf("pre-condition: expected 0 users rows for %q, got %d", env.email, userCount)
	}

	loginResp := postWebmailJSON(t, env, "/api/v1/webmail/login", map[string]string{
		"email":    env.email,
		"password": env.password,
	}, "")
	if loginResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(loginResp.Body)
		t.Fatalf("login without pre-existing user row: expected 200, got %d, body=%s",
			loginResp.StatusCode, string(body))
	}

	// Post-condition: a users row was auto-provisioned
	// with role="user" (the mailbox is not admin).
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM users WHERE email = ?", env.email).Scan(&userCount); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if userCount != 1 {
		t.Fatalf("post-condition: expected 1 users row for %q, got %d", env.email, userCount)
	}
	var role string
	if err := sqlDB.QueryRow("SELECT role FROM users WHERE email = ?", env.email).Scan(&role); err != nil {
		t.Fatalf("scan role: %v", err)
	}
	if role != "user" {
		t.Errorf("auto-provisioned user role: expected 'user', got %q", role)
	}
}

// TestWebmailLoginProvisionsSystemFoldersForFreshMailbox
// pins the contract that webmail login cannot fail with
// "Sent folder not found" — the login handler itself
// re-runs the system-folder provisioner for any mailbox
// that lacks Sent/Drafts/Trash/Junk/Archive. This makes
// the "Sent folder not found" bug impossible to reach
// via the user-facing path: even if a legacy mailbox
// pre-dates the CreateMailbox fix, the first webmail
// login patches it up.
func TestWebmailLoginProvisionsSystemFoldersForFreshMailbox(t *testing.T) {
	env := buildWebmailLoginEnv(t)
	sqlDB, err := env.db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	// Find the mailbox id.
	var mailboxID uint
	if err := sqlDB.QueryRow("SELECT id FROM coremail_mailboxes WHERE email = ?", env.email).Scan(&mailboxID); err != nil {
		t.Fatalf("mailbox lookup: %v", err)
	}
	// Wipe the system folders so the login is
	// exercised against a mailbox that lacks them.
	if _, err := sqlDB.Exec("DELETE FROM coremail_folders WHERE mailbox_id = ?", mailboxID); err != nil {
		t.Fatalf("wipe folders: %v", err)
	}
	var preCount int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_folders WHERE mailbox_id = ?", mailboxID).Scan(&preCount); err != nil {
		t.Fatalf("pre-count: %v", err)
	}
	if preCount != 0 {
		t.Fatalf("pre-condition: expected 0 folders, got %d", preCount)
	}

	// Log in. The handler must re-provision folders as
	// a side-effect.
	loginResp := postWebmailJSON(t, env, "/api/v1/webmail/login", map[string]string{
		"email":    env.email,
		"password": env.password,
	}, "")
	if loginResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(loginResp.Body)
		t.Fatalf("login: expected 200, got %d, body=%s", loginResp.StatusCode, string(body))
	}

	// Post-condition: the six canonical folders are
	// back, including Sent.
	requiredPaths := []string{"INBOX", "Sent", "Drafts", "Trash", "Junk", "Archive"}
	for _, p := range requiredPaths {
		var id uint
		err := sqlDB.QueryRow(
			"SELECT id FROM coremail_folders WHERE mailbox_id = ? AND path = ?",
			mailboxID, p,
		).Scan(&id)
		if err != nil {
			t.Errorf("post-condition: folder %q missing after login: %v", p, err)
		}
	}
}

// TestWebmailSendAfterLoginProvisionsSentFolder is the
// end-to-end "Sent folder not found" regression test:
// after a fresh install (or a legacy mailbox), a user
// can log in via webmail and successfully send a
// message — the Sent folder exists because the login
// handler provisioned it. Without the fix, the Send
// handler returns
//
//	"Sent folder not found for mailbox; ensure
//	 system folders are provisioned"
func TestWebmailSendAfterLoginProvisionsSentFolder(t *testing.T) {
	env := buildWebmailLoginEnv(t)
	sqlDB, err := env.db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	var mailboxID uint
	if err := sqlDB.QueryRow("SELECT id FROM coremail_mailboxes WHERE email = ?", env.email).Scan(&mailboxID); err != nil {
		t.Fatalf("mailbox lookup: %v", err)
	}
	// Wipe the folders so we test the login→provision
	//→send path.
	if _, err := sqlDB.Exec("DELETE FROM coremail_folders WHERE mailbox_id = ?", mailboxID); err != nil {
		t.Fatalf("wipe folders: %v", err)
	}

	loginResp := postWebmailJSON(t, env, "/api/v1/webmail/login", map[string]string{
		"email":    env.email,
		"password": env.password,
	}, "")
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login: %d", loginResp.StatusCode)
	}
	var accessCookie string
	for _, c := range loginResp.Cookies() {
		if c.Name == "access_token" {
			accessCookie = c.Value
		}
	}
	if accessCookie == "" {
		t.Fatal("no access_token cookie from login")
	}

	// Send a message via the real webmail send
	// endpoint.
	sendResp := postWebmailJSON(t, env, "/api/v1/webmail/send", map[string]string{
		"to":      "bob@example.com",
		"subject": "Test after fresh-login",
		"body":    "Sent folder should exist because login provisioned it.",
	}, accessCookie)
	if sendResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(sendResp.Body)
		t.Fatalf("webmail send: expected 201, got %d, body=%s", sendResp.StatusCode, string(body))
	}

	// The Sent folder must have at least one message.
	var sentCount int
	if err := sqlDB.QueryRow(`
		SELECT COUNT(*) FROM coremail_messages m
		JOIN coremail_folders f ON m.folder_id = f.id
		WHERE f.mailbox_id = ? AND f.path = 'Sent'`,
		mailboxID,
	).Scan(&sentCount); err != nil {
		t.Fatalf("sent count: %v", err)
	}
	if sentCount == 0 {
		t.Fatal("post-condition: Sent folder has no messages after a successful send")
	}
}

// TestWebmailGateDoesNotUseAdminSessionEndpoint pins the
// no-regression requirement that the JS auth gate does
// NOT probe /api/v1/me. We grep the JS file for any
// reference to the admin endpoint; if a future refactor
// accidentally re-introduces the dependency, this test
// fails before the user sees a blank webmail.
func TestWebmailGateDoesNotUseAdminSessionEndpoint(t *testing.T) {
	root := webmailRepoRoot(t)
	js := readFile(t, root, "release/webmail/assets/auth-gate.js")
	for _, needle := range []string{"/api/v1/me", "/auth/login", "/admin"} {
		if strings.Contains(js, needle) {
			t.Errorf("auth-gate.js must not reference %q (webmail must be independent of admin auth)", needle)
		}
	}
}

// ── helpers ─────────────────────────────────────────────

// doWebmailRequest issues a Fiber test request and
// returns the response. accessToken is appended as a
// Cookie header if non-empty.
func doWebmailRequest(t *testing.T, env *webmailLoginEnv, method, path string, body io.Reader, accessToken string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if accessToken != "" {
		req.Header.Set("Cookie", "access_token="+accessToken)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := env.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func postWebmailJSON(t *testing.T, env *webmailLoginEnv, path string, payload interface{}, accessToken string) *http.Response {
	t.Helper()
	buf, _ := json.Marshal(payload)
	return doWebmailRequest(t, env, "POST", path, bytes.NewReader(buf), accessToken)
}
