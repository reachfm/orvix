package handlers_test

// End-to-end handler tests for DNS-DKIM-OPERATIONS-2F-SAFETY-FIX.
//
// These tests pin the three Codex blockers through the full
// handler stack:
//
//   1. DNS plan does NOT use SMTP bind host as public IPv4; a
//      fresh default config (SMTPHost=0.0.0.0, PublicIPv4="")
//      must not generate 0.0.0.0 records; configured PublicIPv4
//      generates the correct A / SPF record.
//   2. DKIM keygen for an unprovisioned domain returns 404
//      and does NOT insert a coremail_dkim_config row.
//   3. DKIM selector validation rejects unsafe selectors
//      (dot, space, slash, underscore, unicode, leading /
//      trailing hyphen, too long) with 400 and does not
//      generate or store a key.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestDNSOpsPlanNoSMTPHostAsPublicIP is the core
// DNS-DKIM-OPERATIONS-2F-SAFETY-FIX test. With the default
// config (no PublicIPv4), the plan endpoint must NOT generate
// records containing the listener bind address (0.0.0.0) or
// any value derived from SMTPHost.
func TestDNSOpsPlanNoSMTPHostAsPublicIP(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	// Strip the harness's default PublicIPv4 (203.0.113.10) so
	// the test exercises the no-PublicIPv4 path. We do this
	// AFTER newDNSOpsHarness returns; the harness has a
	// pointer to the config via the handler.
	// (The harness builds a Handler with the cfg; we reset the
	// DNS block by closing and re-opening a new harness with
	// PublicIPv4 explicitly empty.)
	h2 := newDNSOpsHarnessNoPublicIP(t)
	defer h2.close()
	code, body := h2.do(t, "GET", "/api/v1/admin/dns/example.com/plan", h2.adminT, "")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("plan without public IPv4 must be 422; got %d body=%s", code, body)
	}
	if !strings.Contains(body, "public mail IPv4 is not configured") {
		t.Errorf("422 body must explain the missing input; got %s", body)
	}
	if strings.Contains(body, "coremail.smtp_host") &&
		!strings.Contains(body, "do NOT use coremail.smtp_host") {
		// The new error message mentions coremail.smtp_host in
		// the negative ("do NOT use ..."). The dashboard must
		// not tell the operator to set coremail.smtp_host as
		// the public IP — only the negative phrasing is
		// permitted.
		t.Errorf("422 must not tell the operator to set coremail.smtp_host; got %s", body)
	}
}

// TestDNSOpsPlanWithDefaultSMTPHostZeroDoesNotFabricate is the
// first blocker test: with the default config (SMTPHost=0.0.0.0
// and no PublicIPv4), the plan endpoint must NOT generate
// 0.0.0.0 records. We assert both that the response is 422 and
// that no part of the body contains the string "0.0.0.0".
func TestDNSOpsPlanWithDefaultSMTPHostZeroDoesNotFabricate(t *testing.T) {
	h := newDNSOpsHarnessNoPublicIP(t)
	defer h.close()
	code, body := h.do(t, "GET", "/api/v1/admin/dns/example.com/plan", h.adminT, "")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("default config must surface 422; got %d body=%s", code, body)
	}
	if strings.Contains(body, "0.0.0.0") {
		t.Errorf("plan must not contain 0.0.0.0; got %s", body)
	}
}

// TestDNSOpsPlanWithPublicIPv4GeneratesCorrectARecord confirms
// that with a configured public IPv4 (and the listener bind
// address still 0.0.0.0), the A record uses the public IPv4
// only, not 0.0.0.0.
func TestDNSOpsPlanWithPublicIPv4GeneratesCorrectARecord(t *testing.T) {
	h := newDNSOpsHarness(t) // harness sets PublicIPv4 = 203.0.113.10
	defer h.close()
	code, body := h.do(t, "GET", "/api/v1/admin/dns/example.com/plan", h.adminT, "")
	if code != http.StatusOK {
		t.Fatalf("plan must be 200; got %d body=%s", code, body)
	}
	var resp struct {
		Plan struct {
			ServerIPv4 string `json:"server_ipv4"`
			Records    []struct {
				Type    string `json:"type"`
				Name    string `json:"name"`
				Value   string `json:"value"`
				Purpose string `json:"purpose"`
			} `json:"records"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, body)
	}
	if resp.Plan.ServerIPv4 != "203.0.113.10" {
		t.Errorf("plan.ServerIPv4: got %q want 203.0.113.10", resp.Plan.ServerIPv4)
	}
	if strings.Contains(body, "0.0.0.0") {
		t.Errorf("plan body must not mention 0.0.0.0; got %s", body)
	}
	var sawA, sawSPF bool
	for _, r := range resp.Plan.Records {
		if r.Purpose == "mail_a" {
			sawA = true
			if r.Value != "203.0.113.10" {
				t.Errorf("A record value: got %q want 203.0.113.10", r.Value)
			}
			if strings.Contains(r.Value, "0.0.0.0") {
				t.Errorf("A record must not be 0.0.0.0; got %q", r.Value)
			}
		}
		if r.Purpose == "spf" {
			sawSPF = true
			if !strings.Contains(r.Value, "203.0.113.10") {
				t.Errorf("SPF must include the public IPv4; got %q", r.Value)
			}
			if strings.Contains(r.Value, "0.0.0.0") {
				t.Errorf("SPF must not include 0.0.0.0; got %q", r.Value)
			}
		}
	}
	if !sawA {
		t.Errorf("plan must include mail_a record")
	}
	if !sawSPF {
		t.Errorf("plan must include SPF record")
	}
}

// TestDNSOpsDKIMRequiresDomain is the second blocker test: DKIM
// keygen for a domain that is NOT in coremail_domains must
// return 404 and must NOT insert a row.
func TestDNSOpsDKIMRequiresDomain(t *testing.T) {
	h := newDNSOpsHarness(t) // harness seeds only example.com
	defer h.close()
	// orphan.example is not in coremail_domains.
	code, body := h.do(t, "POST", "/api/v1/admin/dns/orphan.example/dkim", h.adminT,
		`{"selector":"orvix"}`)
	if code != http.StatusNotFound {
		t.Errorf("DKIM for unprovisioned domain must be 404; got %d body=%s", code, body)
	}
	if !strings.Contains(body, "not provisioned") {
		t.Errorf("404 body must explain the missing domain; got %s", body)
	}
	// Confirm no coremail_dkim_config row was inserted.
	sqlDB, _ := h.db.DB()
	var count int
	if err := sqlDB.QueryRow(
		`SELECT COUNT(*) FROM coremail_dkim_config WHERE domain = ?`,
		"orphan.example").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("orphan DKIM row must NOT be inserted; got %d rows", count)
	}
}

// TestDNSOpsDKIMRejectsUnsafeSelector is the third blocker
// test: the selector validator must reject every documented
// unsafe shape and the handler must NOT store a row.
func TestDNSOpsDKIMRejectsUnsafeSelector(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	unsafeSelectors := []string{
		"foo.bar",     // dot
		"foo bar",     // space
		"foo/bar",     // slash
		"foo_bar",     // underscore
		"-foo",        // leading hyphen
		"foo-",        // trailing hyphen
		"foo--bar",    // consecutive hyphens
		"α",           // unicode
		"foo*",        // wildcard
		strings.Repeat("a", 64), // too long
	}
	for _, sel := range unsafeSelectors {
		body := `{"selector":"` + sel + `"}`
		code, respBody := h.do(t, "POST", "/api/v1/admin/dns/example.com/dkim", h.adminT, body)
		if code != http.StatusBadRequest {
			t.Errorf("selector %q must be 400; got %d body=%s", sel, code, respBody)
			continue
		}
		if !strings.Contains(respBody, "selector") {
			t.Errorf("400 body must mention selector; got %s", respBody)
		}
	}
	// Confirm no coremail_dkim_config row was inserted for any
	// of the rejected attempts.
	sqlDB, _ := h.db.DB()
	var count int
	if err := sqlDB.QueryRow(
		`SELECT COUNT(*) FROM coremail_dkim_config WHERE domain = ?`,
		"example.com").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("unsafe-selector attempts must NOT insert DKIM rows; got %d", count)
	}
}

// TestDNSOpsDKIMAcceptsSafeSelectorAndEmptyDefault confirms
// the happy path: safe selectors and empty default both work.
func TestDNSOpsDKIMAcceptsSafeSelectorAndEmptyDefault(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	cases := []struct {
		body, wantSel string
	}{
		{`{"selector":"orvix"}`, "orvix"},
		{`{"selector":"ORVIX"}`, "orvix"}, // uppercased -> lowercased
		{`{"selector":"s1"}`, "s1"},
		{`{}`, "orvix"},                  // empty -> default
		{`{"selector":""}`, "orvix"},     // empty string -> default
	}
	for i, c := range cases {
		code, body := h.do(t, "POST", "/api/v1/admin/dns/example.com/dkim", h.adminT, c.body)
		if code != http.StatusCreated {
			t.Errorf("case %d body=%s must be 201; got %d body=%s", i, c.body, code, body)
			continue
		}
		var resp struct {
			Selector       string `json:"selector"`
			DNSRecordName  string `json:"dns_record_name"`
		}
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatalf("case %d unmarshal: %v body=%s", i, err, body)
		}
		if resp.Selector != c.wantSel {
			t.Errorf("case %d selector: got %q want %q", i, resp.Selector, c.wantSel)
		}
		wantRec := c.wantSel + "._domainkey.example.com"
		if resp.DNSRecordName != wantRec {
			t.Errorf("case %d dns_record_name: got %q want %q", i, resp.DNSRecordName, wantRec)
		}
	}
}

// TestDNSOpsDKIMRejectsRejectsLongSelector is a focused variant
// of the unsafe-selector test that also asserts the error
// message mentions the length limit.
func TestDNSOpsDKIMRejectsLongSelector(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	long := strings.Repeat("a", 200)
	body := `{"selector":"` + long + `"}`
	code, respBody := h.do(t, "POST", "/api/v1/admin/dns/example.com/dkim", h.adminT, body)
	if code != http.StatusBadRequest {
		t.Errorf("long selector must be 400; got %d body=%s", code, respBody)
	}
	if !strings.Contains(respBody, "too long") {
		t.Errorf("error must mention length; got %s", respBody)
	}
}

// TestDNSOpsPlanWithPrivatePublicIPv4Rejected confirms that a
// private (RFC1918) IPv4 set as PublicIPv4 is rejected by the
// validator. The plan endpoint must not generate a record for a
// private IP.
func TestDNSOpsPlanWithPrivatePublicIPv4Rejected(t *testing.T) {
	h := newDNSOpsHarnessWithPublicIP(t, "10.0.0.1", "")
	defer h.close()
	code, body := h.do(t, "GET", "/api/v1/admin/dns/example.com/plan", h.adminT, "")
	if code != http.StatusUnprocessableEntity {
		t.Errorf("plan with private public IPv4 must be 422; got %d body=%s", code, body)
	}
	if !strings.Contains(body, "private-range") {
		t.Errorf("422 body must explain private-range rejection; got %s", body)
	}
}

// TestDNSOpsPlanWithLoopbackPublicIPv4Rejected confirms that
// 127.0.0.1 as PublicIPv4 is rejected.
func TestDNSOpsPlanWithLoopbackPublicIPv4Rejected(t *testing.T) {
	h := newDNSOpsHarnessWithPublicIP(t, "127.0.0.1", "")
	defer h.close()
	code, body := h.do(t, "GET", "/api/v1/admin/dns/example.com/plan", h.adminT, "")
	if code != http.StatusUnprocessableEntity {
		t.Errorf("plan with loopback public IPv4 must be 422; got %d body=%s", code, body)
	}
	if !strings.Contains(body, "loopback") {
		t.Errorf("422 body must explain loopback rejection; got %s", body)
	}
}

// TestDNSOpsPlanWithLinkLocalPublicIPv4Rejected confirms that
// 169.254.0.1 as PublicIPv4 is rejected.
func TestDNSOpsPlanWithLinkLocalPublicIPv4Rejected(t *testing.T) {
	h := newDNSOpsHarnessWithPublicIP(t, "169.254.0.1", "")
	defer h.close()
	code, body := h.do(t, "GET", "/api/v1/admin/dns/example.com/plan", h.adminT, "")
	if code != http.StatusUnprocessableEntity {
		t.Errorf("plan with link-local public IPv4 must be 422; got %d body=%s", code, body)
	}
}
