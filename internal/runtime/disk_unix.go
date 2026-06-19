//go:build linux || darwin

package runtime

import "syscall"

// platformDiskUsage returns the disk usage for the supplied path
// using statfs(2). On any error the returned struct has zero
// values; the dashboard surfaces that as "Not reported" rather
// than fabricating a number.
func platformDiskUsage(path string) Disk {
	du := Disk{Label: "data"}
	if path == "" {
		return du
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return du
	}
	bsize := int64(stat.Bsize)
	if bsize <= 0 {
		bsize = 4096
	}
	du.TotalBytes = bsize * int64(stat.Blocks)
	du.FreeBytes = bsize * int64(stat.Bavail)
	du.UsedBytes = du.TotalBytes - du.FreeBytes
	if du.TotalBytes > 0 {
		du.UsedPercent = int((du.UsedBytes * 100) / du.TotalBytes)
	}
	return du
}
