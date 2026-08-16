package handlers_test

// Acceptance tests for POST /api/v1/platform/domains/:tenant_id/:id/dns/verify
// (Platform Admin live public-DNS verification) and the DKIM
// canonicalization fix's end-to-end effect on GET .../dns.
//
// Uses an in-memory FakeResolver — CI/unit tests never touch live
// public DNS.

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	domainadminsvc "github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/api/handlers"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail/dkim"
	"github.com/orvix/orvix/internal/dnsops"
	"github.com/orvix/orvix/internal/dnsops/providers"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/modules"
	"github.com/orvix/orvix/internal/platform/mailcontrol"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type platformDNSVerifyHarness struct {
	app       *fiber.App
	resolver  *dnsops.FakeResolver
	db        *gorm.DB
	psaTok    string
	tenantTok string
}

func (h *platformDNSVerifyHarness) close() {
	if sqlDB, err := h.db.DB(); err == nil && sqlDB != nil {
		_ = sqlDB.Close()
	}
}

func newPlatformDNSVerifyHarness(t *testing.T) *platformDNSVerifyHarness {
	t.Helper()
	logger := zap.NewNop()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(dir, "test.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.License.PublicKeyPath = ""
	cfg.License.OfflineMode = true
	cfg.CoreMail.MailStorePath = dir
	// Real public mail IPv4 the plan generator needs (never
	// 0.0.0.0 / the listener bind address; must not be a
	// documentation/test-net range, which isPublicUnicastIP rejects).
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

	ddls := []string{
		`CREATE TABLE IF NOT EXISTS coremail_dkim_config (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain TEXT UNIQUE NOT NULL,
			selector TEXT NOT NULL DEFAULT 'default',
			private_key_pem TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_mailboxes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL DEFAULT 0,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			deleted_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_aliases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL DEFAULT 0,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			deleted_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS orvix_audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			actor TEXT NOT NULL DEFAULT '',
			actor_id INTEGER NOT NULL DEFAULT 0,
			actor_role TEXT NOT NULL DEFAULT '',
			tenant_id INTEGER NOT NULL DEFAULT 0,
			action TEXT NOT NULL DEFAULT '',
			target TEXT NOT NULL DEFAULT '',
			target_id INTEGER NOT NULL DEFAULT 0,
			result TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			before TEXT NOT NULL DEFAULT '',
			after TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			ip TEXT NOT NULL DEFAULT '',
			user_agent TEXT NOT NULL DEFAULT '',
			timestamp DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_domains (
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
			default_mailbox_quota_mb INTEGER NOT NULL DEFAULT 0,
			dkim_enabled INTEGER NOT NULL DEFAULT 0,
			dkim_selector TEXT NOT NULL DEFAULT '',
			dmarc_enabled INTEGER NOT NULL DEFAULT 0,
			mtasts_enabled INTEGER NOT NULL DEFAULT 0,
			mail_access_mode TEXT NOT NULL DEFAULT 'internal_external',
			catchall_address TEXT NOT NULL DEFAULT '',
			abuse_contact TEXT NOT NULL DEFAULT '',
			labels TEXT NOT NULL DEFAULT '',
			mailbox_count INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			version INTEGER NOT NULL DEFAULT 1,
			deleted_at DATETIME,
			deactivated_at DATETIME,
			deactivation_reason TEXT NOT NULL DEFAULT ''
		)`,
	}
	for _, ddl := range ddls {
		if _, err := sqlDB.Exec(ddl); err != nil {
			t.Fatalf("ddl: %v\n%s", err, ddl)
		}
	}

	authn, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	ff := license.NewFeatureFlags(logger)
	ff.SetTier(license.TierSMB)

	h := handlers.NewHandler(db, authn, nil, logger, cfg, modules.NewRegistry(logger), ff, nil)

	resolver := dnsops.NewFakeResolver()
	dnsSvc := dnsops.NewService(resolver,
		providers.NewCloudflareProvider(providers.CloudflareConfig{}, resolver),
		providers.NewNamecheapProvider(providers.NamecheapConfig{}, providers.NewFakeNamecheapClient()),
	)
	h.SetDNSOpsService(dnsSvc)

	domainRepo := domainadminsvc.NewDomainAdminRepo(sqlDB)
	domainSvc := domainadminsvc.NewService(domainRepo, dkim.NewSQLRepo(sqlDB), audit.NewExtendedStore(sqlDB), nil)
	h.SetDomainAdminService(domainSvc)

	mcRepo := mailcontrol.NewRepository(sqlDB)
	mcSvc := mailcontrol.NewService(mcRepo, mailcontrol.Ports{Domains: domainSvc})
	h.SetMailControlService(mcSvc)

	app := fiber.New()
	psaTok, _ := authn.GenerateAccessToken(1, auth.RolePlatformSuperAdmin)
	tenantTok, _ := authn.GenerateAccessToken(2, auth.RoleAdmin)

	api := app.Group("/api/v1")
	mount := func(method, path string, fn fiber.Handler) {
		api.Add([]string{method}, path, func(c fiber.Ctx) error {
			hdr := c.Get("Authorization")
			switch {
			case strings.HasPrefix(hdr, "Bearer "+psaTok):
				c.Locals("user_id", uint(1))
				c.Locals("role", auth.RolePlatformSuperAdmin)
				return fn(c)
			case strings.HasPrefix(hdr, "Bearer "+tenantTok):
				// A non-PSA caller must never reach the platform
				// handler at all in production (platformMW gates
				// it) — this harness mounts routes directly, so we
				// reproduce that gate here rather than silently
				// falling through to the handler.
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "forbidden", "code": "FORBIDDEN"})
			default:
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
			}
		})
	}
	mount("GET", "/platform/domains/:tenant_id/:id/dns", h.GetPlatformDomainDNS)
	mount("POST", "/platform/domains/:tenant_id/:id/dns/verify", h.VerifyPlatformDomainDNS)

	return &platformDNSVerifyHarness{app: app, resolver: resolver, db: db, psaTok: psaTok, tenantTok: tenantTok}
}

func (h *platformDNSVerifyHarness) do(t *testing.T, method, path, token string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
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

// seedDomainWithDKIM inserts a domain row and generates a real DKIM
// key for it via the production admin/domain service (so
// coremail_dkim_config holds a genuine RSA key, matching production
// data shape rather than a hand-crafted fixture).
func (h *platformDNSVerifyHarness) seedDomain(t *testing.T, tenantID uint, name string, generateDKIM bool) uint {
	t.Helper()
	sqlDB, _ := h.db.DB()
	now := time.Now().UTC()
	res, err := sqlDB.Exec(
		`INSERT INTO coremail_domains (name, tenant_id, status, plan, mail_access_mode, created_at, updated_at, version)
		 VALUES (?, ?, 'active', 'smb', 'internal_external', ?, ?, 1)`,
		name, tenantID, now, now)
	if err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	id, _ := res.LastInsertId()
	if generateDKIM {
		domainRepo := domainadminsvc.NewDomainAdminRepo(sqlDB)
		domainSvc := domainadminsvc.NewService(domainRepo, dkim.NewSQLRepo(sqlDB), audit.NewExtendedStore(sqlDB), nil)
		if _, err := domainSvc.GenerateDKIM(context.Background(), uint(id), tenantID, "orvix"); err != nil {
			t.Fatalf("generate dkim: %v", err)
		}
	}
	return uint(id)
}

// TestPlatformDNSVerify_MatchedRecordsReportVerified proves the happy
// path: SPF and MX published exactly as expected -> verified status
// with Observed populated.
func TestPlatformDNSVerify_MatchedRecordsReportVerified(t *testing.T) {
	h := newPlatformDNSVerifyHarness(t)
	defer h.close()
	id := h.seedDomain(t, 1, "verify-match.example", false)

	h.resolver.Set("verify-match.example", dnsops.FakeEntry{
		MX:  []net.MX{{Host: "mail.verify-match.example.", Pref: 10}},
		TXT: []string{"v=spf1 mx ip4:8.8.8.8 -all"},
	})
	h.resolver.Set("mail.verify-match.example", dnsops.FakeEntry{})

	status, body := h.do(t, "POST", "/api/v1/platform/domains/1/"+strconv.Itoa(int(id))+"/dns/verify", h.psaTok)
	if status != 200 {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var resp mailcontrol.PlatformDNSVerifyResult
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	found := false
	for _, r := range resp.Records {
		if r.Purpose == "spf" {
			found = true
			if r.Status != "verified" || !r.Verified {
				t.Errorf("spf status=%s verified=%v reason=%s", r.Status, r.Verified, r.Reason)
			}
			if r.Observed == "" {
				t.Error("spf Observed must be populated")
			}
		}
	}
	if !found {
		t.Fatal("no spf record in response")
	}
}

// TestPlatformDNSVerify_MissingRecordsReportMissing.
func TestPlatformDNSVerify_MissingRecordsReportMissing(t *testing.T) {
	h := newPlatformDNSVerifyHarness(t)
	defer h.close()
	id := h.seedDomain(t, 1, "verify-missing.example", false)

	status, body := h.do(t, "POST", "/api/v1/platform/domains/1/"+strconv.Itoa(int(id))+"/dns/verify", h.psaTok)
	if status != 200 {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var resp mailcontrol.PlatformDNSVerifyResult
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AllVerified {
		t.Error("all_verified must be false when nothing is published")
	}
	for _, r := range resp.Records {
		if r.Purpose == "mx" && r.Status != "missing" {
			t.Errorf("mx status=%s, want missing", r.Status)
		}
	}
}

// TestPlatformDNSVerify_DKIMOldKeyIsMismatchNeverGreen proves the
// DKIM comparison uses the CURRENT configured key: publishing an old
// or arbitrary DKIM record must be a mismatch, never a false verify.
func TestPlatformDNSVerify_DKIMOldKeyIsMismatchNeverGreen(t *testing.T) {
	h := newPlatformDNSVerifyHarness(t)
	defer h.close()
	id := h.seedDomain(t, 1, "verify-dkim.example", true)

	h.resolver.Set("orvix._domainkey.verify-dkim.example", dnsops.FakeEntry{
		TXT: []string{"v=DKIM1; k=rsa; p=SOMEOLDUNRELATEDKEYVALUE"},
	})

	status, body := h.do(t, "POST", "/api/v1/platform/domains/1/"+strconv.Itoa(int(id))+"/dns/verify", h.psaTok)
	if status != 200 {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var resp mailcontrol.PlatformDNSVerifyResult
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, r := range resp.Records {
		if r.Purpose == "dkim" {
			if r.Status != "mismatch" {
				t.Errorf("dkim with old key must be mismatch, got %s", r.Status)
			}
			if strings.Contains(r.Observed, "BEGIN") || strings.Contains(r.Reason, "BEGIN") {
				t.Error("no private key material may appear in the response")
			}
		}
	}
}

// TestPlatformDNSVerify_ResolverErrorIsErrorNotMismatch.
func TestPlatformDNSVerify_ResolverErrorIsErrorNotMismatch(t *testing.T) {
	h := newPlatformDNSVerifyHarness(t)
	defer h.close()
	id := h.seedDomain(t, 1, "verify-error.example", false)

	h.resolver.Set("verify-error.example", dnsops.FakeEntry{
		Err: &net.DNSError{Err: "timeout", IsTimeout: true},
	})

	status, body := h.do(t, "POST", "/api/v1/platform/domains/1/"+strconv.Itoa(int(id))+"/dns/verify", h.psaTok)
	if status != 200 {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var resp mailcontrol.PlatformDNSVerifyResult
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, r := range resp.Records {
		if r.Purpose == "mx" || r.Purpose == "spf" {
			if r.Status != "error" {
				t.Errorf("%s: resolver timeout must be status=error, got %s", r.Purpose, r.Status)
			}
		}
	}
}

// TestPlatformDNSVerify_TenantIsolation proves a PSA request naming
// tenant A's domain id under tenant B's path yields NOT_FOUND, never
// tenant A's live verification result.
func TestPlatformDNSVerify_TenantIsolation(t *testing.T) {
	h := newPlatformDNSVerifyHarness(t)
	defer h.close()
	idA := h.seedDomain(t, 1, "tenant-a.example", false)
	_ = h.seedDomain(t, 2, "tenant-b.example", false)

	// Tenant A's domain id requested under tenant 2's path.
	status, body := h.do(t, "POST", "/api/v1/platform/domains/2/"+strconv.Itoa(int(idA))+"/dns/verify", h.psaTok)
	if status != 404 {
		t.Fatalf("cross-tenant verify must be 404 NOT_FOUND, got status=%d body=%s", status, body)
	}
}

// TestPlatformDNSVerify_RequiresPlatformRole proves a non-PSA caller
// cannot reach the verify route.
func TestPlatformDNSVerify_RequiresPlatformRole(t *testing.T) {
	h := newPlatformDNSVerifyHarness(t)
	defer h.close()
	id := h.seedDomain(t, 1, "verify-role.example", false)

	status, _ := h.do(t, "POST", "/api/v1/platform/domains/1/"+strconv.Itoa(int(id))+"/dns/verify", h.tenantTok)
	if status != 403 {
		t.Fatalf("non-PSA caller must get 403, got %d", status)
	}
}

// TestPlatformDNSVerify_ConsistentWithReadDKIM: the DKIM value shown
// by GET .../dns (dkim_public_dns_txt) must equal the value the
// verifier compared against (the plan's DKIM record.Value) — proving
// the double-Base64 fix keeps every DKIM-reading surface consistent.
func TestPlatformDNSVerify_ConsistentWithReadDKIM(t *testing.T) {
	h := newPlatformDNSVerifyHarness(t)
	defer h.close()
	id := h.seedDomain(t, 1, "verify-consistent.example", true)

	statusRead, bodyRead := h.do(t, "GET", "/api/v1/platform/domains/1/"+strconv.Itoa(int(id))+"/dns", h.psaTok)
	if statusRead != 200 {
		t.Fatalf("read status=%d body=%s", statusRead, bodyRead)
	}
	var readResp mailcontrol.PlatformDomainDNSResult
	if err := json.Unmarshal([]byte(bodyRead), &readResp); err != nil {
		t.Fatalf("decode read: %v", err)
	}
	if !readResp.DKIMConfigured || readResp.DKIMPublicDNSTxt == "" {
		t.Fatalf("expected dkim configured with a public txt value, got %#v", readResp)
	}

	// Publish exactly that value in DNS, then verify — must be
	// StatusVerified, proving GetDomainDNS's single-encoded value and
	// the plan the verifier checks against agree byte-for-byte.
	h.resolver.Set("orvix._domainkey.verify-consistent.example", dnsops.FakeEntry{
		TXT: []string{readResp.DKIMPublicDNSTxt},
	})

	statusVerify, bodyVerify := h.do(t, "POST", "/api/v1/platform/domains/1/"+strconv.Itoa(int(id))+"/dns/verify", h.psaTok)
	if statusVerify != 200 {
		t.Fatalf("verify status=%d body=%s", statusVerify, bodyVerify)
	}
	var verifyResp mailcontrol.PlatformDNSVerifyResult
	if err := json.Unmarshal([]byte(bodyVerify), &verifyResp); err != nil {
		t.Fatalf("decode verify: %v", err)
	}
	for _, r := range verifyResp.Records {
		if r.Purpose == "dkim" {
			if r.Status != "verified" {
				t.Fatalf("publishing GetDomainDNS's own dkim_public_dns_txt must verify; got %s (%s) — DNS Setup and DKIM tab would disagree", r.Status, r.Reason)
			}
			return
		}
	}
	t.Fatal("no dkim record in verify response")
}
