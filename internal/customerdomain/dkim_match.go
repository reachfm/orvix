package customerdomain

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// parseDKIMTags parses a DKIM DNS TXT record ("v=DKIM1; k=rsa; p=...") into
// its tag=value pairs per RFC 6376 §3.2. Tags are separated by ";", each
// tag is "name=value" split on the FIRST "=" only (base64 values can
// legitimately contain "="), and surrounding whitespace around both the
// tag and its value is trimmed. A tag name repeated more than once is a
// malformed-record error — RFC 6376 does not permit duplicate tags, and a
// record with duplicate tags (e.g. two "p=" values) must never be treated
// as if the first (or last) one is authoritative.
func parseDKIMTags(record string) (map[string]string, error) {
	tags := make(map[string]string)
	for _, part := range strings.Split(record, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			return nil, fmt.Errorf("malformed tag %q: missing '='", part)
		}
		name := strings.TrimSpace(part[:eq])
		value := strings.TrimSpace(part[eq+1:])
		if name == "" {
			return nil, fmt.Errorf("malformed tag %q: empty tag name", part)
		}
		if _, dup := tags[name]; dup {
			return nil, fmt.Errorf("duplicate tag %q", name)
		}
		tags[name] = value
	}
	return tags, nil
}

// dkimPublicKeyBytes extracts and decodes the raw public-key bytes from a
// DKIM TXT record's "p=" tag. It returns an error, not a fabricated empty
// key, for every malformed case:
//   - the record does not parse as valid tag=value pairs (see parseDKIMTags)
//   - "p=" is entirely absent (record is missing the required public key)
//   - "p=" is present but empty — per RFC 6376 §3.6.1, an empty p= value
//     means the key has been REVOKED. This must be treated as a distinct,
//     explicit failure, never silently accepted as "no comparison needed".
//   - "p=" does not decode as valid base64 (accepts both standard and
//     unpadded/raw encodings, since some DNS providers strip trailing "="
//     padding when publishing TXT records)
func dkimPublicKeyBytes(record string) ([]byte, error) {
	tags, err := parseDKIMTags(record)
	if err != nil {
		return nil, err
	}
	p, ok := tags["p"]
	if !ok {
		return nil, fmt.Errorf("record has no p= tag")
	}
	if p == "" {
		return nil, fmt.Errorf("p= tag is empty (key revoked)")
	}
	// DKIM TXT records may have internal whitespace stripped by DNS
	// providers/publishers; base64 payload itself never legitimately
	// contains whitespace, so removing it before decoding is safe and
	// matches how real-world DKIM verifiers normalize the tag value.
	clean := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, p)
	if decoded, err := base64.StdEncoding.DecodeString(clean); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(clean); err == nil {
		return decoded, nil
	}
	return nil, fmt.Errorf("p= value is not valid base64")
}

// dkimRecordsMatch reports whether two DKIM TXT records publish the SAME
// public key, by parsing tags and comparing the decoded p= bytes — never
// by comparing the two records as raw/normalized strings. This makes the
// comparison correct in the presence of:
//   - reordered tags ("k=rsa; v=DKIM1; p=..." vs "v=DKIM1; k=rsa; p=...")
//   - harmless whitespace differences around tags/values
//   - optional valid tags present in one record but not the other (e.g.
//     "t=s" or "h=sha256" — these do not change the key material)
//
// and correctly REJECTS:
//   - a missing or duplicate p= tag in either record
//   - malformed base64 in either record
//   - an empty (revoked) p= value
//   - genuinely different key bytes (including after rotation)
//
// A non-nil error means the records could not be safely compared at all
// (malformed input on one or both sides); callers must treat that as a
// failed/unknown match, never as a pass.
func dkimRecordsMatch(observed, expected string) (bool, error) {
	observedKey, err := dkimPublicKeyBytes(observed)
	if err != nil {
		return false, fmt.Errorf("observed record: %w", err)
	}
	expectedKey, err := dkimPublicKeyBytes(expected)
	if err != nil {
		return false, fmt.Errorf("expected record: %w", err)
	}
	if len(observedKey) != len(expectedKey) {
		return false, nil
	}
	for i := range observedKey {
		if observedKey[i] != expectedKey[i] {
			return false, nil
		}
	}
	return true, nil
}

// splitDKIMTXTRecords separates the []string returned by a TXT lookup into
// individual logical DKIM records. A single DKIM record that was split
// across multiple 255-byte DNS TXT strings (RFC 1035 §3.3.14) has its
// "v=DKIM1" prefix ONLY on the first chunk; continuation chunks contain
// raw base64/tag content and never independently start with "v=dkim1".
// Multiple SEPARATE TXT resource records published at the same DKIM
// selector name — a real, invalid configuration RFC 6376 does not
// permit — each carry their own "v=DKIM1" prefix.
//
// Go's net.Resolver.LookupTXT flattens every TXT string at a name into one
// slice with no record-boundary information, so this is a best-effort
// heuristic, not a guarantee: it cannot distinguish "one record split into
// chunks that happen to start with a v=dkim1-like byte sequence" from a
// true conflict with perfect certainty. It correctly handles the common
// real-world case (one record, one or many chunks) and correctly flags
// the common misconfiguration case (multiple independent v=DKIM1 records)
// that a raw-string join would otherwise silently corrupt together.
func splitDKIMTXTRecords(chunks []string) [][]string {
	var records [][]string
	for _, c := range chunks {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(c)), "v=dkim1") && len(records) > 0 {
			records = append(records, []string{c})
			continue
		}
		if len(records) == 0 {
			records = append(records, []string{c})
			continue
		}
		last := len(records) - 1
		records[last] = append(records[last], c)
	}
	return records
}
