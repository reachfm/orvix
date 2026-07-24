package selfupdate

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// Wire framing: a 4-byte big-endian length prefix followed by exactly that
// many bytes of JSON. This is deliberately simpler than HTTP — the two
// endpoints are both first-party binaries on the same host, and a fixed
// framing avoids any ambiguity a text-based delimiter could introduce.

const frameHeaderSize = 4

var ErrFrameTooLarge = fmt.Errorf("selfupdate: frame exceeds MaxRequestBytes (%d)", MaxRequestBytes)

// WriteFrame encodes v as length-prefixed JSON and writes it to w.
func WriteFrame(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(body) > MaxRequestBytes {
		return ErrFrameTooLarge
	}
	header := make([]byte, frameHeaderSize)
	binary.BigEndian.PutUint32(header, uint32(len(body)))
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

// ReadFrame reads one length-prefixed JSON frame from r and decodes it into
// v. It rejects a frame whose declared length exceeds MaxRequestBytes
// before reading the body, and rejects any JSON field not present on v
// (json.Decoder.DisallowUnknownFields) so a message with an unexpected
// field is a hard error, not silently ignored.
func ReadFrame(r io.Reader, v any) error {
	header := make([]byte, frameHeaderSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(header)
	if n > MaxRequestBytes {
		return ErrFrameTooLarge
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// DefaultIPCTimeout bounds how long the API process will wait for a single
// request/response round trip against the updater daemon (Status,
// CheckRelease, Preflight, GetJob, etc. — everything except the long-running
// install/rollback job itself, which is polled via GetJob instead of held
// open on one connection).
const DefaultIPCTimeout = 10 * time.Second

// ErrUpdaterUnreachable is returned by Call when the socket cannot be
// dialed at all (daemon not running, wrong permissions, etc.) so callers
// can distinguish "updater offline" from "updater rejected the request".
var ErrUpdaterUnreachable = errors.New("selfupdate: updater daemon is unreachable")

// Client is a minimal IPC client for the Unix domain socket the updater
// daemon listens on. It never accepts a socket path, request timeout, or
// any other value from an HTTP request — the caller (internal/api/handlers)
// always constructs it with the compiled-in socket path.
type Client struct {
	SocketPath string
	Timeout    time.Duration
}

func NewClient(socketPath string) *Client {
	return &Client{SocketPath: socketPath, Timeout: DefaultIPCTimeout}
}

// Call opens a fresh connection, sends req, reads exactly one Response, and
// closes the connection. One connection per call keeps the protocol
// stateless and trivially safe to retry.
func (c *Client) Call(req Request) (*Response, error) {
	req.ProtocolVersion = ProtocolVersion
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultIPCTimeout
	}
	conn, err := net.DialTimeout("unix", c.SocketPath, timeout)
	if err != nil {
		return nil, ErrUpdaterUnreachable
	}
	defer conn.Close()
	deadline := time.Now().Add(timeout)
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	if err := WriteFrame(conn, req); err != nil {
		return nil, err
	}
	var resp Response
	if err := ReadFrame(conn, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
