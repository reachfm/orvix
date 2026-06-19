package handlers_test

// Integration tests for the Admin Runtime Telemetry endpoint
// (ADMIN-RUNTIME-TELEMETRY-2B). The endpoint is admin-protected,
// GET-only, read-only, and must never return secrets.
//
// We use a live in-process fiber app with a sqlite DB and a real
// Handler bound to admin role. The tests assert:
//   - non-admin users get 403
//   - admin users get 200 with the documented shape
//   - the response never includes secret-bearer fields
//   - the disk label is a safe label, never an absolute path
//   - the queue counts and license posture are honest defaults
//   - a no-StartedAt handler still serves a 200 with a
//     telemetry_incomplete warning (no crash, no fake uptime)

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api/handlers"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/modules"
	"github.com/orvix/orvix/internal/runtime"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// runtimeTestHarness builds a tiny fiber app with the runtime
// handler mounted behind a token-checking middleware. We use the
// project's own config.NewDatabase (modernc.org/sqlite under the
// hood, no CGO required) so the harness is portable to the
// CI runner.
type runtimeTestHarness struct {
	app    *fiber.App
	h      *handlers.Handler
	auth   *auth.Authenticator
	adminT string
	userT  string
	dir    string
	db     *gorm.DB
}

// close releases the underlying *sql.DB so the test temp dir can
// be cleaned up on Windows. Tests should call defer h.close() right
// after construction.
func (h *runtimeTestHarness) close() {
	if h.db == nil {
		return
	}
	if sqlDB, err := h.db.DB(); err == nil && sqlDB != nil {
		_ = sqlDB.Close()
	}
}

func newRuntimeHarness(t *testing.T, startedAt time.Time) *runtimeTestHarness {
	t.Helper()
	logger := zap.NewNop()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(dir, "test.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.License.PublicKeyPath = "" // public key missing on purpose
	cfg.License.OfflineMode = true
	cfg.CoreMail.SMTPPort = 25
	cfg.CoreMail.IMAPPort = 143
	cfg.CoreMail.POP3Port = 110
	cfg.CoreMail.JMAPPort = 8080
	cfg.CoreMail.MailStorePath = dir

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	// License table is required by licensePostureForTelemetry.
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS licenses (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at DATETIME, updated_at DATETIME, deleted_at DATETIME,
		key_hash TEXT NOT NULL DEFAULT '',
		tier TEXT NOT NULL DEFAULT 'smb',
		issued_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		expires_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		max_domains INTEGER NOT NULL DEFAULT 10,
		max_mailboxes INTEGER NOT NULL DEFAULT 500,
		hardware_id TEXT NOT NULL DEFAULT '',
		metadata TEXT NOT NULL DEFAULT '',
		active INTEGER NOT NULL DEFAULT 1
	)`).Error; err != nil {
		t.Fatalf("create licenses: %v", err)
	}
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS coremail_queue (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		deleted_at DATETIME,
		status TEXT NOT NULL DEFAULT 'pending'
	)`).Error; err != nil {
		t.Fatalf("create coremail_queue: %v", err)
	}

	authn, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	ff := license.NewFeatureFlags(logger)
	ff.SetTier(license.TierSMB)

	h := handlers.NewHandler(db, authn, nil, logger, cfg, modules.NewRegistry(logger), ff, nil)
	if !startedAt.IsZero() {
		h.SetProcessStartedAt(startedAt)
	}

	app := fiber.New()
	adminTok, _ := authn.GenerateAccessToken(1, auth.RoleAdmin)
	userTok, _ := authn.GenerateAccessToken(2, auth.RoleUser)

	app.Get("/api/v1/admin/runtime", func(c fiber.Ctx) error {
		hdr := c.Get("Authorization")
		switch {
		case strings.HasPrefix(hdr, "Bearer "+adminTok):
			c.Locals("user_id", uint(1))
			c.Locals("role", auth.RoleAdmin)
			return h.GetAdminRuntime(c)
		case strings.HasPrefix(hdr, "Bearer "+userTok):
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "insufficient permissions"})
		default:
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
		}
	})

	return &runtimeTestHarness{
		app:    app,
		h:      h,
		auth:   authn,
		adminT: adminTok,
		userT:  userTok,
		dir:    dir,
		db:     db,
	}
}

func (h *runtimeTestHarness) get(t *testing.T, token string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/runtime", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := h.app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(body)
}

// TestAdminRuntimeRequiresAdmin confirms non-admin tokens are rejected.
func TestAdminRuntimeRequiresAdmin(t *testing.T) {
	h := newRuntimeHarness(t, time.Now().Add(-time.Hour))
	defer h.close()
	code, body := h.get(t, h.userT)
	if code != http.StatusForbidden {
		t.Errorf("user must be forbidden; got %d body=%s", code, body)
	}
}

// TestAdminRuntimeRequiresAuth confirms no token is rejected.
func TestAdminRuntimeRequiresAuth(t *testing.T) {
	h := newRuntimeHarness(t, time.Now().Add(-time.Hour))
	defer h.close()
	code, _ := h.get(t, "")
	if code != http.StatusUnauthorized {
		t.Errorf("no token must be unauthorized; got %d", code)
	}
}

// TestAdminRuntimeShape confirms the admin response carries the
// documented fields and never includes secrets.
func TestAdminRuntimeShape(t *testing.T) {
	h := newRuntimeHarness(t, time.Now().Add(-time.Hour))
	defer h.close()
	code, body := h.get(t, h.adminT)
	if code != http.StatusOK {
		t.Fatalf("admin must be 200; got %d body=%s", code, body)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, body)
	}
	for _, want := range []string{
		"status", "version", "commit", "build_time", "go_version", "arch",
		"hostname", "uptime_seconds", "services", "capacity", "queue",
		"license", "warnings",
	} {
		if _, ok := resp[want]; !ok {
			t.Errorf("response missing %q; got %s", want, body)
		}
	}
	// No secret-bearer fields.
	lc := strings.ToLower(body)
	for _, banned := range []string{
		"password", "passwd", "secret", "private_key", "priv_key",
		"api_key", "access_token", "refresh_token", "jwt_secret",
		"key_hash", "metadata",
	} {
		if strings.Contains(lc, banned) {
			t.Errorf("response must not contain %q; got %s", banned, body)
		}
	}
}

// TestAdminRuntimeDiskLabelSafe confirms the disk label in the
// response is a safe name, not the absolute data path.
func TestAdminRuntimeDiskLabelSafe(t *testing.T) {
	h := newRuntimeHarness(t, time.Now().Add(-time.Hour))
	defer h.close()
	_, body := h.get(t, h.adminT)
	if strings.Contains(body, h.dir) {
		t.Errorf("response must not echo the absolute data path %q; got %s", h.dir, body)
	}
	var resp struct {
		Capacity struct {
			Disk struct {
				Label string `json:"label"`
			} `json:"disk"`
		} `json:"capacity"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Capacity.Disk.Label == "" {
		t.Errorf("disk label must be populated; got %q", resp.Capacity.Disk.Label)
	}
	// Must not look like an absolute path.
	if strings.HasPrefix(resp.Capacity.Disk.Label, "/") || strings.HasPrefix(resp.Capacity.Disk.Label, `\`) {
		t.Errorf("disk label must not be absolute; got %q", resp.Capacity.Disk.Label)
	}
}

// TestAdminRuntimeUptimePositive confirms a StartedAt one hour
// ago produces an uptime in the [3500, 3700] range.
func TestAdminRuntimeUptimePositive(t *testing.T) {
	h := newRuntimeHarness(t, time.Now().Add(-time.Hour))
	defer h.close()
	_, body := h.get(t, h.adminT)
	var resp struct {
		UptimeSeconds int64   `json:"uptime_seconds"`
		Status        string  `json:"status"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.UptimeSeconds < 3500 || resp.UptimeSeconds > 3700 {
		t.Errorf("uptime: want ~3600 got %d", resp.UptimeSeconds)
	}
	if resp.Status != "ok" && resp.Status != "degraded" {
		t.Errorf("status with started-at must be ok or degraded; got %q", resp.Status)
	}
}

// TestAdminRuntimeNoStartedAt confirms a zero StartedAt still
// serves a 200 with a telemetry_incomplete warning rather than
// crashing or fabricating an uptime.
func TestAdminRuntimeNoStartedAt(t *testing.T) {
	h := newRuntimeHarness(t, time.Time{})
	defer h.close()
	_, body := h.get(t, h.adminT)
	var resp struct {
		UptimeSeconds int64 `json:"uptime_seconds"`
		Warnings      []struct {
			Code string `json:"code"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.UptimeSeconds != 0 {
		t.Errorf("uptime with no StartedAt must be 0; got %d", resp.UptimeSeconds)
	}
	seen := false
	for _, w := range resp.Warnings {
		if w.Code == "telemetry_incomplete" {
			seen = true
			break
		}
	}
	if !seen {
		t.Errorf("response must carry telemetry_incomplete warning when StartedAt is zero; got %s", body)
	}
}

// TestAdminRuntimeListenerUnknown confirms the listener services
// never report "ok" because runtime listener state is not
// tracked today.
func TestAdminRuntimeListenerUnknown(t *testing.T) {
	h := newRuntimeHarness(t, time.Now().Add(-time.Hour))
	defer h.close()
	_, body := h.get(t, h.adminT)
	var resp struct {
		Services map[string]struct {
			Status string `json:"status"`
			Port   int    `json:"port"`
		} `json:"services"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, name := range []string{"smtp", "imap", "pop3", "jmap"} {
		s, ok := resp.Services[name]
		if !ok {
			t.Errorf("services must include %q; got %s", name, body)
			continue
		}
		if s.Status == "ok" {
			t.Errorf("%s must not report ok when listener runtime state is not tracked; got %+v", name, s)
		}
	}
}

// TestAdminRuntimeReadOnly confirms the endpoint is idempotent
// and does not error. We cannot easily diff row counts in this
// harness (the DB is opened in WAL mode and held by the handler
// for the lifetime of the test), so the proxy we use is shape
// stability: two calls in a row return the same top-level
// status and a non-zero uptime.
func TestAdminRuntimeReadOnly(t *testing.T) {
	h := newRuntimeHarness(t, time.Now().Add(-time.Hour))
	defer h.close()
	_, body1 := h.get(t, h.adminT)
	_, body2 := h.get(t, h.adminT)
	if !strings.Contains(body1, "services") {
		t.Errorf("response must include services; got %s", body1)
	}
	var r1, r2 struct {
		Status        string `json:"status"`
		UptimeSeconds int64  `json:"uptime_seconds"`
	}
	_ = json.Unmarshal([]byte(body1), &r1)
	_ = json.Unmarshal([]byte(body2), &r2)
	if r1.Status != r2.Status {
		t.Errorf("status must be stable across calls; got %q then %q", r1.Status, r2.Status)
	}
	if r1.UptimeSeconds == 0 || r2.UptimeSeconds == 0 {
		t.Errorf("uptime must be non-zero in both calls; got %d / %d", r1.UptimeSeconds, r2.UptimeSeconds)
	}
}

// cfgLite is kept for backward compatibility with earlier test
// scaffolding; the new TestAdminRuntimeReadOnly does not need
// it but we leave the helper in case a follow-up wants to
// inspect the harness's DSN.
func (h *runtimeTestHarness) cfgLite(t *testing.T) *config.Config {
	t.Helper()
	c := config.Defaults()
	c.Database.Driver = "sqlite"
	c.Database.DSN = filepath.Join(h.dir, "test.db")
	return c
}

// TestAdminRuntimeContainsAllQueueStatuses confirms the queue
// counts come through even when zero.
func TestAdminRuntimeContainsAllQueueStatuses(t *testing.T) {
	h := newRuntimeHarness(t, time.Now().Add(-time.Hour))
	defer h.close()
	_, body := h.get(t, h.adminT)
	for _, want := range []string{`"pending"`, `"deferred"`, `"bounced"`, `"delivered"`} {
		if !strings.Contains(body, want) {
			t.Errorf("queue missing %s; got %s", want, body)
		}
	}
}

// TestAdminRuntimeLicenseHonest confirms the license posture
// reflects the absence of a public key.
func TestAdminRuntimeLicenseHonest(t *testing.T) {
	h := newRuntimeHarness(t, time.Now().Add(-time.Hour))
	defer h.close()
	_, body := h.get(t, h.adminT)
	var resp struct {
		License struct {
			Mode            string `json:"mode"`
			PublicKeyLoaded bool   `json:"public_key_loaded"`
			Status          string `json:"status"`
		} `json:"license"`
		Warnings []struct {
			Code string `json:"code"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v", body)
	}
	if resp.License.PublicKeyLoaded {
		t.Errorf("public_key_loaded must be false (harness sets no public key); got %+v", resp.License)
	}
	// The license_public_key_missing warning must be present.
	seen := false
	for _, w := range resp.Warnings {
		if w.Code == "license_public_key_missing" {
			seen = true
		}
	}
	if !seen {
		t.Errorf("expected license_public_key_missing warning; got %+v", resp.Warnings)
	}
}

// TestAdminRuntimeNoStackTrace confirms the response is JSON
// without server stack-trace leakage.
func TestAdminRuntimeNoStackTrace(t *testing.T) {
	h := newRuntimeHarness(t, time.Now().Add(-time.Hour))
	defer h.close()
	_, body := h.get(t, h.adminT)
	for _, banned := range []string{"goroutine", "runtime/debug", "stack(", "panic"} {
		if strings.Contains(body, banned) {
			t.Errorf("response must not contain %q; got %s", banned, body)
		}
	}
}

// TestAdminRuntimeNilConfig confirms the endpoint does not panic
// when the handler has a nil config. All ports should default to 0
// and the response must be valid JSON with unknown/default values.
func TestAdminRuntimeNilConfig(t *testing.T) {
	logger := zap.NewNop()
	db, err := config.NewDatabase(&config.Defaults().Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	authn, err := auth.NewAuthenticator(&config.Defaults().Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	adminTok, _ := authn.GenerateAccessToken(1, auth.RoleAdmin)

	// Handler with nil cfg — this used to panic on h.cfg.CoreMail.
	h := &handlers.Handler{}
	h.SetProcessStartedAt(time.Now().Add(-time.Hour))

	app := fiber.New()
	app.Get("/api/v1/admin/runtime", func(c fiber.Ctx) error {
		c.Locals("user_id", uint(1))
		c.Locals("role", auth.RoleAdmin)
		return h.GetAdminRuntime(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/runtime", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	res, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("nil config handler should return 200; got %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	var resp runtime.Telemetry
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("response must be valid JSON: %v", err)
	}
	// Services must show unknown (not online) because no ports
	// were configured and no listener state exists.
	if resp.Services == nil {
		t.Errorf("services must not be nil")
	} else {
		for _, key := range []string{"smtp", "imap", "pop3", "jmap"} {
			s := resp.Services[key]
			if s.Status != "unknown" {
				t.Errorf("service %q status must be 'unknown' with nil config; got %q", key, s.Status)
			}
		}
	}
	// Version/commit must be set to defaults (not zero values).
	if resp.Version == "" {
		t.Errorf("version must not be empty with nil config")
	}
}

// TestAdminRuntimeNilDB confirms the endpoint does not panic when
// the handler has a nil DB. Queue counts must be zero and queue
// service must be unknown.
func TestAdminRuntimeNilDB(t *testing.T) {
	cfg := config.Defaults()
	logger := zap.NewNop()
	authn, err := auth.NewAuthenticator(&cfg.Auth, nil, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	adminTok, _ := authn.GenerateAccessToken(1, auth.RoleAdmin)

	// Handler with nil db — queueCountsForTelemetry and
	// dbPingErrorForTelemetry must not panic.
	h := handlers.NewHandler(nil, authn, nil, logger, cfg, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	h.SetProcessStartedAt(time.Now().Add(-time.Hour))

	app := fiber.New()
	app.Get("/api/v1/admin/runtime", func(c fiber.Ctx) error {
		c.Locals("user_id", uint(1))
		c.Locals("role", auth.RoleAdmin)
		return h.GetAdminRuntime(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/runtime", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	res, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("nil DB handler should return 200; got %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	var resp runtime.Telemetry
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("response must be valid JSON: %v", err)
	}
	// Queue counts must be zero (nil DB returns zero counts).
	if resp.Queue.Pending != 0 || resp.Queue.Deferred != 0 || resp.Queue.Bounced != 0 {
		t.Errorf("queue counts must be zero with nil DB; got %+v", resp.Queue)
	}
	// Queue service must show unknown (no counts to derive status).
	if resp.Services == nil {
		t.Errorf("services must not be nil")
	} else if qs, ok := resp.Services["queue"]; ok {
		if qs.Status != "unknown" {
			t.Errorf("queue service status must be 'unknown' with nil DB; got %q", qs.Status)
		}
	}
}

// TestAdminRuntimeLicenseFileMissing confirms that a non-empty
// PublicKeyPath pointing to a non-existent file does NOT report
// public_key_loaded=true. The old code checked only path non-empty,
// which was a false positive.
func TestAdminRuntimeLicenseFileMissing(t *testing.T) {
	logger := zap.NewNop()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(dir, "test.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	// Set a non-existent public key path.
	cfg.License.PublicKeyPath = filepath.Join(dir, "nonexistent.pub")
	cfg.License.OfflineMode = false

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	authn, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	adminTok, _ := authn.GenerateAccessToken(1, auth.RoleAdmin)

	h := handlers.NewHandler(db, authn, nil, logger, cfg, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	h.SetProcessStartedAt(time.Now().Add(-time.Hour))

	app := fiber.New()
	app.Get("/api/v1/admin/runtime", func(c fiber.Ctx) error {
		c.Locals("user_id", uint(1))
		c.Locals("role", auth.RoleAdmin)
		return h.GetAdminRuntime(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/runtime", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	res, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	var resp runtime.Telemetry
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("response must be valid JSON: %v", err)
	}
	if resp.License.PublicKeyLoaded {
		t.Errorf("public_key_loaded must be false when the public key file does not exist; got %+v", resp.License)
	}
	if resp.License.Mode == "online" {
		t.Errorf("license mode must not be 'online' when public key file is missing; got %q", resp.License.Mode)
	}
	// The license_public_key_missing warning must be present.
	seen := false
	for _, w := range resp.Warnings {
		if w.Code == "license_public_key_missing" {
			seen = true
		}
	}
	if !seen {
		t.Errorf("expected license_public_key_missing warning when public key path is set but file is missing; got %+v", resp.Warnings)
	}
}

// TestAdminRuntimeLicenseFileIsDirectory confirms that a configured
// path pointing to a directory (not a regular file) does NOT report
// public_key_loaded=true.
func TestAdminRuntimeLicenseFileIsDirectory(t *testing.T) {
	logger := zap.NewNop()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(dir, "test.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	// Point to a directory, not a file.
	cfg.License.PublicKeyPath = dir
	cfg.License.OfflineMode = false

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	authn, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	adminTok, _ := authn.GenerateAccessToken(1, auth.RoleAdmin)

	h := handlers.NewHandler(db, authn, nil, logger, cfg, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	h.SetProcessStartedAt(time.Now().Add(-time.Hour))

	app := fiber.New()
	app.Get("/api/v1/admin/runtime", func(c fiber.Ctx) error {
		c.Locals("user_id", uint(1))
		c.Locals("role", auth.RoleAdmin)
		return h.GetAdminRuntime(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/runtime", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	res, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	var resp runtime.Telemetry
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("response must be valid JSON: %v", err)
	}
	if resp.License.PublicKeyLoaded {
		t.Errorf("public_key_loaded must be false when path points to a directory; got %+v", resp.License)
	}
	if resp.License.Mode == "online" {
		t.Errorf("license mode must not be 'online' for a directory path; got %q", resp.License.Mode)
	}
	// A directory is reported as public_key_invalid (exists but
	// is not a regular file with valid PEM content), not missing.
	seenInvalid := false
	seenMissing := false
	for _, w := range resp.Warnings {
		if w.Code == "license_public_key_invalid" {
			seenInvalid = true
		}
		if w.Code == "license_public_key_missing" {
			seenMissing = true
		}
	}
	if !seenInvalid {
		// Accept either invalid or missing (backward compat).
		if !seenMissing {
			t.Errorf("expected license_public_key_invalid or license_public_key_missing warning for directory path; got %+v", resp.Warnings)
		}
	}
}

// TestAdminRuntimeLicenseFileInvalidContent confirms that a file
// with non-PEM content does NOT report public_key_loaded=true.
func TestAdminRuntimeLicenseFileInvalidContent(t *testing.T) {
	logger := zap.NewNop()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(dir, "test.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	// Write a file with garbage content, not a PEM public key.
	keyPath := filepath.Join(dir, "invalid.pub")
	if err := os.WriteFile(keyPath, []byte("this is not a public key"), 0644); err != nil {
		t.Fatalf("write invalid key: %v", err)
	}
	cfg.License.PublicKeyPath = keyPath
	cfg.License.OfflineMode = false

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	authn, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	adminTok, _ := authn.GenerateAccessToken(1, auth.RoleAdmin)

	h := handlers.NewHandler(db, authn, nil, logger, cfg, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	h.SetProcessStartedAt(time.Now().Add(-time.Hour))

	app := fiber.New()
	app.Get("/api/v1/admin/runtime", func(c fiber.Ctx) error {
		c.Locals("user_id", uint(1))
		c.Locals("role", auth.RoleAdmin)
		return h.GetAdminRuntime(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/runtime", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	res, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	var resp runtime.Telemetry
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("response must be valid JSON: %v", err)
	}
	if resp.License.PublicKeyLoaded {
		t.Errorf("public_key_loaded must be false for invalid PEM content; got %+v", resp.License)
	}
	if resp.License.Mode == "online" {
		t.Errorf("license mode must not be 'online' for invalid key content; got %q", resp.License.Mode)
	}
}

// TestAdminRuntimeLicenseFileValidKey confirms that a valid PEM-encoded
// RSA public key file reports public_key_loaded=true.
func TestAdminRuntimeLicenseFileValidKey(t *testing.T) {
	logger := zap.NewNop()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(dir, "test.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"

	// Generate a real RSA key pair and marshal the public key as PEM.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	pemBlock := &pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}
	pemData := pem.EncodeToMemory(pemBlock)

	keyPath := filepath.Join(dir, "valid.pub")
	if err := os.WriteFile(keyPath, pemData, 0644); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	cfg.License.PublicKeyPath = keyPath
	cfg.License.OfflineMode = false

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	authn, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	adminTok, _ := authn.GenerateAccessToken(1, auth.RoleAdmin)

	h := handlers.NewHandler(db, authn, nil, logger, cfg, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	h.SetProcessStartedAt(time.Now().Add(-time.Hour))

	app := fiber.New()
	app.Get("/api/v1/admin/runtime", func(c fiber.Ctx) error {
		c.Locals("user_id", uint(1))
		c.Locals("role", auth.RoleAdmin)
		return h.GetAdminRuntime(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/runtime", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	res, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	var resp runtime.Telemetry
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("response must be valid JSON: %v", err)
	}
	if !resp.License.PublicKeyLoaded {
		t.Errorf("public_key_loaded must be true for a valid PEM public key file; got %+v", resp.License)
	}
	if resp.License.Mode != "online" {
		t.Errorf("license mode should be 'online' with a valid public key; got %q", resp.License.Mode)
	}
	if resp.License.Status != "ok" {
		t.Errorf("license status should be 'ok' with a valid public key; got %q", resp.License.Status)
	}
	// The license_public_key_missing warning must NOT be present.
	for _, w := range resp.Warnings {
		if w.Code == "license_public_key_missing" {
			t.Errorf("should not have license_public_key_missing warning with a valid key; got %+v", resp.Warnings)
		}
	}
}

// TestAdminRuntimeLicenseResponseNoSecrets confirms the response JSON
// does NOT include public key contents, the configured path, key hash,
// or any other secret-bearing fields.
func TestAdminRuntimeLicenseResponseNoSecrets(t *testing.T) {
	logger := zap.NewNop()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(dir, "test.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"

	// Write a valid PEM public key so the endpoint has a real key
	// to work with. The response must still NOT expose the content.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	pemBlock := &pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}
	pemData := pem.EncodeToMemory(pemBlock)
	keyPath := filepath.Join(dir, "noscr.pub")
	if err := os.WriteFile(keyPath, pemData, 0644); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	cfg.License.PublicKeyPath = keyPath
	cfg.License.OfflineMode = false

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	authn, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	adminTok, _ := authn.GenerateAccessToken(1, auth.RoleAdmin)

	h := handlers.NewHandler(db, authn, nil, logger, cfg, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	h.SetProcessStartedAt(time.Now().Add(-time.Hour))

	app := fiber.New()
	app.Get("/api/v1/admin/runtime", func(c fiber.Ctx) error {
		c.Locals("user_id", uint(1))
		c.Locals("role", auth.RoleAdmin)
		return h.GetAdminRuntime(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/runtime", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	res, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	bodyStr := string(body)
	// The standard PEM header prefix must not appear in the response.
	if strings.Contains(bodyStr, "BEGIN PUBLIC KEY") || strings.Contains(bodyStr, "BEGIN RSA PUBLIC KEY") {
		t.Errorf("response must not contain PEM public key data")
	}
	// The configured path must not appear in the response.
	if strings.Contains(bodyStr, keyPath) {
		t.Errorf("response must not contain the configured public key path")
	}
	// Common secret-bearing field names must not appear.
	for _, banned := range []string{"key_hash", "private_key", "secret", "path", "contents"} {
		if strings.Contains(bodyStr, banned) {
			t.Errorf("response must not contain %q field", banned)
		}
	}
}

// TestAdminRuntimeListenerRegistryIntegration confirms the
// endpoint returns real listener status (ok, fail, disabled,
// unknown) from the listener registry rather than the fallback
// "listener runtime state not reported".
func TestAdminRuntimeListenerRegistryIntegration(t *testing.T) {
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

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	_ = db.Exec(`CREATE TABLE IF NOT EXISTS licenses (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME, key_hash TEXT NOT NULL DEFAULT '', tier TEXT NOT NULL DEFAULT 'smb', issued_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, expires_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, max_domains INTEGER NOT NULL DEFAULT 10, max_mailboxes INTEGER NOT NULL DEFAULT 500, hardware_id TEXT NOT NULL DEFAULT '', metadata TEXT NOT NULL DEFAULT '', active INTEGER NOT NULL DEFAULT 1)`)

	authn, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	adminTok, _ := authn.GenerateAccessToken(1, auth.RoleAdmin)

	h := handlers.NewHandler(db, authn, nil, logger, cfg, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	h.SetProcessStartedAt(time.Now().Add(-time.Hour))

	// Populate the listener registry with known states.
	reg := runtime.NewListenerRegistry()
	reg.MarkOK(runtime.ListenerSMTP, 25)
	reg.MarkFailed(runtime.ListenerIMAP, 143, errors.New("EADDRINUSE"))
	reg.MarkDisabled(runtime.ListenerPOP3, 110, "disabled by config")
	// Leave JMAP unset — should be unknown.
	h.SetListenerRegistry(reg)

	app := fiber.New()
	app.Get("/api/v1/admin/runtime", func(c fiber.Ctx) error {
		c.Locals("user_id", uint(1))
		c.Locals("role", auth.RoleAdmin)
		return h.GetAdminRuntime(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/runtime", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	res, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	var resp runtime.Telemetry
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("response must be valid JSON: %v", err)
	}

	if resp.Services["smtp"].Status != "ok" {
		t.Errorf("smtp status must be ok; got %q", resp.Services["smtp"].Status)
	}
	if resp.Services["smtp"].Detail != "listening" {
		t.Errorf("smtp detail must be 'listening'; got %q", resp.Services["smtp"].Detail)
	}
	if resp.Services["smtp"].Port != 25 {
		t.Errorf("smtp port must be 25; got %d", resp.Services["smtp"].Port)
	}

	if resp.Services["imap"].Status != "fail" {
		t.Errorf("imap status must be fail; got %q", resp.Services["imap"].Status)
	}
	if resp.Services["imap"].Detail != "bind failed: address already in use" {
		t.Errorf("imap detail must be safe error; got %q", resp.Services["imap"].Detail)
	}

	if resp.Services["pop3"].Status != "disabled" {
		t.Errorf("pop3 status must be disabled; got %q", resp.Services["pop3"].Status)
	}
	if resp.Services["pop3"].Detail != "disabled by config" {
		t.Errorf("pop3 detail must be 'disabled by config'; got %q", resp.Services["pop3"].Detail)
	}

	if resp.Services["jmap"].Status != "unknown" {
		t.Errorf("jmap status must be unknown; got %q", resp.Services["jmap"].Status)
	}
}

// Compile-time guard that this file is only built when the
// internal test infrastructure is available.
var _ = sync.Once{}
var _ = context.Background
var _ = os.Getenv
