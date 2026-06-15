//go:build !linux && !darwin

package updater

import "errors"

// statfsPlatform is a no-op on platforms where statfs(2) is not
// available (Windows). The preflight handler treats the resulting
// error as "unknown" and surfaces it as a warning.
func statfsPlatform(_ string) (statfsResult, error) {
	return statfsResult{}, errors.New("statfs: not available on this platform")
}
