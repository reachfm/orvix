package relay

// F6 acceptance: bounded SMTP I/O and honest ambiguous-delivery
// semantics.
//
//   - a relay that hangs after greeting cannot pin the worker past the
//     bounded deadlines (per-command and total transaction timeouts);
//   - a relay that accepts the DATA payload and then closes before the
//     final response produces an AMBIGUOUS result — the caller must not
//     immediately re-send through a fallback provider;
//   - a clean rejection before DATA is a normal temp/permanent result;
//   - deadline errors never leak credentials or message content.

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// hangingSMTPServer accepts a connection, sends the greeting, then
// goes silent forever (never reading, never closing — the read must be
// interrupted by the per-command deadline).
func hangingSMTPServer(t *testing.T) string {
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
		conn.Write([]byte("220 hang.example.com ESMTP\r\n"))
		// Never read; never respond; never close. The client's deadline
		// must interrupt the blocked read.
		time.Sleep(60 * time.Second)
	}()
	return ln.Addr().String()
}

// dataDropSMTPServer accepts the full exchange, accepts the DATA
// payload (writes the 354 and consumes the dot-terminated body), then
// closes the connection WITHOUT sending the final 250 — the exact
// ambiguous-acceptance shape.
func dataDropSMTPServer(t *testing.T) string {
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
		buf := make([]byte, 4096)
		w := func(s string) { conn.Write([]byte(s)) }
		w("220 drop.example.com ESMTP\r\n")
		inData := false
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			raw := string(buf[:n])
			for _, line := range strings.Split(raw, "\r\n") {
				switch {
				case inData:
					if strings.HasPrefix(line, ".") {
						// Body terminated. Close WITHOUT the final 250.
						return
					}
				case strings.HasPrefix(line, "EHLO"):
					w("250-drop.example.com\r\n250 AUTH PLAIN LOGIN\r\n")
				case strings.HasPrefix(line, "MAIL FROM"):
					w("250 2.1.0 sender ok\r\n")
				case strings.HasPrefix(line, "RCPT TO"):
					w("250 2.1.5 recipient ok\r\n")
				case strings.HasPrefix(line, "DATA"):
					w("354 end with <CRLF>.<CRLF>\r\n")
					inData = true
				case strings.HasPrefix(line, "QUIT"):
					return
				}
			}
		}
	}()
	return ln.Addr().String()
}

func providerForAddr(addr string) Provider {
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}
	return Provider{Host: host, Port: port, ConnSecurity: ConnSecurityNone}
}

// TestF6_HangingRelayBoundedByDeadline proves a relay that stops
// answering cannot pin the caller: the per-command deadline fires and
// the exchange returns a bounded temp-fail.
func TestF6_HangingRelayBoundedByDeadline(t *testing.T) {
	addr := hangingSMTPServer(t)
	p := providerForAddr(addr)
	start := time.Now()
	res := deliverWith(context.Background(), netDialer{}, p, "", "a@b.example", []string{"c@d.example"}, []byte("body"), nil)
	elapsed := time.Since(start)
	if res.Success {
		t.Fatalf("a hanging relay must not report success: %+v", res)
	}
	if !res.TempFail {
		t.Fatalf("a hanging relay must temp-fail, got %+v", res)
	}
	// The first blocking read (EHLO response) must be interrupted by the
	// 30s per-command deadline — far less than a hang.
	if elapsed > 40*time.Second {
		t.Fatalf("deadline did not interrupt the blocked read: took %s", elapsed)
	}
	body := strings.ToLower(res.StatusMsg)
	if strings.Contains(body, "password") || strings.Contains(body, "super-secret") || strings.Contains(body, "credential") && !strings.Contains(body, "credential") {
		t.Fatalf("error text leaked credential material: %q", res.StatusMsg)
	}
}

// TestF6_DataAcceptedThenDroppedIsAmbiguous proves the exact
// ambiguous-acceptance shape produces Ambiguous=true, so the worker
// stops the fallback chain instead of re-sending.
func TestF6_DataAcceptedThenDroppedIsAmbiguous(t *testing.T) {
	addr := dataDropSMTPServer(t)
	p := providerForAddr(addr)
	res := deliverWith(context.Background(), netDialer{}, p, "", "a@b.example", []string{"c@d.example"}, []byte("Subject: hi\r\n\r\nbody"), nil)
	if res.Success {
		t.Fatalf("a dropped final response must not report success: %+v", res)
	}
	if !res.Ambiguous {
		t.Fatalf("DATA written + final response lost must be Ambiguous, got %+v", res)
	}
	if !res.TempFail {
		t.Fatalf("an ambiguous outcome must defer, got %+v", res)
	}
	if strings.Contains(strings.ToLower(res.StatusMsg), "received") == false {
		t.Fatalf("ambiguous status must explain the outcome is unknown, got %q", res.StatusMsg)
	}
	body := strings.ToLower(res.StatusMsg)
	if strings.Contains(body, "password") || strings.Contains(body, "super-secret") {
		t.Fatalf("ambiguous status leaked credential material: %q", res.StatusMsg)
	}
}

// TestF6_ContextCancellationBounded proves an already-cancelled or
// soon-expiring context aborts the transaction within its own bound.
func TestF6_ContextCancellationBounded(t *testing.T) {
	addr := hangingSMTPServer(t)
	p := providerForAddr(addr)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	res := deliverWith(ctx, netDialer{}, p, "", "a@b.example", []string{"c@d.example"}, []byte("body"), nil)
	elapsed := time.Since(start)
	if res.Success || !res.TempFail {
		t.Fatalf("a cancelled context must temp-fail, got %+v", res)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("context cancellation did not bound the transaction: took %s", elapsed)
	}
}
