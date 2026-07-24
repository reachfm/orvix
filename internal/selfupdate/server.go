package selfupdate

import (
	"errors"
	"fmt"
	"net"
	"os"
	"time"
)

// Handler processes one validated Request and returns a Response. Handlers
// never see an invalid operation, an unsupported protocol version, or a
// malformed request — Server.Serve rejects those before a Handler is ever
// invoked, and never from a peer whose UID failed the AllowedUID check.
type Handler func(Request) Response

// PeerAuthFunc extracts the kernel-verified UID/GID of the connecting
// process. In production this is PeerCredentials (SO_PEERCRED, Linux
// only); tests inject a fake so the rest of Server is portable and
// testable on any OS.
type PeerAuthFunc func(*net.UnixConn) (uid, gid uint32, err error)

// Server is the updater daemon's Unix-domain-socket IPC listener. It is the
// single point where "is this connection allowed to ask the updater to do
// anything at all" is decided — purely from the kernel-reported UID of the
// connecting process, never from anything the peer sends over the wire.
type Server struct {
	SocketPath string
	// AllowedUID is the only UID permitted to issue requests (the
	// unprivileged `orvix` service account). A connection from any other
	// UID — including a UID belonging to some other local user — is
	// closed immediately, before a single byte is read from it.
	AllowedUID uint32
	Auth       PeerAuthFunc // defaults to PeerCredentials if nil
	Handlers   map[Operation]Handler
	Timeout    time.Duration // per-connection read/write deadline

	listener *net.UnixListener
}

var ErrPeerNotAllowed = errors.New("selfupdate: connecting peer UID is not allowed to use this socket")

// Listen creates the Unix domain socket, removing any stale socket file
// left behind by a previous (unclean) shutdown, and sets its mode to 0660
// so only the socket's owner (root, since the daemon itself must run as
// root) and group members (the restricted orvix-update group) can connect
// — matching the ADR's socket-permission design. The caller (main.go) is
// responsible for chown'ing the file to root:orvix-update after Listen
// returns, since Go's standard library has no portable "create with group"
// primitive.
func (s *Server) Listen() error {
	_ = os.Remove(s.SocketPath)
	addr, err := net.ResolveUnixAddr("unix", s.SocketPath)
	if err != nil {
		return err
	}
	ln, err := net.ListenUnix("unix", addr)
	if err != nil {
		return err
	}
	if err := os.Chmod(s.SocketPath, 0o660); err != nil {
		_ = ln.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}
	s.listener = ln
	return nil
}

// Serve accepts connections until the listener is closed. Each connection
// handles exactly one request/response pair, matching Client.Call's
// one-connection-per-call design.
func (s *Server) Serve() error {
	if s.listener == nil {
		return errors.New("selfupdate: Listen must be called before Serve")
	}
	for {
		conn, err := s.listener.AcceptUnix()
		if err != nil {
			return err
		}
		go s.handleConn(conn)
	}
}

func (s *Server) Close() error {
	if s.listener == nil {
		return nil
	}
	return s.listener.Close()
}

func (s *Server) handleConn(conn *net.UnixConn) {
	defer conn.Close()

	timeout := s.Timeout
	if timeout <= 0 {
		timeout = DefaultIPCTimeout
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))

	auth := s.Auth
	if auth == nil {
		auth = PeerCredentials
	}
	uid, _, err := auth(conn)
	if err != nil || uid != s.AllowedUID {
		// Deliberately no response is written — an unauthorized peer gets
		// nothing but a closed connection, not even an error message that
		// could confirm the socket exists and is listening.
		return
	}

	var req Request
	if err := ReadFrame(conn, &req); err != nil {
		return
	}
	if err := req.Validate(); err != nil {
		_ = WriteFrame(conn, Response{OK: false, Error: sanitizeError(err)})
		return
	}
	handler, ok := s.Handlers[req.Op]
	if !ok {
		_ = WriteFrame(conn, Response{OK: false, Error: sanitizeError(ErrUnknownOperation)})
		return
	}
	resp := handler(req)
	_ = WriteFrame(conn, resp)
}

// sanitizeError returns only a small fixed set of known-safe error strings
// to the caller. Every error this package returns is already a static,
// non-sensitive message (see protocol.go/verify.go) rather than a wrapped
// OS/library error, so this is a defense-in-depth backstop, not the
// primary control — the primary control is that no handler ever
// constructs an error from raw OS/library output in the first place.
func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
