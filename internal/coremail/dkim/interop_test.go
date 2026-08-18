package dkim

// Independent DKIM body-hash interoperability test.
//
// This test deliberately does NOT call production's canonicalizeBody
// to compute the "expected" hash — doing so is exactly how the
// original CRLF/LF bug escaped every previous test (signer and
// verifier shared one buggy function and agreed with each other while
// disagreeing with Gmail). independentRelaxedBodyHash below is a
// second, independently-written implementation of RFC 6376 §3.4.4
// using a different algorithmic approach (regex-driven line
// normalization instead of a byte-scanning state machine), so a bug
// in one is unlikely to be reproduced identically in the other.

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"regexp"
	"strings"
	"testing"
)

var (
	interopWSPRunRe    = regexp.MustCompile(`[ \t]+`)
	interopLineSplitRe = regexp.MustCompile(`\r\n|\r|\n`)
)

// independentRelaxedBodyHash re-derives the RFC 6376 §3.4.4 relaxed
// body hash from scratch, via regexp-based line splitting and
// whitespace collapsing rather than the byte-scanner
// canonicalizeBody/splitBodyLines/relaxWSP production uses.
func independentRelaxedBodyHash(body []byte) string {
	lines := interopLineSplitRe.Split(string(body), -1)
	// A regexp Split on a body ending in a line terminator yields a
	// trailing empty element representing "after the last terminator" —
	// strip it so line count matches actual logical lines, mirroring
	// how the production splitter never emits a phantom final line for
	// content that just ends exactly on a terminator.
	if len(lines) > 0 && lines[len(lines)-1] == "" && len(body) > 0 {
		last := body[len(body)-1]
		if last == '\n' || last == '\r' {
			lines = lines[:len(lines)-1]
		}
	}
	for i, l := range lines {
		l = interopWSPRunRe.ReplaceAllString(l, " ")
		lines[i] = strings.TrimRight(l, " ")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var canon string
	if len(lines) > 0 {
		canon = strings.Join(lines, "\r\n") + "\r\n"
	}
	sum := sha256.Sum256([]byte(canon))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func TestDKIM_Interop_SignedMessageBodyHashMatchesIndependentOracle(t *testing.T) {
	cases := []struct {
		name string
		body []byte
	}{
		{"simple LF body", []byte("Hello there.\nSecond line.\n")},
		{"CRLF body with trailing WSP", []byte("Hello   \r\nWorld\t\r\n\r\n")},
		{"live 9dac713 body", dkimLiveTest9dac713Body},
		{"empty body", []byte{}},
		{"utf8 body", []byte("héllo wörld — 日本語\n")},
	}

	signer := NewSigner()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := []byte("From: sender@orvix.email\r\nTo: recipient@example.com\r\nSubject: Interop test\r\n\r\n")
			msg = append(msg, tc.body...)

			result, err := signer.Sign(msg, HeaderSet{
				Domain:        "orvix.email",
				Selector:      "orvix",
				PrivateKeyPEM: testKey(t),
			})
			if err != nil {
				t.Fatalf("sign: %v", err)
			}

			emittedBH := extractTag(result.Signature, "bh")
			if emittedBH == "" {
				t.Fatalf("bh tag not found in signature %q", result.Signature)
			}
			wantBH := independentRelaxedBodyHash(tc.body)
			if emittedBH != wantBH {
				t.Fatalf("emitted bh=%s does not match independently-computed RFC 6376 body hash %s for body %q", emittedBH, wantBH, tc.body)
			}
		})
	}
}

var interopHeaderLineRe = regexp.MustCompile(`(?m)^([^:\r\n]+):[ \t]?(.*)$`)

// independentRelaxedHeaderCanon re-derives RFC 6376 §3.4.2 relaxed
// header-value canonicalization independently of
// canonicalizeHeaderValue: unfold via regex, collapse WSP via regex,
// trim — a different code path from the byte-scanner production uses.
func independentRelaxedHeaderCanon(value string) string {
	unfolded := interopLineSplitRe.ReplaceAllString(value, " ")
	collapsed := interopWSPRunRe.ReplaceAllString(unfolded, " ")
	return strings.TrimSpace(collapsed)
}

// verifyRSASignatureIndependently re-derives the entire DKIM header
// hash input from scratch — independent header/value extraction
// (regex, not splitMessage/parseHeaders), independent relaxed
// canonicalization (independentRelaxedHeaderCanon, not
// canonicalizeHeaderValue), and an independently-constructed
// DKIM-Signature hashing field (built inline here, NOT via
// dkimSignatureFieldForHashing) — then verifies the RSA signature
// against the given private key's own public half. This is the
// mandatory "does not call production canonicalization" oracle: a
// signer and verifier that only ever check themselves proved
// insufficient once already (the live e790ac5 failure).
func verifyRSASignatureIndependently(t *testing.T, rfc822 []byte, sigValue string, privateKeyPEM string) bool {
	t.Helper()

	headerBlockEnd := strings.Index(string(rfc822), "\r\n\r\n")
	if headerBlockEnd < 0 {
		t.Fatal("no header/body separator found")
	}
	headerBlock := string(rfc822[:headerBlockEnd])

	type hv struct{ name, value string }
	var all []hv
	for _, m := range interopHeaderLineRe.FindAllStringSubmatch(headerBlock, -1) {
		all = append(all, hv{name: m[1], value: m[2]})
	}

	hlist := strings.Split(extractTag(sigValue, "h"), ":")
	// Bottom-up instance selection, independently re-derived (not a
	// call into canonicalizeHeaders).
	byName := map[string][]hv{}
	for _, h := range all {
		key := strings.ToLower(h.name)
		byName[key] = append(byName[key], h)
	}
	nextIdx := map[string]int{}
	for k, v := range byName {
		nextIdx[k] = len(v) - 1
	}

	var sb strings.Builder
	for _, name := range hlist {
		key := strings.ToLower(name)
		idx, ok := nextIdx[key]
		if !ok || idx < 0 {
			continue
		}
		h := byName[key][idx]
		nextIdx[key] = idx - 1
		sb.WriteString(key)
		sb.WriteString(":")
		sb.WriteString(independentRelaxedHeaderCanon(h.value))
		sb.WriteString("\r\n")
	}

	// The DKIM-Signature field itself, b= emptied, relaxed-canonicalized,
	// NO trailing CRLF — constructed independently here rather than via
	// dkimSignatureFieldForHashing.
	bTag := "b=" + extractTag(sigValue, "b")
	sigWithoutB := strings.TrimSuffix(sigValue, bTag) + "b="
	sb.WriteString("dkim-signature:")
	sb.WriteString(independentRelaxedHeaderCanon(sigWithoutB))

	sigData := sb.String()

	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		t.Fatal("decode private key PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("key is not RSA")
	}

	sigBytes, err := base64.StdEncoding.DecodeString(extractTag(sigValue, "b"))
	if err != nil {
		t.Fatalf("decode b= signature: %v", err)
	}

	hash := sha256.Sum256([]byte(sigData))
	err = rsa.VerifyPKCS1v15(&rsaKey.PublicKey, crypto.SHA256, hash[:], sigBytes)
	return err == nil
}
