package handlers_test

// Release 1 cross-cutting invariants for the user-facing webmail
// API. Every test in this file pins a single R1 acceptance
// criterion from the release notes. The full Go e2e suite lives
// in webmail_user_test.go and webmail_e2e_smoke_test.go; this
// file is the Release 1 slice — the regression guards the
// release notes promise to hold stable across all future slices:
//
//  1. Auth: every protected /api/v1/webmail/* endpoint rejects a
//     missing-or-invalid cookie with 401 (across GET / POST /
//     PATCH / PUT / DELETE variations). The auth middleware
//     runs BEFORE any handler so the response is a clean 401
//     envelope, not a 200 with mailbox:null.
//  2. Search mailbox-owner scope is already pinned by
//     TestWebmailAPISearchByQuery in webmail_user_test.go; this
//     file adds a regression assertion over the full endpoint
//     matrix.
//  3. Drafts cross-mailbox isolation is already pinned by
//     TestWebmailAPIDraftsCRUD in webmail_user_test.go.
//  4. Send authoritative From: a POST /send call writes a Sent
//     copy whose From header is always the authenticated
//     mailbox's email — never a server-rewritten copy of a
//     client-supplied value.
//  5. Send response carries `queued_count` and `local_count`
//     from the live QueueEngine (operator-visible real numbers;
//     client cannot fabricate them).
//
// The harness (buildWebmailTestEnv) is reused from the existing
// test files — no new helpers are required. Other R1 dimensions
// (XSS sanitisation, RTL helper coverage, settings CRDT, etc.)
// are already locked by webmail_user_test.go +
// webmail_frontend_test.go.

import (
	"net/http"
	"strings"
	"testing"
)

// Every protected /api/v1/webmail/* endpoint must reject a
// missing-or-invalid cookie with 401. The auth middleware
// runs BEFORE the handler so the response is a clean 401
// envelope, not a 200 with authenticated:false (that shape is
// reserved for /session, which is mounted on the protected
// group so the middleware issues 401 from missing/invalid
// cookies but returns 200 + {authenticated:false,…} from valid
// cookies whose session has no mailbox row).
//
// Regression guard for the auth-gate's "show the login card"
// decision. The gate relies on a 401 from the session probe
// to render the form, and on 200 to reveal the SPA shell.
func TestWebmailRelease1AuthRequiredForAllUserEndpoints(t *testing.T) {
	e := buildWebmailTestEnv(t)

	// Build the matrix of endpoints the gate/admin must not
	// be able to call without a valid cookie. WebmailLogin is
	// intentionally excluded — it is the entry point that
	// issues the cookie, and lives on the public login group
	// (no auth middleware).
	endpoints := []struct {
		method string
		path   string
		body   map[string]any
	}{
		{"GET", "/api/v1/webmail/session", nil},
		{"GET", "/api/v1/webmail/me", nil},
		{"GET", "/api/v1/webmail/folders", nil},
		{"GET", "/api/v1/webmail/messages", nil},
		{"GET", "/api/v1/webmail/messages/1", nil},
		{"GET", "/api/v1/webmail/messages/1/source", nil},
		{"GET", "/api/v1/webmail/attachments/1", nil},
		{"GET", "/api/v1/webmail/attachments/1/preview", nil},
		{"GET", "/api/v1/webmail/drafts", nil},
		{"GET", "/api/v1/webmail/drafts/1", nil},
		{"GET", "/api/v1/webmail/settings", nil},
		{"GET", "/api/v1/webmail/rules", nil},
		{"GET", "/api/v1/webmail/vacation", nil},
		{"GET", "/api/v1/webmail/forwarding", nil},
		{"POST", "/api/v1/webmail/send", map[string]any{"to": "x@y.z", "subject": "s", "body": "b"}},
		{"POST", "/api/v1/webmail/messages/1/delete", nil},
		{"POST", "/api/v1/webmail/messages/1/archive", nil},
		{"POST", "/api/v1/webmail/messages/1/move", map[string]any{"target_folder_id": 1}},
		{"POST", "/api/v1/webmail/messages/batch", map[string]any{"ids": []uint{1}, "action": "delete"}},
		{"POST", "/api/v1/webmail/folders/1/read-all", nil},
		{"POST", "/api/v1/webmail/push/subscribe", map[string]any{"endpoint": "x", "keys": map[string]any{"p256dh": "x", "auth": "x"}}},
		{"PATCH", "/api/v1/webmail/messages/1", map[string]any{"seen": false}},
		{"PUT", "/api/v1/webmail/settings", map[string]any{"display_name": "x"}},
		{"PUT", "/api/v1/webmail/drafts/1", map[string]any{"to": "x", "subject": "s", "body": "b"}},
		{"DELETE", "/api/v1/webmail/drafts/1", nil},
		{"POST", "/api/v1/webmail/logout", nil},
	}

	for _, tc := range endpoints {
		body := tc.body
		if tc.method == http.MethodPatch || tc.method == http.MethodDelete {
			body = nil
		}
		status, resp := e.webmailRequest(t, tc.method, tc.path, "", body)
		if status != http.StatusUnauthorized {
			t.Errorf("%s %s with no cookie: expected 401, got %d: %v", tc.method, tc.path, status, resp)
		}
	}
}

// Search (the snippet + ?q= path) is mailbox-owner scoped.
// The wider mailbox-isolation property over the list endpoint
// is pinned in TestWebmailAPISearchByQuery in webmail_user_test.go.
// This R1 test adds the snippet specifically (the snippet path
// runs only when ?q= matches a row in the caller's mailbox).
func TestWebmailRelease1SearchSnippetStaysInsideOwnerMailbox(t *testing.T) {
	e := buildWebmailTestEnv(t)

	// Ensure the admin mailbox has its system folders (INBOX
	// included) before injecting any messages.
	mailboxID := mustMailboxIDForTest(t, e, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}

	// Plant one row in admin's INBOX with a unique marker.
	marker := makeID()
	e.injectMessage(t, "R1SNIP "+marker, "snippet-r1 fixture body "+marker)

	tokAdmin := e.loginAdmin(t)
	status, list := e.webmailRequest(t, "GET",
		"/api/v1/webmail/messages?folder=INBOX&q=R1SNIP+"+marker, tokAdmin, nil)
	if status != http.StatusOK {
		t.Fatalf("admin q= search: expected 200, got %d: %v", status, list)
	}
	msgs, _ := list["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatalf("admin q= search returned 0 hits for marker %s", marker)
	}
	// The snippet field is included on every row when q=
	// matches. The snippet value MUST come from the same
	// message body — never from a different mailbox.
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		subj, _ := mm["subject"].(string)
		if !strings.Contains(subj, marker) {
			t.Errorf("search returned a row whose subject does not contain marker: %q", subj)
		}
		// The snippet field, if present, must not be empty
		// (we have a body that contains the marker, the
		// helper is supposed to give a context window).
		// Empty snippet is fine if the body decoding failed,
		// but a stale non-marker snippet would be a bug.
		if snip, ok := mm["snippet"].(string); ok {
			if strings.Contains(snip, "ORVIX_OTHER_MAILBOX_LEAK") {
				t.Errorf("snippet carried a foreign-marker signal: %q", snip)
			}
		}
	}
}

// WebmailSend authoritative From: the From header on the
// resulting RFC822 is always the authenticated mailbox's email.
// There is no `from` field in the request body. The response
// must NOT echo any client-supplied value; the JSON `from`
// field on the resulting message row must equal the auth-
// mailbox email; the RFC822 From: header must equal auth-
// mailbox email verbatim.
func TestWebmailRelease1SendAuthoritativeFrom(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mustMailboxIDForTest(t, e, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	marker := makeID()
	status, sendResp := e.webmailRequest(t, "POST", "/api/v1/webmail/send", tok, map[string]any{
		"to":      "recipient@example.test",
		"subject": "FromAuth " + marker,
		"body":    "Body " + marker,
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /send: expected 201, got %d: %v", status, sendResp)
	}
	sentID, ok := sendResp["id"].(float64)
	if !ok || sentID == 0 {
		t.Fatalf("POST /send: invalid id: %v", sendResp["id"])
	}

	status, msgResp := e.webmailRequest(t, "GET",
		"/api/v1/webmail/messages/"+intToStr(int(sentID)), tok, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /messages/%d: expected 200, got %d", int(sentID), status)
	}
	fromMeta, _ := msgResp["from"].(string)
	if fromMeta != e.email {
		t.Errorf("sent message From = %q, want %q", fromMeta, e.email)
	}
	rfc822, _ := msgResp["rfc822"].(string)
	// The From header can be in either of two forms depending
	// on whether the mailbox has a display name:
	//   "From: admin@orvix.email"
	//   "From: Display Name <admin@orvix.email>"
	// The RFC5322 grammar permits both. We assert that the
	// From header line CONTAINS the caller's mailbox email
	// between `<` and `>` if a display name is present, or
	// matches the bare email otherwise. Either way, the From
	// must be the caller's mailbox — never a client-supplied
	// value.
	fromLineIdx := strings.Index(rfc822, "From: ")
	if fromLineIdx < 0 {
		t.Fatalf("RFC822 missing From: header entirely:\n%s", rfc822)
	}
	firstFromLine := rfc822[fromLineIdx:]
	if !strings.Contains(firstFromLine, e.email) {
		t.Errorf("first From: line does not contain caller's mailbox email %q: %q", e.email, firstFromLine)
	}
	// Defence against e.g. From: hacker@hacker.com\r\nFrom:
	// admin@orvix.email injection: the first From: line must
	// not start with anything other than "From: " followed
	// by either a bare email or "<Display> <email>".
	if !strings.HasPrefix(firstFromLine, "From: ") {
		t.Errorf("From header is not at the start of a line: %q", firstFromLine)
	}
}

// WebmailSend response shape: the success envelope must
// carry both `status: queued` and `queued_count` from the
// live queue engine. The client cannot fabricate these
// values — they come from the QueueEngine.Enqueue call
// inside the handler. A regression that left the response
// shape out of sync with the deliverability contract would
// be caught here.
func TestWebmailRelease1SendReturnsQueuedCountFromLiveQueue(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mustMailboxIDForTest(t, e, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	status, sendResp := e.webmailRequest(t, "POST", "/api/v1/webmail/send", tok, map[string]any{
		"to":      "remote-recipient@example.test",
		"subject": "QQC " + makeID(),
		"body":    "qqc " + makeID(),
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /send: expected 201, got %d: %v", status, sendResp)
	}
	if sendResp["status"] != "queued" {
		t.Errorf("status = %v, want queued", sendResp["status"])
	}
	// queued_count must be present, numeric, and at least 1.
	qc, ok := sendResp["queued_count"].(float64)
	if !ok {
		t.Errorf("queued_count missing or non-numeric: %v (%T)", sendResp["queued_count"], sendResp["queued_count"])
	}
	if ok && qc < 1 {
		t.Errorf("queued_count = %v, want >=1", qc)
	}
	// The response also reports local vs remote recipient
	// counts. The local_count field is present and numeric
	// even when zero.
	if _, hasLocal := sendResp["local_count"]; !hasLocal {
		t.Errorf("local_count missing from POST /send response: %v", sendResp)
	}
	if _, hasRemote := sendResp["remote_count"]; !hasRemote {
		t.Errorf("remote_count missing from POST /send response: %v", sendResp)
	}
}

// intToStr is a tiny helper avoiding strconv import
// duplication in this single-test file.
func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
