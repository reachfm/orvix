package restorecoord

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func newCoord(t *testing.T) *Coordinator {
	t.Helper()
	c := New(t.TempDir())
	if err := c.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	return c
}

func TestNewJobID_UnguessableAndValidated(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id, err := NewJobID()
		if err != nil {
			t.Fatal(err)
		}
		if len(id) != 64 || !ValidJobID(id) {
			t.Fatalf("bad id %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id generated: %q", id)
		}
		seen[id] = true
	}
	// Reject non-canonical ids (traversal, wrong charset/length).
	for _, bad := range []string{"", "abc", "../etc/passwd", "g" + strings.Repeat("0", 63), strings.Repeat("A", 64), "/" + strings.Repeat("0", 63)} {
		if ValidJobID(bad) {
			t.Fatalf("ValidJobID accepted bad id %q", bad)
		}
	}
}

func TestSubmit_And_GetResult(t *testing.T) {
	c := newCoord(t)
	job, err := c.Submit("backup-123", "user:1")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !ValidJobID(job.ID) || job.BackupID != "backup-123" {
		t.Fatalf("unexpected job: %+v", job)
	}
	res, err := c.GetResult(job.ID)
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if res.Status != StatusPending {
		t.Fatalf("expected pending, got %s", res.Status)
	}
	if strings.Contains(strings.ToLower(res.Message), "activated") {
		t.Fatalf("pre-restart result must not claim activation: %q", res.Message)
	}
}

func TestSubmit_RejectsConcurrentActiveJob(t *testing.T) {
	c := newCoord(t)
	if _, err := c.Submit("b1", "user:1"); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if _, err := c.Submit("b2", "user:1"); err != ErrActiveJob {
		t.Fatalf("second submit should be rejected with ErrActiveJob, got %v", err)
	}
	// After the active job reaches a terminal state, a new submit is allowed.
	res, _ := c.GetResult(mustFirstJobID(t, c))
	res.Status = StatusSucceeded
	if err := c.WriteResult(res); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Submit("b3", "user:1"); err != nil {
		t.Fatalf("submit after terminal should succeed, got %v", err)
	}
}

func TestGetResult_InvalidID_Rejected(t *testing.T) {
	c := newCoord(t)
	for _, bad := range []string{"../../etc/passwd", "..", "foo/bar", strings.Repeat("z", 64)} {
		if _, err := c.GetResult(bad); err != ErrInvalidID {
			t.Fatalf("GetResult(%q) should reject with ErrInvalidID, got %v", bad, err)
		}
	}
	if _, err := c.GetResult(canonicalID()); err != ErrNotFound {
		t.Fatalf("unknown canonical id should be ErrNotFound, got %v", err)
	}
}

func TestReadJob_RejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is unreliable on Windows CI")
	}
	c := newCoord(t)
	id := canonicalID()
	// Plant a symlink at the job path pointing at a sensitive file.
	target := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(target, []byte(`{"id":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, c.jobPath(id)); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	if _, err := c.ReadJob(id); err != ErrTampered {
		t.Fatalf("ReadJob over a symlink must be ErrTampered, got %v", err)
	}
}

func TestReadJob_RejectsTamperedContent(t *testing.T) {
	c := newCoord(t)
	id := canonicalID()
	if err := os.WriteFile(c.jobPath(id), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadJob(id); err == nil {
		t.Fatal("malformed job file must be rejected")
	}
	// ID mismatch between filename and payload is tampering.
	other := canonicalID2()
	if err := os.WriteFile(c.jobPath(id), []byte(`{"id":"`+other+`","backup_id":"b"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadJob(id); err != ErrTampered {
		t.Fatalf("id/filename mismatch must be ErrTampered, got %v", err)
	}
}

func TestResultSurvivesReopen(t *testing.T) {
	c := newCoord(t)
	job, _ := c.Submit("b1", "user:1")
	// Simulate the external helper advancing state across a process restart:
	// a brand-new Coordinator instance (as the API would use after restart)
	// must read the durable result written by the helper instance.
	helper := New(c.root)
	res, _ := helper.GetResult(job.ID)
	res.Status = StatusVerifying
	if err := helper.WriteResult(res); err != nil {
		t.Fatal(err)
	}
	res.Status = StatusSucceeded
	res.Message = "restore complete; restarted service healthy"
	if err := helper.WriteResult(res); err != nil {
		t.Fatal(err)
	}
	fresh := New(c.root)
	got, err := fresh.GetResult(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusSucceeded {
		t.Fatalf("durable status not observed after reopen: %s", got.Status)
	}
}

func TestPendingJobIDs_SkipsTerminal(t *testing.T) {
	c := newCoord(t)
	job, _ := c.Submit("b1", "user:1")
	ids, err := c.PendingJobIDs()
	if err != nil || len(ids) != 1 || ids[0] != job.ID {
		t.Fatalf("expected one pending id, got %v err=%v", ids, err)
	}
	res, _ := c.GetResult(job.ID)
	res.Status = StatusFailed
	_ = c.WriteResult(res)
	ids, _ = c.PendingJobIDs()
	if len(ids) != 0 {
		t.Fatalf("terminal job must not be pending, got %v", ids)
	}
}

// helpers

func mustFirstJobID(t *testing.T, c *Coordinator) string {
	t.Helper()
	entries, _ := os.ReadDir(c.queueDir())
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".job" {
			return strings.TrimSuffix(e.Name(), ".job")
		}
	}
	t.Fatal("no job found")
	return ""
}

func canonicalID() string {
	id, _ := NewJobID()
	return id
}
func canonicalID2() string {
	id, _ := NewJobID()
	return id
}
