package dns

import (
	"strings"
	"testing"
)

func TestGenerateDKIMKey(t *testing.T) {
	svc := NewService()
	priv, pub, err := svc.GenerateDKIMKey(2048)
	if err != nil {
		t.Fatalf("GenerateDKIMKey failed: %v", err)
	}
	if priv == "" {
		t.Error("private key is empty")
	}
	if !strings.Contains(priv, "RSA PRIVATE KEY") {
		t.Error("private key missing PEM header")
	}
	if pub == "" {
		t.Error("public key is empty")
	}
	if !strings.HasPrefix(pub, "v=DKIM1") {
		t.Error("public key should start with v=DKIM1")
	}
}

func TestGenerateSPFRecord(t *testing.T) {
	svc := NewService()
	record := svc.GenerateSPFRecord("example.com", []string{"192.168.1.1", "10.0.0.1"})

	if !strings.HasPrefix(record, "v=spf1") {
		t.Error("SPF record should start with v=spf1")
	}
	if !strings.Contains(record, "ip4:192.168.1.1") {
		t.Error("SPF record should include first IP")
	}
	if !strings.Contains(record, "ip4:10.0.0.1") {
		t.Error("SPF record should include second IP")
	}
	if !strings.Contains(record, "~all") {
		t.Error("SPF record should end with ~all")
	}
}

func TestGenerateSPFRecordNoIPs(t *testing.T) {
	svc := NewService()
	record := svc.GenerateSPFRecord("example.com", nil)

	if !strings.Contains(record, "include:_spf.orvix.email") {
		t.Error("SPF record should include orvix include")
	}
}

func TestGenerateDMARCRecord(t *testing.T) {
	svc := NewService()
	record := svc.GenerateDMARCRecord("reject", "admin@orvix.email")

	if !strings.HasPrefix(record, "v=DMARC1") {
		t.Error("DMARC record should start with v=DMARC1")
	}
	if !strings.Contains(record, "p=reject") {
		t.Error("DMARC record should contain p=reject")
	}
	if !strings.Contains(record, "rua=mailto:admin@orvix.email") {
		t.Error("DMARC record should contain rua")
	}
}

func TestGenerateDMARCRecordDefaultPolicy(t *testing.T) {
	svc := NewService()
	record := svc.GenerateDMARCRecord("", "")

	if !strings.Contains(record, "p=none") {
		t.Error("DMARC record should default to p=none")
	}
}

func TestDKIMSelector(t *testing.T) {
	svc := NewService()
	selector := svc.DKIMSelector("example.com")

	if !strings.HasPrefix(selector, "orvix") {
		t.Errorf("selector should start with orvix, got %s", selector)
	}
	if len(selector) < 8 {
		t.Errorf("selector too short: %s", selector)
	}

	selector2 := svc.DKIMSelector("example.org")
	if selector == selector2 {
		t.Error("different domains should have different selectors")
	}
}

func TestCheckDNS(t *testing.T) {
	svc := NewService()
	results, err := svc.CheckDNS("google.com")
	if err != nil {
		t.Fatalf("CheckDNS failed: %v", err)
	}

	if results["mx"] != true {
		t.Log("Note: google.com MX check failed (may be network-dependent)")
	}
}
