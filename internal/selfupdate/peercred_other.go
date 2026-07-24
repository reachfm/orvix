//go:build !linux

package selfupdate

import (
	"errors"
	"net"
)

// PeerCredentials is unsupported outside Linux. Orvix only ships a signed
// Linux amd64/arm64 bundle (see release/scripts/build-release-bundle.sh),
// so the updater daemon only ever runs on Linux in production; this stub
// exists solely so the module builds and unit-tests on a non-Linux
// development machine.
func PeerCredentials(conn *net.UnixConn) (uid, gid uint32, err error) {
	return 0, 0, errors.New("selfupdate: PeerCredentials is only implemented on linux")
}
