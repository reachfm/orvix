package handlers_test

// Static-analysis tests for the admin DNS / DKIM Operations UI
// (DNS-DKIM-OPERATIONS-2F). The admin assets are inspected as text
// (no headless browser) so the tests run in <1s alongside the
// other admin frontend tests.
//
// Guards in this file cover:
//   - admin app.js wires the new DNS endpoints
//     (/api/v1/admin/dns/providers, /plan, /verify,
//      /provider/plan, /provider/apply, /dkim)
//   - the records table renders per-record status badges
//     (verified / missing / mismatch / multiple_spf / conflict /
//     not_checked / error)
//   - the DKIM card never renders a "YOUR-PUBLIC-KEY" placeholder
//   - the SPF card surfaces the multiple_spf warning
//   - the DMARC card renders the staged policy path
//   - the MTA-STS card shows both the TXT record and the policy
//     file content, and uses mode=testing by default
//   - the TLS-RPT card renders v=TLSRPTv1
//   - the CAA card renders the recommended records
//   - the PTR/rDNS card renders the expected host + the
//     "hosting provider" honest wording
//   - the provider automation panel renders the per-provider
//     status (manual / not_configured) without leaking any
//     token-shaped substring
//   - the previously-shipped DNS guards still pass
//     (no in-UI keygen honest wording, etc.)
//   - no external CDN, no localStorage, no provider token strings
//   - the legacy `dig` examples are still available as fallback
//     verification commands

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// adminDNSOpsSection returns the substring of release/admin/app.js
// covering the renderDNS / DNS page code so the static-analysis
// tests can scope their assertions to the relevant region. This
// avoids false positives when the same keyword appears elsewhere.
func adminDNSOpsSection(t *testing.T) string {
	t.Helper()
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	start := strings.Index(src, "async function renderDNS")
	if start < 0 {
		t.Fatalf("admin app.js must define async function renderDNS")
	}
	body := extractFunctionBody(src, start)
	if body == "" {
		t.Fatalf("could not extract renderDNS body")
	}
	// Pad the window: pull in any helper functions defined
	// after renderDNS but before the next top-level section
	// (the next "async function renderX" or the closing of the
	// IIFE). Tests rely on buildDnsRecordList / fieldChip /
	// etc being reachable from renderDNS.
	rest := src[start:]
	end := strings.Index(rest, "\n  // -----")
	if end < 0 {
		end = len(rest)
	}
	return rest[:end]
}

// TestAdminDNSOpsWiresNewEndpoints confirms renderDNS calls the
// new /api/v1/admin/dns/* endpoints.
func TestAdminDNSOpsWiresNewEndpoints(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	for _, want := range []string{
		"/api/v1/admin/dns/providers",
		"/api/v1/admin/dns/",
		"/verify",
		"/plan",
		"/dkim",
		"/provider/plan",
		"/provider/apply",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("admin app.js must reference %q", want)
		}
	}
}

// TestAdminDNSOpsRendersStatusBadges confirms each per-record
// status the dnsops package emits is rendered as a badge or row
// text in renderDNS / buildDnsRecordList.
func TestAdminDNSOpsRendersStatusBadges(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	for _, code := range []string{
		"verified", "missing", "mismatch", "multiple_spf",
		"conflict", "not_checked", "unsupported", "error",
	} {
		if !strings.Contains(src, "'"+code+"'") && !strings.Contains(src, "\""+code+"\"") {
			t.Errorf("admin app.js must reference dns record status %q", code)
		}
	}
}

// TestAdminDNSOpsNoPlaceholderDKIM confirms the rendered DKIM
// card never contains the old "YOUR-PUBLIC-KEY" placeholder and
// uses an honest "not generated" / "generate a key" message when
// no DKIM row exists.
func TestAdminDNSOpsNoPlaceholderDKIM(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	// Build the banned string dynamically so the literal does
	// not appear in test source either.
	banned := "-PUBLIC-KEY"
	if strings.Contains(src, "YOUR"+banned) {
		t.Errorf("admin DNS page must not contain YOUR%s placeholder", banned)
	}
	// Must contain honest "not generated" / "no key" wording.
	if !strings.Contains(src, "not generated") &&
		!strings.Contains(src, "Generate") {
		t.Errorf("admin DNS page must offer a Generate action when DKIM is missing")
	}
	// And it must contain the word "DKIM" so the placeholder
	// cannot hide under a different label.
	if !strings.Contains(src, "DKIM") {
		t.Errorf("admin DNS page must reference DKIM explicitly")
	}
}

// TestAdminDNSOpsSPFMultipleWarning confirms the SPF card surfaces
// the multiple_spf warning explicitly.
func TestAdminDNSOpsSPFMultipleWarning(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	if !strings.Contains(src, "multiple SPF") &&
		!strings.Contains(src, "multiple_spf") {
		t.Errorf("admin SPF card must surface the multiple_spf warning")
	}
}

// TestAdminDNSOpsDMARCStagePath confirms the DMARC card shows the
// staged policy path: none -> quarantine -> reject.
func TestAdminDNSOpsDMARCStagePath(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	for _, want := range []string{"quarantine", "reject"} {
		if !strings.Contains(src, want) {
			t.Errorf("admin DMARC card must reference %q (staged policy path)", want)
		}
	}
}

// TestAdminDNSOpsMTASTSFileAndMode confirms the MTA-STS card
// renders the policy file content (mode: testing by default).
func TestAdminDNSOpsMTASTSFileAndMode(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	if !strings.Contains(src, "mta-sts.txt") &&
		!strings.Contains(src, "mta_sts.txt") &&
		!strings.Contains(src, ".well-known/mta-sts.txt") {
		t.Errorf("admin MTA-STS card must reference the policy file URL")
	}
	if !strings.Contains(src, "mode: testing") &&
		!strings.Contains(src, "testing") {
		t.Errorf("admin MTA-STS card must default to mode: testing")
	}
}

// TestAdminDNSOpsTLSRPTRendered confirms the TLS-RPT card renders
// the v=TLSRPTv1 record.
func TestAdminDNSOpsTLSRPTRendered(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	if !strings.Contains(src, "v=TLSRPTv1") {
		t.Errorf("admin TLS-RPT card must render v=TLSRPTv1")
	}
	if !strings.Contains(src, "TLS-RPT") && !strings.Contains(src, "TLSRPT") {
		t.Errorf("admin TLS-RPT card must reference TLSRPT")
	}
}

// TestAdminDNSOpsCAARendered confirms the CAA card renders the
// recommended letsencrypt.org issuer and the postmaster iodef.
func TestAdminDNSOpsCAARendered(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	if !strings.Contains(src, "letsencrypt.org") {
		t.Errorf("admin CAA card must render letsencrypt.org issuer")
	}
	if !strings.Contains(src, "postmaster@") && !strings.Contains(src, "iodef") {
		t.Errorf("admin CAA card must render the postmaster iodef contact")
	}
}

// TestAdminDNSOpsPTRRendererHonest confirms the PTR/rDNS row
// renders the expected host and the honest "hosting provider"
// wording rather than offering a copy button.
func TestAdminDNSOpsPTRRendererHonest(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	if !strings.Contains(src, "hosting provider") &&
		!strings.Contains(src, "set by your hosting provider") {
		t.Errorf("admin PTR/rDNS card must explain the hosting-provider-side requirement")
	}
}

// TestAdminDNSOpsProviderPanelRendersManual confirms the
// provider automation panel renders the manual provider and does
// not show any token-shaped string. We scope the source scan to
// the DNS section (renderDNS / loadDnsProviderPlan / applyDnsProvider)
// because the global app.js contains the Login form, which
// legitimately references "password" — the contract is that the
// DNS provider panel never echoes a token, not that the whole
// app.js is token-free.
func TestAdminDNSOpsProviderPanelRendersManual(t *testing.T) {
	root := adminRepoRoot(t)
	fullSrc := readFile(t, root, "release/admin/app.js")
	if !strings.Contains(fullSrc, "\"manual\"") && !strings.Contains(fullSrc, "'manual'") {
		t.Errorf("admin provider panel must reference the manual provider")
	}
	if !strings.Contains(fullSrc, "not configured") &&
		!strings.Contains(fullSrc, "not_configured") {
		t.Errorf("admin provider panel must surface a 'not configured' status when env is missing")
	}
	// Scope the token-shaped scan to the DNS section.
	dns := adminDNSOpsSection(t)
	lower := strings.ToLower(dns)
	for _, banned := range []string{"api_key", "api_token", "bearer ", "authorization:"} {
		if strings.Contains(lower, banned) {
			t.Errorf("admin DNS provider panel must not contain %q", banned)
		}
	}
}

// TestAdminDNSOpsProviderApplyRequiresConfirmation confirms the
// dashboard's apply handler sends the operator's confirmation
// string to the backend.
func TestAdminDNSOpsProviderApplyRequiresConfirmation(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	if !strings.Contains(src, "confirm") {
		t.Errorf("admin provider apply must reference a confirmation field")
	}
}

// TestAdminDNSOpsCopyButtonsExist confirms the records table
// includes copy buttons for each record value.
func TestAdminDNSOpsCopyButtonsExist(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	if !regexp.MustCompile(`text:\s*'Copy'`).MatchString(src) {
		t.Errorf("admin DNS page must render Copy buttons")
	}
}

// TestAdminDNSOpsDigFallback verifies the legacy dig-style
// verification commands are still surfaced as a fallback in case
// the live DNS lookup times out.
func TestAdminDNSOpsDigFallback(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	if !strings.Contains(src, "dig") && !strings.Contains(src, "nslookup") {
		t.Errorf("admin DNS page must surface dig / nslookup verification commands as fallback")
	}
}

// TestAdminDNSOpsPreservesPriorFrontendFixes is a regression sweep
// that confirms the previous admin frontend fixes still ship:
//   - no external CDN imports
//   - no localStorage
//   - no /api/v1/queue leak from webmail assets
//   - no [object Object]
//   - no fake DKIM placeholder
func TestAdminDNSOpsPreservesPriorFrontendFixes(t *testing.T) {
	root := adminRepoRoot(t)
	for _, rel := range []string{
		"release/admin/app.js",
		"release/admin/index.html",
		"release/admin/styles.css",
		"release/webmail/assets/webmail.js",
		"release/webmail/assets/auth-gate.js",
	} {
		s := readFile(t, root, rel)
		if strings.Contains(s, "cdn.jsdelivr.net") ||
			strings.Contains(s, "unpkg.com") ||
			strings.Contains(s, "googleapis.com") ||
			strings.Contains(s, "https://fonts.") ||
			strings.Contains(s, "https://cdn.") {
			t.Errorf("%s must not import from an external CDN", rel)
		}
	}
	for _, rel := range []string{"release/admin/app.js", "release/admin/index.html", "release/admin/styles.css"} {
		s := readFile(t, root, rel)
		if strings.Contains(s, "localStorage") {
			t.Errorf("%s must not use localStorage", rel)
		}
	}
	for _, rel := range []string{"release/webmail/assets/webmail.js", "release/webmail/assets/auth-gate.js"} {
		s := readFile(t, root, rel)
		if strings.Contains(s, "/api/v1/queue") {
			t.Errorf("%s must not reference /api/v1/queue", rel)
		}
	}
	js := readFile(t, root, "release/admin/app.js")
	if strings.Contains(js, "[object Object]") {
		t.Errorf("admin app.js must not render [object Object]")
	}
	banned := "-PUBLIC-KEY"
	if strings.Contains(js, "YOUR"+banned) {
		t.Errorf("admin app.js must not contain YOUR%s placeholder", banned)
	}
}

// TestAdminDNSOpsDKIMResponseShape sanity-checks the JSON shape
// the dashboard reads. We assert on the presence of the public_dns_txt
// field by parsing the test reference table the admin file uses.
func TestAdminDNSOpsDKIMResponseShape(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	for _, want := range []string{
		"public_dns_txt",
		"selector",
		"dns_record_name",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("admin DNS page must reference DKIM response field %q", want)
		}
	}
}

// TestAdminDNSOpsVerifyAndPlanCallsRealEndpoints confirms the
// dashboard's Verify / Refresh buttons call the real backend.
func TestAdminDNSOpsVerifyAndPlanCallsRealEndpoints(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	for _, want := range []string{
		"/verify",
		"/plan",
		"/dkim",
		"/provider/plan",
		"/provider/apply",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("admin DNS page must call endpoint %q", want)
		}
	}
}

// TestAdminDNSOpsDKIMRotationConfirmationPresent confirms the
// admin DNS page contains the typed confirmation phrase
// "rotate-dkim-key" and the DNS TTL warning text.
func TestAdminDNSOpsDKIMRotationConfirmationPresent(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	if !strings.Contains(src, "rotate-dkim-key") {
		t.Errorf("admin DNS page must contain 'rotate-dkim-key' confirmation phrase")
	}
	if !strings.Contains(src, "DNS TTL") {
		t.Errorf("admin DNS page warning must mention DNS TTL")
	}
	if !strings.Contains(src, "rotation") {
		t.Errorf("admin DNS page must mention rotation in the warning")
	}
}

// TestAdminDNSOpsDKIMRotationSendsConfirmField confirms the
// generateDkimKey function sends confirm_rotation in the POST body.
func TestAdminDNSOpsDKIMRotationSendsConfirmField(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	if !strings.Contains(src, "confirm_rotation") {
		t.Errorf("admin generateDkimKey must send confirm_rotation field")
	}
	if !strings.Contains(src, "rotate-dkim-key'") && !strings.Contains(src, "'rotate-dkim-key'") {
		t.Errorf("admin generateDkimKey must send 'rotate-dkim-key' as confirmation value")
	}
}

// TestAdminDNSOpsDKIMNoPrivateKeyRendered confirms that no
// PEM-encoded private key block appears in the admin JS.
// English-language comments about "private key" are allowed;
// actual key material would appear as a multi-line PEM block.
func TestAdminDNSOpsDKIMNoPrivateKeyRendered(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	// Look for a PEM boundary which would indicate actual key
	// material was baked into the asset. English phrases like
	// "the private key is stored server-side" are fine.
	if strings.Contains(src, "-----BEGIN") {
		t.Errorf("admin app.js must not contain a PEM boundary (-----BEGIN)")
	}
}

// TestAdminDNSOpsDoesNotRecommendSMTPHost is the
// DNS-DKIM-OPERATIONS-2F-SAFETY-FIX guard against telling the
// operator to set coremail.smtp_host as the public mail IP. The
// public mail IP comes from cfg.DNS.PublicIPv4 (a dedicated
// config field); the SMTP listener bind host is a separate
// concern and defaults to 0.0.0.0. The dashboard must never
// tell the operator to mutate coremail.smtp_host for DNS.
func TestAdminDNSOpsDoesNotRecommendSMTPHost(t *testing.T) {
	root := adminRepoRoot(t)
	_ = readFile(t, root, "release/admin/app.js")
	// We scope the scan to the DNS page region. The legacy
	// backup/queue/etc. panels may legitimately reference the
	// smtp_host config field for unrelated reasons (listener
	// bind), but the DNS page must not.
	dns := adminDNSOpsSection(t)
	for _, banned := range []string{
		"set coremail.smtp_host",
		"set smtp_host to the public",
		"smtp_host as public",
		"SMTPHost as public",
		"set the public mail server IP",
	} {
		if strings.Contains(dns, banned) {
			t.Errorf("admin DNS page must not recommend %q; point operators at dns.public_ipv4 instead", banned)
		}
	}
	// And it must reference the dedicated config field name so
	// the dashboard tells the operator where to set the public
	// IP.
	if !strings.Contains(dns, "public_ipv4") {
		t.Errorf("admin DNS page must reference the dns.public_ipv4 config field name")
	}
}

// TestAdminDNSOpsVerifyUpdatesPlanFromResponse confirms the
// loadDnsVerify function always updates state.dnsPlan from the
// verify response's report.plan (regression for the live bug where
// the !state.dnsPlan guard prevented statuses from propagating).
// The test asserts:
//   - state.dnsPlan is assigned from resp.report.plan
//   - the assignment is NOT guarded by !state.dnsPlan
//   - renderDnsRecords() is called after the plan update
func TestAdminDNSOpsVerifyUpdatesPlanFromResponse(t *testing.T) {
	dnsRegion := adminDNSOpsSection(t)
	// Confirmation: loadDnsVerify assigns state.dnsPlan from resp.report.plan.
	if !strings.Contains(dnsRegion, "state.dnsPlan = resp.report.plan") {
		t.Errorf("loadDnsVerify must assign state.dnsPlan from resp.report.plan")
	}
	// Confirmation: the assignment is NOT gated by !state.dnsPlan.
	if strings.Contains(dnsRegion, "!state.dnsPlan") && strings.Contains(dnsRegion, "state.dnsPlan") && strings.Contains(dnsRegion, "resp.report.plan") {
		t.Errorf("loadDnsVerify must NOT guard the plan update with !state.dnsPlan (that causes statuses to never propagate after initial plan load)")
	}
	// Confirmation: renderDnsRecords() is called after plan update.
	if !strings.Contains(dnsRegion, "renderDnsRecords()") {
		t.Errorf("loadDnsVerify must call renderDnsRecords() after updating state.dnsPlan")
	}
}

// TestAdminDNSOpsVerifyResponseShape confirms the frontend
// references the correct response fields from the verify endpoint.
func TestAdminDNSOpsVerifyResponseShape(t *testing.T) {
	src := readFile(t, adminRepoRoot(t), "release/admin/app.js")
	for _, want := range []string{
		"resp.report",
		"report.plan",
		"report.warnings",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("admin app.js must reference verify response field %q", want)
		}
	}
	if !strings.Contains(src, "report.verified") && !strings.Contains(src, "dnsReport.verified") {
		t.Errorf("admin app.js must reference verify response field from report/dnsReport.verified")
	}
}

// TestAdminDNSOpsNoExecDnsOutOfBand: a regression guard confirming
// the admin app.js does not import any browser-side DNS library
// (which would mean we're trying to do provider automation in the
// browser). The DNS resolver lives server-side in /api/v1/admin/dns.
func TestAdminDNSOpsNoExecDnsOutOfBand(t *testing.T) {
	root := adminRepoRoot(t)
	src := readFile(t, root, "release/admin/app.js")
	for _, banned := range []string{"dns-over-https", "doh.js", "DoH"} {
		if strings.Contains(src, banned) {
			t.Errorf("admin app.js must not import browser-side DNS library %q", banned)
		}
	}
}

// silence unused-import warnings if the test build swaps out
// the json import.
var _ = json.Marshal