package handlers_test

// API-boundary pin for attachment metadata: the webmail message detail
// response exposes only the public attachment fields (id, filename,
// content_type, size_bytes) — never filesystem storage paths, content
// hashes, or other internal metadata.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestWebmailSendAttachmentResponseHidesInternalMetadata proves that
// after a successful send with an attachment, the message detail
// response's attachment objects expose exactly the four public fields
// and never leak storage_path (filesystem paths) or other internal
// metadata such as sha256/cid/created_at.
func TestWebmailSendAttachmentResponseHidesInternalMetadata(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	marker := makeID()
	fields := map[string]string{
		"to":      "recipient@example.com",
		"subject": "Metadata shape " + marker,
		"body":    "Check response shape.",
	}
	files := []struct {
		FieldName string
		Filename  string
		Body      []byte
	}{
		{FieldName: "attachment", Filename: "secret.txt", Body: []byte("content " + marker)},
	}

	status, resp := multipartSend(t, e, tok, fields, files)
	if status != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", status, resp)
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
	if len(atts) == 0 {
		t.Fatalf("expected at least one attachment in response")
	}
	for _, a := range atts {
		am, _ := a.(map[string]interface{})
		if _, ok := am["storage_path"]; ok {
			t.Errorf("attachment response leaks storage_path: %v", am)
		}
		if _, ok := am["sha256"]; ok {
			t.Errorf("attachment response leaks sha256: %v", am)
		}
		if _, ok := am["cid"]; ok {
			t.Errorf("attachment response leaks cid: %v", am)
		}
		if _, ok := am["created_at"]; ok {
			t.Errorf("attachment response leaks created_at: %v", am)
		}
		for _, required := range []string{"id", "filename", "content_type", "size_bytes"} {
			if _, ok := am[required]; !ok {
				t.Errorf("attachment response missing public field %q: %v", required, am)
			}
		}
	}

	// The raw body must not contain any storage path marker either.
	raw := fmt.Sprintf("%v", msgResp)
	if strings.Contains(raw, "attachments\\") || strings.Contains(raw, "attachments/") {
		t.Errorf("message response body leaks filesystem path material: %s", raw)
	}
}
