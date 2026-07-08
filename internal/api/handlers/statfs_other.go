//go:build !linux && !darwin

package handlers

// statfsPlatform is the non-POSIX stub. We deliberately do not call
// GetDiskFreeSpaceExA from here because (a) it would pull syscalls
// into the build for every Windows target, (b) the storage-topology
// page should render the honest empty state on Windows until a
// proper windows.StatfsApi is wired in.
//
// ListStorageVolumes surfaces this honestly: the VolumeStat row has
// Available=false with Detail="statfs not implemented on this
// platform" so the operator never sees a fabricated byte count.
func statfsPlatform(path string) (totalBytes, freeBytes int64, err error) {
	return 0, 0, errStatfsUnsupported
}

// errStatfsUnsupported is a sentinel callers can match against. We
// never expose the raw error string outside of the admin API JSON
// response so the error message stays under our control.
var errStatfsUnsupported = statfsErr("statfs not implemented on this platform")

type statfsErr string

func (e statfsErr) Error() string { return string(e) }
