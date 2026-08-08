//go:build unix

package main

import "os"

// isRoot reports whether the current process is running as root (EUID 0).
// The admin recovery CLI (`orvix admin reset-password` / `orvix admin
// recover`) refuses to open the database or touch credentials unless this
// returns true, so a compromised or mis-invoked non-root shell can never
// reach the mutation path.
func isRoot() bool {
	return os.Geteuid() == 0
}
