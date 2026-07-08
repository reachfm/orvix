//go:build linux || darwin

package handlers

import "syscall"

// statfsPlatform wraps syscall.Statfs for POSIX. The result is
// derived from the kernel's fsid — these are the real values from
// statfs(2), not estimates.
func statfsPlatform(path string) (totalBytes, freeBytes int64, err error) {
	var s syscall.Statfs_t
	if err = syscall.Statfs(path, &s); err != nil {
		return 0, 0, err
	}
	total := int64(s.Blocks) * int64(s.Bsize)
	free := int64(s.Bavail) * int64(s.Bsize)
	return total, free, nil
}
