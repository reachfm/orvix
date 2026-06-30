package handlers_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"
)

// ────────────────────────────────────────────────────────────
// E2E smoke: login → compose → send → receive → open
// ────────────────────────────────────────────────────────────

func TestWebmailE2ELoginComposeReceive(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}

	// 1. Login to webmail
	tok := e.loginAdmin(t)
	if tok == "" {
		t.Fatal("login returned empty token")
	}

	// 2. Verify session is valid
	status, sess := e.webmailRequest(t, "GET", "/api/v1/webmail/session", tok, nil)
	if status != 200 {
		t.Fatalf("GET /session: expected 200, got %d: %v", status, sess)
	}
	if sess["authenticated"] != true {
		t.Fatalf("GET /session: authenticated = %v, want true", sess["authenticated"])
	}

	// 3. Compose and send a message
	bodyMarker := makeID()
	req := map[string]string{
		"to":      "recipient@example.com",
		"subject": "E2E Send " + bodyMarker,
		"body":    "This is the E2E test body " + bodyMarker,
	}
	status, sendResp := e.webmailRequest(t, "POST", "/api/v1/webmail/send", tok, req)
	if status != http.StatusCreated {
		t.Fatalf("POST /send: expected 201, got %d: %v", status, sendResp)
	}
	if sendResp["status"] != "queued" {
		t.Fatalf("POST /send: status = %v, want queued", sendResp["status"])
	}
	sentID, ok := sendResp["id"].(float64)
	if !ok || sentID == 0 {
		t.Fatalf("POST /send: invalid id: %v", sendResp["id"])
	}

	// 4. Verify sent copy in Sent folder
	status, sentList := e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=Sent", tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages?folder=Sent: expected 200, got %d", status)
	}
	messages, _ := sentList["messages"].([]interface{})
	foundSent := false
	for _, m := range messages {
		mm, _ := m.(map[string]interface{})
		if int(mm["id"].(float64)) == int(sentID) {
			foundSent = true
			if subj, _ := mm["subject"].(string); !strings.Contains(subj, bodyMarker) {
				t.Errorf("Sent message subject missing marker: %q", subj)
			}
			break
		}
	}
	if !foundSent {
		t.Errorf("Sent message id=%v not in Sent folder", int(sentID))
	}

	// 5. Open the sent message
	status, msgResp := e.webmailRequest(t, "GET", fmt.Sprintf("/api/v1/webmail/messages/%d", int(sentID)), tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages/%d: expected 200, got %d", int(sentID), status)
	}
	rfc822, _ := msgResp["rfc822"].(string)
	if !strings.Contains(rfc822, bodyMarker) {
		t.Errorf("RFC822 body missing marker %q", bodyMarker)
	}
	if subj, _ := msgResp["subject"].(string); !strings.Contains(subj, bodyMarker) {
		t.Errorf("Message subject missing marker: %q", subj)
	}

	// 6. Verify /me returns the correct mailbox
	status, me := e.webmailRequest(t, "GET", "/api/v1/webmail/me", tok, nil)
	if status != 200 {
		t.Fatalf("GET /me: expected 200, got %d", status)
	}
	mb, _ := me["mailbox"].(map[string]interface{})
	if mb == nil {
		t.Fatal("GET /me: mailbox is nil")
	}
	if mb["email"] != e.email {
		t.Errorf("GET /me: mailbox email = %v, want %s", mb["email"], e.email)
	}
}

// ────────────────────────────────────────────────────────────
// Folder list
// ────────────────────────────────────────────────────────────

func TestWebmailE2EFolderList(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mustMailboxIDForTest(t, e, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	status, body := e.webmailRequest(t, "GET", "/api/v1/webmail/folders", tok, nil)
	if status != 200 {
		t.Fatalf("GET /folders: expected 200, got %d: %v", status, body)
	}
	folders, ok := body["folders"].([]interface{})
	if !ok {
		t.Fatalf("GET /folders: expected folders array, got: %v", body)
	}
	if len(folders) == 0 {
		t.Fatal("GET /folders: empty folder list")
	}

	// All system folders must be present.
	wantFolders := map[string]bool{
		"INBOX": false, "Sent": false, "Drafts": false,
		"Trash": false, "Junk": false, "Archive": false,
	}
	for _, f := range folders {
		ff, _ := f.(map[string]interface{})
		name, _ := ff["path"].(string)
		if _, ok := wantFolders[name]; ok {
			wantFolders[name] = true
		}
		// Each folder must have essential fields.
		if ff["id"] == nil || ff["id"].(float64) == 0 {
			t.Errorf("folder %s missing id", name)
		}
		if _, hasName := ff["name"]; !hasName {
			t.Errorf("folder %s missing name", name)
		}
		if _, hasType := ff["folder_type"]; !hasType {
			t.Errorf("folder %s missing folder_type", name)
		}
	}
	for name, found := range wantFolders {
		if !found {
			t.Errorf("system folder %s missing from response", name)
		}
	}
}

// ────────────────────────────────────────────────────────────
// Message list with pagination
// ────────────────────────────────────────────────────────────

func TestWebmailE2EMessageList(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mustMailboxIDForTest(t, e, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	// Inject 5 messages.
	for i := 0; i < 5; i++ {
		e.injectMessage(t, fmt.Sprintf("Message %d", i), fmt.Sprintf("Body %d", i))
	}

	// Default listing (no params = limit 50, offset 0).
	status, body := e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=INBOX", tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages: expected 200, got %d: %v", status, body)
	}
	messages, _ := body["messages"].([]interface{})
	if len(messages) != 5 {
		t.Errorf("GET /messages: expected 5 messages, got %d", len(messages))
	}
	if body["folder"] != "INBOX" {
		t.Errorf("GET /messages: folder = %v, want INBOX", body["folder"])
	}

	// Limit=2, offset=0: first page with has_more=true.
	status, p1 := e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=INBOX&limit=2&offset=0", tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages page 1: expected 200, got %d", status)
	}
	msgs1, _ := p1["messages"].([]interface{})
	if len(msgs1) != 2 {
		t.Errorf("page 1: expected 2 messages, got %d", len(msgs1))
	}
	if hasMore, _ := p1["has_more"].(bool); !hasMore {
		t.Error("page 1: has_more = false, want true (5 total, 2 per page)")
	}

	// Limit=2, offset=2: second page.
	status, p2 := e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=INBOX&limit=2&offset=2", tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages page 2: expected 200, got %d", status)
	}
	msgs2, _ := p2["messages"].([]interface{})
	if len(msgs2) != 2 {
		t.Errorf("page 2: expected 2 messages, got %d", len(msgs2))
	}

	// Limit=2, offset=4: last page, has_more=false.
	status, p3 := e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=INBOX&limit=2&offset=4", tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages page 3: expected 200, got %d", status)
	}
	msgs3, _ := p3["messages"].([]interface{})
	if len(msgs3) != 1 {
		t.Errorf("page 3: expected 1 message, got %d", len(msgs3))
	}
	if hasMore, _ := p3["has_more"].(bool); hasMore {
		t.Error("page 3: has_more = true, want false (last page)")
	}
}

// ────────────────────────────────────────────────────────────
// Open single message
// ────────────────────────────────────────────────────────────

func TestWebmailE2EMessageOpen(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mustMailboxIDForTest(t, e, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	id := e.injectMessage(t, "Test Subject Open", "Test body content for open test.")

	status, body := e.webmailRequest(t, "GET", fmt.Sprintf("/api/v1/webmail/messages/%d", id), tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages/%d: expected 200, got %d: %v", id, status, body)
	}

	// Check all expected fields.
	if body["id"] == nil || uint(body["id"].(float64)) != id {
		t.Errorf("id = %v, want %d", body["id"], id)
	}
	if body["subject"] != "Test Subject Open" {
		t.Errorf("subject = %v, want 'Test Subject Open'", body["subject"])
	}
	if body["from"] != "sender@example.com" {
		t.Errorf("from = %v, want 'sender@example.com'", body["from"])
	}

	rfc822, ok := body["rfc822"].(string)
	if !ok || rfc822 == "" {
		t.Errorf("rfc822 is empty or not a string")
	}
	if !strings.Contains(rfc822, "Test Subject Open") {
		t.Errorf("rfc822 missing subject")
	}
	if !strings.Contains(rfc822, "Test body content for open test.") {
		t.Errorf("rfc822 missing body")
	}

	// Opening marks as seen.
	if seen, _ := body["seen"].(bool); !seen {
		t.Error("message seen = false after open, want true")
	}
}

// ────────────────────────────────────────────────────────────
// Session expired / invalid token
// ────────────────────────────────────────────────────────────

func TestWebmailE2ESessionExpired(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mustMailboxIDForTest(t, e, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}

	// Empty token.
	status, _ := e.webmailRequest(t, "GET", "/api/v1/webmail/me", "", nil)
	if status != 401 && status != 403 {
		t.Errorf("GET /me (no token): expected 401 or 403, got %d", status)
	}

	// Garbage token.
	status, _ = e.webmailRequest(t, "GET", "/api/v1/webmail/me", "garbage_invalid_token", nil)
	if status != 401 && status != 403 {
		t.Errorf("GET /me (garbage token): expected 401 or 403, got %d", status)
	}

	// Garbage token on folders, messages, send.
	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/webmail/folders"},
		{"GET", "/api/v1/webmail/messages?folder=INBOX"},
		{"POST", "/api/v1/webmail/send"},
		{"GET", "/api/v1/webmail/drafts"},
	}
	for _, ep := range endpoints {
		bodyObj := interface{}(nil)
		if ep.method == "POST" {
			bodyObj = map[string]string{"to": "x@example.com", "subject": "x", "body": "x"}
		}
		status, resp := e.webmailRequest(t, ep.method, ep.path, "bad_token", bodyObj)
		if status != 401 && status != 403 {
			t.Errorf("%s %s (garbage token): expected 401 or 403, got %d: %v", ep.method, ep.path, status, resp)
		}
	}
}

// ────────────────────────────────────────────────────────────
// Logout flow
// ────────────────────────────────────────────────────────────

func TestWebmailE2ELogout(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mustMailboxIDForTest(t, e, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	// Verify authenticated before logout.
	status, me := e.webmailRequest(t, "GET", "/api/v1/webmail/me", tok, nil)
	if status != 200 {
		t.Fatalf("pre-logout GET /me: expected 200, got %d: %v", status, me)
	}
	if me["mailbox"] == nil {
		t.Fatal("pre-logout: mailbox is nil")
	}

	// Fetch CSRF token required for webmail logout.
	csrfReq := httptest.NewRequest("GET", "/api/v1/csrf-token", nil)
	csrfReq.Header.Set("Cookie", "access_token="+tok)
	csrfRespObj, err := e.router.App().Test(csrfReq, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("csrf-token request: %v", err)
	}
	var csrfTok string
	for _, c := range csrfRespObj.Cookies() {
		if c.Name == "csrf_token" {
			csrfTok = c.Value
			break
		}
	}
	if csrfTok == "" {
		t.Fatal("no csrf_token cookie returned for CSRF-protected logout")
	}

	// Webmail logout with CSRF.
	logoutReq := httptest.NewRequest("POST", "/api/v1/webmail/logout", nil)
	logoutReq.Header.Set("Cookie", "access_token="+tok+"; csrf_token="+csrfTok)
	logoutReq.Header.Set("X-CSRF-Token", csrfTok)
	logoutReq.Header.Set("Content-Type", "application/json")
	logoutResp, err := e.router.App().Test(logoutReq, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("POST /webmail/logout: %v", err)
	}
	if logoutResp.StatusCode != 200 {
		logoutBody, _ := io.ReadAll(logoutResp.Body)
		t.Fatalf("POST /webmail/logout: expected 200, got %d: %s", logoutResp.StatusCode, string(logoutBody))
	}
	logoutBody, _ := io.ReadAll(logoutResp.Body)
	var logout map[string]interface{}
	json.Unmarshal(logoutBody, &logout)
	if logout["status"] != "logged_out" {
		t.Errorf("POST /webmail/logout: status = %v, want logged_out", logout["status"])
	}

	// After logout, the server has cleared the session. Auth
	// middleware may still validate the JWT (it hasn't expired),
	// so the session probe may still return authenticated=true.
	// This is expected: access tokens are valid until they expire
	// or are revoked via refresh-token invalidation.
}

// ────────────────────────────────────────────────────────────
// Drafts CRUD: save → list → load → update → delete
// ────────────────────────────────────────────────────────────

func TestWebmailE2EDraftsSaveLoadDelete(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mustMailboxIDForTest(t, e, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	// Create a draft.
	draftMarker := makeID()
	status, createResp := e.webmailRequest(t, "POST", "/api/v1/webmail/drafts", tok, map[string]string{
		"to":      "draft-to@example.com",
		"subject": "Draft " + draftMarker,
		"body":    "Draft body " + draftMarker,
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /drafts: expected 201, got %d: %v", status, createResp)
	}
	if createResp["status"] != "draft" {
		t.Fatalf("POST /drafts: status = %v, want draft", createResp["status"])
	}
	draftID, ok := createResp["id"].(float64)
	if !ok || draftID == 0 {
		t.Fatalf("POST /drafts: invalid id: %v", createResp["id"])
	}

	// List drafts — the new draft must appear.
	status, listResp := e.webmailRequest(t, "GET", "/api/v1/webmail/drafts", tok, nil)
	if status != 200 {
		t.Fatalf("GET /drafts: expected 200, got %d: %v", status, listResp)
	}
	drafts, _ := listResp["drafts"].([]interface{})
	found := false
	for _, d := range drafts {
		dd, _ := d.(map[string]interface{})
		if int(dd["id"].(float64)) == int(draftID) {
			found = true
			if subj, _ := dd["subject"].(string); !strings.Contains(subj, draftMarker) {
				t.Errorf("listed draft subject = %q, want containing %q", subj, draftMarker)
			}
			break
		}
	}
	if !found {
		t.Errorf("draft id=%d not in drafts list (%d drafts)", int(draftID), len(drafts))
	}

	// Load the draft.
	status, loadResp := e.webmailRequest(t, "GET", fmt.Sprintf("/api/v1/webmail/drafts/%d", int(draftID)), tok, nil)
	if status != 200 {
		t.Fatalf("GET /drafts/%d: expected 200, got %d: %v", int(draftID), status, loadResp)
	}
	if loadResp["status"] != "draft" {
		t.Errorf("GET /drafts/%d: status = %v, want draft", int(draftID), loadResp["status"])
	}
	body, _ := loadResp["body"].(string)
	if !strings.Contains(body, draftMarker) {
		t.Errorf("loaded draft body missing marker %q: %q", draftMarker, body)
	}

	// Update the draft.
	newBody := "Updated body " + makeID()
	status, updateResp := e.webmailRequest(t, "PUT", fmt.Sprintf("/api/v1/webmail/drafts/%d", int(draftID)), tok, map[string]string{
		"to":      "updated-to@example.com",
		"subject": "Updated Draft",
		"body":    newBody,
	})
	if status != 200 {
		t.Fatalf("PUT /drafts/%d: expected 200, got %d: %v", int(draftID), status, updateResp)
	}
	if updateResp["status"] != "updated" {
		t.Errorf("PUT /drafts/%d: status = %v, want updated", int(draftID), updateResp["status"])
	}

	// Reload and verify update persisted.
	status, reloadResp := e.webmailRequest(t, "GET", fmt.Sprintf("/api/v1/webmail/drafts/%d", int(draftID)), tok, nil)
	if status != 200 {
		t.Fatalf("GET /drafts/%d after update: expected 200, got %d", int(draftID), status)
	}
	reloadBody, _ := reloadResp["body"].(string)
	if !strings.Contains(reloadBody, newBody) {
		t.Errorf("reloaded draft body missing update: %q", reloadBody)
	}

	// Delete the draft.
	status, delResp := e.webmailRequest(t, "DELETE", fmt.Sprintf("/api/v1/webmail/drafts/%d", int(draftID)), tok, nil)
	if status != 200 {
		t.Fatalf("DELETE /drafts/%d: expected 200, got %d: %v", int(draftID), status, delResp)
	}
	if delResp["status"] != "deleted" {
		t.Errorf("DELETE /drafts/%d: status = %v, want deleted", int(draftID), delResp["status"])
	}

	// Verify draft no longer in list.
	status, listAfter := e.webmailRequest(t, "GET", "/api/v1/webmail/drafts", tok, nil)
	if status != 200 {
		t.Fatalf("GET /drafts after delete: expected 200, got %d", status)
	}
	for _, d := range listAfter["drafts"].([]interface{}) {
		dd, _ := d.(map[string]interface{})
		if int(dd["id"].(float64)) == int(draftID) {
			t.Errorf("draft id=%d still in list after delete", int(draftID))
		}
	}
}

// ────────────────────────────────────────────────────────────
// Empty states
// ────────────────────────────────────────────────────────────

func TestWebmailE2EEmptyStates(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mustMailboxIDForTest(t, e, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	// Empty Inbox.
	status, inbox := e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=INBOX", tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages?folder=INBOX: expected 200, got %d", status)
	}
	messages, _ := inbox["messages"].([]interface{})
	if len(messages) != 0 {
		t.Errorf("empty INBOX: expected 0 messages, got %d", len(messages))
	}
	if inbox["folder"] != "INBOX" {
		t.Errorf("empty INBOX: folder = %v, want INBOX", inbox["folder"])
	}

	// Empty Sent.
	status, sent := e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=Sent", tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages?folder=Sent: expected 200, got %d", status)
	}
	sentMsgs, _ := sent["messages"].([]interface{})
	if len(sentMsgs) != 0 {
		t.Errorf("empty Sent: expected 0 messages, got %d", len(sentMsgs))
	}

	// Empty Drafts list.
	status, drafts := e.webmailRequest(t, "GET", "/api/v1/webmail/drafts", tok, nil)
	if status != 200 {
		t.Fatalf("GET /drafts: expected 200, got %d", status)
	}
	draftMsgs, _ := drafts["drafts"].([]interface{})
	if len(draftMsgs) != 0 {
		t.Errorf("empty Drafts: expected 0 drafts, got %d", len(draftMsgs))
	}

	// Empty Trash.
	status, trash := e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=Trash", tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages?folder=Trash: expected 200, got %d", status)
	}
	trashMsgs, _ := trash["messages"].([]interface{})
	if len(trashMsgs) != 0 {
		t.Errorf("empty Trash: expected 0 messages, got %d", len(trashMsgs))
	}

	// Empty search.
	status, search := e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=INBOX&q=nonexistent", tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages search: expected 200, got %d", status)
	}
	searchMsgs, _ := search["messages"].([]interface{})
	if len(searchMsgs) != 0 {
		t.Errorf("empty search: expected 0 results, got %d", len(searchMsgs))
	}

	// Folder list should still have entries (system folders).
	status, folders := e.webmailRequest(t, "GET", "/api/v1/webmail/folders", tok, nil)
	if status != 200 {
		t.Fatalf("GET /folders: expected 200, got %d", status)
	}
	folderList, _ := folders["folders"].([]interface{})
	if len(folderList) == 0 {
		t.Error("folders list is empty even though system folders exist")
	}
}

// ────────────────────────────────────────────────────────────
// API error handling
// ────────────────────────────────────────────────────────────

func TestWebmailE2EAPIErrors(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mustMailboxIDForTest(t, e, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	// Non-existent message.
	status, resp := e.webmailRequest(t, "GET", "/api/v1/webmail/messages/99999", tok, nil)
	if status != http.StatusNotFound {
		t.Errorf("GET /messages/99999: expected 404, got %d: %v", status, resp)
	}
	if _, ok := resp["error"]; !ok {
		t.Errorf("GET /messages/99999: missing error field: %v", resp)
	}

	// Non-existent folder.
	status, resp = e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=NoSuchFolder", tok, nil)
	if status != http.StatusNotFound {
		t.Errorf("GET /messages?folder=NoSuchFolder: expected 404, got %d: %v", status, resp)
	}
	if errField, ok := resp["error"]; !ok || errField == "" {
		t.Errorf("GET /messages?folder=NoSuchFolder: missing or empty error: %v", resp)
	}

	// Invalid message id (non-numeric).
	req := httptest.NewRequest("GET", "/api/v1/webmail/messages/abc", nil)
	req.Header.Set("Cookie", "access_token="+tok)
	respHTTP, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("GET /messages/abc: %v", err)
	}
	if respHTTP.StatusCode != http.StatusBadRequest {
		t.Errorf("GET /messages/abc: expected 400, got %d", respHTTP.StatusCode)
	}

	// Send with no To.
	status, resp = e.webmailRequest(t, "POST", "/api/v1/webmail/send", tok, map[string]string{
		"subject": "No to",
		"body":    "No recipient.",
	})
	if status != http.StatusBadRequest {
		t.Errorf("POST /send (missing to): expected 400, got %d: %v", status, resp)
	}
	if errField, ok := resp["error"]; !ok || errField == "" {
		t.Errorf("POST /send (missing to): missing error field: %v", resp)
	}

	// Invalid limit.
	status, resp = e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=INBOX&limit=-5", tok, nil)
	if status != http.StatusBadRequest {
		t.Errorf("GET /messages?limit=-5: expected 400, got %d: %v", status, resp)
	}

	// Invalid offset.
	status, resp = e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=INBOX&offset=abc", tok, nil)
	if status != http.StatusBadRequest {
		t.Errorf("GET /messages?offset=abc: expected 400, got %d: %v", status, resp)
	}

	// Send with invalid recipient.
	status, resp = e.webmailRequest(t, "POST", "/api/v1/webmail/send", tok, map[string]string{
		"to":      "not-an-email",
		"subject": "Bad recipient",
		"body":    "Body.",
	})
	if status != http.StatusBadRequest {
		t.Errorf("POST /send (invalid to): expected 400, got %d: %v", status, resp)
	}

	// Send with empty body to make sure it's accepted.
	status, resp = e.webmailRequest(t, "POST", "/api/v1/webmail/send", tok, map[string]string{
		"to":      "ok@example.com",
		"subject": "Empty body",
		"body":    "",
	})
	if status != http.StatusCreated {
		t.Errorf("POST /send (empty body): expected 201, got %d: %v", status, resp)
	}

	// Batch with no ids.
	status, resp = e.webmailRequest(t, "POST", "/api/v1/webmail/messages/batch", tok, map[string]interface{}{
		"ids":    []interface{}{},
		"action": "markRead",
	})
	if status != http.StatusBadRequest {
		t.Errorf("POST /batch (empty ids): expected 400, got %d: %v", status, resp)
	}

	// Batch with invalid action.
	status, resp = e.webmailRequest(t, "POST", "/api/v1/webmail/messages/batch", tok, map[string]interface{}{
		"ids":    []interface{}{float64(1)},
		"action": "invalidAction",
	})
	if status != http.StatusBadRequest {
		t.Errorf("POST /batch (invalid action): expected 400, got %d: %v", status, resp)
	}

	// Delete non-existent message.
	status, resp = e.webmailRequest(t, "POST", "/api/v1/webmail/messages/99999/delete", tok, nil)
	if status != http.StatusNotFound {
		t.Errorf("POST /messages/99999/delete: expected 404, got %d: %v", status, resp)
	}

	// Save draft without body is ok.
	status, resp = e.webmailRequest(t, "POST", "/api/v1/webmail/drafts", tok, map[string]string{
		"subject": "Minimal draft",
	})
	if status != http.StatusCreated {
		t.Errorf("POST /drafts (minimal): expected 201, got %d: %v", status, resp)
	}
}

// ────────────────────────────────────────────────────────────
// Cross-mailbox ownership isolation
// ────────────────────────────────────────────────────────────

func TestWebmailE2ECrossMailboxIsolation(t *testing.T) {
	e := buildWebmailTestEnv(t)
	adminID := mustMailboxIDForTest(t, e, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), adminID, nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	// Inject a message for the admin.
	adminMsgID := e.injectMessage(t, "Admin message", "Admin body")
	if adminMsgID == 0 {
		t.Fatal("injectMessage returned 0")
	}

	// Provision a second user with a mailbox.
	provisionSecondUser(t, e, "bob@orvix.email", "BobPass!1")
	bobID := mustSecondMailboxID(t, e, "bob@orvix.email")
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), bobID, nil); err != nil {
		t.Fatalf("ensure system folders for bob: %v", err)
	}
	bobTok := loginAs(t, e, "bob@orvix.email", "BobPass!1")

	// Bob tries to read admin's message — should get 404.
	status, resp := e.webmailRequest(t, "GET", fmt.Sprintf("/api/v1/webmail/messages/%d", adminMsgID), bobTok, nil)
	if status != http.StatusNotFound {
		t.Errorf("cross-mailbox read: expected 404, got %d: %v", status, resp)
	}

	// Bob tries to delete admin's message — should get 404.
	status, resp = e.webmailRequest(t, "POST", fmt.Sprintf("/api/v1/webmail/messages/%d/delete", adminMsgID), bobTok, nil)
	if status != http.StatusNotFound {
		t.Errorf("cross-mailbox delete: expected 404, got %d: %v", status, resp)
	}

	// Admin can still read and delete their own message.
	status, msgResp := e.webmailRequest(t, "GET", fmt.Sprintf("/api/v1/webmail/messages/%d", adminMsgID), tok, nil)
	if status != 200 {
		t.Errorf("admin own message read: expected 200, got %d: %v", status, msgResp)
	}

	// Bob sends a message that admin doesn't see in Bob's sent.
	bobMarker := makeID()
	status, sendResp := e.webmailRequest(t, "POST", "/api/v1/webmail/send", bobTok, map[string]string{
		"to":      "external@example.com",
		"subject": "Bob's " + bobMarker,
		"body":    "Bob's body " + bobMarker,
	})
	if status != http.StatusCreated {
		t.Fatalf("bob's POST /send: expected 201, got %d: %v", status, sendResp)
	}
	bobSentID, _ := sendResp["id"].(float64)

	// Admin tries to read Bob's sent message — 404.
	status, _ = e.webmailRequest(t, "GET", fmt.Sprintf("/api/v1/webmail/messages/%d", int(bobSentID)), tok, nil)
	if status != http.StatusNotFound {
		t.Errorf("admin reading bob's message: expected 404, got %d", status)
	}

	// Bob can read their own sent message.
	status, bobRead := e.webmailRequest(t, "GET", fmt.Sprintf("/api/v1/webmail/messages/%d", int(bobSentID)), bobTok, nil)
	if status != 200 {
		t.Errorf("bob reading own message: expected 200, got %d: %v", status, bobRead)
	}
}

// ────────────────────────────────────────────────────────────
// Webmail login probe (session endpoint)
// ────────────────────────────────────────────────────────────

func TestWebmailE2ESessionProbe(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mustMailboxIDForTest(t, e, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}

	// Unauthenticated session probe returns 401 from auth middleware.
	req := httptest.NewRequest("GET", "/api/v1/webmail/session", nil)
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("session probe: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /session (no auth): expected 401, got %d", resp.StatusCode)
	}

	// Authenticated session probe returns 200 with mailbox info.
	tok := e.loginAdmin(t)
	status, body := e.webmailRequest(t, "GET", "/api/v1/webmail/session", tok, nil)
	if status != 200 {
		t.Fatalf("GET /session (auth): expected 200, got %d: %v", status, body)
	}
	if body["authenticated"] != true {
		t.Fatalf("GET /session: authenticated = %v, want true", body["authenticated"])
	}
	mb, _ := body["mailbox"].(map[string]interface{})
	if mb == nil || mb["email"] != e.email {
		t.Errorf("GET /session: mailbox email = %v, want %s", mb["email"], e.email)
	}
}

// ────────────────────────────────────────────────────────────
// Webmail login endpoint (POST /api/v1/webmail/login)
// ────────────────────────────────────────────────────────────

func TestWebmailE2ELoginEndpoint(t *testing.T) {
	e := buildWebmailTestEnv(t)

	// The webmail login endpoint verifies against the mailbox's
	// password_hash in coremail_mailboxes. Our test fixtures have
	// a placeholder hash, so we need to set a real bcrypt hash
	// on the mailbox before we can test webmail login.
	sqlDB := e.mailbox.DB
	hash, err := bcrypt.GenerateFromPassword([]byte(e.password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	_, err = sqlDB.Exec(
		"UPDATE coremail_mailboxes SET password_hash = ?, auth_scheme = 'bcrypt' WHERE email = ?",
		string(hash), e.email,
	)
	if err != nil {
		t.Fatalf("update mailbox hash: %v", err)
	}

	// Successful login via webmail login endpoint.
	payload, _ := json.Marshal(map[string]string{
		"email":    e.email,
		"password": e.password,
	})
	req := httptest.NewRequest("POST", "/api/v1/webmail/login", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("webmail login: %v", err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /webmail/login: expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	// Parse response.
	respBody, _ := io.ReadAll(resp.Body)
	var loginResp map[string]interface{}
	json.Unmarshal(respBody, &loginResp)
	if loginResp["authenticated"] != true {
		t.Fatalf("POST /webmail/login: authenticated = %v, want true", loginResp["authenticated"])
	}
	mb, _ := loginResp["mailbox"].(map[string]interface{})
	if mb == nil || mb["email"] != e.email {
		t.Errorf("POST /webmail/login: mailbox email = %v, want %s", mb["email"], e.email)
	}
	// Verify access_token cookie is set.
	hasAccessToken := false
	for _, c := range resp.Cookies() {
		if c.Name == "access_token" {
			hasAccessToken = true
			if c.Value == "" {
				t.Error("access_token cookie has empty value")
			}
			break
		}
	}
	if !hasAccessToken {
		t.Error("access_token cookie not set after webmail login")
	}

	// Bad password.
	payload, _ = json.Marshal(map[string]string{
		"email":    e.email,
		"password": "wrong_password",
	})
	req = httptest.NewRequest("POST", "/api/v1/webmail/login", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err = e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("webmail login bad pw: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST /webmail/login (bad password): expected 401, got %d", resp.StatusCode)
	}

	// Missing email.
	payload, _ = json.Marshal(map[string]string{
		"password": e.password,
	})
	req = httptest.NewRequest("POST", "/api/v1/webmail/login", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err = e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("webmail login no email: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("POST /webmail/login (no email): expected 400, got %d", resp.StatusCode)
	}

	// Invalid email format.
	payload, _ = json.Marshal(map[string]string{
		"email":    "not-valid",
		"password": "anything",
	})
	req = httptest.NewRequest("POST", "/api/v1/webmail/login", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err = e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("webmail login invalid email: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("POST /webmail/login (invalid email): expected 400, got %d", resp.StatusCode)
	}
}

// ────────────────────────────────────────────────────────────
// Message batch operations
// ────────────────────────────────────────────────────────────

func TestWebmailE2EMessageBatch(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mustMailboxIDForTest(t, e, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	// Inject 3 messages.
	ids := make([]float64, 3)
	for i := 0; i < 3; i++ {
		ids[i] = float64(e.injectMessage(t, fmt.Sprintf("Batch %d", i), fmt.Sprintf("Body %d", i)))
	}

	// Batch mark read.
	status, resp := e.webmailRequest(t, "POST", "/api/v1/webmail/messages/batch", tok, map[string]interface{}{
		"ids":    []interface{}{ids[0], ids[1], ids[2]},
		"action": "markRead",
	})
	if status != 200 {
		t.Fatalf("POST /batch markRead: expected 200, got %d: %v", status, resp)
	}
	if resp["action"] != "markRead" {
		t.Errorf("action = %v, want markRead", resp["action"])
	}
	if resp["total"] != float64(3) {
		t.Errorf("total = %v, want 3", resp["total"])
	}
	if resp["succeeded"] != float64(3) {
		t.Errorf("succeeded = %v, want 3", resp["succeeded"])
	}
	if failed, _ := resp["failed"].(float64); failed != 0 {
		t.Errorf("failed = %v, want 0", failed)
	}

	// Batch flag.
	status, resp = e.webmailRequest(t, "POST", "/api/v1/webmail/messages/batch", tok, map[string]interface{}{
		"ids":    []interface{}{ids[0], ids[1]},
		"action": "flag",
	})
	if status != 200 {
		t.Fatalf("POST /batch flag: expected 200, got %d: %v", status, resp)
	}
	if resp["succeeded"] != float64(2) {
		t.Errorf("flag: succeeded = %v, want 2", resp["succeeded"])
	}

	// Batch archive.
	status, resp = e.webmailRequest(t, "POST", "/api/v1/webmail/messages/batch", tok, map[string]interface{}{
		"ids":    []interface{}{ids[0]},
		"action": "archive",
	})
	if status != 200 {
		t.Fatalf("POST /batch archive: expected 200, got %d: %v", status, resp)
	}

	// Verify archived message is in Archive folder.
	status, archiveList := e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=Archive", tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages?folder=Archive: expected 200, got %d", status)
	}
	archiveMsgs, _ := archiveList["messages"].([]interface{})
	foundArchived := false
	for _, m := range archiveMsgs {
		mm, _ := m.(map[string]interface{})
		if int(mm["id"].(float64)) == int(ids[0]) {
			foundArchived = true
			break
		}
	}
	if !foundArchived {
		t.Error("archived message not in Archive folder")
	}

	// Batch delete remaining messages.
	status, resp = e.webmailRequest(t, "POST", "/api/v1/webmail/messages/batch", tok, map[string]interface{}{
		"ids":    []interface{}{ids[1], ids[2]},
		"action": "delete",
	})
	if status != 200 {
		t.Fatalf("POST /batch delete: expected 200, got %d: %v", status, resp)
	}

	// Verify deleted messages are in Trash.
	status, trashList := e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=Trash", tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages?folder=Trash: expected 200, got %d", status)
	}
	trashMsgs, _ := trashList["messages"].([]interface{})
	trashIDs := map[int]bool{}
	for _, m := range trashMsgs {
		mm, _ := m.(map[string]interface{})
		trashIDs[int(mm["id"].(float64))] = true
	}
	if !trashIDs[int(ids[1])] {
		t.Errorf("batch deleted message %d not in Trash", int(ids[1]))
	}
	if !trashIDs[int(ids[2])] {
		t.Errorf("batch deleted message %d not in Trash", int(ids[2]))
	}

	// Batch with cross-mailbox id reports failure.
	status, resp = e.webmailRequest(t, "POST", "/api/v1/webmail/messages/batch", tok, map[string]interface{}{
		"ids":    []interface{}{float64(99999)},
		"action": "markRead",
	})
	if status != 200 {
		t.Fatalf("POST /batch nonexistent: expected 200, got %d", status)
	}
	if succeeded, _ := resp["succeeded"].(float64); succeeded != 0 {
		t.Errorf("batch nonexistent: succeeded = %v, want 0", succeeded)
	}
	if failed, _ := resp["failed"].(float64); failed != 1 {
		t.Errorf("batch nonexistent: failed = %v, want 1", failed)
	}
}

// ────────────────────────────────────────────────────────────
// Message source (raw RFC822 download)
// ────────────────────────────────────────────────────────────

func TestWebmailE2EMessageSource(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mustMailboxIDForTest(t, e, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	marker := makeID()
	id := e.injectMessage(t, "Source test "+marker, "Source body "+marker)

	// Request raw source.
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/webmail/messages/%d/source", id), nil)
	req.Header.Set("Cookie", "access_token="+tok)
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("GET source: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GET source: expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), marker) {
		t.Errorf("source body missing marker %q", marker)
	}
	if !strings.Contains(string(body), "From: sender@example.com") {
		t.Error("source body missing From header")
	}

	// Content-Type should be message/rfc822.
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(strings.ToLower(ct), "message/rfc822") {
		t.Errorf("Content-Type = %q, want message/rfc822", ct)
	}
}
