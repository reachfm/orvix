// Phase K: cross-cutting injection/fuzz-style security tests for the
// self-update protocol layer. These exist to fill genuine gaps left by
// verify_test.go, protocol_test.go, and discovery_test.go — they are not
// a re-test of anything those files already cover (see each existing
// TestRequestValidate_* / TestDiscoverRelease_* for the happy/rejection
// paths already exercised).
package selfupdate

import (
	"strings"
	"testing"
)

// pathologicalStrings is a handful of deliberately malformed/hostile
// values used to fuzz every string field that crosses the IPC boundary:
// unpaired UTF-16 surrogates encoded as WTF-8/replacement bytes, overlong
// UTF-8 sequences, embedded NUL bytes, RTL override control characters
// (used in real-world filename-spoofing attacks), emoji/astral-plane
// runes, and an extremely long string. None of these are valid semver or
// valid identifiers, so every one of them must be rejected wherever the
// field is semantically constrained (RequestedVersion, Channel) and must
// never crash, panic, or corrupt anything wherever the field is merely
// stored/forwarded (IdempotencyKey, InitiatedBy).
var pathologicalStrings = []string{
	"\xed\xa0\x80",                       // unpaired UTF-16 high surrogate, encoded as raw bytes (invalid UTF-8)
	"\xc0\xaf",                           // overlong UTF-8 encoding of '/'
	"1.0.0\x00; rm -rf /",                // embedded NUL byte
	"‮1.0.0",                             // RTL override control char
	"1.0.0\U0001F4A9",                    // version string with a trailing emoji (astral-plane rune)
	strings.Repeat("a", 100000),          // extremely long string
	"1.0.0\r\nSet-Cookie: evil=1",        // embedded CRLF (header/log injection shape)
	"1.0.0\nAuthorization: Bearer x",     // embedded newline
	"../../../../etc/passwd",             // path traversal
	"/etc/passwd",                        // absolute path
	"file:///etc/passwd",                 // arbitrary URL scheme
	"http://169.254.169.254/latest/meta", // SSRF-shaped value
}

func TestRequestValidate_RejectsPathologicalUnicodeInRequestedVersion(t *testing.T) {
	for _, v := range pathologicalStrings {
		r := Request{ProtocolVersion: ProtocolVersion, Op: OpStartInstall, IdempotencyKey: "k", RequestedVersion: v}
		if err := r.Validate(); err == nil {
			t.Errorf("expected pathological RequestedVersion %q to be rejected, got nil error", v)
		}
	}
}

func TestRequestValidate_RejectsPathologicalUnicodeInChannel(t *testing.T) {
	for _, v := range pathologicalStrings {
		r := Request{ProtocolVersion: ProtocolVersion, Op: OpCheckRelease, Channel: v}
		if err := r.Validate(); err == nil {
			t.Errorf("expected pathological Channel %q to be rejected, got nil error", v)
		}
	}
}

// IdempotencyKey has no charset restriction in Validate today — only a
// length cap — so pathological Unicode values pass Validate as long as
// they are short. This test documents that fact (it is not a bug: the
// key is never interpolated into a shell/path/URL — see protocol.go's
// package doc and orchestrator.go's fixed-argv discipline) while proving
// the length cap is actually enforced against a hostile long value, and
// that no pathological short value panics anything in Validate.
func TestRequestValidate_IdempotencyKeyPathologicalUnicodeHandledSafely(t *testing.T) {
	for _, v := range pathologicalStrings {
		key := v
		if len(key) > 128 {
			key = key[:64] // exercise the "hostile but short" case for this field specifically
		}
		r := Request{ProtocolVersion: ProtocolVersion, Op: OpStartInstall, IdempotencyKey: key, RequestedVersion: "1.0.4"}
		// Must not panic; result may legitimately be nil since
		// IdempotencyKey has no charset validation by design.
		_ = r.Validate()
	}
	// The length cap itself must still be enforced against a hostile long key.
	r := Request{ProtocolVersion: ProtocolVersion, Op: OpStartInstall, IdempotencyKey: strings.Repeat("a", 129), RequestedVersion: "1.0.4"}
	if err := r.Validate(); err == nil {
		t.Fatal("expected oversized idempotency_key to be rejected")
	}
}

func TestRequestValidate_RejectsOversizedInitiatedBy(t *testing.T) {
	r := Request{ProtocolVersion: ProtocolVersion, Op: OpStatus, InitiatedBy: strings.Repeat("x", 257)}
	if err := r.Validate(); err == nil {
		t.Fatal("expected oversized initiated_by to be rejected")
	}
}

func TestRequestValidate_PathologicalUnicodeInInitiatedByDoesNotPanic(t *testing.T) {
	for _, v := range pathologicalStrings {
		r := Request{ProtocolVersion: ProtocolVersion, Op: OpStatus, InitiatedBy: v}
		_ = r.Validate() // must not panic regardless of outcome
	}
}

func TestValidateVersionString_RejectsPathologicalUnicode(t *testing.T) {
	for _, v := range pathologicalStrings {
		if err := ValidateVersionString(v); err == nil {
			t.Errorf("expected pathological version %q to be rejected", v)
		}
	}
}
