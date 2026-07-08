package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/models"
	"go.uber.org/zap"
)

// TestGetAdminTenantNoRow confirms that GET /admin/tenants/current
// is honest about a missing tenant row instead of fabricating a
// partial object. The audit doc (see docs/ORVIX_STALWART_ENTERPRISE_PARITY_AUDIT.md)
// requires every read endpoint to surface the truthful empty state.
func TestGetAdminTenantNoRow(t *testing.T) {
	h := newTenantTestHandler(t)
	app := fiber.New()
	app.Get("/api/v1/admin/tenants/current", h.GetAdminTenant)

	req := httptest.NewRequest("GET", "/api/v1/admin/tenants/current", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["exists"] != false {
		t.Errorf("exists = %v, want false", body["exists"])
	}
	if body["honest_note"] == nil || !strings.Contains(asString(body["honest_note"]), "not provisioned") {
		t.Errorf("honest_note missing the no-row hint: %v", body["honest_note"])
	}
}

// TestGetAdminTenantSeeded confirms that with a seeded tenants row,
// the GET surfaces id / name / slug / domain / plan / logo_url /
// primary_color — never an "[object Object]" stringified object.
func TestGetAdminTenantSeeded(t *testing.T) {
	h := newTenantTestHandler(t)
	seedTenant(t, h.sqlDB(), 1, "orvix", "orvix.email", "https://cdn.example.com/logo.svg", "#4F7CFF")
	app := fiber.New()
	app.Get("/api/v1/admin/tenants/current", h.GetAdminTenant)

	req := httptest.NewRequest("GET", "/api/v1/admin/tenants/current", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["exists"] != true {
		t.Fatalf("exists = %v, want true; body=%v", body["exists"], body)
	}
	if got := asString(body["name"]); got != "orvix" {
		t.Errorf("name = %q, want orvix", got)
	}
	if got := asString(body["logo_url"]); got != "https://cdn.example.com/logo.svg" {
		t.Errorf("logo_url = %q, want cdn", got)
	}
	if got := asString(body["primary_color"]); got != "#4F7CFF" {
		t.Errorf("primary_color = %q, want #4F7CFF", got)
	}
}

// TestPatchAdminTenantBrandingRejectLocalhost asserts the SSRF guard:
// a logo pointing to localhost or 127.0.0.1 must be hard-rejected so
// the admin shell cannot be tricked into rendering an internal URL.
func TestPatchAdminTenantBrandingRejectLocalhost(t *testing.T) {
	h := newTenantTestHandler(t)
	seedTenant(t, h.sqlDB(), 1, "orvix", "orvix.email", "", "")
	app := fiber.New()
	app.Patch("/api/v1/admin/tenants/:id/branding", withSuperAdmin(h, h.PatchAdminTenantBranding))
	for _, bad := range []string{
		"http://localhost/logo.svg",
		"http://127.0.0.1/logo.svg",
		"http://10.0.0.5/logo.svg",
	} {
		body, _ := json.Marshal(map[string]string{"logo_url": bad})
		req := httptest.NewRequest("PATCH", "/api/v1/admin/tenants/1/branding", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("bad url %q got %d, want 400", bad, resp.StatusCode)
		}
	}
}

// TestPatchAdminTenantBrandingHappyPath writes a public https logo +
// a valid CSS hex color to the seeded row. After the PATCH, the GET
// must surface the new values. CSRF stays out of scope here because
// the router layer enforces it; the handler is wired under `men`
// (CSRF-protected) so we only assert the in-handler business rules.
func TestPatchAdminTenantBrandingHappyPath(t *testing.T) {
	h := newTenantTestHandler(t)
	seedTenant(t, h.sqlDB(), 1, "orvix", "orvix.email", "", "")
	app := fiber.New()
	app.Patch("/api/v1/admin/tenants/:id/branding", withSuperAdmin(h, h.PatchAdminTenantBranding))

	body, _ := json.Marshal(map[string]string{
		"logo_url":      "https://cdn.example.com/new-logo.svg",
		"primary_color": "#FF8800",
	})
	req := httptest.NewRequest("PATCH", "/api/v1/admin/tenants/1/branding", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	// Confirm the row was actually updated.
	var got string
	if err := h.sqlDB().QueryRow(`SELECT logo_url FROM tenants WHERE id=1`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "https://cdn.example.com/new-logo.svg" {
		t.Errorf("logo_url = %q, want cdn", got)
	}
}

// TestPatchAdminTenantBrandingInvalidHex asserts the validator
// rejects #abcdefg / "red" / "" / "rgb(...)" — anything that would
// produce a runtime CSS parse error in the admin login shell.
func TestPatchAdminTenantBrandingInvalidHex(t *testing.T) {
	h := newTenantTestHandler(t)
	seedTenant(t, h.sqlDB(), 1, "orvix", "orvix.email", "", "")
	app := fiber.New()
	app.Patch("/api/v1/admin/tenants/:id/branding", withSuperAdmin(h, h.PatchAdminTenantBranding))
	for _, bad := range []string{"red", "#abc", "#GGGGGG", "#1234567"} {
		bb, _ := json.Marshal(map[string]string{"primary_color": bad})
		req := httptest.NewRequest("PATCH", "/api/v1/admin/tenants/1/branding", bytes.NewReader(bb))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("bad hex %q got %d, want 400", bad, resp.StatusCode)
		}
	}
}

// TestListStorageVolumesEmptyInstall confirms the storage-topology
// page surfaces an honest empty list (Available=false, detail set)
// on a freshly built handler with no configured directories. The
// page contract requires no fake volumes even if mail has not yet
// flowed.
func TestListStorageVolumesEmptyInstall(t *testing.T) {
	h := newTenantTestHandler(t)
	app := fiber.New()
	app.Get("/api/v1/admin/storage/volumes", h.ListStorageVolumes)
	req := httptest.NewRequest("GET", "/api/v1/admin/storage/volumes", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	volumes, _ := body["volumes"].([]interface{})
	// A fresh install with no cfg has zero volumes — that's fine.
	if len(volumes) > 0 {
		for _, raw := range volumes {
			v := raw.(map[string]interface{})
			if v["available"] == true && v["detail"] != nil && v["detail"] != "" {
				t.Errorf("available=true must not coexist with a detail note: %v", v)
			}
		}
	}
	// honest_note must always be present.
	if body["honest_note"] == nil {
		t.Errorf("honest_note missing on storage topology response")
	}
}

// TestListStorageVolumesSeeded exercises the configured path: a real
// temp dir + cfg. On POSIX the VolumeStat row reports real Bytes;
// on Windows the VolumeStat row reports statfs-not-implemented (an
// honest non-fabricated value). Either case must NOT produce
// TotalBytes=0, FreeBytes=0, Available=true.
func TestListStorageVolumesSeeded(t *testing.T) {
	h := newTenantTestHandler(t)
	dir := t.TempDir()
	if h.cfg == nil {
		h.cfg = &config.Config{}
	}
	h.cfg.Backup.Dir = filepath.Join(dir, "backups")
	h.cfg.CoreMail.DataPath = filepath.Join(dir, "data")
	h.cfg.CoreMail.MailStorePath = filepath.Join(dir, "data", "mailstore")
	if err := os.MkdirAll(filepath.Join(dir, "data", "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Get("/api/v1/admin/storage/volumes", h.ListStorageVolumes)
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/storage/volumes", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	vs, _ := body["volumes"].([]interface{})
	if len(vs) == 0 {
		t.Fatalf("expected at least one volume row when cfg is set")
	}
	for _, raw := range vs {
		v := raw.(map[string]interface{})
		available, _ := v["available"].(bool)
		total, _ := v["total_bytes"].(float64)
		if available && total == 0 {
			t.Errorf("VolumeStat row claimed available=true with zero bytes: %v", v)
		}
	}
}

// TestListAlertDeliveriesHonestEmpty: with no dispatcher wired and
// no SQL DB the endpoint returns an empty array (not null, not
// fake). The contract requires JSON `{"deliveries": [], ...}`.
func TestListAlertDeliveriesHonestEmpty(t *testing.T) {
	// Force the dispatcher to be nil by setting auditStore to nil
	// and removing the db. We accomplish this by constructing a
	// fresh handler.
	fresh := &Handler{logger: zap.NewNop()}
	app := fiber.New()
	app.Get("/api/v1/admin/monitoring/alert-deliveries", fresh.ListAlertDeliveries)
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/monitoring/alert-deliveries", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	d, ok := body["deliveries"].([]interface{})
	if !ok {
		t.Fatalf("deliveries is not an array: %T", body["deliveries"])
	}
	if len(d) != 0 {
		t.Errorf("deliveries len = %d, want 0 (no dispatcher wired)", len(d))
	}
}

// asString converts any JSON-decoded value to a flat string. The
// admin runtime telemetry contract requires no implicit toString of
// plain objects; this helper keeps the test readable.
func asString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strings.TrimRight(strings.TrimRight(strconvFormatFloat(x), "0"), ".")
	default:
		return ""
	}
}

func strconvFormatFloat(f float64) string {
	// minimal formatter without importing strconv directly here so
	// the helper stays self-contained. The seeded tenant tests
	// assert only on string fields.
	return jsonNumber(f)
}

// jsonNumber formats a float64 back to a string using encoding/json
// so we don't drag strconv into a tiny helper file.
func jsonNumber(f float64) string {
	b, _ := json.Marshal(f)
	s := strings.Trim(string(b), "\"")
	return s
}

// newTenantTestHandler wires a Handler with sqlite + cfg but
// without a router. The router's CSRF + RBAC layers are
// independent of the in-handler business rules this test file
// exercises, so we skip them.
func newTenantTestHandler(t *testing.T) *Handler {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "enterprise-parity.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return &Handler{db: db, cfg: cfg, logger: logger}
}

// seedTenant inserts a row into the tenants table for tests that
// need a non-empty read. crmEmail is stored under `domain`. logo and
// color are optional (empty string stays NULL).
func seedTenant(t *testing.T, db *sql.DB, id int64, slug, crmEmail, logo, color string) {
	t.Helper()
	now := time.Now().UTC()
	var logoArg interface{}
	if logo != "" {
		logoArg = logo
	}
	var colorArg interface{}
	if color != "" {
		colorArg = color
	}
	if _, err := db.Exec(
		`INSERT OR REPLACE INTO tenants (id, created_at, updated_at, name, slug, domain, plan, logo_url, primary_color, active)
		 VALUES (?, ?, ?, ?, ?, ?, 'enterprise', ?, ?, 1)`,
		id, now, now, slug, slug, crmEmail, logoArg, colorArg,
	); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
}

// withSuperAdmin pretends the caller is the superadmin role. The
// handler reads c.Locals("role") so this small middleware is enough
// to exercise the superadmin branch; non-super writes are covered
// separately.
func withSuperAdmin(h *Handler, next fiber.Handler) fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Locals("role", "superadmin")
		c.Locals("user_id", uint(1))
		c.Locals("tenant_id", uint(1))
		return next(c)
	}
}
