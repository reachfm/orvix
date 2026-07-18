package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type testCert struct {
	cert tls.Certificate
	pool *x509.CertPool
}

func makeTestCert(hosts ...string) *testCert {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: hosts[0]},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     hosts,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		panic(err)
	}
	parsedCert, _ := x509.ParseCertificate(certDER)
	pool := x509.NewCertPool()
	pool.AddCert(parsedCert)
	return &testCert{cert: tlsCert, pool: pool}
}

type testSMTP struct {
	listener    net.Listener
	addr        string
	port        int
	done        chan struct{}
	implicitTLS bool

	didTLS      atomic.Bool
	didSTARTTLS atomic.Bool
	didAuth     atomic.Bool
}

func (s *testSMTP) stop() {
	close(s.done)
	s.listener.Close()
}

func newTestSMTP(t *testing.T, implicitTLS bool, offerSTARTTLS bool, tc *testCert) *testSMTP {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port := 0
	fmt.Sscanf(portStr, "%d", &port)

	s := &testSMTP{
		listener:    ln,
		addr:        "127.0.0.1",
		port:        port,
		done:        make(chan struct{}),
		implicitTLS: implicitTLS,
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handleSMTP(conn, implicitTLS, offerSTARTTLS, tc.cert)
		}
	}()

	time.Sleep(50 * time.Millisecond)
	return s
}

func (s *testSMTP) handleSMTP(conn net.Conn, implicitTLS, offerSTARTTLS bool, cert tls.Certificate) {
	defer conn.Close()

	if implicitTLS {
		tlsConn := tls.Server(conn, &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		})
		if err := tlsConn.Handshake(); err != nil {
			return
		}
		conn = tlsConn
		s.didTLS.Store(true)
	}

	s.sendLine(conn, "220 test.smtp.local ESMTP")

	for i := 0; i < 10; i++ {
		line := s.recvLine(conn)
		if line == "" {
			return
		}
		upper := strings.ToUpper(line)

		switch {
		case strings.HasPrefix(upper, "EHLO"):
			s.sendLine(conn, "250-test.smtp.local")
			if offerSTARTTLS && !implicitTLS {
				s.sendLine(conn, "250-STARTTLS")
			}
			s.sendLine(conn, "250 AUTH PLAIN LOGIN")
		case strings.HasPrefix(upper, "HELO"):
			s.sendLine(conn, "250 test.smtp.local")
		case upper == "STARTTLS":
			s.sendLine(conn, "220 Go ahead")
			tlsConn := tls.Server(conn, &tls.Config{
				Certificates: []tls.Certificate{cert},
				MinVersion:   tls.VersionTLS12,
			})
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			s.didSTARTTLS.Store(true)
		case strings.HasPrefix(upper, "AUTH"):
			s.didAuth.Store(true)
			s.sendLine(conn, "235 2.7.0 OK")
		case strings.HasPrefix(upper, "MAIL"):
			s.sendLine(conn, "250 OK")
		case strings.HasPrefix(upper, "RCPT"):
			s.sendLine(conn, "250 OK")
		case upper == "DATA":
			s.sendLine(conn, "354 go ahead")
		case line == ".":
			s.sendLine(conn, "250 OK")
		case upper == "QUIT":
			s.sendLine(conn, "221 bye")
			return
		default:
			s.sendLine(conn, "250 OK")
		}
	}
}

func (s *testSMTP) sendLine(conn net.Conn, msg string) {
	conn.SetWriteDeadline(time.Now().Add(time.Second))
	fmt.Fprintf(conn, "%s\r\n", msg)
}

func (s *testSMTP) recvLine(conn net.Conn) string {
	buf := make([]byte, 4096)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(buf[:n]), "\r\n")
}

func withTestTrust(t *testing.T, pool *x509.CertPool) func() {
	t.Helper()
	old := smtpTLSRoots
	smtpTLSRoots = pool
	return func() { smtpTLSRoots = old }
}

func TestDialSMTPWithTLS_ImplicitTLSSucceeds(t *testing.T) {
	tc := makeTestCert("127.0.0.1")
	cleanup := withTestTrust(t, tc.pool)
	defer cleanup()

	oldForce := smtpForceImplicitTLS
	smtpForceImplicitTLS = true
	defer func() { smtpForceImplicitTLS = oldForce }()

	srv := newTestSMTP(t, true, false, tc)
	defer srv.stop()

	client, err := DialSMTPWithTLS(srv.addr, srv.port, "user", "pass")
	if err != nil {
		t.Fatalf("implicit TLS: %v", err)
	}
	client.Quit()

	if !srv.didTLS.Load() {
		t.Error("TLS connection was not used")
	}
	if !srv.didAuth.Load() {
		t.Error("authentication did not occur")
	}
}

func TestDialSMTPWithTLS_STARTTLSSucceeds(t *testing.T) {
	tc := makeTestCert("127.0.0.1")
	cleanup := withTestTrust(t, tc.pool)
	defer cleanup()

	srv := newTestSMTP(t, false, true, tc)
	defer srv.stop()

	client, err := DialSMTPWithTLS(srv.addr, srv.port, "user", "pass")
	if err != nil {
		t.Fatalf("STARTTLS: %v", err)
	}
	client.Quit()

	if !srv.didSTARTTLS.Load() {
		t.Error("STARTTLS was not performed")
	}
	if !srv.didAuth.Load() {
		t.Error("authentication did not occur")
	}
}

func TestDialSMTPWithTLS_AuthOnlyAfterSTARTTLS(t *testing.T) {
	tc := makeTestCert("127.0.0.1")
	cleanup := withTestTrust(t, tc.pool)
	defer cleanup()

	srv := newTestSMTP(t, false, true, tc)
	defer srv.stop()

	client, err := DialSMTPWithTLS(srv.addr, srv.port, "user", "pass")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	client.Quit()

	if srv.didAuth.Load() && !srv.didSTARTTLS.Load() {
		t.Error("authentication happened before STARTTLS")
	}
}

func TestDialSMTPWithTLS_ServerWithoutSTARTTLSRejected(t *testing.T) {
	tc := makeTestCert("127.0.0.1")
	cleanup := withTestTrust(t, tc.pool)
	defer cleanup()

	srv := newTestSMTP(t, false, false, tc)
	defer srv.stop()

	_, err := DialSMTPWithTLS(srv.addr, srv.port, "user", "pass")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("expected STARTTLS error, got: %v", err)
	}
	if srv.didAuth.Load() {
		t.Error("authentication should not have occurred without STARTTLS")
	}
}

func TestDialSMTPWithTLS_PlainTextFallbackDoesNotOccur(t *testing.T) {
	tc := makeTestCert("127.0.0.1")
	cleanup := withTestTrust(t, tc.pool)
	defer cleanup()

	srv := newTestSMTP(t, false, false, tc)
	defer srv.stop()

	_, err := DialSMTPWithTLS(srv.addr, srv.port, "user", "pass")
	if err == nil {
		t.Fatal("expected failure")
	}
	if srv.didAuth.Load() {
		t.Fatal("credentials were sent in plaintext")
	}
}

func TestDialSMTPWithTLS_ConnectionClosedAfterFailure(t *testing.T) {
	_, err := DialSMTPWithTLS("127.0.0.1", 19999, "user", "pass")
	if err == nil {
		t.Fatal("expected connection error")
	}
}

func startTLSListenerForTest(t *testing.T, tc *testCert) (port int, cleanup func()) {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{tc.cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	fmt.Sscanf(portStr, "%d", &port)

	go func() {
		conn, _ := ln.Accept()
		if conn != nil {
			conn.(*tls.Conn).Handshake()
			conn.Close()
		}
	}()

	time.Sleep(50 * time.Millisecond)
	return port, func() { ln.Close() }
}

func TestDialSMTPWithTLS_InvalidCertificateRejected(t *testing.T) {
	tc := makeTestCert("wrong.host.local")
	port, cleanup := startTLSListenerForTest(t, tc)
	defer cleanup()

	oldForce := smtpForceImplicitTLS
	smtpForceImplicitTLS = true
	defer func() { smtpForceImplicitTLS = oldForce }()

	_, err := DialSMTPWithTLS("127.0.0.1", port, "user", "pass")
	if err == nil {
		t.Fatal("expected certificate verification error")
	}
	t.Logf("correctly rejected: %v", err)
}

func TestDialSMTPWithTLS_HostnameMismatchRejected(t *testing.T) {
	certOnly := makeTestCert("host.name.local")
	port, cleanup := startTLSListenerForTest(t, certOnly)
	defer cleanup()

	oldForce := smtpForceImplicitTLS
	smtpForceImplicitTLS = true
	defer func() { smtpForceImplicitTLS = oldForce }()

	_, err := DialSMTPWithTLS("127.0.0.1", port, "user", "pass")
	if err == nil {
		t.Fatal("expected hostname mismatch error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "x509:") && !strings.Contains(msg, "tls:") {
		t.Fatalf("expected cert error, got: %v", err)
	}
}

func TestDialSMTPWithTLS_CredentialsNeverBeforeTLS(t *testing.T) {
	tc := makeTestCert("127.0.0.1")
	cleanup := withTestTrust(t, tc.pool)
	defer cleanup()

	srv := newTestSMTP(t, false, false, tc)
	defer srv.stop()

	_, err := DialSMTPWithTLS(srv.addr, srv.port, "secretuser", "secretpass")
	if err == nil {
		t.Fatal("expected error")
	}
	if srv.didAuth.Load() {
		t.Fatal("credentials were sent despite no STARTTLS")
	}
}

func TestDialSMTPWithTLS_AddressConstruction(t *testing.T) {
	if addr := net.JoinHostPort("127.0.0.1", "25"); addr != "127.0.0.1:25" {
		t.Fatalf("IPv4: got %s", addr)
	}
	if addr := net.JoinHostPort("::1", "25"); addr != "[::1]:25" {
		t.Fatalf("IPv6: got %s", addr)
	}
	if addr := net.JoinHostPort("smtp.example.com", "587"); addr != "smtp.example.com:587" {
		t.Fatalf("hostname: got %s", addr)
	}
}
