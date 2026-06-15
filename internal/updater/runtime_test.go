package updater

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// helper: build a RuntimeService bound to an in-memory SQLite DB and
// a fresh temp dir as the workspace root.
func newService(t *testing.T) (*RuntimeService, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s/update.db?mode=memory&cache=shared", dir))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	svc := NewRuntimeService(db, Config{
		WorkspaceRoot: dir,
		Channel:       ChannelStable,
		BackupDir:     dir,
		MinDiskBytes:  1024,
		Logger:        nil,
	})
	if err := svc.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return svc, dir
}

func TestStatusEmpty(t *testing.T) {
	svc, _ := newService(t)
	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st == nil {
		t.Fatal("status nil")
	}
	if st.Channel != ChannelStable {
		t.Errorf("channel: %q (want stable)", st.Channel)
	}
	if st.JobStatus != "idle" {
		t.Errorf("job status: %q (want idle)", st.JobStatus)
	}
}

func TestStatusUpdateAvailableFalseByDefault(t *testing.T) {
	svc, _ := newService(t)
	st, _ := svc.Status(context.Background())
	if st.UpdateAvailable {
		t.Error("UpdateAvailable must be false when no check has run")
	}
}

func TestPreflightFailWhenScriptMissing(t *testing.T) {
	svc, _ := newService(t)
	// The canonical script at /opt/orvix/release/scripts/ is not
	// present on developer machines, so the script_path check fails
	// and the preflight refuses the run.
	pf := svc.Preflight(context.Background())
	if pf.Pass {
		t.Fatalf("expected preflight to fail when script is missing, got %+v", pf)
	}
	found := false
	for _, c := range pf.Checks {
		if c.Name == "script_path" && c.Status == "fail" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected script_path check to fail, got %+v", pf.Checks)
	}
}

func TestPreflightFailsWhenHelperUnitMissing(t *testing.T) {
	svc, _ := newService(t)
	pf := svc.Preflight(context.Background())
	if pf.Pass {
		t.Fatalf("expected preflight to fail when helper unit is not installed, got %+v", pf)
	}
	found := false
	for _, c := range pf.Checks {
		if c.Name == "update_helper_unit" && c.Status == "fail" {
			found = true
			if c.Detail != "update helper not installed" {
				t.Errorf("helper-unit detail = %q, want %q", c.Detail, "update helper not installed")
			}
			for _, banned := range []string{"/etc/systemd/system/", "/lib/systemd/system/", "/usr/lib/systemd/system/"} {
				if strings.Contains(c.Detail, banned) {
					t.Errorf("helper-unit detail leaks candidate path %q: %q", banned, c.Detail)
				}
			}
		}
	}
	if !found {
		t.Fatalf("expected update_helper_unit check to fail, got %+v", pf.Checks)
	}
}

func TestRunIsRefusedByPreflightWhenScriptMissing(t *testing.T) {
	svc, _ := newService(t)
	_, err := svc.Run(context.Background(), "user:1")
	if err == nil {
		t.Fatal("expected run to be refused when script is missing")
	}
	if !strings.Contains(err.Error(), "preflight") {
		t.Fatalf("expected preflight error, got %v", err)
	}
}

func TestSingleFlightLock(t *testing.T) {
	// This test exercises the single-flight mutex in isolation
	// (we hold the lock manually and then verify a second call is
	// rejected). It does not need to spawn a real script because
	// the mutex is the unit under test.
	svc, _ := newService(t)
	if !svc.mu.TryLock() {
		t.Fatal("expected to acquire lock on a fresh service")
	}
	defer svc.mu.Unlock()
	// The second TryLock must fail.
	if svc.mu.TryLock() {
		t.Fatal("expected second TryLock to fail while the first is held")
	}
	// And svc.IsRunning() must be false because no Run has been
	// invoked — the mutex is independent of the job-state tracker.
	if svc.IsRunning() {
		t.Fatal("expected IsRunning() to be false when no Run is in flight")
	}
}

func TestSafeModuleID(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"orvix-core", true},
		{"auto-update", true},
		{"foo_bar-1.2.3", true},
		{"../etc/passwd", false},
		{"foo;rm -rf", false},
		{"foo bar", false},
		{"", false},
		{strings.Repeat("a", 65), false},
	}
	for _, c := range cases {
		if got := isSafeModuleID(c.in); got != c.ok {
			t.Errorf("isSafeModuleID(%q) = %v, want %v", c.in, got, c.ok)
		}
	}
}

func TestTruncateNotes(t *testing.T) {
	in := strings.Repeat("a", 10000)
	out := truncateNotes(in)
	if len(out) <= 8000 {
		t.Errorf("truncate too aggressive: %d", len(out))
	}
	if !strings.HasSuffix(out, "...") {
		t.Errorf("truncateNotes must end with ellipsis")
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0.0 B"},
		{1024, "1.0 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStatusDoesNotLeakPrivatePath(t *testing.T) {
	svc, _ := newService(t)
	st, _ := svc.Status(context.Background())
	b, _ := jsonMarshal(st)
	for _, banned := range []string{"/etc/", "Bearer ", "Bearer:", "password=", "secret=", "AKIA", "PRIVATE KEY", "ORVIX_DB_DSN", "x-api-key"} {
		if strings.Contains(string(b), banned) {
			t.Fatalf("status leaks forbidden token %q: %s", banned, b)
		}
	}
}

func TestHistoryDoesNotLeakPrivatePath(t *testing.T) {
	svc, _ := newService(t)
	// Insert a history row directly (Run requires systemd).
	if svc.db != nil {
		_, _ = svc.db.Exec(`INSERT INTO update_history (started_at, completed_at, duration_seconds, previous_sha, new_sha, from_version, to_version, status, severity, actor, notes) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"2026-06-15 00:00:00", "2026-06-15 00:00:30", 30, "aaa", "bbb", "1.0.0", "1.1.0", "completed", "info", "user:1", "runtime update completed")
	}
	rows, _ := svc.History(context.Background(), 10)
	for _, r := range rows {
		for _, banned := range []string{"/etc/", "Bearer ", "Bearer:", "password=", "secret="} {
			if strings.Contains(r.Notes, banned) {
				t.Errorf("history row notes leak forbidden token %q: %q", banned, r.Notes)
			}
		}
	}
}

// jsonMarshal is a tiny shim to keep the test file imports small.
func jsonMarshal(v interface{}) ([]byte, error) {
	return jsonStdMarshal(v)
}
