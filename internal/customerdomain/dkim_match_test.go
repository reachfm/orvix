package customerdomain

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/orvix/orvix/internal/dnsops"
)

func mustDKIMRecord(t *testing.T, keyBytes []byte, extra ...string) string {
	t.Helper()
	tags := []string{"v=DKIM1", "k=rsa"}
	tags = append(tags, extra...)
	tags = append(tags, "p="+base64.StdEncoding.EncodeToString(keyBytes))
	return strings.Join(tags, "; ")
}

func TestDKIMRecordsMatch_ReorderedTagsStillMatch(t *testing.T) {
	key := []byte("fake-public-key-bytes-1234567890")
	b64 := base64.StdEncoding.EncodeToString(key)
	observed := "k=rsa; v=DKIM1; p=" + b64
	expected := "v=DKIM1; k=rsa; p=" + b64
	match, err := dkimRecordsMatch(observed, expected)
	if err != nil {
		t.Fatalf("dkimRecordsMatch: %v", err)
	}
	if !match {
		t.Error("expected reordered tags with the same key to match")
	}
}

func TestDKIMRecordsMatch_HarmlessWhitespaceStillMatches(t *testing.T) {
	key := []byte("another-fake-key-abcdefghijklmno")
	b64 := base64.StdEncoding.EncodeToString(key)
	observed := "v=DKIM1;   k=rsa ;p=" + b64
	expected := "v=DKIM1; k=rsa; p=" + b64
	match, err := dkimRecordsMatch(observed, expected)
	if err != nil {
		t.Fatalf("dkimRecordsMatch: %v", err)
	}
	if !match {
		t.Error("expected whitespace differences to be harmless")
	}
}

func TestDKIMRecordsMatch_OptionalExtraTagsStillMatch(t *testing.T) {
	key := []byte("yet-another-fake-key-000000000000")
	b64 := base64.StdEncoding.EncodeToString(key)
	observed := "v=DKIM1; k=rsa; t=s; h=sha256; p=" + b64
	expected := "v=DKIM1; k=rsa; p=" + b64
	match, err := dkimRecordsMatch(observed, expected)
	if err != nil {
		t.Fatalf("dkimRecordsMatch: %v", err)
	}
	if !match {
		t.Error("expected optional valid tags present in only one record to not break the match")
	}
}

func TestDKIMRecordsMatch_DifferentKeysDoNotMatch(t *testing.T) {
	observedKey := base64.StdEncoding.EncodeToString([]byte("key-one-aaaaaaaaaaaaaaaaaaaaaaaaa"))
	expectedKey := base64.StdEncoding.EncodeToString([]byte("key-two-bbbbbbbbbbbbbbbbbbbbbbbbb"))
	observed := "v=DKIM1; k=rsa; p=" + observedKey
	expected := "v=DKIM1; k=rsa; p=" + expectedKey
	match, err := dkimRecordsMatch(observed, expected)
	if err != nil {
		t.Fatalf("dkimRecordsMatch: %v", err)
	}
	if match {
		t.Error("different key bytes must never be reported as matching — this is the post-rotation case")
	}
}

func TestDKIMRecordsMatch_MissingPIsRejected(t *testing.T) {
	expected := "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString([]byte("some-key-bytes-here-padding-pad"))
	observed := "v=DKIM1; k=rsa"
	if _, err := dkimRecordsMatch(observed, expected); err == nil {
		t.Fatal("expected an error for a record with no p= tag, got nil")
	}
}

func TestDKIMRecordsMatch_DuplicatePIsRejected(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString([]byte("some-key-bytes-here-padding-pad"))
	expected := "v=DKIM1; k=rsa; p=" + valid
	observed := "v=DKIM1; k=rsa; p=" + valid + "; p=AAAA"
	if _, err := dkimRecordsMatch(observed, expected); err == nil {
		t.Fatal("expected an error for a record with a duplicate p= tag, got nil")
	}
}

func TestDKIMRecordsMatch_EmptyPIsRejectedAsRevoked(t *testing.T) {
	expected := "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString([]byte("some-key-bytes-here-padding-pad"))
	observed := "v=DKIM1; k=rsa; p="
	_, err := dkimRecordsMatch(observed, expected)
	if err == nil {
		t.Fatal("expected an error for an empty (revoked) p= tag, got nil")
	}
	if !strings.Contains(err.Error(), "revoked") {
		t.Errorf("error = %q, want it to mention 'revoked'", err.Error())
	}
}

func TestDKIMRecordsMatch_MalformedBase64IsRejected(t *testing.T) {
	expected := "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString([]byte("some-key-bytes-here-padding-pad"))
	observed := "v=DKIM1; k=rsa; p=not-valid-base64!!!"
	if _, err := dkimRecordsMatch(observed, expected); err == nil {
		t.Fatal("expected an error for malformed base64 in p=, got nil")
	}
}

func TestDKIMRecordsMatch_UnpaddedBase64Accepted(t *testing.T) {
	key := []byte("odd-length-key-for-padding-test1") // deliberately not a multiple of 3 bytes
	padded := base64.StdEncoding.EncodeToString(key)
	unpadded := base64.RawStdEncoding.EncodeToString(key)
	if strings.TrimRight(padded, "=") != unpadded {
		t.Fatalf("test setup: expected %q and %q to represent the same bytes with/without padding", padded, unpadded)
	}
	observed := "v=DKIM1; k=rsa; p=" + unpadded
	expected := "v=DKIM1; k=rsa; p=" + padded
	match, err := dkimRecordsMatch(observed, expected)
	if err != nil {
		t.Fatalf("dkimRecordsMatch: %v", err)
	}
	if !match {
		t.Error("expected padded and unpadded base64 encodings of the same key to match (some DNS providers strip padding)")
	}
}

func TestParseDKIMTags_DuplicateTagRejected(t *testing.T) {
	if _, err := parseDKIMTags("v=DKIM1; v=DKIM1; k=rsa; p=AAAA"); err == nil {
		t.Fatal("expected an error for a duplicate non-p tag, got nil")
	}
}

func TestParseDKIMTags_MalformedTagRejected(t *testing.T) {
	if _, err := parseDKIMTags("v=DKIM1; justsomejunk; p=AAAA"); err == nil {
		t.Fatal("expected an error for a tag with no '=', got nil")
	}
}

func TestSplitDKIMTXTRecords_SingleRecordSingleChunk(t *testing.T) {
	records := splitDKIMTXTRecords([]string{"v=DKIM1; k=rsa; p=AAAA"})
	if len(records) != 1 {
		t.Fatalf("logical records = %d, want 1", len(records))
	}
}

func TestSplitDKIMTXTRecords_SingleRecordMultipleChunks(t *testing.T) {
	// A single record split across the 255-byte TXT string limit: only the
	// FIRST chunk starts with v=DKIM1; continuation chunks are raw base64
	// and must never be misread as a second record.
	records := splitDKIMTXTRecords([]string{"v=DKIM1; k=rsa; p=AAAABBBB", "CCCCDDDD", "EEEEFFFF=="})
	if len(records) != 1 {
		t.Fatalf("logical records = %d, want 1 (chunked single record)", len(records))
	}
	if len(records[0]) != 3 {
		t.Fatalf("chunks in the single record = %d, want 3", len(records[0]))
	}
}

func TestSplitDKIMTXTRecords_MultipleConflictingRecordsDetected(t *testing.T) {
	// Two independent, complete DKIM records published at the same
	// selector name — each chunk starts with its own "v=DKIM1" prefix.
	records := splitDKIMTXTRecords([]string{
		"v=DKIM1; k=rsa; p=AAAA",
		"v=DKIM1; k=rsa; p=BBBB",
	})
	if len(records) != 2 {
		t.Fatalf("logical records = %d, want 2 (conflicting records must be detected, not silently concatenated)", len(records))
	}
}

func TestCheckDKIM_MultipleConflictingRecordsFailsWithReason(t *testing.T) {
	fr := dnsops.NewFakeResolver()
	fr.Set("mail._domainkey.conflict.example.com", dnsops.FakeEntry{
		TXT: []string{
			"v=DKIM1; k=rsa; p=AAAA",
			"v=DKIM1; k=rsa; p=BBBB",
		},
	})
	insp := NewDNSInspector(fr)
	result := insp.CheckDKIM(context.Background(), "conflict.example.com", "mail", "")
	if result.Status != string(DNSStatusFail) {
		t.Errorf("status = %q, want fail", result.Status)
	}
	if !strings.Contains(strings.ToLower(result.Reason), "conflict") && !strings.Contains(strings.ToLower(result.Reason), "multiple") {
		t.Errorf("reason = %q, want it to mention multiple/conflicting records", result.Reason)
	}
}

func TestCheckDKIM_RevokedKeyFailsEvenWithoutExpectedValue(t *testing.T) {
	fr := dnsops.NewFakeResolver()
	fr.Set("mail._domainkey.revoked.example.com", dnsops.FakeEntry{
		TXT: []string{"v=DKIM1; k=rsa; p="},
	})
	insp := NewDNSInspector(fr)
	result := insp.CheckDKIM(context.Background(), "revoked.example.com", "mail", "")
	if result.Status != string(DNSStatusFail) {
		t.Errorf("status = %q, want fail for a revoked (empty p=) key even with no expected value to compare against", result.Status)
	}
}

func TestCheckDKIM_RotationChangesExpectedKeyMismatchFails(t *testing.T) {
	oldKey := base64.StdEncoding.EncodeToString([]byte("old-key-bytes-aaaaaaaaaaaaaaaaaaa"))
	newKey := base64.StdEncoding.EncodeToString([]byte("new-key-bytes-bbbbbbbbbbbbbbbbbbb"))
	fr := dnsops.NewFakeResolver()
	// DNS still serves the OLD key — simulates "rotated in the DB but not
	// yet propagated/republished", which must FAIL, not pass.
	fr.Set("mail._domainkey.rotated.example.com", dnsops.FakeEntry{
		TXT: []string{"v=DKIM1; k=rsa; p=" + oldKey},
	})
	insp := NewDNSInspector(fr)
	result := insp.CheckDKIM(context.Background(), "rotated.example.com", "mail", "v=DKIM1; k=rsa; p="+newKey)
	if result.Status != string(DNSStatusFail) {
		t.Errorf("status = %q, want fail (published key is stale after rotation)", result.Status)
	}
}
