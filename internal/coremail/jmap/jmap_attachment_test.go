package jmap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/orvix/orvix/internal/coremail/storage"
)

// ── Download Tests ────────────────────────────────────────

func TestDownloadExtractedAttachment(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()
	ctx := context.Background()

	storeTestMsgWithAttach(ctx, t, ms, 1)

	atts, err := ms.Attachments.ListByMessage(ctx, 1, nil)
	if err != nil || len(atts) == 0 {
		t.Fatal("expected at least one extracted attachment")
	}
	attID := atts[0].ID

	downloadURL := fmt.Sprintf("http://%s/jmap/download/1/%d/test.pdf", addr, attID)
	req, _ := http.NewRequest("GET", downloadURL, nil)
	req.SetBasicAuth("user@test.com", "pass")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("download request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "%PDF") {
		t.Fatal("expected PDF content in body")
	}
}

func TestDownloadUnauthorizedBlocked(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()
	ctx := context.Background()

	storeTestMsgWithAttach(ctx, t, ms, 1)
	atts, _ := ms.Attachments.ListByMessage(ctx, 1, nil)
	if len(atts) == 0 {
		t.Fatal("expected attachment")
	}

	downloadURL := fmt.Sprintf("http://%s/jmap/download/1/%d/test.pdf", addr, atts[0].ID)
	req, _ := http.NewRequest("GET", downloadURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("download request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestDownloadPathTraversalBlocked(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()
	ctx := context.Background()

	storeTestMsgWithAttach(ctx, t, ms, 1)

	downloadURL := fmt.Sprintf("http://%s/jmap/download/1/abc/../etc/passwd", addr)
	req, _ := http.NewRequest("GET", downloadURL, nil)
	req.SetBasicAuth("user@test.com", "pass")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("download request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 && resp.StatusCode != 404 {
		t.Fatalf("expected 400 or 404 for path traversal, got %d", resp.StatusCode)
	}
}

// ── Upload + Create Tests ─────────────────────────────────

func TestUploadAndCreateEmailWithAttachment(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	// Upload a file.
	uploadURL := fmt.Sprintf("http://%s/jmap/upload/1", addr)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", "hello.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	fw.Write([]byte("Hello, attachment world!"))
	w.Close()

	req, _ := http.NewRequest("POST", uploadURL, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.SetBasicAuth("user@test.com", "pass")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 from upload, got %d", resp.StatusCode)
	}

	uploadResp := struct {
		BlobID string `json:"blobId"`
		Size   int    `json:"size"`
		Name   string `json:"name"`
	}{}
	body, _ := io.ReadAll(resp.Body)
	json.Unmarshal(body, &uploadResp)
	if uploadResp.BlobID == "" {
		t.Fatal("expected non-empty blobId")
	}

	// Create email with attachment via Email/set.
	createParams := map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"mailboxIds": map[string]bool{"1": true},
				"subject":    "With uploaded attachment",
				"from":       map[string]string{"email": "user@test.com"},
				"to":         []map[string]string{{"email": "rcpt@test.com"}},
				"body":       "See attached",
				"attachments": []map[string]interface{}{
					{"blobId": uploadResp.BlobID, "filename": "hello.txt", "contentType": "text/plain"},
				},
			},
		},
	}
	method, rawBody := emailSetRaw(t, addr, createParams)
	if method == "error" {
		t.Fatalf("Email/set create failed: %s", rawBody)
	}

	// Parse the Email/set params from the full JMAP response.
	type methodResp struct {
		Name   string          `json:"name"`
		Params json.RawMessage `json:"params"`
	}
	var wrapper struct {
		MethodResponses []methodResp `json:"methodResponses"`
	}
	json.Unmarshal([]byte(rawBody), &wrapper)
	var setParams struct {
		Created    map[string]string `json:"created"`
		NotCreated map[string]string `json:"notCreated"`
	}
	for _, mr := range wrapper.MethodResponses {
		if mr.Name == "Email/set" {
			json.Unmarshal(mr.Params, &setParams)
			break
		}
	}
	if setParams.Created == nil {
		if setParams.NotCreated != nil {
			t.Fatalf("Email not created: %v", setParams.NotCreated)
		}
		t.Fatal("expected created map in Email/set response")
	}

	// Verify Email/get shows attachments.
	getParams := map[string]interface{}{"accountId": "1", "ids": []string{"1"}}
	_, getBody := jmapAPI(t, addr, "user@test.com", "pass", map[string]interface{}{
		"using":       []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{[]interface{}{"Email/get", getParams, "c1"}},
	})
	var jmapResp struct {
		MethodResponses []struct {
			Name   string          `json:"name"`
			Params json.RawMessage `json:"params"`
		} `json:"methodResponses"`
	}
	json.Unmarshal([]byte(getBody), &jmapResp)
	var emailGetResp EmailGetResponse
	for _, mr := range jmapResp.MethodResponses {
		if mr.Name == "Email/get" {
			json.Unmarshal(mr.Params, &emailGetResp)
			break
		}
	}
	if len(emailGetResp.List) == 0 {
		t.Fatal("expected email in get response")
	}
	email := emailGetResp.List[0]
	if !email.HasAttachment {
		t.Fatal("expected hasAttachment true")
	}
	if len(email.Attachments) == 0 {
		t.Fatal("expected attachment entries")
	}
	if email.Attachments[0].Filename != "hello.txt" {
		t.Fatalf("expected hello.txt, got %s", email.Attachments[0].Filename)
	}

	// Verify attachment was stored by MailStore (extracted from RFC822).
	ctx := context.Background()
	savedAtts, _ := ms.Attachments.ListByMessage(ctx, 1, nil)
	if len(savedAtts) == 0 {
		t.Fatal("expected extracted attachment in MailStore")
	}
}

func TestUploadSizeLimit(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	uploadURL := fmt.Sprintf("http://%s/jmap/upload/1", addr)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "large.bin")
	largeData := make([]byte, 30*1024*1024)
	fw.Write(largeData)
	w.Close()

	req, _ := http.NewRequest("POST", uploadURL, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.SetBasicAuth("user@test.com", "pass")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", resp.StatusCode)
	}
}

func TestUploadUnauthorized(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	uploadURL := fmt.Sprintf("http://%s/jmap/upload/1", addr)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "test.txt")
	fw.Write([]byte("data"))
	w.Close()

	req, _ := http.NewRequest("POST", uploadURL, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// ── Helpers ───────────────────────────────────────────────

func storeTestMsgWithAttach(ctx context.Context, t *testing.T, ms *storage.MailStore, mailboxID uint) {
	t.Helper()
	inbox, err := ms.Folders.GetByPath(ctx, mailboxID, "INBOX", nil)
	if err != nil || inbox == nil {
		t.Fatalf("get inbox: %v", err)
	}

	boundary := "==boundary=="
	rfc822 := []byte("From: user@test.com\r\nSubject: With Attach\r\nContent-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n" +
		"--" + boundary + "\r\nContent-Type: text/plain\r\n\r\nBody\r\n" +
		"--" + boundary + "\r\nContent-Disposition: attachment; filename=\"test.pdf\"\r\nContent-Type: application/pdf\r\n\r\n%PDF-1.4 fake file\r\n" +
		"--" + boundary + "--\r\n")

	msg := &storage.Message{
		MessageID:    storage.GenerateMessageID(),
		MailboxID:    mailboxID,
		FolderID:     inbox.ID,
		FromAddress:  "user@test.com",
		ToAddresses:  "rcpt@test.com",
		Subject:      "With Attach",
		TenantID:     1,
		DomainID:     1,
	}

	if err := ms.StoreMessage(ctx, msg, rfc822, nil); err != nil {
		t.Fatalf("store message: %v", err)
	}
}
