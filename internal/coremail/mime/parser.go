package mime

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"path"
	"strings"
	"time"
)

// Part represents a single MIME part extracted from a message.
type Part struct {
	Headers      textproto.MIMEHeader
	Body         []byte
	Filename     string
	ContentType  string
	ContentID    string
	IsAttachment bool
	Size         int
}

// ExtractParts parses RFC822 message bytes and returns all MIME parts.
func ExtractParts(data []byte) ([]*Part, error) {
	// Find the boundary between headers and body.
	idx := bytes.Index(data, []byte("\r\n\r\n"))
	if idx < 0 {
		// No body, no attachments.
		return nil, nil
	}

	headerData := data[:idx]
	bodyData := data[idx+4:]

	// Parse top-level headers.
	headerReader := textproto.NewReader(NewLineReader(headerData))
	headers, err := headerReader.ReadMIMEHeader()
	if err != nil && len(headers) == 0 {
		return nil, fmt.Errorf("parse headers: %w", err)
	}

	contentType := headers.Get("Content-Type")
	if contentType == "" {
		// No content type, treat as single part.
		return singlePart(headers, bodyData), nil
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		// Default to single part.
		return singlePart(headers, bodyData), nil
	}

	// Check for multipart.
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return singlePart(headers, bodyData), nil
		}
		return extractMultipart(bodyData, boundary)
	}

	// Single part with known content type.
	return singlePart(headers, bodyData), nil
}

func singlePart(headers textproto.MIMEHeader, body []byte) []*Part {
	ct := headers.Get("Content-Type")
	if ct == "" {
		ct = "text/plain"
	}
	mediaType, ctParams, _ := mime.ParseMediaType(ct)
	cd := headers.Get("Content-Disposition")
	_, dispParams, _ := mime.ParseMediaType(cd)
	filename := dispParams["filename"]
	if filename == "" {
		filename = ctParams["name"]
	}
	if filename == "" {
		filename = sanitizeFilename(headers.Get("Content-Description"))
	}

	return []*Part{{
		Headers:      headers,
		Body:         body,
		Filename:     filename,
		ContentType:  mediaType,
		ContentID:    headers.Get("Content-ID"),
		IsAttachment: cd != "" || filename != "" || !strings.HasPrefix(mediaType, "text/"),
		Size:         len(body),
	}}
}

func extractMultipart(body []byte, boundary string) ([]*Part, error) {
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	var parts []*Part
	maxParts := 500 // safety limit to prevent infinite loops on malformed input

	for i := 0; i < maxParts; i++ {
		p, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break // malformed input, stop processing
		}

		partData, _ := io.ReadAll(p)
		ct := p.Header.Get("Content-Type")
		if ct == "" {
			ct = "text/plain"
		}
		mediaType, ctParams, _ := mime.ParseMediaType(ct)
		cd := p.Header.Get("Content-Disposition")
		_, dispParams, _ := mime.ParseMediaType(cd)
		filename := dispParams["filename"]
		if filename == "" {
			filename = ctParams["name"]
		}
		if filename == "" {
			filename = p.FileName()
		}
		filename = sanitizeFilename(filename)

		// If this part is itself multipart, recurse.
		if strings.HasPrefix(mediaType, "multipart/") {
			subParts, err := extractMultipart(partData, extractBoundary(ct))
			if err == nil {
				parts = append(parts, subParts...)
			}
			p.Close()
			continue
		}

		parts = append(parts, &Part{
			Headers:      p.Header,
			Body:         partData,
			Filename:     filename,
			ContentType:  mediaType,
			ContentID:    p.Header.Get("Content-ID"),
			IsAttachment: cd != "" || filename != "" || !strings.HasPrefix(mediaType, "text/"),
			Size:         len(partData),
		})

		p.Close()
	}

	return parts, nil
}

func extractBoundary(contentType string) string {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	return params["boundary"]
}

func sanitizeFilename(name string) string {
	if name == "" {
		return ""
	}
	// Remove null bytes.
	name = strings.ReplaceAll(name, "\x00", "")
	// Treat Windows and Unix separators the same on every platform.
	name = strings.ReplaceAll(name, "\\", "/")
	if len(name) >= 2 && name[1] == ':' && ((name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z')) {
		name = strings.TrimLeft(name[2:], "/")
	}
	name = path.Base(name)
	if name == "." || name == ".." {
		return ""
	}
	return name
}

// NewLineReader creates a bufio.Reader from bytes.
func NewLineReader(data []byte) *bufio.Reader {
	return bufio.NewReader(bytes.NewReader(data))
}

// BodyContent holds the extracted body parts of an email message.
type BodyContent struct {
	TextBody        string `json:"textBody"`
	HTMLBody        string `json:"htmlBody"`
	HasHTML         bool   `json:"hasHTML"`
	HasRemoteImages bool   `json:"hasRemoteImages"`
}

// ExtractBodies parses RFC822 data and returns the text and HTML bodies.
// It handles multipart/alternative, multipart/mixed, and nested structures.
// For multipart/alternative, the last text/* part wins (typically HTML preferred).
func ExtractBodies(data []byte) *BodyContent {
	parts, err := ExtractParts(data)
	if err != nil || len(parts) == 0 {
		return &BodyContent{}
	}

	result := &BodyContent{}
	for _, p := range parts {
		if p.IsAttachment {
			continue
		}
		if p.ContentType == "text/plain" && result.TextBody == "" {
			result.TextBody = string(p.Body)
		}
		if p.ContentType == "text/html" {
			result.HTMLBody = string(p.Body)
			result.HasHTML = true
		}
	}

	// Sanitize HTML body.
	if result.HasHTML {
		htmlBody := SanitizeHTML(result.HTMLBody)
		result.HasRemoteImages = HasRemoteImages(result.HTMLBody)
		result.HTMLBody = htmlBody
	}

	return result
}

// decodeQuotedPrintable decodes quoted-printable encoded content.
// This is a simple implementation supporting =XX hex encoding and soft line breaks.
func decodeQuotedPrintable(data []byte) []byte {
	var buf bytes.Buffer
	for i := 0; i < len(data); i++ {
		if data[i] == '=' && i+2 < len(data) {
			if data[i+1] == '\r' && data[i+2] == '\n' {
				i += 2 // skip soft line break
				continue
			}
			if data[i+1] == '\n' {
				i += 1 // skip soft line break (bare LF)
				continue
			}
			high := hexChar(data[i+1])
			low := hexChar(data[i+2])
			if high >= 0 && low >= 0 {
				buf.WriteByte(byte(high<<4 | low))
				i += 2
				continue
			}
		}
		buf.WriteByte(data[i])
	}
	return buf.Bytes()
}

func hexChar(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c - 'a' + 10)
	case c >= 'A' && c <= 'F':
		return int(c - 'A' + 10)
	default:
		return -1
	}
}

// AttachmentData holds an attachment to include in a built message.
type AttachmentData struct {
	Filename    string
	ContentType string
	Data        []byte
	ContentID   string
}

// BuildMultipartRFC822 constructs a multipart/mixed RFC822 message containing
// a text/plain body and a set of attachments. This is used for Email/set create
// when the client has uploaded attachments that must be included in the stored message.
func BuildMultipartRFC822(from, to, cc, bcc, subject, body, messageID string, attachments []AttachmentData) ([]byte, error) {
	boundary := fmt.Sprintf("orvix-mixed-%d", time.Now().UnixNano())

	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("From: %s\r\n", from))
	buf.WriteString(fmt.Sprintf("To: %s\r\n", to))
	if cc != "" {
		buf.WriteString(fmt.Sprintf("Cc: %s\r\n", cc))
	}
	if bcc != "" {
		buf.WriteString(fmt.Sprintf("Bcc: %s\r\n", bcc))
	}
	buf.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	buf.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z)))
	buf.WriteString(fmt.Sprintf("Message-ID: %s\r\n", messageID))
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString(fmt.Sprintf("Content-Type: multipart/mixed; boundary=\"%s\"\r\n", boundary))
	buf.WriteString("\r\n")

	// text/plain part.
	buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: 7bit\r\n")
	buf.WriteString("\r\n")
	buf.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		buf.WriteString("\r\n")
	}

	// Attachment parts.
	for _, att := range attachments {
		buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		buf.WriteString(fmt.Sprintf("Content-Type: %s\r\n", att.ContentType))
		buf.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n", sanitizeFilename(att.Filename)))
		buf.WriteString("Content-Transfer-Encoding: base64\r\n")
		if att.ContentID != "" {
			buf.WriteString(fmt.Sprintf("Content-ID: %s\r\n", att.ContentID))
		}
		buf.WriteString("\r\n")
		encoded := base64.StdEncoding.EncodeToString(att.Data)
		// Split base64 into lines of 76 chars.
		for i := 0; i < len(encoded); i += 76 {
			end := i + 76
			if end > len(encoded) {
				end = len(encoded)
			}
			buf.WriteString(encoded[i:end])
			buf.WriteString("\r\n")
		}
	}

	buf.WriteString(fmt.Sprintf("--%s--\r\n", boundary))
	return buf.Bytes(), nil
}
