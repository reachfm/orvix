package push

// SSRF validation unit tests (WEBMAIL-ENTERPRISE-PUSH-NOTIFICATIONS).
//
// These tests are pure (no network) — they exercise the deterministic
// part of ValidatePushEndpoint (scheme, userinfo, label blocklist,
// IP-fragment detection, port allowlist, strict known-service
// allowlist, missing dot) without performing a DNS lookup. The DNS
// path is exercised separately by the integration tests in
// internal/api/handlers/webmail_push_integration_test.go where we can
// skip on resolution failure.

import (
	"strings"
	"testing"
)

func TestValidatePushEndpoint_RejectsHTTP(t *testing.T) {
	for _, ep := range []string{
		"http://fcm.googleapis.com/fcm/send/x",
		"ftp://fcm.googleapis.com/fcm/send/x",
		"ws://fcm.googleapis.com/fcm/send/x",
	} {
		err := ValidatePushEndpoint(ep)
		if err == nil {
			t.Errorf("expected error for %q", ep)
			continue
		}
		if !strings.Contains(err.Error(), "HTTPS") {
			t.Errorf("%q: expected HTTPS error, got %v", ep, err)
		}
	}
}

func TestValidatePushEndpoint_RejectsUserinfo(t *testing.T) {
	for _, ep := range []string{
		"https://user:pass@fcm.googleapis.com/push",
		"https://user@fcm.googleapis.com/push",
	} {
		err := ValidatePushEndpoint(ep)
		if err == nil {
			t.Errorf("expected error for %q", ep)
			continue
		}
		if !strings.Contains(err.Error(), "userinfo") {
			t.Errorf("%q: expected userinfo error, got %v", ep, err)
		}
	}
}

func TestValidatePushEndpoint_RejectsLocalhost(t *testing.T) {
	for _, ep := range []string{
		"https://localhost/push",
		"https://localhost:8080/push",
		"https://localhost.fcm.googleapis.com/push",
		"https://LOCALHOST.fcm.googleapis.com/push",
	} {
		err := ValidatePushEndpoint(ep)
		if err == nil {
			t.Errorf("expected error for %q", ep)
			continue
		}
		if !strings.Contains(err.Error(), "localhost") && !strings.Contains(err.Error(), "forbidden label") {
			t.Errorf("%q: expected localhost/forbidden-label error, got %v", ep, err)
		}
	}
}

func TestValidatePushEndpoint_RejectsInternalHostnameLabels(t *testing.T) {
	// These are the per-label blocklist cases that the previous
	// (suffix-based) allowlist missed.
	cases := []string{
		"https://internal.fcm.googleapis.com/push",
		"https://corp.fcm.googleapis.com/push",
		"https://lan.fcm.googleapis.com/push",
		"https://home.fcm.googleapis.com/push",
		"https://intranet.fcm.googleapis.com/push",
		"https://private.fcm.googleapis.com/push",
		"https://test.fcm.googleapis.com/push",
		"https://example.fcm.googleapis.com/push",
		"https://invalid.fcm.googleapis.com/push",
		"https://push.internal/push",
		"https://push.local/push",
		"https://push.corp/push",
		"https://loopback.fcm.googleapis.com/push",
	}
	for _, ep := range cases {
		err := ValidatePushEndpoint(ep)
		if err == nil {
			t.Errorf("BUG: accepted %q (should reject internal hostname)", ep)
			continue
		}
		if !strings.Contains(err.Error(), "forbidden label") &&
			!strings.Contains(err.Error(), "internal") {
			t.Errorf("%q: expected forbidden-label / internal-hostname error, got %v", ep, err)
		}
	}
}

func TestValidatePushEndpoint_RejectsIPFragmentHostnames(t *testing.T) {
	// The killer bypass: previous version used HasSuffix on the
	// push-service domain, so `127.0.0.1.fcm.googleapis.com/push`
	// slipped past the localhost / private-IP / internal-hostname
	// checks. The new IP-fragment detector closes this gap.
	cases := []string{
		"https://127.0.0.1.fcm.googleapis.com/push",
		"https://127.0.0.1.push.apple.com/push",
		"https://10.0.0.1.push.services.mozilla.com/push",
		"https://10.0.0.1.web.push.apple.com/push",
		"https://192.168.1.1.fcm.googleapis.com/push",
		"https://169.254.169.254.web.push.apple.com/push",
		"https://169.254.169.254.fcm.googleapis.com/push",
		"https://172.16.0.1.push.apple.com/push",
		// Three-octet prefix — accepted by the IP detector? Only
		// when the result parses as a valid IP. "127.0.0" does
		// not parse as a valid IP, so this falls through to the
		// "not a known push service" rejection, which is also
		// correct security-wise (the URL is unreachable from a
		// real push service).
		"https://127.0.0.fcm.googleapis.com/push",
		// Bare dotted-quads are also rejected (they look like IP
		// fragments with no trailing labels).
		"https://127.0.0.1/push",
		"https://10.0.0.1/push",
		"https://169.254.169.254/push",
		"https://192.168.1.1/push",
	}
	for _, ep := range cases {
		err := ValidatePushEndpoint(ep)
		if err == nil {
			t.Errorf("BUG: accepted %q (should reject IP-fragment hostname)", ep)
			continue
		}
		// Acceptable rejection reasons:
		//   - "IP-fragment label"      ← our IP-fragment detector
		//   - "private IP"             ← resolved to a private IP
		//   - "is not a known push service" ← falls through to the
		//     strict allowlist (catches 3-octet and shorter prefixes
		//     because they never parse as valid IPs)
		if !strings.Contains(err.Error(), "IP-fragment") &&
			!strings.Contains(err.Error(), "private IP") &&
			!strings.Contains(err.Error(), "is not a known push service") {
			t.Errorf("%q: expected IP-fragment / private-IP / not-known error, got %v", ep, err)
		}
	}
}

func TestValidatePushEndpoint_RejectsBareIP(t *testing.T) {
	for _, ep := range []string{
		"https://127.0.0.1/push",
		"https://10.0.0.1/push",
		"https://192.168.1.1/push",
		"https://169.254.169.254/push",
		"https://[::1]:8080/push",
	} {
		err := ValidatePushEndpoint(ep)
		if err == nil {
			t.Errorf("BUG: accepted %q (bare IP should be rejected)", ep)
		}
	}
}

func TestValidatePushEndpoint_RejectsHostnamesWithDotsButNoPublicSuffix(t *testing.T) {
	// Hostnames that don't match any known push service are
	// rejected outright — there is no "trust the suffix" fallback.
	for _, ep := range []string{
		"https://fcm-googleapis.com/push",     // typosquat
		"https://fcm.googleapis.com.attacker.com/push", // suffix confusion
		"https://attacker.com/push",
		"https://webmail.malicious.example/push",
		"https://something.push.apple.com.attacker.io/push",
	} {
		err := ValidatePushEndpoint(ep)
		if err == nil {
			t.Errorf("BUG: accepted %q (non-allowlisted hostname)", ep)
		}
	}
}

func TestValidatePushEndpoint_RejectsBadPorts(t *testing.T) {
	for _, ep := range []string{
		"https://fcm.googleapis.com:25/push",
		"https://fcm.googleapis.com:587/push",
		"https://fcm.googleapis.com:3306/push",
		"https://fcm.googleapis.com:8080/push",
		"https://fcm.googleapis.com:9999/push",
	} {
		err := ValidatePushEndpoint(ep)
		if err == nil {
			t.Errorf("BUG: accepted %q (unsafe port)", ep)
			continue
		}
		if !strings.Contains(err.Error(), "port") {
			t.Errorf("%q: expected port error, got %v", ep, err)
		}
	}
}

func TestValidatePushEndpoint_AllowsKnownPushServicePorts(t *testing.T) {
	// 443 (default) and 8443 (Mozilla autopush) are allowed. We
	// only test the port-rule layer here — DNS resolution happens
	// after.
	for _, ep := range []string{
		"https://fcm.googleapis.com:443/something",
		"https://updates.push.services.mozilla.com:8443/wpush/v1/abc",
	} {
		// We expect the validation to fail later in the DNS layer
		// (no network in unit tests) but NOT on the port check.
		err := ValidatePushEndpoint(ep)
		if err != nil && strings.Contains(err.Error(), "port") {
			t.Errorf("%q: unexpected port rejection: %v", ep, err)
		}
	}
}

func TestValidatePushEndpoint_RejectsMissingHostname(t *testing.T) {
	for _, ep := range []string{
		"https:///path",
		"https://",
	} {
		err := ValidatePushEndpoint(ep)
		if err == nil {
			t.Errorf("BUG: accepted %q (missing hostname)", ep)
		}
	}
}

func TestValidatePushEndpoint_RejectsEmptyLabels(t *testing.T) {
	for _, ep := range []string{
		"https://.fcm.googleapis.com/push",
		"https://fcm..googleapis.com/push",
		"https://fcm.googleapis.com./push",
	} {
		err := ValidatePushEndpoint(ep)
		if err == nil {
			t.Errorf("BUG: accepted %q (empty label)", ep)
		}
	}
}

func TestKnownPushServices_AreExactMatch(t *testing.T) {
	// Direct test of the strict allowlist semantics: known push
	// service entries must be exact-match — subdomains like
	// "attacker.fcm.googleapis.com" must NOT be accepted even if
	// the suffix matches.
	for _, host := range []string{
		"fcm.googleapis.com",
		"fcm-notifications.googleapis.com",
		"updates.push.services.mozilla.com",
		"push.services.mozilla.com",
		"push.apple.com",
		"web.push.apple.com",
	} {
		if _, ok := knownPushServices[host]; !ok {
			t.Errorf("knownPushServices missing %q", host)
		}
	}
	for _, host := range []string{
		"attacker.fcm.googleapis.com",
		"subdomain.updates.push.services.mozilla.com",
		"subdomain.web.push.apple.com",
		"Fcm.googleapis.com", // exact case — allowed
	} {
		if _, ok := knownPushServices[host]; ok {
			t.Errorf("knownPushServices should not contain non-apex host %q", host)
		}
	}
}

func TestBlockedHostnameLabels_ContainsRequiredEntries(t *testing.T) {
	// Regression pin: every label the reviewer explicitly called
	// out must remain in the blocklist.
	required := []string{
		"localhost", "internal", "corp", "home", "lan",
		"intranet", "private", "test", "example", "invalid",
	}
	for _, label := range required {
		if _, ok := blockedHostnameLabels[label]; !ok {
			t.Errorf("blockedHostnameLabels missing required label %q", label)
		}
	}
}

func TestHasIPv4PrefixLabels(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"127.0.0.1.fcm.googleapis.com", true},
		{"10.0.0.1.push.mozilla.com", true},
		{"169.254.169.254.web.push.apple.com", true},
		{"192.168.1.1.fcm.googleapis.com", true},
		{"127.0.0.1", true}, // 4-label bare dotted quad
		{"8.8.8.8", true},
		{"fcm.googleapis.com", false},
		{"push.apple.com", false},
		{"web.push.apple.com", false},
		{"3.example.com", false},        // one numeric label — not suspicious
		{"127.fcm.googleapis.com", false}, // only one numeric label
		{"mail.1.example.com", false},    // one numeric label mid-hostname
	}
	for _, c := range cases {
		got := hasIPv4PrefixLabels(c.host)
		if got != c.want {
			t.Errorf("hasIPv4PrefixLabels(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}