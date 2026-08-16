package dkim

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"strings"
)

// Signer creates DKIM signatures for outbound messages.
type Signer struct {
}

// NewSigner creates a DKIM signer.
func NewSigner() *Signer {
	return &Signer{}
}

// SignResult holds the result of signing.
type SignResult struct {
	Signature string // the DKIM-Signature header value (without header name)
	HeaderSet HeaderSet
}

// Sign produces a DKIM-Signature for the given message.
// rfc822 is the full RFC822 message with headers and body.
// Returns the DKIM-Signature header value and any error.
func (s *Signer) Sign(rfc822 []byte, hs HeaderSet) (*SignResult, error) {
	if hs.SignedHeaders == nil {
		hs.SignedHeaders = DefaultHeaders
	}
	if hs.BodyCanon == "" {
		hs.BodyCanon = CanonRelaxed
	}
	if hs.HeaderCanon == "" {
		hs.HeaderCanon = CanonRelaxed
	}
	if hs.HashAlgo == "" {
		hs.HashAlgo = HashSHA256
	}
	if hs.SignAlgo == "" {
		hs.SignAlgo = SignRSASHA256
	}

	// Split headers and body.
	headers, body := splitMessage(rfc822)

	// Canonicalize body and compute hash.
	canonBody := canonicalizeBody(body, hs.BodyCanon)
	bodyHash := sha256.Sum256(canonBody)
	bh := base64.StdEncoding.EncodeToString(bodyHash[:])

	// Canonicalize and collect signed headers.
	canonHeaders := canonicalizeHeaders(headers, hs.SignedHeaders, hs.HeaderCanon)

	// Build signature data (without the actual signature).
	sigData := buildSignatureData(canonHeaders, hs, bh, "")

	// Load private key.
	block, _ := pem.Decode([]byte(hs.PrivateKeyPEM))
	if block == nil {
		return nil, fmt.Errorf("failed to decode private key PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS1.
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is not RSA")
	}

	// Sign.
	hash := sha256.Sum256([]byte(sigData))
	signature, err := rsa.SignPKCS1v15(nil, rsaKey, crypto.SHA256, hash[:])
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}

	b64sig := base64.StdEncoding.EncodeToString(signature)

	// Build final signature value.
	alg := "rsa-sha256"
	hlist := strings.Join(hs.SignedHeaders, ":")
	sigValue := fmt.Sprintf("v=1; a=%s; c=%s/%s; d=%s; s=%s; h=%s; bh=%s; b=%s",
		alg, hs.HeaderCanon, hs.BodyCanon, hs.Domain, hs.Selector, hlist, bh, b64sig)

	return &SignResult{Signature: sigValue, HeaderSet: hs}, nil
}

// ── Canonicalization ─────────────────────────────────────────

// canonicalizeBody applies the RFC 6376 §3.4 body canonicalization
// algorithm. DKIM operates on the Internet Message Format's CRLF line
// endings (RFC 5322) — the canonicalized body MUST use CRLF, never a
// bare LF. splitBodyLines first normalizes any of CRLF/LF/CR input
// into logical lines so a message stored or transmitted with LF-only
// line endings still canonicalizes to the RFC-correct CRLF bytes that
// a standards-compliant verifier (e.g. Gmail) independently computes.
func canonicalizeBody(body []byte, canon CanonAlgo) []byte {
	lines := splitBodyLines(body)

	if canon == CanonRelaxed {
		// §3.4.4: reduce WSP sequences within a line to a single SP,
		// and remove all trailing WSP from each line.
		for i, line := range lines {
			lines[i] = relaxWSP(line)
		}
	}

	// §3.4.3/§3.4.4 (both algorithms): remove all trailing empty lines
	// from the end of the body — the CRLF immediately preceding the
	// end of a canonicalized body is not considered part of the body.
	for len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}

	if len(lines) == 0 {
		if canon == CanonSimple {
			// §3.4.3: a completely empty (or all-blank) body
			// canonicalizes, under "simple", to a single CRLF —
			// never a zero-length result.
			return []byte("\r\n")
		}
		// §3.4.4: under "relaxed", a completely empty (or all-blank)
		// body canonicalizes to the empty string.
		return nil
	}

	result := bytes.Join(lines, []byte("\r\n"))
	result = append(result, '\r', '\n')
	return result
}

// splitBodyLines splits body into logical lines on any of CRLF, bare
// CR, or bare LF, accepting whatever line-ending convention the
// message was actually stored/transmitted with. The trailing line
// terminator is never retained as part of a line's content.
func splitBodyLines(body []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '\n':
			lines = append(lines, body[start:i])
			start = i + 1
		case '\r':
			lines = append(lines, body[start:i])
			if i+1 < len(body) && body[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	if start < len(body) {
		lines = append(lines, body[start:])
	} else if len(body) == 0 {
		lines = nil
	}
	return lines
}

// relaxWSP applies RFC 6376 §3.4.4 relaxed whitespace canonicalization
// to a single line: every run of SP/HTAB collapses to one SP, and any
// SP/HTAB at the end of the line is removed entirely.
func relaxWSP(line []byte) []byte {
	out := make([]byte, 0, len(line))
	inWSP := false
	for _, b := range line {
		if b == ' ' || b == '\t' {
			inWSP = true
			continue
		}
		if inWSP {
			out = append(out, ' ')
			inWSP = false
		}
		out = append(out, b)
	}
	// Trailing WSP run is dropped, never emitted.
	return out
}

// canonicalizeHeaders applies the header canonicalization algorithm to selected headers.
// relaxed: reduces whitespace, unfolds, lowercases.
func canonicalizeHeaders(headers []header, selectedHeaders []string, canon CanonAlgo) []header {
	// Build a map for O(1) lookup.
	selected := make(map[string]bool)
	for _, h := range selectedHeaders {
		selected[strings.ToLower(h)] = true
	}

	// Collect selected headers in order.
	var result []header
	for _, h := range headers {
		if selected[strings.ToLower(h.Name)] {
			if canon == CanonRelaxed {
				// RFC 6376 §3.4.2: "Convert all header field names
				// ... to lowercase." Name was previously passed
				// through unchanged despite a comment claiming this
				// was "already done via Name field" — it was not.
				result = append(result, header{
					Name:  strings.ToLower(h.Name),
					Value: canonicalizeHeaderValue(h.Value),
				})
			} else {
				result = append(result, h)
			}
		}
	}
	return result
}

// canonicalizeHeaderValue applies relaxed canonicalization to a header value:
// fold whitespace, remove leading/trailing whitespace.
func canonicalizeHeaderValue(value string) string {
	// Unfold: remove CRLF followed by WSP.
	value = strings.ReplaceAll(value, "\r\n ", "")
	value = strings.ReplaceAll(value, "\r\n\t", "")
	value = strings.ReplaceAll(value, "\n ", "")
	value = strings.ReplaceAll(value, "\n\t", "")

	// Reduce WSP: replace all WSP sequences with single space.
	result := make([]byte, 0, len(value))
	prevSpace := false
	for _, c := range []byte(value) {
		if c == ' ' || c == '\t' || c == '\r' {
			if !prevSpace {
				result = append(result, ' ')
				prevSpace = true
			}
		} else {
			result = append(result, c)
			prevSpace = false
		}
	}

	// Lowercase header name part — already done via Name field.
	return strings.TrimSpace(string(result))
}

// buildSignatureData constructs the data that gets signed.
func buildSignatureData(canonHeaders []header, hs HeaderSet, bh, b64sig string) string {
	// Build canonical header text.
	var hdrText strings.Builder
	for _, h := range canonHeaders {
		hdrText.WriteString(fmt.Sprintf("%s:%s\r\n", h.Name, h.Value))
	}

	// Append DKIM-Signature header (without the b= tag value).
	alg := "rsa-sha256"
	hlist := strings.Join(hs.SignedHeaders, ":")
	sigWithoutB := fmt.Sprintf("v=1; a=%s; c=%s/%s; d=%s; s=%s; h=%s; bh=%s; b=",
		alg, hs.HeaderCanon, hs.BodyCanon, hs.Domain, hs.Selector, hlist, bh)

	hdrText.WriteString(fmt.Sprintf("DKIM-Signature:%s\r\n", sigWithoutB))
	return hdrText.String()
}

// ── Message Parsing ──────────────────────────────────────────

type header struct {
	Name  string
	Value string
}

// splitMessage splits an RFC822 message into headers and body.
func splitMessage(rfc822 []byte) ([]header, []byte) {
	idx := bytes.Index(rfc822, []byte("\r\n\r\n"))
	if idx < 0 {
		idx = bytes.Index(rfc822, []byte("\n\n"))
		if idx < 0 {
			return nil, rfc822
		}
		return parseHeaders(rfc822[:idx]), rfc822[idx+2:]
	}
	return parseHeaders(rfc822[:idx]), rfc822[idx+4:]
}

func parseHeaders(data []byte) []header {
	var headers []header
	// splitBodyLines normalizes CRLF/CR/LF line endings the same way
	// the body canonicalizer does, so a header block stored with
	// either convention parses identically.
	for _, line := range splitBodyLines(data) {
		if len(line) == 0 {
			continue
		}
		// RFC 5322 folding: a continuation line begins with SP or
		// HTAB and belongs to the immediately preceding header field.
		// RFC 6376 §3.4.2 unfolding removes the line terminator but
		// retains the WSP that followed it — previously this parser
		// had no folding support at all, so a wrapped header's
		// continuation line (lacking a colon) was silently dropped
		// entirely rather than being appended to the header's value.
		if (line[0] == ' ' || line[0] == '\t') && len(headers) > 0 {
			headers[len(headers)-1].Value += string(line)
			continue
		}
		idx := bytes.IndexByte(line, ':')
		if idx < 0 {
			continue
		}
		name := strings.TrimSpace(string(line[:idx]))
		value := strings.TrimSpace(string(line[idx+1:]))
		headers = append(headers, header{Name: name, Value: value})
	}
	return headers
}

// parseTagValue parses a DKIM tag=value list.
func parseTagValue(s string) map[string]string {
	result := make(map[string]string)
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		idx := strings.IndexByte(part, '=')
		if idx < 0 {
			continue
		}
		tag := strings.TrimSpace(part[:idx])
		val := strings.TrimSpace(part[idx+1:])
		result[tag] = val
	}
	return result
}
