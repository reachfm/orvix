package mime

import (
	"strings"
	"testing"
)

func TestExtractBodiesTextPlain(t *testing.T) {
	data := []byte("Content-Type: text/plain\r\n\r\nHello World")
	bc := ExtractBodies(data)
	if bc.TextBody != "Hello World" {
		t.Fatalf("expected 'Hello World', got '%s'", bc.TextBody)
	}
	if bc.HasHTML {
		t.Fatal("expected no HTML")
	}
}

func TestExtractBodiesTextHTML(t *testing.T) {
	data := []byte("Content-Type: text/html\r\n\r\n<p>Hello World</p>")
	bc := ExtractBodies(data)
	if !bc.HasHTML {
		t.Fatal("expected HTML")
	}
	if bc.HTMLBody == "" {
		t.Fatal("expected non-empty HTML body")
	}
}

func TestExtractBodiesMultipartAlternative(t *testing.T) {
	boundary := "==alt=="
	data := []byte("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n" +
		"--" + boundary + "\r\nContent-Type: text/plain\r\n\r\nPlain text\r\n" +
		"--" + boundary + "\r\nContent-Type: text/html\r\n\r\n<p>HTML text</p>\r\n" +
		"--" + boundary + "--\r\n")
	bc := ExtractBodies(data)
	if bc.TextBody != "Plain text" {
		t.Fatalf("expected 'Plain text', got '%s'", bc.TextBody)
	}
	if !bc.HasHTML {
		t.Fatal("expected HTML")
	}
	if !has(bc.HTMLBody, "HTML text") {
		t.Fatal("expected HTML content in body")
	}
}

func TestExtractBodiesMultipartMixedWithAttachments(t *testing.T) {
	boundary := "==mixed=="
	data := []byte("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n" +
		"--" + boundary + "\r\nContent-Type: text/plain\r\n\r\nBody text\r\n" +
		"--" + boundary + "\r\nContent-Disposition: attachment; filename=\"file.txt\"\r\nContent-Type: text/plain\r\n\r\nAttachment content\r\n" +
		"--" + boundary + "--\r\n")
	bc := ExtractBodies(data)
	if bc.TextBody != "Body text" {
		t.Fatalf("expected 'Body text', got '%s'", bc.TextBody)
	}
	if bc.HasHTML {
		t.Fatal("expected no HTML")
	}
}

func TestExtractBodiesNoContent(t *testing.T) {
	data := []byte("Subject: empty\r\n\r\n")
	bc := ExtractBodies(data)
	if bc.TextBody != "" || bc.HTMLBody != "" {
		t.Fatal("expected empty bodies")
	}
}

func TestExtractBodiesOnlyHTML(t *testing.T) {
	data := []byte("Content-Type: text/html\r\n\r\n<h1>Title</h1><p>Content</p>")
	bc := ExtractBodies(data)
	if !bc.HasHTML {
		t.Fatal("expected HTML")
	}
	if bc.HTMLBody == "" {
		t.Fatal("expected non-empty HTML")
	}
}

func TestExtractBodiesMalformedHTML(t *testing.T) {
	data := []byte("Content-Type: text/html\r\n\r\n<p>Unclosed tag")
	bc := ExtractBodies(data)
	if !bc.HasHTML {
		t.Fatal("expected HTML even if malformed")
	}
}

func TestExtractBodiesScriptStripped(t *testing.T) {
	data := []byte("Content-Type: text/html\r\n\r\n<p>Hello</p><script>alert('xss')</script><p>World</p>")
	bc := ExtractBodies(data)
	if has(bc.HTMLBody, "<script") {
		t.Fatal("script tags should be stripped")
	}
	if !has(bc.HTMLBody, "<p>Hello") || !has(bc.HTMLBody, "<p>World") {
		t.Fatal("safe HTML should be preserved")
	}
}

func TestExtractBodiesJavascriptURLStripped(t *testing.T) {
	data := []byte("Content-Type: text/html\r\n\r\n<a href=\"javascript:alert(1)\">click</a>")
	bc := ExtractBodies(data)
	if has(bc.HTMLBody, "javascript:") {
		t.Fatal("javascript: URLs should be blocked")
	}
	if !has(bc.HTMLBody, "blocked:") {
		t.Fatal("javascript: should be replaced with blocked:")
	}
}

func TestExtractBodiesRemoteImageDetected(t *testing.T) {
	data := []byte("Content-Type: text/html\r\n\r\n<img src=\"https://example.com/tracker.gif\">")
	bc := ExtractBodies(data)
	if !bc.HasRemoteImages {
		t.Fatal("expected remote images detected")
	}
}

func TestExtractBodiesRemoteImageBlocked(t *testing.T) {
	data := []byte("Content-Type: text/html\r\n\r\n<img src=\"https://example.com/img.png\">")
	bc := ExtractBodies(data)
	if has(bc.HTMLBody, " src=\"https://") {
		t.Fatal("remote image src should be replaced with data-remote-src")
	}
	if !has(bc.HTMLBody, "data-remote-src=\"https://") {
		t.Fatalf("expected data-remote-src, got: %s", bc.HTMLBody)
	}
}

func TestExtractBodiesLocalImagePreserved(t *testing.T) {
	data := []byte("Content-Type: text/html\r\n\r\n<img src=\"cid:img123\">")
	bc := ExtractBodies(data)
	if !has(bc.HTMLBody, "src=\"cid:img123\"") {
		t.Fatal("local cid: images should be preserved")
	}
}

func TestExtractBodiesRemoteImageTogglePreservesOriginal(t *testing.T) {
	data := []byte("Content-Type: text/html\r\n\r\n<img src=\"https://cdn.example.com/photo.jpg\" alt=\"Photo\" width=\"500\">")
	bc := ExtractBodies(data)
	if !has(bc.HTMLBody, "data-remote-src=\"https://cdn.example.com/photo.jpg\"") {
		t.Fatal("original URL should be preserved in data-remote-src")
	}
	if !has(bc.HTMLBody, "alt=\"Photo\"") {
		t.Fatal("other attributes should be preserved")
	}
}

func has(s, substr string) bool {
	return strings.Contains(s, substr)
}
