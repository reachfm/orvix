//go:build linux || darwin

package monitoring

import "syscall"

// statfsInto populates the disk usage for path using statfs(2).
// It is defined per-platform; on Windows the no-statfs shim returns
// the input unchanged.
func statfsInto(path string, du DiskUsage) DiskUsage {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err == nil {
		bsize := int64(stat.Bsize)
		if bsize <= 0 {
			bsize = 4096
		}
		du.TotalBytes = bsize * int64(stat.Blocks)
		du.FreeBytes = bsize * int64(stat.Bavail)
		du.UsedBytes = du.TotalBytes - du.FreeBytes
		if du.TotalBytes > 0 {
			du.UsedPct = int((du.UsedBytes * 100) / du.TotalBytes)
		}
	}
	return du
}
