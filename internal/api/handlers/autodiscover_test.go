package handlers_test

// Tests for the mail client setup endpoints — Outlook Autodiscover
// (lowercase + uppercase paths, GET + POST) and Mozilla Thunderbird
// autoconfig (well-known + /mail fallback).
//
// These tests mount ONLY the public client-setup handlers, not the
// full router, so they do not need an admin token or a real
// coremail runtime module. The handler's only dependencies are:
//   - cfg.CoreMail (for IMAP/SMTP host + port defaults)
//   - db (a sqlite handle with a coremail_domains table)
//
// We seed one row for example.com and verify that:
//   - known local domain returns well-formed XML with IMAP 993
//     and SMTP 587 and the full email address as username;
//   - unknown domain is rejected safely (404 for autoconfig,
//     200 with an error envelope for Autodiscover);
//   - the response never contains a password.
//   - GET and POST both work on the Outlook lowercase path.

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"

	"github.com/orvix/orvix/internal/api/handlers"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/modules"
	"gorm.io/gorm"
)

// autodiscoverHarness is a minimal fiber app + handler that
// mounts only the four public client-setup endpoints. It seeds a
// single coremail_domains row for example.com so the
// "known local domain" assertions have something to look up.
type autodiscoverHarness struct {
	app *fiber.App
	h   *handlers.Handler
	db  *gormDB
	dir string
}

// gormDB is the minimal interface we need from a *gorm.DB in
// tests; we only call .DB().Close() through it.
type gormDB = gorm.DB

func (ah *autodiscoverHarness) close() {
	if ah.db == nil {
		return
	}
	if sqlDB, err := ah.db.DB(); err == nil && sqlDB != nil {
		_ = sqlDB.Close()
	}
}

// newAutodiscoverHarness builds the harness. cfg.CoreMail
// defaults are intentionally left zero so the tests verify the
// "mail.<domain>" + 993 / 587 fallback path.
func newAutodiscoverHarness(t *testing.T) *autodiscoverHarness {
	t.Helper()
	logger := zap.NewNop()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(dir, "test.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.License.OfflineMode = true
	// Force Hostname / ports to their zero values so the
	// handler falls back to mail.<domain>, 993, 587.
	cfg.CoreMail.Hostname = ""
	cfg.CoreMail.IMAPsPort = 0
	cfg.CoreMail.SubmissionPort = 0

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	sqlDB, _ := db.DB()
	if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS coremail_domains (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		tenant_id INTEGER NOT NULL DEFAULT 1,
		reseller_id INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'active',
		plan TEXT NOT NULL DEFAULT 'smb',
		description TEXT NOT NULL DEFAULT '',
		max_mailboxes INTEGER NOT NULL DEFAULT 100,
		max_aliases INTEGER NOT NULL DEFAULT 50,
		max_quota_mb INTEGER NOT NULL DEFAULT 1024,
		dkim_enabled INTEGER NOT NULL DEFAULT 0,
		dkim_selector TEXT NOT NULL DEFAULT '',
		dmarc_enabled INTEGER NOT NULL DEFAULT 0,
		mtasts_enabled INTEGER NOT NULL DEFAULT 0,
		catchall_address TEXT NOT NULL DEFAULT '',
		abuse_contact TEXT NOT NULL DEFAULT '',
		labels TEXT NOT NULL DEFAULT '',
		mailbox_count INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		deleted_at DATETIME
	)`); err != nil {
		t.Fatalf("create coremail_domains: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_domains (name, status, plan, description, max_mailboxes, max_aliases, max_quota_mb,
		   dkim_enabled, dkim_selector, dmarc_enabled, mtasts_enabled, catchall_address, abuse_contact, labels,
		   mailbox_count, created_at, updated_at)
		 VALUES ('example.com', 'active', 'smb', '', 100, 50, 1024, 0, '', 0, 0, '', '', '', 0, ?, ?)`,
		time.Now().UTC(), time.Now().UTC()); err != nil {
		t.Fatalf("seed coremail_domains: %v", err)
	}

	authn, _ := auth.NewAuthenticator(&cfg.Auth, db, logger)
	ff := license.NewFeatureFlags(logger)
	h := handlers.NewHandler(db, authn, nil, logger, cfg, modules.NewRegistry(logger), ff, nil)

	app := fiber.New(fiber.Config{ReadBufferSize: 128 * 1024})
	app.Get("/autodiscover/autodiscover.xml", h.OutlookAutodiscoverXML)
	app.Post("/autodiscover/autodiscover.xml", h.OutlookAutodiscoverXML)
	app.Get("/Autodiscover/Autodiscover.xml", h.OutlookAutodiscoverXMLUpper)
	app.Post("/Autodiscover/Autodiscover.xml", h.OutlookAutodiscoverXMLUpper)
	app.Get("/.well-known/autoconfig/mail/config-v1.1.xml", h.MozillaAutoconfig)
	app.Get("/mail/config-v1.1.xml", h.MozillaAutoconfigFallback)

	return &autodiscoverHarness{app: app, h: h, db: db, dir: dir}
}

// doRequest runs a request against the harness and returns
// (status, body).
func doRequest(t *testing.T, app *fiber.App, method, target, body string) (int, string) {
	t.Helper()
	code, body2, _ := doRequestWithHeaders(t, app, method, target, body)
	return code, body2
}

// doRequestWithHeaders runs a request and returns the status
// code, body, AND a copy of the response headers so callers
// can assert on Content-Type and similar. The Fiber test
// transport uses fasthttp under the hood; its response
// headers are available via resp.Header.
//
// Important: this helper requires the supplied fiber.App
// to have been constructed with `fiber.Config{ReadBufferSize:
// 128 * 1024}` (or larger) so BLOCKER-2's oversized-POST
// tests can read the body off the wire without Fiber's
// default 4 KiB buffer truncating it.
func doRequestWithHeaders(t *testing.T, app *fiber.App, method, target, body string) (int, string, http.Header) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/xml")
	}
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

// assertNoPasswordInResponse scans the body for any of the
// documented secret keywords (case-insensitive). The four
// endpoints must NEVER leak credentials — neither the user's
// password (the client supplied it in the original login form,
// we never see it here) nor any API key / token.
//
// IMPORTANT: the literal string `password-cleartext` IS allowed
// because it is the documented Outlook / Mozilla auth-method
// label, not a leaked credential. We therefore check for
// patterns that look like a leaked secret (e.g. `password=`,
// `<password>foo</password>`) rather than the bare word.
func assertNoPasswordInResponse(t *testing.T, body string) {
	t.Helper()
	lower := strings.ToLower(body)
	banned := []string{
		"<password>",
		"password=",
		"password:",
		"passwd=",
		"secret=",
		"api_key=",
		"api-key=",
		"bearer ",
		"authorization:",
		"access_token=",
		"refresh_token=",
	}
	for _, b := range banned {
		if strings.Contains(lower, b) {
			t.Errorf("response must not contain credential leak %q; got: %s", b, body)
		}
	}
}

// TestOutlookAutodiscoverLowercaseGET exercises the lowercase
// path with a GET and a known domain. The response must:
//   - return 200
//   - contain "993" (IMAP SSL)
//   - contain "587" (SMTP submission)
//   - contain the full email as username
//   - contain "mail.example.com" as the mail host (the fallback
//     we forced by leaving cfg.CoreMail.Hostname empty)
//   - never contain a password
//   - never contain a stack trace / debug dump
func TestOutlookAutodiscoverLowercaseGET(t *testing.T) {
	ah := newAutodiscoverHarness(t)
	defer ah.close()

	code, body := doRequest(t, ah.app, http.MethodGet,
		"/autodiscover/autodiscover.xml?email=user@example.com", "")
	if code != http.StatusOK {
		t.Fatalf("lowercase GET: want 200, got %d body=%s", code, body)
	}
	for _, want := range []string{
		"993",
		"587",
		"user@example.com",
		"mail.example.com",
		"IMAP",
		"SMTP",
		"password-cleartext",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("lowercase GET: response missing %q; body=%s", want, body)
		}
	}
	assertNoPasswordInResponse(t, body)
}

// TestOutlookAutodiscoverLowercasePOST exercises the lowercase
// path with the Outlook POST XML body format. Outlook sends the
// request as POST when SCP / OAB lookup fails; the body contains
// an <EMailAddress> element.
func TestOutlookAutodiscoverLowercasePOST(t *testing.T) {
	ah := newAutodiscoverHarness(t)
	defer ah.close()

	body := `<?xml version="1.0" encoding="utf-8"?>
<Autodiscover xmlns="http://schemas.microsoft.com/exchange/autodiscover/outlook/requestschema/2006">
  <Request>
    <EMailAddress>user@example.com</EMailAddress>
    <AcceptableResponseSchema>http://schemas.microsoft.com/exchange/autodiscover/outlook/responseschema/2006a</AcceptableResponseSchema>
  </Request>
</Autodiscover>`
	code, resp := doRequest(t, ah.app, http.MethodPost,
		"/autodiscover/autodiscover.xml", body)
	if code != http.StatusOK {
		t.Fatalf("lowercase POST: want 200, got %d body=%s", code, resp)
	}
	for _, want := range []string{
		"993",
		"587",
		"user@example.com",
		"mail.example.com",
		"IMAP",
		"SMTP",
	} {
		if !strings.Contains(resp, want) {
			t.Errorf("lowercase POST: response missing %q; body=%s", want, resp)
		}
	}
	assertNoPasswordInResponse(t, resp)
}

// TestOutlookAutodiscoverUppercaseGET verifies the uppercase
// path. Some Outlook builds hard-code
// /Autodiscover/Autodiscover.xml so both paths must work.
func TestOutlookAutodiscoverUppercaseGET(t *testing.T) {
	ah := newAutodiscoverHarness(t)
	defer ah.close()

	code, body := doRequest(t, ah.app, http.MethodGet,
		"/Autodiscover/Autodiscover.xml?email=user@example.com", "")
	if code != http.StatusOK {
		t.Fatalf("uppercase GET: want 200, got %d body=%s", code, body)
	}
	for _, want := range []string{"993", "587", "user@example.com"} {
		if !strings.Contains(body, want) {
			t.Errorf("uppercase GET: response missing %q; body=%s", want, body)
		}
	}
	assertNoPasswordInResponse(t, body)
}

// TestOutlookAutodiscoverUppercasePOST mirrors the lowercase
// POST but for the uppercase path.
func TestOutlookAutodiscoverUppercasePOST(t *testing.T) {
	ah := newAutodiscoverHarness(t)
	defer ah.close()

	body := `<Autodiscover><Request><EMailAddress>user@example.com</EMailAddress></Request></Autodiscover>`
	code, resp := doRequest(t, ah.app, http.MethodPost,
		"/Autodiscover/Autodiscover.xml", body)
	if code != http.StatusOK {
		t.Fatalf("uppercase POST: want 200, got %d body=%s", code, resp)
	}
	for _, want := range []string{"993", "587", "user@example.com"} {
		if !strings.Contains(resp, want) {
			t.Errorf("uppercase POST: response missing %q; body=%s", want, resp)
		}
	}
	assertNoPasswordInResponse(t, resp)
}

// TestOutlookAutodiscoverUnknownDomainRejected — an unknown
// domain must return an Outlook error envelope (HTTP 200 + Error
// element). Outlook does NOT treat HTTP 4xx as failure for
// autodiscover; it expects an XML error so the user sees a
// readable message.
func TestOutlookAutodiscoverUnknownDomainRejected(t *testing.T) {
	ah := newAutodiscoverHarness(t)
	defer ah.close()

	code, body := doRequest(t, ah.app, http.MethodGet,
		"/autodiscover/autodiscover.xml?email=user@unknown.test", "")
	if code != http.StatusOK {
		t.Fatalf("unknown domain: want 200 (error envelope), got %d body=%s", code, body)
	}
	for _, want := range []string{"<Error>", "<ErrorCode>", "domain not provisioned"} {
		if !strings.Contains(body, want) {
			t.Errorf("unknown domain: response missing %q; body=%s", want, body)
		}
	}
	assertNoPasswordInResponse(t, body)
}

// TestOutlookAutodiscoverMissingEmail — when no email is
// supplied (no query param, no POST body), Outlook still expects
// a 200 with an error envelope.
func TestOutlookAutodiscoverMissingEmail(t *testing.T) {
	ah := newAutodiscoverHarness(t)
	defer ah.close()

	code, body := doRequest(t, ah.app, http.MethodGet,
		"/autodiscover/autodiscover.xml", "")
	if code != http.StatusOK {
		t.Fatalf("missing email: want 200 (error envelope), got %d body=%s", code, body)
	}
	if !strings.Contains(body, "<Error>") {
		t.Errorf("missing email: response missing <Error>; body=%s", body)
	}
	assertNoPasswordInResponse(t, body)
}

// TestOutlookAutodiscoverResponseParses is a structural check:
// the response must round-trip through encoding/xml so a real
// Outlook client (or anyone using an XML parser) can read it.
func TestOutlookAutodiscoverResponseParses(t *testing.T) {
	ah := newAutodiscoverHarness(t)
	defer ah.close()

	code, body := doRequest(t, ah.app, http.MethodGet,
		"/autodiscover/autodiscover.xml?email=user@example.com", "")
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d", code)
	}
	// We can't strictly parse the spliced envelope with a
	// struct (the IMAP + SMTP <Protocol> blocks share a tag),
	// but the body must at least parse as generic XML.
	dec := xml.NewDecoder(strings.NewReader(body))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("xml parse: %v\nbody=%s", err, body)
		}
	}
}

// TestMozillaAutoconfigWellKnownGET exercises the canonical
// Mozilla path with a known domain and verifies the response
// shape (imap + smtp servers, full email username).
func TestMozillaAutoconfigWellKnownGET(t *testing.T) {
	ah := newAutodiscoverHarness(t)
	defer ah.close()

	code, body := doRequest(t, ah.app, http.MethodGet,
		"/.well-known/autoconfig/mail/config-v1.1.xml?emailaddress=user@example.com", "")
	if code != http.StatusOK {
		t.Fatalf("well-known: want 200, got %d body=%s", code, body)
	}
	for _, want := range []string{
		"<clientConfig",
		"<emailProvider",
		"<incomingServer",
		"<outgoingServer",
		"<hostname>mail.example.com</hostname>",
		"<port>993</port>",
		"<port>587</port>",
		"<type>imap</type>",
		"<type>smtp</type>",
		"<socketType>SSL</socketType>",
		"<socketType>STARTTLS</socketType>",
		"user@example.com",
		"example.com",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("well-known: response missing %q; body=%s", want, body)
		}
	}
	assertNoPasswordInResponse(t, body)
}

// TestMozillaAutoconfigFallbackGET verifies the secondary
// /mail/config-v1.1.xml path. Same body, different URL.
func TestMozillaAutoconfigFallbackGET(t *testing.T) {
	ah := newAutodiscoverHarness(t)
	defer ah.close()

	code, body := doRequest(t, ah.app, http.MethodGet,
		"/mail/config-v1.1.xml?emailaddress=user@example.com", "")
	if code != http.StatusOK {
		t.Fatalf("fallback: want 200, got %d body=%s", code, body)
	}
	for _, want := range []string{"993", "587", "user@example.com"} {
		if !strings.Contains(body, want) {
			t.Errorf("fallback: response missing %q; body=%s", want, body)
		}
	}
	assertNoPasswordInResponse(t, body)
}

// TestMozillaAutoconfigUnknownDomainRejected — unknown domains
// return HTTP 404 (Mozilla's documented behaviour; Thunderbird
// falls back to manual setup).
func TestMozillaAutoconfigUnknownDomainRejected(t *testing.T) {
	ah := newAutodiscoverHarness(t)
	defer ah.close()

	code, body := doRequest(t, ah.app, http.MethodGet,
		"/.well-known/autoconfig/mail/config-v1.1.xml?emailaddress=user@unknown.test", "")
	if code != http.StatusNotFound {
		t.Fatalf("unknown domain: want 404, got %d body=%s", code, body)
	}
	if body != "" {
		t.Errorf("unknown domain: body should be empty, got %q", body)
	}
}

// TestMozillaAutoconfigMissingEmail — same 404 behaviour when
// the email query param is absent.
func TestMozillaAutoconfigMissingEmail(t *testing.T) {
	ah := newAutodiscoverHarness(t)
	defer ah.close()

	code, _ := doRequest(t, ah.app, http.MethodGet,
		"/.well-known/autoconfig/mail/config-v1.1.xml", "")
	if code != http.StatusNotFound {
		t.Fatalf("missing email: want 404, got %d", code)
	}
}

// TestDomainIsProvisioned exercises the gating helper directly
// so we know the per-domain DB lookup matches what the public
// endpoints see.
func TestDomainIsProvisioned(t *testing.T) {
	ah := newAutodiscoverHarness(t)
	defer ah.close()

	ctx := context.Background()
	for _, tc := range []struct {
		domain string
		want   bool
	}{
		{"example.com", true},
		{"EXAMPLE.com", true}, // case-insensitive
		{"unknown.test", false},
		{"", false},
		{"  example.com  ", true}, // trimmed
	} {
		got, err := ah.h.DomainIsProvisionedForTest(ctx, tc.domain)
		if err != nil {
			t.Fatalf("DomainIsProvisioned(%q): %v", tc.domain, err)
		}
		if got != tc.want {
			t.Errorf("DomainIsProvisioned(%q) = %v, want %v", tc.domain, got, tc.want)
		}
	}
}

// TestMailClientSettingsForFallback verifies the host / port
// fallback logic when cfg.CoreMail is zero-valued.
func TestMailClientSettingsForFallback(t *testing.T) {
	ah := newAutodiscoverHarness(t)
	defer ah.close()

	s := ah.h.MailClientSettingsForTest("example.com", "User@Example.COM")
	if s.MailHost != "mail.example.com" {
		t.Errorf("MailHost: got %q want mail.example.com", s.MailHost)
	}
	if s.IMAPPort != 993 {
		t.Errorf("IMAPPort: got %d want 993", s.IMAPPort)
	}
	if s.SMTPPort != 587 {
		t.Errorf("SMTPPort: got %d want 587", s.SMTPPort)
	}
	if s.Domain != "example.com" {
		t.Errorf("Domain: got %q want example.com", s.Domain)
	}
	if s.Email != "user@example.com" {
		t.Errorf("Email: got %q want user@example.com", s.Email)
	}
}

// TestMailClientSettingsForOverride verifies that explicit
// cfg.CoreMail.Hostname / IMAPsPort / SubmissionPort values
// override the defaults.
func TestMailClientSettingsForOverride(t *testing.T) {
	ah := newAutodiscoverHarness(t)
	defer ah.close()

	// The harness leaves cfg.CoreMail zero; we cannot easily
	// mutate it from here. Instead, exercise the helper via a
	// separate harness whose config is overridden.
	logger := zap.NewNop()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(dir, "test2.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.License.OfflineMode = true
	cfg.CoreMail.Hostname = "mx.example.net"
	cfg.CoreMail.IMAPsPort = 1430
	cfg.CoreMail.SubmissionPort = 2587
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	sqlDB, _ := db.DB()
	if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS coremail_domains (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		status TEXT NOT NULL DEFAULT 'active',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		deleted_at DATETIME
	)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO coremail_domains (name, status, created_at, updated_at) VALUES ('example.com', 'active', ?, ?)`, time.Now().UTC(), time.Now().UTC()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	defer sqlDB.Close()
	authn, _ := auth.NewAuthenticator(&cfg.Auth, db, logger)
	ff := license.NewFeatureFlags(logger)
	h := handlers.NewHandler(db, authn, nil, logger, cfg, modules.NewRegistry(logger), ff, nil)

	s := h.MailClientSettingsForTest("example.com", "user@example.com")
	if s.MailHost != "mx.example.net" {
		t.Errorf("MailHost: got %q want mx.example.net", s.MailHost)
	}
	if s.IMAPPort != 1430 {
		t.Errorf("IMAPPort: got %d want 1430", s.IMAPPort)
	}
	if s.SMTPPort != 2587 {
		t.Errorf("SMTPPort: got %d want 2587", s.SMTPPort)
	}
}

// TestExtractEmailFromRequestPost covers the POST XML body
// parsing.
func TestExtractEmailFromRequestPost(t *testing.T) {
	body := `<Autodiscover><Request><EMailAddress>  Foo@Example.COM  </EMailAddress></Request></Autodiscover>`
	app := fiber.New()
	app.Post("/x", func(c fiber.Ctx) error {
		got := handlers.ExtractEmailFromRequestForTest(c)
		if got != "foo@example.com" {
			t.Errorf("got %q want foo@example.com", got)
		}
		return c.SendStatus(200)
	})
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/xml")
	resp, _ := app.Test(req, fiber.TestConfig{Timeout: 2 * time.Second})
	_ = resp.Body.Close()
}

// TestExtractEmailFromRequestGetQueryParam covers the GET query
// param path.
func TestExtractEmailFromRequestGetQueryParam(t *testing.T) {
	for _, key := range []string{"email", "emailaddress", "EmailAddress"} {
		app := fiber.New()
		app.Get("/x", func(c fiber.Ctx) error {
			got := handlers.ExtractEmailFromRequestForTest(c)
			if got != "user@example.com" {
				t.Errorf("key=%s got %q", key, got)
			}
			return c.SendStatus(200)
		})
		req := httptest.NewRequest(http.MethodGet, "/x?"+key+"=User@Example.COM", nil)
		resp, _ := app.Test(req, fiber.TestConfig{Timeout: 2 * time.Second})
		_ = resp.Body.Close()
	}
}

// TestExtractDomainFromEmail covers the helper used to pull the
// domain portion out of a canonical email.
func TestExtractDomainFromEmail(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"user@example.com", "example.com"},
		{"USER@EXAMPLE.COM", "example.com"},
		{"no-at-sign", ""},
		{"trailing@", ""},
		{"", ""},
		{"  user@Example.com  ", "example.com"},
	} {
		got := handlers.ExtractDomainFromEmailForTest(tc.in)
		if got != tc.want {
			t.Errorf("ExtractDomainFromEmail(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestBuildOutlookAutodiscoverErrorXML verifies the error
// envelope shape matches [MS-OXDISCO].
func TestBuildOutlookAutodiscoverErrorXML(t *testing.T) {
	body, err := handlers.BuildOutlookAutodiscoverErrorXMLForTest(600, "test error")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	bs := string(body)
	for _, want := range []string{"<Error", "<ErrorCode>600</ErrorCode>", "<Message>test error</Message>"} {
		if !strings.Contains(bs, want) {
			t.Errorf("missing %q; body=%s", want, bs)
		}
	}
}

// TestBuildMozillaAutoconfigXMLRoundTrip verifies the response
// parses cleanly back into the struct we built it from.
func TestBuildMozillaAutoconfigXMLRoundTrip(t *testing.T) {
	body, err := handlers.BuildMozillaAutoconfigXMLForTest(
		"user@example.com",
		handlers.MailClientSettingsForTest("example.com", "user@example.com"),
	)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Round-trip via encoding/xml.
	type roundTrip struct {
		XMLName     xml.Name `xml:"clientConfig"`
		EmailProvider struct {
			ID       string `xml:"id,attr"`
			Domains  string `xml:"domains>domain"`
			Incoming struct {
				Type     string `xml:"type"`
				Hostname string `xml:"hostname"`
				Port     int    `xml:"port"`
				Username string `xml:"username"`
			} `xml:"incomingServer"`
			Outgoing struct {
				Type     string `xml:"type"`
				Hostname string `xml:"hostname"`
				Port     int    `xml:"port"`
				Username string `xml:"username"`
			} `xml:"outgoingServer"`
		} `xml:"emailProvider"`
	}
	var rt roundTrip
	if err := xml.Unmarshal(body, &rt); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, body)
	}
	if rt.EmailProvider.Incoming.Type != "imap" {
		t.Errorf("incoming.type: %q", rt.EmailProvider.Incoming.Type)
	}
	if rt.EmailProvider.Incoming.Port != 993 {
		t.Errorf("incoming.port: %d", rt.EmailProvider.Incoming.Port)
	}
	if rt.EmailProvider.Outgoing.Type != "smtp" {
		t.Errorf("outgoing.type: %q", rt.EmailProvider.Outgoing.Type)
	}
	if rt.EmailProvider.Outgoing.Port != 587 {
		t.Errorf("outgoing.port: %d", rt.EmailProvider.Outgoing.Port)
	}
	if rt.EmailProvider.Incoming.Username != "user@example.com" {
		t.Errorf("incoming.username: %q", rt.EmailProvider.Incoming.Username)
	}
	if rt.EmailProvider.Outgoing.Username != "user@example.com" {
		t.Errorf("outgoing.username: %q", rt.EmailProvider.Outgoing.Username)
	}
	if rt.EmailProvider.Domains != "example.com" {
		t.Errorf("domains: %q", rt.EmailProvider.Domains)
	}
}

// ============================================================
// Soft-deleted domain tests (BLOCKER 1)
// ------------------------------------------------------------
// The implementation filters `coremail_domains` by
// `deleted_at IS NULL`. These tests prove that filter holds
// under both the Outlook and Thunderbird code paths.
// ============================================================

// newSoftDeletedDomainHarness builds a harness with TWO
// domains in coremail_domains:
//   - example.com   active   (deleted_at IS NULL)
//   - deleted.com   soft-deleted (deleted_at IS NOT NULL)
//
// All four public endpoints must treat deleted.com as
// "not provisioned" exactly the same way they treat a domain
// that was never inserted.
func newSoftDeletedDomainHarness(t *testing.T) *autodiscoverHarness {
	t.Helper()
	logger := zap.NewNop()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(dir, "test.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.License.OfflineMode = true
	cfg.CoreMail.Hostname = ""
	cfg.CoreMail.IMAPsPort = 0
	cfg.CoreMail.SubmissionPort = 0

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	sqlDB, _ := db.DB()
	if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS coremail_domains (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		tenant_id INTEGER NOT NULL DEFAULT 1,
		reseller_id INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'active',
		plan TEXT NOT NULL DEFAULT 'smb',
		description TEXT NOT NULL DEFAULT '',
		max_mailboxes INTEGER NOT NULL DEFAULT 100,
		max_aliases INTEGER NOT NULL DEFAULT 50,
		max_quota_mb INTEGER NOT NULL DEFAULT 1024,
		dkim_enabled INTEGER NOT NULL DEFAULT 0,
		dkim_selector TEXT NOT NULL DEFAULT '',
		dmarc_enabled INTEGER NOT NULL DEFAULT 0,
		mtasts_enabled INTEGER NOT NULL DEFAULT 0,
		catchall_address TEXT NOT NULL DEFAULT '',
		abuse_contact TEXT NOT NULL DEFAULT '',
		labels TEXT NOT NULL DEFAULT '',
		mailbox_count INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		deleted_at DATETIME
	)`); err != nil {
		t.Fatalf("create coremail_domains: %v", err)
	}
	now := time.Now().UTC()
	// Active domain.
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_domains (name, status, plan, description, max_mailboxes, max_aliases, max_quota_mb,
		   dkim_enabled, dkim_selector, dmarc_enabled, mtasts_enabled, catchall_address, abuse_contact, labels,
		   mailbox_count, created_at, updated_at)
		 VALUES ('example.com', 'active', 'smb', '', 100, 50, 1024, 0, '', 0, 0, '', '', '', 0, ?, ?)`,
		now, now); err != nil {
		t.Fatalf("seed active domain: %v", err)
	}
	// Soft-deleted domain — same shape as the active row
	// except deleted_at is set.
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_domains (name, status, plan, description, max_mailboxes, max_aliases, max_quota_mb,
		   dkim_enabled, dkim_selector, dmarc_enabled, mtasts_enabled, catchall_address, abuse_contact, labels,
		   mailbox_count, created_at, updated_at, deleted_at)
		 VALUES ('deleted.com', 'deleted', 'smb', '', 100, 50, 1024, 0, '', 0, 0, '', '', '', 0, ?, ?, ?)`,
		now, now, now); err != nil {
		t.Fatalf("seed soft-deleted domain: %v", err)
	}

	authn, _ := auth.NewAuthenticator(&cfg.Auth, db, logger)
	ff := license.NewFeatureFlags(logger)
	h := handlers.NewHandler(db, authn, nil, logger, cfg, modules.NewRegistry(logger), ff, nil)

	app := fiber.New(fiber.Config{ReadBufferSize: 128 * 1024})
	app.Get("/autodiscover/autodiscover.xml", h.OutlookAutodiscoverXML)
	app.Post("/autodiscover/autodiscover.xml", h.OutlookAutodiscoverXML)
	app.Get("/Autodiscover/Autodiscover.xml", h.OutlookAutodiscoverXMLUpper)
	app.Post("/Autodiscover/Autodiscover.xml", h.OutlookAutodiscoverXMLUpper)
	app.Get("/.well-known/autoconfig/mail/config-v1.1.xml", h.MozillaAutoconfig)
	app.Get("/mail/config-v1.1.xml", h.MozillaAutoconfigFallback)

	return &autodiscoverHarness{app: app, h: h, db: db, dir: dir}
}

// TestOutlookAutodiscoverSoftDeletedDomainRejectedGET — a
// GET against an email whose domain is soft-deleted in
// coremail_domains must be rejected safely: HTTP 200 with the
// Outlook error envelope, NOT a 200 with valid settings.
func TestOutlookAutodiscoverSoftDeletedDomainRejectedGET(t *testing.T) {
	ah := newSoftDeletedDomainHarness(t)
	defer ah.close()

	code, body := doRequest(t, ah.app, http.MethodGet,
		"/autodiscover/autodiscover.xml?email=user@deleted.com", "")
	if code != http.StatusOK {
		t.Fatalf("soft-deleted GET: want 200 (error envelope), got %d body=%s", code, body)
	}
	for _, want := range []string{
		"<Error>",
		"<ErrorCode>600</ErrorCode>",
		"domain not provisioned: deleted.com",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("soft-deleted GET: response missing %q; body=%s", want, body)
		}
	}
	// Sanity: must NOT serve valid settings — there should be
	// no IMAP/SMTP protocol block advertising ports.
	if strings.Contains(body, "<Type>IMAP</Type>") {
		t.Errorf("soft-deleted GET: response must NOT serve IMAP settings; body=%s", body)
	}
	if strings.Contains(body, "<Type>SMTP</Type>") {
		t.Errorf("soft-deleted GET: response must NOT serve SMTP settings; body=%s", body)
	}
	assertNoPasswordInResponse(t, body)
}

// TestOutlookAutodiscoverSoftDeletedDomainRejectedPOST —
// mirror of the GET test but for the POST path. Outlook
// posts a real XML envelope; the body parser still must
// reject the domain after the soft-delete filter.
func TestOutlookAutodiscoverSoftDeletedDomainRejectedPOST(t *testing.T) {
	ah := newSoftDeletedDomainHarness(t)
	defer ah.close()

	body := `<?xml version="1.0" encoding="utf-8"?>
<Autodiscover xmlns="http://schemas.microsoft.com/exchange/autodiscover/outlook/requestschema/2006">
  <Request>
    <EMailAddress>user@deleted.com</EMailAddress>
    <AcceptableResponseSchema>http://schemas.microsoft.com/exchange/autodiscover/outlook/responseschema/2006a</AcceptableResponseSchema>
  </Request>
</Autodiscover>`
	code, resp := doRequest(t, ah.app, http.MethodPost,
		"/autodiscover/autodiscover.xml", body)
	if code != http.StatusOK {
		t.Fatalf("soft-deleted POST: want 200 (error envelope), got %d body=%s", code, resp)
	}
	for _, want := range []string{"<Error>", "<ErrorCode>600</ErrorCode>", "domain not provisioned: deleted.com"} {
		if !strings.Contains(resp, want) {
			t.Errorf("soft-deleted POST: response missing %q; body=%s", want, resp)
		}
	}
	if strings.Contains(resp, "<Type>IMAP</Type>") {
		t.Errorf("soft-deleted POST: response must NOT serve IMAP settings; body=%s", resp)
	}
	if strings.Contains(resp, "<Type>SMTP</Type>") {
		t.Errorf("soft-deleted POST: response must NOT serve SMTP settings; body=%s", resp)
	}
	assertNoPasswordInResponse(t, resp)
}

// TestOutlookAutodiscoverUppercaseSoftDeletedDomainRejected —
// the uppercase route goes through the same helper, so the
// soft-delete filter must also hold there.
func TestOutlookAutodiscoverUppercaseSoftDeletedDomainRejected(t *testing.T) {
	ah := newSoftDeletedDomainHarness(t)
	defer ah.close()

	code, body := doRequest(t, ah.app, http.MethodGet,
		"/Autodiscover/Autodiscover.xml?email=USER@DELETED.COM", "")
	if code != http.StatusOK {
		t.Fatalf("uppercase soft-deleted: want 200 (error envelope), got %d body=%s", code, body)
	}
	if !strings.Contains(body, "domain not provisioned: deleted.com") {
		t.Errorf("uppercase soft-deleted: missing rejection; body=%s", body)
	}
	assertNoPasswordInResponse(t, body)
}

// TestMozillaAutoconfigSoftDeletedDomainRejected — the
// Thunderbird / Mozilla autoconfig path returns HTTP 404 for
// non-provisioned domains. Soft-deleted domains are not
// provisioned, so the same 404 must apply.
func TestMozillaAutoconfigSoftDeletedDomainRejected(t *testing.T) {
	ah := newSoftDeletedDomainHarness(t)
	defer ah.close()

	code, body := doRequest(t, ah.app, http.MethodGet,
		"/.well-known/autoconfig/mail/config-v1.1.xml?emailaddress=user@deleted.com", "")
	if code != http.StatusNotFound {
		t.Fatalf("soft-deleted autoconfig: want 404, got %d body=%s", code, body)
	}
	if body != "" {
		t.Errorf("soft-deleted autoconfig: body should be empty, got %q", body)
	}
}

// TestMozillaAutoconfigFallbackSoftDeletedDomainRejected —
// the secondary /mail/config-v1.1.xml path must also reject
// soft-deleted domains.
func TestMozillaAutoconfigFallbackSoftDeletedDomainRejected(t *testing.T) {
	ah := newSoftDeletedDomainHarness(t)
	defer ah.close()

	code, body := doRequest(t, ah.app, http.MethodGet,
		"/mail/config-v1.1.xml?emailaddress=user@deleted.com", "")
	if code != http.StatusNotFound {
		t.Fatalf("soft-deleted fallback: want 404, got %d body=%s", code, body)
	}
	if body != "" {
		t.Errorf("soft-deleted fallback: body should be empty, got %q", body)
	}
}

// TestDomainIsProvisionedExcludesSoftDeleted — direct unit
// test on the gating helper. Confirms that the soft-delete
// filter is the SINGLE source of truth for "is this domain
// yours?" across all four endpoints.
func TestDomainIsProvisionedExcludesSoftDeleted(t *testing.T) {
	ah := newSoftDeletedDomainHarness(t)
	defer ah.close()

	ctx := context.Background()
	for _, tc := range []struct {
		domain string
		want   bool
	}{
		{"example.com", true}, // active
		{"deleted.com", false}, // soft-deleted must be filtered
		{"DELETED.com", false}, // case-insensitive AND soft-deleted
		{"deleted.com  ", false}, // trimmed AND soft-deleted
		{"unknown.test", false}, // not present at all
	} {
		got, err := ah.h.DomainIsProvisionedForTest(ctx, tc.domain)
		if err != nil {
			t.Fatalf("DomainIsProvisioned(%q): %v", tc.domain, err)
		}
		if got != tc.want {
			t.Errorf("DomainIsProvisioned(%q) = %v, want %v (soft-delete filter must exclude deleted.com)", tc.domain, got, tc.want)
		}
	}
}

// ============================================================
// Per-endpoint POST body size cap (BLOCKER 2)
// ------------------------------------------------------------
// The Outlook Autodiscover endpoint is public and
// unauthenticated. We cap POST bodies at 64 KiB and reject
// oversized requests with HTTP 413 + a safe XML error
// envelope BEFORE the body is regex-scanned or reflected.
// ============================================================

// TestAutodiscoverPostBodyLimitConstant exposes the limit
// the production handler enforces. If this ever changes,
// every test below automatically picks up the new value.
func TestAutodiscoverPostBodyLimitConstant(t *testing.T) {
	limit := handlers.AutodiscoverPostBodyLimitForTest()
	if limit != 64*1024 {
		t.Errorf("POST body limit = %d, want 64 KiB (65536)", limit)
	}
}

// TestEnforceAutodiscoverPostBodyLimitGETPassesThrough —
// the limit is POST-only; GET requests must never trigger
// it regardless of how big the (empty) body is.
func TestEnforceAutodiscoverPostBodyLimitGETPassesThrough(t *testing.T) {
	app := fiber.New()
	app.Get("/x", func(c fiber.Ctx) error {
		_, oversized := handlers.EnforceAutodiscoverPostBodyLimitForTest(c)
		if oversized {
			t.Errorf("GET must not trigger POST body limit; got oversized=true")
		}
		return c.SendStatus(204)
	})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if _, err := app.Test(req, fiber.TestConfig{Timeout: 2 * time.Second}); err != nil {
		t.Fatalf("app.Test: %v", err)
	}
}

// TestEnforceAutodiscoverPostBodyLimitRejectsOversizedPOST —
// helper-level check that the production helper returns
// (envelope, true) when given a Ctx whose body exceeds the
// limit. We construct the Ctx by sending a real POST through
// a minimal Fiber app with a generous ReadBufferSize so the
// body fits on the wire.
func TestEnforceAutodiscoverPostBodyLimitRejectsOversizedPOST(t *testing.T) {
	limit := handlers.AutodiscoverPostBodyLimitForTest()
	app := fiber.New(fiber.Config{ReadBufferSize: 128 * 1024})
	app.Post("/x", func(c fiber.Ctx) error {
		body, oversized := handlers.EnforceAutodiscoverPostBodyLimitForTest(c)
		if !oversized {
			t.Errorf("expected oversized=true (body len=%d, limit=%d)", len(c.Body()), limit)
		}
		if !strings.Contains(string(body), "<Error>") {
			t.Errorf("expected XML error envelope, got %q", string(body))
		}
		if !strings.Contains(string(body), "<ErrorCode>413</ErrorCode>") {
			t.Errorf("expected ErrorCode 413, got %q", string(body))
		}
		return c.SendStatus(204)
	})
	// Body length is exactly limit+1 so the production
	// Content-Length auto-set will also exceed the limit.
	// Padding matches Content-Length, so Fiber's test
	// transport does not EOF on a short read.
	big := strings.Repeat("A", limit+1)
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(big))
	req.Header.Set("Content-Type", "application/xml")
	if _, err := app.Test(req, fiber.TestConfig{Timeout: 2 * time.Second}); err != nil {
		t.Fatalf("app.Test: %v", err)
	}
}

// TestOutlookAutodiscoverOversizedPOSTRejected — POST with
// a body above the per-endpoint cap returns HTTP 413 + XML
// error envelope. The regex MUST NOT run on the body, and
// no part of the body may be reflected.
func TestOutlookAutodiscoverOversizedPOSTRejected(t *testing.T) {
	ah := newAutodiscoverHarness(t)
	defer ah.close()

	limit := handlers.AutodiscoverPostBodyLimitForTest()
	// Build a body that LOOKS like a real Autodiscover POST
	// envelope but is padded out past the limit. We include
	// a recognisable marker string so we can assert the
	// marker is NOT reflected back to us.
	const marker = "PWNED_BY_AUTODISCOVER_TEST_MARKER"
	padding := strings.Repeat("X", limit) // > 64 KiB
	body := `<?xml version="1.0" encoding="utf-8"?>
<Autodiscover xmlns="http://schemas.microsoft.com/exchange/autodiscover/outlook/requestschema/2006">
  <Request>
    <EMailAddress>user@example.com</EMailAddress>
    <PADDING>` + padding + `</PADDING>
    <MARKER>` + marker + `</MARKER>
  </Request>
</Autodiscover>`

	if len(body) <= limit {
		t.Fatalf("test body must exceed limit; len=%d limit=%d", len(body), limit)
	}

	code, resp, headers := doRequestWithHeaders(t, ah.app, http.MethodPost,
		"/autodiscover/autodiscover.xml", body)
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized POST: want 413, got %d body=%s", code, resp)
	}
	for _, want := range []string{
		"<Error>",
		"<ErrorCode>413</ErrorCode>",
		"request body too large",
	} {
		if !strings.Contains(resp, want) {
			t.Errorf("oversized POST: response missing %q; body=%s", want, resp)
		}
	}
	// Content-Type header is set so a well-behaved client
	// (Outlook, debug tooling) parses the body as XML.
	if got := headers.Get("Content-Type"); !strings.HasPrefix(got, "text/xml") {
		t.Errorf("oversized POST: Content-Type header = %q, want text/xml prefix", got)
	}
	// No part of the offending body may be reflected.
	if strings.Contains(resp, marker) {
		t.Errorf("oversized POST: response MUST NOT reflect marker %q; body=%s", marker, resp)
	}
	if strings.Contains(resp, "PWNED_BY_AUTODISCOVER_TEST_MARKER") {
		t.Errorf("oversized POST: response leaked marker; body=%s", resp)
	}
	// The response must not advertise valid settings either.
	if strings.Contains(resp, "<Type>IMAP</Type>") {
		t.Errorf("oversized POST: response must NOT serve IMAP settings; body=%s", resp)
	}
	if strings.Contains(resp, "<Type>SMTP</Type>") {
		t.Errorf("oversized POST: response must NOT serve SMTP settings; body=%s", resp)
	}
	assertNoPasswordInResponse(t, resp)
}

// TestOutlookAutodiscoverOversizedPOSTUppercaseRejected —
// same as above but on the uppercase route, since both
// routes go through serveOutlookAutodiscover.
func TestOutlookAutodiscoverOversizedPOSTUppercaseRejected(t *testing.T) {
	ah := newAutodiscoverHarness(t)
	defer ah.close()

	limit := handlers.AutodiscoverPostBodyLimitForTest()
	body := strings.Repeat("X", limit+1) // minimal payload > limit
	code, resp := doRequest(t, ah.app, http.MethodPost,
		"/Autodiscover/Autodiscover.xml", body)
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized uppercase POST: want 413, got %d body=%s", code, resp)
	}
	if !strings.Contains(resp, "<Error>") {
		t.Errorf("oversized uppercase POST: missing <Error>; body=%s", resp)
	}
	assertNoPasswordInResponse(t, resp)
}

// TestOutlookAutodiscoverNormalPOSTStillWorks — sanity:
// the body limit must NOT reject a normal-sized POST. We
// already have TestOutlookAutodiscoverLowercasePOST but
// adding this here keeps the body-limit tests grouped.
func TestOutlookAutodiscoverNormalPOSTStillWorks(t *testing.T) {
	ah := newAutodiscoverHarness(t)
	defer ah.close()

	body := `<?xml version="1.0" encoding="utf-8"?>
<Autodiscover xmlns="http://schemas.microsoft.com/exchange/autodiscover/outlook/requestschema/2006">
  <Request>
    <EMailAddress>user@example.com</EMailAddress>
  </Request>
</Autodiscover>`
	code, resp := doRequest(t, ah.app, http.MethodPost,
		"/autodiscover/autodiscover.xml", body)
	if code != http.StatusOK {
		t.Fatalf("normal POST: want 200, got %d body=%s", code, resp)
	}
	if !strings.Contains(resp, "993") || !strings.Contains(resp, "587") {
		t.Errorf("normal POST: missing IMAP/SMTP ports; body=%s", resp)
	}
}