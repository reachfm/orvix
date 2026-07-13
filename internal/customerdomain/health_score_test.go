package customerdomain

import (
	"testing"
)

func TestHealthScoreAllPass(t *testing.T) {
	r := &DNSResult{
		MX:    &MXCheck{Status: "pass"},
		SPF:   &SPFCheck{Status: "pass"},
		DKIM:  &DKIMCheck{Status: "pass"},
		DMARC: &DMARCCheck{Status: "pass"},
	}
	hr := HealthScore(r)
	if hr.Score != 100 {
		t.Errorf("all pass = %d, want 100", hr.Score)
	}
}

func TestHealthScoreAllFail(t *testing.T) {
	r := &DNSResult{
		MX:    &MXCheck{Status: "fail"},
		SPF:   &SPFCheck{Status: "fail"},
		DKIM:  &DKIMCheck{Status: "fail"},
		DMARC: &DMARCCheck{Status: "fail"},
	}
	hr := HealthScore(r)
	if hr.Score != 0 {
		t.Errorf("all fail = %d, want 0", hr.Score)
	}
}

func TestHealthScoreMixed(t *testing.T) {
	r := &DNSResult{
		MX:    &MXCheck{Status: "pass"},
		SPF:   &SPFCheck{Status: "warning"},
		DKIM:  &DKIMCheck{Status: "fail"},
		DMARC: &DMARCCheck{Status: "unknown"},
	}
	hr := HealthScore(r)
	expected := 30 + 10 + 0 + 0
	if hr.Score != expected {
		t.Errorf("mixed = %d, want %d (mx:30 + spf:10 + dkim:0 + dmarc:0)", hr.Score, expected)
	}
}

func TestHealthScoreNilResult(t *testing.T) {
	hr := HealthScore(nil)
	if hr.Score != 0 {
		t.Errorf("nil result = %d, want 0", hr.Score)
	}
}

func TestHealthScoreBreakdown(t *testing.T) {
	r := &DNSResult{
		MX:    &MXCheck{Status: "pass"},
		SPF:   &SPFCheck{Status: "pass"},
		DKIM:  &DKIMCheck{Status: "warning"},
		DMARC: &DMARCCheck{Status: "fail"},
	}
	hr := HealthScore(r)
	if c, ok := hr.Breakdown["mx"]; !ok || c.Earned != 30 || c.Weight != 30 {
		t.Errorf("mx breakdown: %+v", c)
	}
	if c, ok := hr.Breakdown["dkim"]; !ok || c.Earned != 15 || c.Weight != 30 {
		t.Errorf("dkim breakdown: %+v", c)
	}
	if c, ok := hr.Breakdown["dmarc"]; !ok || c.Earned != 0 || c.Weight != 20 {
		t.Errorf("dmarc breakdown: %+v", c)
	}
}

func TestHealthScoreNoDNS(t *testing.T) {
	r := &DNSResult{}
	hr := HealthScore(r)
	if hr.Score != 0 {
		t.Errorf("empty result = %d, want 0", hr.Score)
	}
	for _, name := range []string{"mx", "spf", "dkim", "dmarc"} {
		if c, ok := hr.Breakdown[name]; !ok || c.Status != "unknown" {
			t.Errorf("%s status = %s, want unknown", name, c.Status)
		}
	}
}

func TestHealthScoreDeterministic(t *testing.T) {
	r := &DNSResult{
		MX:    &MXCheck{Status: "pass"},
		SPF:   &SPFCheck{Status: "warning"},
		DKIM:  &DKIMCheck{Status: "pass"},
		DMARC: &DMARCCheck{Status: "pass"},
	}
	first := HealthScore(r)
	for i := 0; i < 10; i++ {
		again := HealthScore(r)
		if again.Score != first.Score {
			t.Errorf("run %d: score %d != first %d", i, again.Score, first.Score)
		}
	}
}

func TestOverallStatus(t *testing.T) {
	tests := []struct {
		name   string
		r      *DNSResult
		expect string
	}{
		{"nil", nil, "unchecked"},
		{"all pass", &DNSResult{MX: &MXCheck{Status: "pass"}, SPF: &SPFCheck{Status: "pass"}, DKIM: &DKIMCheck{Status: "pass"}, DMARC: &DMARCCheck{Status: "pass"}}, "pass"},
		{"one warning", &DNSResult{MX: &MXCheck{Status: "pass"}, SPF: &SPFCheck{Status: "warning"}, DKIM: &DKIMCheck{Status: "pass"}, DMARC: &DMARCCheck{Status: "pass"}}, "warning"},
		{"one fail", &DNSResult{MX: &MXCheck{Status: "pass"}, SPF: &SPFCheck{Status: "fail"}, DKIM: &DKIMCheck{Status: "pass"}, DMARC: &DMARCCheck{Status: "pass"}}, "fail"},
		{"fail beats warning", &DNSResult{MX: &MXCheck{Status: "fail"}, SPF: &SPFCheck{Status: "warning"}, DKIM: &DKIMCheck{Status: "pass"}, DMARC: &DMARCCheck{Status: "pass"}}, "fail"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := overallStatus(tc.r)
			if got != tc.expect {
				t.Errorf("overallStatus = %q, want %q", got, tc.expect)
			}
		})
	}
}
