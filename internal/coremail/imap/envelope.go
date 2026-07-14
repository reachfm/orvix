package imap

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

// Envelope represents an IMAP ENVELOPE response structure.
type Envelope struct {
	Date      string // Date header
	Subject   string // Subject header
	From      []*Address
	Sender    []*Address
	ReplyTo   []*Address
	To        []*Address
	Cc        []*Address
	Bcc       []*Address
	InReplyTo string
	MessageID string
}

// Address represents an IMAP email address structure.
type Address struct {
	Name    string // personal name (NIL if absent)
	Source  string // SMTP source route (obsolete, NIL)
	Mailbox string // local part
	Host    string // domain
}

// FormatEnvelope formats an Envelope into the IMAP ENVELOPE response string.
func FormatEnvelope(e *Envelope) string {
	return fmt.Sprintf("(%s %s %s %s %s %s %s %s %s %s)",
		formatNILString(e.Date),
		formatNILString(e.Subject),
		formatAddressList(e.From),
		formatAddressList(e.Sender),
		formatAddressList(e.ReplyTo),
		formatAddressList(e.To),
		formatAddressList(e.Cc),
		formatAddressList(e.Bcc),
		formatNILString(e.InReplyTo),
		formatNILString(e.MessageID),
	)
}

// BuildEnvelope constructs an Envelope from raw RFC822 headers.
func BuildEnvelope(rfc822 []byte) *Envelope {
	e := &Envelope{}

	headers := parseRFC822Headers(rfc822)

	for _, h := range headers {
		switch strings.ToLower(h.Name) {
		case "date":
			e.Date = h.Value
		case "subject":
			e.Subject = h.Value
		case "from":
			e.From = parseAddressList(h.Value)
		case "sender":
			e.Sender = parseAddressList(h.Value)
		case "reply-to":
			e.ReplyTo = parseAddressList(h.Value)
		case "to":
			e.To = parseAddressList(h.Value)
		case "cc":
			e.Cc = parseAddressList(h.Value)
		case "bcc":
			e.Bcc = parseAddressList(h.Value)
		case "in-reply-to":
			e.InReplyTo = h.Value
		case "message-id":
			e.MessageID = h.Value
		}
	}

	return e
}

// ── Address Parsing ──────────────────────────────────────────

type rawHeader struct {
	Name  string
	Value string
}

func parseRFC822Headers(data []byte) []rawHeader {
	idx := bytes.Index(data, []byte("\r\n\r\n"))
	if idx < 0 {
		idx = bytes.Index(data, []byte("\n\n"))
		if idx < 0 {
			return nil
		}
		data = data[:idx]
	} else {
		data = data[:idx]
	}

	var headers []rawHeader
	lines := bytes.Split(data, []byte("\r\n"))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		pos := bytes.IndexByte(line, ':')
		if pos < 0 {
			continue
		}
		name := strings.TrimSpace(string(line[:pos]))
		value := strings.TrimSpace(string(line[pos+1:]))
		headers = append(headers, rawHeader{Name: name, Value: value})
	}
	return headers
}

func parseAddressList(s string) []*Address {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}

	var addrs []*Address

	// Handle the common case: "Display Name <email>, ..."
	// Split on commas, handling quoted strings.
	current := ""
	inQuote := false
	for _, ch := range s {
		if ch == '"' {
			inQuote = !inQuote
			current += string(ch)
		} else if ch == ',' && !inQuote {
			if a := parseAddress(strings.TrimSpace(current)); a != nil {
				addrs = append(addrs, a)
			}
			current = ""
		} else {
			current += string(ch)
		}
	}
	if current != "" {
		if a := parseAddress(strings.TrimSpace(current)); a != nil {
			addrs = append(addrs, a)
		}
	}

	return addrs
}

func parseAddress(s string) *Address {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}

	a := &Address{}

	// Check for "Display Name <email>" format.
	if idx := strings.IndexByte(s, '<'); idx >= 0 {
		name := strings.TrimSpace(s[:idx])
		name = strings.Trim(name, "\"")
		a.Name = name

		email := s[idx+1:]
		if end := strings.IndexByte(email, '>'); end >= 0 {
			email = email[:end]
		}
		if at := strings.IndexByte(email, '@'); at >= 0 {
			a.Mailbox = email[:at]
			a.Host = email[at+1:]
		} else {
			a.Mailbox = email
		}
	} else if at := strings.IndexByte(s, '@'); at >= 0 {
		// Bare email.
		a.Mailbox = s[:at]
		a.Host = s[at+1:]
	} else {
		// Just a name or mailbox.
		a.Mailbox = s
	}

	return a
}

func formatAddressList(addrs []*Address) string {
	if len(addrs) == 0 {
		return "NIL"
	}
	parts := make([]string, len(addrs))
	for i, a := range addrs {
		parts[i] = formatAddress(a)
	}
	return "(" + strings.Join(parts, " ") + ")"
}

func formatAddress(a *Address) string {
	return fmt.Sprintf("(%s NIL %s %s)",
		formatNILString(a.Name),
		formatNILString(a.Mailbox),
		formatNILString(a.Host),
	)
}

func formatNILString(s string) string {
	if s == "" {
		return "NIL"
	}
	// Escape backslashes and double quotes.
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return "\"" + s + "\""
}

// formatIMAPDate formats a time.Time to IMAP INTERNALDATE format.
func formatIMAPDate(t time.Time) string {
	return t.Format("02-Jan-2006 15:04:05 -0700")
}

// formatFlags converts message flags to IMAP flag list.
func formatFlags(seen, answered, flagged, draft, deleted bool) string {
	var flags []string
	if seen {
		flags = append(flags, "\\Seen")
	}
	if answered {
		flags = append(flags, "\\Answered")
	}
	if flagged {
		flags = append(flags, "\\Flagged")
	}
	if draft {
		flags = append(flags, "\\Draft")
	}
	if deleted {
		flags = append(flags, "\\Deleted")
	}
	if len(flags) == 0 {
		return "()"
	}
	return "(" + strings.Join(flags, " ") + ")"
}

// ── BODY Helpers ────────────────────────────────────────────

// SplitBody splits an RFC822 message into header and body sections.
// Returns (header, body) where body is everything after the blank line.
func SplitBody(data []byte) (header, body []byte) {
	idx := bytes.Index(data, []byte("\r\n\r\n"))
	if idx >= 0 {
		return data[:idx], data[idx+4:]
	}
	idx = bytes.Index(data, []byte("\n\n"))
	if idx >= 0 {
		return data[:idx], data[idx+2:]
	}
	return data, nil
}

// FormatLiteral formats a byte slice as an IMAP literal.
// Returns "{size}\r\n<data>" for non-empty data, or "NIL" for empty.
func FormatLiteral(data []byte) string {
	if len(data) == 0 {
		return "NIL"
	}
	return fmt.Sprintf("{%d}\r\n%s", len(data), string(data))
}

// FormatBodySection formats a body section response per RFC 3501.
// BODY[<section>] <literal>
func FormatBodySection(section string, data []byte) string {
	if section == "" {
		return fmt.Sprintf("BODY[] %s", FormatLiteral(data))
	}
	return fmt.Sprintf("BODY[%s] %s", section, FormatLiteral(data))
}

// detectMIMEType returns basic MIME type and subtype from Content-Type header.
func detectMIMEType(contentType string) (string, string) {
	ct := strings.ToLower(contentType)
	if idx := strings.IndexByte(ct, ';'); idx >= 0 {
		ct = strings.TrimSpace(ct[:idx])
	}
	ct = strings.TrimSpace(ct)
	parts := strings.SplitN(ct, "/", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	if ct == "" {
		return "text", "plain"
	}
	return ct, ""
}

// detectBoundary returns the boundary string from a Content-Type header.
func detectBoundary(contentType string) string {
	lower := strings.ToLower(contentType)
	idx := strings.Index(lower, "boundary=")
	if idx < 0 {
		return ""
	}
	b := contentType[idx+9:]
	// Strip quotes.
	if strings.HasPrefix(b, "\"") {
		b = strings.Trim(b, "\"")
	}
	return b
}

// GetBodyStructure returns the IMAP BODYSTRUCTURE for a message.
// This is a simplified implementation that handles common cases.
func GetBodyStructure(data []byte) string {
	headers := parseRFC822Headers(data)

	contentType := ""
	for _, h := range headers {
		if strings.EqualFold(h.Name, "Content-Type") {
			contentType = h.Value
			break
		}
	}

	mimeType, subType := detectMIMEType(contentType)

	if mimeType == "multipart" {
		boundary := detectBoundary(contentType)
		return formatMultipartBody(data, mimeType, subType, boundary)
	}

	// Simple single-part body.
	params := extractContentTypeParams(contentType)
	// Count lines in body.
	_, body := SplitBody(data)
	lineCount := bytes.Count(body, []byte("\n"))
	size := len(body)

	return fmt.Sprintf("(\"%s\" \"%s\" %s NIL NIL %s %d NIL NIL NIL)",
		mimeType, subType,
		formatContentTypeParams(params),
		formatNumeric(size),
		lineCount,
	)
}

func formatMultipartBody(data []byte, mimeType, subType, boundary string) string {
	// Simplified multipart: return the subtype with basic structure.
	_ = mimeType

	// Count parts by splitting on boundary.
	var parts []string
	if boundary != "" {
		_, body := SplitBody(data)
		bodyStr := string(body)
		segments := strings.Split(bodyStr, "--"+boundary)
		for _, seg := range segments {
			seg = strings.TrimSpace(seg)
			if seg == "" || seg == "--" {
				continue
			}
			parts = append(parts, seg)
		}
	}

	// Build part structures.
	var partStrs []string
	for _, part := range parts {
		partData := []byte(part)
		partHeaders := parseRFC822Headers(partData)

		partCT := ""
		for _, h := range partHeaders {
			if strings.EqualFold(h.Name, "Content-Type") {
				partCT = h.Value
				break
			}
		}
		pmt, pst := detectMIMEType(partCT)
		_, pBody := SplitBody(partData)
		pSize := len(pBody)
		pLines := bytes.Count(pBody, []byte("\n"))
		params := extractContentTypeParams(partCT)

		partStrs = append(partStrs, fmt.Sprintf("(\"%s\" \"%s\" %s NIL NIL %s %d NIL NIL NIL)",
			pmt, pst,
			formatContentTypeParams(params),
			formatNumeric(pSize),
			pLines,
		))
	}

	// Handle case with no boundary (treat as single text).
	if len(partStrs) == 0 {
		return fmt.Sprintf("(\"text\" \"plain\" (\"charset\" \"utf-8\") NIL NIL \"7bit\" %d 1 NIL NIL NIL \"%s\")",
			0, subType)
	}

	return fmt.Sprintf("(%s \"%s\")",
		strings.Join(partStrs, " "),
		subType,
	)
}

func extractContentTypeParams(contentType string) map[string]string {
	params := make(map[string]string)
	parts := strings.Split(contentType, ";")
	for i := 1; i < len(parts); i++ {
		part := strings.TrimSpace(parts[i])
		if idx := strings.IndexByte(part, '='); idx >= 0 {
			key := strings.ToLower(strings.TrimSpace(part[:idx]))
			value := strings.TrimSpace(part[idx+1:])
			value = strings.Trim(value, "\"")
			params[key] = value
		}
	}
	return params
}

func formatContentTypeParams(params map[string]string) string {
	if len(params) == 0 {
		return "NIL"
	}
	// Only format charset for now.
	if charset, ok := params["charset"]; ok {
		return fmt.Sprintf("(\"charset\" \"%s\")", charset)
	}
	return "NIL"
}

func formatNumeric(n int) string {
	if n == 0 {
		return "0"
	}
	return fmt.Sprintf("%d", n)
}

// Ensure time import is used for formatIMAPDate.
var _ = time.Now
