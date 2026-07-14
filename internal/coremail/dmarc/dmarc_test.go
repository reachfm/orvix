package dmarc

import (
	"strings"
	"testing"
)

// ── Fake Resolver for DMARC Tests ────────────────────────────

type fakeResolver struct {
	records map[string]string // domain -> TXT record
}

func newFakeResolver() *fakeResolver {
	return &fakeResolver{records: make(map[string]string)}
}

func (f *fakeResolver) add(domain, txt string) {
	f.records[domain] = txt
}

func (f *fakeResolver) LookupTXT(domain string) ([]string, error) {
	txt, ok := f.records[domain]
	if !ok {
		return []string{}, nil
	}
	return []string{txt}, nil
}

type dnsError struct {
	msg  string
	name string
}

func (e *dnsError) Error() string   { return e.msg }
func (e *dnsError) Timeout() bool   { return false }
func (e *dnsError) Temporary() bool { return true }

// ── Parser Tests ─────────────────────────────────────────────

func TestParseValidRecord(t *testing.T) {
	rec, err := ParseRecord("v=DMARC1; p=none; sp=quarantine; pct=50; rua=mailto:dmarc@example.com; ruf=mailto:ruf@example.com; adkim=r; aspf=s")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Version != "DMARC1" {
		t.Fatalf("expected DMARC1, got %s", rec.Version)
	}
	if rec.Policy != PolicyNone {
		t.Fatalf("expected none, got %s", rec.Policy)
	}
	if rec.SubdomainPol != PolicyQuarantine {
		t.Fatalf("expected quarantine, got %s", rec.SubdomainPol)
	}
	if rec.Pct != 50 {
		t.Fatalf("expected 50, got %d", rec.Pct)
	}
	if rec.RUA != "mailto:dmarc@example.com" {
		t.Fatalf("unexpected rua: %s", rec.RUA)
	}
	if rec.RUF != "mailto:ruf@example.com" {
		t.Fatalf("unexpected ruf: %s", rec.RUF)
	}
	if rec.ADKIM != AlignmentRelaxed {
		t.Fatalf("expected relaxed adkim")
	}
	if rec.ASPF != AlignmentStrict {
		t.Fatalf("expected strict aspf")
	}
}

func TestParseRecordPolicyReject(t *testing.T) {
	rec, err := ParseRecord("v=DMARC1; p=reject")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Policy != PolicyReject {
		t.Fatalf("expected reject, got %s", rec.Policy)
	}
}

func TestParseRecordPolicyQuarantine(t *testing.T) {
	rec, err := ParseRecord("v=DMARC1; p=quarantine")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Policy != PolicyQuarantine {
		t.Fatalf("expected quarantine, got %s", rec.Policy)
	}
}

func TestParseRecordMissingVersion(t *testing.T) {
	_, err := ParseRecord("p=none")
	if err == nil {
		t.Fatal("expected error for missing v tag")
	}
}

func TestParseRecordInvalidVersion(t *testing.T) {
	_, err := ParseRecord("v=DMARC2; p=none")
	if err == nil {
		t.Fatal("expected error for invalid version")
	}
}

func TestParseRecordMissingPolicy(t *testing.T) {
	_, err := ParseRecord("v=DMARC1")
	if err == nil {
		t.Fatal("expected error for missing p tag")
	}
}

func TestParseRecordInvalidPolicy(t *testing.T) {
	_, err := ParseRecord("v=DMARC1; p=invalid")
	if err == nil {
		t.Fatal("expected error for invalid policy")
	}
}

func TestParseRecordADKIMStrict(t *testing.T) {
	rec, err := ParseRecord("v=DMARC1; p=none; adkim=s")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.ADKIM != AlignmentStrict {
		t.Fatal("expected strict adkim")
	}
}

func TestParseRecordASPFStrict(t *testing.T) {
	rec, err := ParseRecord("v=DMARC1; p=none; aspf=s")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.ASPF != AlignmentStrict {
		t.Fatal("expected strict aspf")
	}
}

func TestParseRecordSubdomainInherits(t *testing.T) {
	rec, err := ParseRecord("v=DMARC1; p=reject")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.SubdomainPol != PolicyReject {
		t.Fatalf("expected subdomain policy to inherit reject, got %s", rec.SubdomainPol)
	}
}

func TestParseRecordEmptyRecord(t *testing.T) {
	_, err := ParseRecord("")
	if err == nil {
		t.Fatal("expected error for empty record")
	}
}

func TestParseRecordPctInvalid(t *testing.T) {
	_, err := ParseRecord("v=DMARC1; p=none; pct=101")
	if err == nil {
		t.Fatal("expected error for pct > 100")
	}
}

func TestParseRecordPctNegative(t *testing.T) {
	_, err := ParseRecord("v=DMARC1; p=none; pct=-1")
	if err == nil {
		t.Fatal("expected error for negative pct")
	}
}

func TestParseRecordMalformedTag(t *testing.T) {
	_, err := ParseRecord("v=DMARC1; p none")
	if err == nil {
		t.Fatal("expected error for missing =")
	}
}

// ── Alignment Tests ──────────────────────────────────────────

func TestSPFAlignmentRelaxedPass(t *testing.T) {
	if !CheckSPFAlignment("example.com", "mail.example.com", AlignmentRelaxed) {
		t.Fatal("expected SPF relaxed alignment to pass")
	}
}

func TestSPFAlignmentStrictPass(t *testing.T) {
	if !CheckSPFAlignment("example.com", "example.com", AlignmentStrict) {
		t.Fatal("expected SPF strict alignment to pass")
	}
}

func TestSPFAlignmentStrictFail(t *testing.T) {
	if CheckSPFAlignment("example.com", "mail.example.com", AlignmentStrict) {
		t.Fatal("expected SPF strict alignment to fail")
	}
}

func TestDKIMAlignmentRelaxedPass(t *testing.T) {
	if !CheckDKIMAlignment("example.com", "mail.example.com", AlignmentRelaxed) {
		t.Fatal("expected DKIM relaxed alignment to pass")
	}
}

func TestDKIMAlignmentStrictPass(t *testing.T) {
	if !CheckDKIMAlignment("example.com", "example.com", AlignmentStrict) {
		t.Fatal("expected DKIM strict alignment to pass")
	}
}

func TestDKIMAlignmentStrictFail(t *testing.T) {
	if CheckDKIMAlignment("example.com", "mail.example.com", AlignmentStrict) {
		t.Fatal("expected DKIM strict alignment to fail")
	}
}

func TestSPFAlignmentDifferentOrgDomain(t *testing.T) {
	if CheckSPFAlignment("example.com", "example.org", AlignmentRelaxed) {
		t.Fatal("expected SPF relaxed alignment to fail for different org domains")
	}
}

func TestDKIMAlignmentDifferentOrgDomain(t *testing.T) {
	if CheckDKIMAlignment("example.com", "example.org", AlignmentRelaxed) {
		t.Fatal("expected DKIM relaxed alignment to fail for different org domains")
	}
}

func TestSPFAlignmentEmptyDomain(t *testing.T) {
	if CheckSPFAlignment("", "example.com", AlignmentRelaxed) {
		t.Fatal("expected false for empty from domain")
	}
	if CheckSPFAlignment("example.com", "", AlignmentRelaxed) {
		t.Fatal("expected false for empty spf domain")
	}
}

// ── Evaluation Tests ─────────────────────────────────────────

func evalWithResolver(t *testing.T, resolver *fakeResolver, input *EvaluationInput) *EvaluationResult {
	t.Helper()
	e := NewEvaluator(resolver)
	result, err := e.Evaluate(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return result
}

func TestSPFAlignedOnlyPass(t *testing.T) {
	r := newFakeResolver()
	r.add("_dmarc.example.com", "v=DMARC1; p=none")

	result := evalWithResolver(t, r, &EvaluationInput{
		FromDomain:    "example.com",
		SPFResult:     "pass",
		SPFAuthDomain: "example.com",
		DKIMResult:    "none",
	})
	if result.Result != ResultPass {
		t.Fatalf("expected pass, got %s", result.Result)
	}
	if !result.SPFAligned {
		t.Fatal("expected SPF aligned")
	}
}

func TestDKIMAlignedOnlyPass(t *testing.T) {
	r := newFakeResolver()
	r.add("_dmarc.example.com", "v=DMARC1; p=none")

	result := evalWithResolver(t, r, &EvaluationInput{
		FromDomain:        "example.com",
		SPFResult:         "none",
		DKIMResult:        "pass",
		DKIMSigningDomain: "example.com",
	})
	if result.Result != ResultPass {
		t.Fatalf("expected pass, got %s", result.Result)
	}
	if !result.DKIMAligned {
		t.Fatal("expected DKIM aligned")
	}
}

func TestBothPass(t *testing.T) {
	r := newFakeResolver()
	r.add("_dmarc.example.com", "v=DMARC1; p=reject")

	result := evalWithResolver(t, r, &EvaluationInput{
		FromDomain:        "example.com",
		SPFResult:         "pass",
		SPFAuthDomain:     "example.com",
		DKIMResult:        "pass",
		DKIMSigningDomain: "example.com",
	})
	if result.Result != ResultPass {
		t.Fatalf("expected pass, got %s", result.Result)
	}
	if !result.SPFAligned || !result.DKIMAligned {
		t.Fatal("expected both aligned")
	}
}

func TestBothFail(t *testing.T) {
	r := newFakeResolver()
	r.add("_dmarc.example.com", "v=DMARC1; p=reject")

	result := evalWithResolver(t, r, &EvaluationInput{
		FromDomain:    "example.com",
		SPFResult:     "fail",
		SPFAuthDomain: "other.com",
		DKIMResult:    "fail",
	})
	if result.Result != ResultFail {
		t.Fatalf("expected fail, got %s", result.Result)
	}
}

func TestPolicyNone(t *testing.T) {
	r := newFakeResolver()
	r.add("_dmarc.example.com", "v=DMARC1; p=none")

	result := evalWithResolver(t, r, &EvaluationInput{
		FromDomain:    "example.com",
		SPFResult:     "fail",
		SPFAuthDomain: "other.com",
		DKIMResult:    "fail",
	})
	if result.Result != ResultFail {
		t.Fatalf("expected fail, got %s", result.Result)
	}
	if result.Policy != PolicyNone {
		t.Fatalf("expected policy none, got %s", result.Policy)
	}
}

func TestPolicyQuarantine(t *testing.T) {
	r := newFakeResolver()
	r.add("_dmarc.example.com", "v=DMARC1; p=quarantine")

	result := evalWithResolver(t, r, &EvaluationInput{
		FromDomain:    "example.com",
		SPFResult:     "pass",
		SPFAuthDomain: "example.com",
		DKIMResult:    "none",
	})
	if result.Policy != PolicyQuarantine {
		t.Fatalf("expected quarantine, got %s", result.Policy)
	}
}

func TestPolicyReject(t *testing.T) {
	r := newFakeResolver()
	r.add("_dmarc.example.com", "v=DMARC1; p=reject")

	result := evalWithResolver(t, r, &EvaluationInput{
		FromDomain:    "example.com",
		SPFResult:     "pass",
		SPFAuthDomain: "example.com",
		DKIMResult:    "none",
	})
	if result.Policy != PolicyReject {
		t.Fatalf("expected reject, got %s", result.Policy)
	}
}

func TestSubdomainPolicy(t *testing.T) {
	r := newFakeResolver()
	r.add("_dmarc.example.com", "v=DMARC1; p=none; sp=quarantine")

	result := evalWithResolver(t, r, &EvaluationInput{
		FromDomain:    "sub.example.com",
		SPFResult:     "fail",
		SPFAuthDomain: "other.com",
		DKIMResult:    "fail",
	})
	if result.SubdomainPolicy != PolicyQuarantine {
		t.Fatalf("expected quarantine for subdomain, got %s", result.SubdomainPolicy)
	}
}

func TestSubdomainPolicyInherits(t *testing.T) {
	r := newFakeResolver()
	r.add("_dmarc.example.com", "v=DMARC1; p=reject")

	result := evalWithResolver(t, r, &EvaluationInput{
		FromDomain:    "sub.example.com",
		SPFResult:     "fail",
		SPFAuthDomain: "other.com",
		DKIMResult:    "fail",
	})
	if result.SubdomainPolicy != PolicyReject {
		t.Fatalf("expected subdomain policy to inherit reject, got %s", result.SubdomainPolicy)
	}
}

func TestPctHandling(t *testing.T) {
	r := newFakeResolver()
	r.add("_dmarc.example.com", "v=DMARC1; p=reject; pct=50")

	result := evalWithResolver(t, r, &EvaluationInput{
		FromDomain:    "example.com",
		SPFResult:     "pass",
		SPFAuthDomain: "example.com",
		DKIMResult:    "pass",
	})
	if result.Pct != 50 {
		t.Fatalf("expected pct 50, got %d", result.Pct)
	}
}

func TestNoneResult(t *testing.T) {
	r := newFakeResolver()
	r.add("_dmarc.example.com", "v=DMARC1; p=none")

	result := evalWithResolver(t, r, &EvaluationInput{
		FromDomain:    "example.com",
		SPFResult:     "none",
		SPFAuthDomain: "",
		DKIMResult:    "none",
	})
	if result.Result != ResultNone {
		t.Fatalf("expected none, got %s", result.Result)
	}
}

func TestNoDMARCRecord(t *testing.T) {
	r := newFakeResolver()
	result := evalWithResolver(t, r, &EvaluationInput{
		FromDomain: "nodmarc.com",
		SPFResult:  "pass",
		DKIMResult: "pass",
	})
	if result.Result != ResultNone {
		t.Fatalf("expected none for no DMARC record, got %s", result.Result)
	}
}

func TestPermError(t *testing.T) {
	r := newFakeResolver()
	r.add("_dmarc.example.com", "v=DMARC1; p=invalid")

	result := evalWithResolver(t, r, &EvaluationInput{
		FromDomain: "example.com",
	})
	if result.Result != ResultPermError {
		t.Fatalf("expected permerror, got %s", result.Result)
	}
}

func TestTempError(t *testing.T) {
	// Custom resolver that returns a transient DNS error.
	r := &transientResolver{}
	e := NewEvaluator(r)
	result, err := e.Evaluate(&EvaluationInput{
		FromDomain: "example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Result != ResultTempError {
		t.Fatalf("expected temperror, got %s", result.Result)
	}
}

type transientResolver struct{}

func (r *transientResolver) LookupTXT(domain string) ([]string, error) {
	return nil, &dnsError{msg: "temporary DNS failure", name: domain}
}

// ── Alignment Edge Cases ─────────────────────────────────────

func TestSameOrganizationalDomain(t *testing.T) {
	cases := []struct {
		a, b   string
		expect bool
	}{
		{"example.com", "example.com", true},
		{"example.com", "mail.example.com", true},
		{"example.com", "Example.com", true},
		{"example.com", "example.org", false},
		{"example.co.uk", "sub.example.co.uk", true},
		{"example.com", "sub.example.co.uk", false},
	}
	for _, c := range cases {
		got := sameOrganizationalDomain(c.a, c.b)
		if got != c.expect {
			t.Errorf("sameOrganizationalDomain(%q, %q) = %v, want %v", c.a, c.b, got, c.expect)
		}
	}
}

// ── Authentication-Results Header Tests ──────────────────────

func TestFormatAuthResults(t *testing.T) {
	results := &AuthResultList{
		SPF:   &AuthResult{Method: "spf", Result: "pass", Domain: "example.com"},
		DKIM:  &AuthResult{Method: "dkim", Result: "pass", Domain: "example.com"},
		DMARC: &AuthResult{Method: "dmarc", Result: "pass", Domain: "example.com"},
	}
	header := FormatAuthResults(results, "mx.example.com", nil)
	if header == "" {
		t.Fatal("expected non-empty header")
	}
	if !strings.Contains(header, "spf=pass") {
		t.Fatal("expected spf=pass")
	}
	if !strings.Contains(header, "dkim=pass") {
		t.Fatal("expected dkim=pass")
	}
	if !strings.Contains(header, "dmarc=pass") {
		t.Fatal("expected dmarc=pass")
	}
}

func TestFormatAuthResultsPartial(t *testing.T) {
	results := &AuthResultList{
		SPF: &AuthResult{Method: "spf", Result: "fail", Domain: "attacker.com"},
	}
	header := FormatAuthResults(results, "mx.example.com", nil)
	if !strings.Contains(header, "spf=fail") {
		t.Fatal("expected spf=fail")
	}
	if strings.Contains(header, "dkim=") {
		t.Fatal("expected no dkim result")
	}
}

func TestFormatAuthResultsNil(t *testing.T) {
	header := FormatAuthResults(nil, "mx.example.com", nil)
	if header != "" {
		t.Fatal("expected empty for nil")
	}
}

// ── AuthResultFromDMARC Tests ────────────────────────────────

func TestAuthResultFromDMARC(t *testing.T) {
	er := &EvaluationResult{
		Result:          ResultPass,
		EvaluatedDomain: "example.com",
		Explanation:     "aligned",
	}
	ar := AuthResultFromDMARC(er)
	if ar.Method != "dmarc" {
		t.Fatalf("expected dmarc, got %s", ar.Method)
	}
	if ar.Result != "pass" {
		t.Fatalf("expected pass, got %s", ar.Result)
	}
	if ar.Domain != "example.com" {
		t.Fatalf("expected example.com, got %s", ar.Domain)
	}
}

func TestAuthResultFromDMARCNil(t *testing.T) {
	ar := AuthResultFromDMARC(nil)
	if ar.Result != "none" {
		t.Fatalf("expected none for nil, got %s", ar.Result)
	}
}

// ── Policy Explanation Tests ─────────────────────────────────

func TestPolicyExplanation(t *testing.T) {
	if PolicyExplanation(PolicyNone) != "no action required" {
		t.Fatal("unexpected none explanation")
	}
	if PolicyExplanation(PolicyQuarantine) != "message should be quarantined" {
		t.Fatal("unexpected quarantine explanation")
	}
	if PolicyExplanation(PolicyReject) != "message should be rejected" {
		t.Fatal("unexpected reject explanation")
	}
}

// ── Edge Cases ───────────────────────────────────────────────

func TestNilInput(t *testing.T) {
	e := NewEvaluator(newFakeResolver())
	_, err := e.Evaluate(nil)
	if err == nil {
		t.Fatal("expected error for nil input")
	}
}

func TestEmptyFromDomain(t *testing.T) {
	r := newFakeResolver()
	result := evalWithResolver(t, r, &EvaluationInput{})
	if result.Result != ResultNone {
		t.Fatalf("expected none for empty from domain, got %s", result.Result)
	}
}

func TestDefaultValues(t *testing.T) {
	rec, err := ParseRecord("v=DMARC1; p=reject")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Pct != 100 {
		t.Fatalf("expected default pct 100, got %d", rec.Pct)
	}
	if rec.ADKIM != AlignmentRelaxed {
		t.Fatal("expected default adkim relaxed")
	}
	if rec.ASPF != AlignmentRelaxed {
		t.Fatal("expected default aspf relaxed")
	}
}

func TestADKIMInvalid(t *testing.T) {
	_, err := ParseRecord("v=DMARC1; p=none; adkim=x")
	if err == nil {
		t.Fatal("expected error for invalid adkim")
	}
}

func TestASPFInvalid(t *testing.T) {
	_, err := ParseRecord("v=DMARC1; p=none; aspf=x")
	if err == nil {
		t.Fatal("expected error for invalid aspf")
	}
}

func TestSimultaneousSPFDKIMFail(t *testing.T) {
	r := newFakeResolver()
	r.add("_dmarc.example.com", "v=DMARC1; p=reject")

	result := evalWithResolver(t, r, &EvaluationInput{
		FromDomain:        "example.com",
		SPFResult:         "fail",
		SPFAuthDomain:     "attacker.com",
		DKIMResult:        "pass",
		DKIMSigningDomain: "attacker.com",
	})
	if result.Result != ResultFail {
		t.Fatalf("expected fail, got %s", result.Result)
	}
	if result.SPFAligned {
		t.Fatal("expected SPF not aligned")
	}
	if result.DKIMAligned {
		t.Fatal("expected DKIM not aligned")
	}
}
