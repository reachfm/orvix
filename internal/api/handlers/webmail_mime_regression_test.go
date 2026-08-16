package handlers_test

// Regression coverage for the webmail reading-pane raw-MIME bug: the
// released webmail.js reading pane was rendering msg.rfc822 (the raw
// RFC822/MIME wire format) directly as the visible message body
// instead of the server's already-parsed text_body/html_body fields.
// For a realistic external multipart message — the shape Outlook and
// Mimecast produce — that meant the user saw MIME boundaries,
// Content-Type/Content-Transfer-Encoding headers, and base64
// attachment payloads instead of the actual email.
//
// This file is backend-only: it proves the API contract the fix
// relies on (text_body/html_body/has_html/has_remote_images/
// attachments) is correct for a synthetic Outlook/Mimecast-style
// fixture. The frontend rendering behavior itself (webmail.js no
// longer using msg.rfc822 for display) is covered by the Node
// regression script at release/scripts/tests/test-webmail-reading-pane.mjs.
//
// The fixture below is entirely synthetic — no real customer email
// content is used or referenced anywhere in this repository.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// buildSyntheticOutlookMimecastFixture returns a realistic
// multipart/mixed RFC822 message matching the shape of a real
// external Outlook/Mimecast message: multipart/alternative
// (text/plain + text/html, both quoted-printable) plus a PDF
// attachment (base64), a remote tracking-pixel <img> in the HTML
// part, and a plausible Message-ID.
func buildSyntheticOutlookMimecastFixture() []byte {
	const outerBoundary = "OUTLOOK_OUTER_BOUNDARY_7f3a"
	const altBoundary = "OUTLOOK_ALT_BOUNDARY_9c21"

	plainQP := "Hi Salma,=0D=0A=0D=0A" +
		"Please find attached our Q3 report as requested.=0D=0A=0D=0A" +
		"Kind regards,=0D=0AJohn Smith=0D=0AAcme Corp"

	htmlQP := "<html><body><p>Hi Salma,</p>" +
		"<p>Please find attached our Q3 report as requested.</p>" +
		"<p>Kind regards,<br>John Smith<br>Acme Corp</p>" +
		"<img src=3D\"http://tracker.example.com/open.gif\" width=3D\"1\" height=3D\"1\">" +
		"</body></html>"

	// Synthetic non-PDF-format bytes standing in for an attachment —
	// deliberately NOT a real/parseable PDF, just arbitrary payload
	// bytes to prove the attachment round-trips as an attachment
	// (never inline in the body) regardless of its content.
	pdfBytes := []byte("%SYNTHETIC-PDF-PAYLOAD-NOT-A-REAL-PDF-0123456789")
	pdfB64 := syntheticBase64Encode(pdfBytes)

	var b strings.Builder
	b.WriteString("From: John Smith <john.smith@acme-example.com>\r\n")
	b.WriteString("To: salma@orvix.email\r\n")
	b.WriteString("Subject: Q3 Report\r\n")
	b.WriteString("Date: Mon, 17 Aug 2026 10:00:00 +0000\r\n")
	b.WriteString("Message-ID: <a1b2c3d4-outlook-mimecast-fixture@acme-example.com>\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString(fmt.Sprintf("Content-Type: multipart/mixed; boundary=\"%s\"\r\n", outerBoundary))
	b.WriteString("\r\n")

	b.WriteString("--" + outerBoundary + "\r\n")
	b.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"\r\n", altBoundary))
	b.WriteString("\r\n")

	b.WriteString("--" + altBoundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	b.WriteString("\r\n")
	b.WriteString(plainQP + "\r\n")

	b.WriteString("--" + altBoundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	b.WriteString("\r\n")
	b.WriteString(htmlQP + "\r\n")
	b.WriteString("--" + altBoundary + "--\r\n")

	b.WriteString("--" + outerBoundary + "\r\n")
	b.WriteString("Content-Type: application/pdf; name=\"Q3-Report.pdf\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	b.WriteString("Content-Disposition: attachment; filename=\"Q3-Report.pdf\"\r\n")
	b.WriteString("\r\n")
	// Base64 lines wrapped at 76 chars, as real MTAs produce.
	for i := 0; i < len(pdfB64); i += 76 {
		end := i + 76
		if end > len(pdfB64) {
			end = len(pdfB64)
		}
		b.WriteString(pdfB64[i:end] + "\r\n")
	}
	b.WriteString("--" + outerBoundary + "--\r\n")

	return []byte(b.String())
}

func syntheticBase64Encode(data []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var out strings.Builder
	for i := 0; i < len(data); i += 3 {
		var chunk [3]byte
		n := copy(chunk[:], data[i:])
		out.WriteByte(alphabet[chunk[0]>>2])
		out.WriteByte(alphabet[((chunk[0]&0x03)<<4)|(chunk[1]>>4)])
		if n > 1 {
			out.WriteByte(alphabet[((chunk[1]&0x0f)<<2)|(chunk[2]>>6)])
		} else {
			out.WriteByte('=')
		}
		if n > 2 {
			out.WriteByte(alphabet[chunk[2]&0x3f])
		} else {
			out.WriteByte('=')
		}
	}
	return out.String()
}

// TestWebmailAPIMessage_OutlookMimecastFixture_ExposesParsedBodies
// pins A.5 items 1/2/6/7/8/9: the API response for a realistic
// external multipart Outlook/Mimecast-style message exposes a short
// human-readable text_body, a sanitized html_body, has_html=true,
// has_remote_images=true (the tracking pixel), and the PDF as a
// separate attachment entry — never inline in either body field.
func TestWebmailAPIMessage_OutlookMimecastFixture_ExposesParsedBodies(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	fixture := buildSyntheticOutlookMimecastFixture()
	id := e.injectRawMessage(t, "Q3 Report", fixture)

	status, resp := e.webmailRequest(t, "GET", fmt.Sprintf("/api/v1/webmail/messages/%d", id), tok, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /messages/%d: expected 200, got %d: %v", id, status, resp)
	}

	textBody, _ := resp["text_body"].(string)
	htmlBody, _ := resp["html_body"].(string)
	hasHTML, _ := resp["has_html"].(bool)
	hasRemoteImages, _ := resp["has_remote_images"].(bool)
	attachments, _ := resp["attachments"].([]interface{})

	// 1/6: short human-readable body, not the multi-KB raw source.
	if !strings.Contains(textBody, "Please find attached our Q3 report") {
		t.Errorf("text_body missing expected human-readable content: %q", textBody)
	}
	if len(textBody) > 2000 {
		t.Errorf("text_body implausibly large (%d bytes) — looks like raw MIME leaked in", len(textBody))
	}

	// 2: html_body present and sanitized (no script/on* — this is a
	// coarse smoke check; internal/coremail/mime has its own
	// dedicated sanitizer tests).
	if !hasHTML {
		t.Fatal("has_html = false, want true (fixture has a text/html alternative)")
	}
	if !strings.Contains(htmlBody, "Please find attached") {
		t.Errorf("html_body missing expected content: %q", htmlBody)
	}
	if strings.Contains(htmlBody, "<script") {
		t.Errorf("html_body was not sanitized: contains <script>: %q", htmlBody)
	}

	// 9: remote image protection — the fixture's tracking pixel must
	// be flagged.
	if !hasRemoteImages {
		t.Error("has_remote_images = false, want true (fixture has an http:// <img> pixel)")
	}

	// 8: neither body field may contain raw MIME plumbing.
	for _, forbidden := range []string{
		"Content-Type:", "Content-Transfer-Encoding:",
		"OUTLOOK_OUTER_BOUNDARY", "OUTLOOK_ALT_BOUNDARY",
		"%SYNTHETIC-PDF-PAYLOAD", // the raw/base64 attachment bytes must never leak into either body
	} {
		if strings.Contains(textBody, forbidden) {
			t.Errorf("text_body leaked raw MIME artifact %q:\n%s", forbidden, textBody)
		}
		if strings.Contains(htmlBody, forbidden) {
			t.Errorf("html_body leaked raw MIME artifact %q:\n%s", forbidden, htmlBody)
		}
	}

	// 5: attachment present as separate structured metadata, not
	// inlined in the body.
	if len(attachments) != 1 {
		t.Fatalf("attachments: got %d, want 1", len(attachments))
	}
	att, _ := attachments[0].(map[string]interface{})
	if att["filename"] != "Q3-Report.pdf" {
		t.Errorf("attachment filename = %v, want Q3-Report.pdf", att["filename"])
	}
	if ct, _ := att["content_type"].(string); ct != "application/pdf" {
		t.Errorf("attachment content_type = %q, want application/pdf", ct)
	}
}

// TestWebmailAPIMessage_TextOnlyMessage_StillWorks pins A.5 item 6:
// a plain text/plain-only message (no multipart, no HTML) still
// round-trips correctly — has_html must be false and text_body must
// carry the content.
func TestWebmailAPIMessage_TextOnlyMessage_StillWorks(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	id := e.injectMessage(t, "Plain text only", "Just a plain text message, no HTML at all.")

	status, resp := e.webmailRequest(t, "GET", fmt.Sprintf("/api/v1/webmail/messages/%d", id), tok, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /messages/%d: expected 200, got %d: %v", id, status, resp)
	}
	if hasHTML, _ := resp["has_html"].(bool); hasHTML {
		t.Error("has_html = true for a plain-text-only message, want false")
	}
	textBody, _ := resp["text_body"].(string)
	if !strings.Contains(textBody, "Just a plain text message") {
		t.Errorf("text_body missing expected content: %q", textBody)
	}
}

// TestWebmailAPIMessage_HTMLOnlyMessage_StillWorks pins A.5 item 7:
// an HTML-only message (no text/plain alternative) still exposes a
// usable html_body with has_html=true.
func TestWebmailAPIMessage_HTMLOnlyMessage_StillWorks(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	var b strings.Builder
	b.WriteString("From: sender@example.com\r\n")
	b.WriteString("To: " + e.email + "\r\n")
	b.WriteString("Subject: HTML only\r\n")
	b.WriteString("Date: Mon, 17 Aug 2026 10:00:00 +0000\r\n")
	b.WriteString("Message-ID: <html-only-fixture@example.com>\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString("<html><body><p>An HTML-only message body.</p></body></html>\r\n")

	id := e.injectRawMessage(t, "HTML only", []byte(b.String()))

	status, resp := e.webmailRequest(t, "GET", fmt.Sprintf("/api/v1/webmail/messages/%d", id), tok, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /messages/%d: expected 200, got %d: %v", id, status, resp)
	}
	if hasHTML, _ := resp["has_html"].(bool); !hasHTML {
		t.Error("has_html = false for an HTML-only message, want true")
	}
	htmlBody, _ := resp["html_body"].(string)
	if !strings.Contains(htmlBody, "An HTML-only message body") {
		t.Errorf("html_body missing expected content: %q", htmlBody)
	}
}
