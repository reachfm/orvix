//go:build !unix

// Non-unix (i.e. Windows dev machine) stub. Orvix only ships and runs in
// production on Linux (see peercred_other.go's doc comment for the same
// reasoning); this stub exists solely so the package builds and its tests
// run on a non-Linux development machine. The free-disk-space preflight
// check degrades to "unknown, best effort" on this platform rather than
// failing the build or panicking.
package selfupdate

import "errors"

func diskFreeBytes(path string) (int64, error) {
	return 0, errors.New("selfupdate: free disk space check is only implemented on unix")
}

// diskSpaceCheckSupported reports whether diskFreeBytes returns a real
// value on this platform (false here — see doc comment above).
const diskSpaceCheckSupported = false
