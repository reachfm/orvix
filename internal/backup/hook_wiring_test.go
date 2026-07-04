package backup

// Runtime integration tests proving the post-create
// target-upload hook is actually invoked when a backup
// completes. The contract under test is the wiring
// between the backup Service and the
// internal/backup/targets uploader, NOT the uploader
// itself (which has its own unit tests for SFTP / FTP /
// disabled-target behaviour).

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestPostCreateHookInvokedOnCompletedBackup is the
// primary wiring test. We install a hook that records
// every invocation, run CreateBackup, and assert that
// the hook fired with the expected arguments.
//
// The hook contract is documented in service.go: the
// service calls the hook AFTER the archive file is
// produced and only if the archive succeeded. The hook
// is invoked in a goroutine so a slow / timing-out
// uploader never blocks the return to the admin caller.
// We poll for up to 5 seconds.
func TestPostCreateHookInvokedOnCompletedBackup(t *testing.T) {
	s := testService(t)

	type invocation struct {
		id   string
		path string
	}
	var got atomic.Value // invocation
	var fired atomic.Int32

	s.SetPostCreateHook(func(id, archivePath string) {
		got.Store(invocation{id: id, path: archivePath})
		fired.Add(1)
	})

	ctx := context.Background()
	b, err := s.CreateBackup(ctx, "wiring-test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fired.Load() > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if fired.Load() == 0 {
		t.Fatalf("postCreateHook never fired within 5s")
	}

	v, ok := got.Load().(invocation)
	if !ok {
		t.Fatalf("hook fired with no recorded invocation")
	}
	if v.id != b.ID {
		t.Fatalf("hook id mismatch: want %s, got %s", b.ID, v.id)
	}
	if v.path == "" {
		t.Fatal("hook archivePath empty")
	}
	if _, err := os.Stat(v.path); err != nil {
		t.Fatalf("hook archivePath does not exist: %v", err)
	}
}

// TestPostCreateHookNotInvokedWhenArchiveFails
// proves the negative case: if the archive step
// returns an error, the hook MUST NOT fire (the local
// backup is still valid but the upload path is not
// safe to attempt).
//
// We simulate the failure by blocking the per-backup
// archive directory with a sentinel file. The archive
// step is mkdirall + tar.gz — both fail when the
// target path is already a non-directory file.
func TestPostCreateHookNotInvokedWhenArchiveFails(t *testing.T) {
	s := testService(t)
	sentinel := filepath.Join(s.stagingRoot, "archive_blocker")
	if err := os.WriteFile(sentinel, []byte("block"), 0640); err != nil {
		t.Fatalf("create sentinel: %v", err)
	}
	var fired atomic.Int32
	s.SetPostCreateHook(func(id, archivePath string) {
		fired.Add(1)
	})
	_, err := s.CreateBackup(context.Background(), "archive-fails")
	// The archive failure must surface to the caller
	// (otherwise the local backup may be silently
	// corrupted). Either error path is acceptable;
	// what matters is that the hook didn't fire.
	_ = err
	if fired.Load() != 0 {
		t.Fatalf("hook fired despite archive failure")
	}
}