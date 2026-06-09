package dnsverify

import (
	"fmt"
	"net"
	"testing"
)

type mockResolver struct {
	txts   map[string][]string
	mxs    map[string][]*net.MX
	hosts  map[string][]string
	addrs  map[string][]string
}

func (m *mockResolver) LookupTXT(host string) ([]string, error) {
	if v, ok := m.txts[host]; ok { return v, nil }
	return nil, fmt.Errorf("not found")
}
func (m *mockResolver) LookupMX(host string) ([]*net.MX, error) {
	if v, ok := m.mxs[host]; ok { return v, nil }
	return nil, fmt.Errorf("not found")
}
func (m *mockResolver) LookupHost(host string) ([]string, error) {
	if v, ok := m.hosts[host]; ok { return v, nil }
	return nil, fmt.Errorf("not found")
}
func (m *mockResolver) LookupAddr(addr string) ([]string, error) {
	if v, ok := m.addrs[addr]; ok { return v, nil }
	return nil, fmt.Errorf("not found")
}

func TestSPFPass(t *testing.T) {
	s := NewService("default")
	s.SetResolver(&mockResolver{
		txts: map[string][]string{"example.com": {"v=spf1 include:_spf.example.com ~all"}},
	})
	r := s.checkSPF("example.com")
	if !r.Present || !r.Valid {
		t.Fatal("expected SPF present and valid")
	}
}

func TestSPFFail(t *testing.T) {
	s := NewService("default")
	s.SetResolver(&mockResolver{txts: map[string][]string{"example.com": {}}})
	r := s.checkSPF("example.com")
	if r.Present {
		t.Fatal("expected SPF not present")
	}
}

func TestDKIMPass(t *testing.T) {
	s := NewService("default")
	s.SetResolver(&mockResolver{
		txts: map[string][]string{"default._domainkey.example.com": {"v=DKIM1; p=MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQC4"}},
	})
	r := s.checkDKIM("example.com")
	if !r.Present {
		t.Fatal("expected DKIM present")
	}
}

func TestDKIMFail(t *testing.T) {
	s := NewService("default")
	s.SetResolver(&mockResolver{txts: map[string][]string{}})
	r := s.checkDKIM("example.com")
	if r.Present {
		t.Fatal("expected DKIM not present")
	}
}

func TestDMARCPass(t *testing.T) {
	s := NewService("default")
	s.SetResolver(&mockResolver{
		txts: map[string][]string{"_dmarc.example.com": {"v=DMARC1; p=reject; rua=mailto:dmarc@example.com"}},
	})
	r := s.checkDMARC("example.com")
	if !r.Present || !r.Valid {
		t.Fatal("expected DMARC present and valid")
	}
}

func TestDMARCFail(t *testing.T) {
	s := NewService("default")
	s.SetResolver(&mockResolver{txts: map[string][]string{}})
	r := s.checkDMARC("example.com")
	if r.Present {
		t.Fatal("expected DMARC not present")
	}
}

func TestMXPass(t *testing.T) {
	s := NewService("default")
	s.SetResolver(&mockResolver{
		mxs: map[string][]*net.MX{
			"example.com": {{Host: "mail.example.com", Pref: 10}},
		},
	})
	r := s.checkMX("example.com")
	if !r.Present {
		t.Fatal("expected MX present")
	}
}

func TestMXFail(t *testing.T) {
	s := NewService("default")
	s.SetResolver(&mockResolver{mxs: map[string][]*net.MX{}})
	r := s.checkMX("example.com")
	if r.Present {
		t.Fatal("expected MX not present")
	}
}

func TestAPass(t *testing.T) {
	s := NewService("default")
	s.SetResolver(&mockResolver{
		hosts: map[string][]string{"example.com": {"192.0.2.1"}},
	})
	r := s.checkA("example.com")
	if !r.Present {
		t.Fatal("expected A record present")
	}
}

func TestAFail(t *testing.T) {
	s := NewService("default")
	s.SetResolver(&mockResolver{hosts: map[string][]string{}})
	r := s.checkA("example.com")
	if r.Present {
		t.Fatal("expected A record not present")
	}
}

func TestPTRPass(t *testing.T) {
	s := NewService("default")
	s.SetResolver(&mockResolver{
		addrs: map[string][]string{"192.0.2.1": {"mail.example.com"}},
	})
	r := s.checkPTR("192.0.2.1")
	if !r.Present || !r.Valid {
		t.Fatal("expected PTR present and valid")
	}
}

func TestPTRFail(t *testing.T) {
	s := NewService("default")
	s.SetResolver(&mockResolver{addrs: map[string][]string{}})
	r := s.checkPTR("192.0.2.1")
	if r.Present {
		t.Fatal("expected PTR not present")
	}
}

func TestOverallHealthy(t *testing.T) {
	s := NewService("default")
	r := &DomainDNSReport{
		SPF:   SPFResult{Present: true, Valid: true},
		DKIM:  DKIMResult{Present: true},
		DMARC: DMARCResult{Present: true, Valid: true},
		MX:    MXResult{Present: true},
		A:     AResult{Present: true},
	}
	overall := s.computeOverall(r)
	if overall != StatusHealthy {
		t.Fatalf("expected healthy, got %s", overall)
	}
}

func TestOverallWarning(t *testing.T) {
	s := NewService("default")
	r := &DomainDNSReport{
		SPF:   SPFResult{Present: true, Valid: true},
		DKIM:  DKIMResult{Present: false},
		DMARC: DMARCResult{Present: false},
		MX:    MXResult{Present: true},
		A:     AResult{Present: true},
	}
	overall := s.computeOverall(r)
	if overall != StatusWarning {
		t.Fatalf("expected warning, got %s", overall)
	}
}

func TestOverallFailed(t *testing.T) {
	s := NewService("default")
	r := &DomainDNSReport{
		SPF:   SPFResult{Present: false},
		DKIM:  DKIMResult{Present: false},
		DMARC: DMARCResult{Present: false},
		MX:    MXResult{Present: false},
		A:     AResult{Present: false},
	}
	overall := s.computeOverall(r)
	if overall != StatusFailed {
		t.Fatalf("expected failed, got %s", overall)
	}
}

func TestGenerateReport(t *testing.T) {
	s := NewService("default")
	s.SetResolver(&mockResolver{
		txts: map[string][]string{
			"example.com":                  {"v=spf1 include:_spf.example.com ~all"},
			"default._domainkey.example.com": {"v=DKIM1; p=keydata"},
			"_dmarc.example.com":            {"v=DMARC1; p=reject"},
		},
		mxs: map[string][]*net.MX{
			"example.com": {{Host: "mail.example.com", Pref: 10}},
		},
		hosts: map[string][]string{"example.com": {"192.0.2.1"}},
		addrs: map[string][]string{"192.0.2.1": {"mail.example.com"}},
	})
	report := s.GenerateReport("example.com")
	if report.Overall != StatusHealthy {
		t.Fatalf("expected healthy, got %s", report.Overall)
	}
	if !report.SPF.Present || !report.DKIM.Present || !report.DMARC.Present || !report.MX.Present || !report.A.Present {
		t.Fatal("expected all records present")
	}
}

func TestInvalidDomain(t *testing.T) {
	s := NewService("default")
	s.SetResolver(&mockResolver{})
	r := s.checkSPF("")
	if r.Present {
		t.Fatal("expected no SPF for empty domain")
	}
}
