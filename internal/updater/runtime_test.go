package updater

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// helper: build a RuntimeService bound to an in-memory SQLite DB and
// a fresh temp dir as the workspace root.
func newService(t *testing.T) (*RuntimeService, string) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s/update.db?mode=memory&cache=shared", dir))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	svc := NewRuntimeService(db, Config{
		ScriptPath:    DefaultScriptPath,
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
	// The workspace root has no release/scripts/apply-runtime-update.sh,
	// so the script_path check should fail and the preflight should
	// refuse the run.
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

func TestPreflightPassWhenScriptPresent(t *testing.T) {
	svc, dir := newService(t)
	// Lay down a fake script at the canonical location.
	scriptDir := filepath.Join(dir, "release", "scripts")
	if err := os.MkdirAll(scriptDir, 0750); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	scriptPath := filepath.Join(scriptDir, "apply-runtime-update.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho ok\n"), 0750); err != nil {
		t.Fatalf("write script: %v", err)
	}
	pf := svc.Preflight(context.Background())
	if !pf.Pass {
		t.Fatalf("expected preflight to pass, got %+v", pf)
	}
}

func TestPreflightPassesWithWorkspaceRootLayout(t *testing.T) {
	svc, dir := newService(t)
	createUpdateWorkspace(t, dir)
	pf := svc.Preflight(context.Background())
	if !pf.Pass {
		t.Fatalf("expected preflight to pass, got %+v", pf)
	}
	requireCheck(t, pf, "script_path", "pass")
	requireCheck(t, pf, "binary_build", "pass")
	data, _ := jsonMarshal(pf)
	for _, banned := range []string{dir, filepath.Join(dir, "release", "scripts"), filepath.Join(dir, "cmd", "orvix")} {
		if strings.Contains(string(data), banned) {
			t.Fatalf("preflight leaks path %q: %s", banned, data)
		}
	}
}

func TestPreflightWrongWorkspaceRootFailsSafely(t *testing.T) {
	outside := t.TempDir()
	t.Chdir(outside)
	svc, dir := newService(t)
	svc.cfg.WorkspaceRoot = filepath.Join(dir, "missing-root")
	pf := svc.Preflight(context.Background())
	if pf.Pass {
		t.Fatalf("expected preflight to fail for wrong root, got %+v", pf)
	}
	requireCheck(t, pf, "script_path", "fail")
	data, _ := jsonMarshal(pf)
	for _, banned := range []string{dir, svc.cfg.WorkspaceRoot, "release" + string(filepath.Separator) + "scripts"} {
		if strings.Contains(string(data), banned) {
			t.Fatalf("preflight leak for wrong root %q: %s", banned, data)
		}
	}
}

func TestDetectWorkspaceRootUsesConfiguredRootOutsideGit(t *testing.T) {
	outside := t.TempDir()
	t.Chdir(outside)
	root := filepath.Join(outside, "orvix")
	createUpdateWorkspace(t, root)
	if got := DetectWorkspaceRoot(root); got != root {
		t.Fatalf("DetectWorkspaceRoot() = %q, want %q", got, root)
	}
}

func TestEnsureScriptPathRejectedOutsideRoot(t *testing.T) {
	root := t.TempDir()
	svc, _ := newService(t)
	svc.cfg.WorkspaceRoot = root
	svc.cfg.ScriptPath = "/etc/passwd" // absolute path outside root
	_, err := svc.resolveScriptPath()
	if err == nil {
		t.Fatal("expected script path to be rejected")
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("expected rejection error, got %v", err)
	}
}

func TestEnsureScriptPathRejectedWrongSuffix(t *testing.T) {
	svc, dir := newService(t)
	svc.cfg.WorkspaceRoot = dir
	svc.cfg.ScriptPath = filepath.Join(dir, "release", "scripts", "evil.sh")
	_, err := svc.resolveScriptPath()
	if err == nil {
		t.Fatal("expected script path to be rejected for wrong suffix")
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

func TestRunExecutesScriptAndPersistsHistory(t *testing.T) {
	// The runtime update script is a bash script (apply-runtime-update.sh)
	// that depends on bash, `id`, `setcap`, `systemctl`, etc., which are
	// only present on POSIX. On Windows, the kernel cannot directly
	// execute a .sh file. We skip the test on Windows so the test suite
	// stays platform-portable. The non-execution code paths
	// (preflight refusal, audit logging, history persistence on a
	// failed exec, single-flight lock) are all still covered by the
	// other tests in this file.
	if runtime.GOOS == "windows" {
		t.Skip("runtime update script is bash-only; skip on Windows")
	}
	svc, dir := newService(t)
	// Lay down a script that exits 0.
	scriptDir := filepath.Join(dir, "release", "scripts")
	if err := os.MkdirAll(scriptDir, 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	scriptPath := filepath.Join(scriptDir, "apply-runtime-update.sh")
	// A no-op script that exits 0.
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0750); err != nil {
		t.Fatalf("write: %v", err)
	}
	row, err := svc.Run(context.Background(), "user:42")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if row.Status != "completed" {
		t.Errorf("status: %q (want completed)", row.Status)
	}
	if row.Actor != "user:42" {
		t.Errorf("actor: %q (want user:42)", row.Actor)
	}
	if row.DurationSeconds < 0 {
		t.Errorf("duration: %d (must be non-negative)", row.DurationSeconds)
	}
	// History should have one row.
	rows, err := svc.History(context.Background(), 10)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 history row, got %d", len(rows))
	}
}

// runtimeGoos helper removed: tests now import runtime directly.

func TestRunPersistsFailureOnNonZeroExit(t *testing.T) {
	svc, dir := newService(t)
	scriptDir := filepath.Join(dir, "release", "scripts")
	if err := os.MkdirAll(scriptDir, 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	scriptPath := filepath.Join(scriptDir, "apply-runtime-update.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 1\n"), 0750); err != nil {
		t.Fatalf("write: %v", err)
	}
	row, err := svc.Run(context.Background(), "user:1")
	if err == nil {
		t.Fatal("expected non-zero exit to surface as error")
	}
	if row == nil {
		t.Fatal("expected non-nil history row on failure")
	}
	if row.Status != "failed" {
		t.Errorf("status: %q (want failed)", row.Status)
	}
	if row.Severity != SeverityCritical {
		t.Errorf("severity: %q (want critical)", row.Severity)
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
	svc, dir := newService(t)
	scriptDir := filepath.Join(dir, "release", "scripts")
	_ = os.MkdirAll(scriptDir, 0750)
	_ = os.WriteFile(filepath.Join(scriptDir, "apply-runtime-update.sh"), []byte("#!/bin/sh\nexit 0\n"), 0750)
	_, _ = svc.Run(context.Background(), "user:1")
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

func createUpdateWorkspace(t *testing.T, root string) {
	t.Helper()
	scriptDir := filepath.Join(root, "release", "scripts")
	if err := os.MkdirAll(scriptDir, 0750); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "apply-runtime-update.sh"), []byte("#!/bin/sh\nexit 0\n"), 0750); err != nil {
		t.Fatalf("write script: %v", err)
	}
	cmdDir := filepath.Join(root, "cmd", "orvix")
	if err := os.MkdirAll(cmdDir, 0750); err != nil {
		t.Fatalf("mkdir cmd/orvix: %v", err)
	}
}

func requireCheck(t *testing.T, pf *PreflightResult, name, status string) {
	t.Helper()
	for _, check := range pf.Checks {
		if check.Name == name {
			if check.Status != status {
				t.Fatalf("%s status = %q, want %q in %+v", name, check.Status, status, pf.Checks)
			}
			return
		}
	}
	t.Fatalf("missing preflight check %q in %+v", name, pf.Checks)
}
