package relay

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
)

// fakeSMTPServer is a minimal, single-connection SMTP server used to
// prove dialAndTest actually speaks the protocol correctly —
// greeting, EHLO, AUTH PLAIN, QUIT — without any real network
// dependency. It runs on 127.0.0.1 so it is safe in any CI sandbox.
func fakeSMTPServer(t *testing.T, expectAuth bool) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		w := func(s string) { conn.Write([]byte(s + "\r\n")) }
		r := bufio.NewReader(conn)

		w("220 fake.relay.test ESMTP")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "EHLO"):
				w("250-fake.relay.test")
				if expectAuth {
					w("250 AUTH PLAIN LOGIN")
				} else {
					w("250 OK")
				}
			case strings.HasPrefix(line, "AUTH PLAIN"):
				w("235 2.7.0 Authentication successful")
			case strings.HasPrefix(line, "QUIT"):
				w("221 Bye")
				return
			default:
				w("500 unrecognized command")
			}
		}
	}()
	return ln.Addr().String()
}

func TestDialAndTest_UnauthenticatedConnectionSucceeds(t *testing.T) {
	addr := fakeSMTPServer(t, false)
	host, port := splitHostPort(t, addr)
	p := Provider{Host: host, Port: port, ConnSecurity: ConnSecurityNone}
	result := dialAndTest(context.Background(), netDialer{}, p, "")
	if !result.Connected {
		t.Fatalf("expected connected, got error: %s", result.Error)
	}
	if result.AuthOK {
		t.Fatal("expected AuthOK=false when no credential is configured")
	}
}

func TestDialAndTest_AuthenticatedConnectionSucceeds(t *testing.T) {
	addr := fakeSMTPServer(t, true)
	host, port := splitHostPort(t, addr)
	p := Provider{Host: host, Port: port, ConnSecurity: ConnSecurityNone, Username: "relay-user"}
	result := dialAndTest(context.Background(), netDialer{}, p, "relay-password")
	if !result.Connected {
		t.Fatalf("expected connected, got error: %s", result.Error)
	}
	if !result.AuthOK {
		t.Fatalf("expected AuthOK=true, got error: %s", result.Error)
	}
}

func TestDialAndTest_UnreachableHostReportsErrorNotPanic(t *testing.T) {
	p := Provider{Host: "127.0.0.1", Port: 1, ConnSecurity: ConnSecurityNone}
	result := dialAndTest(context.Background(), netDialer{}, p, "")
	if result.Connected {
		t.Fatal("expected connection to an unused port to fail")
	}
	if result.Error == "" {
		t.Fatal("expected a non-empty error message")
	}
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return host, port
}
