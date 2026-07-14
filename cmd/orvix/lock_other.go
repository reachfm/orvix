//go:build !unix

package main

// acquireExclusiveLock is a build-portability stub for non-unix platforms. The
// restore helper only ever runs under systemd on Linux; this exists solely so
// the tree compiles on developer Windows/macOS hosts.
func acquireExclusiveLock(path string) (release func(), ok bool, err error) {
	return func() {}, true, nil
}
