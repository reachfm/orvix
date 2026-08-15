package handlers

// Fail-closed recipient-classification tests for WebmailSend.
//
// classifyLocalRecipient is the only source of truth for "local vs
// remote" in the webmail send flow, and it is a read-only, tenant-scoped
// database lookup. These tests pin the requirement that a
// database/repository failure during classification ABORTS the send
// (503, generic error) with zero durable side effects — it must never be
// silently treated as "remote SMTP recipient", which could misroute a
// local/tenant-scoped recipient or bypass tenant isolation. They use a
// real Handler (package handlers) with a driver-level wrapper that fails
// exactly the two classification lookups and passes everything else
// through untouched.

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
	"modernc.org/sqlite"
)

// failClassifyConnector opens modernc.org/sqlite connections over the
// harness database file and, when armed, fails exactly the two
// recipient-classification lookups used by WebmailSend's
// classifyLocalRecipient:
//
//	SELECT id, status FROM coremail_domains ...
//	SELECT id, status FROM coremail_mailboxes ...
//
// Every other statement passes through to the real driver untouched, so
// the domain operability check, quota reservation, message store, queue
// enqueue, and finalize steps all behave normally.
type failClassifyConnector struct {
	dsn   string
	inner *sqlite.Driver
	arm   *bool
}

func (c *failClassifyConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.inner.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &failClassifyConn{Conn: conn, arm: c.arm}, nil
}

func (c *failClassifyConnector) Driver() driver.Driver { return c.inner }

// isClassificationLookup matches the exact SQL shapes emitted by
// classifyLocalRecipient (the only place in the send chain that selects
// id,status from the domains/mailboxes tables in this shape).
func isClassificationLookup(query string) bool {
	return strings.Contains(query, "SELECT id, status FROM coremail_domains") ||
		strings.Contains(query, "SELECT id, status FROM coremail_mailboxes")
}

type failClassifyConn struct {
	driver.Conn
	arm *bool
}

func (c *failClassifyConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.arm != nil && *c.arm && isClassificationLookup(query) {
		return nil, errors.New("simulated recipient-classification lookup failure")
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *failClassifyConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *failClassifyConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *failClassifyConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, query)
}

func (c *failClassifyConn) ResetSession(ctx context.Context) error {
	return c.Conn.(driver.SessionResetter).ResetSession(ctx)
}

func (c *failClassifyConn) Ping(ctx context.Context) error {
	return c.Conn.(driver.Pinger).Ping(ctx)
}

// wrapClassificationDB swaps the handler's gorm ConnPool for a wrapped
// *sql.DB whose driver fails classification lookups only when armed.
func wrapClassificationDB(t *testing.T, db *gorm.DB, dsn string, armed bool) (*sql.DB, *bool) {
	t.Helper()
	arm := armed
	wrapped := sql.OpenDB(&failClassifyConnector{dsn: dsn, inner: &sqlite.Driver{}, arm: &arm})
	if err := wrapped.Ping(); err != nil {
		t.Fatalf("wrapped db ping: %v", err)
	}
	db.ConnPool = wrapped
	t.Cleanup(func() { _ = wrapped.Close() })
	return wrapped, &arm
}

func classifySend(t *testing.T, h *Handler, body string) (int, map[string]interface{}) {
	t.Helper()
	app := fiber.New()
	app.Post("/api/v1/webmail/send", func(c fiber.Ctx) error {
		c.Locals("user_id", uint(1))
		return h.WebmailSend(c)
	})
	req := httptest.NewRequest("POST", "/api/v1/webmail/send", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("webmail send request: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// assertZeroDurableSideEffects verifies that a failed send left no
// durable state behind: no queue rows, no messages, no send events, no
// committed usage, no abuse counters, and no attachment metadata.
func assertZeroDurableSideEffects(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	assertScalarZero(t, sqlDB, "SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL", "queue rows")
	assertScalarZero(t, sqlDB, "SELECT COUNT(*) FROM coremail_messages", "message rows")
	assertScalarZero(t, sqlDB, "SELECT COUNT(*) FROM coremail_attachments", "attachment rows")
	assertScalarZero(t, sqlDB, "SELECT COUNT(*) FROM send_events", "send events")
	assertScalarZero(t, sqlDB, "SELECT COALESCE(SUM(emails_sent), 0) FROM usage_records WHERE tenant_id = 1", "usage emails_sent")
	assertScalarZero(t, sqlDB, "SELECT COALESCE(SUM(emails_sent), 0) FROM abuse_send_counts WHERE tenant_id = 1", "abuse emails_sent")
}

func assertScalarZero(t *testing.T, sqlDB *sql.DB, query, what string) {
	t.Helper()
	var got int64
	if err := sqlDB.QueryRow(query).Scan(&got); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	if got != 0 {
		t.Errorf("%s: got %d, want 0 (query %q)", what, got, query)
	}
}

// TestWebmailSendClassificationFailureAbortsSendFailClosed proves the
// core fail-closed contract: when recipient classification hits a
// database/repository failure, WebmailSend returns 503 with a generic
// error (no internal lookup details), and NOTHING durable survives — no
// queue rows, no Sent message, no send events, no quota usage, no abuse
// counters, no attachment metadata. The recipient is NEVER silently
// classified as remote_smtp.
func TestWebmailSendClassificationFailureAbortsSendFailClosed(t *testing.T) {
	h, db, sqlDB, dsn := buildSendEnforcementHarness(t, true)
	t.Cleanup(func() {
		sqlDB.Close()
		if s, err := db.DB(); err == nil {
			s.Close()
		}
	})

	// Arm the classification failure BEFORE any request: the sender's
	// own domain check and the recipient classification both read the
	// domains table, but only the classification lookup has the
	// "SELECT id, status FROM coremail_domains" shape, so the early
	// domain operability check still succeeds and we exercise exactly
	// the classification failure path.
	_, arm := wrapClassificationDB(t, db, dsn, true)

	status, resp := classifySend(t, h, `{"to":"alice@example.com","subject":"fc","body":"body"}`)
	if status != fiber.StatusServiceUnavailable {
		t.Fatalf("classification failure: status=%d resp=%v, want 503", status, resp)
	}
	if code, _ := resp["code"].(string); code != "INTERNAL_ERROR" {
		t.Errorf("classification failure: code=%v, want INTERNAL_ERROR (resp=%v)", code, resp)
	}
	if errMsg, _ := resp["error"].(string); errMsg != "unable to classify recipient" {
		t.Errorf("classification failure: error=%q, want generic 'unable to classify recipient' (no internal lookup details)", errMsg)
	}
	// No success or partial-success fields may leak.
	for _, key := range []string{"id", "message_id", "queue_ids", "enqueue_errors", "queued_count"} {
		if _, ok := resp[key]; ok {
			t.Errorf("classification failure: %q must not appear in response: %v", key, resp)
		}
	}
	if s, _ := resp["status"].(string); s == "queued" {
		t.Errorf("classification failure: status must not be queued: %v", resp)
	}

	assertZeroDurableSideEffects(t, sqlDB)

	// The same request after disarming succeeds — proving the wrapper
	// itself did not distort the healthy path (control).
	*arm = false
	status, resp = classifySend(t, h, `{"to":"alice@example.com","subject":"ok","body":"body"}`)
	if status != fiber.StatusCreated {
		t.Fatalf("control after disarm: status=%d resp=%v, want 201", status, resp)
	}
	assertScalarZero(t, sqlDB, "SELECT COUNT(*) FROM send_events WHERE event_type='reservation'", "reservation events after successful send")
	var queued int64
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND to_address = 'alice@example.com'`).Scan(&queued); err != nil {
		t.Fatalf("queue count: %v", err)
	}
	if queued != 1 {
		t.Errorf("control: queued rows for alice@example.com = %d, want 1", queued)
	}
}

// TestWebmailSendClassificationHealthyControlSucceeds proves the wrapper
// (and the ConnPool swap) does not distort the healthy send path when
// disarmed: a remote recipient is still classified remote, enqueued once,
// and reported as queued.
func TestWebmailSendClassificationHealthyControlSucceeds(t *testing.T) {
	h, db, sqlDB, dsn := buildSendEnforcementHarness(t, true)
	t.Cleanup(func() {
		sqlDB.Close()
		if s, err := db.DB(); err == nil {
			s.Close()
		}
	})

	wrapClassificationDB(t, db, dsn, false)

	status, resp := classifySend(t, h, `{"to":"bob@example.com","subject":"hi","body":"body"}`)
	if status != fiber.StatusCreated {
		t.Fatalf("healthy control: status=%d resp=%v, want 201", status, resp)
	}
	if id, _ := resp["id"].(float64); id == 0 {
		t.Errorf("healthy control: expected stored message id, got %v", resp["id"])
	}
	var queued int64
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND to_address = 'bob@example.com' AND delivery_mode = 'remote_smtp'`).Scan(&queued); err != nil {
		t.Fatalf("queue count: %v", err)
	}
	if queued != 1 {
		t.Errorf("healthy control: remote_smtp queue rows for bob@example.com = %d, want 1", queued)
	}
}

// TestWebmailSendClassificationFailureCrossTenantStillFailsClosed proves
// tenant isolation is preserved under classification failure: a mailbox
// that exists in ANOTHER tenant must never be reached through the local
// path, and a classification database failure must still abort the send
// with zero side effects instead of falling back to a remote queue row.
func TestWebmailSendClassificationFailureCrossTenantStillFailsClosed(t *testing.T) {
	h, db, sqlDB, dsn := buildSendEnforcementHarness(t, true)
	t.Cleanup(func() {
		sqlDB.Close()
		if s, err := db.DB(); err == nil {
			s.Close()
		}
	})

	// Provision tenant 2 with its own domain and a mailbox whose
	// address differs only by domain from anything tenant 1 owns.
	now := time.Now().UTC()
	if _, err := sqlDB.Exec(
		"INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		2, now, now, "othercorp", "othercorp", "othercorp.test", "enterprise", 1,
	); err != nil {
		t.Fatalf("tenant 2: %v", err)
	}
	if _, err := sqlDB.Exec(
		"INSERT INTO coremail_domains (name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		"othercorp.test", 2, "active", "enterprise", 0, 0, 0, now, now,
	); err != nil {
		t.Fatalf("domain 2: %v", err)
	}
	var otherDomainID uint
	if err := sqlDB.QueryRow("SELECT id FROM coremail_domains WHERE name='othercorp.test' AND tenant_id=2").Scan(&otherDomainID); err != nil {
		t.Fatalf("domain 2 id: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		otherDomainID, 2, "alice", "alice@othercorp.test", "Alice", "x-bcrypt-hash-placeholder", "bcrypt", "active", 1024, 0, now, now,
	); err != nil {
		t.Fatalf("mailbox 2: %v", err)
	}

	_, _ = wrapClassificationDB(t, db, dsn, true)

	// Sender is tenant 1 (admin@orvix.email). The recipient exists in
	// tenant 2 only. With the classification lookup failing, the send
	// must abort — never fall back to a remote_smtp queue row, and
	// never route locally to tenant 2's mailbox.
	status, resp := classifySend(t, h, `{"to":"alice@othercorp.test","subject":"x","body":"body"}`)
	if status != fiber.StatusServiceUnavailable {
		t.Fatalf("cross-tenant classification failure: status=%d resp=%v, want 503", status, resp)
	}
	if code, _ := resp["code"].(string); code != "INTERNAL_ERROR" {
		t.Errorf("cross-tenant classification failure: code=%v, want INTERNAL_ERROR", code)
	}

	assertZeroDurableSideEffects(t, sqlDB)
}
