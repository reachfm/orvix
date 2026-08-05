//go:build linux

package storage

import (
	"fmt"
	"os"
)

// fsyncDir fsyncs the directory so a preceding os.Rename is persisted to
// stable storage. Required on Linux ext4/xfs for crash-safety of the rename.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open dir %s: %w", dir, err)
	}
	syncErr := d.Sync()
	closeErr := d.Close()
	if syncErr != nil {
		return fmt.Errorf("sync dir %s: %w", dir, syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close dir %s: %w", dir, closeErr)
	}
	return nil
}
