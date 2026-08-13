package relay

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ── Connectivity-test target policy ────────────────────────────────
//
// Operator-triggered relay tests dial a remote SMTP endpoint from the
// platform control plane. That is a server-side network operation, so
// the target is validated twice:
//
//  1. ValidateRelayTarget at write time (create/update) AND at test
//     time (TestConnection): hostname syntax and, for IP literals, an
//     explicit block-list policy.
//  2. validatingDialer at dial time: resolves the hostname itself and
//     rejects the dial unless EVERY resolved address passes the same
//     policy. The address actually dialed is the validated IP — the
//     hostname is never re-resolved between validation and connect, so
//     a DNS rebinding attacker cannot swap the answer after the check.

// blockedPrefixes enumerates special-use / reserved CIDRs that the
// standard-library address predicates (loopback / private / link-local /
// multicast / unspecified) do NOT already cover but which must never be the
// target of a server-side relay connection. Comparison happens AFTER any
// IPv4-in-IPv6 form is unmapped, so ::ffff:100.64.0.1 is judged as the
// underlying IPv4 100.64.0.1.
//
//   - 0.0.0.0/8      "this host on this network" (RFC 1122) — reaches the
//     local host on many stacks; IsUnspecified only catches 0.0.0.0 exactly.
//   - 100.64.0.0/10  carrier-grade NAT (RFC 6598) — internal to the provider
//     network and NOT covered by IsPrivate.
//   - 192.0.0.0/24   IETF protocol assignments (RFC 6890).
//   - 192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24  documentation (TEST-NET).
//   - 198.18.0.0/15  benchmarking (RFC 2544).
//   - 240.0.0.0/4    reserved for future use (includes 255.255.255.255).
//   - 2001:db8::/32  IPv6 documentation.
//   - 2001:10::/28, 2001:20::/28  ORCHID.
//   - 64:ff9b::/96   NAT64 well-known prefix — embeds an arbitrary IPv4 in
//     the low 32 bits, so it can smuggle a loopback/private v4 to a NAT64
//     gateway; the whole prefix is refused.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2001:10::/28"),
	netip.MustParsePrefix("2001:20::/28"),
	netip.MustParsePrefix("64:ff9b::/96"),
}

// validateDialIP rejects every IP class that must never be dialed by a
// server-side relay connection. There is deliberately no "local relay
// allowed" escape hatch: loopback, unspecified, link-local (which includes
// the cloud metadata address 169.254.169.254), multicast, RFC1918 private,
// IPv6 ULA, carrier-grade NAT, benchmark/documentation/reserved ranges, and
// NAT64 are all refused. Only a globally-routable unicast address survives.
//
// IPv4-mapped IPv6 addresses are unmapped first, so ::ffff:127.0.0.1 is judged
// as IPv4 loopback rather than as a distinct — and otherwise "global" — v6
// address. That closes the mapped-address bypass.
func validateDialIP(ip net.IP) error {
	if ip == nil {
		return fmt.Errorf("nil address")
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return fmt.Errorf("malformed address")
	}
	addr = addr.Unmap()

	switch {
	case addr.IsUnspecified():
		return fmt.Errorf("unspecified address")
	case addr.IsLoopback():
		return fmt.Errorf("loopback address")
	case addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast():
		return fmt.Errorf("link-local address (includes cloud metadata 169.254.169.254)")
	case addr.IsMulticast():
		return fmt.Errorf("multicast address")
	case addr.IsPrivate():
		return fmt.Errorf("private address")
	}
	for _, p := range blockedPrefixes {
		if p.Contains(addr) {
			return fmt.Errorf("special-use/reserved address")
		}
	}
	// Anything that is not a globally-routable unicast address (e.g. reserved
	// classes the predicates above do not name) is refused rather than
	// dialed.
	if !addr.IsGlobalUnicast() {
		return fmt.Errorf("non-global address")
	}
	return nil
}

// hostnameRe is the allowed hostname shape: letters, digits, hyphens,
// underscores, dots (a trailing dot is permitted), labels separated by
// dots, up to 253 chars total. No scheme, no userinfo, no port, no
// path, no wildcard — anything else is rejected before DNS is even
// consulted.
var hostnameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9_-])?$`)

// loopbackHostnames are the canonical hostnames that resolve to the
// local machine. They are rejected by name so the policy does not
// depend on the local resolver agreeing with the block-list (a
// compromised or unusual /etc/hosts must not make loopback reachable
// through a friendly name).
var loopbackHostnames = map[string]bool{
	"localhost":             true,
	"localhost.localdomain": true,
	"ip6-localhost":         true,
	"ip6-loopback":          true,
}

// ValidateRelayTarget validates a relay host/port pair for both
// persistence and connectivity testing. Port must be 1..65535; the
// host must be a plain hostname or an IP literal, and IP literals must
// pass the unsafe-class policy (loopback/link-local/metadata/
// unspecified/multicast/private). Hostnames are NOT resolved here —
// resolution safety is enforced by validatingDialer at dial time.
func ValidateRelayTarget(host string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("host is required")
	}
	if strings.ContainsAny(host, "/\\@ \t\r\n") || strings.Contains(host, "://") {
		return fmt.Errorf("host must be a bare hostname or IP address, without scheme, userinfo, port, or path")
	}
	if strings.Contains(host, ":") {
		// A colon is only legitimate in a bracketed IPv6 literal.
		if !strings.HasPrefix(host, "[") || !strings.HasSuffix(host, "]") {
			return fmt.Errorf("host must be a bare hostname or IP address, without a port")
		}
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	if ip := net.ParseIP(host); ip != nil {
		if err := validateDialIP(ip); err != nil {
			return fmt.Errorf("unsafe relay target: %s", err.Error())
		}
		return nil
	}
	// A single trailing root dot (FQDN form) is permitted and stripped
	// before matching.
	host = strings.TrimSuffix(host, ".")
	if loopbackHostnames[strings.ToLower(host)] {
		return fmt.Errorf("unsafe relay target: loopback hostname")
	}
	if len(host) > 253 || !hostnameRe.MatchString(host) {
		return fmt.Errorf("host must be a valid hostname (letters, digits, hyphens, underscores, dots)")
	}
	return nil
}

// ── Safe dialer ────────────────────────────────────────────────────

// hostResolver is the narrow DNS port the validating dialer uses, so
// tests can inject a resolver that returns unsafe answers without real
// DNS.
type hostResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

type netResolver struct{}

func (netResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	// Bounded DNS: the resolver dials its configured nameservers with
	// this timeout, so a dead/hijacked resolver cannot hang the test.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

// validatingDialer resolves the target hostname and dials ONLY a
// policy-validated IP, with a bounded connection timeout. The dialed
// address is the validated IP (never a re-resolution of the hostname),
// which is the DNS-rebinding protection: an attacker who controls the
// answer either returns a safe IP (which we dial — no rebinding
// possible after the fact) or an unsafe IP (which rejects the dial).
type validatingDialer struct {
	timeout  time.Duration
	resolver hostResolver
	// connect performs the raw TCP connect to an ALREADY-VALIDATED
	// ip:port. It is injectable so an adversarial unit test can observe
	// exactly which address is dialed (rebinding protection) without a real
	// network, and defaults to a bounded net.Dialer.
	connect func(ctx context.Context, network, addr string) (net.Conn, error)
}

func newValidatingDialer() *validatingDialer {
	return &validatingDialer{timeout: 10 * time.Second, resolver: netResolver{}}
}

func (d *validatingDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid dial address: %w", err)
	}
	if _, err := strconv.Atoi(port); err != nil {
		return nil, fmt.Errorf("invalid dial port: %w", err)
	}

	var dialIP net.IP
	if ip := net.ParseIP(host); ip != nil {
		// An IP literal is validated directly; there is no name to resolve
		// and therefore no rebinding window.
		if err := validateDialIP(ip); err != nil {
			return nil, fmt.Errorf("unsafe relay target: %s", err.Error())
		}
		dialIP = ip
	} else {
		addrs, lerr := d.resolver.LookupIPAddr(ctx, host)
		if lerr != nil {
			return nil, fmt.Errorf("resolve relay host: %w", lerr)
		}
		if len(addrs) == 0 {
			return nil, fmt.Errorf("resolve relay host: no addresses")
		}
		// Validate EVERY answer: if any resolved address is unsafe the
		// whole target is rejected, so an attacker cannot mix one safe
		// answer into the list to pass validation.
		for _, a := range addrs {
			if err := validateDialIP(a.IP); err != nil {
				return nil, fmt.Errorf("unsafe relay target: %s resolves to a blocked address", host)
			}
		}
		dialIP = addrs[0].IP
	}

	// Re-validate the exact address we are about to dial, then connect to
	// THAT resolved IP — never a second resolution of the hostname. An
	// attacker who controls DNS either returned a safe IP (which we dial) or
	// an unsafe IP (already rejected above); there is no window in which the
	// name could be re-resolved to a different, unvalidated address.
	if err := validateDialIP(dialIP); err != nil {
		return nil, fmt.Errorf("unsafe relay target: %s", err.Error())
	}
	connect := d.connect
	if connect == nil {
		plain := &net.Dialer{Timeout: d.timeout}
		connect = plain.DialContext
	}
	return connect(ctx, network, net.JoinHostPort(dialIP.String(), port))
}

// ── Error redaction ────────────────────────────────────────────────

// redactHealthError caps a downstream SMTP/TLS error to a bounded
// length. Errors are only ever generated by this package from server
// responses (which never echo the AUTH payload), but the cap is
// defense in depth so a pathological server banner can never bloat a
// response, audit record, or log line.
func redactHealthError(msg string) string {
	const maxLen = 300
	if len(msg) <= maxLen {
		return msg
	}
	return msg[:maxLen] + "..."
}
