// Package msgid resolves the id-right (domain) portion of an
// RFC 5322 Message-ID header to a real, deployment-owned Internet
// hostname instead of the private pseudo-domain "orvix.local".
//
// This package intentionally has zero dependencies on the rest of
// the codebase (stdlib only) so it can be imported by both the
// webmail API handlers and the rules engine (forwarding/vacation)
// without adding a new coupling edge between those packages.
package msgid

import (
	"fmt"
	"regexp"
	"strings"
)

// validHostnameRE matches a dotted Internet-style hostname: one or
// more labels of 1-63 alphanumeric-or-hyphen characters (not
// starting/ending with a hyphen), joined by dots, with at least one
// dot (a bare single-label name like "mailhost" is not accepted —
// Message-ID id-right must look like a real Internet domain).
var validHostnameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// ResolveHostname picks the id-right domain for a server-generated
// Message-ID.
//
// Preference order:
//  1. the normalized, validated configured hostname (coremail.hostname)
//  2. the normalized, validated authenticated sender's domain, as a
//     fallback — safe only because callers of this function operate on
//     an authenticated, already-resolved local mailbox/domain
//
// If neither yields a valid Internet-style hostname, an error is
// returned. Callers MUST treat that as a fail-safe: refuse to
// originate the message rather than falling back to "orvix.local" or
// concatenating unvalidated text into a header.
func ResolveHostname(configuredHostname, fallbackSenderDomain string) (string, error) {
	if h, err := NormalizeHostname(configuredHostname); err == nil {
		return h, nil
	}
	if h, err := NormalizeHostname(fallbackSenderDomain); err == nil {
		return h, nil
	}
	return "", fmt.Errorf("no valid hostname available for Message-ID generation (configured=%q, fallback sender domain=%q)", configuredHostname, fallbackSenderDomain)
}

// NormalizeHostname validates and lowercases a hostname for safe use
// as the id-right of a Message-ID header (or any other RFC 5322
// header context). It rejects anything that is not a plausible
// Internet-style domain name, and specifically refuses private/
// non-routable pseudo-domains ("localhost", "*.local") so a
// misconfigured or empty coremail.hostname can never silently
// resurface "orvix.local" (or any other .local domain) in outbound
// mail.
func NormalizeHostname(raw string) (string, error) {
	h := strings.TrimSpace(raw)
	if h == "" {
		return "", fmt.Errorf("hostname is empty")
	}
	// Reject control characters (including CR/LF) before any other
	// processing — never let unvalidated text reach a header.
	for i := 0; i < len(h); i++ {
		c := h[i]
		if c < 0x20 || c == 0x7f {
			return "", fmt.Errorf("hostname contains control characters")
		}
	}
	if strings.ContainsAny(h, " \t") {
		return "", fmt.Errorf("hostname contains whitespace")
	}
	h = strings.ToLower(h)
	// One optional trailing DNS root dot.
	h = strings.TrimSuffix(h, ".")
	if h == "" {
		return "", fmt.Errorf("hostname is empty after normalization")
	}
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return "", fmt.Errorf("localhost is not a valid Internet hostname for Message-ID")
	}
	if h == "local" || strings.HasSuffix(h, ".local") {
		return "", fmt.Errorf("%q is a private pseudo-domain, not a valid Internet hostname for Message-ID", h)
	}
	if len(h) > 255 {
		return "", fmt.Errorf("hostname too long (max 255 chars)")
	}
	if !validHostnameRE.MatchString(h) {
		return "", fmt.Errorf("hostname %q is not a valid Internet domain name", h)
	}
	return h, nil
}
