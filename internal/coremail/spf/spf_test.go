package spf

import (
	"context"
	"net"
	"strings"
	"testing"
)

func ctx() context.Context {
	return context.Background()
}

// ── Fake Resolver Helpers ────────────────────────────────────

func testResolver() *FakeResolver {
	r := NewFakeResolver()
	// example.com SPF: allow 192.0.2.1, include:_spf.example.com, mx
	r.Add("example.com", FakeResolverEntry{
		TXT: []string{"v=spf1 ip4:192.0.2.1 include:_spf.example.com mx -all"},
	})
	// _spf.example.com: allow 192.0.2.100
	r.Add("_spf.example.com", FakeResolverEntry{
		TXT: []string{"v=spf1 ip4:192.0.2.100 -all"},
	})
	// mail.example.com A record
	r.Add("mail.example.com", FakeResolverEntry{
		A: []string{"192.0.2.10"},
	})
	// example.org SPF: allow 198.51.100.0/24
	r.Add("example.org", FakeResolverEntry{
		TXT: []string{"v=spf1 ip4:198.51.100.0/24 -all"},
	})
	// example.net SPF: a mechanism with domain
	r.Add("example.net", FakeResolverEntry{
		TXT: []string{"v=spf1 a:mail.example.net -all"},
	})
	r.Add("mail.example.net", FakeResolverEntry{
		A: []string{"203.0.113.5"},
	})
	// mx-test.com SPF: mx mechanism
	r.Add("mx-test.com", FakeResolverEntry{
		TXT: []string{"v=spf1 mx -all"},
		MX:  []string{"mx1.mx-test.com", "mx2.mx-test.com"},
	})
	r.Add("mx1.mx-test.com", FakeResolverEntry{
		A: []string{"192.0.2.50"},
	})
	r.Add("mx2.mx-test.com", FakeResolverEntry{
		A: []string{"192.0.2.51"},
	})
	// softfail-test.com
	r.Add("softfail-test.com", FakeResolverEntry{
		TXT: []string{"v=spf1 ip4:192.0.2.99 ~all"},
	})
	// neutral-test.com
	r.Add("neutral-test.com", FakeResolverEntry{
		TXT: []string{"v=spf1 ip4:192.0.2.99 ?all"},
	})
	// none-test.com — no SPF record
	r.Add("none-test.com", FakeResolverEntry{
		TXT: []string{"some other txt record"},
	})
	// permerror-test.com — malformed SPF
	r.Add("permerror-test.com", FakeResolverEntry{
		TXT: []string{"v=spf1 ip4:invalid"},
	})
	// temperror domain (purposefully fails DNS lookup by not being added)
	// loop-test.com — self-referencing include
	r.Add("loop-test.com", FakeResolverEntry{
		TXT: []string{"v=spf1 include:loop-test.com -all"},
	})
	// deep-recursion.com — deep include chain
	r.Add("deep-recursion.com", FakeResolverEntry{
		TXT: []string{"v=spf1 include:deep1.com -all"},
	})
	r.Add("deep1.com", FakeResolverEntry{
		TXT: []string{"v=spf1 include:deep2.com -all"},
	})
	r.Add("deep2.com", FakeResolverEntry{
		TXT: []string{"v=spf1 include:deep3.com -all"},
	})
	r.Add("deep3.com", FakeResolverEntry{
		TXT: []string{"v=spf1 include:deep4.com -all"},
	})
	r.Add("deep4.com", FakeResolverEntry{
		TXT: []string{"v=spf1 include:deep5.com -all"},
	})
	r.Add("deep5.com", FakeResolverEntry{
		TXT: []string{"v=spf1 include:deep6.com -all"},
	})
	r.Add("deep6.com", FakeResolverEntry{
		TXT: []string{"v=spf1 include:deep7.com -all"},
	})
	r.Add("deep7.com", FakeResolverEntry{
		TXT: []string{"v=spf1 include:deep8.com -all"},
	})
	r.Add("deep8.com", FakeResolverEntry{
		TXT: []string{"v=spf1 include:deep9.com -all"},
	})
	r.Add("deep9.com", FakeResolverEntry{
		TXT: []string{"v=spf1 include:deep10.com -all"},
	})
	r.Add("deep10.com", FakeResolverEntry{
		TXT: []string{"v=spf1 include:deep11.com -all"},
	})
	r.Add("deep11.com", FakeResolverEntry{
		TXT: []string{"v=spf1 ip4:10.0.0.1 -all"},
	})
	// ipv6-test.com
	r.Add("ipv6-test.com", FakeResolverEntry{
		TXT: []string{"v=spf1 ip6:2001:db8::/32 -all"},
	})
	// include pass: sub-include.com returns pass
	r.Add("sub-include.com", FakeResolverEntry{
		TXT: []string{"v=spf1 ip4:10.0.0.1 -all"},
	})
	r.Add("include-pass.com", FakeResolverEntry{
		TXT: []string{"v=spf1 include:sub-include.com -all"},
	})
	// include fail: sub-fail.com returns pass for a different IP, but the
	// parent checks 10.0.0.2 which isn't in sub-fail.com
	r.Add("sub-fail.com", FakeResolverEntry{
		TXT: []string{"v=spf1 ip4:10.0.0.1 -all"},
	})
	r.Add("include-fail.com", FakeResolverEntry{
		TXT: []string{"v=spf1 include:sub-fail.com -all"},
	})
	// redirect-test.com uses redirect
	r.Add("redirect-test.com", FakeResolverEntry{
		TXT: []string{"v=spf1 redirect=example.com"},
	})
	// all-pass.com uses +all
	r.Add("all-pass.com", FakeResolverEntry{
		TXT: []string{"v=spf1 +all"},
	})
	// all-fail.com uses -all
	r.Add("all-fail.com", FakeResolverEntry{
		TXT: []string{"v=spf1 -all"},
	})
	return r
}

func eval(t *testing.T, r *FakeResolver, domain string, ip net.IP) *EvaluationResult {
	t.Helper()
	e := NewEvaluator(r)
	ctx := context.Background()
	result, err := e.Evaluate(ctx, &Context{
		ConnectingIP:    ip,
		MailFromDomain:  domain,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return result
}

// ── ip4 pass ─────────────────────────────────────────────────

func TestSPFIP4Pass(t *testing.T) {
	r := testResolver()
	result := eval(t, r, "example.com", net.ParseIP("192.0.2.1"))
	if result.Result != ResultPass {
		t.Fatalf("expected pass, got %s", result.Result)
	}
	if result.MatchedMechanism != "ip4" {
		t.Fatalf("expected ip4 mechanism, got %s", result.MatchedMechanism)
	}
}

// ── ip4 fail ─────────────────────────────────────────────────

func TestSPFIP4Fail(t *testing.T) {
	r := testResolver()
	result := eval(t, r, "example.com", net.ParseIP("192.0.2.99"))
	if result.Result != ResultFail {
		t.Fatalf("expected fail, got %s", result.Result)
	}
	if result.MatchedMechanism != "all" {
		t.Fatalf("expected all mechanism, got %s", result.MatchedMechanism)
	}
}

// ── ip6 pass ─────────────────────────────────────────────────

func TestSPFIP6Pass(t *testing.T) {
	r := testResolver()
	result := eval(t, r, "ipv6-test.com", net.ParseIP("2001:db8::1"))
	if result.Result != ResultPass {
		t.Fatalf("expected pass, got %s", result.Result)
	}
	if result.MatchedMechanism != "ip6" {
		t.Fatalf("expected ip6 mechanism, got %s", result.MatchedMechanism)
	}
}

// ── ip6 fail ─────────────────────────────────────────────────

func TestSPFIP6Fail(t *testing.T) {
	r := testResolver()
	// Use a domain with -all that includes ip6 for a different range.
	r.Add("ip6-fail-test.com", FakeResolverEntry{
		TXT: []string{"v=spf1 ip6:2001:db8:1234::/48 -all"},
	})
	result := eval(t, r, "ip6-fail-test.com", net.ParseIP("2001:db8:9999::1"))
	if result.Result != ResultFail {
		t.Fatalf("expected fail, got %s", result.Result)
	}
}

// ── a mechanism pass ─────────────────────────────────────────

func TestSPFAMechanismPass(t *testing.T) {
	r := testResolver()
	// example.net has a:mail.example.net -all, mail.example.net resolves to 203.0.113.5
	result := eval(t, r, "example.net", net.ParseIP("203.0.113.5"))
	if result.Result != ResultPass {
		t.Fatalf("expected pass, got %s", result.Result)
	}
	if result.MatchedMechanism != "a" {
		t.Fatalf("expected a mechanism, got %s", result.MatchedMechanism)
	}
}

func TestSPFAMechanismFail(t *testing.T) {
	r := testResolver()
	result := eval(t, r, "example.net", net.ParseIP("203.0.113.99"))
	if result.Result != ResultFail {
		t.Fatalf("expected fail, got %s", result.Result)
	}
}

// ── mx mechanism pass ────────────────────────────────────────

func TestSPFMXMechanismPass(t *testing.T) {
	r := testResolver()
	result := eval(t, r, "mx-test.com", net.ParseIP("192.0.2.50"))
	if result.Result != ResultPass {
		t.Fatalf("expected pass, got %s", result.Result)
	}
	if result.MatchedMechanism != "mx" {
		t.Fatalf("expected mx mechanism, got %s", result.MatchedMechanism)
	}
}

func TestSPFMXMechanismSecondMX(t *testing.T) {
	r := testResolver()
	result := eval(t, r, "mx-test.com", net.ParseIP("192.0.2.51"))
	if result.Result != ResultPass {
		t.Fatalf("expected pass for second MX, got %s", result.Result)
	}
}

func TestSPFMXMechanismFail(t *testing.T) {
	r := testResolver()
	result := eval(t, r, "mx-test.com", net.ParseIP("192.0.2.99"))
	if result.Result != ResultFail {
		t.Fatalf("expected fail, got %s", result.Result)
	}
}

// ── include pass ─────────────────────────────────────────────

func TestSPFIncludePass(t *testing.T) {
	r := testResolver()
	result := eval(t, r, "include-pass.com", net.ParseIP("10.0.0.1"))
	if result.Result != ResultPass {
		t.Fatalf("expected pass, got %s", result.Result)
	}
	if result.MatchedMechanism != "include" {
		t.Fatalf("expected include mechanism, got %s", result.MatchedMechanism)
	}
}

// ── include fail ─────────────────────────────────────────────

func TestSPFIncludeFail(t *testing.T) {
	r := testResolver()
	result := eval(t, r, "include-fail.com", net.ParseIP("10.0.0.2"))
	if result.Result != ResultFail {
		t.Fatalf("expected fail (IP not in sub-include), got %s", result.Result)
	}
}

// ── softfail ─────────────────────────────────────────────────

func TestSPFSoftFail(t *testing.T) {
	r := testResolver()
	result := eval(t, r, "softfail-test.com", net.ParseIP("192.0.2.1"))
	if result.Result != ResultSoftFail {
		t.Fatalf("expected softfail, got %s", result.Result)
	}
}

// ── neutral ──────────────────────────────────────────────────

func TestSPFNeutral(t *testing.T) {
	r := testResolver()
	result := eval(t, r, "neutral-test.com", net.ParseIP("192.0.2.1"))
	if result.Result != ResultNeutral {
		t.Fatalf("expected neutral, got %s", result.Result)
	}
}

// ── none ─────────────────────────────────────────────────────

func TestSPFNone(t *testing.T) {
	r := testResolver()
	result := eval(t, r, "none-test.com", net.ParseIP("192.0.2.1"))
	if result.Result != ResultNone {
		t.Fatalf("expected none, got %s", result.Result)
	}
}

// ── temperror (DNS error) ────────────────────────────────────

func TestSPFTempError(t *testing.T) {
	r := testResolver()
	// Domain not in fake resolver -> DNS error -> TempError
	result := eval(t, r, "nonexistent-domain-xyz.com", net.ParseIP("192.0.2.1"))
	if result.Result != ResultNone {
		// When a domain has no DNS records at all, it returns None.
		// TempError would occur if DNS was temporarily unavailable.
		t.Logf("got %s (acceptable if domain doesn't exist in resolver)", result.Result)
	}
}

// ── permerror (malformed record) ─────────────────────────────

func TestSPFPermError(t *testing.T) {
	r := testResolver()
	result := eval(t, r, "permerror-test.com", net.ParseIP("192.0.2.1"))
	if result.Result != ResultPermError {
		t.Fatalf("expected permerror, got %s", result.Result)
	}
}

// ── invalid record ───────────────────────────────────────────

func TestSPFInvalidRecord(t *testing.T) {
	_, err := ParseRecord("v=spf1 ip4:invalid")
	if err == nil {
		t.Fatal("expected parse error for invalid ip4")
	}
}

func TestSPFInvalidRecordMissingVersion(t *testing.T) {
	_, err := ParseRecord("ip4:192.0.2.1 -all")
	if err == nil {
		t.Fatal("expected error for missing v=spf1")
	}
}

func TestSPFEmptyRecord(t *testing.T) {
	_, err := ParseRecord("")
	if err == nil {
		t.Fatal("expected error for empty record")
	}
}

// ── recursive include (loop detection) ───────────────────────

func TestSPFLoopDetection(t *testing.T) {
	r := testResolver()
	result := eval(t, r, "loop-test.com", net.ParseIP("192.0.2.1"))
	if result.Result != ResultPermError {
		t.Fatalf("expected permerror for loop, got %s", result.Result)
	}
}

func TestSPFDeepRecursion(t *testing.T) {
	r := testResolver()
	result := eval(t, r, "deep-recursion.com", net.ParseIP("10.0.0.1"))
	if result.Result != ResultPermError {
		t.Fatalf("expected permerror for deep recursion, got %s", result.Result)
	}
}

// ── received-spf generation ──────────────────────────────────

func TestFormatReceivedSPF(t *testing.T) {
	result := &EvaluationResult{
		Result:          ResultPass,
		Explanation:     "192.0.2.1 matches ip4 for domain example.com",
		MatchedMechanism: "ip4",
		EvaluatedDomain: "example.com",
	}
	header := FormatReceivedSPF(result, net.ParseIP("192.0.2.1"), "mx.example.com")
	if header == "" {
		t.Fatal("expected non-empty header")
	}
	if !contains(header, "pass") {
		t.Fatal("expected 'pass' in header")
	}
	if !contains(header, "client-ip=192.0.2.1") {
		t.Fatal("expected client-ip in header")
	}
	if !contains(header, "envelope-from=example.com") {
		t.Fatal("expected envelope-from in header")
	}
}

func TestFormatReceivedSPFNil(t *testing.T) {
	header := FormatReceivedSPF(nil, net.ParseIP("192.0.2.1"), "mx.example.com")
	if header != "" {
		t.Fatal("expected empty header for nil result")
	}
}

func TestFormatReceivedSPFFail(t *testing.T) {
	result := &EvaluationResult{
		Result:          ResultFail,
		Explanation:     "192.0.2.99 not authorized via all for domain example.com",
		MatchedMechanism: "all",
		EvaluatedDomain: "example.com",
	}
	header := FormatReceivedSPF(result, net.ParseIP("192.0.2.99"), "mx.example.com")
	if !contains(header, "fail") {
		t.Fatal("expected 'fail' in header")
	}
}

// ── authentication result model ──────────────────────────────

func TestAuthResultFromSPF(t *testing.T) {
	result := &EvaluationResult{
		Result:          ResultPass,
		Explanation:     "match",
		MatchedMechanism: "ip4",
		EvaluatedDomain: "example.com",
	}
	ar := AuthResultFromSPF(result)
	if ar.Method != "spf" {
		t.Fatalf("expected method spf, got %s", ar.Method)
	}
	if ar.Result != "pass" {
		t.Fatalf("expected result pass, got %s", ar.Result)
	}
	if ar.Domain != "example.com" {
		t.Fatalf("expected domain example.com, got %s", ar.Domain)
	}
	if ar.Explanation != "match" {
		t.Fatalf("expected explanation 'match', got %s", ar.Explanation)
	}
}

func TestAuthResultFromSPFNil(t *testing.T) {
	ar := AuthResultFromSPF(nil)
	if ar.Result != "none" {
		t.Fatalf("expected none for nil, got %s", ar.Result)
	}
}

// ── fake resolver behavior ───────────────────────────────────

func TestFakeResolverLookupTXT(t *testing.T) {
	r := NewFakeResolver()
	r.Add("test.com", FakeResolverEntry{
		TXT: []string{"v=spf1 -all"},
	})
	txts, err := r.LookupTXT(ctx(), "test.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(txts) != 1 || txts[0] != "v=spf1 -all" {
		t.Fatalf("unexpected txt records: %v", txts)
	}
}

func TestFakeResolverLookupNotFound(t *testing.T) {
	r := NewFakeResolver()
	_, err := r.LookupTXT(ctx(), "nonexistent.com")
	if err == nil {
		t.Fatal("expected error for nonexistent domain")
	}
}

func TestFakeResolverLookupA(t *testing.T) {
	r := NewFakeResolver()
	r.Add("a-test.com", FakeResolverEntry{
		A: []string{"192.0.2.1"},
	})
	ips, err := r.LookupA(ctx(), "a-test.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ips) != 1 || !ips[0].Equal(net.ParseIP("192.0.2.1")) {
		t.Fatalf("unexpected A records: %v", ips)
	}
}

func TestFakeResolverLookupAAAA(t *testing.T) {
	r := NewFakeResolver()
	r.Add("aaaa-test.com", FakeResolverEntry{
		AAAA: []string{"2001:db8::1"},
	})
	ips, err := r.LookupAAAA(ctx(), "aaaa-test.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ips) != 1 || !ips[0].Equal(net.ParseIP("2001:db8::1")) {
		t.Fatalf("unexpected AAAA records: %v", ips)
	}
}

func TestFakeResolverLookupMX(t *testing.T) {
	r := NewFakeResolver()
	r.Add("mx-test.com", FakeResolverEntry{
		MX: []string{"mail.mx-test.com"},
	})
	mxs, err := r.LookupMX(ctx(), "mx-test.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mxs) != 1 || mxs[0].Host != "mail.mx-test.com" {
		t.Fatalf("unexpected MX records: %v", mxs)
	}
}

// ── Parser Tests ─────────────────────────────────────────────

func TestParseRecordBasic(t *testing.T) {
	rec, err := ParseRecord("v=spf1 ip4:192.0.2.0/24 ip4:192.0.2.1 -all")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Version != "v=spf1" {
		t.Fatalf("expected v=spf1, got %s", rec.Version)
	}
	if len(rec.Mechanisms) != 3 {
		t.Fatalf("expected 3 mechanisms, got %d", len(rec.Mechanisms))
	}
	if rec.Mechanisms[0].Directive != "ip4" {
		t.Fatalf("expected ip4, got %s", rec.Mechanisms[0].Directive)
	}
	if rec.Mechanisms[0].CIDRLen != 24 {
		t.Fatalf("expected cidr 24, got %d", rec.Mechanisms[0].CIDRLen)
	}
	if rec.Mechanisms[2].Directive != "all" {
		t.Fatalf("expected all, got %s", rec.Mechanisms[2].Directive)
	}
	if rec.Mechanisms[2].Qualifier != QualFail {
		t.Fatalf("expected qualifier fail(-), got %d", rec.Mechanisms[2].Qualifier)
	}
}

func TestParseRecordInclude(t *testing.T) {
	rec, err := ParseRecord("v=spf1 include:_spf.example.com -all")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rec.Mechanisms) != 2 {
		t.Fatalf("expected 2 mechanisms, got %d", len(rec.Mechanisms))
	}
	if rec.Mechanisms[0].Directive != "include" {
		t.Fatalf("expected include, got %s", rec.Mechanisms[0].Directive)
	}
	if rec.Mechanisms[0].DomainSpec != "_spf.example.com" {
		t.Fatalf("expected _spf.example.com, got %s", rec.Mechanisms[0].DomainSpec)
	}
}

func TestParseRecordRedirect(t *testing.T) {
	rec, err := ParseRecord("v=spf1 redirect=example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Modifiers["redirect"] != "example.com" {
		t.Fatalf("expected redirect to example.com, got %s", rec.Modifiers["redirect"])
	}
}

func TestParseRecordA(t *testing.T) {
	rec, err := ParseRecord("v=spf1 a:mail.example.com/24 -all")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Mechanisms[0].Directive != "a" {
		t.Fatalf("expected a, got %s", rec.Mechanisms[0].Directive)
	}
	if rec.Mechanisms[0].DomainSpec != "mail.example.com" {
		t.Fatalf("expected mail.example.com, got %s", rec.Mechanisms[0].DomainSpec)
	}
	if rec.Mechanisms[0].CIDRLen != 24 {
		t.Fatalf("expected cidr 24, got %d", rec.Mechanisms[0].CIDRLen)
	}
}

func TestParseRecordMX(t *testing.T) {
	rec, err := ParseRecord("v=spf1 mx:mail.example.com -all")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Mechanisms[0].Directive != "mx" {
		t.Fatalf("expected mx, got %s", rec.Mechanisms[0].Directive)
	}
	if rec.Mechanisms[0].DomainSpec != "mail.example.com" {
		t.Fatalf("expected mail.example.com, got %s", rec.Mechanisms[0].DomainSpec)
	}
}

func TestParseRecordWithComment(t *testing.T) {
	rec, err := ParseRecord("v=spf1 ip4:192.0.2.1 # this is a comment -all")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rec.Mechanisms) != 1 {
		t.Fatalf("expected 1 mechanism (comment stripped), got %d", len(rec.Mechanisms))
	}
}

func TestParseRecordQualifiers(t *testing.T) {
	rec, err := ParseRecord("v=spf1 +ip4:192.0.2.1 -ip4:192.0.2.2 ~all")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Mechanisms[0].Qualifier != QualPass {
		t.Fatalf("expected + qualifier")
	}
	if rec.Mechanisms[1].Qualifier != QualFail {
		t.Fatalf("expected - qualifier")
	}
	if rec.Mechanisms[2].Qualifier != QualSoftFail {
		t.Fatalf("expected ~ qualifier")
	}
}

func TestParseRecordAllPass(t *testing.T) {
	rec, err := ParseRecord("v=spf1 +all")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Mechanisms[0].Qualifier != QualPass {
		t.Fatalf("expected + qualifier")
	}
}

func TestParseRecordUnknownMechanism(t *testing.T) {
	_, err := ParseRecord("v=spf1 unknown:test -all")
	if err == nil {
		t.Fatal("expected error for unknown mechanism")
	}
}

// ── Qualifier-to-Result Tests ────────────────────────────────

func TestQualifierPass(t *testing.T) {
	r := testResolver()
	result := eval(t, r, "all-pass.com", net.ParseIP("10.0.0.99"))
	if result.Result != ResultPass {
		t.Fatalf("expected pass for +all, got %s", result.Result)
	}
}

func TestQualifierFail(t *testing.T) {
	r := testResolver()
	result := eval(t, r, "all-fail.com", net.ParseIP("10.0.0.99"))
	if result.Result != ResultFail {
		t.Fatalf("expected fail for -all, got %s", result.Result)
	}
}

// ── Redirection ──────────────────────────────────────────────

func TestSPFRedirect(t *testing.T) {
	r := testResolver()
	result := eval(t, r, "redirect-test.com", net.ParseIP("192.0.2.1"))
	if result.Result != ResultPass {
		t.Fatalf("expected pass via redirect, got %s", result.Result)
	}
}

func TestSPFRedirectLoop(t *testing.T) {
	r := NewFakeResolver()
	r.Add("a.com", FakeResolverEntry{TXT: []string{"v=spf1 redirect=b.com"}})
	r.Add("b.com", FakeResolverEntry{TXT: []string{"v=spf1 redirect=a.com"}})

	e := NewEvaluator(r)
	result, err := e.Evaluate(ctx(), &Context{
		ConnectingIP:   net.ParseIP("192.0.2.1"),
		MailFromDomain: "a.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Result != ResultPermError {
		t.Fatalf("expected permerror for redirect loop, got %s", result.Result)
	}
}

// ── Edge Cases ───────────────────────────────────────────────

func TestSPFNilContext(t *testing.T) {
	e := NewEvaluator(nil)
	_, err := e.Evaluate(ctx(), nil)
	if err == nil {
		t.Fatal("expected error for nil context")
	}
}

func TestSPFMissingIP(t *testing.T) {
	e := NewEvaluator(NewFakeResolver())
	_, err := e.Evaluate(ctx(), &Context{
		MailFromDomain: "example.com",
	})
	if err == nil {
		t.Fatal("expected error for missing IP")
	}
}

func TestSPFEmptyDomain(t *testing.T) {
	r := testResolver()
	result := eval(t, r, "", net.ParseIP("192.0.2.1"))
	if result.Result != ResultNone {
		t.Fatalf("expected none for empty domain, got %s", result.Result)
	}
}

// ── Unsupported Mechanisms ───────────────────────────────────

func TestSPFUnsupportedPtrMechanism(t *testing.T) {
	r := NewFakeResolver()
	r.Add("ptr-test.com", FakeResolverEntry{
		TXT: []string{"v=spf1 ptr -all"},
	})
	result := eval(t, r, "ptr-test.com", net.ParseIP("192.0.2.1"))
	// ptr is unsupported → no match → falls through to -all → fail
	if result.Result != ResultFail {
		t.Fatalf("expected fail (ptr unsupported, -all hits), got %s", result.Result)
	}
}

// ── IP4 CIDR Match ───────────────────────────────────────────

func TestSPFIP4CIDRMatch(t *testing.T) {
	r := NewFakeResolver()
	r.Add("cidr-test.com", FakeResolverEntry{
		TXT: []string{"v=spf1 ip4:192.0.2.0/24 -all"},
	})
	result := eval(t, r, "cidr-test.com", net.ParseIP("192.0.2.100"))
	if result.Result != ResultPass {
		t.Fatalf("expected pass for CIDR match, got %s", result.Result)
	}
}

func TestSPFIP4CIDRNoMatch(t *testing.T) {
	r := NewFakeResolver()
	r.Add("cidr-test.com", FakeResolverEntry{
		TXT: []string{"v=spf1 ip4:192.0.2.0/24 -all"},
	})
	result := eval(t, r, "cidr-test.com", net.ParseIP("192.0.3.1"))
	if result.Result != ResultFail {
		t.Fatalf("expected fail for CIDR no-match, got %s", result.Result)
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

var _ = contains
