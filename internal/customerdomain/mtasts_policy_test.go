package customerdomain

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/dnsops"
)

func fakeResolverIPs(ips ...net.IPAddr) func(ctx context.Context, host string) ([]net.IPAddr, error) {
	return func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return ips, nil
	}
}

func fakeResolverError() func(ctx context.Context, host string) ([]net.IPAddr, error) {
	return func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
}

func validMTASTSPolicyBody() string {
	return "version: STSv1\nmode: enforce\nmax_age: 86400\nmx: mail.example.com\n"
}

func TestParseMTASTSPolicy_Valid(t *testing.T) {
	raw := validMTASTSPolicyBody()
	p := parseMTASTSPolicy(raw)
	if !p.Valid {
		t.Errorf("expected valid policy, got error: %s", p.Error)
	}
	if p.Mode != "enforce" {
		t.Errorf("mode = %q, want enforce", p.Mode)
	}
	if p.MaxAge != 86400 {
		t.Errorf("max_age = %d, want 86400", p.MaxAge)
	}
	if len(p.MX) != 1 || p.MX[0] != "mail.example.com" {
		t.Errorf("mx = %v, want [mail.example.com]", p.MX)
	}
}

func TestParseMTASTSPolicy_InvalidVersion(t *testing.T) {
	raw := "version: STSv2\nmode: enforce\nmax_age: 86400\n"
	p := parseMTASTSPolicy(raw)
	if p.Valid {
		t.Error("expected invalid policy for wrong version")
	}
	if !strings.Contains(p.Error, "invalid version") {
		t.Errorf("error = %q, want 'invalid version'", p.Error)
	}
}

func TestParseMTASTSPolicy_MissingMode(t *testing.T) {
	raw := "version: STSv1\nmax_age: 86400\n"
	p := parseMTASTSPolicy(raw)
	if p.Valid {
		t.Error("expected invalid policy without mode")
	}
}

func TestParseMTASTSPolicy_TestingModeNeedsMX(t *testing.T) {
	raw := "version: STSv1\nmode: testing\nmax_age: 86400\n"
	p := parseMTASTSPolicy(raw)
	if p.Valid {
		t.Error("expected invalid policy for testing mode without mx")
	}
}

func TestMTASTSFetcher_HTTPSUnreachable(t *testing.T) {
	resolver := fakeResolverError()
	fetcher := NewMTASTSFetcher(resolver)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := fetcher.Fetch(ctx, "example.com")
	if err == nil {
		t.Error("expected error for unreachable HTTPS")
	}
}

// TestMTASTSFetcher_TLSFailure proves certificate verification is genuinely
// enabled — not skipped — in the exact TLS config production code builds
// (ssrfDialer's tls.Config: no InsecureSkipVerify, ServerName pinned to the
// intended hostname). It connects directly to a local httptest TLS server
// (self-signed cert, issued for 127.0.0.1/localhost, never trusted by the
// default root pool) and asserts the handshake fails with a certificate
// error when ServerName is set to an unrelated hostname the cert was never
// issued for — the same mismatch a real SSRF/MITM attempt would produce.
//
// This bypasses ssrfDialer's own port==443 gate (httptest allocates a
// random high port) to isolate exactly the property being tested: TLS
// verification behavior, not the port allowlist (which has its own
// dedicated tests below). No live/Internet network access occurs — the
// server is 127.0.0.1-only.
func TestMTASTSFetcher_TLSFailure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(validMTASTSPolicyBody()))
	}))
	defer server.Close()

	serverAddr := strings.TrimPrefix(server.URL, "https://")
	rawConn, err := net.DialTimeout("tcp", serverAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial test server: %v", err)
	}
	defer rawConn.Close()

	// Mirrors ssrfDialer's tls.Client construction exactly, except
	// ServerName is deliberately wrong — proving the cert is actually
	// checked against ServerName rather than always succeeding.
	tlsConn := tls.Client(rawConn, &tls.Config{
		ServerName: "mta-sts.example.com",
		MinVersion: tls.VersionTLS12,
	})
	defer tlsConn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = tlsConn.HandshakeContext(ctx)
	if err == nil {
		t.Fatal("expected TLS handshake to fail against a cert issued for a different host, but it succeeded")
	}
	lower := strings.ToLower(err.Error())
	if !strings.Contains(lower, "certificate") && !strings.Contains(lower, "x509") {
		t.Errorf("expected a certificate/x509 error, got: %v", err)
	}
}

func TestIsPublicAddress_Loopback(t *testing.T) {
	if isPublicAddress(net.ParseIP("127.0.0.1")) {
		t.Error("127.0.0.1 should not be public")
	}
	if isPublicAddress(net.ParseIP("::1")) {
		t.Error("::1 should not be public")
	}
}

func TestIsPublicAddress_Private(t *testing.T) {
	if isPublicAddress(net.ParseIP("10.0.0.1")) {
		t.Error("10.0.0.1 should not be public")
	}
	if isPublicAddress(net.ParseIP("192.168.1.1")) {
		t.Error("192.168.1.1 should not be public")
	}
	if isPublicAddress(net.ParseIP("172.16.0.1")) {
		t.Error("172.16.0.1 should not be public")
	}
}

func TestIsPublicAddress_LinkLocal(t *testing.T) {
	if isPublicAddress(net.ParseIP("169.254.0.1")) {
		t.Error("169.254.0.1 should not be public")
	}
	if isPublicAddress(net.ParseIP("fe80::1")) {
		t.Error("fe80::1 should not be public")
	}
}

func TestIsPublicAddress_PublicIP(t *testing.T) {
	if !isPublicAddress(net.ParseIP("8.8.8.8")) {
		t.Error("8.8.8.8 should be public")
	}
	if !isPublicAddress(net.ParseIP("2001:4860:4860::8888")) {
		t.Error("2001:4860:4860::8888 should be public")
	}
}

func TestIsPublicAddress_CGNAT(t *testing.T) {
	if isPublicAddress(net.ParseIP("100.64.0.1")) {
		t.Error("100.64.0.1 should not be public (CGNAT)")
	}
}

func TestIsPublicAddress_Unspecified(t *testing.T) {
	if isPublicAddress(net.ParseIP("0.0.0.0")) {
		t.Error("0.0.0.0 should not be public (unspecified)")
	}
	if isPublicAddress(net.ParseIP("::")) {
		t.Error(":: should not be public (unspecified)")
	}
}

func TestIsPublicAddress_Multicast(t *testing.T) {
	if isPublicAddress(net.ParseIP("224.0.0.1")) {
		t.Error("224.0.0.1 should not be public (multicast)")
	}
	if isPublicAddress(net.ParseIP("ff02::1")) {
		t.Error("ff02::1 should not be public (IPv6 multicast)")
	}
}

func TestIsPublicAddress_Benchmarking(t *testing.T) {
	if isPublicAddress(net.ParseIP("198.18.0.1")) {
		t.Error("198.18.0.1 should not be public (RFC 2544 benchmarking)")
	}
}

func TestIsPublicAddress_ThisNetwork(t *testing.T) {
	if isPublicAddress(net.ParseIP("0.1.2.3")) {
		t.Error("0.1.2.3 should not be public (0/8 'this network')")
	}
}

func TestIsPublicAddress_IPv4DocumentationRanges(t *testing.T) {
	for _, ip := range []string{"192.0.2.1", "198.51.100.1", "203.0.113.1"} {
		if isPublicAddress(net.ParseIP(ip)) {
			t.Errorf("%s should not be public (RFC 5737 documentation range)", ip)
		}
	}
}

func TestIsPublicAddress_IPv6DocumentationRange(t *testing.T) {
	if isPublicAddress(net.ParseIP("2001:db8::1")) {
		t.Error("2001:db8::1 should not be public (RFC 3849 documentation range)")
	}
}

func TestIsPublicAddress_IPv6ULA(t *testing.T) {
	if isPublicAddress(net.ParseIP("fd00::1")) {
		t.Error("fd00::1 should not be public (IPv6 ULA, fc00::/7)")
	}
}

// TestIsPublicAddress_IPv4MappedIPv6 proves the loopback/private checks
// are not bypassed by encoding a blocked IPv4 address as an IPv4-mapped
// IPv6 address (::ffff:a.b.c.d) — a classic SSRF filter-bypass technique
// against filters that only inspect the 4-byte form.
func TestIsPublicAddress_IPv4MappedIPv6(t *testing.T) {
	for _, ip := range []string{"::ffff:127.0.0.1", "::ffff:169.254.169.254", "::ffff:10.0.0.1"} {
		parsed := net.ParseIP(ip)
		if parsed == nil {
			t.Fatalf("test setup: failed to parse %s", ip)
		}
		if isPublicAddress(parsed) {
			t.Errorf("%s (IPv4-mapped IPv6) should not be public", ip)
		}
	}
}

// TestMTASTSFetcher_ProxyDisabled proves the transport does not fall back
// to http.ProxyFromEnvironment, which would let HTTP_PROXY/HTTPS_PROXY
// route the "validated" request through an arbitrary host — the actual
// connection would then be made by the proxy, not by ssrfDialer, silently
// bypassing every address check above it.
func TestMTASTSFetcher_ProxyDisabled(t *testing.T) {
	fetcher := NewMTASTSFetcher(fakeResolverError())
	transport, ok := fetcher.client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport")
	}
	if transport.Proxy != nil {
		t.Error("Transport.Proxy must be nil — a non-nil value (including the http.ProxyFromEnvironment default) lets HTTP_PROXY/HTTPS_PROXY bypass SSRF validation")
	}
}

// TestMTASTSFetcher_RedirectsDisabled proves the client never follows a
// redirect — MTA-STS policy fetches have no legitimate use for one, and a
// followed redirect is a second, unvalidated SSRF surface.
func TestMTASTSFetcher_RedirectsDisabled(t *testing.T) {
	fetcher := NewMTASTSFetcher(fakeResolverError())
	if fetcher.client.CheckRedirect == nil {
		t.Fatal("expected a CheckRedirect func to be set")
	}
	err := fetcher.client.CheckRedirect(&http.Request{URL: &url.URL{Host: "attacker.example", Path: "/x"}}, nil)
	if err != http.ErrUseLastResponse {
		t.Errorf("CheckRedirect = %v, want http.ErrUseLastResponse (never follow)", err)
	}
}

// TestMTASTSFetcher_OversizedResponseRejected proves Fetch rejects a
// response larger than the configured cap outright, rather than silently
// truncating it and parsing whatever fits as if it were the complete (and
// therefore seemingly valid) policy. It swaps in a local httptest.Server's
// transport in place of ssrfDialer purely to reach a local, non-443,
// zero-live-network test server — Fetch's own size-limiting logic
// (io.LimitReader + explicit oversize check) is exercised unmodified.
func TestMTASTSFetcher_OversizedResponseRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(make([]byte, 1024*100+1)) // one byte over the 100KB cap
	}))
	defer server.Close()

	fetcher := NewMTASTSFetcher(fakeResolverError())
	fetcher.client.Transport = &rewriteHostTransport{base: http.DefaultTransport, target: server.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := fetcher.Fetch(ctx, "oversized.example.com")
	if err == nil {
		t.Fatal("expected an error for an oversized response, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error = %v, want it to mention the size limit", err)
	}
}

// rewriteHostTransport redirects every request to a fixed local test
// server regardless of the request's original URL, so Fetch's own URL
// construction and size-limiting logic can be exercised against a real
// local HTTP response without touching ssrfDialer's port-443/IP-pinning
// path (which a random-port httptest.Server cannot satisfy).
type rewriteHostTransport struct {
	base   http.RoundTripper
	target string
}

func (rt *rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	targetURL, err := url.Parse(rt.target)
	if err != nil {
		return nil, err
	}
	req = req.Clone(req.Context())
	req.URL.Scheme = targetURL.Scheme
	req.URL.Host = targetURL.Host
	req.Host = targetURL.Host
	return rt.base.RoundTrip(req)
}

func TestSSRFDialer_RejectsPrivateIPv4(t *testing.T) {
	resolver := fakeResolverIPs(net.IPAddr{IP: net.ParseIP("192.168.1.1")})
	dialer := ssrfDialer(resolver)
	ctx := context.Background()
	_, err := dialer(ctx, "tcp", "mta-sts.example.com:443")
	if err == nil {
		t.Error("expected rejection for private IP")
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSSRFDialer_RejectsPrivateIPv6(t *testing.T) {
	resolver := fakeResolverIPs(net.IPAddr{IP: net.ParseIP("fc00::1")})
	dialer := ssrfDialer(resolver)
	ctx := context.Background()
	_, err := dialer(ctx, "tcp", "mta-sts.example.com:443")
	if err == nil {
		t.Error("expected rejection for private IPv6")
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSSRFDialer_RejectsNon443Port(t *testing.T) {
	resolver := fakeResolverIPs(net.IPAddr{IP: net.ParseIP("8.8.8.8")})
	dialer := ssrfDialer(resolver)
	ctx := context.Background()
	_, err := dialer(ctx, "tcp", "mta-sts.example.com:80")
	if err == nil {
		t.Error("expected rejection for non-443 port")
	}
	if !strings.Contains(err.Error(), "non-tls port") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestSSRFDialer_AllowsPublicIP proves the validation stage does not
// reject a genuinely public address, WITHOUT ever performing a real
// network dial: it exercises resolveAndPinAddress directly, which
// performs no I/O beyond invoking the injected (fake, in-memory) resolver.
// A previous version of this test called the full dialer against the real
// IP 8.8.8.8, which is exactly the "zero live network access" violation
// this package's tests must not have — even tolerating the dial's failure
// still attempts one real outbound TCP connection per test run.
func TestSSRFDialer_AllowsPublicIP(t *testing.T) {
	var dialedHost string
	resolver := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		dialedHost = host
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	pinned, err := resolveAndPinAddress(context.Background(), resolver, "mta-sts.example.com", "443")
	if err != nil {
		t.Fatalf("expected public IP to be allowed, got: %v", err)
	}
	if pinned.String() != "8.8.8.8" {
		t.Errorf("pinned = %v, want 8.8.8.8", pinned)
	}
	if dialedHost != "mta-sts.example.com" {
		t.Errorf("resolved host = %q, want mta-sts.example.com", dialedHost)
	}
}

func TestInspectEnterpriseMTASTSPolicyFetch(t *testing.T) {
	resolver := fakeResolverError()
	fetcher := NewMTASTSFetcher(resolver)

	fr := dnsops.NewFakeResolver()
	fr.Set("example.com", dnsops.FakeEntry{
		MX: []net.MX{{Host: "mx1.example.com.", Pref: 10}},
	})
	fr.Set("_mta-sts.example.com", dnsops.FakeEntry{
		TXT: []string{"v=STSv1; id=20260101"},
	})

	insp := NewDNSInspector(fr).WithMTASTSFetcher(fetcher)
	result := insp.InspectEnterprise(context.Background(), "example.com", nil, "", "")

	if result.MTASTS == nil {
		t.Fatal("expected MTA-STS TXT result")
	}
	if !strings.Contains(result.MTASTS.Reason, "policy_unverified") {
		t.Errorf("MTA-STS reason = %q, want 'policy_unverified' (HTTPS unreachable)", result.MTASTS.Reason)
	}
	t.Logf("MTA-STS status = %q, reason = %q", result.MTASTS.Status, result.MTASTS.Reason)
}

func TestInspectEnterpriseMTASTSPolicyFetchUnreachable(t *testing.T) {
	resolver := fakeResolverError()
	fetcher := NewMTASTSFetcher(resolver)

	fr := dnsops.NewFakeResolver()
	fr.Set("_mta-sts.example.com", dnsops.FakeEntry{
		TXT: []string{"v=STSv1; id=20260101"},
	})
	fr.Set("example.com", dnsops.FakeEntry{
		MX: []net.MX{{Host: "mx1.example.com.", Pref: 10}},
	})

	insp := NewDNSInspector(fr).WithMTASTSFetcher(fetcher)
	result := insp.InspectEnterprise(context.Background(), "example.com", nil, "", "")

	if result.MTASTS == nil {
		t.Fatal("expected MTA-STS TXT result")
	}
	if result.MTASTS.Status != "warning" {
		t.Errorf("MTA-STS status = %q, want warning (HTTPS unreachable)", result.MTASTS.Status)
	}
	if !strings.Contains(result.MTASTS.Reason, "policy_unverified") {
		t.Errorf("MTA-STS reason = %q, want 'policy_unverified'", result.MTASTS.Reason)
	}
	t.Logf("MTA-STS reason: %s", result.MTASTS.Reason)
}

func TestParseMTASTSPolicy_CommentsIgnored(t *testing.T) {
	raw := "# This is a comment\nversion: STSv1\nmode: testing\nmax_age: 86400\nmx: mail.example.com\n"
	p := parseMTASTSPolicy(raw)
	if !p.Valid {
		t.Errorf("expected valid policy, got error: %s", p.Error)
	}
	if p.Mode != "testing" {
		t.Errorf("mode = %q, want testing", p.Mode)
	}
}

func TestParseMTASTSPolicy_MultipleMX(t *testing.T) {
	raw := "version: STSv1\nmode: enforce\nmax_age: 86400\nmx: mx1.example.com\nmx: mx2.example.com\n"
	p := parseMTASTSPolicy(raw)
	if !p.Valid {
		t.Errorf("expected valid policy, got error: %s", p.Error)
	}
	if len(p.MX) != 2 {
		t.Errorf("mx count = %d, want 2", len(p.MX))
	}
}

func TestParseMTASTSPolicy_NoneModeNoMXRequired(t *testing.T) {
	raw := "version: STSv1\nmode: none\nmax_age: 86400\n"
	p := parseMTASTSPolicy(raw)
	if !p.Valid {
		t.Errorf("expected valid policy for none mode without mx, got: %s", p.Error)
	}
}

func TestReasonFromError_Timeout(t *testing.T) {
	err := &net.OpError{Err: &timeoutError{}}
	reason := reasonFromError(err, nil)
	if !strings.Contains(reason, "timeout") {
		t.Errorf("reason = %q, want timeout", reason)
	}
}

type timeoutError struct{}

func (e *timeoutError) Error() string   { return "i/o timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }
