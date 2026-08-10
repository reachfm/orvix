package updatecoord

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestCoordinator(t *testing.T) (*Coordinator, string) {
	t.Helper()
	root := t.TempDir()
	staging := filepath.Join(t.TempDir(), "staging")
	if err := os.MkdirAll(staging, 0o750); err != nil {
		t.Fatal(err)
	}
	return New(filepath.Join(root, "jobs"), staging), staging
}

func TestSubmit_ArtifactInsideStagingRoot_Accepted(t *testing.T) {
	c, staging := newTestCoordinator(t)
	artifact := filepath.Join(staging, "abc.artifact")
	if err := os.WriteFile(artifact, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	job, err := c.Submit(artifact, "2.0.0", "deadbeef", "user:1")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if !ValidJobID(job.ID) {
		t.Fatal("expected a valid job id")
	}
}

func TestSubmit_PathOutsideStagingRoot_Rejected(t *testing.T) {
	c, _ := newTestCoordinator(t)
	outside := filepath.Join(t.TempDir(), "evil.artifact")
	if err := os.WriteFile(outside, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Submit(outside, "2.0.0", "deadbeef", "user:1"); err != ErrPathNotAllowed {
		t.Fatalf("expected ErrPathNotAllowed, got %v", err)
	}
}

func TestSubmit_PathTraversalOutsideStagingRoot_Rejected(t *testing.T) {
	c, staging := newTestCoordinator(t)
	traversal := filepath.Join(staging, "..", "..", "etc", "passwd")
	if _, err := c.Submit(traversal, "2.0.0", "deadbeef", "user:1"); err != ErrPathNotAllowed {
		t.Fatalf("expected ErrPathNotAllowed for a traversal path, got %v", err)
	}
}

func TestSubmit_SymlinkArtifact_Rejected(t *testing.T) {
	c, staging := newTestCoordinator(t)
	real := filepath.Join(t.TempDir(), "real.artifact")
	if err := os.WriteFile(real, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(staging, "link.artifact")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable in this environment: %v", err)
	}
	if _, err := c.Submit(link, "2.0.0", "deadbeef", "user:1"); err != ErrTampered {
		t.Fatalf("expected ErrTampered for a symlinked artifact path, got %v", err)
	}
}

func TestSubmit_SecondApplyWhileActive_Rejected(t *testing.T) {
	c, staging := newTestCoordinator(t)
	artifact := filepath.Join(staging, "abc.artifact")
	os.WriteFile(artifact, []byte("x"), 0o640)
	if _, err := c.Submit(artifact, "2.0.0", "deadbeef", "user:1"); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if _, err := c.Submit(artifact, "2.0.0", "deadbeef", "user:1"); err != ErrActiveJob {
		t.Fatalf("expected ErrActiveJob on a second apply while one is active, got %v", err)
	}
}

func TestSubmitRollback_WhileApplyActive_Rejected(t *testing.T) {
	c, staging := newTestCoordinator(t)
	artifact := filepath.Join(staging, "abc.artifact")
	os.WriteFile(artifact, []byte("x"), 0o640)
	if _, err := c.Submit(artifact, "2.0.0", "deadbeef", "user:1"); err != nil {
		t.Fatalf("apply submit: %v", err)
	}
	// A rollback attempted while an apply is still active must also be
	// rejected — apply and rollback are mutually exclusive because they
	// share the same job queue/lock.
	if _, err := c.SubmitRollback("1.0.0", "cafebabe", "2.0.0", "user:1"); err != ErrActiveJob {
		t.Fatalf("expected ErrActiveJob for a rollback while apply is active, got %v", err)
	}
}

func TestSubmit_AfterTerminalResult_Accepted(t *testing.T) {
	c, staging := newTestCoordinator(t)
	artifact := filepath.Join(staging, "abc.artifact")
	os.WriteFile(artifact, []byte("x"), 0o640)
	job, err := c.Submit(artifact, "2.0.0", "deadbeef", "user:1")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	// Simulate the external helper completing the job.
	if err := c.WriteResult(&Result{JobID: job.ID, Kind: KindApply, Status: StatusSucceeded}); err != nil {
		t.Fatalf("write result: %v", err)
	}
	if _, err := c.Submit(artifact, "2.1.0", "deadbeef2", "user:1"); err != nil {
		t.Fatalf("expected a new submit to succeed once the prior job is terminal, got %v", err)
	}
}

func TestGetResult_InvalidID_Rejected(t *testing.T) {
	c, _ := newTestCoordinator(t)
	if _, err := c.GetResult("not-a-valid-id"); err != ErrInvalidID {
		t.Fatalf("expected ErrInvalidID, got %v", err)
	}
}

func TestGetResult_Unknown_NotFound(t *testing.T) {
	c, _ := newTestCoordinator(t)
	id, _ := NewJobID()
	if _, err := c.GetResult(id); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
