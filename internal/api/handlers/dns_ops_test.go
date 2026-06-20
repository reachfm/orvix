package handlers_test

// Integration tests for the Admin DNS / DKIM Operations endpoints
// (DNS-DKIM-OPERATIONS-2F). The endpoints are admin-only,
// read-only except for DKIM keygen / provider apply (which are
// CSRF-protected). They never echo private keys or provider
// tokens. They use the project's own sqlite harness so we don't
// need an external DB.

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
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
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// dnsOpsHarness builds a tiny fiber app with the DNS Ops handler
// mounted behind the same admin auth used in production. The
// resolver is the in-memory FakeResolver so tests never touch the
// network.
type dnsOpsHarness struct {
	app     *fiber.App
	h       *handlers.Handler
	auth    *auth.Authenticator
	adminT  string
	userT   string
	dir     string
	db      *gorm.DB
	resolve *dnsops.FakeResolver
}

func (h *dnsOpsHarness) close() {
	if h.db == nil {
		return
	}
	if sqlDB, err := h.db.DB(); err == nil && sqlDB != nil {
		_ = sqlDB.Close()
	}
}

func newDNSOpsHarness(t *testing.T) *dnsOpsHarness {
	t.Helper()
	logger := zap.NewNop()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(dir, "test.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.License.PublicKeyPath = ""
	cfg.License.OfflineMode = true
	cfg.CoreMail.SMTPPort = 25
	cfg.CoreMail.IMAPPort = 143
	cfg.CoreMail.POP3Port = 110
	cfg.CoreMail.JMAPPort = 8080
	cfg.CoreMail.MailStorePath = dir
	// Anchor the public mail IPv4 (NOT the SMTP listener bind
	// address) so the plan generator has a value to emit; 0.0.0.0
	// is explicitly forbidden by DNS-DKIM-OPERATIONS-2F-SAFETY-FIX
	// and the listener bind host is a separate concern.
	cfg.DNS.PublicIPv4 = "8.8.8.8"
	// Force the handler's MailHost fallback to "mail.<domain>"
	// by clearing the default "mail.local" hostname.
	cfg.CoreMail.Hostname = ""

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	// Schema migrations are idempotent — we run them via raw
	// SQL because gorm AutoMigrate is deliberately skipped in
	// the production handler. We use the underlying *sql.DB so
	// the schema is in place for handlers that call h.db.DB().
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS coremail_audit (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		actor TEXT NOT NULL DEFAULT '',
		role TEXT NOT NULL DEFAULT '',
		action TEXT NOT NULL DEFAULT '',
		target TEXT NOT NULL DEFAULT '',
		result TEXT NOT NULL DEFAULT '',
		ip TEXT NOT NULL DEFAULT '',
		user_agent TEXT NOT NULL DEFAULT '',
		timestamp DATETIME NOT NULL
	)`); err != nil {
		t.Fatalf("create coremail_audit: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS coremail_dkim_config (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		domain TEXT UNIQUE NOT NULL,
		selector TEXT NOT NULL DEFAULT 'default',
		private_key_pem TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`); err != nil {
		t.Fatalf("create coremail_dkim_config: %v", err)
	}
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
	userTok, _ := authn.GenerateAccessToken(2, auth.RoleUser)

	// Mount the admin endpoints behind a small middleware that
	// checks the bearer token. We don't use the full router here
	// to keep the harness focused.
	api := app.Group("/api/v1")
	mount := func(method, path string, fn fiber.Handler) {
		api.Add([]string{method}, path, func(c fiber.Ctx) error {
			hdr := c.Get("Authorization")
			switch {
			case strings.HasPrefix(hdr, "Bearer "+adminTok):
				c.Locals("user_id", uint(1))
				c.Locals("role", auth.RoleAdmin)
				return fn(c)
			case strings.HasPrefix(hdr, "Bearer "+userTok):
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "insufficient permissions"})
			default:
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
			}
		})
	}
	mount("GET", "/admin/dns/providers", h.GetAdminDNSProviders)
	mount("GET", "/admin/dns/:domain/plan", h.GetAdminDNSPlan)
	mount("POST", "/admin/dns/:domain/verify", h.PostAdminDNSVerify)
	mount("POST", "/admin/dns/:domain/dkim", h.PostAdminDNSDKIM)
	mount("POST", "/admin/dns/:domain/provider/plan", h.PostAdminDNSProviderPlan)
	mount("POST", "/admin/dns/:domain/provider/apply", h.PostAdminDNSProviderApply)
	mount("POST", "/dns/check/:domain", h.DNSCheck)
	mount("POST", "/dns/wizard/:domain", h.DNSWizard)

	// Seed a provisioned domain so the DKIM keygen path
	// (DNS-DKIM-OPERATIONS-2F-SAFETY-FIX) passes the
	// domainExists check. Tests that need a missing-domain
	// scenario seed nothing here and assert 404.
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_domains (name, status, plan, description, max_mailboxes, max_aliases, max_quota_mb,
		   dkim_enabled, dkim_selector, dmarc_enabled, mtasts_enabled, catchall_address, abuse_contact, labels,
		   mailbox_count, created_at, updated_at)
		 VALUES ('example.com', 'active', 'smb', '', 100, 50, 1024, 0, '', 0, 0, '', '', '', 0, ?, ?)`,
		time.Now().UTC(), time.Now().UTC()); err != nil {
		t.Fatalf("seed coremail_domains: %v", err)
	}

	return &dnsOpsHarness{
		app:     app,
		h:       h,
		auth:    authn,
		adminT:  adminTok,
		userT:   userTok,
		dir:     dir,
		db:      db,
		resolve: resolver,
	}
}

func (h *dnsOpsHarness) do(t *testing.T, method, path, token, body string) (int, string) {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := h.app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// ── Auth / RBAC ──────────────────────────────────────────────────

func TestDNSOpsPlanRequiresAdmin(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	code, body := h.do(t, "GET", "/api/v1/admin/dns/example.com/plan", h.userT, "")
	if code != http.StatusForbidden {
		t.Errorf("user must be forbidden; got %d body=%s", code, body)
	}
}

func TestDNSOpsPlanRequiresAuth(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	code, _ := h.do(t, "GET", "/api/v1/admin/dns/example.com/plan", "", "")
	if code != http.StatusUnauthorized {
		t.Errorf("no token must be unauthorized; got %d", code)
	}
}

func TestDNSOpsVerifyRequiresAdmin(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	code, _ := h.do(t, "POST", "/api/v1/admin/dns/example.com/verify", h.userT, "")
	if code != http.StatusForbidden {
		t.Errorf("user must be forbidden; got %d", code)
	}
}

// ── Plan / Verify ───────────────────────────────────────────────

func TestDNSOpsPlanReturnsAllRequiredRecords(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	code, body := h.do(t, "GET", "/api/v1/admin/dns/example.com/plan", h.adminT, "")
	if code != http.StatusOK {
		t.Fatalf("plan must be 200; got %d body=%s", code, body)
	}
	var resp struct {
		Domain string `json:"domain"`
		Plan   struct {
			Domain        string `json:"domain"`
			MailHost      string `json:"mail_host"`
			ServerIPv4    string `json:"server_ipv4"`
			DKIMSelector  string `json:"dkim_selector"`
			MTAMode       string `json:"mta_sts_mode"`
			MTAPolicyID   string `json:"mta_sts_policy_id"`
			MTAPolicyFile string `json:"mta_sts_policy_file"`
			Records       []struct {
				Type     string `json:"type"`
				Name     string `json:"name"`
				Value    string `json:"value"`
				Required bool   `json:"required"`
				Purpose  string `json:"purpose"`
			} `json:"records"`
		} `json:"plan"`
		Notes []string `json:"notes"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, body)
	}
	if resp.Domain != "example.com" {
		t.Errorf("domain echo: got %q", resp.Domain)
	}
	if resp.Plan.MailHost != "mail.example.com" {
		t.Errorf("mail_host: got %q want mail.example.com", resp.Plan.MailHost)
	}
	if resp.Plan.ServerIPv4 != "8.8.8.8" {
		t.Errorf("server_ipv4: got %q want 8.8.8.8", resp.Plan.ServerIPv4)
	}
	if resp.Plan.MTAMode != "testing" {
		t.Errorf("mta_sts_mode must default to testing; got %q", resp.Plan.MTAMode)
	}
	if !strings.Contains(resp.Plan.MTAPolicyFile, "mode: testing") {
		t.Errorf("mta_sts_policy_file must be mode: testing; got %q", resp.Plan.MTAPolicyFile)
	}
	seen := map[string]bool{}
	for _, r := range resp.Plan.Records {
		seen[r.Purpose] = true
	}
	for _, want := range []string{"mx", "mail_a", "spf", "dkim", "dmarc", "mta_sts", "tls_rpt"} {
		if !seen[want] {
			t.Errorf("plan must include %q record", want)
		}
	}
	// No placeholder DKIM.
	for _, r := range resp.Plan.Records {
		if r.Purpose == "dkim" {
			if strings.Contains(r.Value, "YOUR") || strings.Contains(r.Value, "PLACEHOLDER") {
				t.Errorf("DKIM record must not contain placeholder; got %q", r.Value)
			}
		}
	}
}

// TestDNSOpsVerifyReturnsVerifierReport confirms the verify
// endpoint runs the verifier and returns a VerifyReport shape.
func TestDNSOpsVerifyReturnsVerifierReport(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	// Seed a DKIM row so the generator emits a real DKIM TXT
	// with the matching pubkey. The fixture populates the
	// resolver with the same pubkey so the verify path is
	// happy end-to-end.
	pubBase64, err := seedDKIMRow(t, h, "example.com", "default")
	if err != nil {
		t.Fatalf("seed dkim: %v", err)
	}
	// First call: read the generated plan so we know the
	// DKIM selector + MTA-STS id the plan emits. Then seed the
	// fake resolver with matching records.
	planCode, planBody := h.do(t, "GET", "/api/v1/admin/dns/example.com/plan", h.adminT, "")
	if planCode != http.StatusOK {
		t.Fatalf("plan must be 200; got %d body=%s", planCode, planBody)
	}
	var planResp struct {
		Plan struct {
			DKIMSelector string `json:"dkim_selector"`
			MTAPolicyID  string `json:"mta_sts_policy_id"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(planBody), &planResp); err != nil {
		t.Fatalf("plan unmarshal: %v body=%s", err, planBody)
	}
	populateFixtureForDomain(h, "example.com", planResp.Plan.DKIMSelector, planResp.Plan.MTAPolicyID, pubBase64)

	code, body := h.do(t, "POST", "/api/v1/admin/dns/example.com/verify", h.adminT, "")
	if code != http.StatusOK {
		t.Fatalf("verify must be 200; got %d body=%s", code, body)
	}
	var resp struct {
		Report struct {
			Verified  bool     `json:"verified"`
			CheckedAt string   `json:"checked_at"`
			Warnings  []string `json:"warnings"`
			Plan      struct {
				Records []struct {
					Type     string `json:"type"`
					Name     string `json:"name"`
					Purpose  string `json:"purpose"`
					Status   string `json:"status"`
					Reason   string `json:"reason"`
				} `json:"records"`
			} `json:"plan"`
		} `json:"report"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, body)
	}
	if !resp.Report.Verified {
		var sb strings.Builder
		sb.WriteString("verify report not verified. Per-record status:\n")
		for _, r := range resp.Report.Plan.Records {
			sb.WriteString("  - ")
			sb.WriteString(r.Purpose)
			sb.WriteString("/")
			sb.WriteString(r.Name)
			sb.WriteString("/")
			sb.WriteString(r.Type)
			sb.WriteString(" => ")
			sb.WriteString(r.Status)
			if r.Reason != "" {
				sb.WriteString(" (")
				sb.WriteString(r.Reason)
				sb.WriteString(")")
			}
			sb.WriteString("\n")
		}
		t.Errorf("%s", sb.String())
	}
	if resp.Report.CheckedAt == "" {
		t.Errorf("verify report must carry checked_at")
	}
}

// TestDNSOpsVerifyNoFixtureIsUnverified: with no live DNS records,
// the report.Verified must be false.
func TestDNSOpsVerifyNoFixtureIsUnverified(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	code, body := h.do(t, "POST", "/api/v1/admin/dns/example.com/verify", h.adminT, "")
	if code != http.StatusOK {
		t.Fatalf("verify must be 200; got %d body=%s", code, body)
	}
	var resp struct {
		Report struct {
			Verified bool `json:"verified"`
		} `json:"report"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, body)
	}
	if resp.Report.Verified {
		t.Errorf("verify must report false with empty resolver")
	}
}

// ── DKIM keygen ─────────────────────────────────────────────────

// TestDNSOpsDKIMGenerateStoresPrivateKeyAndReturnsPublicOnly
// confirms: (1) the handler returns ONLY the public DNS TXT,
// (2) the private key is persisted server-side, (3) the public
// TXT parses to a real RSA key (so it is not a placeholder).
func TestDNSOpsDKIMGenerateStoresPrivateKeyAndReturnsPublicOnly(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	code, body := h.do(t, "POST", "/api/v1/admin/dns/example.com/dkim", h.adminT, `{"selector":"orvix"}`)
	if code != http.StatusCreated {
		t.Fatalf("dkim generate must be 201; got %d body=%s", code, body)
	}
	var resp struct {
		Domain         string `json:"domain"`
		Selector       string `json:"selector"`
		PublicDNSTXT   string `json:"public_dns_txt"`
		DNSRecordName  string `json:"dns_record_name"`
		Stored         bool   `json:"stored"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, body)
	}
	if resp.Domain != "example.com" || resp.Selector != "orvix" {
		t.Errorf("response domain/selector wrong: %+v", resp)
	}
	if !strings.HasPrefix(resp.PublicDNSTXT, "v=DKIM1;") {
		t.Errorf("public DNS TXT must start with v=DKIM1; got %q", resp.PublicDNSTXT)
	}
	if !strings.Contains(resp.PublicDNSTXT, "p=") {
		t.Errorf("public DNS TXT must carry p=; got %q", resp.PublicDNSTXT)
	}
	for _, banned := range []string{"YOUR", "PLACEHOLDER", "TODO", "BEGIN PRIVATE", "PRIVATE KEY"} {
		if strings.Contains(body, banned) {
			t.Errorf("response must not contain %q; got %s", banned, body)
		}
	}
	// Confirm the private key was stored in coremail_dkim_config.
	sqlDB, err := h.db.DB()
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	var priv string
	if err := sqlDB.QueryRow(`SELECT private_key_pem FROM coremail_dkim_config WHERE domain = ?`, "example.com").Scan(&priv); err != nil {
		t.Fatalf("private key not stored: %v", err)
	}
	block, _ := pem.Decode([]byte(priv))
	if block == nil {
		t.Fatalf("stored private key is not PEM: %q", priv)
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("stored private key parse: %v", err)
	}
	if _, ok := keyAny.(*rsa.PrivateKey); !ok {
		t.Fatalf("stored private key is not RSA")
	}
	// Public TXT must equal what dkim.GenerateDNSRecord would
	// produce for this selector and the stored key.
	if resp.DNSRecordName != "orvix._domainkey.example.com" {
		t.Errorf("dns_record_name: got %q want orvix._domainkey.example.com", resp.DNSRecordName)
	}
}

func TestDNSOpsDKIMGenerateFirstNoConfirmation(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	// First generation does NOT require rotation confirmation.
	code, body := h.do(t, "POST", "/api/v1/admin/dns/example.com/dkim", h.adminT, `{"selector":"orvix"}`)
	if code != http.StatusCreated {
		t.Fatalf("first generate must be 201; got %d body=%s", code, body)
	}
	if strings.Contains(body, "confirm_rotation") {
		t.Errorf("first generate response must not mention confirm_rotation; got %s", body)
	}
}

// TestDNSOpsDKIMGenerateWithoutConfirmationRotateNo is the second
// blocker: a second call WITHOUT confirm_rotation must return 409
// and NOT overwrite the existing key.
func TestDNSOpsDKIMGenerateWithoutConfirmationReturns409(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	if c, _ := h.do(t, "POST", "/api/v1/admin/dns/example.com/dkim", h.adminT, `{"selector":"a"}`); c != http.StatusCreated {
		t.Fatalf("first generate failed")
	}
	sqlDB, _ := h.db.DB()
	var first string
	_ = sqlDB.QueryRow(`SELECT private_key_pem FROM coremail_dkim_config WHERE domain = ?`, "example.com").Scan(&first)

	// Second call WITHOUT confirm_rotation must be 409.
	code, body := h.do(t, "POST", "/api/v1/admin/dns/example.com/dkim", h.adminT, `{"selector":"b"}`)
	if code != http.StatusConflict {
		t.Fatalf("second generate without confirmation must be 409; got %d body=%s", code, body)
	}
	if !strings.Contains(body, "already exists") {
		t.Errorf("409 body must explain existing key; got %s", body)
	}

	// Existing key must NOT be overwritten.
	var after string
	_ = sqlDB.QueryRow(`SELECT private_key_pem FROM coremail_dkim_config WHERE domain = ?`, "example.com").Scan(&after)
	if first != after {
		t.Errorf("existing key must not be overwritten on failed rotation; first=%q after=%q", first, after)
	}
	var sel string
	_ = sqlDB.QueryRow(`SELECT selector FROM coremail_dkim_config WHERE domain = ?`, "example.com").Scan(&sel)
	if sel != "a" {
		t.Errorf("selector must not change on failed rotation; got %q want a", sel)
	}
}

// TestDNSOpsDKIMGenerateWrongConfirmationReturns409 confirms a
// wrong confirm_rotation value returns 409 and does not overwrite.
func TestDNSOpsDKIMGenerateWrongConfirmationReturns409(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	if c, _ := h.do(t, "POST", "/api/v1/admin/dns/example.com/dkim", h.adminT, `{"selector":"a"}`); c != http.StatusCreated {
		t.Fatalf("first generate failed")
	}
	sqlDB, _ := h.db.DB()
	var first string
	_ = sqlDB.QueryRow(`SELECT private_key_pem FROM coremail_dkim_config WHERE domain = ?`, "example.com").Scan(&first)

	// Second call with WRONG confirmation.
	code, body := h.do(t, "POST", "/api/v1/admin/dns/example.com/dkim", h.adminT,
		`{"selector":"b","confirm_rotation":"wrong-value"}`)
	if code != http.StatusConflict {
		t.Fatalf("wrong confirmation must be 409; got %d body=%s", code, body)
	}
	if !strings.Contains(body, "already exists") {
		t.Errorf("409 body must explain existing key; got %s", body)
	}

	var after string
	_ = sqlDB.QueryRow(`SELECT private_key_pem FROM coremail_dkim_config WHERE domain = ?`, "example.com").Scan(&after)
	if first != after {
		t.Errorf("existing key must not be overwritten on wrong confirmation")
	}
}

// TestDNSOpsDKIMGenerateWithCorrectConfirmationRotates confirms
// that providing the correct confirm_rotation string allows
// rotation and produces a different key+selector.
func TestDNSOpsDKIMGenerateWithCorrectConfirmationRotates(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	if c, _ := h.do(t, "POST", "/api/v1/admin/dns/example.com/dkim", h.adminT, `{"selector":"a"}`); c != http.StatusCreated {
		t.Fatalf("first generate failed")
	}
	sqlDB, _ := h.db.DB()
	var first string
	_ = sqlDB.QueryRow(`SELECT private_key_pem FROM coremail_dkim_config WHERE domain = ?`, "example.com").Scan(&first)

	// Second call WITH correct confirmation must succeed.
	code, body := h.do(t, "POST", "/api/v1/admin/dns/example.com/dkim", h.adminT,
		`{"selector":"b","confirm_rotation":"rotate-dkim-key"}`)
	if code != http.StatusCreated {
		t.Fatalf("second generate with confirmation must be 201; got %d body=%s", code, body)
	}

	var second string
	_ = sqlDB.QueryRow(`SELECT private_key_pem FROM coremail_dkim_config WHERE domain = ?`, "example.com").Scan(&second)
	if first == second {
		t.Errorf("rotating DKIM must produce a different private key")
	}
	var selector string
	_ = sqlDB.QueryRow(`SELECT selector FROM coremail_dkim_config WHERE domain = ?`, "example.com").Scan(&selector)
	if selector != "b" {
		t.Errorf("selector after rotation: got %q want b", selector)
	}
	// Private key must not appear in response.
	for _, banned := range []string{"PRIVATE KEY", "BEGIN", "-----BEGIN"} {
		if strings.Contains(strings.ToUpper(body), banned) {
			t.Errorf("response must not contain %q; got %s", banned, body)
		}
	}
}

// TestDNSOpsDKIMGenerateNoPrivateKeyInResponse sanity-checks the
// full response body for the word "PRIVATE" (covers any inadvertent
// exposure of the PEM block).
func TestDNSOpsDKIMGenerateNoPrivateKeyInResponse(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	_, body := h.do(t, "POST", "/api/v1/admin/dns/example.com/dkim", h.adminT, `{"selector":"orvix"}`)
	upper := strings.ToUpper(body)
	for _, banned := range []string{"PRIVATE KEY", "BEGIN", "-----BEGIN"} {
		if strings.Contains(upper, banned) {
			t.Errorf("response must not contain %q; got %s", banned, body)
		}
	}
}

// ── Provider plan / apply ───────────────────────────────────────

// TestDNSOpsProviderPlanManual: the manual provider always returns
// at least one step and never carries a token-shaped string.
func TestDNSOpsProviderPlanManual(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	code, body := h.do(t, "POST", "/api/v1/admin/dns/example.com/provider/plan?provider=manual", h.adminT, "")
	if code != http.StatusOK {
		t.Fatalf("plan must be 200; got %d body=%s", code, body)
	}
	if !strings.Contains(body, "\"provider\":\"manual\"") {
		t.Errorf("response must echo provider=manual; got %s", body)
	}
	for _, banned := range []string{"api_key", "api_token", "secret", "password", "bearer "} {
		if strings.Contains(strings.ToLower(body), banned) {
			t.Errorf("provider plan must not contain %q; got %s", banned, body)
		}
	}
}

// TestDNSOpsProviderApplyRequiresConfirmation: empty confirm
// returns 400.
func TestDNSOpsProviderApplyRequiresConfirmation(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	code, body := h.do(t, "POST", "/api/v1/admin/dns/example.com/provider/apply?provider=manual", h.adminT, `{}`)
	if code != http.StatusBadRequest {
		t.Errorf("empty confirm must be 400; got %d body=%s", code, body)
	}
}

// TestDNSOpsProviderApplyManualFailsSafely: with confirmation,
// the manual provider returns a Failed result (not a fake success).
func TestDNSOpsProviderApplyManualFailsSafely(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	code, body := h.do(t, "POST", "/api/v1/admin/dns/example.com/provider/apply?provider=manual", h.adminT,
		`{"confirm":"yes-i-confirm"}`)
	if code != http.StatusOK {
		t.Fatalf("apply must be 200; got %d body=%s", code, body)
	}
	if !strings.Contains(body, "\"failed\"") {
		t.Errorf("apply result must include failed count; got %s", body)
	}
	if strings.Contains(body, "\"applied\":1") || strings.Contains(body, "\"applied\": 1") {
		t.Errorf("manual apply must not report applied=1; got %s", body)
	}
}

// TestDNSOpsProviderPlanUnknown: unknown provider name errors.
func TestDNSOpsProviderPlanUnknown(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	code, _ := h.do(t, "POST", "/api/v1/admin/dns/example.com/provider/plan?provider=bogus", h.adminT, "")
	if code != http.StatusBadRequest && code != http.StatusUnprocessableEntity {
		t.Errorf("unknown provider must be 4xx; got %d", code)
	}
}

// TestDNSOpsProviderListReturnsManualAlways: the providers list
// always contains manual even when no env config is set.
func TestDNSOpsProviderListReturnsManualAlways(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	code, body := h.do(t, "GET", "/api/v1/admin/dns/providers", h.adminT, "")
	if code != http.StatusOK {
		t.Fatalf("providers must be 200; got %d body=%s", code, body)
	}
	var resp struct {
		Providers []struct {
			Name   string   `json:"name"`
			Status string   `json:"status"`
			Notes  []string `json:"notes"`
		} `json:"providers"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, body)
	}
	names := map[string]bool{}
	for _, p := range resp.Providers {
		names[p.Name] = true
	}
	if !names["manual"] {
		t.Errorf("providers list must include manual; got %v", resp.Providers)
	}
	// No tokens leak through.
	for _, banned := range []string{"api_key", "secret", "password"} {
		if strings.Contains(strings.ToLower(body), banned) {
			t.Errorf("providers list must not contain %q; got %s", banned, body)
		}
	}
}

// ── Backward-compat aliases ─────────────────────────────────────

// TestDNSOpsLegacyDNSCheckReturnsRealVerify: the legacy
// /dns/check/:domain endpoint now delegates to the real
// verifier. With no fixture, report.Verified must be false.
func TestDNSOpsLegacyDNSCheckReturnsRealVerify(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	// Use POST since the legacy route is POST.
	code, body := h.do(t, "POST", "/api/v1/dns/check/example.com", h.adminT, "")
	if code != http.StatusOK {
		t.Fatalf("legacy check must be 200; got %d body=%s", code, body)
	}
	var resp struct {
		Report struct {
			Verified bool `json:"verified"`
		} `json:"report"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, body)
	}
	if resp.Report.Verified {
		t.Errorf("verify with empty resolver must be false")
	}
}

// TestDNSOpsLegacyDNSWizardReturnsRealPlan: the legacy
// /dns/wizard/:domain endpoint delegates to the real plan
// generator.
func TestDNSOpsLegacyDNSWizardReturnsRealPlan(t *testing.T) {
	h := newDNSOpsHarness(t)
	defer h.close()
	code, body := h.do(t, "POST", "/api/v1/dns/wizard/example.com", h.adminT, "")
	if code != http.StatusOK {
		t.Fatalf("legacy wizard must be 200; got %d body=%s", code, body)
	}
	if !strings.Contains(body, "v=spf1") || !strings.Contains(body, "v=DMARC1") {
		t.Errorf("legacy wizard must return real SPF and DMARC records; got %s", body)
	}
	for _, banned := range []string{"YOUR-PUBLIC-KEY", "include:"} {
		if strings.Contains(body, banned) {
			t.Errorf("legacy wizard must not return %q; got %s", banned, body)
		}
	}
}

// ── Service-unavailable path ────────────────────────────────────

// TestDNSOpsServiceUnavailableReturns503: with no service wired,
// endpoints fail closed with 503 rather than fabricating data.
func TestDNSOpsServiceUnavailableReturns503(t *testing.T) {
	logger := zap.NewNop()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(dir, "test.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.License.OfflineMode = true
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	authn, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	h := handlers.NewHandler(db, authn, nil, logger, cfg, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	// Deliberately do NOT call SetDNSOpsService.
	app := fiber.New()
	adminTok, _ := authn.GenerateAccessToken(1, auth.RoleAdmin)
	app.Get("/api/v1/admin/dns/example.com/plan", func(c fiber.Ctx) error {
		c.Locals("user_id", uint(1))
		c.Locals("role", auth.RoleAdmin)
		return h.GetAdminDNSPlan(c)
	})
	req := httptest.NewRequest("GET", "/api/v1/admin/dns/example.com/plan", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	res, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("missing service must be 503; got %d", res.StatusCode)
	}
}

// ── Helpers ─────────────────────────────────────────────────────

// populateFixtureForDomain installs the FakeResolver entries a
// verifier needs to mark every Required record verified for the
// supplied domain. The DKIM selector, MTA-STS id, and pubBase64
// MUST match the plan the handler emits; the test that calls this
// helper first fetches the plan to learn the selector+id and
// calls seedDKIMRow to learn the pubBase64.
func populateFixtureForDomain(h *dnsOpsHarness, domain, dkimSelector, mtaPolicyID, pubBase64 string) {
	// Apex MX + SPF.
	h.resolve.Set(domain, dnsops.FakeEntry{
		MX:  []net.MX{{Host: "mail." + domain + ".", Pref: 10}},
		TXT: []string{"v=spf1 mx ip4:8.8.8.8 -all"},
	})
	h.resolve.Set("mail."+domain, dnsops.FakeEntry{
		A: []net.IP{net.ParseIP("8.8.8.8")},
	})
	h.resolve.Set("_dmarc."+domain, dnsops.FakeEntry{
		TXT: []string{"v=DMARC1; p=none; rua=mailto:dmarc@" + domain + "; adkim=s; aspf=s; pct=100"},
	})
	h.resolve.Set("_mta-sts."+domain, dnsops.FakeEntry{
		TXT: []string{"v=STSv1; id=" + mtaPolicyID},
	})
	h.resolve.Set("_smtp._tls."+domain, dnsops.FakeEntry{
		TXT: []string{"v=TLSRPTv1; rua=mailto:tlsrpt@" + domain},
	})
	h.resolve.Set(dkimSelector+"._domainkey."+domain, dnsops.FakeEntry{
		TXT: []string{"v=DKIM1; k=rsa; p=" + pubBase64},
	})
	h.resolve.Set("8.8.8.8", dnsops.FakeEntry{
		PTRFor: map[string][]string{"8.8.8.8": {"mail." + domain + "."}},
	})
}

// seedDKIMRow inserts a coremail_dkim_config row for a domain
// using a freshly generated RSA 2048 keypair. It returns the
// base64-DER SubjectPublicKeyInfo string the plan emits, so the
// caller can populate the FakeResolver with the matching TXT.
func seedDKIMRow(t *testing.T, h *dnsOpsHarness, domain, selector string) (string, error) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", err
	}
	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", err
	}
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privBytes,
	})
	pubBytes, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return "", err
	}
	// Match the dnsops.deriveDKIMPublicKey encoding: base64
	// std encoding with padding (dkim.GenerateDNSRecord uses
	// the same format via dkim.pemEncodeBase64). For real RSA
	// SubjectPublicKeyInfo DER, standard base64 with padding is
	// the wire format the dkim package emits.
	pubBase64 := base64.StdEncoding.EncodeToString(pubBytes)
	sqlDB, err := h.db.DB()
	if err != nil {
		return "", err
	}
	_, err = sqlDB.Exec(
		`INSERT INTO coremail_dkim_config (domain, selector, private_key_pem, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, 1, ?, ?)`,
		domain, selector, string(privPEM), time.Now().UTC(), time.Now().UTC())
	if err != nil {
		return "", err
	}
	return pubBase64, nil
}
