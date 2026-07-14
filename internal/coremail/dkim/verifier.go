package dkim

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"strings"
)

// VerifyResult is the outcome of DKIM verification.
type VerifyResult int

const (
	VerifyPass VerifyResult = iota
	VerifyFail
	VerifyNone
	VerifyTempError
	VerifyPermError
)

func (v VerifyResult) String() string {
	switch v {
	case VerifyPass:
		return "pass"
	case VerifyFail:
		return "fail"
	case VerifyNone:
		return "none"
	case VerifyTempError:
		return "temperror"
	case VerifyPermError:
		return "permerror"
	default:
		return "none"
	}
}

// VerifyResultInfo holds the full DKIM verification outcome.
type VerifyResultInfo struct {
	Result      VerifyResult
	Domain      string
	Selector    string
	Explanation string
}

// DNSResolver abstracts DKIM DNS TXT lookups for public key retrieval.
type DNSResolver interface {
	LookupTXT(ctx context.Context, domain string) ([]string, error)
}

// Verifier verifies DKIM signatures on inbound messages.
type Verifier struct {
	resolver DNSResolver
	signer   *Signer
}

// NewVerifier creates a DKIM verifier with the given DNS resolver.
func NewVerifier(resolver DNSResolver) *Verifier {
	return &Verifier{resolver: resolver, signer: NewSigner()}
}

// VerifyMessage extracts and verifies all DKIM-Signature headers in a message.
// Returns the best result (first valid signature wins for pass).
func (v *Verifier) VerifyMessage(ctx context.Context, rfc822 []byte) *VerifyResultInfo {
	headers, _ := splitMessage(rfc822)

	// Find all DKIM-Signature headers.
	var sigHeaders []string
	for _, h := range headers {
		if strings.EqualFold(h.Name, "DKIM-Signature") {
			sigHeaders = append(sigHeaders, h.Value)
		}
	}

	if len(sigHeaders) == 0 {
		return &VerifyResultInfo{
			Result:      VerifyNone,
			Explanation: "no DKIM-Signature header found",
		}
	}

	// Try each signature; return first pass, or best failure.
	bestResult := &VerifyResultInfo{Result: VerifyNone, Explanation: "no valid signatures"}

	for _, sigVal := range sigHeaders {
		result := v.verifyOne(ctx, rfc822, sigVal)
		if result.Result == VerifyPass {
			return result
		}
		// Track the most specific failure.
		if result.Result != VerifyNone {
			bestResult = result
		}
	}

	return bestResult
}

func (v *Verifier) verifyOne(ctx context.Context, rfc822 []byte, sigValue string) *VerifyResultInfo {
	params := parseTagValue(sigValue)

	domain := params["d"]
	selector := params["s"]
	bh := params["bh"]
	b64sig := params["b"]
	hlist := params["h"]
	alg := params["a"]

	if domain == "" || selector == "" || bh == "" || b64sig == "" || hlist == "" {
		return &VerifyResultInfo{
			Result:      VerifyPermError,
			Explanation: "missing required DKIM fields",
		}
	}

	// Compute body hash.
	_, body := splitMessage(rfc822)
	canonBody := canonicalizeBody(body, CanonRelaxed)
	bodyHash := sha256.Sum256(canonBody)
	expectedBH := base64.StdEncoding.EncodeToString(bodyHash[:])

	if bh != expectedBH {
		return &VerifyResultInfo{
			Result: VerifyFail, Domain: domain, Selector: selector,
			Explanation: "body hash mismatch",
		}
	}

	// Fetch public key via DNS.
	keyDomain := fmt.Sprintf("%s._domainkey.%s", selector, domain)
	txts, err := v.resolver.LookupTXT(ctx, keyDomain)
	if err != nil {
		return &VerifyResultInfo{
			Result: VerifyTempError, Domain: domain, Selector: selector,
			Explanation: fmt.Sprintf("DNS lookup failed for %s: %v", keyDomain, err),
		}
	}

	pubKeyPEM := extractPublicKey(txts)
	if pubKeyPEM == "" {
		return &VerifyResultInfo{
			Result: VerifyPermError, Domain: domain, Selector: selector,
			Explanation: fmt.Sprintf("no public key found in DNS for %s", keyDomain),
		}
	}

	// Parse public key.
	block, _ := pem.Decode([]byte(pubKeyPEM))
	if block == nil {
		return &VerifyResultInfo{
			Result: VerifyPermError, Domain: domain, Selector: selector,
			Explanation: "failed to decode public key PEM",
		}
	}
	pubKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return &VerifyResultInfo{
			Result: VerifyPermError, Domain: domain, Selector: selector,
			Explanation: fmt.Sprintf("parse public key: %v", err),
		}
	}
	rsaPubKey, ok := pubKey.(*rsa.PublicKey)
	if !ok {
		return &VerifyResultInfo{
			Result: VerifyPermError, Domain: domain, Selector: selector,
			Explanation: "public key is not RSA",
		}
	}

	// Canonicalize headers and build signed data.
	signedHeaders := strings.Split(hlist, ":")
	// Re-parse the message headers properly.
	headers, _ := splitMessage(rfc822)
	canonHeaders := canonicalizeHeaders(headers, signedHeaders, CanonRelaxed)

	// Build data that was signed. Per RFC 6376, the signer signs:
	//   canonicalized_header_fields + CRLF + DKIM-Signature header (without b=)
	var hdrText strings.Builder
	for _, h := range canonHeaders {
		hdrText.WriteString(fmt.Sprintf("%s:%s\r\n", h.Name, h.Value))
	}

	// Append DKIM-Signature with b= removed.
	sigWithoutB := fmt.Sprintf("v=1; a=%s; c=%s/%s; d=%s; s=%s; h=%s; bh=%s; b=",
		alg, CanonRelaxed, CanonRelaxed, domain, selector, hlist, bh)
	hdrText.WriteString(fmt.Sprintf("DKIM-Signature:%s\r\n", sigWithoutB))
	sigData := hdrText.String()

	// Decode signature.
	sigBytes, err := base64.StdEncoding.DecodeString(b64sig)
	if err != nil {
		return &VerifyResultInfo{
			Result: VerifyPermError, Domain: domain, Selector: selector,
			Explanation: fmt.Sprintf("decode signature: %v", err),
		}
	}

	// Verify RSA signature.
	hash := sha256.Sum256([]byte(sigData))
	err = rsa.VerifyPKCS1v15(rsaPubKey, crypto.SHA256, hash[:], sigBytes)
	if err != nil {
		return &VerifyResultInfo{
			Result: VerifyFail, Domain: domain, Selector: selector,
			Explanation: fmt.Sprintf("signature verification failed: %v", err),
		}
	}

	return &VerifyResultInfo{
		Result: VerifyPass, Domain: domain, Selector: selector,
		Explanation: fmt.Sprintf("DKIM signature valid for %s", domain),
	}
}

// extractPublicKey parses a DKIM DNS TXT record and returns the public key
// in PEM format (PKIX/SPKI), or empty string if not found.
func extractPublicKey(txts []string) string {
	var dkimRecord string
	for _, txt := range txts {
		t := strings.TrimSpace(txt)
		if strings.Contains(t, "v=DKIM1") || strings.Contains(t, "p=") {
			dkimRecord = t
			break
		}
	}
	if dkimRecord == "" {
		return ""
	}

	params := parseTagValue(dkimRecord)
	p := params["p"]
	if p == "" {
		return ""
	}

	// The p= value is base64-encoded SubjectPublicKeyInfo (DER).
	der, err := base64.StdEncoding.DecodeString(p)
	if err != nil {
		// Try without padding.
		der, err = base64.RawStdEncoding.DecodeString(p)
		if err != nil {
			return ""
		}
	}

	pemBlock := &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: der,
	}
	return string(pem.EncodeToMemory(pemBlock))
}

// isDKIMSignedMessage returns true if the message has a DKIM-Signature header.
func isDKIMSignedMessage(data []byte) bool {
	return bytes.Contains(data, []byte("\nDKIM-Signature:")) ||
		bytes.Contains(data, []byte("\r\nDKIM-Signature:")) ||
		bytes.HasPrefix(data, []byte("DKIM-Signature:"))
}
