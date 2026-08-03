package customerdomain

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/orvix/orvix/internal/dnsops"
)

// ── Old Inspect tests (kept, still pass with checkMX([]string)) ──

func TestDNSInspectorMXValid(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("example.com", dnsops.FakeEntry{
		MX: []net.MX{{Host: "mail.example.com.", Pref: 10}},
	})
	insp := NewDNSInspector(r)
	result := insp.Inspect(context.Background(), "example.com", "mail.example.com", "", "")
	if result.MX == nil {
		t.Fatal("expected MX result")
	}
	if result.MX.Status != "pass" {
		t.Errorf("MX status = %q, want pass", result.MX.Status)
	}
}

func TestDNSInspectorMXMissing(t *testing.T) {
	r := dnsops.NewFakeResolver()
	insp := NewDNSInspector(r)
	result := insp.Inspect(context.Background(), "example.com", "", "", "")
	if result.MX == nil {
		t.Fatal("expected MX result")
	}
	if result.MX.Status != "fail" {
		t.Errorf("MX status = %q, want fail", result.MX.Status)
	}
}

func TestDNSInspectorSPFValid(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("example.com", dnsops.FakeEntry{
		TXT: []string{"v=spf1 mx -all"},
	})
	insp := NewDNSInspector(r)
	result := insp.Inspect(context.Background(), "example.com", "", "", "")
	if result.SPF == nil {
		t.Fatal("expected SPF result")
	}
	if result.SPF.Status != "pass" {
		t.Errorf("SPF status = %q, want pass", result.SPF.Status)
	}
}

func TestDNSInspectorSPFMissing(t *testing.T) {
	r := dnsops.NewFakeResolver()
	insp := NewDNSInspector(r)
	result := insp.Inspect(context.Background(), "example.com", "", "", "")
	if result.SPF == nil {
		t.Fatal("expected SPF result")
	}
	if result.SPF.Status != "fail" {
		t.Errorf("SPF status = %q, want fail", result.SPF.Status)
	}
}

func TestDNSInspectorSPFMultiple(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("example.com", dnsops.FakeEntry{
		TXT: []string{"v=spf1 mx -all", "v=spf1 include:other.com -all"},
	})
	insp := NewDNSInspector(r)
	result := insp.Inspect(context.Background(), "example.com", "", "", "")
	if result.SPF.Status != "fail" {
		t.Errorf("multiple SPF status = %q, want fail", result.SPF.Status)
	}
}

func TestDNSInspectorDKIMPass(t *testing.T) {
	expectedRecord := "v=DKIM1; k=rsa; p=MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQC"
	r := dnsops.NewFakeResolver()
	r.Set("default._domainkey.example.com", dnsops.FakeEntry{
		TXT: []string{expectedRecord},
	})
	insp := NewDNSInspector(r)
	result := insp.Inspect(context.Background(), "example.com", "", "default", expectedRecord)
	if result.DKIM == nil {
		t.Fatal("expected DKIM result")
	}
	if result.DKIM.Status != "pass" {
		t.Errorf("DKIM status = %q, want pass", result.DKIM.Status)
	}
}

func TestDNSInspectorDKIMMissing(t *testing.T) {
	r := dnsops.NewFakeResolver()
	insp := NewDNSInspector(r)
	result := insp.Inspect(context.Background(), "example.com", "", "default", "")
	if result.DKIM == nil {
		t.Fatal("expected DKIM result")
	}
	if result.DKIM.Status != "fail" {
		t.Errorf("missing DKIM status = %q, want fail", result.DKIM.Status)
	}
}

func TestDNSInspectorDKIMMismatch(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("default._domainkey.example.com", dnsops.FakeEntry{
		TXT: []string{"v=DKIM1; k=rsa; p=WRONGKEY"},
	})
	insp := NewDNSInspector(r)
	result := insp.Inspect(context.Background(), "example.com", "", "default", "v=DKIM1; k=rsa; p=MIGfMA0")
	if result.DKIM == nil {
		t.Fatal("expected DKIM result")
	}
	if result.DKIM.Status != "fail" {
		t.Errorf("mismatch DKIM status = %q, want fail", result.DKIM.Status)
	}
}

func TestDNSInspectorDMARCPass(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("_dmarc.example.com", dnsops.FakeEntry{
		TXT: []string{"v=DMARC1; p=reject; rua=mailto:dmarc@example.com"},
	})
	insp := NewDNSInspector(r)
	result := insp.Inspect(context.Background(), "example.com", "", "", "")
	if result.DMARC == nil {
		t.Fatal("expected DMARC result")
	}
	if result.DMARC.Status != "pass" {
		t.Errorf("DMARC status = %q, want pass", result.DMARC.Status)
	}
}

func TestDNSInspectorDMARCMissing(t *testing.T) {
	r := dnsops.NewFakeResolver()
	insp := NewDNSInspector(r)
	result := insp.Inspect(context.Background(), "example.com", "", "", "")
	if result.DMARC == nil {
		t.Fatal("expected DMARC result")
	}
	if result.DMARC.Status != "fail" {
		t.Errorf("missing DMARC status = %q, want fail", result.DMARC.Status)
	}
}

func TestDNSInspectorDMARCNoEnforcement(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("_dmarc.example.com", dnsops.FakeEntry{
		TXT: []string{"v=DMARC1; p=none"},
	})
	insp := NewDNSInspector(r)
	result := insp.Inspect(context.Background(), "example.com", "", "", "")
	if result.DMARC.Status != "warning" {
		t.Errorf("p=none DMARC status = %q, want warning", result.DMARC.Status)
	}
}

func TestDNSInspectorNoDKIMPrivateKeyExposed(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("default._domainkey.example.com", dnsops.FakeEntry{
		TXT: []string{"v=DKIM1; k=rsa; p=PUBLICDATA"},
	})
	insp := NewDNSInspector(r)
	result := insp.Inspect(context.Background(), "example.com", "", "default", "v=DKIM1; k=rsa; p=PUBLICDATA")
	if result.DKIM.Observed == "" {
		t.Fatal("expected DKIM observed value")
	}
	if result.DKIM.PublicKey != "" {
		t.Errorf("PublicKey = %q, want empty (no private key exposure)", result.DKIM.PublicKey)
	}
}

// ── Multi-MX matching tests ──

func TestInspectEnterpriseMXMultiAllMatch(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("example.com", dnsops.FakeEntry{
		MX: []net.MX{
			{Host: "mx1.example.com.", Pref: 10},
			{Host: "mx2.example.com.", Pref: 20},
		},
	})
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com",
		[]string{"mx1.example.com", "mx2.example.com"}, "", "")
	if result.MX == nil {
		t.Fatal("expected MX result")
	}
	if result.MX.Status != "pass" {
		t.Errorf("MX status = %q, want pass", result.MX.Status)
	}
}

func TestInspectEnterpriseMXMultiOnlyPrimaryFound(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("example.com", dnsops.FakeEntry{
		MX: []net.MX{
			{Host: "mx1.example.com.", Pref: 10},
		},
	})
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com",
		[]string{"mx1.example.com", "mx2.example.com"}, "", "")
	if result.MX == nil {
		t.Fatal("expected MX result")
	}
	if result.MX.Status != "warning" {
		t.Errorf("MX status = %q, want warning", result.MX.Status)
	}
	if !strings.Contains(result.MX.Reason, "not all expected MX hosts found") {
		t.Errorf("MX reason = %q, want 'not all expected MX hosts found'", result.MX.Reason)
	}
}

func TestInspectEnterpriseMXMultiNeitherFound(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("example.com", dnsops.FakeEntry{
		MX: []net.MX{
			{Host: "other.example.com.", Pref: 10},
		},
	})
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com",
		[]string{"mx1.example.com", "mx2.example.com"}, "", "")
	if result.MX == nil {
		t.Fatal("expected MX result")
	}
	if result.MX.Status != "fail" {
		t.Errorf("MX status = %q, want fail", result.MX.Status)
	}
}

func TestInspectEnterpriseMXFallbackWhenEmpty(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("example.com", dnsops.FakeEntry{
		MX: []net.MX{
			{Host: "mail.example.com.", Pref: 10},
		},
	})
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com", nil, "", "")
	if result.MX == nil {
		t.Fatal("expected MX result")
	}
	if result.MX.Status != "pass" {
		t.Errorf("MX status = %q, want pass (fell back to mail.example.com)", result.MX.Status)
	}
}

func TestInspectEnterpriseMXExtraUnexpectedHostsIgnored(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("example.com", dnsops.FakeEntry{
		MX: []net.MX{
			{Host: "mx1.example.com.", Pref: 10},
			{Host: "mx2.example.com.", Pref: 20},
			{Host: "mx3.example.com.", Pref: 30},
		},
	})
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com",
		[]string{"mx1.example.com", "mx2.example.com"}, "", "")
	if result.MX == nil {
		t.Fatal("expected MX result")
	}
	if result.MX.Status != "pass" {
		t.Errorf("MX status = %q, want pass (extra unexpected host should not change verdict)", result.MX.Status)
	}
}

func TestInspectEnterpriseMXTrailingDotsNormalized(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("example.com", dnsops.FakeEntry{
		MX: []net.MX{
			{Host: "mx1.example.com.", Pref: 10},
			{Host: "mx2.example.com.", Pref: 20},
		},
	})
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com",
		[]string{"mx1.example.com.", "mx2.example.com."}, "", "")
	if result.MX == nil {
		t.Fatal("expected MX result")
	}
	if result.MX.Status != "pass" {
		t.Errorf("MX status = %q, want pass (trailing dots should be normalized)", result.MX.Status)
	}
}

// ── MTA-STS tests ──

func TestInspectEnterpriseMTASTSValid(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("_mta-sts.example.com", dnsops.FakeEntry{
		TXT: []string{"v=STSv1; id=20260101"},
	})
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com", nil, "", "")
	if result.MTASTS == nil {
		t.Fatal("expected MTA-STS result")
	}
	// A syntactically valid MTA-STS TXT record is NOT sufficient on its own.
	// The policy it advertises must also be fetched and parsed over HTTPS
	// before MTA-STS can be recorded as a pass. With no fetcher wired the
	// policy is unverified, so the check must stay at warning.
	if result.MTASTS.Status != "warning" {
		t.Errorf("MTA-STS status = %q, want warning (TXT valid but HTTPS policy unverified)", result.MTASTS.Status)
	}
	if !strings.Contains(result.MTASTS.Reason, "unverified") {
		t.Errorf("MTA-STS reason = %q, want it to state the HTTPS policy is unverified", result.MTASTS.Reason)
	}
}

func TestInspectEnterpriseMTASTSMissing(t *testing.T) {
	r := dnsops.NewFakeResolver()
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com", nil, "", "")
	if result.MTASTS == nil {
		t.Fatal("expected MTA-STS result")
	}
	if result.MTASTS.Status != "fail" {
		t.Errorf("MTA-STS status = %q, want fail", result.MTASTS.Status)
	}
}

func TestInspectEnterpriseMTASTSNoValidPrefix(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("_mta-sts.example.com", dnsops.FakeEntry{
		TXT: []string{"some-other-record"},
	})
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com", nil, "", "")
	if result.MTASTS == nil {
		t.Fatal("expected MTA-STS result")
	}
	if result.MTASTS.Status != "fail" {
		t.Errorf("MTA-STS status = %q, want fail (no valid v=stsv1 prefix)", result.MTASTS.Status)
	}
	if !strings.Contains(strings.ToLower(result.MTASTS.Reason), "no valid mta-sts") {
		t.Errorf("MTA-STS reason = %q, want 'no valid MTA-STS record found'", result.MTASTS.Reason)
	}
}

func TestInspectEnterpriseMTASTSNoIDTag(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("_mta-sts.example.com", dnsops.FakeEntry{
		TXT: []string{"v=STSv1"},
	})
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com", nil, "", "")
	if result.MTASTS == nil {
		t.Fatal("expected MTA-STS result")
	}
	if result.MTASTS.Status != "warning" {
		t.Errorf("MTA-STS status = %q, want warning (missing id tag)", result.MTASTS.Status)
	}
	if !strings.Contains(strings.ToLower(result.MTASTS.Reason), "missing id tag") {
		t.Errorf("MTA-STS reason = %q, want 'MTA-STS record missing id tag'", result.MTASTS.Reason)
	}
}

type errorOnlyResolver struct {
	dnsops.Resolver
	errName string
}

func (e *errorOnlyResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	if name == e.errName {
		return nil, &net.DNSError{Err: "server misbehaving", Name: name, Server: "test"}
	}
	return e.Resolver.LookupTXT(ctx, name)
}

func (e *errorOnlyResolver) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	return e.Resolver.LookupMX(ctx, name)
}

func TestInspectEnterpriseMTASTSDNSError(t *testing.T) {
	fr := dnsops.NewFakeResolver()
	r := &errorOnlyResolver{Resolver: fr, errName: "_mta-sts.example.com"}
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com", nil, "", "")
	if result.MTASTS == nil {
		t.Fatal("expected MTA-STS result")
	}
	if result.MTASTS.Status != "unknown" {
		t.Errorf("MTA-STS status = %q, want unknown (DNS error)", result.MTASTS.Status)
	}
}

// ── TLS-RPT tests ──

func TestInspectEnterpriseTLSRPTValid(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("_smtp._tls.example.com", dnsops.FakeEntry{
		TXT: []string{"v=TLSRPTv1; rua=mailto:reports@example.com"},
	})
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com", nil, "", "")
	if result.TLSRPT == nil {
		t.Fatal("expected TLS-RPT result")
	}
	if result.TLSRPT.Status != "pass" {
		t.Errorf("TLS-RPT status = %q, want pass", result.TLSRPT.Status)
	}
}

func TestInspectEnterpriseTLSRPTMissing(t *testing.T) {
	r := dnsops.NewFakeResolver()
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com", nil, "", "")
	if result.TLSRPT == nil {
		t.Fatal("expected TLS-RPT result")
	}
	if result.TLSRPT.Status != "fail" {
		t.Errorf("TLS-RPT status = %q, want fail", result.TLSRPT.Status)
	}
}

func TestInspectEnterpriseTLSRPTNoValidPrefix(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("_smtp._tls.example.com", dnsops.FakeEntry{
		TXT: []string{"some-other-record"},
	})
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com", nil, "", "")
	if result.TLSRPT == nil {
		t.Fatal("expected TLS-RPT result")
	}
	if result.TLSRPT.Status != "fail" {
		t.Errorf("TLS-RPT status = %q, want fail (no valid v=tlsrptv1 prefix)", result.TLSRPT.Status)
	}
	if !strings.Contains(strings.ToLower(result.TLSRPT.Reason), "no valid tls-rpt") {
		t.Errorf("TLS-RPT reason = %q, want 'no valid TLS-RPT record found'", result.TLSRPT.Reason)
	}
}

func TestInspectEnterpriseTLSRPTNoRUATag(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("_smtp._tls.example.com", dnsops.FakeEntry{
		TXT: []string{"v=TLSRPTv1"},
	})
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com", nil, "", "")
	if result.TLSRPT == nil {
		t.Fatal("expected TLS-RPT result")
	}
	if result.TLSRPT.Status != "warning" {
		t.Errorf("TLS-RPT status = %q, want warning (missing rua tag)", result.TLSRPT.Status)
	}
	if !strings.Contains(strings.ToLower(result.TLSRPT.Reason), "missing rua") {
		t.Errorf("TLS-RPT reason = %q, want 'TLS-RPT record missing rua tag'", result.TLSRPT.Reason)
	}
}

func TestInspectEnterpriseTLSRPTDNSError(t *testing.T) {
	fr := dnsops.NewFakeResolver()
	r := &errorOnlyResolver{Resolver: fr, errName: "_smtp._tls.example.com"}
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com", nil, "", "")
	if result.TLSRPT == nil {
		t.Fatal("expected TLS-RPT result")
	}
	if result.TLSRPT.Status != "unknown" {
		t.Errorf("TLS-RPT status = %q, want unknown (DNS error)", result.TLSRPT.Status)
	}
}

// ── Enterprise health score tests ──

// TestInspectEnterpriseMTASTSUnverifiedBlocksFullPass is the regression test
// for the "no 100% unless the MTA-STS HTTPS policy is verified" rule.
//
// Every record this domain publishes is correct at the DNS layer: MX, SPF,
// DKIM, DMARC, MTA-STS TXT and TLS-RPT all resolve and validate, the mail host
// has an A record, the MX host resolves, and rDNS matches. The ONLY thing not
// established is the MTA-STS HTTPS policy document. The overall result must
// therefore be strictly below 100 and must not read as an unqualified pass.
func TestInspectEnterpriseMTASTSUnverifiedBlocksFullPass(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("example.com", dnsops.FakeEntry{
		MX:  []net.MX{{Host: "mx1.example.com.", Pref: 10}},
		TXT: []string{"v=spf1 mx -all"},
	})
	r.Set("mx1.example.com", dnsops.FakeEntry{
		A:      []net.IP{net.ParseIP("192.0.2.25")},
		PTRFor: map[string][]string{"192.0.2.25": {"mx1.example.com."}},
	})
	r.Set("default._domainkey.example.com", dnsops.FakeEntry{
		TXT: []string{"v=DKIM1; k=rsa; p=TEST"},
	})
	r.Set("_dmarc.example.com", dnsops.FakeEntry{
		TXT: []string{"v=DMARC1; p=reject"},
	})
	r.Set("_mta-sts.example.com", dnsops.FakeEntry{
		TXT: []string{"v=STSv1; id=20260101"},
	})
	r.Set("_smtp._tls.example.com", dnsops.FakeEntry{
		TXT: []string{"v=TLSRPTv1; rua=mailto:reports@example.com"},
	})

	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com",
		[]string{"mx1.example.com"}, "default", "v=DKIM1; k=rsa; p=TEST")

	// Every other required record really did pass — this test is only
	// meaningful if the MTA-STS policy is the sole outstanding item.
	for name, status := range map[string]string{
		"mx":           result.MX.Status,
		"spf":          result.SPF.Status,
		"dkim":         result.DKIM.Status,
		"dmarc":        result.DMARC.Status,
		"tlsrpt":       result.TLSRPT.Status,
		"mail_host_a":  result.MailHostA.Status,
		"ptr":          result.PTR.Status,
		"mx_host[0]":   result.MXHosts[0].Status,
		"__mtasts_txt": "pass",
	} {
		if status != "pass" {
			t.Fatalf("precondition: %s = %q, want pass", name, status)
		}
	}

	if result.MTASTS.Status == "pass" {
		t.Fatalf("MTA-STS reported pass without a verified HTTPS policy")
	}
	if result.MTASTSPolicy != nil {
		t.Fatalf("MTASTSPolicy = %+v, want nil (no policy was ever fetched)", result.MTASTSPolicy)
	}
	if result.HealthScore >= 100 {
		t.Errorf("HealthScore = %d, want < 100 while the MTA-STS HTTPS policy is unverified", result.HealthScore)
	}
	if result.DNSHealth == "pass" {
		t.Errorf("DNSHealth = %q, want a non-pass rollup while the MTA-STS HTTPS policy is unverified", result.DNSHealth)
	}
}

func TestInspectEnterpriseHealthScoreAllPass(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("example.com", dnsops.FakeEntry{
		MX:  []net.MX{{Host: "mx1.example.com.", Pref: 10}},
		TXT: []string{"v=spf1 mx -all"},
	})
	r.Set("mx1.example.com", dnsops.FakeEntry{
		A:      []net.IP{net.ParseIP("192.0.2.25")},
		PTRFor: map[string][]string{"192.0.2.25": {"mx1.example.com."}},
	})
	r.Set("default._domainkey.example.com", dnsops.FakeEntry{
		TXT: []string{"v=DKIM1; k=rsa; p=TEST"},
	})
	r.Set("_dmarc.example.com", dnsops.FakeEntry{
		TXT: []string{"v=DMARC1; p=reject"},
	})
	r.Set("_mta-sts.example.com", dnsops.FakeEntry{
		TXT: []string{"v=STSv1; id=20260101"},
	})
	r.Set("_smtp._tls.example.com", dnsops.FakeEntry{
		TXT: []string{"v=TLSRPTv1; rua=mailto:reports@example.com"},
	})
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com",
		[]string{"mx1.example.com"}, "default", "v=DKIM1; k=rsa; p=TEST")
	// "All pass" at the DNS layer is deliberately NOT 100%: the MTA-STS
	// HTTPS policy cannot be verified without a fetcher, and an unverified
	// policy can never earn full credit. See
	// TestInspectEnterpriseMTASTSUnverifiedBlocksFullPass. Everything except
	// MTA-STS earns full credit, and MTA-STS earns half (warning), so the
	// score is the maximum reachable without policy verification.
	const maxWithoutPolicyVerification = 96 // 96 of 100 weighted points
	if result.HealthScore != maxWithoutPolicyVerification {
		t.Errorf("all-DNS-pass score = %d, want %d", result.HealthScore, maxWithoutPolicyVerification)
	}
	if result.HealthScore >= 100 {
		t.Errorf("score reached %d without a verified MTA-STS HTTPS policy", result.HealthScore)
	}
	if result.DNSHealth != "warning" {
		t.Errorf("DNSHealth = %q, want warning (MTA-STS HTTPS policy unverified)", result.DNSHealth)
	}
	// Optional and not-applicable records must be present and explicitly
	// marked, never silently omitted.
	if result.MailHostAAAA == nil || result.MailHostAAAA.Status != "optional" {
		t.Errorf("MailHostAAAA = %+v, want status optional", result.MailHostAAAA)
	}
	if result.Autodiscover == nil || result.Autodiscover.Status != "optional" {
		t.Errorf("Autodiscover = %+v, want status optional", result.Autodiscover)
	}
	if result.Autoconfig == nil || result.Autoconfig.Status != "optional" {
		t.Errorf("Autoconfig = %+v, want status optional", result.Autoconfig)
	}
	if result.TLSA == nil || result.TLSA.Status != "not_applicable" {
		t.Errorf("TLSA = %+v, want status not_applicable", result.TLSA)
	}
	// Required values must always be real, never blank.
	if result.SPF.Expected != "v=spf1 mx -all" {
		t.Errorf("SPF expected = %q, want %q", result.SPF.Expected, "v=spf1 mx -all")
	}
	want := "v=DMARC1; p=quarantine; rua=mailto:dmarc@example.com"
	if result.DMARC.Expected != want {
		t.Errorf("DMARC expected = %q, want %q", result.DMARC.Expected, want)
	}
	for name, g := range map[string]string{
		"mx":     result.MX.Guidance,
		"spf":    result.SPF.Guidance,
		"dmarc":  result.DMARC.Guidance,
		"mtasts": result.MTASTS.Guidance,
		"tlsrpt": result.TLSRPT.Guidance,
		"ptr":    result.PTR.Guidance,
		"tlsa":   result.TLSA.Guidance,
	} {
		if strings.TrimSpace(g) == "" {
			t.Errorf("%s guidance is empty; every row must carry repair guidance", name)
		}
	}
}

func TestInspectEnterpriseHealthScoreOneFail(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("example.com", dnsops.FakeEntry{
		MX:  []net.MX{{Host: "mx1.example.com.", Pref: 10}},
		TXT: []string{"v=spf1 mx -all"},
	})
	r.Set("default._domainkey.example.com", dnsops.FakeEntry{
		TXT: []string{"v=DKIM1; k=rsa; p=TEST"},
	})
	r.Set("_dmarc.example.com", dnsops.FakeEntry{
		TXT: []string{"v=DMARC1; p=reject"},
	})
	r.Set("_mta-sts.example.com", dnsops.FakeEntry{
		TXT: []string{"v=STSv1; id=20260101"},
	})
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com",
		[]string{"mx1.example.com"}, "default", "v=DKIM1; k=rsa; p=TEST")
	if result.HealthScore == 100 {
		t.Errorf("one fail (TLS-RPT missing) score = %d, want < 100", result.HealthScore)
	}
	if result.DNSHealth != "fail" {
		t.Errorf("DNSHealth = %q, want fail", result.DNSHealth)
	}
}

func TestInspectEnterpriseHealthScoreMXFailOthersPass(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("example.com", dnsops.FakeEntry{
		TXT: []string{"v=spf1 mx -all"},
	})
	r.Set("default._domainkey.example.com", dnsops.FakeEntry{
		TXT: []string{"v=DKIM1; k=rsa; p=TEST"},
	})
	r.Set("_dmarc.example.com", dnsops.FakeEntry{
		TXT: []string{"v=DMARC1; p=reject"},
	})
	r.Set("_mta-sts.example.com", dnsops.FakeEntry{
		TXT: []string{"v=STSv1; id=20260101"},
	})
	r.Set("_smtp._tls.example.com", dnsops.FakeEntry{
		TXT: []string{"v=TLSRPTv1; rua=mailto:reports@example.com"},
	})
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com",
		[]string{"mx1.example.com"}, "default", "v=DKIM1; k=rsa; p=TEST")
	// Weighted out of 100 possible points, with optional/not_applicable
	// records excluded from both numerator and denominator:
	//   mx           fail     0/20
	//   spf          pass    12/12
	//   dkim         pass    20/20
	//   dmarc        pass    12/12
	//   mtasts       warning  4/8   (HTTPS policy unverified)
	//   tlsrpt       pass     8/8
	//   mail_host_a  fail     0/8   (mx1.example.com has no A record here)
	//   mx_host      fail     0/8   (no MX published, so nothing resolves)
	//   ptr          fail     0/4   (no mail-host IP to reverse-resolve)
	expectedScore := 56
	if result.HealthScore != expectedScore {
		t.Errorf("MX fail others pass score = %d, want %d", result.HealthScore, expectedScore)
	}
	if result.DNSHealth != "fail" {
		t.Errorf("DNSHealth = %q, want fail", result.DNSHealth)
	}
}

// ── DKIM matching in InspectEnterprise ──

func TestInspectEnterpriseDKIMMatchesDNS(t *testing.T) {
	expectedRecord := "v=DKIM1; k=rsa; p=MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQC"
	r := dnsops.NewFakeResolver()
	r.Set("default._domainkey.example.com", dnsops.FakeEntry{
		TXT: []string{expectedRecord},
	})
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com", nil,
		"default", expectedRecord)
	if result.DKIM == nil {
		t.Fatal("expected DKIM result")
	}
	if !result.DKIM.Configured {
		t.Error("DKIM.Configured = false, want true")
	}
	if !result.DKIM.MatchesDNS {
		t.Error("DKIM.MatchesDNS = false, want true")
	}
}

func TestInspectEnterpriseDKIMMismatchDNS(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("default._domainkey.example.com", dnsops.FakeEntry{
		TXT: []string{"v=DKIM1; k=rsa; p=DIFFERENT_KEY"},
	})
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com", nil,
		"default", "v=DKIM1; k=rsa; p=EXPECTED_KEY")
	if result.DKIM == nil {
		t.Fatal("expected DKIM result")
	}
	if !result.DKIM.Configured {
		t.Error("DKIM.Configured = false, want true")
	}
	if result.DKIM.MatchesDNS {
		t.Error("DKIM.MatchesDNS = true, want false (record mismatch)")
	}
}

func TestInspectEnterpriseDKIMNotConfigured(t *testing.T) {
	r := dnsops.NewFakeResolver()
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com", nil, "", "")
	if result.DKIM == nil {
		t.Fatal("expected DKIM result")
	}
	if result.DKIM.Configured {
		t.Error("DKIM.Configured = true, want false (no selector provided)")
	}
	if result.DKIM.MatchesDNS {
		t.Error("DKIM.MatchesDNS = true, want false (not configured)")
	}
}

// ── No private key exposure in DKIMHealthCheck ──

func TestInspectEnterpriseNoDKIMPrivateKeyExposed(t *testing.T) {
	r := dnsops.NewFakeResolver()
	r.Set("default._domainkey.example.com", dnsops.FakeEntry{
		TXT: []string{"v=DKIM1; k=rsa; p=PUBLICKEYDATA"},
	})
	insp := NewDNSInspector(r)
	result := insp.InspectEnterprise(context.Background(), "example.com", nil,
		"default", "v=DKIM1; k=rsa; p=PUBLICKEYDATA")
	if result.DKIM == nil {
		t.Fatal("expected DKIM result")
	}
	if result.DKIM.Observed == "" {
		t.Fatal("expected DKIM observed value")
	}
	jsonable := map[string]interface{}{
		"selector":    result.DKIM.Selector,
		"status":      result.DKIM.Status,
		"expected":    result.DKIM.Expected,
		"observed":    result.DKIM.Observed,
		"reason":      result.DKIM.Reason,
		"checked_at":  result.DKIM.CheckedAt,
		"record_name": result.DKIM.RecordName,
		"configured":  result.DKIM.Configured,
		"matches_dns": result.DKIM.MatchesDNS,
		"public_txt":  result.DKIM.PublicTXT,
	}
	for key := range jsonable {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "private") || strings.Contains(lower, "secret") || strings.Contains(lower, "private_key") {
			t.Errorf("DKIMHealthCheck contains sensitive field: %q", key)
		}
	}
	if strings.Contains(strings.ToLower(result.DKIM.Observed), "private") {
		t.Errorf("DKIM Observed should not contain private key words, got: %s", result.DKIM.Observed)
	}
}
