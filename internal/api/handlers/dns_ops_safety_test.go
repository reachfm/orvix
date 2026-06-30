package handlers

// Safety-fix tests for DNS-DKIM-OPERATIONS-2F-SAFETY-FIX.
//
// These tests pin the three Codex blockers the previous build
// failed on:
//
//   1. Public DNS A/SPF generation must NOT use the SMTP
//      listener bind host (which defaults to 0.0.0.0). The
//      public IP must come from cfg.DNS.PublicIPv4 /
//      PublicIPv6 and must be a valid global unicast address.
//   2. DKIM keygen must require a provisioned domain in
//      coremail_domains. Orphan DKIM rows for unprovisioned
//      domains are not allowed.
//   3. DKIM selector must be strict DNS-label safe: lowercase,
//      [a-z0-9-], 1..63 chars, no leading/trailing hyphen, no
//      dots / spaces / slashes / underscores / unicode /
//      wildcards / consecutive hyphens.
//
// The tests live in `package handlers` (not `handlers_test`)
// so they can call the unexported helpers directly without a
// full fiber harness for the pure-function ones. The end-to-
// end coverage is in handlers_test (TestDNSOpsDKIMRequiresDomain
// etc.).

import (
	"net"
	"strings"
	"testing"
)

// â”€â”€ isPublicUnicastIP â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestIsPublicUnicastIP_Empty(t *testing.T) {
	_, err := isPublicUnicastIP("")
	if err == nil {
		t.Fatalf("empty IP must error")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("error must mention not configured; got %q", err.Error())
	}
}

func TestIsPublicUnicastIP_Invalid(t *testing.T) {
	_, err := isPublicUnicastIP("not-an-ip")
	if err == nil {
		t.Fatalf("invalid IP must error")
	}
}

func TestIsPublicUnicastIP_Unspecified(t *testing.T) {
	for _, ip := range []string{"0.0.0.0", "::", " 0.0.0.0 "} {
		_, err := isPublicUnicastIP(ip)
		if err == nil {
			t.Errorf("unspecified IP %q must be rejected", ip)
		}
	}
}

func TestIsPublicUnicastIP_Loopback(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "127.255.255.254", "::1"} {
		_, err := isPublicUnicastIP(ip)
		if err == nil {
			t.Errorf("loopback IP %q must be rejected", ip)
		}
	}
}

func TestIsPublicUnicastIP_LinkLocal(t *testing.T) {
	for _, ip := range []string{"169.254.0.1", "fe80::1"} {
		_, err := isPublicUnicastIP(ip)
		if err == nil {
			t.Errorf("link-local IP %q must be rejected", ip)
		}
	}
}

func TestIsPublicUnicastIP_Private(t *testing.T) {
	for _, ip := range []string{
		"10.0.0.1",
		"10.255.255.254",
		"172.16.0.1",
		"172.31.255.254",
		"192.168.0.1",
		"192.168.255.254",
		"fc00::1",
		"fd00::1",
	} {
		_, err := isPublicUnicastIP(ip)
		if err == nil {
			t.Errorf("private IP %q must be rejected", ip)
		}
	}
}

func TestIsPublicUnicastIP_Multicast(t *testing.T) {
	for _, ip := range []string{"224.0.0.1", "ff02::1"} {
		_, err := isPublicUnicastIP(ip)
		if err == nil {
			t.Errorf("multicast IP %q must be rejected", ip)
		}
	}
}

func TestIsPublicUnicastIP_RejectsDocumentationRanges(t *testing.T) {
	for _, ip := range []string{
		"192.0.2.1",     // TEST-NET-1 (RFC 5737)
		"198.51.100.1",  // TEST-NET-2 (RFC 5737)
		"203.0.113.10",  // TEST-NET-3 (RFC 5737)
		"2001:db8::1",   // Documentation IPv6 (RFC 3849)
	} {
		_, err := isPublicUnicastIP(ip)
		if err == nil {
			t.Errorf("documentation IP %q must be rejected", ip)
		}
	}
}

func TestIsPublicUnicastIP_RejectsCarrierGradeNAT(t *testing.T) {
	for _, ip := range []string{"100.64.0.1", "100.127.255.255", "100.64.255.255"} {
		_, err := isPublicUnicastIP(ip)
		if err == nil {
			t.Errorf("carrier-grade NAT %q must be rejected", ip)
		}
		if !strings.Contains(err.Error(), "carrier-grade NAT") && !strings.Contains(err.Error(), "6598") {
			t.Errorf("error for %q must mention carrier-grade NAT; got %q", ip, err.Error())
		}
	}
}

func TestIsPublicUnicastIP_RejectsBenchmarkingRange(t *testing.T) {
	for _, ip := range []string{"198.18.0.1", "198.19.255.255"} {
		_, err := isPublicUnicastIP(ip)
		if err == nil {
			t.Errorf("benchmarking range %q must be rejected", ip)
		}
		if !strings.Contains(err.Error(), "benchmark") && !strings.Contains(err.Error(), "2544") {
			t.Errorf("error for %q must mention benchmark; got %q", ip, err.Error())
		}
	}
}

func TestIsPublicUnicastIP_RejectsSpecialUse192_0_0_0(t *testing.T) {
	for _, ip := range []string{"192.0.0.1", "192.0.0.255"} {
		_, err := isPublicUnicastIP(ip)
		if err == nil {
			t.Errorf("special-use 192.0.0.0/24 %q must be rejected", ip)
		}
		if !strings.Contains(err.Error(), "special-use") {
			t.Errorf("error for %q must mention special-use; got %q", ip, err.Error())
		}
	}
}

func TestIsPublicUnicastIP_AcceptsEdgeOutsideNewRanges(t *testing.T) {
	for _, ip := range []string{"100.128.0.1", "198.17.255.255", "198.20.0.1", "192.0.3.0"} {
		got, err := isPublicUnicastIP(ip)
		if err != nil {
			t.Errorf("%q must be accepted; got %v", ip, err)
			continue
		}
		if !got.Equal(net.ParseIP(ip)) {
			t.Errorf("round-trip mismatch for %q: %v", ip, got)
		}
	}
}

func TestIsPublicUnicastIP_AcceptsGenuinelyPublic(t *testing.T) {
	got, err := isPublicUnicastIP("8.8.8.8")
	if err != nil {
		t.Fatalf("8.8.8.8 must be accepted; got %v", err)
	}
	if !got.Equal(net.ParseIP("8.8.8.8")) {
		t.Errorf("round-trip mismatch: %v vs %v", got, net.ParseIP("8.8.8.8"))
	}
	got6, err6 := isPublicUnicastIP("2607:f8b0::1")
	if err6 != nil {
		t.Fatalf("2607:f8b0::1 must be accepted; got %v", err6)
	}
	if !got6.Equal(net.ParseIP("2607:f8b0::1")) {
		t.Errorf("round-trip mismatch: %v vs %v", got6, net.ParseIP("2607:f8b0::1"))
	}
}

// â”€â”€ validateDKIMSelector â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestValidateDKIMSelector_EmptyDefaultsToOrvix(t *testing.T) {
	got, err := validateDKIMSelector("")
	if err != nil {
		t.Fatalf("empty selector must default, not error; got %v", err)
	}
	if got != "orvix" {
		t.Errorf("empty default must be orvix; got %q", got)
	}
}

func TestValidateDKIMSelector_WhitespaceDefaultsToOrvix(t *testing.T) {
	got, err := validateDKIMSelector("   ")
	if err != nil {
		t.Fatalf("whitespace selector must default; got %v", err)
	}
	if got != "orvix" {
		t.Errorf("whitespace default must be orvix; got %q", got)
	}
}

func TestValidateDKIMSelector_AcceptsSafe(t *testing.T) {
	cases := []string{
		"orvix",
		"default",
		"s1",
		"a",
		"a-b-c",
		"abc123",
		"0",
		"a1b2c3",
		"x-y-z-1-2-3",
	}
	for _, in := range cases {
		got, err := validateDKIMSelector(in)
		if err != nil {
			t.Errorf("selector %q must be accepted; got %v", in, err)
		}
		// Lowercase invariant.
		if got != strings.ToLower(in) {
			t.Errorf("selector %q must be normalised to lowercase; got %q", in, got)
		}
	}
}

func TestValidateDKIMSelector_Lowercases(t *testing.T) {
	got, err := validateDKIMSelector("ORVIX")
	if err != nil {
		t.Fatalf("uppercase selector must be lowercased; got %v", err)
	}
	if got != "orvix" {
		t.Errorf("uppercase must lowercase; got %q", got)
	}
}

func TestValidateDKIMSelector_RejectsDot(t *testing.T) {
	if _, err := validateDKIMSelector("foo.bar"); err == nil {
		t.Errorf("selector with dot must be rejected")
	}
}

func TestValidateDKIMSelector_RejectsSpace(t *testing.T) {
	if _, err := validateDKIMSelector("foo bar"); err == nil {
		t.Errorf("selector with space must be rejected")
	}
}

func TestValidateDKIMSelector_RejectsSlash(t *testing.T) {
	if _, err := validateDKIMSelector("foo/bar"); err == nil {
		t.Errorf("selector with slash must be rejected")
	}
}

func TestValidateDKIMSelector_RejectsUnderscore(t *testing.T) {
	if _, err := validateDKIMSelector("foo_bar"); err == nil {
		t.Errorf("selector with underscore must be rejected")
	}
}

func TestValidateDKIMSelector_RejectsQuote(t *testing.T) {
	for _, in := range []string{`foo"bar`, "foo'bar"} {
		if _, err := validateDKIMSelector(in); err == nil {
			t.Errorf("selector %q with quote must be rejected", in)
		}
	}
}

func TestValidateDKIMSelector_RejectsUnicode(t *testing.T) {
	// Greek alpha, Chinese char, em-dash, etc.
	for _, in := range []string{"Î±", "é‚®", "Ã©m", "xâ€”y"} {
		if _, err := validateDKIMSelector(in); err == nil {
			t.Errorf("selector %q with unicode must be rejected", in)
		}
	}
}

func TestValidateDKIMSelector_RejectsWildcard(t *testing.T) {
	if _, err := validateDKIMSelector("foo*"); err == nil {
		t.Errorf("selector with wildcard must be rejected")
	}
}

func TestValidateDKIMSelector_RejectsLeadingHyphen(t *testing.T) {
	if _, err := validateDKIMSelector("-foo"); err == nil {
		t.Errorf("selector with leading hyphen must be rejected")
	}
}

func TestValidateDKIMSelector_RejectsTrailingHyphen(t *testing.T) {
	if _, err := validateDKIMSelector("foo-"); err == nil {
		t.Errorf("selector with trailing hyphen must be rejected")
	}
}

func TestValidateDKIMSelector_RejectsConsecutiveHyphens(t *testing.T) {
	if _, err := validateDKIMSelector("foo--bar"); err == nil {
		t.Errorf("selector with consecutive hyphens must be rejected")
	}
}

func TestValidateDKIMSelector_RejectsTooLong(t *testing.T) {
	long := strings.Repeat("a", 64)
	if _, err := validateDKIMSelector(long); err == nil {
		t.Errorf("selector > 63 chars must be rejected")
	}
	// Boundary: 63 chars is allowed.
	at := strings.Repeat("a", 63)
	if _, err := validateDKIMSelector(at); err != nil {
		t.Errorf("selector of exactly 63 chars must be accepted; got %v", err)
	}
}

func TestValidateDKIMSelector_RejectsEmptyResult(t *testing.T) {
	// Pure whitespace and the bare "-" both collapse to an
	// unsafe value after TrimSpace.
	if _, err := validateDKIMSelector("-"); err == nil {
		t.Errorf("selector \"-\" must be rejected (leading and trailing hyphen)")
	}
}
