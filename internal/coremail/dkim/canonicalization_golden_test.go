package dkim

// Golden RFC 6376 §3.4 body-canonicalization regression tests.
//
// This file exists because of a real, externally-confirmed
// interoperability failure: a live message signed by Orvix reached
// Gmail with a present DKIM-Signature (d=orvix.email, s=orvix) that
// Gmail rejected as "dkim=neutral (body hash did not verify)". The
// root cause was that canonicalizeBody joined canonical lines with a
// bare LF ("\n") instead of the CRLF ("\r\n") RFC 6376 requires,
// because it treated '\r' as ordinary trailing whitespace to trim
// rather than part of the line terminator.
//
// Every expected byte sequence and hash below is hand-derived from
// RFC 6376 §3.4.3/§3.4.4 directly — NONE of them are computed by
// calling canonicalizeBody itself. That is the whole point: the
// previous "cryptographic DKIM verification" tests passed because the
// signer and this package's own verifier agreed with each other while
// both disagreed with RFC 6376 and with Gmail. A signer that is only
// ever checked against its own verifier is not evidence of anything.

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func sha256B64(b []byte) string {
	sum := sha256.Sum256(b)
	return base64.StdEncoding.EncodeToString(sum[:])
}

// ── A-J: independently hand-derived canonical byte sequences ───────

func TestCanonicalizeBody_Golden_RelaxedCRLFInput(t *testing.T) {
	// "A". RFC 6376 §3.4.4 relaxed on an already-CRLF body: internal
	// WSP runs collapse to a single SP, trailing WSP on each line is
	// stripped, trailing blank lines are removed, and the result ends
	// in exactly one CRLF.
	input := []byte(" C \r\nD \t E\r\n\r\n\r\n")
	got := canonicalizeBody(input, CanonRelaxed)
	want := []byte(" C\r\nD E\r\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("relaxed CRLF input:\n got=%q\nwant=%q", got, want)
	}
}

func TestCanonicalizeBody_Golden_RelaxedLFOnlyInputMatchesCRLFOutput(t *testing.T) {
	// "B". The exact defect: a message stored/composed with bare LF
	// line endings must STILL canonicalize to the same RFC CRLF bytes
	// a CRLF-native message would — the wire/storage line-ending
	// convention must never leak into the canonical form.
	crlfInput := []byte(" C \r\nD \t E\r\n\r\n\r\n")
	lfInput := []byte(" C \nD \t E\n\n\n")
	gotCRLF := canonicalizeBody(crlfInput, CanonRelaxed)
	gotLF := canonicalizeBody(lfInput, CanonRelaxed)
	if !bytes.Equal(gotCRLF, gotLF) {
		t.Fatalf("LF-only input must canonicalize identically to CRLF input:\n crlf=%q\n lf=%q", gotCRLF, gotLF)
	}
	want := []byte(" C\r\nD E\r\n")
	if !bytes.Equal(gotLF, want) {
		t.Fatalf("LF-only input canonicalization: got=%q want=%q", gotLF, want)
	}
}

func TestCanonicalizeBody_Golden_CROnlyInput(t *testing.T) {
	// "C". A lone CR (no following LF) is still a valid line
	// terminator for this canonicalizer's normalization pass.
	input := []byte("foo\rbar\r")
	got := canonicalizeBody(input, CanonRelaxed)
	want := []byte("foo\r\nbar\r\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("CR-only input: got=%q want=%q", got, want)
	}
}

func TestCanonicalizeBody_Golden_TrailingSpacesAndTabs(t *testing.T) {
	// "D". Trailing SP/HTAB removed per line, under relaxed.
	input := []byte("hello  \t \r\nworld\t\r\n")
	got := canonicalizeBody(input, CanonRelaxed)
	want := []byte("hello\r\nworld\r\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("trailing WSP: got=%q want=%q", got, want)
	}
}

func TestCanonicalizeBody_Golden_RepeatedInternalWSPCollapses(t *testing.T) {
	// "E". Repeated internal SP/HTAB collapse to exactly one SP.
	input := []byte("a    b\t\t\tc \t d\r\n")
	got := canonicalizeBody(input, CanonRelaxed)
	want := []byte("a b c d\r\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("internal WSP collapse: got=%q want=%q", got, want)
	}
}

func TestCanonicalizeBody_Golden_MultipleTrailingBlankLines(t *testing.T) {
	// "F". Any number of trailing blank lines canonicalize away
	// entirely, leaving exactly one terminating CRLF.
	input := []byte("only line\r\n\r\n\r\n\r\n\r\n")
	got := canonicalizeBody(input, CanonRelaxed)
	want := []byte("only line\r\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("trailing blank lines: got=%q want=%q", got, want)
	}
}

func TestCanonicalizeBody_Golden_EmptyBody(t *testing.T) {
	// "G". RFC 6376 §3.4.4: an empty (or all-blank) body canonicalizes
	// under "relaxed" to the empty string; under "simple" (§3.4.3) to
	// a single CRLF — never a zero-length result for simple.
	if got := canonicalizeBody([]byte{}, CanonRelaxed); len(got) != 0 {
		t.Fatalf("relaxed empty body: got=%q, want zero-length", got)
	}
	if got := canonicalizeBody([]byte("\r\n\r\n\r\n"), CanonRelaxed); len(got) != 0 {
		t.Fatalf("relaxed all-blank body: got=%q, want zero-length", got)
	}
	want := []byte("\r\n")
	if got := canonicalizeBody([]byte{}, CanonSimple); !bytes.Equal(got, want) {
		t.Fatalf("simple empty body: got=%q want=%q", got, want)
	}
	if got := canonicalizeBody([]byte("\r\n\r\n"), CanonSimple); !bytes.Equal(got, want) {
		t.Fatalf("simple all-blank body: got=%q want=%q", got, want)
	}
}

func TestCanonicalizeBody_Golden_OneLineNoFinalNewline(t *testing.T) {
	// "H". A body with no trailing line terminator at all still gets
	// the RFC-required terminating CRLF appended.
	input := []byte("no trailing newline")
	got := canonicalizeBody(input, CanonRelaxed)
	want := []byte("no trailing newline\r\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("no final newline: got=%q want=%q", got, want)
	}
}

func TestCanonicalizeBody_Golden_AlreadyEndsCRLF(t *testing.T) {
	// "I". A body that already ends in exactly one CRLF must not gain
	// a second, spurious trailing CRLF.
	input := []byte("exactly one terminator\r\n")
	got := canonicalizeBody(input, CanonRelaxed)
	want := []byte("exactly one terminator\r\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("already-CRLF body: got=%q want=%q", got, want)
	}
}

func TestCanonicalizeBody_Golden_UTF8Body(t *testing.T) {
	// "J". Multi-byte UTF-8 sequences must pass through untouched —
	// WSP detection operates on raw bytes 0x20/0x09 only, and no UTF-8
	// continuation byte can collide with those values.
	input := []byte("héllo wörld — 日本語 テスト  \r\n")
	got := canonicalizeBody(input, CanonRelaxed)
	want := []byte("héllo wörld — 日本語 テスト\r\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("UTF-8 body: got=%q want=%q", got, want)
	}
}

func TestCanonicalizeBody_Golden_Simple_PreservesContentExceptTrailingBlankLines(t *testing.T) {
	// Simple canonicalization must NOT collapse internal whitespace —
	// only the RFC-defined trailing-blank-line removal and the
	// terminating CRLF apply.
	input := []byte("keep   this   spacing\t\r\n\r\n\r\n")
	got := canonicalizeBody(input, CanonSimple)
	want := []byte("keep   this   spacing\t\r\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("simple canonicalization: got=%q want=%q", got, want)
	}
}

// ── Live external-evidence regression: DKIM LIVE TEST 9dac713 ──────

// dkimLiveTest9dac713Body is the exact plaintext body of the real
// message sent from salma@orvix.email that Gmail rejected with
// "dkim=neutral (body hash did not verify)" after the outbound-DKIM
// wiring fix (9dac713) but before this canonicalization fix. Trailing
// spaces are preserved verbatim — they are part of the forensic
// evidence and part of what relaxed canonicalization must strip.
var dkimLiveTest9dac713Body = []byte("Dear Mostafa \n\nthis is DKIM LIVE TEST 9dac713 \n\nRegards \nSalma Mostafa")

const (
	// The INCORRECT body hash Orvix emitted with the pre-fix LF-only
	// canonicalizer — this is what Gmail received and rejected.
	dkimLiveTest9dac713WrongHash = "gGTh1gr2OuvXTseuoRD5fEl3697qVzC7wyZo+zRYomQ="
	// The CORRECT RFC 6376 relaxed body hash for the same body,
	// independently confirmed against Gmail's own verifier.
	dkimLiveTest9dac713CorrectHash = "6bS4cEx1d/bFKs7x0g1a90aXpUvwOWfGgPq/8klGRCc="
)

func TestCanonicalizeBody_LiveRegression_9dac713_ProducesCorrectHash(t *testing.T) {
	canon := canonicalizeBody(dkimLiveTest9dac713Body, CanonRelaxed)
	got := sha256B64(canon)
	if got != dkimLiveTest9dac713CorrectHash {
		t.Fatalf("live DKIM regression body hash mismatch:\n got=%s\nwant=%s (Gmail-verified correct value)", got, dkimLiveTest9dac713CorrectHash)
	}
}

func TestCanonicalizeBody_LiveRegression_9dac713_NoLongerProducesWrongHash(t *testing.T) {
	canon := canonicalizeBody(dkimLiveTest9dac713Body, CanonRelaxed)
	got := sha256B64(canon)
	if got == dkimLiveTest9dac713WrongHash {
		t.Fatalf("regression: canonicalizeBody still reproduces the OLD, RFC-incorrect LF-only hash %s that Gmail rejected", dkimLiveTest9dac713WrongHash)
	}
}

// oldBuggyLFOnlyCanonicalize reproduces the PRE-FIX canonicalizeBody
// behavior exactly (bare-LF join, '\r' treated as trimmable trailing
// whitespace) — kept ONLY as a fixture proving what the bug actually
// did, never called by production code.
func oldBuggyLFOnlyCanonicalize(body []byte) []byte {
	lines := bytes.Split(body, []byte("\n"))
	for i, line := range lines {
		lines[i] = bytes.TrimRight(line, " \t\r")
	}
	for len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil
	}
	result := bytes.Join(lines, []byte("\n"))
	result = append(result, '\n')
	return result
}

func TestCanonicalizeBody_LiveRegression_OldBuggyFixtureReproducesWrongHash(t *testing.T) {
	// Proves the fixture body above is genuinely the one that produced
	// Gmail's rejection — the OLD buggy algorithm reproduces the exact
	// wrong hash Gmail saw, confirming this is the real live body, not
	// an approximation.
	old := oldBuggyLFOnlyCanonicalize(dkimLiveTest9dac713Body)
	got := sha256B64(old)
	if got != dkimLiveTest9dac713WrongHash {
		t.Fatalf("fixture calibration failed: old buggy canonicalizer produced %s, want the Gmail-observed wrong hash %s — the body fixture does not match the live forensic evidence", got, dkimLiveTest9dac713WrongHash)
	}
}
