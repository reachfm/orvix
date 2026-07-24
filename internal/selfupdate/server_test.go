package selfupdate

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestServer(t *testing.T, uid uint32, authUID uint32) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "updater.sock")
	s := &Server{
		SocketPath: sockPath,
		AllowedUID: authUID,
		Auth: func(conn *net.UnixConn) (uint32, uint32, error) {
			return uid, uid, nil
		},
		Handlers: map[Operation]Handler{
			OpStatus: func(r Request) Response {
				return Response{OK: true}
			},
			OpGetJob: func(r Request) Response {
				return Response{OK: true, Job: &Job{ID: r.JobID, Phase: PhaseQueued}}
			},
		},
		Timeout: 2 * time.Second,
	}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go s.Serve()
	t.Cleanup(func() { s.Close() })
	return s, sockPath
}

func TestServer_AllowsMatchingUID(t *testing.T) {
	_, sock := newTestServer(t, 1000, 1000)
	c := NewClient(sock)
	resp, err := c.Call(Request{Op: OpStatus})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !resp.OK {
		t.Fatalf("expected OK response, got: %+v", resp)
	}
}

func TestServer_RejectsMismatchedUID(t *testing.T) {
	// Peer reports UID 1001 but the server only allows UID 1000 (the
	// orvix service account) — the connection must be silently dropped,
	// not answered with an error that would confirm the socket exists.
	_, sock := newTestServer(t, 1001, 1000)
	c := &Client{SocketPath: sock, Timeout: 500 * time.Millisecond}
	_, err := c.Call(Request{Op: OpStatus})
	if err == nil {
		t.Fatal("expected call from disallowed UID to fail (connection dropped with no response)")
	}
}

func TestServer_RejectsUnknownFieldsInFrame(t *testing.T) {
	_, sock := newTestServer(t, 1000, 1000)
	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

	type evilRequest struct {
		ProtocolVersion int       `json:"protocol_version"`
		Op              Operation `json:"op"`
		ShellCommand    string    `json:"shell_command"` // not a real field
	}
	if err := WriteFrame(conn, evilRequest{ProtocolVersion: ProtocolVersion, Op: OpStatus, ShellCommand: "rm -rf /"}); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	// The server's ReadFrame uses DisallowUnknownFields, so it should
	// fail to decode and the connection should be closed without a
	// response frame ever being written.
	var resp Response
	err = ReadFrame(conn, &resp)
	if err == nil {
		t.Fatal("expected no response frame for a request containing an unknown field")
	}
}

func TestServer_RejectsOversizedFrame(t *testing.T) {
	_, sock := newTestServer(t, 1000, 1000)
	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

	huge := make([]byte, MaxRequestBytes+1024)
	for i := range huge {
		huge[i] = 'a'
	}
	err = WriteFrame(conn, struct {
		Op   Operation `json:"op"`
		Blob string    `json:"blob"`
	}{Op: OpStatus, Blob: string(huge)})
	if err != ErrFrameTooLarge {
		t.Fatalf("expected client-side WriteFrame to refuse an oversized frame, got: %v", err)
	}
}

func TestIPC_FrameRoundTrip(t *testing.T) {
	dir := t.TempDir()
	f, err := os.CreateTemp(dir, "frame")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	want := Request{ProtocolVersion: ProtocolVersion, Op: OpCheckRelease, Channel: "stable"}
	if err := WriteFrame(f, want); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var got Request
	if err := ReadFrame(f, &got); err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if got != want {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestClient_UnreachableSocketReturnsSentinelError(t *testing.T) {
	c := NewClient(filepath.Join(t.TempDir(), "does-not-exist.sock"))
	_, err := c.Call(Request{Op: OpStatus})
	if err != ErrUpdaterUnreachable {
		t.Fatalf("expected ErrUpdaterUnreachable, got: %v", err)
	}
}
