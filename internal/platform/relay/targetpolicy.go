package relay

import (
	"context"
	"fmt"
	"net"
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

// IP policy: every IP class that must never be dialed by a platform
// connectivity test. There is deliberately no "local relay allowed"
// escape hatch: a deliberate local-relay config policy does not exist
// in this build, so loopback, link-local (which includes the cloud
// metadata address 169.254.169.254), unspecified, multicast, and
// RFC1918 private ranges are all rejected.
func validateDialIP(ip net.IP) error {
	if ip == nil {
		return fmt.Errorf("nil address")
	}
	switch {
	case ip.IsUnspecified():
		return fmt.Errorf("unspecified address")
	case ip.IsLoopback():
		return fmt.Errorf("loopback address")
	case ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast():
		return fmt.Errorf("link-local address (includes cloud metadata 169.254.169.254)")
	case ip.IsMulticast():
		return fmt.Errorf("multicast address")
	case ip.IsPrivate():
		return fmt.Errorf("private address")
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
				return nil, fmt.Errorf("unsafe relay target: %s resolves to %s", host, a.IP)
			}
		}
		dialIP = addrs[0].IP
	}

	if err := validateDialIP(dialIP); err != nil {
		return nil, fmt.Errorf("unsafe relay target: %s", err.Error())
	}
	plain := &net.Dialer{Timeout: d.timeout}
	conn, derr := plain.DialContext(ctx, network, net.JoinHostPort(dialIP.String(), port))
	if derr != nil {
		return nil, derr
	}
	return conn, nil
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
