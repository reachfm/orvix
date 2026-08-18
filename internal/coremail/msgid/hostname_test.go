package msgid

import "testing"

func TestNormalizeHostname_ConfiguredHostname(t *testing.T) {
	got, err := NormalizeHostname("mail.example.test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "mail.example.test" {
		t.Fatalf("got %q, want mail.example.test", got)
	}
}

func TestNormalizeHostname_TrailingDotStripped(t *testing.T) {
	got, err := NormalizeHostname("mail.example.test.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "mail.example.test" {
		t.Fatalf("got %q, want mail.example.test (trailing dot stripped)", got)
	}
}

func TestNormalizeHostname_UppercaseLowercased(t *testing.T) {
	got, err := NormalizeHostname("MAIL.EXAMPLE.TEST")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "mail.example.test" {
		t.Fatalf("got %q, want mail.example.test (lowercased)", got)
	}
}

func TestNormalizeHostname_RejectsEmpty(t *testing.T) {
	if _, err := NormalizeHostname(""); err == nil {
		t.Fatal("expected error for empty hostname")
	}
	if _, err := NormalizeHostname("   "); err == nil {
		t.Fatal("expected error for whitespace-only hostname")
	}
}

func TestNormalizeHostname_RejectsCRLFInjection(t *testing.T) {
	cases := []string{
		"mail.example.test\r\nBcc: attacker@evil.test",
		"mail.example.test\nX-Injected: yes",
		"mail.example.test\rX-Injected: yes",
		"mail\x00.example.test",
	}
	for _, c := range cases {
		if _, err := NormalizeHostname(c); err == nil {
			t.Fatalf("expected error for CRLF/control injection in %q", c)
		}
	}
}

func TestNormalizeHostname_RejectsWhitespace(t *testing.T) {
	if _, err := NormalizeHostname("mail example.test"); err == nil {
		t.Fatal("expected error for embedded whitespace")
	}
}

func TestNormalizeHostname_RejectsLocalhost(t *testing.T) {
	if _, err := NormalizeHostname("localhost"); err == nil {
		t.Fatal("expected error for localhost")
	}
}

func TestNormalizeHostname_RejectsDotLocal(t *testing.T) {
	cases := []string{"orvix.local", "mail.orvix.local", ".local", "local"}
	for _, c := range cases {
		if _, err := NormalizeHostname(c); err == nil {
			t.Fatalf("expected error for private pseudo-domain %q", c)
		}
	}
}

func TestNormalizeHostname_RejectsInvalidSyntax(t *testing.T) {
	cases := []string{
		"-badstart.example.test",
		"badend-.example.test",
		"..example.test",
		"bareword", // no dot: not a plausible Internet domain
		"exa mple.test",
	}
	for _, c := range cases {
		if _, err := NormalizeHostname(c); err == nil {
			t.Fatalf("expected error for invalid hostname %q", c)
		}
	}
}

func TestResolveHostname_PrefersConfigured(t *testing.T) {
	got, err := ResolveHostname("mail.example.test", "fallback.example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "mail.example.test" {
		t.Fatalf("got %q, want configured hostname to win", got)
	}
}

func TestResolveHostname_FallsBackToSenderDomain(t *testing.T) {
	// Empty/invalid configured hostname, valid authenticated sender
	// domain — the documented safe fallback.
	got, err := ResolveHostname("", "sender.example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sender.example.com" {
		t.Fatalf("got %q, want sender.example.com fallback", got)
	}
}

func TestResolveHostname_ConfiguredOrvixLocalFallsBackToSenderDomain(t *testing.T) {
	// A misconfigured coremail.hostname of the old pseudo-domain must
	// never win — the fallback sender domain must be used instead.
	got, err := ResolveHostname("orvix.local", "sender.example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sender.example.com" {
		t.Fatalf("got %q, want sender.example.com fallback (orvix.local must never be used)", got)
	}
}

func TestResolveHostname_FailsSafeWhenNeitherValid(t *testing.T) {
	cases := []struct {
		configured string
		fallback   string
	}{
		{"", ""},
		{"orvix.local", "orvix.local"},
		{"localhost", ""},
		{"\r\ninjected", "also\r\ninjected"},
	}
	for _, c := range cases {
		if _, err := ResolveHostname(c.configured, c.fallback); err == nil {
			t.Fatalf("expected fail-safe error for configured=%q fallback=%q", c.configured, c.fallback)
		}
	}
}
