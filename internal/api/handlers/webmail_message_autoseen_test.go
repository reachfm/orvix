package handlers_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/coremail/storage"
)

// autoSeenRead issues GET /api/v1/webmail/messages/:id with an explicit
// auto_seen query param so the test exercises the client-controlled
// mark-read path that the webmail SPA uses for the
// mark_read_delay_seconds setting.
func autoSeenRead(t *testing.T, e *webmailTestEnv, tok string, id uint, autoSeen string) (int, map[string]interface{}) {
	t.Helper()
	// url.QueryEscape so padded / non-URL-safe values like " 0 "
	// don't break httptest.NewRequest.
	path := fmt.Sprintf("/api/v1/webmail/messages/%d?auto_seen=%s", id, url.QueryEscape(autoSeen))
	return e.webmailRequest(t, "GET", path, tok, nil)
}

// TestWebmailMessageAutoSeenDefault pins the existing behaviour: a
// plain GET (no query param) marks the message as seen, so the test
// suite keeps asserting the same contract it did before this branch
// was extended. The new ?auto_seen=0 path is covered by the next
// test.
func TestWebmailMessageAutoSeenDefault(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)
	id := e.injectMessage(t, "Auto-seen default", "body")

	// First read with no query param — must flip seen to true.
	status, resp := e.webmailRequest(t, "GET", fmt.Sprintf("/api/v1/webmail/messages/%d", id), tok, nil)
	if status != http.StatusOK {
		t.Fatalf("GET default: expected 200, got %d: %v", status, resp)
	}
	if resp["seen"] != true {
		t.Errorf("default GET: seen = %v, want true", resp["seen"])
	}
}

// TestWebmailMessageAutoSeenZeroOptsOut pins the new opt-out path
// that the webmail SPA uses to implement the user-configurable
// `mark_read_delay_seconds` setting. Without this contract the JS
// cannot truly delay a mark-read: the backend would always mark the
// row seen on the first GET regardless of the client preference.
func TestWebmailMessageAutoSeenZeroOptsOut(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)
	id := e.injectMessage(t, "Auto-seen opt-out", "body")

	// GET with auto_seen=0 — must leave seen = false.
	status, resp := autoSeenRead(t, e, tok, id, "0")
	if status != http.StatusOK {
		t.Fatalf("GET auto_seen=0: expected 200, got %d: %v", status, resp)
	}
	if resp["seen"] != false {
		t.Errorf("auto_seen=0 GET: seen = %v, want false", resp["seen"])
	}

	// GET with auto_seen=false — same opt-out.
	status, resp = autoSeenRead(t, e, tok, id, "false")
	if status != http.StatusOK {
		t.Fatalf("GET auto_seen=false: expected 200, got %d: %v", status, resp)
	}
	if resp["seen"] != false {
		t.Errorf("auto_seen=false GET: seen = %v, want false", resp["seen"])
	}

	// Subsequent PATCH {seen: true} must still flip the flag —
	// proves the opt-out only suppresses the implicit mark on read,
	// it does NOT break the explicit mark endpoint.
	status, _ = e.webmailRequest(t, "PATCH", fmt.Sprintf("/api/v1/webmail/messages/%d", id), tok,
		map[string]bool{"seen": true})
	if status != http.StatusOK {
		t.Fatalf("PATCH seen=true: expected 200, got %d", status)
	}
	status, resp = e.webmailRequest(t, "GET", fmt.Sprintf("/api/v1/webmail/messages/%d", id), tok, nil)
	if status != http.StatusOK {
		t.Fatalf("readback GET: expected 200, got %d", status)
	}
	if resp["seen"] != true {
		t.Errorf("after PATCH: seen = %v, want true", resp["seen"])
	}
}

// TestWebmailMessageAutoSeenUnknownFallsBackToMarkRead ensures that
// only the documented opt-out values ("0", "false", "no") suppress
// the implicit mark. Anything else — including garbage like
// "auto_seen=yes" — preserves the legacy behaviour. This is a
// defensive guard so a future typo in the JS helper cannot
// silently disable mark-read.
func TestWebmailMessageAutoSeenUnknownFallsBackToMarkRead(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)
	id := e.injectMessage(t, "Auto-seen unknown", "body")

	status, resp := autoSeenRead(t, e, tok, id, "yes")
	if status != http.StatusOK {
		t.Fatalf("GET auto_seen=yes: expected 200, got %d: %v", status, resp)
	}
	if resp["seen"] != true {
		t.Errorf("auto_seen=yes GET: seen = %v, want true (only 0/false/no opt out)", resp["seen"])
	}
	// Also verify the lowercase prefix-trim guard (e.g. " 0 " is OK).
	id2 := e.injectMessage(t, "Auto-seen padded", "body")
	status, resp = autoSeenRead(t, e, tok, id2, " 0 ")
	if status != http.StatusOK {
		t.Fatalf("GET auto_seen=' 0 ': expected 200, got %d", status)
	}
	if resp["seen"] != false {
		t.Errorf("auto_seen=' 0 ' GET: seen = %v, want false", resp["seen"])
	}
	// Also reject a setting the API must not silently accept:
	// setting "0" via the unused "yes" spelling must NOT be possible.
	id3 := e.injectMessage(t, "Auto-seen numeric-1", "body")
	status, resp = autoSeenRead(t, e, tok, id3, "1")
	if status != http.StatusOK {
		t.Fatalf("GET auto_seen=1: expected 200, got %d", status)
	}
	if resp["seen"] != true {
		t.Errorf("auto_seen=1 GET: seen = %v, want true (only 0/false/no opt out)", resp["seen"])
	}
}

// TestWebmailMessageAutoSeenDoesNotLeakOwnership confirms the new
// query param does not affect the ownership check that already
// returns 404 when a foreign-mailbox message id is requested.
// Regression guard for the v2A ownership tests.
func TestWebmailMessageAutoSeenDoesNotLeakOwnership(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tokA := e.loginAdmin(t)

	// Provision mailbox B.
	provisionSecondUser(t, e, "alice2@orvix.email", "AlicePass!1")
	mailboxBID := mustSecondMailboxID(t, e, "alice2@orvix.email")
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxBID, nil); err != nil {
		t.Fatalf("ensure folders B: %v", err)
	}

	// Inject a message in B's mailbox — we use the same inbox
	// path but bind to mailboxBID via a synthetic message row.
	inboxB, err := e.mailbox.Folders.GetByPath(t.Context(), mailboxBID, "INBOX", nil)
	if err != nil || inboxB == nil {
		t.Fatalf("get B inbox: %v", err)
	}
	now := time.Now().UTC()
	rfc822 := []byte("From: sender@example.com\r\nTo: alice2@orvix.email\r\nSubject: B-only\r\n\r\nbody")
	msgB := &storage.Message{
		MessageID:         fmt.Sprintf("b-msg-%d", now.UnixNano()),
		InternetMessageID: fmt.Sprintf("<b-msg-%d@orvix.local>", now.UnixNano()),
		TenantID:          1, DomainID: 1, MailboxID: mailboxBID, FolderID: inboxB.ID,
		Subject: "B-only", FromAddress: "sender@example.com",
		ToAddresses: "alice2@orvix.email", Seen: false, MessageDate: &now, ReceivedDate: now,
	}
	if err := e.mailbox.StoreMessage(t.Context(), msgB, rfc822, nil); err != nil {
		t.Fatalf("store B msg: %v", err)
	}

	// A tries to read B's message with auto_seen=0 — must still 404
	// because of the mailbox ownership check that runs BEFORE the
	// auto-seen branch.
	status, resp := e.webmailRequest(t, "GET",
		fmt.Sprintf("/api/v1/webmail/messages/%d?auto_seen=0", msgB.ID), tokA, nil)
	if status != http.StatusNotFound {
		t.Errorf("cross-mailbox GET with auto_seen=0: expected 404, got %d: %v", status, resp)
	}
	if !strings.Contains(fmt.Sprintf("%v", resp["error"]), "not found") {
		t.Errorf("cross-mailbox error message: %v", resp["error"])
	}
}
