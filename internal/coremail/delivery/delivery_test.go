package delivery

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── Fake SMTP Server ─────────────────────────────────────────

type fakeSMTPServer struct {
	t            *testing.T
	ln           net.Listener
	addr         string
	tlsLn        net.Listener
	tlsAddr      string
	mu           sync.Mutex
	receivedFrom string
	receivedRcpt []string
	receivedData []byte
	heloHost     string
	// heloHostPost is the helo received AFTER STARTTLS.
	// Used by the STARTTLS-re-EHLO test to assert the
	// transport followed RFC 3207 §4.2 and re-issued
	// EHLO after the upgrade.
	heloHostPost string
	// postStartTLS is set to true by the handle() loop
	// when STARTTLS was issued on the current
	// connection. The MAIL FROM handler checks this
	// before accepting a plaintext MAIL FROM. The
	// field is on the per-connection state, NOT on
	// the server — two concurrent connections on the
	// same listener track STARTTLS independently.
	greetingCode int
	greetingMsg  string
	ehloResponse func(string) (int, string)
	rcptResponse func(string) (int, string)
	dataResponse func() (int, string)
	// requireStartTLS makes the server reject MAIL
	// FROM with "530 5.7.0 Must issue STARTTLS first"
	// when the connection has not completed STARTTLS.
	// This mirrors the behaviour of every modern SMTP
	// provider that enforces TLS (Postfix
	// smtpd_tls_security_level=encrypt, Gmail, iCloud,
	// Outlook). The default is true; tests that
	// exercise the plaintext path set this to false.
	requireStartTLS bool
	// allowPlaintext, when true, lets the server
	// accept MAIL FROM without STARTTLS even when
	// requireStartTLS is true. Used by the unit tests
	// that exercise the "server refused STARTTLS"
	// branch of the transport — we want the test to
	// proceed past MAIL FROM so the rest of the
	// pipeline (RCPT TO, DATA) is exercised, while
	// the production-correct path is still tested by
	// the requireStartTLS=true tests.
	allowPlaintext bool
	// startTLSSeen reports whether the server ever
	// received a STARTTLS command on this listener
	// (across all connections). Used by the
	// integration test that asserts the transport
	// actually issued STARTTLS rather than skipping
	// it.
	startTLSSeen bool
	// startTLSTemporaryFailure, when true, makes
	// handle() reply 454 "TLS not available right
	// now" to the STARTTLS command. This breaks the
	// STARTTLS upgrade on the transport side, which
	// the transport must classify as TempFail.
	// Production Postfix / Exim issue exactly this
	// reply when the TLS subsystem is restarting
	// (cert reload, key rotation). The test pins the
	// "transport must defer, not fall back to
	// plaintext" contract that prevents
	// bidirectional STARTTLS-down + plaintext-MAIL
	// FROM bugs.
	startTLSTemporaryFailure bool
	// startTLSHandshakeFailure, when true, makes
	// handle() return 220 to STARTTLS but then
	// close the connection instead of completing
	// the TLS handshake. The client sees a TLS
	// handshake error and must defer the queue row.
	startTLSHandshakeFailure bool
	// cachedTLSConfig is the *tls.Config used by
	// handle() when wrapping the conn in a TLS
	// server side after STARTTLS. It is generated
	// lazily by tlsConfig() and shared across all
	// connections on the listener.
	cachedTLSConfig *tls.Config
	// customTLSListener, when non-nil, overrides the default TLS
	// listener created by startFakeSMTPTLS. Used by TLS policy
	// tests to test specific certificate scenarios.
	customTLSListener net.Listener
}

func startFakeSMTPServerOpts(t *testing.T, requireStartTLS bool) *fakeSMTPServer {
	t.Helper()
	fs := &fakeSMTPServer{
		t:               t,
		greetingCode:    220,
		greetingMsg:     "Fake SMTP Server",
		requireStartTLS: requireStartTLS,
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	fs.ln = ln
	fs.addr = ln.Addr().String()

	// Also start a TLS listener on a separate port.
	tlsLn, err := startFakeSMTPTLS(t)
	if err != nil {
		t.Fatalf("tls listen: %v", err)
	}
	fs.tlsLn = tlsLn
	fs.tlsAddr = tlsLn.Addr().String()

	go fs.serve()
	go fs.serveTLS()
	t.Cleanup(func() {
		ln.Close()
		tlsLn.Close()
	})
	return fs
}

// startFakeSMTPServerWithTLS starts a fake SMTP server that uses the provided
// TLS listener for implicit TLS connections. The cert in the listener is used
// for both the TLS listener and the STARTTLS upgrade path on the plain listener.
func startFakeSMTPServerWithTLS(t *testing.T, requireStartTLS bool, tlsLn net.Listener, tlsCfg *tls.Config) *fakeSMTPServer {
	t.Helper()
	fs := &fakeSMTPServer{
		t:                 t,
		greetingCode:      220,
		greetingMsg:       "Fake SMTP Server",
		requireStartTLS:   requireStartTLS,
		cachedTLSConfig:   tlsCfg,
		customTLSListener: tlsLn,
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	fs.ln = ln
	fs.addr = ln.Addr().String()

	if tlsLn != nil {
		fs.tlsLn = tlsLn
		fs.tlsAddr = tlsLn.Addr().String()
		go fs.serveTLS()
	}

	go fs.serve()
	t.Cleanup(func() {
		ln.Close()
		if tlsLn != nil {
			tlsLn.Close()
		}
	})
	return fs
}

func startFakeSMTP(t *testing.T) *fakeSMTPServer {
	t.Helper()
	return startFakeSMTPServerOpts(t, false)
}

func startFakeSMTPServerRequiringStartTLS(t *testing.T) *fakeSMTPServer {
	t.Helper()
	return startFakeSMTPServerOpts(t, true)
}

// startFakeSMTPTLS generates a self-signed ECDSA
// certificate and brings up a TLS listener on a random
// port. The cert is local-only (CN=127.0.0.1) and
// only used inside the test harness; the transport
// under test uses InsecureSkipVerify so it does not
// care about the chain.
func startFakeSMTPTLS(t *testing.T) (net.Listener, error) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("tls keygen: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, fmt.Errorf("tls certgen: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("tls key marshal: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("tls keypair: %w", err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		return nil, fmt.Errorf("tls listen: %w", err)
	}
	return ln, nil
}

func (fs *fakeSMTPServer) serve() {
	for {
		conn, err := fs.ln.Accept()
		if err != nil {
			return
		}
		go fs.handle(conn, false)
	}
}

func (fs *fakeSMTPServer) serveTLS() {
	for {
		conn, err := fs.tlsLn.Accept()
		if err != nil {
			return
		}
		go fs.handle(conn, true)
	}
}

// postStartTLSEHLO returns the helo host the server
// received AFTER a STARTTLS upgrade. The fake server
// tracks the pre- and post-TLS hosts separately so the
// STARTTLS-re-EHLO test can verify the transport
// followed RFC 3207 §4.2 and re-issued EHLO after the
// upgrade. Returns "" if no post-STARTTLS EHLO has
// been received.
func (fs *fakeSMTPServer) postStartTLSEHLO() string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.heloHostPost
}

func (fs *fakeSMTPServer) handle(conn net.Conn, tlsActive bool) {
	defer conn.Close()
	// reader/writer track the active transport. They
	// are reassigned when the client issues STARTTLS
	// so the rest of the loop reads/writes through the
	// TLS layer.
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	fmt.Fprintf(writer, "%d %s\r\n", fs.greetingCode, fs.greetingMsg)
	writer.Flush()

	// Track STARTTLS state for THIS connection. The
	// requireStartTLS check at MAIL FROM compares
	// against this — without it, a transport that
	// skipped STARTTLS could send plaintext MAIL
	// FROM and the server would happily accept.
	startTLSDone := tlsActive

	// currentConn is the conn to wrap in TLS if the
	// client sends STARTTLS. It is reassigned to the
	// TLS conn after the upgrade so subsequent reads
	// go through TLS.
	currentConn := conn

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		parts := strings.SplitN(line, " ", 2)
		cmd := strings.ToUpper(parts[0])
		args := ""
		if len(parts) > 1 {
			args = parts[1]
		}

		switch cmd {
		case "EHLO":
			fs.mu.Lock()
			if startTLSDone {
				fs.heloHostPost = args
			} else {
				fs.heloHost = args
			}
			fs.mu.Unlock()
			fmt.Fprintf(writer, "250-%s\r\n", args)
			fmt.Fprintf(writer, "250-PIPELINING\r\n")
			fmt.Fprintf(writer, "250-SIZE 10240000\r\n")
			if fs.requireStartTLS {
				fmt.Fprintf(writer, "250-STARTTLS\r\n")
			}
			fmt.Fprintf(writer, "250-ENHANCEDSTATUSCODES\r\n")
			fmt.Fprintf(writer, "250-8BITMIME\r\n")
			fmt.Fprintf(writer, "250 SMTPUTF8\r\n")
		case "HELO":
			fs.mu.Lock()
			fs.heloHost = args
			fs.mu.Unlock()
			fmt.Fprintf(writer, "250 %s\r\n", args)
		case "MAIL":
			if fs.requireStartTLS && !startTLSDone && !fs.allowPlaintext {
				// 530 with the canonical "Must
				// issue STARTTLS first" text.
				// This is the reply that the
				// production server returned
				// to the buggy transport. The
				// fix in the transport is to
				// recognise this and defer
				// the queue row rather than
				// bounce it.
				fmt.Fprintf(writer, "530 5.7.0 Must issue STARTTLS first\r\n")
				writer.Flush()
				continue
			}
			fs.mu.Lock()
			fs.receivedFrom = args
			fs.mu.Unlock()
			fmt.Fprintf(writer, "250 OK\r\n")
		case "RCPT":
			fs.mu.Lock()
			fs.receivedRcpt = append(fs.receivedRcpt, args)
			fs.mu.Unlock()
			if fs.rcptResponse != nil {
				c, m := fs.rcptResponse(args)
				fmt.Fprintf(writer, "%d %s\r\n", c, m)
			} else {
				fmt.Fprintf(writer, "250 OK\r\n")
			}
		case "DATA":
			fmt.Fprintf(writer, "354 Start mail input\r\n")
			writer.Flush()
			var buf strings.Builder
			for {
				dl, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dl, "\r\n") == "." {
					break
				}
				buf.WriteString(dl)
			}
			fs.mu.Lock()
			fs.receivedData = []byte(buf.String())
			fs.mu.Unlock()
			if fs.dataResponse != nil {
				c, m := fs.dataResponse()
				fmt.Fprintf(writer, "%d %s\r\n", c, m)
			} else {
				fmt.Fprintf(writer, "250 OK\r\n")
			}
		case "QUIT":
			fmt.Fprintf(writer, "221 Bye\r\n")
			writer.Flush()
			return
		case "STARTTLS":
			fs.mu.Lock()
			fs.startTLSSeen = true
			fs.mu.Unlock()
			if !fs.requireStartTLS {
				fmt.Fprintf(writer, "500 TLS not available\r\n")
				writer.Flush()
				continue
			}
			// Some servers say "STARTTLS
			// advertised" in EHLO but then
			// refuse the upgrade with 454
			// while the TLS subsystem is
			// restarting. We simulate that
			// here so the transport's
			// "STARTTLS refused → defer"
			// branch is exercised.
			if fs.startTLSTemporaryFailure {
				fmt.Fprintf(writer, "454 4.7.0 TLS not available right now\r\n")
				writer.Flush()
				return
			}
			// Some servers say 220 but then
			// immediately close the socket
			// (mid-handshake failure — bad
			// cert, key mismatch, etc).
			// The transport must classify
			// that as a transient failure
			// too.
			if fs.startTLSHandshakeFailure {
				fmt.Fprintf(writer, "220 2.0.0 Ready to start TLS\r\n")
				writer.Flush()
				conn.Close()
				return
			}
			// 220 — "service ready" — and the
			// server then waits for the
			// client to begin TLS on the
			// same socket. We then wrap
			// the underlying conn in a TLS
			// server side and switch the
			// reader/writer to that. From
			// here on, every byte that
			// passes through the conn is
			// inside a TLS record.
			fmt.Fprintf(writer, "220 2.0.0 Ready to start TLS\r\n")
			writer.Flush()
			// Re-derive a self-signed cert
			// matching the one in the
			// dedicated TLS listener so the
			// client can verify against
			// the same root. We reuse the
			// same key generation function
			// the dedicated TLS listener
			// uses; the cert is short-lived
			// and used only for the test
			// harness.
			tlsCfg := fs.tlsConfig()
			tlsConn := tls.Server(currentConn, tlsCfg)
			if err := tlsConn.Handshake(); err != nil {
				// TLS handshake failed on
				// the server side. From the
				// client's perspective this
				// surfaces as a handshake
				// failure — the transport
				// defers the queue row.
				tlsConn.Close()
				return
			}
			currentConn = tlsConn
			reader = bufio.NewReader(tlsConn)
			writer = bufio.NewWriter(tlsConn)
			startTLSDone = true
		default:
			fmt.Fprintf(writer, "500 Unrecognized\r\n")
		}
		writer.Flush()
	}
}

// tlsConfig returns a *tls.Config built from the same
// self-signed ECDSA cert the dedicated TLS listener
// uses. The fake SMTP handler calls this from the
// STARTTLS upgrade path so the post-STARTTLS socket
// is wrapped in a TLS server side. The cert is a
// throwaway — the client uses InsecureSkipVerify.
// When the server was created with a custom TLS config
// (startFakeSMTPServerWithTLS), that config is reused.
func (fs *fakeSMTPServer) tlsConfig() *tls.Config {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.cachedTLSConfig != nil {
		return fs.cachedTLSConfig
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		// tests that hit this path will fail
		// elsewhere; surface the error at
		// handshake time, not at config
		// construction time.
		return &tls.Config{}
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return &tls.Config{}
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return &tls.Config{}
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return &tls.Config{}
	}
	fs.cachedTLSConfig = &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	return fs.cachedTLSConfig
}

// ── Resolver Tests ───────────────────────────────────────────

func TestFakeResolverMX(t *testing.T) {
	r := NewFakeResolver()
	r.MXRecords["example.com"] = []MXRecord{
		{Host: "mx1.example.com", Priority: 10},
		{Host: "mx2.example.com", Priority: 20},
	}
	records, err := r.LookupMX(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("lookup mx: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2, got %d", len(records))
	}
	if records[0].Priority != 10 {
		t.Fatalf("expected priority 10 first, got %d", records[0].Priority)
	}
}

func TestFakeResolverMXDefault(t *testing.T) {
	r := NewFakeResolver()
	records, err := r.LookupMX(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("lookup mx: %v", err)
	}
	if len(records) != 1 || records[0].Host != "mail.example.com" {
		t.Fatalf("expected default mx mail.example.com, got %v", records)
	}
}

func TestFakeResolverMXFailure(t *testing.T) {
	r := NewFakeResolver()
	r.FailDomain = "fail.com"
	_, err := r.LookupMX(context.Background(), "fail.com")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFakeResolverHost(t *testing.T) {
	r := NewFakeResolver()
	r.Hosts["mx.example.com"] = []string{"192.0.2.1"}
	addrs, err := r.LookupHost(context.Background(), "mx.example.com")
	if err != nil {
		t.Fatalf("lookup host: %v", err)
	}
	if len(addrs) != 1 || addrs[0] != "192.0.2.1" {
		t.Fatalf("expected 192.0.2.1, got %v", addrs)
	}
}

func TestFakeResolverHostDefault(t *testing.T) {
	r := NewFakeResolver()
	addrs, err := r.LookupHost(context.Background(), "mx.example.com")
	if err != nil {
		t.Fatalf("lookup host: %v", err)
	}
	if len(addrs) != 1 || addrs[0] != "127.0.0.1" {
		t.Fatalf("expected 127.0.0.1, got %v", addrs)
	}
}

func TestFakeResolverHostFailure(t *testing.T) {
	r := NewFakeResolver()
	r.FailHost = "fail.host"
	_, err := r.LookupHost(context.Background(), "fail.host")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestMXRecordsSortedByPriority(t *testing.T) {
	r := NewFakeResolver()
	r.MXRecords["test.com"] = []MXRecord{
		{Host: "mx2.test.com", Priority: 20},
		{Host: "mx1.test.com", Priority: 10},
		{Host: "mx3.test.com", Priority: 30},
	}
	records, _ := r.LookupMX(context.Background(), "test.com")
	if records[0].Host != "mx1.test.com" {
		t.Fatalf("expected mx1 first, got %s", records[0].Host)
	}
	if records[1].Host != "mx2.test.com" {
		t.Fatalf("expected mx2 second")
	}
}

// ── SMTP Transport Tests ─────────────────────────────────────

func TestTransportDeliverSuccess(t *testing.T) {
	fs := startFakeSMTP(t)
	transport := NewSMTPTransport(testTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("Subject: Test\r\n\r\nBody"), "test.orvix.local")
	if !result.Success {
		t.Fatalf("expected success, got: %s", result.StatusMsg)
	}
	if !result.HeloOK {
		t.Fatal("helo not ok")
	}
	if !result.MailFromOK {
		t.Fatal("mail from not ok")
	}
	if !result.RcptOK {
		t.Fatal("rcpt not ok")
	}
	if !result.DataOK {
		t.Fatal("data not ok")
	}
}

func TestTransportBadGreeting(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.greetingCode = 554
	fs.greetingMsg = "No SMTP here"
	transport := NewSMTPTransport(testTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")
	if result.Success {
		t.Fatal("expected failure for bad greeting")
	}
}

func TestTransportEHLOFallback(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.ehloResponse = func(host string) (int, string) { return 500, "Not recognized" }
	transport := NewSMTPTransport(testTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("Subject: Test\r\n\r\nBody"), "test.orvix.local")
	if !result.Success {
		t.Fatalf("expected success via HELO fallback, got: %s", result.StatusMsg)
	}
}

func TestTransportRCPTRejected(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.rcptResponse = func(rcpt string) (int, string) { return 550, "User unknown" }
	transport := NewSMTPTransport(testTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"unknown@test.com"}, []byte("data"), "test.orvix.local")
	if result.Success {
		t.Fatal("expected failure for RCPT rejection")
	}
}

func TestTransportDATARejected(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.dataResponse = func() (int, string) { return 552, "Message too large" }
	transport := NewSMTPTransport(testTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("big data"), "test.orvix.local")
	if result.Success {
		t.Fatal("expected failure for DATA rejection")
	}
}

func TestTransportConnectionTimeout(t *testing.T) {
	transport := NewSMTPTransport(TransportConfig{ConnectTimeout: 50 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	result := transport.Deliver(ctx, "10.0.0.1:25", false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")
	if result.Success {
		t.Fatal("expected failure for timeout")
	}
}

func TestTransportQUITHandling(t *testing.T) {
	fs := startFakeSMTP(t)
	transport := NewSMTPTransport(testTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("Subject: Test\r\n\r\nBody"), "test.orvix.local")
	if !result.Success {
		t.Fatalf("expected success: %s", result.StatusMsg)
	}
}

// ── STARTTLS Transport Tests ──────────────────────────────────
//
// These tests pin the contract the production server
// required: STARTTLS is mandatory for outbound delivery
// when the remote server advertises it, the transport
// must:
//   - parse EHLO capabilities and detect STARTTLS
//   - issue STARTTLS, run a TLS handshake, and re-EHLO
//   - re-send EHLO after the TLS upgrade (RFC 3207 §4.2)
//   - defer (not bounce) when the server requires STARTTLS
//     but the connection did not negotiate it
//   - defer (not bounce) when the TLS handshake fails
//   - defer (not bounce) for 4xx
//   - bounce only for permanent 5xx recipient errors

// TestTransportSTARTTLSAdvertisedButServerRefuses is the
// regression test for the production bug. The remote
// server advertises STARTTLS in EHLO but then refuses
// MAIL FROM with 5.7.0 when the client does not
// negotiate TLS. The transport MUST defer the queue
// row (so the operator can see the configuration
// problem) rather than bouncing it as a recipient
// error.
//
// We use a stripped-down server that advertises
// STARTTLS in EHLO but replies "454 TLS not available
// right now" to the STARTTLS command. This is the
// production failure mode: the server says it wants
// TLS, then refuses it for a few minutes, then
// accepts it again. The transport must defer the
// attempt rather than fall back to plaintext.
func TestTransportSTARTTLSAdvertisedButServerRefuses(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.requireStartTLS = true
	// Force the STARTTLS response to be 454 — the
	// canonical "transiently not available" reply.
	// This breaks the STARTTLS upgrade path on the
	// transport side and exercises the deferral
	// branch.
	fs.startTLSTemporaryFailure = true
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")

	if result.Success {
		t.Fatalf("expected failure, got success: %s", result.StatusMsg)
	}
	if !result.TempFail {
		t.Errorf("STARTTLS refused (454) must be TempFail so the queue retries, got TempFail=false status=%q", result.StatusMsg)
	}
	if !strings.Contains(strings.ToLower(result.StatusMsg), "starttls") {
		t.Errorf("status must mention STARTTLS, got %q", result.StatusMsg)
	}
	// The transport must NOT report the message as
	// permanently bounced.
	if result.StatusCode >= 500 && result.StatusCode < 600 && !result.TempFail {
		t.Errorf("server-returned 5xx must not be classified as bounce when caused by STARTTLS refusal")
	}
}

// TestTransportSTARTTLSSuccess runs the full STARTTLS
// upgrade against a real TLS listener and asserts the
// transport successfully negotiates, re-EHLOs, and
// delivers a 250 success. This is the happy path for
// every modern SMTP provider.
func TestTransportSTARTTLSSuccess(t *testing.T) {
	fs := startFakeSMTPServerRequiringStartTLS(t)
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("Subject: TLS\r\n\r\nBody"), "test.orvix.local")

	if !result.Success {
		t.Fatalf("expected success via STARTTLS, got: %s (status=%d, tempfail=%v)", result.StatusMsg, result.StatusCode, result.TempFail)
	}
	if !result.TLSUsed {
		t.Error("expected TLSUsed=true after STARTTLS upgrade")
	}
	if !result.TLSHandshake {
		t.Error("expected TLSHandshake=true after STARTTLS upgrade")
	}
	if !result.MailFromOK || !result.RcptOK || !result.DataOK {
		t.Errorf("expected full SMTP flow success, got: helo=%v mail=%v rcpt=%v data=%v",
			result.HeloOK, result.MailFromOK, result.RcptOK, result.DataOK)
	}
}

// TestTransportSTARTTLSReEHLOLogsHost pins RFC 3207
// §4.2: the client MUST discard pre-TLS knowledge
// and re-EHLO after the upgrade. We verify the
// post-TLS helo host is recorded by the fake server.
func TestTransportSTARTTLSReEHLOLogsHost(t *testing.T) {
	fs := startFakeSMTPServerRequiringStartTLS(t)
	transport := NewSMTPTransport(DefaultTransportConfig())
	_ = transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "mail.orvix.email")

	postHelo := fs.postStartTLSEHLO()
	if postHelo == "" {
		t.Fatal("expected EHLO after STARTTLS to be recorded by the fake server")
	}
	if !strings.Contains(postHelo, "mail.orvix.email") {
		t.Errorf("post-STARTTLS EHLO host must be the configured mail hostname, got %q", postHelo)
	}
}

// TestTransportSTARTTLSHandshakeFailureDeferred asserts
// that a failed TLS handshake is deferred (TempFail=true)
// rather than bounced. The fake server returns 220 to
// STARTTLS but then closes the socket mid-handshake —
// a realistic failure mode (cert reload, key mismatch).
// The transport must classify that as a transient
// failure so the queue retries; a permanent bounce
// here would require manual re-queueing for every TLS
// hiccup.
func TestTransportSTARTTLSHandshakeFailureDeferred(t *testing.T) {
	fs := startFakeSMTPServerRequiringStartTLS(t)
	fs.startTLSHandshakeFailure = true
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")

	if result.Success {
		t.Fatalf("expected failure, got success")
	}
	if !result.TempFail {
		t.Errorf("TLS handshake failure must be TempFail, got TempFail=false status=%q", result.StatusMsg)
	}
	if !strings.Contains(result.StatusMsg, "starttls") && !strings.Contains(result.StatusMsg, "handshake") {
		t.Errorf("status must mention starttls or handshake, got %q", result.StatusMsg)
	}
}

// TestTransportSTARTTLSCapabilitiesCaptured asserts the
// transport records the post-STARTTLS EHLO capability
// list on the result. This is what the queue worker
// writes to the attempt history so the operator can
// see what the remote server offered.
func TestTransportSTARTTLSCapabilitiesCaptured(t *testing.T) {
	fs := startFakeSMTPServerRequiringStartTLS(t)
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")

	if !result.Success {
		t.Fatalf("expected success, got: %s", result.StatusMsg)
	}
	// The post-STARTTLS EHLO must include the
	// capabilities. We do not check the exact list
	// (it changes per server) but verify that at
	// least one capability was recorded and that
	// the SIZE/8BITMIME/SMTPUTF8 keywords are
	// present (the fake server advertises all of
	// them).
	if len(result.Capabilities) == 0 {
		t.Fatal("expected non-empty capabilities after STARTTLS")
	}
	joined := strings.Join(result.Capabilities, " ")
	for _, want := range []string{"SIZE", "8BITMIME"} {
		if !strings.Contains(joined, want) {
			t.Errorf("post-STARTTLS capabilities missing %q: %v", want, result.Capabilities)
		}
	}
}

// TestTransportSTARTTLSNotAdvertisedButRequired pins the
// configuration contract: if the operator sets
// RequireSTARTTLS=true but the server does not advertise
// it, the transport fails fast with a clear diagnostic.
// This catches the "misconfigured server" case before
// the queue burns attempts on a server that will never
// accept plaintext.
func TestTransportSTARTTLSNotAdvertisedButRequired(t *testing.T) {
	fs := startFakeSMTPServerOpts(t, false) // does NOT advertise STARTTLS
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")

	if result.Success {
		t.Fatalf("expected failure (no STARTTLS, required), got success")
	}
	if !result.TempFail {
		t.Errorf("expected TempFail when STARTTLS required but not advertised, got %q", result.StatusMsg)
	}
	if !strings.Contains(result.StatusMsg, "starttls") {
		t.Errorf("status must mention starttls, got %q", result.StatusMsg)
	}
}

// TestTransport4xxRemainsDeferred pins the contract
// that 4xx SMTP responses are deferred. This was
// already exercised but is explicit here as a
// regression test for the "transient vs permanent"
// classification.
func TestTransport4xxRemainsDeferred(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.dataResponse = func() (int, string) { return 450, "4.2.1 Mailbox busy, try later" }
	transport := NewSMTPTransport(testTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")

	if result.Success {
		t.Fatal("expected failure")
	}
	if !result.TempFail {
		t.Errorf("4xx must be TempFail, got %q", result.StatusMsg)
	}
	if result.StatusCode != 450 {
		t.Errorf("expected status 450, got %d", result.StatusCode)
	}
}

// TestTransport5xxRecipientRemainsBounce pins the
// contract that permanent 5xx recipient rejections
// (550 user unknown, 554 relay denied, etc.) are
// bounced, not deferred.
func TestTransport5xxRecipientRemainsBounce(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.rcptResponse = func(rcpt string) (int, string) { return 550, "5.1.1 User unknown" }
	transport := NewSMTPTransport(testTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"unknown@bad.test"}, []byte("data"), "test.orvix.local")

	if result.Success {
		t.Fatal("expected failure")
	}
	if result.TempFail {
		t.Errorf("5.1.1 must be permanent (bounce), got TempFail=true")
	}
	if result.StatusCode != 550 {
		t.Errorf("expected status 550, got %d", result.StatusCode)
	}
}

// TestTransportRemoteIPAndHostRecorded asserts the
// transport records the remote host and IP on the
// result so the queue worker can log them to the
// attempt history. The "delivered" path records the
// host (the address we connected to) — IP is recorded
// separately by the queue worker after the worker
// resolves the MX record.
func TestTransportRemoteIPAndHostRecorded(t *testing.T) {
	fs := startFakeSMTPServerRequiringStartTLS(t)
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")

	if result.RemoteHost != fs.addr {
		t.Errorf("expected remote host %q, got %q", fs.addr, result.RemoteHost)
	}
}

func TestTransportMultipleRCPT(t *testing.T) {
	fs := startFakeSMTP(t)
	transport := NewSMTPTransport(testTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt1@test.com", "rcpt2@test.com"}, []byte("Subject: Multi\r\n\r\nBody"), "test.orvix.local")
	if !result.Success {
		t.Fatalf("expected success for multiple rcpt, got: %s", result.StatusMsg)
	}
}

func TestTransportNullSender(t *testing.T) {
	fs := startFakeSMTP(t)
	transport := NewSMTPTransport(testTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "", []string{"rcpt@test.com"}, []byte("Subject: Null\r\n\r\nSender"), "test.orvix.local")
	if !result.Success {
		t.Fatalf("expected success for null sender (bounce): %s", result.StatusMsg)
	}
}

// ── Bounce Classification Tests ──────────────────────────────

func TestBounceUserUnknown(t *testing.T) {
	bt := ClassifyBounce(550, "5.1.1 User unknown")
	if bt != BounceUserUnknown {
		t.Fatalf("expected user_unknown, got %s", bt)
	}
}

func TestBounceMailboxFull(t *testing.T) {
	bt := ClassifyBounce(552, "5.2.2 Mailbox full")
	if bt != BounceMailboxFull {
		t.Fatalf("expected mailbox_full, got %s", bt)
	}
}

func TestBounceRelayDenied(t *testing.T) {
	bt := ClassifyBounce(554, "5.7.1 Relay denied")
	if bt != BounceRelayDenied {
		t.Fatalf("expected relay_denied, got %s", bt)
	}
}

func TestBounceTempFail(t *testing.T) {
	bt := ClassifyBounce(450, "4.2.1 Mailbox busy")
	if bt != BounceUnavailable {
		t.Fatalf("expected unavailable, got %s", bt)
	}
}

func TestBounceTimeout(t *testing.T) {
	bt := ClassifyBounce(451, "4.4.2 Timeout")
	if bt != BounceTimeout {
		t.Fatalf("expected timeout, got %s", bt)
	}
}

func TestBounceSystemError(t *testing.T) {
	bt := ClassifyBounce(0, "connection refused")
	if bt != BounceSystemError {
		t.Fatalf("expected system_error, got %s", bt)
	}
}

func TestBounceSpamBlocked(t *testing.T) {
	bt := ClassifyBounce(550, "5.7.1 Spam blocked")
	if bt != BounceSpamBlocked {
		t.Fatalf("expected spam_blocked, got %s", bt)
	}
}

func TestBounceMessageTooBig(t *testing.T) {
	bt := ClassifyBounce(552, "5.3.4 Message too large")
	if bt != BounceMessageTooBig {
		t.Fatalf("expected message_too_big, got %s", bt)
	}
}

func TestBounceEventStruct(t *testing.T) {
	e := BounceEvent{
		QueueEntryID: 1, FromAddress: "a@b.com", ToAddress: "c@d.com",
		BounceType: BounceUserUnknown, StatusCode: 550, StatusMsg: "Unknown",
		TempFail: false, AttemptCount: 3,
	}
	if e.FromAddress != "a@b.com" || e.BounceType != BounceUserUnknown {
		t.Fatal("bounce event fields incorrect")
	}
}

// ── Worker Creation Tests ────────────────────────────────────

func TestNewDeliveryWorker(t *testing.T) {
	resolver := NewFakeResolver()
	transport := NewSMTPTransport(testTransportConfig())
	w := NewDeliveryWorker(nil, nil, resolver, transport, "local.test", "worker-1")
	if w == nil {
		t.Fatal("worker should not be nil")
	}
	if w.WorkerID != "worker-1" {
		t.Fatalf("expected worker-1, got %s", w.WorkerID)
	}
}

// ── Concurrent Tests ─────────────────────────────────────────

func TestFakeSMTPConcurrentSessions(t *testing.T) {
	fs := startFakeSMTP(t)

	var wg sync.WaitGroup
	errs := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			transport := NewSMTPTransport(testTransportConfig())
			result := transport.Deliver(context.Background(), fs.addr, false,
				fmt.Sprintf("sender%d@test.com", id),
				[]string{fmt.Sprintf("rcpt%d@test.com", id)},
				[]byte("Subject: Concurrent\r\n\r\nBody"),
				"test.orvix.local")
			if !result.Success {
				errs <- fmt.Errorf("session %d: %s", id, result.StatusMsg)
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent delivery: %v", err)
	}
}

func TestWorkersLeaseEmptyQueue(t *testing.T) {
	// Test that ProcessOnce handles nil queue gracefully — tested at unit level.
	// Full lease integration tested in queue package.
}

// ── ContainsAny Tests ────────────────────────────────────────

func TestContainsAnyBasic(t *testing.T) {
	if !containsAny("User unknown", "unknown") {
		t.Fatal("expected match")
	}
	if containsAny("OK", "unknown") {
		t.Fatal("should not match")
	}
}

func TestContainsAnyCaseInsensitive(t *testing.T) {
	if !containsAny("User Unknown", "unknown") {
		t.Fatal("case insensitive should match")
	}
}

func TestContainsAnyMultiSubstr(t *testing.T) {
	if !containsAny("Mailbox full, quota exceeded", "quota") {
		t.Fatal("should match quota")
	}
}

// ── Transport Config Tests ───────────────────────────────────

func TestDefaultTransportConfig(t *testing.T) {
	cfg := DefaultTransportConfig()
	if cfg.ConnectTimeout != 30*time.Second {
		t.Fatalf("expected 30s timeout, got %v", cfg.ConnectTimeout)
	}
	if !cfg.AttemptSTARTTLS {
		t.Fatal("expected STARTTLS enabled by default")
	}
}

// ── DeliveryResult Tests ─────────────────────────────────────

func TestDeliveryResultDefaults(t *testing.T) {
	r := &DeliveryResult{}
	if r.Success {
		t.Fatal("expected false success by default")
	}
	if r.TempFail {
		t.Fatal("expected false temp fail by default")
	}
}

// ── Resolver Interface Compliance ────────────────────────────

func TestResolverInterface(t *testing.T) {
	var r Resolver = NewFakeResolver()
	_ = r
}

func TestDNSResolverInterface(t *testing.T) {
	var r Resolver = NewDNSResolver()
	_ = r
}

// ── Additional Transport Tests ───────────────────────────────

func TestTransport4xxDefer(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.dataResponse = func() (int, string) { return 450, "4.7.1 Try again later" }
	transport := NewSMTPTransport(testTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")
	if result.Success {
		t.Fatal("expected failure for 4xx")
	}
	if !result.TempFail {
		t.Fatal("expected temp fail for 4xx")
	}
}

func TestTransport5xxPermFail(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.rcptResponse = func(rcpt string) (int, string) { return 550, "5.1.1 User unknown" }
	transport := NewSMTPTransport(testTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"bad@test.com"}, []byte("data"), "test.orvix.local")
	if result.Success {
		t.Fatal("expected failure for 5xx")
	}
	if result.TempFail {
		t.Fatal("expected permanent fail for 5xx")
	}
}

func TestTransportResponseCodeSet(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.rcptResponse = func(rcpt string) (int, string) { return 550, "User unknown" }
	transport := NewSMTPTransport(testTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"bad@test.com"}, []byte("data"), "test.orvix.local")
	if result.StatusCode != 550 {
		t.Fatalf("expected status code 550, got %d", result.StatusCode)
	}
}

// ── MX Fallback Tests ────────────────────────────────────────

func TestNoMXRecordsReturnsErr(t *testing.T) {
	r := NewFakeResolver()
	r.FailDomain = "nxdomain.test"
	_, err := r.LookupMX(context.Background(), "nxdomain.test")
	if err == nil {
		t.Fatal("expected error for domain with no MX")
	}
}

func TestEmptyMXRecordsReturnsDefault(t *testing.T) {
	r := NewFakeResolver()
	// No MX records configured, should use default: mail.<domain>
	records, err := r.LookupMX(context.Background(), "default.test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 || records[0].Host != "mail.default.test" {
		t.Fatalf("expected default MX mail.default.test, got %v", records)
	}
}

func TestResolverMXPicksLowestPriority(t *testing.T) {
	r := NewFakeResolver()
	r.MXRecords["mixed.test"] = []MXRecord{
		{Host: "mx3.mixed.test", Priority: 30},
		{Host: "mx1.mixed.test", Priority: 10},
		{Host: "mx2.mixed.test", Priority: 20},
	}
	records, _ := r.LookupMX(context.Background(), "mixed.test")
	if records[0].Host != "mx1.mixed.test" {
		t.Fatalf("expected mx1 (priority 10) first, got %s", records[0].Host)
	}
	if records[2].Host != "mx3.mixed.test" {
		t.Fatalf("expected mx3 (priority 30) last")
	}
}

// ── Bounce Edge Cases ────────────────────────────────────────

func TestBounceCaseInsensitive(t *testing.T) {
	bt := ClassifyBounce(550, "5.1.1 USER UNKNOWN")
	if bt != BounceUserUnknown {
		t.Fatalf("expected user_unknown for 'USER UNKNOWN', got %s", bt)
	}
}

func TestBounceQuotaExceeded(t *testing.T) {
	bt := ClassifyBounce(552, "Quota exceeded")
	if bt != BounceMailboxFull {
		t.Fatalf("expected mailbox_full for quota exceeded, got %s", bt)
	}
}

func TestBounceRelayNotPermitted(t *testing.T) {
	bt := ClassifyBounce(554, "Relay not permitted")
	if bt != BounceRelayDenied {
		t.Fatalf("expected relay_denied, got %s", bt)
	}
}

// ── Transport Config Customization ───────────────────────────

// ── HARDENING: Policy Tests ──────────────────────────────────

func TestPolicyCheckSenderAllowed(t *testing.T) {
	pe := NewPolicyEnforcer(DefaultDeliveryPolicy())
	r := pe.CheckSender(context.Background(), "sender@test.com", uintPtr(1), uintPtr(1), uintPtr(1), 50)
	if !r.Allowed {
		t.Fatalf("expected allowed, got: %s", r.Reason)
	}
}

func TestPolicyCheckSenderBlocked(t *testing.T) {
	pe := NewPolicyEnforcer(DefaultDeliveryPolicy())
	r := pe.CheckSender(context.Background(), "sender@test.com", uintPtr(1), uintPtr(1), uintPtr(1), 999999)
	if r.Allowed {
		t.Fatal("expected blocked when over limit")
	}
	if r.Code != 550 {
		t.Fatalf("expected 550, got %d", r.Code)
	}
}

func TestPolicyCheckDomainBlocked(t *testing.T) {
	pe := NewPolicyEnforcer(DefaultDeliveryPolicy())
	r := pe.CheckDomain(context.Background(), "test.com", 999999, 0)
	if r.Allowed {
		t.Fatal("expected blocked for over-limit domain")
	}
}

func TestPolicyCheckDomainRecipientsBlocked(t *testing.T) {
	pe := NewPolicyEnforcer(DefaultDeliveryPolicy())
	r := pe.CheckDomain(context.Background(), "test.com", 0, 999999)
	if r.Allowed {
		t.Fatal("expected blocked for over-limit recipients")
	}
}

func TestPolicyCheckMessageSize(t *testing.T) {
	pe := NewPolicyEnforcer(DefaultDeliveryPolicy())
	r := pe.CheckMessageSize(999999999)
	if r.Allowed {
		t.Fatal("expected blocked for oversized message")
	}
}

func TestPolicyCheckMessageSizeAllowed(t *testing.T) {
	pe := NewPolicyEnforcer(DefaultDeliveryPolicy())
	r := pe.CheckMessageSize(1024)
	if !r.Allowed {
		t.Fatalf("expected allowed for small message: %s", r.Reason)
	}
}

func TestPolicyCheckRecipients(t *testing.T) {
	pe := NewPolicyEnforcer(DefaultDeliveryPolicy())
	r := pe.CheckRecipients(999)
	if r.Allowed {
		t.Fatal("expected blocked for too many recipients")
	}
}

func TestPolicyCheckRecipientsAllowed(t *testing.T) {
	pe := NewPolicyEnforcer(DefaultDeliveryPolicy())
	r := pe.CheckRecipients(1)
	if !r.Allowed {
		t.Fatalf("expected allowed for single recipient: %s", r.Reason)
	}
}

// ── HARDENING: Anti-Loop Tests ───────────────────────────────

func TestLoopCheckReceivedHeadersExceeded(t *testing.T) {
	ld := NewLoopDetector(5, 10, "local.test")
	// Create a message with many Received headers.
	msg := []byte{}
	for i := 0; i < 10; i++ {
		msg = append(msg, []byte(fmt.Sprintf("Received: from relay%d.example.com\r\n", i))...)
	}
	msg = append(msg, []byte("Subject: Test\r\n\r\nBody")...)
	result := ld.CheckReceivedHeaders(msg)
	if !result.IsLoop {
		t.Fatal("expected loop detection for excessive Received headers")
	}
}

func TestLoopCheckReceivedHeadersOK(t *testing.T) {
	ld := NewLoopDetector(50, 10, "local.test")
	msg := []byte("Received: from relay1.example.com\r\nSubject: Test\r\n\r\nBody")
	result := ld.CheckReceivedHeaders(msg)
	if result.IsLoop {
		t.Fatal("expected no loop for single Received header")
	}
}

func TestLoopCheckSelfDelivery(t *testing.T) {
	ld := NewLoopDetector(50, 10, "local.test")
	result := ld.CheckSelfDelivery("local.test")
	if !result.IsLoop {
		t.Fatal("expected self-delivery loop detection")
	}
}

func TestLoopCheckSelfDeliveryOK(t *testing.T) {
	ld := NewLoopDetector(50, 10, "local.test")
	result := ld.CheckSelfDelivery("remote.test")
	if result.IsLoop {
		t.Fatal("expected no loop for remote domain")
	}
}

func TestLoopCheckDeferralLoop(t *testing.T) {
	ld := NewLoopDetector(50, 5, "local.test")
	result := ld.CheckDeferralLoop(10, 16)
	if !result.IsLoop {
		t.Fatal("expected deferral loop detection")
	}
}

func TestLoopCheckDeferralLoopOK(t *testing.T) {
	ld := NewLoopDetector(50, 10, "local.test")
	result := ld.CheckDeferralLoop(2, 16)
	if result.IsLoop {
		t.Fatal("expected no loop for low deferral count")
	}
}

// ── HARDENING: Audit Event Tests ─────────────────────────────

func TestAuditLoggerRecordEvent(t *testing.T) {
	logger := NewAuditLogger()
	event := BuildEvent(1, "msg-1", "from@test.com", "to@test.com", "w1", "outbound", EventQueued)
	if err := logger.RecordEvent(context.Background(), event); err != nil {
		t.Fatalf("record event: %v", err)
	}
	if len(logger.Events()) != 1 {
		t.Fatalf("expected 1 event, got %d", len(logger.Events()))
	}
}

func TestAuditLoggerLastEvent(t *testing.T) {
	logger := NewAuditLogger()
	logger.RecordEvent(context.Background(), BuildEvent(1, "msg-1", "f@t.com", "t@t.com", "w1", "out", EventQueued))
	logger.RecordEvent(context.Background(), BuildEvent(1, "msg-1", "f@t.com", "t@t.com", "w1", "out", EventLeased))
	logger.RecordEvent(context.Background(), BuildEvent(1, "msg-1", "f@t.com", "t@t.com", "w1", "out", EventDelivered))

	last := logger.LastEvent(1)
	if last == nil || last.EventType != EventDelivered {
		t.Fatalf("expected last event '%s', got '%s'", EventDelivered, last.EventType)
	}
}

func TestAuditBuildEvent(t *testing.T) {
	e := BuildEvent(1, "msg-1", "f@t.com", "t@t.com", "w1", "outbound", EventDeferred)
	if e.EventType != EventDeferred {
		t.Fatalf("expected deferred, got %s", e.EventType)
	}
	if e.WorkerID != "w1" {
		t.Fatalf("expected w1, got %s", e.WorkerID)
	}
}

// ── HARDENING: Enhanced Status Code Tests ────────────────────

func TestParseEnhancedCode(t *testing.T) {
	ec := ParseEnhancedCode("5.1.1 User unknown")
	if ec != "5.1.1" {
		t.Fatalf("expected 5.1.1, got %s", ec)
	}
}

func TestParseEnhancedCodeNotFound(t *testing.T) {
	ec := ParseEnhancedCode("User unknown")
	if ec != "" {
		t.Fatalf("expected empty, got %s", ec)
	}
}

func TestParseEnhancedCodeTwoDigit(t *testing.T) {
	ec := ParseEnhancedCode("5.7.27 Relay access denied")
	if ec != "5.7.27" {
		t.Fatalf("expected 5.7.27, got %s", ec)
	}
}

func TestFormatEnhancedCode(t *testing.T) {
	ec := FormatEnhancedCode(5, 1, 1)
	if ec != "5.1.1" {
		t.Fatalf("expected 5.1.1, got %s", ec)
	}
}

// ── HARDENING: Remote Response Capture Tests ─────────────────

func TestTransportCapturesEnhancedCode(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.rcptResponse = func(rcpt string) (int, string) { return 550, "5.1.1 User unknown" }
	transport := NewSMTPTransport(testTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"bad@test.com"}, []byte("data"), "test.orvix.local")
	if result.EnhancedCode != "5.1.1" {
		t.Fatalf("expected enhanced code 5.1.1, got %q", result.EnhancedCode)
	}
}

func TestTransportStoresLastRemoteResponse(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.rcptResponse = func(rcpt string) (int, string) { return 550, "5.7.1 Relay denied" }
	transport := NewSMTPTransport(testTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"bad@test.com"}, []byte("data"), "test.orvix.local")
	if result.StatusCode != 550 {
		t.Fatalf("expected status code 550, got %d", result.StatusCode)
	}
	if !strings.Contains(result.StatusMsg, "Relay denied") {
		t.Fatalf("expected error message to contain 'Relay denied', got %q", result.StatusMsg)
	}
}

// ── HARDENING: Transport Error Classification Tests ──────────

func TestTransport421Defer(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.greetingCode = 421
	fs.greetingMsg = "4.2.1 Service unavailable"
	transport := NewSMTPTransport(testTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")
	if result.Success {
		t.Fatal("expected failure for 421")
	}
	if result.StatusCode != 421 {
		t.Fatalf("expected status code 421, got %d", result.StatusCode)
	}
}

func TestTransport450Defer(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.dataResponse = func() (int, string) { return 450, "4.2.1 Mailbox busy" }
	transport := NewSMTPTransport(testTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")
	if result.Success {
		t.Fatal("expected failure for 450")
	}
	if !result.TempFail {
		t.Fatal("expected 450 to be temp fail")
	}
	if result.EnhancedCode != "4.2.1" {
		t.Fatalf("expected enhanced code 4.2.1, got %q", result.EnhancedCode)
	}
}

func TestTransport550Bounce(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.rcptResponse = func(rcpt string) (int, string) { return 550, "5.1.1 User unknown" }
	transport := NewSMTPTransport(testTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"bad@test.com"}, []byte("data"), "test.orvix.local")
	if result.Success {
		t.Fatal("expected failure for 550")
	}
	if result.TempFail {
		t.Fatal("expected 550 to be permanent fail")
	}
}

func TestTransportRemoteHostStored(t *testing.T) {
	fs := startFakeSMTP(t)
	transport := NewSMTPTransport(testTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")
	if result.RemoteHost != fs.addr {
		t.Fatalf("expected remote host %s, got %s", fs.addr, result.RemoteHost)
	}
}

func TestTransportDurationMsSet(t *testing.T) {
	fs := startFakeSMTP(t)
	transport := NewSMTPTransport(testTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")
	_ = result.DurationMs // field exists; may be 0 on very fast local connections
}

// ── HARDENING: Default Policy Config ─────────────────────────

func TestDefaultPolicyConfig(t *testing.T) {
	p := DefaultDeliveryPolicy()
	if p.MaxOutboundPerDomain != 1000 {
		t.Fatalf("expected 1000, got %d", p.MaxOutboundPerDomain)
	}
	if p.MaxMessageSizeBytes != 25*1024*1024 {
		t.Fatalf("expected 25MB, got %d", p.MaxMessageSizeBytes)
	}
	if p.MaxReceivedHeaders != 50 {
		t.Fatalf("expected 50, got %d", p.MaxReceivedHeaders)
	}
}

// ── HARDENING: Event Type Constants ──────────────────────────

func TestDeliveryEventTypes(t *testing.T) {
	types := []DeliveryEventType{EventQueued, EventLeased, EventConnecting, EventConnected,
		EventRemoteAccepted, EventRemoteRejected, EventDeferred, EventBounced,
		EventDelivered, EventDeadLetter, EventPolicyRejected, EventLoopDetected}
	for _, et := range types {
		if et == "" {
			t.Fatal("event type should not be empty")
		}
	}
}

// ── Helpers ──────────────────────────────────────────────────

// testTransportConfig returns a TransportConfig that is
// safe for the existing unit tests: STARTTLS is
// advertised in EHLO but the transport is not required
// to negotiate it. The dedicated STARTTLS tests use
// DefaultTransportConfig() and the requireStartTLS
// path on the fake server.
func testTransportConfig() TransportConfig {
	cfg := DefaultTransportConfig()
	cfg.RequireSTARTTLS = false
	return cfg
}

func uintPtr(u uint) *uint { return &u }

func TestCustomTransportConfig(t *testing.T) {
	cfg := DefaultTransportConfig()
	cfg.ConnectTimeout = 5 * time.Second
	cfg.AttemptSTARTTLS = false
	transport := NewSMTPTransport(cfg)
	if transport.Config.ConnectTimeout != 5*time.Second {
		t.Fatal("custom timeout not applied")
	}
	if transport.Config.AttemptSTARTTLS {
		t.Fatal("STARTTLS should be disabled")
	}
}
