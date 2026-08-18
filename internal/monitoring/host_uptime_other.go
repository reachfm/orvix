//go:build !linux

package monitoring

// hostUptimeSeconds is a no-op shim on platforms without a safe,
// dependency-free OS-uptime source (Windows, and anything else that
// isn't Linux). Returns (0, false) so the handler reports the field
// as unavailable rather than fabricating a value. Darwin is grouped
// here too: sysctl "kern.boottime" would work but is out of scope
// until a real deployment target needs it.
func hostUptimeSeconds() (int64, bool) {
	return 0, false
}
