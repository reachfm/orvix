package dkim

// Golden RFC 6376 §3.4.2 header-canonicalization regression tests,
// added by the same audit that found and fixed the body-CRLF bug.
// Two real gaps were found and fixed here:
//
//  1. canonicalizeHeaders never lowercased the header field name for
//     relaxed canonicalization (RFC 6376 §3.4.2: "Convert all header
//     field names ... to lowercase") — a comment claimed this was
//     "already done via Name field" but it was not.
//  2. parseHeaders had no RFC 5322 folding support at all: a wrapped
//     header's continuation line (beginning with SP/HTAB, no colon)
//     was silently DROPPED rather than unfolded and appended to the
//     preceding header's value.

import (
	"testing"
)

func TestCanonicalizeHeaders_Golden_NameIsLowercased(t *testing.T) {
	headers := []header{{Name: "Subject", Value: "Hello"}, {Name: "FROM", Value: "a@b.com"}}
	got := canonicalizeHeaders(headers, []string{"Subject", "From"}, CanonRelaxed)
	if len(got) != 2 {
		t.Fatalf("expected 2 headers, got %d", len(got))
	}
	if got[0].Name != "subject" {
		t.Fatalf("expected lowercased name 'subject', got %q", got[0].Name)
	}
	if got[1].Name != "from" {
		t.Fatalf("expected lowercased name 'from', got %q", got[1].Name)
	}
}

func TestCanonicalizeHeaders_Golden_ValueWhitespaceCollapsedAndTrimmed(t *testing.T) {
	headers := []header{{Name: "Subject", Value: "Hello   World\t\tagain"}}
	got := canonicalizeHeaders(headers, []string{"Subject"}, CanonRelaxed)
	want := "Hello World again"
	if got[0].Value != want {
		t.Fatalf("got=%q want=%q", got[0].Value, want)
	}
}

func TestParseHeaders_Golden_FoldedContinuationLineIsUnfoldedNotDropped(t *testing.T) {
	// A Subject wrapped across two physical lines per RFC 5322
	// folding: the second line begins with a single SP.
	raw := []byte("Subject: This is a very long subject that\r\n continues on a folded line\r\nFrom: sender@orvix.email\r\n")
	headers := parseHeaders(raw)

	var subject, from string
	for _, h := range headers {
		switch h.Name {
		case "Subject":
			subject = h.Value
		case "From":
			from = h.Value
		}
	}
	if from != "sender@orvix.email" {
		t.Fatalf("From header missing/wrong: got %q", from)
	}
	// The fold's content must be present — proving it was appended,
	// not silently dropped (the pre-fix behavior).
	wantSubstr := "continues on a folded line"
	if !containsSubstring(subject, wantSubstr) {
		t.Fatalf("folded continuation line was dropped: Subject=%q, expected it to contain %q", subject, wantSubstr)
	}
	// After relaxed canonicalization the fold's CRLF disappears and
	// internal whitespace collapses to single spaces.
	canon := canonicalizeHeaders(headers, []string{"Subject"}, CanonRelaxed)
	wantCanon := "This is a very long subject that continues on a folded line"
	if canon[0].Value != wantCanon {
		t.Fatalf("canonicalized folded subject: got=%q want=%q", canon[0].Value, wantCanon)
	}
}

func TestParseHeaders_Golden_MultipleFoldedContinuationLines(t *testing.T) {
	raw := []byte("To: recipient@example.com,\r\n second@example.com,\r\n\tthird@example.com\r\n")
	headers := parseHeaders(raw)
	if len(headers) != 1 || headers[0].Name != "To" {
		t.Fatalf("expected exactly one To header, got %+v", headers)
	}
	canon := canonicalizeHeaders(headers, []string{"To"}, CanonRelaxed)
	want := "recipient@example.com, second@example.com, third@example.com"
	if canon[0].Value != want {
		t.Fatalf("multi-fold To header: got=%q want=%q", canon[0].Value, want)
	}
}

func TestParseHeaders_Golden_LFOnlyHeadersAlsoUnfold(t *testing.T) {
	// The same folding support must work for LF-only stored headers,
	// consistent with the body canonicalizer's line-ending tolerance.
	raw := []byte("Subject: wrapped\n across LF-only\nFrom: sender@orvix.email\n")
	headers := parseHeaders(raw)
	var subject string
	for _, h := range headers {
		if h.Name == "Subject" {
			subject = h.Value
		}
	}
	canon := canonicalizeHeaders([]header{{Name: "Subject", Value: subject}}, []string{"Subject"}, CanonRelaxed)
	want := "wrapped across LF-only"
	if canon[0].Value != want {
		t.Fatalf("LF-only folded subject: got=%q want=%q", canon[0].Value, want)
	}
}

func TestCanonicalizeHeaders_Golden_DKIMSignatureHeaderWithEmptyB(t *testing.T) {
	// buildSignatureData appends a synthetic DKIM-Signature header with
	// b= empty for the signing computation itself (RFC 6376 §3.7):
	// confirm canonicalizeHeaderValue handles this shape (trailing
	// "b=" with nothing after it) without truncating or erroring.
	value := "v=1; a=rsa-sha256; c=relaxed/relaxed; d=orvix.email; s=orvix; h=from:to; bh=abc123=; b="
	got := canonicalizeHeaderValue(value)
	if got == "" {
		t.Fatal("expected non-empty canonicalized DKIM-Signature value")
	}
	if got[len(got)-1] != '=' {
		t.Fatalf("expected value to still end with the empty b= tag's '=', got %q", got)
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
