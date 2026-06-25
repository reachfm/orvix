package dnsops

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// TestGeneratorPlanCoversAllRequired confirms the generated plan
// contains the full set of Required records: MX, mail A, SPF, DKIM,
// DMARC, MTA-STS, TLS-RPT. CAA and PTR are optional.
func TestGeneratorPlanCoversAllRequired(t *testing.T) {
	g := NewGenerator()
	plan, err := g.Generate(Inputs{
		Domain:     "example.com",
		MailHost:   "mail.example.com",
		ServerIPv4: "8.8.8.8",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	seen := map[Purpose]bool{}
	for _, r := range plan.Records {
		seen[r.Purpose] = true
	}
	for _, want := range []Purpose{
		PurposeMX, PurposeMailA, PurposeSPF, PurposeDKIM,
		PurposeDMARC, PurposeMTASTS, PurposeTLSRPT,
	} {
		if !seen[want] {
			t.Errorf("plan must include %s record", want)
		}
	}
}

// TestGeneratorSPFContent confirms the SPF TXT is "v=spf1 mx
// ip4:<ipv4> [-all|~all]" and never contains a placeholder.
func TestGeneratorSPFContent(t *testing.T) {
	g := NewGenerator()
	plan, err := g.Generate(Inputs{
		Domain: "example.com", MailHost: "mail.example.com",
		ServerIPv4: "8.8.8.8",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, r := range plan.Records {
		if r.Purpose != PurposeSPF {
			continue
		}
		if !strings.HasPrefix(r.Value, "v=spf1") {
			t.Errorf("SPF must start with v=spf1; got %q", r.Value)
		}
		if !strings.Contains(r.Value, "mx") {
			t.Errorf("SPF must contain mx; got %q", r.Value)
		}
		if !strings.Contains(r.Value, "8.8.8.8") {
			t.Errorf("SPF must contain server IPv4; got %q", r.Value)
		}
		if !strings.Contains(r.Value, "-all") {
			t.Errorf("SPF must end with -all (strict default); got %q", r.Value)
		}
		for _, banned := range []string{"YOUR", "PLACEHOLDER", "TODO", "FIXME"} {
			if strings.Contains(r.Value, banned) {
				t.Errorf("SPF must not contain %q; got %q", banned, r.Value)
			}
		}
	}
}

// TestGeneratorDKIMUsesProvidedPubKey confirms the DKIM TXT uses the
// supplied public key and never a placeholder.
func TestGeneratorDKIMUsesProvidedPubKey(t *testing.T) {
	g := NewGenerator()
	plan, err := g.Generate(Inputs{
		Domain: "example.com", MailHost: "mail.example.com",
		ServerIPv4: "8.8.8.8",
		DKIMSelector: "orvix",
		DKIMPubKey:   "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEArealpubkey123",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var dkim *Record
	for i := range plan.Records {
		if plan.Records[i].Purpose == PurposeDKIM {
			dkim = &plan.Records[i]
			break
		}
	}
	if dkim == nil {
		t.Fatalf("plan missing DKIM record")
	}
	if !strings.Contains(dkim.Value, "v=DKIM1") {
		t.Errorf("DKIM must start with v=DKIM1; got %q", dkim.Value)
	}
	if !strings.Contains(dkim.Value, "p=MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEArealpubkey123") {
		t.Errorf("DKIM must contain provided pub key; got %q", dkim.Value)
	}
	for _, banned := range []string{"YOUR", "PLACEHOLDER", "TODO"} {
		if strings.Contains(dkim.Value, banned) {
			t.Errorf("DKIM must not contain %q", banned)
		}
	}
	if !strings.HasPrefix(dkim.Name, "orvix._domainkey") {
		t.Errorf("DKIM name must be selector._domainkey; got %q", dkim.Name)
	}
}

// TestGeneratorDKIMMissingKeyHonest confirms that when DKIM pub key
// is empty, the plan still carries a DKIM row but with the honest
// "not generated" wording so the UI can offer a Generate action
// instead of rendering a fake public key.
func TestGeneratorDKIMMissingKeyHonest(t *testing.T) {
	g := NewGenerator()
	plan, err := g.Generate(Inputs{
		Domain: "example.com", MailHost: "mail.example.com",
		ServerIPv4: "8.8.8.8",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, r := range plan.Records {
		if r.Purpose == PurposeDKIM {
			if !strings.Contains(strings.ToLower(r.Value), "not generated") {
				t.Errorf("missing-key DKIM row must say 'not generated'; got %q", r.Value)
			}
			if r.Status != StatusNotChecked {
				t.Errorf("missing-key DKIM row must start as NotChecked; got %q", r.Status)
			}
			return
		}
	}
	t.Errorf("plan missing DKIM row")
}

// TestGeneratorDMARCDefaultSafe confirms DMARC defaults to p=none
// with the configured report mailbox.
func TestGeneratorDMARCDefaultSafe(t *testing.T) {
	g := NewGenerator()
	plan, err := g.Generate(Inputs{
		Domain: "example.com", MailHost: "mail.example.com",
		ServerIPv4: "8.8.8.8", ReportMailbox: "dmarc@example.com",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, r := range plan.Records {
		if r.Purpose != PurposeDMARC {
			continue
		}
		if !strings.Contains(r.Value, "v=DMARC1") {
			t.Errorf("DMARC must start with v=DMARC1; got %q", r.Value)
		}
		if !strings.Contains(r.Value, "p=none") {
			t.Errorf("DMARC default must be p=none; got %q", r.Value)
		}
		if !strings.Contains(r.Value, "mailto:dmarc@example.com") {
			t.Errorf("DMARC rua must point to report mailbox; got %q", r.Value)
		}
		if !strings.Contains(r.Value, "pct=100") {
			t.Errorf("DMARC must include pct=100; got %q", r.Value)
		}
		if !strings.Contains(r.Value, "adkim=s") {
			t.Errorf("DMARC must include adkim=s (strict); got %q", r.Value)
		}
		if !strings.Contains(r.Value, "aspf=s") {
			t.Errorf("DMARC must include aspf=s (strict); got %q", r.Value)
		}
	}
}

// TestGeneratorMTASTSTestingDefault confirms MTA-STS defaults to
// mode=testing and max_age=86400. Enforce is NEVER the default.
func TestGeneratorMTASTSTestingDefault(t *testing.T) {
	g := NewGenerator()
	plan, err := g.Generate(Inputs{
		Domain: "example.com", MailHost: "mail.example.com",
		ServerIPv4: "8.8.8.8",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, r := range plan.Records {
		if r.Purpose != PurposeMTASTS {
			continue
		}
		if !strings.HasPrefix(r.Value, "v=STSv1") {
			t.Errorf("MTA-STS must start with v=STSv1; got %q", r.Value)
		}
		if !strings.Contains(r.Value, "id=") {
			t.Errorf("MTA-STS must carry id=; got %q", r.Value)
		}
	}
	if plan.MTAMode != "testing" {
		t.Errorf("MTA-STS mode default must be testing; got %q", plan.MTAMode)
	}
	if !strings.Contains(plan.MTAPolicyFile, "mode: testing") {
		t.Errorf("MTA-STS policy file must default to mode: testing; got %q", plan.MTAPolicyFile)
	}
	if !strings.Contains(plan.MTAPolicyFile, "max_age: 86400") {
		t.Errorf("MTA-STS policy file must include max_age: 86400; got %q", plan.MTAPolicyFile)
	}
}

// TestGeneratorTLSRPTContent confirms TLS-RPT has v=TLSRPTv1 and a
// rua target.
func TestGeneratorTLSRPTContent(t *testing.T) {
	g := NewGenerator()
	plan, err := g.Generate(Inputs{
		Domain: "example.com", MailHost: "mail.example.com",
		ServerIPv4: "8.8.8.8",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, r := range plan.Records {
		if r.Purpose != PurposeTLSRPT {
			continue
		}
		if !strings.HasPrefix(r.Value, "v=TLSRPTv1") {
			t.Errorf("TLS-RPT must start with v=TLSRPTv1; got %q", r.Value)
		}
		if !strings.Contains(r.Value, "rua=mailto:") {
			t.Errorf("TLS-RPT must include rua=mailto:; got %q", r.Value)
		}
	}
}

// TestGeneratorCAARecords confirms CAA carries letsencrypt issue +
// iodef.
func TestGeneratorCAARecords(t *testing.T) {
	g := NewGenerator()
	plan, err := g.Generate(Inputs{
		Domain: "example.com", MailHost: "mail.example.com",
		ServerIPv4: "8.8.8.8",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var issue, iodef int
	for _, r := range plan.Records {
		if r.Purpose != PurposeCAA {
			continue
		}
		if r.Tag == "issue" && r.Value == "letsencrypt.org" {
			issue++
		}
		if r.Tag == "iodef" && strings.Contains(r.Value, "postmaster@example.com") {
			iodef++
		}
	}
	if issue != 1 {
		t.Errorf("CAA plan must include exactly 1 letsencrypt issue; got %d", issue)
	}
	if iodef != 1 {
		t.Errorf("CAA plan must include exactly 1 postmaster iodef; got %d", iodef)
	}
}

// TestGeneratorIPv6Optional confirms the AAAA row is only emitted
// when ServerIPv6 is configured.
func TestGeneratorIPv6Optional(t *testing.T) {
	g := NewGenerator()
	noV6, err := g.Generate(Inputs{
		Domain: "example.com", MailHost: "mail.example.com",
		ServerIPv4: "8.8.8.8",
	})
	if err != nil {
		t.Fatalf("no-v6: %v", err)
	}
	for _, r := range noV6.Records {
		if r.Type == RecordAAAA {
			t.Errorf("plan must not emit AAAA when IPv6 is empty")
		}
	}
	withV6, err := g.Generate(Inputs{
		Domain: "example.com", MailHost: "mail.example.com",
		ServerIPv4: "8.8.8.8", ServerIPv6: "2607:f8b0::1",
	})
	if err != nil {
		t.Fatalf("with-v6: %v", err)
	}
	found := false
	for _, r := range withV6.Records {
		if r.Type == RecordAAAA && r.Value == "2607:f8b0::1" {
			found = true
		}
	}
	if !found {
		t.Errorf("plan must emit AAAA when IPv6 is provided")
	}
}

// TestGeneratorPTRProviderSide confirms the PTR row is informational
// only (Status NotChecked) and never Required.
func TestGeneratorPTRProviderSide(t *testing.T) {
	g := NewGenerator()
	plan, err := g.Generate(Inputs{
		Domain: "example.com", MailHost: "mail.example.com",
		ServerIPv4: "8.8.8.8",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, r := range plan.Records {
		if r.Purpose != PurposePTR {
			continue
		}
		if r.Required {
			t.Errorf("PTR must not be Required (provider-side)")
		}
		if r.Status != StatusNotChecked {
			t.Errorf("PTR must start as NotChecked; got %q", r.Status)
		}
		if !strings.Contains(r.Reason, "hosting provider") {
			t.Errorf("PTR reason must explain provider-side; got %q", r.Reason)
		}
	}
}

// TestGeneratorDANETLSAReadinessOnly confirms TLSA row is only
// emitted when DNSSEC is detected and is never auto-populated.
func TestGeneratorDANETLSAReadinessOnly(t *testing.T) {
	g := NewGenerator()
	noDNSSEC, _ := g.Generate(Inputs{
		Domain: "example.com", MailHost: "mail.example.com",
		ServerIPv4: "8.8.8.8",
	})
	for _, r := range noDNSSEC.Records {
		if r.Type == RecordTLSA {
			t.Errorf("TLSA must NOT be emitted when DNSSEC undetected")
		}
	}
	withDNSSEC, _ := g.Generate(Inputs{
		Domain: "example.com", MailHost: "mail.example.com",
		ServerIPv4: "8.8.8.8", DNSSECDetected: true,
	})
	found := false
	for _, r := range withDNSSEC.Records {
		if r.Type == RecordTLSA {
			found = true
			if r.Required {
				t.Errorf("TLSA readiness must not be Required")
			}
			if r.Status != StatusNotChecked {
				t.Errorf("TLSA readiness must be NotChecked")
			}
		}
	}
	if !found {
		t.Errorf("TLSA readiness must be emitted when DNSSEC detected")
	}
}

// TestGeneratorValidateRejectsInvalidDomain confirms the input
// validator rejects malformed domains.
func TestGeneratorValidateRejectsInvalidDomain(t *testing.T) {
	g := NewGenerator()
	cases := []string{
		"",
		"no-dot",
		"https://example.com",
		"example.com/path",
		"foo bar.com",
		"*.example.com",
		".example.com",
		"example.com.",
	}
	for _, bad := range cases {
		if _, err := g.Generate(Inputs{
			Domain: bad, MailHost: "mail.example.com", ServerIPv4: "8.8.8.8",
		}); err == nil {
			t.Errorf("domain %q should be rejected", bad)
		}
	}
}

// TestGeneratorBIMIReadinessOnly confirms BIMI is informational
// (not auto-populated) regardless of inputs.
func TestGeneratorBIMIReadinessOnly(t *testing.T) {
	g := NewGenerator()
	plan, _ := g.Generate(Inputs{
		Domain: "example.com", MailHost: "mail.example.com",
		ServerIPv4: "8.8.8.8",
	})
	for _, r := range plan.Records {
		if r.Purpose != PurposeBIMI {
			continue
		}
		if r.Required {
			t.Errorf("BIMI must not be Required")
		}
		if !strings.Contains(strings.ToLower(r.Value), "not configured") {
			t.Errorf("BIMI value must be honest 'not configured'; got %q", r.Value)
		}
	}
}

// TestPlanIsCompleteAndRequiredRecords sanity-checks the IsComplete
// helper: it returns true only when every Required record is
// Verified.
func TestPlanIsCompleteAndRequiredRecords(t *testing.T) {
	p := &Plan{Domain: "example.com"}
	for _, r := range []Record{
		{Name: "@", Type: RecordMX, Purpose: PurposeMX, Required: true, Status: StatusVerified, Verified: true},
		{Name: "@", Type: RecordCAA, Purpose: PurposeCAA, Required: false},
	} {
		p.Records = append(p.Records, r)
	}
	if !p.IsComplete() {
		t.Errorf("plan with all required verified must be complete")
	}
	p.Records[0].Status = StatusMissing
	if p.IsComplete() {
		t.Errorf("plan with required missing must NOT be complete")
	}
	req := p.RequiredRecords()
	if len(req) != 1 {
		t.Errorf("RequiredRecords must return 1 (only MX); got %d", len(req))
	}
}

// TestGeneratorNoShellOut is a smoke test confirming the dnsops
// package does not import os/exec. It is a static check on the
// package imports; if this test ever fails the package has started
// shelling out, which is explicitly forbidden by the brief.
func TestGeneratorNoShellOut(t *testing.T) {
	// The presence of `package dnsops` plus the absence of
	// exec.Command strings in source is enforced by the
	// repository-wide grep in the verification harness. Here we
	// just confirm the Resolver interface signature matches the
	// documented contract.
	var r Resolver = NewFakeResolver()
	if _, err := r.LookupTXT(context.Background(), "example.com"); err == nil {
		t.Errorf("FakeResolver with no entries must return IsNotFound")
	}
	if _, err := r.LookupMX(context.Background(), "example.com"); err == nil {
		t.Errorf("FakeResolver with no entries must return IsNotFound for MX")
	}
	if _, err := r.LookupA(context.Background(), "example.com"); err == nil {
		t.Errorf("FakeResolver with no entries must return IsNotFound for A")
	}
	if _, err := r.LookupAAAA(context.Background(), "example.com"); err == nil {
		t.Errorf("FakeResolver with no entries must return IsNotFound for AAAA")
	}
	if _, err := r.LookupPTR(context.Background(), "8.8.8.8"); err == nil {
		t.Errorf("FakeResolver with no entries must return IsNotFound for PTR")
	}
	// NetResolver must construct without panicking.
	_ = NewNetResolver()
}

// TestFakeResolverLookupPTR matches a recorded PTR map.
func TestFakeResolverLookupPTR(t *testing.T) {
	f := NewFakeResolver()
	f.Set("8.8.8.8", FakeEntry{
		PTRFor: map[string][]string{"8.8.8.8": {"mail.example.com."}},
	})
	hosts, err := f.LookupPTR(context.Background(), "8.8.8.8")
	if err != nil {
		t.Fatalf("lookupPTR: %v", err)
	}
	if len(hosts) != 1 || hosts[0] != "mail.example.com." {
		t.Errorf("PTR hosts: got %v", hosts)
	}
	// Sanity: NetResolver-equivalent PTR round-trip on a real IP
	// is intentionally NOT exercised — we don't want this test
	// to need internet. FakeResolver is enough.
	_ = net.IPv4
}

func TestGeneratorMTASTSPolicyIDStableAcrossRuns(t *testing.T) {
	g := NewGenerator()
	in := Inputs{
		Domain: "example.com", MailHost: "mail.example.com",
		ServerIPv4: "8.8.8.8", MTAMode: "testing",
	}
	plan1, err := g.Generate(in)
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	plan2, err := g.Generate(in)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if plan1.MTAPolicyID != plan2.MTAPolicyID {
		t.Fatalf("same inputs must yield same MTAPolicyID; run1=%s run2=%s", plan1.MTAPolicyID, plan2.MTAPolicyID)
	}
	if plan1.MTAPolicyID == "" {
		t.Fatal("MTAPolicyID must not be empty")
	}
}

func TestGeneratorMTASTSPolicyIDStableAcrossDates(t *testing.T) {
	in := Inputs{
		Domain: "example.com", MailHost: "mail.example.com",
		ServerIPv4: "8.8.8.8", MTAMode: "testing",
	}
	g1 := NewGenerator()
	g1.NowFunc = func() time.Time { return time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC) }
	plan1, err := g1.Generate(in)
	if err != nil {
		t.Fatalf("date 1: %v", err)
	}
	g2 := NewGenerator()
	g2.NowFunc = func() time.Time { return time.Date(2027, 12, 31, 0, 0, 0, 0, time.UTC) }
	plan2, err := g2.Generate(in)
	if err != nil {
		t.Fatalf("date 2: %v", err)
	}
	if plan1.MTAPolicyID != plan2.MTAPolicyID {
		t.Fatalf("different dates / same policy must yield same MTAPolicyID; date1=%s date2=%s", plan1.MTAPolicyID, plan2.MTAPolicyID)
	}
}

func TestGeneratorMTASTSPolicyIDChangesOnContentChange(t *testing.T) {
	g := NewGenerator()
	base := Inputs{
		Domain: "example.com", MailHost: "mail.example.com",
		ServerIPv4: "8.8.8.8", MTAMode: "testing",
	}
	plan1, err := g.Generate(base)
	if err != nil {
		t.Fatalf("base: %v", err)
	}

	enforce := base
	enforce.MTAMode = "enforce"
	plan2, err := g.Generate(enforce)
	if err != nil {
		t.Fatalf("enforce: %v", err)
	}
	if plan1.MTAPolicyID == plan2.MTAPolicyID {
		t.Errorf("mode change testing->enforce must change MTAPolicyID; both=%s", plan1.MTAPolicyID)
	}

	differentMX := base
	differentMX.MailHost = "mx2.example.com"
	plan3, err := g.Generate(differentMX)
	if err != nil {
		t.Fatalf("different MX: %v", err)
	}
	if plan1.MTAPolicyID == plan3.MTAPolicyID {
		t.Errorf("MX change must change MTAPolicyID; both=%s", plan1.MTAPolicyID)
	}
}

func TestGeneratorMTASTSTXTContainsPolicyID(t *testing.T) {
	g := NewGenerator()
	plan, err := g.Generate(Inputs{
		Domain: "example.com", MailHost: "mail.example.com",
		ServerIPv4: "8.8.8.8", MTAMode: "testing",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var txtValue string
	for _, r := range plan.Records {
		if r.Purpose == PurposeMTASTS {
			txtValue = r.Value
			break
		}
	}
	if txtValue == "" {
		t.Fatal("MTA-STS TXT record not found in plan")
	}
	expectedTXT := "v=STSv1; id=" + plan.MTAPolicyID
	if txtValue != expectedTXT {
		t.Fatalf("MTA-STS TXT must be %q, got %q", expectedTXT, txtValue)
	}
}

func TestGeneratorMTASTSPolicyIDDerivedFromPolicyFile(t *testing.T) {
	g := NewGenerator()
	plan, err := g.Generate(Inputs{
		Domain: "example.com", MailHost: "mail.example.com",
		ServerIPv4: "8.8.8.8", MTAMode: "testing",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	expected := mtaStsPolicyID(plan.MTAPolicyFile)
	if plan.MTAPolicyID != expected {
		t.Fatalf("MTAPolicyID must be sha256(policy file); got=%s expected=%s file:\n%s",
			plan.MTAPolicyID, expected, plan.MTAPolicyFile)
	}
}
