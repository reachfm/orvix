package handlers_test

// Tests for the Webmail Enterprise 2 backend endpoints:
//
//   - GET    /api/v1/webmail/messages?limit=&offset=&q=
//             — pagination, snippet, attachment_count
//   - GET    /api/v1/webmail/messages/:id  (attachments list)
//   - GET    /api/v1/webmail/messages/:id/source
//   - GET    /api/v1/webmail/attachments/:id
//   - GET    /api/v1/webmail/attachments/:id/preview
//   - POST   /api/v1/webmail/messages/:id/move
//   - POST   /api/v1/webmail/messages/batch
//
// Each test exercises a real route against the live
// router. Where applicable the test asserts both the
// happy path and one ownership / cross-mailbox failure.

import (
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
	"github.com/orvix/orvix/internal/coremail/storage"
)

// appTest runs an httptest request against the live
// router with the supplied auth cookie. Centralised so
// every test below shares the same config.
func (e *webmailTestEnv) appTest(t *testing.T, method, path, accessToken string, body io.Reader) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
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
	return resp
}

// provisionExtraMailbox inserts a second active mailbox
// with the given local part. Returns the new mailbox id.
// The mailbox is in tenant 1, on the same domain as
// admin@orvix.email, so cross-mailbox / ownership tests
// can drive real ownership failures.
func (e *webmailTestEnv) provisionExtraMailbox(t *testing.T, localPart, email string) uint {
	t.Helper()
	sqlDB := e.mailbox.DB
	now := time.Now().UTC()
	if _, err := sqlDB.Exec(
		"INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		1, 1, localPart, email, localPart, "x", "bcrypt", "active", 1024, 0, now, now,
	); err != nil {
		t.Fatalf("insert mailbox %s: %v", email, err)
	}
	row := sqlDB.QueryRow("SELECT id FROM coremail_mailboxes WHERE email = ?", email)
	var id uint
	if err := row.Scan(&id); err != nil {
		t.Fatalf("scan id: %v", err)
	}
	return id
}

// injectForeignMessage stores a message in a mailbox
// other than the caller's. Returns the new message id.
func (e *webmailTestEnv) injectForeignMessage(t *testing.T, mailboxID uint, subject, body string) uint {
	t.Helper()
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	inbox, err := e.mailbox.Folders.GetByPath(t.Context(), mailboxID, "INBOX", nil)
	if err != nil || inbox == nil {
		t.Fatalf("foreign inbox: %v", err)
	}
	now := time.Now().UTC()
	rfc822 := fmt.Sprintf("From: f@x\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: <%s@test>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n",
		"stranger@orvix.email", subject, makeID(), body)
	m := &storage.Message{
		MessageID:         makeID(),
		InternetMessageID: "<foreign@test>",
		TenantID:          1,
		DomainID:          1,
		MailboxID:         mailboxID,
		FolderID:          inbox.ID,
		Subject:           subject,
		FromAddress:       "stranger@orvix.email",
		ToAddresses:       "stranger@orvix.email",
		ReceivedDate:      now,
	}
	if err := e.mailbox.StoreMessage(t.Context(), m, []byte(rfc822), nil); err != nil {
		t.Fatalf("store foreign: %v", err)
	}
	return m.ID
}

// ────────────────────────────────────────────────────────
// pagination / snippet / attachment_count
// ────────────────────────────────────────────────────────

func TestWebmailMessagesHonorLimit(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	tok := e.loginAdmin(t)

	for i := 0; i < 7; i++ {
		e.injectMessage(t, fmt.Sprintf("S%d", i), "body")
	}
	status, list := e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=INBOX&limit=3", tok, nil)
	if status != 200 {
		t.Fatalf("limit=3: %d %v", status, list)
	}
	msgs, _ := list["messages"].([]interface{})
	if len(msgs) != 3 {
		t.Errorf("limit=3: got %d messages, want 3", len(msgs))
	}
	if list["limit"] == nil || int(list["limit"].(float64)) != 3 {
		t.Errorf("limit=3: response.limit = %v, want 3", list["limit"])
	}
	if hasMore, _ := list["has_more"].(bool); !hasMore {
		t.Errorf("limit=3 of 7 total: has_more = false, want true")
	}
}

func TestWebmailMessagesHonorOffset(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	tok := e.loginAdmin(t)

	for i := 0; i < 6; i++ {
		e.injectMessage(t, fmt.Sprintf("P%d", i), "body")
	}
	status, p1 := e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=INBOX&limit=3&offset=0", tok, nil)
	if status != 200 {
		t.Fatalf("page1: %d", status)
	}
	status, p2 := e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=INBOX&limit=3&offset=3", tok, nil)
	if status != 200 {
		t.Fatalf("page2: %d", status)
	}
	m1, _ := p1["messages"].([]interface{})
	m2, _ := p2["messages"].([]interface{})
	if len(m1) != 3 || len(m2) != 3 {
		t.Fatalf("expected 3+3, got %d + %d", len(m1), len(m2))
	}
	seen := map[float64]bool{}
	for _, m := range m1 {
		seen[m.(map[string]interface{})["id"].(float64)] = true
	}
	for _, m := range m2 {
		id := m.(map[string]interface{})["id"].(float64)
		if seen[id] {
			t.Errorf("offset paging returned a duplicate id: %v", id)
		}
	}
}

func TestWebmailMessagesLimitClampedToMax(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	tok := e.loginAdmin(t)
	status, list := e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=INBOX&limit=10000", tok, nil)
	if status != 200 {
		t.Fatalf("huge limit: %d", status)
	}
	if got, _ := list["limit"].(float64); int(got) > 200 {
		t.Errorf("huge limit not clamped: response.limit = %v", got)
	}
}

func TestWebmailMessagesInvalidLimit(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	tok := e.loginAdmin(t)
	cases := []string{"abc", "0", "-1"}
	for _, c := range cases {
		path := "/api/v1/webmail/messages?folder=INBOX&limit=" + c
		status, _ := e.webmailRequest(t, "GET", path, tok, nil)
		if status != http.StatusBadRequest {
			t.Errorf("limit=%s: expected 400, got %d", c, status)
		}
	}
}

func TestWebmailMessagesSearchReturnsSnippet(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	tok := e.loginAdmin(t)
	marker := "SNIPPET_MARK_" + makeID()
	e.injectMessage(t, marker+" hello", "body of "+marker)

	status, list := e.webmailRequest(t, "GET",
		"/api/v1/webmail/messages?folder=INBOX&q="+marker, tok, nil)
	if status != 200 {
		t.Fatalf("search: %d", status)
	}
	msgs, _ := list["messages"].([]interface{})
	if len(msgs) != 1 {
		t.Fatalf("search: got %d, want 1", len(msgs))
	}
	m := msgs[0].(map[string]interface{})
	snip, _ := m["snippet"].(string)
	if !strings.Contains(snip, marker) {
		t.Errorf("search: snippet = %q, want contains %q", snip, marker)
	}
	if strings.Contains(snip, "<") {
		t.Errorf("search: snippet contains HTML-like char: %q", snip)
	}
}

// ────────────────────────────────────────────────────────
// message detail with attachments
// ────────────────────────────────────────────────────────

func TestWebmailMessageReturnsAttachments(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	tok := e.loginAdmin(t)
	id := e.injectMessage(t, "Attachment test", "body")

	attDir := filepath.Join(e.mailbox.BasePath, "attachments", fmt.Sprintf("%d", id))
	if err := os.MkdirAll(attDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	add := func(name, ct string) {
		p := filepath.Join(attDir, name)
		if err := os.WriteFile(p, []byte("x"), 0o640); err != nil {
			t.Fatalf("write: %v", err)
		}
		att := &storage.Attachment{
			MessageID: id, Filename: name, ContentType: ct,
			SizeBytes: 1, SHA256: "deadbeef", StoragePath: p,
		}
		if err := e.mailbox.Attachments.Create(t.Context(), att, nil); err != nil {
			t.Fatalf("create att: %v", err)
		}
	}
	add("first.txt", "text/plain")
	add("second.txt", "text/plain")

	status, msg := e.webmailRequest(t, "GET",
		fmt.Sprintf("/api/v1/webmail/messages/%d", id), tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages/%d: %d %v", id, status, msg)
	}
	atts, ok := msg["attachments"].([]interface{})
	if !ok {
		t.Fatalf("expected attachments array, got %T (%v)", msg["attachments"], msg["attachments"])
	}
	if len(atts) != 2 {
		t.Errorf("expected 2 attachments, got %d", len(atts))
	}
	first := atts[0].(map[string]interface{})
	if first["id"] == nil {
		t.Errorf("attachment row missing id: %v", first)
	}
	if first["filename"] == nil {
		t.Errorf("attachment row missing filename: %v", first)
	}
	if first["size_bytes"] == nil {
		t.Errorf("attachment row missing size_bytes: %v", first)
	}

	status, list := e.webmailRequest(t, "GET",
		"/api/v1/webmail/messages?folder=INBOX&limit=200", tok, nil)
	if status != 200 {
		t.Fatalf("list: %d", status)
	}
	for _, m := range list["messages"].([]interface{}) {
		mm := m.(map[string]interface{})
		if int(mm["id"].(float64)) == int(id) {
			if ac, _ := mm["attachment_count"].(float64); int(ac) != 2 {
				t.Errorf("attachment_count = %v, want 2", ac)
			}
		}
	}
}

// ────────────────────────────────────────────────────────
// source download
// ────────────────────────────────────────────────────────

func TestWebmailMessageSourceReturnsRFC822(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	tok := e.loginAdmin(t)
	marker := "SOURCE_MARK_" + makeID()
	id := e.injectMessage(t, "Source test", "body of "+marker)

	resp := e.appTest(t, "GET", fmt.Sprintf("/api/v1/webmail/messages/%d/source", id), tok, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("source: expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "message/rfc822") {
		t.Errorf("source: Content-Type = %q, want message/rfc822...", ct)
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment;") {
		t.Errorf("source: Content-Disposition = %q, want attachment;...", cd)
	}
	if !strings.Contains(cd, ".eml") {
		t.Errorf("source: Content-Disposition missing .eml: %q", cd)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), marker) {
		t.Errorf("source: body missing marker: %s", string(body))
	}
}

func TestWebmailMessageSourceOwnership(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	tok := e.loginAdmin(t)
	victimID := e.provisionExtraMailbox(t, "victim", "victim@orvix.email")
	victimMsgID := e.injectForeignMessage(t, victimID, "Victim subject", "secret body")

	resp := e.appTest(t, "GET",
		fmt.Sprintf("/api/v1/webmail/messages/%d/source", victimMsgID), tok, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("cross-mailbox source: expected 404, got %d", resp.StatusCode)
	}
}

// ────────────────────────────────────────────────────────
// move
// ────────────────────────────────────────────────────────

func TestWebmailMoveMessageHappyPath(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	tok := e.loginAdmin(t)
	id := e.injectMessage(t, "Move me", "body")

	archive, err := e.mailbox.Folders.GetByPath(t.Context(),
		mustMailboxIDForTest(t, e, e.email), "Archive", nil)
	if err != nil || archive == nil {
		t.Fatalf("get archive: %v %v", err, archive)
	}
	status, resp := e.webmailRequest(t, "POST",
		fmt.Sprintf("/api/v1/webmail/messages/%d/move", id), tok,
		map[string]uint{"target_folder_id": archive.ID})
	if status != 200 {
		t.Fatalf("move: %d %v", status, resp)
	}
	if resp["moved_to"] != "Archive" {
		t.Errorf("move: moved_to = %v, want Archive", resp["moved_to"])
	}

	// After move, message is not in INBOX but is in Archive.
	status, list := e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=INBOX&limit=200", tok, nil)
	if status != 200 {
		t.Fatalf("list inbox: %d", status)
	}
	for _, m := range list["messages"].([]interface{}) {
		if int(m.(map[string]interface{})["id"].(float64)) == int(id) {
			t.Errorf("after move: message %d still in INBOX", id)
		}
	}
	status, list = e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=Archive&limit=200", tok, nil)
	if status != 200 {
		t.Fatalf("list archive: %d", status)
	}
	found := false
	for _, m := range list["messages"].([]interface{}) {
		if int(m.(map[string]interface{})["id"].(float64)) == int(id) {
			found = true
		}
	}
	if !found {
		t.Errorf("after move: message %d not in Archive", id)
	}
}

func TestWebmailMoveMessageRejectsCrossMailboxTarget(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	tok := e.loginAdmin(t)
	id := e.injectMessage(t, "Move me 2", "body")

	otherID := e.provisionExtraMailbox(t, "other", "other@orvix.email")
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), otherID, nil); err != nil {
		t.Fatalf("ensure other folders: %v", err)
	}
	otherInbox, err := e.mailbox.Folders.GetByPath(t.Context(), otherID, "INBOX", nil)
	if err != nil || otherInbox == nil {
		t.Fatalf("get other inbox: %v", err)
	}
	status, _ := e.webmailRequest(t, "POST",
		fmt.Sprintf("/api/v1/webmail/messages/%d/move", id), tok,
		map[string]uint{"target_folder_id": otherInbox.ID})
	if status != http.StatusForbidden {
		t.Errorf("cross-mailbox move: expected 403, got %d", status)
	}
}

// ────────────────────────────────────────────────────────
// batch
// ────────────────────────────────────────────────────────

func TestWebmailBatchArchive(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	tok := e.loginAdmin(t)

	ids := []uint{
		e.injectMessage(t, "Batch1", "b1"),
		e.injectMessage(t, "Batch2", "b2"),
		e.injectMessage(t, "Batch3", "b3"),
	}
	status, resp := e.webmailRequest(t, "POST", "/api/v1/webmail/messages/batch", tok,
		map[string]interface{}{
			"ids":    ids,
			"action": "archive",
		})
	if status != 200 {
		t.Fatalf("batch archive: %d %v", status, resp)
	}
	if resp["action"] != "archive" {
		t.Errorf("batch: action = %v, want archive", resp["action"])
	}
	if int(resp["total"].(float64)) != 3 {
		t.Errorf("batch: total = %v, want 3", resp["total"])
	}
	if int(resp["succeeded"].(float64)) != 3 {
		t.Errorf("batch: succeeded = %v, want 3", resp["succeeded"])
	}
	if int(resp["failed"].(float64)) != 0 {
		t.Errorf("batch: failed = %v, want 0", resp["failed"])
	}

	status, list := e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=Archive&limit=200", tok, nil)
	if status != 200 {
		t.Fatalf("list archive: %d", status)
	}
	gotIDs := map[float64]bool{}
	for _, m := range list["messages"].([]interface{}) {
		gotIDs[m.(map[string]interface{})["id"].(float64)] = true
	}
	for _, id := range ids {
		if !gotIDs[float64(id)] {
			t.Errorf("after batch archive: %d not in Archive", id)
		}
	}
}

func TestWebmailBatchPartialFailure(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	tok := e.loginAdmin(t)

	owned := []uint{
		e.injectMessage(t, "Own1", "b1"),
		e.injectMessage(t, "Own2", "b2"),
	}
	strangerID := e.provisionExtraMailbox(t, "stranger", "stranger@orvix.email")
	foreign := e.injectForeignMessage(t, strangerID, "Foreign", "fbody")

	req := map[string]interface{}{
		"ids":    []uint{owned[0], foreign, owned[1], 999999},
		"action": "markRead",
	}
	status, resp := e.webmailRequest(t, "POST", "/api/v1/webmail/messages/batch", tok, req)
	if status != 200 {
		t.Fatalf("batch: %d %v", status, resp)
	}
	if int(resp["total"].(float64)) != 4 {
		t.Errorf("batch: total = %v, want 4", resp["total"])
	}
	if int(resp["succeeded"].(float64)) != 2 {
		t.Errorf("batch: succeeded = %v, want 2", resp["succeeded"])
	}
	if int(resp["failed"].(float64)) != 2 {
		t.Errorf("batch: failed = %v, want 2", resp["failed"])
	}
	errs, _ := resp["errors"].([]interface{})
	if len(errs) != 2 {
		t.Fatalf("batch: errors array len = %d, want 2", len(errs))
	}
}

func TestWebmailBatchInvalidAction(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	tok := e.loginAdmin(t)
	status, _ := e.webmailRequest(t, "POST", "/api/v1/webmail/messages/batch", tok,
		map[string]interface{}{"ids": []uint{1, 2}, "action": "selfDestruct"})
	if status != http.StatusBadRequest {
		t.Errorf("invalid action: expected 400, got %d", status)
	}
}

// ────────────────────────────────────────────────────────
// attachment download / preview
// ────────────────────────────────────────────────────────

// addAttachment writes an attachment file for the given
// message id and inserts a row in coremail_attachments.
func (e *webmailTestEnv) addAttachment(t *testing.T, messageID uint, filename, contentType string, payload []byte) uint {
	t.Helper()
	attDir := filepath.Join(e.mailbox.BasePath, "attachments", fmt.Sprintf("%d", messageID))
	if err := os.MkdirAll(attDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	storagePath := filepath.Join(attDir, filename)
	if err := os.WriteFile(storagePath, payload, 0o640); err != nil {
		t.Fatalf("write: %v", err)
	}
	att := &storage.Attachment{
		MessageID:   messageID,
		Filename:    filename,
		ContentType: contentType,
		SizeBytes:   int64(len(payload)),
		SHA256:      "deadbeef",
		StoragePath: storagePath,
	}
	if err := e.mailbox.Attachments.Create(t.Context(), att, nil); err != nil {
		t.Fatalf("create att: %v", err)
	}
	return att.ID
}

func TestWebmailAttachmentDownloadOwnership(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	tok := e.loginAdmin(t)
	id := e.injectMessage(t, "Att owner", "body")
	attID := e.addAttachment(t, id, "test.txt", "text/plain", []byte("hello world"))

	// Happy path: caller downloads their own attachment.
	resp := e.appTest(t, "GET", fmt.Sprintf("/api/v1/webmail/attachments/%d", attID), tok, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("download own: expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Errorf("download: Content-Disposition = %q, want attachment;...", cd)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello world" {
		t.Errorf("download: body = %q, want %q", body, "hello world")
	}

	// Non-digit id is rejected.
	resp = e.appTest(t, "GET", "/api/v1/webmail/attachments/abc", tok, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("non-digit id: expected 400, got %d", resp.StatusCode)
	}

	// Unauthenticated is 401.
	resp = e.appTest(t, "GET", fmt.Sprintf("/api/v1/webmail/attachments/%d", attID), "", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauth: expected 401, got %d", resp.StatusCode)
	}
}

func TestWebmailAttachmentDownloadCrossMailboxForbidden(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	tok := e.loginAdmin(t)
	victimID := e.provisionExtraMailbox(t, "victim2", "victim2@orvix.email")
	vid := e.injectForeignMessage(t, victimID, "Secret", "x")
	attID := e.addAttachment(t, vid, "secret.txt", "text/plain", []byte("classified"))

	resp := e.appTest(t, "GET", fmt.Sprintf("/api/v1/webmail/attachments/%d", attID), tok, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("cross-mailbox download: expected 404, got %d", resp.StatusCode)
	}
}

func TestWebmailAttachmentPreviewRefusesSvg(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	tok := e.loginAdmin(t)
	id := e.injectMessage(t, "SVG test", "body")
	attID := e.addAttachment(t, id, "evil.svg", "image/svg+xml", []byte("<svg></svg>"))

	resp := e.appTest(t, "GET", fmt.Sprintf("/api/v1/webmail/attachments/%d/preview", attID), tok, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("SVG preview: expected 415, got %d", resp.StatusCode)
	}
}

func TestWebmailAttachmentPreviewAllowsPng(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	tok := e.loginAdmin(t)
	id := e.injectMessage(t, "PNG test", "body")
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	attID := e.addAttachment(t, id, "pic.png", "image/png", png)

	resp := e.appTest(t, "GET", fmt.Sprintf("/api/v1/webmail/attachments/%d/preview", attID), tok, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("PNG preview: expected 200, got %d", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "inline;") {
		t.Errorf("PNG preview: Content-Disposition = %q, want inline;...", cd)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) != len(png) {
		t.Errorf("PNG preview: body len = %d, want %d", len(body), len(png))
	}
}

func TestWebmailAttachmentPreviewRefusesHuge(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure folders: %v", err)
	}
	tok := e.loginAdmin(t)
	id := e.injectMessage(t, "Big image", "body")
	// 1.5 MB PNG — over the preview cap.
	huge := make([]byte, 1_500_000)
	attID := e.addAttachment(t, id, "big.png", "image/png", huge)

	resp := e.appTest(t, "GET", fmt.Sprintf("/api/v1/webmail/attachments/%d/preview", attID), tok, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("huge preview: expected 413, got %d", resp.StatusCode)
	}
}
