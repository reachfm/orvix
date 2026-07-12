package delivery

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

// ── Test Certificate Generation Helpers ───────────────────────

// testCertPool generates a CA certificate and key, then returns the CA cert,
// CA key, and a CertPool containing the CA. Server certs issued with the
// returned CA will be trusted by the pool.
func testCertPool(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey, *x509.CertPool) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ca keygen: %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("ca cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("ca parse: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return caCert, caKey, pool
}

// issueServerCert creates a TLS certificate signed by the given CA.
func issueServerCert(t *testing.T, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, commonName string, dnsNames []string, ips []net.IP, notAfter time.Time) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("server keygen: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("server cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("key marshal: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	return cert
}

// newSMTPTLSListener creates a TLS listener with the given cert on a random port.
func newSMTPTLSListener(t *testing.T, cert tls.Certificate) net.Listener {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("tls listen: %v", err)
	}
	return ln
}

// ── TLS Policy Configuration Tests ───────────────────────────

func TestTLSPolicyParseValidValues(t *testing.T) {
	tests := []struct {
		input string
		want  TLSPolicy
	}{
		{"opportunistic", TLSPolicyOpportunistic},
		{"strict", TLSPolicyStrict},
		{"", TLSPolicyOpportunistic},
		{"OPPORTUNISTIC", TLSPolicyOpportunistic},
		{"StRiCt", TLSPolicyStrict},
	}
	for _, tc := range tests {
		got, err := ParseTLSPolicy(tc.input)
		if err != nil {
			t.Errorf("ParseTLSPolicy(%q): unexpected error: %v", tc.input, err)
		}
		if got != tc.want {
			t.Errorf("ParseTLSPolicy(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestTLSPolicyParseUnknownValue(t *testing.T) {
	_, err := ParseTLSPolicy("disabled")
	if err == nil {
		t.Fatal("expected error for unknown policy value")
	}
	if !strings.Contains(err.Error(), "unknown tls policy") {
		t.Errorf("error should mention unknown policy, got: %v", err)
	}
}

func TestTLSPolicyString(t *testing.T) {
	if TLSPolicyOpportunistic.String() != "opportunistic" {
		t.Errorf("expected opportunistic, got %s", TLSPolicyOpportunistic.String())
	}
	if TLSPolicyStrict.String() != "strict" {
		t.Errorf("expected strict, got %s", TLSPolicyStrict.String())
	}
}

func TestTLSPolicyDefaultTransportConfig(t *testing.T) {
	cfg := DefaultTransportConfig()
	if cfg.TLSPolicy != TLSPolicyOpportunistic {
		t.Errorf("default TLSPolicy should be opportunistic, got %v", cfg.TLSPolicy)
	}
}

// ── TLS Minimum Version Test ─────────────────────────────────

func TestTLSPolicyTLSMinVersion(t *testing.T) {
	// Verify that BuildTLSConfig sets MinVersion to TLS 1.2 or higher.
	cfg := TLSPolicyOpportunistic.BuildTLSConfig("test.example.com")
	if cfg.MinVersion < tls.VersionTLS12 {
		t.Errorf("MinVersion should be at least TLS 1.2, got %d", cfg.MinVersion)
	}
	cfg2 := TLSPolicyStrict.BuildTLSConfig("test.example.com")
	if cfg2.MinVersion < tls.VersionTLS12 {
		t.Errorf("strict MinVersion should be at least TLS 1.2, got %d", cfg2.MinVersion)
	}
}

// testTLSTransportConfig returns a TransportConfig suitable for TLS policy tests.
func testTLSTransportConfig(policy TLSPolicy) TransportConfig {
	cfg := DefaultTransportConfig()
	cfg.TLSPolicy = policy
	cfg.RequireSTARTTLS = true
	cfg.AttemptSTARTTLS = true
	return cfg
}

// testTLSTransportConfigNoRequire returns a TransportConfig similar to
// testTLSTransportConfig but with RequireSTARTTLS=false.
func testTLSTransportConfigNoRequire(policy TLSPolicy) TransportConfig {
	cfg := DefaultTransportConfig()
	cfg.TLSPolicy = policy
	cfg.RequireSTARTTLS = false
	cfg.AttemptSTARTTLS = true
	return cfg
}

// ── Opportunistic TLS Tests ──────────────────────────────────

// TestTLSPolicyOpportunisticSelfSigned verifies that opportunistic mode
// accepts a self-signed certificate (the TLS handshake succeeds) but
// does not mark the connection as verified.
func TestTLSPolicyOpportunisticSelfSigned(t *testing.T) {
	fs := startFakeSMTPServerRequiringStartTLS(t)
	transport := NewSMTPTransport(testTLSTransportConfig(TLSPolicyOpportunistic))
	result := transport.DeliverWithTLSName(context.Background(), fs.addr, false,
		"sender@test.com", []string{"rcpt@test.com"}, []byte("Subject: Test\r\n\r\nBody"),
		"test.orvix.local", "127.0.0.1")

	if !result.Success {
		t.Fatalf("opportunistic should succeed with self-signed cert, got: %s", result.StatusMsg)
	}
	if !result.TLSUsed {
		t.Error("expected TLSUsed=true after STARTTLS")
	}
	if result.TLSVerified {
		t.Error("TLSVerified should be false for unverified self-signed cert")
	}
}

// TestTLSPolicyOpportunisticValidCert verifies that opportunistic mode
// succeeds AND marks as verified when presented with a valid CA-signed cert.
func TestTLSPolicyOpportunisticValidCert(t *testing.T) {
	caCert, caKey, pool := testCertPool(t)
	serverCert := issueServerCert(t, caCert, caKey, "127.0.0.1",
		[]string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")},
		time.Now().Add(24*time.Hour))
	tlsLn := newSMTPTLSListener(t, serverCert)
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		MinVersion:   tls.VersionTLS12,
	}
	fs := startFakeSMTPServerWithTLS(t, true, tlsLn, tlsCfg)
	cfg := testTLSTransportConfig(TLSPolicyOpportunistic)
	cfg.TLSRootCAs = pool
	transport := NewSMTPTransport(cfg)

	// Test STARTTLS path
	result := transport.DeliverWithTLSName(context.Background(), fs.addr, false,
		"sender@test.com", []string{"rcpt@test.com"}, []byte("Subject: Test\r\n\r\nBody"),
		"test.orvix.local", "127.0.0.1")

	if !result.Success {
		t.Fatalf("opportunistic STARTTLS should succeed with valid cert, got: %s", result.StatusMsg)
	}
	if !result.TLSUsed {
		t.Error("expected TLSUsed=true")
	}
	if !result.TLSVerified {
		t.Error("TLSVerified should be true for valid CA-signed cert")
	}

	// Test implicit TLS path (same cert semantics)
	result2 := transport.DeliverWithTLSName(context.Background(), fs.tlsAddr, true,
		"sender@test.com", []string{"rcpt@test.com"}, []byte("Subject: Test\r\n\r\nBody"),
		"test.orvix.local", "127.0.0.1")

	if !result2.Success {
		t.Fatalf("opportunistic implicit TLS should succeed with valid cert, got: %s", result2.StatusMsg)
	}
	if !result2.TLSUsed {
		t.Error("expected TLSUsed=true for implicit TLS")
	}
	if !result2.TLSVerified {
		t.Error("TLSVerified should be true for valid cert in implicit TLS")
	}
}

// ── Strict Mode Tests ────────────────────────────────────────

// TestTLSPolicyStrictSelfSigned verifies that strict mode rejects
// a self-signed certificate.
func TestTLSPolicyStrictSelfSigned(t *testing.T) {
	fs := startFakeSMTPServerRequiringStartTLS(t)
	transport := NewSMTPTransport(testTLSTransportConfig(TLSPolicyStrict))
	result := transport.DeliverWithTLSName(context.Background(), fs.addr, false,
		"sender@test.com", []string{"rcpt@test.com"}, []byte("Subject: Test\r\n\r\nBody"),
		"test.orvix.local", "127.0.0.1")

	if result.Success {
		t.Fatal("strict mode should fail with self-signed cert")
	}
	if !result.TempFail {
		t.Error("strict mode failure should be TempFail (deferred, not bounced)")
	}
	if !strings.Contains(result.StatusMsg, "handshake") {
		t.Errorf("status should mention handshake failure, got: %q", result.StatusMsg)
	}
}

// TestTLSPolicyStrictValidCert verifies that strict mode succeeds
// with a valid CA-signed cert matching the expected hostname.
func TestTLSPolicyStrictValidCert(t *testing.T) {
	caCert, caKey, pool := testCertPool(t)
	serverCert := issueServerCert(t, caCert, caKey, "127.0.0.1",
		[]string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")},
		time.Now().Add(24*time.Hour))
	tlsLn := newSMTPTLSListener(t, serverCert)
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		MinVersion:   tls.VersionTLS12,
	}
	fs := startFakeSMTPServerWithTLS(t, true, tlsLn, tlsCfg)
	strictCfg := testTLSTransportConfig(TLSPolicyStrict)
	strictCfg.TLSRootCAs = pool
	transport := NewSMTPTransport(strictCfg)

	// Test STARTTLS path
	result := transport.DeliverWithTLSName(context.Background(), fs.addr, false,
		"sender@test.com", []string{"rcpt@test.com"}, []byte("Subject: Test\r\n\r\nBody"),
		"test.orvix.local", "127.0.0.1")

	if !result.Success {
		t.Fatalf("strict STARTTLS should succeed with valid cert, got: %s", result.StatusMsg)
	}
	if !result.TLSUsed {
		t.Error("expected TLSUsed=true")
	}
	if !result.TLSVerified {
		t.Error("TLSVerified should be true in strict mode")
	}

	// Test implicit TLS path
	result2 := transport.DeliverWithTLSName(context.Background(), fs.tlsAddr, true,
		"sender@test.com", []string{"rcpt@test.com"}, []byte("Subject: Test\r\n\r\nBody"),
		"test.orvix.local", "127.0.0.1")

	if !result2.Success {
		t.Fatalf("strict implicit TLS should succeed with valid cert, got: %s", result2.StatusMsg)
	}
	if !result2.TLSVerified {
		t.Error("TLSVerified should be true for implicit TLS in strict mode")
	}
}

// TestTLSPolicyStrictHostnameMismatch verifies that strict mode rejects
// a certificate whose hostname does not match the expected server name.
func TestTLSPolicyStrictHostnameMismatch(t *testing.T) {
	caCert, caKey, pool := testCertPool(t)
	// Cert is for "wrong-host.example.com" but we connect to 127.0.0.1
	serverCert := issueServerCert(t, caCert, caKey, "wrong-host.example.com",
		[]string{"wrong-host.example.com"}, nil,
		time.Now().Add(24*time.Hour))
	tlsLn := newSMTPTLSListener(t, serverCert)
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		MinVersion:   tls.VersionTLS12,
	}
	fs := startFakeSMTPServerWithTLS(t, true, tlsLn, tlsCfg)
	strictCfg := testTLSTransportConfig(TLSPolicyStrict)
	strictCfg.TLSRootCAs = pool
	transport := NewSMTPTransport(strictCfg)

	result := transport.DeliverWithTLSName(context.Background(), fs.addr, false,
		"sender@test.com", []string{"rcpt@test.com"}, []byte("Subject: Test\r\n\r\nBody"),
		"test.orvix.local", "127.0.0.1")

	if result.Success {
		t.Fatal("strict mode should fail on hostname mismatch")
	}
	if !result.TempFail {
		t.Error("hostname mismatch failure should be TempFail")
	}
}

// TestTLSPolicyStrictExpiredCert verifies that strict mode rejects
// an expired certificate.
func TestTLSPolicyStrictExpiredCert(t *testing.T) {
	caCert, caKey, pool := testCertPool(t)
	serverCert := issueServerCert(t, caCert, caKey, "127.0.0.1",
		[]string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")},
		time.Now().Add(-24*time.Hour)) // expired 24h ago
	tlsLn := newSMTPTLSListener(t, serverCert)
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		MinVersion:   tls.VersionTLS12,
	}
	fs := startFakeSMTPServerWithTLS(t, true, tlsLn, tlsCfg)
	strictCfg := testTLSTransportConfig(TLSPolicyStrict)
	strictCfg.TLSRootCAs = pool
	transport := NewSMTPTransport(strictCfg)

	result := transport.DeliverWithTLSName(context.Background(), fs.addr, false,
		"sender@test.com", []string{"rcpt@test.com"}, []byte("Subject: Test\r\n\r\nBody"),
		"test.orvix.local", "127.0.0.1")

	if result.Success {
		t.Fatal("strict mode should fail with expired cert")
	}
	if !result.TempFail {
		t.Error("expired cert failure should be TempFail")
	}
}

// ── Opportunistic Edge Case Tests ────────────────────────────

// TestTLSPolicyOpportunisticHostnameMismatch verifies that opportunistic mode
// does NOT mark the connection as verified when the hostname does not match.
func TestTLSPolicyOpportunisticHostnameMismatch(t *testing.T) {
	caCert, caKey, pool := testCertPool(t)
	serverCert := issueServerCert(t, caCert, caKey, "wrong-host.example.com",
		[]string{"wrong-host.example.com"}, nil,
		time.Now().Add(24*time.Hour))
	tlsLn := newSMTPTLSListener(t, serverCert)
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		MinVersion:   tls.VersionTLS12,
	}
	fs := startFakeSMTPServerWithTLS(t, true, tlsLn, tlsCfg)
	oppCfg := testTLSTransportConfig(TLSPolicyOpportunistic)
	oppCfg.TLSRootCAs = pool
	transport := NewSMTPTransport(oppCfg)

	result := transport.DeliverWithTLSName(context.Background(), fs.addr, false,
		"sender@test.com", []string{"rcpt@test.com"}, []byte("Subject: Test\r\n\r\nBody"),
		"test.orvix.local", "127.0.0.1")

	if !result.Success {
		t.Fatalf("opportunistic should succeed despite hostname mismatch, got: %s", result.StatusMsg)
	}
	if !result.TLSUsed {
		t.Error("expected TLSUsed=true")
	}
	if result.TLSVerified {
		t.Error("TLSVerified should be false on hostname mismatch even in opportunistic mode")
	}
}

// TestTLSPolicyOpportunisticExpiredCert verifies that opportunistic mode
// still delivers despite an expired certificate, but does not mark verified.
func TestTLSPolicyOpportunisticExpiredCert(t *testing.T) {
	caCert, caKey, pool := testCertPool(t)
	serverCert := issueServerCert(t, caCert, caKey, "127.0.0.1",
		[]string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")},
		time.Now().Add(-24*time.Hour))
	tlsLn := newSMTPTLSListener(t, serverCert)
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		MinVersion:   tls.VersionTLS12,
	}
	fs := startFakeSMTPServerWithTLS(t, true, tlsLn, tlsCfg)
	oppCfg := testTLSTransportConfig(TLSPolicyOpportunistic)
	oppCfg.TLSRootCAs = pool
	transport := NewSMTPTransport(oppCfg)

	result := transport.DeliverWithTLSName(context.Background(), fs.addr, false,
		"sender@test.com", []string{"rcpt@test.com"}, []byte("Subject: Test\r\n\r\nBody"),
		"test.orvix.local", "127.0.0.1")

	if !result.Success {
		t.Fatalf("opportunistic should succeed despite expired cert, got: %s", result.StatusMsg)
	}
	if !result.TLSUsed {
		t.Error("expected TLSUsed=true")
	}
	if result.TLSVerified {
		t.Error("TLSVerified should be false for expired cert even in opportunistic mode")
	}
}

// TestTLSPolicyOpportunisticSelfSignedUntrusted verifies that opportunistic
// mode accepts a self-signed cert (untrusted by any CA) and does not mark verified.
func TestTLSPolicyOpportunisticSelfSignedUntrusted(t *testing.T) {
	// Use the default fake server which generates a self-signed cert.
	fs := startFakeSMTPServerRequiringStartTLS(t)
	transport := NewSMTPTransport(testTLSTransportConfig(TLSPolicyOpportunistic))

	result := transport.DeliverWithTLSName(context.Background(), fs.addr, false,
		"sender@test.com", []string{"rcpt@test.com"}, []byte("Subject: Test\r\n\r\nBody"),
		"test.orvix.local", "127.0.0.1")

	if !result.Success {
		t.Fatalf("opportunistic should succeed with self-signed untrusted cert, got: %s", result.StatusMsg)
	}
	if !result.TLSUsed {
		t.Error("expected TLSUsed=true")
	}
	if result.TLSVerified {
		t.Error("TLSVerified should be false for self-signed untrusted cert")
	}
}

// ── No STARTTLS Tests ────────────────────────────────────────

// TestTLSPolicyStrictNoSTARTTLS verifies that strict mode fails when the
// server does not advertise STARTTLS.
func TestTLSPolicyStrictNoSTARTTLS(t *testing.T) {
	// Start a server that does NOT advertise STARTTLS
	fs := startFakeSMTP(t)
	transport := NewSMTPTransport(testTLSTransportConfig(TLSPolicyStrict))
	result := transport.DeliverWithTLSName(context.Background(), fs.addr, false,
		"sender@test.com", []string{"rcpt@test.com"}, []byte("data"),
		"test.orvix.local", "127.0.0.1")

	if result.Success {
		t.Fatal("strict mode should fail when server has no STARTTLS")
	}
	if !result.TempFail {
		t.Error("no-STARTTLS failure should be TempFail")
	}
	if !strings.Contains(result.StatusMsg, "starttls") {
		t.Errorf("status should mention STARTTLS, got: %q", result.StatusMsg)
	}
}

// TestTLSPolicyOpportunisticNoSTARTTLS verifies that opportunistic mode
// follows the documented delivery behavior when the server does not
// advertise STARTTLS (plaintext delivery when requireSTARTTLS is false).
func TestTLSPolicyOpportunisticNoSTARTTLS(t *testing.T) {
	fs := startFakeSMTP(t)
	transport := NewSMTPTransport(testTLSTransportConfigNoRequire(TLSPolicyOpportunistic))
	result := transport.DeliverWithTLSName(context.Background(), fs.addr, false,
		"sender@test.com", []string{"rcpt@test.com"}, []byte("Subject: Test\r\n\r\nBody"),
		"test.orvix.local", "127.0.0.1")

	if !result.Success {
		t.Fatalf("opportunistic should deliver in plaintext when no STARTTLS, got: %s", result.StatusMsg)
	}
	if result.TLSUsed {
		t.Error("expected TLSUsed=false for plaintext delivery")
	}
}

// ── STARTTLS Handshake Failure Tests ─────────────────────────

// TestTLSPolicyStrictHandshakeFailure verifies that strict mode fails
// without plaintext fallback when the STARTTLS handshake fails.
func TestTLSPolicyStrictHandshakeFailure(t *testing.T) {
	fs := startFakeSMTPServerRequiringStartTLS(t)
	fs.startTLSHandshakeFailure = true
	transport := NewSMTPTransport(testTLSTransportConfig(TLSPolicyStrict))
	result := transport.DeliverWithTLSName(context.Background(), fs.addr, false,
		"sender@test.com", []string{"rcpt@test.com"}, []byte("data"),
		"test.orvix.local", "127.0.0.1")

	if result.Success {
		t.Fatal("strict mode should fail on handshake failure")
	}
	if !result.TempFail {
		t.Error("handshake failure should be TempFail")
	}
	if result.TLSUsed {
		t.Error("TLSUsed should be false when handshake fails")
	}
	if !strings.Contains(result.StatusMsg, "handshake") {
		t.Errorf("status should mention handshake, got: %q", result.StatusMsg)
	}
}

// TestTLSPolicyOpportunisticHandshakeFailure verifies that opportunistic
// mode also fails (not falls back to plaintext) when the STARTTLS handshake
// itself fails (the server said 220 but then closed the socket).
func TestTLSPolicyOpportunisticHandshakeFailure(t *testing.T) {
	fs := startFakeSMTPServerRequiringStartTLS(t)
	fs.startTLSHandshakeFailure = true
	transport := NewSMTPTransport(testTLSTransportConfig(TLSPolicyOpportunistic))
	result := transport.DeliverWithTLSName(context.Background(), fs.addr, false,
		"sender@test.com", []string{"rcpt@test.com"}, []byte("data"),
		"test.orvix.local", "127.0.0.1")

	if result.Success {
		t.Fatal("opportunistic should fail on handshake failure (not fall back to plaintext)")
	}
	if !result.TempFail {
		t.Error("handshake failure should be TempFail")
	}
	if result.TLSUsed {
		t.Error("TLSUsed should be false when handshake fails")
	}
	if !strings.Contains(result.StatusMsg, "handshake") {
		t.Errorf("status should mention handshake, got: %q", result.StatusMsg)
	}
}

// ── Implicit TLS Policy Tests ────────────────────────────────

// TestTLSPolicyStrictImplicitTLSInvalid verifies that strict mode rejects
// an invalid certificate on implicit TLS (same policy as STARTTLS).
func TestTLSPolicyStrictImplicitTLSInvalid(t *testing.T) {
	// Use the default TLS listener which has a self-signed cert.
	fs := startFakeSMTPServerRequiringStartTLS(t)
	transport := NewSMTPTransport(testTLSTransportConfig(TLSPolicyStrict))
	result := transport.DeliverWithTLSName(context.Background(), fs.tlsAddr, true,
		"sender@test.com", []string{"rcpt@test.com"}, []byte("data"),
		"test.orvix.local", "127.0.0.1")

	if result.Success {
		t.Fatal("strict implicit TLS should fail with self-signed cert")
	}
	if !strings.Contains(result.StatusMsg, "handshake") {
		t.Errorf("status should mention handshake failure, got: %q", result.StatusMsg)
	}
}

// TestTLSPolicyOpportunisticImplicitTLSUnverified verifies that opportunistic
// implicit TLS succeeds but is not verified with a self-signed cert.
func TestTLSPolicyOpportunisticImplicitTLSUnverified(t *testing.T) {
	fs := startFakeSMTPServerRequiringStartTLS(t)
	transport := NewSMTPTransport(testTLSTransportConfig(TLSPolicyOpportunistic))

	// Use implicit TLS to the TLS listener (which has a self-signed cert)
	result := transport.DeliverWithTLSName(context.Background(), fs.tlsAddr, true,
		"sender@test.com", []string{"rcpt@test.com"}, []byte("Subject: Implicit TLS test\r\n\r\nBody"),
		"test.orvix.local", "127.0.0.1")

	if !result.Success {
		t.Fatalf("opportunistic implicit TLS should succeed with self-signed cert, got: %s", result.StatusMsg)
	}
	if !result.TLSUsed {
		t.Error("expected TLSUsed=true for implicit TLS")
	}
	if !result.TLSHandshake {
		t.Error("expected TLSHandshake=true for implicit TLS")
	}
	if result.TLSVerified {
		t.Error("TLSVerified should be false for self-signed cert in opportunistic implicit TLS")
	}
}

// ── TLSPolicy String Round-trip ──────────────────────────────

func TestTLSPolicyStringRoundTrip(t *testing.T) {
	for _, v := range []TLSPolicy{TLSPolicyOpportunistic, TLSPolicyStrict} {
		s := v.String()
		parsed, err := ParseTLSPolicy(s)
		if err != nil {
			t.Errorf("round-trip ParseTLSPolicy(%q) error: %v", s, err)
		}
		if parsed != v {
			t.Errorf("round-trip: %v.String() = %q, ParseTLSPolicy = %v", v, s, parsed)
		}
	}
}

// ── Exact MX Hostname Match Test ─────────────────────────────

// TestTLSPolicyStrictMXHostname verifies that strict mode succeeds when
// the explicit TLS server name matches the certificate, via the
// DeliverWithTLSName method. This simulates what the delivery worker does
// when it passes the MX hostname.
func TestTLSPolicyStrictMXHostname(t *testing.T) {
	caCert, caKey, pool := testCertPool(t)
	serverCert := issueServerCert(t, caCert, caKey, "mx.test.example.com",
		[]string{"mx.test.example.com"}, nil,
		time.Now().Add(24*time.Hour))
	tlsLn := newSMTPTLSListener(t, serverCert)
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		MinVersion:   tls.VersionTLS12,
	}
	fs := startFakeSMTPServerWithTLS(t, true, tlsLn, tlsCfg)
	mxCfg := testTLSTransportConfig(TLSPolicyStrict)
	mxCfg.TLSRootCAs = pool
	transport := NewSMTPTransport(mxCfg)

	// Verify against the MX hostname that matches the cert.
	result := transport.DeliverWithTLSName(context.Background(), fs.addr, false,
		"sender@test.com", []string{"rcpt@test.com"}, []byte("Subject: Test\r\n\r\nBody"),
		"test.orvix.local", "mx.test.example.com")

	if !result.Success {
		t.Fatalf("strict mode with MX hostname matching cert should succeed, got: %s", result.StatusMsg)
	}
	if !result.TLSUsed {
		t.Error("expected TLSUsed=true")
	}
	if !result.TLSVerified {
		t.Error("TLSVerified should be true when MX hostname matches cert")
	}
}

// Ensure DeliverWithTLSName is accessible (linker check).
var _ = (&SMTPTransport{}).DeliverWithTLSName
