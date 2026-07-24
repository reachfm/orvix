//go:build linux

package selfupdate

import (
	"errors"
	"net"

	"golang.org/x/sys/unix"
)

// PeerCredentials returns the UID/GID of the process on the other end of a
// Unix domain socket connection, via SO_PEERCRED. This is the only identity
// check the updater daemon trusts — there is no token, password, or other
// credential on this socket, because the kernel-verified peer UID/GID is
// stronger than anything the connecting process could present itself.
func PeerCredentials(conn *net.UnixConn) (uid, gid uint32, err error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, 0, err
	}
	var ucred *unix.Ucred
	var innerErr error
	err = raw.Control(func(fd uintptr) {
		ucred, innerErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil {
		return 0, 0, err
	}
	if innerErr != nil {
		return 0, 0, innerErr
	}
	if ucred == nil {
		return 0, 0, errors.New("selfupdate: SO_PEERCRED returned no credentials")
	}
	return ucred.Uid, ucred.Gid, nil
}
