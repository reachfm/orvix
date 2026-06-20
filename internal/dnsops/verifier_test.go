package dnsops

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// buildVerifiedFixturePlan returns a Plan whose required records
// would all pass verification if the FakeResolver is populated with
// the matching entries. Tests then mutate individual entries to
// exercise specific failure modes.
func buildVerifiedFixturePlan(t *testing.T) *Plan {
	t.Helper()
	g := NewGenerator()
	g.NowFunc = func() time.Time {
		return time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	}
	plan, err := g.Generate(Inputs{
		Domain:        "example.com",
		MailHost:      "mail.example.com",
		ServerIPv4:    "203.0.113.10",
		DKIMSelector:  "orvix",
		DKIMPubKey:    "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEArealpubkey123",
		ReportMailbox: "dmarc@example.com",
		MTAMode:       "testing",
	})
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return plan
}

// populateResolverForFixture installs the entries a verifier needs
// to mark every Required record verified for the fixture plan.
func populateResolverForFixture(f *FakeResolver, plan *Plan) {
	pubKey := "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEArealpubkey123"
	f.Set("example.com", FakeEntry{
		MX:  []net.MX{{Host: "mail.example.com.", Pref: 10}},
		TXT: []string{"v=spf1 mx ip4:203.0.113.10 -all"},
	})
	f.Set("mail.example.com", FakeEntry{
		A: []net.IP{net.ParseIP("203.0.113.10")},
	})
	f.Set("_dmarc.example.com", FakeEntry{
		TXT: []string{"v=DMARC1; p=none; rua=mailto:dmarc@example.com; adkim=s; aspf=s; pct=100"},
	})
	f.Set("_mta-sts.example.com", FakeEntry{
		TXT: []string{"v=STSv1; id=" + plan.MTAPolicyID},
	})
	f.Set("_smtp._tls.example.com", FakeEntry{
		TXT: []string{"v=TLSRPTv1; rua=mailto:tlsrpt@example.com"},
	})
	f.Set("orvix._domainkey.example.com", FakeEntry{
		TXT: []string{"v=DKIM1; k=rsa; p=" + pubKey},
	})
	f.Set("203.0.113.10", FakeEntry{
		PTRFor: map[string][]string{"203.0.113.10": {"mail.example.com."}},
	})
}

// TestVerifierAllRecordsVerified is the happy-path: every Required
// record matches and the report is Verified=true.
func TestVerifierAllRecordsVerified(t *testing.T) {
	plan := buildVerifiedFixturePlan(t)
	f := NewFakeResolver()
	populateResolverForFixture(f, plan)

	v := NewVerifier(f)
	report, err := v.Verify(context.Background(), plan)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !report.Verified {
		t.Errorf("report.Verified must be true with matching fixture; got warnings=%v", report.Warnings)
	}
	for _, r := range report.Plan.Records {
		if r.Required && r.Status != StatusVerified {
			t.Errorf("required record %s/%s must be verified; got %s (%s)",
				r.Name, r.Type, r.Status, r.Reason)
		}
	}
}

// TestVerifierMXPriorityMismatch: priority differs from the plan.
func TestVerifierMXPriorityMismatch(t *testing.T) {
	plan := buildVerifiedFixturePlan(t)
	f := NewFakeResolver()
	f.Set("example.com", FakeEntry{
		MX: []net.MX{{Host: "mail.example.com.", Pref: 20}},
	})
	v := NewVerifier(f)
	report, err := v.Verify(context.Background(), plan)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	for _, r := range report.Plan.Records {
		if r.Purpose == PurposeMX {
			if r.Status != StatusMismatch {
				t.Errorf("MX priority mismatch must be Mismatch; got %s (%s)", r.Status, r.Reason)
			}
			if report.Verified {
				t.Errorf("plan must NOT be verified when MX priority mismatches")
			}
			return
		}
	}
}

// TestVerifierMXMissing: no MX records at all.
func TestVerifierMXMissing(t *testing.T) {
	plan := buildVerifiedFixturePlan(t)
	f := NewFakeResolver()
	// empty resolver; everything returns NXDOMAIN
	v := NewVerifier(f)
	report, err := v.Verify(context.Background(), plan)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	for _, r := range report.Plan.Records {
		if r.Purpose == PurposeMX {
			if r.Status != StatusMissing {
				t.Errorf("MX missing must be Missing; got %s", r.Status)
			}
			return
		}
	}
}

// TestVerifierMailAIPv4Mismatch: live A record points to a different IP.
func TestVerifierMailAIPv4Mismatch(t *testing.T) {
	plan := buildVerifiedFixturePlan(t)
	f := NewFakeResolver()
	f.Set("mail.example.com", FakeEntry{A: []net.IP{net.ParseIP("198.51.100.10")}})
	v := NewVerifier(f)
	report, err := v.Verify(context.Background(), plan)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	for _, r := range report.Plan.Records {
		if r.Purpose == PurposeMailA {
			if r.Status != StatusMismatch {
				t.Errorf("A mismatch must be Mismatch; got %s (%s)", r.Status, r.Reason)
			}
			return
		}
	}
}

// TestVerifierMultipleSPF: two v=spf1 records — must surface as
// StatusMultipleSPF, not silently pass.
func TestVerifierMultipleSPF(t *testing.T) {
	plan := buildVerifiedFixturePlan(t)
	f := NewFakeResolver()
	f.Set("example.com", FakeEntry{
		TXT: []string{
			"v=spf1 mx ip4:203.0.113.10 -all",
			"v=spf1 include:_spf.google.com -all",
		},
	})
	v := NewVerifier(f)
	report, err := v.Verify(context.Background(), plan)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	for _, r := range report.Plan.Records {
		if r.Purpose == PurposeSPF {
			if r.Status != StatusMultipleSPF {
				t.Errorf("multiple SPF must be MultipleSPF; got %s (%s)", r.Status, r.Reason)
			}
			found := false
			for _, w := range report.Warnings {
				if strings.Contains(w, "multiple SPF") {
					found = true
				}
			}
			if !found {
				t.Errorf("warning must mention multiple SPF; got %v", report.Warnings)
			}
			return
		}
	}
}

// TestVerifierSPFChunkedAcrossStrings: two TXT chunks that join to
// one logical SPF — must be detected as one record, not as
// multiple_spf.
func TestVerifierSPFChunkedAcrossStrings(t *testing.T) {
	plan := buildVerifiedFixturePlan(t)
	f := NewFakeResolver()
	f.Set("example.com", FakeEntry{
		TXT: []string{"v=spf1 mx ", "ip4:203.0.113.10 -all"},
	})
	v := NewVerifier(f)
	report, err := v.Verify(context.Background(), plan)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	for _, r := range report.Plan.Records {
		if r.Purpose == PurposeSPF {
			if r.Status != StatusVerified {
				t.Errorf("chunked SPF must join and verify; got %s (%s)", r.Status, r.Reason)
			}
			return
		}
	}
}

// TestVerifierDMARCParse: a malformed DMARC record (no p= tag) must
// surface as StatusError, not StatusVerified.
func TestVerifierDMARCParse(t *testing.T) {
	plan := buildVerifiedFixturePlan(t)
	f := NewFakeResolver()
	f.Set("_dmarc.example.com", FakeEntry{
		TXT: []string{"v=DMARC1; rua=mailto:dmarc@example.com"}, // missing p=
	})
	v := NewVerifier(f)
	report, err := v.Verify(context.Background(), plan)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	for _, r := range report.Plan.Records {
		if r.Purpose == PurposeDMARC {
			if r.Status != StatusError {
				t.Errorf("malformed DMARC must be Error; got %s (%s)", r.Status, r.Reason)
			}
			return
		}
	}
}

// TestVerifierDMARCStricterIsAcceptable: live DMARC with p=reject
// is acceptable even when the plan emits p=none — the dashboard's
// recommendation is staged, the operator may already be ahead.
func TestVerifierDMARCStricterIsAcceptable(t *testing.T) {
	plan := buildVerifiedFixturePlan(t)
	f := NewFakeResolver()
	f.Set("_dmarc.example.com", FakeEntry{
		TXT: []string{"v=DMARC1; p=reject; rua=mailto:dmarc@example.com; pct=100"},
	})
	v := NewVerifier(f)
	report, err := v.Verify(context.Background(), plan)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	for _, r := range report.Plan.Records {
		if r.Purpose == PurposeDMARC {
			if r.Status != StatusVerified {
				t.Errorf("stricter live DMARC must verify; got %s (%s)", r.Status, r.Reason)
			}
			return
		}
	}
}

// TestVerifierDKIMPublicKeyMatch: live DKIM TXT with same public
// key must verify.
func TestVerifierDKIMPublicKeyMatch(t *testing.T) {
	plan := buildVerifiedFixturePlan(t)
	f := NewFakeResolver()
	f.Set("orvix._domainkey.example.com", FakeEntry{
		TXT: []string{"v=DKIM1; k=rsa; p=MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEArealpubkey123"},
	})
	v := NewVerifier(f)
	report, err := v.Verify(context.Background(), plan)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	for _, r := range report.Plan.Records {
		if r.Purpose == PurposeDKIM {
			if r.Status != StatusVerified {
				t.Errorf("matching DKIM must verify; got %s (%s)", r.Status, r.Reason)
			}
			return
		}
	}
}

// TestVerifierDKIMKeyDiffers: live DKIM TXT with a different
// public key must be Mismatch, not silently pass.
func TestVerifierDKIMKeyDiffers(t *testing.T) {
	plan := buildVerifiedFixturePlan(t)
	f := NewFakeResolver()
	f.Set("orvix._domainkey.example.com", FakeEntry{
		TXT: []string{"v=DKIM1; k=rsa; p=DIFFERENTKEY"},
	})
	v := NewVerifier(f)
	report, err := v.Verify(context.Background(), plan)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	for _, r := range report.Plan.Records {
		if r.Purpose == PurposeDKIM {
			if r.Status != StatusMismatch {
				t.Errorf("DKIM key mismatch must be Mismatch; got %s (%s)", r.Status, r.Reason)
			}
			return
		}
	}
}

// TestVerifierMTASTSIDMatch: id matches plan.
func TestVerifierMTASTSIDMatch(t *testing.T) {
	plan := buildVerifiedFixturePlan(t)
	f := NewFakeResolver()
	f.Set("_mta-sts.example.com", FakeEntry{
		TXT: []string{"v=STSv1; id=" + plan.MTAPolicyID},
	})
	v := NewVerifier(f)
	report, err := v.Verify(context.Background(), plan)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	for _, r := range report.Plan.Records {
		if r.Purpose == PurposeMTASTS {
			if r.Status != StatusVerified {
				t.Errorf("matching MTA-STS id must verify; got %s (%s)", r.Status, r.Reason)
			}
			return
		}
	}
}

// TestVerifierMTASTSIDDrift: id has rotated but TXT still v=STSv1.
// This must be StatusMismatch (operator needs to bump plan id or
// align live policy).
func TestVerifierMTASTSIDDrift(t *testing.T) {
	plan := buildVerifiedFixturePlan(t)
	f := NewFakeResolver()
	f.Set("_mta-sts.example.com", FakeEntry{
		TXT: []string{"v=STSv1; id=stale-id-2025"},
	})
	v := NewVerifier(f)
	report, err := v.Verify(context.Background(), plan)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	for _, r := range report.Plan.Records {
		if r.Purpose == PurposeMTASTS {
			if r.Status != StatusMismatch {
				t.Errorf("MTA-STS id drift must be Mismatch; got %s (%s)", r.Status, r.Reason)
			}
			return
		}
	}
}

// TestVerifierTLSRPTPresent: any v=TLSRPTv1 record verifies.
func TestVerifierTLSRPTPresent(t *testing.T) {
	plan := buildVerifiedFixturePlan(t)
	f := NewFakeResolver()
	f.Set("_smtp._tls.example.com", FakeEntry{
		TXT: []string{"v=TLSRPTv1; rua=mailto:tlsrpt@example.com"},
	})
	v := NewVerifier(f)
	report, err := v.Verify(context.Background(), plan)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	for _, r := range report.Plan.Records {
		if r.Purpose == PurposeTLSRPT {
			if r.Status != StatusVerified {
				t.Errorf("TLS-RPT must verify; got %s (%s)", r.Status, r.Reason)
			}
			return
		}
	}
}

// TestVerifierCAAConflictNotOverwritten: existing CAA with a
// different issuer must surface as Conflict, never as Verified or
// Missing.
func TestVerifierCAAConflictNotOverwritten(t *testing.T) {
	plan := buildVerifiedFixturePlan(t)
	f := NewFakeResolver()
	// Cloudflare's CAA presentation form for issue letsencrypt is
	// the operator's existing record. We add a different one to
	// force the conflict path.
	f.Set("example.com", FakeEntry{
		TXT: []string{"0 issuewild \"globalsign.com\""},
	})
	v := NewVerifier(f)
	report, err := v.Verify(context.Background(), plan)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	var sawConflict bool
	for _, r := range report.Plan.Records {
		if r.Purpose != PurposeCAA {
			continue
		}
		if r.Status == StatusConflict {
			sawConflict = true
		}
	}
	if !sawConflict {
		t.Errorf("existing CAA with different issuer must surface Conflict")
	}
	found := false
	for _, w := range report.Warnings {
		if strings.Contains(w, "CAA") {
			found = true
		}
	}
	if !found {
		t.Errorf("warning must mention CAA conflict; got %v", report.Warnings)
	}
}

// TestVerifierPTRMismatch: live reverse DNS points elsewhere.
func TestVerifierPTRMismatch(t *testing.T) {
	plan := buildVerifiedFixturePlan(t)
	f := NewFakeResolver()
	f.Set("203.0.113.10", FakeEntry{
		PTRFor: map[string][]string{"203.0.113.10": {"some-other-host.example.com."}},
	})
	v := NewVerifier(f)
	report, err := v.Verify(context.Background(), plan)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	for _, r := range report.Plan.Records {
		if r.Purpose == PurposePTR {
			if r.Status != StatusMismatch {
				t.Errorf("PTR mismatch must be Mismatch; got %s (%s)", r.Status, r.Reason)
			}
			return
		}
	}
}

// TestVerifierReportTopLevelVerifiedFalse confirms report.Verified
// is false when any required record is missing.
func TestVerifierReportTopLevelVerifiedFalse(t *testing.T) {
	plan := buildVerifiedFixturePlan(t)
	f := NewFakeResolver()
	// empty resolver
	v := NewVerifier(f)
	report, err := v.Verify(context.Background(), plan)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.Verified {
		t.Errorf("report.Verified must be false with empty resolver")
	}
	if report.CheckedAt == "" {
		t.Errorf("report.CheckedAt must be populated")
	}
}

// TestVerifierDMARCMissing: no DMARC record at all.
func TestVerifierDMARCMissing(t *testing.T) {
	plan := buildVerifiedFixturePlan(t)
	f := NewFakeResolver()
	v := NewVerifier(f)
	report, err := v.Verify(context.Background(), plan)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	for _, r := range report.Plan.Records {
		if r.Purpose == PurposeDMARC {
			if r.Status != StatusMissing {
				t.Errorf("missing DMARC must be Missing; got %s", r.Status)
			}
			return
		}
	}
}

// TestVerifierDKIMMissing: no DKIM TXT at all.
func TestVerifierDKIMMissing(t *testing.T) {
	plan := buildVerifiedFixturePlan(t)
	f := NewFakeResolver()
	v := NewVerifier(f)
	report, err := v.Verify(context.Background(), plan)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	for _, r := range report.Plan.Records {
		if r.Purpose == PurposeDKIM {
			if r.Status != StatusMissing {
				t.Errorf("missing DKIM must be Missing; got %s", r.Status)
			}
			return
		}
	}
}
