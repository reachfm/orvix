//go:build unix

package main

import (
	"os"
	"syscall"
)

// acquireExclusiveLock takes a non-blocking exclusive flock on path so at most
// one restore helper drains the queue at a time even if the systemd path unit
// fires repeatedly. It returns ok=false (no error) when another holder has the
// lock. The returned release closes the fd (dropping the lock).
func acquireExclusiveLock(path string) (release func(), ok bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, true, nil
}
