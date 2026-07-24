//go:build unix

// This file implements the real free-disk-space probe used by Phase F's
// preflight "free disk space" checks. See diskspace_other.go for the
// non-Linux/non-unix stub — same split as internal/backup/disk_unix.go and
// disk_other.go, and as peercred_linux.go/peercred_other.go in this package.
package selfupdate

import "golang.org/x/sys/unix"

// diskFreeBytes returns the number of bytes available to an unprivileged
// caller on the filesystem containing path, or an error if path cannot be
// statted (e.g. it does not exist yet).
func diskFreeBytes(path string) (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

// diskSpaceCheckSupported reports whether diskFreeBytes returns a real
// value on this platform (true everywhere unix builds).
const diskSpaceCheckSupported = true
