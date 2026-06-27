package handlers_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// multipartSend builds a multipart/form-data body with the given
// form fields and file attachments, then sends it to the Fiber test
// harness. Returns status + parsed JSON body.
func multipartSend(t *testing.T, e *webmailTestEnv, accessToken string, fields map[string]string, files []struct {
	FieldName string
	Filename  string
	Body      []byte
}) (int, map[string]interface{}) {
	t.Helper()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}

	for _, f := range files {
		part, err := mw.CreateFormFile(f.FieldName, f.Filename)
		if err != nil {
			t.Fatalf("create form file %s: %v", f.Filename, err)
		}
		if _, err := part.Write(f.Body); err != nil {
			t.Fatalf("write file %s: %v", f.Filename, err)
		}
	}
	mw.Close()

	req := httptest.NewRequest("POST", "/api/v1/webmail/send", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if accessToken != "" {
		req.Header.Set("Cookie", "access_token="+accessToken)
	}
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("POST /send multipart: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	out := map[string]interface{}{}
	if len(respBody) > 0 {
		json.Unmarshal(respBody, &out)
	}
	return resp.StatusCode, out
}

// ────────────────────────────────────────────────────────────
// Send with attachments tests
// ────────────────────────────────────────────────────────────

func TestWebmailSendWithOneAttachment(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	marker := makeID()
	fields := map[string]string{
		"to":      "recipient@example.com",
		"subject": "Attach test " + marker,
		"body":    "See attached file.",
	}
	files := []struct {
		FieldName string
		Filename  string
		Body      []byte
	}{
		{FieldName: "attachment", Filename: "report.pdf", Body: []byte("%PDF-1.4 fake pdf content " + marker)},
	}

	status, resp := multipartSend(t, e, tok, fields, files)
	if status != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", status, resp)
	}
	if resp["status"] != "queued" {
		t.Errorf("status = %v, want queued", resp["status"])
	}
	id, ok := resp["id"].(float64)
	if !ok || id == 0 {
		t.Fatalf("response id invalid: %v", resp["id"])
	}

	// Verify Sent folder has the message.
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
			if ac, ok := mm["attachment_count"].(float64); !ok || ac == 0 {
				t.Errorf("sent message attachment_count = %v, want > 0", ac)
			}
		}
	}
	if !found {
		t.Errorf("sent message id=%v not found in Sent folder", id)
	}

	// Verify stored message has attachment metadata.
	status, msgResp := e.webmailRequest(t, "GET", fmt.Sprintf("/api/v1/webmail/messages/%d", int(id)), tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages/%d: expected 200, got %d", int(id), status)
	}
	atts, _ := msgResp["attachments"].([]interface{})
	if len(atts) == 0 {
		t.Errorf("sent message has no attachments in metadata")
	}
	rfc822, _ := msgResp["rfc822"].(string)
	if !strings.Contains(rfc822, marker) {
		t.Errorf("stored RFC822 missing marker")
	}
	if !strings.Contains(rfc822, "report.pdf") {
		t.Errorf("stored RFC822 missing attachment filename")
	}
	if !strings.Contains(rfc822, "Content-Type:") {
		t.Errorf("stored RFC822 missing Content-Type for attachment")
	}
}

func TestWebmailSendWithMultipleAttachments(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	marker := makeID()
	fields := map[string]string{
		"to":      "recipient@example.com",
		"subject": "Multi attach " + marker,
		"body":    "Multiple files.",
	}
	files := []struct {
		FieldName string
		Filename  string
		Body      []byte
	}{
		{FieldName: "attachment", Filename: "doc1.pdf", Body: []byte("pdf one " + marker)},
		{FieldName: "attachment", Filename: "doc2.txt", Body: []byte("text two " + marker)},
		{FieldName: "attachment", Filename: "image.png", Body: []byte("png three " + marker)},
	}

	status, resp := multipartSend(t, e, tok, fields, files)
	if status != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", status, resp)
	}
	if resp["status"] != "queued" {
		t.Errorf("status = %v, want queued", resp["status"])
	}
	id, ok := resp["id"].(float64)
	if !ok || id == 0 {
		t.Fatalf("response id invalid: %v", resp["id"])
	}

	status, msgResp := e.webmailRequest(t, "GET", fmt.Sprintf("/api/v1/webmail/messages/%d", int(id)), tok, nil)
	if status != 200 {
		t.Fatalf("GET /messages/%d: expected 200, got %d", int(id), status)
	}
	atts, _ := msgResp["attachments"].([]interface{})
	if len(atts) < 3 {
		t.Errorf("expected >=3 attachments, got %d", len(atts))
	}
	rfc822, _ := msgResp["rfc822"].(string)
	for _, f := range files {
		if !strings.Contains(rfc822, f.Filename) {
			t.Errorf("stored RFC822 missing filename %q", f.Filename)
		}
	}
}

func TestWebmailSendRejectsOversizedAttachment(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	oversized := make([]byte, 26*1024*1024+1)
	for i := range oversized {
		oversized[i] = byte(i % 256)
	}

	fields := map[string]string{
		"to":      "recipient@example.com",
		"subject": "Oversized attachment",
		"body":    "This should be rejected.",
	}
	files := []struct {
		FieldName string
		Filename  string
		Body      []byte
	}{
		{FieldName: "attachment", Filename: "huge.bin", Body: oversized},
	}

	status, resp := multipartSend(t, e, tok, fields, files)
	if status != http.StatusBadRequest {
		t.Errorf("expected 400 for oversized attachment, got %d: %v", status, resp)
	}
	errMsg, _ := resp["error"].(string)
	if !strings.Contains(errMsg, "exceeds max size") {
		t.Errorf("error message must mention size limit, got %q", errMsg)
	}
}

func TestWebmailSendSanitizesDangerousFilename(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	tests := []struct {
		name     string
		filename string
	}{
		{"path traversal unix", "../../etc/passwd"},
		{"path traversal windows", "..\\..\\windows\\system32\\config"},
		{"absolute path", "/var/tmp/evil.sh"},
		{"drive letter", "C:\\autoexec.bat"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := map[string]string{
				"to":      "recipient@example.com",
				"subject": "Bad filename " + tt.name,
				"body":    "test",
			}
			files := []struct {
				FieldName string
				Filename  string
				Body      []byte
			}{
				{FieldName: "attachment", Filename: tt.filename, Body: []byte("evil")},
			}
			status, resp := multipartSend(t, e, tok, fields, files)
			if status != http.StatusCreated {
				t.Errorf("expected 201 for sanitized filename %q, got %d: %v", tt.filename, status, resp)
			}
		})
	}
}

func TestWebmailSendPreservesSentCopyWithAttachments(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	marker := makeID()
	fields := map[string]string{
		"to":      "recipient@example.com",
		"subject": "Sent copy attachment " + marker,
		"body":    "Body content " + marker,
	}
	files := []struct {
		FieldName string
		Filename  string
		Body      []byte
	}{
		{FieldName: "attachment", Filename: "doc.pdf", Body: []byte("pdf " + marker)},
		{FieldName: "attachment", Filename: "notes.txt", Body: []byte("text " + marker)},
	}

	status, resp := multipartSend(t, e, tok, fields, files)
	if status != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", status, resp)
	}
	id, _ := resp["id"].(float64)
	if id == 0 {
		t.Fatalf("response id missing")
	}

	status, msgResp := e.webmailRequest(t, "GET", fmt.Sprintf("/api/v1/webmail/messages/%d", int(id)), tok, nil)
	if status != 200 {
		t.Fatalf("GET sent message: expected 200, got %d", status)
	}
	atts, _ := msgResp["attachments"].([]interface{})
	if len(atts) == 0 {
		t.Errorf("sent message has no attachments in JSON response")
	}
	for _, a := range atts {
		am, _ := a.(map[string]interface{})
		fn, _ := am["filename"].(string)
		sz, _ := am["size_bytes"].(float64)
		if fn == "" {
			t.Errorf("attachment missing filename")
		}
		if sz <= 0 {
			t.Errorf("attachment %q has invalid size_bytes=%v", fn, sz)
		}
	}

	// Confirm the message appears in Sent folder listing.
	status, listResp := e.webmailRequest(t, "GET", "/api/v1/webmail/messages?folder=Sent", tok, nil)
	if status != 200 {
		t.Fatalf("GET Sent folder: expected 200, got %d", status)
	}
	messages, _ := listResp["messages"].([]interface{})
	found := false
	for _, m := range messages {
		mm, _ := m.(map[string]interface{})
		if int(mm["id"].(float64)) == int(id) {
			found = true
		}
	}
	if !found {
		t.Errorf("sent message not found in Sent folder listing")
	}
}

func TestWebmailSendAttachmentNoAuth(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}

	fields := map[string]string{
		"to":      "recipient@example.com",
		"subject": "No auth attachment",
		"body":    "Should be rejected.",
	}
	files := []struct {
		FieldName string
		Filename  string
		Body      []byte
	}{
		{FieldName: "attachment", Filename: "test.txt", Body: []byte("content")},
	}
	status, _ := multipartSend(t, e, "", fields, files)
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Errorf("expected 401 or 403 for unauthenticated attachment send, got %d", status)
	}
}
