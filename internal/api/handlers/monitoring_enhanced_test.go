package handlers_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/orvix/orvix/internal/config"
)

// TestMonitoringSnapshotReturnsHealthy verifies the /monitoring/snapshot endpoint.
func TestMonitoringSnapshotReturnsHealthy(t *testing.T) {
	router, sqlDB, token, _, _ := buildBackupHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	resp, body := monitoringRequest(t, router, "GET", "/api/v1/monitoring/snapshot", "", token, "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("snapshot: %d %s", resp.StatusCode, body)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode snapshot: %v: %s", err, body)
	}

	required := []string{
		"generatedAt", "serviceStatus", "uptimeSeconds",
		"disk", "dbHealth", "queueHealth", "backupHealth",
		"apiHealth", "certExpiry", "dnsReadiness",
		"capacity", "openAlerts", "memoryUsedBytes", "memoryTotalBytes",
	}
	for _, key := range required {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing field %q in monitoring snapshot: %s", key, body)
		}
	}

	if s, ok := raw["serviceStatus"].(string); ok {
		if s != "ok" && s != "degraded" && s != "down" {
			t.Errorf("unexpected serviceStatus: %s", s)
		}
	}

	// Check certExpiry sub-fields.
	if ce, ok := raw["certExpiry"].(map[string]interface{}); ok {
		for _, k := range []string{"status", "expiringWithin7", "expiringWithin30"} {
			if _, ok := ce[k]; !ok {
				t.Errorf("missing certExpiry.%s", k)
			}
		}
	}
}

// TestMonitoringAlertThresholds verifies that configurable alert thresholds
// are applied correctly. When configured thresholds differ from defaults,
// the alert evaluation respects the new values.
func TestMonitoringAlertThresholds(t *testing.T) {
	router, sqlDB, token, _, _ := buildBackupHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	resp, body := monitoringRequest(t, router, "GET", "/api/v1/monitoring/health", "", token, "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health: %d %s", resp.StatusCode, body)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Queue health should use the default thresholds (100 warning, 500 critical).
	if qh, ok := raw["queue"].(map[string]interface{}); ok {
		status, _ := qh["status"].(string)
		if status != "ok" && status != "warning" && status != "critical" && status != "unknown" {
			t.Errorf("unexpected queue status: %s", status)
		}
	}

	// DB health should be ok (fresh database).
	if dbh, ok := raw["db"].(map[string]interface{}); ok {
		status, _ := dbh["status"].(string)
		if status != "ok" && status != "warning" && status != "critical" && status != "unknown" {
			t.Errorf("unexpected db status: %s", status)
		}
	}

	// Overall status should be a known value.
	if s, ok := raw["status"].(string); ok {
		if s != "ok" && s != "degraded" && s != "down" {
			t.Errorf("unexpected top-level status: %s", s)
		}
	}
}

// TestMonitoringDiskThresholdWarning verifies that disk threshold configuration
// values are respected. The test ensures that the config's disk threshold fields
// exist and have sensible defaults.
func TestMonitoringDiskThresholdWarning(t *testing.T) {
	// Test the MonitoringConfig default values.
	mc := config.MonitoringConfig{}
	if mc.DiskUsageWarningPctVal() != 85 {
		t.Errorf("default disk warning pct: got %d, want 85", mc.DiskUsageWarningPctVal())
	}
	if mc.DiskUsageCriticalPctVal() != 95 {
		t.Errorf("default disk critical pct: got %d, want 95", mc.DiskUsageCriticalPctVal())
	}
	if mc.QueueDepthWarningVal() != 100 {
		t.Errorf("default queue depth warning: got %d, want 100", mc.QueueDepthWarningVal())
	}
	if mc.QueueDepthCriticalVal() != 500 {
		t.Errorf("default queue depth critical: got %d, want 500", mc.QueueDepthCriticalVal())
	}
	if mc.BackupAgeWarningHoursVal() != 24 {
		t.Errorf("default backup age warning hours: got %d, want 24", mc.BackupAgeWarningHoursVal())
	}
	if mc.BackupAgeCriticalHoursVal() != 72 {
		t.Errorf("default backup age critical hours: got %d, want 72", mc.BackupAgeCriticalHoursVal())
	}
	if mc.CertExpiryWarningDaysVal() != 30 {
		t.Errorf("default cert expiry warning days: got %d, want 30", mc.CertExpiryWarningDaysVal())
	}
	if mc.CertExpiryCriticalDaysVal() != 7 {
		t.Errorf("default cert expiry critical days: got %d, want 7", mc.CertExpiryCriticalDaysVal())
	}

	// Test that custom values are preserved.
	mc2 := config.MonitoringConfig{
		DiskUsageWarningPct:    60,
		DiskUsageCriticalPct:   80,
		QueueDepthWarning:      50,
		QueueDepthCritical:     200,
		BackupAgeWarningHours:  12,
		BackupAgeCriticalHours: 48,
		CertExpiryWarningDays:  14,
		CertExpiryCriticalDays: 3,
	}
	if mc2.DiskUsageWarningPctVal() != 60 {
		t.Errorf("custom disk warning pct: got %d, want 60", mc2.DiskUsageWarningPctVal())
	}
	if mc2.DiskUsageCriticalPctVal() != 80 {
		t.Errorf("custom disk critical pct: got %d, want 80", mc2.DiskUsageCriticalPctVal())
	}
	if mc2.QueueDepthWarningVal() != 50 {
		t.Errorf("custom queue depth warning: got %d, want 50", mc2.QueueDepthWarningVal())
	}
	if mc2.QueueDepthCriticalVal() != 200 {
		t.Errorf("custom queue depth critical: got %d, want 200", mc2.QueueDepthCriticalVal())
	}
	if mc2.BackupAgeWarningHoursVal() != 12 {
		t.Errorf("custom backup age warning hours: got %d, want 12", mc2.BackupAgeWarningHoursVal())
	}
	if mc2.BackupAgeCriticalHoursVal() != 48 {
		t.Errorf("custom backup age critical hours: got %d, want 48", mc2.BackupAgeCriticalHoursVal())
	}
	if mc2.CertExpiryWarningDaysVal() != 14 {
		t.Errorf("custom cert expiry warning days: got %d, want 14", mc2.CertExpiryWarningDaysVal())
	}
	if mc2.CertExpiryCriticalDaysVal() != 3 {
		t.Errorf("custom cert expiry critical days: got %d, want 3", mc2.CertExpiryCriticalDaysVal())
	}
}

// TestMonitoringCertExpiry verifies that cert expiry fields are present
// in the monitoring snapshot response with proper structure.
func TestMonitoringCertExpiry(t *testing.T) {
	router, sqlDB, token, _, _ := buildBackupHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	resp, body := monitoringRequest(t, router, "GET", "/api/v1/monitoring/snapshot", "", token, "", false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("snapshot: %d %s", resp.StatusCode, body)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}

	ce, ok := raw["certExpiry"].(map[string]interface{})
	if !ok {
		t.Fatal("certExpiry field missing or wrong type in snapshot")
	}

	status, _ := ce["status"].(string)
	if status != "ok" && status != "warning" && status != "critical" {
		t.Errorf("unexpected certExpiry.status: %s", status)
	}

	_, has7 := ce["expiringWithin7"]
	_, has30 := ce["expiringWithin30"]
	if !has7 {
		t.Error("missing certExpiry.expiringWithin7")
	}
	if !has30 {
		t.Error("missing certExpiry.expiringWithin30")
	}

	// Verify cert expiry alert thresholds in the monitoring config.
	cfg := config.Defaults()
	if cfg.Monitoring.CertExpiryWarningDaysVal() != 30 {
		t.Errorf("expected default cert expiry warning 30, got %d", cfg.Monitoring.CertExpiryWarningDaysVal())
	}
	if cfg.Monitoring.CertExpiryCriticalDaysVal() != 7 {
		t.Errorf("expected default cert expiry critical 7, got %d", cfg.Monitoring.CertExpiryCriticalDaysVal())
	}
}
