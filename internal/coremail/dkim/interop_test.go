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
	"crypto/sha256"
	"encoding/base64"
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
