package customerdomain

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/dnsops"
)

// realORVIXSPF is the actual, deployed ORVIX SPF policy. The regression this
// file guards is that a previous change replaced this specific value with the
// generic "v=spf1 mx -all".
const realORVIXSPF = "v=spf1 ip4:65.75.203.74 include:spf.orvix.email -all"

func newTestInspector(t *testing.T, r dnsops.Resolver, e CanonicalExpectations) *DNSInspector {
	t.Helper()
	return NewDNSInspector(r).
		WithExpectations(e).
		WithClock(func() time.Time { return time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC) })
}

// baseFake wires the minimum records so InspectEnterprise runs end to end.
func baseFake(domain string) *dnsops.FakeResolver {
	f := dnsops.NewFakeResolver()
	f.Set(domain, dnsops.FakeEntry{
		MX:  []net.MX{{Host: "mail." + domain + ".", Pref: 10}},
		TXT: []string{realORVIXSPF},
	})
	f.Set("mail."+domain, dnsops.FakeEntry{A: []net.IP{net.ParseIP("65.75.203.74")}})
	f.Set("_dmarc."+domain, dnsops.FakeEntry{TXT: []string{"v=DMARC1; p=quarantine; rua=mailto:dmarc-reports@orvix.email"}})
	return f
}

// ── SPF round-trip ────────────────────────────────────────────────────────

// TestConfiguredRealORVIXSPFRoundTripsThroughVerifyAndUI proves the REAL
// ORVIX policy survives verification and reaches the UI/download payload
// unchanged, rather than being replaced by the old generic hard-code.
func TestConfiguredRealORVIXSPFRoundTripsThroughVerifyAndUI(t *testing.T) {
	const domain = "example.com"
	insp := newTestInspector(t, baseFake(domain), CanonicalExpectations{SPFRecord: realORVIXSPF})

	h := insp.InspectEnterprise(context.Background(), domain, []string{"mail." + domain}, "", "")

	if h.SPF.Expected != realORVIXSPF {
		t.Fatalf("expected SPF shown to UI = %q, want the configured real policy %q", h.SPF.Expected, realORVIXSPF)
	}
	if h.SPF.Status != string(DNSStatusPass) {
		t.Fatalf("SPF status = %q (reason %q), want pass: the live record equals the configured policy", h.SPF.Status, h.SPF.Reason)
	}
	// (c) guidance and (d) the downloadable record file both render the
	// same expected string, so a substring check on guidance proves they
	// cannot diverge from what the verifier used.
	if !strings.Contains(h.SPF.Guidance, realORVIXSPF) {
		t.Fatalf("repair guidance %q does not contain the canonical SPF value %q", h.SPF.Guidance, realORVIXSPF)
	}
	if strings.Contains(h.SPF.Expected, "v=spf1 mx -all") {
		t.Fatalf("expected SPF still contains the removed generic hard-code: %q", h.SPF.Expected)
	}
}

// TestDifferentlyConfiguredSPFAlsoRoundTrips proves the value is genuinely
// read from configuration and not merely a second hard-code.
func TestDifferentlyConfiguredSPFAlsoRoundTrips(t *testing.T) {
	const domain = "example.com"
	const other = "v=spf1 ip4:203.0.113.9 include:spf.other.example ~all"

	f := baseFake(domain)
	f.Set(domain, dnsops.FakeEntry{
		MX:  []net.MX{{Host: "mail." + domain + ".", Pref: 10}},
		TXT: []string{other},
	})
	insp := newTestInspector(t, f, CanonicalExpectations{SPFRecord: other})

	h := insp.InspectEnterprise(context.Background(), domain, []string{"mail." + domain}, "", "")
	if h.SPF.Expected != other {
		t.Fatalf("expected SPF = %q, want the differently-configured value %q", h.SPF.Expected, other)
	}
	if h.SPF.Status != string(DNSStatusPass) {
		t.Fatalf("SPF status = %q (reason %q), want pass", h.SPF.Status, h.SPF.Reason)
	}
	if !strings.Contains(h.SPF.Guidance, other) {
		t.Fatalf("guidance %q must quote the configured value %q", h.SPF.Guidance, other)
	}
}

// TestConfiguredSPFMismatchIsFlagged proves the verifier actually compares
// the live record against the configured policy instead of passing anything
// that starts with v=spf1.
func TestConfiguredSPFMismatchIsFlagged(t *testing.T) {
	const domain = "example.com"
	f := baseFake(domain)
	f.Set(domain, dnsops.FakeEntry{
		MX:  []net.MX{{Host: "mail." + domain + ".", Pref: 10}},
		TXT: []string{"v=spf1 mx -all"},
	})
	insp := newTestInspector(t, f, CanonicalExpectations{SPFRecord: realORVIXSPF})

	h := insp.InspectEnterprise(context.Background(), domain, []string{"mail." + domain}, "", "")
	if h.SPF.Status != string(DNSStatusWarning) {
		t.Fatalf("SPF status = %q, want warning when the published record differs from the configured policy", h.SPF.Status)
	}
	if h.SPF.Expected != realORVIXSPF {
		t.Fatalf("expected SPF = %q, want %q", h.SPF.Expected, realORVIXSPF)
	}
}

// TestUnconfiguredSPFFallsBackCompatibly documents the backward-compatible
// default for deployments predating expected_spf, and requires the guidance
// to say the value is a generic stand-in.
func TestUnconfiguredSPFFallsBackCompatibly(t *testing.T) {
	const domain = "example.com"
	insp := newTestInspector(t, baseFake(domain), CanonicalExpectations{})

	h := insp.InspectEnterprise(context.Background(), domain, []string{"mail." + domain}, "", "")
	if h.SPF.Expected != "v=spf1 mx -all" {
		t.Fatalf("unconfigured fallback = %q, want %q", h.SPF.Expected, "v=spf1 mx -all")
	}
	if !strings.Contains(h.SPF.Guidance, "expected_spf") {
		t.Fatalf("fallback guidance must tell the operator to configure expected_spf, got %q", h.SPF.Guidance)
	}
}

// ── DMARC rua ─────────────────────────────────────────────────────────────

// TestDMARCRUAUsesConfiguredAddressWhenSet proves a real configured mailbox
// is used verbatim and no dmarc@<domain> is fabricated.
func TestDMARCRUAUsesConfiguredAddressWhenSet(t *testing.T) {
	const domain = "example.com"
	insp := newTestInspector(t, baseFake(domain), CanonicalExpectations{
		DMARCPolicy: "reject",
		DMARCRUA:    "mailto:dmarc-reports@orvix.email",
	})

	h := insp.InspectEnterprise(context.Background(), domain, []string{"mail." + domain}, "", "")
	want := "v=DMARC1; p=reject; rua=mailto:dmarc-reports@orvix.email"
	if h.DMARC.Expected != want {
		t.Fatalf("expected DMARC = %q, want %q", h.DMARC.Expected, want)
	}
	if strings.Contains(h.DMARC.Expected, "dmarc@"+domain) {
		t.Fatalf("DMARC rua must never be fabricated as dmarc@<domain> when configured: %q", h.DMARC.Expected)
	}
	if strings.Contains(h.DMARC.Guidance, "placeholder") {
		t.Fatalf("guidance must not call a configured address a placeholder: %q", h.DMARC.Guidance)
	}
}

// TestDMARCRUAPlaceholderIsLabelledAsSuch documents the only circumstance in
// which dmarc@<domain> appears: no configured address, and the guidance says
// plainly that it is a placeholder requiring real configuration.
func TestDMARCRUAPlaceholderIsLabelledAsSuch(t *testing.T) {
	const domain = "example.com"
	insp := newTestInspector(t, baseFake(domain), CanonicalExpectations{})

	h := insp.InspectEnterprise(context.Background(), domain, []string{"mail." + domain}, "", "")
	want := "v=DMARC1; p=quarantine; rua=mailto:dmarc@example.com"
	if h.DMARC.Expected != want {
		t.Fatalf("unconfigured DMARC default = %q, want documented placeholder %q", h.DMARC.Expected, want)
	}
	if !strings.Contains(h.DMARC.Guidance, "placeholder") ||
		!strings.Contains(h.DMARC.Guidance, "expected_dmarc_rua") {
		t.Fatalf("placeholder guidance must name it a placeholder and point at expected_dmarc_rua, got %q", h.DMARC.Guidance)
	}
}

// ── Autodiscover SRV ──────────────────────────────────────────────────────

func srvExpectations() CanonicalExpectations {
	return CanonicalExpectations{
		SPFRecord: realORVIXSPF,
		SRVTarget: "mail.example.com",
		SRVPort:   443,
	}
}

func inspectSRV(t *testing.T, mutate func(f *dnsops.FakeResolver)) *DNSRecordCheck {
	t.Helper()
	const domain = "example.com"
	f := baseFake(domain)
	mutate(f)
	insp := newTestInspector(t, f, srvExpectations())
	h := insp.InspectEnterprise(context.Background(), domain, []string{"mail." + domain}, "", "")
	if h.AutodiscoverSRV == nil {
		t.Fatal("AutodiscoverSRV row missing from health payload")
	}
	return h.AutodiscoverSRV
}

// TestAutodiscoverSRVCorrectMatch — scenario 1 of 6.
func TestAutodiscoverSRVCorrectMatch(t *testing.T) {
	c := inspectSRV(t, func(f *dnsops.FakeResolver) {
		f.Set("_autodiscover._tcp.example.com", dnsops.FakeEntry{
			SRV: []net.SRV{{Target: "mail.example.com.", Port: 443, Priority: 0, Weight: 0}},
		})
	})
	if c.Status != string(DNSStatusPass) {
		t.Fatalf("status = %q (reason %q), want pass", c.Status, c.Reason)
	}
	if c.Expected != "0 0 443 mail.example.com." {
		t.Fatalf("expected = %q", c.Expected)
	}
	if !c.Optional {
		t.Fatal("the SRV row must be optional so it never reduces the health score")
	}
}

// TestAutodiscoverSRVMissingRecord — scenario 2 of 6.
func TestAutodiscoverSRVMissingRecord(t *testing.T) {
	c := inspectSRV(t, func(f *dnsops.FakeResolver) {})
	if c.Status != string(DNSStatusOptional) {
		t.Fatalf("status = %q, want optional for an absent (but non-required) record", c.Status)
	}
	if !strings.Contains(c.Reason, "no autodiscover SRV record published") {
		t.Fatalf("reason = %q", c.Reason)
	}
	if c.Expected == "" {
		t.Fatal("a missing record must still show the operator what to publish")
	}
}

// TestAutodiscoverSRVWrongTarget — scenario 3 of 6.
func TestAutodiscoverSRVWrongTarget(t *testing.T) {
	c := inspectSRV(t, func(f *dnsops.FakeResolver) {
		f.Set("_autodiscover._tcp.example.com", dnsops.FakeEntry{
			SRV: []net.SRV{{Target: "legacy.elsewhere.example.", Port: 443}},
		})
	})
	if c.Status != string(DNSStatusWarning) {
		t.Fatalf("status = %q, want warning", c.Status)
	}
	if !strings.Contains(c.Reason, "target legacy.elsewhere.example") {
		t.Fatalf("reason must name the wrong target, got %q", c.Reason)
	}
}

// TestAutodiscoverSRVWrongPort — scenario 4 of 6.
func TestAutodiscoverSRVWrongPort(t *testing.T) {
	c := inspectSRV(t, func(f *dnsops.FakeResolver) {
		f.Set("_autodiscover._tcp.example.com", dnsops.FakeEntry{
			SRV: []net.SRV{{Target: "mail.example.com.", Port: 80}},
		})
	})
	if c.Status != string(DNSStatusWarning) {
		t.Fatalf("status = %q, want warning", c.Status)
	}
	if !strings.Contains(c.Reason, "port 80 (want 443)") {
		t.Fatalf("reason must name the wrong port, got %q", c.Reason)
	}
}

// TestAutodiscoverSRVMultipleAnswers — scenario 5 of 6. RFC 2782 permits
// several answers; a client uses ONE. We pass when at least one answer
// matches, but warn about strays because they divert a share of clients.
func TestAutodiscoverSRVMultipleAnswers(t *testing.T) {
	all := inspectSRV(t, func(f *dnsops.FakeResolver) {
		f.Set("_autodiscover._tcp.example.com", dnsops.FakeEntry{
			SRV: []net.SRV{
				{Target: "mail.example.com.", Port: 443},
				{Target: "mail.example.com.", Port: 443},
			},
		})
	})
	if all.Status != string(DNSStatusPass) {
		t.Fatalf("all-matching answers: status = %q (reason %q), want pass", all.Status, all.Reason)
	}
	if len(all.Observed) != 2 {
		t.Fatalf("every answer must be reported, got %v", all.Observed)
	}

	mixed := inspectSRV(t, func(f *dnsops.FakeResolver) {
		f.Set("_autodiscover._tcp.example.com", dnsops.FakeEntry{
			SRV: []net.SRV{
				{Target: "mail.example.com.", Port: 443},
				{Target: "stray.example.net.", Port: 443},
			},
		})
	})
	if mixed.Status != string(DNSStatusWarning) {
		t.Fatalf("mixed answers: status = %q, want warning", mixed.Status)
	}
	if !strings.Contains(mixed.Reason, "additional answers do not") {
		t.Fatalf("mixed answers reason = %q", mixed.Reason)
	}
	if len(mixed.Observed) != 2 {
		t.Fatalf("mixed answers must all be observed, got %v", mixed.Observed)
	}
}

// TestAutodiscoverSRVResolverFailure — scenario 6 of 6. A resolver failure
// must NOT be reported as "not published": we do not know.
func TestAutodiscoverSRVResolverFailure(t *testing.T) {
	c := inspectSRV(t, func(f *dnsops.FakeResolver) {
		f.Set("_autodiscover._tcp.example.com", dnsops.FakeEntry{
			SRVErr: errors.New("read udp 127.0.0.1:53: i/o timeout"),
		})
	})
	if c.Status != string(DNSStatusUnknown) {
		t.Fatalf("status = %q, want unknown on resolver failure", c.Status)
	}
	if !strings.Contains(c.Reason, "timed out") {
		t.Fatalf("a timeout must be reported as such, got %q", c.Reason)
	}
	if strings.Contains(c.Reason, "not published") {
		t.Fatalf("a resolver failure must never be reported as an absent record: %q", c.Reason)
	}
}

// TestAutodiscoverSRVUsesConfiguredTargetAndPort proves the expectation comes
// from configuration rather than being hard-coded.
func TestAutodiscoverSRVUsesConfiguredTargetAndPort(t *testing.T) {
	const domain = "example.com"
	f := baseFake(domain)
	f.Set("_autodiscover._tcp."+domain, dnsops.FakeEntry{
		SRV: []net.SRV{{Target: "ad.orvix.email.", Port: 8443, Priority: 10, Weight: 5}},
	})
	insp := newTestInspector(t, f, CanonicalExpectations{
		SRVTarget: "ad.orvix.email", SRVPort: 8443, SRVPriority: 10, SRVWeight: 5,
	})
	h := insp.InspectEnterprise(context.Background(), domain, []string{"mail." + domain}, "", "")
	if h.AutodiscoverSRV.Expected != "10 5 8443 ad.orvix.email." {
		t.Fatalf("expected = %q, want the configured 10 5 8443 ad.orvix.email.", h.AutodiscoverSRV.Expected)
	}
	if h.AutodiscoverSRV.Status != string(DNSStatusPass) {
		t.Fatalf("status = %q (reason %q), want pass", h.AutodiscoverSRV.Status, h.AutodiscoverSRV.Reason)
	}
}

// ── Dynamic row composition (no fixed row count) ──────────────────────────

// TestRowInventoryIsDynamicInMXHostCount proves the record inventory and the
// score breakdown grow with the number of published MX hosts instead of
// assuming a fixed 14 rows.
func TestRowInventoryIsDynamicInMXHostCount(t *testing.T) {
	count := func(hosts int) (mxRows int, breakdown int) {
		const domain = "example.com"
		f := dnsops.NewFakeResolver()
		mx := make([]net.MX, 0, hosts)
		expected := make([]string, 0, hosts)
		for n := 1; n <= hosts; n++ {
			host := "mx" + string(rune('0'+n)) + "." + domain
			mx = append(mx, net.MX{Host: host + ".", Pref: uint16(10 * n)})
			expected = append(expected, host)
			f.Set(host, dnsops.FakeEntry{A: []net.IP{net.ParseIP("65.75.203.74")}})
		}
		f.Set(domain, dnsops.FakeEntry{MX: mx, TXT: []string{realORVIXSPF}})
		f.Set("_dmarc."+domain, dnsops.FakeEntry{TXT: []string{"v=DMARC1; p=quarantine"}})

		insp := newTestInspector(t, f, CanonicalExpectations{SPFRecord: realORVIXSPF})
		h := insp.InspectEnterprise(context.Background(), domain, expected, "", "")
		score := computeEnterpriseHealthScore(h)
		return len(h.MXHosts), len(score.Breakdown)
	}

	oneRows, oneBreakdown := count(1)
	threeRows, threeBreakdown := count(3)

	if oneRows != 1 || threeRows != 3 {
		t.Fatalf("MX host rows must track the number of published MX hosts: got %d for 1 host and %d for 3", oneRows, threeRows)
	}
	if threeBreakdown != oneBreakdown+2 {
		t.Fatalf("score breakdown must grow with MX host count: %d entries for 1 host vs %d for 3", oneBreakdown, threeBreakdown)
	}
}

// TestScoreIgnoresOptionalAndNotApplicableRows proves completeness is
// computed from which checks actually apply, not from a fixed denominator.
func TestScoreIgnoresOptionalAndNotApplicableRows(t *testing.T) {
	const domain = "example.com"
	f := baseFake(domain)
	insp := newTestInspector(t, f, CanonicalExpectations{SPFRecord: realORVIXSPF})
	h := insp.InspectEnterprise(context.Background(), domain, []string{"mail." + domain}, "", "")

	score := computeEnterpriseHealthScore(h)
	for name, comp := range score.Breakdown {
		if !scoredDNSStatus(comp.Status) && comp.Weight != 0 {
			t.Fatalf("row %q has non-scoring status %q but weight %d; optional/NA rows must carry zero weight", name, comp.Status, comp.Weight)
		}
	}
	if _, ok := score.Breakdown["autodiscover_srv"]; !ok {
		t.Fatal("the autodiscover SRV row must appear in the score breakdown even though it is unweighted")
	}
}
