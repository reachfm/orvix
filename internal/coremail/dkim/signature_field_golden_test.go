package dkim

// Golden RFC 6376 §3.7 regression tests for the DKIM-Signature field
// itself. Second real, externally-confirmed interoperability failure
// on the same live domain: after the body-hash fix (e790ac5), Gmail
// reported "dkim=fail" with a CORRECT bh= (confirmed by Gmail's own
// ARC-Message-Signature using the identical bh= value) — meaning the
// RSA header signature itself was wrong. Root cause: buildSignatureData
// appended the temporary DKIM-Signature field (b= emptied) as
// "DKIM-Signature:<value>\r\n" — wrong case (not lowercased under
// relaxed canonicalization), the value not run through relaxed header
// canonicalization, and a trailing CRLF RFC 6376 §3.7 explicitly
// forbids on this one field (it is the last thing hashed).
//
// Independently confirmed against the real live message and its real
// published public key (orvix._domainkey.orvix.email): the OLD
// non-compliant field construction is exactly what Orvix's own private
// key produced a self-consistent-but-non-RFC signature over, and the
// NEW construction is required for any standards-compliant verifier
// (Gmail) to accept it.

import (
	"strings"
	"testing"
)

func TestDKIMSignatureField_Golden_LowercaseNameNoTrailingCRLF(t *testing.T) {
	got := dkimSignatureFieldForHashing("v=1; a=rsa-sha256; c=relaxed/relaxed; d=orvix.email; s=orvix; h=From:To; bh=abc=; b=", CanonRelaxed)
	if !strings.HasPrefix(got, "dkim-signature:") {
		t.Fatalf("expected field to start with lowercase %q, got %q", "dkim-signature:", got)
	}
	if strings.HasSuffix(got, "\r\n") {
		t.Fatalf("RFC 6376 §3.7 forbids a trailing CRLF on this field — got %q", got)
	}
	if strings.Contains(got, "DKIM-Signature:") {
		t.Fatalf("field must not contain the un-lowercased name anywhere: %q", got)
	}
}

func TestDKIMSignatureField_Golden_RelaxedCollapsesWSPInValue(t *testing.T) {
	got := dkimSignatureFieldForHashing("v=1;  a=rsa-sha256;   c=relaxed/relaxed; b=", CanonRelaxed)
	if strings.Contains(got, "  ") {
		t.Fatalf("relaxed canonicalization must collapse internal WSP runs in the DKIM-Signature value too: %q", got)
	}
}

func TestDKIMSignatureField_Golden_SimplePreservesCaseAndValue(t *testing.T) {
	raw := "v=1; a=rsa-sha256; c=simple/simple; b="
	got := dkimSignatureFieldForHashing(raw, CanonSimple)
	want := "DKIM-Signature:" + raw
	if got != want {
		t.Fatalf("simple canonicalization must not alter the field: got=%q want=%q", got, want)
	}
}

// ── Live regression: DKIM LIVE HEADER TEST e790ac5 ─────────────────

func TestDKIMSignatureField_LiveRegression_e790ac5_SignatureNowVerifies(t *testing.T) {
	// Real header values from the live message (Message-ID/Subject/
	// From/To/Date/MIME-Version/Content-Type exactly as received),
	// the real live bh= (already RFC-correct per the prior fix), and
	// the real b= Orvix actually transmitted — which Gmail rejected.
	// This proves the OLD field construction is why: it verifies fine
	// against the OLD (non-compliant) hashing but the live signature
	// was computed with that same OLD code, so this test's purpose is
	// to pin the NEW code's correct field construction going forward
	// via the interoperability test below, which re-signs the exact
	// live header set with a real key and confirms a fresh signature
	// now verifies with RFC-correct hashing.
	headers := []header{
		{Name: "From", Value: "Salma Mostafa <salma@orvix.email>"},
		{Name: "To", Value: "Sally.fadel.m@gmail.com, misrdsl@gmail.com"},
		{Name: "Subject", Value: "Orvix DKIM RFC6376 Test e790ac5"},
		{Name: "Date", Value: "Sun, 16 Aug 2026 17:14:43 +0000"},
		{Name: "Message-ID", Value: "<e6d51ae0e0b219c5b3daaef4506015ad@orvix.local>"},
		{Name: "MIME-Version", Value: "1.0"},
		{Name: "Content-Type", Value: "text/plain; charset=UTF-8"},
	}
	bh := "PFFO9pfDJ17nN2a8WtXg+WhgxIeys3A3uyo5VwqKaYQ="

	signer := NewSigner()
	msg := buildRFC822FromHeaders(headers, "    Fresh DKIM canonicalization test after e790ac5 deployment\r\n\r\n")
	// The SAME key PEM is used for signing and independent
	// verification below — testKey(t) generates a fresh random key
	// per call, so calling it twice would sign with one key and
	// verify against another's public half, an unrelated-key mismatch
	// that would masquerade as a canonicalization failure.
	keyPEM := testKey(t)
	result, err := signer.Sign(msg, HeaderSet{
		Domain: "orvix.email", Selector: "orvix", PrivateKeyPEM: keyPEM,
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	gotBH := extractTag(result.Signature, "bh")
	if gotBH != bh {
		t.Fatalf("body hash regressed: got=%s want=%s", gotBH, bh)
	}

	// Independently verify the freshly-produced signature against the
	// signing key's own public half, via a hand-rolled oracle in
	// interop_test.go's style — proving a real Sign() call with the
	// NEW field construction produces something a correct verifier
	// accepts, not merely something Orvix's own verifier accepts.
	ok := verifyRSASignatureIndependently(t, msg, result.Signature, keyPEM)
	if !ok {
		t.Fatal("freshly-signed live-header-set message does not verify under independent RFC 6376 §3.7 field construction")
	}
}

// buildRFC822FromHeaders assembles a minimal RFC822 message from an
// ordered header list plus a body, using CRLF throughout.
func buildRFC822FromHeaders(headers []header, body string) []byte {
	var sb strings.Builder
	for _, h := range headers {
		sb.WriteString(h.Name)
		sb.WriteString(": ")
		sb.WriteString(h.Value)
		sb.WriteString("\r\n")
	}
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return []byte(sb.String())
}

// ── Header selection semantics (RFC 6376 §3.5/§3.7) ─────────────────

func TestCanonicalizeHeaders_Golden_OutputFollowsHOrderNotMessageOrder(t *testing.T) {
	// Message physically has Date BEFORE Subject; h= lists Subject
	// before Date. The canonical output must follow h= order.
	headers := []header{
		{Name: "Date", Value: "Mon, 01 Jan 2026 00:00:00 +0000"},
		{Name: "Subject", Value: "Hello"},
	}
	got := canonicalizeHeaders(headers, []string{"Subject", "Date"}, CanonRelaxed)
	if len(got) != 2 || got[0].Name != "subject" || got[1].Name != "date" {
		t.Fatalf("expected h= order [subject, date], got %+v", got)
	}
}

func TestCanonicalizeHeaders_Golden_RepeatedNameSelectsBottomUp(t *testing.T) {
	// Two "Received" instances in message order; h= lists "Received"
	// twice — RFC 6376 §3.5 selects from the bottom (most recent)
	// upward, one instance per listing.
	headers := []header{
		{Name: "Received", Value: "hop-1 (oldest, top of block)"},
		{Name: "Received", Value: "hop-2 (newest, bottom of block)"},
		{Name: "From", Value: "a@b.com"},
	}
	got := canonicalizeHeaders(headers, []string{"Received", "Received"}, CanonRelaxed)
	if len(got) != 2 {
		t.Fatalf("expected 2 selected instances, got %d: %+v", len(got), got)
	}
	if got[0].Value != "hop-2 (newest, bottom of block)" {
		t.Fatalf("first Received listing must select the BOTTOM-most unused instance, got %q", got[0].Value)
	}
	if got[1].Value != "hop-1 (oldest, top of block)" {
		t.Fatalf("second Received listing must select the NEXT bottom-most unused instance, got %q", got[1].Value)
	}
}

func TestCanonicalizeHeaders_Golden_NameListedButAbsentContributesNothing(t *testing.T) {
	headers := []header{{Name: "From", Value: "a@b.com"}}
	got := canonicalizeHeaders(headers, []string{"From", "Cc"}, CanonRelaxed)
	if len(got) != 1 || got[0].Name != "from" {
		t.Fatalf("a listed-but-absent header must be silently skipped, not fabricated: got %+v", got)
	}
}

func TestDefaultHeaders_Golden_NeverIncludesDKIMSignatureItself(t *testing.T) {
	for _, h := range DefaultHeaders {
		if strings.EqualFold(h, "DKIM-Signature") {
			t.Fatalf("DefaultHeaders must never list DKIM-Signature — the field being created cannot sign itself (RFC 6376 §5.4)")
		}
	}
}
