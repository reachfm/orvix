package runtime

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestNewTelemetryHonestDefaults confirms that when the caller
// supplies zero values, the response carries the honest defaults
// the dashboard requires — never a fabricated "Online" or "ok".
func TestNewTelemetryHonestDefaults(t *testing.T) {
	tel := NewTelemetry(Inputs{})
	if tel.Status == "ok" {
		t.Errorf("status with zero inputs must not be 'ok' (telemetry incomplete); got %q", tel.Status)
	}
	if tel.Commit != "not reported" {
		t.Errorf("commit default must be 'not reported'; got %q", tel.Commit)
	}
	if tel.BuildTime != "not reported" {
		t.Errorf("build_time default must be 'not reported'; got %q", tel.BuildTime)
	}
	if tel.Version != "unknown" {
		t.Errorf("version default must be 'unknown'; got %q", tel.Version)
	}
	if tel.UptimeSeconds != 0 {
		t.Errorf("uptime_seconds with no StartedAt must be 0; got %d", tel.UptimeSeconds)
	}
}

// TestNewTelemetryNoSecrets confirms the response never includes
// the typical secret-bearer fields. This is a static guard against
// the package growing fields like "token", "key", "password",
// "private_key" or env-dump content.
func TestNewTelemetryNoSecrets(t *testing.T) {
	tel := NewTelemetry(Inputs{
		Version:   "1.0.0",
		Commit:    "abc123",
		BuildTime: "development",
		StartedAt: time.Now().Add(-time.Hour),
		DataPath:  "/var/lib/orvix/data",
		DBPing:    func() error { return nil },
		QueueCounts: QueueCounts{Pending: 1, Deferred: 0, Bounced: 0, Delivered: 0},
		License:   LicensePosture{Mode: "offline", PublicKeyLoaded: true, Status: "ok"},
		SMHTTPPort: 25, IMAPPort: 143, POP3Port: 110, JMAPPort: 8080,
		HostnameFn: func() (string, error) { return "mail.example.com", nil },
	})
	b, err := json.Marshal(tel)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := strings.ToLower(string(b))
	for _, banned := range []string{
		"password", "passwd", "secret", "private_key", "priv_key",
		"api_key", "apikey", "access_token", "refresh_token",
		"jwt_secret", "jwt_secret", "auth_token",
		"env", "env_dump", "environ", "os.env",
	} {
		if strings.Contains(s, banned) {
			t.Errorf("telemetry JSON must not contain %q; got %s", banned, string(b))
		}
	}
	// The DataPath itself must not appear in the response.
	if strings.Contains(string(b), "/var/lib/orvix/data") {
		t.Errorf("telemetry JSON must not echo the absolute data path; got %s", string(b))
	}
}

// TestNewTelemetryHostname confirms the hostname is read safely
// and surfaces "unknown" on error rather than panicking.
func TestNewTelemetryHostname(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		tel := NewTelemetry(Inputs{HostnameFn: func() (string, error) { return "mx-1", nil }})
		if tel.Hostname != "mx-1" {
			t.Errorf("hostname: want %q got %q", "mx-1", tel.Hostname)
		}
	})
	t.Run("error", func(t *testing.T) {
		tel := NewTelemetry(Inputs{HostnameFn: func() (string, error) { return "", errors.New("nope") }})
		if tel.Hostname != "unknown" {
			t.Errorf("hostname on error: want %q got %q", "unknown", tel.Hostname)
		}
	})
	t.Run("empty", func(t *testing.T) {
		tel := NewTelemetry(Inputs{HostnameFn: func() (string, error) { return "", nil }})
		if tel.Hostname != "unknown" {
			t.Errorf("hostname on empty: want %q got %q", "unknown", tel.Hostname)
		}
	})
}

// TestNewTelemetryUptime confirms the uptime is computed from
// StartedAt and clamped to zero for negative durations.
func TestNewTelemetryUptime(t *testing.T) {
	now := time.Now()
	t.Run("one hour ago", func(t *testing.T) {
		tel := NewTelemetry(Inputs{StartedAt: now.Add(-time.Hour)})
		// Allow 5s skew for test runtime.
		if tel.UptimeSeconds < 3600-5 || tel.UptimeSeconds > 3600+5 {
			t.Errorf("uptime: want ~3600 got %d", tel.UptimeSeconds)
		}
		if tel.StartedAt == "" {
			t.Errorf("started_at must be populated when StartedAt is set")
		}
	})
	t.Run("future start clamps to zero", func(t *testing.T) {
		tel := NewTelemetry(Inputs{StartedAt: now.Add(time.Hour)})
		if tel.UptimeSeconds != 0 {
			t.Errorf("future-started uptime must clamp to 0; got %d", tel.UptimeSeconds)
		}
	})
}

// TestNewTelemetryDisk confirms the disk label is safe and the
// values are passed through from the supplied function.
func TestNewTelemetryDisk(t *testing.T) {
	tel := NewTelemetry(Inputs{
		DataPath: "/var/lib/orvix/data",
		DiskFn: func(path string) Disk {
			return Disk{Label: "data", TotalBytes: 100, UsedBytes: 40, FreeBytes: 60, UsedPercent: 40}
		},
	})
	if tel.Capacity.Disk.TotalBytes != 100 || tel.Capacity.Disk.UsedBytes != 40 {
		t.Errorf("disk passthrough: want 100/40 got %d/%d", tel.Capacity.Disk.TotalBytes, tel.Capacity.Disk.UsedBytes)
	}
	if tel.Capacity.Disk.UsedPercent != 40 {
		t.Errorf("disk used percent: want 40 got %d", tel.Capacity.Disk.UsedPercent)
	}
	if tel.Capacity.Disk.Label != "data" {
		t.Errorf("disk label: want %q got %q", "data", tel.Capacity.Disk.Label)
	}
}

// TestSafeDiskLabel confirms the helper rejects absolute paths
// even when the caller passes one as the "hint".
func TestSafeDiskLabel(t *testing.T) {
	cases := map[string]struct {
		dataPath, hinted, want string
	}{
		"empty":      {"", "", "data"},
		"data only":  {"/var/lib/orvix/data", "", "data"},
		"good hint":  {"/var/lib/orvix/data", "mailstore", "mailstore"},
		"abs hint":   {"/var/lib/orvix/data", "/etc/passwd", "data"},
		"win abs":    {"C:\\data", "C:\\secret", "data"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := safeDiskLabel(c.dataPath, c.hinted); got != c.want {
				t.Errorf("safeDiskLabel(%q,%q)=%q want %q", c.dataPath, c.hinted, got, c.want)
			}
		})
	}
}

// TestServicesNoFakeOnline confirms the listener entries never
// report "ok" when listener-runtime state is not tracked. The
// API service reports "ok" because the very fact this function
// is being called proves the API responded. The database and
// queue services use the live callbacks.
func TestServicesNoFakeOnline(t *testing.T) {
	tel := NewTelemetry(Inputs{
		StartedAt:  time.Now().Add(-time.Minute),
		SMHTTPPort: 25, IMAPPort: 143, POP3Port: 110, JMAPPort: 8080,
	})
	for _, name := range []string{"smtp", "imap", "pop3", "jmap"} {
		s := tel.Services[name]
		if s.Status == "ok" {
			t.Errorf("%s must not report 'ok' when listener runtime state is not tracked; got %+v", name, s)
		}
		if s.Detail == "" {
			t.Errorf("%s must carry a detail label; got empty", name)
		}
	}
	if tel.Services["api"].Status != "ok" {
		t.Errorf("api service must report 'ok' (it is responding to this very request); got %+v", tel.Services["api"])
	}
}

// TestServicesDatabase confirms the database service uses the
// supplied ping callback to label ok / fail / unknown.
func TestServicesDatabase(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		tel := NewTelemetry(Inputs{DBPing: func() error { return nil }})
		if tel.Services["database"].Status != "ok" {
			t.Errorf("db ok: got %+v", tel.Services["database"])
		}
	})
	t.Run("fail", func(t *testing.T) {
		tel := NewTelemetry(Inputs{DBPing: func() error { return errors.New("boom") }})
		if tel.Services["database"].Status != "fail" {
			t.Errorf("db fail: got %+v", tel.Services["database"])
		}
	})
	t.Run("unknown", func(t *testing.T) {
		tel := NewTelemetry(Inputs{DBPing: nil})
		if tel.Services["database"].Status != "unknown" {
			t.Errorf("db unknown: got %+v", tel.Services["database"])
		}
	})
}

// TestServicesQueue confirms the queue service derives status
// from the live counts and reports "unknown" when none are set.
func TestServicesQueue(t *testing.T) {
	t.Run("unknown when all zero", func(t *testing.T) {
		tel := NewTelemetry(Inputs{QueueCounts: QueueCounts{}})
		if tel.Services["queue"].Status != "unknown" {
			t.Errorf("queue unknown: got %+v", tel.Services["queue"])
		}
	})
	t.Run("warn when deferred", func(t *testing.T) {
		tel := NewTelemetry(Inputs{QueueCounts: QueueCounts{Deferred: 1}})
		if tel.Services["queue"].Status != "warn" {
			t.Errorf("queue warn: got %+v", tel.Services["queue"])
		}
	})
	t.Run("fail when bounced", func(t *testing.T) {
		tel := NewTelemetry(Inputs{QueueCounts: QueueCounts{Bounced: 1}})
		if tel.Services["queue"].Status != "fail" {
			t.Errorf("queue fail: got %+v", tel.Services["queue"])
		}
	})
	t.Run("ok when only pending or delivered", func(t *testing.T) {
		tel := NewTelemetry(Inputs{QueueCounts: QueueCounts{Delivered: 5}})
		if tel.Services["queue"].Status != "ok" {
			t.Errorf("queue ok: got %+v", tel.Services["queue"])
		}
	})
}

// TestWarningsRollup confirms the server-side warning list is
// built from the inputs and never duplicates a license warning
// when the public key is loaded.
func TestWarningsRollup(t *testing.T) {
	t.Run("public key missing", func(t *testing.T) {
		tel := NewTelemetry(Inputs{
			StartedAt: time.Now().Add(-time.Minute),
			License:   LicensePosture{Mode: "offline", PublicKeyLoaded: false, Status: "offline"},
		})
		seen := false
		for _, w := range tel.Warnings {
			if w.Code == "license_public_key_missing" {
				seen = true
			}
		}
		if !seen {
			t.Errorf("expected license_public_key_missing warning; got %+v", tel.Warnings)
		}
	})
	t.Run("public key loaded skips warning", func(t *testing.T) {
		tel := NewTelemetry(Inputs{
			StartedAt: time.Now().Add(-time.Minute),
			License:   LicensePosture{Mode: "offline", PublicKeyLoaded: true, Status: "ok"},
		})
		for _, w := range tel.Warnings {
			if w.Code == "license_public_key_missing" {
				t.Errorf("must not warn when public key is loaded; got %+v", tel.Warnings)
			}
		}
	})
	t.Run("queue deferred warning", func(t *testing.T) {
		tel := NewTelemetry(Inputs{
			StartedAt:   time.Now().Add(-time.Minute),
			QueueCounts: QueueCounts{Deferred: 5},
		})
		seen := false
		for _, w := range tel.Warnings {
			if w.Code == "queue_deferred" {
				seen = true
			}
		}
		if !seen {
			t.Errorf("expected queue_deferred warning; got %+v", tel.Warnings)
		}
	})
	t.Run("queue bounced warning", func(t *testing.T) {
		tel := NewTelemetry(Inputs{
			StartedAt:   time.Now().Add(-time.Minute),
			QueueCounts: QueueCounts{Bounced: 1},
		})
		seen := false
		for _, w := range tel.Warnings {
			if w.Code == "queue_bounced" {
				seen = true
			}
		}
		if !seen {
			t.Errorf("expected queue_bounced warning; got %+v", tel.Warnings)
		}
	})
	t.Run("disk high warning at 85 percent", func(t *testing.T) {
		tel := NewTelemetry(Inputs{
			StartedAt: time.Now().Add(-time.Minute),
			DiskFn: func(string) Disk {
				return Disk{Label: "data", TotalBytes: 100, UsedBytes: 90, UsedPercent: 90}
			},
		})
		seen := false
		for _, w := range tel.Warnings {
			if w.Code == "disk_high" {
				seen = true
			}
		}
		if !seen {
			t.Errorf("expected disk_high warning at 90%%; got %+v", tel.Warnings)
		}
	})
	t.Run("telemetry incomplete when no StartedAt", func(t *testing.T) {
		tel := NewTelemetry(Inputs{})
		seen := false
		for _, w := range tel.Warnings {
			if w.Code == "telemetry_incomplete" {
				seen = true
			}
		}
		if !seen {
			t.Errorf("expected telemetry_incomplete warning when StartedAt is zero; got %+v", tel.Warnings)
		}
	})
}

// TestStatusDegradedWithWarnings confirms the top-level status
// reports "degraded" when at least one warning is present.
func TestStatusDegradedWithWarnings(t *testing.T) {
	tel := NewTelemetry(Inputs{
		StartedAt:   time.Now().Add(-time.Minute),
		QueueCounts: QueueCounts{Bounced: 1},
	})
	if tel.Status != "degraded" {
		t.Errorf("status with warnings: want 'degraded' got %q", tel.Status)
	}
}

// TestTelemetryJSONShape confirms the JSON shape is stable and
// the dashboard-facing fields are present.
func TestTelemetryJSONShape(t *testing.T) {
	tel := NewTelemetry(Inputs{
		Version:   "1.0.0",
		Commit:    "abc",
		BuildTime: "dev",
		StartedAt: time.Now().Add(-time.Minute),
	})
	b, err := json.Marshal(tel)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, want := range []string{
		`"status"`, `"version"`, `"commit"`, `"build_time"`,
		`"go_version"`, `"arch"`, `"hostname"`, `"uptime_seconds"`,
		`"services"`, `"capacity"`, `"disk"`, `"queue"`,
		`"license"`, `"warnings"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("JSON missing %s: %s", want, s)
		}
	}
	// Confirm services map has api, smtp, imap, pop3, jmap, database, queue.
	for _, want := range []string{`"api"`, `"smtp"`, `"imap"`, `"pop3"`, `"jmap"`, `"database"`, `"queue"`} {
		if !strings.Contains(s, want) {
			t.Errorf("services JSON missing %s: %s", want, s)
		}
	}
}
