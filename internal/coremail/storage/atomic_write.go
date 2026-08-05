package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrSymlinkDestination is returned by atomicWriteFile when the destination
// path exists and is a symbolic link. We refuse to follow symlinks to avoid
// TOCTOU / redirect attacks on message storage.
var ErrSymlinkDestination = errors.New("atomic write: destination is a symlink")

// Injectable seams for failure-injection tests. Production code always uses
// the real implementations; tests swap these in and restore via t.Cleanup.
// The tests using these hooks must not run with t.Parallel().
var (
	atomicWriteHookWrite    func(f *os.File, data []byte) (int, error) = func(f *os.File, data []byte) (int, error) { return f.Write(data) }
	atomicWriteHookSync     func(f *os.File) error                     = func(f *os.File) error { return f.Sync() }
	atomicWriteHookClose    func(f *os.File) error                     = func(f *os.File) error { return f.Close() }
	atomicWriteHookRename   func(from, to string) error                = os.Rename
	atomicWriteHookFsyncDir func(dir string) error                     = fsyncDir
)

// atomicWriteFile writes data to destPath atomically and durably, using a
// temp-then-rename dance in the destination's parent directory. It never
// leaves a truncated destination visible to any reader: readers observe
// either the complete previous content (or ENOENT if none existed) or the
// complete new content. On every pre-rename failure the destination is
// unchanged and the temp file is removed.
//
// mode is enforced on the temp file BEFORE rename so the visible file has
// the right permissions from its first moment of existence. Ownership is
// inherited from the process euid/egid — callers must not rely on this
// helper to chown.
//
// On Linux, atomicWriteFile fsyncs the temp file, closes it, renames over
// destPath, then fsyncs the parent directory to persist the rename against
// power-loss / kernel-panic. On non-Linux (Windows tests, macOS dev), the
// parent-directory fsync is a no-op — see fsync_dir_*.go.
//
// Sequence (Linux):
//  1. Validate destPath is absolute and parent exists.
//  2. Reject if destPath is an existing symlink (Lstat).
//  3. CreateTemp(".orvix-atomic-*") in parent dir.
//  4. Chmod temp to `mode` before writing so visible mode is correct from t0.
//  5. Write; short write is an error.
//  6. Sync temp.
//  7. Close temp.
//  8. Rename temp over destPath.
//  9. fsyncDir(parent) to persist the rename (Linux only).
//  10. On any failure at steps 2-8, os.Remove(temp) best-effort. Returned
//     errors never contain data bytes.
func atomicWriteFile(destPath string, data []byte, mode os.FileMode) error {
	if !filepath.IsAbs(destPath) {
		return fmt.Errorf("atomic write: destination is not absolute: %s", destPath)
	}
	parent := filepath.Dir(destPath)
	if info, err := os.Stat(parent); err != nil {
		return fmt.Errorf("atomic write: parent dir stat %s: %w", parent, err)
	} else if !info.IsDir() {
		return fmt.Errorf("atomic write: parent is not a directory: %s", parent)
	}

	if lst, err := os.Lstat(destPath); err == nil {
		if lst.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s", ErrSymlinkDestination, destPath)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("atomic write: lstat destination %s: %w", destPath, err)
	}

	tmp, err := os.CreateTemp(parent, ".orvix-atomic-*")
	if err != nil {
		return fmt.Errorf("atomic write: create temp in %s: %w", parent, err)
	}
	tmpPath := tmp.Name()

	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}

	if err := tmp.Chmod(mode); err != nil {
		cleanup()
		return fmt.Errorf("atomic write: chmod temp %s: %w", tmpPath, err)
	}

	n, werr := atomicWriteHookWrite(tmp, data)
	if werr != nil {
		cleanup()
		return fmt.Errorf("atomic write: write temp %s: %w", tmpPath, werr)
	}
	if n != len(data) {
		cleanup()
		return fmt.Errorf("atomic write: short write to %s: wrote %d of %d bytes", tmpPath, n, len(data))
	}

	if err := atomicWriteHookSync(tmp); err != nil {
		cleanup()
		return fmt.Errorf("atomic write: fsync temp %s: %w", tmpPath, err)
	}
	if err := atomicWriteHookClose(tmp); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic write: close temp %s: %w", tmpPath, err)
	}

	if err := atomicWriteHookRename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic write: rename %s -> %s: %w", tmpPath, destPath, err)
	}

	if err := atomicWriteHookFsyncDir(parent); err != nil {
		return fmt.Errorf("atomic write: fsync parent dir %s: %w", parent, err)
	}
	return nil
}
