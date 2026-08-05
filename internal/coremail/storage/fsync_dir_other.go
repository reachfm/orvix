//go:build !linux

package storage

// fsyncDir is a no-op on non-Linux platforms. Windows and macOS have
// different semantics for directory fsync; message storage is only supported
// on Linux for production, and dev/test on other platforms does not require
// this durability guarantee.
func fsyncDir(dir string) error {
	_ = dir
	return nil
}
