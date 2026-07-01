package handlers_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api"
)

// monitoringRequest is a thin wrapper around the backup test request
// helper so the monitoring tests can stay focused on assertions.
func monitoringRequest(t *testing.T, router *api.Router, method, path, body, token, csrf string, withCSRF bool) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if withCSRF && csrf != "" {
		req.Header.Set("Cookie", "csrf_token="+csrf)
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

// TestMonitoringV1_HealthReturnsSafeFields verifies that the /monitoring/health
// response contains only safe, non-secret fields.
//
// Security: the response MUST NOT contain env values, secret tokens,
// file contents, or private absolute filesystem paths.
func TestMonitoringV1_HealthReturnsSafeFields(t *testing.T) {
	router, sqlDB, token, _, _ := buildBackupHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	resp, body := monitoringRequest(t, router, "GET", "/api/v1/monitoring/health", "", token, "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health: %d %s", resp.StatusCode, body)
	}

	// Decode into a permissive struct so we can introspect the
	// raw keys without depending on the precise field list.
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode: %v: %s", err, body)
	}

	// Required top-level fields.
	for _, key := range []string{"status", "uptimeSeconds", "generatedAt", "disk", "db", "queue", "backup", "api", "capacity", "openAlerts"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing top-level field %q in /monitoring/health: %s", key, body)
		}
	}

	// Status must be one of the documented safe values.
	if s, _ := raw["status"].(string); s != "ok" && s != "degraded" && s != "down" {
		t.Errorf("status not in {ok,degraded,down}: %v", raw["status"])
	}

	// Banned substrings in the whole response.
	banned := []string{
		"Bearer ", "bearer ", "Bearer:",
		"password=", "password:",
		"jwt=", "jwt:",
		"secret=", "secret:",
		"AKIA", // AWS key prefix
		"-----BEGIN",
		"/etc/orvix/orvix.yaml", // a real private config path
		"private_key",
		"PRIVATE KEY",
		"DATABASE_URL",
		"ORVIX_DB_DSN",
		"x-api-key",
	}
	for _, b := range banned {
		if strings.Contains(string(body), b) {
			t.Fatalf("health response leaks forbidden token %q: %s", b, body)
		}
	}

	// Disk labels must be safe labels (not absolute paths).
	if disk, ok := raw["disk"].([]interface{}); ok {
		for i, entry := range disk {
			m, _ := entry.(map[string]interface{})
			if m == nil {
				t.Fatalf("disk[%d] not an object: %+v", i, entry)
			}
			label, _ := m["label"].(string)
			if label == "" {
				t.Errorf("disk[%d] missing label", i)
			}
			// Banned label patterns.
			if strings.ContainsAny(label, `/\`) || strings.Contains(label, "..") {
				t.Fatalf("disk[%d] label has path chars: %q", i, label)
			}
			// label must not look like an absolute Windows or POSIX path.
			if strings.HasPrefix(label, "C:") || strings.HasPrefix(label, "/") {
				t.Fatalf("disk[%d] label is an absolute path: %q", i, label)
			}
		}
	}
}

// TestMonitoringV1_HealthAuth verifies /monitoring/health requires a
// valid token (admin role). A request with no token must 401/403.
func TestMonitoringV1_HealthAuth(t *testing.T) {
	router, sqlDB, _, _, _ := buildBackupHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	resp, _ := monitoringRequest(t, router, "GET", "/api/v1/monitoring/health", "", "", "", false)
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 401/403 for unauth health, got %d", resp.StatusCode)
	}
}

// TestMonitoringV1_AlertsListReturnsSafeFields verifies /monitoring/alerts
// renders alert rows with safe fields only. Each alert must have
// id, category, severity, title, message, source, active, createdAt —
// and no field must leak env values or private paths.
func TestMonitoringV1_AlertsListReturnsSafeFields(t *testing.T) {
	router, sqlDB, token, _, _ := buildBackupHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	// Create the schema for the seed. The handler does this on the
	// first request, but we want a deterministic seed path here.
	if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS monitoring_alerts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		category TEXT NOT NULL DEFAULT '',
		severity TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL DEFAULT '',
		message TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL DEFAULT '',
		active INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL,
		resolved_at DATETIME
	)`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO monitoring_alerts (category, severity, title, message, source, active, created_at) VALUES ('runtime', 'critical', 'SMTP unhealthy', 'SMTP service is not healthy', 'runtime', 1, datetime('now'))`); err != nil {
		t.Fatalf("seed alert: %v", err)
	}

	resp, body := monitoringRequest(t, router, "GET", "/api/v1/monitoring/alerts", "", token, "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("alerts: %d %s", resp.StatusCode, body)
	}
	var out struct {
		Alerts []map[string]interface{} `json:"alerts"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v: %s", err, body)
	}
	if len(out.Alerts) == 0 {
		t.Fatalf("expected at least one alert, got 0: %s", body)
	}
	for i, a := range out.Alerts {
		for _, key := range []string{"id", "category", "severity", "title", "message", "source", "active", "createdAt"} {
			if _, ok := a[key]; !ok {
				t.Errorf("alert[%d] missing field %q: %+v", i, key, a)
			}
		}
		msg, _ := a["message"].(string)
		for _, banned := range []string{"Bearer", "password=", "secret=", "AKIA", "PRIVATE KEY", "/etc/orvix", "C:\\"} {
			if strings.Contains(msg, banned) {
				t.Fatalf("alert[%d] message leaks forbidden token %q: %q", i, banned, msg)
			}
		}
	}
}

// TestMonitoringV1_ResolveRequiresCSRF verifies the POST resolve route
// is CSRF-protected. A request with valid auth but no CSRF token must
// be rejected (403 or 401 depending on how the CSRF middleware
// signals failure). A request with a valid CSRF token must succeed.
func TestMonitoringV1_ResolveRequiresCSRF(t *testing.T) {
	router, sqlDB, token, csrf, _ := buildBackupHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS monitoring_alerts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		category TEXT NOT NULL DEFAULT '',
		severity TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL DEFAULT '',
		message TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL DEFAULT '',
		active INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL,
		resolved_at DATETIME
	)`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO monitoring_alerts (category, severity, title, message, source, active, created_at) VALUES ('runtime', 'critical', 'SMTP unhealthy', 'SMTP service is not healthy', 'runtime', 1, datetime('now'))`); err != nil {
		t.Fatalf("seed alert: %v", err)
	}
	var alertID int64
	row := sqlDB.QueryRow(`SELECT id FROM monitoring_alerts ORDER BY id DESC LIMIT 1`)
	if err := row.Scan(&alertID); err != nil {
		t.Fatalf("read alert id: %v", err)
	}
	idStr := itoa(alertID)

	// No CSRF: must be rejected.
	resp, _ := monitoringRequest(t, router, "POST", "/api/v1/monitoring/alerts/"+idStr+"/resolve", "", token, "", false)
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("resolve without CSRF must not return 200, got %d", resp.StatusCode)
	}

	// With CSRF: must succeed.
	resp, body := monitoringRequest(t, router, "POST", "/api/v1/monitoring/alerts/"+idStr+"/resolve", "", token, csrf, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resolve with CSRF: %d %s", resp.StatusCode, body)
	}
	var out struct {
		Status string `json:"status"`
		ID     uint   `json:"id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode resolve: %v: %s", err, body)
	}
	if out.Status != "resolved" {
		t.Fatalf("status: %q (want resolved)", out.Status)
	}

	// Re-resolve must 404.
	resp2, _ := monitoringRequest(t, router, "POST", "/api/v1/monitoring/alerts/"+idStr+"/resolve", "", token, csrf, true)
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("re-resolve: %d (want 404)", resp2.StatusCode)
	}
}

// TestMonitoringV1_ResolveInvalidIDRejected verifies the resolve route
// rejects non-numeric or zero ids.
func TestMonitoringV1_ResolveInvalidIDRejected(t *testing.T) {
	router, sqlDB, token, csrf, _ := buildBackupHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	for _, badID := range []string{"abc", "0", "-1", "99999999999999999999"} {
		resp, _ := monitoringRequest(t, router, "POST", "/api/v1/monitoring/alerts/"+badID+"/resolve", "", token, csrf, true)
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("resolve(%q) must not return 200, got %d", badID, resp.StatusCode)
		}
	}
}

// TestMonitoringAlertProvidersReportsHonestly verifies the
// /monitoring/alert-providers endpoint reports the configured delivery
// providers honestly and never leaks a secret. The default backup
// harness configures no webhook, so only the always-on in-app provider
// is present.
func TestMonitoringAlertProvidersReportsHonestly(t *testing.T) {
	router, sqlDB, token, _, _ := buildBackupHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	resp, body := monitoringRequest(t, router, "GET", "/api/v1/monitoring/alert-providers", "", token, "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("alert-providers: %d %s", resp.StatusCode, body)
	}

	var payload struct {
		Providers []struct {
			Name      string `json:"name"`
			Enabled   bool   `json:"enabled"`
			Target    string `json:"target"`
			HasSecret bool   `json:"hasSecret"`
		} `json:"providers"`
		Deliveries []map[string]interface{} `json:"deliveries"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v: %s", err, body)
	}

	var haveInApp bool
	for _, p := range payload.Providers {
		if p.Name == "inapp" {
			haveInApp = true
			if !p.Enabled {
				t.Errorf("inapp provider must be enabled")
			}
		}
		if strings.Contains(p.Target, "http://") || strings.Contains(p.Target, "https://") {
			t.Fatalf("provider %q leaked a URL in target: %q", p.Name, p.Target)
		}
	}
	if !haveInApp {
		t.Fatalf("expected the always-on inapp provider, got %s", body)
	}

	for _, b := range []string{"Bearer ", "Authorization", "-----BEGIN", "PRIVATE KEY"} {
		if strings.Contains(string(body), b) {
			t.Fatalf("alert-providers response leaked %q: %s", b, body)
		}
	}
}

// itoa is a small helper so the test file does not pull in strconv
// for a single conversion.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
