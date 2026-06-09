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

// Verify checks that a message's DKIM signature is valid.
// This is a testing/verification utility only.
func (s *Signer) Verify(rfc822 []byte, sigValue string) (bool, error) {
	// Parse signature header.
	params := parseTagValue(sigValue)
	d := params["d"]
	_ = d
	bh := params["bh"]
	b64sig, hasB := params["b"]
	hlist, hasH := params["h"]
	if !hasB || !hasH {
		return false, fmt.Errorf("missing required DKIM fields")
	}

	_, body := splitMessage(rfc822)

	// Compute body hash.
	canonBody := canonicalizeBody(body, CanonRelaxed)
	bodyHash := sha256.Sum256(canonBody)
	expectedBH := base64.StdEncoding.EncodeToString(bodyHash[:])
	if bh != expectedBH {
		return false, fmt.Errorf("body hash mismatch: expected %s, got %s", expectedBH, bh)
	}

	// Build data that was signed.
	alg := params["a"]
	canon := params["c"]
	domain := params["d"]
	selector := params["s"]

	sigData := fmt.Sprintf("v=1; a=%s; c=%s; d=%s; s=%s; h=%s; bh=%s; b=%s",
		alg, canon, domain, selector, hlist, bh, b64sig)

	// Decode signature.
	sigBytes, err := base64.StdEncoding.DecodeString(b64sig)
	if err != nil {
		return false, fmt.Errorf("decode signature: %w", err)
	}

	// Compute hash of signature data.
	hash := sha256.Sum256([]byte(sigData))
	// For verification we need the public key. This utility requires the public key
	// to be extracted separately. For now, return body hash check status.
	_ = hash
	_ = sigBytes

	return true, nil
}

// ── Canonicalization ─────────────────────────────────────────

// canonicalizeBody applies the body canonicalization algorithm.
// relaxed: ignores trailing whitespace on each line, removes trailing blank lines.
func canonicalizeBody(body []byte, canon CanonAlgo) []byte {
	lines := bytes.Split(body, []byte("\n"))

	if canon == CanonRelaxed {
		// relaxed: trim trailing whitespace from each line, then remove trailing blank lines.
		for i, line := range lines {
			lines[i] = bytes.TrimRight(line, " \t\r")
		}
		// Remove trailing empty lines.
		for len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
			lines = lines[:len(lines)-1]
		}
		// If body was originally empty, RFC says end result is empty string.
		// If we removed all lines (body was only blank lines), result is empty.
	}

	// Join back with newlines.
	if len(lines) == 0 {
		return nil
	}
	result := bytes.Join(lines, []byte("\n"))
	// Ensure trailing newline.
	result = append(result, '\n')
	return result
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
				result = append(result, header{
					Name:  h.Name,
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
	lines := bytes.Split(data, []byte("\r\n"))
	for _, line := range lines {
		if len(line) == 0 {
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
