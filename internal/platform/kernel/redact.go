package kernel

import (
	"regexp"
	"strings"
)

// secretFieldPatterns mirrors internal/api/handlers/settings/store.go's
// forbiddenPatterns list — the same substrings that must never be
// persisted, logged, or returned as a plain value anywhere in the
// platform control plane. Kept as a case-insensitive substring match for
// the same reason the settings store uses one: new secret-shaped keys
// (e.g. "smtp_relay_password") must be caught without an explicit entry.
var secretFieldPatterns = []string{
	"password", "secret", "private_key", "api_key", "apikey",
	"token", "credential", "dsn", "bearer", "authorization",
}

// IsSecretField reports whether a field name looks like it holds secret
// material, for audit-event and log redaction call sites that only have a
// field name (not a typed struct) to work with — e.g. a generic
// map[string]any diff being written to an audit row.
func IsSecretField(fieldName string) bool {
	lower := strings.ToLower(fieldName)
	for _, p := range secretFieldPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// RedactMap returns a copy of m with every secret-shaped value replaced by
// "[REDACTED]". Used before writing an audit event's metadata or logging a
// request body — the copy is intentional so the caller's original map
// (which may still be needed to actually apply an update) is untouched.
func RedactMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if IsSecretField(k) {
			out[k] = "[REDACTED]"
			continue
		}
		out[k] = v
	}
	return out
}

// emailPattern is used only to redact an email's local part in
// low-cardinality contexts (metric labels, structured log fields) where a
// full address would otherwise leak PII into a system with unbounded
// label/field cardinality — see the "no unbounded labels containing email
// addresses" cross-cutting rule.
var emailPattern = regexp.MustCompile(`^([^@]+)(@.+)$`)

// RedactEmailLocalPart turns "alice@example.com" into "a***@example.com".
// The domain is preserved because domain-level aggregation (e.g. "how many
// distinct domains hit this rate limit") is a legitimate, non-PII metric
// dimension; the local part is not.
func RedactEmailLocalPart(email string) string {
	m := emailPattern.FindStringSubmatch(email)
	if m == nil {
		return "[REDACTED]"
	}
	local := m[1]
	if len(local) <= 1 {
		return local + "***" + m[2]
	}
	return local[:1] + "***" + m[2]
}
