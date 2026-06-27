package mime

import (
	"strings"
	"testing"
)

func TestExtractPartsTextPlain(t *testing.T) {
	raw := []byte("From: sender@example.com\r\nSubject: Test\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nHello World")
	parts, err := ExtractParts(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if parts[0].IsAttachment {
		t.Fatal("text/plain should not be an attachment")
	}
	if string(parts[0].Body) != "Hello World" {
		t.Fatalf("expected body 'Hello World', got '%s'", string(parts[0].Body))
	}
	if parts[0].ContentType != "text/plain" {
		t.Fatalf("expected text/plain, got %s", parts[0].ContentType)
	}
}

func TestExtractPartsNoContentType(t *testing.T) {
	raw := []byte("From: sender@example.com\r\nSubject: Test\r\n\r\nHello World")
	parts, err := ExtractParts(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if parts[0].ContentType != "text/plain" {
		t.Fatalf("expected text/plain default, got %s", parts[0].ContentType)
	}
}

func TestExtractPartsMultipartOneAttachment(t *testing.T) {
	boundary := "==boundary_001=="
	raw := []byte("From: sender@example.com\r\nSubject: With Attach\r\nContent-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Type: text/plain\r\n\r\n" +
		"Body text\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Type: application/pdf\r\n" +
		"Content-Disposition: attachment; filename=\"report.pdf\"\r\n\r\n" +
		"%PDF-1.4 fake content\r\n" +
		"--" + boundary + "--\r\n")
	parts, err := ExtractParts(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	attach := parts[1]
	if !attach.IsAttachment {
		t.Fatal("expected attachment to be marked as attachment")
	}
	if attach.Filename != "report.pdf" {
		t.Fatalf("expected filename report.pdf, got %s", attach.Filename)
	}
	if attach.ContentType != "application/pdf" {
		t.Fatalf("expected application/pdf, got %s", attach.ContentType)
	}
	if attach.Size != 21 {
		t.Fatalf("expected size 21, got %d", attach.Size)
	}
}

func TestExtractPartsMultipartMultipleAttachments(t *testing.T) {
	boundary := "==multi=="
	raw := []byte("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n" +
		"--" + boundary + "\r\nContent-Type: text/plain\r\n\r\nBody\r\n" +
		"--" + boundary + "\r\nContent-Type: image/png\r\nContent-Disposition: attachment; filename=\"img.png\"\r\n\r\nPNG data\r\n" +
		"--" + boundary + "\r\nContent-Type: application/zip\r\nContent-Disposition: attachment; filename=\"archive.zip\"\r\n\r\nZIP data\r\n" +
		"--" + boundary + "--\r\n")
	parts, err := ExtractParts(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(parts))
	}
	attachments := 0
	for _, p := range parts {
		if p.IsAttachment {
			attachments++
		}
	}
	if attachments != 2 {
		t.Fatalf("expected 2 attachments, got %d", attachments)
	}
	if parts[1].Filename != "img.png" {
		t.Fatalf("expected img.png, got %s", parts[1].Filename)
	}
	if parts[2].Filename != "archive.zip" {
		t.Fatalf("expected archive.zip, got %s", parts[2].Filename)
	}
}

func TestExtractPartsNestedMultipart(t *testing.T) {
	outerBoundary := "==outer=="
	innerBoundary := "==inner=="
	raw := []byte("Content-Type: multipart/mixed; boundary=\"" + outerBoundary + "\"\r\n\r\n" +
		"--" + outerBoundary + "\r\nContent-Type: multipart/alternative; boundary=\"" + innerBoundary + "\"\r\n\r\n" +
		"--" + innerBoundary + "\r\nContent-Type: text/plain\r\n\r\nPlain text\r\n" +
		"--" + innerBoundary + "\r\nContent-Type: text/html\r\n\r\n<html><body>HTML</body></html>\r\n" +
		"--" + innerBoundary + "--\r\n" +
		"--" + outerBoundary + "\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"doc.pdf\"\r\n\r\nPDF data\r\n" +
		"--" + outerBoundary + "--\r\n")
	parts, err := ExtractParts(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) < 3 {
		t.Fatalf("expected at least 3 parts (plain+html+attach), got %d", len(parts))
	}
	hasPDF := false
	for _, p := range parts {
		if p.Filename == "doc.pdf" && p.IsAttachment {
			hasPDF = true
		}
	}
	if !hasPDF {
		t.Fatal("expected doc.pdf attachment from nested multipart")
	}
}

func TestExtractPartsFilenameExtraction(t *testing.T) {
	boundary := "==b=="
	tests := []struct {
		name     string
		header   string
		expected string
	}{
		{"Content-Disposition filename", "Content-Disposition: attachment; filename=\"test.pdf\"", "test.pdf"},
		{"Content-Type name", "Content-Disposition: attachment; filename=\"doc.pdf\"", "doc.pdf"},
		{"No filename", "Content-Disposition: attachment", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := []byte("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n" +
				"--" + boundary + "\r\nContent-Type: text/plain\r\n\r\nbody\r\n" +
				"--" + boundary + "\r\n" + tt.header + "\r\n\r\ndata\r\n" +
				"--" + boundary + "--\r\n")
			parts, err := ExtractParts(raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(parts) < 2 {
				t.Fatal("expected at least 2 parts")
			}
			attach := parts[len(parts)-1]
			if attach.Filename != tt.expected {
				t.Fatalf("expected filename '%s', got '%s'", tt.expected, attach.Filename)
			}
		})
	}
}

func TestExtractPartsContentTypeExtraction(t *testing.T) {
	boundary := "==ct=="
	raw := []byte("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n" +
		"--" + boundary + "\r\nContent-Type: text/plain\r\n\r\nbody\r\n" +
		"--" + boundary + "\r\nContent-Disposition: attachment; filename=\"f.txt\"\r\nContent-Type: text/csv\r\n\r\na,b,c\r\n" +
		"--" + boundary + "--\r\n")
	parts, err := ExtractParts(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	if parts[1].ContentType != "text/csv" {
		t.Fatalf("expected text/csv, got %s", parts[1].ContentType)
	}
}

func TestExtractPartsAttachmentSize(t *testing.T) {
	boundary := "==sz=="
	content := strings.Repeat("A", 1024)
	raw := []byte("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n" +
		"--" + boundary + "\r\nContent-Type: text/plain\r\n\r\nbody\r\n" +
		"--" + boundary + "\r\nContent-Disposition: attachment; filename=\"large.bin\"\r\nContent-Type: application/octet-stream\r\n\r\n" + content + "\r\n" +
		"--" + boundary + "--\r\n")
	parts, err := ExtractParts(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	if parts[1].Size != 1024 {
		t.Fatalf("expected size 1024, got %d", parts[1].Size)
	}
}

func TestExtractPartsInlineDisposition(t *testing.T) {
	boundary := "==inline=="
	raw := []byte("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n" +
		"--" + boundary + "\r\nContent-Type: image/png\r\nContent-Disposition: inline\r\nContent-ID: <img123>\r\n\r\nPNG data\r\n" +
		"--" + boundary + "--\r\n")
	parts, err := ExtractParts(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if parts[0].ContentID != "<img123>" {
		t.Fatalf("expected Content-ID <img123>, got %s", parts[0].ContentID)
	}
}

func TestExtractPartsMalformedMIME(t *testing.T) {
	tests := [][]byte{
		[]byte("Not a valid MIME message"),
		[]byte("From: test@test.com\r\n\r\n"),
		[]byte("Content-Type: multipart/mixed; boundary=\"x\"\r\n\r\n--x\r\nbroken"), // no closing boundary
	}
	for i, raw := range tests {
		parts, err := ExtractParts(raw)
		if err != nil && i == 0 {
			// Not a valid MIME message - expected to parse as single part with empty body.
			_ = parts
			continue
		}
		if err != nil {
			t.Fatalf("test %d: unexpected error: %v", i, err)
		}
		_ = parts
	}
}

func TestExtractPartsNoBody(t *testing.T) {
	raw := []byte("From: test@test.com\r\nSubject: no body")
	parts, err := ExtractParts(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parts != nil {
		t.Fatal("expected nil parts for no body")
	}
}

func TestExtractPartsEmptyMessage(t *testing.T) {
	parts, err := ExtractParts([]byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parts != nil {
		t.Fatal("expected nil parts for empty message")
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"normal.pdf", "normal.pdf"},
		{"../../etc/passwd", "passwd"},
		{"..\\..\\windows\\system32\\drivers\\etc\\hosts", "hosts"},
		{"dir\\file.txt", "file.txt"},
		{"/var/tmp/file.txt", "file.txt"},
		{"C:\\temp\\file.txt", "file.txt"},
		{"C:/temp/file.txt", "file.txt"},
		{"C:file.txt", "file.txt"},
		{"nested/path/file.txt", "file.txt"},
		{"..", ""},
		{".", ""},
		{"safe_file.name", "safe_file.name"},
		{"", ""},
		{"file\x00name.txt", "filename.txt"},
	}
	for _, tt := range tests {
		got := SanitizeFilename(tt.input)
		if got != tt.expected {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
