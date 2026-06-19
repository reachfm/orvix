//go:build !linux && !darwin

package runtime

// platformDiskUsage is a no-op shim for platforms where statfs(2)
// is not available (Windows). The function returns the input
// struct with zero usage values; the handler will surface the
// label as "Not reported" and the dashboard will show an honest
// placeholder rather than fabricating a number.
func platformDiskUsage(_ string) Disk {
	return Disk{Label: "data"}
}
