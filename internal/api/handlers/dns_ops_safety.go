package handlers

// DNS Ops safety helpers (DNS-DKIM-OPERATIONS-2F-SAFETY-FIX).
//
// This file is the only place the DNS Ops handlers look up
// public mail IPs, validate DKIM selectors, or check whether a
// domain is provisioned in Orvix. The functions here are pure /
// side-effect free where possible so they can be unit-tested
// without spinning up a full handler harness.
//
// The three helpers cover the three Codex blockers:
//
//   1. Public mail IP — separate from the listener bind host
//      (coremail.smtp_host). A fresh install defaults the
//      listener to 0.0.0.0; we must NEVER generate A / SPF
//      records from 0.0.0.0 or from a private / loopback /
//      link-local address. The public IP comes from
//      cfg.DNS.PublicIPv4 (and optionally PublicIPv6) and
//      must pass isPublicUnicastIP.
//
//   2. DKIM selector — must be strict DNS-label safe:
//      lowercase, [a-z0-9-], 1..63 chars, no leading / trailing
//      hyphen, no dots, no spaces, no slashes, no underscores,
//      no quotes, no unicode, no wildcard, no double-dot. The
//      function lowercases the input so callers get a single
//      deterministic form. Empty input returns the safe default
//      "orvix".
//
//   3. Domain existence — Orvix must already know the domain
//      (coremail_domains row, deleted_at IS NULL) before we
//      generate a DKIM key pair for it. The function returns
//      (true, nil) when a row exists, (false, nil) when it
//      does not, and (_, err) on DB error.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// isPublicUnicastIP returns the IP if the string parses as a
// public unicast IPv4 or IPv6 address, and an explanatory error
// otherwise. The set of rejected addresses is conservative:
//
//   - invalid format (net.ParseIP returns nil)
//   - IPv4 unspecified (0.0.0.0) and IPv6 unspecified (::)
//   - IPv4 loopback (127.0.0.0/8) and IPv6 loopback (::1)
//   - IPv4 link-local (169.254.0.0/16) and IPv6 link-local
//     (fe80::/10)
//   - IPv4 private (10/8, 172.16/12, 192.168/16) and IPv6
//     unique-local (fc00::/7)
//   - IPv4 / IPv6 multicast
//   - IPv4 / IPv6 unspecified / broadcast-like
//
// A future "lab mode" config flag could relax this. For the
// production DNS Ops path, only global unicast addresses are
// accepted. We surface the reason string in the error so the
// dashboard can render an honest "why" instead of a generic
// 422.
func isPublicUnicastIP(s string) (net.IP, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("public mail IP is not configured; set dns.public_ipv4 in the server config")
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, fmt.Errorf("public mail IP %q is not a valid IPv4 or IPv6 address", s)
	}
	if ip.IsUnspecified() {
		return nil, errors.New("public mail IP is the unspecified address (0.0.0.0 or ::); set dns.public_ipv4 to the real mail server IP")
	}
	if ip.IsLoopback() {
		return nil, fmt.Errorf("public mail IP %s is a loopback address; DNS records must point at a real public IP", ip)
	}
	if ip.IsLinkLocalUnicast() {
		return nil, fmt.Errorf("public mail IP %s is a link-local address; DNS records must point at a real public IP", ip)
	}
	if ip.IsMulticast() {
		return nil, fmt.Errorf("public mail IP %s is a multicast address; DNS records must point at a real public IP", ip)
	}
	// net.IP.IsPrivate covers the IPv4 RFC1918 ranges and the
	// IPv6 ULA range. We deliberately reject all of these in
	// the production path; lab mode is out of scope for this
	// build.
	if ip.IsPrivate() {
		return nil, fmt.Errorf("public mail IP %s is a private-range address; DNS records must point at a real public IP", ip)
	}
	return ip, nil
}

// validateDKIMSelector applies the strict DKIM selector rules
// from RFC 6376 §3.1 (selector tag = 1*<domain-label> with
// additional safety guards). The function lowercases the input
// before validating and returns the normalised selector on
// success.
//
// Allowed:
//
//   - lowercase letters [a-z]
//   - digits [0-9]
//   - hyphen [-]
//   - length 1..63
//   - must not start or end with hyphen
//   - no dots, no spaces, no slashes, no underscores, no
//     quotes, no unicode, no wildcards, no double-hyphen
//
// Empty input returns ("orvix", nil). The default must always
// pass the validator so the public DNS TXT is always safe.
//
// The error string is sanitised and safe to surface to the
// admin UI.
func validateDKIMSelector(selector string) (string, error) {
	s := strings.TrimSpace(selector)
	if s == "" {
		return "orvix", nil
	}
	s = strings.ToLower(s)
	if len(s) > 63 {
		return "", fmt.Errorf("dkim selector %q is too long (%d > 63 characters)", s, len(s))
	}
	if strings.HasPrefix(s, "-") || strings.HasSuffix(s, "-") {
		return "", fmt.Errorf("dkim selector %q must not start or end with a hyphen", s)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-':
		default:
			// Reject anything else: dot, space, slash,
			// underscore, quote, unicode (multi-byte),
			// wildcard, control char.
			return "", fmt.Errorf("dkim selector %q contains an unsafe character at position %d; only lowercase letters, digits, and hyphens are allowed", s, i)
		}
	}
	if strings.Contains(s, "--") {
		return "", fmt.Errorf("dkim selector %q must not contain consecutive hyphens", s)
	}
	return s, nil
}

// domainExists reports whether the supplied domain has an active
// row in coremail_domains (deleted_at IS NULL). The function is
// used by the DKIM keygen path to refuse orphan rotations and
// by the DNS Ops handlers that need a domain ownership check.
//
// The lookup is name-keyed and case-insensitive. The function
// returns (true, nil) on a single matching row, (false, nil)
// when no row matches, and (false, err) on a database error so
// the handler can return 500 rather than silently accept the
// request.
func (h *Handler) domainExists(ctx context.Context, domain string) (bool, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return false, errors.New("domain is required")
	}
	sqlDB, err := h.db.DB()
	if err != nil {
		return false, fmt.Errorf("db unavailable: %w", err)
	}
	var count int64
	row := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM coremail_domains WHERE LOWER(name) = ? AND deleted_at IS NULL`,
		domain)
	if err := row.Scan(&count); err != nil {
		return false, fmt.Errorf("domain lookup: %w", err)
	}
	return count > 0, nil
}
