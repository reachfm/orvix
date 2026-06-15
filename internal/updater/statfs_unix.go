//go:build linux || darwin

package updater

import "syscall"

// statfsPlatform wraps syscall.Statfs into the platform-agnostic
// statfsResult shape.
func statfsPlatform(path string) (statfsResult, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return statfsResult{}, err
	}
	return statfsResult{
		Bsize:  int64(s.Bsize),
		Blocks: uint64(s.Blocks),
		Bavail: uint64(s.Bavail),
	}, nil
}
