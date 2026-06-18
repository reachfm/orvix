package handlers_test

// Tests for the Webmail Send local-vs-remote
// classification fix (fix/prod-blockers).
//
// These tests pin the contract that:
//   - A recipient on a configured local domain with
//     an active mailbox is delivered through the
//     local MailStore path (no SMTP, no MX lookup,
//     no remote_smtp queue row).
//   - A recipient on a remote domain is delivered
//     through the remote_smtp path.
//   - A mixed recipient list splits correctly: local
//     recipients get a DeliveryLocal queue row, remote
//     recipients get a DeliveryRemoteSMTP queue row,
//     and the sender's Sent folder holds exactly one
//     copy of the message.
//   - The classifier is tenant-scoped: a sender in
//     tenant A cannot route to a mailbox in tenant B
//     through the local path.
//
// The tests are end-to-end: they go through the
// real router, the real auth middleware, the real
// webmail Send handler, the real queue engine, and
// the real MailStore. There is no parallel test
// pipeline — if the production behaviour regresses,
// these tests fail.

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
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

// webmailRoutingEnv wires a router with a MailStore +
// QueueEngine and provisions an admin mailbox (for
// sending) and a second local mailbox (for local
// delivery). The recipient mailbox exists in the
// same tenant as the admin so the local path is
// eligible.
type webmailRoutingEnv struct {
	router       *api.Router
	mailStore    *storage.MailStore
	qe           *queue.QueueEngine
	db           *gorm.DB
	sqlDB        *sql.DB
	senderEmail  string
	senderPass   string
	recipientEmail string
	recipientID  uint
}

func buildWebmailRoutingEnv(t *testing.T) *webmailRoutingEnv {
	t.Helper()

	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "wmroute.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
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

	// MailStore on a temp dir.
	mailstoreDir := filepath.Join(t.TempDir(), "ms")
	if err := mkdirAllHelper(mailstoreDir, 0o750); err != nil {
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

	// Runtime provider module exposing MailStore +
	// QueueEngine. The webmail Send handler enqueues
	// to qe and stores to mailStore; the webmail
	// router wires both.
	rm := &routingRuntimeModule{store: mailStore, queue: qe}

	// Webmail/admin asset dirs (router construction
	// only needs the dirs to exist; the JS content
	// is not exercised here).
	scratchDir := t.TempDir()
	adminDir := filepath.Join(scratchDir, "admin")
	if err := mkdirAllHelper(adminDir, 0o755); err != nil {
		t.Fatalf("mkdir admin: %v", err)
	}
	_ = writeFileHelper(filepath.Join(adminDir, "index.html"), "<html></html>")
	_ = writeFileHelper(filepath.Join(adminDir, "app.js"), "")
	_ = writeFileHelper(filepath.Join(adminDir, "styles.css"), "")
	webmailDir := filepath.Join(scratchDir, "webmail")
	if err := mkdirAllHelper(webmailDir, 0o755); err != nil {
		t.Fatalf("mkdir webmail: %v", err)
	}
	_ = writeFileHelper(filepath.Join(webmailDir, "index.html"), "<html></html>")
	_ = writeFileHelper(filepath.Join(webmailDir, "auth-gate.css"), "")
	_ = writeFileHelper(filepath.Join(webmailDir, "auth-gate.js"), "")
	_ = writeFileHelper(filepath.Join(webmailDir, "webmail.css"), "")
	_ = writeFileHelper(filepath.Join(webmailDir, "webmail.js"), "")

	cfg.Server.AdminUIDir = adminDir
	cfg.Server.WebmailUIDir = webmailDir
	cfg.CoreMail.MailStorePath = mailstoreDir
	reg := modules.NewRegistry(logger)
	ff := license.NewFeatureFlags(logger)
	reg.Register(rm)
	router := api.NewRouter(cfg, authenticator, logger, db, reg, ff, nil)

	// Provision tenant + domain + two users +
	// mailboxes. The sender has a coremail_mailboxes
	// row, the recipient has one too. Both are in
	// tenant 1.
	const (
		senderEmail    = "admin@orvix.email"
		senderPass     = "AdminPass!2026"
		recipientEmail = "alice@orvix.email"
		recipientPass  = "AlicePass!2026"
	)
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
	senderHash, _ := bcrypt.GenerateFromPassword([]byte(senderPass), bcrypt.MinCost)
	recipientHash, _ := bcrypt.GenerateFromPassword([]byte(recipientPass), bcrypt.MinCost)
	if _, err := sqlDB.Exec(
		"INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, 'admin', 1, 1, 1)",
		now, now, senderEmail, string(senderHash),
	); err != nil {
		t.Fatalf("insert sender user: %v", err)
	}
	if _, err := sqlDB.Exec(
		"INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, 'user', 1, 1, 1)",
		now, now, recipientEmail, string(recipientHash),
	); err != nil {
		t.Fatalf("insert recipient user: %v", err)
	}
	// Sender mailbox (admin). This is the existing
	// coremail_mailboxes row the test exercises
	// send from.
	argonHash, _ := hashArgon2idRouting(senderPass)
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_mailboxes
			(domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
			VALUES (1, 1, 'admin', ?, 'Admin', ?, 'argon2id', 'active', 1024, 1, ?, ?)`,
		senderEmail, argonHash, now, now,
	); err != nil {
		t.Fatalf("insert sender mailbox: %v", err)
	}
	// Recipient mailbox (alice).
	recipientHashArgon, _ := hashArgon2idRouting(recipientPass)
	res, err := sqlDB.Exec(
		`INSERT INTO coremail_mailboxes
			(domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
			VALUES (1, 1, 'alice', ?, 'Alice', ?, 'argon2id', 'active', 1024, 0, ?, ?)`,
		recipientEmail, recipientHashArgon, now, now,
	)
	if err != nil {
		t.Fatalf("insert recipient mailbox: %v", err)
	}
	recipientID, _ := res.LastInsertId()

	t.Cleanup(func() {
		_ = router.App().Shutdown()
		_ = sqlDB.Close()
	})

	return &webmailRoutingEnv{
		router:         router,
		mailStore:      mailStore,
		qe:             qe,
		db:             db,
		sqlDB:          sqlDB,
		senderEmail:    senderEmail,
		senderPass:     senderPass,
		recipientEmail: recipientEmail,
		recipientID:    uint(recipientID),
	}
}

// routingRuntimeModule exposes MailStore +
// QueueEngine to the webmail router so the webmail
// Send handler can store messages and enqueue
// delivery.
type routingRuntimeModule struct {
	store *storage.MailStore
	queue *queue.QueueEngine
}

func (m *routingRuntimeModule) ID() string                              { return "coremail-runtime" }
func (m *routingRuntimeModule) Version() string                         { return "test" }
func (m *routingRuntimeModule) Requires() []string                       { return nil }
func (m *routingRuntimeModule) Init(_ *config.Config, _ *gorm.DB) error { return nil }
func (m *routingRuntimeModule) Start() error                             { return nil }
func (m *routingRuntimeModule) Stop() error                              { return nil }
func (m *routingRuntimeModule) Migrate() error                           { return nil }
func (m *routingRuntimeModule) MailStore() *storage.MailStore           { return m.store }
func (m *routingRuntimeModule) QueueEngine() *queue.QueueEngine          { return m.queue }

// loginSender performs a real webmail login (the
// form-posted /api/v1/webmail/login flow) and
// returns the access_token cookie. This is the
// contract a browser-based webmail client uses.
func loginSender(t *testing.T, e *webmailRoutingEnv) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{
		"email":    e.senderEmail,
		"password": e.senderPass,
	})
	req := httptest.NewRequest("POST", "/api/v1/webmail/login", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("login: expected 200, got %d, body=%s", resp.StatusCode, string(body))
	}
	for _, c := range resp.Cookies() {
		if c.Name == "access_token" {
			return c.Value
		}
	}
	t.Fatal("no access_token cookie from /api/v1/webmail/login")
	return ""
}

// webmailSend is a small helper that POSTs the given
// body to /api/v1/webmail/send with the supplied
// bearer token. Returns (status, response body).
func webmailSend(t *testing.T, e *webmailRoutingEnv, token, to, subject, body string) (int, map[string]interface{}) {
	t.Helper()
	payload := map[string]string{
		"to":      to,
		"subject": subject,
		"body":    body,
	}
	buf, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/v1/webmail/send", strings.NewReader(string(buf)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	out := map[string]interface{}{}
	respBody, _ := io.ReadAll(resp.Body)
	if len(respBody) > 0 {
		_ = json.Unmarshal(respBody, &out)
	}
	return resp.StatusCode, out
}

// queueRowsForMessage returns all queue rows for the
// given message_id, regardless of recipient. Used to
// assert how many entries the Send handler created
// for a given message and what delivery_mode each
// has.
func queueRowsForMessage(t *testing.T, e *webmailRoutingEnv, messageID string) []queue.QueueEntry {
	t.Helper()
	rows, err := e.sqlDB.Query(
		"SELECT id, tenant_id, domain_id, mailbox_id, message_id, from_address, to_address, recipient_domain, direction, status, attempt_count, delivery_mode, max_attempts, last_error, tls_used, created_at, updated_at FROM coremail_queue WHERE message_id = ?",
		messageID,
	)
	if err != nil {
		t.Fatalf("query queue: %v", err)
	}
	defer rows.Close()
	var entries []queue.QueueEntry
	for rows.Next() {
		var entry queue.QueueEntry
		var errMsg sql.NullString
		if err := rows.Scan(&entry.ID, &entry.TenantID, &entry.DomainID, &entry.MailboxID, &entry.MessageID, &entry.FromAddress, &entry.ToAddress, &entry.RecipientDomain, &entry.Direction, &entry.Status, &entry.AttemptCount, &entry.DeliveryMode, &entry.MaxAttempts, &errMsg, &entry.TLSUsed, &entry.CreatedAt, &entry.UpdatedAt); err != nil {
			t.Fatalf("scan queue: %v", err)
		}
		entry.LastError = errMsg.String
		entries = append(entries, entry)
	}
	return entries
}

// messageCountInFolder returns the number of messages
// in the named folder for the given mailbox.
func messageCountInFolder(t *testing.T, e *webmailRoutingEnv, mailboxID uint, folderPath string) int {
	t.Helper()
	var count int
	err := e.sqlDB.QueryRow(`
		SELECT COUNT(*) FROM coremail_messages m
		JOIN coremail_folders f ON m.folder_id = f.id
		WHERE f.mailbox_id = ? AND f.path = ?`,
		mailboxID, folderPath,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count folder: %v", err)
	}
	return count
}

// senderMailboxID returns the coremail_mailboxes id
// for the sender email.
func senderMailboxID(t *testing.T, e *webmailRoutingEnv) uint {
	t.Helper()
	var id uint
	if err := e.sqlDB.QueryRow("SELECT id FROM coremail_mailboxes WHERE email = ?", e.senderEmail).Scan(&id); err != nil {
		t.Fatalf("sender mailbox: %v", err)
	}
	return id
}

// ── Tests ─────────────────────────────────────────────────

// TestWebmailSendSelfDeliveryUsesLocalPath is the
// primary regression test for Blocker 1. A webmail
// message from admin@orvix.email to admin@orvix.email
// must be classified as local and delivered through
// the local MailStore path. The queue must NOT have
// a remote_smtp row for a self-recipient.
func TestWebmailSendSelfDeliveryUsesLocalPath(t *testing.T) {
	e := buildWebmailRoutingEnv(t)
	tok := loginSender(t, e)
	status, body := webmailSend(t, e, tok, e.senderEmail, "self-send", "body")
	if status != 201 {
		t.Fatalf("send: expected 201, got %d, body=%v", status, body)
	}
	// The Send response must include the message_id
	// and a local_count that matches the recipient
	// list. The sender is also the recipient, so
	// local_count=1 and remote_count=0.
	if got := body["message_id"]; got == nil {
		t.Fatalf("send response missing message_id: %v", body)
	}
	if local, _ := body["local_count"].(float64); int(local) != 1 {
		t.Errorf("local_count: expected 1, got %v", body["local_count"])
	}
	if remote, _ := body["remote_count"].(float64); int(remote) != 0 {
		t.Errorf("remote_count: expected 0, got %v", body["remote_count"])
	}
	messageID, _ := body["message_id"].(string)

	// The queue MUST NOT have a remote_smtp row for
	// the self-recipient. The local MailStore path
	// writes a queue.QueueEntry with DeliveryMode=local
	// and a queue entry for the local MailStore's
	// deliverLocal path.
	entries := queueRowsForMessage(t, e, messageID)
	for _, ent := range entries {
		if ent.DeliveryMode == queue.DeliveryRemoteSMTP {
			t.Errorf("self-send: queue row for %q has DeliveryMode=remote_smtp (must be local)", ent.ToAddress)
		}
		if ent.DeliveryMode == queue.DeliveryLocal {
			if ent.RecipientDomain != "orvix.email" {
				t.Errorf("local row: recipient domain %q, want orvix.email", ent.RecipientDomain)
			}
			if ent.MailboxID == nil || *ent.MailboxID != senderMailboxID(t, e) {
				t.Errorf("local row: mailbox id %v, want %d", ent.MailboxID, senderMailboxID(t, e))
			}
		}
	}
	// The sender's Sent folder must have exactly
	// one row (no duplicate). This is the
	// "sender receives exactly one Sent copy"
	// contract.
	sentCount := messageCountInFolder(t, e, senderMailboxID(t, e), "Sent")
	if sentCount != 1 {
		t.Errorf("Sent folder: expected 1 message, got %d", sentCount)
	}
}

// TestWebmailSendLocalRecipientNeverRemoteSMTP is the
// "no remote_smtp" regression test. A recipient on
// the same local domain as the sender (and an active
// mailbox in the same tenant) must be classified as
// local — the queue row must have
// DeliveryMode=local, NOT remote_smtp.
func TestWebmailSendLocalRecipientNeverRemoteSMTP(t *testing.T) {
	e := buildWebmailRoutingEnv(t)
	tok := loginSender(t, e)
	status, body := webmailSend(t, e, tok, e.recipientEmail, "local delivery", "body")
	if status != 201 {
		t.Fatalf("send: expected 201, got %d, body=%v", status, body)
	}
	messageID, _ := body["message_id"].(string)

	entries := queueRowsForMessage(t, e, messageID)
	if len(entries) != 1 {
		t.Fatalf("expected 1 queue row, got %d", len(entries))
	}
	if entries[0].DeliveryMode == queue.DeliveryRemoteSMTP {
		t.Errorf("local recipient: DeliveryMode=remote_smtp (must be local); got %q", entries[0].DeliveryMode)
	}
	if entries[0].DeliveryMode != queue.DeliveryLocal {
		t.Errorf("local recipient: DeliveryMode=%q, want local", entries[0].DeliveryMode)
	}
	if entries[0].RecipientDomain != "orvix.email" {
		t.Errorf("local recipient: recipient domain %q, want orvix.email", entries[0].RecipientDomain)
	}
	if entries[0].MailboxID == nil || *entries[0].MailboxID != e.recipientID {
		t.Errorf("local recipient: mailbox id %v, want %d (recipient's mailbox)", entries[0].MailboxID, e.recipientID)
	}
	// The recipient mailbox must NOT have the
	// message in INBOX yet — the local delivery
	// worker hasn't run. We assert the queue row is
	// pending, not delivered, so we know the
	// transport did not do the delivery inline.
	if entries[0].Status != queue.StatusPending {
		t.Errorf("local recipient: status=%q, want pending (worker has not run)", entries[0].Status)
	}
}

// TestWebmailSendRemoteRecipientUsesRemoteSMTP is
// the "remote path is intact" test. A recipient on a
// non-local domain must be classified as remote —
// the queue row must have DeliveryMode=remote_smtp.
func TestWebmailSendRemoteRecipientUsesRemoteSMTP(t *testing.T) {
	e := buildWebmailRoutingEnv(t)
	tok := loginSender(t, e)
	status, body := webmailSend(t, e, tok, "external@somewhere.test", "remote delivery", "body")
	if status != 201 {
		t.Fatalf("send: expected 201, got %d, body=%v", status, body)
	}
	messageID, _ := body["message_id"].(string)

	entries := queueRowsForMessage(t, e, messageID)
	if len(entries) != 1 {
		t.Fatalf("expected 1 queue row, got %d", len(entries))
	}
	if entries[0].DeliveryMode != queue.DeliveryRemoteSMTP {
		t.Errorf("remote recipient: DeliveryMode=%q, want remote_smtp", entries[0].DeliveryMode)
	}
	if entries[0].RecipientDomain != "somewhere.test" {
		t.Errorf("remote recipient: recipient domain %q, want somewhere.test", entries[0].RecipientDomain)
	}
}

// TestWebmailSendMixedRecipientsSplitsCorrectly is
// the "mixed list" test. To: includes both a local
// recipient (alice@orvix.email) and a remote
// recipient (bob@somewhere.test). The queue must
// have TWO rows: one local, one remote, with the
// right delivery_mode and recipient. The Sent
// folder must have exactly one copy.
func TestWebmailSendMixedRecipientsSplitsCorrectly(t *testing.T) {
	e := buildWebmailRoutingEnv(t)
	tok := loginSender(t, e)
	status, body := webmailSend(t, e, tok, e.recipientEmail+", bob@somewhere.test", "mixed", "body")
	if status != 201 {
		t.Fatalf("send: expected 201, got %d, body=%v", status, body)
	}
	messageID, _ := body["message_id"].(string)

	entries := queueRowsForMessage(t, e, messageID)
	if len(entries) != 2 {
		t.Fatalf("expected 2 queue rows (one per recipient), got %d", len(entries))
	}

	var localCount, remoteCount int
	for _, ent := range entries {
		switch ent.DeliveryMode {
		case queue.DeliveryLocal:
			localCount++
			if ent.ToAddress != e.recipientEmail {
				t.Errorf("local entry: to=%q, want %q", ent.ToAddress, e.recipientEmail)
			}
			if ent.RecipientDomain != "orvix.email" {
				t.Errorf("local entry: domain=%q, want orvix.email", ent.RecipientDomain)
			}
			if ent.MailboxID == nil || *ent.MailboxID != e.recipientID {
				t.Errorf("local entry: mailbox=%v, want %d", ent.MailboxID, e.recipientID)
			}
		case queue.DeliveryRemoteSMTP:
			remoteCount++
			if ent.ToAddress != "bob@somewhere.test" {
				t.Errorf("remote entry: to=%q, want bob@somewhere.test", ent.ToAddress)
			}
			if ent.RecipientDomain != "somewhere.test" {
				t.Errorf("remote entry: domain=%q, want somewhere.test", ent.RecipientDomain)
			}
		}
	}
	if localCount != 1 || remoteCount != 1 {
		t.Errorf("queue split: local=%d remote=%d, want 1/1", localCount, remoteCount)
	}
	if got, _ := body["local_count"].(float64); int(got) != 1 {
		t.Errorf("response local_count=%v, want 1", body["local_count"])
	}
	if got, _ := body["remote_count"].(float64); int(got) != 1 {
		t.Errorf("response remote_count=%v, want 1", body["remote_count"])
	}
	// Exactly one Sent copy.
	if sent := messageCountInFolder(t, e, senderMailboxID(t, e), "Sent"); sent != 1 {
		t.Errorf("Sent folder: expected 1, got %d", sent)
	}
}

// TestWebmailSendNoRemoteSMTPForLocalDomainWithoutMailbox
// verifies the classifier does not crash on a local
// domain with no matching mailbox row. The recipient
// is on a configured local domain but has no
// coremail_mailboxes row, so the classifier must
// fall back to remote_smtp (the safe default — the
// remote path will return a clean bounce).
func TestWebmailSendNoRemoteSMTPForLocalDomainWithoutMailbox(t *testing.T) {
	e := buildWebmailRoutingEnv(t)
	tok := loginSender(t, e)
	// ghost@orvix.email is on the local domain
	// but has no mailbox row.
	status, body := webmailSend(t, e, tok, "ghost@orvix.email", "unknown local", "body")
	if status != 201 {
		t.Fatalf("send: expected 201, got %d, body=%v", status, body)
	}
	messageID, _ := body["message_id"].(string)
	entries := queueRowsForMessage(t, e, messageID)
	if len(entries) != 1 {
		t.Fatalf("expected 1 queue row, got %d", len(entries))
	}
	// No mailbox row → fall back to remote_smtp.
	if entries[0].DeliveryMode != queue.DeliveryRemoteSMTP {
		t.Errorf("unknown local recipient: DeliveryMode=%q, want remote_smtp", entries[0].DeliveryMode)
	}
}

// TestWebmailSendCrossTenantLocalPathBlocked pins the
// cross-tenant guard. A sender in tenant A must NOT
// route to a mailbox in tenant B through the local
// path. The recipient is in a different tenant;
// the classifier must fall back to remote_smtp
// because the cross-tenant filter excludes the
// mailbox.
//
// We simulate this by creating a second tenant +
// domain + mailbox for the recipient.
func TestWebmailSendCrossTenantLocalPathBlocked(t *testing.T) {
	e := buildWebmailRoutingEnv(t)
	now := time.Now().UTC()

	// Add a second tenant + domain + mailbox in
	// the second tenant.
	if _, err := e.sqlDB.Exec(
		"INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (2, ?, ?, 'other', 'other', 'othercorp.email', 'enterprise', 1)",
		now, now,
	); err != nil {
		t.Fatalf("insert tenant 2: %v", err)
	}
	if _, err := e.sqlDB.Exec(
		"INSERT INTO coremail_domains (name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES ('othercorp.email', 2, 'active', 'enterprise', 0, 0, 0, ?, ?)",
		now, now,
	); err != nil {
		t.Fatalf("insert othercorp domain: %v", err)
	}
	hash, _ := hashArgon2idRouting("OtherPass!2026")
	if _, err := e.sqlDB.Exec(
		`INSERT INTO coremail_mailboxes
			(domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
			VALUES (2, 2, 'carol', 'carol@othercorp.email', 'Carol', ?, 'argon2id', 'active', 1024, 0, ?, ?)`,
		hash, now, now,
	); err != nil {
		t.Fatalf("insert carol mailbox: %v", err)
	}

	tok := loginSender(t, e)
	status, body := webmailSend(t, e, tok, "carol@othercorp.email", "cross tenant", "body")
	if status != 201 {
		t.Fatalf("send: expected 201, got %d, body=%v", status, body)
	}
	messageID, _ := body["message_id"].(string)
	entries := queueRowsForMessage(t, e, messageID)
	if len(entries) != 1 {
		t.Fatalf("expected 1 queue row, got %d", len(entries))
	}
	// Cross-tenant: must fall back to remote_smtp.
	if entries[0].DeliveryMode != queue.DeliveryRemoteSMTP {
		t.Errorf("cross-tenant recipient: DeliveryMode=%q, want remote_smtp", entries[0].DeliveryMode)
	}
}

// ── helpers ─────────────────────────────────────────────

func mkdirAllHelper(path string, _ int) error {
	return os.MkdirAll(path, 0o755)
}

func writeFileHelper(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o644)
}

func hashArgon2idRouting(password string) (string, error) {
	const (
		mem     uint32 = 65536
		timeP   uint32 = 3
		threads uint8  = 4
		keyLen  uint32 = 32
	)
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, timeP, mem, threads, keyLen)
	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Key := base64.RawStdEncoding.EncodeToString(key)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", mem, timeP, threads, b64Salt, b64Key), nil
}
