package customerdomain

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type MTASTSPolicy struct {
	Raw    string
	Valid  bool
	Mode   string
	MaxAge int
	MX     []string
	Error  string
}

type MTASTSPolicyCheck struct {
	*MTASTSCheck
	Policy *MTASTSPolicy `json:"policy,omitempty"`
}

var ssrfBlockedCIDRs = [...]string{
	"127.0.0.0/8",     // loopback
	"10.0.0.0/8",      // RFC1918
	"172.16.0.0/12",   // RFC1918
	"192.168.0.0/16",  // RFC1918
	"169.254.0.0/16",  // link-local (also covers cloud metadata: 169.254.169.254)
	"::1/128",         // loopback
	"fc00::/7",        // IPv6 ULA
	"fe80::/10",       // IPv6 link-local (also covers metadata over v6)
	"100.64.0.0/10",   // CGNAT (RFC 6598)
	"198.18.0.0/15",   // benchmarking (RFC 2544)
	"224.0.0.0/4",     // multicast
	"ff00::/8",        // IPv6 multicast
	"0.0.0.0/8",       // "this network"
	"192.0.2.0/24",    // IPv4 documentation (TEST-NET-1, RFC 5737)
	"198.51.100.0/24", // IPv4 documentation (TEST-NET-2, RFC 5737)
	"203.0.113.0/24",  // IPv4 documentation (TEST-NET-3, RFC 5737)
	"2001:db8::/32",   // IPv6 documentation (RFC 3849)
}

func isPublicAddress(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, cidr := range ssrfBlockedCIDRs {
		_, block, _ := net.ParseCIDR(cidr)
		if block != nil && block.Contains(ip) {
			return false
		}
	}
	return true
}

// ssrfDialer performs the full connect+TLS handshake for one MTA-STS
// fetch, with the IP address that was validated against isPublicAddress
// being the EXACT address dialed — never the hostname string. This is the
// critical property: passing the hostname to net.Dialer (or letting
// http.Transport's own DialContext+TLS wrapping do the connect) would let
// the standard library re-resolve the hostname a second time using the
// system resolver, silently discarding every validation performed here
// (classic TOCTOU / DNS-rebinding bypass — validate address A, dial
// address B because the attacker's DNS server answered differently on the
// second query).
//
// addr is "host:port" exactly as http.Transport passes it (still the
// hostname, not an IP — Transport never resolves on our behalf when we
// supply DialTLSContext). We resolve it ourselves via the injected
// resolver, validate every returned address, dial the network connection
// directly to the first validated IP's address, and only then start TLS —
// with ServerName pinned to the original hostname (never the IP) so
// certificate verification is checked against what the request actually
// intended to reach.
// resolveAndPinAddress resolves host via the injected resolver and
// validates every returned address, returning the first validated IP to
// pin the subsequent dial to. It performs NO network I/O beyond calling
// the injected resolver — no TCP dial, no TLS handshake — so it can be
// unit tested for the validation/rejection contract in complete isolation
// from real connectivity (which would otherwise make tests slow, flaky,
// and a live-network dependency the test suite must not have).
func resolveAndPinAddress(ctx context.Context, resolver func(ctx context.Context, host string) ([]net.IPAddr, error), host, port string) (net.IP, error) {
	if port != "443" {
		return nil, fmt.Errorf("mtasts: non-tls port rejected")
	}
	ips, err := resolver(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("mtasts: resolve error: %w", err)
	}
	var pinned net.IP
	for _, ipAddr := range ips {
		if !isPublicAddress(ipAddr.IP) {
			// Fail closed on ANY unsafe address in the result set, even if
			// other addresses in the same answer are safe — a mixed result
			// is treated as fully untrusted.
			return nil, fmt.Errorf("mtasts: address %s rejected", ipAddr.IP)
		}
		if pinned == nil {
			pinned = ipAddr.IP
		}
	}
	if pinned == nil {
		return nil, fmt.Errorf("mtasts: no usable address")
	}
	return pinned, nil
}

// handshakeAndCleanup wraps rawConn in TLS (ServerName pinned to the real
// hostname, never the dialed IP) and performs the handshake. On failure it
// closes rawConn itself — the caller must not also close it — and returns
// an error.
//
// The handshake error is what callers need: reasonFromError classifies it
// into the generic client-facing "policy_unverified: TLS error" string, so
// it must remain the PRIMARY, %w-wrapped error and stay first in the
// message so it keeps matching that classifier's "tls" substring check. A
// cleanup (Close) failure must never replace or hide it. The close result
// is still checked explicitly — never discarded — and, if it also failed,
// is folded in as secondary context only. Nothing here is logged, so no
// additional network detail is exposed to API clients beyond what the
// handshake error itself already carried.
func handshakeAndCleanup(ctx context.Context, rawConn net.Conn, serverName string) (net.Conn, error) {
	tlsConn := tls.Client(rawConn, &tls.Config{
		ServerName: serverName,
		MinVersion: tls.VersionTLS12,
	})
	handshakeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := tlsConn.HandshakeContext(handshakeCtx); err != nil {
		if closeErr := rawConn.Close(); closeErr != nil {
			return nil, fmt.Errorf("mtasts: tls handshake: %w (connection cleanup also failed: %v)", err, closeErr)
		}
		return nil, fmt.Errorf("mtasts: tls handshake: %w", err)
	}
	return tlsConn, nil
}

func ssrfDialer(resolver func(ctx context.Context, host string) ([]net.IPAddr, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("mtasts: bad address: %w", err)
		}
		pinned, err := resolveAndPinAddress(ctx, resolver, host, port)
		if err != nil {
			return nil, err
		}

		d := &net.Dialer{Timeout: 5 * time.Second}
		// Dial the PINNED IP address directly. net.JoinHostPort handles
		// IPv6 bracketing; this never passes the hostname string to Dial.
		rawConn, err := d.DialContext(ctx, network, net.JoinHostPort(pinned.String(), port))
		if err != nil {
			return nil, fmt.Errorf("mtasts: dial pinned address: %w", err)
		}

		// ServerName is pinned to host (the real hostname), never the IP.
		return handshakeAndCleanup(ctx, rawConn, host)
	}
}

type MTASTSFetcher struct {
	client  *http.Client
	timeout time.Duration
	maxSize int64
}

func NewMTASTSFetcher(resolver func(ctx context.Context, host string) ([]net.IPAddr, error)) *MTASTSFetcher {
	transport := &http.Transport{
		// DialTLSContext takes over the ENTIRE connect+handshake — when
		// set, http.Transport does not perform its own DNS resolution or
		// TLS wrapping at all, so there is no code path left that could
		// re-resolve the hostname after we've validated it.
		DialTLSContext: ssrfDialer(resolver),
		// Explicitly disabled: without this, http.Transport defaults to
		// http.ProxyFromEnvironment, which honors HTTP_PROXY/HTTPS_PROXY/
		// ALL_PROXY environment variables. A proxy makes the actual outbound
		// connection itself, completely bypassing DialTLSContext's address
		// validation — an operator-set (or attacker-influenced, in a
		// container/CI environment) proxy env var would silently defeat
		// every SSRF control above it.
		Proxy:                 nil,
		ForceAttemptHTTP2:     false,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		IdleConnTimeout:       5 * time.Second,
	}
	f := &MTASTSFetcher{
		client: &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
			// Redirects disabled outright. MTA-STS policy fetches never
			// legitimately need one (RFC 8461 does not define redirect
			// handling), and a followed redirect is a second, independent
			// SSRF surface — the safest contract is "the first response is
			// the only response"; a 3xx is treated as a fetch failure by
			// the caller's status-code check.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		timeout: 10 * time.Second,
		maxSize: 1024 * 100,
	}
	return f
}

func (f *MTASTSFetcher) Fetch(ctx context.Context, domain string) (*MTASTSPolicy, error) {
	// Built via net/url rather than string concatenation so a domain value
	// containing an unexpected character cannot alter the request's scheme,
	// host, port, or path. The domain itself is still validated/normalized
	// at domain-creation time elsewhere in the codebase; this is defence
	// in depth, not the primary control.
	u := url.URL{
		Scheme: "https",
		Host:   "mta-sts." + domain,
		Path:   "/.well-known/mta-sts.txt",
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("mta-sts request: %w", err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mta-sts fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mta-sts: unexpected status %d", resp.StatusCode)
	}
	// io.LimitReader caps the body actually read regardless of a
	// declared/absent Content-Length, and regardless of compression:
	// http.Transport transparently decompresses gzip responses before
	// resp.Body.Read returns bytes (since no Accept-Encoding header is set
	// here), so this limit bounds the DECOMPRESSED size actually buffered
	// in memory — a compressed body cannot inflate past this cap unnoticed.
	limited := io.LimitReader(resp.Body, f.maxSize+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("mta-sts read: %w", err)
	}
	if int64(len(body)) > f.maxSize {
		return nil, fmt.Errorf("mta-sts: response exceeds %d byte limit", f.maxSize)
	}
	return parseMTASTSPolicy(string(body)), nil
}

// validMTASTSModes are the only mode values RFC 8461 §3.2 defines. Any
// other value — including an empty string, a typo, or something a
// malicious/broken policy host invented — must make the policy invalid,
// never silently pass through as if it were a recognized enforcement mode.
var validMTASTSModes = map[string]bool{"enforce": true, "testing": true, "none": true}

func parseMTASTSPolicy(raw string) *MTASTSPolicy {
	policy := &MTASTSPolicy{Raw: raw}
	// strings.TrimSpace on each line strips a trailing "\r" too, so CRLF
	// and LF line endings are both handled by splitting on "\n" alone.
	lines := strings.Split(raw, "\n")
	seenVersion, seenMode, seenMaxAge := false, false, false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(strings.ToLower(parts[0]))
		val := strings.TrimSpace(parts[1])
		switch key {
		case "version":
			// RFC 8461 §3.2: a policy with more than one "version" field
			// is invalid — silently keeping the first (or last) one hides
			// a malformed/conflicting policy behind a false "valid".
			if seenVersion {
				policy.Error = "duplicate version field"
				return policy
			}
			seenVersion = true
			if val != "STSv1" {
				policy.Error = "invalid version, expected STSv1"
				return policy
			}
		case "mode":
			if seenMode {
				policy.Error = "duplicate mode field"
				return policy
			}
			seenMode = true
			policy.Mode = val
		case "max_age":
			if seenMaxAge {
				policy.Error = "duplicate max_age field"
				return policy
			}
			seenMaxAge = true
			n, err := strconv.Atoi(val)
			if err != nil {
				policy.Error = "invalid max_age: not an integer"
				return policy
			}
			policy.MaxAge = n
		case "mx":
			policy.MX = append(policy.MX, val)
		}
	}
	if policy.Mode == "" || policy.MaxAge <= 0 {
		policy.Error = "missing mode or max_age"
		return policy
	}
	if !validMTASTSModes[policy.Mode] {
		policy.Error = fmt.Sprintf("unrecognized mode %q", policy.Mode)
		return policy
	}
	if policy.Mode == "enforce" || policy.Mode == "testing" {
		if len(policy.MX) == 0 {
			policy.Error = "mx required for enforce/testing mode"
			return policy
		}
	}
	policy.Valid = true
	return policy
}
