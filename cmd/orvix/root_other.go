//go:build !unix

package main

// isRoot is a build-portability stub for non-unix platforms. The admin
// recovery CLI only ever runs as root on the Linux hosts orvix.service is
// deployed to; this exists solely so the tree compiles and tests run on
// developer Windows/macOS hosts. It always reports false, so the recovery
// commands always refuse to run when built for a non-unix target — the same
// fail-closed behavior as an unprivileged Linux user.
func isRoot() bool {
	return false
}
