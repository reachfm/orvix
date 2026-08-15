package handlers_test

// Phase 8 C3A: domain operability enforcement for webmail send.
// Reuses buildWebmailTestEnv (webmail_user_test.go) — the seeded
// mailbox's sender domain is "orvix.email", tenant 1, status active
// by default.

import (
	"net/http"
	"testing"
)

func setSenderDomainStatus(t *testing.T, e *webmailTestEnv, status string) {
	t.Helper()
	if _, err := e.mailbox.DB.Exec(`UPDATE coremail_domains SET status = ? WHERE name = 'orvix.email'`, status); err != nil {
		t.Fatalf("set domain status: %v", err)
	}
}

func TestWebmailSend_ActiveDomainSucceeds(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mailboxIDForEmail(t, e.mailbox, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	tok := e.loginAdmin(t)

	status, body := e.webmailRequest(t, "POST", "/api/v1/webmail/send", tok, map[string]string{
		"to": e.email, "subject": "hi", "body": "body",
	})
	if status != http.StatusCreated {
		t.Fatalf("expected 201 for an active domain, got %d: %v", status, body)
	}
}

func TestWebmailSend_DisabledDomainRejectedWithZeroSideEffects(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mailboxIDForEmail(t, e.mailbox, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	setSenderDomainStatus(t, e, "disabled")
	tok := e.loginAdmin(t)

	status, body := e.webmailRequest(t, "POST", "/api/v1/webmail/send", tok, map[string]string{
		"to": "external@example.com", "subject": "hi", "body": "body",
	})
	if status != http.StatusConflict {
		t.Fatalf("expected 409 for a disabled sender domain, got %d: %v", status, body)
	}
	if body["code"] != "DOMAIN_DISABLED" {
		t.Fatalf("expected DOMAIN_DISABLED code, got %v", body)
	}

	// Zero queue side effects.
	metrics, err := e.queue.Metrics(t.Context(), nil)
	if err != nil {
		t.Fatalf("queue metrics: %v", err)
	}
	if metrics.Pending != 0 || metrics.Delivered != 0 {
		t.Fatalf("a rejected send must not enqueue anything: %+v", metrics)
	}

	// Zero Sent-copy messages (StoreMessage never ran).
	var msgCount int
	if err := e.mailbox.DB.QueryRow(`SELECT COUNT(*) FROM coremail_messages WHERE mailbox_id = ?`, mailboxID).Scan(&msgCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if msgCount != 0 {
		t.Fatalf("a rejected send must not persist a Sent-folder copy, got %d messages", msgCount)
	}
}

func TestWebmailSend_SuspendedDomainRejected(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mailboxIDForEmail(t, e.mailbox, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	setSenderDomainStatus(t, e, "suspended")
	tok := e.loginAdmin(t)

	status, body := e.webmailRequest(t, "POST", "/api/v1/webmail/send", tok, map[string]string{
		"to": "external@example.com", "subject": "hi", "body": "body",
	})
	if status != http.StatusConflict {
		t.Fatalf("expected 409 for a suspended sender domain, got %d: %v", status, body)
	}
}

func TestWebmailSend_LockedDomainRejected(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mailboxIDForEmail(t, e.mailbox, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	setSenderDomainStatus(t, e, "locked")
	tok := e.loginAdmin(t)

	status, body := e.webmailRequest(t, "POST", "/api/v1/webmail/send", tok, map[string]string{
		"to": "external@example.com", "subject": "hi", "body": "body",
	})
	if status != http.StatusConflict {
		t.Fatalf("expected 409 for a locked sender domain, got %d: %v", status, body)
	}
}

// TestWebmailSend_ForgedRecipientDomainCannotBypass proves the guard
// checks the SENDER's server-resolved domain, not anything from the
// request body: even with a "to" address on a completely different
// (nonexistent) domain, a disabled sender domain still rejects the
// send — there is no way to smuggle an operational-looking domain in
// through the recipient field.
func TestWebmailSend_ForgedRecipientDomainCannotBypass(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mailboxIDForEmail(t, e.mailbox, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	setSenderDomainStatus(t, e, "disabled")
	tok := e.loginAdmin(t)

	status, _ := e.webmailRequest(t, "POST", "/api/v1/webmail/send", tok, map[string]string{
		"to": "someone@totally-unrelated-domain.example", "subject": "hi", "body": "body",
	})
	if status != http.StatusConflict {
		t.Fatalf("expected 409 regardless of recipient domain (sender's domain is what's checked), got %d", status)
	}
}
