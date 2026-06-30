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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api/handlers"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/dnsops"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
)

// TestDNSOpsPlanNoSMTPHostAsPublicIP is the core
// DNS-DKIM-OPERATIONS-2F-SAFETY-FIX test. With the default
// config (no PublicIPv4), the plan endpoint must NOT generate
// records containing the listener bind address (0.0.0.0) or
// any value derived from SMTPHost.
func TestDNSOpsPlanNoSMTPHostAsPublicIP(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	// Strip the harness's default PublicIPv4 (8.8.8.8) so
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
	h := newDNSOpsHarness(t) // harness sets PublicIPv4 = 8.8.8.8
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
	if resp.Plan.ServerIPv4 != "8.8.8.8" {
		t.Errorf("plan.ServerIPv4: got %q want 8.8.8.8", resp.Plan.ServerIPv4)
	}
	// The body MAY contain "0.0.0.0" in the informational
	// `listener_bind` field (which echoes coremail.smtp_host
	// separately from the public DNS plan). What MUST NOT
	// contain 0.0.0.0 are the actual A / AAAA / SPF / MX
	// record values — those are the records that would be
	// published at the DNS provider. We assert per-record
	// below.
	var sawA, sawSPF bool
	for _, r := range resp.Plan.Records {
		if r.Purpose == "mail_a" {
			sawA = true
			if r.Value != "8.8.8.8" {
				t.Errorf("A record value: got %q want 8.8.8.8", r.Value)
			}
			if strings.Contains(r.Value, "0.0.0.0") {
				t.Errorf("A record must not be 0.0.0.0; got %q", r.Value)
			}
		}
		if r.Purpose == "spf" {
			sawSPF = true
			if !strings.Contains(r.Value, "8.8.8.8") {
				t.Errorf("SPF must include the public IPv4; got %q", r.Value)
			}
			if strings.Contains(r.Value, "0.0.0.0") {
				t.Errorf("SPF must not include 0.0.0.0; got %q", r.Value)
			}
		}
		// No record value (A / AAAA / MX / SPF / DKIM / DMARC)
		// may be 0.0.0.0. listener_bind is a Plan-level field
		// checked separately.
		switch r.Type {
		case "A", "AAAA", "MX", "SPF", "TXT", "CNAME", "CAA":
			if r.Value == "0.0.0.0" {
				t.Errorf("%s record %q must not be 0.0.0.0; purpose=%s", r.Type, r.Value, r.Purpose)
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

// TestDNSOpsDKIMRotationConfirmationRequired confirms the
// rotation confirmation enforcement through the full handler stack.
func TestDNSOpsDKIMRotationConfirmationRequired(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	// First: create a DKIM key.
	code, body := h.do(t, "POST", "/api/v1/admin/dns/example.com/dkim", h.adminT, `{"selector":"a"}`)
	if code != http.StatusCreated {
		t.Fatalf("first generate must be 201; got %d body=%s", code, body)
	}
	// Second: try to rotate without confirmation — must be 409.
	code2, body2 := h.do(t, "POST", "/api/v1/admin/dns/example.com/dkim", h.adminT, `{"selector":"b"}`)
	if code2 != http.StatusConflict {
		t.Errorf("rotation without confirmation must be 409; got %d body=%s", code2, body2)
	}
	if !strings.Contains(body2, "already exists") {
		t.Errorf("409 body must explain; got %s", body2)
	}
	// Third: try with wrong confirmation — must be 409.
	code3, body3 := h.do(t, "POST", "/api/v1/admin/dns/example.com/dkim", h.adminT,
		`{"selector":"b","confirm_rotation":"wrong"}`)
	if code3 != http.StatusConflict {
		t.Errorf("wrong confirmation must be 409; got %d body=%s", code3, body3)
	}
	// Fourth: correct confirmation succeeds.
	code4, body4 := h.do(t, "POST", "/api/v1/admin/dns/example.com/dkim", h.adminT,
		`{"selector":"b","confirm_rotation":"rotate-dkim-key"}`)
	if code4 != http.StatusCreated {
		t.Errorf("correct confirmation must be 201; got %d body=%s", code4, body4)
	}
	// Private key must not appear in any response.
	for _, banned := range []string{"PRIVATE KEY", "BEGIN PRIVATE"} {
		if strings.Contains(strings.ToUpper(body4), banned) {
			t.Errorf("response must not contain %q; got %s", banned, body4)
		}
	}
}

// TestDNSOpsDKIMFirstCreateIgnoresConfirmationField confirms that
// the first DKIM creation for a domain does not require
// confirm_rotation, but also does not reject it if present.
func TestDNSOpsDKIMFirstCreateIgnoresConfirmationField(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	code, body := h.do(t, "POST", "/api/v1/admin/dns/example.com/dkim", h.adminT,
		`{"selector":"orvix","confirm_rotation":"rotate-dkim-key"}`)
	if code != http.StatusCreated {
		t.Fatalf("first create with confirm_rotation must be 201; got %d body=%s", code, body)
	}
	var count int
	sqlDB, _ := h.db.DB()
	_ = sqlDB.QueryRow(`SELECT COUNT(*) FROM coremail_dkim_config WHERE domain = ?`, "example.com").Scan(&count)
	if count != 1 {
		t.Errorf("DKIM row must be inserted; got %d rows", count)
	}
	// Second call without confirmation must still fail (existing key).
	code2, _ := h.do(t, "POST", "/api/v1/admin/dns/example.com/dkim", h.adminT, `{"selector":"orvix"}`)
	if code2 != http.StatusConflict {
		t.Errorf("second call without confirmation must be 409; got %d", code2)
	}
}

// TestDNSOpsDKIMAcceptsSafeSelectorAndEmptyDefault confirms
// the happy path: safe selectors and empty default both work.
// Each case uses a fresh domain so rotation confirmation is not
// needed.
func TestDNSOpsDKIMAcceptsSafeSelectorAndEmptyDefault(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	sqlDB, _ := h.db.DB()
	cases := []struct {
		name, body, wantSel string
	}{
		{"explicit orvix", `{"selector":"orvix"}`, "orvix"},
		{"uppercased", `{"selector":"ORVIX"}`, "orvix"},
		{"short", `{"selector":"s1"}`, "s1"},
		{"empty json", `{}`, "orvix"},
		{"empty string", `{"selector":""}`, "orvix"},
	}
	for i, c := range cases {
		dom := fmt.Sprintf("case%d.example.com", i)
		if _, err := sqlDB.Exec(
			`INSERT INTO coremail_domains (name, status, plan, description, max_mailboxes, max_aliases, max_quota_mb,
			   dkim_enabled, dkim_selector, dmarc_enabled, mtasts_enabled, catchall_address, abuse_contact, labels,
			   mailbox_count, created_at, updated_at)
			 VALUES (?, 'active', 'smb', '', 100, 50, 1024, 0, '', 0, 0, '', '', '', 0, ?, ?)`,
			dom, time.Now().UTC(), time.Now().UTC()); err != nil {
			t.Fatalf("seed domain %s: %v", dom, err)
		}
		code, body := h.do(t, "POST", "/api/v1/admin/dns/"+dom+"/dkim", h.adminT, c.body)
		if code != http.StatusCreated {
			t.Errorf("case %d (%s) body=%s must be 201; got %d body=%s", i, c.name, c.body, code, body)
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
		wantRec := c.wantSel + "._domainkey." + dom
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

func TestDNSOpsPlanWithTestNetIPv4Rejected(t *testing.T) {
	for _, ip := range []string{"192.0.2.1", "198.51.100.1", "203.0.113.10"} {
		h := newDNSOpsHarnessWithPublicIP(t, ip, "")
		code, body := h.do(t, "GET", "/api/v1/admin/dns/example.com/plan", h.adminT, "")
		if code != http.StatusUnprocessableEntity {
			t.Errorf("plan with TEST-NET IPv4 %s must be 422; got %d body=%s", ip, code, body)
		}
		if !strings.Contains(body, "TEST-NET") {
			t.Errorf("422 body must mention TEST-NET; got %s", body)
		}
		h.close()
	}
}

func TestDNSOpsPlanWithDocumentationIPv6Rejected(t *testing.T) {
	h := newDNSOpsHarnessWithPublicIP(t, "", "2001:db8::1")
	defer h.close()
	code, body := h.do(t, "GET", "/api/v1/admin/dns/example.com/plan", h.adminT, "")
	if code != http.StatusUnprocessableEntity {
		t.Errorf("plan with documentation IPv6 must be 422; got %d body=%s", code, body)
	}
	if !strings.Contains(body, "documentation") && !strings.Contains(body, "3849") {
		t.Errorf("422 body must mention documentation range; got %s", body)
	}
}
// TestDNSOpsDKIMKeygenWorksAfterMigrateAllRaw is the regression
// test for the live VPS blocker: a fresh DB initialized via the
// canonical migration path (models.MigrateAllRaw) must have the
// coremail_dkim_config table so that the DKIM keygen handler
// does not fail with "no such table".
func TestDNSOpsDKIMKeygenWorksAfterMigrateAllRaw(t *testing.T) {
	dir := t.TempDir()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(dir, "orvix.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.CoreMail.SMTPPort = 25
	cfg.CoreMail.IMAPPort = 143
	cfg.CoreMail.POP3Port = 110
	cfg.CoreMail.JMAPPort = 8080
	cfg.CoreMail.MailStorePath = dir
	cfg.DNS.PublicIPv4 = "8.8.8.8"
	cfg.CoreMail.Hostname = ""

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	// Close the DB handle before t.TempDir() cleanup runs.
	defer sqlDB.Close()

	// Run the REAL canonical migration — this is what main.go does.
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Verify the DKIM table was created by MigrateAllRaw.
	var tableCount int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='coremail_dkim_config'").Scan(&tableCount); err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if tableCount != 1 {
		t.Fatalf("coremail_dkim_config must exist after MigrateAllRaw")
	}
	// Create remaining schema tables the handler test harness needs.
	if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS coremail_audit (
		id INTEGER PRIMARY KEY AUTOINCREMENT, actor TEXT NOT NULL DEFAULT '',
		role TEXT NOT NULL DEFAULT '', action TEXT NOT NULL DEFAULT '',
		target TEXT NOT NULL DEFAULT '', result TEXT NOT NULL DEFAULT '',
		ip TEXT NOT NULL DEFAULT '', user_agent TEXT NOT NULL DEFAULT '',
		timestamp DATETIME NOT NULL
	)`); err != nil {
		t.Fatalf("create coremail_audit: %v", err)
	}

	authn, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	ff := license.NewFeatureFlags(logger)
	ff.SetTier(license.TierSMB)
	h := handlers.NewHandler(db, authn, nil, logger, cfg, modules.NewRegistry(logger), ff, nil)
	resolver := dnsops.NewFakeResolver()
	svc := dnsops.NewService(resolver)
	h.SetDNSOpsService(svc)

	app := fiber.New()
	adminTok, _ := authn.GenerateAccessToken(1, auth.RoleAdmin)
	mount := func(method, path string, fn fiber.Handler) {
		app.Add([]string{method}, path, func(c fiber.Ctx) error {
			c.Locals("user_id", uint(1))
			c.Locals("role", auth.RoleAdmin)
			return fn(c)
		})
	}
	mount("POST", "/admin/dns/:domain/dkim", h.PostAdminDNSDKIM)
	// Seed a provisioned domain.
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_domains (name, status, plan, max_mailboxes, max_aliases, max_quota_mb,
		   mailbox_count, created_at, updated_at)
		 VALUES ('example.com', 'active', 'smb', 100, 50, 1024, 0, ?, ?)`,
		time.Now().UTC(), time.Now().UTC()); err != nil {
		t.Fatalf("seed domain: %v", err)
	}

	// This is the call that failed on the live VPS with "no such table".
	code, body := dnsOpsHarnessDo(app, adminTok, "POST", "/admin/dns/example.com/dkim", `{"selector":"orvix"}`)
	if code != http.StatusCreated {
		t.Fatalf("DKIM keygen must succeed after MigrateAllRaw; got %d body=%s", code, body)
	}
	// No private key in response.
	for _, banned := range []string{"PRIVATE KEY", "BEGIN PRIVATE"} {
		if strings.Contains(strings.ToUpper(body), banned) {
			t.Errorf("response must not contain %q; got %s", banned, body)
		}
	}
}

// dnsOpsHarnessDo is a minimal request helper for the
// VPS-regression test above.
func dnsOpsHarnessDo(app *fiber.App, token, method, path, body string) (int, string) {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, _ := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if res != nil {
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	return 0, ""
}
