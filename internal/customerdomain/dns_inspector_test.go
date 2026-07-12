package customerdomain

import (
	"context"
	"net"
	"testing"

	"github.com/orvix/orvix/internal/dnsops"
)

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
	// PublicKey field should never contain private key data from the inspector
	if result.DKIM.PublicKey != "" {
		t.Errorf("PublicKey = %q, want empty (no private key exposure)", result.DKIM.PublicKey)
	}
}
