package handlers_test

// DNS Ops harness variants for the SAFETY-FIX test suite.
//
// newDNSOpsHarness (in dns_ops_test.go) is the default harness
// with cfg.DNS.PublicIPv4 pinned to 8.8.8.8. The variants
// here let each test pin the public IP explicitly:
//
//   - newDNSOpsHarnessNoPublicIP: PublicIPv4 is empty (so the
//     plan endpoint returns 422 "public mail IPv4 is not
//     configured").
//   - newDNSOpsHarnessWithPublicIP(publicIPv4, publicIPv6):
//     pin both fields explicitly so tests can verify
//     loopback / private / link-local / unspecified rejection.

import (
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
)

// newDNSOpsHarnessNoPublicIP clears cfg.DNS.PublicIPv4 so the
// "not configured" 422 path is exercised. The listener bind
// host stays at 0.0.0.0 (the production default) so the test
// also implicitly asserts that the handler does NOT fall back
// to the listener bind address.
func newDNSOpsHarnessNoPublicIP(t *testing.T) *dnsOpsHarness {
	t.Helper()
	return newDNSOpsHarnessWithPublicIP(t, "", "")
}

// newDNSOpsHarnessWithPublicIP is the most flexible constructor.
// Pass "" for publicIPv4 / publicIPv6 to disable that field.
// Pass a private / loopback / link-local value to verify the
// validator rejects it.
func newDNSOpsHarnessWithPublicIP(t *testing.T, publicIPv4, publicIPv6 string) *dnsOpsHarness {
	t.Helper()
	logger := zap.NewNop()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(dir, "test.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.License.OfflineMode = true
	cfg.CoreMail.SMTPPort = 25
	cfg.CoreMail.IMAPPort = 143
	cfg.CoreMail.POP3Port = 110
	cfg.CoreMail.JMAPPort = 8080
	cfg.CoreMail.MailStorePath = dir
	// Pin the public IPs to whatever the caller asks for. The
	// listener bind host stays at the default 0.0.0.0 so the
	// listener-vs-public-IP separation is exercised in every
	// test that uses this constructor.
	cfg.DNS.PublicIPv4 = publicIPv4
	cfg.DNS.PublicIPv6 = publicIPv6
	cfg.CoreMail.Hostname = ""

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}
	schemas := []string{
		`CREATE TABLE IF NOT EXISTS coremail_audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			actor TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT '',
			target TEXT NOT NULL DEFAULT '',
			result TEXT NOT NULL DEFAULT '',
			ip TEXT NOT NULL DEFAULT '',
			user_agent TEXT NOT NULL DEFAULT '',
			timestamp DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_dkim_config (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain TEXT UNIQUE NOT NULL,
			selector TEXT NOT NULL DEFAULT 'default',
			private_key_pem TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
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
		)`,
	}
	for _, s := range schemas {
		if _, err := sqlDB.Exec(s); err != nil {
			t.Fatalf("create schema: %v", err)
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
	svc := dnsops.NewService(resolver)
	h.SetDNSOpsService(svc)
	app := fiber.New()
	adminTok, _ := authn.GenerateAccessToken(1, auth.RoleAdmin)
	userTok, _ := authn.GenerateAccessToken(2, auth.RoleUser)
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
	// Seed the example.com domain so the DKIM keygen path
	// passes the domainExists check by default. Tests that
	// need a missing-domain scenario override via direct SQL
	// DELETE or by checking a different domain.
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_domains (name, status, plan, description, max_mailboxes, max_aliases, max_quota_mb,
		   dkim_enabled, dkim_selector, dmarc_enabled, mtasts_enabled, catchall_address, abuse_contact, labels,
		   mailbox_count, created_at, updated_at)
		 VALUES ('example.com', 'active', 'smb', '', 100, 50, 1024, 0, '', 0, 0, '', '', '', 0, ?, ?)`,
		time.Now().UTC(), time.Now().UTC()); err != nil {
		t.Fatalf("seed coremail_domains: %v", err)
	}
	return &dnsOpsHarness{
		app: app, h: h, auth: authn, adminT: adminTok, userT: userTok, dir: dir, db: db, resolve: resolver,
	}
}
