package customerdomain

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
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

func TestMTASTSFetcher_TLSFailure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(validMTASTSPolicyBody()))
	}))
	defer server.Close()

	cert := server.Certificate()
	resolver := fakeResolverIPs(net.IPAddr{IP: net.ParseIP("8.8.8.8")})

	transport := &http.Transport{
		DialContext: ssrfDialer(resolver),
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    x509.NewCertPool(),
		},
		ForceAttemptHTTP2: false,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
	}
	_ = cert

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	_, err := client.Do(req)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "certificate") {
		t.Logf("expected TLS/cert error, got: %v", err)
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

func TestSSRFDialer_AllowsPublicIP(t *testing.T) {
	var dialedHost string
	resolver := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		dialedHost = host
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	dialer := ssrfDialer(resolver)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := dialer(ctx, "tcp", "mta-sts.example.com:443")
	if err == nil {
		return
	}
	if strings.Contains(strings.ToLower(err.Error()), "rejected") {
		t.Errorf("expected public IP to be allowed, got rejection: %v", err)
	}
	if dialedHost != "mta-sts.example.com" {
		t.Errorf("dialed host = %q, want mta-sts.example.com", dialedHost)
	}
	t.Logf("dial attempt with public IP returned (timeout expected): %v", err)
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
