//go:build !linux && !darwin

package monitoring

// statfsInto is a no-op shim for platforms where statfs(2) is not
// available (Windows). The function returns the input struct with
// zero usage values; the handler will treat the label as
// "unknown" and the dashboard will show "0% used" rather than
// fabricating a value.
func statfsInto(_ string, du DiskUsage) DiskUsage {
	return du
}
