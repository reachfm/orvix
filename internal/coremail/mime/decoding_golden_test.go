package mime

// Golden regression tests for the "raw MIME in the reading pane"
// live bug: quoted-printable ("=3D"-style) and base64 part bodies
// were never decoded, and no charset transcoding was ever applied —
// decodeQuotedPrintable existed but was dead code, never called from
// ExtractParts/ExtractBodies. A synthetic fixture matching the shape
// of the real reported Outlook/Mimecast multipart/alternative message
// (quoted-printable text/plain + quoted-printable text/html) pins the
// fix without committing any private/company-sensitive content from
// the real message.

import (
	"strings"
	"testing"
)

func TestExtractBodies_QuotedPrintable_PlainTextDecoded(t *testing.T) {
	data := []byte("Content-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
		"Hello=2C this has an equals sign (=3D) and a soft line break=\r\n" +
		"that continues here.")
	bc := ExtractBodies(data)
	want := "Hello, this has an equals sign (=) and a soft line breakthat continues here."
	if bc.TextBody != want {
		t.Fatalf("quoted-printable text/plain not decoded:\n got=%q\nwant=%q", bc.TextBody, want)
	}
	if strings.Contains(bc.TextBody, "=3D") || strings.Contains(bc.TextBody, "=2C") {
		t.Fatalf("quoted-printable escapes leaked into rendered body: %q", bc.TextBody)
	}
}

func TestExtractBodies_QuotedPrintable_HTMLDecoded(t *testing.T) {
	data := []byte("Content-Type: text/html; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
		"<html><body><p>Caf=C3=A9 =E2=80=94 price is =E2=82=AC10</p></body></html>")
	bc := ExtractBodies(data)
	if strings.Contains(bc.HTMLBody, "=C3=A9") || strings.Contains(bc.HTMLBody, "=E2=80=94") {
		t.Fatalf("quoted-printable escapes leaked into rendered HTML: %q", bc.HTMLBody)
	}
	if !strings.Contains(bc.HTMLBody, "Café") {
		t.Fatalf("expected decoded UTF-8 'Café' in HTML body, got %q", bc.HTMLBody)
	}
}

func TestExtractBodies_Base64_PlainTextDecoded(t *testing.T) {
	// "Hello from base64 encoded body" base64-encoded, wrapped as a
	// real MUA would.
	encoded := "SGVsbG8gZnJvbSBiYXNlNjQgZW5jb2RlZCBib2R5"
	data := []byte("Content-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + encoded)
	bc := ExtractBodies(data)
	want := "Hello from base64 encoded body"
	if bc.TextBody != want {
		t.Fatalf("base64 text/plain not decoded: got=%q want=%q", bc.TextBody, want)
	}
}

func TestExtractBodies_Base64_TolerantOfLineWrapping(t *testing.T) {
	// Real MUAs wrap base64 at ~76 chars with CRLF.
	data := []byte("Content-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		"SGVsbG8gZnJvbSB3cmFwcGVkIGJhc2U2NCBlbmNvZGVk\r\nIGJvZHkgdGhhdCBzcGFucyBsaW5lcw==")
	bc := ExtractBodies(data)
	if !strings.Contains(bc.TextBody, "Hello from wrapped base64") {
		t.Fatalf("line-wrapped base64 not decoded correctly: %q", bc.TextBody)
	}
}

func TestExtractBodies_MultipartAlternative_BothPartsQuotedPrintableDecoded(t *testing.T) {
	// Mirrors the real live bug report's exact structure:
	// multipart/alternative with a quoted-printable text/plain part
	// and a quoted-printable text/html part.
	data := []byte(
		"Content-Type: multipart/alternative; boundary=\"BOUNDARY123\"\r\n\r\n" +
			"--BOUNDARY123\r\n" +
			"Content-Type: text/plain; charset=UTF-8\r\n" +
			"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
			"Dear Customer,=0D=0A=0D=0APlease find the invoice attached.=0D=0A=0D=0ARegards\r\n" +
			"--BOUNDARY123\r\n" +
			"Content-Type: text/html; charset=UTF-8\r\n" +
			"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
			"<html><body><p>Dear Customer,</p><p>Please find the invoice attached.</p>" +
			"<p>Regards</p></body></html>\r\n" +
			"--BOUNDARY123--\r\n")
	bc := ExtractBodies(data)
	if !bc.HasHTML {
		t.Fatal("expected HasHTML=true")
	}
	if strings.Contains(bc.TextBody, "=0D=0A") {
		t.Fatalf("text/plain still contains raw QP escapes: %q", bc.TextBody)
	}
	if !strings.Contains(bc.TextBody, "Please find the invoice attached.") {
		t.Fatalf("decoded text/plain missing expected content: %q", bc.TextBody)
	}
	if !strings.Contains(bc.HTMLBody, "Please find the invoice attached.") {
		t.Fatalf("decoded HTML missing expected content: %q", bc.HTMLBody)
	}
	// Neither body may contain the raw MIME boundary or a
	// Content-Type/Content-Transfer-Encoding header line — the
	// reading pane must never show MIME structure as message text.
	for _, body := range []string{bc.TextBody, bc.HTMLBody} {
		if strings.Contains(body, "BOUNDARY123") {
			t.Fatalf("MIME boundary leaked into rendered body: %q", body)
		}
		if strings.Contains(body, "Content-Type:") || strings.Contains(body, "Content-Transfer-Encoding:") {
			t.Fatalf("MIME header line leaked into rendered body: %q", body)
		}
	}
}

// TestExtractBodies_SyntheticOutlookFixture is a synthetic
// (non-private) fixture matching the STRUCTURE of the real reported
// message: Outlook/Mimecast-style headers, multipart/alternative,
// quoted-printable text/plain + quoted-printable text/html with an
// HTML signature block. No content from the real message is used.
func TestExtractBodies_SyntheticOutlookFixture(t *testing.T) {
	data := []byte(strings.Join([]string{
		"Received: from EXAMPLE-MB01.example.prod.outlook.com",
		"X-MS-Exchange-Organization-AuthAs: Internal",
		"X-Mimecast-Spam-Score: 0",
		"From: Example Sender <sender@example-corp.test>",
		"To: Recipient Person <recipient@orvix.email>",
		"Subject: Re: Example invoice",
		"Date: Mon, 01 Jan 2026 10:00:00 +0000",
		"Message-ID: <EXAMPLE01MB1234@EXAMPLE01MB1234.example.prod.outlook.com>",
		"MIME-Version: 1.0",
		"Content-Type: multipart/alternative; boundary=\"_000_EXAMPLE01MB1234_\"",
		"",
		"--_000_EXAMPLE01MB1234_",
		"Content-Type: text/plain; charset=\"utf-8\"",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"Hi there,=0D=0A=0D=0AThanks for reaching out=2E Please see the details =",
		"below=2E=0D=0A=0D=0ABest regards,=0D=0AExample Sender",
		"--_000_EXAMPLE01MB1234_",
		"Content-Type: text/html; charset=\"utf-8\"",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"<html><body><p>Hi there,</p><p>Thanks for reaching out. Please see the =",
		"details below.</p><table><tr><td>Item</td><td>Amount</td></tr></table>",
		"<p style=3D\"font-family:Calibri\">Best regards,<br>Example Sender</p>",
		"</body></html>",
		"--_000_EXAMPLE01MB1234_--",
		"",
	}, "\r\n"))

	bc := ExtractBodies(data)
	if !bc.HasHTML {
		t.Fatal("expected HasHTML=true")
	}
	for _, artifact := range []string{"=0D=0A", "=2E", "=3D", "_000_EXAMPLE01MB1234_"} {
		if strings.Contains(bc.TextBody, artifact) {
			t.Fatalf("QP/boundary artifact %q leaked into text body: %q", artifact, bc.TextBody)
		}
		if strings.Contains(bc.HTMLBody, artifact) {
			t.Fatalf("QP/boundary artifact %q leaked into HTML body: %q", artifact, bc.HTMLBody)
		}
	}
	if !strings.Contains(bc.TextBody, "Thanks for reaching out.") {
		t.Fatalf("decoded text/plain missing expected content: %q", bc.TextBody)
	}
	if !strings.Contains(bc.HTMLBody, "Thanks for reaching out.") {
		t.Fatalf("decoded HTML missing expected content: %q", bc.HTMLBody)
	}
	if !strings.Contains(bc.HTMLBody, `style="font-family:Calibri"`) {
		t.Fatalf("decoded HTML signature style attribute mangled: %q", bc.HTMLBody)
	}
}

// ── Sanitize: iframe/object/embed hardening ─────────────────────────

func TestSanitizeRemovesIframeTags(t *testing.T) {
	html := `<p>before</p><iframe src="https://evil.example/track"></iframe><p>after</p>`
	got := SanitizeHTML(html)
	if strings.Contains(got, "<iframe") {
		t.Fatalf("iframe not stripped: %q", got)
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Fatalf("surrounding content must survive: %q", got)
	}
}

func TestSanitizeRemovesSelfClosingIframe(t *testing.T) {
	got := SanitizeHTML(`<iframe src="https://evil.example/x" />`)
	if strings.Contains(got, "<iframe") {
		t.Fatalf("self-closing iframe not stripped: %q", got)
	}
}

func TestSanitizeRemovesObjectTags(t *testing.T) {
	html := `<object data="https://evil.example/x.swf"><param name="a" value="b"></object>`
	got := SanitizeHTML(html)
	if strings.Contains(got, "<object") || strings.Contains(got, "<param") {
		t.Fatalf("object not stripped: %q", got)
	}
}

func TestSanitizeRemovesEmbedTags(t *testing.T) {
	got := SanitizeHTML(`<embed src="https://evil.example/x.swf">`)
	if strings.Contains(got, "<embed") {
		t.Fatalf("embed not stripped: %q", got)
	}
}
