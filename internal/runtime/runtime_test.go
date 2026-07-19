package runtime

import (
	"encoding/json"
	"errors"
	"net"
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
		Version:     "1.0.0",
		Commit:      "abc123",
		BuildTime:   "development",
		StartedAt:   time.Now().Add(-time.Hour),
		DataPath:    "/var/lib/orvix/data",
		DBPing:      func() error { return nil },
		QueueCounts: QueueCounts{Pending: 1, Deferred: 0, Bounced: 0, Delivered: 0},
		License:     LicensePosture{Mode: "offline", PublicKeyLoaded: true, Status: "ok"},
		SMHTTPPort:  25, IMAPPort: 143, POP3Port: 110, JMAPPort: 8080,
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
		"empty":     {"", "", "data"},
		"data only": {"/var/lib/orvix/data", "", "data"},
		"good hint": {"/var/lib/orvix/data", "mailstore", "mailstore"},
		"abs hint":  {"/var/lib/orvix/data", "/etc/passwd", "data"},
		"win abs":   {"C:\\data", "C:\\secret", "data"},
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
// built from the inputs. License warnings are intentionally
// suppressed — local product licensing is retired.
func TestWarningsRollup(t *testing.T) {
	t.Run("public key missing does not warn", func(t *testing.T) {
		tel := NewTelemetry(Inputs{
			StartedAt: time.Now().Add(-time.Minute),
			License:   LicensePosture{Mode: "offline", PublicKeyLoaded: false, PublicKeyState: "missing", Status: "offline"},
		})
		for _, w := range tel.Warnings {
			if w.Code == "license_public_key_missing" {
				t.Errorf("must not emit license_public_key_missing warning after license retirement; got %+v", tel.Warnings)
			}
		}
	})
	t.Run("public key invalid does not warn", func(t *testing.T) {
		tel := NewTelemetry(Inputs{
			StartedAt: time.Now().Add(-time.Minute),
			License:   LicensePosture{Mode: "missing", PublicKeyLoaded: false, PublicKeyState: "invalid", Status: "missing"},
		})
		for _, w := range tel.Warnings {
			if w.Code == "license_public_key_invalid" {
				t.Errorf("must not emit license_public_key_invalid warning after license retirement; got %+v", tel.Warnings)
			}
		}
	})
	t.Run("public key loaded skips warning", func(t *testing.T) {
		tel := NewTelemetry(Inputs{
			StartedAt: time.Now().Add(-time.Minute),
			License:   LicensePosture{Mode: "offline", PublicKeyLoaded: true, PublicKeyState: "loaded", Status: "ok"},
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

// TestListenerRegistryDefaults confirms a fresh registry returns
// unknown for all listener kinds.
func TestListenerRegistryDefaults(t *testing.T) {
	r := NewListenerRegistry()
	snap := r.Snapshot()
	for _, kind := range allKinds {
		s, ok := snap[kind]
		if !ok {
			t.Errorf("snapshot missing kind %s", kind)
			continue
		}
		if s.Status != "unknown" {
			t.Errorf("%s status must be unknown by default; got %q", kind, s.Status)
		}
		if s.Detail != "listener runtime state not reported" {
			t.Errorf("%s detail must be default; got %q", kind, s.Detail)
		}
	}
}

// TestListenerRegistryOK confirms MarkOK sets the correct status.
func TestListenerRegistryOK(t *testing.T) {
	r := NewListenerRegistry()
	r.MarkOK(ListenerSMTP, 25)
	snap := r.Snapshot()
	s := snap[ListenerSMTP]
	if s.Status != "ok" {
		t.Errorf("status must be ok; got %q", s.Status)
	}
	if s.Detail != "listening" {
		t.Errorf("detail must be 'listening'; got %q", s.Detail)
	}
	if s.Port != 25 {
		t.Errorf("port must be 25; got %d", s.Port)
	}
}

// TestListenerRegistryFailed confirms MarkFailed sets fail status
// with a safe error detail.
func TestListenerRegistryFailed(t *testing.T) {
	r := NewListenerRegistry()
	r.MarkFailed(ListenerIMAP, 143, errors.New("bind: address already in use"))
	snap := r.Snapshot()
	s := snap[ListenerIMAP]
	if s.Status != "fail" {
		t.Errorf("status must be fail; got %q", s.Status)
	}
	if s.Detail != "bind failed: address already in use" {
		t.Errorf("detail must be safe error; got %q", s.Detail)
	}
	// Original error must not appear verbatim.
	if strings.Contains(s.Detail, "bind: address already in use") {
		t.Errorf("detail must not contain raw error: %q", s.Detail)
	}
}

// TestListenerRegistryDisabled confirms MarkDisabled sets disabled
// status with the provided reason.
func TestListenerRegistryDisabled(t *testing.T) {
	r := NewListenerRegistry()
	r.MarkDisabled(ListenerPOP3, 110, "disabled by config")
	snap := r.Snapshot()
	s := snap[ListenerPOP3]
	if s.Status != "disabled" {
		t.Errorf("status must be disabled; got %q", s.Status)
	}
	if s.Detail != "disabled by config" {
		t.Errorf("detail must match; got %q", s.Detail)
	}
}

// TestListenerRegistrySnapshotUseWithTelemetry confirms that when
// the snapshot is wired into NewTelemetry, the services reflect
// the real listener state instead of the fallback unknown.
func TestListenerRegistrySnapshotUseWithTelemetry(t *testing.T) {
	r := NewListenerRegistry()
	r.MarkOK(ListenerSMTP, 25)
	r.MarkFailed(ListenerIMAP, 143, errors.New("EADDRINUSE"))
	r.MarkDisabled(ListenerPOP3, 110, "disabled by config")
	// Leave JMAP unset — should remain unknown.

	// Construct a telemetry with known queue / license so the
	// overall Status is driven by listener state. Without a startedAt
	// the telemetry will say "unknown", but the service entries must
	// still show the registry values.
	tel := NewTelemetry(Inputs{
		Version:          "1.0.0",
		StartedAt:        time.Now().Add(-time.Hour),
		ListenerSnapshot: r.Snapshot(),
	})

	if tel.Services["smtp"].Status != "ok" {
		t.Errorf("smtp status must be ok; got %q", tel.Services["smtp"].Status)
	}
	if tel.Services["imap"].Status != "fail" {
		t.Errorf("imap status must be fail; got %q", tel.Services["imap"].Status)
	}
	if tel.Services["pop3"].Status != "disabled" {
		t.Errorf("pop3 status must be disabled; got %q", tel.Services["pop3"].Status)
	}
	if tel.Services["jmap"].Status != "unknown" {
		t.Errorf("jmap status must be unknown (unset); got %q", tel.Services["jmap"].Status)
	}
	if tel.Services["smtp"].Port != 25 {
		t.Errorf("smtp port must be 25; got %d", tel.Services["smtp"].Port)
	}
	if tel.Services["smtp"].Detail != "listening" {
		t.Errorf("smtp detail must be 'listening'; got %q", tel.Services["smtp"].Detail)
	}
}

// TestListenerRegistrySnapshotNilFallback confirms that when the
// ListenerSnapshot is nil, the pre-tracking fallback is used
// (unknown + "listener runtime state not reported").
func TestListenerRegistrySnapshotNilFallback(t *testing.T) {
	tel := NewTelemetry(Inputs{
		Version:          "1.0.0",
		StartedAt:        time.Now().Add(-time.Hour),
		ListenerSnapshot: nil,
		SMHTTPPort:       25,
		IMAPPort:         143,
		POP3Port:         110,
		JMAPPort:         8080,
	})
	for _, key := range []string{"smtp", "imap", "pop3", "jmap"} {
		s := tel.Services[key]
		if s.Status != "unknown" {
			t.Errorf("%s status must be unknown (nil snapshot fallback); got %q", key, s.Status)
		}
		if s.Detail != "listener runtime state not reported" {
			t.Errorf("%s detail must be fallback; got %q", key, s.Detail)
		}
	}
}

// TestListenerRegistryEmptySnapshotFallback confirms that an empty
// snapshot (created but not populated) still falls through to the
// default unknown entries for unset kinds.
func TestListenerRegistryEmptySnapshotFallback(t *testing.T) {
	r := NewListenerRegistry()
	// Only set SMTP; IMAP/POP3/JMAP remain unset.
	r.MarkOK(ListenerSMTP, 25)
	snap := r.Snapshot()
	tel := NewTelemetry(Inputs{
		Version:          "1.0.0",
		StartedAt:        time.Now().Add(-time.Hour),
		ListenerSnapshot: snap,
	})
	if tel.Services["smtp"].Status != "ok" {
		t.Errorf("smtp status must be ok; got %q", tel.Services["smtp"].Status)
	}
	for _, key := range []string{"imap", "pop3", "jmap"} {
		s := tel.Services[key]
		if s.Status != "unknown" {
			t.Errorf("%s status must be unknown (unset); got %q", key, s.Status)
		}
	}
}

// ── Normalized state taxonomy (active|skipped|degraded|failed) ──

// TestListenerRegistryStateActive confirms MarkOK maps to the
// normalized "active" state.
func TestListenerRegistryStateActive(t *testing.T) {
	r := NewListenerRegistry()
	r.MarkOK(ListenerSMTP, 25)
	s := r.Snapshot()[ListenerSMTP]
	if s.State != StateActive {
		t.Errorf("MarkOK must yield state=active; got %q", s.State)
	}
}

// TestListenerRegistryStateSkipped confirms MarkDisabled maps to the
// normalized "skipped" state — a config-disabled listener is skipped,
// never fake-active.
func TestListenerRegistryStateSkipped(t *testing.T) {
	r := NewListenerRegistry()
	r.MarkDisabled(ListenerIMAPS, 993, "IMAPS disabled: TLS cert not configured")
	s := r.Snapshot()[ListenerIMAPS]
	if s.State != StateSkipped {
		t.Errorf("MarkDisabled must yield state=skipped; got %q", s.State)
	}
	if s.Status != "disabled" {
		t.Errorf("legacy Status must remain 'disabled'; got %q", s.Status)
	}
}

// TestListenerRegistryStateFailed confirms MarkFailed maps to the
// normalized "failed" state (used for port conflicts).
func TestListenerRegistryStateFailed(t *testing.T) {
	r := NewListenerRegistry()
	r.MarkFailed(ListenerSMTP, 25, errors.New("listen tcp :25: bind: address already in use"))
	s := r.Snapshot()[ListenerSMTP]
	if s.State != StateFailed {
		t.Errorf("MarkFailed must yield state=failed; got %q", s.State)
	}
	if s.Detail != "bind failed: address already in use" {
		t.Errorf("failed detail must be the safe port-conflict summary; got %q", s.Detail)
	}
}

// TestListenerRegistryStateDegraded confirms MarkDegraded maps to the
// normalized "degraded" state while keeping the listener reachable.
func TestListenerRegistryStateDegraded(t *testing.T) {
	r := NewListenerRegistry()
	r.MarkDegraded(ListenerSMTP, 25, "STARTTLS unavailable: certificate failed to load")
	s := r.Snapshot()[ListenerSMTP]
	if s.State != StateDegraded {
		t.Errorf("MarkDegraded must yield state=degraded; got %q", s.State)
	}
	// Degraded listeners are still reachable, so legacy Status is "ok".
	if s.Status != "ok" {
		t.Errorf("degraded listener legacy Status must stay 'ok'; got %q", s.Status)
	}
}

// TestListenerRegistryStateDefaultsUnknown confirms unset listeners
// report the normalized "unknown" state, never a fabricated "active".
func TestListenerRegistryStateDefaultsUnknown(t *testing.T) {
	r := NewListenerRegistry()
	for _, kind := range allKinds {
		s := r.Snapshot()[kind]
		if s.State != StateUnknown {
			t.Errorf("%s default state must be unknown; got %q", kind, s.State)
		}
	}
}

// TestListenerRegistryStatePortConflict simulates two listeners
// competing for the same port: the loser is recorded as failed with a
// safe address-in-use detail, and the registry state matches the actual
// (failed) bind result rather than a config-derived value.
func TestListenerRegistryStatePortConflict(t *testing.T) {
	// Bind a real socket so the second attempt genuinely conflicts.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	_, err = net.Listen("tcp", ln.Addr().String())
	if err == nil {
		t.Fatal("expected the second bind on the same port to fail")
	}

	r := NewListenerRegistry()
	r.MarkStarting(ListenerSMTP, port)
	// The bind failed for real → the runtime records failed, matching
	// the actual result.
	r.MarkFailed(ListenerSMTP, port, err)
	s := r.Snapshot()[ListenerSMTP]
	if s.State != StateFailed {
		t.Fatalf("port conflict must yield state=failed; got %q (%q)", s.State, s.Detail)
	}
	if s.Port != port {
		t.Errorf("failed listener must retain its port; got %d want %d", s.Port, port)
	}
}

// TestListenerRegistryStateInTelemetry confirms the normalized state is
// carried through into the admin runtime telemetry Service entries so
// the admin endpoint reports actual listener state.
func TestListenerRegistryStateInTelemetry(t *testing.T) {
	r := NewListenerRegistry()
	r.MarkOK(ListenerSMTP, 25)
	r.MarkDisabled(ListenerIMAPS, 993, "IMAPS disabled by config")
	r.MarkFailed(ListenerIMAP, 143, errors.New("bind: address already in use"))
	r.MarkDegraded(ListenerPOP3, 110, "STARTTLS unavailable")

	tel := NewTelemetry(Inputs{
		Version:          "1.0.0",
		StartedAt:        time.Now().Add(-time.Hour),
		ListenerSnapshot: r.Snapshot(),
	})

	cases := map[string]string{
		"smtp":  StateActive,
		"imaps": StateSkipped,
		"imap":  StateFailed,
		"pop3":  StateDegraded,
	}
	for svc, want := range cases {
		if got := tel.Services[svc].State; got != want {
			t.Errorf("telemetry %s state = %q, want %q", svc, got, want)
		}
	}
	// Unset listeners must report unknown, never active.
	if tel.Services["jmap"].State != StateUnknown {
		t.Errorf("jmap (unset) state must be unknown; got %q", tel.Services["jmap"].State)
	}
}
