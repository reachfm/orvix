//go:build linux

package storage

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestAtomicWriteLinuxParentDirFsyncCalled(t *testing.T) {
	resetHooks(t)
	dir := t.TempDir()
	dest := filepath.Join(dir, "msg.eml")

	var called int32
	var seenDir string
	atomicWriteHookFsyncDir = func(d string) error {
		atomic.StoreInt32(&called, 1)
		seenDir = d
		return nil
	}
	if err := atomicWriteFile(dest, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&called) != 1 {
		t.Fatal("fsyncDir was not called")
	}
	if seenDir != dir {
		t.Fatalf("fsyncDir got %q want %q", seenDir, dir)
	}
	_ = os.RemoveAll
}
