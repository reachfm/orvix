//go:build linux

package monitoring

import "golang.org/x/sys/unix"

// hostUptimeSeconds returns the real Linux OS host uptime (seconds
// since boot) via the sysinfo(2) syscall — distinct from the
// process/service uptime tracked by Service.uptimeFrom(). Returns
// (0, false) if the syscall fails, so callers never fabricate a
// value.
func hostUptimeSeconds() (int64, bool) {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return 0, false
	}
	if info.Uptime < 0 {
		return 0, false
	}
	return info.Uptime, true
}
